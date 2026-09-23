package diffparse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/model"
)

func TestNormalizeStructuredRejections(t *testing.T) {
	cases := []struct {
		name   string
		raw    model.RawStructuredChange
		reason model.RejectCode
	}{
		{"missing_path", model.RawStructuredChange{Op: "create", Key: "k"}, model.RejectMissingPath},
		{"unknown_op", model.RawStructuredChange{Path: "f", Op: "explode", Key: "k"}, model.RejectUnknownOp},
		{"missing_key", model.RawStructuredChange{Path: "f", Op: "create"}, model.RejectMissingKey},
		{"rename_missing_newkey", model.RawStructuredChange{Path: "f", Op: "rename", Key: "k"}, model.RejectMissingNewKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rc := validateStructured(tc.raw); rc != tc.reason {
				t.Fatalf("want %s, got %s", tc.reason, rc)
			}
		})
	}
}

func TestNormalizeTextRangeRejections(t *testing.T) {
	p := NewGoDiffParser()
	raw := model.RawChangeSet{
		Author: "alice",
		TextDiffs: []model.RawTextDiff{
			{Path: "", OldStart: 1, OldLines: 1},                                // 缺路径
			{Path: "a.txt", OldStart: 0, OldLines: 1, NewStart: 1},              // 非法起点
			{Path: "b.txt", OldStart: 3, OldLines: 2, NewStart: 3, NewLines: 2}, // 声明替换但缺内容
			{Path: "c.txt", Body: "garbage without hunk header"},                // 无 hunk 头
			{Path: "d.txt", Body: "@@ -1,2 +1,2 @@\n+only-add"},                 // 计数不符
			{Path: "ok.txt", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []string{"x"}},
		},
	}
	set, rejects, err := p.Normalize(context.Background(), raw)
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	wantCodes := []model.RejectCode{
		model.RejectMissingPath,
		model.RejectInvalidRange,
		model.RejectMissingDiffBody,
		model.RejectInvalidHunkHeader,
		model.RejectInvalidHunkHeader,
	}
	if len(rejects) != len(wantCodes) {
		t.Fatalf("want %d rejections, got %d: %+v", len(wantCodes), len(rejects), rejects)
	}
	for i, want := range wantCodes {
		if rejects[i].Reason != want {
			t.Fatalf("rejection %d: want %s got %s (detail=%s)", i, want, rejects[i].Reason, rejects[i].Detail)
		}
	}
	if len(set.Changes) != 1 || set.Changes[0].Path != "ok.txt" {
		t.Fatalf("only the legal diff should survive, got %+v", set.Changes)
	}
}

func TestParseSyntaxMappedFromThirdParty(t *testing.T) {
	p := NewGoDiffParser()
	raw := model.RawChangeSet{
		Author: "a",
		TextDiffs: []model.RawTextDiff{{
			Path: "f",
			Body: "@@ -1,1 +1,1 @@\n%unrecognized-prefix",
		}},
	}
	_, _, err := p.Normalize(context.Background(), raw)
	if err == nil {
		t.Fatal("expected syntax error to be surfaced as engine error")
	}
	code := errs.AsCode(err)
	t.Logf("syntax case err = %v (code=%s)", err, code)
	if code != errs.CodeParseSyntax {
		t.Fatalf("error must be classified as parse_syntax, got %s: %v", code, err)
	}
	if !strings.Contains(err.Error(), "third-party") {
		t.Fatalf("syntax error should retain third-party context: %v", err)
	}
}

func TestEnsureEngineErrorPanic(t *testing.T) {
	err := EnsureEngineError("boom-in-library", errs.CodeParseLibrary)
	if errs.AsCode(err) != errs.CodeParseLibrary {
		t.Fatalf("want parse_library, got %v", err)
	}
	var ee *errs.EngineError
	if !errors.As(err, &ee) || ee.Cause == nil || !strings.Contains(ee.Cause.Error(), "boom") {
		t.Fatalf("original panic context must be preserved: %+v", ee)
	}
}

func TestValidateLineRange(t *testing.T) {
	if err := ValidateLineRange(model.LineRange{Start: 1, End: 0}); err != nil {
		t.Fatalf("pure insert point [1,0] should be legal: %v", err)
	}
	if errs.AsCode(ValidateLineRange(model.LineRange{Start: 0, End: 0})) != errs.CodeInvalidInput {
		t.Fatal("start<1 must be invalid_input")
	}
}
