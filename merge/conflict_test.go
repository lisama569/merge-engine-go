package merge

import (
	"testing"

	"github.com/lisama569/merge-engine-go/model"
)

func assertSingleConflict(t *testing.T, r *model.MergeResult, kind model.ConflictKind, path string) model.Conflict {
	t.Helper()
	if r.OK {
		t.Fatalf("expected conflict kind=%s path=%s, but merge was OK", kind, path)
	}
	for _, c := range r.Conflicts {
		if c.Kind == kind && c.Path == path {
			if len(c.Evidence) == 0 {
				t.Fatalf("conflict %s@%s carries no evidence", kind, path)
			}
			t.Logf("REJECTION %s at %s: %s | evidence=%v", c.Kind, c.Path, c.Detail, c.Evidence)
			return c
		}
	}
	t.Fatalf("conflict kind=%s path=%s not found; got %+v", kind, path, r.Conflicts)
	return model.Conflict{}
}

func TestRenameRenameConflict(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "renamed-a.txt", OldPath: "r.txt", Op: model.OpRename},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "renamed-b.txt", OldPath: "r.txt", Op: model.OpRename},
	}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	c := assertSingleConflict(t, r, model.ConflictRenameRename, "r.txt")
	if c.SideA != "alice" || c.SideB != "bob" {
		t.Fatalf("sides wrong: %s/%s", c.SideA, c.SideB)
	}
	if r.Merged.Has("renamed-a.txt") || r.Merged.Has("renamed-b.txt") {
		t.Fatal("conflicted rename must not move the file")
	}
	if !r.Merged.Has("r.txt") {
		t.Fatal("conflicted merge must keep the base path intact")
	}
}

func TestRenameRenameSameDestinationAutoMerges(t *testing.T) {
	base := baseSnapshot()
	ren := model.Change{Path: "renamed.txt", OldPath: "r.txt", Op: model.OpRename}
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{ren}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{ren}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("same rename on both sides must auto-merge once: %+v", r.Conflicts)
	}
	if !r.Merged.Has("renamed.txt") || r.Merged.Has("r.txt") {
		t.Fatalf("rename result wrong: %v", sortedFileKeys(r.Merged.Files))
	}
}

func TestRenameDeleteConflict(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "renamed.txt", OldPath: "r.txt", Op: model.OpRename},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "r.txt", Op: model.OpDelete},
	}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	assertSingleConflict(t, r, model.ConflictRenameDelete, "r.txt")
}

func TestRenameModifyConflict(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "renamed.txt", OldPath: "r.txt", Op: model.OpRename},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{{
		Path: "r.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(1, 1, []string{"rename-me"}, []string{"edited"})},
	}}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	assertSingleConflict(t, r, model.ConflictRenameDelete, "r.txt")
}

func TestModifyDeleteConflict(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{{
		Path: "d.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(1, 1, []string{"delete-me"}, []string{"keep-me"})},
	}}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "d.txt", Op: model.OpDelete},
	}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	assertSingleConflict(t, r, model.ConflictModifyDelete, "d.txt")
	if r.Merged.Has("d.txt") == false {
		t.Fatal("conflicted modify/delete must retain the base file")
	}
}

func TestAddAddDivergentConflict(t *testing.T) {
	base := baseSnapshot() // no "new.txt" in base
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "new.txt", Op: model.OpAdd, NewContent: "from alice\n"},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{
		{Path: "new.txt", Op: model.OpAdd, NewContent: "from bob\n"},
	}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	assertSingleConflict(t, r, model.ConflictAddAdd, "new.txt")
}

func TestAddAddIdenticalAutoMerges(t *testing.T) {
	base := baseSnapshot()
	add := model.Change{Path: "new.txt", Op: model.OpAdd, NewContent: "same content\n"}
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{add}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{add}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	if !r.OK {
		t.Fatalf("identical adds must auto-merge: %+v", r.Conflicts)
	}
	if r.Merged.Files["new.txt"] != "same content\n" {
		t.Fatalf("identical add content wrong: %q", r.Merged.Files["new.txt"])
	}
}

func TestTypeChangeConflict(t *testing.T) {
	base := baseSnapshot()
	sideA := &model.ChangeSet{SourceID: "alice", Changes: []model.Change{
		{Path: "x.txt", Op: model.OpTypeChange, ChangeType: "file_to_dir"},
	}}
	sideB := &model.ChangeSet{SourceID: "bob", Changes: []model.Change{{
		Path: "x.txt", Op: model.OpModify,
		Hunks: []model.Hunk{modHunk(1, 1, []string{"same"}, []string{"edited"})},
	}}}
	logThreeWay(t, base, sideA, sideB)
	r := MergeSets(base, sideA, sideB)
	logVerdict(t, r)
	assertSingleConflict(t, r, model.ConflictTypeMismatch, "x.txt")
}

func TestDeterminismAcrossInputPermutations(t *testing.T) {
	base := baseSnapshot()
	edits := func() []*model.ChangeSet {
		return []*model.ChangeSet{
			{SourceID: "alice", Changes: []model.Change{{
				Path: "f.txt", Op: model.OpModify,
				Hunks: []model.Hunk{modHunk(1, 1, []string{"alpha"}, []string{"ALPHA"})}}}},
			{SourceID: "bob", Changes: []model.Change{{
				Path: "f.txt", Op: model.OpModify,
				Hunks: []model.Hunk{modHunk(4, 4, []string{"delta"}, []string{"DELTA"})}}}},
			{SourceID: "carol", Changes: []model.Change{
				{Path: "d.txt", Op: model.OpDelete}}},
		}
	}
	r1 := MergeSets(base, edits()...)
	reversed := edits()
	reversed[0], reversed[2] = reversed[2], reversed[0]
	r2 := MergeSets(base, reversed...)
	if r1.Explain() != r2.Explain() {
		t.Fatalf("non-deterministic result under side permutation:\n%s\n---\n%s", r1.Explain(), r2.Explain())
	}
	if r1.Merged.Files["f.txt"] != r2.Merged.Files["f.txt"] {
		t.Fatal("merged content differs under permutation")
	}
	t.Log("deterministic report under permutation:\n" + r1.Explain())
}
