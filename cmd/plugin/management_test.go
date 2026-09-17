package main

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestManagementRegistrationSeparatesResourceAndProtectedStatus(t *testing.T) {
	r := managementRegistration(pluginapi.ManagementRegistrationRequest{})
	if len(r.Resources) != 1 || r.Resources[0].Path != "/status" {
		t.Fatalf("resources=%#v", r.Resources)
	}
	if len(r.Routes) != 1 || r.Routes[0].Path != managementPath {
		t.Fatalf("routes=%#v", r.Routes)
	}
}

func TestManagementRegistrationUsesActualPluginID(t *testing.T) {
	r := managementRegistration(pluginapi.ManagementRegistrationRequest{ResourceBasePath: "/v0/resource/plugins/custom-file-name"})
	if len(r.Routes) != 1 || r.Routes[0].Path != "/plugins/custom-file-name/status" {
		t.Fatalf("routes=%#v", r.Routes)
	}
}

func TestManagementStatusRecognizesFullCPAPath(t *testing.T) {
	for _, path := range []string{managementPath, "/v0/management" + managementPath, "/v0/management/plugins/custom-file-name/status"} {
		if !isManagementStatusRequest(pluginapi.ManagementRequest{Method: "GET", Path: path}) {
			t.Fatalf("status path not recognized: %s", path)
		}
	}
	if isManagementStatusRequest(pluginapi.ManagementRequest{Method: "POST", Path: "/v0/management" + managementPath}) {
		t.Fatal("POST unexpectedly recognized as status GET")
	}
}

func TestStatusPageContainsNoCredentialDataAndRequiresKey(t *testing.T) {
	if !strings.Contains(statusPage, "type=\"password\"") || !strings.Contains(statusPage, "Authorization") {
		t.Fatal("status page must request a management key")
	}
	for _, required := range []string{
		"resourceMarker", "encodeURIComponent(pluginID)", "configPath", "method:'PUT'", "disabledCredentials",
		"observationInterval", "activate_if_lazy", "Quota Window 状态", "['antigravity','Antigravity'",
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
	for _, removed := range []string{"<select class=\"mode\"", "['gemini-cli'", "['kimi'", "['xai'"} {
		if strings.Contains(statusPage, removed) {
			t.Fatalf("status page still exposes observe-only provider UI %q", removed)
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
