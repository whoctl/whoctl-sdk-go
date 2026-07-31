package protocol

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/schema"
)

// Transport carries one request to a provider and brings back its answer. The
// loopback transport calls a Server in this process; the stdio transport writes
// a line to a subprocess and reads one back. Nothing above this interface knows
// which it is.
type Transport interface {
	Call(ctx context.Context, req Request) (Response, error)
}

// Client is a core.Provider backed by a Transport. Every handler it hands out
// answers by making a call, so `internal/cli` cannot tell a provider in this
// process from one in another.
type Client struct {
	transport Transport
	handshake Handshake
	handlers  []core.Handler
	nextID    int
}

// Connect performs the handshake and reads the provider's schema, which is
// everything whoctl needs to serve every command against it.
func Connect(ctx context.Context, t Transport, cfg Config) (*Client, error) {
	c := &Client{transport: t}

	if err := c.call(ctx, MethodHandshake, HandshakeParams{Protocol: Version, Config: cfg}, &c.handshake); err != nil {
		return nil, err
	}
	if c.handshake.Protocol != Version {
		return nil, core.Invalidf("provider %q speaks protocol %s, whoctl speaks %s",
			c.handshake.Name, c.handshake.Protocol, Version)
	}

	var s Schema
	if err := c.call(ctx, MethodSchema, nil, &s); err != nil {
		return nil, err
	}
	for _, rt := range s.Resources {
		c.handlers = append(c.handlers, newHandler(c, rt))
	}
	return c, nil
}

// Name implements core.Provider.
func (c *Client) Name() string { return c.handshake.Name }

// Aliases implements core.Aliaser.
func (c *Client) Aliases() []string { return c.handshake.Aliases }

// Handlers implements core.Provider.
func (c *Client) Handlers() []core.Handler { return c.handlers }

// HonoursDryRun reports the provider's own claim. whoctl cannot verify it: a
// provider it did not write could ignore the flag entirely, which is the price
// of out-of-tree providers and is why the claim is recorded rather than
// trusted.
func (c *Client) HonoursDryRun() bool { return c.handshake.HonoursDryRun }

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	c.nextID++
	req := Request{ID: c.nextID, Method: method}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = encoded
	}

	resp, err := c.transport.Call(ctx, req)
	if err != nil {
		return err
	}
	if resp.ID != req.ID {
		return fmt.Errorf("provider %q answered request %d with response %d", c.handshake.Name, req.ID, resp.ID)
	}
	if resp.Error != nil {
		return coreError(resp.Error)
	}
	if result == nil || len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, result)
}

// coreError turns the wire error back into the typed one the commands key on.
// The code is what survived; the cause did not, by design.
func coreError(e *Error) error {
	return &core.Error{
		Code:     core.Code(e.Code),
		Resource: e.Resource,
		Name:     e.Name,
		Message:  e.Message,
	}
}

// handler is one kind, served over the transport.
//
// It implements every optional interface unconditionally and reports what it
// can actually do through Capabilities. That is safe because commands ask
// res.Can(...) before calling — and if one ever forgets, the provider answers
// UNSUPPORTED rather than doing something surprising.
type handler struct {
	client *Client
	rt     ResourceType
	typ    core.ResourceType
	caps   []core.Capability
}

func newHandler(c *Client, rt ResourceType) *handler {
	t := core.ResourceType{
		Group: rt.Group, Version: rt.Version, Kind: rt.Kind,
		Plural: rt.Plural, Singular: rt.Singular, ShortNames: rt.ShortNames,
		Description: rt.Description,
	}
	for _, col := range rt.Columns {
		t.Columns = append(t.Columns, core.Column{Name: col.Name, Wide: col.Wide, Path: col.Path, Format: col.Format})
	}
	caps := make([]core.Capability, 0, len(rt.Capabilities))
	for _, c := range rt.Capabilities {
		caps = append(caps, core.Capability(c))
	}
	return &handler{client: c, rt: rt, typ: t, caps: caps}
}

func (h *handler) Type() core.ResourceType { return h.typ }

// Capabilities implements core.Capable: what the provider published, not what
// this Go type happens to implement.
func (h *handler) Capabilities() []core.Capability { return h.caps }

// SpecSchema and StatusSchema implement core.SchemaPublisher. There is no Go
// type here to reflect over, and there does not need to be: the provider sent
// the fields.
func (h *handler) SpecSchema() []schema.Field   { return h.rt.Spec }
func (h *handler) StatusSchema() []schema.Field { return h.rt.Status }

// NewSpec hands out an ordered map for a manifest to decode into. The provider
// is what knows the real type; this side only has to carry the fields across
// without reordering them.
func (h *handler) NewSpec() any { return NewMap() }

func (h *handler) List(ctx context.Context) ([]core.Object, error) {
	var result ObjectsResult
	if err := h.client.call(ctx, MethodList, KindParams{Kind: h.typ.Kind}, &result); err != nil {
		return nil, err
	}
	return decodeObjects(result.Objects)
}

func (h *handler) Get(ctx context.Context, name string) (core.Object, error) {
	var result ObjectResult
	if err := h.client.call(ctx, MethodGet, NameParams{Kind: h.typ.Kind, Name: name}, &result); err != nil {
		return core.Object{}, err
	}
	return result.Object.Decode(nil)
}

func (h *handler) ListScoped(ctx context.Context, scope string) ([]core.Object, error) {
	var result ObjectsResult
	if err := h.client.call(ctx, MethodListScoped, ScopeParams{Kind: h.typ.Kind, Scope: scope}, &result); err != nil {
		return nil, err
	}
	return decodeObjects(result.Objects)
}

func (h *handler) Apply(ctx context.Context, obj core.Object) (core.Result, error) {
	wire, err := ObjectFrom(obj)
	if err != nil {
		return core.Result{}, err
	}
	var result ApplyResult
	if err := h.client.call(ctx, MethodApply, ApplyParams{Kind: h.typ.Kind, Object: wire}, &result); err != nil {
		return core.Result{}, err
	}
	applied, err := result.Object.Decode(nil)
	if err != nil {
		return core.Result{}, err
	}
	return core.Result{Action: core.Action(result.Action), Object: applied, Diff: result.Diff}, nil
}

func (h *handler) Delete(ctx context.Context, name string) error {
	// Whatever `delete` put in the context has to be spelled out here, because
	// the provider's context is its own.
	opts := core.DeleteOptionsFrom(ctx)
	params := DeleteParams{Kind: h.typ.Kind, Name: name, Cascade: opts.Cascade}
	return h.client.call(ctx, MethodDelete, params, nil)
}

func (h *handler) Describe(ctx context.Context, name string) (string, error) {
	var result TextResult
	if err := h.client.call(ctx, MethodDescribe, NameParams{Kind: h.typ.Kind, Name: name}, &result); err != nil {
		return "", err
	}
	return result.Text, nil
}

func (h *handler) Restart(ctx context.Context, name string) error {
	return h.client.call(ctx, MethodRestart, NameParams{Kind: h.typ.Kind, Name: name}, nil)
}

func decodeObjects(wire []Object) ([]core.Object, error) {
	out := make([]core.Object, 0, len(wire))
	for _, w := range wire {
		obj, err := w.Decode(nil)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}

// Every optional interface is satisfied, so that a capability the provider
// publishes is always reachable.
var (
	_ core.Provider        = (*Client)(nil)
	_ core.Aliaser         = (*Client)(nil)
	_ core.Handler         = (*handler)(nil)
	_ core.Capable         = (*handler)(nil)
	_ core.SchemaPublisher = (*handler)(nil)
	_ core.Describer       = (*handler)(nil)
	_ core.Restarter       = (*handler)(nil)
	_ core.ScopedLister    = (*handler)(nil)
)
