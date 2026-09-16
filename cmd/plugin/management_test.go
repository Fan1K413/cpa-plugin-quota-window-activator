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
	for _, secret := range []string{"access_token", "refresh_token", "Bearer secret"} {
		if strings.Contains(statusPage, secret) {
			t.Fatalf("status page contains forbidden value %q", secret)
		}
	}
}
