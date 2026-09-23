package ingest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// logEnvelope prints the three-way-relevant input for test explainability.
func logEnvelope(t *testing.T, env *Envelope) {
	t.Helper()
	t.Logf("INPUT source_id=%q kind=%q", env.SourceID, env.Kind)
	if env.TextDiff != nil {
		t.Logf("INPUT text_diff path=%q body=\n%s", env.TextDiff.Path, env.TextDiff.Diff)
	}
	if env.Structured != nil {
		for i, c := range env.Structured.Changes {
			t.Logf("INPUT structured[%d] op=%s path=%q old_path=%q hunks=%d",
				i, c.Op, c.Path, c.OldPath, len(c.Hunks))
		}
	}
}

func assertReason(t *testing.T, err error, kind merr.Kind, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind=%s reason=%s, got nil", kind, reason)
	}
	var ee *merr.EngineError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *EngineError, got %T: %v", err, err)
	}
	if ee.Kind != kind {
		t.Fatalf("kind=%s(%s) want %s; reason=%q context=%v", ee.Kind, ee.Kind, kind, ee.Reason, ee.Context)
	}
	if ee.Reason != reason {
		t.Fatalf("reason=%q want %q; context=%v", ee.Reason, reason, ee.Context)
	}
	t.Logf("REJECTED as kind=%s reason=%s msg=%q context=%v", ee.Kind, ee.Reason, ee.Message, ee.Context)
}

func TestNormalizeStructuredInvalidEnvelopes(t *testing.T) {
	// 1. nil envelope
	if _, err := Normalize(nil); !merr.IsInvalid(err) {
		t.Fatalf("nil envelope must be invalid: %v", err)
	}
	// 2. missing source id
	_, err := Normalize(&Envelope{Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "a", Op: model.OpAdd}}}})
	assertReason(t, err, merr.KindInvalidInput, "missing_source_id")

	// 3. missing kind
	_, err = Normalize(&Envelope{SourceID: "s1"})
	assertReason(t, err, merr.KindInvalidInput, "missing_kind")

	// 4. unsupported kind
	_, err = Normalize(&Envelope{SourceID: "s1", Kind: "binary_blob"})
	assertReason(t, err, merr.KindInvalidInput, "unsupported_kind")

	// 5. empty structured batch
	_, err = Normalize(&Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{}})
	assertReason(t, err, merr.KindInvalidInput, "empty_changes")

	// 6. missing path
	env := &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Op: model.OpAdd}}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "missing_path")

	// 7. unknown op
	env = &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "a", Op: "explode"}}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "unknown_op")

	// 8. duplicate path (no silent skip)
	env = &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{
			{Path: "a", Op: model.OpAdd, NewContent: "1"},
			{Path: "a", Op: model.OpDelete},
		}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "duplicate_path")
}

func TestNormalizeIllegalLineRanges(t *testing.T) {
	base := func(h StructuredHunk) *Envelope {
		return &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
			Changes: []StructuredChange{{Path: "f", Op: model.OpModify, Hunks: []StructuredHunk{h}}}}}
	}

	// end before start
	env := base(StructuredHunk{OldStart: 5, OldEnd: 2, NewStart: 1, NewEnd: 1,
		OldLines: []string{}, NewLines: []string{"x"}})
	logEnvelope(t, env)
	_, err := Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "illegal_line_range")

	// negative numbers
	env = base(StructuredHunk{OldStart: -1, OldEnd: 2})
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "illegal_line_range")

	// length mismatch: declares 2 old lines, provides 1
	env = base(StructuredHunk{OldStart: 1, OldEnd: 2, NewStart: 1, NewEnd: 1,
		OldLines: []string{"only-one"}, NewLines: []string{"x"}})
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "hunk_length_mismatch")

	// modify without hunks
	env = &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f", Op: model.OpModify}}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "missing_hunks")

	// rename missing old_path
	env = &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f2", Op: model.OpRename}}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "missing_old_path")
}

func TestNormalizeStructuredValid(t *testing.T) {
	env := &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{
			{Path: "new.txt", Op: model.OpAdd, NewContent: "hello"},
			{Path: "f.txt", Op: model.OpModify, Hunks: []StructuredHunk{{
				OldStart: 1, OldEnd: 1, NewStart: 1, NewEnd: 1,
				OldLines: []string{"a"}, NewLines: []string{"b"}}}},
			{Path: "renamed.txt", OldPath: "old.txt", Op: model.OpRename},
			{Path: "gone.txt", Op: model.OpDelete},
		}}}
	logEnvelope(t, env)
	cs, err := Normalize(env)
	if err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	if len(cs.Changes) != 4 {
		t.Fatalf("got %d changes, want 4", len(cs.Changes))
	}
	t.Logf("ACCEPTED change set %q with %d changes", cs.SourceID, len(cs.Changes))
}

func TestTextDiffSyntaxErrorIsNormalized(t *testing.T) {
	// Malformed hunk header => third-party *DiffSyntaxError -> KindSyntax.
	env := &Envelope{SourceID: "s1", Kind: SourceTextDiff, TextDiff: &TextDiffInput{
		Path: "f.txt",
		Diff: "+++ b/f.txt\n@@ not-a-range @@\n context\n",
	}}
	logEnvelope(t, env)
	_, err := Normalize(env)
	assertReason(t, err, merr.KindSyntax, "diff_syntax")

	// Unrecognized prefix also maps to syntax, not internal fault.
	env = &Envelope{SourceID: "s1", Kind: SourceTextDiff, TextDiff: &TextDiffInput{
		Path: "f.txt", Diff: "+++ b/f.txt\n@@ -1,1 +1,1 @@\n?? weird\n"}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindSyntax, "diff_syntax")

	// Missing path in envelope itself is invalid input.
	env = &Envelope{SourceID: "s1", Kind: SourceTextDiff, TextDiff: &TextDiffInput{Diff: "+++ b/x\n"}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "missing_path")
}

func TestTextDiffLibraryPanicIsNormalized(t *testing.T) {
	// The sentinel triggers a simulated native panic inside the library.
	env := &Envelope{SourceID: "s1", Kind: SourceTextDiff, TextDiff: &TextDiffInput{
		Path: "f.txt",
		Diff: "+++ b/f.txt\n@@ -1,1 +1,1 @@\n LIBRARY-BOOM\n",
	}}
	logEnvelope(t, env)
	_, err := Normalize(env)
	if err == nil {
		t.Fatal("expected normalized error for library panic")
	}
	assertReason(t, err, merr.KindLibraryInternal, "diff_library_panic")
	var ee *merr.EngineError
	errors.As(err, &ee)
	if ee.Context["recovered"] == nil {
		t.Fatalf("original panic context must be retained: %#v", ee.Context)
	}
	t.Logf("panic contained; original context=%v", ee.Context)
}

func TestTextDiffValidTranslation(t *testing.T) {
	diff := "+++ b/f.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" keep\n" +
		"-old\n" +
		"+new\n"
	env := &Envelope{SourceID: "s1", Kind: SourceTextDiff, TextDiff: &TextDiffInput{
		Path: "f.txt", Diff: diff}}
	logEnvelope(t, env)
	cs, err := Normalize(env)
	if err != nil {
		t.Fatalf("valid diff rejected: %v", err)
	}
	if len(cs.Changes) != 1 || cs.Changes[0].Op != model.OpModify {
		t.Fatalf("unexpected change set: %#v", cs)
	}
	h := cs.Changes[0].Hunks[0]
	if h.OldStart != 1 || h.OldEnd != 2 || h.NewStart != 1 || h.NewEnd != 2 {
		t.Fatalf("range translation wrong: %+v", h)
	}
	if fmt.Sprint(h.OldLines) != "[keep old]" || fmt.Sprint(h.NewLines) != "[keep new]" {
		t.Fatalf("line translation wrong: old=%v new=%v", h.OldLines, h.NewLines)
	}
	t.Logf("ACCEPTED translated hunk: %+v old=%v new=%v", h, h.OldLines, h.NewLines)
}
