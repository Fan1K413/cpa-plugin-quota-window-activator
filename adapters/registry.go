package adapters

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/cpa-plugins/quota-window-activator/core"
)

type HTTPResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type HTTPClient interface {
	Do(context.Context, core.Credential, core.ActivationRequest) (HTTPResponse, error)
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]core.Adapter
}

func NewRegistry(items ...core.Adapter) (*Registry, error) {
	r := &Registry{adapters: map[string]core.Adapter{}}
	for _, item := range items {
		if err := r.Register(item); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(adapter core.Adapter) error {
	if adapter == nil || adapter.ID() == "" {
		return errors.New("adapter ID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[adapter.ID()]; exists {
		return errors.New("duplicate adapter: " + adapter.ID())
	}
	r.adapters[adapter.ID()] = adapter
	return nil
}

func (r *Registry) Match(credential core.Credential) core.Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.adapters))
	for id := range r.adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if r.adapters[id].Match(credential) {
			return r.adapters[id]
		}
	}
	return nil
}

func (r *Registry) List() []core.Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.adapters))
	for id := range r.adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]core.Adapter, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.adapters[id])
	}
	return out
}

type Sender struct{ Client HTTPClient }

func (s Sender) Send(ctx context.Context, credential core.Credential, request core.ActivationRequest) error {
	if s.Client == nil {
		return errors.New("host HTTP client is unavailable")
	}
	response, err := s.Client.Do(ctx, credential, request)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("activation upstream returned non-success status")
	}
	return nil
}
