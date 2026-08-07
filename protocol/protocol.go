// Package protocol is the contract between whoctl and a provider.
//
// A provider is a process that speaks this over stdio: one JSON request per
// line in, one JSON response per line out. The CLI has the client half, the SDK
// has the server half, and neither needs a Go type from the other — which is
// the point, since a provider may not be written in Go at all.
package protocol

import (
	"encoding/json"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/schema"
)

// Version is the protocol version, negotiated in the handshake. It changes when
// a provider built against an older whoctl would misbehave rather than merely
// miss a feature; anything additive does not bump it.
//
// Version 2 addresses kinds by group, version and kind rather than by kind
// alone, and adds watch. A version 1 provider misbehaves against it in the
// worst way available — its handlers are keyed by a name that is not unique, so
// one silently answers for another — which is exactly the case this number
// exists for.
const Version = "2"

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
	// MethodWatch opens a stream. Its response is many frames rather than one —
	// see Response.Stream.
	MethodWatch = "watch"
	// MethodStopWatch closes one, naming the request id of the watch it ends.
	MethodStopWatch = "stopWatch"
)

// Streaming reports whether a method answers with a stream of frames instead of
// a single response. A transport has to know before it reads.
func Streaming(method string) bool { return method == MethodWatch }

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
//
// A streaming method answers with many of these under one id. Stream marks
// every frame but the last, so the reader knows another is coming; the frame
// that ends a stream carries Stream false, with or without an error. A method
// that is not streaming answers with exactly one frame and never sets it.
type Response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	Stream bool            `json:"stream,omitempty"`
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

// ResourceType describes one kind on the wire. It carries what a Kubernetes
// discovery document carries, so that serving this to a Kubernetes client is a
// translation and not a reconstruction.
type ResourceType struct {
	Group      string   `json:"group"`
	Version    string   `json:"version"`
	Kind       string   `json:"kind"`
	Plural     string   `json:"plural"`
	Singular   string   `json:"singular"`
	ShortNames []string `json:"shortNames,omitempty"`
	// ListKind names a collection of this kind. It is sent resolved rather than
	// defaulted on arrival, so that both sides read the same name.
	ListKind string `json:"listKind"`
	// Namespaced says whether objects of this kind live in a namespace. Every
	// Kubernetes client branches on it.
	Namespaced bool `json:"namespaced"`
	// Categories are the collective names this kind answers to.
	Categories []string `json:"categories,omitempty"`
	// Verbs are what the kind serves, in Kubernetes' vocabulary. Sent resolved:
	// a kind that declared nothing sends what implementing Handler implies.
	Verbs       []string `json:"verbs,omitempty"`
	Description string   `json:"description,omitempty"`
	Columns     []Column `json:"columns,omitempty"`
	// Capabilities are what whoctl's own commands ask about, which in this
	// process would have been Go interfaces. Verbs is the same question asked
	// by a Kubernetes client, in words it knows.
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
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	CreationTimestamp core.Time         `json:"creationTimestamp,omitzero"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
}

// Ref names a kind on the wire, by the whole triple.
//
// The kind's name alone was enough for exactly as long as no provider served
// two kinds sharing one — and a provider covering several services of a cloud
// does that on its first day, with Instance under ec2 and Instance under rds.
// The old shape did not fail loudly there: the server keyed its handlers by
// name in a map and one quietly replaced the other.
type Ref struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// RefOf is the reference to a core type.
func RefOf(t core.ResourceType) Ref {
	return Ref{Group: t.Group, Version: t.Version, Kind: t.Kind}
}

// GVK is the core form of the same triple.
func (r Ref) GVK() core.GVK { return core.GVK{Group: r.Group, Version: r.Version, Kind: r.Kind} }

// String is "group/version, Kind=X", which is what an error names.
func (r Ref) String() string { return r.GVK().String() }

// Params for the per-kind methods.
//
// Namespace is spelled out on every one of them that can take it, for the same
// reason DeleteParams spells out cascade: it reaches a handler through the
// context, and a context does not cross a process boundary. Empty means every
// namespace, which is what `-A` asks for and what a kind that has no namespaces
// always sees.
type (
	// KindParams addresses a whole kind. It is also what watch takes.
	KindParams struct {
		Ref
		Namespace     string `json:"namespace,omitempty"`
		AllNamespaces bool   `json:"allNamespaces,omitempty"`
		LabelSelector string `json:"labelSelector,omitempty"`
		FieldSelector string `json:"fieldSelector,omitempty"`
	}
	// NameParams addresses one object.
	NameParams struct {
		Ref
		Namespace     string `json:"namespace,omitempty"`
		AllNamespaces bool   `json:"allNamespaces,omitempty"`
		LabelSelector string `json:"labelSelector,omitempty"`
		FieldSelector string `json:"fieldSelector,omitempty"`
		Name          string `json:"name"`
	}
	// ScopeParams addresses everything a scope covers.
	ScopeParams struct {
		Ref
		Namespace     string `json:"namespace,omitempty"`
		AllNamespaces bool   `json:"allNamespaces,omitempty"`
		LabelSelector string `json:"labelSelector,omitempty"`
		FieldSelector string `json:"fieldSelector,omitempty"`
		Scope         string `json:"scope"`
	}
	// ApplyParams carries the desired object, whose metadata carries its
	// namespace — an apply says where the object goes rather than being told.
	ApplyParams struct {
		Ref
		Object Object `json:"object"`
	}
	// DeleteParams addresses one object and carries the options for removing
	// it. Every field of core.DeleteOptions must appear here: they reach a
	// handler through the context, and a context does not cross a process
	// boundary — see the note on core.DeleteOptions.
	DeleteParams struct {
		Ref
		Namespace     string `json:"namespace,omitempty"`
		AllNamespaces bool   `json:"allNamespaces,omitempty"`
		LabelSelector string `json:"labelSelector,omitempty"`
		FieldSelector string `json:"fieldSelector,omitempty"`
		Name          string `json:"name"`
		// Cascade also removes what the object owns, such as a user's home
		// directory.
		Cascade bool `json:"cascade,omitempty"`
	}
	// StopWatchParams ends a watch, naming the id of the request that opened
	// it. It is the one params that addresses a call rather than a kind.
	StopWatchParams struct {
		Watch int `json:"watch"`
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
	// EventResult is one frame of a watch.
	EventResult struct {
		Type   string `json:"type"`
		Object Object `json:"object"`
	}
)
