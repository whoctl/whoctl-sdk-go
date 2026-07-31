package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Generate rewrites the generated sections of every page a provider keeps in
// the repository, and scaffolds a page for any kind that has none. root is the
// directory the providers' SourceDir paths are relative to, normally the
// module root.
//
// It returns the files it changed. Providers that do not declare a SourceDir —
// anything shipped by somebody else, whose pages are embedded and nothing
// else — are left alone.
func Generate(site *Site, root string) ([]string, error) {
	var changed []string
	for _, p := range site.Providers {
		if p.SourceDir == "" {
			continue
		}
		for _, r := range p.Resources {
			file := filepath.Join(root, filepath.FromSlash(p.SourceDir), filepath.FromSlash(pageLayout(p.PageLayout)(r.Slug)))
			want, err := pageContent(file, r)
			if err != nil {
				return changed, err
			}
			got, err := os.ReadFile(file)
			if err == nil && string(got) == want {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return changed, err
			}
			if err := os.WriteFile(file, []byte(want), 0o644); err != nil {
				return changed, err
			}
			changed = append(changed, file)
		}
	}
	return changed, nil
}

// pageContent is what a page should look like on disk: the file as written,
// with its generated regions refreshed, or a scaffold when there is no file.
func pageContent(file string, r Resource) (string, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return inject(scaffold(r), r), nil
		}
		return "", err
	}
	// Markers are HTML comments, so injecting over the whole file — front
	// matter included — cannot disturb anything else in it.
	return inject(strings.ReplaceAll(string(raw), "\r\n", "\n"), r), nil
}

// scaffold is the page a new kind starts from: the sections whoever adds the
// resource is expected to fill in, and the markers that keep the rest current.
func scaffold(r Resource) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nsubcategory: %s\n---\n\n", r.Subcategory)
	fmt.Fprintf(&b, "# %s\n\n", r.Kind)
	if r.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", r.Description)
	}
	b.WriteString("## Example\n\n```yaml\n")
	fmt.Fprintf(&b, "apiVersion: %s\nkind: %s\nmetadata:\n  name: example\nspec:\n", r.APIVersion, r.Kind)
	for _, f := range r.Spec {
		if f.Example != "" {
			fmt.Fprintf(&b, "  %s: %s\n", f.Name, f.Example)
		}
	}
	b.WriteString("```\n\n")
	fmt.Fprintf(&b, "## Spec\n\n%s", markers(SectionSpec))
	fmt.Fprintf(&b, "## Status\n\n%s", markers(SectionStatus))
	fmt.Fprintf(&b, "## Columns\n\n%s", markers(SectionColumns))
	return b.String()
}

func markers(name string) string {
	return beginMarker(name) + "\n" + endMarker(name) + "\n\n"
}

// Problem is one thing wrong with the documentation.
type Problem struct {
	Provider string
	Resource string
	Message  string
}

func (p Problem) String() string {
	where := p.Provider
	if p.Resource != "" {
		where += "/" + p.Resource
	}
	return where + ": " + p.Message
}

// Check reports what is missing or stale, so that adding a field without
// documenting it fails the build instead of quietly shipping a blank cell.
// A root of "" skips the on-disk comparison and checks only what the binary
// itself carries.
func Check(site *Site, root string) ([]Problem, error) {
	var problems []Problem
	for _, p := range site.Providers {
		if !p.Overview.Exists() {
			problems = append(problems, Problem{Provider: p.Name, Message: "no overview page (index.md)"})
		}
		if p.Summary == "" {
			problems = append(problems, Problem{Provider: p.Name, Message: "provider docs declare no summary"})
		}
		for _, r := range p.Resources {
			problems = append(problems, checkResource(p, r, root)...)
		}
	}
	// Whether every page also *renders* is checked where the renderer lives, in
	// whoctl-docs: a provider must not need a site builder to know that its own
	// documentation is complete.
	return problems, nil
}

func checkResource(p Provider, r Resource, root string) []Problem {
	var problems []Problem
	report := func(format string, args ...any) {
		problems = append(problems, Problem{Provider: p.Name, Resource: r.Kind, Message: fmt.Sprintf(format, args...)})
	}

	if r.Description == "" {
		report("ResourceType.Description is empty")
	}
	if !r.Exists() {
		report("no page: scaffold %s/%s", p.SourceDir, pageLayout(p.PageLayout)(r.Slug))
	}
	for _, f := range r.Spec {
		if !f.Documented() {
			report("spec.%s has no doc tag", f.Name)
		}
	}
	for _, f := range r.Status {
		if !f.Documented() {
			report("status.%s has no doc tag", f.Name)
		}
	}
	if len(r.Status) == 0 {
		report("handler has no statusSchema capability (core.StatusTyper), so its status is undocumented")
	}

	if root == "" || p.SourceDir == "" {
		return problems
	}
	file := filepath.Join(root, filepath.FromSlash(p.SourceDir), filepath.FromSlash(pageLayout(p.PageLayout)(r.Slug)))
	want, err := pageContent(file, r)
	if err != nil {
		report("%v", err)
		return problems
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != want {
		report("generated sections are out of date: run `whoctl docs generate`")
	}
	return problems
}
