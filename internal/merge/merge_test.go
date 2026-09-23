package merge

import (
	"reflect"
	"testing"

	"github.com/lisama569/merge-engine-go/internal/model"
)

func baseSnapshot() model.Snapshot {
	return model.Snapshot{
		Label: "base",
		Files: map[string]model.FileState{
			"a.txt": {Lines: []string{"l1", "l2", "l3", "l4", "l5"}},
			"users": {Entries: map[string]model.StructuredEntry{
				"u1": {Type: "int", Value: "1"},
				"u2": {Type: "str", Value: "two"},
			}},
		},
	}
}

func logThreeWay(t *testing.T, base model.Snapshot, ours, theirs model.ChangeSet, r model.MergeReport) {
	t.Helper()
	t.Logf("BASE label=%s files=%v", base.Label, paths(base))
	t.Logf("OURS(%s) changes=%d", ours.Author, len(ours.Changes))
	for _, c := range ours.Changes {
		t.Logf("  ours: %+v", c)
	}
	t.Logf("THEIRS(%s) changes=%d", theirs.Author, len(theirs.Changes))
	for _, c := range theirs.Changes {
		t.Logf("  theirs: %+v", c)
	}
	t.Logf("VERDICT conflicts=%d auto=%d token=%s",
		len(r.Conflicts), len(r.AutoMerged), r.ReplayToken)
	for _, c := range r.Conflicts {
		t.Logf("  CONFLICT kind=%s path=%s detail=%s authors=%v",
			c.Kind, c.Path, c.Detail, c.Authors)
	}
}

func paths(s model.Snapshot) []string {
	var out []string
	for k := range s.Files {
		out = append(out, k)
	}
	return out
}

func TestOverlappingTextConflict(t *testing.T) {
	base := baseSnapshot()
	ours := model.ChangeSet{Author: "alice", Changes: []model.Change{
		{Path: "a.txt", Source: model.SourceTextDiff,
			Text: &model.TextHunk{Range: model.LineRange{Start: 2, End: 2}, Replacement: []string{"A2"}}},
	}}
	theirs := model.ChangeSet{Author: "bob", Changes: []model.Change{
		{Path: "a.txt", Source: model.SourceTextDiff,
			Text: &model.TextHunk{Range: model.LineRange{Start: 2, End: 3}, Replacement: []string{"B2", "B3"}}},
	}}
	r := ThreeWay(base, ours, theirs)
	logThreeWay(t, base, ours, theirs, r)

	if !r.HasConflict || len(r.Conflicts) != 1 {
		t.Fatalf("want exactly 1 conflict, got %+v", r.Conflicts)
	}
	if r.Conflicts[0].Kind != model.ConflictOverlap {
		t.Fatalf("want overlap conflict, got %s", r.Conflicts[0].Kind)
	}
	// 冲突路径保持基线，绝不任选一方
	got := r.Merged.Files["a.txt"].Lines
	if !reflect.DeepEqual(got, base.Files["a.txt"].Lines) {
		t.Fatalf("conflicted file must retain base content, got %v", got)
	}
}

func TestNonOverlappingAutoMerge(t *testing.T) {
	base := baseSnapshot()
	ours := model.ChangeSet{Author: "alice", Changes: []model.Change{
		{Path: "a.txt", Source: model.SourceTextDiff,
			Text: &model.TextHunk{Range: model.LineRange{Start: 1, End: 1}, Replacement: []string{"L1"}}},
	}}
	theirs := model.ChangeSet{Author: "bob", Changes: []model.Change{
		{Path: "a.txt", Source: model.SourceTextDiff,
			Text: &model.TextHunk{Range: model.LineRange{Start: 4, End: 4}, Replacement: []string{"L4"}}},
	}}
	r := ThreeWay(base, ours, theirs)
	logThreeWay(t, base, ours, theirs, r)

	if r.HasConflict {
		t.Fatalf("non-overlapping changes must auto-merge, got %+v", r.Conflicts)
	}
	want := []string{"L1", "l2", "l3", "L4", "l5"}
	if got := r.Merged.Files["a.txt"].Lines; !reflect.DeepEqual(got, want) {
		t.Fatalf("auto-merge result=%v want=%v", got, want)
	}
}

func TestRenameConflict(t *testing.T) {
	base := baseSnapshot()
	ours := model.ChangeSet{Author: "alice", Changes: []model.Change{
		{Path: "users", Source: model.SourceStructured, Structured: &model.StructuredChange{
			Op: model.StructuredRename, Key: "u1", NewKey: "u1_a",
		}},
	}}
	theirs := model.ChangeSet{Author: "bob", Changes: []model.Change{
		{Path: "users", Source: model.SourceStructured, Structured: &model.StructuredChange{
			Op: model.StructuredRename, Key: "u1", NewKey: "u1_b",
		}},
	}}
	r := ThreeWay(base, ours, theirs)
	logThreeWay(t, base, ours, theirs, r)

	if !r.HasConflict {
		t.Fatal("rename to different targets must conflict")
	}
	found := false
	for _, c := range r.Conflicts {
		if c.Kind == model.ConflictRename {
			found = true
		}
	}
	if !found {
		t.Fatalf("want rename_clash conflict, got %+v", r.Conflicts)
	}
}

func TestDeleteModifyConflict(t *testing.T) {
	base := baseSnapshot()
	del := model.ChangeSet{Author: "alice", Changes: []model.Change{
		{Path: "users", Source: model.SourceStructured, Structured: &model.StructuredChange{
			Op: model.StructuredDelete, Key: "u2",
		}},
	}}
	mod := model.ChangeSet{Author: "bob", Changes: []model.Change{
		{Path: "users", Source: model.SourceStructured, Structured: &model.StructuredChange{
			Op: model.StructuredUpdate, Key: "u2", NewValue: "TWO",
		}},
	}}
	r := ThreeWay(base, del, mod)
	logThreeWay(t, base, del, mod, r)
	if !r.HasConflict || r.Conflicts[0].Kind != model.ConflictDeleteModify {
		t.Fatalf("want delete_modify, got %+v", r.Conflicts)
	}
}

func TestTypeChangeConflict(t *testing.T) {
	base := baseSnapshot()
	a := model.ChangeSet{Author: "alice", Changes: []model.Change{
		{Path: "users", Source: model.SourceStructured, Structured: &model.StructuredChange{
			Op: model.StructuredTypeSet, Key: "u1", NewType: "string",
		}},
	}}
	b := model.ChangeSet{Author: "bob", Changes: []model.Change{
		{Path: "users", Source: model.SourceStructured, Structured: &model.StructuredChange{
			Op: model.StructuredTypeSet, Key: "u1", NewType: "float",
		}},
	}}
	r := ThreeWay(base, a, b)
	logThreeWay(t, base, a, b, r)
	if len(r.Conflicts) != 1 || r.Conflicts[0].Kind != model.ConflictTypeChange {
		t.Fatalf("want single type_change conflict, got %+v", r.Conflicts)
	}
}

func TestIdempotentReplay(t *testing.T) {
	base := baseSnapshot()
	ours := model.ChangeSet{Author: "alice", Changes: []model.Change{
		{Path: "a.txt", Source: model.SourceTextDiff,
			Text: &model.TextHunk{Range: model.LineRange{Start: 5, End: 4}, Replacement: []string{"new"}}},
	}}
	theirs := model.ChangeSet{Author: "bob", Changes: []model.Change{
		{Path: "users", Source: model.SourceStructured, Structured: &model.StructuredChange{
			Op: model.StructuredCreate, Key: "u3", NewType: "int", NewValue: "3",
		}},
	}}
	first := ThreeWay(base, ours, theirs)

	// 再次计算完全相同输入 => token 与合并结果一致
	second := ThreeWay(base, ours, theirs)
	if first.ReplayToken != second.ReplayToken {
		t.Fatalf("replay token drifted: %s vs %s", first.ReplayToken, second.ReplayToken)
	}
	if !reflect.DeepEqual(first.Merged.Files, second.Merged.Files) {
		t.Fatal("replay merged files differ")
	}

	// 对同一快照重复 Apply 同一变更集 => 结果稳定
	once, _ := Apply(base, ours)
	twice, _ := Apply(once, ours)
	if !reflect.DeepEqual(once.Files, twice.Files) {
		t.Fatalf("Apply is not idempotent:\n%v\nvs\n%v", once.Files, twice.Files)
	}
	t.Logf("IDEMPOTENT token=%s once=%v twice=%v",
		first.ReplayToken, once.Files["a.txt"].Lines, twice.Files["a.txt"].Lines)
}

func TestRangesOverlap(t *testing.T) {
	cases := []struct {
		x, y model.LineRange
		want bool
	}{
		{model.LineRange{Start: 2, End: 2}, model.LineRange{Start: 2, End: 3}, true},
		{model.LineRange{Start: 1, End: 1}, model.LineRange{Start: 4, End: 4}, false},
		{model.LineRange{Start: 2, End: 1}, model.LineRange{Start: 2, End: 1}, true}, // 同点插入
		{model.LineRange{Start: 2, End: 1}, model.LineRange{Start: 1, End: 3}, true}, // 插入点落在区间
		{model.LineRange{Start: 5, End: 4}, model.LineRange{Start: 1, End: 1}, false},
	}
	for _, c := range cases {
		if got := rangesOverlap(c.x, c.y); got != c.want {
			t.Fatalf("rangesOverlap(%v,%v)=%v want %v", c.x, c.y, got, c.want)
		}
	}
}
