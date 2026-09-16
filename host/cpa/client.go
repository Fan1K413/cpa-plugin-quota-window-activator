package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cpa-plugins/quota-window-activator/adapters"
	"github.com/cpa-plugins/quota-window-activator/core"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// HasCredentialProxy reports a per-auth proxy that host.http.do cannot inherit
// in CPA v7.2.154. Callers must fail closed instead of bypassing that proxy.
func HasCredentialProxy(raw []byte) bool {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	for _, key := range []string{"proxy_url", "proxy-url"} {
		if value, ok := doc[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

type Caller func(string, any) (json.RawMessage, error)
type Client struct{ Call Caller }

func (c Client) ListCredentials() ([]core.Credential, error) {
	raw, e := c.Call(pluginabi.MethodHostAuthList, map[string]any{})
	if e != nil {
		return nil, e
	}
	var response struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if e = json.Unmarshal(raw, &response); e != nil {
		return nil, fmt.Errorf("decode host.auth.list: %w", e)
	}
	out := make([]core.Credential, 0, len(response.Files))
	for _, f := range response.Files {
		out = append(out, core.Credential{AuthID: f.ID, AuthIndex: f.AuthIndex, Provider: f.Provider, Label: f.Label, Disabled: f.Disabled || f.Unavailable, RuntimeOnly: f.RuntimeOnly, Attributes: map[string]string{"path": f.Path, "status": f.Status, "project_id": f.ProjectID, "email": f.Email}})
	}
	return out, nil
}
func (c Client) GetCredential(in core.Credential) (core.Credential, error) {
	if in.RuntimeOnly {
		return in, fmt.Errorf("runtime-only credential %s has no host.auth.get payload", in.AuthID)
	}
	raw, e := c.Call(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: in.AuthIndex})
	if e != nil {
		return in, e
	}
	var response pluginapi.HostAuthGetResponse
	if e = json.Unmarshal(raw, &response); e != nil {
		return in, fmt.Errorf("decode host.auth.get: %w", e)
	}
	in.RawJSON = append([]byte(nil), response.JSON...)
	return in, nil
}
func (c Client) Do(ctx context.Context, _ core.Credential, request core.ActivationRequest) (adapters.HTTPResponse, error) {
	return c.do(ctx, request)
}

func (c Client) do(ctx context.Context, request core.ActivationRequest) (adapters.HTTPResponse, error) {
	if c.Call == nil {
		return adapters.HTTPResponse{}, fmt.Errorf("host callback unavailable")
	}
	type result struct {
		raw json.RawMessage
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := c.Call(pluginabi.MethodHostHTTPDo, pluginapi.HTTPRequest{Method: request.Method, URL: request.URL, Headers: request.Headers, Body: request.Body})
		done <- result{raw: raw, err: err}
	}()
	var raw json.RawMessage
	select {
	case <-ctx.Done():
		return adapters.HTTPResponse{}, ctx.Err()
	case got := <-done:
		if got.err != nil {
			return adapters.HTTPResponse{}, got.err
		}
		raw = got.raw
	}
	var response pluginapi.HTTPResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return adapters.HTTPResponse{}, fmt.Errorf("decode host.http.do: %w", err)
	}
	return adapters.HTTPResponse{StatusCode: response.StatusCode, Headers: response.Headers, Body: response.Body}, nil
}
func (c Client) Log(level, message string, fields map[string]any) {
	_, _ = c.Call(pluginabi.MethodHostLog, map[string]any{"level": level, "message": "[quota-activator] " + message, "fields": fields})
}
