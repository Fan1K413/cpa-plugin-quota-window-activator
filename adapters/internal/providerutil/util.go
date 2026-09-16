package providerutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func Decode(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}
func DeepString(v any, keys ...string) (string, bool) {
	for _, key := range keys {
		if got, ok := deepString(v, key); ok && strings.TrimSpace(got) != "" {
			return strings.TrimSpace(got), true
		}
	}
	return "", false
}
func deepString(v any, key string) (string, bool) {
	switch x := v.(type) {
	case map[string]any:
		if got, ok := x[key].(string); ok {
			return got, true
		}
		for _, child := range x {
			if got, ok := deepString(child, key); ok {
				return got, true
			}
		}
	case []any:
		for _, child := range x {
			if got, ok := deepString(child, key); ok {
				return got, true
			}
		}
	}
	return "", false
}
func Object(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		if x, ok := m[k].(map[string]any); ok {
			return x, true
		}
	}
	return nil, false
}
func Slice(m map[string]any, keys ...string) ([]any, bool) {
	for _, k := range keys {
		if x, ok := m[k].([]any); ok {
			return x, true
		}
	}
	return nil, false
}
func String(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if x, ok := m[k].(string); ok {
			return strings.TrimSpace(x)
		}
	}
	return ""
}
func Number(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		switch x := m[k].(type) {
		case float64:
			return x, true
		case json.Number:
			v, e := x.Float64()
			return v, e == nil
		case string:
			v, e := json.Number(x).Float64()
			return v, e == nil
		}
	}
	return 0, false
}
func Time(m map[string]any, keys ...string) (time.Time, bool) {
	for _, k := range keys {
		if x, ok := m[k].(string); ok {
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
				if v, e := time.Parse(layout, x); e == nil {
					return v, true
				}
			}
		}
	}
	return time.Time{}, false
}
func Fingerprint(parts ...string) (string, error) {
	if len(parts) < 2 {
		return "", errors.New("credential identity fields are missing")
	}
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) != "" {
			sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
			return hex.EncodeToString(sum[:]), nil
		}
	}
	return "", errors.New("credential identity fields are missing")
}
func WindowDuration(raw string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "5h", "five-hour", "five_hour":
		return 5 * time.Hour
	case "7d", "weekly", "week", "seven_day", "seven-day":
		return 7 * 24 * time.Hour
	case "monthly", "month":
		return 30 * 24 * time.Hour
	}
	if d, e := time.ParseDuration(raw); e == nil {
		return d
	}
	return 0
}
