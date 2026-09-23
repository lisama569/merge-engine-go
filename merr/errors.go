// Package merr defines the normalized error taxonomy for the merge engine.
//
// Every error returned by the engine's public API is one of the concrete
// error kinds defined here (inspectable via errors.As); raw errors from
// third-party libraries never cross the API boundary.
package merr

import (
	"errors"
	"fmt"
)

// Kind classifies an engine error.
type Kind int

const (
	// KindUnknown is the zero value and must never be produced by the engine.
	KindUnknown Kind = iota
	// KindInvalidInput: malformed envelope, missing required fields,
	// illegal line ranges, unsupported source kind.
	KindInvalidInput
	// KindSyntax: the third-party diff parser rejected the input syntax.
	KindSyntax
	// KindLibraryInternal: the third-party parser panicked or failed for a
	// reason that is not a user-facing syntax error.
	KindLibraryInternal
	// KindNotFound: a referenced snapshot or resource does not exist.
	KindNotFound
	// KindConflict: the changes cannot be merged automatically.
	KindConflict
)

// String renders the canonical wire name of a kind.
func (k Kind) String() string {
	switch k {
	case KindInvalidInput:
		return "invalid_input"
	case KindSyntax:
		return "syntax_error"
	case KindLibraryInternal:
		return "library_internal_error"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// EngineError is the uniform error wrapper used across all public APIs.
type EngineError struct {
	Kind    Kind
	Reason  string // machine-readable short code, e.g. "illegal_line_range"
	Message string // human-readable explanation
	// Source names the subsystem that produced the error
	// (e.g. "ingest", "thirddiff", "merge").
	Source string
	// Context keeps original, redaction-free context for explainability.
	Context map[string]any
	// Cause is the underlying error, retained for errors.Is/As.
	Cause error
}

func (e *EngineError) Error() string {
	s := fmt.Sprintf("%s: %s: %s", e.Source, e.Kind, e.Message)
	if e.Reason != "" {
		s = fmt.Sprintf("%s [%s]", s, e.Reason)
	}
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

func (e *EngineError) Unwrap() error { return e.Cause }

// New builds an EngineError without an underlying cause.
func New(kind Kind, source, reason, message string) *EngineError {
	return &EngineError{Kind: kind, Reason: reason, Message: message, Source: source, Context: map[string]any{}}
}

// Wrap builds an EngineError wrapping an underlying error.
func Wrap(kind Kind, source, reason, message string, cause error) *EngineError {
	return &EngineError{Kind: kind, Reason: reason, Message: message, Source: source, Context: map[string]any{}, Cause: cause}
}

// WithContext attaches diagnostic context entries and returns the same error.
func (e *EngineError) WithContext(kv map[string]any) *EngineError {
	if e.Context == nil {
		e.Context = map[string]any{}
	}
	for k, v := range kv {
		e.Context[k] = v
	}
	return e
}

// asEngine extracts the *EngineError from err, if present.
func asEngine(err error) *EngineError {
	var ee *EngineError
	if errors.As(err, &ee) {
		return ee
	}
	return nil
}

// IsInvalid reports whether err is an invalid-input error.
func IsInvalid(err error) bool {
	if ee := asEngine(err); ee != nil {
		return ee.Kind == KindInvalidInput
	}
	return false
}

// IsSyntax reports whether err is a parser syntax error.
func IsSyntax(err error) bool {
	if ee := asEngine(err); ee != nil {
		return ee.Kind == KindSyntax
	}
	return false
}

// IsLibraryInternal reports whether err is a third-party library fault.
func IsLibraryInternal(err error) bool {
	if ee := asEngine(err); ee != nil {
		return ee.Kind == KindLibraryInternal
	}
	return false
}

// IsNotFound reports whether err is a missing-resource error.
func IsNotFound(err error) bool {
	if ee := asEngine(err); ee != nil {
		return ee.Kind == KindNotFound
	}
	return false
}

// IsConflict reports whether err is a merge conflict.
func IsConflict(err error) bool {
	if ee := asEngine(err); ee != nil {
		return ee.Kind == KindConflict
	}
	return false
}

// Formatf is a small helper for structured messages.
func Formatf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
