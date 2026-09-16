package codex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cpa-plugins/quota-window-activator/adapters"
	"github.com/cpa-plugins/quota-window-activator/core"
)

const (
	quotaURL        = "https://chatgpt.com/backend-api/wham/usage"
	activationURL   = "https://chatgpt.com/backend-api/codex/responses/compact"
	activationModel = "gpt-5.4-mini"
)

type Adapter struct {
	Client adapters.HTTPClient
	Now    func() time.Time
}

func New(client adapters.HTTPClient) *Adapter { return &Adapter{Client: client, Now: time.Now} }
func (*Adapter) ID() string                   { return "codex" }
func (*Adapter) Match(c core.Credential) bool {
	return strings.EqualFold(strings.TrimSpace(c.Provider), "codex")
}

type credentials struct{ AccessToken, RefreshToken, AccountID string }

func (a *Adapter) Fingerprint(c core.Credential) (string, error) {
	parsed, err := extractCredentials(c.RawJSON)
	if err != nil {
		return "", err
	}
	// Account identity is deliberately independent of access/refresh token
	// rotation. AuthIndex in InstanceKey separates duplicate local records.
	if parsed.AccountID == "" {
		return "", errors.New("missing ChatGPT account ID")
	}
	sum := sha256.Sum256([]byte("codex\x00" + parsed.AccountID))
	return hex.EncodeToString(sum[:]), nil
}

func (a *Adapter) ReadQuota(ctx context.Context, c core.Credential) (core.Observation, error) {
	if a.Client == nil {
		return core.Observation{}, errors.New("HTTP client is unavailable")
	}
	parsed, err := extractCredentials(c.RawJSON)
	if err != nil {
		return core.Observation{}, err
	}
	now := a.Now().UTC()
	req := core.ActivationRequest{Method: http.MethodGet, URL: quotaURL, Headers: authHeaders(parsed)}
	response, err := a.Client.Do(ctx, c, req)
	if err != nil {
		return core.Observation{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return core.Observation{}, fmt.Errorf("Codex quota status %d", response.StatusCode)
	}
	return parseQuota(response.Body, c.AuthID, now)
}

func (*Adapter) CanActivate(w core.QuotaWindow) bool {
	if w.Scope != "main" || w.WindowDuration <= 0 {
		return false
	}
	return w.WindowDuration == 5*time.Hour || (w.WindowDuration >= 7*24*time.Hour && w.WindowDuration <= 32*24*time.Hour)
}

func (a *Adapter) PlanActivation(_ context.Context, c core.Credential, records []core.WindowRecord) (core.ActivationPlan, error) {
	parsed, err := extractCredentials(c.RawJSON)
	if err != nil {
		return core.ActivationPlan{}, err
	}
	if len(records) == 0 {
		return core.ActivationPlan{}, errors.New("no target windows")
	}
	keys := make([]string, 0, len(records))
	for _, r := range records {
		if r.ActivationGroup != "codex-main" {
			return core.ActivationPlan{}, errors.New("unsupported Codex activation group")
		}
		keys = append(keys, r.Key)
	}
	payload := map[string]any{
		"model": activationModel, "instructions": "", "max_output_tokens": 1,
		"reasoning": map[string]any{"effort": "low"},
		"input":     []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "ping"}}}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return core.ActivationPlan{}, err
	}
	return core.ActivationPlan{Group: "codex-main", WindowKeys: keys, TargetModel: activationModel, MaxInput: 4, MaxOutput: 1, Request: core.ActivationRequest{Method: http.MethodPost, URL: activationURL, Headers: authHeaders(parsed), Body: body}}, nil
}

func (*Adapter) VerifyActivation(before core.Baseline, after core.QuotaWindow) core.VerifyResult {
	if after.ResetAt.After(before.ResetAt.Add(2*time.Minute)) && !after.LazyHint {
		return core.VerifyResult{Confirmed: true, Reason: "new Codex quota window confirmed"}
	}
	if before.UsedPercent != nil && after.UsedPercent != nil && *before.UsedPercent > 0 && *after.UsedPercent == 0 && !after.LazyHint {
		return core.VerifyResult{Confirmed: true, Reason: "Codex activation inferred from usage reset"}
	}
	return core.VerifyResult{Reason: "Codex window has not rolled after activation"}
}

func authHeaders(c credentials) http.Header {
	h := http.Header{"Authorization": []string{"Bearer " + c.AccessToken}, "Content-Type": []string{"application/json"}, "User-Agent": []string{"cpa-quota-window-activator/0.1"}}
	if c.AccountID != "" {
		h.Set("Chatgpt-Account-Id", c.AccountID)
	}
	return h
}

func parseQuota(raw []byte, authID string, now time.Time) (core.Observation, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return core.Observation{}, err
	}
	rate, ok := object(doc, "rate_limit", "rateLimit")
	if !ok {
		return core.Observation{}, errors.New("Codex quota response missing rate_limit")
	}
	windows := make([]core.QuotaWindow, 0, 8)
	for _, item := range []struct {
		name     string
		fallback time.Duration
	}{{"primary_window", 5 * time.Hour}, {"secondary_window", 7 * 24 * time.Hour}} {
		rawWindow, ok := object(rate, item.name, toCamel(item.name))
		if !ok {
			continue
		}
		if parsed, ok := parseWindow(rawWindow, authID, "main", item.name, item.fallback, now, true); ok {
			windows = append(windows, parsed)
		}
	}
	if review, ok := object(doc, "code_review_rate_limit", "codeReviewRateLimit"); ok {
		for _, item := range []struct {
			name     string
			fallback time.Duration
		}{{"primary_window", 5 * time.Hour}, {"secondary_window", 7 * 24 * time.Hour}} {
			if rawWindow, ok := object(review, item.name, toCamel(item.name)); ok {
				if parsed, ok := parseWindow(rawWindow, authID, "code_review", "code-review:"+item.name, item.fallback, now, false); ok {
					windows = append(windows, parsed)
				}
			}
		}
	}
	if limits, ok := slice(doc, "additional_rate_limits", "additionalRateLimits"); ok {
		for i, item := range limits {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := stringValue(m, "limit_name", "limitName")
			if name == "" {
				name = fmt.Sprintf("additional-%d", i)
			}
			r, ok := object(m, "rate_limit", "rateLimit")
			if !ok {
				continue
			}
			for _, slot := range []string{"primary_window", "secondary_window"} {
				if rw, ok := object(r, slot, toCamel(slot)); ok {
					if parsed, ok := parseWindow(rw, authID, "additional", name+":"+slot, 0, now, false); ok {
						parsed.ModelFamily = name
						windows = append(windows, parsed)
					}
				}
			}
		}
	}
	if len(windows) == 0 {
		return core.Observation{}, errors.New("Codex quota response has no complete windows")
	}
	return core.Observation{Windows: windows}, nil
}

func parseWindow(raw map[string]any, authID, scope, id string, fallback time.Duration, now time.Time, activate bool) (core.QuotaWindow, bool) {
	duration := fallback
	if seconds, ok := number(raw, "limit_window_seconds", "limitWindowSeconds"); ok && seconds > 0 {
		duration = time.Duration(seconds * float64(time.Second))
	}
	reset, ok := resetAt(raw, now)
	if !ok || duration <= 0 {
		return core.QuotaWindow{}, false
	}
	used, usedOK := number(raw, "used_percent", "usedPercent")
	var usedPtr *float64
	if usedOK {
		usedPtr = &used
	}
	lazy := usedOK && used == 0 && abs(reset.Sub(now.Add(duration))) <= 3*time.Minute
	group := ""
	if activate {
		group = "codex-main"
	}
	bucket := scope + ":" + id + ":" + fmt.Sprint(int64(duration/time.Second))
	return core.QuotaWindow{Provider: "codex", AuthID: authID, BucketID: bucket, Scope: scope, UsedPercent: usedPtr, ResetAt: reset, WindowDuration: duration, ObservedAt: now, ActivationGroup: group, LazyHint: lazy, Complete: usedOK}, true
}

func extractCredentials(raw []byte) (credentials, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return credentials{}, err
	}
	access, _ := deepString(doc, "access_token")
	if access == "" {
		return credentials{}, errors.New("missing access_token")
	}
	refresh, _ := deepString(doc, "refresh_token")
	account, _ := deepString(doc, "account_id")
	if account == "" {
		account, _ = deepString(doc, "chatgpt_account_id")
	}
	if account == "" {
		if id, _ := deepString(doc, "id_token"); id != "" {
			account = accountFromJWT(id)
		}
	}
	return credentials{AccessToken: access, RefreshToken: refresh, AccountID: account}, nil
}
func accountFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims any
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	v, _ := deepString(claims, "https://api.openai.com/auth.chatgpt_account_id")
	if v == "" {
		v, _ = deepString(claims, "chatgpt_account_id")
	}
	return v
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
func object(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		if x, ok := m[k].(map[string]any); ok {
			return x, true
		}
	}
	return nil, false
}
func slice(m map[string]any, keys ...string) ([]any, bool) {
	for _, k := range keys {
		if x, ok := m[k].([]any); ok {
			return x, true
		}
	}
	return nil, false
}
func stringValue(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if x, ok := m[k].(string); ok {
			return x
		}
	}
	return ""
}
func number(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		switch x := m[k].(type) {
		case float64:
			return x, true
		case json.Number:
			v, e := x.Float64()
			return v, e == nil
		}
	}
	return 0, false
}
func resetAt(m map[string]any, now time.Time) (time.Time, bool) {
	if raw := stringValue(m, "reset_at", "resetAt"); raw != "" {
		if v, e := time.Parse(time.RFC3339, raw); e == nil {
			return v, true
		}
	}
	if seconds, ok := number(m, "reset_after_seconds", "resetAfterSeconds"); ok {
		return now.Add(time.Duration(seconds * float64(time.Second))), true
	}
	return time.Time{}, false
}
func toCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}
func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
