package core

import (
	"reflect"
	"strings"
)

// Lookup resolves a dotted path against an object and returns the value it
// names, or nil when the path leads nowhere.
//
// # Why paths and not closures
//
// A column used to carry a func(Object) string, which is the obvious thing to
// write and the one thing that cannot cross a process boundary. A provider is
// another process, so what a column shows has to be data it can send: the path
// of a field, plus a format.
//
// # The path is a manifest path
//
// Segments are matched against the yaml tag, not the Go field name, so the path
// of a value is spelled exactly as the user sees it in `-o yaml` and writes it
// in a manifest: "status.uid", "metadata.name", "spec.passwordHash". There is
// one name for a field and it is the same everywhere.
//
// Two forms beyond a plain walk:
//
//   - "a|b|c" tries each path in order and takes the first non-empty one, which
//     is how a dnf repository shows a baseurl, or a metalink when it has no
//     baseurl, or a mirrorlist when it has neither.
//   - a segment resolving to a nil pointer stops the walk and yields nil rather
//     than panicking, because an optional field is normal.
//
// Structs and map[string]any both resolve, so the same path works against the
// typed values a handler returns to the provider serving it and against the
// decoded JSON that arrives at the CLI.
func Lookup(o Object, path string) any {
	for alternative := range strings.SplitSeq(path, "|") {
		v, ok := Resolve(o, strings.TrimSpace(alternative))
		if ok && !isEmptyValue(v) {
			return v
		}
	}
	return nil
}

// Resolve walks a single path — no alternatives — and reports whether it names
// anything at all.
//
// The distinction matters because an empty value and a misspelled path both
// print as a dash. Resolve against a zeroed object is what lets a test say
// "this column names a field that does not exist", which is the only way a
// typo in a path gets caught: nothing else about it is checked by the compiler
// once columns stopped being code.
func Resolve(o Object, path string) (any, bool) {
	var current any = o
	for segment := range strings.SplitSeq(path, ".") {
		if segment == "" {
			return nil, false
		}
		v, ok := field(current, segment)
		if !ok {
			return nil, false
		}
		current = v
	}
	return current, true
}

// Fielder is implemented by values that resolve their own members by name.
//
// It exists for the ordered map a provider's spec and status arrive as once the
// provider is another process: the order of a manifest's fields is part of what
// whoctl prints, so they cannot be decoded into a plain map, and a type that
// keeps the order answers here instead of being reflected over.
type Fielder interface {
	Field(name string) (any, bool)
}

// field reads one named member of a struct, a map, or anything that resolves
// its own members.
func field(v any, name string) (any, bool) {
	if f, ok := v.(Fielder); ok {
		return f.Field(name)
	}
	rv := deref(reflect.ValueOf(v))
	if !rv.IsValid() {
		return nil, false
	}
	switch rv.Kind() {
	case reflect.Map:
		// The decoded-JSON case: keys are already the yaml names.
		value := rv.MapIndex(reflect.ValueOf(name))
		if !value.IsValid() {
			return nil, false
		}
		return value.Interface(), true
	case reflect.Struct:
		t := rv.Type()
		for i := range t.NumField() {
			sf := t.Field(i)
			if sf.IsExported() && yamlName(sf) == name {
				return rv.Field(i).Interface(), true
			}
		}
	}
	return nil, false
}

// ValidPath reports whether every alternative in path names a field that a kind
// with these spec and status types actually has. spec and status are the values
// Handler.NewSpec and StatusTyper.NewStatus return; either may be nil.
//
// This walks types rather than values, so an unset optional field is still a
// field: resolving "spec.uid" against a zeroed spec would stop at a nil pointer
// and say nothing is there, which is true of the value and false of the schema.
func ValidPath(spec, status any, path string) bool {
	for alternative := range strings.SplitSeq(path, "|") {
		if !validOne(spec, status, strings.TrimSpace(alternative)) {
			return false
		}
	}
	return true
}

func validOne(spec, status any, path string) bool {
	segments := strings.Split(path, ".")
	if len(segments) == 0 || segments[0] == "" {
		return false
	}

	var t reflect.Type
	switch segments[0] {
	case "apiVersion", "kind":
		return len(segments) == 1
	case "metadata":
		t = reflect.TypeFor[Metadata]()
	case "spec":
		t = derefType(reflect.TypeOf(spec))
	case "status":
		t = derefType(reflect.TypeOf(status))
	default:
		return false
	}
	if len(segments) == 1 {
		return true
	}
	if t == nil {
		// A kind that hands out no status cannot have its status paths checked.
		// Reporting them valid is the honest answer: nothing here disproves them.
		return true
	}

	for _, segment := range segments[1:] {
		if t == nil || t.Kind() != reflect.Struct {
			return t != nil && t.Kind() == reflect.Map
		}
		field, ok := fieldType(t, segment)
		if !ok {
			return false
		}
		t = derefType(field)
	}
	return true
}

func fieldType(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.IsExported() && yamlName(sf) == name {
			return sf.Type, true
		}
	}
	return nil, false
}

func derefType(t reflect.Type) reflect.Type {
	for t != nil && (t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface) {
		if t.Kind() == reflect.Interface {
			return nil
		}
		t = t.Elem()
	}
	return t
}

// yamlName is the field's name in a manifest, which is what a path spells.
func yamlName(sf reflect.StructField) string {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		return sf.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return sf.Name
	}
	return name
}

// deref follows pointers and interfaces. A nil anywhere along the way ends the
// walk, which is what makes an unset optional field resolve to nothing instead
// of panicking.
func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

// isEmptyValue decides what "|" skips over. Only absence counts: a zero number
// and a false boolean are answers, and a column showing "0" or "false" must not
// fall through to the next alternative.
func isEmptyValue(v any) bool {
	rv := deref(reflect.ValueOf(v))
	if !rv.IsValid() {
		return true
	}
	switch rv.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	}
	return false
}
