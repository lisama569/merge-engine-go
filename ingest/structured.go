package ingest

import (
	"fmt"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// normalizeStructured validates every record and translates it into model
// changes. Nothing is silently skipped: the first illegal record rejects
// the whole envelope with a distinguishable reason code.
func normalizeStructured(in *StructuredInput) ([]model.Change, error) {
	if in == nil {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "missing_structured",
			"kind=structured requires a structured payload")
	}
	if len(in.Changes) == 0 {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "empty_changes",
			"structured.changes must contain at least one record")
	}

	seen := map[string]int{} // path -> record index, to flag duplicates
	out := make([]model.Change, 0, len(in.Changes))
	for i, rc := range in.Changes {
		ctx := map[string]any{"index": i, "path": rc.Path}
		if rc.Path == "" {
			return nil, merr.New(merr.KindInvalidInput, "ingest", "missing_path",
				fmt.Sprintf("changes[%d]: path is required", i)).WithContext(ctx)
		}
		if prev, dup := seen[rc.Path]; dup {
			return nil, merr.New(merr.KindInvalidInput, "ingest", "duplicate_path",
				fmt.Sprintf("changes[%d]: path %q already used by changes[%d]", i, rc.Path, prev)).
				WithContext(ctx)
		}
		seen[rc.Path] = i

		ch, err := convertStructuredChange(rc, i)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, nil
}

func convertStructuredChange(rc StructuredChange, idx int) (model.Change, error) {
	ctx := map[string]any{"index": idx, "path": rc.Path, "op": string(rc.Op)}
	ch := model.Change{Path: rc.Path, OldPath: rc.OldPath, Op: rc.Op,
		NewContent: rc.NewContent, ChangeType: rc.ChangeType}

	switch rc.Op {
	case model.OpAdd:
		if len(rc.Hunks) != 0 {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "hunks_not_allowed",
				fmt.Sprintf("changes[%d]: add must not carry hunks; use new_content", idx)).WithContext(ctx)
		}
		// new_content may legitimately be empty for an empty file.
	case model.OpDelete:
		if len(rc.Hunks) != 0 || rc.NewContent != "" {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "delete_with_payload",
				fmt.Sprintf("changes[%d]: delete must not carry hunks or new_content", idx)).WithContext(ctx)
		}
	case model.OpModify:
		if rc.NewContent != "" {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "modify_with_full_content",
				fmt.Sprintf("changes[%d]: modify must use hunks, not new_content", idx)).WithContext(ctx)
		}
		if len(rc.Hunks) == 0 {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "missing_hunks",
				fmt.Sprintf("changes[%d]: modify requires at least one hunk", idx)).WithContext(ctx)
		}
		hunks := make([]model.Hunk, 0, len(rc.Hunks))
		for j, sh := range rc.Hunks {
			h, err := convertHunk(sh, idx, j)
			if err != nil {
				return model.Change{}, err
			}
			hunks = append(hunks, h)
		}
		ch.Hunks = hunks
	case model.OpRename:
		if rc.OldPath == "" {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "missing_old_path",
				fmt.Sprintf("changes[%d]: rename requires old_path", idx)).WithContext(ctx)
		}
		if rc.OldPath == rc.Path {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "rename_same_path",
				fmt.Sprintf("changes[%d]: rename old_path equals path", idx)).WithContext(ctx)
		}
		if len(rc.Hunks) != 0 {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "hunks_not_allowed",
				fmt.Sprintf("changes[%d]: rename must not carry hunks", idx)).WithContext(ctx)
		}
	case model.OpTypeChange:
		if rc.ChangeType == "" {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "missing_change_type",
				fmt.Sprintf("changes[%d]: typechange requires change_type", idx)).WithContext(ctx)
		}
		if rc.ChangeType != "file_to_dir" && rc.ChangeType != "dir_to_file" {
			return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "illegal_change_type",
				fmt.Sprintf("changes[%d]: change_type must be file_to_dir|dir_to_file", idx)).WithContext(ctx)
		}
	default:
		return model.Change{}, merr.New(merr.KindInvalidInput, "ingest", "unknown_op",
			fmt.Sprintf("changes[%d]: unsupported op %q", idx, rc.Op)).WithContext(ctx)
	}
	return ch, nil
}

func convertHunk(sh StructuredHunk, ci, hi int) (model.Hunk, error) {
	ctx := map[string]any{"change_index": ci, "hunk_index": hi,
		"old_start": sh.OldStart, "old_end": sh.OldEnd,
		"new_start": sh.NewStart, "new_end": sh.NewEnd}
	reason := func(code, msg string) error {
		return merr.New(merr.KindInvalidInput, "ingest", code, msg).WithContext(ctx)
	}
	if sh.OldStart < 0 || sh.OldEnd < 0 || sh.NewStart < 0 || sh.NewEnd < 0 {
		return model.Hunk{}, reason("illegal_line_range", "line numbers must be >= 0")
	}
	if sh.OldStart > 0 && sh.OldEnd < sh.OldStart {
		return model.Hunk{}, reason("illegal_line_range", "old_end precedes old_start")
	}
	if sh.NewStart > 0 && sh.NewEnd < sh.NewStart {
		return model.Hunk{}, reason("illegal_line_range", "new_end precedes new_start")
	}
	oldCount := 0
	if sh.OldStart > 0 {
		oldCount = sh.OldEnd - sh.OldStart + 1
	}
	newCount := 0
	if sh.NewStart > 0 {
		newCount = sh.NewEnd - sh.NewStart + 1
	}
	if len(sh.OldLines) != oldCount {
		return model.Hunk{}, reason("hunk_length_mismatch",
			fmt.Sprintf("old_lines length %d disagrees with range length %d", len(sh.OldLines), oldCount))
	}
	if len(sh.NewLines) != newCount {
		return model.Hunk{}, reason("hunk_length_mismatch",
			fmt.Sprintf("new_lines length %d disagrees with range length %d", len(sh.NewLines), newCount))
	}
	if oldCount == 0 && newCount == 0 {
		return model.Hunk{}, reason("empty_hunk", "hunk changes nothing")
	}
	return model.Hunk{
		OldStart: sh.OldStart, OldEnd: sh.OldEnd,
		NewStart: sh.NewStart, NewEnd: sh.NewEnd,
		OldLines: append([]string(nil), sh.OldLines...),
		NewLines: append([]string(nil), sh.NewLines...),
	}, nil
}
