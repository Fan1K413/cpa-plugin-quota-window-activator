package geminicli

import (
	"testing"
	"time"
)

func TestParseUsesModelAndTokenTypeAsObservationKey(t *testing.T) {
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	raw := []byte(`{"buckets":[{"modelId":"gemini-flash","tokenType":"input","remainingFraction":0.8,"resetTime":"2026-09-16T13:00:00Z"}]}`)
	obs, err := parse(raw, "auth", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Windows) != 1 || obs.Windows[0].BucketID != "gemini-flash:input" || obs.Windows[0].WindowDuration != 5*time.Hour {
		t.Fatalf("windows=%#v", obs.Windows)
	}
}

func TestParseRejectsMissingRemainingFraction(t *testing.T) {
	raw := []byte(`{"buckets":[{"modelId":"gemini-flash","tokenType":"input","resetTime":"2026-09-16T13:00:00Z"}]}`)
	if _, err := parse(raw, "auth", time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected incomplete bucket to be rejected")
	}
}
