package core

import "context"

// DeleteOptions carries per-call options for Handler.Delete. They travel in the
// context so the Handler interface stays uniform across providers.
//
// # Every field here needs a field on protocol.DeleteParams
//
// A context does not cross a process boundary. The provider serving a delete
// runs with a context of its own, so an option that only rides the context
// arrives as its zero value and the flag silently does nothing — which is
// exactly what happened to --cascade when providers moved behind the protocol,
// and only the container suite noticed.
//
// So the wire carries them explicitly and the server rebuilds the context on
// the far side. TestEveryDeleteOptionCrossesTheWire fails if a field is added
// here without one there.
type DeleteOptions struct {
	// Cascade also removes resources owned by the object. For a linux user
	// that means the home directory; for a cloud provider it might mean
	// attached volumes.
	Cascade bool
}

type deleteOptionsKey struct{}

// WithDeleteOptions attaches delete options to the context.
func WithDeleteOptions(ctx context.Context, opts DeleteOptions) context.Context {
	return context.WithValue(ctx, deleteOptionsKey{}, opts)
}

// DeleteOptionsFrom reads delete options from the context, returning the zero
// value when none were set.
func DeleteOptionsFrom(ctx context.Context) DeleteOptions {
	opts, _ := ctx.Value(deleteOptionsKey{}).(DeleteOptions)
	return opts
}
