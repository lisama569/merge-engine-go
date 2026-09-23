package storage

import (
	"errors"
	"testing"

	"github.com/lisama569/merge-engine-go/merr"
	"github.com/lisama569/merge-engine-go/model"
)

func TestSnapshotDeepCopy(t *testing.T) {
	st := New()
	snap := &model.Snapshot{Name: "base", Files: map[string]string{"a": "1"}}
	if err := st.PutSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	// Mutate the caller's object after Put; stored state must not change.
	snap.Files["a"] = "mutated"
	snap.Files["b"] = "new"

	got, err := st.GetSnapshot("base")
	if err != nil {
		t.Fatal(err)
	}
	if got.Files["a"] != "1" {
		t.Fatalf("store was mutated through caller object: %v", got.Files)
	}
	if got.Has("b") {
		t.Fatalf("unexpected key leaked into store: %v", got.Files)
	}
	// Mutate the returned object; store stays intact.
	got.Files["a"] = "again"
	got2, _ := st.GetSnapshot("base")
	if got2.Files["a"] != "1" {
		t.Fatalf("store was mutated through returned object: %v", got2.Files)
	}
	t.Log("snapshot copies are independent on both Put and Get")
}

func TestSnapshotNotFound(t *testing.T) {
	st := New()
	_, err := st.GetSnapshot("ghost")
	var ee *merr.EngineError
	if !errors.As(err, &ee) || ee.Kind != merr.KindNotFound || ee.Reason != "snapshot_not_found" {
		t.Fatalf("want typed not_found, got %v", err)
	}
	if err := st.DeleteSnapshot("ghost"); !merr.IsNotFound(err) {
		t.Fatalf("delete of missing snapshot must be not_found, got %v", err)
	}
	t.Logf("REJECTED missing snapshot: %v (ctx=%v)", err, ee.Context)
}

func TestSnapshotNamesSorted(t *testing.T) {
	st := New()
	for _, n := range []string{"z", "a", "m"} {
		if err := st.PutSnapshot(&model.Snapshot{Name: n, Files: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
	}
	names := st.SnapshotNames()
	if len(names) != 3 || names[0] != "a" || names[1] != "m" || names[2] != "z" {
		t.Fatalf("names not sorted: %v", names)
	}
}

func TestChangeSetCRUD(t *testing.T) {
	st := New()
	if err := st.PutChangeSet(nil); !merr.IsInvalid(err) {
		t.Fatalf("nil change set must be invalid: %v", err)
	}
	cs := &model.ChangeSet{SourceID: "s1", Changes: []model.Change{{Path: "a", Op: model.OpAdd}}}
	if err := st.PutChangeSet(cs); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetChangeSet("s1")
	if err != nil {
		t.Fatal(err)
	}
	got.Changes[0].Path = "tampered"
	again, _ := st.GetChangeSet("s1")
	if again.Changes[0].Path != "a" {
		t.Fatal("change set store leaked a mutation")
	}
	_, err = st.GetChangeSet("nope")
	if !merr.IsNotFound(err) {
		t.Fatalf("want not_found, got %v", err)
	}
}
