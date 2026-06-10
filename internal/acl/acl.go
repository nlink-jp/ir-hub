// Package acl decides whether a Slack user may interact with
// ir-hub. Entries are user IDs and User Group handles; deny lists
// are evaluated before allow lists, and with no allow lists
// configured everything is denied (fail-safe default for a
// workspace where most members are not IR staff).
//
// Group membership is resolved via the Slack API and cached with a
// TTL. Any resolution failure results in a deny — never an
// accidental allow.
package acl

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// groupIDRe matches Slack User Group (subteam) IDs like
// "S0BAEBU39G8". Group entries matching this are treated as IDs
// directly; anything else is resolved as a handle. Handles are
// lowercase by Slack's rules, so the uppercase pattern cannot
// collide.
var groupIDRe = regexp.MustCompile(`^S[A-Z0-9]{6,}$`)

// GroupResolver is the Slack API subset the checker needs.
// slackapi.API satisfies it.
type GroupResolver interface {
	GetUserGroups(ctx context.Context) ([]slack.UserGroup, error)
	GetUserGroupMembers(ctx context.Context, groupID string) ([]string, error)
}

// Config mirrors the [acl] config section, decoupled from the
// config package to avoid an import cycle risk and keep this
// package self-contained.
type Config struct {
	AllowUsers  []string
	AllowGroups []string
	DenyUsers   []string
	DenyGroups  []string
	CacheTTL    time.Duration
}

// Decision is the outcome of an ACL check.
type Decision struct {
	Allowed bool
	Reason  string
}

type cachedMembers struct {
	set     map[string]bool
	expires time.Time
}

// Checker evaluates ACL decisions with cached group membership.
type Checker struct {
	cfg      Config
	resolver GroupResolver
	now      func() time.Time

	mu            sync.Mutex
	handleToID    map[string]string
	handlesExpire time.Time
	members       map[string]cachedMembers // keyed by group ID
}

// Option configures a Checker.
type Option func(*Checker)

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option {
	return func(c *Checker) { c.now = now }
}

// New creates a Checker.
func New(cfg Config, resolver GroupResolver, opts ...Option) *Checker {
	c := &Checker{
		cfg:      cfg,
		resolver: resolver,
		now:      time.Now,
		members:  map[string]cachedMembers{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ValidateGroups resolves every configured group entry (handle or
// S… ID) and returns an error naming the unknown ones. Run at
// startup so config typos fail fast instead of silently denying
// (or never denying).
func (c *Checker) ValidateGroups(ctx context.Context) error {
	entries := map[string]bool{}
	for _, h := range c.cfg.AllowGroups {
		entries[h] = true
	}
	for _, h := range c.cfg.DenyGroups {
		entries[h] = true
	}
	if len(entries) == 0 {
		return nil
	}
	mapping, err := c.handleMap(ctx)
	if err != nil {
		return fmt.Errorf("acl: resolve user groups: %w", err)
	}
	ids := make(map[string]bool, len(mapping))
	for _, id := range mapping {
		ids[id] = true
	}
	var unknown []string
	for e := range entries {
		if groupIDRe.MatchString(e) {
			if !ids[e] {
				unknown = append(unknown, e)
			}
		} else if _, ok := mapping[e]; !ok {
			unknown = append(unknown, e)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("acl: unknown user group handle(s)/ID(s): %s (typo in allow_groups/deny_groups?)",
			strings.Join(unknown, ", "))
	}
	return nil
}

// Check evaluates the ACL for a user. A non-nil error always
// accompanies a denied decision (fail-safe); the caller should log
// it but needs no further branching.
func (c *Checker) Check(ctx context.Context, userID string) (Decision, error) {
	for _, u := range c.cfg.DenyUsers {
		if u == userID {
			return Decision{Allowed: false, Reason: "user in deny_users"}, nil
		}
	}
	for _, h := range c.cfg.DenyGroups {
		member, err := c.isMember(ctx, h, userID)
		if err != nil {
			return Decision{Allowed: false, Reason: "group resolution failed"},
				fmt.Errorf("acl: deny group %q: %w", h, err)
		}
		if member {
			return Decision{Allowed: false, Reason: fmt.Sprintf("user in deny group %q", h)}, nil
		}
	}

	if len(c.cfg.AllowUsers) == 0 && len(c.cfg.AllowGroups) == 0 {
		return Decision{Allowed: false, Reason: "no allow lists configured (deny-all)"}, nil
	}
	for _, u := range c.cfg.AllowUsers {
		if u == userID {
			return Decision{Allowed: true, Reason: "user in allow_users"}, nil
		}
	}
	for _, h := range c.cfg.AllowGroups {
		member, err := c.isMember(ctx, h, userID)
		if err != nil {
			return Decision{Allowed: false, Reason: "group resolution failed"},
				fmt.Errorf("acl: allow group %q: %w", h, err)
		}
		if member {
			return Decision{Allowed: true, Reason: fmt.Sprintf("user in allow group %q", h)}, nil
		}
	}
	return Decision{Allowed: false, Reason: "not in any allow list"}, nil
}

// isMember resolves a group entry — an S… ID is used directly, a
// handle goes through the handle→ID mapping — and checks userID
// against the cached member set.
func (c *Checker) isMember(ctx context.Context, entry, userID string) (bool, error) {
	groupID := entry
	if !groupIDRe.MatchString(entry) {
		mapping, err := c.handleMap(ctx)
		if err != nil {
			return false, err
		}
		id, ok := mapping[entry]
		if !ok {
			return false, fmt.Errorf("user group handle %q not found", entry)
		}
		groupID = id
	}
	set, err := c.memberSet(ctx, groupID)
	if err != nil {
		return false, err
	}
	return set[userID], nil
}

// handleMap returns the handle→group-ID mapping, refreshing the
// cache when expired.
func (c *Checker) handleMap(ctx context.Context) (map[string]string, error) {
	c.mu.Lock()
	if c.handleToID != nil && c.now().Before(c.handlesExpire) {
		m := c.handleToID
		c.mu.Unlock()
		return m, nil
	}
	c.mu.Unlock()

	groups, err := c.resolver.GetUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(groups))
	for _, g := range groups {
		m[g.Handle] = g.ID
	}

	c.mu.Lock()
	c.handleToID = m
	c.handlesExpire = c.now().Add(c.cfg.CacheTTL)
	c.mu.Unlock()
	return m, nil
}

// memberSet returns the member set of a group ID, refreshing the
// cache when expired.
func (c *Checker) memberSet(ctx context.Context, groupID string) (map[string]bool, error) {
	c.mu.Lock()
	if cm, ok := c.members[groupID]; ok && c.now().Before(cm.expires) {
		c.mu.Unlock()
		return cm.set, nil
	}
	c.mu.Unlock()

	users, err := c.resolver.GetUserGroupMembers(ctx, groupID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(users))
	for _, u := range users {
		set[u] = true
	}

	c.mu.Lock()
	c.members[groupID] = cachedMembers{set: set, expires: c.now().Add(c.cfg.CacheTTL)}
	c.mu.Unlock()
	return set, nil
}
