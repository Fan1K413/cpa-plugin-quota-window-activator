package antigravity

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

var quotaURLs = []string{"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary", "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary", "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"}

type Adapter struct {
	Client adapters.HTTPClient
	Now    func() time.Time
}

func New(c adapters.HTTPClient) *Adapter      { return &Adapter{Client: c, Now: time.Now} }
func (*Adapter) ID() string                   { return "antigravity" }
func (*Adapter) Match(c core.Credential) bool { return strings.EqualFold(c.Provider, "antigravity") }
func (*Adapter) Fingerprint(c core.Credential) (string, error) {
	doc, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return "", e
	}
	project, _ := providerutil.DeepString(doc, "project_id", "projectId")
	email, _ := providerutil.DeepString(doc, "email")
	return providerutil.Fingerprint("antigravity", project, email)
}
func (a *Adapter) ReadQuota(ctx context.Context, c core.Credential) (core.Observation, error) {
	if a.Client == nil {
		return core.Observation{}, errors.New("HTTP client unavailable")
	}
	doc, e := providerutil.Decode(c.RawJSON)
	if e != nil {
		return core.Observation{}, e
	}
	token, _ := providerutil.DeepString(doc, "access_token")
	project, _ := providerutil.DeepString(doc, "project_id", "projectId")
	if token == "" || project == "" {
		return core.Observation{}, errors.New("Antigravity token or project missing")
	}
	body, _ := json.Marshal(map[string]string{"project": project})
	var last error
	for _, url := range quotaURLs {
		resp, e := a.Client.Do(ctx, c, core.ActivationRequest{Method: http.MethodPost, URL: url, Headers: http.Header{"Authorization": []string{"Bearer " + token}, "Content-Type": []string{"application/json"}, "User-Agent": []string{"antigravity/cli/1.0.13 (aidev_client; os_type=windows; arch=amd64)"}}, Body: body})
		if e != nil {
			last = e
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			last = fmt.Errorf("Antigravity quota status %d", resp.StatusCode)
			continue
		}
		return parse(resp.Body, c.AuthID, a.Now().UTC())
	}
	return core.Observation{}, last
}
func parse(raw []byte, authID string, now time.Time) (core.Observation, error) {
	doc, e := providerutil.Decode(raw)
	if e != nil {
		return core.Observation{}, e
	}
	groups, ok := providerutil.Slice(doc, "groups")
	if !ok {
		return core.Observation{}, errors.New("Antigravity quota groups missing")
	}
	var out []core.QuotaWindow
	for gi, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		group := providerutil.String(gm, "displayName", "display_name")
		buckets, _ := providerutil.Slice(gm, "buckets")
		for bi, b := range buckets {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			id := providerutil.String(bm, "bucketId", "bucket_id")
			if id == "" {
				continue
			}
			fraction, ok := providerutil.Number(bm, "remainingFraction", "remaining_fraction")
			if !ok {
				continue
			}
			reset, ok := providerutil.Time(bm, "resetTime", "reset_time")
			if !ok {
				continue
			}
			window := providerutil.String(bm, "window")
			duration := providerutil.WindowDuration(window)
			if duration <= 0 {
				continue
			}
			used := 100 * (1 - fraction)
			out = append(out, core.QuotaWindow{Provider: "antigravity", AuthID: authID, BucketID: id, ModelFamily: group, Scope: fmt.Sprintf("group-%d-bucket-%d", gi, bi), UsedPercent: &used, ResetAt: reset, WindowDuration: duration, ObservedAt: now, Complete: true})
		}
	}
	if len(out) == 0 {
		return core.Observation{}, errors.New("Antigravity response has no complete stable buckets")
	}
	return core.Observation{Windows: out}, nil
}
