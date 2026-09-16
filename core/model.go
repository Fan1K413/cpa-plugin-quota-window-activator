package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type ProbeState string

const (
	StateObserving         ProbeState = "observing"
	StateWaitingReset      ProbeState = "waiting_reset"
	StatePendingPrecheck   ProbeState = "pending_precheck"
	StateNormalReset       ProbeState = "normal_reset"
	StateLazyDetected      ProbeState = "lazy_reset_detected"
	StateActivationSending ProbeState = "activation_sending"
	StateActivationSent    ProbeState = "activation_sent"
	StateSentUnknown       ProbeState = "sent_unknown"
	StateVerifying         ProbeState = "verifying"
	StateConfirmed         ProbeState = "confirmed"
	StateRetryWait         ProbeState = "retry_wait"
	StateAuthBlocked       ProbeState = "auth_blocked"
	StateUnsupported       ProbeState = "unsupported"
	StateDisabled          ProbeState = "disabled"
)

type AttemptPhase string

const (
	AttemptPrepared    AttemptPhase = "prepared"
	AttemptSending     AttemptPhase = "sending"
	AttemptSent        AttemptPhase = "sent"
	AttemptSentUnknown AttemptPhase = "sent_unknown"
	AttemptCompleted   AttemptPhase = "completed"
)

type Credential struct {
	AuthID      string            `json:"auth_id"`
	AuthIndex   string            `json:"auth_index"`
	Provider    string            `json:"provider"`
	Label       string            `json:"label,omitempty"`
	Disabled    bool              `json:"disabled"`
	RuntimeOnly bool              `json:"runtime_only,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	RawJSON     []byte            `json:"-"`
}

func (c Credential) InstanceKey(adapterID, fingerprint string) string {
	return strings.Join([]string{adapterID, c.AuthIndex, fingerprint}, "|")
}

// QuotaWindow is the provider-neutral observation. UsedPercent is nil when the
// provider omitted it; nil must never be treated as zero.
type QuotaWindow struct {
	Provider        string        `json:"provider"`
	AuthID          string        `json:"auth_id"`
	BucketID        string        `json:"bucket_id"`
	ModelFamily     string        `json:"model_family,omitempty"`
	Scope           string        `json:"scope,omitempty"`
	UsedPercent     *float64      `json:"used_percent,omitempty"`
	ResetAt         time.Time     `json:"reset_at"`
	WindowDuration  time.Duration `json:"window_duration"`
	ObservedAt      time.Time     `json:"observed_at"`
	ServerTime      time.Time     `json:"server_time,omitempty"`
	ActivationGroup string        `json:"activation_group,omitempty"`
	LazyHint        bool          `json:"lazy_hint,omitempty"`
	Complete        bool          `json:"complete"`
}

func (w QuotaWindow) Valid() bool {
	return w.BucketID != "" && !w.ResetAt.IsZero() && !w.ObservedAt.IsZero() && w.WindowDuration > 0 && w.Complete
}

type Observation struct {
	Windows []QuotaWindow `json:"windows"`
}

type Baseline struct {
	ResetAt        time.Time     `json:"reset_at"`
	WindowDuration time.Duration `json:"window_duration"`
	UsedPercent    *float64      `json:"used_percent,omitempty"`
	ObservedAt     time.Time     `json:"observed_at"`
	CycleID        string        `json:"cycle_id"`
}

type WindowRecord struct {
	Key                string      `json:"key"`
	InstanceKey        string      `json:"instance_key"`
	Provider           string      `json:"provider"`
	AuthID             string      `json:"auth_id"`
	BucketID           string      `json:"bucket_id"`
	ModelFamily        string      `json:"model_family,omitempty"`
	ActivationGroup    string      `json:"activation_group,omitempty"`
	CredentialFP       string      `json:"credential_fingerprint"`
	Baseline           Baseline    `json:"baseline"`
	Latest             QuotaWindow `json:"latest"`
	State              ProbeState  `json:"state"`
	LastProbe          time.Time   `json:"last_probe,omitempty"`
	NextCheck          time.Time   `json:"next_check,omitempty"`
	LastActivation     time.Time   `json:"last_activation,omitempty"`
	LastResult         string      `json:"last_result,omitempty"`
	RetryCount         int         `json:"retry_count,omitempty"`
	ActiveAttemptID    string      `json:"active_attempt_id,omitempty"`
	ActivationDisabled bool        `json:"activation_disabled,omitempty"`
}

type ProbeAttempt struct {
	ID              string       `json:"id"`
	InstanceKey     string       `json:"instance_key"`
	Provider        string       `json:"provider"`
	AuthID          string       `json:"auth_id"`
	AuthIndex       string       `json:"auth_index"`
	CredentialFP    string       `json:"credential_fingerprint"`
	ActivationGroup string       `json:"activation_group"`
	WindowKeys      []string     `json:"window_keys"`
	CycleIDs        []string     `json:"cycle_ids"`
	Phase           AttemptPhase `json:"phase"`
	CreatedAt       time.Time    `json:"created_at"`
	SendingAt       time.Time    `json:"sending_at,omitempty"`
	SentAt          time.Time    `json:"sent_at,omitempty"`
	VerifyNotBefore time.Time    `json:"verify_not_before"`
	SuppressUntil   time.Time    `json:"suppress_until"`
	LastError       string       `json:"last_error,omitempty"`
}

type CycleLedgerEntry struct {
	CycleID     string    `json:"cycle_id"`
	AttemptID   string    `json:"attempt_id"`
	SendingAt   time.Time `json:"sending_at"`
	SendOutcome string    `json:"send_outcome"`
}

type ActivationRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

type ActivationPlan struct {
	Group       string
	Request     ActivationRequest
	WindowKeys  []string
	TargetModel string
	MaxInput    int
	MaxOutput   int
}

type VerifyResult struct {
	Confirmed bool
	Reason    string
}

type Adapter interface {
	ID() string
	Match(Credential) bool
	Fingerprint(Credential) (string, error)
	ReadQuota(context.Context, Credential) (Observation, error)
}

type ActivationAdapter interface {
	Adapter
	CanActivate(QuotaWindow) bool
	PlanActivation(context.Context, Credential, []WindowRecord) (ActivationPlan, error)
	VerifyActivation(Baseline, QuotaWindow) VerifyResult
}

type Sender interface {
	Send(context.Context, Credential, ActivationRequest) error
}

func WindowKey(instanceKey, bucketID string) string {
	return instanceKey + "|" + bucketID
}

func NewCycleID(instanceKey, bucketID string, resetAt time.Time) string {
	sum := sha256.Sum256([]byte(instanceKey + "\x00" + bucketID + "\x00" + resetAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:16])
}

func normalizeWindows(in []QuotaWindow) ([]QuotaWindow, error) {
	seen := make(map[string]struct{}, len(in))
	out := append([]QuotaWindow(nil), in...)
	for _, w := range out {
		if !w.Valid() {
			return nil, fmt.Errorf("invalid quota window %q", w.BucketID)
		}
		if _, ok := seen[w.BucketID]; ok {
			return nil, fmt.Errorf("duplicate quota bucket %q", w.BucketID)
		}
		seen[w.BucketID] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BucketID < out[j].BucketID })
	return out, nil
}
