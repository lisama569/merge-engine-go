package mergeengine

import (
	"errors"
	"strings"
	"testing"

	"github.com/lisama569/merge-engine-go/ingest"
	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// TestEndToEndTextAndStructuredMerge exercises the full facade: a
// structured change side and a text-diff side are normalized, merged over
// a local base snapshot and replayed, with three-way inputs logged.
func TestEndToEndTextAndStructuredMerge(t *testing.T) {
	e := New()
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"doc.txt": "one\ntwo\nthree\nfour\n",
	}}
	if err := e.SaveSnapshot(base); err != nil {
		t.Fatal(err)
	}
	t.Logf("BASE doc.txt=%q", base.Files["doc.txt"])

	// Side A: structured, edits line 1.
	envA := &ingest.Envelope{SourceID: "alice", Kind: ingest.SourceStructured,
		Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{{
			Path: "doc.txt", Op: model.OpModify,
			Hunks: []ingest.StructuredHunk{{
				OldStart: 1, OldEnd: 1, NewStart: 1, NewEnd: 1,
				OldLines: []string{"one"}, NewLines: []string{"ONE"},
			}},
		}}}}
	csA, err := e.Ingest(envA)
	if err != nil {
		t.Fatalf("ingest A: %v", err)
	}
	t.Logf("SIDE alice normalized: %+v", csA.Changes)

	// Side B: text diff, edits line 4 (disjoint).
	diff := "+++ b/doc.txt\n@@ -4,1 +4,1 @@\n-four\n+FOUR\n"
	envB := &ingest.Envelope{SourceID: "bob", Kind: ingest.SourceTextDiff,
		TextDiff: &ingest.TextDiffInput{Path: "doc.txt", Diff: diff}}
	t.Logf("SIDE bob raw diff:\n%s", diff)
	csB, err := e.Ingest(envB)
	if err != nil {
		t.Fatalf("ingest B: %v", err)
	}
	t.Logf("SIDE bob normalized: %+v", csB.Changes)

	r, err := e.Replay("base", "merged", 2, "alice", "bob")
	if err != nil {
		t.Fatalf("merge/replay: %v", err)
	}
	t.Logf("VERDICT:\n%s", r.Explain())
	if !r.OK {
		t.Fatalf("disjoint cross-format edits must merge: %+v", r.Conflicts)
	}
	want := "ONE\ntwo\nthree\nFOUR\n"
	if r.Merged.Files["doc.txt"] != want {
		t.Fatalf("merged content=%q want %q", r.Merged.Files["doc.txt"], want)
	}
	if r.Replays != 2 {
		t.Fatalf("replays=%d", r.Replays)
	}
	if _, err := e.LoadSnapshot("merged"); err != nil {
		t.Fatalf("merged snapshot must be persisted: %v", err)
	}
}

// TestEndToEndOverlapConflictReplay ensures an overlapping merge is
// reported with a distinguishable kind and stays identical on replay.
func TestEndToEndOverlapConflictReplay(t *testing.T) {
	e := New()
	if err := e.SaveSnapshot(&model.Snapshot{Name: "base", Files: map[string]string{
		"f": "a\nb\nc\n",
	}}); err != nil {
		t.Fatal(err)
	}
	mk := func(id, replacement string) *ingest.Envelope {
		return &ingest.Envelope{SourceID: id, Kind: ingest.SourceStructured,
			Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{{
				Path: "f", Op: model.OpModify,
				Hunks: []ingest.StructuredHunk{{
					OldStart: 2, OldEnd: 2, NewStart: 2, NewEnd: 2,
					OldLines: []string{"b"}, NewLines: []string{replacement},
				}},
			}}}}
	}
	if _, err := e.Ingest(mk("alice", "B1")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(mk("bob", "B2")); err != nil {
		t.Fatal(err)
	}
	r1, err := e.Merge("base", "should-not-persist", "alice", "bob")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VERDICT 1:\n%s", r1.Explain())
	if r1.OK || len(r1.Conflicts) != 1 || r1.Conflicts[0].Kind != model.ConflictTextOverlap {
		t.Fatalf("want one text_overlap conflict, got %+v", r1.Conflicts)
	}
	// Conflict result must NOT be persisted under the result name.
	if _, err := e.LoadSnapshot("should-not-persist"); !merr.IsNotFound(err) {
		t.Fatalf("conflicted merge must not persist output, got %v", err)
	}
	r2, err := e.Merge("base", "", "bob", "alice") // reversed side order
	if err != nil {
		t.Fatal(err)
	}
	if r2.Explain() != r1.Explain() {
		t.Fatalf("conflict report must be order-independent:\n%s\n---\n%s", r1.Explain(), r2.Explain())
	}
}

// TestEndToEndInvalidInputIsRejectedNotSkipped proves malformed records
// surface with distinguishable reasons and never enter storage.
func TestEndToEndInvalidInputIsRejectedNotSkipped(t *testing.T) {
	e := New()
	cases := []struct {
		name     string
		env      *ingest.Envelope
		reason   string
		syntax   bool
		internal bool
	}{
		{
			name: "illegal range",
			env: &ingest.Envelope{SourceID: "s", Kind: ingest.SourceStructured,
				Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{{
					Path: "f", Op: model.OpModify,
					Hunks: []ingest.StructuredHunk{{OldStart: 9, OldEnd: 1}},
				}}}},
			reason: "illegal_line_range",
		},
		{
			name: "missing source id",
			env: &ingest.Envelope{Kind: ingest.SourceStructured,
				Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{
					{Path: "f", Op: model.OpDelete}}}},
			reason: "missing_source_id",
		},
		{
			name: "diff syntax",
			env: &ingest.Envelope{SourceID: "s", Kind: ingest.SourceTextDiff,
				TextDiff: &ingest.TextDiffInput{Path: "f", Diff: "+++ b/f\n@@ bad @@\n"}},
			reason: "diff_syntax", syntax: true,
		},
		{
			name: "library internal panic",
			env: &ingest.Envelope{SourceID: "s", Kind: ingest.SourceTextDiff,
				TextDiff: &ingest.TextDiffInput{Path: "f",
					Diff: "+++ b/f\n@@ -1,1 +1,1 @@\n LIBRARY-BOOM\n"}},
			reason: "diff_library_panic", internal: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.Ingest(tc.env)
			if err == nil {
				t.Fatal("expected rejection")
			}
			var ee *merr.EngineError
			if !errors.As(err, &ee) {
				t.Fatalf("raw non-engine error escaped: %T %v", err, err)
			}
			if ee.Reason != tc.reason {
				t.Fatalf("reason=%q want %q (kind=%s ctx=%v)", ee.Reason, tc.reason, ee.Kind, ee.Context)
			}
			if tc.syntax && !merr.IsSyntax(err) {
				t.Fatalf("want syntax kind, got %s", ee.Kind)
			}
			if tc.internal && !merr.IsLibraryInternal(err) {
				t.Fatalf("want library-internal kind, got %s", ee.Kind)
			}
			if !tc.syntax && !tc.internal && !merr.IsInvalid(err) {
				t.Fatalf("want invalid-input kind, got %s", ee.Kind)
			}
			// The raw library type must never appear at the facade.
			if strings.Contains(err.Error(), "DiffSyntaxError") ||
				strings.Contains(err.Error(), "LibraryFault{") {
				t.Fatalf("raw library error leaked: %v", err)
			}
			t.Logf("INPUT rejected (%s): kind=%s reason=%s context=%v",
				tc.name, ee.Kind, ee.Reason, ee.Context)
			if _, getErr := e.LoadSnapshot("x"); getErr == nil {
				t.Fatal("unexpected snapshot present")
			}
		})
	}
}

// TestEndToEndUnknownBaseErrors ensures missing base is a typed error.
func TestEndToEndUnknownBaseErrors(t *testing.T) {
	e := New()
	_, err := e.Merge("no-such-base", "out")
	if !merr.IsNotFound(err) {
		t.Fatalf("want not_found for missing base, got %v", err)
	}
	t.Logf("missing base reported: %v", err)
}
