package core

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const stateVersion = 1

type PersistentState struct {
	Version     int                         `json:"version"`
	Sequence    uint64                      `json:"sequence"`
	Windows     map[string]WindowRecord     `json:"windows"`
	Attempts    map[string]ProbeAttempt     `json:"attempts"`
	CycleLedger map[string]CycleLedgerEntry `json:"cycle_ledger"`
}

type stateEnvelope struct {
	State    PersistentState `json:"state"`
	Checksum string          `json:"checksum"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	state PersistentState
}

func (s *Store) walPath() string { return s.path + ".wal" }

func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("state path is required")
	}
	s := &Store{path: path, state: emptyState()}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func emptyState() PersistentState {
	return PersistentState{Version: stateVersion, Windows: map[string]WindowRecord{}, Attempts: map[string]ProbeAttempt{}, CycleLedger: map[string]CycleLedgerEntry{}}
}

func (s *Store) Snapshot() PersistentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state)
}

func (s *Store) Update(fn func(*PersistentState) error) (PersistentState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneState(s.state)
	if err := fn(&next); err != nil {
		return cloneState(s.state), err
	}
	next.Sequence++
	if err := s.persist(next); err != nil {
		return cloneState(s.state), err
	}
	s.state = next
	return cloneState(next), nil
}

func (s *Store) load() error {
	best := emptyState()
	found := false
	raw, err := os.ReadFile(s.path)
	if err == nil {
		candidate, decodeErr := decodeEnvelope(raw)
		if decodeErr == nil {
			best, found = candidate, true
		} else if _, statErr := os.Stat(s.walPath()); errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("decode state: %w", decodeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read state: %w", err)
	}
	wal, err := os.Open(s.walPath())
	if err == nil {
		scanner := bufio.NewScanner(wal)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			candidate, decodeErr := decodeEnvelope(scanner.Bytes())
			if decodeErr == nil && (!found || candidate.Sequence >= best.Sequence) {
				best, found = candidate, true
			}
		}
		closeErr := wal.Close()
		if scanErr := scanner.Err(); scanErr != nil {
			return fmt.Errorf("read state WAL: %w", scanErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close state WAL: %w", closeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("open state WAL: %w", err)
	}
	if !found {
		if _, stateErr := os.Stat(s.path); stateErr == nil {
			return errors.New("no valid state snapshot or WAL entry")
		}
		return nil
	}
	normalizeState(&best)
	s.state = best
	return nil
}

func (s *Store) persist(state PersistentState) error {
	normalizeState(&state)
	checksum, err := checksumState(state)
	if err != nil {
		return err
	}
	envelope := stateEnvelope{State: state, Checksum: checksum}
	walRaw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	writableDir := filepath.Dir(s.path)
	if err := os.MkdirAll(writableDir, 0o700); err != nil {
		return err
	}
	// Append and fsync the complete next state before replacing the snapshot.
	// A crash at any later point can therefore recover the highest valid
	// sequence from the WAL without guessing whether a send fence committed.
	w, err := os.OpenFile(s.walPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	walRaw = append(walRaw, '\n')
	if _, err = w.Write(walRaw); err != nil {
		_ = w.Close()
		return err
	}
	if err = w.Sync(); err != nil {
		_ = w.Close()
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cleanup := func(cause error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return cause
	}
	if _, err = f.Write(raw); err != nil {
		return cleanup(err)
	}
	if err = f.Sync(); err != nil {
		return cleanup(err)
	}
	if err = f.Close(); err != nil {
		return cleanup(err)
	}
	if err = replaceFile(tmp, s.path); err != nil {
		return cleanup(err)
	}
	// Cleanup is best effort. The snapshot is already committed; returning an
	// error here would leave in-memory state behind durable state and could
	// incorrectly re-authorize work. A leftover WAL is harmless and is compared
	// by sequence during the next load.
	_ = os.Remove(s.walPath())
	return nil
}

func decodeEnvelope(raw []byte) (PersistentState, error) {
	var envelope stateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return PersistentState{}, err
	}
	want, err := checksumState(envelope.State)
	if err != nil || envelope.Checksum != want {
		return PersistentState{}, errors.New("state checksum mismatch")
	}
	if envelope.State.Version != stateVersion {
		return PersistentState{}, fmt.Errorf("unsupported state version %d", envelope.State.Version)
	}
	normalizeState(&envelope.State)
	return envelope.State, nil
}

func checksumState(state PersistentState) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeState(s *PersistentState) {
	if s.Windows == nil {
		s.Windows = map[string]WindowRecord{}
	}
	if s.Attempts == nil {
		s.Attempts = map[string]ProbeAttempt{}
	}
	if s.CycleLedger == nil {
		s.CycleLedger = map[string]CycleLedgerEntry{}
	}
}

func cloneState(in PersistentState) PersistentState {
	raw, _ := json.Marshal(in)
	var out PersistentState
	_ = json.Unmarshal(raw, &out)
	normalizeState(&out)
	return out
}
