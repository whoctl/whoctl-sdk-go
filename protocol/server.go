package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// ProviderFunc builds a provider from the session's configuration.
//
// It is a function rather than a value because --root, --dry-run and --verbose
// arrive in the handshake, and every provider takes them at construction: the
// filesystem root decides what it reads, and the runner decides whether a
// mutation runs or is only printed. A provider built before the handshake would
// have to be reconfigured afterwards, and "reconfigure" is exactly the kind of
// second path where a flag gets forgotten.
type ProviderFunc func(Config) (core.Provider, error)

// Server answers protocol requests on behalf of a core.Provider. It is the half
// that moves to the SDK: a provider author implements core.Handler as they
// always have, and this turns it into a process that speaks the protocol.
type Server struct {
	newProvider ProviderFunc
	// Version is the provider's own release version, reported in the handshake.
	Version string
	// HonoursDryRun is the provider's claim that every mutation goes through a
	// runner respecting the flag. Providers built on the SDK's sysexec do.
	HonoursDryRun bool

	provider core.Provider
	// handlers is keyed by the whole triple. Keying it by kind alone let one
	// handler replace another the moment a provider served two kinds of the
	// same name in different groups, which is ordinary for a provider covering
	// several services of one cloud.
	handlers map[Ref]core.Handler

	// watches are the streams currently open, so stopWatch can end one. The
	// mutex guards it because a watch runs concurrently with the requests
	// arriving behind it — that is the whole reason it is a stream.
	mu      sync.Mutex
	watches map[int]context.CancelFunc
}

// NewServer wraps a provider constructor.
func NewServer(new ProviderFunc) *Server {
	return &Server{
		newProvider:   new,
		HonoursDryRun: true,
		handlers:      map[Ref]core.Handler{},
		watches:       map[int]context.CancelFunc{},
	}
}

// NewServerOf wraps a provider that is already built, for a caller that has no
// configuration to apply — the in-process case and the tests.
func NewServerOf(p core.Provider) *Server {
	return NewServer(func(Config) (core.Provider, error) { return p, nil })
}

// Handle answers one request. It never returns a Go error: a failure is a
// Response carrying an Error, because that is what a transport can write.
func (s *Server) Handle(ctx context.Context, req Request) Response {
	result, err := s.dispatch(ctx, req)
	if err != nil {
		return Response{ID: req.ID, Error: errorOf(err)}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Response{ID: req.ID, Error: &Error{Code: string(core.CodeInternal), Message: err.Error()}}
	}
	return Response{ID: req.ID, Result: encoded}
}

func (s *Server) dispatch(ctx context.Context, req Request) (any, error) {
	switch req.Method {
	case MethodHandshake:
		var params HandshakeParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if params.Protocol != Version {
			return nil, core.Invalidf("whoctl speaks protocol %s and this provider speaks %s", params.Protocol, Version)
		}
		p, err := s.newProvider(params.Config)
		if err != nil {
			return nil, err
		}
		s.provider = p
		for _, h := range p.Handlers() {
			ref := RefOf(h.Type())
			if _, clash := s.handlers[ref]; clash {
				// Two handlers under one triple is a provider bug that used to
				// be silent, and the symptom was one kind answering for
				// another. providertest catches it; this is the backstop for a
				// provider that never ran the suite.
				return nil, core.Invalidf("this provider serves %s twice", ref)
			}
			s.handlers[ref] = h
		}
		return Handshake{
			Protocol:      Version,
			Name:          s.provider.Name(),
			Aliases:       aliasesOf(s.provider),
			Version:       s.Version,
			HonoursDryRun: s.HonoursDryRun,
		}, nil

	case MethodSchema:
		return s.schema(), nil
	}

	if s.provider == nil {
		return nil, core.Invalidf("no handshake: whoctl must call %q before anything else", MethodHandshake)
	}

	if req.Method == MethodStopWatch {
		var params StopWatchParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		s.stopWatch(params.Watch)
		// Stopping a watch that already ended is not a failure: the stream may
		// have closed itself between the last frame and this call.
		return struct{}{}, nil
	}

	// Everything else names a kind, and may name a namespace to answer for.
	h, ref, err := s.handlerFor(req.Params)
	if err != nil {
		return nil, err
	}
	ctx, err = withScope(ctx, req.Params)
	if err != nil {
		return nil, err
	}
	kind := ref.Kind

	switch req.Method {
	case MethodList:
		objs, err := h.List(ctx)
		if err != nil {
			return nil, err
		}
		return objectsResult(objs)

	case MethodWatch:
		return nil, core.Invalidf("%s is a stream and this transport did not open one", MethodWatch)

	case MethodGet:
		var params NameParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		obj, err := h.Get(ctx, params.Name)
		if err != nil {
			return nil, err
		}
		wire, err := ObjectFrom(obj)
		if err != nil {
			return nil, err
		}
		return ObjectResult{Object: wire}, nil

	case MethodListScoped:
		var params ScopeParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		scoped, ok := h.(core.ScopedLister)
		if !ok {
			return nil, core.Unsupportedf("%s is not listed by scope", kind)
		}
		objs, err := scoped.ListScoped(ctx, params.Scope)
		if err != nil {
			return nil, err
		}
		return objectsResult(objs)

	case MethodApply:
		var params ApplyParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		// The spec arrives as JSON and is decoded into the provider's own type,
		// which is where a manifest stops being generic and starts being
		// checked. Nothing else in the pipeline knows what a UID is.
		obj, err := params.Object.Decode(h.NewSpec())
		if err != nil {
			return nil, core.Invalidf("%s %q: invalid spec: %w", kind, params.Object.Metadata.Name, err)
		}
		res, err := h.Apply(ctx, obj)
		if err != nil {
			return nil, err
		}
		applied, err := ObjectFrom(res.Object)
		if err != nil {
			return nil, err
		}
		return ApplyResult{Action: string(res.Action), Object: applied, Diff: res.Diff}, nil

	case MethodDelete:
		var params DeleteParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		// The options reach the handler the way they always have, through the
		// context; what changed is that they got here explicitly rather than
		// by riding a context that cannot cross a pipe.
		ctx = core.WithDeleteOptions(ctx, core.DeleteOptions{Cascade: params.Cascade})
		return struct{}{}, h.Delete(ctx, params.Name)

	case MethodDescribe:
		var params NameParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		describer, ok := h.(core.Describer)
		if !ok {
			return nil, core.Unsupportedf("%s does not describe itself", kind)
		}
		text, err := describer.Describe(ctx, params.Name)
		if err != nil {
			return nil, err
		}
		return TextResult{Text: text}, nil

	case MethodRestart:
		var params NameParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		restarter, ok := h.(core.Restarter)
		if !ok {
			return nil, core.Unsupportedf("%s cannot be restarted", kind)
		}
		return struct{}{}, restarter.Restart(ctx, params.Name)
	}
	return nil, core.Invalidf("unknown method %q", req.Method)
}

// HandleStream answers a request that may be a stream, emitting one frame per
// answer. A method that is not streaming emits exactly one and returns.
//
// # Why the provider side grew concurrency
//
// A watch runs until somebody stops it, and the requests behind it have to keep
// being served — a `get` while a table is streaming is the ordinary case, not
// an exotic one. So a watch runs in its own goroutine and the transport
// serializes the writing.
//
// That concurrency reaches exactly the handlers that implement core.Watcher and
// no others: a provider with no watch is called the way it always was, one
// request at a time. A provider that does implement it is saying its Watch may
// run alongside its other verbs.
func (s *Server) HandleStream(ctx context.Context, req Request, emit func(Response) error) error {
	if !Streaming(req.Method) {
		return emit(s.Handle(ctx, req))
	}
	if s.provider == nil {
		return emit(Response{ID: req.ID, Error: errorOf(
			core.Invalidf("no handshake: whoctl must call %q before anything else", MethodHandshake))})
	}

	h, ref, err := s.handlerFor(req.Params)
	if err != nil {
		return emit(Response{ID: req.ID, Error: errorOf(err)})
	}
	watcher, ok := h.(core.Watcher)
	if !ok {
		return emit(Response{ID: req.ID, Error: errorOf(core.Unsupportedf("%s is not watched", ref.Kind))})
	}
	scoped, err := withScope(ctx, req.Params)
	if err != nil {
		return emit(Response{ID: req.ID, Error: errorOf(err)})
	}

	scoped, cancel := context.WithCancel(scoped)
	defer cancel()
	s.startWatch(req.ID, cancel)
	defer s.stopWatch(req.ID)

	err = watcher.Watch(scoped, func(event core.Event) error {
		wire, err := ObjectFrom(event.Object)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(EventResult{Type: string(event.Type), Object: wire})
		if err != nil {
			return err
		}
		return emit(Response{ID: req.ID, Result: encoded, Stream: true})
	})
	// A watch the far side stopped ends normally: cancellation is how it says
	// so, and reporting it as a failure would print an error for every `-w`
	// somebody ctrl-C'd.
	if err != nil && scoped.Err() == nil {
		return emit(Response{ID: req.ID, Error: errorOf(err)})
	}
	return emit(Response{ID: req.ID})
}

func (s *Server) schema() Schema {
	var out Schema
	for _, h := range s.provider.Handlers() {
		t := h.Type()
		rt := ResourceType{
			Group: t.Group, Version: t.Version, Kind: t.Kind,
			Plural: t.Plural, Singular: t.Singular, ShortNames: t.ShortNames,
			// Both are sent resolved rather than defaulted on arrival, so the
			// two sides cannot disagree about what a kind is called or what it
			// serves.
			ListKind:    t.CollectionKind(),
			Verbs:       core.VerbsOf(h),
			Namespaced:  t.Namespaced,
			Categories:  t.Categories,
			Description: t.Description,
			Spec:        core.SpecFieldsOf(h),
			Status:      core.StatusFieldsOf(h),
		}
		for _, c := range t.Columns {
			rt.Columns = append(rt.Columns, Column{Name: c.Name, Wide: c.Wide, Path: c.Path, Format: c.Format})
		}
		for _, c := range core.CapabilitiesOf(h) {
			rt.Capabilities = append(rt.Capabilities, string(c))
		}
		out.Resources = append(out.Resources, rt)
	}
	return out
}

// ObjectFrom converts a core.Object for the wire, keeping the field order of
// the provider's spec and status structs.
func ObjectFrom(o core.Object) (Object, error) {
	spec, err := MapFrom(o.Spec)
	if err != nil {
		return Object{}, err
	}
	status, err := MapFrom(o.Status)
	if err != nil {
		return Object{}, err
	}
	return Object{
		APIVersion: o.APIVersion,
		Kind:       o.Kind,
		Metadata: Metadata{
			Name:              o.Metadata.Name,
			Namespace:         o.Metadata.Namespace,
			UID:               o.Metadata.UID,
			ResourceVersion:   o.Metadata.ResourceVersion,
			CreationTimestamp: o.Metadata.CreationTimestamp,
			Labels:            o.Metadata.Labels,
			Annotations:       o.Metadata.Annotations,
		},
		Spec:   spec,
		Status: status,
	}, nil
}

// Decode turns a wire object back into a core.Object, decoding its spec into
// the value the handler hands out. A nil target leaves the spec ordered and
// generic, which is what the client side wants.
func (o Object) Decode(spec any) (core.Object, error) {
	out := core.Object{
		APIVersion: o.APIVersion,
		Kind:       o.Kind,
		Metadata: core.Metadata{
			Name:              o.Metadata.Name,
			Namespace:         o.Metadata.Namespace,
			UID:               o.Metadata.UID,
			ResourceVersion:   o.Metadata.ResourceVersion,
			CreationTimestamp: o.Metadata.CreationTimestamp,
			Labels:            o.Metadata.Labels,
			Annotations:       o.Metadata.Annotations,
		},
		Status: statusValue(o.Status),
	}
	if spec == nil {
		out.Spec = specValue(o.Spec)
		return out, nil
	}
	if o.Spec != nil {
		data, err := json.Marshal(o.Spec)
		if err != nil {
			return core.Object{}, err
		}
		if err := json.Unmarshal(data, spec); err != nil {
			return core.Object{}, err
		}
	}
	out.Spec = spec
	return out, nil
}

// A nil *Map must reach core.Object as a nil interface, or `spec:` shows up in
// the yaml output as an empty mapping instead of being omitted.
func specValue(m *Map) any {
	if m == nil {
		return nil
	}
	return m
}

func statusValue(m *Map) any { return specValue(m) }

func objectsResult(objs []core.Object) (ObjectsResult, error) {
	out := ObjectsResult{Objects: make([]Object, 0, len(objs))}
	for _, o := range objs {
		wire, err := ObjectFrom(o)
		if err != nil {
			return ObjectsResult{}, err
		}
		out.Objects = append(out.Objects, wire)
	}
	return out, nil
}

func decodeParams(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return core.Invalidf("bad params: %w", err)
	}
	return nil
}

// handlerFor resolves the triple every per-kind request carries.
func (s *Server) handlerFor(raw json.RawMessage) (core.Handler, Ref, error) {
	var params KindParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, Ref{}, err
	}
	if params.Kind == "" {
		return nil, Ref{}, core.Invalidf("no kind given")
	}
	h, ok := s.handlers[params.Ref]
	if !ok {
		// Naming the whole triple matters here: a provider that serves the kind
		// in another group is a different mistake from one that does not serve
		// it at all, and the message is all whoctl has to tell them apart.
		return nil, params.Ref, core.Invalidf("this provider serves no %s", params.Ref)
	}
	return h, params.Ref, nil
}

// withScope puts the namespace the call named onto the context, the way delete
// options travel. A handler for a kind with no namespaces never reads it.
func withScope(ctx context.Context, raw json.RawMessage) (context.Context, error) {
	var params KindParams
	if err := decodeParams(raw, &params); err != nil {
		return ctx, err
	}
	return core.WithScope(ctx, core.Scope{
		Namespace:     params.Namespace,
		AllNamespaces: params.AllNamespaces,
		LabelSelector: params.LabelSelector,
		FieldSelector: params.FieldSelector,
	}), nil
}

func (s *Server) startWatch(id int, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watches[id] = cancel
}

func (s *Server) stopWatch(id int) {
	s.mu.Lock()
	cancel := s.watches[id]
	delete(s.watches, id)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func aliasesOf(p core.Provider) []string {
	if a, ok := p.(core.Aliaser); ok {
		return a.Aliases()
	}
	return nil
}

// errorOf flattens a Go error into what the wire carries: a code and the text.
// The cause chain does not travel, which is documented on core.Error and is why
// nothing downstream may depend on it.
func errorOf(err error) *Error {
	out := &Error{Code: string(core.CodeOf(err)), Message: err.Error()}
	var coded *core.Error
	if errors.As(err, &coded) {
		out.Resource, out.Name = coded.Resource, coded.Name
	}
	return out
}
