package main

import (
	"testing"
	"time"
)

func TestDefaultConfigIsDryRunAndOnlyCodexCanActivate(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.DryRun || !cfg.Core.DryRun {
		t.Fatal("default must be dry-run")
	}
	if cfg.Providers["codex"].Mode != "activate_if_lazy" {
		t.Fatal("Codex activation mode missing")
	}
	for id, provider := range cfg.Providers {
		if id != "codex" && provider.Mode != "observe" {
			t.Fatalf("provider %s mode=%s", id, provider.Mode)
		}
	}
}

func TestProviderEnabledOnlyPreservesDefaultMode(t *testing.T) {
	cfg, err := decodeConfig([]byte("providers:\n  codex:\n    enabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["codex"].Mode; got != "activate_if_lazy" {
		t.Fatalf("mode=%s", got)
	}
}

func TestDecodeConfigDurationsAndDisabledCredential(t *testing.T) {
	cfg, err := decodeConfig([]byte("dry_run: false\nrequest_timeout: 12s\nverify_delay: 2s\ndisabled_credentials: [auth-1]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DryRun || cfg.Core.DryRun || cfg.RequestTimeout != 12*time.Second || cfg.Core.VerifyDelay != 2*time.Second {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if _, ok := cfg.Disabled["auth-1"]; !ok {
		t.Fatal("disabled credential missing")
	}
}

func TestRejectInvalidProviderMode(t *testing.T) {
	if _, err := decodeConfig([]byte("providers:\n  codex:\n    mode: ping_forever\n")); err == nil {
		t.Fatal("expected invalid mode error")
	}
}

func TestRejectNonPositiveRequestTimeout(t *testing.T) {
	if _, err := decodeConfig([]byte("request_timeout: 0s\n")); err == nil {
		t.Fatal("expected request timeout error")
	}
}
