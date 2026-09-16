package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cpa-plugins/quota-window-activator/core"
)

func TestHasCredentialProxy(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"proxy_url":"http://127.0.0.1:8080"}`, true},
		{`{"proxy-url":"socks5://127.0.0.1:1080"}`, true},
		{`{"proxy_url":"  "}`, false},
		{`{"access_token":"secret"}`, false},
	} {
		if got := HasCredentialProxy([]byte(tc.raw)); got != tc.want {
			t.Fatalf("HasCredentialProxy(%s)=%v want %v", tc.raw, got, tc.want)
		}
	}
}

func TestDoReturnsWhenContextExpires(t *testing.T) {
	release := make(chan struct{})
	client := Client{Call: func(string, any) (json.RawMessage, error) {
		<-release
		return nil, errors.New("late")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Do(ctx, core.Credential{}, core.ActivationRequest{Method: "GET", URL: "https://example.invalid"})
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}
