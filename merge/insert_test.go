package merge

import (
	"strings"
	"testing"

	"github.com/lisama569/merge-engine-go/model"
	"github.com/lisama569/merge-engine-go/storage"
)

// insHunk builds a pure-insertion hunk: NewLines are inserted before base
// line `anchor` (anchor 0 = beginning/empty file, anchor > line count =
// append at end). No base line is modified or deleted.
func insHunk(anchor int, newLines ...string) model.Hunk {
	return model.Hunk{
		OldStart: anchor, OldEnd: anchor,
		NewStart: 1, NewEnd: len(newLines),
		NewLines: newLines,
	}
}

func insertSide(id, path string, anchor int, newLines ...string) *model.ChangeSet {
	return &model.ChangeSet{SourceID: id, Changes: []model.Change{{
		Path: path, Op: model.OpModify, Hunks: []model.Hunk{insHunk(anchor, newLines...)}}}}
}

func TestSingleSideInsertMiddle(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"f.txt": "one\ntwo\nthree\n",
	}}
	side := insertSide("alice", "f.txt", 2, "inserted")
	logThreeWay(t, base, side)
	r := MergeSets(base, side)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("single-side insert must auto-merge: %+v", r.Conflicts)
	}
	want := "one\ninserted\ntwo\nthree\n"
	if r.Merged.Files["f.txt"] != want {
		t.Fatalf("insert result=%q want %q", r.Merged.Files["f.txt"], want)
	}
}

func TestInsertAtEndOfFile(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"f.txt": "one\ntwo\n",
	}}
	// Anchor 3 is past the last base line: append at end.
	side := insertSide("alice", "f.txt", 3, "three", "four")
	logThreeWay(t, base, side)
	r := MergeSets(base, side)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("end insert must auto-merge: %+v", r.Conflicts)
	}
	want := "one\ntwo\nthree\nfour\n"
	if r.Merged.Files["f.txt"] != want {
		t.Fatalf("end insert result=%q want %q", r.Merged.Files["f.txt"], want)
	}
}

func TestInsertIntoEmptyFile(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"empty.txt": "",
	}}
	side := insertSide("alice", "empty.txt", 0, "first", "second")
	logThreeWay(t, base, side)
	r := MergeSets(base, side)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("empty-file insert must auto-merge: %+v", r.Conflicts)
	}
	if got := r.Merged.Files["empty.txt"]; got != "first\nsecond" {
		t.Fatalf("empty-file insert result=%q", got)
	}
}

func TestBothSidesInsertSameAnchorDifferentContentConflicts(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"f.txt": "one\ntwo\n",
	}}
	sideA := insertSide("alice", "f.txt", 2, "from-alice")
	sideB := insertSide("bob", "f.txt", 2, "from-bob")
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	c := assertSingleConflict(t, r, model.ConflictTextOverlap, "f.txt")
	if c.SideA != "alice" || c.SideB != "bob" {
		t.Fatalf("sides wrong: %s/%s", c.SideA, c.SideB)
	}
	// The conflicted file keeps base content; neither insertion is guessed.
	if r.Merged.Files["f.txt"] != "one\ntwo\n" {
		t.Fatalf("conflicted insert must keep base, got %q", r.Merged.Files["f.txt"])
	}
}

func TestBothSidesInsertIdenticalContentAutoMergesOnce(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"f.txt": "one\ntwo\n",
	}}
	sideA := insertSide("alice", "f.txt", 2, "same-line")
	sideB := insertSide("bob", "f.txt", 2, "same-line")
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("identical inserts must auto-merge, not conflict: %+v", r.Conflicts)
	}
	if strings.Count(r.Merged.Files["f.txt"], "same-line") != 1 {
		t.Fatalf("identical insert applied more than once: %q", r.Merged.Files["f.txt"])
	}
}

func TestInsertAndDisjointModifyAutoMerge(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"f.txt": "one\ntwo\nthree\n",
	}}
	sideA := insertSide("alice", "f.txt", 4, "tail") // append at end
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(1, 1, []string{"one"}, []string{"ONE"})},
	}}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("disjoint insert+modify must auto-merge: %+v", r.Conflicts)
	}
	want := "ONE\ntwo\nthree\ntail\n"
	if r.Merged.Files["f.txt"] != want {
		t.Fatalf("result=%q want %q", r.Merged.Files["f.txt"], want)
	}
}

// TestMixedConflictAndAutoMerge is the core new guarantee: one merge may
// contain both an unresolvable overlap and independent clean changes; the
// clean part must land in the merged tree instead of rolling back to base.
func TestMixedConflictAndAutoMerge(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"conflicted.txt": "a\nb\nc\n",
		"clean.txt":      "x\ny\n",
	}}
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "conflicted.txt", Op: model.OpModify,
			Hunks: []model.Hunk{modHunk(2, 2, []string{"b"}, []string{"B-alice"})}},
		{Path: "clean.txt", Op: model.OpModify,
			Hunks: []model.Hunk{insHunk(2, "inserted-by-alice")}},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "conflicted.txt", Op: model.OpModify,
			Hunks: []model.Hunk{modHunk(2, 2, []string{"b"}, []string{"B-bob"})}},
		{Path: "added.txt", Op: model.OpAdd, NewContent: "brand new\n"},
	}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)

	// Conflict part: clearly marked with kind, path, sides and evidence.
	assertSingleConflict(t, r, model.ConflictTextOverlap, "conflicted.txt")
	if r.OK {
		t.Fatal("mixed merge with a conflict must report ok=false")
	}
	// ...while the non-interfering part is still applied.
	if got := r.Merged.Files["clean.txt"]; got != "x\ninserted-by-alice\ny\n" {
		t.Fatalf("clean insert must survive the conflict elsewhere, got %q", got)
	}
	if got := r.Merged.Files["added.txt"]; got != "brand new\n" {
		t.Fatalf("clean add must survive the conflict elsewhere, got %q", got)
	}
	// Conflicted path is NOT rolled back to a guessed state: base content.
	if got := r.Merged.Files["conflicted.txt"]; got != "a\nb\nc\n" {
		t.Fatalf("conflicted path must keep base content, got %q", got)
	}
	// Applied records confirm exactly which changes took effect.
	var sawInsert, sawAdd bool
	for _, a := range r.Applied {
		t.Logf("APPLIED [%s] %s %s", a.SourceID, a.Op, a.Path)
		sawInsert = sawInsert || (a.SourceID == "alice" && a.Path == "clean.txt")
		sawAdd = sawAdd || (a.SourceID == "bob" && a.Path == "added.txt")
		if a.Path == "conflicted.txt" {
			t.Fatalf("conflicted change must not appear in applied: %+v", a)
		}
	}
	if !sawInsert || !sawAdd {
		t.Fatalf("applied must list both clean changes: %+v", r.Applied)
	}
}

// TestMixedMergeReplayStability runs the same mixed input repeatedly and
// through the engine replay path; every run must be bit-identical.
func TestMixedMergeReplayStability(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"conflicted.txt": "a\nb\nc\n",
		"clean.txt":      "x\ny\n",
	}}
	mk := func() (*model.ChangeSet, *model.ChangeSet) {
		return &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
				{Path: "conflicted.txt", Op: model.OpModify,
					Hunks: []model.Hunk{modHunk(2, 2, []string{"b"}, []string{"B-alice"})}},
				{Path: "clean.txt", Op: model.OpModify,
					Hunks: []model.Hunk{insHunk(3, "tail-by-alice")}},
			}}, &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
				{Path: "conflicted.txt", Op: model.OpModify,
					Hunks: []model.Hunk{modHunk(2, 2, []string{"b"}, []string{"B-bob"})}},
			}}
	}
	a1, b1 := mk()
	first := MergeSets(base, a1, b1)
	logThreeWay(t, base, a1, b1)
	logVerdict(t, first)
	for i := 0; i < 5; i++ {
		a, b := mk()
		r := MergeSets(base, a, b)
		if r.Explain() != first.Explain() {
			t.Fatalf("run %d drifted:\n%s\n---\n%s", i, r.Explain(), first.Explain())
		}
		for k, v := range first.Merged.Files {
			if r.Merged.Files[k] != v {
				t.Fatalf("run %d file %q drifted: %q vs %q", i, k, r.Merged.Files[k], v)
			}
		}
	}

	// Same input through the storage-backed engine replay path.
	st := storage.New()
	if err := st.PutSnapshot(base); err != nil {
		t.Fatal(err)
	}
	a2, b2 := mk()
	if err := st.PutChangeSet(a2); err != nil {
		t.Fatal(err)
	}
	if err := st.PutChangeSet(b2); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(st)
	r, err := eng.Replay("base", "", 4, "alice", "bob")
	if err != nil {
		t.Fatalf("mixed replay must be stable: %v", err)
	}
	logVerdict(t, r)
	if r.OK || r.Replays != 4 {
		t.Fatalf("replays=%d ok=%t", r.Replays, r.OK)
	}
	// Explain embeds the replay count; normalize it before comparing.
	expect := *first
	expect.Replays = 4
	if r.Explain() != expect.Explain() {
		t.Fatalf("engine replay drifted from pure merge:\n%s\n---\n%s", r.Explain(), expect.Explain())
	}
}

// TestChainedMergeNoDrift layers two merges: the output of layer 1 becomes
// the base of layer 2, and a validation merge with no changes over the
// final output must reproduce it exactly.
func TestChainedMergeNoDrift(t *testing.T) {
	base := &model.Snapshot{Name: "base", Files: map[string]string{
		"f.txt": "one\ntwo\n",
	}}
	layer1Side := insertSide("alice", "f.txt", 2, "middle")
	layer1 := MergeSets(base, layer1Side)
	logThreeWay(t, base, layer1Side)
	logVerdict(t, layer1)
	if !layer1.OK {
		t.Fatalf("layer 1 must be clean: %+v", layer1.Conflicts)
	}

	layer2Base := layer1.Merged.Clone()
	layer2Base.Name = "layer1-out"
	layer2Side := insertSide("bob", "f.txt", 4, "tail")
	layer2 := MergeSets(layer2Base, layer2Side)
	logThreeWay(t, layer2Base, layer2Side)
	logVerdict(t, layer2)
	if !layer2.OK {
		t.Fatalf("layer 2 must be clean: %+v", layer2.Conflicts)
	}
	want := "one\nmiddle\ntwo\ntail\n"
	if layer2.Merged.Files["f.txt"] != want {
		t.Fatalf("chained result=%q want %q", layer2.Merged.Files["f.txt"], want)
	}

	// Validation merge: no changes over the chained output must not drift.
	validationBase := layer2.Merged.Clone()
	validationBase.Name = "layer2-out"
	check := MergeSets(validationBase)
	logVerdict(t, check)
	if !check.OK || len(check.Applied) != 0 || len(check.Conflicts) != 0 {
		t.Fatalf("validation merge must be a clean no-op: %+v", check)
	}
	if check.Merged.Files["f.txt"] != want {
		t.Fatalf("validation merge drifted: %q want %q", check.Merged.Files["f.txt"], want)
	}

	// Re-running the whole chain yields identical output.
	again1 := MergeSets(base, insertSide("alice", "f.txt", 2, "middle"))
	again1.Merged.Name = "layer1-out"
	again2 := MergeSets(again1.Merged, insertSide("bob", "f.txt", 4, "tail"))
	if again2.Merged.Files["f.txt"] != want {
		t.Fatalf("re-run of the chain drifted: %q", again2.Merged.Files["f.txt"])
	}
}
