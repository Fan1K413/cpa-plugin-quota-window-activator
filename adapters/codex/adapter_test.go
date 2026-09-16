package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cpa-plugins/quota-window-activator/core"
)

func TestParseQuotaPreservesBucketsAndLazyHint(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	raw := []byte(`{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":18000},"secondary_window":{"used_percent":25,"limit_window_seconds":604800,"reset_after_seconds":100}},"additional_rate_limits":[{"limit_name":"spark","rate_limit":{"primary_window":{"used_percent":3,"limit_window_seconds":18000,"reset_after_seconds":60}}}]}`)
	got, err := parseQuota(raw, "a", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Windows) != 3 {
		t.Fatalf("windows=%d", len(got.Windows))
	}
	if !got.Windows[0].LazyHint {
		t.Fatal("expected strict Codex lazy hint")
	}
	if got.Windows[2].Scope != "additional" {
		t.Fatalf("scope=%s", got.Windows[2].Scope)
	}
}

func TestActivationPlanIsSmallAndCredentialBound(t *testing.T) {
	a := New(nil)
	cred := credential(`{"access_token":"access-secret","refresh_token":"refresh-secret","account_id":"acct-123"}`)
	record := core.WindowRecord{Key: "window", ActivationGroup: "codex-main"}
	plan, err := a.PlanActivation(context.Background(), cred, []core.WindowRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetModel != activationModel || plan.MaxInput > 4 || plan.MaxOutput != 1 {
		t.Fatalf("budget=%#v", plan)
	}
	if got := plan.Request.Headers.Get("Chatgpt-Account-Id"); got != "acct-123" {
		t.Fatalf("account header=%q", got)
	}
	if got := plan.Request.Headers.Get("Authorization"); got != "Bearer access-secret" {
		t.Fatal("activation did not use target credential token")
	}
	if string(plan.Request.Body) == "" || strings.Contains(string(plan.Request.Body), "refresh-secret") {
		t.Fatal("invalid activation body")
	}
	var payload map[string]any
	if err = json.Unmarshal(plan.Request.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["max_output_tokens"] != float64(1) || payload["model"] != activationModel {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestCanActivateOnlyMainKnownDurations(t *testing.T) {
	a := New(nil)
	if !a.CanActivate(core.QuotaWindow{Scope: "main", WindowDuration: 5 * time.Hour}) {
		t.Fatal("main 5h should be activatable")
	}
	if a.CanActivate(core.QuotaWindow{Scope: "code_review", WindowDuration: 5 * time.Hour}) {
		t.Fatal("code review must be observe-only")
	}
	if a.CanActivate(core.QuotaWindow{Scope: "main", WindowDuration: 24 * time.Hour}) {
		t.Fatal("unknown duration must be observe-only")
	}
}

func TestFingerprintSurvivesTokenRotation(t *testing.T) {
	a := New(nil)
	c1 := credential(`{"access_token":"a","refresh_token":"r1","account_id":"acct"}`)
	c2 := credential(`{"access_token":"b","refresh_token":"r2","account_id":"acct"}`)
	f1, e := a.Fingerprint(c1)
	if e != nil {
		t.Fatal(e)
	}
	f2, e := a.Fingerprint(c2)
	if e != nil {
		t.Fatal(e)
	}
	if f1 != f2 {
		t.Fatal("token rotation changed identity fingerprint")
	}
}

func credential(raw string) core.Credential {
	return core.Credential{Provider: "codex", RawJSON: []byte(raw)}
}
