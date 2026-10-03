package merge

import (
	"fmt"

	"github.com/lisama569/merge-engine-go/model"
)

// hunkTouches describes which base (1-based) line positions a hunk reads.
// Context/deleted lines occupy [OldStart, OldEnd]; a pure insertion
// (OldStart==OldEnd with zero-length OldLines) touches only its anchor.
type hunkSpan struct {
	start, end int
	insertion  bool
}

func spanOf(h model.Hunk) hunkSpan {
	ins := len(h.OldLines) == 0
	return hunkSpan{start: h.OldStart, end: h.OldEnd, insertion: ins}
}

// overlaps reports whether two base-relative spans touch the same base
// line position. Two pure insertions anchored at the same position are
// considered touching only if they insert identical text; divergent
// insertions at one anchor are flagged by the caller via content compare.
func spansOverlap(a, b hunkSpan) bool {
	if a.insertion && b.insertion {
		// Both insert at a base anchor; positions are zero-width.
		return a.start == b.start
	}
	// Standard closed-interval overlap against base line numbers.
	return a.start <= b.end && b.start <= a.end
}

// conflictingHunkPairs compares every hunk of change A against every hunk
// of change B on the same file. It returns the indices of the hunks on
// each side that overlap a non-identical hunk of the other side, plus one
// human-readable evidence line per conflicting pair. Identical hunks
// (duplicate application of the same edit) never count as conflicts.
func conflictingHunkPairs(a, b model.Change, sideA, sideB string) (confA, confB map[int]bool, evidence []string) {
	confA = map[int]bool{}
	confB = map[int]bool{}
	for ia, ha := range a.Hunks {
		sa := spanOf(ha)
		for ib, hb := range b.Hunks {
			sb := spanOf(hb)
			if !spansOverlap(sa, sb) {
				continue
			}
			// Same position, identical intent -> not a conflict.
			if sameHunkEdit(ha, hb) {
				continue
			}
			confA[ia] = true
			confB[ib] = true
			evidence = append(evidence, fmt.Sprintf(
				"side %s hunk base [%d,%d] -> %d line(s) overlaps side %s hunk base [%d,%d] -> %d line(s)",
				sideA, ha.OldStart, ha.OldEnd, len(ha.NewLines),
				sideB, hb.OldStart, hb.OldEnd, len(hb.NewLines)))
		}
	}
	if len(evidence) > 0 {
		evidence = append(evidence,
			"ranges intersect on the base file; engine refuses to pick either side")
	}
	return confA, confB, evidence
}

// sameHunkEdit reports whether two hunks express the identical edit
// (duplicate application of the same change must be idempotent, not a
// conflict).
func sameHunkEdit(a, b model.Hunk) bool {
	if a.OldStart != b.OldStart || a.OldEnd != b.OldEnd {
		return false
	}
	return equalLines(a.NewLines, b.NewLines) && equalLines(a.OldLines, b.OldLines)
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
