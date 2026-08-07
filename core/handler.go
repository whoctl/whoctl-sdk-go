package core

import "context"

// Action describes what an Apply did to the object, using the same vocabulary
// as kubectl.
type Action string

const (
	ActionCreated    Action = "created"
	ActionConfigured Action = "configured"
	ActionUnchanged  Action = "unchanged"
)

// Result is what Handler.Apply returns.
type Result struct {
	Action Action
	Object Object
	// Diff describes, in human terms, what changed. Used by `apply -v`.
	Diff []string
}

// Handler implements the verbs for a single kind. Every provider registers one
// Handler per resource it exposes.
//
// Contract:
//   - List and Get read the current system state and return Spec filled with
//     the observed state, so that `get -o yaml` produces a manifest that is
//     valid input for `apply` (round-trip / export).
//   - Apply is an upsert: it creates the resource when missing, reconciles it
//     when present.
//   - Delete returns a NotFound error when the resource does not exist, and
//     every error a handler returns for a case a command distinguishes carries
//     a Code — see errors.go.
type Handler interface {
	Type() ResourceType

	// NewSpec returns a pointer to a zeroed spec for the kind, used to decode
	// manifests.
	NewSpec() any

	List(ctx context.Context) ([]Object, error)
	Get(ctx context.Context, name string) (Object, error)
	Apply(ctx context.Context, obj Object) (Result, error)
	Delete(ctx context.Context, name string) error
}

// Describer is optional: handlers implementing it provide detailed output for
// `whoctl describe`. Without it, describe falls back to a generic rendering of
// spec and status.
type Describer interface {
	Describe(ctx context.Context, name string) (string, error)
}

// Restarter is optional: handlers implementing it back `whoctl restart`. It
// exists because restarting is not expressible as a desired state — the state
// before and after is the same — so it cannot be an `apply`.
type Restarter interface {
	Restart(ctx context.Context, name string) error
}

// EventType is what happened to an object in a watch, using Kubernetes'
// vocabulary because these events are what a Kubernetes client consumes.
type EventType string

const (
	EventAdded    EventType = "ADDED"
	EventModified EventType = "MODIFIED"
	EventDeleted  EventType = "DELETED"
)

// Event is one change to one object.
type Event struct {
	Type   EventType
	Object Object
}

// Watcher is optional: handlers implementing it stream changes instead of only
// answering point-in-time questions.
//
// It exists because a table that never updates is all a client can draw without
// it — k9s and Lens are built on streams, and polling every kind of every
// provider is what they were written to stop doing.
//
// Watch returns when ctx is cancelled, when the stream ends of its own accord,
// or when emit fails, which is how the far side says it stopped listening. A
// provider with nothing better to offer may implement this by polling its own
// List; that is still worth doing, because where the polling lives is the
// difference between one provider doing it and every client doing it.
type Watcher interface {
	Watch(ctx context.Context, emit func(Event) error) error
}

// ScopedLister is optional: handlers implementing it read a positional argument
// as the scope of a listing rather than as the name of one object, so `get` and
// `describe` expand it into however many objects it covers.
//
// It exists for kinds whose objects are not addressable on their own. An
// achievement belongs to a game and its name is unique only within that game,
// so `whoctl get steam/achievements 620` has to mean "every achievement of app
// 620" — a Get returning a single object cannot say that, and listing every
// achievement of every owned game is a thousand API calls nobody asked for.
// Prefer Get: a kind whose objects have names of their own does not want this.
type ScopedLister interface {
	ListScoped(ctx context.Context, scope string) ([]Object, error)
}

// Provider groups handlers under a single API group and is whoctl's unit of
// extension: one provider for linux, another for aws, and so on. Its name is
// the prefix every one of its resources is addressed by, as in `linux/users`.
type Provider interface {
	Name() string
	Handlers() []Handler
}

// Aliaser is optional: a provider implementing it also answers to shorter
// prefixes, so `nix/usr` reaches the same handler as `linux/users`. The prefix
// is now typed on every single command, which is what makes a two-key saving
// worth an interface.
type Aliaser interface {
	Aliases() []string
}
