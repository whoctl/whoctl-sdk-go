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
	t.Run("columns", func(t *testing.T) { Columns(t, p) })
	t.Run("capabilities", func(t *testing.T) { Capabilities(t, p) })
	t.Run("documentation", func(t *testing.T) { Documentation(t, p, opts.SourceRoot) })
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
			case "", core.FormatBytes, core.FormatMinutes, core.FormatFirst:
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
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
