// Package model 定义版本合并引擎使用的统一数据模型。
//
// 所有外部变更来源（文本差异、结构化变更记录）在进入合并流程前
// 都必须被归一化为 ChangeSet；三方状态（基线/我方/对方）使用
// Snapshot 表达。
package model

// Op 表示归一化后的单条变更操作类型。
type Op string

const (
	OpInsert  Op = "insert"  // 插入新行
	OpDelete  Op = "delete"  // 删除既有行
	OpReplace Op = "replace" // 替换既有行区间
)

// StructuredOp 表示结构化变更记录支持的操作类型。
type StructuredOp string

const (
	StructuredCreate  StructuredOp = "create"  // 新建条目
	StructuredUpdate  StructuredOp = "update"  // 修改条目
	StructuredDelete  StructuredOp = "delete"  // 删除条目
	StructuredRename  StructuredOp = "rename"  // 重命名条目（键发生变化）
	StructuredTypeSet StructuredOp = "typeset" // 仅修改条目类型
)

// SourceKind 标识变更来源类别，用于解释合并结论。
type SourceKind string

const (
	SourceTextDiff   SourceKind = "text_diff"
	SourceStructured SourceKind = "structured"
)

// LineRange 描述文本文件中以 1 为起始行号的闭区间 [Start, End]。
// Start==End 表示单行；纯插入使用 End==Start-1 表示插入点在 Start 之前。
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// TextHunk 是归一化后的一段文本改动。
type TextHunk struct {
	Range       LineRange `json:"range"`       // 基线文件上的行区间
	Replacement []string  `json:"replacement"` // 替换后的行（delete 时为空）
}

// Change 是统一变更模型中的最小单位：针对某个文件路径的一次改动。
type Change struct {
	Path       string            `json:"path"`
	Source     SourceKind        `json:"source"`
	Text       *TextHunk         `json:"text,omitempty"`
	Structured *StructuredChange `json:"struct,omitempty"`
}

// StructuredChange 是归一化后的结构化改动。
type StructuredChange struct {
	Op       StructuredOp `json:"op"`
	Key      string       `json:"key,omitempty"`
	NewKey   string       `json:"new_key,omitempty"` // rename 时的目标键
	OldType  string       `json:"old_type,omitempty"`
	NewType  string       `json:"new_type,omitempty"`
	OldValue string       `json:"old_value,omitempty"`
	NewValue string       `json:"new_value,omitempty"`
}

// RawTextDiff 是外部输入的文本差异记录（未归一化）。
type RawTextDiff struct {
	Path     string   `json:"path"`
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Lines    []string `json:"lines"`
	Body     string   `json:"body,omitempty"` // 可选：统一 diff 文本体
}

// RawStructuredChange 是外部输入的结构化变更记录（未归一化）。
type RawStructuredChange struct {
	Path     string `json:"path"`
	Op       string `json:"op"`
	Key      string `json:"key"`
	NewKey   string `json:"new_key,omitempty"`
	OldType  string `json:"old_type,omitempty"`
	NewType  string `json:"new_type,omitempty"`
	OldValue string `json:"old_value,omitempty"`
	NewValue string `json:"new_value,omitempty"`
}

// RawChangeSet 是一次并行修改提交的原始变更集合。
type RawChangeSet struct {
	Author     string                `json:"author"`
	TextDiffs  []RawTextDiff         `json:"text_diffs"`
	Structured []RawStructuredChange `json:"structured"`
}

// StructuredEntry 是结构化文件中的一个具名条目。
type StructuredEntry struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// FileState 表示某一路径在某一方状态下的文件内容。
// 文本文件使用 Lines；结构化文件使用 Entries。
// Deleted 为 true 表示该文件在该方状态下已被删除。
type FileState struct {
	Lines   []string                   `json:"lines,omitempty"`
	Entries map[string]StructuredEntry `json:"entries,omitempty"`
	Type    string                     `json:"type,omitempty"`
	Deleted bool                       `json:"deleted,omitempty"`
}

// Snapshot 是某一方（基线/我方/对方）的完整只读状态视图。
// Applied 记录本快照已应用变更的内容指纹，使同一变更集重复应用时结果稳定。
type Snapshot struct {
	Label   string               `json:"label"`
	Files   map[string]FileState `json:"files"`
	Applied []string             `json:"applied,omitempty"`
}

// ChangeSet 是归一化并通过校验后的变更集合。
type ChangeSet struct {
	Author  string   `json:"author"`
	Changes []Change `json:"changes"`
}

// ConflictKind 标识冲突的语义类别。
type ConflictKind string

const (
	ConflictOverlap        ConflictKind = "overlap"          // 行区间重叠
	ConflictRename         ConflictKind = "rename_clash"     // 重命名冲突
	ConflictDeleteModify   ConflictKind = "delete_modify"    // 一方删除、另一方修改
	ConflictTypeChange     ConflictKind = "type_change"      // 对同一条目做了不一致的类型变更
	ConflictStructured     ConflictKind = "structured_clash" // 同键结构化改动冲突
	ConflictCreateExisting ConflictKind = "create_existing"  // 新建已存在条目
)

// Conflict 描述一处必须由人裁决、引擎不得任选其一的冲突。
type Conflict struct {
	Kind    ConflictKind `json:"kind"`
	Path    string       `json:"path"`
	Detail  string       `json:"detail"`
	Authors []string     `json:"authors"`
}

// RejectCode 是逐条输入拒绝原因的可区分分类码。
type RejectCode string

const (
	RejectMissingPath        RejectCode = "missing_path"           // 缺少文件路径
	RejectUnknownOp          RejectCode = "unknown_op"             // 未知操作类型
	RejectMissingKey         RejectCode = "missing_key"            // 结构化变更缺少键
	RejectMissingNewKey      RejectCode = "missing_new_key"        // rename 缺少目标键
	RejectInvalidRange       RejectCode = "invalid_line_range"     // 行区间非法
	RejectRangeOutOfBounds   RejectCode = "line_range_outofbounds" // 行区间越过基线长度
	RejectMissingDiffBody    RejectCode = "missing_diff_body"      // 文本差异缺少内容
	RejectInvalidHunkHeader  RejectCode = "invalid_hunk_header"    // hunk 头计数与声明不一致
	RejectUnknownStructField RejectCode = "unknown_struct_field"   // 结构化记录含未定义字段语义
	RejectEmptyChangeSet     RejectCode = "empty_changeset"        // 变更集为空且缺少作者
)

// Rejection 描述一条因输入非法而被拒绝的原始变更。
type Rejection struct {
	Index  int        `json:"index"`
	Path   string     `json:"path"`
	Reason RejectCode `json:"reason"`
	Detail string     `json:"detail"`
}

// MergeReport 是一次合并的完整结论，保证稳定、可解释、可复现。
type MergeReport struct {
	BaseLabel    string      `json:"base_label"`
	Merged       *Snapshot   `json:"merged"`
	AutoMerged   []Change    `json:"auto_merged"`
	Conflicts    []Conflict  `json:"conflicts"`
	Rejections   []Rejection `json:"rejections"`
	AppliedSets  []string    `json:"applied_sets"`
	ReplayToken  string      `json:"replay_token"`
	HasConflict  bool        `json:"has_conflict"`
	HasRejection bool        `json:"has_rejection"`
}
