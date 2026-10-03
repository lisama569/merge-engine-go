// Package merge implements deterministic three-way merging of normalized
// change sets over locally stored snapshots.
//
// Semantics (see README for the full matrix):
//
//   - Disjoint paths auto-merge.
//   - Identical changes from multiple sides apply once (replay-safe).
//   - Overlapping text hunks on the same base lines conflict; pure
//     insertions (zero-width anchor hunks) at the same anchor conflict
//     only when their inserted text differs.
//   - Rename/rename to different targets, rename/delete, modify/delete
//     and incompatible type changes are explicit conflicts.
//   - Conflicts never roll back unrelated changes: non-conflicting
//     changes are always applied to the merged tree, conflicted paths
//     keep their base content, and OK reports whether any conflict
//     occurred.
//   - Merging is pure: MergeSets(base, sides...) is deterministic and
//     applying the same change sets again changes nothing.
package merge

import (
	"sort"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
	"github.com/lisama569/merge-engine-go/storage"
)

// Engine performs three-way merges against a local Store.
type Engine struct {
	store *storage.Store
}

// NewEngine constructs an engine bound to store.
func NewEngine(store *storage.Store) *Engine { return &Engine{store: store} }

// Merge merges change sets identified by sideIDs over the snapshot named
// baseName. Non-overlapping changes are applied automatically; overlaps
// and semantic conflicts are reported explicitly. The merged snapshot is
// persisted under resultName only when there are no conflicts.
func (e *Engine) Merge(baseName, resultName string, sideIDs ...string) (*model.MergeResult, error) {
	base, err := e.store.GetSnapshot(baseName)
	if err != nil {
		return nil, err
	}
	sides := make([]*model.ChangeSet, 0, len(sideIDs))
	for _, id := range sideIDs {
		cs, err := e.store.GetChangeSet(id)
		if err != nil {
			return nil, err
		}
		sides = append(sides, cs)
	}
	res := MergeSets(base, sides...)
	res.Base = baseName
	if res.OK && resultName != "" {
		res.Merged.Name = resultName
		if err := e.store.PutSnapshot(res.Merged); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// Replay re-applies the same inputs replays times; each application yields
// an identical result, proving idempotency. It errors when replays < 1.
func (e *Engine) Replay(baseName, resultName string, replays int, sideIDs ...string) (*model.MergeResult, error) {
	if replays < 1 {
		return nil, merr.New(merr.KindInvalidInput, "merge", "illegal_replays",
			"replays must be >= 1").WithContext(map[string]any{"replays": replays})
	}
	var first *model.MergeResult
	for i := 0; i < replays; i++ {
		// Each round starts from the SAME base: repeated application of a
		// change set must not stack its effects.
		r, err := e.Merge(baseName, resultName, sideIDs...)
		if err != nil {
			return nil, err
		}
		if first == nil {
			first = r
			continue
		}
		if !resultsEqual(first, r) {
			return nil, merr.New(merr.KindInvalidInput, "merge", "non_idempotent_replay",
				"replaying the same change sets produced a different result")
		}
	}
	first.Replays = replays
	return first, nil
}

// MergeSets merges already-loaded change sets without storage lookups.
// It is pure: identical inputs always yield an identical result.
func MergeSets(base *model.Snapshot, sides ...*model.ChangeSet) *model.MergeResult {
	result := &model.MergeResult{
		Base:   baseName(base),
		Merged: base.Clone(),
	}

	// Deterministic side order: sort by SourceID.
	ordered := make([]*model.ChangeSet, len(sides))
	copy(ordered, sides)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].SourceID < ordered[j].SourceID })

	// Index changes:
	//   bySource: source path (old path for renames) -> sides touching it
	//   byTarget: post-image path -> add/rename destinations
	bySource := map[string][]sideChange{}
	byTarget := map[string][]sideChange{}
	renameSources := map[string][]sideChange{} // source path -> rename ops
	for _, cs := range ordered {
		for _, ch := range cs.Changes {
			key := ch.Path
			if ch.Op == model.OpRename {
				key = ch.OldPath
				renameSources[key] = append(renameSources[key], sideChange{side: cs.SourceID, ch: ch})
			}
			bySource[key] = append(bySource[key], sideChange{side: cs.SourceID, ch: ch})
			if ch.Op == model.OpAdd || ch.Op == model.OpRename {
				byTarget[ch.Path] = append(byTarget[ch.Path], sideChange{side: cs.SourceID, ch: ch})
			}
		}
	}

	// Pass 1: cross-cutting semantic conflicts that span two indices.
	// rename vs modify on the old path.
	if c := renameModifyConflicts(bySource); c != nil {
		result.Conflicts = append(result.Conflicts, c...)
	}

	// Pass 2: per-source reconciliation over deterministic key order.
	// accepted collects the concrete changes that may be applied to the
	// merged tree; conflicted groups contribute nothing.
	var accepted []sideChange
	keys := make([]string, 0, len(bySource))
	for k := range bySource {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		group := bySource[k]
		_, existed := base.Files[k]
		d := reconcilePath(k, group, existed)
		if d.conflict != nil {
			// Avoid duplicating conflicts already found in pass 1.
			result.Conflicts = appendConflictIfNew(result.Conflicts, *d.conflict)
			continue
		}
		result.Applied = append(result.Applied, d.applied...)
		accepted = append(accepted, d.accept...)
	}

	// Pass 3: target-path collisions (add/add, rename destination clashes,
	// add landing on a path another side modifies independently, etc.).
	targets := make([]string, 0, len(byTarget))
	for t := range byTarget {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, t := range targets {
		group := byTarget[t]
		if len(group) < 2 {
			continue
		}
		if c := targetCollision(t, group, renameSources); c != nil {
			result.Conflicts = appendConflictIfNew(result.Conflicts, *c)
			// Drop any applied add/rename already recorded for this target
			// or its (renamed) source so the merged tree stays conservative.
			result.Applied = removeAppliedForConflict(result.Applied, c)
			accepted = removeAcceptedForConflict(accepted, c)
		}
	}

	// The non-conflicting part of the input is ALWAYS applied to the merged
	// tree, even when other paths conflict: one bad path must not roll back
	// unrelated, cleanly mergeable changes. Conflicted paths keep their
	// base content because their changes never entered `accepted`.
	result.OK = len(result.Conflicts) == 0
	applyAccepted(result.Merged, accepted)
	if !result.OK {
		sortConflicts(result.Conflicts)
	}
	return result
}

func baseName(base *model.Snapshot) string {
	if base == nil {
		return ""
	}
	return base.Name
}

// renameModifyConflicts detects a side renaming a file while another side
// modifies the file under its old path.
func renameModifyConflicts(bySource map[string][]sideChange) []model.Conflict {
	var out []model.Conflict
	keys := make([]string, 0, len(bySource))
	for k := range bySource {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var ren, mod *sideChange
		group := bySource[k]
		for i := range group {
			switch group[i].ch.Op {
			case model.OpRename:
				ren = &group[i]
			case model.OpModify:
				mod = &group[i]
			}
		}
		if ren != nil && mod != nil {
			out = append(out, model.Conflict{
				Kind: model.ConflictRenameDelete, Path: k,
				SideA: ren.side, SideB: mod.side,
				Detail: "side " + ren.side + " renames while side " + mod.side +
					" modifies the old path",
				Evidence: pairEvidence(group),
			})
		}
	}
	return out
}

// targetCollision inspects changes that converge on the same post-image
// path but did not share a source key.
func targetCollision(target string, group []sideChange, renameSources map[string][]sideChange) *model.Conflict {
	if identicalChanges(group) {
		return nil
	}
	// add/add with divergent content, or rename+add landing together.
	hasAdd, hasRename := false, false
	for _, sc := range group {
		switch sc.ch.Op {
		case model.OpAdd:
			hasAdd = true
		case model.OpRename:
			hasRename = true
		}
	}
	kind := model.ConflictAddAdd
	detail := "multiple sides create the same target path with different intent"
	if hasRename {
		kind = model.ConflictRenameRename
		detail = "rename destinations collide with another creation at the same path"
	}
	_ = hasAdd
	return &model.Conflict{
		Kind: kind, Path: target,
		SideA: group[0].side, SideB: group[len(group)-1].side,
		Detail:   detail,
		Evidence: pairEvidence(group),
	}
}

// applyAccepted applies the accepted (non-conflicting, deduplicated)
// changes to the merged snapshot. The input order is deterministic:
// changes were collected while iterating sorted path keys, and within one
// path group sides are sorted by SourceID.
//
// All accepted modify hunks for one file are batched and applied to the
// BASE content of that file in a single pass: hunk coordinates are
// base-relative, so applying them one change at a time onto already
// modified content would misplace insertions after line shifts.
func applyAccepted(snap *model.Snapshot, accepted []sideChange) {
	if snap.Files == nil {
		snap.Files = map[string]string{}
	}
	modHunks := map[string][]model.Hunk{}
	var modPaths []string
	for _, sc := range accepted {
		ch := sc.ch
		switch ch.Op {
		case model.OpModify:
			if _, seen := modHunks[ch.Path]; !seen {
				modPaths = append(modPaths, ch.Path)
			}
			modHunks[ch.Path] = append(modHunks[ch.Path], ch.Hunks...)
		case model.OpAdd:
			snap.Files[ch.Path] = ch.NewContent
		case model.OpDelete:
			delete(snap.Files, ch.Path)
		case model.OpRename:
			cur, ok := snap.Files[ch.OldPath]
			if ok {
				delete(snap.Files, ch.OldPath)
			}
			if ch.NewContent != "" {
				cur = ch.NewContent
			}
			snap.Files[ch.Path] = cur
		case model.OpTypeChange:
			if ch.ChangeType == "file_to_dir" {
				delete(snap.Files, ch.Path)
				snap.Files[ch.Path+"/"] = ""
			} else {
				delete(snap.Files, ch.Path+"/")
				snap.Files[ch.Path] = ch.NewContent
			}
		}
	}
	sort.Strings(modPaths)
	for _, p := range modPaths {
		snap.Files[p] = applyHunks(snap.Files[p], modHunks[p])
	}
}

func removeAppliedForConflict(applied []model.AppliedChange, c *model.Conflict) []model.AppliedChange {
	out := applied[:0]
	for _, a := range applied {
		if a.SourceID == c.SideA || a.SourceID == c.SideB {
			if a.Path == c.Path {
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// removeAcceptedForConflict drops accepted changes whose post-image path
// collides with a target-collision conflict, mirroring
// removeAppliedForConflict for the to-apply list.
func removeAcceptedForConflict(accepted []sideChange, c *model.Conflict) []sideChange {
	out := accepted[:0]
	for _, sc := range accepted {
		if sc.side == c.SideA || sc.side == c.SideB {
			if sc.ch.Path == c.Path || sc.ch.OldPath == c.Path {
				continue
			}
		}
		out = append(out, sc)
	}
	return out
}

func appendConflictIfNew(existing []model.Conflict, c model.Conflict) []model.Conflict {
	for _, e := range existing {
		if e.Kind == c.Kind && e.Path == c.Path && e.SideA == c.SideA && e.SideB == c.SideB {
			return existing
		}
	}
	return append(existing, c)
}

func sortConflicts(cs []model.Conflict) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Path != cs[j].Path {
			return cs[i].Path < cs[j].Path
		}
		if cs[i].Kind != cs[j].Kind {
			return cs[i].Kind < cs[j].Kind
		}
		if cs[i].SideA != cs[j].SideA {
			return cs[i].SideA < cs[j].SideA
		}
		return cs[i].SideB < cs[j].SideB
	})
}

func resultsEqual(a, b *model.MergeResult) bool {
	if a.OK != b.OK || len(a.Conflicts) != len(b.Conflicts) || len(a.Applied) != len(b.Applied) {
		return false
	}
	for i := range a.Conflicts {
		x, y := a.Conflicts[i], b.Conflicts[i]
		if x.Kind != y.Kind || x.Path != y.Path || x.SideA != y.SideA || x.SideB != y.SideB {
			return false
		}
	}
	for i := range a.Applied {
		if a.Applied[i] != b.Applied[i] {
			return false
		}
	}
	if (a.Merged == nil) != (b.Merged == nil) {
		return false
	}
	if a.Merged != nil {
		if len(a.Merged.Files) != len(b.Merged.Files) {
			return false
		}
		for k, v := range a.Merged.Files {
			if b.Merged.Files[k] != v {
				return false
			}
		}
	}
	return true
}
