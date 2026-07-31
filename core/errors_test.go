package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodeOfClassifies(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want Code
	}{
		{nil, ""},
		{NotFound("user", "alice"), CodeNotFound},
		{Invalidf("bad"), CodeInvalid},
		{Unsupportedf("no"), CodeUnsupported},
		{Unavailablef("absent"), CodeUnavailable},
		{Refusedf("not now"), CodeRefused},
		// Anything nobody classified is internal, which is the honest answer.
		{errors.New("something broke"), CodeInternal},
		// A code survives being wrapped on the way up through a handler.
		{fmt.Errorf("applying user %q: %w", "alice", NotFound("user", "alice")), CodeNotFound},
	} {
		if got := CodeOf(tc.err); got != tc.want {
			t.Errorf("CodeOf(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestNotFoundReadsTheWayItAlwaysDid(t *testing.T) {
	err := NotFound("user", "alice")
	if got, want := err.Error(), `user "alice" not found`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound = false")
	}
	if err.Resource != "user" || err.Name != "alice" {
		t.Errorf("resource/name = %q/%q, want user/alice", err.Resource, err.Name)
	}
}

// %w has to keep meaning %w: providers wrap sentinels of their own — BusyBox's
// unsupported-operation error is one — and errors.Is must still find them while
// the code travels alongside.
func TestWrappingKeepsBothTheCauseAndTheCode(t *testing.T) {
	sentinel := errors.New("operation not supported by the available toolset")
	err := Unsupportedf("%w: busybox does not implement %q", sentinel, "usermod")

	if !errors.Is(err, sentinel) {
		t.Error("errors.Is lost the wrapped sentinel")
	}
	if CodeOf(err) != CodeUnsupported {
		t.Errorf("code = %q, want UNSUPPORTED", CodeOf(err))
	}
	// Message is the flat text, because that is all that can cross a process
	// boundary — the cause does not travel and nothing may depend on it.
	want := `operation not supported by the available toolset: busybox does not implement "usermod"`
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorWithoutAMessageStillSaysSomething(t *testing.T) {
	if got := (&Error{Code: CodeRefused}).Error(); got != "REFUSED" {
		t.Errorf("bare code = %q, want REFUSED", got)
	}
	if got := (&Error{Code: CodeNotFound, Resource: "user", Name: "bob"}).Error(); got != `user "bob": NOT_FOUND` {
		t.Errorf("bare resource = %q", got)
	}
}
