// Package providertest is the conformance suite a provider runs against itself.
//
// The checks here used to live in whoctl's own test suite, where they ran over
// the two providers that happened to be compiled into it. Once providers are
// separate repositories that is the wrong place: whoctl cannot test a provider
// it has never seen, and every provider — including one somebody else writes —
// needs the same answers.
//
// A provider's suite is then one test:
//
//	func TestConformance(t *testing.T) {
//		providertest.Conformance(t, newProvider(), providertest.Options{SourceRoot: "."})
//	}
package providertest

import (
	"slices"
	"strings"
	"testing"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/docs"
)

// Options configures the suite.
type Options struct {
	// SourceRoot is the module root, so the documentation check can compare the
	// generated tables against the markdown in the repository. Empty checks
	// only what the binary carries.
	SourceRoot string
}

// Conformance runs every check a provider has to pass.
func Conformance(t *testing.T, p core.Provider, opts Options) {
	t.Helper()
	t.Run("naming", func(t *testing.T) { Naming(t, p) })
	t.Run("columns", func(t *testing.T) { Columns(t, p) })
	t.Run("capabilities", func(t *testing.T) { Capabilities(t, p) })
	t.Run("verbs", func(t *testing.T) { Verbs(t, p) })
	t.Run("documentation", func(t *testing.T) { Documentation(t, p, opts.SourceRoot) })
}

// Naming checks that every kind can be told apart from every other one, and
// that the command line can resolve what it is told.
//
// Two kinds may share a plural — Instance under ec2 and Instance under rds is
// the ordinary shape of a cloud provider, and the group is what separates them.
// What may not happen is two kinds under one group, version and kind: that is
// the same resource twice, and it used to be silent, with one handler replacing
// the other in a map.
//
// The second rule is what makes `aws/route53/hostedzones` resolvable. A command
// line reaching a resource in three segments has to decide whether the middle
// one names a group or a kind, and it decides by trying the kind first. If a
// name could be both, that choice is a coin toss — so it may not be both.
func Naming(t *testing.T, p core.Provider) {
	t.Helper()

	seen := map[core.GVK]bool{}
	labels := map[string]bool{}
	for _, h := range p.Handlers() {
		rt := h.Type()
		switch {
		case rt.Group == "":
			t.Errorf("%s has no group, so it cannot be told from a kind of the same name", rt.Kind)
		case rt.Version == "":
			t.Errorf("%s has no version", rt.Kind)
		}
		if gvk := rt.GVK(); seen[gvk] {
			t.Errorf("%s is served twice, and one handler would answer for the other", gvk)
		} else {
			seen[gvk] = true
		}
		for label := range strings.SplitSeq(strings.ToLower(rt.Group), ".") {
			labels[label] = true
		}
		if rt.CollectionKind() == rt.Kind {
			t.Errorf("%s: listKind is the same as the kind, so a collection cannot be told from one object", rt.Kind)
		}
	}

	for _, h := range p.Handlers() {
		rt := h.Type()
		names := append([]string{rt.Plural, rt.Singular, rt.Kind}, rt.ShortNames...)
		for _, name := range names {
			if name == "" {
				t.Errorf("%s does not name itself completely: plural, singular and kind are all required", rt.Kind)
				continue
			}
			if labels[strings.ToLower(name)] {
				t.Errorf("%s answers to %q, which is also a label of a group this provider serves: "+
					"`provider/%s/something` could not be resolved", rt.Kind, name, name)
			}
		}
	}
}

// Verbs checks that a kind publishes verbs a Kubernetes client can act on, and
// that the ones it publishes are real.
//
// A verb is a promise made in a discovery document: a client that reads `watch`
// will open a stream, and one that does not read `delete` will not offer to
// remove anything. Both are worth being right about before a client nobody in
// this repository wrote is pointed at it.
func Verbs(t *testing.T, p core.Provider) {
	t.Helper()
	for _, h := range p.Handlers() {
		rt := h.Type()
		verbs := core.VerbsOf(h)
		if len(verbs) == 0 {
			t.Errorf("%s publishes no verbs, so a client cannot do anything with it", rt.Kind)
		}
		for _, v := range verbs {
			if !slices.Contains(core.KnownVerbs, v) {
				t.Errorf("%s publishes verb %q, which is not one Kubernetes knows: %s",
					rt.Kind, v, strings.Join(core.KnownVerbs, ", "))
			}
		}
		if _, ok := h.(core.Watcher); slices.Contains(verbs, core.VerbWatch) != ok {
			t.Errorf("%s and core.Watcher disagree about whether it can be watched: "+
				"verbs say %v, the handler says %v", rt.Kind, slices.Contains(verbs, core.VerbWatch), ok)
		}
	}
}

// Columns checks that every table column names a field the kind really has.
//
// Columns are paths rather than closures — they have to cross a process
// boundary — so the compiler has no opinion about them, and a misspelled path
// renders a dash forever and looks exactly like a field nobody set. This is the
// check that replaces the one the compiler used to do for free.
func Columns(t *testing.T, p core.Provider) {
	t.Helper()
	for _, h := range p.Handlers() {
		rt := h.Type()
		if len(rt.Columns) == 0 {
			t.Errorf("%s declares no columns", rt.Kind)
			continue
		}
		spec := h.NewSpec()
		var status any
		if st, ok := h.(core.StatusTyper); ok {
			status = st.NewStatus()
		}
		for _, c := range rt.Columns {
			switch {
			case c.Path == "":
				t.Errorf("%s: column %s has no path", rt.Kind, c.Name)
			case !core.ValidPath(spec, status, c.Path):
				t.Errorf("%s: column %s reads %q, which is not a field of its spec or status",
					rt.Kind, c.Name, c.Path)
			}
			switch c.Format {
			case "", core.FormatBytes, core.FormatMinutes, core.FormatFirst, core.FormatAge:
			default:
				t.Errorf("%s: column %s asks for format %q, which the printer does not have",
					rt.Kind, c.Name, c.Format)
			}
		}
	}
}

// Capabilities checks what a kind publishes about itself.
//
// Every kind must hand out a status, or its observed fields go undocumented;
// and a capability has to be reachable, because whoctl decides what to offer
// from this list and calls through the matching interface.
func Capabilities(t *testing.T, p core.Provider) {
	t.Helper()
	for _, h := range p.Handlers() {
		kind := h.Type().Kind
		caps := core.CapabilitiesOf(h)

		if !contains(caps, core.CapabilityStatusSchema) {
			t.Errorf("%s has no statusSchema capability, so its status cannot be documented", kind)
		}
		for _, c := range caps {
			var ok bool
			switch c {
			case core.CapabilityDescribe:
				_, ok = h.(core.Describer)
			case core.CapabilityRestart:
				_, ok = h.(core.Restarter)
			case core.CapabilityScopedList:
				_, ok = h.(core.ScopedLister)
			case core.CapabilityStatusSchema:
				_, ok = h.(core.StatusTyper)
			case core.CapabilityWatch:
				_, ok = h.(core.Watcher)
			default:
				t.Errorf("%s claims capability %q, which whoctl does not know", kind, c)
				continue
			}
			if !ok {
				t.Errorf("%s publishes %q but does not implement it, so the verb would fail when reached", kind, c)
			}
		}
	}
}

// Documentation checks that the provider's pages are complete and current: a
// field with no doc tag, a kind with no page, or a page whose generated tables
// were never refreshed.
func Documentation(t *testing.T, p core.Provider, sourceRoot string) {
	t.Helper()
	site, err := docs.Build([]core.Provider{p}, docs.Options{})
	if err != nil {
		t.Fatalf("building the documentation: %v", err)
	}
	problems, err := docs.Check(site, sourceRoot)
	if err != nil {
		t.Fatalf("checking the documentation: %v", err)
	}
	for _, problem := range problems {
		t.Errorf("%s\n\trun `make docs` after changing a resource", problem)
	}
}

func contains(caps []core.Capability, want core.Capability) bool {
	return slices.Contains(caps, want)
}
