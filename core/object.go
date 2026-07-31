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

// Metadata carries the object identity. Resources from the linux provider are
// not namespaced; the field exists for cloud providers that need it.
type Metadata struct {
	Name        string            `yaml:"name" json:"name"`
	Namespace   string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// List is the envelope used by `-o yaml` when there is more than one object, so
// the output stays a valid manifest for `apply`.
type List struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Items      []Object `yaml:"items" json:"items"`
}

// ResourceType describes a kind exposed by a provider: how to name it on the
// command line and how to print it as a table.
type ResourceType struct {
	Group      string
	Version    string
	Kind       string
	Plural     string
	Singular   string
	ShortNames []string
	Columns    []Column
	// Description is a single line about the kind, used by `whoctl docs`. It
	// lives here rather than in the markdown so the kind carries its own
	// summary even before anybody writes a page for it.
	Description string
}

// APIVersion returns "group/version", as in "linux.whoctl.io/v1alpha1".
func (r ResourceType) APIVersion() string {
	return r.Group + "/" + r.Version
}

// MatchesName reports whether name — the part after the provider prefix —
// identifies this resource. It accepts the plural, the singular, the kind and
// any short name: "users", "user", "User" or "usr".
//
// The prefix is matched by the registry rather than here, because a
// ResourceType does not know which provider serves it.
func (r ResourceType) MatchesName(name string) bool {
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
)
