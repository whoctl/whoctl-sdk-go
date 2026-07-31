package core

import (
	"errors"
	"fmt"
)

// Code classifies an error in terms a command can act on.
//
// # Why a code and not a Go type
//
// `delete --ignore-not-found` needs to know that a failure means "no such
// object". Asking errors.As for a *NotFoundError answers that only while the
// provider is a Go package in this process, and it is not: a Go type does not
// survive the trip between two processes. A code does.
//
// The set is small on purpose. Every one of these means something different to
// a *user*, which is the only test for adding another: "the package manager is
// not installed here" and "the package is not installed here" lead to different
// next steps, so they are different codes. Anything with no such distinction is
// CodeInternal and reads as the message it carries.
type Code string

const (
	// CodeNotFound is a named object that does not exist.
	CodeNotFound Code = "NOT_FOUND"
	// CodeInvalid is a manifest or an argument the provider will not accept.
	CodeInvalid Code = "INVALID"
	// CodeUnsupported is a verb this kind — or the tooling behind it — does not
	// have. A Steam achievement cannot be applied; BusyBox cannot modify a user.
	// Nothing about the machine's state would change the answer.
	CodeUnsupported Code = "UNSUPPORTED"
	// CodeUnavailable is a backend that is not on this machine at all, which is
	// different from having nothing to report: `whoctl get linux/dnfpackages`
	// on Alpine is not an empty list.
	CodeUnavailable Code = "UNAVAILABLE"
	// CodeRefused is an operation that would work, but must not right now — the
	// Steam client is running and would overwrite the change on exit.
	CodeRefused Code = "REFUSED"
	// CodeInternal is everything else: a parse failure, a command that exited
	// non-zero, a file that would not open.
	CodeInternal Code = "INTERNAL"
)

// Error is an error carrying a Code. Providers return these for the cases a
// command distinguishes; anything else is CodeInternal by default.
type Error struct {
	Code Code
	// Resource and Name identify the object, when the error is about one.
	Resource string
	Name     string
	Message  string
	// Cause is the underlying error, for errors.Is and errors.As on this side
	// of the boundary. It does not travel: Message is what a provider in
	// another process can send, so nothing may depend on Cause surviving.
	Cause error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Resource != "" {
		return fmt.Sprintf("%s %q: %s", e.Resource, e.Name, e.Code)
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.Cause }

// NotFound reports a missing object. The wording is the one users have always
// seen, and `-o name` style references are not used here because the caller
// already printed what it asked for.
func NotFound(resource, name string) *Error {
	return &Error{
		Code:     CodeNotFound,
		Resource: resource,
		Name:     name,
		Message:  fmt.Sprintf("%s %q not found", resource, name),
	}
}

// Invalidf rejects a manifest or an argument.
func Invalidf(format string, args ...any) *Error {
	return codedf(CodeInvalid, format, args...)
}

// Unsupportedf reports a verb this kind or its tooling does not have.
func Unsupportedf(format string, args ...any) *Error {
	return codedf(CodeUnsupported, format, args...)
}

// Unavailablef reports a backend that is not on this machine.
func Unavailablef(format string, args ...any) *Error {
	return codedf(CodeUnavailable, format, args...)
}

// Refusedf reports something whoctl will not do right now, and says why.
func Refusedf(format string, args ...any) *Error {
	return codedf(CodeRefused, format, args...)
}

// codedf formats through fmt.Errorf rather than fmt.Sprintf so %w means what it
// always means: the wrapped error stays reachable through errors.Is on this side
// of the boundary, while Message is the flat text that can cross it.
func codedf(code Code, format string, args ...any) *Error {
	err := fmt.Errorf(format, args...)
	return &Error{Code: code, Message: err.Error(), Cause: errors.Unwrap(err)}
}

// CodeOf classifies any error. An error nobody classified is internal, which is
// the honest answer: whoctl does not know what it means either.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return CodeInternal
}

// IsNotFound reports whether err represents a missing object.
func IsNotFound(err error) bool { return CodeOf(err) == CodeNotFound }

// Internalf reports a failure whoctl cannot classify further — a provider that
// crashed, a file that would not open.
func Internalf(format string, args ...any) *Error {
	return codedf(CodeInternal, format, args...)
}
