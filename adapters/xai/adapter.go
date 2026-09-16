package xai

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

func New(c adapters.HTTPClient) *Adapter { return &Adapter{Client: c, Now: time.Now} }
func (*Adapter) ID() string              { return "xai" }
func (*Adapter) Match(c core.Credential) bool {
	return strings.EqualFold(c.Provider, "xai") || strings.EqualFold(c.Provider, "grok")
}
func (*Adapter) Fingerprint(c core.Credential) (string, error) {
	d, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return "", e
	}
	id, _ := providerutil.DeepString(d, "user_id", "sub", "email")
	return providerutil.Fingerprint("xai", id)
}
func (a *Adapter) ReadQuota(ctx context.Context, c core.Credential) (core.Observation, error) {
	d, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return core.Observation{}, e
	}
	token, _ := providerutil.DeepString(d, "access_token")
	if token == "" {
		return core.Observation{}, errors.New("xAI access token missing")
	}
	r, e := a.Client.Do(ctx, c, core.ActivationRequest{Method: http.MethodGet, URL: "https://cli-chat-proxy.grok.com/v1/billing?format=credits", Headers: http.Header{"Authorization": []string{"Bearer " + token}, "x-xai-token-auth": []string{"xai-grok-cli"}, "accept": []string{"application/json"}}})
	if e != nil {
		return core.Observation{}, e
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return core.Observation{}, fmt.Errorf("xAI billing status %d", r.StatusCode)
	}
	return parse(r.Body, c.AuthID, a.Now().UTC())
}
func parse(raw []byte, authID string, now time.Time) (core.Observation, error) {
	d, e := providerutil.Decode(raw)
	if e != nil {
		return core.Observation{}, e
	}
	cfg, ok := providerutil.Object(d, "config")
	if !ok {
		cfg = d
	}
	periods, ok := providerutil.Slice(cfg, "periods", "billing_periods")
	if !ok {
		return core.Observation{}, errors.New("xAI billing periods missing")
	}
	var out []core.QuotaWindow
	for _, x := range periods {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		kind := providerutil.String(m, "periodType", "period_type", "type")
		reset, ok := providerutil.Time(m, "resetAt", "reset_at", "endTime", "end_time")
		if !ok {
			continue
		}
		used, ok := providerutil.Number(m, "usedPercent", "used_percent", "utilization")
		if !ok {
			continue
		}
		duration := providerutil.WindowDuration(kind)
		if duration <= 0 {
			if strings.Contains(strings.ToLower(kind), "week") {
				duration = 7 * 24 * time.Hour
			} else if strings.Contains(strings.ToLower(kind), "month") {
				duration = 30 * 24 * time.Hour
			}
		}
		if duration <= 0 {
			continue
		}
		id := providerutil.String(m, "id", "product")
		if id == "" {
			id = kind
		}
		if id == "" {
			continue
		}
		out = append(out, core.QuotaWindow{Provider: "xai", AuthID: authID, BucketID: id, Scope: kind, UsedPercent: &used, ResetAt: reset, WindowDuration: duration, ObservedAt: now, Complete: true})
	}
	if len(out) == 0 {
		return core.Observation{}, errors.New("xAI response has no complete billing windows")
	}
	return core.Observation{Windows: out}, nil
}
