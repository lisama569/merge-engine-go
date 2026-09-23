package store

import (
	"testing"

	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/model"
)

func TestPutGetSnapshotDeepCopy(t *testing.T) {
	s := New()
	snap := model.Snapshot{Label: "base", Files: map[string]model.FileState{
		"f": {Lines: []string{"a"}},
	}}
	if err := s.PutSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSnapshot("base")
	if err != nil {
		t.Fatal(err)
	}
	got.Files["f"].Lines[0] = "tampered"
	again, _ := s.GetSnapshot("base")
	if again.Files["f"].Lines[0] != "a" {
		t.Fatal("store must return defensive copies")
	}
}

func TestChangeSetIdempotentRegistration(t *testing.T) {
	s := New()
	set := model.ChangeSet{Author: "a", Changes: []model.Change{
		{Path: "f", Source: model.SourceTextDiff},
	}}
	if err := s.PutChangeSet(set); err != nil {
		t.Fatal(err)
	}
	if err := s.PutChangeSet(set); err != nil {
		t.Fatalf("same set re-registration must be idempotent: %v", err)
	}
	diff := model.ChangeSet{Author: "a", Changes: []model.Change{
		{Path: "g", Source: model.SourceStructured},
	}}
	err := s.PutChangeSet(diff)
	if errs.AsCode(err) != errs.CodeConflict {
		t.Fatalf("same author with different content must conflict, got %v", err)
	}
}

func TestMarkAppliedDedup(t *testing.T) {
	s := New()
	s.MarkApplied("target", "a")
	s.MarkApplied("target", "b")
	s.MarkApplied("target", "a")
	got := s.AppliedAuthors("target")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("applied authors must dedup and keep order, got %v", got)
	}
}
