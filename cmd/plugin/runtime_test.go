package main

import (
	"testing"

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
