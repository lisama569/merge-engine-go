// Package mergeengine is the public facade of the local three-way merge
// engine. It wires ingestion, storage and merging together and guarantees
// that no raw third-party error ever reaches callers.
package mergeengine

import (
	"github.com/lisama569/merge-engine-go/ingest"
	"github.com/lisama569/merge-engine-go/merge"
	"github.com/lisama569/merge-engine-go/model"
	"github.com/lisama569/merge-engine-go/storage"
)

// Engine is the top-level, ready-to-use merge engine.
type Engine struct {
	store  *storage.Store
	merger *merge.Engine
}

// New creates an engine backed by fresh local storage.
func New() *Engine {
	st := storage.New()
	return &Engine{store: st, merger: merge.NewEngine(st)}
}

// SaveSnapshot persists a snapshot (base/side state simulation).
func (e *Engine) SaveSnapshot(snap *model.Snapshot) error {
	return e.store.PutSnapshot(snap)
}

// LoadSnapshot reads a persisted snapshot.
func (e *Engine) LoadSnapshot(name string) (*model.Snapshot, error) {
	return e.store.GetSnapshot(name)
}

// Ingest validates and persists one raw input envelope. Any malformed
// input is rejected with a typed merr error and nothing is stored.
func (e *Engine) Ingest(env *ingest.Envelope) (*model.ChangeSet, error) {
	cs, err := ingest.Normalize(env)
	if err != nil {
		return nil, err
	}
	if err := e.store.PutChangeSet(cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// Merge runs a three-way merge of the given change sources over a base.
func (e *Engine) Merge(baseName, resultName string, sourceIDs ...string) (*model.MergeResult, error) {
	return e.merger.Merge(baseName, resultName, sourceIDs...)
}

// Replay merges the same sources repeatedly, asserting idempotency.
func (e *Engine) Replay(baseName, resultName string, replays int, sourceIDs ...string) (*model.MergeResult, error) {
	return e.merger.Replay(baseName, resultName, replays, sourceIDs...)
}
