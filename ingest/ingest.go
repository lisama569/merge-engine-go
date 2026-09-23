// Package ingest normalizes heterogeneous change sources into
// model.ChangeSet. Incomplete envelopes, illegal line ranges and missing
// required fields are rejected with distinguishable merr errors rather
// than silently skipped.
//
// Two source encodings are accepted:
//
//   - text_diff:  a unified-diff text parsed through the third-party lib,
//     whose native errors are normalized (syntax vs internal fault).
//   - structured: explicit change records validated field by field.
package ingest

import (
	"fmt"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// SourceKind enumerates accepted input encodings.
type SourceKind string

const (
	// SourceTextDiff: a unified-diff text parsed through the third-party lib.
	SourceTextDiff SourceKind = "text_diff"
	// SourceStructured: an explicit structured change record (JSON-like).
	SourceStructured SourceKind = "structured"
)

// Envelope is the common input wrapper.
type Envelope struct {
	// SourceID is required and identifies the change origin.
	SourceID string     `json:"source_id"`
	Kind     SourceKind `json:"kind"`
	// TextDiff is populated when Kind == SourceTextDiff.
	TextDiff *TextDiffInput `json:"text_diff,omitempty"`
	// Structured is populated when Kind == SourceStructured.
	Structured *StructuredInput `json:"structured,omitempty"`
}

// TextDiffInput is a raw unified diff for one target file.
type TextDiffInput struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}

// StructuredHunk is the wire shape for a structured line-range edit.
type StructuredHunk struct {
	OldStart int      `json:"old_start"`
	OldEnd   int      `json:"old_end"`
	NewStart int      `json:"new_start"`
	NewEnd   int      `json:"new_end"`
	OldLines []string `json:"old_lines,omitempty"`
	NewLines []string `json:"new_lines,omitempty"`
}

// StructuredChange is one explicit file-level change record.
type StructuredChange struct {
	Path       string           `json:"path"`
	OldPath    string           `json:"old_path,omitempty"`
	Op         model.ChangeOp   `json:"op"`
	Hunks      []StructuredHunk `json:"hunks,omitempty"`
	NewContent string           `json:"new_content,omitempty"`
	ChangeType string           `json:"change_type,omitempty"`
}

// StructuredInput is a batch of explicit change records.
type StructuredInput struct {
	Changes []StructuredChange `json:"changes"`
}

// Normalize converts one envelope into a validated model.ChangeSet.
// It never partially succeeds: either all records are valid or an error
// describing the first rejection is returned.
func Normalize(env *Envelope) (*model.ChangeSet, error) {
	if env == nil {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "nil_envelope",
			"input envelope is nil")
	}
	if env.SourceID == "" {
		return nil, merr.New(merr.KindInvalidInput, "ingest", "missing_source_id",
			"envelope.source_id is required")
	}

	var changes []model.Change
	var err error
	switch env.Kind {
	case SourceTextDiff:
		changes, err = normalizeTextDiff(env.TextDiff)
	case SourceStructured:
		changes, err = normalizeStructured(env.Structured)
	case "":
		return nil, merr.New(merr.KindInvalidInput, "ingest", "missing_kind",
			"envelope.kind is required (text_diff|structured)").
			WithContext(map[string]any{"source_id": env.SourceID})
	default:
		return nil, merr.New(merr.KindInvalidInput, "ingest", "unsupported_kind",
			fmt.Sprintf("envelope.kind %q is not supported", env.Kind)).
			WithContext(map[string]any{"source_id": env.SourceID, "kind": string(env.Kind)})
	}
	if err != nil {
		return nil, err
	}
	return &model.ChangeSet{SourceID: env.SourceID, Changes: changes}, nil
}
