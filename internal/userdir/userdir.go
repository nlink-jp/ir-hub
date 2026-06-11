// Package userdir resolves Slack user IDs to a human-readable
// "display name (ID)" form so that stored postmortems, knowledge
// documents, and status output identify people, not opaque IDs. The
// ID is kept in parentheses for uniqueness and auditing.
//
// Resolution is fail-safe: an API error, a deleted user, or a nil
// resolver all fall back to the raw ID, so records are never lost.
package userdir

import (
	"context"
	"fmt"
	"sync"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/slackapi"
)

// Resolver looks up display names via the Slack API, caching results
// for the process lifetime (profiles rarely change mid-incident).
type Resolver struct {
	api   slackapi.API
	mu    sync.Mutex
	cache map[string]string
}

// New creates a Resolver backed by api.
func New(api slackapi.API) *Resolver {
	return &Resolver{api: api, cache: map[string]string{}}
}

// Resolve returns "display (ID)" for userID, or the raw ID when the
// resolver is nil, the ID is empty, or the lookup fails. Cached.
func (r *Resolver) Resolve(ctx context.Context, userID string) string {
	if r == nil || userID == "" {
		return userID
	}
	r.mu.Lock()
	if v, ok := r.cache[userID]; ok {
		r.mu.Unlock()
		return v
	}
	r.mu.Unlock()

	resolved := userID
	if u, err := r.api.GetUserInfo(ctx, userID); err == nil && u != nil {
		if name := displayName(u); name != "" && name != userID {
			resolved = fmt.Sprintf("%s (%s)", name, userID)
		}
	}

	r.mu.Lock()
	r.cache[userID] = resolved
	r.mu.Unlock()
	return resolved
}

// ResolveAll resolves each ID, preserving order.
func (r *Resolver) ResolveAll(ctx context.Context, ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = r.Resolve(ctx, id)
	}
	return out
}

// displayName picks the most human-friendly name available:
// display_name → real_name → handle. Empty if none are set.
func displayName(u *slack.User) string {
	if u.Profile.DisplayName != "" {
		return u.Profile.DisplayName
	}
	if u.RealName != "" {
		return u.RealName
	}
	return u.Name
}
