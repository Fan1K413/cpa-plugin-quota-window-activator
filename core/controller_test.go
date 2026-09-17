package core

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeAdapter struct {
	mu           sync.Mutex
	observations []Observation
	reads        int
	activatable  map[string]bool
}

func (f *fakeAdapter) ID() string            { return "fake" }
func (f *fakeAdapter) Match(Credential) bool { return true }
func (f *fakeAdapter) Fingerprint(c Credential) (string, error) {
	if len(c.RawJSON) > 0 {
		return string(c.RawJSON), nil
	}
	return "fp", nil
}
func (f *fakeAdapter) ReadQuota(context.Context, Credential) (Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reads >= len(f.observations) {
		return Observation{}, errors.New("no observation")
	}
	o := f.observations[f.reads]
	f.reads++
	return o, nil
}
func (f *fakeAdapter) CanActivate(w QuotaWindow) bool {
	return f.activatable == nil || f.activatable[w.BucketID]
}
func (f *fakeAdapter) PlanActivation(_ context.Context, _ Credential, rs []WindowRecord) (ActivationPlan, error) {
	keys := make([]string, len(rs))
	for i, r := range rs {
		keys[i] = r.Key
	}
	return ActivationPlan{Group: "g", WindowKeys: keys, TargetModel: "tiny", MaxInput: 2, MaxOutput: 1, Request: ActivationRequest{Method: "POST", URL: "https://example.invalid", Headers: http.Header{}, Body: []byte(`{}`)}}, nil
}
func (f *fakeAdapter) VerifyActivation(b Baseline, a QuotaWindow) VerifyResult {
	return VerifyResult{Confirmed: a.ResetAt.After(b.ResetAt.Add(time.Second)), Reason: "verified"}
}

type fakeSender struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *fakeSender) Send(context.Context, Credential, ActivationRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}
func (s *fakeSender) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

func pct(v float64) *float64 { return &v }
func win(bucket string, used float64, reset, observed time.Time) QuotaWindow {
	return QuotaWindow{Provider: "fake", BucketID: bucket, UsedPercent: pct(used), ResetAt: reset, WindowDuration: 5 * time.Hour, ObservedAt: observed, ActivationGroup: "shared", Complete: true}
}
func testController(t *testing.T, now *time.Time, sender *fakeSender, dry bool) (*Controller, *Store) {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.DryRun = dry
	cfg.ResetGracePeriod = 0
	cfg.VerifyDelay = 0
	c, err := NewController(store, sender, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.SetClock(func() time.Time { return *now }, func(context.Context, time.Duration) error { return nil })
	return c, store
}

func TestNormalResetDoesNotActivate(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	old := now.Add(-time.Minute)
	a := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now.Add(-time.Hour))}}, {Windows: []QuotaWindow{win("5h", 0, now.Add(5*time.Hour), now)}}}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	if err := c.Tick(context.Background(), cred, a); err != nil {
		t.Fatal(err)
	}
	if send.count() != 0 {
		t.Fatalf("send calls=%d", send.count())
	}
}

func TestLazyResetSendsOnceAndVerifies(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	old := now.Add(-time.Minute)
	a := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now.Add(-time.Hour))}}, {Windows: []QuotaWindow{win("5h", 90, old, now)}}, {Windows: []QuotaWindow{win("5h", 1, now.Add(5*time.Hour), now)}}}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	if err := c.Tick(context.Background(), cred, a); err != nil {
		t.Fatal(err)
	}
	if send.count() != 1 {
		t.Fatalf("send calls=%d", send.count())
	}
	for _, r := range c.Snapshot().Windows {
		if r.State != StateConfirmed {
			t.Fatalf("state=%s", r.State)
		}
	}
}

func TestFirstObservationStrictLazyRunsPrecheckAndActivatesImmediately(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	reset := now.Add(5 * time.Hour)
	first := win("5h", 0, reset, now)
	first.LazyHint = true
	precheck := first
	verified := win("5h", 1, reset.Add(5*time.Hour), now)
	adapter := &fakeAdapter{observations: []Observation{
		{Windows: []QuotaWindow{first}},
		{Windows: []QuotaWindow{precheck}},
		{Windows: []QuotaWindow{verified}},
	}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	if err := c.Tick(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if send.count() != 1 {
		t.Fatalf("send calls=%d", send.count())
	}
	for _, record := range c.Snapshot().Windows {
		if record.State != StateConfirmed {
			t.Fatalf("state=%s result=%s", record.State, record.LastResult)
		}
	}
}

func TestPersistedWaitingLazyBaselineMigratesWithoutWaitingFullWindow(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	reset := now.Add(5 * time.Hour)
	ordinary := win("5h", 0, reset, now)
	lazy := ordinary
	lazy.LazyHint = true
	verified := win("5h", 1, reset.Add(5*time.Hour), now)
	adapter := &fakeAdapter{observations: []Observation{
		{Windows: []QuotaWindow{ordinary}},
		{Windows: []QuotaWindow{lazy}},
		{Windows: []QuotaWindow{lazy}},
		{Windows: []QuotaWindow{verified}},
	}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	if err := c.Tick(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if send.count() != 0 {
		t.Fatal("ordinary first observation unexpectedly activated")
	}
	if err := c.Tick(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if send.count() != 1 {
		t.Fatalf("migrated baseline send calls=%d", send.count())
	}
}

func TestDuplicateTickSingleSend(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	old := now.Add(-time.Minute)
	a := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now.Add(-time.Hour))}}, {Windows: []QuotaWindow{win("5h", 90, old, now)}}, {Windows: []QuotaWindow{win("5h", 1, now.Add(5*time.Hour), now)}}, {Windows: []QuotaWindow{win("5h", 1, now.Add(5*time.Hour), now)}}}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	_ = c.Tick(context.Background(), cred, a)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = c.Tick(context.Background(), cred, a) }()
	go func() { defer wg.Done(); _ = c.Tick(context.Background(), cred, a) }()
	wg.Wait()
	if send.count() != 1 {
		t.Fatalf("send calls=%d", send.count())
	}
}

func TestRestartAfterSendingVerifyOnly(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{err: errors.New("timeout")}
	c, store := testController(t, &now, send, false)
	old := now.Add(-time.Minute)
	a := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now.Add(-time.Hour))}}, {Windows: []QuotaWindow{win("5h", 90, old, now)}}}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	_ = c.Tick(context.Background(), cred, a)
	_ = c.Tick(context.Background(), cred, a)
	if send.count() != 1 {
		t.Fatal("expected one send")
	}
	send2 := &fakeSender{}
	cfg := DefaultConfig()
	cfg.DryRun = false
	cfg.VerifyDelay = 0
	c2, _ := NewController(store, send2, cfg)
	c2.SetClock(func() time.Time { return now }, func(context.Context, time.Duration) error { return nil })
	a2 := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 1, now.Add(5*time.Hour), now)}}}}
	if err := c2.Recover(context.Background(), cred, a2); err != nil {
		t.Fatal(err)
	}
	if send2.count() != 0 {
		t.Fatalf("recovery resent %d", send2.count())
	}
}

func TestRestartBeforeSendRunsFreshPrecheckThenSends(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	old := now.Add(time.Hour)
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	firstSender := &fakeSender{}
	c, store := testController(t, &now, firstSender, false)
	seedAdapter := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now)}}}}
	if err := c.Tick(context.Background(), cred, seedAdapter); err != nil {
		t.Fatal(err)
	}
	now = old.Add(time.Minute)
	var record WindowRecord
	for _, item := range store.Snapshot().Windows {
		record = item
	}
	prepared := ProbeAttempt{ID: "prepared", InstanceKey: record.InstanceKey, Provider: "fake", AuthID: cred.AuthID, AuthIndex: cred.AuthIndex, CredentialFP: record.CredentialFP, ActivationGroup: "shared", WindowKeys: []string{record.Key}, CycleIDs: []string{record.Baseline.CycleID}, Phase: AttemptPrepared, CreatedAt: now, VerifyNotBefore: now}
	if _, err := store.Update(func(s *PersistentState) error {
		s.Attempts[prepared.ID] = prepared
		r := s.Windows[record.Key]
		r.State = StatePendingPrecheck
		r.ActiveAttemptID = prepared.ID
		s.Windows[record.Key] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	secondSender := &fakeSender{}
	c2, err := NewController(store, secondSender, Config{DryRun: false, ObservationInterval: time.Minute, ClockSkewTolerance: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	c2.SetClock(func() time.Time { return now }, func(context.Context, time.Duration) error { return nil })
	adapter := &fakeAdapter{observations: []Observation{
		{Windows: []QuotaWindow{win("5h", 90, old, now)}},
		{Windows: []QuotaWindow{win("5h", 1, now.Add(5*time.Hour), now)}},
	}}
	if err = c2.Recover(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if secondSender.count() != 0 {
		t.Fatal("prepared recovery must not send")
	}
	if err = c2.Tick(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if secondSender.count() != 1 {
		t.Fatalf("send calls=%d", secondSender.count())
	}
}

func TestRestartDuringSendVerifiesOnly(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	old := now.Add(time.Hour)
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	c, store := testController(t, &now, &fakeSender{}, false)
	if err := c.Tick(context.Background(), cred, &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now)}}}}); err != nil {
		t.Fatal(err)
	}
	var record WindowRecord
	for _, item := range store.Snapshot().Windows {
		record = item
	}
	attempt := ProbeAttempt{ID: "sending", InstanceKey: record.InstanceKey, Provider: "fake", AuthID: cred.AuthID, AuthIndex: cred.AuthIndex, CredentialFP: record.CredentialFP, ActivationGroup: "shared", WindowKeys: []string{record.Key}, CycleIDs: []string{record.Baseline.CycleID}, Phase: AttemptSending, CreatedAt: now, SendingAt: now, VerifyNotBefore: now, SuppressUntil: now.Add(10 * time.Minute)}
	if _, err := store.Update(func(s *PersistentState) error {
		s.Attempts[attempt.ID] = attempt
		s.CycleLedger[record.Baseline.CycleID] = CycleLedgerEntry{CycleID: record.Baseline.CycleID, AttemptID: attempt.ID, SendingAt: now, SendOutcome: "unknown"}
		r := s.Windows[record.Key]
		r.State = StateActivationSending
		r.ActiveAttemptID = attempt.ID
		s.Windows[record.Key] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	c2, err := NewController(store, sender, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	c2.SetClock(func() time.Time { return now }, func(context.Context, time.Duration) error { return nil })
	adapter := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 1, now.Add(5*time.Hour), now)}}}}
	if err = c2.Recover(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 0 {
		t.Fatalf("recovery resent %d", sender.count())
	}
	for _, r := range c2.Snapshot().Windows {
		if r.State != StateConfirmed {
			t.Fatalf("state=%s", r.State)
		}
	}
}

func TestActivationTimeoutNeverResendsAfterSuppression(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	old := now.Add(-time.Minute)
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	sender := &fakeSender{err: errors.New("timeout")}
	c, _ := testController(t, &now, sender, false)
	adapter := &fakeAdapter{observations: []Observation{
		{Windows: []QuotaWindow{win("5h", 90, old, now.Add(-time.Hour))}},
		{Windows: []QuotaWindow{win("5h", 90, old, now)}},
		{Windows: []QuotaWindow{win("5h", 90, old, now.Add(time.Hour))}},
		{Windows: []QuotaWindow{win("5h", 90, old, now.Add(time.Hour))}},
		{Windows: []QuotaWindow{win("5h", 90, old, now.Add(time.Hour))}},
	}}
	if err := c.Tick(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 1 {
		t.Fatalf("send calls=%d", sender.count())
	}
	now = now.Add(time.Hour)
	if err := c.Recover(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(context.Background(), cred, adapter); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 1 {
		t.Fatalf("timeout cycle resent after suppression: %d", sender.count())
	}
}

func TestMultipleWindowsOnlyLazyBucketActivates(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	old := now.Add(-time.Minute)
	weekOld := now.Add(-2 * time.Minute)
	a := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now.Add(-time.Hour)), win("7d", 50, weekOld, now.Add(-time.Hour))}}, {Windows: []QuotaWindow{win("5h", 90, old, now), win("7d", 0, now.Add(7*24*time.Hour), now)}}, {Windows: []QuotaWindow{win("5h", 1, now.Add(5*time.Hour), now), win("7d", 0, now.Add(7*24*time.Hour), now)}}}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	_ = c.Tick(context.Background(), cred, a)
	if send.count() != 1 {
		t.Fatalf("send calls=%d", send.count())
	}
}

func TestCredentialReplacementDoesNotReuseState(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	old := now.Add(time.Hour)
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake", RawJSON: []byte("one")}
	a := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now)}}}}
	if err := c.Tick(context.Background(), cred, a); err != nil {
		t.Fatal(err)
	}
	cred.RawJSON = []byte("two")
	a2 := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now)}}}}
	if err := c.Tick(context.Background(), cred, a2); err != nil {
		t.Fatal(err)
	}
	if send.count() != 0 {
		t.Fatalf("replacement sent %d", send.count())
	}
}

func TestDisabledCredentialNeverSends(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, false)
	a := &fakeAdapter{}
	err := c.Tick(context.Background(), Credential{AuthID: "a", AuthIndex: "i", Provider: "fake", Disabled: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	if send.count() != 0 {
		t.Fatal("disabled credential sent")
	}
}

func TestDryRunNeverSends(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	send := &fakeSender{}
	c, _ := testController(t, &now, send, true)
	old := now.Add(-time.Minute)
	a := &fakeAdapter{observations: []Observation{{Windows: []QuotaWindow{win("5h", 90, old, now.Add(-time.Hour))}}, {Windows: []QuotaWindow{win("5h", 90, old, now)}}}}
	cred := Credential{AuthID: "a", AuthIndex: "i", Provider: "fake"}
	_ = c.Tick(context.Background(), cred, a)
	_ = c.Tick(context.Background(), cred, a)
	if send.count() != 0 {
		t.Fatal("dry-run sent")
	}
}
