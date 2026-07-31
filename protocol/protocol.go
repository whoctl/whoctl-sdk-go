// Package protocol is the contract between whoctl and a provider.
//
// A provider is a process that speaks this over stdio: one JSON request per
// line in, one JSON response per line out. The CLI has the client half, the SDK
// has the server half, and neither needs a Go type from the other — which is
// the point, since a provider may not be written in Go at all.
package protocol

import (
	"encoding/json"

	"github.com/whoctl/whoctl-sdk-go/schema"
)

// Version is the protocol version, negotiated in the handshake. It changes when
// a provider built against an older whoctl would misbehave rather than merely
// miss a feature; anything additive does not bump it.
const Version = "1"

// The methods a provider serves. A provider must answer Handshake and Schema;
// everything else is reached only for a kind whose capabilities allow it.
const (
	MethodHandshake  = "handshake"
	MethodSchema     = "schema"
	MethodList       = "list"
	MethodGet        = "get"
	MethodListScoped = "listScoped"
	MethodApply      = "apply"
	MethodDelete     = "delete"
	MethodDescribe   = "describe"
	MethodRestart    = "restart"
)

// Request is one call. ID is echoed in the response so a transport that
// pipelines can match them up; the stdio transport is strictly
// request-response and sets it anyway, because a mismatch is a bug worth
// catching rather than a state worth tolerating.
type Request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is one answer. Exactly one of Result and Error is set.
type Response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a failure with a code, which is what survives the trip that a Go
// error type does not. Message is the whole text the user sees.
type Error struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Resource string `json:"resource,omitempty"`
	Name     string `json:"name,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Handshake is what a provider says about itself before anything else.
type Handshake struct {
	// Protocol is the version of this contract the provider implements.
	Protocol string `json:"protocol"`
	// Name is the prefix its resources are addressed by, as in "linux".
	Name string `json:"name"`
	// Aliases are shorter prefixes that reach the same provider.
	Aliases []string `json:"aliases,omitempty"`
	// Version is the provider's own release version.
	Version string `json:"version,omitempty"`
	// HonoursDryRun is the provider's claim that it routes every mutation
	// through a runner that respects the flag. whoctl cannot verify it — see
	// the design note on what the split costs — so it is recorded, not trusted.
	HonoursDryRun bool `json:"honoursDryRun"`
}

// Config is what whoctl tells a provider about the run. It is deliberately tiny
// and deliberately not extensible per provider: anything a single provider
// needs it reads from its own environment, or the CLI ends up carrying flags
// for kinds it knows nothing about.
type Config struct {
	// Root is the filesystem root to read from, from --root.
	Root string `json:"root,omitempty"`
	// DryRun asks the provider to report what it would do and change nothing.
	DryRun bool `json:"dryRun,omitempty"`
	// Verbose asks it to log each external command it runs, on stderr.
	Verbose bool `json:"verbose,omitempty"`
}

// HandshakeParams carries the configuration for the whole session.
type HandshakeParams struct {
	// Protocol is the version whoctl speaks.
	Protocol string `json:"protocol"`
	Config   Config `json:"config"`
}

// Schema is everything whoctl needs to know about a provider's kinds without
// having any of its Go types: how to name them, how to print them, what they
// can do, and what their fields are.
type Schema struct {
	Resources []ResourceType `json:"resources"`
}

// ResourceType describes one kind on the wire.
type ResourceType struct {
	Group       string   `json:"group"`
	Version     string   `json:"version"`
	Kind        string   `json:"kind"`
	Plural      string   `json:"plural"`
	Singular    string   `json:"singular"`
	ShortNames  []string `json:"shortNames,omitempty"`
	Description string   `json:"description,omitempty"`
	Columns     []Column `json:"columns,omitempty"`
	// Capabilities are the optional verbs this kind serves, which in this
	// process would have been Go interfaces.
	Capabilities []string `json:"capabilities,omitempty"`
	// Spec and Status are the field records the documentation is generated
	// from, and — once manifests are validated against the published schema
	// rather than against a Go type — what an apply is checked against.
	Spec   []schema.Field `json:"spec,omitempty"`
	Status []schema.Field `json:"status,omitempty"`
}

// Column is a table column on the wire.
type Column struct {
	Name   string `json:"name"`
	Wide   bool   `json:"wide,omitempty"`
	Path   string `json:"path"`
	Format string `json:"format,omitempty"`
}

// Object is a resource on the wire. Spec and Status keep their declared field
// order — see Map for why that matters.
type Object struct {
	APIVersion  string            `json:"apiVersion"`
	Kind        string            `json:"kind"`
	Metadata    Metadata          `json:"metadata"`
	Spec        *Map              `json:"spec,omitempty"`
	Status      *Map              `json:"status,omitempty"`
	Annotations map[string]string `json:"-"`
}

// Metadata is the object identity on the wire.
type Metadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Params for the per-kind methods. Kind names the resource; it is the Kind
// field of ResourceType, not the plural, because that is what a manifest says
// and what cannot be ambiguous.
type (
	// KindParams addresses a whole kind.
	KindParams struct {
		Kind string `json:"kind"`
	}
	// NameParams addresses one object.
	NameParams struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	// ScopeParams addresses everything a scope covers.
	ScopeParams struct {
		Kind  string `json:"kind"`
		Scope string `json:"scope"`
	}
	// ApplyParams carries the desired object.
	ApplyParams struct {
		Kind   string `json:"kind"`
		Object Object `json:"object"`
	}
	// DeleteParams addresses one object and carries the options for removing
	// it. Every field of core.DeleteOptions must appear here: they reach a
	// handler through the context, and a context does not cross a process
	// boundary — see the note on core.DeleteOptions.
	DeleteParams struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		// Cascade also removes what the object owns, such as a user's home
		// directory.
		Cascade bool `json:"cascade,omitempty"`
	}
)

// Results.
type (
	// ObjectsResult is what list and listScoped return.
	ObjectsResult struct {
		Objects []Object `json:"objects"`
	}
	// ObjectResult is what get returns.
	ObjectResult struct {
		Object Object `json:"object"`
	}
	// ApplyResult mirrors core.Result.
	ApplyResult struct {
		Action string   `json:"action"`
		Object Object   `json:"object"`
		Diff   []string `json:"diff,omitempty"`
	}
	// TextResult is what describe returns.
	TextResult struct {
		Text string `json:"text"`
	}
)
