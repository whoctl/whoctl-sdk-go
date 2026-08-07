package core

import (
	"strings"
	"time"
)

// Time is a timestamp spelled the way Kubernetes spells one: RFC 3339 with
// second precision, and absent rather than zero when it was never set.
//
// # Why not time.Time
//
// encoding/json writes a time.Time as RFC 3339 with nanoseconds, and a zero one
// as "0001-01-01T00:00:00Z". Kubernetes writes seconds and `null`, and every
// client that reads a creationTimestamp — kubectl, k9s, Lens — parses what
// Kubernetes writes. A field that is compatible in Go and incompatible on the
// wire is the kind of difference that is only found by pointing a real client
// at it.
//
// The yaml methods are the untyped forms on purpose: implementing them costs
// this package no dependency on a yaml library, and core is imported by every
// provider.
type Time struct{ time.Time }

// NewTime wraps a time.Time, truncated to the second because that is all the
// wire carries.
func NewTime(t time.Time) Time { return Time{t.Truncate(time.Second)} }

// MarshalJSON writes RFC 3339, or null for a zero time.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.UTC().Format(time.RFC3339) + `"`), nil
}

// UnmarshalJSON reads RFC 3339, accepting null as the zero time.
func (t *Time) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "null" || text == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}

// MarshalYAML mirrors the JSON form, so `-o yaml` and `-o json` agree.
func (t Time) MarshalYAML() (any, error) {
	if t.IsZero() {
		return nil, nil
	}
	return t.UTC().Format(time.RFC3339), nil
}

// UnmarshalYAML takes the callback form so that core does not import a yaml
// library; gopkg.in/yaml.v3 still honours it.
func (t *Time) UnmarshalYAML(unmarshal func(any) error) error {
	var text string
	if err := unmarshal(&text); err != nil {
		return err
	}
	if text == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}
