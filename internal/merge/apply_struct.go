package merge

import (
	"fmt"

	"github.com/lisama569/merge-engine-go/internal/model"
)

// applyStructuredChange 把一条结构化变更作用于文件状态。
//
// 幂等：create 对已存在且相同的条目保持原样；delete 对不存在的键
// 不产生变化；rename/update/typeset 在内容未变时结果稳定。
// 与当前状态矛盾的应用（如 create 已存在的不同条目）记入 conflicts，
// 由调用方决定阻塞，绝不静默覆盖。
func applyStructuredChange(fs model.FileState, c model.Change, author string, conflicts *[]model.Conflict) model.FileState {
	st := c.Structured
	if fs.Entries == nil {
		fs.Entries = map[string]model.StructuredEntry{}
	}
	entries := fs.Entries

	switch st.Op {
	case model.StructuredCreate:
		if ex, ok := entries[st.Key]; ok {
			if ex.Type == st.NewType && ex.Value == st.NewValue {
				// 幂等重放：相同 create 不产生变化
				break
			}
			*conflicts = append(*conflicts, model.Conflict{
				Kind: model.ConflictCreateExisting, Path: c.Path,
				Detail:  fmt.Sprintf("create target key %q already exists", st.Key),
				Authors: []string{author},
			})
			break
		}
		entries[st.Key] = model.StructuredEntry{Type: st.NewType, Value: st.NewValue}

	case model.StructuredUpdate:
		ex, ok := entries[st.Key]
		if !ok {
			*conflicts = append(*conflicts, model.Conflict{
				Kind: model.ConflictStructured, Path: c.Path,
				Detail:  fmt.Sprintf("update target key %q does not exist", st.Key),
				Authors: []string{author},
			})
			break
		}
		if st.NewType != "" {
			ex.Type = st.NewType
		}
		ex.Value = st.NewValue
		entries[st.Key] = ex

	case model.StructuredTypeSet:
		ex, ok := entries[st.Key]
		if !ok {
			*conflicts = append(*conflicts, model.Conflict{
				Kind: model.ConflictTypeChange, Path: c.Path,
				Detail:  fmt.Sprintf("typeset target key %q does not exist", st.Key),
				Authors: []string{author},
			})
			break
		}
		ex.Type = st.NewType
		entries[st.Key] = ex

	case model.StructuredDelete:
		// 幂等：键不存在时删除是空操作
		delete(entries, st.Key)

	case model.StructuredRename:
		ex, ok := entries[st.Key]
		if !ok {
			if _, ok2 := entries[st.NewKey]; ok2 {
				// 已经处于重命名后的状态：幂等重放
				break
			}
			*conflicts = append(*conflicts, model.Conflict{
				Kind: model.ConflictRename, Path: c.Path,
				Detail:  fmt.Sprintf("rename source key %q does not exist", st.Key),
				Authors: []string{author},
			})
			break
		}
		if dst, ok2 := entries[st.NewKey]; ok2 && dst != ex {
			*conflicts = append(*conflicts, model.Conflict{
				Kind: model.ConflictRename, Path: c.Path,
				Detail:  fmt.Sprintf("rename destination key %q already occupied", st.NewKey),
				Authors: []string{author},
			})
			break
		}
		delete(entries, st.Key)
		entries[st.NewKey] = ex
	}

	fs.Entries = entries
	return fs
}
