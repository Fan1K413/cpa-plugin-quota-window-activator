package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRecoversNewerWALState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(func(s *PersistentState) error {
		s.Windows["one"] = WindowRecord{Key: "one"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	newer := store.Snapshot()
	newer.Sequence++
	newer.Windows["two"] = WindowRecord{Key: "two"}
	checksum, err := checksumState(newer)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stateEnvelope{State: newer, Checksum: checksum})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path+".wal", append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := recovered.Snapshot().Windows["two"]; !ok {
		t.Fatal("newer WAL state was not recovered")
	}
}

func TestStoreRejectsCorruptSnapshotWithoutWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"state":{},"checksum":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path); err == nil {
		t.Fatal("expected corrupt state to be rejected")
	}
}

func TestStoreUsesValidWALWhenSnapshotCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := emptyState()
	state.Sequence = 9
	checksum, _ := checksumState(state)
	raw, _ := json.Marshal(stateEnvelope{State: state, Checksum: checksum})
	if err := os.WriteFile(path+".wal", append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Sequence; got != 9 {
		t.Fatalf("sequence=%d", got)
	}
}
