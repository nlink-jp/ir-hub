package acl

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type fakeResolver struct {
	mu           sync.Mutex
	groups       []slack.UserGroup
	members      map[string][]string // group ID → user IDs
	groupsErr    error
	membersErr   error
	groupsCalls  int
	membersCalls int
}

func (f *fakeResolver) GetUserGroups(ctx context.Context) ([]slack.UserGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupsCalls++
	return f.groups, f.groupsErr
}

func (f *fakeResolver) GetUserGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.membersCalls++
	if f.membersErr != nil {
		return nil, f.membersErr
	}
	return f.members[groupID], nil
}

func newFake() *fakeResolver {
	return &fakeResolver{
		groups: []slack.UserGroup{
			{ID: "S-IR", Handle: "ir-team"},
			{ID: "S-BAD", Handle: "contractors"},
		},
		members: map[string][]string{
			"S-IR":  {"U-ALICE", "U-BOB"},
			"S-BAD": {"U-EVE", "U-BOB"},
		},
	}
}

func ck(t *testing.T, c *Checker, user string) (Decision, error) {
	t.Helper()
	return c.Check(context.Background(), user)
}

func TestDenyAllWithoutAllowLists(t *testing.T) {
	c := New(Config{CacheTTL: time.Minute}, newFake())
	d, err := ck(t, c, "U-ANYONE")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Allowed {
		t.Error("empty allow lists must deny all")
	}
	if !strings.Contains(d.Reason, "deny-all") {
		t.Errorf("Reason = %q", d.Reason)
	}
}

func TestAllowUser(t *testing.T) {
	c := New(Config{AllowUsers: []string{"U-ALICE"}, CacheTTL: time.Minute}, newFake())
	d, _ := ck(t, c, "U-ALICE")
	if !d.Allowed {
		t.Errorf("U-ALICE should be allowed: %+v", d)
	}
	d, _ = ck(t, c, "U-MALLORY")
	if d.Allowed {
		t.Errorf("U-MALLORY should be denied: %+v", d)
	}
}

func TestAllowGroup(t *testing.T) {
	c := New(Config{AllowGroups: []string{"ir-team"}, CacheTTL: time.Minute}, newFake())
	d, _ := ck(t, c, "U-BOB")
	if !d.Allowed {
		t.Errorf("U-BOB (ir-team) should be allowed: %+v", d)
	}
	d, _ = ck(t, c, "U-EVE")
	if d.Allowed {
		t.Errorf("U-EVE should be denied: %+v", d)
	}
}

func TestDenyPrecedence(t *testing.T) {
	// U-BOB is in both ir-team (allowed) and contractors (denied):
	// deny wins. U-EVE in deny_users wins over allow_users.
	c := New(Config{
		AllowUsers:  []string{"U-EVE"},
		AllowGroups: []string{"ir-team"},
		DenyUsers:   []string{"U-EVE"},
		DenyGroups:  []string{"contractors"},
		CacheTTL:    time.Minute,
	}, newFake())

	d, _ := ck(t, c, "U-BOB")
	if d.Allowed {
		t.Errorf("deny group must beat allow group: %+v", d)
	}
	d, _ = ck(t, c, "U-EVE")
	if d.Allowed || !strings.Contains(d.Reason, "deny_users") {
		t.Errorf("deny_users must beat allow_users: %+v", d)
	}
	d, _ = ck(t, c, "U-ALICE")
	if !d.Allowed {
		t.Errorf("U-ALICE should remain allowed: %+v", d)
	}
}

func TestResolutionErrorDenies(t *testing.T) {
	f := newFake()
	f.groupsErr = errors.New("slack down")
	c := New(Config{AllowGroups: []string{"ir-team"}, CacheTTL: time.Minute}, f)
	d, err := ck(t, c, "U-BOB")
	if d.Allowed {
		t.Error("resolution error must deny")
	}
	if err == nil {
		t.Error("want error surfaced for logging")
	}
}

func TestMembersErrorDenies(t *testing.T) {
	f := newFake()
	f.membersErr = errors.New("slack down")
	c := New(Config{AllowGroups: []string{"ir-team"}, CacheTTL: time.Minute}, f)
	d, err := ck(t, c, "U-BOB")
	if d.Allowed || err == nil {
		t.Errorf("members error must deny with error, got %+v, %v", d, err)
	}
}

func TestCacheTTL(t *testing.T) {
	f := newFake()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	c := New(Config{AllowGroups: []string{"ir-team"}, CacheTTL: 5 * time.Minute}, f,
		WithClock(func() time.Time { return now }))

	ck(t, c, "U-BOB")
	ck(t, c, "U-ALICE")
	if f.groupsCalls != 1 || f.membersCalls != 1 {
		t.Errorf("within TTL: calls = %d/%d, want 1/1", f.groupsCalls, f.membersCalls)
	}

	now = now.Add(6 * time.Minute) // past TTL
	ck(t, c, "U-BOB")
	if f.groupsCalls != 2 || f.membersCalls != 2 {
		t.Errorf("after TTL: calls = %d/%d, want 2/2", f.groupsCalls, f.membersCalls)
	}
}

func TestValidateGroups(t *testing.T) {
	c := New(Config{AllowGroups: []string{"ir-team"}, DenyGroups: []string{"contractors"}, CacheTTL: time.Minute}, newFake())
	if err := c.ValidateGroups(context.Background()); err != nil {
		t.Errorf("ValidateGroups with known handles: %v", err)
	}

	c = New(Config{AllowGroups: []string{"ir-teem"}, CacheTTL: time.Minute}, newFake())
	err := c.ValidateGroups(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ir-teem") {
		t.Errorf("ValidateGroups with typo: err = %v, want unknown handle error", err)
	}
}

func TestValidateGroupsNoGroupsConfigured(t *testing.T) {
	f := newFake()
	c := New(Config{AllowUsers: []string{"U-ALICE"}, CacheTTL: time.Minute}, f)
	if err := c.ValidateGroups(context.Background()); err != nil {
		t.Errorf("ValidateGroups with no groups: %v", err)
	}
	if f.groupsCalls != 0 {
		t.Errorf("no API call expected, got %d", f.groupsCalls)
	}
}
