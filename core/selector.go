package core

import (
	"fmt"
	"strings"
)

// A Selector filters objects, in the syntax Kubernetes uses for `-l` and
// `--field-selector`: comma-separated requirements, each `key=value`,
// `key==value` or `key!=value`, plus the bare `key` and `!key` forms that ask
// whether a label is set at all.
//
// # Where it is applied, and why in both places
//
// It crosses the wire, so a provider *may* push it down to whatever it is
// talking to — the aws provider turns `status.state=running` into an EC2 filter
// and fetches less. It is also applied by whoctl to whatever comes back, so
// correctness never depends on a provider having bothered: a provider that
// ignores the selector returns too much and the answer is still right.
//
// That is the same shape as sysexec and --dry-run. The provider is trusted to
// make it faster, never to make it correct.
type Selector []Requirement

// A Requirement is one term of a selector.
type Requirement struct {
	Key   string
	Value string
	// Negated is `!=` for a term with a value, and `!key` for one without.
	Negated bool
	// HasValue distinguishes `key=` — a value that is the empty string — from
	// `key`, which asks only whether it is set.
	HasValue bool
}

// ParseSelector reads the command-line syntax. An empty string is a selector
// that matches everything.
func ParseSelector(s string) (Selector, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out Selector
	for term := range strings.SplitSeq(s, ",") {
		req, err := parseRequirement(strings.TrimSpace(term))
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
}

func parseRequirement(term string) (Requirement, error) {
	if term == "" {
		return Requirement{}, Invalidf("empty term in selector")
	}
	// != is checked before = so that "a!=b" is not read as key "a!".
	if key, value, ok := strings.Cut(term, "!="); ok {
		return named(key, value, true)
	}
	if key, value, ok := strings.Cut(term, "=="); ok {
		return named(key, value, false)
	}
	if key, value, ok := strings.Cut(term, "="); ok {
		return named(key, value, false)
	}
	if key, ok := strings.CutPrefix(term, "!"); ok {
		if strings.TrimSpace(key) == "" {
			return Requirement{}, Invalidf("selector term %q names nothing", term)
		}
		return Requirement{Key: strings.TrimSpace(key), Negated: true}, nil
	}
	return Requirement{Key: term}, nil
}

func named(key, value string, negated bool) (Requirement, error) {
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	if key == "" {
		return Requirement{}, Invalidf("selector term names no key")
	}
	return Requirement{Key: key, Value: value, Negated: negated, HasValue: true}, nil
}

// String renders the selector back to the syntax it was parsed from, which is
// what puts it on the wire.
func (s Selector) String() string {
	if len(s) == 0 {
		return ""
	}
	terms := make([]string, 0, len(s))
	for _, r := range s {
		switch {
		case !r.HasValue && r.Negated:
			terms = append(terms, "!"+r.Key)
		case !r.HasValue:
			terms = append(terms, r.Key)
		case r.Negated:
			terms = append(terms, r.Key+"!="+r.Value)
		default:
			terms = append(terms, r.Key+"="+r.Value)
		}
	}
	return strings.Join(terms, ",")
}

// Empty reports a selector that matches everything.
func (s Selector) Empty() bool { return len(s) == 0 }

// MatchesLabels reports whether a label set satisfies every requirement. This
// is what `-l` asks.
func (s Selector) MatchesLabels(labels map[string]string) bool {
	for _, r := range s {
		value, present := labels[r.Key]
		if !r.matches(value, present) {
			return false
		}
	}
	return true
}

// MatchesObject reports whether an object satisfies every requirement, with
// each key read as a manifest path — "status.state", "metadata.namespace". This
// is what `--field-selector` asks, and the paths are spelled the way a column
// spells one and the way `-o yaml` prints one. There is one name for a field.
func (s Selector) MatchesObject(o Object) bool {
	for _, r := range s {
		value, present := Resolve(o, r.Key)
		if !r.matches(render(value), present && value != nil) {
			return false
		}
	}
	return true
}

func (r Requirement) matches(value string, present bool) bool {
	if !r.HasValue {
		return present != r.Negated
	}
	// An absent field is not equal to anything, and it *is* unequal to
	// everything — which is what makes `status.publicIp!=` mean "has one".
	equal := present && value == r.Value
	if !present && r.Value == "" {
		equal = true
	}
	return equal != r.Negated
}

// render is how a value read from an object is compared to the text somebody
// typed. Numbers and booleans compare as they print, which is the same
// rendering a table cell shows — so what you see in a column is what you filter
// on.
func render(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
