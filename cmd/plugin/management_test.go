package main

import (
	"strings"
	"testing"
)

func TestManagementRegistrationSeparatesResourceAndProtectedStatus(t *testing.T) {
	r := managementRegistration()
	if len(r.Resources) != 1 || r.Resources[0].Path != "/status" {
		t.Fatalf("resources=%#v", r.Resources)
	}
	if len(r.Routes) != 1 || r.Routes[0].Path != managementPath {
		t.Fatalf("routes=%#v", r.Routes)
	}
}

func TestStatusPageContainsNoCredentialDataAndRequiresKey(t *testing.T) {
	if !strings.Contains(statusPage, "type=\"password\"") || !strings.Contains(statusPage, "Authorization") {
		t.Fatal("status page must request a management key")
	}
	for _, required := range []string{
		"/v0/management/plugins/", "configPath", "method:'PUT'", "disabledCredentials",
		"observationInterval", "activate_if_lazy", "Quota Window 状态",
	} {
		if !strings.Contains(statusPage, required) {
			t.Fatalf("status page missing configuration feature %q", required)
		}
	}
	for _, secret := range []string{"access_token", "refresh_token", "Bearer secret"} {
		if strings.Contains(statusPage, secret) {
			t.Fatalf("status page contains forbidden value %q", secret)
		}
	}
}

func TestPluginRegistrationPublishesCompleteConfigFields(t *testing.T) {
	r := pluginRegistration()
	if r.Metadata.Version != pluginVersion || r.Metadata.GitHubRepository == "" {
		t.Fatalf("metadata=%#v", r.Metadata)
	}
	fields := make(map[string]bool, len(r.Metadata.ConfigFields))
	for _, field := range r.Metadata.ConfigFields {
		fields[field.Name] = true
	}
	for _, name := range []string{"enabled", "dry_run", "observation_interval", "reset_grace_period", "verify_delay", "suppression_duration", "request_timeout", "max_retries", "max_concurrency", "state_dir", "providers", "disabled_credentials"} {
		if !fields[name] {
			t.Fatalf("config field %q missing", name)
		}
	}
}
