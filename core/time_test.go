package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The spelling is the whole point of the type. A creationTimestamp with
// nanoseconds, or a zero one written as year 1, is valid Go and not what a
// Kubernetes client reads.
func TestTimeIsSpelledTheWayKubernetesSpellsIt(t *testing.T) {
	stamp := NewTime(time.Date(2024, 3, 1, 12, 0, 0, 123456789, time.UTC))

	encoded, err := json.Marshal(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `"2024-03-01T12:00:00Z"`; got != want {
		t.Errorf("json = %s, want %s", got, want)
	}

	var back Time
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(stamp.Time) {
		t.Errorf("round trip gave %v, want %v", back, stamp)
	}
}

func TestAnUnsetTimeIsNull(t *testing.T) {
	encoded, err := json.Marshal(Time{})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "null" {
		t.Errorf("json = %s, want null", encoded)
	}

	var back Time
	if err := json.Unmarshal([]byte("null"), &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsZero() {
		t.Errorf("null decoded to %v", back)
	}
}

// An object whose timestamp was never set must not carry one at all, or every
// manifest whoctl prints gains a creationTimestamp of year 1.
func TestAnUnsetTimeIsOmittedFromAnObject(t *testing.T) {
	encoded, err := json.Marshal(Object{Metadata: Metadata{Name: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); strings.Contains(got, "creationTimestamp") {
		t.Errorf("json = %s, want no creationTimestamp", got)
	}
}
