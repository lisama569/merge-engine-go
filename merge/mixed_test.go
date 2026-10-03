package merge

import (
	"strings"
	"testing"

	"github.com/lisama569/merge-engine-go/model"
)

// mixedFixture builds a base and two sides where one file overlaps
// (unresolvable) while other changes are mutually independent.
func mixedFixture() (base *model.Snapshot, sideA, sideB *model.ChangeSet) {
	base = baseSnapshot()
	base.Files["g.txt"] = "g1\ng2\n"
	sideA = &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		// Overlapping edit on f.txt line 2 -> conflict with bob.
		{Path: "f.txt", Op: model.OpModify,
			Hunks: []model.Hunk{modHunk(2, 2, []string{"beta"}, []string{"BETA-A"})}},
		// Independent add.
		{Path: "new-a.txt", Op: model.OpAdd, NewContent: "from alice\n"},
	}}
	sideB = &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "f.txt", Op: model.OpModify,
			Hunks: []model.Hunk{modHunk(2, 2, []string{"beta"}, []string{"BETA-B"})}},
		// Independent insertion into g.txt.
		{Path: "g.txt", Op: model.OpModify,
			Hunks: []model.Hunk{insHunk(2, "g-insert")}},
	}}
	return base, sideA, sideB
}

// TestMixedConflictAndAutoMergeCoexist proves that one unresolvable
// overlap does not roll the whole merge back to base: independent changes
// still land in the merged snapshot, the conflict is marked explicitly and
// the verdict explains both halves.
func TestMixedConflictAndAutoMergeCoexist(t *testing.T) {
	base, sideA, sideB := mixedFixture()
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)

	// The conflict half: exactly one text_overlap on f.txt with evidence.
	c := assertSingleConflict(t, r, model.ConflictTextOverlap, "f.txt")
	if r.OK {
		t.Fatal("merge with a conflict must report ok=false")
	}
	if r.Merged.Files["f.txt"] != baseFile {
		t.Fatalf("conflicted path must keep base content, got %q", r.Merged.Files["f.txt"])
	}

	// The auto-merge half: independent changes are still applied.
	if got := r.Merged.Files["new-a.txt"]; got != "from alice\n" {
		t.Fatalf("independent add must survive the conflict, got %q", got)
	}
	if got := r.Merged.Files["g.txt"]; got != "g1\ng-insert\ng2\n" {
		t.Fatalf("independent insertion must survive the conflict, got %q", got)
	}
	var addOK, insOK bool
	for _, a := range r.Applied {
		if a.SourceID == "alice" && a.Path == "new-a.txt" && a.Op == model.OpAdd {
			addOK = true
		}
		if a.SourceID == "bob" && a.Path == "g.txt" && a.Op == model.OpModify {
			insOK = true
		}
	}
	if !addOK || !insOK {
		t.Fatalf("applied list must record the auto-merged changes: %+v", r.Applied)
	}

	// The report must expose both the conflict kind and the applied set.
	ex := r.Explain()
	if !strings.Contains(ex, string(model.ConflictTextOverlap)) ||
		!strings.Contains(ex, "new-a.txt") {
		t.Fatalf("report must name the conflict kind and applied paths:\n%s", ex)
	}
	_ = c
}

// TestMixedResultStableAcrossRuns applies the same mixed input repeatedly
// on the same state: verdict, content, conflict marks and applied records
// must be byte-identical every time.
func TestMixedResultStableAcrossRuns(t *testing.T) {
	base, sideA, sideB := mixedFixture()
	first := MergeSets(base, sideA, sideB)
	firstReport := first.Explain()
	for i := 0; i < 25; i++ {
		r := MergeSets(base, sideA, sideB)
		if r.Explain() != firstReport {
			t.Fatalf("run %d drifted in report:\n%s\n---\n%s", i, firstReport, r.Explain())
		}
		for path, want := range first.Merged.Files {
			if got := r.Merged.Files[path]; got != want {
				t.Fatalf("run %d drifted at %q: %q vs %q", i, path, want, got)
			}
		}
		if r.OK != first.OK || len(r.Conflicts) != len(first.Conflicts) ||
			len(r.Applied) != len(first.Applied) {
			t.Fatalf("run %d drifted in verdict shape", i)
		}
	}
	t.Logf("25 identical runs; stable report:\n%s", firstReport)
}

// TestChainedTwoLayerMerge chains two merges: layer 2 takes layer 1's
// output as its base with fresh change sets formulated against it. The
// chained result must be deterministic and reproducible.
func TestChainedTwoLayerMerge(t *testing.T) {
	base, sideA, sideB := mixedFixture()
	// Fix the conflict away for a clean layer 1: make bob's f.txt edit
	// disjoint from alice's.
	sideB.Changes[0].Hunks[0] = modHunk(4, 4, []string{"delta"}, []string{"DELTA"})
	logThreeWay(t, base, sideA, sideB)
	layer1 := MergeSets(base, sideA, sideB)
	logVerdict(t, layer1)
	if !layer1.OK {
		t.Fatalf("layer 1 must be clean: %+v", layer1.Conflicts)
	}

	l1Base := layer1.Merged.Clone()
	l1Base.Name = "layer1"
	// Layer 2 changes are formulated against layer 1's output.
	carol := &model.ChangeSet{SourceID: "carol", Changes: []model.Change{
		{Path: "f.txt", Op: model.OpModify,
			Hunks: []model.Hunk{insHunk(1, "header")}},
	}}
	dave := &model.ChangeSet{SourceID: "dave", Changes: []model.Change{
		{Path: "g.txt", Op: model.OpModify,
			Hunks: []model.Hunk{modHunk(1, 1, []string{"g1"}, []string{"G1"})}},
	}}
	logThreeWay(t, l1Base, carol, dave)
	layer2 := MergeSets(l1Base, carol, dave)
	logVerdict(t, layer2)
	if !layer2.OK {
		t.Fatalf("layer 2 must be clean: %+v", layer2.Conflicts)
	}

	// Re-running the whole chain yields the identical final state.
	again1 := MergeSets(base, sideA, sideB)
	again1.Merged.Name = "layer1"
	again2 := MergeSets(again1.Merged, carol, dave)
	if layer2.Explain() != again2.Explain() {
		t.Fatalf("chained merge drifted:\n%s\n---\n%s", layer2.Explain(), again2.Explain())
	}
	for path, want := range layer2.Merged.Files {
		if got := again2.Merged.Files[path]; got != want {
			t.Fatalf("chained merge drifted at %q: %q vs %q", path, want, got)
		}
	}
	got := layer2.Merged.Files["f.txt"]
	if !strings.HasPrefix(got, "header\n") || !strings.Contains(got, "BETA-A") ||
		!strings.Contains(got, "DELTA") {
		t.Fatalf("layered result must contain every layer's edits: %q", got)
	}
	if layer2.Merged.Files["g.txt"] != "G1\ng-insert\ng2\n" {
		t.Fatalf("layered g.txt wrong: %q", layer2.Merged.Files["g.txt"])
	}
	t.Logf("chained two-layer merge stable; final f.txt=%q", got)
}

// TestPartialHunkAcceptance proves hunk-granular reconciliation: within
// one file, the overlapping hunk pair conflicts while each side's
// remaining clean hunks still merge.
func TestPartialHunkAcceptance(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{
			modHunk(1, 1, []string{"alpha"}, []string{"ALPHA"}),   // clean
			modHunk(3, 3, []string{"gamma"}, []string{"GAMMA-A"}), // overlaps bob
		}}}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{
			modHunk(3, 3, []string{"gamma"}, []string{"GAMMA-B"}), // overlaps alice
			insHunk(5, "tail"), // clean
		}}}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)

	assertSingleConflict(t, r, model.ConflictTextOverlap, "f.txt")
	// Clean hunks from BOTH sides survive; the conflicted line keeps base.
	want := "ALPHA\nbeta\ngamma\ndelta\ntail\n"
	if got := r.Merged.Files["f.txt"]; got != want {
		t.Fatalf("partial merge wrong: got %q want %q", got, want)
	}
	if len(r.Applied) != 2 {
		t.Fatalf("both sides' partial modifies must be recorded, got %+v", r.Applied)
	}
}
