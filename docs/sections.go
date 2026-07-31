package docs

import (
	"fmt"
	"strings"

	"github.com/whoctl/whoctl-sdk-go/schema"
)

// Generated sections are the parts of a page that come from the code rather
// than from whoever wrote the page: the field tables, the resource's identity,
// the columns `get` prints. They are delimited in the markdown source by HTML
// comments, so the file stays readable — and correct — on GitHub, while
// `whoctl docs generate` keeps the content between the markers in step with
// the struct tags.
//
//	<!-- whoctl:begin spec -->
//	...regenerated, do not edit...
//	<!-- whoctl:end spec -->
const (
	sectionMeta    = "meta"
	SectionSpec    = "spec"
	SectionStatus  = "status"
	SectionColumns = "columns"
)

var sectionNames = []string{sectionMeta, SectionSpec, SectionStatus, SectionColumns}

func beginMarker(name string) string { return "<!-- whoctl:begin " + name + " -->" }
func endMarker(name string) string   { return "<!-- whoctl:end " + name + " -->" }

// Segment is a piece of a page: either prose written by hand or a placeholder
// for a generated section.
type Segment struct {
	Markdown string
	Section  string
}

// SplitSegments cuts a page at its section markers, dropping whatever sits
// between them: the content is regenerated from the resource every time, so
// keeping the old text would only give it a chance to disagree with the code.
func SplitSegments(body string) []Segment {
	var out []Segment
	rest := body
	for {
		start, name := nextMarker(rest)
		if start < 0 {
			if strings.TrimSpace(rest) != "" {
				out = append(out, Segment{Markdown: rest})
			}
			return out
		}
		if prose := rest[:start]; strings.TrimSpace(prose) != "" {
			out = append(out, Segment{Markdown: prose})
		}
		out = append(out, Segment{Section: name})

		after := rest[start+len(beginMarker(name)):]
		if end := strings.Index(after, endMarker(name)); end >= 0 {
			rest = after[end+len(endMarker(name)):]
		} else {
			rest = after
		}
	}
}

// nextMarker finds the earliest opening marker in s.
func nextMarker(s string) (int, string) {
	best, bestName := -1, ""
	for _, name := range sectionNames {
		if i := strings.Index(s, beginMarker(name)); i >= 0 && (best < 0 || i < best) {
			best, bestName = i, name
		}
	}
	return best, bestName
}

// inject rewrites the generated regions of a page in place, leaving the prose
// around them untouched. An opening marker with no closing one gets closed,
// so a page started by hand ends up well formed.
func inject(body string, r Resource) string {
	var b strings.Builder
	rest := body
	for {
		start, name := nextMarker(rest)
		if start < 0 {
			b.WriteString(rest)
			return b.String()
		}
		begin, end := beginMarker(name), endMarker(name)
		b.WriteString(rest[:start])
		b.WriteString(begin)
		b.WriteString("\n")
		b.WriteString(SectionMarkdown(name, r))
		b.WriteString(end)

		rest = rest[start+len(begin):]
		if stop := strings.Index(rest, end); stop >= 0 {
			rest = rest[stop+len(end):]
		}
	}
}

// SectionMarkdown renders one generated section as markdown, ready to be
// dropped between its markers.
func SectionMarkdown(name string, r Resource) string {
	switch name {
	case sectionMeta:
		return metaTable(r)
	case SectionSpec:
		return fieldTable(r.Spec, true)
	case SectionStatus:
		return fieldTable(r.Status, false)
	case SectionColumns:
		return columnTable(r)
	default:
		return ""
	}
}

func metaTable(r Resource) string {
	var b strings.Builder
	b.WriteString("| | |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| **apiVersion** | `%s` |\n", r.APIVersion)
	fmt.Fprintf(&b, "| **kind** | `%s` |\n", r.Kind)
	fmt.Fprintf(&b, "| **Names** | %s |\n", codeList(append([]string{r.Plural, r.Singular}, r.ShortNames...)))
	fmt.Fprintf(&b, "| **Verbs** | %s |\n", codeList(r.Verbs))
	return b.String()
}

func columnTable(r Resource) string {
	if len(r.Columns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| Column | Shown |\n| --- | --- |\n")
	for _, c := range r.Columns {
		shown := "always"
		if c.Wide {
			shown = "`-o wide`"
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", c.Name, shown)
	}
	return b.String()
}

// fieldTable renders a spec or a status. The spec carries a notes column,
// because whether a field is optional and when it takes effect is the first
// thing somebody writing a manifest needs; a status is read-only throughout,
// so the column would say nothing.
func fieldTable(fields []schema.Field, spec bool) string {
	if len(fields) == 0 {
		return "_None._\n"
	}
	var b strings.Builder
	if spec {
		b.WriteString("| Field | Type | Notes | Description |\n| --- | --- | --- | --- |\n")
	} else {
		b.WriteString("| Field | Type | Description |\n| --- | --- | --- |\n")
	}
	for _, f := range fields {
		doc := escapePipes(strings.TrimSpace(f.Doc))
		if doc == "" {
			doc = "_undocumented_"
		}
		if f.Example != "" {
			doc += fmt.Sprintf(" Example: `%s`.", escapePipes(f.Example))
		}
		if spec {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", f.Name, f.Type, strings.Join(fieldNotes(f), ", "), doc)
		} else {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", f.Name, f.Type, doc)
		}
	}
	return b.String()
}

// fieldNotes spells the field's markers the way a reader thinks of them.
//
// These are presentation, so they stay here rather than travelling to the
// schema package with the Field: how a marker is worded belongs to the site
// that renders it, not to the provider that declared it.
func fieldNotes(f schema.Field) []string {
	first := "optional"
	if !f.Optional {
		first = "**required**"
	}
	return append([]string{first}, FlagLabels(f)...)
}

// FlagLabels turns docFlags into words. required is left out: it is already
// said by the optional/required note in front of them.
func FlagLabels(f schema.Field) []string {
	var out []string
	for _, flag := range f.Flags {
		switch flag {
		case schema.FlagRequired:
		case schema.FlagCreateOnly:
			out = append(out, "create-only")
		case schema.FlagWriteOnly:
			out = append(out, "write-only")
		case schema.FlagImmutable:
			out = append(out, "immutable")
		default:
			out = append(out, flag)
		}
	}
	return out
}

func codeList(items []string) string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s != "" {
			out = append(out, "`"+s+"`")
		}
	}
	return strings.Join(out, ", ")
}

func escapePipes(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
