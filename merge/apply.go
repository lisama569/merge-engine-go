package merge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lisama569/merge-engine-go/model"
)

// applyHunks applies disjoint, validated hunks to content. Hunks are
// applied from the lowest base position upward while tracking the
// cumulative line-count shift, so every hunk lands at its correct
// base-relative position even when earlier hunks inserted or deleted
// lines. Identical hunks (duplicate replays) collapse to one application.
//
// Application is idempotent: a hunk whose post-image already sits at its
// (shift-adjusted) base position is skipped instead of applied twice. This
// keeps validation-style re-merges (taking a previous merge output as the
// new base and re-applying the same change sets) drift-free — most
// importantly for pure insertions, which would otherwise stack their lines
// on every re-application. Known limitation: inserting lines that are
// already present verbatim at the anchor is indistinguishable from an
// already-applied insertion and is treated as a no-op.
func applyHunks(content string, hunks []model.Hunk) string {
	lines := splitLines(content)
	trailingNL := strings.HasSuffix(content, "\n")
	uniq := dedupeHunks(hunks)
	sort.SliceStable(uniq, func(i, j int) bool { return uniq[i].OldStart < uniq[j].OldStart })
	shift := 0 // cumulative line-count delta from hunks already processed
	for _, h := range uniq {
		if len(h.OldLines) == 0 {
			// Pure insertion before base line OldStart (anchor semantics).
			anchor := h.OldStart - 1 + shift
			if anchor < 0 {
				anchor = 0
			}
			if anchor > len(lines) {
				anchor = len(lines)
			}
			// Idempotency guard: after a previous application the inserted
			// lines sit exactly at [anchor, anchor+len); do not stack them.
			if !linesAt(lines, anchor, h.NewLines) {
				lines = insertAt(lines, anchor, h.NewLines)
			}
			shift += len(h.NewLines)
			continue
		}
		// Replacement/deletion: 1-based inclusive base range, shift-adjusted.
		lo := h.OldStart - 1 + shift
		if lo < 0 {
			lo = 0
		}
		if lo > len(lines) {
			lo = len(lines)
		}
		hi := lo + len(h.OldLines)
		if hi > len(lines) {
			hi = len(lines)
		}
		// Idempotency guard: if the old lines are gone but the new lines
		// already sit at this position, the hunk was applied before.
		if !linesAt(lines, lo, h.OldLines) && linesAt(lines, lo, h.NewLines) {
			shift += len(h.NewLines) - len(h.OldLines)
			continue
		}
		lines = replaceRange(lines, lo, hi, h.NewLines)
		shift += len(h.NewLines) - len(h.OldLines)
	}
	return joinLines(lines, trailingNL)
}

// linesAt reports whether the lines starting at idx equal want.
func linesAt(lines []string, idx int, want []string) bool {
	if idx < 0 || idx+len(want) > len(lines) {
		return false
	}
	for i, w := range want {
		if lines[idx+i] != w {
			return false
		}
	}
	return true
}

func dedupeHunks(hunks []model.Hunk) []model.Hunk {
	seen := map[string]bool{}
	out := make([]model.Hunk, 0, len(hunks))
	for _, h := range hunks {
		key := fmt.Sprintf("%d|%d|%v", h.OldStart, h.OldEnd, h.NewLines)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	return out
}

func replaceRange(lines []string, lo, hi int, repl []string) []string {
	out := make([]string, 0, len(lines)-hi+lo+len(repl))
	out = append(out, lines[:lo]...)
	out = append(out, repl...)
	out = append(out, lines[hi:]...)
	return out
}

func insertAt(lines []string, idx int, add []string) []string {
	out := make([]string, 0, len(lines)+len(add))
	out = append(out, lines[:idx]...)
	out = append(out, add...)
	out = append(out, lines[idx:]...)
	return out
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	// Use Scan-style split but keep it dependency-free.
	var out []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			out = append(out, content[start:i])
			start = i + 1
		}
	}
	if start < len(content) {
		out = append(out, content[start:])
	}
	return out
}

func joinLines(lines []string, trailingNL bool) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	if trailingNL && len(lines) > 0 {
		out += "\n"
	}
	return out
}
