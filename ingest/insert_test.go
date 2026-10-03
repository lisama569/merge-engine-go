package ingest

import (
	"testing"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

func insertEnv(anchor int, newLines ...string) *Envelope {
	return &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f.txt", Op: model.OpModify, Hunks: []StructuredHunk{{
			OldStart: anchor, OldEnd: anchor,
			NewStart: 1, NewEnd: len(newLines),
			NewLines: newLines,
		}}}}}}
}

// TestStructuredInsertAccepted covers the legal pure-insertion forms:
// mid-file anchor, anchor 0 (empty file / beginning) and an anchor past
// the end of file (append).
func TestStructuredInsertAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		anchor int
		lines  []string
	}{
		{"middle anchor", 2, []string{"ins"}},
		{"anchor zero (empty file / beginning)", 0, []string{"a", "b"}},
		{"anchor past end (append)", 99, []string{"tail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := insertEnv(tc.anchor, tc.lines...)
			logEnvelope(t, env)
			cs, err := Normalize(env)
			if err != nil {
				t.Fatalf("valid insertion rejected: %v", err)
			}
			h := cs.Changes[0].Hunks[0]
			if len(h.OldLines) != 0 || h.OldStart != tc.anchor || h.OldEnd != tc.anchor {
				t.Fatalf("insertion hunk mistranslated: %+v", h)
			}
			if len(h.NewLines) != len(tc.lines) {
				t.Fatalf("new lines lost: %+v", h)
			}
			t.Logf("ACCEPTED insertion anchor=%d new=%v", tc.anchor, h.NewLines)
		})
	}
}

// TestStructuredInsertMalformed proves malformed insertion-shaped records
// are rejected with distinguishable reasons and never silently treated as
// insertions.
func TestStructuredInsertMalformed(t *testing.T) {
	// Empty old_lines but old_start != old_end: not an anchor, ambiguous.
	env := &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f", Op: model.OpModify, Hunks: []StructuredHunk{{
			OldStart: 2, OldEnd: 5, NewStart: 1, NewEnd: 1,
			NewLines: []string{"x"},
		}}}}}}
	logEnvelope(t, env)
	_, err := Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "illegal_line_range")

	// Anchor form but nothing to insert: the hunk changes nothing.
	env = &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f", Op: model.OpModify, Hunks: []StructuredHunk{{
			OldStart: 3, OldEnd: 3,
		}}}}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "empty_hunk")

	// Insertion whose new range disagrees with the provided lines.
	env = &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f", Op: model.OpModify, Hunks: []StructuredHunk{{
			OldStart: 1, OldEnd: 1, NewStart: 1, NewEnd: 3,
			NewLines: []string{"only-one"},
		}}}}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "hunk_length_mismatch")

	// Replacement of existing lines must use a 1-based old_start; a
	// zero start with old lines is an illegal range, not an insertion.
	env = &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f", Op: model.OpModify, Hunks: []StructuredHunk{{
			OldStart: 0, OldEnd: 1, NewStart: 1, NewEnd: 1,
			OldLines: []string{"old"}, NewLines: []string{"new"},
		}}}}}}
	logEnvelope(t, env)
	_, err = Normalize(env)
	assertReason(t, err, merr.KindInvalidInput, "illegal_line_range")
}
