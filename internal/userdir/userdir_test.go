package userdir

import (
	"context"
	"errors"
	"testing"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/slackapi/slackapitest"
)

func TestResolvePrefersDisplayName(t *testing.T) {
	fake := &slackapitest.Fake{
		GetUserInfoFn: func(ctx context.Context, id string) (*slack.User, error) {
			u := &slack.User{ID: id, Name: "handle", RealName: "Real Name"}
			u.Profile.DisplayName = "displayed"
			return u, nil
		},
	}
	r := New(fake)
	if got := r.Resolve(context.Background(), "U1"); got != "displayed (U1)" {
		t.Errorf("got %q, want display_name", got)
	}
}

func TestResolveFallbackChain(t *testing.T) {
	cases := []struct {
		name string
		u    *slack.User
		want string
	}{
		{"real_name when no display", &slack.User{ID: "U1", Name: "handle", RealName: "Real"}, "Real (U1)"},
		{"handle when no real", &slack.User{ID: "U1", Name: "handle"}, "handle (U1)"},
		{"id when nothing", &slack.User{ID: "U1"}, "U1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &slackapitest.Fake{
				GetUserInfoFn: func(ctx context.Context, id string) (*slack.User, error) { return tc.u, nil },
			}
			if got := New(fake).Resolve(context.Background(), "U1"); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveAPIErrorFallsBackToID(t *testing.T) {
	fake := &slackapitest.Fake{
		GetUserInfoFn: func(ctx context.Context, id string) (*slack.User, error) {
			return nil, errors.New("user_not_found")
		},
	}
	if got := New(fake).Resolve(context.Background(), "U9"); got != "U9" {
		t.Errorf("got %q, want raw ID on error", got)
	}
}

func TestResolveCaches(t *testing.T) {
	calls := 0
	fake := &slackapitest.Fake{
		GetUserInfoFn: func(ctx context.Context, id string) (*slack.User, error) {
			calls++
			return &slack.User{ID: id, RealName: "Cached"}, nil
		},
	}
	r := New(fake)
	r.Resolve(context.Background(), "U1")
	r.Resolve(context.Background(), "U1")
	if calls != 1 {
		t.Errorf("API called %d times, want 1 (cached)", calls)
	}
}

func TestResolveNilSafe(t *testing.T) {
	var r *Resolver
	if got := r.Resolve(context.Background(), "U1"); got != "U1" {
		t.Errorf("nil resolver got %q, want raw ID", got)
	}
	if got := r.ResolveAll(context.Background(), []string{"U1", "U2"}); len(got) != 2 || got[0] != "U1" {
		t.Errorf("nil ResolveAll = %v", got)
	}
}

func TestResolveEmptyID(t *testing.T) {
	if got := New(&slackapitest.Fake{}).Resolve(context.Background(), ""); got != "" {
		t.Errorf("empty id got %q", got)
	}
}

func TestResolveAll(t *testing.T) {
	fake := &slackapitest.Fake{
		GetUserInfoFn: func(ctx context.Context, id string) (*slack.User, error) {
			return &slack.User{ID: id, RealName: "N" + id}, nil
		},
	}
	got := New(fake).ResolveAll(context.Background(), []string{"U1", "U2"})
	if len(got) != 2 || got[0] != "NU1 (U1)" || got[1] != "NU2 (U2)" {
		t.Errorf("ResolveAll = %v", got)
	}
}
