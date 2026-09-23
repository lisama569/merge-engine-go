// Package thirddiff simulates an external third-party unified-diff
// parsing library. It deliberately speaks its OWN error dialect
// (DiffSyntaxError) and may panic on internal faults; callers must never
// let those raw errors escape the engine's public boundary.
//
// Supported wire form (one file):
//
//	+++ b/path
//	@@ -oldStart,oldCount +newStart,newCount @@
//	 context
//	-removed
//	+added
//
// The special line "LIBRARY-BOOM" inside a diff body triggers an internal
// fault panic, used to exercise recovery/normalization paths.
package thirddiff

import (
	"fmt"
	"strconv"
	"strings"
)

// RawHunk is the library's native hunk shape (intentionally different
// from the engine's model to prove real translation occurs).
type RawHunk struct {
	OrigStart  int
	OrigCount  int
	NewStart   int
	NewCount   int
	OrigLines  []string
	AddedLines []string
}

// RawPatch is the library's native parse result.
type RawPatch struct {
	TargetPath string
	Hunks      []RawHunk
}

// DiffSyntaxError is the library's native syntax error type.
type DiffSyntaxError struct {
	Line    int
	Message string
	Raw     string
}

func (e *DiffSyntaxError) Error() string {
	return fmt.Sprintf("diff syntax error at line %d: %s (raw=%q)", e.Line, e.Message, e.Raw)
}

// LibraryFault is the library's native "internal" error type.
type LibraryFault struct {
	Code    int
	Message string
}

func (e *LibraryFault) Error() string {
	return fmt.Sprintf("library fault %d: %s", e.Code, e.Message)
}

func syntaxErr(line int, msg, raw string) *DiffSyntaxError {
	return &DiffSyntaxError{Line: line, Message: msg, Raw: raw}
}

// Parse parses a minimal unified-diff text.
//
// It returns *DiffSyntaxError on malformed syntax and panics with
// *LibraryFault on simulated internal breakage.
func Parse(text string) (*RawPatch, error) {
	lines := strings.Split(text, "\n")
	patch := &RawPatch{}
	var cur *RawHunk
	headerSeen := false
	hunkOpen := false

	flush := func() {
		if cur != nil {
			patch.Hunks = append(patch.Hunks, *cur)
			cur = nil
			hunkOpen = false
		}
	}

	for i, raw := range lines {
		lineNo := i + 1
		if raw == "" {
			continue
		}
		// Sentinel check runs before prefix dispatch so a leading context
		// space cannot mask the simulated internal-crash marker.
		if strings.TrimSpace(raw) == "LIBRARY-BOOM" {
			panic(&LibraryFault{Code: 500, Message: "internal allocator failure on sentinel"})
		}
		switch {
		case strings.HasPrefix(raw, "+++"):
			flush()
			headerSeen = true
			fields := strings.Fields(raw)
			if len(fields) < 2 {
				return nil, syntaxErr(lineNo, "missing target path after +++", raw)
			}
			path := fields[1]
			path = strings.TrimPrefix(path, "b/")
			if path == "" || path == "/" {
				return nil, syntaxErr(lineNo, "illegal target path", raw)
			}
			patch.TargetPath = path
		case strings.HasPrefix(raw, "@@"):
			if !headerSeen {
				return nil, syntaxErr(lineNo, "hunk header before file header", raw)
			}
			flush()
			h, err := parseHunkHeader(raw)
			if err != nil {
				return nil, syntaxErr(lineNo, err.Error(), raw)
			}
			cur = &h
			hunkOpen = true
		case strings.HasPrefix(raw, "+"):
			if !hunkOpen {
				return nil, syntaxErr(lineNo, "added line outside hunk", raw)
			}
			cur.AddedLines = append(cur.AddedLines, strings.TrimPrefix(raw, "+"))
		case strings.HasPrefix(raw, "-"):
			if !hunkOpen {
				return nil, syntaxErr(lineNo, "removed line outside hunk", raw)
			}
			cur.OrigLines = append(cur.OrigLines, strings.TrimPrefix(raw, "-"))
		case strings.HasPrefix(raw, " "):
			if !hunkOpen {
				return nil, syntaxErr(lineNo, "context line outside hunk", raw)
			}
			line := strings.TrimPrefix(raw, " ")
			cur.OrigLines = append(cur.OrigLines, line)
			cur.AddedLines = append(cur.AddedLines, line)
		default:
			return nil, syntaxErr(lineNo, "unrecognized diff line prefix", raw)
		}
	}
	flush()
	return patch, nil
}

// parseHunkHeader parses: @@ -oldStart,oldCount +newStart,newCount @@ optional-ctx
func parseHunkHeader(raw string) (RawHunk, error) {
	body := strings.Trim(raw, "@")
	body = strings.TrimSpace(body)
	parts := strings.Fields(body)
	if len(parts) < 2 {
		return RawHunk{}, fmt.Errorf("malformed hunk header")
	}
	var h RawHunk
	var err error
	if h.OrigStart, h.OrigCount, err = parseRange(parts[0], "-"); err != nil {
		return RawHunk{}, err
	}
	if h.NewStart, h.NewCount, err = parseRange(parts[1], "+"); err != nil {
		return RawHunk{}, err
	}
	return h, nil
}

func parseRange(token, marker string) (int, int, error) {
	if !strings.HasPrefix(token, marker) {
		return 0, 0, fmt.Errorf("range must start with %q", marker)
	}
	token = strings.TrimPrefix(token, marker)
	startStr, countStr, hasCount := strings.Cut(token, ",")
	start, err := strconv.Atoi(startStr)
	if err != nil {
		return 0, 0, fmt.Errorf("range start not numeric: %q", token)
	}
	count := 1
	if hasCount {
		count, err = strconv.Atoi(countStr)
		if err != nil {
			return 0, 0, fmt.Errorf("range count not numeric: %q", token)
		}
	}
	return start, count, nil
}
