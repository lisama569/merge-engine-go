package merge

import (
	"fmt"
	"sort"

	"github.com/lisama569/merge-engine-go/internal/model"
)

// unit 标识可能发生冲突的最小单元：文本文件按路径+行区间，
// 结构化文件按路径+条目键（rename 同时占用旧键与新键）。
type unit struct {
	path   string
	key    string // 文本改动为空
	text   bool
	rStart int
	rEnd   int
}

// detectConflicts 在两个变更集之间找出全部冲突（不做取舍）。
//
// 覆盖：
//   - 同文件文本 hunk 行区间重叠（含纯插入点相撞）
//   - 一方删除/重命名文件级条目、另一方修改（delete_modify）
//   - 同一条目的 rename 冲突（双方重命名到不同目标）
//   - 同一条目的类型被改成不同类型（type_change）
//   - 同键的不一致结构化改动（structured_clash）
//   - 对已存在键执行 create（create_existing）
func detectConflicts(a, b model.ChangeSet) []model.Conflict {
	var out []model.Conflict
	for _, ca := range a.Changes {
		for _, cb := range b.Changes {
			if ca.Path != cb.Path {
				continue
			}
			if c := pairConflict(ca, cb, a.Author, b.Author); c != nil {
				out = append(out, *c)
			}
		}
	}
	return dedupConflicts(out)
}

// pairConflict 判定两条变更之间是否存在冲突。
func pairConflict(x, y model.Change, ax, ay string) *model.Conflict {
	authors := []string{ax, ay}
	switch {
	case x.Text != nil && y.Text != nil:
		if rangesOverlap(x.Text.Range, y.Text.Range) {
			return &model.Conflict{
				Kind: model.ConflictOverlap, Path: x.Path,
				Detail: fmt.Sprintf(
					"overlapping line ranges [%d,%d] vs [%d,%d]",
					x.Text.Range.Start, x.Text.Range.End,
					y.Text.Range.Start, y.Text.Range.End),
				Authors: authors,
			}
		}
	case x.Structured != nil && y.Structured != nil:
		return structuredPairConflict(x.Path, x.Structured, y.Structured, authors)
	case x.Text != nil || y.Text != nil:
		// 同一路径一方改文本、一方改结构化 => 语义层面的文件类型分歧
		return &model.Conflict{
			Kind: model.ConflictTypeChange, Path: x.Path,
			Detail:  "same path modified as both text and structured",
			Authors: authors,
		}
	}
	return nil
}

// structuredPairConflict 判定两条结构化变更之间的冲突。
func structuredPairConflict(path string, x, y *model.StructuredChange, authors []string) *model.Conflict {
	sx, sy := *x, *y

	// 重命名冲突：同一旧键被双方改名到不同新键
	if sx.Op == model.StructuredRename && sy.Op == model.StructuredRename &&
		sx.Key == sy.Key && sx.NewKey != sy.NewKey {
		return &model.Conflict{
			Kind: model.ConflictRename, Path: path,
			Detail: fmt.Sprintf("key %q renamed to %q vs %q",
				sx.Key, sx.NewKey, sy.NewKey),
			Authors: authors,
		}
	}

	// 删除/修改冲突：针对同一旧键，一方删除、另一方改动（改名/改值/改类型）
	touchedX := renameTouchedKeys(sx)
	touchedY := renameTouchedKeys(sy)
	common := intersectKeys(touchedX, touchedY)
	for _, k := range common {
		if (sx.Op == model.StructuredDelete && modifies(sy)) ||
			(sy.Op == model.StructuredDelete && modifies(sx)) {
			return &model.Conflict{
				Kind: model.ConflictDeleteModify, Path: path,
				Detail:  fmt.Sprintf("key %q deleted by one side and modified by the other", k),
				Authors: authors,
			}
		}
	}

	// 类型变化冲突：同键被改成不同类型
	if sx.Key == sy.Key && sx.NewType != "" && sy.NewType != "" &&
		sx.NewType != sy.NewType {
		return &model.Conflict{
			Kind: model.ConflictTypeChange, Path: path,
			Detail: fmt.Sprintf("key %q type changed to %q vs %q",
				sx.Key, sx.NewType, sy.NewType),
			Authors: authors,
		}
	}

	// 同键新建冲突（create vs create）
	if sx.Op == model.StructuredCreate && sy.Op == model.StructuredCreate &&
		sx.Key == sy.Key {
		return &model.Conflict{
			Kind: model.ConflictCreateExisting, Path: path,
			Detail:  fmt.Sprintf("key %q created by both sides", sx.Key),
			Authors: authors,
		}
	}

	// 同键的不一致更新（改值分歧）
	if sx.Key == sy.Key && sx.Op == model.StructuredUpdate &&
		sy.Op == model.StructuredUpdate && sx.NewValue != sy.NewValue {
		return &model.Conflict{
			Kind: model.ConflictStructured, Path: path,
			Detail: fmt.Sprintf("key %q updated to %q vs %q",
				sx.Key, sx.NewValue, sy.NewValue),
			Authors: authors,
		}
	}
	return nil
}

// rangesOverlap 判断两个文本行区间是否重叠。
// 纯插入点为 [s, s-1]；两个相同插入点视为重叠（插入顺序会改变结果）。
func rangesOverlap(x, y model.LineRange) bool {
	insertX := x.End == x.Start-1
	insertY := y.End == y.Start-1
	if insertX && insertY {
		return x.Start == y.Start
	}
	if insertX {
		return y.Start <= x.Start && x.Start <= y.End+1
	}
	if insertY {
		return x.Start <= y.Start && y.Start <= x.End+1
	}
	return x.Start <= y.End && y.Start <= x.End
}

// modifies 判断结构化操作是否对既有条目产生修改。
func modifies(s model.StructuredChange) bool {
	return s.Op != model.StructuredDelete
}

// renameTouchedKeys 返回一次操作触碰的键（rename 同时占用新旧键）。
func renameTouchedKeys(s model.StructuredChange) []string {
	if s.Op == model.StructuredRename {
		return []string{s.Key, s.NewKey}
	}
	return []string{s.Key}
}

func intersectKeys(a, b []string) []string {
	set := map[string]bool{}
	for _, k := range a {
		set[k] = true
	}
	var out []string
	for _, k := range b {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

// blockedUnits 把冲突展开为"不可自动合并"的单元集合，
// 冲突双方路径上的相关改动都会被阻塞，避免任选其一。
func blockedUnits(cs []model.Conflict) map[string]bool {
	m := map[string]bool{}
	for _, c := range cs {
		m[c.Path] = true
	}
	return m
}

// isBlocked 判断一条自动合并候选变更是否落在冲突路径上。
func isBlocked(c model.Change, blocked map[string]bool) bool {
	return blocked[c.Path]
}

// fingerprint 计算变更内容指纹，用于幂等去重。
func fingerprint(c model.Change) string {
	s := c.Path + "|" + string(c.Source)
	if c.Text != nil {
		s += fmt.Sprintf("|T:%d,%d:%v", c.Text.Range.Start, c.Text.Range.End, c.Text.Replacement)
	}
	if c.Structured != nil {
		st := c.Structured
		s += fmt.Sprintf("|S:%s:%s:%s:%s:%s:%s:%s",
			st.Op, st.Key, st.NewKey, st.OldType, st.NewType, st.OldValue, st.NewValue)
	}
	return s
}

// dedupConflicts 按 (kind,path,detail,authors) 去重。
func dedupConflicts(cs []model.Conflict) []model.Conflict {
	seen := map[string]bool{}
	var out []model.Conflict
	for _, c := range cs {
		key := string(c.Kind) + "|" + c.Path + "|" + c.Detail
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// sortChanges 按 路径/来源/区间/内容 稳定排序。
func sortChanges(changes []model.Change) {
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		ai, bi := 0, 0
		if a.Text != nil {
			ai = a.Text.Range.Start
		}
		if b.Text != nil {
			bi = b.Text.Range.Start
		}
		if ai != bi {
			return ai < bi
		}
		return fingerprint(a) < fingerprint(b)
	})
}
