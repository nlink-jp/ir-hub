// Package llmtest provides a configurable fake llm.Client for
// analysis-pipeline tests.
package llmtest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/nlink-jp/ir-hub/internal/llm"
)

// Fake implements llm.Client. Responses are selected by the first
// rule whose Marker appears in the request's System prompt; tests
// give each analysis stage a distinct marker. GenerateFn, when set,
// overrides everything.
type Fake struct {
	mu       sync.Mutex
	Requests []llm.Request

	GenerateFn func(ctx context.Context, req llm.Request) (*llm.Response, error)
	Rules      []Rule
}

// Rule maps a system-prompt marker to a canned response or error.
type Rule struct {
	Marker string
	Text   string
	Err    error
}

// Generate records the request and answers via GenerateFn or Rules.
func (f *Fake) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	f.mu.Lock()
	f.Requests = append(f.Requests, req)
	f.mu.Unlock()

	if f.GenerateFn != nil {
		return f.GenerateFn(ctx, req)
	}
	for _, r := range f.Rules {
		if strings.Contains(req.System, r.Marker) {
			if r.Err != nil {
				return nil, r.Err
			}
			return &llm.Response{Text: r.Text}, nil
		}
	}
	return nil, fmt.Errorf("llmtest: no rule matched system prompt %.60q", req.System)
}

// RequestsCopy returns a snapshot of recorded requests.
func (f *Fake) RequestsCopy() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]llm.Request, len(f.Requests))
	copy(out, f.Requests)
	return out
}
