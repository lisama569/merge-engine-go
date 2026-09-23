// Package storage simulates local persistence of base/side/merged
// snapshots and of normalized change sets. It is intentionally local and
// dependency-free so merge outcomes stay reproducible.
//
// Stored values are deep-copied on both Put and Get, making the store a
// reliable local "third-party state" simulation: callers cannot mutate
// previously persisted states.
package storage

import (
	"sort"
	"sync"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

// Store is an in-memory, goroutine-safe snapshot/change-set repository.
type Store struct {
	mu        sync.RWMutex
	snapshots map[string]*model.Snapshot
	changes   map[string]*model.ChangeSet // keyed by SourceID
}

// New returns an empty store.
func New() *Store {
	return &Store{
		snapshots: map[string]*model.Snapshot{},
		changes:   map[string]*model.ChangeSet{},
	}
}

// PutSnapshot stores (or replaces) a snapshot by name.
func (s *Store) PutSnapshot(snap *model.Snapshot) error {
	if snap == nil {
		return merr.New(merr.KindInvalidInput, "storage", "nil_snapshot", "cannot store a nil snapshot")
	}
	if snap.Name == "" {
		return merr.New(merr.KindInvalidInput, "storage", "missing_snapshot_name", "snapshot name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snap.Name] = snap.Clone()
	return nil
}

// GetSnapshot retrieves a snapshot by name.
func (s *Store) GetSnapshot(name string) (*model.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[name]
	if !ok {
		return nil, merr.New(merr.KindNotFound, "storage", "snapshot_not_found",
			"snapshot not found").WithContext(map[string]any{"name": name})
	}
	return snap.Clone(), nil
}

// DeleteSnapshot removes a snapshot by name.
func (s *Store) DeleteSnapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snapshots[name]; !ok {
		return merr.New(merr.KindNotFound, "storage", "snapshot_not_found",
			"snapshot not found").WithContext(map[string]any{"name": name})
	}
	delete(s.snapshots, name)
	return nil
}

// PutChangeSet stores a normalized change set under its SourceID.
func (s *Store) PutChangeSet(cs *model.ChangeSet) error {
	if cs == nil {
		return merr.New(merr.KindInvalidInput, "storage", "nil_changeset", "cannot store a nil change set")
	}
	if cs.SourceID == "" {
		return merr.New(merr.KindInvalidInput, "storage", "missing_source_id", "change set source_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := &model.ChangeSet{SourceID: cs.SourceID, Changes: append([]model.Change(nil), cs.Changes...)}
	for i := range cp.Changes {
		c := &cp.Changes[i]
		if len(c.Hunks) > 0 {
			c.Hunks = append([]model.Hunk(nil), c.Hunks...)
		}
	}
	s.changes[cs.SourceID] = cp
	return nil
}

// GetChangeSet retrieves a change set by SourceID.
func (s *Store) GetChangeSet(sourceID string) (*model.ChangeSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cs, ok := s.changes[sourceID]
	if !ok {
		return nil, merr.New(merr.KindNotFound, "storage", "changeset_not_found",
			"change set not found").WithContext(map[string]any{"source_id": sourceID})
	}
	out := &model.ChangeSet{SourceID: cs.SourceID, Changes: append([]model.Change(nil), cs.Changes...)}
	return out, nil
}

// SnapshotNames returns the stored snapshot names in sorted order.
func (s *Store) SnapshotNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.snapshots))
	for n := range s.snapshots {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
