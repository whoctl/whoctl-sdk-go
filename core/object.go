// Package core defines the object model and the interface every whoctl
// provider implements. The vocabulary deliberately mirrors Kubernetes: an
// object has apiVersion, kind, metadata, spec and status.
//
// It lives outside internal/ because implementing Handler is what writing a
// provider *is*, and a provider in another repository has to be able to import
// it. What only the CLI uses — the registry, manifest decoding — stayed behind
// in whoctl itself.
package core

import "strings"

// Object is the representation of a resource managed by a provider.
//
// Spec and Status are `any` so each provider can use its own typed structs:
// that preserves field order in the YAML output and keeps the core generic.
// Handlers fill Spec with a pointer to the type returned by Handler.NewSpec.
type Object struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       any      `yaml:"spec,omitempty" json:"spec,omitempty"`
	Status     any      `yaml:"status,omitempty" json:"status,omitempty"`
}

// Metadata carries the object identity, with the fields a Kubernetes client
// expects to find there.
//
// # Why the machinery fields are not optional extras
//
// Name and Namespace are what a person types. UID, ResourceVersion and
// CreationTimestamp are what a client needs to be more than a table: AGE is a
// column k9s draws for every kind from CreationTimestamp, and a watch is
// resumed from a ResourceVersion. A provider that leaves them empty still
// works; one that fills them can be driven by a tool nobody wrote for whoctl.
//
// Namespace means whatever the provider's kinds mean by it, and a kind says
// whether it has one at all through ResourceType.Namespaced.
type Metadata struct {
	Name      string `yaml:"name" json:"name"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	// UID identifies this object across deletions and recreations of the same
	// name. A provider whose system has an identifier of its own — an instance
	// id, a hosted zone id — should use it.
	UID string `yaml:"uid,omitempty" json:"uid,omitempty"`
	// ResourceVersion changes whenever the object does. It is opaque: only the
	// provider that wrote it may interpret it, and a client compares it for
	// equality and nothing else.
	ResourceVersion   string            `yaml:"resourceVersion,omitempty" json:"resourceVersion,omitempty"`
	CreationTimestamp Time              `yaml:"creationTimestamp,omitempty" json:"creationTimestamp,omitzero"`
	Labels            map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations       map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// List is the envelope used by `-o yaml` when there is more than one object, so
// the output stays a valid manifest for `apply`.
//
// Kind is the kind's own ListKind — "UserList", not "List" — because that is
// what a Kubernetes client matches on when it decodes a collection.
type List struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   ListMeta `yaml:"metadata,omitempty" json:"metadata,omitzero"`
	Items      []Object `yaml:"items" json:"items"`
}

// ListMeta is what a collection carries instead of an identity: the version the
// listing was taken at, which is where a watch started from would resume.
type ListMeta struct {
	ResourceVersion string `yaml:"resourceVersion,omitempty" json:"resourceVersion,omitempty"`
}

// ResourceType describes a kind exposed by a provider: how to name it, what it
// can do, and how to print it as a table.
//
// It is deliberately the same set of facts Kubernetes publishes about a
// resource in its discovery document, because that is where these go: a kind
// described here can be served to kubectl, k9s or Lens without any of them
// learning anything about whoctl.
//
// # The group is per kind, not per provider
//
// One provider serves as many groups as it has services to cover. That is what
// lets an aws provider expose Instance in "ec2.aws.whoctl.io" and another
// Instance in "rds.aws.whoctl.io" — legal and ordinary in Kubernetes, where a
// kind is unique within its group and version and nowhere else. Group is what
// tells them apart everywhere: on the wire, in a manifest and on the command
// line.
type ResourceType struct {
	Group      string
	Version    string
	Kind       string
	Plural     string
	Singular   string
	ShortNames []string
	// ListKind names a collection of this kind, as in "UserList". Empty means
	// Kind + "List", which is the Kubernetes convention and what every kind
	// should want.
	ListKind string
	// Namespaced says whether an object of this kind lives in a namespace.
	// Every Kubernetes client branches on it, so it is not optional
	// information — a kind that is global (a hosted zone, a linux user) leaves
	// it false and its objects carry no namespace.
	Namespaced bool
	// Categories are the collective names this kind answers to, as in `kubectl
	// get all`. Empty is normal.
	Categories []string
	// Verbs are what this kind serves, in Kubernetes' vocabulary — see
	// VerbsOf, which is what anything asking should call. Empty means the five
	// a Handler always implements; a read-only kind narrows it, and narrowing
	// it here is how a client learns not to offer an edit rather than being
	// told after trying.
	Verbs   []string
	Columns []Column
	// Description is a single line about the kind, used by `whoctl docs`. It
	// lives here rather than in the markdown so the kind carries its own
	// summary even before anybody writes a page for it.
	Description string
}

// APIVersion returns "group/version", as in "route53.aws.whoctl.io/v1alpha1".
func (r ResourceType) APIVersion() string {
	return r.Group + "/" + r.Version
}

// GVK is the triple that identifies this kind everywhere.
func (r ResourceType) GVK() GVK {
	return GVK{Group: r.Group, Version: r.Version, Kind: r.Kind}
}

// CollectionKind is the name of a list of this kind, defaulted.
func (r ResourceType) CollectionKind() string {
	if r.ListKind != "" {
		return r.ListKind
	}
	return r.Kind + "List"
}

// GVK identifies a kind by group, version and kind, which is the only
// identification that is unique.
//
// # Why the whole triple travels
//
// Dispatch used to key on Kind alone, which holds exactly as long as no
// provider serves two kinds of the same name. A provider covering several
// services of one cloud does that immediately — Instance under ec2 and
// Instance under rds — and keying on the name alone did not fail loudly there:
// one handler quietly replaced the other in a map.
type GVK struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// APIVersion returns "group/version".
func (g GVK) APIVersion() string { return g.Group + "/" + g.Version }

// String is "group/version, Kind=X", the spelling Kubernetes uses in errors.
func (g GVK) String() string { return g.APIVersion() + ", Kind=" + g.Kind }

// MatchesName reports whether name — the part after the provider prefix —
// identifies this resource. It accepts the plural, the singular, the kind and
// any short name: "users", "user", "User" or "usr".
//
// It also accepts the qualified form kubectl uses, "resource.group", where the
// group may be given in full or cut short at a label boundary: "instances.ec2",
// "instances.ec2.aws.whoctl.io". That is what disambiguates two kinds sharing a
// plural, and it is the same syntax as `kubectl get jobs.batch`.
//
// The provider prefix is matched by the registry rather than here, because a
// ResourceType does not know which provider serves it.
func (r ResourceType) MatchesName(name string) bool {
	bare, qualifier, qualified := strings.Cut(name, ".")
	if qualified && !r.MatchesGroupPrefix(qualifier) {
		return false
	}
	return r.matchesBareName(bare)
}

// MatchesGroupPrefix reports whether prefix names this kind's group, either in
// full or cut short at a label boundary: "route53", "route53.aws" and
// "route53.aws.whoctl.io" all name "route53.aws.whoctl.io", and "route5" names
// nothing.
//
// Cutting at a label is what lets a command line say `aws/route53/hostedzones`
// without anybody spelling out a DNS name. Where the cut is allowed is the
// whole point: matching any prefix would make "aws" match "aws.whoctl.io" and
// every group under it at once.
func (r ResourceType) MatchesGroupPrefix(prefix string) bool {
	prefix, group := strings.ToLower(prefix), strings.ToLower(r.Group)
	return prefix != "" && (prefix == group || strings.HasPrefix(group, prefix+"."))
}

func (r ResourceType) matchesBareName(name string) bool {
	name = strings.ToLower(name)
	if name == strings.ToLower(r.Plural) || name == strings.ToLower(r.Singular) || name == strings.ToLower(r.Kind) {
		return true
	}
	for _, short := range r.ShortNames {
		if name == strings.ToLower(short) {
			return true
		}
	}
	return false
}

// Column is a column of the table output. Columns marked Wide only show up
// under `-o wide`.
//
// A column is data, not code: Path names the value — see Lookup for the syntax
// — and Format says how to render it. That is what lets a provider in another
// process describe its table, and it keeps the rendering itself in the printer,
// where it belongs.
type Column struct {
	Name string
	Wide bool
	// Path is the value the cell shows, spelled as in a manifest:
	// "status.uid", "metadata.name", "status.baseUrl|status.metalink".
	Path string
	// Format renders the value. Empty means the obvious rendering for the type
	// — a number in decimal, a boolean as true or false, a list comma-joined —
	// which is what almost every column wants. See printers for the vocabulary.
	Format string
}

// Formats a Column may ask for. The set is closed: a provider cannot ship a
// formatter, because the printer runs in whoctl's process and not in theirs.
const (
	// FormatBytes renders a byte count as "23.8M".
	FormatBytes = "bytes"
	// FormatMinutes renders a count of minutes as "45m" or "3.2h".
	FormatMinutes = "minutes"
	// FormatFirst renders only the first element of a list.
	FormatFirst = "first"
	// FormatAge renders a timestamp as how long ago it was, "3d" or "5m". It
	// is what the AGE column shows, which every Kubernetes client draws from
	// metadata.creationTimestamp.
	FormatAge = "age"
)
