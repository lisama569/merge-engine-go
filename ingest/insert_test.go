package ingest

import (
	"testing"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// structuredWith builds a one-change structured envelope carrying hunks.
func structuredWith(h StructuredHunk) *Envelope {
	return &Envelope{SourceID: "s1", Kind: SourceStructured, Structured: &StructuredInput{
		Changes: []StructuredChange{{Path: "f", Op: model.OpModify, Hunks: []StructuredHunk{h}}}}}
}

// TestStructuredInsertionAccepted covers the legal pure-insertion shapes:
// mid-file anchor, end-of-file anchor, empty file (anchor 0) and multi-line
// insertion. Each must normalize into a zero-old-line hunk.
func TestStructuredInsertionAccepted(t *testing.T) {
	cases := []struct {
		name   string
		hunk   StructuredHunk
		anchor int
		lines  int
	}{
		{"mid file", StructuredHunk{OldStart: 2, OldEnd: 2, NewStart: 2, NewEnd: 2,
			NewLines: []string{"x"}}, 2, 1},
		{"end of file", StructuredHunk{OldStart: 5, OldEnd: 5, NewStart: 5, NewEnd: 6,
			NewLines: []string{"t1", "t2"}}, 5, 2},
		{"empty file", StructuredHunk{OldStart: 0, OldEnd: 0, NewStart: 1, NewEnd: 1,
			NewLines: []string{"first"}}, 0, 1},
		{"before first line", StructuredHunk{OldStart: 1, OldEnd: 1, NewStart: 1, NewEnd: 1,
			NewLines: []string{"top"}}, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := structuredWith(tc.hunk)
			logEnvelope(t, env)
			cs, err := Normalize(env)
			if err != nil {
				t.Fatalf("legal insertion rejected: %v", err)
			}
			h := cs.Changes[0].Hunks[0]
			if len(h.OldLines) != 0 || h.OldStart != tc.anchor || h.OldEnd != tc.anchor {
				t.Fatalf("insertion hunk shape wrong: %+v", h)
			}
			if len(h.NewLines) != tc.lines {
				t.Fatalf("insertion lines lost: %+v", h)
			}
			t.Logf("ACCEPTED insertion anchor=%d new=%v", h.OldStart, h.NewLines)
		})
	}
}

// TestStructuredInsertionLookalikesRejected proves malformed records are
// still rejected with their own distinguishable reasons and are never
// silently reinterpreted as insertions.
func TestStructuredInsertionLookalikesRejected(t *testing.T) {
	cases := []struct {
		name   string
		hunk   StructuredHunk
		reason string
	}{
		// Declares a 3-line old range but ships no old lines: that is a
		// length mismatch, NOT an insertion.
		{"range without old lines", StructuredHunk{
			OldStart: 2, OldEnd: 4, NewStart: 2, NewEnd: 2,
			NewLines: []string{"x"}}, "hunk_length_mismatch"},
		// No old lines and no new lines: changes nothing.
		{"empty hunk", StructuredHunk{
			OldStart: 3, OldEnd: 3}, "empty_hunk"},
		// Negative anchor.
		{"negative anchor", StructuredHunk{
			OldStart: -1, OldEnd: -1, NewLines: []string{"x"}}, "illegal_line_range"},
		// new_lines length disagrees with the declared new range.
		{"new range mismatch", StructuredHunk{
			OldStart: 2, OldEnd: 2, NewStart: 2, NewEnd: 4,
			NewLines: []string{"only-one"}}, "hunk_length_mismatch"},
		// End before start is still an illegal range, not an insertion.
		{"end before start", StructuredHunk{
			OldStart: 4, OldEnd: 2, NewLines: []string{"x"}}, "illegal_line_range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := structuredWith(tc.hunk)
			logEnvelope(t, env)
			_, err := Normalize(env)
			assertReason(t, err, merr.KindInvalidInput, tc.reason)
		})
	}
}

// TestTextDiffPureInsertion confirms the text-diff path also surfaces
// pure insertions (zero old-count hunks) as insertion hunks.
func TestTextDiffPureInsertion(t *testing.T) {
	diff := "+++ b/f.txt\n" +
		"@@ -2,0 +2,2 @@\n" +
		"+ins1\n" +
		"+ins2\n"
	env := &Envelope{SourceID: "s1", Kind: SourceTextDiff, TextDiff: &TextDiffInput{
		Path: "f.txt", Diff: diff}}
	logEnvelope(t, env)
	cs, err := Normalize(env)
	if err != nil {
		t.Fatalf("insertion diff rejected: %v", err)
	}
	h := cs.Changes[0].Hunks[0]
	if len(h.OldLines) != 0 || h.OldStart != h.OldEnd {
		t.Fatalf("text-diff insertion must be zero-width: %+v", h)
	}
	if len(h.NewLines) != 2 {
		t.Fatalf("inserted lines lost: %+v", h)
	}
	t.Logf("ACCEPTED text-diff insertion anchor=%d new=%v", h.OldStart, h.NewLines)
}
