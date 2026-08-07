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

// Scope narrows what a call is about. It travels the same way DeleteOptions
// does, and for the same reason: the Handler interface stays uniform, so a
// provider whose kinds are all global never learns that namespaces exist.
//
// # Every field here needs a field on the wire
//
// This is the lesson of DeleteOptions written down a second time, because the
// failure looks identical: an option that only rides the context arrives as its
// zero value on the far side, and a namespaced list silently answers for the
// wrong slice of the world. TestEveryScopeFieldCrossesTheWire is what fails
// when a field is added here and nowhere else.
type Scope struct {
	// Namespace is the one to answer for. A kind whose ResourceType.Namespaced
	// is false ignores it.
	Namespace string
	// AllNamespaces asks for every one of them.
	//
	// # Why this is not just an empty Namespace
	//
	// Because "every namespace" and "whichever one is the default" are
	// different questions, and a provider is the only thing that knows the
	// answer to the second: the aws provider's default region comes from the
	// same AWS configuration it authenticates with, and nothing above it can
	// read that. Encoding both as an empty string would make `-A` and no flag
	// at all indistinguishable, and for a regional kind those are the
	// difference between one API call and thirty.
	//
	// Kubernetes draws the same distinction, in the URL: a collection is either
	// /apis/g/v/resource or /apis/g/v/namespaces/ns/resource, and kubectl picks
	// between them from the context. This is that choice, spelled as a field.
	AllNamespaces bool

	// LabelSelector and FieldSelector narrow a listing, in the syntax
	// ParseSelector reads. They are strings rather than parsed Selectors
	// because that is what crosses the wire, and both sides parse the same
	// text with the same function.
	//
	// A handler may use them to fetch less — the aws provider turns a state
	// requirement into an EC2 filter — and may equally ignore them. whoctl
	// applies both to whatever comes back, so a provider that ignores them is
	// slower and never wrong.
	LabelSelector string
	FieldSelector string
}

// Labels parses the label selector, or an error naming what is wrong with it.
func (s Scope) Labels() (Selector, error) { return ParseSelector(s.LabelSelector) }

// Fields parses the field selector.
func (s Scope) Fields() (Selector, error) { return ParseSelector(s.FieldSelector) }

type scopeKey struct{}

// WithScope attaches a scope to the context.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// ScopeFrom reads the scope from the context, returning the zero value — every
// namespace — when none was set.
func ScopeFrom(ctx context.Context) Scope {
	s, _ := ctx.Value(scopeKey{}).(Scope)
	return s
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
