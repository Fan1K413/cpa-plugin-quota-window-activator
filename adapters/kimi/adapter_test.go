package kimi

import (
	"testing"
	"time"
)

func TestParseRequiresStableBucketIdentity(t *testing.T) {
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	raw := []byte(`{"limits":[{"id":"coding-week","used_percent":10,"reset_at":"2026-09-23T08:00:00Z","window":{"duration":7,"timeUnit":"DAY"}},{"used_percent":1,"reset_at":"2026-09-16T13:00:00Z","window":{"duration":5,"timeUnit":"HOUR"}}]}`)
	obs, err := parse(raw, "auth", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Windows) != 1 || obs.Windows[0].BucketID != "coding-week" || obs.Windows[0].WindowDuration != 7*24*time.Hour {
		t.Fatalf("windows=%#v", obs.Windows)
	}
}
