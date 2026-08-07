package core

import (
	"slices"

	"github.com/whoctl/whoctl-sdk-go/schema"
)

// A Capability is something a kind can do beyond the five verbs every handler
// implements. `whoctl restart` reaches services and nothing else; `describe`
// renders itself for some kinds and generically for the rest.
//
// # Why a list of strings and not a type assertion
//
// In this process, "can this be restarted" is `handler.(Restarter)`. A provider
// in another process has no Go type to assert on, so what it publishes in its
// schema is this list, and the CLI decides from the list rather than from the
// type.
//
// The interfaces do not go away: something still has to *do* the restarting,
// and in process that is the interface. What changes is that no command asks a
// handler what it implements in order to decide what to offer. CapabilitiesOf
// is the one place the two are reconciled.
type Capability string

const (
	// CapabilityDescribe is Describer: the kind renders its own long form.
	CapabilityDescribe Capability = "describe"
	// CapabilityRestart is Restarter: the kind backs `whoctl restart`.
	CapabilityRestart Capability = "restart"
	// CapabilityScopedList is ScopedLister: a positional argument names a scope
	// rather than an object.
	CapabilityScopedList Capability = "scopedList"
	// CapabilityStatusSchema is StatusTyper: the kind hands out a zeroed status,
	// which is what lets the documentation describe its observed fields.
	CapabilityStatusSchema Capability = "statusSchema"
	// CapabilityWatch is Watcher: the kind streams changes. It is published
	// here as well as in ResourceType.Verbs because the two are read by
	// different sides — whoctl's own commands ask here, and a Kubernetes client
	// reads the verb.
	CapabilityWatch Capability = "watch"
)

// Capable is implemented by a handler that already knows its capabilities
// rather than expressing them as Go interfaces — which is every handler that
// answers for a provider in another process, since what it has is the list its
// provider published.
type Capable interface {
	Capabilities() []Capability
}

// CapabilitiesOf reports what a handler can do, in a stable order so that
// api-resources and the documentation do not reshuffle between runs.
//
// This is the one place where "what a provider published" and "what a Go type
// implements" are reconciled, which is why every command asks here rather than
// asserting on a type of its own.
func CapabilitiesOf(h Handler) []Capability {
	if c, ok := h.(Capable); ok {
		return c.Capabilities()
	}
	var out []Capability
	if _, ok := h.(Describer); ok {
		out = append(out, CapabilityDescribe)
	}
	if _, ok := h.(Restarter); ok {
		out = append(out, CapabilityRestart)
	}
	if _, ok := h.(ScopedLister); ok {
		out = append(out, CapabilityScopedList)
	}
	if _, ok := h.(StatusTyper); ok {
		out = append(out, CapabilityStatusSchema)
	}
	if _, ok := h.(Watcher); ok {
		out = append(out, CapabilityWatch)
	}
	return out
}

// Capabilities reports what this resource's handler can do.
func (r Resource) Capabilities() []Capability { return CapabilitiesOf(r.Handler) }

// Can reports whether the resource has a capability.
func (r Resource) Can(c Capability) bool { return slices.Contains(r.Capabilities(), c) }

// SchemaPublisher is implemented by a handler that carries its field records
// instead of a Go type to reflect over. A provider in another process is the
// case: whoctl has its published schema and nothing to reflect on.
type SchemaPublisher interface {
	SpecSchema() []schema.Field
	StatusSchema() []schema.Field
}

// SpecFieldsOf and StatusFieldsOf are how anything that documents or validates
// a kind gets its fields, whichever side of a process boundary the provider is
// on. Reflection is one implementation of this, not the definition of it.
func SpecFieldsOf(h Handler) []schema.Field {
	if p, ok := h.(SchemaPublisher); ok {
		return p.SpecSchema()
	}
	return schema.Of(h.NewSpec())
}

func StatusFieldsOf(h Handler) []schema.Field {
	if p, ok := h.(SchemaPublisher); ok {
		return p.StatusSchema()
	}
	if st, ok := h.(StatusTyper); ok {
		return schema.Of(st.NewStatus())
	}
	return nil
}
