// Package diffparse 将外部原始变更输入归一化为 model.ChangeSet。
//
// 它封装底层第三方差异解析库（github.com/sergi/go-diff），
// 库的语法错误与内部异常分别映射为 errs.CodeParseSyntax 与
// errs.CodeParseLibrary，绝不透出未处理的库异常。
package diffparse

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/model"
)

// Parser 把原始变更集合归一化并做输入校验。
type Parser interface {
	// Normalize 解析并归一化；非法输入通过返回的拒绝列表逐条给出
	// 可区分原因，绝不静默跳过。
	Normalize(ctx context.Context, raw model.RawChangeSet) (*model.ChangeSet, []model.Rejection, error)
}

// hunkHeaderRe 匹配 unified diff 的 hunk 头：@@ -a,b +c,d @@
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ValidateLineRange 校验行区间合法性。
//
// 规则：start>=1；end>=start-1（end==start-1 表示纯插入点）。
// 越出基线长度的校验需要文件长度信息，在归一化时另行处理。
func ValidateLineRange(r model.LineRange) error {
	if r.Start < 1 {
		return errs.New(errs.CodeInvalidInput,
			fmt.Sprintf("line range start must be >= 1, got %d", r.Start), nil)
	}
	if r.End < r.Start-1 {
		return errs.New(errs.CodeInvalidInput,
			fmt.Sprintf("line range end %d before start-1 %d", r.End, r.Start-1), nil)
	}
	return nil
}

// EnsureEngineError 把任意错误（含 panic 恢复值）归一化为 *errs.EngineError。
// 已是 *errs.EngineError 的保持其分类；其余按 fallback 归类为库内部异常，
// 原始值作为上下文保留，绝不返回未处理的第三方错误。
func EnsureEngineError(r any, fallback errs.Code) error {
	if r == nil {
		return nil
	}
	if err, ok := r.(error); ok {
		if ee, ok2 := err.(*errs.EngineError); ok2 {
			return ee
		}
		return errs.New(fallback, "third-party parser failure", err)
	}
	return errs.New(fallback, "third-party parser failure",
		fmt.Errorf("%v", r))
}

// validateStructured 校验单条结构化变更，返回拒绝原因码（空串表示通过）。
func validateStructured(raw model.RawStructuredChange) model.RejectCode {
	if strings.TrimSpace(raw.Path) == "" {
		return model.RejectMissingPath
	}
	switch model.StructuredOp(raw.Op) {
	case model.StructuredCreate, model.StructuredUpdate,
		model.StructuredDelete, model.StructuredRename, model.StructuredTypeSet:
	default:
		return model.RejectUnknownOp
	}
	if strings.TrimSpace(raw.Key) == "" {
		return model.RejectMissingKey
	}
	if model.StructuredOp(raw.Op) == model.StructuredRename &&
		strings.TrimSpace(raw.NewKey) == "" {
		return model.RejectMissingNewKey
	}
	return ""
}

// validateText 校验单条文本差异的字段完整性与行区间，
// 返回拒绝原因码（空串表示通过）。body 模式的 hunk 语法由解析阶段校验。
func validateText(raw model.RawTextDiff) model.RejectCode {
	if strings.TrimSpace(raw.Path) == "" {
		return model.RejectMissingPath
	}
	body := strings.TrimSpace(raw.Body)
	if body != "" {
		// body 模式：必须含 hunk 头，具体语法由 go-diff + 行计数核对
		if !strings.Contains(body, "@@") {
			return model.RejectInvalidHunkHeader
		}
		return ""
	}
	// 替换（old>0 且 new>0）必须提供替换行；删除（new==0）允许空替换。
	if len(raw.Lines) == 0 && raw.OldLines > 0 && raw.NewLines > 0 {
		return model.RejectMissingDiffBody
	}
	if raw.OldStart < 1 || raw.NewStart < 1 || raw.OldLines < 0 || raw.NewLines < 0 {
		return model.RejectInvalidRange
	}
	r := model.LineRange{}
	switch {
	case raw.OldLines == 0 && raw.NewLines > 0: // 纯插入
		r.Start = raw.OldStart + 1
		r.End = raw.OldStart
	case raw.OldLines > 0: // 删除或替换
		r.Start = raw.OldStart
		r.End = raw.OldStart + raw.OldLines - 1
	default: // 0,0 空改动
		return model.RejectMissingDiffBody
	}
	if err := ValidateLineRange(r); err != nil {
		return model.RejectInvalidRange
	}
	return ""
}

// normalizeStructured 把合法的结构化原始记录转成统一模型。
func normalizeStructured(raw model.RawStructuredChange) model.Change {
	return model.Change{
		Path:   raw.Path,
		Source: model.SourceStructured,
		Structured: &model.StructuredChange{
			Op:       model.StructuredOp(raw.Op),
			Key:      raw.Key,
			NewKey:   raw.NewKey,
			OldType:  raw.OldType,
			NewType:  raw.NewType,
			OldValue: raw.OldValue,
			NewValue: raw.NewValue,
		},
	}
}

// parseHunkCounts 解析 hunk 头声明的旧/新区行数。
func parseHunkCounts(header string) (oldStart, oldLines, newStart, newLines int, ok bool) {
	m := hunkHeaderRe.FindStringSubmatch(header)
	if m == nil {
		return 0, 0, 0, 0, false
	}
	oldStart, _ = strconv.Atoi(m[1])
	oldLines = 1
	if m[2] != "" {
		oldLines, _ = strconv.Atoi(m[2])
	}
	newStart, _ = strconv.Atoi(m[3])
	newLines = 1
	if m[4] != "" {
		newLines, _ = strconv.Atoi(m[4])
	}
	return oldStart, oldLines, newStart, newLines, true
}

// countHunkBody 统计 hunk 体中实际的旧侧/新侧行数。
func countHunkBody(bodyLines []string) (oldN, newN int) {
	for _, ln := range bodyLines {
		if ln == "" {
			continue
		}
		switch ln[0] {
		case '-', ' ':
			oldN++
		case '+':
			newN++
		}
	}
	return oldN, newN
}
