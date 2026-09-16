package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cpa-plugins/quota-window-activator/core"
	"gopkg.in/yaml.v3"
)

type providerConfig struct {
	Enabled bool   `yaml:"enabled"`
	Mode    string `yaml:"mode"`
}
type rawProviderConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Mode    string `yaml:"mode"`
}
type rawConfig struct {
	Enabled             *bool                        `yaml:"enabled"`
	DryRun              *bool                        `yaml:"dry_run"`
	ObservationInterval string                       `yaml:"observation_interval"`
	ResetGracePeriod    string                       `yaml:"reset_grace_period"`
	VerifyDelay         string                       `yaml:"verify_delay"`
	SuppressionDuration string                       `yaml:"suppression_duration"`
	RequestTimeout      string                       `yaml:"request_timeout"`
	MaxRetries          int                          `yaml:"max_retries"`
	MaxConcurrency      int                          `yaml:"max_concurrency"`
	StateDir            string                       `yaml:"state_dir"`
	Providers           map[string]rawProviderConfig `yaml:"providers"`
	DisabledCredentials []string                     `yaml:"disabled_credentials"`
}
type config struct {
	Enabled, DryRun            bool
	Core                       core.Config
	MaxRetries, MaxConcurrency int
	RequestTimeout             time.Duration
	StateDir                   string
	Providers                  map[string]providerConfig
	Disabled                   map[string]struct{}
}

func defaultConfig() config {
	return config{Enabled: true, DryRun: true, Core: core.DefaultConfig(), MaxRetries: 5, MaxConcurrency: 2, RequestTimeout: 30 * time.Second, StateDir: defaultStateDir(), Providers: map[string]providerConfig{"codex": {true, "activate_if_lazy"}, "antigravity": {true, "observe"}, "claude": {true, "observe"}, "kimi": {true, "observe"}, "xai": {true, "observe"}, "gemini-cli": {true, "observe"}}, Disabled: map[string]struct{}{}}
}
func defaultStateDir() string {
	dir, e := os.UserConfigDir()
	if e != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "CLIProxyAPI", "quota-window-activator")
}
func decodeConfig(raw []byte) (config, error) {
	cfg := defaultConfig()
	if len(raw) == 0 {
		return cfg, nil
	}
	var in rawConfig
	if e := yaml.Unmarshal(raw, &in); e != nil {
		return config{}, e
	}
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}
	if in.DryRun != nil {
		cfg.DryRun = *in.DryRun
	}
	cfg.Core.DryRun = cfg.DryRun
	var e error
	if in.ObservationInterval != "" {
		if cfg.Core.ObservationInterval, e = time.ParseDuration(in.ObservationInterval); e != nil {
			return config{}, fmt.Errorf("observation_interval: %w", e)
		}
	}
	if in.ResetGracePeriod != "" {
		if cfg.Core.ResetGracePeriod, e = time.ParseDuration(in.ResetGracePeriod); e != nil {
			return config{}, fmt.Errorf("reset_grace_period: %w", e)
		}
	}
	if in.VerifyDelay != "" {
		if cfg.Core.VerifyDelay, e = time.ParseDuration(in.VerifyDelay); e != nil {
			return config{}, fmt.Errorf("verify_delay: %w", e)
		}
	}
	if in.SuppressionDuration != "" {
		if cfg.Core.SuppressionDuration, e = time.ParseDuration(in.SuppressionDuration); e != nil {
			return config{}, fmt.Errorf("suppression_duration: %w", e)
		}
	}
	if in.RequestTimeout != "" {
		if cfg.RequestTimeout, e = time.ParseDuration(in.RequestTimeout); e != nil {
			return config{}, fmt.Errorf("request_timeout: %w", e)
		}
		if cfg.RequestTimeout <= 0 {
			return config{}, fmt.Errorf("request_timeout must be positive")
		}
	}
	if in.MaxRetries > 0 {
		cfg.MaxRetries = in.MaxRetries
		cfg.Core.MaxRetries = in.MaxRetries
	}
	if in.MaxConcurrency > 0 {
		cfg.MaxConcurrency = in.MaxConcurrency
	}
	if strings.TrimSpace(in.StateDir) != "" {
		cfg.StateDir = in.StateDir
	}
	for id, rawProvider := range in.Providers {
		p, ok := cfg.Providers[id]
		if !ok {
			p = providerConfig{Enabled: true, Mode: "observe"}
		}
		if rawProvider.Enabled != nil {
			p.Enabled = *rawProvider.Enabled
		}
		if mode := strings.ToLower(strings.TrimSpace(rawProvider.Mode)); mode != "" {
			p.Mode = mode
		}
		if p.Mode != "observe" && p.Mode != "activate_if_lazy" {
			return config{}, fmt.Errorf("providers.%s.mode is invalid", id)
		}
		cfg.Providers[id] = p
	}
	for _, id := range in.DisabledCredentials {
		cfg.Disabled[strings.TrimSpace(id)] = struct{}{}
	}
	return cfg, nil
}
