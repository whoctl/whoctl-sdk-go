package docs

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// A provider that exists only here, so the tests measure the docs machinery
// rather than whatever the linux provider happens to document today.
type fakeProvider struct {
	files fstest.MapFS
	spec  any
}

func (f *fakeProvider) Name() string { return "demo" }

func (f *fakeProvider) Handlers() []core.Handler {
	return []core.Handler{&fakeHandler{spec: f.spec}}
}

func (f *fakeProvider) Docs() core.ProviderDocs {
	return core.ProviderDocs{
		DisplayName: "Demo",
		Summary:     "A provider for tests.",
		Categories:  []string{"Test"},
		Maturity:    "alpha",
		FS:          f.files,
	}
}

type fakeStatus struct {
	Size int `yaml:"size" doc:"How big it turned out."`
}

type fakeHandler struct{ spec any }

func (h *fakeHandler) Type() core.ResourceType {
	return core.ResourceType{
		Group:       "demo.whoctl.io",
		Version:     "v1",
		Kind:        "Widget",
		Plural:      "widgets",
		Singular:    "widget",
		ShortNames:  []string{"wg"},
		Description: "A widget.",
		Columns: []core.Column{
			{Name: "NAME"},
			{Name: "SIZE", Wide: true},
		},
	}
}

func (h *fakeHandler) NewSpec() any {
	if h.spec != nil {
		return h.spec
	}
	return &sampleSpec{}
}
func (h *fakeHandler) NewStatus() any                              { return &fakeStatus{} }
func (h *fakeHandler) List(context.Context) ([]core.Object, error) { return nil, nil }
func (h *fakeHandler) Get(context.Context, string) (core.Object, error) {
	return core.Object{}, nil
}
func (h *fakeHandler) Apply(context.Context, core.Object) (core.Result, error) {
	return core.Result{}, nil
}
func (h *fakeHandler) Delete(context.Context, string) error { return nil }

const widgetPage = `---
subcategory: Things
verbs: [get, apply]
---

# Widget

A widget of one's own.

## Spec

<!-- whoctl:begin spec -->
stale content that must be replaced
<!-- whoctl:end spec -->
`

func buildTestSite(t *testing.T, files fstest.MapFS, spec any) *Site {
	t.Helper()
	providers := []core.Provider{&fakeProvider{files: files, spec: spec}}
	site, err := Build(providers, Options{Version: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return site
}

func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"index.md":                   {Data: []byte("# Demo provider\n\nWhat it does.\n")},
		"resources/widget/widget.md": {Data: []byte(widgetPage)},
		"guides/getting-up.md":       {Data: []byte("---\ntitle: Getting up\n---\n\n# Getting up\n\nStart here.\n")},
	}
}

func TestBuildReadsProviderPages(t *testing.T) {
	site := buildTestSite(t, testFiles(), nil)

	if len(site.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(site.Providers))
	}
	p := site.Providers[0]
	if p.DisplayName != "Demo" || p.Summary == "" {
		t.Errorf("provider metadata not carried over: %+v", p)
	}
	if len(p.Groups) != 1 || p.Groups[0] != "demo.whoctl.io" {
		t.Errorf("groups = %v", p.Groups)
	}
	if !p.Overview.Exists() || strings.Contains(p.Overview.Body, "# Demo provider") {
		t.Errorf("overview should exist with its title lifted off: %q", p.Overview.Body)
	}
	if len(p.Guides) != 1 || p.Guides[0].Title != "Getting up" {
		t.Errorf("guides = %+v", p.Guides)
	}

	if len(p.Resources) != 1 {
		t.Fatalf("resources = %d, want 1", len(p.Resources))
	}
	r := p.Resources[0]
	if r.Subcategory != "Things" {
		t.Errorf("subcategory = %q, want the one from the front matter", r.Subcategory)
	}
	if strings.Join(r.Verbs, ",") != "get,apply" {
		t.Errorf("verbs = %v, want the front matter to win over the interfaces", r.Verbs)
	}
	if len(r.Spec) == 0 || len(r.Status) == 0 {
		t.Errorf("spec and status should both be described: %d and %d fields", len(r.Spec), len(r.Status))
	}
	if r.APIVersion != "demo.whoctl.io/v1" {
		t.Errorf("apiVersion = %q", r.APIVersion)
	}
}

func TestInjectReplacesAndIsIdempotent(t *testing.T) {
	site := buildTestSite(t, testFiles(), nil)
	r := site.Providers[0].Resources[0]

	once := inject(widgetPage, r)
	if strings.Contains(once, "stale content") {
		t.Errorf("the old section survived:\n%s", once)
	}
	if !strings.Contains(once, "| `count` | integer | optional |") {
		t.Errorf("the spec table was not written:\n%s", once)
	}
	if !strings.Contains(once, "# Widget") {
		t.Error("prose outside the markers must be left alone")
	}
	if twice := inject(once, r); twice != once {
		t.Errorf("inject is not idempotent:\n%s", twice)
	}
}

func TestInjectClosesAnUnfinishedMarker(t *testing.T) {
	site := buildTestSite(t, testFiles(), nil)
	r := site.Providers[0].Resources[0]

	got := inject("## Status\n\n"+beginMarker(SectionStatus)+"\n", r)
	if !strings.Contains(got, endMarker(SectionStatus)) {
		t.Errorf("an unclosed marker should be closed:\n%s", got)
	}
	if !strings.Contains(got, "| `size` | integer |") {
		t.Errorf("status table missing:\n%s", got)
	}
}

func TestCheckReportsGaps(t *testing.T) {
	// A spec whose field carries no doc tag, and no page for the kind.
	type bareSpec struct {
		Undescribed string `yaml:"undescribed"`
	}
	site := buildTestSite(t, fstest.MapFS{}, &bareSpec{})

	problems, err := Check(site, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	var joined []string
	for _, p := range problems {
		joined = append(joined, p.String())
	}
	all := strings.Join(joined, "\n")

	for _, want := range []string{
		"spec.undescribed has no doc tag",
		"no page",
		"no overview page",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("Check did not report %q, got:\n%s", want, all)
		}
	}
}

func TestCheckIsQuietWhenComplete(t *testing.T) {
	site := buildTestSite(t, testFiles(), nil)
	problems, err := Check(site, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, p := range problems {
		// The sample spec deliberately holds one undocumented field; anything
		// else is a real complaint.
		if !strings.Contains(p.Message, "silent") {
			t.Errorf("unexpected problem: %s", p)
		}
	}
}

func TestSplitFrontMatter(t *testing.T) {
	body, fm, err := splitFrontMatter("---\ntitle: T\nverbs: [get]\n---\n\nBody text\n")
	if err != nil {
		t.Fatalf("splitFrontMatter: %v", err)
	}
	if fm == nil || fm.Title != "T" || len(fm.Verbs) != 1 {
		t.Fatalf("front matter = %+v", fm)
	}
	if body != "Body text\n" {
		t.Errorf("body = %q, the closing delimiter must be consumed", body)
	}

	if body, fm, _ := splitFrontMatter("no front matter\n"); fm != nil || body != "no front matter\n" {
		t.Errorf("a page without front matter must pass through unchanged: %q", body)
	}
}

// The spec the field-table tests are written against. It lived beside the
// reflection until that moved to whoctl/schema; the assertions here are about
// rendering, so the fixture follows them rather than the extractor.
type sampleNested struct {
	Deep string `yaml:"deep" doc:"A nested field."`
}

type sampleSpec struct {
	Name    string       `yaml:"name" doc:"The name." docFlags:"required"`
	Count   *int         `yaml:"count,omitempty" doc:"How many." docExample:"3"`
	Tags    []string     `yaml:"tags,omitempty" doc:"Some tags."`
	Secret  string       `yaml:"secret,omitempty" doc:"Write-only." docFlags:"writeOnly,createOnly"`
	Silent  bool         `yaml:"silent,omitempty"`
	Nested  sampleNested `yaml:"nested,omitempty" doc:"An object."`
	Skipped string       `yaml:"-"`
	hidden  string
}
