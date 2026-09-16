package kimi

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
func (*Adapter) ID() string                   { return "kimi" }
func (*Adapter) Match(c core.Credential) bool { return strings.EqualFold(c.Provider, "kimi") }
func (*Adapter) Fingerprint(c core.Credential) (string, error) {
	d, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return "", e
	}
	id, _ := providerutil.DeepString(d, "user_id", "account_id", "email")
	return providerutil.Fingerprint("kimi", id)
}
func (a *Adapter) ReadQuota(ctx context.Context, c core.Credential) (core.Observation, error) {
	d, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return core.Observation{}, e
	}
	token, _ := providerutil.DeepString(d, "access_token")
	if token == "" {
		return core.Observation{}, errors.New("Kimi access token missing")
	}
	r, e := a.Client.Do(ctx, c, core.ActivationRequest{Method: http.MethodGet, URL: "https://api.kimi.com/coding/v1/usages", Headers: http.Header{"Authorization": []string{"Bearer " + token}}})
	if e != nil {
		return core.Observation{}, e
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return core.Observation{}, fmt.Errorf("Kimi quota status %d", r.StatusCode)
	}
	return parse(r.Body, c.AuthID, a.Now().UTC())
}
func parse(raw []byte, authID string, now time.Time) (core.Observation, error) {
	d, e := providerutil.Decode(raw)
	if e != nil {
		return core.Observation{}, e
	}
	limits, ok := providerutil.Slice(d, "limits", "usages")
	if !ok {
		return core.Observation{}, errors.New("Kimi limits missing")
	}
	var out []core.QuotaWindow
	for _, x := range limits {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		detail, _ := providerutil.Object(m, "detail")
		window, _ := providerutil.Object(m, "window")
		id := providerutil.String(m, "id", "scope", "name")
		if id == "" {
			// Array position is not a stable bucket identity. Omit this row rather
			// than carrying state across a provider-side reorder.
			continue
		}
		used, usedOK := providerutil.Number(m, "used_percent", "utilization", "percent")
		if !usedOK && detail != nil {
			used, usedOK = providerutil.Number(detail, "used_percent", "utilization", "percent")
		}
		reset, resetOK := providerutil.Time(m, "reset_at", "resetAt", "reset_time", "resetTime")
		if !resetOK && detail != nil {
			reset, resetOK = providerutil.Time(detail, "reset_at", "resetAt", "reset_time", "resetTime")
		}
		duration := time.Duration(0)
		if window != nil {
			n, nOK := providerutil.Number(window, "duration")
			unit := strings.ToUpper(providerutil.String(window, "timeUnit", "time_unit"))
			if nOK {
				switch {
				case strings.Contains(unit, "SECOND"):
					duration = time.Duration(n) * time.Second
				case strings.Contains(unit, "HOUR"):
					duration = time.Duration(n) * time.Hour
				case strings.Contains(unit, "DAY"):
					duration = time.Duration(n) * 24 * time.Hour
				case strings.Contains(unit, "WEEK"):
					duration = time.Duration(n) * 7 * 24 * time.Hour
				default:
					duration = time.Duration(n) * time.Minute
				}
			}
		}
		if usedOK && resetOK && duration > 0 {
			out = append(out, core.QuotaWindow{Provider: "kimi", AuthID: authID, BucketID: id, Scope: "coding", UsedPercent: &used, ResetAt: reset, WindowDuration: duration, ObservedAt: now, Complete: true})
		}
	}
	if len(out) == 0 {
		return core.Observation{}, errors.New("Kimi response has no complete stable windows")
	}
	return core.Observation{Windows: out}, nil
}
