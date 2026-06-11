package analysis

import (
	"context"
	"strings"
	"testing"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/llm/llmtest"
	"github.com/nlink-jp/ir-hub/internal/msg"
	"github.com/nlink-jp/ir-hub/internal/slackapi/slackapitest"
	"github.com/nlink-jp/ir-hub/internal/userdir"
)

// resolverNaming returns a resolver whose display name is "name-<ID>".
func resolverNaming(t *testing.T) *userdir.Resolver {
	t.Helper()
	return userdir.New(&slackapitest.Fake{
		GetUserInfoFn: func(ctx context.Context, id string) (*slack.User, error) {
			return &slack.User{ID: id, RealName: "name-" + id}, nil
		},
	})
}

func TestRunPostmortemResolvesParticipants(t *testing.T) {
	st, c := newStoreWithCase(t, "U1 did df -h", "U2 confirmed")
	// Make activity/roles reference the real IDs the conversation uses.
	rules := defaultRules()
	rules[1] = llmtest.Rule{Marker: mActivity, Text: `{"participants":[{"user_name":"@U1","actions":[
		{"timestamp":"1","purpose":"p","method":"m","findings":"f"}]}]}`}
	rules[2] = llmtest.Rule{Marker: mRoles, Text: `{"roles":[{"user_name":"U2","inferred_role":"Lead",
		"confidence":"high","evidence":["e"]}],"relationships":[{"from":"U1","to":"U2","type":"reports_to"}]}`}
	fake := &llmtest.Fake{Rules: rules}
	r := NewRunner(fake, st, Config{Language: "en", BotUserID: "UBOT", MaxInputTokens: 200000},
		WithLogger(t.Logf), WithResolver(resolverNaming(t)))

	rep, err := r.RunPostmortem(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("RunPostmortem: %v", err)
	}

	// Participants resolved.
	for _, p := range rep.Participants {
		if !strings.Contains(p, "name-") || !strings.Contains(p, "(U") {
			t.Errorf("participant not resolved: %q", p)
		}
	}
	// Activity user_name resolved (leading @ stripped).
	if got := rep.Activity.Participants[0].UserName; got != "name-U1 (U1)" {
		t.Errorf("activity user = %q", got)
	}
	// Role user_name resolved.
	if got := rep.Roles.Roles[0].UserName; got != "name-U2 (U2)" {
		t.Errorf("role user = %q", got)
	}
	// Relationship endpoints (ID form) resolved.
	rel := rep.Roles.Relationships[0]
	if string(rel.From) != "name-U1 (U1)" || string(rel.To) != "name-U2 (U2)" {
		t.Errorf("relationship = %s → %s", rel.From, rel.To)
	}

	// Rendered Markdown shows names, no <@ mentions.
	md := RenderMarkdown(rep, &msg.EN)
	if strings.Contains(md, "<@U") {
		t.Errorf("markdown still has <@ mention:\n%s", md)
	}
	if !strings.Contains(md, "name-U1 (U1)") {
		t.Errorf("markdown missing resolved name:\n%s", md)
	}
}

func TestRunPostmortemNoResolverKeepsIDs(t *testing.T) {
	st, c := newStoreWithCase(t, "msg")
	fake := &llmtest.Fake{Rules: defaultRules()}
	r := newRunner(t, fake, st, "en") // no resolver

	rep, err := r.RunPostmortem(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rep.Participants {
		if strings.Contains(p, "(") {
			t.Errorf("participant unexpectedly resolved: %q", p)
		}
	}
}

func TestResolveUserFieldLeavesProseNames(t *testing.T) {
	r := &Runner{resolver: resolverNaming(t)}
	if got := r.resolveUserField(context.Background(), "Alice the responder"); got != "Alice the responder" {
		t.Errorf("prose name changed: %q", got)
	}
	if got := r.resolveUserField(context.Background(), "@U12345AB"); got != "name-U12345AB (U12345AB)" {
		t.Errorf("id form not resolved: %q", got)
	}
}
