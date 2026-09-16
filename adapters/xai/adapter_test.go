package xai

import (
	"testing"
	"time"
)

func TestParseBillingPeriodsWithoutHealthPing(t *testing.T) {
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	raw := []byte(`{"config":{"periods":[{"id":"credits-week","periodType":"weekly","resetAt":"2026-09-23T08:00:00Z","usedPercent":12}]}}`)
	obs, err := parse(raw, "auth", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Windows) != 1 || obs.Windows[0].BucketID != "credits-week" || obs.Windows[0].WindowDuration != 7*24*time.Hour {
		t.Fatalf("windows=%#v", obs.Windows)
	}
}
