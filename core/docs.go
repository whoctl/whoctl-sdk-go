package core

import "io/fs"

// This file is what a provider publishes about itself so `whoctl docs` can
// build a site without knowing anything about the provider.
//
// The field vocabulary — the doc, docExample and docFlags struct tags, and the
// reflection that reads them — lives in the schema package instead, outside
// internal/, because a provider in another repository has to be able to import
// it.

// ProviderDocs is what a provider publishes about itself so the documentation
// site can be built without the generator knowing anything about the provider.
//
// The pages travel with the provider package as an embedded filesystem, which
// is what keeps a provider self-contained: adding one to whoctl adds its pages
// to the registry, with no central index to edit by hand.
type ProviderDocs struct {
	// DisplayName is the human name on the browse page, as in "Linux".
	DisplayName string
	// Summary is the single line shown on the provider's card.
	Summary string
	// Categories place the provider among the browse filters, as in "System"
	// or "Cloud".
	Categories []string
	// Maturity is a badge on the card, as in "alpha" or "stable".
	Maturity string
	// FS holds the markdown tree, rooted at Dir:
	//
	//	index.md              the provider overview
	//	resources/<kind>.md   one page per kind, named after its singular
	//	guides/<name>.md      optional long-form pages
	FS fs.FS
	// Dir is the path of the tree inside FS. Empty means the root of FS.
	Dir string
	// PagePath says where a kind's page lives inside FS, given its singular.
	// Nil means the default layout — beside the handler, under resources/ —
	// which is what a provider with a flat set of kinds wants. A provider that
	// groups its kinds into families declares its own.
	PagePath func(singular string) string
	// SourceDir is where that same tree lives in the repository, relative to
	// the module root. It exists so `whoctl docs generate` can write the
	// generated sections back into the markdown. Providers shipped by
	// somebody else leave it empty and their pages are then read-only.
	SourceDir string
}

// DocumentedProvider is optional: providers implementing it contribute pages
// to `whoctl docs`. One that does not still appears in the site, described by
// what the registry already knows about its kinds.
type DocumentedProvider interface {
	Docs() ProviderDocs
}

// StatusTyper is optional: handlers implementing it hand out a zeroed status
// value, which is what lets the documentation describe the observed fields as
// well as the desired ones. There is no equivalent for the spec because
// NewSpec already exists — manifest decoding needs it.
type StatusTyper interface {
	NewStatus() any
}
