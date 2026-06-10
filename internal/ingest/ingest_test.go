package ingest

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/nlink-jp/ir-hub/internal/slackapi/slackapitest"
	"github.com/nlink-jp/ir-hub/internal/store"
)

type fakeSleeper struct {
	mu    sync.Mutex
	slept []time.Duration
}

func (f *fakeSleeper) Sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slept = append(f.slept, d)
	return nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func openCase(t *testing.T, st *store.Store, channelID string) *store.Case {
	t.Helper()
	c, err := st.CreateCase("t", "low", "public", "U1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ActivateCase(c.ID, channelID, fmt.Sprintf("ir-%04d-t", c.ID)); err != nil {
		t.Fatal(err)
	}
	got, err := st.CaseByID(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func msgEvent(channel, ts, text, subtype string) *slackevents.MessageEvent {
	return &slackevents.MessageEvent{
		Type: "message", Channel: channel, TimeStamp: ts,
		User: "U2", Text: text, SubType: subtype,
	}
}

func TestHandleMessageStoresOpenCaseChannel(t *testing.T) {
	st := newStore(t)
	c := openCase(t, st, "C1")
	ing := New(&slackapitest.Fake{}, st)

	if err := ing.HandleMessage(msgEvent("C1", "1718000000.000100", "hello", "")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	n, _ := st.CountMessages(c.ID)
	if n != 1 {
		t.Errorf("messages = %d, want 1", n)
	}
}

func TestHandleMessageIgnoresUnknownChannel(t *testing.T) {
	st := newStore(t)
	openCase(t, st, "C1")
	ing := New(&slackapitest.Fake{}, st)

	if err := ing.HandleMessage(msgEvent("COTHER", "1718000000.000100", "x", "")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	var total int64
	for id := int64(1); id <= 1; id++ {
		n, _ := st.CountMessages(id)
		total += n
	}
	if total != 0 {
		t.Errorf("messages = %d, want 0", total)
	}
}

func TestHandleMessageIgnoresClosedCase(t *testing.T) {
	st := newStore(t)
	c := openCase(t, st, "C1")
	st.CloseCase(c.ID, "U1")
	ing := New(&slackapitest.Fake{}, st)

	ing.HandleMessage(msgEvent("C1", "1718000000.000100", "x", ""))
	n, _ := st.CountMessages(c.ID)
	if n != 0 {
		t.Errorf("messages = %d, want 0 for closed case", n)
	}
}

func TestHandleMessageSubtypeFilter(t *testing.T) {
	st := newStore(t)
	c := openCase(t, st, "C1")
	ing := New(&slackapitest.Fake{}, st)

	stored := []string{"", "bot_message", "file_share", "thread_broadcast"}
	for i, sub := range stored {
		ing.HandleMessage(msgEvent("C1", fmt.Sprintf("1718000000.%06d", i), "x", sub))
	}
	skipped := []string{"message_changed", "message_deleted", "channel_join", "channel_topic"}
	for i, sub := range skipped {
		ing.HandleMessage(msgEvent("C1", fmt.Sprintf("1718000001.%06d", i), "x", sub))
	}

	n, _ := st.CountMessages(c.ID)
	if n != int64(len(stored)) {
		t.Errorf("messages = %d, want %d", n, len(stored))
	}
}

// historyFake serves canned pages keyed by cursor and can inject a
// one-shot rate-limit error.
func historyFake(t *testing.T, pages map[string]*slack.GetConversationHistoryResponse, rateLimitOnce bool) (*slackapitest.Fake, *[]slack.GetConversationHistoryParameters) {
	t.Helper()
	var mu sync.Mutex
	var calls []slack.GetConversationHistoryParameters
	limited := rateLimitOnce
	fake := &slackapitest.Fake{
		GetConversationHistoryFn: func(ctx context.Context, p *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			if limited {
				limited = false
				return nil, &slack.RateLimitedError{RetryAfter: 3 * time.Second}
			}
			calls = append(calls, *p)
			resp, ok := pages[p.ChannelID+"|"+p.Cursor]
			if !ok {
				t.Errorf("unexpected history call %s cursor=%q", p.ChannelID, p.Cursor)
				return &slack.GetConversationHistoryResponse{}, nil
			}
			return resp, nil
		},
	}
	return fake, &calls
}

func histMsg(ts, text string) slack.Message {
	m := slack.Message{}
	m.Timestamp = ts
	m.User = "U2"
	m.Text = text
	return m
}

func page(msgs []slack.Message, next string) *slack.GetConversationHistoryResponse {
	r := &slack.GetConversationHistoryResponse{Messages: msgs, HasMore: next != ""}
	r.ResponseMetaData.NextCursor = next
	return r
}

func TestBackfillPagesAndRateLimit(t *testing.T) {
	st := newStore(t)
	c := openCase(t, st, "C1")
	// Existing message: oldest must start from it.
	st.InsertMessage(store.Message{ChannelID: "C1", TS: "1718000000.000001", CaseID: c.ID, Raw: "{}", Source: store.SourceEvent})

	fake, calls := historyFake(t, map[string]*slack.GetConversationHistoryResponse{
		"C1|":     page([]slack.Message{histMsg("1718000002.000001", "a"), histMsg("1718000001.000001", "b")}, "cur1"),
		"C1|cur1": page([]slack.Message{histMsg("1718000000.000001", "dup")}, ""),
	}, true)
	sleeper := &fakeSleeper{}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	ing := New(fake, st, WithSleeper(sleeper), WithLogger(t.Logf),
		WithClock(func() time.Time { return now }))

	ing.Backfill(context.Background())

	// Rate limit slept once with RetryAfter.
	if len(sleeper.slept) != 1 || sleeper.slept[0] != 3*time.Second {
		t.Errorf("slept = %v, want [3s]", sleeper.slept)
	}
	// Oldest passed through on every page call.
	for _, p := range *calls {
		if p.Oldest != "1718000000.000001" {
			t.Errorf("oldest = %q", p.Oldest)
		}
	}
	// 2 new + 1 dup ignored + 1 pre-existing = 3 rows.
	n, _ := st.CountMessages(c.ID)
	if n != 3 {
		t.Errorf("messages = %d, want 3", n)
	}
}

func TestBackfillPartialFailureContinues(t *testing.T) {
	st := newStore(t)
	c1 := openCase(t, st, "C1")
	c2 := openCase(t, st, "C2")
	_ = c1

	fake := &slackapitest.Fake{
		GetConversationHistoryFn: func(ctx context.Context, p *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
			if p.ChannelID == "C1" {
				return nil, fmt.Errorf("channel_not_found")
			}
			return page([]slack.Message{histMsg("1718000005.000001", "ok")}, ""), nil
		},
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	ing := New(fake, st, WithLogger(t.Logf), WithClock(func() time.Time { return now }))
	ing.Backfill(context.Background())

	n, _ := st.CountMessages(c2.ID)
	if n != 1 {
		t.Errorf("C2 messages = %d, want 1 despite C1 failure", n)
	}
}

func TestBackfillDebounce(t *testing.T) {
	st := newStore(t)
	openCase(t, st, "C1")

	var mu sync.Mutex
	apiCalls := 0
	fake := &slackapitest.Fake{
		GetConversationHistoryFn: func(ctx context.Context, p *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
			mu.Lock()
			apiCalls++
			mu.Unlock()
			return page(nil, ""), nil
		},
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	ing := New(fake, st, WithLogger(t.Logf), WithClock(func() time.Time { return now }))

	ing.Backfill(context.Background())
	if apiCalls != 1 {
		t.Fatalf("apiCalls = %d, want 1", apiCalls)
	}

	// Immediately again: within min interval → skipped.
	ing.Backfill(context.Background())
	if apiCalls != 1 {
		t.Errorf("apiCalls = %d, want still 1 (debounced)", apiCalls)
	}

	// After the interval: runs again.
	now = now.Add(time.Minute)
	ing.Backfill(context.Background())
	if apiCalls != 2 {
		t.Errorf("apiCalls = %d, want 2", apiCalls)
	}
}
