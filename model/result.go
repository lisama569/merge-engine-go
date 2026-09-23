package model

import (
	"fmt"
	"strings"
)

// ConflictKind classifies why two change sets could not be auto-merged.
type ConflictKind string

const (
	// ConflictTextOverlap: both sides edit the same hunk line range.
	ConflictTextOverlap ConflictKind = "text_overlap"
	// ConflictRenameRename: both sides rename the same path differently.
	ConflictRenameRename ConflictKind = "rename_rename"
	// ConflictRenameDelete: one side renames, the other deletes.
	ConflictRenameDelete ConflictKind = "rename_delete"
	// ConflictModifyDelete: one side modifies, the other deletes.
	ConflictModifyDelete ConflictKind = "modify_delete"
	// ConflictTypeMismatch: a path's kind changes incompatibly
	// (file vs directory, divergent type changes, etc.).
	ConflictTypeMismatch ConflictKind = "type_mismatch"
	// ConflictAddAdd: both sides add the same path with different content.
	ConflictAddAdd ConflictKind = "add_add"
)

// Conflict describes one rejected auto-merge decision with full evidence.
type Conflict struct {
	Kind     ConflictKind `json:"kind"`
	Path     string       `json:"path"`
	SideA    string       `json:"side_a"` // SourceID of the first side
	SideB    string       `json:"side_b"` // SourceID of the second side
	Detail   string       `json:"detail"`
	Evidence []string     `json:"evidence,omitempty"` // human-readable basis lines
}

// AppliedChange records which change from which side was auto-merged.
type AppliedChange struct {
	SourceID string   `json:"source_id"`
	Path     string   `json:"path"`
	Op       ChangeOp `json:"op"`
}

// MergeResult is the deterministic outcome of a merge.
type MergeResult struct {
	Base      string          `json:"base"`      // base snapshot name
	Merged    *Snapshot       `json:"merged"`    // resulting snapshot
	Applied   []AppliedChange `json:"applied"`   // changes taken in
	Conflicts []Conflict      `json:"conflicts"` // explicit conflicts
	// Replays is the number of times the same change sets were applied;
	// results are identical for Replays >= 1 (idempotency).
	Replays int  `json:"replays"`
	OK      bool `json:"ok"` // true iff Conflicts is empty
}

// Explain renders a deterministic, human-readable decision report.
// The report lists the base, every applied change and every conflict with
// its evidence, so merge verdicts are reproducible by inspection.
func (r *MergeResult) Explain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "merge over base=%q replays=%d ok=%t\n", r.Base, r.Replays, r.OK)
	if len(r.Applied) > 0 {
		b.WriteString("applied:\n")
		for _, a := range r.Applied {
			fmt.Fprintf(&b, "  - [%s] %s %s\n", a.SourceID, a.Op, a.Path)
		}
	}
	if len(r.Conflicts) > 0 {
		b.WriteString("conflicts:\n")
		for _, c := range r.Conflicts {
			fmt.Fprintf(&b, "  ! %s at %s between %s and %s: %s\n",
				c.Kind, c.Path, c.SideA, c.SideB, c.Detail)
			for _, ev := range c.Evidence {
				fmt.Fprintf(&b, "      evidence: %s\n", ev)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
