package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Config struct {
	DryRun              bool
	ResetGracePeriod    time.Duration
	VerifyDelay         time.Duration
	SuppressionDuration time.Duration
	ObservationInterval time.Duration
	ClockSkewTolerance  time.Duration
	RetryDelays         []time.Duration
	MaxRetries          int
}

func DefaultConfig() Config {
	return Config{
		DryRun: true, ResetGracePeriod: time.Minute, VerifyDelay: 3 * time.Second,
		SuppressionDuration: 10 * time.Minute, ObservationInterval: 30 * time.Minute,
		ClockSkewTolerance: 2 * time.Minute,
		RetryDelays:        []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute},
		MaxRetries:         5,
	}
}

type Controller struct {
	mu     sync.Mutex
	store  *Store
	sender Sender
	cfg    Config
	now    func() time.Time
	wait   func(context.Context, time.Duration) error
}

func NewController(store *Store, sender Sender, cfg Config) (*Controller, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if cfg.ResetGracePeriod < 0 || cfg.VerifyDelay < 0 || cfg.SuppressionDuration < 0 {
		return nil, errors.New("durations must not be negative")
	}
	if cfg.ObservationInterval <= 0 {
		cfg.ObservationInterval = 30 * time.Minute
	}
	if cfg.ClockSkewTolerance <= 0 {
		cfg.ClockSkewTolerance = 2 * time.Minute
	}
	if cfg.SuppressionDuration <= 0 {
		cfg.SuppressionDuration = 10 * time.Minute
	}
	c := &Controller{store: store, sender: sender, cfg: cfg, now: time.Now}
	c.wait = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	return c, nil
}

func (c *Controller) SetClock(now func() time.Time, wait func(context.Context, time.Duration) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now != nil {
		c.now = now
	}
	if wait != nil {
		c.wait = wait
	}
}

func (c *Controller) Snapshot() PersistentState { return c.store.Snapshot() }

// BlockCredential records a fail-closed host capability or authentication
// condition without attempting a quota or activation request.
func (c *Controller) BlockCredential(cred Credential, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.markCredentialState(cred, StateAuthBlocked, reason)
}

// Tick performs one authoritative quota observation for one credential. A
// baseline must have been persisted by an earlier observation before this call
// can authorize a model request.
func (c *Controller) Tick(ctx context.Context, cred Credential, adapter Adapter) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if adapter == nil {
		return errors.New("adapter is required")
	}
	if cred.Disabled {
		return c.markCredentialState(cred, StateDisabled, "credential disabled")
	}
	if !adapter.Match(cred) {
		return fmt.Errorf("adapter %s does not match credential", adapter.ID())
	}
	fingerprint, err := adapter.Fingerprint(cred)
	if err != nil {
		return fmt.Errorf("credential fingerprint: %w", err)
	}
	instanceKey := cred.InstanceKey(adapter.ID(), fingerprint)
	if err := c.retireReplacedInstances(cred, instanceKey); err != nil {
		return err
	}
	obs, err := adapter.ReadQuota(ctx, cred)
	if err != nil {
		return c.markReadFailure(instanceKey, err)
	}
	windows, err := normalizeWindows(obs.Windows)
	if err != nil {
		return err
	}
	activation, canActivate := adapter.(ActivationAdapter)
	groups, err := c.reconcileObservation(cred, instanceKey, fingerprint, windows, canActivate, activation)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if err := c.runGroup(ctx, cred, activation, group); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) reconcileObservation(cred Credential, instanceKey, fingerprint string, windows []QuotaWindow, canActivate bool, activation ActivationAdapter) ([][]WindowRecord, error) {
	now := c.now().UTC()
	groups := map[string][]WindowRecord{}
	_, err := c.store.Update(func(s *PersistentState) error {
		seen := map[string]struct{}{}
		for _, observed := range windows {
			observed.Provider = adapterProvider(observed.Provider, cred.Provider)
			observed.AuthID = cred.AuthID
			key := WindowKey(instanceKey, observed.BucketID)
			seen[key] = struct{}{}
			record, exists := s.Windows[key]
			if !exists {
				base := baselineFrom(instanceKey, observed)
				state := StateWaitingReset
				result := "baseline established"
				if !canActivate || !activation.CanActivate(observed) {
					state, result = StateUnsupported, "observe only"
				}
				s.Windows[key] = WindowRecord{Key: key, InstanceKey: instanceKey, Provider: cred.Provider, AuthID: cred.AuthID, BucketID: observed.BucketID, ModelFamily: observed.ModelFamily, ActivationGroup: observed.ActivationGroup, CredentialFP: fingerprint, Baseline: base, Latest: observed, State: state, NextCheck: nextDeadline(base, now, c.cfg), LastResult: result, ActivationDisabled: state == StateUnsupported}
				continue
			}
			record.Latest = observed
			record.LastProbe = now
			if !canActivate || !activation.CanActivate(observed) {
				record.State, record.LastResult, record.ActivationDisabled = StateUnsupported, "observe only", true
				record.NextCheck = now.Add(c.cfg.ObservationInterval)
				s.Windows[key] = record
				continue
			}
			if observed.ResetAt.After(record.Baseline.ResetAt.Add(c.cfg.ClockSkewTolerance)) && !observed.LazyHint {
				record.Baseline = baselineFrom(instanceKey, observed)
				record.State, record.LastResult, record.RetryCount, record.ActiveAttemptID = StateNormalReset, "window rolled without activation", 0, ""
				record.NextCheck = nextDeadline(record.Baseline, now, c.cfg)
				s.Windows[key] = record
				continue
			}
			if now.Before(record.Baseline.ResetAt.Add(c.cfg.ResetGracePeriod)) {
				record.State, record.NextCheck = StateWaitingReset, nextDeadline(record.Baseline, now, c.cfg)
				s.Windows[key] = record
				continue
			}
			if _, sent := s.CycleLedger[record.Baseline.CycleID]; sent {
				record.State, record.LastResult = StateSentUnknown, "activation already attempted for this cycle; verify only"
				record.NextCheck = now.Add(c.cfg.ObservationInterval)
				s.Windows[key] = record
				continue
			}
			sameReset := absDuration(observed.ResetAt.Sub(record.Baseline.ResetAt)) <= c.cfg.ClockSkewTolerance
			if !sameReset && !observed.LazyHint {
				record.State, record.LastResult = StateRetryWait, "ambiguous quota transition"
				record.NextCheck = now.Add(c.retryDelay(record.RetryCount))
				record.RetryCount++
				s.Windows[key] = record
				continue
			}
			record.State, record.LastResult = StateLazyDetected, "precheck still shows lazy window"
			record.NextCheck = time.Time{}
			s.Windows[key] = record
			group := observed.ActivationGroup
			if group == "" {
				group = observed.BucketID
			}
			groups[group] = append(groups[group], record)
		}
		for key, record := range s.Windows {
			if record.InstanceKey != instanceKey {
				continue
			}
			if _, ok := seen[key]; !ok {
				record.State, record.LastResult = StateRetryWait, "bucket absent from latest observation"
				record.NextCheck = now.Add(c.cfg.ObservationInterval)
				s.Windows[key] = record
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][]WindowRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, groups[k])
	}
	return out, nil
}

func (c *Controller) runGroup(ctx context.Context, cred Credential, adapter ActivationAdapter, records []WindowRecord) error {
	if len(records) == 0 {
		return nil
	}
	if c.cfg.DryRun {
		_, err := c.store.Update(func(s *PersistentState) error {
			for _, r := range records {
				x := s.Windows[r.Key]
				x.State = StateLazyDetected
				x.LastResult = "dry-run: would activate"
				x.NextCheck = c.now().Add(c.cfg.ObservationInterval)
				s.Windows[r.Key] = x
			}
			return nil
		})
		return err
	}
	if c.sender == nil {
		return errors.New("activation sender is unavailable")
	}
	plan, err := adapter.PlanActivation(ctx, cred, records)
	if err != nil {
		return c.recordGroupFailure(records, err, false)
	}
	if plan.MaxInput <= 0 || plan.MaxOutput <= 0 || plan.Request.URL == "" {
		return c.recordGroupFailure(records, errors.New("adapter returned an unbounded activation plan"), false)
	}
	attemptID, err := randomID()
	if err != nil {
		return err
	}
	now := c.now().UTC()
	attempt := ProbeAttempt{ID: attemptID, InstanceKey: records[0].InstanceKey, Provider: records[0].Provider, AuthID: cred.AuthID, AuthIndex: cred.AuthIndex, CredentialFP: records[0].CredentialFP, ActivationGroup: plan.Group, Phase: AttemptPrepared, CreatedAt: now, VerifyNotBefore: now.Add(c.cfg.VerifyDelay), SuppressUntil: now.Add(c.cfg.SuppressionDuration)}
	for _, r := range records {
		attempt.WindowKeys = append(attempt.WindowKeys, r.Key)
		attempt.CycleIDs = append(attempt.CycleIDs, r.Baseline.CycleID)
	}
	if _, err = c.store.Update(func(s *PersistentState) error {
		for _, cycle := range attempt.CycleIDs {
			if _, ok := s.CycleLedger[cycle]; ok {
				return fmt.Errorf("cycle %s already attempted", cycle)
			}
		}
		s.Attempts[attempt.ID] = attempt
		for _, key := range attempt.WindowKeys {
			x := s.Windows[key]
			x.State = StatePendingPrecheck
			x.ActiveAttemptID = attempt.ID
			s.Windows[key] = x
		}
		return nil
	}); err != nil {
		return err
	}
	// The Sending transition and cycle ledger are one durable transaction. From
	// this point onward no code path may authorize another send for these cycles.
	now = c.now().UTC()
	attempt.Phase, attempt.SendingAt = AttemptSending, now
	if _, err = c.store.Update(func(s *PersistentState) error {
		current, ok := s.Attempts[attempt.ID]
		if !ok || current.Phase != AttemptPrepared {
			return errors.New("attempt changed before send")
		}
		s.Attempts[attempt.ID] = attempt
		for _, cycle := range attempt.CycleIDs {
			s.CycleLedger[cycle] = CycleLedgerEntry{CycleID: cycle, AttemptID: attempt.ID, SendingAt: now, SendOutcome: "unknown"}
		}
		for _, key := range attempt.WindowKeys {
			x := s.Windows[key]
			x.State = StateActivationSending
			x.LastActivation = now
			s.Windows[key] = x
		}
		return nil
	}); err != nil {
		return err
	}
	sendErr := c.sender.Send(ctx, cred, plan.Request)
	if sendErr != nil {
		return c.markSendOutcome(attempt, AttemptSentUnknown, "unknown", sendErr)
	}
	attempt.Phase, attempt.SentAt = AttemptSent, c.now().UTC()
	if err := c.markSendOutcome(attempt, AttemptSent, "sent", nil); err != nil {
		return err
	}
	if err := c.wait(ctx, c.cfg.VerifyDelay); err != nil {
		return c.markSendOutcome(attempt, AttemptSentUnknown, "unknown", err)
	}
	return c.verifyAttempt(ctx, cred, adapter, attempt)
}

// Recover verifies every durable in-flight send. It never calls Sender.Send.
func (c *Controller) Recover(ctx context.Context, cred Credential, adapter ActivationAdapter) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fp, err := adapter.Fingerprint(cred)
	if err != nil {
		return err
	}
	instance := cred.InstanceKey(adapter.ID(), fp)
	snapshot := c.store.Snapshot()
	ids := make([]string, 0)
	for id, a := range snapshot.Attempts {
		if a.InstanceKey == instance && a.Phase != AttemptCompleted {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := c.store.Snapshot().Attempts[id]
		if a.Phase == AttemptPrepared {
			_, err = c.store.Update(func(s *PersistentState) error {
				delete(s.Attempts, id)
				for _, k := range a.WindowKeys {
					x := s.Windows[k]
					x.State = StatePendingPrecheck
					x.ActiveAttemptID = ""
					x.NextCheck = c.now()
					s.Windows[k] = x
				}
				return nil
			})
			if err != nil {
				return err
			}
			continue
		}
		if a.Phase == AttemptSending {
			a.Phase = AttemptSentUnknown
			if err = c.markSendOutcome(a, AttemptSentUnknown, "unknown", errors.New("recovered during send")); err != nil {
				return err
			}
		}
		if c.now().Before(a.VerifyNotBefore) {
			if err = c.wait(ctx, a.VerifyNotBefore.Sub(c.now())); err != nil {
				return err
			}
		}
		if err = c.verifyAttempt(ctx, cred, adapter, a); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) verifyAttempt(ctx context.Context, cred Credential, adapter ActivationAdapter, attempt ProbeAttempt) error {
	obs, err := adapter.ReadQuota(ctx, cred)
	if err != nil {
		return c.markSendOutcome(attempt, AttemptSentUnknown, "unknown", err)
	}
	windows, err := normalizeWindows(obs.Windows)
	if err != nil {
		return err
	}
	byBucket := map[string]QuotaWindow{}
	for _, w := range windows {
		byBucket[w.BucketID] = w
	}
	now := c.now().UTC()
	_, err = c.store.Update(func(s *PersistentState) error {
		current, ok := s.Attempts[attempt.ID]
		if !ok {
			return nil
		}
		for _, key := range current.WindowKeys {
			record, ok := s.Windows[key]
			if !ok {
				continue
			}
			after, ok := byBucket[record.BucketID]
			if !ok {
				record.State = StateRetryWait
				record.LastResult = "verify missing bucket"
				record.NextCheck = now.Add(c.cfg.ObservationInterval)
			} else {
				result := adapter.VerifyActivation(record.Baseline, after)
				record.Latest = after
				record.LastProbe = now
				record.LastResult = result.Reason
				record.ActiveAttemptID = ""
				if result.Confirmed {
					record.State = StateConfirmed
					record.Baseline = baselineFrom(record.InstanceKey, after)
					record.NextCheck = nextDeadline(record.Baseline, now, c.cfg)
					record.RetryCount = 0
				} else {
					record.State = StateRetryWait
					record.NextCheck = now.Add(c.cfg.ObservationInterval)
				}
			}
			s.Windows[key] = record
		}
		current.Phase = AttemptCompleted
		s.Attempts[attempt.ID] = current
		return nil
	})
	return err
}

func (c *Controller) markSendOutcome(attempt ProbeAttempt, phase AttemptPhase, outcome string, cause error) error {
	now := c.now().UTC()
	_, err := c.store.Update(func(s *PersistentState) error {
		current, ok := s.Attempts[attempt.ID]
		if !ok {
			return errors.New("attempt not found")
		}
		current.Phase = phase
		if !attempt.SentAt.IsZero() {
			current.SentAt = attempt.SentAt
		}
		if cause != nil {
			current.LastError = safeError(cause)
		}
		s.Attempts[attempt.ID] = current
		for _, cycle := range current.CycleIDs {
			entry := s.CycleLedger[cycle]
			entry.SendOutcome = outcome
			s.CycleLedger[cycle] = entry
		}
		for _, key := range current.WindowKeys {
			x := s.Windows[key]
			if phase == AttemptSent {
				x.State = StateActivationSent
				x.LastResult = "activation sent"
			} else {
				x.State = StateSentUnknown
				x.LastResult = "send result unknown; verify only"
			}
			x.NextCheck = current.SuppressUntil
			x.LastProbe = now
			s.Windows[key] = x
		}
		return nil
	})
	return err
}

func (c *Controller) recordGroupFailure(records []WindowRecord, cause error, sent bool) error {
	_, err := c.store.Update(func(s *PersistentState) error {
		for _, r := range records {
			x := s.Windows[r.Key]
			x.State = StateRetryWait
			x.LastResult = safeError(cause)
			x.RetryCount++
			x.NextCheck = c.now().Add(c.retryDelay(x.RetryCount))
			s.Windows[r.Key] = x
		}
		return nil
	})
	if err != nil {
		return err
	}
	if sent {
		return nil
	}
	return cause
}

func (c *Controller) markCredentialState(cred Credential, state ProbeState, result string) error {
	_, err := c.store.Update(func(s *PersistentState) error {
		for key, r := range s.Windows {
			if r.AuthID == cred.AuthID {
				r.State = state
				r.LastResult = result
				r.NextCheck = time.Time{}
				s.Windows[key] = r
			}
		}
		return nil
	})
	return err
}

func (c *Controller) retireReplacedInstances(cred Credential, current string) error {
	_, err := c.store.Update(func(s *PersistentState) error {
		for key, r := range s.Windows {
			if r.AuthID == cred.AuthID && r.InstanceKey != current {
				r.State = StateDisabled
				r.LastResult = "credential identity replaced"
				r.NextCheck = time.Time{}
				s.Windows[key] = r
			}
		}
		return nil
	})
	return err
}

func (c *Controller) markReadFailure(instance string, cause error) error {
	_, err := c.store.Update(func(s *PersistentState) error {
		for key, r := range s.Windows {
			if r.InstanceKey == instance {
				r.State = StateRetryWait
				r.LastResult = safeError(cause)
				r.NextCheck = c.now().Add(c.retryDelay(r.RetryCount))
				r.RetryCount++
				s.Windows[key] = r
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return cause
}

func baselineFrom(instance string, w QuotaWindow) Baseline {
	return Baseline{ResetAt: w.ResetAt, WindowDuration: w.WindowDuration, UsedPercent: w.UsedPercent, ObservedAt: w.ObservedAt, CycleID: NewCycleID(instance, w.BucketID, w.ResetAt)}
}
func nextDeadline(b Baseline, now time.Time, cfg Config) time.Time {
	d := b.ResetAt.Add(cfg.ResetGracePeriod)
	if d.Before(now) {
		return now
	}
	return d
}
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
func adapterProvider(got, want string) string {
	if got != "" {
		return got
	}
	return want
}
func safeError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for _, marker := range []string{"Bearer ", "access_token=", "refresh_token="} {
		if i := strings.Index(strings.ToLower(s), strings.ToLower(marker)); i >= 0 {
			s = s[:i] + marker + "[redacted]"
		}
	}
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}
func (c *Controller) retryDelay(n int) time.Duration {
	if c.cfg.MaxRetries > 0 && n >= c.cfg.MaxRetries {
		return c.cfg.ObservationInterval
	}
	if len(c.cfg.RetryDelays) == 0 {
		return c.cfg.ObservationInterval
	}
	if n < 0 {
		n = 0
	}
	if n >= len(c.cfg.RetryDelays) {
		return c.cfg.RetryDelays[len(c.cfg.RetryDelays)-1]
	}
	return c.cfg.RetryDelays[n]
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
