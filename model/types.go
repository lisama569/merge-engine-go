// Package model defines the normalized representation shared by every
// subsystem of the merge engine: snapshots, changes and merge results.
package model

// ChangeOp is the operation kind of a normalized Change.
type ChangeOp string

const (
	OpAdd        ChangeOp = "add"
	OpDelete     ChangeOp = "delete"
	OpModify     ChangeOp = "modify"
	OpRename     ChangeOp = "rename"
	OpTypeChange ChangeOp = "typechange"
)

// Hunk is one line-range edit inside a file.
// Line numbers are 1-based and inclusive on both ends.
// For pure insertions OldStart==OldEnd denotes the insertion anchor line
// and the span covers OldEnd-OldStart+1 original lines; an insertion uses
// OldCount==0 expressed as OldStart==OldEnd (anchor semantics are checked
// by ingest validation).
type Hunk struct {
	OldStart int      `json:"old_start"`
	OldEnd   int      `json:"old_end"`
	NewStart int      `json:"new_start"`
	NewEnd   int      `json:"new_end"`
	OldLines []string `json:"old_lines,omitempty"`
	NewLines []string `json:"new_lines,omitempty"`
}

// Change is one normalized file-level change originating from any source.
type Change struct {
	// Path is the path as seen in the side that produced the change
	// (the "new" path for renames).
	Path string `json:"path"`
	// OldPath is set (and differs from Path) only for renames.
	OldPath string   `json:"old_path,omitempty"`
	Op      ChangeOp `json:"op"`
	// Hunks carry line-level edits for OpModify. They must be empty for
	// add/delete/rename-without-content-change.
	Hunks []Hunk `json:"hunks,omitempty"`
	// NewContent is the full post-image for add / rename-with-content.
	NewContent string `json:"new_content,omitempty"`
	// ChangeType records a file<->directory kind change for OpTypeChange.
	ChangeType string `json:"change_type,omitempty"`
}

// ChangeSet is one normalized batch of changes produced by one source.
type ChangeSet struct {
	// SourceID uniquely identifies the origin (author/branch/txn).
	// The same SourceID replayed must produce an identical result.
	SourceID string   `json:"source_id"`
	Changes  []Change `json:"changes"`
}

// Snapshot is a named file-tree state. Path keys use forward slashes.
// A directory is represented by a key with a trailing slash and empty
// content (e.g. "dir/").
type Snapshot struct {
	Name  string            `json:"name"`
	Files map[string]string `json:"files"`
}

// Clone returns a deep copy of the snapshot.
func (s *Snapshot) Clone() *Snapshot {
	if s == nil {
		return nil
	}
	files := make(map[string]string, len(s.Files))
	for k, v := range s.Files {
		files[k] = v
	}
	return &Snapshot{Name: s.Name, Files: files}
}

// Has reports whether path exists in the snapshot.
func (s *Snapshot) Has(path string) bool {
	if s == nil || s.Files == nil {
		return false
	}
	_, ok := s.Files[path]
	return ok
}

// Get returns the content and existence flag for path.
func (s *Snapshot) Get(path string) (string, bool) {
	if s == nil || s.Files == nil {
		return "", false
	}
	v, ok := s.Files[path]
	return v, ok
}
