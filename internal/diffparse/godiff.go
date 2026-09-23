package diffparse

import (
	"context"
	"fmt"
	"strings"

	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/model"
	dmp "github.com/sergi/go-diff/diffmatchpatch"
)

// patchFromTextFn 是第三方库 go-diff 的调用边界签名，
// 便于在测试中注入"库内部 panic"故障。
type patchFromTextFn func(body string) ([]dmp.Patch, error)

// goDiffParser 基于 github.com/sergi/go-diff 的归一化实现。
type goDiffParser struct {
	dmp       *dmp.DiffMatchPatch
	patchFunc patchFromTextFn
}

// NewGoDiffParser 构造基于 go-diff 的解析器。
func NewGoDiffParser() Parser {
	d := dmp.New()
	return &goDiffParser{
		dmp:       d,
		patchFunc: d.PatchFromText,
	}
}

// newGoDiffParserWithFault 使用自定义库调用边界构造解析器（故障注入测试用）。
func newGoDiffParserWithFault(fn patchFromTextFn) *goDiffParser {
	return &goDiffParser{dmp: dmp.New(), patchFunc: fn}
}

// Normalize 实现 Parser 接口。
//
// 非法的逐条输入以 Rejection 返回（带可区分原因码）；仅当底层库出现
// 致命错误（语法错误/内部异常）时返回归一化后的非空 error。
func (p *goDiffParser) Normalize(ctx context.Context, raw model.RawChangeSet) (set *model.ChangeSet, rejects []model.Rejection, err error) {
	set = &model.ChangeSet{Author: raw.Author}

	// 库内部异常（panic）兜底：任何来自第三方库的崩溃都映射为
	// CodeParseLibrary，并保留原始上下文，绝不透出未处理异常。
	defer func() {
		if r := recover(); r != nil {
			err = EnsureEngineError(r, errs.CodeParseLibrary)
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, nil, errs.New(errs.CodeInvalidInput, "context cancelled", err)
	}

	// 文本差异
	for i, td := range raw.TextDiffs {
		rc := validateText(td)
		if rc != "" {
			rejects = append(rejects, model.Rejection{
				Index: i, Path: td.Path, Reason: rc,
				Detail: rejectDetail(rc, td.Path),
			})
			continue
		}
		if strings.TrimSpace(td.Body) != "" {
			hunks, rj, perr := p.parseBody(td)
			if perr != nil {
				return nil, nil, perr // 库致命错误，立即归一化返回
			}
			if rj != (model.Rejection{}) {
				rejects = append(rejects, withIndex(rj, i))
				continue
			}
			for _, h := range hunks {
				set.Changes = append(set.Changes, model.Change{
					Path: td.Path, Source: model.SourceTextDiff, Text: &h,
				})
			}
			continue
		}
		h, ok := hunkFromFields(td)
		if !ok {
			rejects = append(rejects, model.Rejection{
				Index: i, Path: td.Path, Reason: model.RejectInvalidRange,
				Detail: rejectDetail(model.RejectInvalidRange, td.Path),
			})
			continue
		}
		set.Changes = append(set.Changes, model.Change{
			Path: td.Path, Source: model.SourceTextDiff, Text: &h,
		})
	}

	// 结构化变更
	for j, sc := range raw.Structured {
		rc := validateStructured(sc)
		if rc != "" {
			rejects = append(rejects, model.Rejection{
				Index: len(raw.TextDiffs) + j, Path: sc.Path, Reason: rc,
				Detail: rejectDetail(rc, sc.Path),
			})
			continue
		}
		set.Changes = append(set.Changes, normalizeStructured(sc))
	}

	return set, rejects, nil
}

// parseBody 解析 unified diff 文本体。
//
// 流程：先由第三方库 go-diff PatchFromText 做语法级解析（其返回的
// error 映射为 CodeParseSyntax）；再逐 hunk 核对行计数与前缀语义，
// 计数不符属于输入非法而非库异常（CodeInvalidInput 逐条拒绝）。
func (p *goDiffParser) parseBody(td model.RawTextDiff) (hunks []model.TextHunk, rej model.Rejection, err error) {
	body := strings.TrimSpace(td.Body)

	// —— 第三方库调用边界：只允许归一化错误离开 ——
	patches, perr := p.safePatchFromText(body)
	if perr != nil {
		return nil, model.Rejection{}, perr
	}
	_ = patches // go-diff 负责语法闸门；hunk 语义以行计数为准

	lines := strings.Split(body, "\n")
	idx := 0
	for idx < len(lines) {
		if !strings.HasPrefix(lines[idx], "@@") {
			idx++
			continue
		}
		header := lines[idx]
		oldStart, oldLines, _, newLines, ok := parseHunkCounts(header)
		if !ok {
			return nil, model.Rejection{
				Path: td.Path, Reason: model.RejectInvalidHunkHeader,
				Detail: fmt.Sprintf("unparsable hunk header: %q", header),
			}, nil
		}
		idx++
		var bodyLines []string
		for idx < len(lines) && !strings.HasPrefix(lines[idx], "@@") {
			if strings.TrimSpace(lines[idx]) != "" || lines[idx] != "" {
				bodyLines = append(bodyLines, lines[idx])
			}
			idx++
		}
		gotOld, gotNew := countHunkBody(bodyLines)
		if gotOld != oldLines || gotNew != newLines {
			return nil, model.Rejection{
				Path: td.Path, Reason: model.RejectInvalidHunkHeader,
				Detail: fmt.Sprintf(
					"hunk header declares -%d/+%d but body counts -%d/+%d",
					oldLines, newLines, gotOld, gotNew),
			}, nil
		}

		h, ok := hunkFromBody(oldStart, oldLines, newLines, bodyLines)
		if !ok {
			return nil, model.Rejection{
				Path: td.Path, Reason: model.RejectInvalidRange,
				Detail: rejectDetail(model.RejectInvalidRange, td.Path),
			}, nil
		}
		hunks = append(hunks, h)
	}
	if len(hunks) == 0 {
		return nil, model.Rejection{
			Path: td.Path, Reason: model.RejectInvalidHunkHeader,
			Detail: "diff body contains no hunk",
		}, nil
	}
	return hunks, model.Rejection{}, nil
}

// safePatchFromText 隔离第三方库：
//   - 库显式返回的 error => CodeParseSyntax（语法错误）
//   - 库内部 panic       => CodeParseLibrary（内部异常，recover）
func (p *goDiffParser) safePatchFromText(body string) (patches []dmp.Patch, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = EnsureEngineError(r, errs.CodeParseLibrary)
		}
	}()
	patches, lerr := p.patchFunc(body)
	if lerr != nil {
		return nil, errs.New(errs.CodeParseSyntax,
			"third-party diff parser rejected syntax", lerr)
	}
	return patches, nil
}

// hunkFromFields 从字段式文本差异构造归一化 hunk。
func hunkFromFields(td model.RawTextDiff) (model.TextHunk, bool) {
	h := model.TextHunk{Replacement: td.Lines}
	switch {
	case td.OldLines == 0 && td.NewLines > 0: // 纯插入：插入点在 OldStart 之后
		h.Range = model.LineRange{Start: td.OldStart + 1, End: td.OldStart}
	case td.OldLines > 0:
		h.Range = model.LineRange{Start: td.OldStart, End: td.OldStart + td.OldLines - 1}
	default:
		return model.TextHunk{}, false
	}
	if verr := ValidateLineRange(h.Range); verr != nil {
		return model.TextHunk{}, false
	}
	return h, true
}

// hunkFromBody 从一个 hunk 的体行构造归一化 hunk（每文件每 hunk
// 只保留一个连续区间：hunk 内的上下文行被拆段处理时由调用方保证单段）。
func hunkFromBody(oldStart, oldLines, newLines int, bodyLines []string) (model.TextHunk, bool) {
	// 找到首个变更行的旧侧位置与变更区间
	startOff := -1
	endOff := -1
	var repl []string
	off := 0
	for _, ln := range bodyLines {
		if ln == "" {
			continue
		}
		switch ln[0] {
		case ' ':
			off++
		case '-':
			if startOff < 0 {
				startOff = off
			}
			endOff = off
			off++
		case '+':
			if startOff < 0 && oldLines == 0 {
				startOff = off
				endOff = off - 1
			}
			repl = append(repl, ln[1:])
		}
	}
	if startOff < 0 {
		// 纯上下文：无实际改动
		return model.TextHunk{}, false
	}
	r := model.LineRange{Start: oldStart + startOff, End: oldStart + endOff}
	if verr := ValidateLineRange(r); verr != nil {
		return model.TextHunk{}, false
	}
	return model.TextHunk{Range: r, Replacement: repl}, true
}

func withIndex(r model.Rejection, i int) model.Rejection {
	r.Index = i
	return r
}

func rejectDetail(rc model.RejectCode, path string) string {
	switch rc {
	case model.RejectMissingPath:
		return "change record is missing required field: path"
	case model.RejectUnknownOp:
		return "structured change has unsupported op"
	case model.RejectMissingKey:
		return "structured change is missing required field: key"
	case model.RejectMissingNewKey:
		return "rename change is missing required field: new_key"
	case model.RejectInvalidRange:
		return fmt.Sprintf("illegal line range in %s", path)
	case model.RejectMissingDiffBody:
		return "text diff declares changes but provides no content"
	case model.RejectInvalidHunkHeader:
		return "hunk header inconsistent with body or malformed"
	default:
		return string(rc)
	}
}
