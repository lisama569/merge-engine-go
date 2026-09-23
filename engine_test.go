package mergeengine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mergeengine "github.com/lisama569/merge-engine-go"
	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/model"
)

func dump(t *testing.T, tag string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	t.Logf("%s = %s", tag, string(b))
}

func baseSnap() model.Snapshot {
	return model.Snapshot{
		Label: "base",
		Files: map[string]model.FileState{
			"config.txt": {Lines: []string{"a", "b", "c", "d"}},
			"registry": {Entries: map[string]model.StructuredEntry{
				"svc-a": {Type: "service", Value: "v1"},
				"svc-b": {Type: "service", Value: "v2"},
			}},
		},
	}
}

// TestEngineOverlapConflict 端到端：行区间重叠必须被明确标记，目标保持基线。
func TestEngineOverlapConflict(t *testing.T) {
	e := mergeengine.New()
	ctx := context.Background()
	if err := e.LoadSnapshot("base", baseSnap()); err != nil {
		t.Fatal(err)
	}
	alice := model.RawChangeSet{Author: "alice", TextDiffs: []model.RawTextDiff{
		{Path: "config.txt", OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 1, Lines: []string{"ALICE"}},
	}}
	bob := model.RawChangeSet{Author: "bob", TextDiffs: []model.RawTextDiff{
		{Path: "config.txt", OldStart: 2, OldLines: 2, NewStart: 2, NewLines: 2, Lines: []string{"BOB1", "BOB2"}},
	}}
	sa, ra, err := e.Submit(ctx, alice)
	if err != nil || len(ra) != 0 {
		t.Fatalf("alice submit: err=%v rejections=%v", err, ra)
	}
	sb, rb, err := e.Submit(ctx, bob)
	if err != nil || len(rb) != 0 {
		t.Fatalf("bob submit: err=%v rejections=%v", err, rb)
	}
	dump(t, "BASE", baseSnap())
	dump(t, "OURS", sa)
	dump(t, "THEIRS", sb)

	r, err := e.Merge(ctx, "base", "target", []string{"alice", "bob"})
	if err != nil {
		t.Fatal(err)
	}
	dump(t, "MERGE_REPORT", r)

	if !r.HasConflict || r.Conflicts[0].Kind != model.ConflictOverlap {
		t.Fatalf("want overlap conflict, got %+v", r.Conflicts)
	}
	got := r.Merged.Files["config.txt"].Lines
	want := baseSnap().Files["config.txt"].Lines
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("conflicted file must retain base: got %v", got)
	}
	t.Logf("JUDGEMENT: line 2 changed by both alice and bob => overlap conflict; base retained")

	// 幂等重放
	r2, err := e.Replay(ctx, "base", "target", []string{"alice", "bob"})
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if r2.ReplayToken != r.ReplayToken {
		t.Fatalf("replay token drift: %s vs %s", r.ReplayToken, r2.ReplayToken)
	}
}

// TestEngineRenameConflict 端到端：重命名到不同目标 => rename_clash。
func TestEngineRenameConflict(t *testing.T) {
	e := mergeengine.New()
	ctx := context.Background()
	_ = e.LoadSnapshot("base", baseSnap())
	a := model.RawChangeSet{Author: "alice", Structured: []model.RawStructuredChange{
		{Path: "registry", Op: "rename", Key: "svc-a", NewKey: "svc-alpha"},
	}}
	b := model.RawChangeSet{Author: "bob", Structured: []model.RawStructuredChange{
		{Path: "registry", Op: "rename", Key: "svc-a", NewKey: "svc-a1"},
	}}
	sa, _, _ := e.Submit(ctx, a)
	sb, _, _ := e.Submit(ctx, b)
	dump(t, "OURS", sa)
	dump(t, "THEIRS", sb)
	r, err := e.Merge(ctx, "base", "target", []string{"alice", "bob"})
	if err != nil {
		t.Fatal(err)
	}
	dump(t, "REPORT", r)
	if len(r.Conflicts) != 1 || r.Conflicts[0].Kind != model.ConflictRename {
		t.Fatalf("want rename_clash, got %+v", r.Conflicts)
	}
}

// TestEngineInvalidInputRejections 端到端：非法输入逐条拒绝且原因可区分。
func TestEngineInvalidInputRejections(t *testing.T) {
	e := mergeengine.New()
	ctx := context.Background()
	_ = e.LoadSnapshot("base", baseSnap())
	raw := model.RawChangeSet{
		Author: "carol",
		TextDiffs: []model.RawTextDiff{
			{Path: "config.txt", OldStart: 3, OldLines: 0, NewLines: 1, Lines: []string{"x"}},
			{Path: "x.txt", Body: "@@ -1,1 +1,1 @@\n-broken"},
		},
		Structured: []model.RawStructuredChange{
			{Path: "registry", Op: "update"},               // 缺 key
			{Path: "registry", Op: "rename", Key: "svc-b"}, // 缺 new_key
			{Path: "registry", Op: "frobnicate", Key: "k"}, // 未知 op
		},
	}
	set, rejections, err := e.Submit(ctx, raw)
	if err != nil {
		t.Fatalf("invalid records are rejections, not fatal errors: %v", err)
	}
	dump(t, "ACCEPTED", set)
	dump(t, "REJECTIONS", rejections)
	want := []model.RejectCode{
		model.RejectInvalidRange,
		model.RejectInvalidHunkHeader,
		model.RejectMissingKey,
		model.RejectMissingNewKey,
		model.RejectUnknownOp,
	}
	if len(rejections) != len(want) {
		t.Fatalf("want %d rejections got %d: %+v", len(want), len(rejections), rejections)
	}
	for i, w := range want {
		if rejections[i].Reason != w {
			t.Fatalf("rejection %d want %s got %s", i, w, rejections[i].Reason)
		}
	}
	if len(set.Changes) != 0 {
		t.Fatalf("all records illegal, nothing should be accepted: %+v", set.Changes)
	}
	t.Logf("JUDGEMENT: %d malformed records rejected with distinct codes, none silently dropped", len(rejections))
}

// TestEngineIdempotentReplayMerge 端到端：重复执行合并结论稳定。
func TestEngineIdempotentReplayMerge(t *testing.T) {
	e := mergeengine.New()
	ctx := context.Background()
	_ = e.LoadSnapshot("base", baseSnap())
	a := model.RawChangeSet{Author: "alice", TextDiffs: []model.RawTextDiff{
		{Path: "config.txt", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []string{"A"}},
	}}
	b := model.RawChangeSet{Author: "bob", Structured: []model.RawStructuredChange{
		{Path: "registry", Op: "create", Key: "svc-c", NewType: "service", NewValue: "v3"},
	}}
	if _, _, err := e.Submit(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Submit(ctx, b); err != nil {
		t.Fatal(err)
	}
	r1, err := e.Merge(ctx, "base", "target", []string{"alice", "bob"})
	if err != nil {
		t.Fatal(err)
	}
	// 对同一 target 再次 Merge：必须幂等通过
	r2, err := e.Merge(ctx, "base", "target", []string{"alice", "bob"})
	if err != nil {
		t.Fatalf("second merge must be idempotent, got: %v", err)
	}
	if r1.ReplayToken != r2.ReplayToken {
		t.Fatalf("token drift across merges: %s vs %s", r1.ReplayToken, r2.ReplayToken)
	}
	r3, err := e.Replay(ctx, "base", "target", []string{"alice", "bob"})
	if err != nil || r3.ReplayToken != r1.ReplayToken {
		t.Fatalf("replay mismatch: err=%v", err)
	}
	dump(t, "FIRST_MERGE", r1)
	t.Logf("JUDGEMENT: merge/replay token stable = %s", r1.ReplayToken)
}

// TestEngineLibraryErrorNeverLeaks 端到端：库语法错误归一化后才对外。
func TestEngineLibraryErrorNeverLeaks(t *testing.T) {
	e := mergeengine.New()
	ctx := context.Background()
	_ = e.LoadSnapshot("base", baseSnap())
	raw := model.RawChangeSet{Author: "dave", TextDiffs: []model.RawTextDiff{
		{Path: "config.txt", Body: "@@ -1,1 +1,1 @@\n%invalid"},
	}}
	_, _, err := e.Submit(ctx, raw)
	if err == nil {
		t.Fatal("malformed patch syntax must return a classified error")
	}
	code := errs.AsCode(err)
	if code != errs.CodeParseSyntax {
		t.Fatalf("want parse_syntax, got %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "Invalid patch mode") {
		t.Fatalf("original library context must be preserved: %v", err)
	}
	t.Logf("JUDGEMENT: library syntax error normalized as %s with context: %v", code, err)
}
