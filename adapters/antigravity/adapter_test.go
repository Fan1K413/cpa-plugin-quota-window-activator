package antigravity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cpa-plugins/quota-window-activator/core"
)

func TestParseKeepsStableProviderBucketIDs(t *testing.T) {
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	raw := []byte(`{"groups":[{"displayName":"Gemini Flash","buckets":[{"bucketId":"shared-flash","window":"5h","remainingFraction":0.25,"resetTime":"2026-09-16T13:00:00Z"}]}]}`)
	obs, err := parse(raw, "auth", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Windows) != 1 || obs.Windows[0].BucketID != "shared-flash" || obs.Windows[0].WindowDuration != 5*time.Hour {
		t.Fatalf("windows=%#v", obs.Windows)
	}
	if got := *obs.Windows[0].UsedPercent; got != 75 {
		t.Fatalf("used=%v", got)
	}
}

func TestParseRejectsIdentitylessBucket(t *testing.T) {
	raw := []byte(`{"groups":[{"buckets":[{"window":"5h","remainingFraction":1,"resetTime":"2026-09-16T13:00:00Z"}]}]}`)
	if _, err := parse(raw, "auth", time.Now()); err == nil {
		t.Fatal("expected missing stable bucket ID to be rejected")
	}
}

func TestParseMarksOnlyStrictUnusedAnchorAsLazy(t *testing.T) {
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	raw := []byte(`{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-5h","window":"5h","remainingFraction":1,"resetTime":"2026-09-16T13:00:00Z"},{"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.9,"resetTime":"2026-09-23T08:00:00Z"}]}]}`)
	obs, err := parse(raw, "auth", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Windows) != 2 {
		t.Fatalf("windows=%d", len(obs.Windows))
	}
	byID := map[string]core.QuotaWindow{}
	for _, window := range obs.Windows {
		byID[window.BucketID] = window
	}
	if !byID["gemini-5h"].LazyHint {
		t.Fatal("unused 5h window anchored to observation must be a strict lazy candidate")
	}
	if byID["gemini-weekly"].LazyHint {
		t.Fatal("non-zero usage must not be a lazy candidate")
	}
}

func TestParseAssignsOnlyProvenSharedActivationGroups(t *testing.T) {
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	raw := []byte(`{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-5h","window":"5h","remainingFraction":1,"resetTime":"2026-09-16T13:00:00Z"},{"bucketId":"gemini-weekly","window":"weekly","remainingFraction":1,"resetTime":"2026-09-23T08:00:00Z"},{"bucketId":"future-model-bucket","window":"5h","remainingFraction":1,"resetTime":"2026-09-16T13:00:00Z"}]},{"displayName":"Claude and GPT Models","buckets":[{"bucketId":"3p-5h","window":"5h","remainingFraction":1,"resetTime":"2026-09-16T13:00:00Z"},{"bucketId":"3p-weekly","window":"weekly","remainingFraction":1,"resetTime":"2026-09-23T08:00:00Z"}]}]}`)
	obs, err := parse(raw, "auth", now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"gemini-5h": geminiGroup, "gemini-weekly": geminiGroup,
		"3p-5h": thirdPartyGroup, "3p-weekly": thirdPartyGroup,
		"future-model-bucket": "",
	}
	if len(obs.Windows) != len(want) {
		t.Fatalf("windows=%d want=%d", len(obs.Windows), len(want))
	}
	adapter := New(nil)
	for _, window := range obs.Windows {
		if window.ActivationGroup != want[window.BucketID] {
			t.Fatalf("bucket %s group=%q want=%q", window.BucketID, window.ActivationGroup, want[window.BucketID])
		}
		if got := adapter.CanActivate(window); got != (want[window.BucketID] != "") {
			t.Fatalf("bucket %s CanActivate=%v", window.BucketID, got)
		}
	}
}

func TestPlanActivationBindsCredentialAndMergesSameGroup(t *testing.T) {
	adapter := New(nil)
	cred := core.Credential{
		AuthID: "auth-a", AuthIndex: "index-a", Provider: "antigravity",
		RawJSON: []byte(`{"access_token":"secret-token","project_id":"project-a","email":"a@example.com"}`),
	}
	reset := time.Date(2026, 9, 16, 13, 0, 0, 0, time.UTC)
	records := []core.WindowRecord{
		activationRecord("five", "gemini-5h", geminiGroup, 5*time.Hour, reset),
		activationRecord("week", "gemini-weekly", geminiGroup, 7*24*time.Hour, reset),
	}
	plan, err := adapter.PlanActivation(context.Background(), cred, records)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Group != geminiGroup || plan.TargetModel != geminiProbeModel || len(plan.WindowKeys) != 2 {
		t.Fatalf("plan=%#v", plan)
	}
	if plan.Request.URL != dailyBaseURL+generatePath || plan.Request.Headers.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("request target or auth is wrong: %#v", plan.Request)
	}
	var body map[string]any
	if err := json.Unmarshal(plan.Request.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["project"] != "project-a" || body["model"] != geminiProbeModel || body["requestType"] != "agent" {
		t.Fatalf("body identity=%#v", body)
	}
	request, ok := body["request"].(map[string]any)
	if !ok {
		t.Fatalf("request body missing: %#v", body)
	}
	config, ok := request["generationConfig"].(map[string]any)
	if !ok || config["maxOutputTokens"] != float64(1) || config["temperature"] != float64(0) {
		t.Fatalf("generationConfig=%#v", config)
	}
	if id, _ := body["requestId"].(string); !strings.HasPrefix(id, "agent-") {
		t.Fatalf("requestId=%q", id)
	}
	if session, _ := request["sessionId"].(string); !strings.HasPrefix(session, "-") {
		t.Fatalf("sessionId=%q", session)
	}
}

func TestPlanActivationUsesSeparateThirdPartyModelAndCustomEndpoint(t *testing.T) {
	adapter := New(nil)
	cred := core.Credential{
		Provider: "antigravity", Attributes: map[string]string{"base_url": "https://enterprise.example/"},
		RawJSON: []byte(`{"access_token":"token","project_id":"project"}`),
	}
	record := activationRecord("third-party", "3p-5h", thirdPartyGroup, 5*time.Hour, time.Now())
	plan, err := adapter.PlanActivation(context.Background(), cred, []core.WindowRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetModel != thirdPartyModel || plan.Request.URL != "https://enterprise.example"+generatePath {
		t.Fatalf("plan=%#v", plan)
	}
}

func TestPlanActivationRejectsUnknownAndCrossGroupBuckets(t *testing.T) {
	adapter := New(nil)
	cred := core.Credential{Provider: "antigravity", RawJSON: []byte(`{"access_token":"token","project_id":"project"}`)}
	unknown := activationRecord("unknown", "future", "", 5*time.Hour, time.Now())
	if _, err := adapter.PlanActivation(context.Background(), cred, []core.WindowRecord{unknown}); err == nil {
		t.Fatal("unknown bucket unexpectedly produced activation plan")
	}
	gemini := activationRecord("gemini", "gemini-5h", geminiGroup, 5*time.Hour, time.Now())
	thirdParty := activationRecord("third", "3p-5h", thirdPartyGroup, 5*time.Hour, time.Now())
	if _, err := adapter.PlanActivation(context.Background(), cred, []core.WindowRecord{gemini, thirdParty}); err == nil {
		t.Fatal("cross-group records unexpectedly produced one request")
	}
}

func TestVerifyActivationRequiresAWindowTransition(t *testing.T) {
	used := 40.0
	reset := time.Date(2026, 9, 16, 13, 0, 0, 0, time.UTC)
	baseline := core.Baseline{ResetAt: reset, UsedPercent: &used}
	adapter := New(nil)
	if got := adapter.VerifyActivation(baseline, core.QuotaWindow{ResetAt: reset}); got.Confirmed {
		t.Fatalf("unchanged window confirmed: %#v", got)
	}
	zero := 0.0
	if got := adapter.VerifyActivation(baseline, core.QuotaWindow{ResetAt: reset, UsedPercent: &zero}); got.Confirmed {
		t.Fatalf("usage-only change confirmed without a new reset: %#v", got)
	}
	if got := adapter.VerifyActivation(baseline, core.QuotaWindow{ResetAt: reset.Add(5 * time.Hour)}); !got.Confirmed {
		t.Fatalf("rolled window not confirmed: %#v", got)
	}
}

func activationRecord(key, bucket, group string, duration time.Duration, reset time.Time) core.WindowRecord {
	return core.WindowRecord{
		Key: key, BucketID: bucket, ActivationGroup: group,
		Latest: core.QuotaWindow{BucketID: bucket, ActivationGroup: group, WindowDuration: duration, ResetAt: reset, ObservedAt: reset.Add(-duration), Complete: true},
	}
}
