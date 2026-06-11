package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/nlink-jp/ir-hub/internal/acl"
	"github.com/nlink-jp/ir-hub/internal/analysis"
	"github.com/nlink-jp/ir-hub/internal/cases"
	"github.com/nlink-jp/ir-hub/internal/ingest"
	"github.com/nlink-jp/ir-hub/internal/knowledge"
	"github.com/nlink-jp/ir-hub/internal/modal"
	"github.com/nlink-jp/ir-hub/internal/msg"
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

// ---- fake analyzer ----

type fakeAnalyzer struct {
	mu          sync.Mutex
	pmCalls     int
	statusCalls int
	report      *analysis.Report
	pmErr       error
	statusText  string
	statusErr   error

	answerCalls   int
	answerText    string
	answerErr     error
	lastQuestion  string
	lastDocs      []store.KnowledgeDoc
	briefingCalls int
	briefingText  string
	briefingErr   error
	lastSummaries []analysis.KnowledgeSummary
}

func sampleReport(caseID int64) *analysis.Report {
	return &analysis.Report{
		CaseID:  caseID,
		Channel: "#ir-test",
		Summary: analysis.Summary{Title: "DB outage", Severity: "high",
			RootCause: "disk full", Resolution: "expanded", Summary: "An outage."},
		Review: analysis.Review{OverallScore: 7,
			Strengths: analysis.List{"fast detection"}, Improvements: analysis.List{"add runbook"}},
		Tactics: []knowledge.Tactic{{
			Title: "Check disk usage", Purpose: "Find full volumes.", Category: "log-analysis",
			Tools: []string{"df"}, Procedure: "1. df -h", Observations: "100% = full",
			Tags: []string{"disk"}, Confidence: "confirmed", Evidence: "output shared",
		}},
		Participants:     []string{"U-OK"},
		TotalMessages:    3,
		AnalyzedMessages: 3,
		GeneratedAt:      "2026-06-10T12:00:00Z",
	}
}

func (f *fakeAnalyzer) RunPostmortem(ctx context.Context, c *store.Case, progress func(done, total int)) (*analysis.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pmCalls++
	if f.pmErr != nil {
		return nil, f.pmErr
	}
	// Drive the progress callback so the bot's update path is
	// exercised.
	if progress != nil {
		for i := 1; i <= analysis.PostmortemStages; i++ {
			progress(i, analysis.PostmortemStages)
		}
	}
	if f.report != nil {
		return f.report, nil
	}
	return sampleReport(c.ID), nil
}

func (f *fakeAnalyzer) Translate(ctx context.Context, rep *analysis.Report) *analysis.Report {
	return rep
}

func (f *fakeAnalyzer) StatusSummary(ctx context.Context, c *store.Case) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	if f.statusErr != nil {
		return "", f.statusErr
	}
	if f.statusText != "" {
		return f.statusText, nil
	}
	return "*Status*: investigating", nil
}

func (f *fakeAnalyzer) Answer(ctx context.Context, c *store.Case, question string, docs []store.KnowledgeDoc) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answerCalls++
	f.lastQuestion = question
	f.lastDocs = docs
	if f.answerErr != nil {
		return "", f.answerErr
	}
	if f.answerText != "" {
		return f.answerText, nil
	}
	return "based on tac-x, do this", nil
}

func (f *fakeAnalyzer) Briefing(ctx context.Context, title, severity string, summaries []analysis.KnowledgeSummary) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.briefingCalls++
	f.lastSummaries = summaries
	if f.briefingErr != nil {
		return "", f.briefingErr
	}
	if f.briefingText != "" {
		return f.briefingText, nil
	}
	return analysis.NoBriefing, nil
}

// ---- harness ----

type harness struct {
	bot      *Bot
	socket   *fakeSocket
	api      *slackapitest.Fake
	store    *store.Store
	analyzer *fakeAnalyzer
}

func newHarness(t *testing.T, api *slackapitest.Fake, cfg Config) *harness {
	return newHarnessClock(t, api, cfg, time.Time{})
}

// newHarnessClock is newHarness with a fixed store clock (for
// deterministic tactic IDs); a zero clock means default time.Now.
func newHarnessClock(t *testing.T, api *slackapitest.Fake, cfg Config, clock time.Time) *harness {
	t.Helper()
	var storeOpts []store.Option
	if !clock.IsZero() {
		storeOpts = append(storeOpts, store.WithClock(func() time.Time { return clock }))
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"), storeOpts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	checker := acl.New(acl.Config{
		AllowUsers: []string{"U-OK"},
		CacheTTL:   time.Minute,
	}, groupResolver{})
	caseSvc := cases.New(api, st, cases.Config{DefaultVisibility: "private", NamePrefix: "ir-", Msg: cfg.Msg})
	ing := ingest.New(api, st, ingest.WithLogger(t.Logf))
	sock := newFakeSocket()
	az := &fakeAnalyzer{}
	b := New(sock, api, st, checker, caseSvc, ing, az, cfg, WithLogger(t.Logf))
	return &harness{bot: b, socket: sock, api: api, store: st, analyzer: az}
}

// openCaseWithMessages prepares an active case bound to channelID
// with n ingested messages.
func (h *harness) openCaseWithMessages(t *testing.T, channelID string, n int) *store.Case {
	t.Helper()
	c, err := h.store.CreateCase("t", "low", "public", "U-OK")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.ActivateCase(c.ID, channelID, "ir-0001-t"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		h.store.InsertMessage(store.Message{
			ChannelID: channelID, TS: fmt.Sprintf("1718000%03d.000001", i),
			CaseID: c.ID, UserID: "U-OK", Text: "msg", Raw: "{}", Source: store.SourceEvent,
		})
	}
	got, _ := h.store.CaseByID(c.ID)
	return got
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

	// The form was pushed on top of the action picker: the ack must
	// clear the whole stack, not pop back to the picker.
	acks := h.socket.ackList()
	if len(acks) != 1 || len(acks[0].payloads) != 1 {
		t.Fatalf("acks = %+v, want one ack with clear payload", acks)
	}
	resp, ok := acks[0].payloads[0].(*slack.ViewSubmissionResponse)
	if !ok || resp.ResponseAction != slack.RAClear {
		t.Errorf("payload = %+v, want clear response", acks[0].payloads[0])
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

func mentionEvent(envelope, eventID, channel, user, text string) socketmode.Event {
	return socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			Data: &slackevents.EventsAPICallbackEvent{EventID: eventID},
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "app_mention",
				Data: &slackevents.AppMentionEvent{User: user, Channel: channel, Text: text, TimeStamp: "1718000000.000100"},
			},
		},
		Request: &socketmode.Request{EnvelopeID: envelope},
	}
}

func TestMentionRunsQA(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	api := &slackapitest.Fake{
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, err := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			posts = append(posts, values.Get("text"))
			mu.Unlock()
			return "1.1", nil
		},
	}
	h := newHarness(t, api, Config{BotUserID: "UBOT"})
	c := h.openCaseWithMessages(t, "C1", 2)
	seedBotKnowledge(t, h.store, c.ID, "Inspect crontab")
	h.analyzer.answerText = "Per tac-…, inspect the crontab."

	h.bot.handleEvent(context.Background(),
		mentionEvent("e1", "Ev1", "C1", "U-OK", "<@UBOT> how do we find persistence?"))
	h.bot.Wait()

	if h.analyzer.answerCalls != 1 {
		t.Fatalf("answerCalls = %d, want 1", h.analyzer.answerCalls)
	}
	// Mention stripped from the question; knowledge narrowed and passed.
	if h.analyzer.lastQuestion != "how do we find persistence?" {
		t.Errorf("question = %q", h.analyzer.lastQuestion)
	}
	if len(h.analyzer.lastDocs) != 1 {
		t.Errorf("docs passed = %d, want 1", len(h.analyzer.lastDocs))
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, p := range posts {
		if strings.Contains(p, "inspect the crontab") {
			found = true
		}
	}
	if !found {
		t.Errorf("answer not posted: %v", posts)
	}
	// Working reaction added then removed (immediate acknowledgement).
	calls := api.CallNames()
	var added, removed bool
	for _, c := range calls {
		if c == "AddReaction:eyes" {
			added = true
		}
		if c == "RemoveReaction:eyes" {
			removed = true
		}
	}
	if !added || !removed {
		t.Errorf("reaction lifecycle = add %v / remove %v, want both", added, removed)
	}
}

// TestMentionQAFallsBackToAll covers the cross-language case: a
// Japanese question can't LIKE-match English-canonical knowledge,
// so the bot must fall back to loading all knowledge.
func TestMentionQAFallsBackToAll(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{BotUserID: "UBOT"})
	c := h.openCaseWithMessages(t, "C1", 1)
	seedBotKnowledge(t, h.store, c.ID, "Inspect crontab for persistence")
	h.analyzer.answerText = "answer"

	// Japanese question: SearchKnowledge(Fields(...)) won't match.
	h.bot.handleEvent(context.Background(),
		mentionEvent("e1", "Ev1", "C1", "U-OK", "<@UBOT> 前回のサーバー侵害ではどう対応した?"))
	h.bot.Wait()

	if h.analyzer.answerCalls != 1 {
		t.Fatalf("answerCalls = %d, want 1", h.analyzer.answerCalls)
	}
	if len(h.analyzer.lastDocs) != 1 {
		t.Errorf("docs passed = %d, want 1 (fallback to all knowledge)", len(h.analyzer.lastDocs))
	}
}

// TestMentionQAByTacticID checks that a tactic ID embedded in
// non-space-delimited text is extracted and used to fetch exactly
// that document.
func TestMentionQAByTacticID(t *testing.T) {
	fixed := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	api := &slackapitest.Fake{}
	h := newHarnessClock(t, api, Config{BotUserID: "UBOT"}, fixed)
	c := h.openCaseWithMessages(t, "C1", 1)
	// Two tactics so a narrowing must actually pick one.
	runID, _ := h.store.BeginPMRun(c.ID)
	h.store.FinalizePMRun(runID, c.ID, "{}", "# r", 2, func(i int, tacticID string) store.KnowledgeRow {
		title := []string{"Inspect crontab", "Review auth log"}[i]
		return store.KnowledgeRow{TacticID: tacticID, Title: title, Category: "linux-systemd",
			Confidence: "confirmed", TagsJSON: `["x"]`, Summary: "s", DocJSON: "{}", DocMD: "# " + title}
	})
	h.analyzer.answerText = "here it is"

	// Japanese, non-space-delimited, ID embedded.
	h.bot.handleEvent(context.Background(),
		mentionEvent("e1", "Ev1", "C1", "U-OK", "<@UBOT> tac-20260611-002の内容を見せて"))
	h.bot.Wait()

	if len(h.analyzer.lastDocs) != 1 || h.analyzer.lastDocs[0].TacticID != "tac-20260611-002" {
		t.Errorf("docs = %+v, want exactly tac-20260611-002", h.analyzer.lastDocs)
	}
}

func TestMentionEmptyQuestionPrompts(t *testing.T) {
	var mu sync.Mutex
	var ephemerals []string
	api := &slackapitest.Fake{
		PostEphemeralFn: func(ctx context.Context, channelID, userID string, opts ...slack.MsgOption) error {
			_, values, _ := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			mu.Lock()
			ephemerals = append(ephemerals, values.Get("text"))
			mu.Unlock()
			return nil
		},
	}
	h := newHarness(t, api, Config{BotUserID: "UBOT"})

	h.bot.handleEvent(context.Background(),
		mentionEvent("e1", "Ev1", "C1", "U-OK", "<@UBOT>"))
	h.bot.Wait()

	if h.analyzer.answerCalls != 0 {
		t.Errorf("answerCalls = %d, want 0 for empty question", h.analyzer.answerCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ephemerals) != 1 || !strings.Contains(ephemerals[0], "Ask me a question") {
		t.Errorf("ephemerals = %v", ephemerals)
	}
}

var errExport = errors.New("backend down")

// fakeExporter records export calls.
type fakeExporter struct {
	mu        sync.Mutex
	allCalls  int
	caseCalls int
	caseIDs   []int64
	n         int
	err       error
}

func (f *fakeExporter) ExportAll(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allCalls++
	return f.n, f.err
}
func (f *fakeExporter) ExportCase(ctx context.Context, caseID int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caseCalls++
	f.caseIDs = append(f.caseIDs, caseID)
	return f.n, f.err
}
func (f *fakeExporter) Backend() string { return "fake" }

func TestNewCaseTriggersBriefing(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{DefaultVisibility: "private"})
	// Seed prior knowledge so the corpus is non-empty.
	c0 := h.openCaseWithMessages(t, "C0", 1)
	seedBotKnowledge(t, h.store, c0.ID, "Prior tactic")
	h.analyzer.briefingText = ":books: tac-… applies here"

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "new DB outage --severity high"))
	h.bot.Wait()

	if h.analyzer.briefingCalls != 1 {
		t.Errorf("briefingCalls = %d, want 1", h.analyzer.briefingCalls)
	}
}

func TestNewCaseNoBriefingOnEmptyCorpus(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{})

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "new first ever case"))
	h.bot.Wait()

	if h.analyzer.briefingCalls != 0 {
		t.Errorf("briefingCalls = %d, want 0 (empty corpus)", h.analyzer.briefingCalls)
	}
}

func TestBriefingNoneNotPosted(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	api := &slackapitest.Fake{
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, _ := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			mu.Lock()
			posts = append(posts, values.Get("text"))
			mu.Unlock()
			return "1.1", nil
		},
	}
	h := newHarness(t, api, Config{})
	c0 := h.openCaseWithMessages(t, "C0", 1)
	seedBotKnowledge(t, h.store, c0.ID, "Prior tactic")
	h.analyzer.briefingText = "NONE"

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "new unrelated case"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	for _, p := range posts {
		if strings.Contains(p, "Related past knowledge") {
			t.Errorf("NONE briefing was posted: %q", p)
		}
	}
}

func TestExportCommand(t *testing.T) {
	var mu sync.Mutex
	var responses []string
	api := &slackapitest.Fake{
		PostResponseFn: func(ctx context.Context, url, text string) error {
			mu.Lock()
			responses = append(responses, text)
			mu.Unlock()
			return nil
		},
	}
	exp := &fakeExporter{n: 5}
	h := newHarness(t, api, Config{})
	h.bot.export = exp

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "export"))
	h.bot.Wait()

	if exp.allCalls != 1 {
		t.Errorf("ExportAll calls = %d, want 1", exp.allCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	gotStart, gotDone := false, false
	for _, r := range responses {
		if strings.Contains(r, "Exporting knowledge") {
			gotStart = true
		}
		if strings.Contains(r, "Exported 5") {
			gotDone = true
		}
	}
	if !gotStart || !gotDone {
		t.Errorf("responses = %v", responses)
	}
}

func TestExportNotConfigured(t *testing.T) {
	var mu sync.Mutex
	var responses []string
	api := &slackapitest.Fake{
		PostResponseFn: func(ctx context.Context, url, text string) error {
			mu.Lock()
			responses = append(responses, text)
			mu.Unlock()
			return nil
		},
	}
	h := newHarness(t, api, Config{}) // no export wired

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "export"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(responses) != 1 || !strings.Contains(responses[0], "not configured") {
		t.Errorf("responses = %v", responses)
	}
}

func TestPMAutoExports(t *testing.T) {
	exp := &fakeExporter{n: 2}
	h := newHarness(t, &slackapitest.Fake{}, Config{})
	h.bot.export = exp
	c := h.openCaseWithMessages(t, "C1", 2)

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()

	if exp.caseCalls != 1 || len(exp.caseIDs) != 1 || exp.caseIDs[0] != c.ID {
		t.Errorf("ExportCase calls = %d, ids = %v, want 1 call for case %d", exp.caseCalls, exp.caseIDs, c.ID)
	}
}

func TestPMAutoExportFailureDoesNotFailPM(t *testing.T) {
	exp := &fakeExporter{err: errExport}
	h := newHarness(t, &slackapitest.Fake{}, Config{})
	h.bot.export = exp
	c := h.openCaseWithMessages(t, "C1", 2)

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()

	// PM still finalized despite export failure.
	run, _ := h.store.LatestPMRun(c.ID)
	if run.Status != "done" {
		t.Errorf("run status = %q, want done despite export failure", run.Status)
	}
}

func TestStripMentions(t *testing.T) {
	tests := map[string]string{
		"<@UBOT> hello":               "hello",
		"  <@UBOT>   spaced  ":        "spaced",
		"<@UBOT> <@U2> multi mention": "multi mention",
		"<@UBOT|ir-hub> labeled":      "labeled",
		"<@UBOT>":                     "",
		"no mention at all":           "no mention at all",
	}
	for in, want := range tests {
		if got := stripMentions(in, "UBOT"); got != want {
			t.Errorf("stripMentions(%q) = %q, want %q", in, got, want)
		}
	}
}

// seedBotKnowledge inserts a knowledge row for tests via a finalized
// PM run.
func seedBotKnowledge(t *testing.T, st *store.Store, caseID int64, title string) {
	t.Helper()
	runID, err := st.BeginPMRun(caseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinalizePMRun(runID, caseID, "{}", "# r", 1,
		func(i int, tacticID string) store.KnowledgeRow {
			return store.KnowledgeRow{TacticID: tacticID, Title: title, Category: "linux-systemd",
				Confidence: "confirmed", TagsJSON: `["persistence"]`, Summary: "finds persistence",
				DocJSON: "{}", DocMD: "# " + title}
		}); err != nil {
		t.Fatal(err)
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

func TestSlashParseErrorJapanese(t *testing.T) {
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
	h := newHarness(t, api, Config{Msg: &msg.JA})

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CORIGIN", "destroy everything"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(responses) != 1 || !strings.Contains(responses[0], "未知のサブコマンド") {
		t.Errorf("responses = %v, want Japanese parse error", responses)
	}
}

// ---- postmortem flow ----

func TestPMCommandRunsPostmortem(t *testing.T) {
	var mu sync.Mutex
	var uploads []slack.UploadFileParameters
	api := &slackapitest.Fake{
		UploadFileFn: func(ctx context.Context, p slack.UploadFileParameters) (*slack.FileSummary, error) {
			mu.Lock()
			defer mu.Unlock()
			uploads = append(uploads, p)
			return &slack.FileSummary{ID: "F1"}, nil
		},
	}
	h := newHarness(t, api, Config{})
	c := h.openCaseWithMessages(t, "C1", 3)

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()

	if h.analyzer.pmCalls != 1 {
		t.Fatalf("pmCalls = %d, want 1", h.analyzer.pmCalls)
	}
	// Run recorded as done with the canonical report.
	run, err := h.store.LatestPMRun(c.ID)
	if err != nil || run.Status != "done" {
		t.Fatalf("run = %+v, err %v", run, err)
	}
	if !strings.Contains(run.ReportMD, "# Postmortem: Case #0001") {
		t.Errorf("stored report MD = %.80q", run.ReportMD)
	}
	// Knowledge replaced with allocated IDs.
	rows, _ := h.store.KnowledgeByCase(c.ID)
	if len(rows) != 1 || !strings.HasPrefix(rows[0].TacticID, "tac-") {
		t.Fatalf("knowledge = %+v", rows)
	}
	if rows[0].Title != "Check disk usage" || rows[0].Confidence != "confirmed" {
		t.Errorf("knowledge row = %+v", rows[0])
	}
	// Snippet uploaded with compact summary as the comment.
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(uploads))
	}
	up := uploads[0]
	if up.Channel != "C1" || up.Filename != "ir-0001-postmortem.md" || up.SnippetType != "markdown" {
		t.Errorf("upload params = %+v", up)
	}
	if !strings.Contains(up.InitialComment, "Postmortem: Case #0001") ||
		!strings.Contains(up.InitialComment, "Process score: 7/10") {
		t.Errorf("compact = %q", up.InitialComment)
	}
}

func TestPMPostsProgressUpdates(t *testing.T) {
	var mu sync.Mutex
	var updates []string
	api := &slackapitest.Fake{
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			return "1718000000.000200", nil // progress message ts
		},
		UpdateMessageFn: func(ctx context.Context, channelID, ts string, opts ...slack.MsgOption) error {
			_, values, _ := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			mu.Lock()
			updates = append(updates, values.Get("text"))
			mu.Unlock()
			return nil
		},
	}
	h := newHarness(t, api, Config{})
	h.openCaseWithMessages(t, "C1", 2)

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	// The fake analyzer drives 5 progress ticks; the bot edits the
	// progress message and the final update shows all stages done.
	if len(updates) == 0 {
		t.Fatal("no progress updates posted")
	}
	last := updates[len(updates)-1]
	if !strings.Contains(last, "5/5") {
		t.Errorf("final progress = %q, want 5/5", last)
	}
}

func TestCloseTriggersPostmortem(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{})
	h.openCaseWithMessages(t, "C1", 2)

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "close"))
	h.bot.Wait()

	if h.analyzer.pmCalls != 1 {
		t.Errorf("pmCalls after close = %d, want 1 (auto postmortem)", h.analyzer.pmCalls)
	}
}

func TestReopenCommand(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	api := &slackapitest.Fake{
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, _ := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			mu.Lock()
			posts = append(posts, values.Get("text"))
			mu.Unlock()
			return "1.1", nil
		},
	}
	h := newHarness(t, api, Config{})
	c := h.openCaseWithMessages(t, "C1", 2)
	if err := h.store.CloseCase(c.ID, "U-OK"); err != nil {
		t.Fatal(err)
	}

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "reopen"))
	h.bot.Wait()

	got, _ := h.store.CaseByID(c.ID)
	if got.State != store.StateOpen {
		t.Errorf("state = %q, want open after reopen", got.State)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, p := range posts {
		if strings.Contains(p, "reopened") {
			found = true
		}
	}
	if !found {
		t.Errorf("reopen note not posted: %v", posts)
	}
}

func TestReopenNotClosed(t *testing.T) {
	var mu sync.Mutex
	var responses []string
	api := &slackapitest.Fake{
		PostResponseFn: func(ctx context.Context, url, text string) error {
			mu.Lock()
			responses = append(responses, text)
			mu.Unlock()
			return nil
		},
	}
	h := newHarness(t, api, Config{})
	h.openCaseWithMessages(t, "C1", 1) // open, not closed

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "reopen"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(responses) != 1 || !strings.Contains(responses[0], "not closed") {
		t.Errorf("responses = %v, want not-closed error", responses)
	}
}

func TestPMFailureRecordedAndPosted(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	api := &slackapitest.Fake{
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, err := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			posts = append(posts, values.Get("text"))
			mu.Unlock()
			return "1.1", nil
		},
	}
	h := newHarness(t, api, Config{})
	c := h.openCaseWithMessages(t, "C1", 2)
	h.analyzer.pmErr = fmt.Errorf("stage summary: boom")

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()

	run, _ := h.store.LatestPMRun(c.ID)
	if run.Status != "failed" || !strings.Contains(run.Error, "boom") {
		t.Errorf("run = %+v", run)
	}
	mu.Lock()
	defer mu.Unlock()
	failPosted := false
	for _, p := range posts {
		if strings.Contains(p, "postmortem failed") {
			failPosted = true
		}
	}
	if !failPosted {
		t.Errorf("failure not posted: %v", posts)
	}
}

// pmGuardHarness wires a response recorder; Wait() is terminal
// (draining), so each guard scenario gets its own harness.
func pmGuardHarness(t *testing.T) (*harness, func() []string) {
	t.Helper()
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
	return h, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(responses))
		copy(out, responses)
		return out
	}
}

func TestPMGuardNotCaseChannel(t *testing.T) {
	h, responses := pmGuardHarness(t)
	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "CRANDOM", "pm"))
	h.bot.Wait()
	got := responses()
	if len(got) != 1 || !strings.Contains(got[0], "not an ir-hub case channel") {
		t.Errorf("responses = %v", got)
	}
	if h.analyzer.pmCalls != 0 {
		t.Errorf("pmCalls = %d, want 0", h.analyzer.pmCalls)
	}
}

func TestPMGuardNoMessages(t *testing.T) {
	h, responses := pmGuardHarness(t)
	h.openCaseWithMessages(t, "C1", 0)
	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()
	got := responses()
	if len(got) != 1 || !strings.Contains(got[0], "no ingested messages") {
		t.Errorf("responses = %v", got)
	}
	if h.analyzer.pmCalls != 0 {
		t.Errorf("pmCalls = %d, want 0", h.analyzer.pmCalls)
	}
}

func TestPMGuardAlreadyRunning(t *testing.T) {
	h, responses := pmGuardHarness(t)
	c := h.openCaseWithMessages(t, "C1", 1)
	if _, err := h.store.BeginPMRun(c.ID); err != nil {
		t.Fatal(err)
	}
	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()
	got := responses()
	if len(got) != 1 || !strings.Contains(got[0], "already in progress") {
		t.Errorf("responses = %v", got)
	}
	if h.analyzer.pmCalls != 0 {
		t.Errorf("pmCalls = %d, want 0", h.analyzer.pmCalls)
	}
}

func TestPMUploadFailureFallsBackToPost(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	api := &slackapitest.Fake{
		UploadFileFn: func(ctx context.Context, p slack.UploadFileParameters) (*slack.FileSummary, error) {
			return nil, fmt.Errorf("not_in_channel")
		},
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, err := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			posts = append(posts, values.Get("text"))
			mu.Unlock()
			return "1.1", nil
		},
	}
	h := newHarness(t, api, Config{})
	c := h.openCaseWithMessages(t, "C1", 2)

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "pm"))
	h.bot.Wait()

	// Finalize still succeeded — report and knowledge survive.
	run, _ := h.store.LatestPMRun(c.ID)
	if run.Status != "done" {
		t.Errorf("run status = %q, want done despite upload failure", run.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, p := range posts {
		if strings.Contains(p, "Postmortem: Case #0001") && strings.Contains(p, "could not attach") {
			found = true
		}
	}
	if !found {
		t.Errorf("fallback post missing: %v", posts)
	}
}

func TestModalPMAction(t *testing.T) {
	api := &slackapitest.Fake{}
	h := newHarness(t, api, Config{})
	h.openCaseWithMessages(t, "C1", 2)

	h.bot.handleEvent(context.Background(), submissionEvent("e1", "U-OK",
		modal.CallbackAction, metaJSON("C1", "U-OK"),
		map[string]map[string]slack.BlockAction{"action": {"value": selected("pm")}}))
	h.bot.Wait()

	if h.analyzer.pmCalls != 1 {
		t.Errorf("pmCalls = %d, want 1 via modal", h.analyzer.pmCalls)
	}
}

func TestStatusPostsLLMSummary(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	api := &slackapitest.Fake{
		PostMessageFn: func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
			_, values, err := slack.UnsafeApplyMsgOptions("tok", channelID, "https://slack.test/api/", opts...)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			posts = append(posts, values.Get("text"))
			mu.Unlock()
			return "1.1", nil
		},
	}
	h := newHarness(t, api, Config{})
	h.openCaseWithMessages(t, "C1", 2)
	h.analyzer.statusText = "*Status*: contained, monitoring"

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "status"))
	h.bot.Wait()

	if h.analyzer.statusCalls != 1 {
		t.Fatalf("statusCalls = %d, want 1", h.analyzer.statusCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, p := range posts {
		if strings.Contains(p, "contained, monitoring") {
			found = true
		}
	}
	if !found {
		t.Errorf("LLM summary not posted: %v", posts)
	}
}

func TestStatusLLMFailureKeepsMetadata(t *testing.T) {
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
	h.openCaseWithMessages(t, "C1", 2)
	h.analyzer.statusErr = fmt.Errorf("llm down")

	h.bot.handleEvent(context.Background(), slashEvent("e1", "U-OK", "C1", "status"))
	h.bot.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(responses) < 2 {
		t.Fatalf("responses = %v, want metadata + failure note", responses)
	}
	if !strings.Contains(responses[0], "Case #0001") {
		t.Errorf("metadata reply missing: %q", responses[0])
	}
	if !strings.Contains(responses[1], "situation summary failed") {
		t.Errorf("failure note missing: %q", responses[1])
	}
}

func TestFirstWord(t *testing.T) {
	for in, want := range map[string]string{"new DB outage": "new", "status": "status", "": ""} {
		if got := firstWord(in); got != want {
			t.Errorf("firstWord(%q) = %q, want %q", in, got, want)
		}
	}
}
