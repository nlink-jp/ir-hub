package bot

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/nlink-jp/ir-hub/internal/acl"
	"github.com/nlink-jp/ir-hub/internal/cases"
	"github.com/nlink-jp/ir-hub/internal/ingest"
	"github.com/nlink-jp/ir-hub/internal/modal"
	"github.com/nlink-jp/ir-hub/internal/slackapi/slackapitest"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// ---- fakes ----

type ack struct {
	req      socketmode.Request
	payloads []any
}

type fakeSocket struct {
	mu     sync.Mutex
	events chan socketmode.Event
	acks   []ack
}

func newFakeSocket() *fakeSocket {
	return &fakeSocket{events: make(chan socketmode.Event, 16)}
}

func (f *fakeSocket) Events() <-chan socketmode.Event { return f.events }

func (f *fakeSocket) Ack(req socketmode.Request, payload ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, ack{req: req, payloads: payload})
}

func (f *fakeSocket) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeSocket) ackList() []ack {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ack, len(f.acks))
	copy(out, f.acks)
	return out
}

type groupResolver struct{}

func (groupResolver) GetUserGroups(ctx context.Context) ([]slack.UserGroup, error) {
	return nil, nil
}
func (groupResolver) GetUserGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	return nil, nil
}

// ---- harness ----

type harness struct {
	bot    *Bot
	socket *fakeSocket
	api    *slackapitest.Fake
	store  *store.Store
}

func newHarness(t *testing.T, api *slackapitest.Fake, cfg Config) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	checker := acl.New(acl.Config{
		AllowUsers: []string{"U-OK"},
		CacheTTL:   time.Minute,
	}, groupResolver{})
	caseSvc := cases.New(api, st, cases.Config{DefaultVisibility: "private", NamePrefix: "ir-"})
	ing := ingest.New(api, st, ingest.WithLogger(t.Logf))
	sock := newFakeSocket()
	b := New(sock, api, st, checker, caseSvc, ing, cfg, WithLogger(t.Logf))
	return &harness{bot: b, socket: sock, api: api, store: st}
}

func slashEvent(envelope, user, channel, text string) socketmode.Event {
	return socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: slack.SlashCommand{
			Command: "/ir-hub", Text: text,
			UserID: user, ChannelID: channel,
			TriggerID: "trig-1", ResponseURL: "https://hooks.slack.test/r1",
		},
		Request: &socketmode.Request{EnvelopeID: envelope},
	}
}

func messageEvent(envelope, eventID, channel, ts, text string) socketmode.Event {
	return socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			Data: &slackevents.EventsAPICallbackEvent{EventID: eventID},
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "message",
				Data: &slackevents.MessageEvent{
					Type: "message", Channel: channel, TimeStamp: ts, User: "U-OK", Text: text,
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: envelope},
	}
}

func submissionEvent(envelope, user, callbackID, privateMetadata string, values map[string]map[string]slack.BlockAction) socketmode.Event {
	cb := slack.InteractionCallback{Type: slack.InteractionTypeViewSubmission}
	cb.User.ID = user
	cb.View.CallbackID = callbackID
	cb.View.PrivateMetadata = privateMetadata
	cb.View.State = &slack.ViewState{Values: values}
	return socketmode.Event{
		Type:    socketmode.EventTypeInteractive,
		Data:    cb,
		Request: &socketmode.Request{EnvelopeID: envelope},
	}
}

func metaJSON(channelID, userID string) string {
	b, _ := json.Marshal(modal.Metadata{ChannelID: channelID, UserID: userID})
	return string(b)
}

func selected(value string) slack.BlockAction {
	var a slack.BlockAction
	a.SelectedOption.Value = value
	return a
}

func typed(value string) slack.BlockAction {
	var a slack.BlockAction
	a.Value = value
	return a
}

// ---- tests ----

func TestSlashNewCreatesCaseAndAcksFirst(t *testing.T) {
	var mu sync.Mutex
	var responses []string
	api := &slackapitest.Fake{
		PostResponseFn: func(ctx context.Context, url, text string) error {
			mu.Lock()
			defer mu.Unlock()
			responses = append(responses, text)
			return nil
		},
	}
	h := newHarness(t, api, Config{DefaultVisibility: "private"})

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "new DB outage --severity high"))

	// Ack happened synchronously, with no payload, before the work.
	acks := h.socket.ackList()
	if len(acks) != 1 || len(acks[0].payloads) != 0 {
		t.Fatalf("acks = %+v, want one empty ack", acks)
	}

	h.bot.Wait()

	c, err := h.store.CaseByID(1)
	if err != nil {
		t.Fatalf("case not created: %v", err)
	}
	if c.State != store.StateOpen || c.Severity != "high" {
		t.Errorf("case = %+v", c)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(responses) != 1 || !strings.Contains(responses[0], "Case #0001 opened") {
		t.Errorf("responses = %v", responses)
	}
}

func TestSlashDeniedSilentWithAudit(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{})

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-BAD", "CORIGIN", "new x"))
	h.bot.Wait()

	// Silent: ack only, no API activity.
	if calls := api.CallNames(); len(calls) != 0 {
		t.Errorf("api calls = %v, want none", calls)
	}
	if _, err := h.store.CaseByID(1); err == nil {
		t.Error("case must not be created")
	}
	if n := storeQueryDenials(t, h.store); n != 1 {
		t.Errorf("denials = %d, want 1", n)
	}
}

func storeQueryDenials(t *testing.T, st *store.Store) int {
	t.Helper()
	n, err := st.CountDenials()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSlashDeniedNotifyOptIn(t *testing.T) {
	var mu sync.Mutex
	var notified []string
	api := &slackapitest.Fake{
		PostResponseFn: func(ctx context.Context, url, text string) error {
			mu.Lock()
			defer mu.Unlock()
			notified = append(notified, text)
			return nil
		},
	}
	h := newHarness(t, api, Config{NotifyDenied: true})

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-BAD", "CORIGIN", "status"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(notified) != 1 || !strings.Contains(notified[0], "not authorized") {
		t.Errorf("notified = %v", notified)
	}
}

func TestSlashEmptyTextOpensModal(t *testing.T) {
	var mu sync.Mutex
	var gotTrigger string
	var gotView slack.ModalViewRequest
	api := &slackapitest.Fake{
		OpenViewFn: func(ctx context.Context, triggerID string, view slack.ModalViewRequest) error {
			mu.Lock()
			defer mu.Unlock()
			gotTrigger, gotView = triggerID, view
			return nil
		},
	}
	h := newHarness(t, api, Config{})

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", ""))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if gotTrigger != "trig-1" {
		t.Errorf("trigger = %q", gotTrigger)
	}
	if gotView.CallbackID != modal.CallbackAction {
		t.Errorf("callback = %q", gotView.CallbackID)
	}
}

func TestSlashParseErrorReported(t *testing.T) {
	var mu sync.Mutex
	var responses []string
	api := &slackapitest.Fake{
		PostResponseFn: func(ctx context.Context, url, text string) error {
			mu.Lock()
			defer mu.Unlock()
			responses = append(responses, text)
			return nil
		},
	}
	h := newHarness(t, api, Config{})

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "destroy everything"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(responses) != 1 || !strings.Contains(responses[0], "unknown subcommand") {
		t.Errorf("responses = %v", responses)
	}
}

func TestActionPickerNewPushesForm(t *testing.T) {
	h := newHarness(t, &slackapitest.Fake{}, Config{DefaultVisibility: "private"})

	h.bot.handleEvent(context.Background(), submissionEvent("e1", "U-OK",
		modal.CallbackAction, metaJSON("CORIGIN", "U-OK"),
		map[string]map[string]slack.BlockAction{"action": {"value": selected("new")}}))
	h.bot.Wait()

	acks := h.socket.ackList()
	if len(acks) != 1 || len(acks[0].payloads) != 1 {
		t.Fatalf("acks = %+v, want one ack with payload", acks)
	}
	resp, ok := acks[0].payloads[0].(*slack.ViewSubmissionResponse)
	if !ok {
		t.Fatalf("payload type = %T", acks[0].payloads[0])
	}
	if resp.ResponseAction != slack.RAPush {
		t.Errorf("response_action = %q, want push", resp.ResponseAction)
	}
	if resp.View == nil || resp.View.CallbackID != modal.CallbackNew {
		t.Errorf("pushed view = %+v, want new-case form", resp.View)
	}
}

func TestNewCaseSubmissionEmptyTitleShowsErrors(t *testing.T) {
	h := newHarness(t, &slackapitest.Fake{}, Config{})

	h.bot.handleEvent(context.Background(), submissionEvent("e1", "U-OK",
		modal.CallbackNew, metaJSON("CORIGIN", "U-OK"),
		map[string]map[string]slack.BlockAction{
			"title":      {"value": typed("   ")},
			"severity":   {"value": selected("low")},
			"visibility": {"value": selected("private")},
		}))
	h.bot.Wait()

	acks := h.socket.ackList()
	if len(acks) != 1 || len(acks[0].payloads) != 1 {
		t.Fatalf("acks = %+v", acks)
	}
	resp, ok := acks[0].payloads[0].(*slack.ViewSubmissionResponse)
	if !ok || resp.ResponseAction != slack.RAErrors {
		t.Fatalf("payload = %+v", acks[0].payloads[0])
	}
}

func TestNewCaseSubmissionCreatesCase(t *testing.T) {
	h := newHarness(t, &slackapitest.Fake{}, Config{})

	h.bot.handleEvent(context.Background(), submissionEvent("e1", "U-OK",
		modal.CallbackNew, metaJSON("CORIGIN", "U-OK"),
		map[string]map[string]slack.BlockAction{
			"title":      {"value": typed("Phishing wave")},
			"severity":   {"value": selected("high")},
			"visibility": {"value": selected("public")},
		}))
	h.bot.Wait()

	c, err := h.store.CaseByID(1)
	if err != nil {
		t.Fatalf("case not created: %v", err)
	}
	if c.Title != "Phishing wave" || c.Visibility != "public" {
		t.Errorf("case = %+v", c)
	}
}

func TestSubmissionDeniedClosesSilently(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{})

	h.bot.handleEvent(context.Background(), submissionEvent("e1", "U-BAD",
		modal.CallbackNew, metaJSON("CORIGIN", "U-BAD"),
		map[string]map[string]slack.BlockAction{"title": {"value": typed("x")}}))
	h.bot.Wait()

	acks := h.socket.ackList()
	if len(acks) != 1 || len(acks[0].payloads) != 0 {
		t.Fatalf("acks = %+v, want one empty ack (modal closes)", acks)
	}
	if calls := api.CallNames(); len(calls) != 0 {
		t.Errorf("api calls = %v, want none", calls)
	}
	if n := storeQueryDenials(t, h.store); n != 1 {
		t.Errorf("denials = %d, want 1", n)
	}
}

func TestEventsAPIMessageIngestedWithDedup(t *testing.T) {
	h := newHarness(t, &slackapitest.Fake{}, Config{})

	// Open a case so the channel is ingestable.
	c, _ := h.store.CreateCase("t", "low", "public", "U-OK")
	h.store.ActivateCase(c.ID, "C1", "ir-0001-t")

	h.bot.handleEvent(context.Background(), messageEvent("e1", "Ev1", "C1", "1718000000.000100", "hello"))
	// Redelivery: same event_id, different envelope.
	h.bot.handleEvent(context.Background(), messageEvent("e2", "Ev1", "C1", "1718000000.000100", "hello"))
	h.bot.Wait()

	if len(h.socket.ackList()) != 2 {
		t.Errorf("acks = %d, want 2 (every envelope acked)", len(h.socket.ackList()))
	}
	n, _ := h.store.CountMessages(c.ID)
	if n != 1 {
		t.Errorf("messages = %d, want 1 (deduped)", n)
	}
}

func TestMentionAllowedGetsReply(t *testing.T) {
	var mu sync.Mutex
	var ephemerals []string
	api := &slackapitest.Fake{
		PostEphemeralFn: func(ctx context.Context, channelID, userID string, opts ...slack.MsgOption) error {
			mu.Lock()
			defer mu.Unlock()
			ephemerals = append(ephemerals, channelID+":"+userID)
			return nil
		},
	}
	h := newHarness(t, api, Config{})

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			Data: &slackevents.EventsAPICallbackEvent{EventID: "Ev2"},
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "app_mention",
				Data: &slackevents.AppMentionEvent{User: "U-OK", Channel: "C1"},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "e1"},
	}
	h.bot.handleEvent(context.Background(), evt)
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(ephemerals) != 1 || ephemerals[0] != "C1:U-OK" {
		t.Errorf("ephemerals = %v", ephemerals)
	}
}

func TestMentionDeniedAudited(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{})

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			Data: &slackevents.EventsAPICallbackEvent{EventID: "Ev3"},
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "app_mention",
				Data: &slackevents.AppMentionEvent{User: "U-BAD", Channel: "C1"},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "e1"},
	}
	h.bot.handleEvent(context.Background(), evt)
	h.bot.Wait()

	if calls := api.CallNames(); len(calls) != 0 {
		t.Errorf("api calls = %v, want none", calls)
	}
	if n := storeQueryDenials(t, h.store); n != 1 {
		t.Errorf("denials = %d, want 1", n)
	}
}

func TestRunLoopAndShutdown(t *testing.T) {
	h := newHarness(t, &slackapitest.Fake{}, Config{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- h.bot.Run(ctx) }()

	h.socket.events <- slashEvent("e1", "U-OK", "CORIGIN", "new via run loop")

	// Wait until the event is acked (loop is alive).
	deadline := time.After(2 * time.Second)
	for len(h.socket.ackList()) == 0 {
		select {
		case <-deadline:
			t.Fatal("event was not acked via Run loop")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	if err := <-done; err == nil {
		t.Error("Run should return ctx error")
	}
	h.bot.Wait()

	if _, err := h.store.CaseByID(1); err != nil {
		t.Errorf("case from run-loop event missing: %v", err)
	}
}

func TestFirstWord(t *testing.T) {
	for in, want := range map[string]string{"new DB outage": "new", "status": "status", "": ""} {
		if got := firstWord(in); got != want {
			t.Errorf("firstWord(%q) = %q, want %q", in, got, want)
		}
	}
}
