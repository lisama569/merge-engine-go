package mergeengine

import (
	"testing"

	"github.com/lisama569/merge-engine-go/ingest"
	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// TestEndToEndMixedInsertAndConflict drives the full facade with a batch
// that contains a pure insertion, an independent add and an overlapping
// edit: the overlap must be marked as a conflict while the independent
// changes still land in the merged output, deterministically across
// replays.
func TestEndToEndMixedInsertAndConflict(t *testing.T) {
	e := New()
	if err := e.SaveSnapshot(&model.Snapshot{Name: "base", Files: map[string]string{
		"doc.txt": "one\ntwo\nthree\n",
	}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("BASE doc.txt=%q", "one\ntwo\nthree\n")

	mk := func(id string, changes ...ingest.StructuredChange) *ingest.Envelope {
		return &ingest.Envelope{SourceID: id, Kind: ingest.SourceStructured,
			Structured: &ingest.StructuredInput{Changes: changes}}
	}
	// alice: pure insertion before line 2 + overlapping edit of line 3.
	envA := mk("alice",
		ingest.StructuredChange{Path: "doc.txt", Op: model.OpModify,
			Hunks: []ingest.StructuredHunk{
				{OldStart: 2, OldEnd: 2, NewStart: 2, NewEnd: 2,
					NewLines: []string{"inserted-by-alice"}},
				{OldStart: 3, OldEnd: 3, NewStart: 3, NewEnd: 3,
					OldLines: []string{"three"}, NewLines: []string{"THREE-A"}},
			}},
		ingest.StructuredChange{Path: "notes.txt", Op: model.OpAdd, NewContent: "notes\n"},
	)
	// bob: overlapping edit of line 3 (conflicts with alice).
	envB := mk("bob",
		ingest.StructuredChange{Path: "doc.txt", Op: model.OpModify,
			Hunks: []ingest.StructuredHunk{
				{OldStart: 3, OldEnd: 3, NewStart: 3, NewEnd: 3,
					OldLines: []string{"three"}, NewLines: []string{"THREE-B"}},
			}})
	for _, env := range []*ingest.Envelope{envA, envB} {
		if _, err := e.Ingest(env); err != nil {
			t.Fatalf("ingest %s: %v", env.SourceID, err)
		}
		t.Logf("SIDE %s ingested: %+v", env.SourceID, env.Structured.Changes)
	}

	r1, err := e.Merge("base", "mixed-out", "alice", "bob")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VERDICT 1:\n%s", r1.Explain())

	// Conflict half: the overlap on line 3 is marked explicitly.
	if r1.OK || len(r1.Conflicts) != 1 ||
		r1.Conflicts[0].Kind != model.ConflictTextOverlap {
		t.Fatalf("want exactly one text_overlap conflict, got %+v", r1.Conflicts)
	}
	// Auto-merge half: alice's insertion and add still landed.
	if got := r1.Merged.Files["notes.txt"]; got != "notes\n" {
		t.Fatalf("independent add must survive the conflict, got %q", got)
	}
	if got := r1.Merged.Files["doc.txt"]; got != "one\ninserted-by-alice\ntwo\nthree\n" {
		t.Fatalf("independent insertion must survive the conflict, got %q", got)
	}
	// Conflicted results are not persisted under the result name.
	if _, err := e.LoadSnapshot("mixed-out"); !merr.IsNotFound(err) {
		t.Fatalf("conflicted merge must not persist output, got %v", err)
	}

	// Determinism: the same input on the same state replays identically.
	for i := 0; i < 10; i++ {
		r, err := e.Merge("base", "", "bob", "alice") // reversed side order
		if err != nil {
			t.Fatal(err)
		}
		if r.Explain() != r1.Explain() {
			t.Fatalf("run %d drifted:\n%s\n---\n%s", i, r1.Explain(), r.Explain())
		}
	}
	t.Log("10 replayed runs (reversed side order) produced identical verdicts")
}
