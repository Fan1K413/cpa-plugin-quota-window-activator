package geminicli

import (
	"context"
	"encoding/json"
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
func (*Adapter) ID() string              { return "gemini-cli" }
func (*Adapter) Match(c core.Credential) bool {
	return strings.EqualFold(c.Provider, "gemini-cli") || strings.EqualFold(c.Provider, "geminicli")
}
func (*Adapter) Fingerprint(c core.Credential) (string, error) {
	d, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return "", e
	}
	id, _ := providerutil.DeepString(d, "user_id", "sub", "email")
	project, _ := providerutil.DeepString(d, "project_id", "projectId")
	return providerutil.Fingerprint("gemini-cli", id, project)
}
func (a *Adapter) ReadQuota(ctx context.Context, c core.Credential) (core.Observation, error) {
	d, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return core.Observation{}, e
	}
	token, _ := providerutil.DeepString(d, "access_token")
	project, _ := providerutil.DeepString(d, "project_id", "projectId")
	if token == "" || project == "" {
		return core.Observation{}, errors.New("Gemini CLI token or project missing")
	}
	body, _ := json.Marshal(map[string]string{"project": project})
	r, e := a.Client.Do(ctx, c, core.ActivationRequest{Method: http.MethodPost, URL: "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota", Headers: http.Header{"Authorization": []string{"Bearer " + token}, "Content-Type": []string{"application/json"}}, Body: body})
	if e != nil {
		return core.Observation{}, e
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return core.Observation{}, fmt.Errorf("Gemini CLI quota status %d", r.StatusCode)
	}
	return parse(r.Body, c.AuthID, a.Now().UTC())
}
func parse(raw []byte, authID string, now time.Time) (core.Observation, error) {
	d, e := providerutil.Decode(raw)
	if e != nil {
		return core.Observation{}, e
	}
	buckets, ok := providerutil.Slice(d, "buckets")
	if !ok {
		return core.Observation{}, errors.New("Gemini CLI buckets missing")
	}
	var out []core.QuotaWindow
	for _, x := range buckets {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		model := providerutil.String(m, "modelId")
		tokenType := providerutil.String(m, "tokenType")
		reset, ok := providerutil.Time(m, "resetTime")
		if !ok {
			continue
		}
		fraction, ok := providerutil.Number(m, "remainingFraction")
		if !ok {
			continue
		}
		used := 100 * (1 - fraction)
		duration := reset.Sub(now)
		if duration <= 0 || duration > 32*24*time.Hour {
			continue
		}
		id := model + ":" + tokenType
		if model == "" || tokenType == "" {
			continue
		}
		out = append(out, core.QuotaWindow{Provider: "gemini-cli", AuthID: authID, BucketID: id, ModelFamily: model, Scope: tokenType, UsedPercent: &used, ResetAt: reset, WindowDuration: duration, ObservedAt: now, Complete: true})
	}
	if len(out) == 0 {
		return core.Observation{}, errors.New("Gemini CLI response has no complete buckets")
	}
	return core.Observation{Windows: out}, nil
}
