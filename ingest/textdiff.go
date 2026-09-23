package ingest

import (
	"github.com/lisama569/merge-engine-go/internal/thirddiff"
	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// normalizeTextDiff parses a raw unified diff through the third-party
// library and translates its native dialect into model changes.
//
// Error mapping (no raw library error ever escapes):
//   - *thirddiff.DiffSyntaxError -> merr.KindSyntax / reason "diff_syntax"
//   - recovered library panic    -> merr.KindLibraryInternal / reason "diff_library_panic"
//   - any other library error    -> merr.KindLibraryInternal / reason "diff_library_fault"
//
// The original context (offending line, raw text, recovered value) is
// retained on the returned EngineError.
func normalizeTextDiff(in *TextDiffInput) ([]model.Change, error) {
	if in == nil {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "missing_text_diff",
			"kind=text_diff requires a text_diff payload")
	}
	if in.Path == "" {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "missing_path",
			"text_diff.path is required")
	}
	if in.Diff == "" {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "missing_diff_body",
			"text_diff.diff body is empty").WithContext(map[string]any{"path": in.Path})
	}

	patch, err := safeParse(in.Diff)
	if err != nil {
		return nil, err
	}

	if patch.TargetPath != "" && patch.TargetPath != in.Path {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "path_mismatch",
			"envelope path does not match diff header target").WithContext(map[string]any{
			"envelope_path": in.Path,
			"header_path":   patch.TargetPath,
		})
	}

	hunks := make([]model.Hunk, 0, len(patch.Hunks))
	for _, rh := range patch.Hunks {
		start, end := rangeFromCount(rh.OrigStart, rh.OrigCount)
		nstart, nend := rangeFromCount(rh.NewStart, rh.NewCount)
		hunks = append(hunks, model.Hunk{
			OldStart: start,
			OldEnd:   end,
			NewStart: nstart,
			NewEnd:   nend,
			OldLines: append([]string(nil), rh.OrigLines...),
			NewLines: append([]string(nil), rh.AddedLines...),
		})
	}
	return []model.Change{{Path: in.Path, Op: model.OpModify, Hunks: hunks}}, nil
}

// safeParse invokes the third-party parser, converting panics and native
// errors into the engine taxonomy.
func safeParse(text string) (patch *thirddiff.RawPatch, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case *thirddiff.LibraryFault:
				err = merr.Wrap(merr.KindLibraryInternal, "thirddiff", "diff_library_panic",
					"third-party diff library panicked internally", v).
					WithContext(map[string]any{"fault_code": v.Code, "recovered": v.Error()})
			case error:
				err = merr.Wrap(merr.KindLibraryInternal, "thirddiff", "diff_library_panic",
					"third-party diff library panicked with an unknown error", v).
					WithContext(map[string]any{"recovered": v.Error()})
			default:
				err = merr.New(merr.KindLibraryInternal, "thirddiff", "diff_library_panic",
					"third-party diff library panicked with a non-error value").
					WithContext(map[string]any{"recovered": r})
			}
		}
	}()

	patch, perr := thirddiff.Parse(text)
	if perr != nil {
		switch e := perr.(type) {
		case *thirddiff.DiffSyntaxError:
			return nil, merr.Wrap(merr.KindSyntax, "thirddiff", "diff_syntax",
				"third-party diff parser rejected malformed syntax", e).
				WithContext(map[string]any{
					"line": e.Line,
					"raw":  e.Raw,
				})
		case *thirddiff.LibraryFault:
			return nil, merr.Wrap(merr.KindLibraryInternal, "thirddiff", "diff_library_fault",
				"third-party diff library reported an internal fault", e).
				WithContext(map[string]any{"fault_code": e.Code})
		default:
			return nil, merr.Wrap(merr.KindLibraryInternal, "thirddiff", "diff_library_fault",
				"third-party diff library returned an unrecognized error", e)
		}
	}
	return patch, nil
}

// rangeFromCount converts start/count (count may be 0 for pure insertion)
// into inclusive 1-based line numbers.
func rangeFromCount(start, count int) (int, int) {
	if count <= 0 {
		// Pure insertion: anchor on the line before start; unidiff uses
		// start pointing at the following line, so the anchor is start.
		return start, start
	}
	return start, start + count - 1
}
