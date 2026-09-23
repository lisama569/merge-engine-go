package merge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lisama569/merge-engine-go/model"
)

// applyHunks applies disjoint, validated hunks to base content. Hunks are
// applied from the highest base position downward so earlier offsets stay
// valid. Identical hunks (duplicate replays) collapse to one application.
func applyHunks(content string, hunks []model.Hunk) string {
	lines := splitLines(content)
	trailingNL := strings.HasSuffix(content, "\n")
	uniq := dedupeHunks(hunks)
	sort.SliceStable(uniq, func(i, j int) bool { return uniq[i].OldStart > uniq[j].OldStart })
	for _, h := range uniq {
		// Convert 1-based inclusive range to a 0-based half-open slice range.
		lo := h.OldStart - 1
		if lo < 0 {
			lo = 0
		}
		hi := h.OldEnd
		if hi > len(lines) {
			hi = len(lines)
		}
		if len(h.OldLines) == 0 {
			// Pure insertion before line OldStart (anchor semantics).
			anchor := h.OldStart - 1
			if anchor < 0 {
				anchor = 0
			}
			if anchor > len(lines) {
				anchor = len(lines)
			}
			lines = insertAt(lines, anchor, h.NewLines)
			continue
		}
		lines = replaceRange(lines, lo, hi, h.NewLines)
	}
	return joinLines(lines, trailingNL)
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
