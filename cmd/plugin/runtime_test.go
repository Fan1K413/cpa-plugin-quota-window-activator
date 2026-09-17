package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cpa-plugins/quota-window-activator/core"
)

func TestRuntimeRegistersOnlyActivationCapableProviders(t *testing.T) {
	cfg := defaultConfig()
	cfg.StateDir = t.TempDir()
	runtime, err := newRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	registered := runtime.registry.List()
	if len(registered) != 2 {
		t.Fatalf("registered adapters=%d want=2", len(registered))
	}
	for _, adapter := range registered {
		if _, ok := adapter.(core.ActivationAdapter); !ok {
			t.Fatalf("runtime registered observe-only adapter %s", adapter.ID())
		}
	}
}

func TestNextDelayUsesPersistedWindowDeadline(t *testing.T) {
	store, err := core.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	_, err = store.Update(func(state *core.PersistentState) error {
		state.Windows["window"] = core.WindowRecord{Key: "window", State: core.StateWaitingReset, NextCheck: now.Add(45 * time.Second)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	controller, err := core.NewController(store, nil, cfg.Core)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &runtimeService{cfg: cfg, controller: controller}
	if got := runtime.nextDelay(now); got != 45*time.Second {
		t.Fatalf("next delay=%s want=45s", got)
	}
}

func TestNextDelayCapsPastDeadlineToAvoidBusyLoop(t *testing.T) {
	store, err := core.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	_, err = store.Update(func(state *core.PersistentState) error {
		state.Windows["window"] = core.WindowRecord{Key: "window", State: core.StateRetryWait, NextCheck: now.Add(-time.Minute)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	controller, err := core.NewController(store, nil, cfg.Core)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &runtimeService{cfg: cfg, controller: controller}
	if got := runtime.nextDelay(now); got != time.Second {
		t.Fatalf("next delay=%s want=1s", got)
	}
}
