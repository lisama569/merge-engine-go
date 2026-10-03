package merge

import (
	"strings"
	"testing"

	"github.com/lisama569/merge-engine-go/model"
	"github.com/lisama569/merge-engine-go/storage"
)

// insHunk builds a pure-insertion hunk: no old lines, anchored before base
// line `anchor` (anchor==0 inserts into an empty file / before line 1,
// anchor==len(base)+1 appends at end of file).
func insHunk(anchor int, newLines ...string) model.Hunk {
	return model.Hunk{OldStart: anchor, OldEnd: anchor,
		NewStart: anchor, NewEnd: anchor + len(newLines) - 1,
		NewLines: newLines}
}

func insSide(id, path string, anchor int, newLines ...string) *model.ChangeSet {
	return &model.ChangeSet{SourceID: id, Changes: []model.Change{{
		Path: path, Op: model.OpModify, Hunks: []model.Hunk{insHunk(anchor, newLines...)}}}}
}

func TestSingleSideInsertionAutoMerges(t *testing.T) {
	base := baseSnapshot()
	sideA := insSide("alice", "f.txt", 2, "inserted")
	logThreeWay(t, base, sideA)
	r := MergeSets(base, sideA)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("single-side insertion must auto-merge: %+v", r.Conflicts)
	}
	want := "alpha\ninserted\nbeta\ngamma\ndelta\n"
	if got := r.Merged.Files["f.txt"]; got != want {
		t.Fatalf("merged=%q want %q", got, want)
	}
	if len(r.Applied) != 1 || r.Applied[0].SourceID != "alice" {
		t.Fatalf("applied must record the insertion: %+v", r.Applied)
	}
}

func TestInsertionAtEndOfFile(t *testing.T) {
	base := baseSnapshot() // f.txt has 4 lines
	sideA := insSide("alice", "f.txt", 5, "tail")
	logThreeWay(t, base, sideA)
	r := MergeSets(base, sideA)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("end-of-file insertion must auto-merge: %+v", r.Conflicts)
	}
	want := "alpha\nbeta\ngamma\ndelta\ntail\n"
	if got := r.Merged.Files["f.txt"]; got != want {
		t.Fatalf("merged=%q want %q", got, want)
	}
}

func TestInsertionIntoEmptyFile(t *testing.T) {
	base := baseSnapshot()
	base.Files["empty.txt"] = ""
	sideA := insSide("alice", "empty.txt", 0, "first", "second")
	logThreeWay(t, base, sideA)
	r := MergeSets(base, sideA)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("insertion into empty file must auto-merge: %+v", r.Conflicts)
	}
	if got := r.Merged.Files["empty.txt"]; got != "first\nsecond" {
		t.Fatalf("merged=%q", got)
	}
}

func TestIdenticalInsertionBothSidesAppliesOnce(t *testing.T) {
	base := baseSnapshot()
	sideA := insSide("alice", "f.txt", 2, "same-insert")
	sideB := insSide("bob", "f.txt", 2, "same-insert")
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("identical insertions must collapse, not conflict: %+v", r.Conflicts)
	}
	if strings.Count(r.Merged.Files["f.txt"], "same-insert") != 1 {
		t.Fatalf("identical insertion applied more than once: %q", r.Merged.Files["f.txt"])
	}
}

func TestDivergentInsertionSameAnchorConflicts(t *testing.T) {
	base := baseSnapshot()
	sideA := insSide("alice", "f.txt", 2, "A-insert")
	sideB := insSide("bob", "f.txt", 2, "B-insert")
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	c := assertSingleConflict(t, r, model.ConflictTextOverlap, "f.txt")
	if len(c.Evidence) == 0 {
		t.Fatal("insertion conflict must carry decision evidence")
	}
	if r.Merged.Files["f.txt"] != baseFile {
		t.Fatalf("conflicted file must keep base content, got %q", r.Merged.Files["f.txt"])
	}
}

func TestInsertionsAtDifferentAnchorsAutoMerge(t *testing.T) {
	base := baseSnapshot()
	sideA := insSide("alice", "f.txt", 1, "top")
	sideB := insSide("bob", "f.txt", 4, "mid")
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("disjoint insertions must auto-merge: %+v", r.Conflicts)
	}
	want := "top\nalpha\nbeta\ngamma\nmid\ndelta\n"
	if got := r.Merged.Files["f.txt"]; got != want {
		t.Fatalf("merged=%q want %q", got, want)
	}
}

func TestInsertionPlusDisjointModifyAutoMerge(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(1, 1, []string{"alpha"}, []string{"ALPHA"})}}}}
	sideB := insSide("bob", "f.txt", 3, "inserted-by-bob")
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("insertion disjoint from modify must auto-merge: %+v", r.Conflicts)
	}
	want := "ALPHA\nbeta\ninserted-by-bob\ngamma\ndelta\n"
	if got := r.Merged.Files["f.txt"]; got != want {
		t.Fatalf("merged=%q want %q", got, want)
	}
}

func TestInsertionAtModifiedAnchorConflicts(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(2, 2, []string{"beta"}, []string{"BETA"})}}}}
	sideB := insSide("bob", "f.txt", 2, "before-beta")
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	// Conservative: an insertion anchored exactly on a line the other side
	// rewrites is a text_overlap conflict, never a guessed ordering.
	assertSingleConflict(t, r, model.ConflictTextOverlap, "f.txt")
}

func TestInsertionReplayStabilityViaEngine(t *testing.T) {
	st := storage.New()
	if err := st.PutSnapshot(baseSnapshot()); err != nil {
		t.Fatal(err)
	}
	csA := insSide("alice", "f.txt", 2, "A-insert")
	csB := insSide("bob", "f.txt", 5, "tail")
	if err := st.PutChangeSet(csA); err != nil {
		t.Fatal(err)
	}
	if err := st.PutChangeSet(csB); err != nil {
		t.Fatal(err)
	}
	logThreeWay(t, baseSnapshot(), csA, csB)
	eng := NewEngine(st)
	r, err := eng.Replay("base", "merged", 5, "alice", "bob")
	if err != nil {
		t.Fatalf("insertion replay must be stable: %v", err)
	}
	logVerdict(t, r)
	want := "alpha\nA-insert\nbeta\ngamma\ndelta\ntail\n"
	if got := r.Merged.Files["f.txt"]; got != want {
		t.Fatalf("merged=%q want %q", got, want)
	}
	persisted, err := st.GetSnapshot("merged")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Files["f.txt"] != want {
		t.Fatalf("persisted snapshot drifted: %q", persisted.Files["f.txt"])
	}
}

// TestValidationRemergeDoesNotDrift takes the merged output as the new base
// and re-applies the same change sets: insertions must not stack and the
// content must stay byte-identical.
func TestValidationRemergeDoesNotDrift(t *testing.T) {
	base := baseSnapshot()
	sideA := insSide("alice", "f.txt", 2, "A-insert")
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "renamed.txt", OldPath: "r.txt", Op: model.OpRename},
		{Path: "f.txt", Op: model.OpModify,
			Hunks: []model.Hunk{modHunk(4, 4, []string{"delta"}, []string{"DELTA"})}},
	}}
	logThreeWay(t, base, sideA, sideB)
	r1 := MergeSets(base, sideA, sideB)
	logVerdict(t, r1)
	if !r1.OK {
		t.Fatalf("first merge must be clean: %+v", r1.Conflicts)
	}

	// Validation-style re-merge: same change sets over the previous output.
	m1 := r1.Merged.Clone()
	m1.Name = "m1"
	t.Logf("LAYER-2 BASE (previous output): %v", m1.Files)
	r2 := MergeSets(m1, sideA, sideB)
	logVerdict(t, r2)
	for path, want := range r1.Merged.Files {
		if got := r2.Merged.Files[path]; got != want {
			t.Fatalf("re-merge drifted at %q:\nfirst=%q\nsecond=%q", path, want, got)
		}
	}
	if len(r1.Merged.Files) != len(r2.Merged.Files) {
		t.Fatalf("re-merge changed file set: %v vs %v",
			sortedFileKeys(r1.Merged.Files), sortedFileKeys(r2.Merged.Files))
	}
	if strings.Count(r2.Merged.Files["f.txt"], "A-insert") != 1 {
		t.Fatalf("insertion stacked on re-merge: %q", r2.Merged.Files["f.txt"])
	}
	if got := r2.Merged.Files["renamed.txt"]; got != "rename-me\n" {
		t.Fatalf("rename content clobbered on re-merge: %q", got)
	}
	t.Log("validation re-merge is drift-free")
}
