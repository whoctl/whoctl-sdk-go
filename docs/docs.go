// Package docs turns the registered providers into a documentation registry:
// one page per resource, one overview per provider, and a browse page that
// compiles all of them.
//
// Nothing here knows about any particular provider. A provider contributes its
// own pages through core.DocumentedProvider — the markdown lives next to the
// provider's code and is embedded into the binary — and its field tables are
// read off the doc tags of its spec and status structs. Registering a provider
// is therefore all it takes for it to appear in the site.
package docs

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/schema"
)

// Site is the whole registry.
//
// The json tags are the published bundle format, not a detail: renaming a Go
// field would otherwise change what every provider emits, silently. See
// BundleFormat.
type Site struct {
	Title     string     `json:"title"`
	Version   string     `json:"version"`
	Providers []Provider `json:"providers"`
}

// Provider is one provider's corner of the registry: an overview, a set of
// resource pages and any long-form guides.
type Provider struct {
	// Name is the provider's own name, used as the URL Segment.
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Summary     string   `json:"summary"`
	Maturity    string   `json:"maturity"`
	Categories  []string `json:"categories,omitempty"`
	// Groups lists the API groups the provider serves, as they appear in a
	// manifest's apiVersion.
	Groups []string `json:"groups,omitempty"`
	// SourceDir is where the pages live in the repository, empty when the
	// provider does not offer them for rewriting.
	SourceDir string `json:"sourceDir,omitempty"`
	// PageLayout is where this provider keeps a kind's page, given its
	// singular. It is not part of the bundle: a site renders pages, it does
	// not go looking for them on anybody's disk.
	PageLayout func(singular string) string `json:"-"`
	Overview   Page                         `json:"overview"`
	Guides     []Page                       `json:"guides,omitempty"`
	Resources  []Resource                   `json:"resources,omitempty"`
}

// Page is one markdown page of a provider.
type Page struct {
	Slug  string `json:"slug,omitempty"`
	Title string `json:"title,omitempty"`
	// Body is the markdown, front matter removed.
	Body string `json:"body,omitempty"`
	// Path is the page's location inside the provider's docs tree, as in
	// "resources/user.md". Empty when no such page was written.
	Path string `json:"path,omitempty"`

	frontMatter *frontMatter
}

// Exists reports whether the provider actually ships this page.
func (p Page) Exists() bool { return p.Path != "" }

// Resource is a documented kind: what the registry knows about it, plus the
// page the provider wrote for it.
type Resource struct {
	Page
	Kind        string         `json:"kind"`
	Singular    string         `json:"singular"`
	Plural      string         `json:"plural"`
	ShortNames  []string       `json:"shortNames,omitempty"`
	APIVersion  string         `json:"apiVersion"`
	Description string         `json:"description,omitempty"`
	Subcategory string         `json:"subcategory,omitempty"`
	Verbs       []string       `json:"verbs,omitempty"`
	Columns     []Column       `json:"columns,omitempty"`
	Spec        []schema.Field `json:"spec,omitempty"`
	Status      []schema.Field `json:"status,omitempty"`
}

// Column is a column of `whoctl get` output.
type Column struct {
	Name string `json:"name"`
	Wide bool   `json:"wide,omitempty"`
}

// Options tunes the generated site.
type Options struct {
	// Title is the site's name, "whoctl registry" when empty.
	Title string
	// Version is shown in the header, usually the whoctl version.
	Version string
}

// DefaultSubcategory groups the resources a provider did not file under any
// heading of its own.
const DefaultSubcategory = "Resources"

// verbs every handler serves, because core.Handler requires them.
var baseVerbs = []string{"get", "describe", "apply", "edit", "delete"}

// Build reads a set of providers and their embedded pages, and returns the site
// model the renderers work from.
//
// It takes providers rather than whoctl's registry because a registry is the
// CLI's index of what it has connected to, and this package has to work for one
// provider documenting itself as much as for a site documenting many.
func Build(providers []core.Provider, opts Options) (*Site, error) {
	site := &Site{Title: opts.Title, Version: opts.Version}
	if site.Title == "" {
		site.Title = "whoctl registry"
	}

	var errs []error
	for _, p := range providers {
		built, err := buildProvider(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("provider %s: %w", p.Name(), err))
			continue
		}
		site.Providers = append(site.Providers, built)
	}
	sort.Slice(site.Providers, func(i, j int) bool {
		return site.Providers[i].Name < site.Providers[j].Name
	})
	return site, errors.Join(errs...)
}

func buildProvider(p core.Provider) (Provider, error) {
	out := Provider{Name: p.Name(), DisplayName: p.Name()}

	var tree fs.FS
	if d, ok := p.(core.DocumentedProvider); ok {
		pd := d.Docs()
		if pd.DisplayName != "" {
			out.DisplayName = pd.DisplayName
		}
		out.Summary = pd.Summary
		out.Categories = pd.Categories
		out.Maturity = pd.Maturity
		out.SourceDir = pd.SourceDir
		out.PageLayout = pd.PagePath
		if pd.FS != nil {
			sub, err := subFS(pd.FS, pd.Dir)
			if err != nil {
				return out, err
			}
			tree = sub
		}
	}

	overview, err := readPage(tree, "index.md")
	if err != nil {
		return out, err
	}
	if overview.Title == "" {
		overview.Title = "Overview"
	}
	out.Overview = overview

	guides, err := readGuides(tree)
	if err != nil {
		return out, err
	}
	out.Guides = guides

	seenGroup := map[string]bool{}
	for _, h := range p.Handlers() {
		r, err := buildResource(tree, h, pageLayout(out.PageLayout))
		if err != nil {
			return out, err
		}
		if !seenGroup[h.Type().Group] {
			seenGroup[h.Type().Group] = true
			out.Groups = append(out.Groups, h.Type().Group)
		}
		out.Resources = append(out.Resources, r)
	}
	sort.SliceStable(out.Resources, func(i, j int) bool {
		if a, b := out.Resources[i].Subcategory, out.Resources[j].Subcategory; a != b {
			return a < b
		}
		return out.Resources[i].Kind < out.Resources[j].Kind
	})
	return out, nil
}

func buildResource(tree fs.FS, h core.Handler, pagePath func(string) string) (Resource, error) {
	t := h.Type()
	r := Resource{
		Kind:        t.Kind,
		Singular:    t.Singular,
		Plural:      t.Plural,
		ShortNames:  t.ShortNames,
		APIVersion:  t.APIVersion(),
		Description: t.Description,
		Subcategory: DefaultSubcategory,
		Verbs:       defaultVerbs(h),
		// Fields come from the handler, whichever way it has them: reflected
		// over a Go type in this process, or published by a provider that has
		// no Go type on this side at all.
		Spec:   core.SpecFieldsOf(h),
		Status: core.StatusFieldsOf(h),
	}
	for _, c := range t.Columns {
		r.Columns = append(r.Columns, Column{Name: c.Name, Wide: c.Wide})
	}

	page, err := readPage(tree, pagePath(t.Singular))
	if err != nil {
		return r, err
	}
	r.Page = page
	if r.Title == "" {
		r.Title = t.Kind
	}
	r.Slug = t.Singular
	if fm := page.frontMatter; fm != nil {
		if fm.Subcategory != "" {
			r.Subcategory = fm.Subcategory
		}
		if len(fm.Verbs) > 0 {
			r.Verbs = fm.Verbs
		}
		if fm.Description != "" {
			r.Description = fm.Description
		}
	}
	return r, nil
}

// defaultVerbs is what a handler's capabilities say it can do. A resource whose
// story is more subtle than that — Service, which has no create and refuses
// delete — narrows it in the page's front matter.
func defaultVerbs(h core.Handler) []string {
	verbs := append([]string(nil), baseVerbs...)
	if slices.Contains(core.CapabilitiesOf(h), core.CapabilityRestart) {
		verbs = append(verbs, "restart")
	}
	return verbs
}

func readGuides(tree fs.FS) ([]Page, error) {
	if tree == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(tree, "guides")
	if err != nil {
		return nil, nil // no guides directory is the normal case
	}
	var out []Page
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p, err := readPage(tree, path.Join("guides", e.Name()))
		if err != nil {
			return nil, err
		}
		p.Slug = strings.TrimSuffix(e.Name(), ".md")
		if p.Title == "" {
			p.Title = p.Slug
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// readPage loads one markdown file. A page that does not exist is not an
// error: the site still documents the resource from what the registry knows,
// and `whoctl docs check` is what turns the gap into a failure.
func readPage(tree fs.FS, name string) (Page, error) {
	if tree == nil {
		return Page{}, nil
	}
	raw, err := fs.ReadFile(tree, name)
	if err != nil {
		return Page{}, nil
	}
	body, fm, err := splitFrontMatter(string(raw))
	if err != nil {
		return Page{}, fmt.Errorf("%s: %w", name, err)
	}
	// The page keeps its own `# Title` heading so it reads as a document on
	// its own — on GitHub, or in an editor. The site renders the title from
	// the model instead, so the heading is lifted out here rather than shown
	// twice.
	title, body := splitTitle(body)
	p := Page{Path: name, Body: body, Title: title, frontMatter: fm}
	if fm != nil && fm.Title != "" {
		p.Title = fm.Title
	}
	return p, nil
}

// splitTitle lifts a leading level-one heading off a page.
func splitTitle(body string) (title, rest string) {
	trimmed := strings.TrimLeft(body, "\n")
	if !strings.HasPrefix(trimmed, "# ") {
		return "", body
	}
	line, rest, _ := strings.Cut(trimmed, "\n")
	return strings.TrimSpace(strings.TrimPrefix(line, "# ")), strings.TrimLeft(rest, "\n")
}

// frontMatter is the YAML header a page may carry. Everything in it is about
// how the page is filed, never about the schema: the schema comes from the
// struct tags.
type frontMatter struct {
	Title       string   `yaml:"title"`
	Subcategory string   `yaml:"subcategory"`
	Description string   `yaml:"description"`
	Verbs       []string `yaml:"verbs"`
}

func splitFrontMatter(raw string) (string, *frontMatter, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return raw, nil, nil
	}
	end := strings.Index(raw[4:], "\n---")
	if end < 0 {
		return raw, nil, nil
	}
	head := raw[4 : 4+end]
	// Step over the closing "\n---" and the rest of the line it sits on.
	rest := raw[4+end+len("\n---"):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		rest = ""
	}
	fm := &frontMatter{}
	if err := yaml.Unmarshal([]byte(head), fm); err != nil {
		return raw, nil, fmt.Errorf("front matter: %w", err)
	}
	return strings.TrimLeft(rest, "\n"), fm, nil
}

func subFS(f fs.FS, dir string) (fs.FS, error) {
	if dir == "" || dir == "." {
		return f, nil
	}
	return fs.Sub(f, dir)
}

// pageLayout falls back to the default when a provider declares none.
func pageLayout(declared func(string) string) func(string) string {
	if declared != nil {
		return declared
	}
	return ResourcePage
}
