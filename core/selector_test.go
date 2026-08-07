package core

import "testing"

func TestParseSelectorReadsTheKubectlSyntax(t *testing.T) {
	for input, want := range map[string]string{
		"":                              "",
		"owner=platform":                "owner=platform",
		"owner==platform":               "owner=platform",
		"owner!=platform":               "owner!=platform",
		"owner":                         "owner",
		"!owner":                        "!owner",
		"owner=platform,env!=prod,!tmp": "owner=platform,env!=prod,!tmp",
		" owner = platform ":            "owner=platform",
	} {
		got, err := ParseSelector(input)
		if err != nil {
			t.Errorf("ParseSelector(%q): %v", input, err)
			continue
		}
		if got.String() != want {
			t.Errorf("ParseSelector(%q) = %q, want %q", input, got.String(), want)
		}
	}
}

// "a!=b" must not be read as the key "a!" with the value "b", which is what
// cutting on "=" before "!=" does.
func TestNotEqualsIsReadBeforeEquals(t *testing.T) {
	s, err := ParseSelector("state!=running")
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 || s[0].Key != "state" || s[0].Value != "running" || !s[0].Negated {
		t.Errorf("parsed as %+v", s)
	}
}

func TestASelectorThatNamesNothingIsRejected(t *testing.T) {
	for _, input := range []string{"=value", "!", "a=b,,c=d", "!=x"} {
		if _, err := ParseSelector(input); err == nil {
			t.Errorf("ParseSelector(%q) was accepted", input)
		}
	}
}

func TestMatchesLabels(t *testing.T) {
	labels := map[string]string{"owner": "platform", "env": "prod", "blank": ""}
	for selector, want := range map[string]bool{
		"owner=platform":           true,
		"owner=other":              false,
		"owner!=other":             true,
		"owner":                    true,
		"missing":                  false,
		"!missing":                 true,
		"!owner":                   false,
		"owner=platform,env=prod":  true,
		"owner=platform,env=stage": false,
		"blank=":                   true,
		"missing=":                 true, // absent and empty compare alike
		"blank!=":                  false,
	} {
		s, err := ParseSelector(selector)
		if err != nil {
			t.Fatalf("%q: %v", selector, err)
		}
		if got := s.MatchesLabels(labels); got != want {
			t.Errorf("%q matched %t, want %t", selector, got, want)
		}
	}
}

type selectorStatus struct {
	State    string `yaml:"state"`
	PublicIP string `yaml:"publicIp,omitempty"`
	Count    int    `yaml:"count"`
}

// A field selector reads a manifest path, spelled the way a column spells one
// and the way -o yaml prints one: what you see in a table is what you filter on.
func TestMatchesObjectReadsManifestPaths(t *testing.T) {
	obj := Object{
		Metadata: Metadata{Name: "i-1", Namespace: "us-east-1"},
		Status:   &selectorStatus{State: "running", Count: 3},
	}
	for selector, want := range map[string]bool{
		"status.state=running":                   true,
		"status.state=stopped":                   false,
		"status.state!=stopped":                  true,
		"metadata.namespace=us-east-1":           true,
		"metadata.name=i-1,status.state=running": true,
		"status.count=3":                         true, // a number compares as it prints
		"status.count=4":                         false,
		"status.publicIp=":                       true, // unset
		"status.publicIp!=":                      false,
		"status.nosuchfield=x":                   false,
	} {
		s, err := ParseSelector(selector)
		if err != nil {
			t.Fatalf("%q: %v", selector, err)
		}
		if got := s.MatchesObject(obj); got != want {
			t.Errorf("%q matched %t, want %t", selector, got, want)
		}
	}
}

func TestAnEmptySelectorMatchesEverything(t *testing.T) {
	s, err := ParseSelector("")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Empty() {
		t.Error("an empty selector is not Empty()")
	}
	if !s.MatchesLabels(nil) || !s.MatchesObject(Object{}) {
		t.Error("an empty selector rejected something")
	}
}
