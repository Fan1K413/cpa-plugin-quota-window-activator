package main

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/cpa-plugins/quota-window-activator/adapters"
	"github.com/cpa-plugins/quota-window-activator/adapters/antigravity"
	"github.com/cpa-plugins/quota-window-activator/adapters/codex"
	"github.com/cpa-plugins/quota-window-activator/core"
	cpahost "github.com/cpa-plugins/quota-window-activator/host/cpa"
)

type runtimeService struct {
	mu                sync.Mutex
	cfg               config
	host              cpahost.Client
	registry          *adapters.Registry
	controller        *core.Controller
	cancel            context.CancelFunc
	done              chan struct{}
	lastRun           time.Time
	lastError         string
	unsupportedLogged map[string]struct{}
}

func newRuntime(cfg config) (*runtimeService, error) {
	host := cpahost.Client{Call: callHostCallback}
	registry, e := adapters.NewRegistry(codex.New(host), antigravity.New(host))
	if e != nil {
		return nil, e
	}
	store, e := core.NewStore(filepath.Join(cfg.StateDir, "runtime-state.json"))
	if e != nil {
		return nil, e
	}
	controller, e := core.NewController(store, adapters.Sender{Client: host}, cfg.Core)
	if e != nil {
		return nil, e
	}
	return &runtimeService{cfg: cfg, host: host, registry: registry, controller: controller, unsupportedLogged: map[string]struct{}{}}, nil
}
func (r *runtimeService) Start() {
	r.mu.Lock()
	if r.cancel != nil || !r.cfg.Enabled {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	r.mu.Unlock()
	go func() {
		defer close(r.done)
		for {
			r.runOnce(ctx)
			timer := time.NewTimer(r.nextDelay(time.Now().UTC()))
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}
		}
	}()
}

func (r *runtimeService) nextDelay(now time.Time) time.Duration {
	delay := r.cfg.Core.ObservationInterval
	if delay <= 0 {
		delay = 30 * time.Minute
	}
	for _, window := range r.controller.Snapshot().Windows {
		if window.NextCheck.IsZero() || window.State == core.StateDisabled || window.State == core.StateAuthBlocked || window.State == core.StateUnsupported {
			continue
		}
		candidate := window.NextCheck.Sub(now)
		if candidate < time.Second {
			candidate = time.Second
		}
		if candidate < delay {
			delay = candidate
		}
	}
	return delay
}
func (r *runtimeService) Stop() error {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("background observation did not stop within 5s")
		}
	}
	return nil
}
func (r *runtimeService) runOnce(ctx context.Context) {
	credentials, e := r.host.ListCredentials()
	if e != nil {
		r.setError(e)
		return
	}
	slots := make(chan struct{}, r.cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for _, summary := range credentials {
		summary := summary
		adapter := r.registry.Match(summary)
		if adapter == nil {
			r.logUnsupportedOnce(summary)
			continue
		}
		p := r.cfg.Providers[adapter.ID()]
		if !p.Enabled || p.Mode != "activate_if_lazy" {
			continue
		}
		if _, off := r.cfg.Disabled[summary.AuthID]; off {
			summary.Disabled = true
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			requestCtx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
			defer cancel()
			if summary.Disabled {
				if e := r.controller.Tick(requestCtx, summary, adapter); e != nil && !errors.Is(e, context.Canceled) {
					r.logFailure(summary, e)
				}
				return
			}
			if summary.RuntimeOnly {
				_ = r.controller.BlockCredential(summary, "runtime-only credential cannot be read safely")
				r.host.Log("warn", "credential unsupported", map[string]any{"auth": redactedID(summary.AuthID), "provider": summary.Provider, "reason": "runtime_only"})
				return
			}
			cred, e := r.host.GetCredential(summary)
			if e != nil {
				r.logFailure(summary, e)
				return
			}
			if cpahost.HasCredentialProxy(cred.RawJSON) {
				_ = r.controller.BlockCredential(summary, "per-credential proxy is unsupported by host.http.do")
				r.host.Log("warn", "credential blocked", map[string]any{"auth": redactedID(summary.AuthID), "provider": summary.Provider, "reason": "credential_proxy_not_inherited"})
				return
			}
			before := r.controller.Snapshot()
			if activating, ok := adapter.(core.ActivationAdapter); ok {
				if e = r.controller.Recover(requestCtx, cred, activating); e != nil && !errors.Is(e, context.Canceled) {
					r.logFailure(summary, e)
				}
			}
			if e = r.controller.Tick(requestCtx, cred, adapter); e != nil && !errors.Is(e, context.Canceled) {
				r.logFailure(summary, e)
			}
			r.logStateChanges(before, r.controller.Snapshot(), summary)
		}()
	}
	wg.Wait()
	r.mu.Lock()
	r.lastRun = time.Now().UTC()
	r.mu.Unlock()
}

func (r *runtimeService) logStateChanges(before, after core.PersistentState, c core.Credential) {
	for id, attempt := range after.Attempts {
		if attempt.AuthIndex != c.AuthIndex {
			continue
		}
		if _, existed := before.Attempts[id]; existed {
			continue
		}
		fields := map[string]any{"auth": redactedID(c.AuthID), "provider": c.Provider, "attempt": attempt.ID, "activation_group": attempt.ActivationGroup, "bucket_count": len(attempt.WindowKeys)}
		r.host.Log("info", "lazy reset detected; activation attempt durably fenced", fields)
		if attempt.Phase == core.AttemptCompleted && !attempt.SentAt.IsZero() {
			r.host.Log("info", "activation request sent", fields)
		} else if attempt.Phase == core.AttemptSentUnknown || (attempt.Phase == core.AttemptCompleted && attempt.SentAt.IsZero()) {
			r.host.Log("warn", "activation send result unknown; verify only", fields)
		}
	}
	for key, current := range after.Windows {
		if current.AuthID != c.AuthID {
			continue
		}
		previous, existed := before.Windows[key]
		if existed && previous.State == current.State && previous.Baseline.CycleID == current.Baseline.CycleID {
			continue
		}
		message := "quota window state changed"
		switch current.State {
		case core.StateNormalReset:
			message = "quota window rolled normally; no activation"
		case core.StateConfirmed:
			message = "activation confirmed"
		case core.StateLazyDetected:
			message = "lazy reset detected"
		case core.StateSentUnknown:
			message = "activation outcome unknown; verify only"
		}
		r.host.Log("info", message, map[string]any{"auth": redactedID(c.AuthID), "provider": current.Provider, "bucket": current.BucketID, "state": current.State, "reset_at": current.Latest.ResetAt, "result": current.LastResult})
	}
}

func (r *runtimeService) logUnsupportedOnce(c core.Credential) {
	r.mu.Lock()
	key := c.Provider + "|" + c.AuthIndex
	if _, ok := r.unsupportedLogged[key]; ok {
		r.mu.Unlock()
		return
	}
	r.unsupportedLogged[key] = struct{}{}
	r.mu.Unlock()
	r.host.Log("info", "provider detected but no activation adapter available", map[string]any{"auth": redactedID(c.AuthID), "provider": c.Provider})
}
func (r *runtimeService) setError(e error) {
	r.mu.Lock()
	r.lastError = e.Error()
	r.lastRun = time.Now().UTC()
	r.mu.Unlock()
}
func (r *runtimeService) logFailure(c core.Credential, e error) {
	r.setError(e)
	r.host.Log("warn", "credential observation failed", map[string]any{"auth": redactedID(c.AuthID), "provider": c.Provider, "error": bounded(e.Error())})
}
func redactedID(s string) string {
	if len(s) <= 8 {
		return "redacted"
	}
	return s[:4] + "…" + s[len(s)-4:]
}
func bounded(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func (r *runtimeService) status() any {
	r.mu.Lock()
	lastRun, lastError := r.lastRun, r.lastError
	r.mu.Unlock()
	state := r.controller.Snapshot()
	windows := make([]core.WindowRecord, 0, len(state.Windows))
	for _, w := range state.Windows {
		if w.Provider != "codex" && w.Provider != "antigravity" {
			continue
		}
		w.CredentialFP = ""
		windows = append(windows, w)
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].Key < windows[j].Key })
	return map[string]any{"plugin_id": "quota-window-activator", "generated_at": time.Now().UTC(), "enabled": r.cfg.Enabled, "dry_run": r.cfg.DryRun, "last_run": lastRun, "last_error": lastError, "windows": windows, "attempts": len(state.Attempts), "cycle_ledger_entries": len(state.CycleLedger)}
}
