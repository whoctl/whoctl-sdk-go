package schema

import "testing"

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

func TestFieldsOf(t *testing.T) {
	fields := Of(&sampleSpec{})

	var names []string
	for _, f := range fields {
		names = append(names, f.Name)
	}
	want := []string{"name", "count", "tags", "secret", "silent", "nested", "nested.deep"}
	if len(names) != len(want) {
		t.Fatalf("fields = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("field %d = %q, want %q", i, names[i], n)
		}
	}

	byName := map[string]Field{}
	for _, f := range fields {
		byName[f.Name] = f
	}

	if f := byName["name"]; f.Optional || f.Type != "string" {
		t.Errorf("name: optional=%v type=%q, want required string", f.Optional, f.Type)
	}
	if f := byName["count"]; !f.Optional || f.Type != "integer" || f.Example != "3" {
		t.Errorf("count: %+v, want optional integer with example 3", f)
	}
	if f := byName["tags"]; f.Type != "list of string" {
		t.Errorf("tags type = %q, want list of string", f.Type)
	}
	if f := byName["secret"]; !f.HasFlag("writeOnly") || !f.HasFlag("createOnly") {
		t.Errorf("secret flags = %v, want writeOnly and createOnly", f.Flags)
	}
	if f := byName["silent"]; f.Documented() {
		t.Error("silent has no doc tag, so it must not count as documented")
	}
	if f := byName["nested"]; f.Type != "object" {
		t.Errorf("nested type = %q, want object", f.Type)
	}
}

func TestFieldsOfIgnoresNonStructs(t *testing.T) {
	if got := Of(nil); got != nil {
		t.Errorf("Of(nil) = %v, want nil", got)
	}
	s := "not a struct"
	if got := Of(&s); got != nil {
		t.Errorf("Of(*string) = %v, want nil", got)
	}
}
