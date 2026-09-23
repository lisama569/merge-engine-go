package merge

import (
	"testing"

	"github.com/lisama569/merge-engine-go/model"
)

func TestSingleSidedTypeChangeAutoApplies(t *testing.T) {
	base := baseSnapshot()
	side := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "x.txt", Op: model.OpTypeChange, ChangeType: "file_to_dir"},
	}}
	logThreeWay(t, base, side)
	r := MergeSets(base, side)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("single-sided type change must auto-apply: %+v", r.Conflicts)
	}
	if r.Merged.Has("x.txt") || !r.Merged.Has("x.txt/") {
		t.Fatalf("file_to_dir result wrong: %v", sortedFileKeys(r.Merged.Files))
	}
}

func TestSingleSidedRenameAutoApplies(t *testing.T) {
	base := baseSnapshot()
	side := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "new-name.txt", OldPath: "r.txt", Op: model.OpRename},
	}}
	logThreeWay(t, base, side)
	r := MergeSets(base, side)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("single-sided rename must auto-apply: %+v", r.Conflicts)
	}
	if r.Merged.Has("r.txt") || !r.Merged.Has("new-name.txt") {
		t.Fatalf("rename result wrong: %v", sortedFileKeys(r.Merged.Files))
	}
	if r.Merged.Files["new-name.txt"] != "rename-me\n" {
		t.Fatalf("rename must carry content, got %q", r.Merged.Files["new-name.txt"])
	}
}

func TestReplayWithConflictsIsStable(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "renamed-a", OldPath: "r.txt", Op: model.OpRename},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "renamed-b", OldPath: "r.txt", Op: model.OpRename},
	}}
	logThreeWay(t, base, sideA, sideB)
	r1 := MergeSets(base, sideA, sideB)
	r2 := MergeSets(base, sideB, sideA) // reversed order
	r3 := MergeSets(base, sideA, sideB)
	if r1.OK || r2.OK || r3.OK {
		t.Fatal("rename/rename must conflict on every replay")
	}
	if r1.Explain() != r2.Explain() || r1.Explain() != r3.Explain() {
		t.Fatalf("conflicting replay not stable:\n%s\n---\n%s", r1.Explain(), r2.Explain())
	}
	t.Logf("stable conflicting replay:\n%s", r1.Explain())
}
