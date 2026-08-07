package core

import "slices"

// The verbs a kind may serve. The vocabulary is Kubernetes', not whoctl's,
// because this is the list that goes into a discovery document: a client asking
// what it may do with a resource has to read words it already knows.
//
// # Why this is not the Capability list
//
// A Capability is what whoctl's own commands ask about — whether a kind
// describes itself, whether it can be restarted. Those have no meaning to a
// Kubernetes client: there is no restart verb there, a restart is a patch on an
// annotation, and describe is something the client builds for itself. Two
// audiences, two vocabularies, and folding them into one would mean publishing
// words to clients that cannot act on them.
const (
	VerbGet    = "get"
	VerbList   = "list"
	VerbWatch  = "watch"
	VerbCreate = "create"
	VerbUpdate = "update"
	VerbPatch  = "patch"
	VerbDelete = "delete"
)

// KnownVerbs is the closed set a kind may publish.
var KnownVerbs = []string{VerbGet, VerbList, VerbWatch, VerbCreate, VerbUpdate, VerbPatch, VerbDelete}

// baseVerbs are what implementing Handler already means: List and Get read, and
// Apply is an upsert, which is create and update at once.
var baseVerbs = []string{VerbGet, VerbList, VerbCreate, VerbUpdate, VerbDelete}

// VerbsOf reports what a kind serves, which is what it declared or — the usual
// case — what implementing Handler already implies, plus watch when the handler
// can stream.
//
// Declaring is how a kind says it serves *less*. A kind that reads a system it
// cannot write refuses apply and delete at runtime today, which means a client
// only learns after asking; declaring `get, list` says it up front and is the
// difference between a greyed-out button and an error dialog.
func VerbsOf(h Handler) []string {
	if declared := h.Type().Verbs; len(declared) > 0 {
		return slices.Clone(declared)
	}
	verbs := slices.Clone(baseVerbs)
	if _, ok := h.(Watcher); ok {
		verbs = append(verbs, VerbWatch)
	}
	return verbs
}
