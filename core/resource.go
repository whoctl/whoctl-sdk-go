package core

import "strings"

// Resource is a handler together with the provider that serves it.
//
// The pair is the unit the command line works with, because every resource is
// named `provider/resource`: a Handler on its own cannot say where it came
// from, and the prefix is exactly what tells `linux/users` apart from
// `aws/users`.
type Resource struct {
	Provider Provider
	Handler  Handler
}

// Type is shorthand for the handler's resource type.
func (r Resource) Type() ResourceType { return r.Handler.Type() }

// Name returns the canonical qualified name, as in "linux/users". It is what
// `api-resources` lists and what error messages suggest.
func (r Resource) Name() string {
	return r.Provider.Name() + "/" + r.Type().Plural
}

// Ref returns the reference to a single object, as in "linux/user/alice".
// `-o name` prints it and apply, delete and restart report it, so what one
// command prints is a name the next one accepts.
func (r Resource) Ref(name string) string {
	return r.Provider.Name() + "/" + r.Type().Singular + "/" + name
}

// SplitRef splits an argument that may already name one object. "linux/users"
// yields no name; "linux/user/alice" yields "alice". It is what lets the output
// of `get -o name` be pasted straight back into another verb.
//
// Only the first two segments are the resource, so a name containing a slash —
// a repository URL, say — survives intact.
func SplitRef(arg string) (resource, name string) {
	parts := strings.SplitN(arg, "/", 3)
	if len(parts) < 3 {
		return arg, ""
	}
	return parts[0] + "/" + parts[1], parts[2]
}
