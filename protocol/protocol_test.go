package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// --- a provider that touches nothing ------------------------------------

type widgetSpec struct {
	Size    int      `yaml:"size" json:"size" doc:"How big." docExample:"3"`
	Colour  string   `yaml:"colour,omitempty" json:"colour,omitempty" doc:"What colour."`
	Tags    []string `yaml:"tags,omitempty" json:"tags,omitempty" doc:"Labels."`
	Enabled *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty" doc:"Whether it is on."`
}

type widgetStatus struct {
	Size     int      `yaml:"size" json:"size" doc:"How big it is."`
	Colour   string   `yaml:"colour,omitempty" json:"colour,omitempty" doc:"What colour it is."`
	Tags     []string `yaml:"tags,omitempty" json:"tags,omitempty" doc:"Labels it carries."`
	Enabled  bool     `yaml:"enabled" json:"enabled" doc:"Whether it is on."`
	Observed int64    `yaml:"observed" json:"observed" doc:"Unix time it was read."`
}

type widgetHandler struct {
	applied            core.Object
	restarts           []string
	deletedWithCascade bool
}

func (h *widgetHandler) Type() core.ResourceType {
	return core.ResourceType{
		Group: "test.whoctl.io", Version: "v1", Kind: "Widget",
		Plural: "widgets", Singular: "widget", ShortNames: []string{"wid"},
		Description: "A widget.",
		Columns: []core.Column{
			{Name: "NAME", Path: "metadata.name"},
			{Name: "SIZE", Path: "status.size"},
			{Name: "TAGS", Wide: true, Path: "status.tags"},
		},
	}
}

func (h *widgetHandler) NewSpec() any   { return &widgetSpec{} }
func (h *widgetHandler) NewStatus() any { return &widgetStatus{} }

func (h *widgetHandler) object(name string) core.Object {
	t := h.Type()
	return core.Object{
		APIVersion: t.APIVersion(), Kind: t.Kind,
		Metadata: core.Metadata{Name: name, Labels: map[string]string{"tier": "one"}},
		Spec:     &widgetSpec{Size: 3, Colour: "red", Tags: []string{"a", "b"}},
		Status:   &widgetStatus{Size: 3, Colour: "red", Tags: []string{"a", "b"}, Enabled: true, Observed: 1700000000},
	}
}

func (h *widgetHandler) List(context.Context) ([]core.Object, error) {
	return []core.Object{h.object("one"), h.object("two")}, nil
}

func (h *widgetHandler) Get(_ context.Context, name string) (core.Object, error) {
	if name != "one" {
		return core.Object{}, core.NotFound("widget", name)
	}
	return h.object(name), nil
}

func (h *widgetHandler) Apply(_ context.Context, obj core.Object) (core.Result, error) {
	h.applied = obj
	return core.Result{Action: core.ActionConfigured, Object: obj, Diff: []string{"size 2 -> 3"}}, nil
}

func (h *widgetHandler) Delete(ctx context.Context, name string) error {
	h.deletedWithCascade = core.DeleteOptionsFrom(ctx).Cascade
	return core.Refusedf("widget %q is busy", name)
}

func (h *widgetHandler) Restart(_ context.Context, name string) error {
	h.restarts = append(h.restarts, name)
	return nil
}

func (h *widgetHandler) ListScoped(_ context.Context, scope string) ([]core.Object, error) {
	return []core.Object{h.object(scope + ":one")}, nil
}

// gadgetHandler has none of the optional verbs, so it proves a capability is
// absent as well as present. It cannot embed widgetHandler: it would inherit
// Restart and ListScoped and stop being the thing this tests.
type gadgetHandler struct{}

func (h *gadgetHandler) Type() core.ResourceType {
	return core.ResourceType{
		Group: "test.whoctl.io", Version: "v1", Kind: "Gadget",
		Plural: "gadgets", Singular: "gadget", Description: "A gadget.",
		Columns: []core.Column{{Name: "NAME", Path: "metadata.name"}},
	}
}

func (h *gadgetHandler) NewSpec() any { return &widgetSpec{} }

func (h *gadgetHandler) List(context.Context) ([]core.Object, error) { return nil, nil }

func (h *gadgetHandler) Get(_ context.Context, name string) (core.Object, error) {
	return core.Object{}, core.NotFound("gadget", name)
}

func (h *gadgetHandler) Apply(context.Context, core.Object) (core.Result, error) {
	return core.Result{}, nil
}

func (h *gadgetHandler) Delete(context.Context, string) error { return nil }

type fakeProvider struct{ widget *widgetHandler }

func (p *fakeProvider) Name() string      { return "test" }
func (p *fakeProvider) Aliases() []string { return []string{"tst"} }
func (p *fakeProvider) Handlers() []core.Handler {
	return []core.Handler{p.widget, &gadgetHandler{}}
}

func served(t *testing.T) (*Client, *widgetHandler) {
	t.Helper()
	h := &widgetHandler{}
	client, err := Serve(context.Background(), NewServerOf(&fakeProvider{widget: h}), Config{Root: "/fixture"})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	return client, h
}

func handlerFor(t *testing.T, c *Client, kind string) core.Handler {
	t.Helper()
	for _, h := range c.Handlers() {
		if h.Type().Kind == kind {
			return h
		}
	}
	t.Fatalf("no handler for %q", kind)
	return nil
}

// --- what has to survive the trip ---------------------------------------

func TestHandshakeCarriesTheProviderIdentity(t *testing.T) {
	c, _ := served(t)
	if c.Name() != "test" {
		t.Errorf("name = %q", c.Name())
	}
	if got := c.Aliases(); len(got) != 1 || got[0] != "tst" {
		t.Errorf("aliases = %v, want [tst]", got)
	}
}

func TestAProviderSpeakingAnotherProtocolIsRefused(t *testing.T) {
	s := NewServerOf(&fakeProvider{widget: &widgetHandler{}})
	resp := s.Handle(context.Background(), Request{ID: 1, Method: MethodHandshake,
		Params: json.RawMessage(`{"protocol":"999"}`)})
	if resp.Error == nil {
		t.Fatal("a version mismatch must fail rather than proceed")
	}
	if resp.Error.Code != string(core.CodeInvalid) {
		t.Errorf("code = %q, want INVALID", resp.Error.Code)
	}
}

// The schema is what replaces having the provider's Go types.
func TestSchemaCarriesColumnsCapabilitiesAndFields(t *testing.T) {
	c, _ := served(t)
	h := handlerFor(t, c, "Widget")

	if got := h.Type().Columns; len(got) != 3 || got[0].Path != "metadata.name" || got[2].Wide != true {
		t.Errorf("columns = %+v", got)
	}
	want := []core.Capability{core.CapabilityRestart, core.CapabilityScopedList, core.CapabilityStatusSchema}
	got := core.CapabilitiesOf(h)
	if len(got) != len(want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("capability %d = %q, want %q", i, got[i], want[i])
		}
	}

	spec := core.SpecFieldsOf(h)
	if len(spec) != 4 || spec[0].Name != "size" || spec[0].Doc == "" {
		t.Errorf("spec fields = %+v", spec)
	}
	if status := core.StatusFieldsOf(h); len(status) != 5 {
		t.Errorf("status fields = %+v", status)
	}
}

// A kind with none of the optional verbs must publish none of them, or every
// command would offer everything.
func TestAKindWithoutOptionalVerbsPublishesNoCapabilities(t *testing.T) {
	c, _ := served(t)
	h := handlerFor(t, c, "Gadget")
	for _, c := range core.CapabilitiesOf(h) {
		if c == core.CapabilityRestart || c == core.CapabilityScopedList {
			t.Errorf("Gadget claims %q", c)
		}
	}
}

// Field order is the reason spec and status are not plain maps: `get -o yaml`
// has always printed a manifest in the order the provider declared, and a
// provider in another process must not change that.
func TestFieldOrderSurvivesTheRoundTrip(t *testing.T) {
	c, _ := served(t)
	obj, err := handlerFor(t, c, "Widget").Get(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(obj); err != nil {
		t.Fatal(err)
	}
	enc.Close()

	got := buf.String()
	want := `apiVersion: test.whoctl.io/v1
kind: Widget
metadata:
  name: one
  labels:
    tier: one
spec:
  size: 3
  colour: red
  tags:
    - a
    - b
status:
  size: 3
  colour: red
  tags:
    - a
    - b
  enabled: true
  observed: 1700000000
`
	if got != want {
		t.Errorf("yaml round-tripped through the protocol:\n%s\nwant:\n%s", got, want)
	}
}

// JSON has one number type. A uid printed as 1000.000000 would not be cosmetic.
func TestIntegersStayIntegers(t *testing.T) {
	c, _ := served(t)
	obj, err := handlerFor(t, c, "Widget").Get(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	if got := core.Lookup(obj, "status.observed"); got != int64(1700000000) {
		t.Errorf("observed = %#v, want an integer", got)
	}
	if got := core.Lookup(obj, "status.size"); got != int64(3) {
		t.Errorf("size = %#v, want an integer", got)
	}
}

// Every table column has to resolve against what came back, or the tables go
// blank the moment a provider moves out of process.
func TestColumnPathsResolveAgainstADecodedObject(t *testing.T) {
	c, _ := served(t)
	h := handlerFor(t, c, "Widget")
	obj, err := h.Get(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"metadata.name": "one",
		"status.size":   int64(3),
		"status.colour": "red",
	} {
		if got := core.Lookup(obj, path); got != want {
			t.Errorf("Lookup(%q) = %#v, want %#v", path, got, want)
		}
	}
	if got := core.Lookup(obj, "status.tags"); len(got.([]any)) != 2 {
		t.Errorf("tags = %#v", got)
	}
}

func TestListAndScopedList(t *testing.T) {
	c, _ := served(t)
	h := handlerFor(t, c, "Widget")

	objs, err := h.List(context.Background())
	if err != nil || len(objs) != 2 {
		t.Fatalf("list = %v, %v", objs, err)
	}
	scoped, err := h.(core.ScopedLister).ListScoped(context.Background(), "620")
	if err != nil || len(scoped) != 1 || scoped[0].Metadata.Name != "620:one" {
		t.Fatalf("scoped = %v, %v", scoped, err)
	}
}

// The spec goes out generic and has to arrive as the provider's own type, which
// is where a manifest stops being a bag of keys and starts being checked.
func TestApplyDecodesTheSpecIntoTheProvidersType(t *testing.T) {
	c, h := served(t)

	var spec Map
	if err := yaml.Unmarshal([]byte("size: 7\ncolour: blue\ntags: [x]\nenabled: true\n"), &spec); err != nil {
		t.Fatal(err)
	}
	result, err := handlerFor(t, c, "Widget").Apply(context.Background(), core.Object{
		Metadata: core.Metadata{Name: "one"},
		Spec:     &spec,
	})
	if err != nil {
		t.Fatal(err)
	}

	typed, ok := h.applied.Spec.(*widgetSpec)
	if !ok {
		t.Fatalf("the provider received %T, want *widgetSpec", h.applied.Spec)
	}
	if typed.Size != 7 || typed.Colour != "blue" || len(typed.Tags) != 1 || typed.Enabled == nil || !*typed.Enabled {
		t.Errorf("decoded spec = %+v", typed)
	}
	if result.Action != core.ActionConfigured || len(result.Diff) != 1 {
		t.Errorf("result = %+v", result)
	}
}

// A spec whose shape is wrong must fail as INVALID at the provider, not become
// a zero value that silently applies.
func TestApplyRejectsASpecThatDoesNotFit(t *testing.T) {
	c, _ := served(t)
	var spec Map
	if err := yaml.Unmarshal([]byte("size: not-a-number\n"), &spec); err != nil {
		t.Fatal(err)
	}
	_, err := handlerFor(t, c, "Widget").Apply(context.Background(), core.Object{
		Metadata: core.Metadata{Name: "one"}, Spec: &spec,
	})
	if err == nil {
		t.Fatal("a spec that does not fit must be refused")
	}
	if core.CodeOf(err) != core.CodeInvalid {
		t.Errorf("code = %q, want INVALID: %v", core.CodeOf(err), err)
	}
}

// The code is what a Go error type could not be. delete --ignore-not-found
// depends on this one arriving intact.
func TestErrorCodesSurvive(t *testing.T) {
	c, _ := served(t)
	h := handlerFor(t, c, "Widget")

	_, err := h.Get(context.Background(), "nope")
	if !core.IsNotFound(err) {
		t.Errorf("Get of a missing object: code = %q", core.CodeOf(err))
	}
	if got, want := err.Error(), `widget "nope" not found`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	var coded *core.Error
	if !as(err, &coded) || coded.Resource != "widget" || coded.Name != "nope" {
		t.Errorf("resource/name did not survive: %+v", coded)
	}

	if err := h.Delete(context.Background(), "one"); core.CodeOf(err) != core.CodeRefused {
		t.Errorf("Delete: code = %q, want REFUSED", core.CodeOf(err))
	}
}

func TestRestartReachesTheProvider(t *testing.T) {
	c, h := served(t)
	if err := handlerFor(t, c, "Widget").(core.Restarter).Restart(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if len(h.restarts) != 1 || h.restarts[0] != "one" {
		t.Errorf("restarts = %v", h.restarts)
	}
}

// Calling a verb a kind does not have must refuse rather than do something
// surprising — the client implements every optional interface, so this is the
// backstop for a command that forgets to check the capability first.
func TestAVerbTheKindDoesNotHaveIsRefused(t *testing.T) {
	c, _ := served(t)
	err := handlerFor(t, c, "Gadget").(core.Restarter).Restart(context.Background(), "one")
	if core.CodeOf(err) != core.CodeUnsupported {
		t.Errorf("code = %q, want UNSUPPORTED: %v", core.CodeOf(err), err)
	}
}

// A kind nobody serves must not reach a handler at all.
func TestAnUnknownKindIsRefused(t *testing.T) {
	s := handshaken(t)
	resp := s.Handle(context.Background(), Request{ID: 2, Method: MethodList,
		Params: json.RawMessage(`{"kind":"Nonesuch"}`)})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "Nonesuch") {
		t.Errorf("error = %+v", resp.Error)
	}
}

// The provider is built from the handshake's configuration, so nothing can be
// served before it: a request that arrived first would reach a provider
// configured with defaults rather than with --root and --dry-run.
func TestNothingIsServedBeforeTheHandshake(t *testing.T) {
	s := NewServerOf(&fakeProvider{widget: &widgetHandler{}})
	resp := s.Handle(context.Background(), Request{ID: 1, Method: MethodList,
		Params: json.RawMessage(`{"kind":"Widget"}`)})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "no handshake") {
		t.Errorf("error = %+v, want a refusal naming the missing handshake", resp.Error)
	}
}

// The configuration reaches the constructor, which is the only place a provider
// can act on it.
func TestTheHandshakeConfiguresTheProvider(t *testing.T) {
	var got Config
	s := NewServer(func(cfg Config) (core.Provider, error) {
		got = cfg
		return &fakeProvider{widget: &widgetHandler{}}, nil
	})
	want := Config{Root: "/fixture", DryRun: true, Verbose: true}
	if _, err := Serve(context.Background(), s, want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("config = %+v, want %+v", got, want)
	}
}

// A provider that cannot start must say so at the handshake rather than fail
// every command afterwards.
func TestAProviderThatCannotStartFailsTheHandshake(t *testing.T) {
	s := NewServer(func(Config) (core.Provider, error) {
		return nil, core.Unavailablef("no such thing on this machine")
	})
	_, err := Serve(context.Background(), s, Config{})
	if core.CodeOf(err) != core.CodeUnavailable {
		t.Errorf("code = %q, want UNAVAILABLE: %v", core.CodeOf(err), err)
	}
}

func handshaken(t *testing.T) *Server {
	t.Helper()
	s := NewServerOf(&fakeProvider{widget: &widgetHandler{}})
	resp := s.Handle(context.Background(), Request{ID: 1, Method: MethodHandshake,
		Params: json.RawMessage(`{"protocol":"` + Version + `"}`)})
	if resp.Error != nil {
		t.Fatalf("handshake: %v", resp.Error)
	}
	return s
}

func as(err error, target **core.Error) bool {
	for err != nil {
		if e, ok := err.(*core.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// --- what rides the context has to ride the wire ------------------------

// core.DeleteOptions reaches a handler through the context, and a context does
// not cross a process boundary: the provider's is its own. --cascade was lost
// that way the moment providers moved behind the protocol, and nothing on the
// host noticed — it took five containers.
//
// So every field of DeleteOptions must have one on DeleteParams. This fails
// when the next option is added without one, instead of the option quietly
// doing nothing.
func TestEveryDeleteOptionCrossesTheWire(t *testing.T) {
	opts := reflect.TypeFor[core.DeleteOptions]()
	params := reflect.TypeFor[DeleteParams]()

	for i := range opts.NumField() {
		field := opts.Field(i)
		wire, ok := params.FieldByName(field.Name)
		if !ok {
			t.Errorf("core.DeleteOptions.%s has no field on protocol.DeleteParams, so it cannot reach a provider in another process",
				field.Name)
			continue
		}
		if wire.Type != field.Type {
			t.Errorf("DeleteParams.%s is %s, want %s", field.Name, wire.Type, field.Type)
		}
	}
}

// And the option really arrives, rather than merely having somewhere to sit.
func TestCascadeReachesTheProvider(t *testing.T) {
	h := &widgetHandler{}
	client, err := Serve(context.Background(), NewServerOf(&fakeProvider{widget: h}), Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithDeleteOptions(context.Background(), core.DeleteOptions{Cascade: true})
	_ = handlerFor(t, client, "Widget").Delete(ctx, "one")

	if !h.deletedWithCascade {
		t.Error("--cascade did not reach the provider")
	}
}
