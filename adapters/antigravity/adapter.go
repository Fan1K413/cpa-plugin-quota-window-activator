package antigravity

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cpa-plugins/quota-window-activator/adapters"
	"github.com/cpa-plugins/quota-window-activator/adapters/internal/providerutil"
	"github.com/cpa-plugins/quota-window-activator/core"
)

const (
	dailyBaseURL     = "https://daily-cloudcode-pa.googleapis.com"
	quotaSummaryPath = "/v1internal:retrieveUserQuotaSummary"
	generatePath     = "/v1internal:generateContent"
	geminiGroup      = "antigravity-gemini"
	thirdPartyGroup  = "antigravity-third-party"
	geminiProbeModel = "gemini-3.1-flash-lite"
	thirdPartyModel  = "claude-sonnet-4-6"
	antigravityUA    = "antigravity/cli/1.0.13 (aidev_client; os_type=windows; arch=amd64)"
)

var quotaURLs = []string{dailyBaseURL + quotaSummaryPath, "https://daily-cloudcode-pa.sandbox.googleapis.com" + quotaSummaryPath, "https://cloudcode-pa.googleapis.com" + quotaSummaryPath}

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
		resp, e := a.Client.Do(ctx, c, core.ActivationRequest{Method: http.MethodPost, URL: url, Headers: antigravityHeaders(token), Body: body})
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

// CanActivate is deliberately limited to the stable buckets published by
// retrieveUserQuotaSummary. Unknown/model-specific buckets remain observable
// until their sharing and lazy-reset semantics are proven.
func (*Adapter) CanActivate(w core.QuotaWindow) bool {
	group := activationGroup(w.BucketID)
	if group == "" || w.ActivationGroup != group {
		return false
	}
	return w.WindowDuration == 5*time.Hour || w.WindowDuration == 7*24*time.Hour
}

func (a *Adapter) PlanActivation(_ context.Context, c core.Credential, records []core.WindowRecord) (core.ActivationPlan, error) {
	if len(records) == 0 {
		return core.ActivationPlan{}, errors.New("no target windows")
	}
	group := records[0].ActivationGroup
	model := activationModel(group)
	if model == "" {
		return core.ActivationPlan{}, errors.New("unsupported Antigravity activation group")
	}
	keys := make([]string, 0, len(records))
	for _, record := range records {
		if record.ActivationGroup != group || !a.CanActivate(record.Latest) {
			return core.ActivationPlan{}, errors.New("Antigravity activation records cross quota groups")
		}
		keys = append(keys, record.Key)
	}
	doc, err := providerutil.Decode(c.RawJSON)
	if err != nil {
		return core.ActivationPlan{}, err
	}
	token, _ := providerutil.DeepString(doc, "access_token")
	project, _ := providerutil.DeepString(doc, "project_id", "projectId")
	if token == "" || project == "" {
		return core.ActivationPlan{}, errors.New("Antigravity token or project missing")
	}
	requestID, sessionID, err := requestIDs()
	if err != nil {
		return core.ActivationPlan{}, err
	}
	payload := map[string]any{
		"model":       model,
		"userAgent":   "antigravity",
		"requestType": "agent",
		"project":     project,
		"requestId":   requestID,
		"request": map[string]any{
			"sessionId": sessionID,
			"contents": []any{map[string]any{
				"role":  "user",
				"parts": []any{map[string]any{"text": "ping"}},
			}},
			"generationConfig": map[string]any{
				"candidateCount":  1,
				"maxOutputTokens": 1,
				"temperature":     0,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return core.ActivationPlan{}, err
	}
	baseURL := antigravityBaseURL(c, doc)
	return core.ActivationPlan{
		Group: group, WindowKeys: keys, TargetModel: model, MaxInput: 4, MaxOutput: 1,
		Request: core.ActivationRequest{Method: http.MethodPost, URL: baseURL + generatePath, Headers: antigravityHeaders(token), Body: body},
	}, nil
}

func (*Adapter) VerifyActivation(before core.Baseline, after core.QuotaWindow) core.VerifyResult {
	if after.ResetAt.After(before.ResetAt.Add(2 * time.Minute)) {
		return core.VerifyResult{Confirmed: true, Reason: "new Antigravity quota window confirmed"}
	}
	return core.VerifyResult{Reason: "Antigravity window has not rolled after activation"}
}

func antigravityHeaders(token string) http.Header {
	return http.Header{
		"Authorization": []string{"Bearer " + token},
		"Content-Type":  []string{"application/json"},
		"User-Agent":    []string{antigravityUA},
	}
}

func activationGroup(bucketID string) string {
	switch strings.ToLower(strings.TrimSpace(bucketID)) {
	case "gemini-5h", "gemini-weekly":
		return geminiGroup
	case "3p-5h", "3p-weekly":
		return thirdPartyGroup
	default:
		return ""
	}
}

func activationModel(group string) string {
	switch group {
	case geminiGroup:
		return geminiProbeModel
	case thirdPartyGroup:
		return thirdPartyModel
	default:
		return ""
	}
}

func antigravityBaseURL(c core.Credential, doc map[string]any) string {
	for _, key := range []string{"base_url", "base-url"} {
		if value := strings.TrimSpace(c.Attributes[key]); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	if value, _ := providerutil.DeepString(doc, "base_url", "base-url"); value != "" {
		return strings.TrimRight(value, "/")
	}
	return dailyBaseURL
}

func requestIDs() (string, string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", fmt.Errorf("generate Antigravity request ID: %w", err)
	}
	requestID := fmt.Sprintf("agent-%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
	var sessionRaw [8]byte
	if _, err := rand.Read(sessionRaw[:]); err != nil {
		return "", "", fmt.Errorf("generate Antigravity session ID: %w", err)
	}
	var value uint64
	for _, b := range sessionRaw {
		value = value<<8 | uint64(b)
	}
	value = value%9_000_000_000_000_000_000 + 1
	return requestID, "-" + strconv.FormatUint(value, 10), nil
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
			lazy := used == 0 && absDuration(reset.Sub(now.Add(duration))) <= 3*time.Minute
			out = append(out, core.QuotaWindow{Provider: "antigravity", AuthID: authID, BucketID: id, ModelFamily: group, Scope: fmt.Sprintf("group-%d-bucket-%d", gi, bi), UsedPercent: &used, ResetAt: reset, WindowDuration: duration, ObservedAt: now, ActivationGroup: activationGroup(id), LazyHint: lazy, Complete: true})
		}
	}
	if len(out) == 0 {
		return core.Observation{}, errors.New("Antigravity response has no complete stable buckets")
	}
	return core.Observation{Windows: out}, nil
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
