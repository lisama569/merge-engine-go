package merge

import (
	"strings"
	"testing"

	"github.com/lisama569/merge-engine-go/model"
	"github.com/lisama569/merge-engine-go/storage"
)

// baseFile is the shared base content used by the text-overlap scenarios.
const baseFile = "alpha\nbeta\ngamma\ndelta\n"

func baseSnapshot() *model.Snapshot {
	return &model.Snapshot{Name: "base", Files: map[string]string{
		"f.txt":      baseFile,
		"r.txt":      "rename-me\n",
		"d.txt":      "delete-me\n",
		"x.txt":      "same\n",
		"dir_marker": "v",
	}}
}

// logThreeWay prints base + both sides and the verdict basis, as required
// for explainability in the unit-test logs.
func logThreeWay(t *testing.T, base *model.Snapshot, sides ...*model.ChangeSet) {
	t.Helper()
	t.Logf("BASE snapshot=%q files=%v", base.Name, sortedFileKeys(base.Files))
	for f, c := range base.Files {
		t.Logf("BASE   %q = %q", f, c)
	}
	for _, s := range sides {
		for _, c := range s.Changes {
			t.Logf("SIDE %s: op=%s path=%q old_path=%q hunks=%d",
				s.SourceID, c.Op, c.Path, c.OldPath, len(c.Hunks))
			for i, h := range c.Hunks {
				t.Logf("SIDE %s   hunk[%d] base=[%d,%d] old=%v new=%v",
					s.SourceID, i, h.OldStart, h.OldEnd, h.OldLines, h.NewLines)
			}
		}
	}
}

func logVerdict(t *testing.T, r *model.MergeResult) {
	t.Helper()
	t.Logf("VERDICT ok=%t applied=%d conflicts=%d", r.OK, len(r.Applied), len(r.Conflicts))
	t.Logf("VERDICT REPORT:\n%s", r.Explain())
}

func sortedFileKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort to avoid import noise
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

func modHunk(start, end int, old, new []string) model.Hunk {
	return model.Hunk{OldStart: start, OldEnd: end, NewStart: start,
		NewEnd: start + len(new) - 1, OldLines: old, NewLines: new}
}

func TestDisjointEditsAutoMerge(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(1, 1, []string{"alpha"}, []string{"ALPHA"})},
	}}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(4, 4, []string{"delta"}, []string{"DELTA"})},
	}}}
	logThreeWay(t, base, sideA, sideB)

	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("disjoint edits must auto-merge, got conflicts: %+v", r.Conflicts)
	}
	got := r.Merged.Files["f.txt"]
	if !strings.Contains(got, "ALPHA") || !strings.Contains(got, "DELTA") ||
		strings.Contains(got, "alpha") || strings.Contains(got, "delta") {
		t.Fatalf("both edits must be present: %q", got)
	}
}

func TestDisjointFilesAutoMerge(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "new-a.txt", Op: model.OpAdd, NewContent: "A file\n"},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "d.txt", Op: model.OpDelete},
	}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("disjoint file ops must auto-merge: %+v", r.Conflicts)
	}
	if r.Merged == nil {
		t.Fatal("nil merged snapshot")
	}
	if v, ok := r.Merged.Get("new-a.txt"); !ok || v != "A file\n" {
		t.Fatalf("add missing: %v", r.Merged.Files)
	}
	if r.Merged.Has("d.txt") {
		t.Fatal("delete not applied")
	}
}

func TestOverlappingHunksConflict(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(2, 2, []string{"beta"}, []string{"BETA-A"})},
	}}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(2, 2, []string{"beta"}, []string{"BETA-B"})},
	}}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)

	if r.OK || len(r.Conflicts) != 1 {
		t.Fatalf("want exactly 1 conflict, got ok=%t conflicts=%+v", r.OK, r.Conflicts)
	}
	c := r.Conflicts[0]
	if c.Kind != model.ConflictTextOverlap || c.Path != "f.txt" {
		t.Fatalf("wrong conflict: %+v", c)
	}
	if c.SideA != "alice" || c.SideB != "bob" {
		t.Fatalf("conflict sides wrong: %q vs %q", c.SideA, c.SideB)
	}
	if len(c.Evidence) == 0 {
		t.Fatal("conflict must carry decision evidence")
	}
	// On conflict the merged tree must remain equal to base (no guessing).
	if r.Merged.Files["f.txt"] != baseFile {
		t.Fatalf("conflicted merge must keep base content, got %q", r.Merged.Files["f.txt"])
	}
	t.Logf("REJECTION REASON: %s (%s)", c.Detail, c.Kind)
}

func TestIdenticalOverlapIsIdempotentNotConflict(t *testing.T) {
	base := baseSnapshot()
	edit := model.Change{Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(2, 2, []string{"beta"}, []string{"BETA"})}}
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{edit}}
	sideAClone := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{edit}}
	logThreeWay(t, base, sideA, sideAClone)
	r := MergeSets(base, sideA, sideAClone)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("identical edits from the same source must collapse, not conflict: %+v", r.Conflicts)
	}
	if strings.Count(r.Merged.Files["f.txt"], "BETA") != 1 {
		t.Fatalf("identical edit applied more than once: %q", r.Merged.Files["f.txt"])
	}
}

func TestReplayIdempotencyViaEngine(t *testing.T) {
	st := storage.New()
	if err := st.PutSnapshot(baseSnapshot()); err != nil {
		t.Fatal(err)
	}
	csA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{{
		Path: "f.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(1, 1, []string{"alpha"}, []string{"ALPHA"})},
	}}}
	csB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "new.txt", Op: model.OpAdd, NewContent: "fresh\n"},
	}}
	if err := st.PutChangeSet(csA); err != nil {
		t.Fatal(err)
	}
	if err := st.PutChangeSet(csB); err != nil {
		t.Fatal(err)
	}
	logThreeWay(t, baseSnapshot(), csA, csB)

	eng := NewEngine(st)
	r, err := eng.Replay("base", "merged", 3, "alice", "bob")
	if err != nil {
		t.Fatalf("replay must be stable: %v", err)
	}
	logVerdict(t, r)
	if r.Replays != 3 || !r.OK {
		t.Fatalf("replays=%d ok=%t", r.Replays, r.OK)
	}
	persisted, err := st.GetSnapshot("merged")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Files["f.txt"] != r.Merged.Files["f.txt"] ||
		persisted.Files["new.txt"] != "fresh\n" {
		t.Fatalf("persisted merged snapshot diverged: %v", persisted.Files)
	}
}
