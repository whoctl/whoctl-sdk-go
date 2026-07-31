// Package schema turns a provider's spec and status structs into the field
// records that describe them.
//
// It lives outside internal/ because it is what a provider imports: the tags
// below are the vocabulary a provider author writes, and this reflection is
// what turns them into the schema the provider publishes.
//
// One schema does two jobs. It generates the documentation tables, and it is
// what a manifest is validated against, because the CLI does not have the Go
// types to decode into — the provider that owns them is another process.
//
//		UID *int `yaml:"uid,omitempty" json:"uid,omitempty" doc:"Numeric user ID. Allocated by the system when omitted." docExample:"4200"`
//
//	  - doc         the description, one or two sentences.
//	  - docExample  a value worth showing in the field table.
//	  - docFlags    comma-separated markers, see the Flag constants.
//
// A field with no doc tag is reported by `whoctl docs check`, which is how the
// documentation stays complete as resources grow.
package schema

import (
	"reflect"
	"slices"
	"strings"
)

// Struct tags this package reads.
const (
	TagDoc     = "doc"
	TagExample = "docExample"
	TagFlags   = "docFlags"
)

// Values accepted in docFlags: semantics the Go type cannot express on its own.
const (
	// FlagRequired marks a field a manifest must set.
	FlagRequired = "required"
	// FlagCreateOnly marks a field that only has an effect when the object is
	// created, and is ignored when reconciling an existing one.
	FlagCreateOnly = "createOnly"
	// FlagWriteOnly marks a field that can be applied but is never read back,
	// so an exported manifest cannot leak it.
	FlagWriteOnly = "writeOnly"
	// FlagImmutable marks a field that cannot be changed after creation.
	FlagImmutable = "immutable"
)

// Field is one documented field of a spec or a status, as read off the struct
// tags. The generator never invents a description: what is not tagged shows up
// empty and `whoctl docs check` complains about it.
type Field struct {
	// Name is the key as it appears in a manifest, taken from the yaml tag.
	Name string
	// Type is the human spelling of the Go type: "string", "integer",
	// "list of string".
	Type string
	// Optional is true for pointers and for anything tagged omitempty, unless
	// docFlags says the field is required.
	Optional bool
	// Flags are the docFlags markers, in the order they were written.
	Flags []string
	// Example is the docExample tag, empty when there is none.
	Example string
	// Doc is the doc tag.
	Doc string
}

// Documented reports whether the field carries a description.
func (f Field) Documented() bool { return strings.TrimSpace(f.Doc) != "" }

// HasFlag reports whether the field is marked with one of the Flag constants.
func (f Field) HasFlag(flag string) bool { return slices.Contains(f.Flags, flag) }

// Of reflects over a spec or status value — anything Handler.NewSpec or
// StatusTyper.NewStatus returns — and describes its fields in declaration
// order, which is also the order they are written in a manifest.
func Of(v any) []Field {
	if v == nil {
		return nil
	}
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	return structFields(t, "")
}

// structFields walks one struct level. prefix is non-empty for nested objects,
// so a field of a nested struct is documented as "parent.child" rather than
// disappearing into an opaque "object".
func structFields(t reflect.Type, prefix string) []Field {
	var out []Field
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, omitempty := yamlName(sf)
		if name == "-" {
			continue
		}
		// An embedded struct with no name of its own contributes its fields
		// directly, exactly as it does in the YAML.
		if sf.Anonymous && name == "" {
			if inner := deref(sf.Type); inner.Kind() == reflect.Struct {
				out = append(out, structFields(inner, prefix)...)
				continue
			}
		}
		if name == "" {
			name = strings.ToLower(sf.Name[:1]) + sf.Name[1:]
		}

		f := Field{
			Name:     prefix + name,
			Type:     typeName(sf.Type),
			Optional: omitempty || sf.Type.Kind() == reflect.Pointer,
			Doc:      sf.Tag.Get(TagDoc),
			Example:  sf.Tag.Get(TagExample),
			Flags:    splitFlags(sf.Tag.Get(TagFlags)),
		}
		if f.HasFlag(FlagRequired) {
			f.Optional = false
		}
		out = append(out, f)

		// Nested objects are flattened; lists of objects are left as they are,
		// because a list has no single key path to document.
		if inner := deref(sf.Type); inner.Kind() == reflect.Struct && !isOpaque(inner) {
			out = append(out, structFields(inner, f.Name+".")...)
		}
	}
	return out
}

// yamlName returns the manifest key of a field and whether it is omitempty.
func yamlName(sf reflect.StructField) (name string, omitempty bool) {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		tag = sf.Tag.Get("json")
	}
	parts := strings.Split(tag, ",")
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return parts[0], omitempty
}

func splitFlags(tag string) []string {
	if strings.TrimSpace(tag) == "" {
		return nil
	}
	var out []string
	for f := range strings.SplitSeq(tag, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// isOpaque marks struct types that are values rather than objects, and so are
// documented as a single field instead of being walked into.
func isOpaque(t reflect.Type) bool {
	return t.PkgPath() == "time" && t.Name() == "Time"
}

// typeName spells a Go type the way a manifest author thinks of it.
func typeName(t reflect.Type) string {
	t = deref(t)
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "list of " + typeName(t.Elem())
	case reflect.Map:
		return "map of " + typeName(t.Elem())
	case reflect.Struct:
		if isOpaque(t) {
			return "string"
		}
		return "object"
	default:
		return t.Kind().String()
	}
}
