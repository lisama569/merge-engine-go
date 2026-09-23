// Package store 用本地内存存储模拟基线、变更与目标三方状态，
// 不依赖任何外部服务，保证合并结论可复现。
package store

import (
	"fmt"
	"sort"
	"sync"

	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/model"
)

// Store 保存命名快照与已登记的变更集。
type Store struct {
	mu        sync.RWMutex
	snapshots map[string]model.Snapshot
	sets      map[string]model.ChangeSet
	applied   map[string][]string // target -> 已应用变更集作者（有序、去重）
	tokens    map[string]string   // target -> 最近一次合并的 replay token
}

// New 创建空的本地存储。
func New() *Store {
	return &Store{
		snapshots: map[string]model.Snapshot{},
		sets:      map[string]model.ChangeSet{},
		applied:   map[string][]string{},
		tokens:    map[string]string{},
	}
}

// PutSnapshot 写入（或覆盖）一个命名快照，并深拷贝以防外部篡改。
func (s *Store) PutSnapshot(snap model.Snapshot) error {
	if snap.Files == nil {
		snap.Files = map[string]model.FileState{}
	}
	cp := cloneSnapshot(snap)
	s.mu.Lock()
	s.snapshots[snap.Label] = cp
	s.mu.Unlock()
	return nil
}

// GetSnapshot 读取命名快照（返回深拷贝）。
func (s *Store) GetSnapshot(name string) (model.Snapshot, error) {
	s.mu.RLock()
	snap, ok := s.snapshots[name]
	s.mu.RUnlock()
	if !ok {
		return model.Snapshot{}, errs.New(errs.CodeNotFound,
			fmt.Sprintf("snapshot %q not found in local store", name), nil)
	}
	return cloneSnapshot(snap), nil
}

// PutChangeSet 登记一个归一化变更集（按作者幂等：重复登记内容一致时
// 视为同一变更集；内容不一致则明确报错而不是静默覆盖）。
func (s *Store) PutChangeSet(set model.ChangeSet) error {
	if set.Author == "" {
		return errs.New(errs.CodeInvalidInput, "change set author is empty", nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ex, ok := s.sets[set.Author]; ok && !sameSet(ex, set) {
		return errs.New(errs.CodeConflict,
			fmt.Sprintf("change set author %q already registered with different content", set.Author), nil)
	}
	s.sets[set.Author] = cloneSet(set)
	return nil
}

// GetChangeSet 按作者读取变更集（返回拷贝）。
func (s *Store) GetChangeSet(author string) (model.ChangeSet, error) {
	s.mu.RLock()
	set, ok := s.sets[author]
	s.mu.RUnlock()
	if !ok {
		return model.ChangeSet{}, errs.New(errs.CodeNotFound,
			fmt.Sprintf("change set %q not registered", author), nil)
	}
	return cloneSet(set), nil
}

// HasChangeSet 判断变更集是否已登记。
func (s *Store) HasChangeSet(author string) bool {
	s.mu.RLock()
	_, ok := s.sets[author]
	s.mu.RUnlock()
	return ok
}

// MarkApplied 记录某变更集已应用到目标快照（幂等，按作者去重保序）。
func (s *Store) MarkApplied(target, author string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.applied[target] {
		if a == author {
			return
		}
	}
	s.applied[target] = append(s.applied[target], author)
}

// AppliedAuthors 返回目标快照已应用的变更集作者列表（有序）。
func (s *Store) AppliedAuthors(target string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.applied[target]...)
}

// SaveToken 保存目标快照的重放令牌。
func (s *Store) SaveToken(target, token string) {
	s.mu.Lock()
	s.tokens[target] = token
	s.mu.Unlock()
}

// Token 读取目标快照的重放令牌。
func (s *Store) Token(target string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokens[target]
}

func cloneSnapshot(s model.Snapshot) model.Snapshot {
	cp := model.Snapshot{Label: s.Label, Files: map[string]model.FileState{}}
	if s.Applied != nil {
		cp.Applied = append([]string(nil), s.Applied...)
	}
	for p, fs := range s.Files {
		nfs := model.FileState{Type: fs.Type, Deleted: fs.Deleted}
		if fs.Lines != nil {
			nfs.Lines = append([]string(nil), fs.Lines...)
		}
		if fs.Entries != nil {
			nfs.Entries = map[string]model.StructuredEntry{}
			for k, v := range fs.Entries {
				nfs.Entries[k] = v
			}
		}
		cp.Files[p] = nfs
	}
	return cp
}

func cloneSet(set model.ChangeSet) model.ChangeSet {
	cp := model.ChangeSet{Author: set.Author, Changes: append([]model.Change(nil), set.Changes...)}
	return cp
}

func sameSet(a, b model.ChangeSet) bool {
	if a.Author != b.Author || len(a.Changes) != len(b.Changes) {
		return false
	}
	sa := append([]model.Change(nil), a.Changes...)
	sb := append([]model.Change(nil), b.Changes...)
	sortChangesStore(sa)
	sortChangesStore(sb)
	for i := range sa {
		if fingerprintStore(sa[i]) != fingerprintStore(sb[i]) {
			return false
		}
	}
	return true
}

func sortChangesStore(ch []model.Change) {
	sort.SliceStable(ch, func(i, j int) bool {
		return fingerprintStore(ch[i]) < fingerprintStore(ch[j])
	})
}

func fingerprintStore(c model.Change) string {
	s := c.Path + "|" + string(c.Source)
	if c.Text != nil {
		s += fmt.Sprintf("|%d,%d:%v", c.Text.Range.Start, c.Text.Range.End, c.Text.Replacement)
	}
	if c.Structured != nil {
		st := c.Structured
		s += fmt.Sprintf("|%s:%s:%s:%s:%s", st.Op, st.Key, st.NewKey, st.NewType, st.NewValue)
	}
	return s
}
