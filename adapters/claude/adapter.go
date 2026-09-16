package claude

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cpa-plugins/quota-window-activator/adapters"
	"github.com/cpa-plugins/quota-window-activator/adapters/internal/providerutil"
	"github.com/cpa-plugins/quota-window-activator/core"
)

type Adapter struct {
	Client adapters.HTTPClient
	Now    func() time.Time
}

func New(c adapters.HTTPClient) *Adapter      { return &Adapter{Client: c, Now: time.Now} }
func (*Adapter) ID() string                   { return "claude" }
func (*Adapter) Match(c core.Credential) bool { return strings.EqualFold(c.Provider, "claude") }
func (*Adapter) Fingerprint(c core.Credential) (string, error) {
	doc, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return "", e
	}
	subject, _ := providerutil.DeepString(doc, "account_id", "user_id", "email", "organization_id")
	return providerutil.Fingerprint("claude", subject)
}
func (a *Adapter) ReadQuota(ctx context.Context, c core.Credential) (core.Observation, error) {
	doc, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return core.Observation{}, e
	}
	token, _ := providerutil.DeepString(doc, "access_token")
	if token == "" {
		return core.Observation{}, errors.New("Claude access token missing")
	}
	resp, e := a.Client.Do(ctx, c, core.ActivationRequest{Method: http.MethodGet, URL: "https://api.anthropic.com/api/oauth/usage", Headers: http.Header{"Authorization": []string{"Bearer " + token}, "Content-Type": []string{"application/json"}, "anthropic-beta": []string{"oauth-2025-04-20"}}})
	if e != nil {
		return core.Observation{}, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return core.Observation{}, fmt.Errorf("Claude quota status %d", resp.StatusCode)
	}
	return parse(resp.Body, c.AuthID, a.Now().UTC())
}
func parse(raw []byte, authID string, now time.Time) (core.Observation, error) {
	doc, e := providerutil.Decode(raw)
	if e != nil {
		return core.Observation{}, e
	}
	keys := []string{"five_hour", "seven_day", "seven_day_oauth_apps", "seven_day_opus", "seven_day_sonnet", "seven_day_cowork", "iguana_necktie"}
	var out []core.QuotaWindow
	for _, key := range keys {
		m, ok := providerutil.Object(doc, key)
		if !ok {
			continue
		}
		used, ok := providerutil.Number(m, "utilization")
		if !ok {
			continue
		}
		reset, ok := providerutil.Time(m, "resets_at")
		if !ok {
			continue
		}
		duration := 7 * 24 * time.Hour
		if key == "five_hour" {
			duration = 5 * time.Hour
		}
		out = append(out, core.QuotaWindow{Provider: "claude", AuthID: authID, BucketID: key, ModelFamily: key, Scope: "oauth_usage", UsedPercent: &used, ResetAt: reset, WindowDuration: duration, ObservedAt: now, Complete: true})
	}
	if len(out) == 0 {
		return core.Observation{}, errors.New("Claude response has no complete quota windows")
	}
	return core.Observation{Windows: out}, nil
}
