package cases

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/command"
	"github.com/nlink-jp/ir-hub/internal/msg"
	"github.com/nlink-jp/ir-hub/internal/slackapi/slackapitest"
	"github.com/nlink-jp/ir-hub/internal/store"
	"github.com/nlink-jp/ir-hub/internal/userdir"
)

var fixedNow = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T, fake *slackapitest.Fake, cfg Config) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"),
		store.WithClock(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := New(fake, st, cfg, WithClock(func() time.Time { return fixedNow.Add(90 * time.Minute) }))
	return svc, st
}

func defaultCfg() Config {
	return Config{DefaultVisibility: "private", NamePrefix: "ir-"}
}

func TestNewCase(t *testing.T) {
	var gotName string
	var gotPrivate bool
	var kickoff string
	fake := &slackapitest.Fake{
		CreateConversationFn: func(ctx context.Context, name string, private bool) (*slack.Channel, error) {
			gotName, gotPrivate = name, private
			ch := &slack.Channel{}
			ch.ID = "C100"
			ch.Name = name
			return ch, nil
		},
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, err := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			if err != nil {
				t.Fatal(err)
			}
			kickoff = values.Get("text")
			return "1.1", nil
		},
	}
	svc, st := newService(t, fake, defaultCfg())

	res, err := svc.NewCase(context.Background(), NewRequest{
		Title: "DB outage", Severity: "high",
		Visibility: command.VisibilityDefault, OpenedBy: "U001",
	})
	if err != nil {
		t.Fatalf("NewCase: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v", res.Warnings)
	}
	if gotName != "ir-0001-db-outage" {
		t.Errorf("channel name = %q", gotName)
	}
	if !gotPrivate {
		t.Error("default visibility private not applied")
	}
	if res.Case.State != store.StateOpen || res.Case.ChannelID != "C100" {
		t.Errorf("case = %+v", res.Case)
	}
	if !strings.Contains(kickoff, "Case #0001 opened: DB outage") ||
		!strings.Contains(kickoff, "<@U001>") {
		t.Errorf("kickoff = %q", kickoff)
	}
	if !strings.Contains(kickoff, "private") {
		t.Errorf("kickoff should warn about private channel: %q", kickoff)
	}

	// DB agrees.
	got, err := st.CaseByChannel("C100")
	if err != nil {
		t.Fatalf("CaseByChannel: %v", err)
	}
	if got.ChannelName != "ir-0001-db-outage" {
		t.Errorf("stored name = %q", got.ChannelName)
	}
}

func TestNewCaseVisibilityOverride(t *testing.T) {
	var gotPrivate bool
	fake := &slackapitest.Fake{
		CreateConversationFn: func(ctx context.Context, name string, private bool) (*slack.Channel, error) {
			gotPrivate = private
			ch := &slack.Channel{}
			ch.ID = "C1"
			return ch, nil
		},
	}
	svc, _ := newService(t, fake, defaultCfg()) // default private

	_, err := svc.NewCase(context.Background(), NewRequest{
		Title: "x", Severity: "low", Visibility: command.VisibilityPublic, OpenedBy: "U1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPrivate {
		t.Error("--public flag must override private default")
	}
}

func TestNewCaseChannelCreationFails(t *testing.T) {
	fake := &slackapitest.Fake{
		CreateConversationFn: func(ctx context.Context, name string, private bool) (*slack.Channel, error) {
			return nil, fmt.Errorf("name_taken")
		},
	}
	svc, st := newService(t, fake, defaultCfg())

	_, err := svc.NewCase(context.Background(), NewRequest{
		Title: "x", Severity: "low", OpenedBy: "U1",
	})
	if err == nil || !strings.Contains(err.Error(), "name_taken") {
		t.Fatalf("err = %v, want creation failure", err)
	}
	c, err := st.CaseByID(1)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != store.StateFailed {
		t.Errorf("state = %q, want failed", c.State)
	}

	// Sequence keeps counting after a failure (gap accepted).
	fake.CreateConversationFn = nil
	res, err := svc.NewCase(context.Background(), NewRequest{Title: "y", Severity: "low", OpenedBy: "U1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Case.ID != 2 {
		t.Errorf("next case id = %d, want 2", res.Case.ID)
	}
}

func TestNewCaseInviteFailureIsWarning(t *testing.T) {
	fake := &slackapitest.Fake{
		InviteUsersFn: func(ctx context.Context, channelID string, userIDs ...string) error {
			return fmt.Errorf("cant_invite_self")
		},
	}
	svc, _ := newService(t, fake, defaultCfg())
	res, err := svc.NewCase(context.Background(), NewRequest{Title: "x", Severity: "low", OpenedBy: "U1"})
	if err != nil {
		t.Fatalf("invite failure must not fail the case: %v", err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "invite") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	if res.Case.State != store.StateOpen {
		t.Errorf("state = %q, want open", res.Case.State)
	}
}

func TestClose(t *testing.T) {
	fake := &slackapitest.Fake{}
	svc, _ := newService(t, fake, defaultCfg())
	res, _ := svc.NewCase(context.Background(), NewRequest{Title: "x", Severity: "low", OpenedBy: "U1"})

	closed, err := svc.Close(context.Background(), res.Case.ChannelID, "U2")
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.State != store.StateClosed || closed.ClosedBy != "U2" {
		t.Errorf("closed = %+v", closed)
	}

	// Closing again: ErrNotOpen.
	if _, err := svc.Close(context.Background(), res.Case.ChannelID, "U2"); !errors.Is(err, store.ErrNotOpen) {
		t.Errorf("double close err = %v", err)
	}
}

func TestCloseOutsideCaseChannel(t *testing.T) {
	svc, _ := newService(t, &slackapitest.Fake{}, defaultCfg())
	if _, err := svc.Close(context.Background(), "CRANDOM", "U1"); !errors.Is(err, ErrNotCaseChannel) {
		t.Errorf("err = %v, want ErrNotCaseChannel", err)
	}
}

func TestReopen(t *testing.T) {
	fake := &slackapitest.Fake{}
	svc, _ := newService(t, fake, defaultCfg())
	res, _ := svc.NewCase(context.Background(), NewRequest{Title: "x", Severity: "low", OpenedBy: "U1"})
	svc.Close(context.Background(), res.Case.ChannelID, "U2")

	reopened, err := svc.Reopen(context.Background(), res.Case.ChannelID, "U3")
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if reopened.State != store.StateOpen || reopened.ClosedBy != "" || !reopened.ClosedAt.IsZero() {
		t.Errorf("reopened = %+v, want open with cleared closure", reopened)
	}

	// Reopening an open case: ErrNotClosed.
	if _, err := svc.Reopen(context.Background(), res.Case.ChannelID, "U3"); !errors.Is(err, store.ErrNotClosed) {
		t.Errorf("reopen of open err = %v, want ErrNotClosed", err)
	}

	// Outside a case channel.
	if _, err := svc.Reopen(context.Background(), "CRANDOM", "U1"); !errors.Is(err, ErrNotCaseChannel) {
		t.Errorf("err = %v, want ErrNotCaseChannel", err)
	}
}

func TestStatus(t *testing.T) {
	fake := &slackapitest.Fake{}
	svc, st := newService(t, fake, defaultCfg())
	res, _ := svc.NewCase(context.Background(), NewRequest{Title: "DB outage", Severity: "high", OpenedBy: "U1"})

	for i := range 3 {
		st.InsertMessage(store.Message{
			ChannelID: res.Case.ChannelID, TS: fmt.Sprintf("1718000000.%06d", i),
			CaseID: res.Case.ID, Raw: "{}", Source: store.SourceEvent,
		})
	}

	text, err := svc.Status(context.Background(), res.Case.ChannelID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, want := range []string{"Case #0001: DB outage", "`open`", "`high`", "Ingested messages: 3", "Open for: 1h30m"} {
		if !strings.Contains(text, want) {
			t.Errorf("Status missing %q in:\n%s", want, text)
		}
	}

	// No resolver: opener shows as the raw ID.
	if !strings.Contains(text, "by U1") {
		t.Errorf("Status without resolver should show raw ID:\n%s", text)
	}

	if _, err := svc.Status(context.Background(), "CRANDOM"); !errors.Is(err, ErrNotCaseChannel) {
		t.Errorf("err = %v, want ErrNotCaseChannel", err)
	}
}

func TestStatusResolvesUserNames(t *testing.T) {
	fake := &slackapitest.Fake{
		GetUserInfoFn: func(ctx context.Context, id string) (*slack.User, error) {
			return &slack.User{ID: id, RealName: "Alice"}, nil
		},
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"),
		store.WithClock(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := New(fake, st, defaultCfg(),
		WithClock(func() time.Time { return fixedNow.Add(90 * time.Minute) }),
		WithResolver(userdir.New(fake)))
	res, _ := svc.NewCase(context.Background(), NewRequest{Title: "t", Severity: "high", OpenedBy: "U1"})

	text, err := svc.Status(context.Background(), res.Case.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Alice (U1)") {
		t.Errorf("Status should resolve opener to name (ID):\n%s", text)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "less than a minute"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{25*time.Hour + 5*time.Minute, "25h05m"},
	}
	for _, tt := range tests {
		if got := formatDuration(tt.d, &msg.EN); got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
	if got := formatDuration(30*time.Second, &msg.JA); got != "1分未満" {
		t.Errorf("formatDuration(ja) = %q, want 1分未満", got)
	}
}

func TestKickoffJapanese(t *testing.T) {
	var kickoff string
	fake := &slackapitest.Fake{
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, err := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			if err != nil {
				t.Fatal(err)
			}
			kickoff = values.Get("text")
			return "1.1", nil
		},
	}
	svc, _ := newService(t, fake, Config{DefaultVisibility: "private", NamePrefix: "ir-", Msg: &msg.JA})

	_, err := svc.NewCase(context.Background(), NewRequest{
		Title: "情報漏えい", Severity: "high", OpenedBy: "U001",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"案件 #0001 を開設: 情報漏えい", "重要度: `high`", "起票者: <@U001>", "プライベート"} {
		if !strings.Contains(kickoff, want) {
			t.Errorf("kickoff missing %q:\n%s", want, kickoff)
		}
	}
}
