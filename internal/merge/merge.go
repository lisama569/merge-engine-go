// Package merge 实现纯函数式的三方合并与并发修改处理。
//
// 互不重叠的改动自动合并；重叠与语义冲突（重命名、删除/修改、
// 类型变化等）显式标记为 Conflict，绝不任选一方。所有函数不修改
// 输入快照，相同输入永远产生相同输出。
package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/lisama569/merge-engine-go/internal/model"
)

// ThreeWay 在 base 之上合并 ours 与 theirs 两个变更集。
//
// 该函数确定性：相同输入（含变更集顺序）永远产生相同输出。
// 存在冲突的路径/条目不做任何取舍，保留基线内容并全部列出冲突；
// 互不重叠的改动自动合并。
func ThreeWay(base model.Snapshot, ours, theirs model.ChangeSet) model.MergeReport {
	conflicts := detectConflicts(ours, theirs)

	blocked := blockedUnits(conflicts)
	merged := CloneSnapshot(base)
	merged.Label = "merged"

	auto := make([]model.Change, 0)
	// 固定应用顺序：先 ours 后 theirs，保证可复现。
	for _, c := range ours.Changes {
		if !isBlocked(c, blocked) {
			auto = append(auto, c)
		}
	}
	for _, c := range theirs.Changes {
		if !isBlocked(c, blocked) {
			auto = append(auto, c)
		}
	}

	// 应用自动合并的变更；Apply 对同一变更集幂等。
	combined := model.ChangeSet{Changes: auto}
	merged, applyConflicts := Apply(merged, combined)
	conflicts = append(conflicts, applyConflicts...)

	sortConflicts(conflicts)
	return model.MergeReport{
		BaseLabel:    base.Label,
		Merged:       &merged,
		AutoMerged:   auto,
		Conflicts:    conflicts,
		AppliedSets:  []string{ours.Author, theirs.Author},
		ReplayToken:  replayToken(base.Label, ours, theirs),
		HasConflict:  len(conflicts) > 0,
		HasRejection: false,
	}
}

// Apply 将一个已归一化变更集应用到快照上，返回新快照。
// 重复应用同一变更集的结果保持稳定：相同 (path, 操作, 键, 区间, 内容)
// 的变更只生效一次。
func Apply(snap model.Snapshot, set model.ChangeSet) (model.Snapshot, []model.Conflict) {
	out := CloneSnapshot(snap)
	seen := map[string]bool{}
	for _, f := range out.Applied {
		seen[f] = true
	}
	var appliedNow []string
	var conflicts []model.Conflict

	// 按稳定顺序排序：路径、来源、区间起点，保证幂等与可复现。
	changes := append([]model.Change(nil), set.Changes...)
	sortChanges(changes)

	for _, c := range changes {
		fp := fingerprint(c)
		if seen[fp] {
			continue // 幂等：该变更集此前已应用，跳过，结果不变
		}
		seen[fp] = true
		appliedNow = append(appliedNow, fp)

		fs := out.Files[c.Path]
		switch {
		case c.Text != nil:
			// 越过文件当前长度的区间属于非法应用，标记为冲突而不是 panic。
			if c.Text.Range.Start > len(fs.Lines)+1 {
				conflicts = append(conflicts, model.Conflict{
					Kind: model.ConflictOverlap, Path: c.Path,
					Detail:  "line range exceeds file length",
					Authors: []string{set.Author},
				})
				continue
			}
			fs.Lines = applyTextChange(fs.Lines, c)
			fs.Deleted = false
		case c.Structured != nil:
			fs = applyStructuredChange(fs, c, set.Author, &conflicts)
		}
		out.Files[c.Path] = fs
	}
	sort.Strings(appliedNow)
	out.Applied = append(out.Applied, appliedNow...)
	sort.Strings(out.Applied)
	return out, conflicts
}

// CloneSnapshot 深拷贝快照，保证合并过程不修改输入。
func CloneSnapshot(s model.Snapshot) model.Snapshot {
	cp := model.Snapshot{Label: s.Label, Files: map[string]model.FileState{}}
	if s.Applied != nil {
		cp.Applied = append([]string(nil), s.Applied...)
	}
	for path, fs := range s.Files {
		nfs := model.FileState{Type: fs.Type, Deleted: fs.Deleted}
		if fs.Lines != nil {
			nfs.Lines = append([]string(nil), fs.Lines...)
		}
		if fs.Entries != nil {
			nfs.Entries = map[string]model.StructuredEntry{}
			for k, v := range fs.Entries {
				nfs.Entries[k] = v
			}
		}
		cp.Files[path] = nfs
	}
	return cp
}

// sortConflicts 使冲突输出顺序确定。
func sortConflicts(cs []model.Conflict) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Path != cs[j].Path {
			return cs[i].Path < cs[j].Path
		}
		if cs[i].Kind != cs[j].Kind {
			return cs[i].Kind < cs[j].Kind
		}
		return cs[i].Detail < cs[j].Detail
	})
}

// replayToken 由三方标签、作者与变更指纹生成稳定令牌，
// 相同输入必然得到相同令牌。
func replayToken(baseLabel string, sets ...model.ChangeSet) string {
	h := sha256.New()
	h.Write([]byte(baseLabel))
	for _, s := range sets {
		h.Write([]byte("|author=" + s.Author))
		ch := append([]model.Change(nil), s.Changes...)
		sortChanges(ch)
		for _, c := range ch {
			h.Write([]byte(fingerprint(c)))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
