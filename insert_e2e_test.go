package mergeengine

import (
	"testing"

	"github.com/lisama569/merge-engine-go/ingest"
	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// TestEndToEndInsertAndMixedMerge drives the full facade: structured
// insertions are ingested, a mixed merge (conflict + clean changes) keeps
// the clean part, repeated runs are identical, and the chained second
// layer over the persisted output does not drift.
func TestEndToEndInsertAndMixedMerge(t *testing.T) {
	e := New()
	if err := e.SaveSnapshot(&model.Snapshot{Name: "base", Files: map[string]string{
		"doc.txt":  "one\ntwo\nthree\n",
		"note.txt": "hello\n",
	}}); err != nil {
		t.Fatal(err)
	}

	// Alice: pure insertion into doc.txt (before line 2) + append to note.txt.
	envA := &ingest.Envelope{SourceID: "alice", Kind: ingest.SourceStructured,
		Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{
			{Path: "doc.txt", Op: model.OpModify, Hunks: []ingest.StructuredHunk{{
				OldStart: 2, OldEnd: 2, NewStart: 1, NewEnd: 1,
				NewLines: []string{"inserted-by-alice"}}}},
			{Path: "note.txt", Op: model.OpModify, Hunks: []ingest.StructuredHunk{{
				OldStart: 2, OldEnd: 2, NewStart: 1, NewEnd: 1,
				NewLines: []string{"tail"}}}},
		}}}
	if _, err := e.Ingest(envA); err != nil {
		t.Fatalf("ingest alice: %v", err)
	}
	// Bob: overlapping edit on doc.txt line 2 (conflicts with nothing of
	// alice's? it does NOT overlap the insertion anchor) — make it overlap
	// by editing line 2 which alice inserts before.
	envB := &ingest.Envelope{SourceID: "bob", Kind: ingest.SourceStructured,
		Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{
			{Path: "doc.txt", Op: model.OpModify, Hunks: []ingest.StructuredHunk{{
				OldStart: 2, OldEnd: 2, NewStart: 2, NewEnd: 2,
				OldLines: []string{"two"}, NewLines: []string{"TWO"}}}},
		}}}
	if _, err := e.Ingest(envB); err != nil {
		t.Fatalf("ingest bob: %v", err)
	}

	r1, err := e.Merge("base", "", "alice", "bob")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VERDICT run1:\n%s", r1.Explain())
	if r1.OK {
		t.Fatal("insert-at-anchor vs line edit on the anchor line must conflict")
	}
	if len(r1.Conflicts) != 1 || r1.Conflicts[0].Kind != model.ConflictTextOverlap ||
		r1.Conflicts[0].Path != "doc.txt" {
		t.Fatalf("unexpected conflicts: %+v", r1.Conflicts)
	}
	// The non-interfering insertion on note.txt must still be applied.
	if got := r1.Merged.Files["note.txt"]; got != "hello\ntail\n" {
		t.Fatalf("clean insertion lost to the conflict elsewhere: %q", got)
	}
	// Conflicted file keeps base content.
	if got := r1.Merged.Files["doc.txt"]; got != "one\ntwo\nthree\n" {
		t.Fatalf("conflicted file must keep base content, got %q", got)
	}

	// Identical repeats: conclusion, content, conflicts all identical.
	for i := 0; i < 5; i++ {
		r, err := e.Merge("base", "", "bob", "alice") // permuted side order
		if err != nil {
			t.Fatal(err)
		}
		if r.Explain() != r1.Explain() {
			t.Fatalf("run %d drifted:\n%s\n---\n%s", i, r.Explain(), r1.Explain())
		}
		for k, v := range r1.Merged.Files {
			if r.Merged.Files[k] != v {
				t.Fatalf("run %d file %q drifted", i, k)
			}
		}
	}

	// Chained second layer: persist a clean merge, then merge a fresh
	// insertion on top of that output; re-running the chain must not drift.
	envC := &ingest.Envelope{SourceID: "carol", Kind: ingest.SourceStructured,
		Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{
			{Path: "doc.txt", Op: model.OpModify, Hunks: []ingest.StructuredHunk{{
				OldStart: 4, OldEnd: 4, NewStart: 1, NewEnd: 1,
				NewLines: []string{"appended-by-carol"}}}},
		}}}
	if _, err := e.Ingest(envC); err != nil {
		t.Fatalf("ingest carol: %v", err)
	}
	layer1, err := e.Merge("base", "layer1", "alice", "carol")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VERDICT layer1:\n%s", layer1.Explain())
	if !layer1.OK {
		t.Fatalf("alice+carol must be clean: %+v", layer1.Conflicts)
	}
	// Alice inserts before base line 2, carol appends after base line 3;
	// both base-relative anchors must land correctly in one pass.
	wantLayer1 := "one\ninserted-by-alice\ntwo\nthree\nappended-by-carol\n"
	if got := layer1.Merged.Files["doc.txt"]; got != wantLayer1 {
		t.Fatalf("layer1 doc.txt=%q want %q", got, wantLayer1)
	}

	envD := &ingest.Envelope{SourceID: "dave", Kind: ingest.SourceStructured,
		Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{
			{Path: "doc.txt", Op: model.OpModify, Hunks: []ingest.StructuredHunk{{
				OldStart: 6, OldEnd: 6, NewStart: 1, NewEnd: 1,
				NewLines: []string{"appended-by-dave"}}}},
		}}}
	if _, err := e.Ingest(envD); err != nil {
		t.Fatalf("ingest dave: %v", err)
	}
	layer2, err := e.Merge("layer1", "layer2", "dave")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VERDICT layer2 (chained over layer1 output):\n%s", layer2.Explain())
	if !layer2.OK {
		t.Fatalf("layer2 must be clean: %+v", layer2.Conflicts)
	}
	wantLayer2 := wantLayer1 + "appended-by-dave\n"
	if got := layer2.Merged.Files["doc.txt"]; got != wantLayer2 {
		t.Fatalf("layer2 doc.txt=%q want %q", got, wantLayer2)
	}

	// Validation merge: no changes over the chained output reproduces it.
	check, err := e.Merge("layer2", "")
	if err != nil {
		t.Fatal(err)
	}
	if !check.OK || len(check.Applied) != 0 || check.Merged.Files["doc.txt"] != wantLayer2 {
		t.Fatalf("validation merge drifted: %+v", check)
	}

	// Re-running the whole chain from storage yields identical output.
	again1, err := e.Merge("base", "", "alice", "carol")
	if err != nil {
		t.Fatal(err)
	}
	again2Base := again1.Merged.Clone()
	again2Base.Name = "again1"
	if err := e.SaveSnapshot(again2Base); err != nil {
		t.Fatal(err)
	}
	again2, err := e.Merge("again1", "", "dave")
	if err != nil {
		t.Fatal(err)
	}
	if again2.Merged.Files["doc.txt"] != wantLayer2 {
		t.Fatalf("re-run of the chain drifted: %q", again2.Merged.Files["doc.txt"])
	}
}

// TestEndToEndMalformedInsertRejected proves a malformed insertion-shaped
// record is refused at the facade with a distinguishable reason and never
// reaches storage.
func TestEndToEndMalformedInsertRejected(t *testing.T) {
	e := New()
	env := &ingest.Envelope{SourceID: "bad", Kind: ingest.SourceStructured,
		Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{
			{Path: "f", Op: model.OpModify, Hunks: []ingest.StructuredHunk{{
				OldStart: 2, OldEnd: 4, // empty old_lines but not an anchor
				NewStart: 1, NewEnd: 1, NewLines: []string{"x"}}}},
		}}}
	_, err := e.Ingest(env)
	if !merr.IsInvalid(err) {
		t.Fatalf("malformed insertion must be invalid_input, got %v", err)
	}
	t.Logf("REJECTED malformed insertion: %v", err)
	if _, getErr := e.LoadSnapshot("bad"); getErr == nil {
		t.Fatal("rejected input must not reach storage")
	}
}
