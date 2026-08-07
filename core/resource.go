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

// Name returns the canonical qualified name, as in "linux/users" or
// "aws/route53/hostedzones". It is what `resources` lists and what error
// messages suggest.
func (r Resource) Name() string {
	return r.Provider.Name() + "/" + r.path() + r.Type().Plural
}

// Ref returns the reference to a single object, as in "linux/user/alice" or
// "aws/route53/hostedzone/example.com.". `-o name` prints it and apply, delete
// and restart report it, so what one command prints is a name the next one
// accepts.
func (r Resource) Ref(name string) string {
	return r.Provider.Name() + "/" + r.path() + r.Type().Singular + "/" + name
}

// GroupPath is the part of a kind's group that the command line spells between
// the provider and the resource: "route53" for a kind in
// "route53.aws.whoctl.io" served by the provider named "aws", and nothing at
// all for a kind in "linux.whoctl.io" served by "linux".
//
// # Why it is derived rather than declared
//
// The group already says everything. Reading it as a path is just reading it
// backwards, with the provider's own label as the point where its name ends and
// its services begin: route53.aws.whoctl.io is aws/route53. A separate field
// would be the same fact spelled a second way, and two spellings of one fact
// eventually disagree.
//
// A group that does not contain the provider's name has no path — it is still
// reachable as provider/resource, and as provider/resource.group when something
// else shares the name.
func (r Resource) GroupPath() string {
	labels := strings.Split(r.Type().Group, ".")
	for i, label := range labels {
		if strings.EqualFold(label, r.Provider.Name()) {
			return strings.Join(labels[:i], "/")
		}
	}
	return ""
}

func (r Resource) path() string {
	if p := r.GroupPath(); p != "" {
		return p + "/"
	}
	return ""
}
