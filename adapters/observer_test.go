package adapters_test

import (
	"testing"
	"time"

	"github.com/cpa-plugins/quota-window-activator/adapters/antigravity"
	"github.com/cpa-plugins/quota-window-activator/adapters/claude"
	"github.com/cpa-plugins/quota-window-activator/adapters/geminicli"
	"github.com/cpa-plugins/quota-window-activator/adapters/kimi"
	"github.com/cpa-plugins/quota-window-activator/adapters/xai"
	"github.com/cpa-plugins/quota-window-activator/core"
)

func TestObservationAdaptersDoNotExposeActivation(t *testing.T) {
	for _, a := range []core.Adapter{claude.New(nil), geminicli.New(nil), kimi.New(nil), xai.New(nil)} {
		if _, ok := a.(core.ActivationAdapter); ok {
			t.Fatalf("%s unexpectedly exposes activation", a.ID())
		}
	}
}

func TestAntigravityExposesCredentialBoundActivation(t *testing.T) {
	if _, ok := any(antigravity.New(nil)).(core.ActivationAdapter); !ok {
		t.Fatal("antigravity activation adapter missing")
	}
}

func TestClaudeFingerprintRejectsIdentitylessCredential(t *testing.T) {
	_, err := claude.New(nil).Fingerprint(core.Credential{Provider: "claude", RawJSON: []byte(`{"access_token":"secret"}`)})
	if err == nil {
		t.Fatal("expected identity error")
	}
}

func TestWindowJSONDurationRoundTrip(t *testing.T) {
	w := core.QuotaWindow{BucketID: "b", ResetAt: time.Now(), WindowDuration: 5 * time.Hour, ObservedAt: time.Now(), Complete: true}
	if !w.Valid() {
		t.Fatal("expected valid neutral window")
	}
}
