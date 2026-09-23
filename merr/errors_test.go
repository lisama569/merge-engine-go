package merr

import (
	"errors"
	"fmt"
	"testing"
)

func TestKindStrings(t *testing.T) {
	cases := map[Kind]string{
		KindInvalidInput:    "invalid_input",
		KindSyntax:          "syntax_error",
		KindLibraryInternal: "library_internal_error",
		KindNotFound:        "not_found",
		KindConflict:        "conflict",
		KindUnknown:         "unknown",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Fatalf("Kind(%d).String()=%q want %q", k, got, want)
		}
	}
}

func TestEngineErrorClassification(t *testing.T) {
	base := errors.New("boom")
	err := Wrap(KindSyntax, "thirddiff", "diff_syntax", "bad token", base).
		WithContext(map[string]any{"line": 3, "raw": "@@ broken"})

	if !IsSyntax(err) {
		t.Fatalf("expected syntax error, got %v", err)
	}
	if IsInvalid(err) || IsLibraryInternal(err) || IsConflict(err) {
		t.Fatalf("error misclassified: %v", err)
	}
	if !errors.Is(err, base) {
		t.Fatalf("Unwrap must retain the cause")
	}
	if err.Context["line"] != 3 || err.Context["raw"] != "@@ broken" {
		t.Fatalf("context not retained: %#v", err.Context)
	}
	if s := err.Error(); s == "" {
		t.Fatalf("Error() must not be empty")
	} else {
		t.Logf("rendered error: %s", s)
	}

	var ee *EngineError
	if !errors.As(err, &ee) {
		t.Fatalf("errors.As must find *EngineError")
	}
	if ee.Reason != "diff_syntax" {
		t.Fatalf("reason=%q", ee.Reason)
	}
}

func TestNewAndPredicates(t *testing.T) {
	if IsInvalid(nil) || IsSyntax(nil) || IsLibraryInternal(nil) || IsConflict(nil) || IsNotFound(nil) {
		t.Fatalf("predicates must be false for nil")
	}
	if !IsInvalid(New(KindInvalidInput, "ingest", "x", "y")) {
		t.Fatal("invalid classification failed")
	}
	if !IsNotFound(New(KindNotFound, "storage", "x", "y")) {
		t.Fatal("not_found classification failed")
	}
	if !IsLibraryInternal(New(KindLibraryInternal, "thirddiff", "x", "y")) {
		t.Fatal("library classification failed")
	}
	if !IsConflict(New(KindConflict, "merge", "x", "y")) {
		t.Fatal("conflict classification failed")
	}
	if got := Formatf("%d", 7); got != "7" {
		t.Fatalf("Formatf=%q", got)
	}
	// A foreign error must never classify as a typed engine error.
	if IsInvalid(fmt.Errorf("plain")) {
		t.Fatal("plain error must not be classified")
	}
}
