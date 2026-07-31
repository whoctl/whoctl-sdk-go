package docs

import (
	"testing"

	"github.com/whoctl/whoctl-sdk-go/schema"
)

// The flag labels are what a reader sees, so they are worth pinning.
func TestFlagLabels(t *testing.T) {
	f := schema.Field{Flags: []string{"required", "createOnly", "writeOnly", "immutable", "custom"}}
	got := FlagLabels(f)
	want := []string{"create-only", "write-only", "immutable", "custom"}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label %d = %q, want %q", i, got[i], want[i])
		}
	}
}
