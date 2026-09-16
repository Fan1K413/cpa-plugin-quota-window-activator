package antigravity

import (
	"testing"
	"time"
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
