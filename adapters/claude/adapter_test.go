package claude

import (
	"testing"
	"time"
)

func TestParseDistinctClaudeWindows(t *testing.T) {
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	raw := []byte(`{"five_hour":{"utilization":42,"resets_at":"2026-09-16T13:00:00Z"},"seven_day":{"utilization":7,"resets_at":"2026-09-23T08:00:00Z"}}`)
	obs, err := parse(raw, "auth", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Windows) != 2 {
		t.Fatalf("windows=%#v", obs.Windows)
	}
	if obs.Windows[0].BucketID != "five_hour" || obs.Windows[0].WindowDuration != 5*time.Hour {
		t.Fatalf("first=%#v", obs.Windows[0])
	}
	if obs.Windows[1].BucketID != "seven_day" || obs.Windows[1].WindowDuration != 7*24*time.Hour {
		t.Fatalf("second=%#v", obs.Windows[1])
	}
}
