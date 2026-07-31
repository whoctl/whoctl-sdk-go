package core

import "testing"

type testStatus struct {
	UID        int      `yaml:"uid"`
	SteamID64  string   `yaml:"steamId64"`
	Groups     []string `yaml:"groups,omitempty"`
	Locked     bool     `yaml:"locked"`
	Priority   *int     `yaml:"priority,omitempty"`
	BaseURL    []string `yaml:"baseurl,omitempty"`
	Metalink   string   `yaml:"metalink,omitempty"`
	Mirrorlist string   `yaml:"mirrorlist,omitempty"`
	Untagged   string
}

type testSpec struct {
	Shell string `yaml:"shell,omitempty"`
}

func object(status *testStatus) Object {
	return Object{
		Metadata: Metadata{Name: "alice"},
		Spec:     &testSpec{Shell: "/bin/sh"},
		Status:   status,
	}
}

func TestLookupReadsByManifestName(t *testing.T) {
	o := object(&testStatus{UID: 1000, SteamID64: "7656119", Untagged: "x"})
	for path, want := range map[string]any{
		"metadata.name":    "alice",
		"spec.shell":       "/bin/sh",
		"status.uid":       1000,
		"status.steamId64": "7656119",
		// A field with no yaml tag falls back to its Go name, which is the only
		// answer available and keeps a missing tag from silently reading nothing.
		"status.Untagged": "x",
	} {
		if got := Lookup(o, path); got != want {
			t.Errorf("Lookup(%q) = %v, want %v", path, got, want)
		}
	}
}

// The path is spelled as the manifest spells it. Anything else — the Go field
// name where a yaml tag exists, the json name, a different case — is not a path.
func TestLookupDoesNotAcceptTheGoFieldName(t *testing.T) {
	o := object(&testStatus{SteamID64: "7656119"})
	for _, path := range []string{"status.SteamID64", "status.steamid64", "status.nosuchfield", "spec.uid", ""} {
		if got := Lookup(o, path); got != nil {
			t.Errorf("Lookup(%q) = %v, want nil", path, got)
		}
	}
}

// The alternative syntax exists for the dnf repository URL, which is a baseurl,
// or a metalink, or a mirrorlist, in that order.
func TestLookupTakesTheFirstAlternativeThatHasSomething(t *testing.T) {
	const path = "status.baseurl|status.metalink|status.mirrorlist"

	got := Lookup(object(&testStatus{BaseURL: []string{"https://a"}, Metalink: "https://m"}), path)
	if v, ok := got.([]string); !ok || len(v) != 1 || v[0] != "https://a" {
		t.Errorf("with a baseurl, got %v, want the baseurl", got)
	}
	if got := Lookup(object(&testStatus{Metalink: "https://m"}), path); got != "https://m" {
		t.Errorf("with no baseurl, got %v, want the metalink", got)
	}
	if got := Lookup(object(&testStatus{Mirrorlist: "https://l"}), path); got != "https://l" {
		t.Errorf("with neither, got %v, want the mirrorlist", got)
	}
	if got := Lookup(object(&testStatus{}), path); got != nil {
		t.Errorf("with none of the three, got %v, want nil", got)
	}
}

// A false boolean and a zero number are answers, not absences: a column reading
// "locked" must print false rather than falling through to the next alternative.
func TestLookupDoesNotTreatZeroAsAbsent(t *testing.T) {
	o := object(&testStatus{UID: 0, Locked: false})
	if got := Lookup(o, "status.locked|metadata.name"); got != false {
		t.Errorf("got %v, want false rather than the fallback", got)
	}
	if got := Lookup(o, "status.uid|metadata.name"); got != 0 {
		t.Errorf("got %v, want 0 rather than the fallback", got)
	}
}

// An unset optional field is normal, and reading one must not panic.
func TestLookupSurvivesNilAlongTheWay(t *testing.T) {
	if got := Lookup(object(&testStatus{}), "status.priority"); got != nil {
		t.Errorf("unset pointer = %v, want nil", got)
	}
	if got := Lookup(Object{Metadata: Metadata{Name: "x"}}, "status.uid"); got != nil {
		t.Errorf("absent status = %v, want nil", got)
	}
}

// The same path has to work against decoded JSON, because that is what a
// provider in another process will send.
func TestLookupResolvesAgainstAMap(t *testing.T) {
	o := Object{
		Metadata: Metadata{Name: "alice"},
		Status:   map[string]any{"uid": float64(1000), "nested": map[string]any{"deep": "value"}},
	}
	if got := Lookup(o, "status.uid"); got != float64(1000) {
		t.Errorf("status.uid = %v, want 1000", got)
	}
	if got := Lookup(o, "status.nested.deep"); got != "value" {
		t.Errorf("status.nested.deep = %v, want value", got)
	}
}

func TestValidPathChecksTheSchemaAndNotTheValue(t *testing.T) {
	spec, status := &testSpec{}, &testStatus{}
	valid := []string{
		"metadata.name", "spec.shell", "status.uid",
		// Unset in the zeroed value, still a field of the type.
		"status.priority", "status.groups",
		"status.baseurl|status.metalink|status.mirrorlist",
		"kind", "apiVersion",
	}
	for _, path := range valid {
		if !ValidPath(spec, status, path) {
			t.Errorf("ValidPath(%q) = false, want true", path)
		}
	}
	invalid := []string{
		"status.nosuchfield", "spec.uid", "status.SteamID64",
		"nowhere.name", "", "status.",
		// One bad alternative poisons the whole path: it would render a dash and
		// nobody would know which half was wrong.
		"status.uid|status.nosuchfield",
	}
	for _, path := range invalid {
		if ValidPath(spec, status, path) {
			t.Errorf("ValidPath(%q) = true, want false", path)
		}
	}
}
