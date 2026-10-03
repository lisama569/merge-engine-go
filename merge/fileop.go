package merge

import (
	"fmt"
	"sort"

	"github.com/lisama569/merge-engine-go/model"
)

// sideChange pairs a change with its originating side ID.
type sideChange struct {
	side string
	ch   model.Change
}

// fileDecisions holds everything concluded for one path during a merge.
type fileDecisions struct {
	applied  []model.AppliedChange
	conflict *model.Conflict
	// accept lists the concrete changes to apply to the merged tree when
	// there is no conflict. Identical changes from several sides appear
	// only once so application stays idempotent.
	accept []sideChange
}

// reconcilePath merges every side's change to one logical file.
//
// keyFrom is the identity key under which this change was grouped: the
// pre-image path for renames/deletes/modifies and the post-image path
// for adds. baseExisted tells whether the file existed in the base state.
func reconcilePath(key string, group []sideChange, baseExisted bool) fileDecisions {
	// Deterministic ordering: by side ID, then op.
	sort.SliceStable(group, func(i, j int) bool {
		if group[i].side != group[j].side {
			return group[i].side < group[j].side
		}
		return group[i].ch.Op < group[j].ch.Op
	})

	// Fast path: single side touches this file -> auto-apply.
	if len(group) == 1 {
		return fileDecisions{
			applied: []model.AppliedChange{{
				SourceID: group[0].side, Path: group[0].ch.Path, Op: group[0].ch.Op}},
			accept: []sideChange{group[0]},
		}
	}

	// Multi-side: classify by op set.
	return reconcileMulti(key, group, baseExisted)
}

func reconcileMulti(key string, g []sideChange, baseExisted bool) fileDecisions {
	ops := map[model.ChangeOp]int{}
	for _, sc := range g {
		ops[sc.ch.Op]++
	}

	// All sides identical (same op + same target) -> dedupe once.
	if identicalChanges(g) {
		return fileDecisions{
			applied: []model.AppliedChange{{
				SourceID: g[0].side, Path: g[0].ch.Path, Op: g[0].ch.Op}},
			accept: []sideChange{g[0]},
		}
	}

	switch {
	case ops[model.OpAdd] >= 2:
		return addAddConflict(key, g)
	case ops[model.OpDelete] > 0 && ops[model.OpModify] > 0:
		return modifyDeleteConflict(key, g)
	case ops[model.OpDelete] > 0 && ops[model.OpRename] > 0:
		return renameDeleteConflict(key, g)
	case ops[model.OpRename] >= 1:
		return renameFamilyConflict(key, g)
	case ops[model.OpTypeChange] > 0:
		return typeConflict(key, g)
	case ops[model.OpModify] >= 2:
		// Two modifies: hunk-level disjointness decides.
		return modifyPair(key, g)
	}

	// Fallback: conservative explicit conflict rather than guessing.
	return fileDecisions{conflict: &model.Conflict{
		Kind: model.ConflictTypeMismatch, Path: key,
		SideA: g[0].side, SideB: g[1].side,
		Detail:   "unhandled op combination: " + opSetString(ops),
		Evidence: pairEvidence(g),
	}}
}

func identicalChanges(g []sideChange) bool {
	first := g[0].ch
	for _, sc := range g[1:] {
		c := sc.ch
		if c.Op != first.Op || c.Path != first.Path || c.OldPath != first.OldPath ||
			c.NewContent != first.NewContent || c.ChangeType != first.ChangeType {
			return false
		}
		if len(c.Hunks) != len(first.Hunks) {
			return false
		}
		for i := range c.Hunks {
			if !sameHunkEdit(c.Hunks[i], first.Hunks[i]) {
				return false
			}
		}
	}
	return true
}

func addAddConflict(key string, g []sideChange) fileDecisions {
	contents := map[string]bool{}
	for _, sc := range g {
		contents[sc.ch.NewContent] = true
	}
	if len(contents) == 1 {
		// Identical add: apply once, record first side.
		return fileDecisions{
			applied: []model.AppliedChange{{
				SourceID: g[0].side, Path: key, Op: model.OpAdd}},
			accept: []sideChange{g[0]},
		}
	}
	return fileDecisions{conflict: &model.Conflict{
		Kind: model.ConflictAddAdd, Path: key,
		SideA: g[0].side, SideB: g[1].side,
		Detail:   fmt.Sprintf("%d sides add the same path with different content", len(g)),
		Evidence: pairEvidence(g),
	}}
}

func modifyDeleteConflict(key string, g []sideChange) fileDecisions {
	var del, mod sideChange
	for _, sc := range g {
		switch sc.ch.Op {
		case model.OpDelete:
			del = sc
		case model.OpModify:
			mod = sc
		}
	}
	return fileDecisions{conflict: &model.Conflict{
		Kind: model.ConflictModifyDelete, Path: key,
		SideA: mod.side, SideB: del.side,
		Detail:   fmt.Sprintf("side %s modifies while side %s deletes", mod.side, del.side),
		Evidence: pairEvidence(g),
	}}
}

func renameDeleteConflict(key string, g []sideChange) fileDecisions {
	var del, ren sideChange
	for _, sc := range g {
		switch sc.ch.Op {
		case model.OpDelete:
			del = sc
		case model.OpRename:
			ren = sc
		}
	}
	return fileDecisions{conflict: &model.Conflict{
		Kind: model.ConflictRenameDelete, Path: key,
		SideA: ren.side, SideB: del.side,
		Detail: fmt.Sprintf("side %s renames %q -> %q while side %s deletes it",
			ren.side, ren.ch.OldPath, ren.ch.Path, del.side),
		Evidence: pairEvidence(g),
	}}
}

func renameFamilyConflict(key string, g []sideChange) fileDecisions {
	targets := map[string]string{} // side -> new path
	var renames []sideChange
	for _, sc := range g {
		if sc.ch.Op == model.OpRename {
			renames = append(renames, sc)
			targets[sc.side] = sc.ch.Path
		}
	}
	// delete vs rename already handled earlier; reaching here means >=1 rename
	// without a delete. Compare rename destinations.
	distinct := map[string]bool{}
	for _, p := range targets {
		distinct[p] = true
	}
	if len(distinct) == 1 && len(renames) == len(g) {
		// Every side performs the same rename: idempotent auto-apply.
		return fileDecisions{
			applied: []model.AppliedChange{{
				SourceID: renames[0].side, Path: renames[0].ch.Path, Op: model.OpRename}},
			accept: []sideChange{renames[0]},
		}
	}
	return fileDecisions{conflict: &model.Conflict{
		Kind: model.ConflictRenameRename, Path: key,
		SideA: renames[0].side, SideB: renames[len(renames)-1].side,
		Detail:   fmt.Sprintf("sides rename %q to divergent destinations", key),
		Evidence: pairEvidence(g),
	}}
}

func typeConflict(key string, g []sideChange) fileDecisions {
	return fileDecisions{conflict: &model.Conflict{
		Kind: model.ConflictTypeMismatch, Path: key,
		SideA: g[0].side, SideB: g[1].side,
		Detail:   "incompatible file-kind operations on the same path",
		Evidence: pairEvidence(g),
	}}
}

func modifyPair(key string, g []sideChange) fileDecisions {
	for i := 0; i < len(g); i++ {
		for j := i + 1; j < len(g); j++ {
			if c := hunkConflict(key, g[i].ch, g[j].ch, g[i].side, g[j].side); c != nil {
				return fileDecisions{conflict: c}
			}
		}
	}
	// Disjoint hunks: auto-apply each side.
	var out fileDecisions
	for _, sc := range g {
		out.applied = append(out.applied, model.AppliedChange{
			SourceID: sc.side, Path: sc.ch.Path, Op: sc.ch.Op})
		out.accept = append(out.accept, sc)
	}
	return out
}

func pairEvidence(g []sideChange) []string {
	ev := make([]string, 0, len(g))
	for _, sc := range g {
		ev = append(ev, fmt.Sprintf("side %s op=%s path=%q", sc.side, sc.ch.Op, sc.ch.Path))
	}
	return ev
}

func opSetString(ops map[model.ChangeOp]int) string {
	keys := make([]string, 0, len(ops))
	for k := range ops {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k
	}
	return out
}
