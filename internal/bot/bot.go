// Package bot runs the Socket Mode event loop: it acks every
// envelope within Slack's 3-second window, enforces the ACL at all
// three entry points (slash command, mention, view submission),
// dedups redelivered envelopes, and dispatches the actual work to
// bounded background goroutines that a graceful shutdown waits for.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/nlink-jp/ir-hub/internal/acl"
	"github.com/nlink-jp/ir-hub/internal/analysis"
	"github.com/nlink-jp/ir-hub/internal/cases"
	"github.com/nlink-jp/ir-hub/internal/command"
	"github.com/nlink-jp/ir-hub/internal/ingest"
	"github.com/nlink-jp/ir-hub/internal/knowledge"
	"github.com/nlink-jp/ir-hub/internal/modal"
	"github.com/nlink-jp/ir-hub/internal/msg"
	"github.com/nlink-jp/ir-hub/internal/slackapi"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// defaultMaxConcurrent bounds background work (LLM analyses,
// ingest/backfill, Slack calls).
const defaultMaxConcurrent = 16

// Analyzer is the LLM analysis surface the bot consumes;
// analysis.Runner implements it, tests fake it.
type Analyzer interface {
	RunPostmortem(ctx context.Context, c *store.Case) (*analysis.Report, error)
	Translate(ctx context.Context, rep *analysis.Report) *analysis.Report
	StatusSummary(ctx context.Context, c *store.Case) (string, error)
	Answer(ctx context.Context, c *store.Case, question string, docs []store.KnowledgeDoc) (string, error)
	Briefing(ctx context.Context, title, severity string, summaries []analysis.KnowledgeSummary) (string, error)
}

// Exporter writes knowledge documents to storage; export.Service
// implements it. Nil when storage is unavailable (graceful
// degradation).
type Exporter interface {
	ExportAll(ctx context.Context) (int, error)
	ExportCase(ctx context.Context, caseID int64) (int, error)
	Backend() string
}

// Config carries bot-level settings.
type Config struct {
	// DefaultVisibility preselects the new-case modal radio.
	DefaultVisibility string
	// NotifyDenied sends an ephemeral note to denied users instead
	// of staying silent.
	NotifyDenied bool
	// MaxConcurrent bounds dispatched background work.
	MaxConcurrent int
	// Msg is the user-facing message catalog; nil means English.
	Msg *msg.Catalog
	// BotUserID is the bot's own Slack user ID, used to strip the
	// mention token from @-mention questions.
	BotUserID string
}

// Bot wires the event loop to the services.
type Bot struct {
	socket   Socket
	api      slackapi.API
	store    *store.Store
	acl      *acl.Checker
	cases    *cases.Service
	ingest   *ingest.Ingester
	analyzer Analyzer
	export   Exporter // nil when storage is unavailable
	cfg      Config
	logf     func(format string, v ...any)
	dedup    *dedup
	now      func() time.Time

	mu       sync.Mutex
	draining bool
	wg       sync.WaitGroup
	sem      chan struct{}
}

// Option configures a Bot.
type Option func(*Bot)

// WithLogger overrides the log function.
func WithLogger(logf func(format string, v ...any)) Option {
	return func(b *Bot) { b.logf = logf }
}

// WithExport injects the knowledge export service. Without it,
// export-related actions report "not configured" and auto-export is
// skipped.
func WithExport(e Exporter) Option {
	return func(b *Bot) { b.export = e }
}

// WithClock injects a deterministic clock (dedup TTL, knowledge
// timestamps) for tests.
func WithClock(now func() time.Time) Option {
	return func(b *Bot) {
		b.dedup.now = now
		b.now = now
	}
}

// New creates a Bot.
func New(socket Socket, api slackapi.API, st *store.Store, checker *acl.Checker,
	caseSvc *cases.Service, ing *ingest.Ingester, analyzer Analyzer, cfg Config, opts ...Option) *Bot {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.Msg == nil {
		cfg.Msg = &msg.EN
	}
	b := &Bot{
		socket:   socket,
		api:      api,
		store:    st,
		acl:      checker,
		cases:    caseSvc,
		ingest:   ing,
		analyzer: analyzer,
		cfg:      cfg,
		logf:     log.Printf,
		dedup:    newDedup(time.Now),
		now:      time.Now,
		sem:      make(chan struct{}, cfg.MaxConcurrent),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Run consumes events until ctx is cancelled or the socket closes.
// It blocks like socketmode.Client.RunContext does.
func (b *Bot) Run(ctx context.Context) error {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-b.socket.Events():
				if !ok {
					return
				}
				b.handleEvent(ctx, evt)
			}
		}
	}()
	return b.socket.Run(ctx)
}

// Wait stops accepting new work and blocks until in-flight
// background work completes. Call after Run returns.
func (b *Bot) Wait() {
	b.mu.Lock()
	b.draining = true
	b.mu.Unlock()
	b.wg.Wait()
}

// dispatch runs fn on a background goroutine. Slow work goes
// through the concurrency semaphore; fast=true bypasses it for
// paths racing Slack's 3-second timers (views.open).
func (b *Bot) dispatch(fast bool, fn func()) {
	b.mu.Lock()
	if b.draining {
		b.mu.Unlock()
		return
	}
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		if !fast {
			b.sem <- struct{}{}
			defer func() { <-b.sem }()
		}
		fn()
	}()
}

func (b *Bot) handleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		b.logf("bot: connecting to slack")

	case socketmode.EventTypeConnectionError:
		b.logf("bot: connection error: %v", evt.Data)

	case socketmode.EventTypeConnected:
		b.logf("bot: connected")
		b.dispatch(false, func() { b.ingest.Backfill(ctx) })

	case socketmode.EventTypeDisconnect:
		b.logf("bot: disconnected")

	case socketmode.EventTypeSlashCommand:
		cmd, ok := evt.Data.(slack.SlashCommand)
		if !ok || evt.Request == nil {
			b.logf("bot: malformed slash command event")
			return
		}
		// ACK immediately — 3-second rule. Results arrive later via
		// response_url / posted messages.
		req := *evt.Request
		b.socket.Ack(req)
		if b.dedup.Seen("env:" + req.EnvelopeID) {
			return
		}
		// trigger_id (modal path) expires in 3s: bypass the semaphore.
		b.dispatch(true, func() { b.handleSlash(ctx, cmd) })

	case socketmode.EventTypeInteractive:
		cb, ok := evt.Data.(slack.InteractionCallback)
		if !ok || evt.Request == nil {
			b.logf("bot: malformed interactive event")
			return
		}
		b.handleInteractive(ctx, *evt.Request, cb)

	case socketmode.EventTypeEventsAPI:
		ev, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok || evt.Request == nil {
			b.logf("bot: malformed events api event")
			return
		}
		req := *evt.Request
		b.socket.Ack(req)
		if req.RetryAttempt > 0 {
			b.logf("bot: events api redelivery (attempt %d, reason %s)", req.RetryAttempt, req.RetryReason)
		}
		if b.dedup.Seen(eventKey(ev, req)) {
			return
		}
		b.dispatch(false, func() { b.handleEventsAPI(ctx, ev) })
	}
}

// eventKey prefers the Events API event_id (stable across
// redeliveries); envelope ID is the fallback.
func eventKey(ev slackevents.EventsAPIEvent, req socketmode.Request) string {
	if cb, ok := ev.Data.(*slackevents.EventsAPICallbackEvent); ok && cb.EventID != "" {
		return "evt:" + cb.EventID
	}
	return "env:" + req.EnvelopeID
}

// ---- slash commands ----

func (b *Bot) handleSlash(ctx context.Context, cmd slack.SlashCommand) {
	decision, err := b.acl.Check(ctx, cmd.UserID)
	if err != nil {
		b.logf("bot: acl check for %s: %v", cmd.UserID, err)
	}
	if !decision.Allowed {
		b.deny(ctx, store.Denial{
			UserID:     cmd.UserID,
			ChannelID:  cmd.ChannelID,
			Entrypoint: "slash",
			Action:     firstWord(cmd.Text),
			Reason:     decision.Reason,
		}, cmd.ResponseURL)
		return
	}

	parsed, err := command.Parse(cmd.Text)
	if err != nil {
		b.respond(ctx, cmd.ResponseURL, ":warning: "+b.parseErrText(err))
		return
	}

	switch parsed.Sub {
	case "":
		view := modal.BuildActionPicker(modal.Metadata{ChannelID: cmd.ChannelID, UserID: cmd.UserID}, b.cfg.Msg)
		if err := b.api.OpenView(ctx, cmd.TriggerID, view); err != nil {
			b.logf("bot: open action picker: %v", err)
			b.respond(ctx, cmd.ResponseURL, b.cfg.Msg.ModalOpenFailed)
		}
	case "new":
		b.runNew(ctx, *parsed.New, cmd.UserID, cmd.ResponseURL)
	case "close":
		b.runClose(ctx, cmd.ChannelID, cmd.UserID, cmd.ResponseURL)
	case "status":
		b.runStatus(ctx, cmd.ChannelID, cmd.UserID, cmd.ResponseURL)
	case "pm":
		b.runPM(ctx, cmd.ChannelID, cmd.UserID, cmd.ResponseURL)
	case "export":
		b.runExport(ctx, cmd.ChannelID, cmd.UserID, cmd.ResponseURL)
	}
}

// ---- interactive (view submissions) ----

func (b *Bot) handleInteractive(ctx context.Context, req socketmode.Request, cb slack.InteractionCallback) {
	if cb.Type != slack.InteractionTypeViewSubmission {
		b.socket.Ack(req)
		return
	}
	if b.dedup.Seen("env:" + req.EnvelopeID) {
		b.socket.Ack(req)
		return
	}

	// Defense in depth: the user passed the ACL to open the modal,
	// but membership may have changed before submission.
	decision, err := b.acl.Check(ctx, cb.User.ID)
	if err != nil {
		b.logf("bot: acl check for %s: %v", cb.User.ID, err)
	}
	if !decision.Allowed {
		b.socket.Ack(req) // close the modal silently
		b.deny(ctx, store.Denial{
			UserID:     cb.User.ID,
			Entrypoint: "view_submission",
			Action:     cb.View.CallbackID,
			Reason:     decision.Reason,
		}, "")
		return
	}

	switch cb.View.CallbackID {
	case modal.CallbackAction:
		action, meta, err := modal.ParseAction(cb.View)
		if err != nil {
			b.socket.Ack(req)
			b.logf("bot: parse action submission: %v", err)
			return
		}
		switch action {
		case modal.ActionNew:
			next := modal.BuildNewCase(meta, b.cfg.DefaultVisibility, b.cfg.Msg)
			// Synchronous response: push the parameter form.
			b.socket.Ack(req, slack.NewPushViewSubmissionResponse(&next))
		case modal.ActionClose:
			b.socket.Ack(req)
			b.dispatch(false, func() { b.runClose(ctx, meta.ChannelID, meta.UserID, "") })
		case modal.ActionStatus:
			b.socket.Ack(req)
			b.dispatch(false, func() { b.runStatus(ctx, meta.ChannelID, meta.UserID, "") })
		case modal.ActionPM:
			b.socket.Ack(req)
			b.dispatch(false, func() { b.runPM(ctx, meta.ChannelID, meta.UserID, "") })
		case modal.ActionExport:
			b.socket.Ack(req)
			b.dispatch(false, func() { b.runExport(ctx, meta.ChannelID, meta.UserID, "") })
		}

	case modal.CallbackNew:
		args, meta, fieldErrs, err := modal.ParseNewCase(cb.View, b.cfg.Msg)
		if err != nil {
			b.socket.Ack(req)
			b.logf("bot: parse new-case submission: %v", err)
			return
		}
		if len(fieldErrs) > 0 {
			// Synchronous response: show validation errors in place.
			b.socket.Ack(req, slack.NewErrorsViewSubmissionResponse(fieldErrs))
			return
		}
		// This view was pushed on top of the action picker: an empty
		// ack would only pop back to the picker. Clear the whole
		// modal stack instead.
		b.socket.Ack(req, slack.NewClearViewSubmissionResponse())
		b.dispatch(false, func() { b.runNew(ctx, args, meta.UserID, "") })

	default:
		b.socket.Ack(req)
		b.logf("bot: unknown view callback %q", cb.View.CallbackID)
	}
}

// ---- events api ----

func (b *Bot) handleEventsAPI(ctx context.Context, ev slackevents.EventsAPIEvent) {
	switch inner := ev.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		if err := b.ingest.HandleMessage(inner); err != nil {
			b.logf("bot: ingest message %s/%s: %v", inner.Channel, inner.TimeStamp, err)
		}

	case *slackevents.AppMentionEvent:
		decision, err := b.acl.Check(ctx, inner.User)
		if err != nil {
			b.logf("bot: acl check for %s: %v", inner.User, err)
		}
		if !decision.Allowed {
			b.deny(ctx, store.Denial{
				UserID:     inner.User,
				ChannelID:  inner.Channel,
				Entrypoint: "mention",
				Reason:     decision.Reason,
			}, "")
			return
		}
		b.runQA(ctx, inner.Channel, inner.User, stripMentions(inner.Text, b.cfg.BotUserID))
	}
}

// runQA answers a knowledge question. An empty question (bare
// mention) prompts the user; otherwise it narrows knowledge by the
// question's words, runs the Answer analysis, and posts to the
// channel (collaborative — others learn the answer).
func (b *Bot) runQA(ctx context.Context, channelID, userID, question string) {
	m := b.cfg.Msg
	if question == "" {
		if err := b.api.PostEphemeral(ctx, channelID, userID,
			slack.MsgOptionText(m.MentionEmptyQuestion, false)); err != nil {
			b.logf("bot: empty-question reply: %v", err)
		}
		return
	}

	docs, err := b.store.SearchKnowledge(strings.Fields(question), nil, "")
	if err != nil {
		b.logf("bot: search knowledge: %v", err)
		b.postOrLog(ctx, channelID, m.F(m.MentionAnswerFailed, err))
		return
	}
	// Keyword narrowing can miss when the question and the
	// (English-canonical) knowledge are in different languages, or
	// for non-space-delimited scripts. Fall back to loading all
	// knowledge — Answer budget-limits the docs it actually uses.
	if len(docs) == 0 {
		if docs, err = b.store.ListAllKnowledge(); err != nil {
			b.logf("bot: list knowledge: %v", err)
			b.postOrLog(ctx, channelID, m.F(m.MentionAnswerFailed, err))
			return
		}
	}

	// Case context applies only inside a case channel.
	var c *store.Case
	if got, err := b.store.CaseByChannel(channelID); err == nil {
		c = got
	}

	answer, err := b.analyzer.Answer(ctx, c, question, docs)
	if err != nil {
		b.logf("bot: knowledge Q&A: %v", err)
		b.postOrLog(ctx, channelID, m.F(m.MentionAnswerFailed, err))
		return
	}
	b.postOrLog(ctx, channelID, answer)
}

// stripMentions removes leading Slack mention tokens (<@U…> or
// <@U…|label>) from text and trims the remainder.
func stripMentions(text, botUserID string) string {
	s := strings.TrimSpace(text)
	for strings.HasPrefix(s, "<@") {
		end := strings.Index(s, ">")
		if end < 0 {
			break
		}
		s = strings.TrimSpace(s[end+1:])
	}
	_ = botUserID // all leading mentions are stripped, not just the bot's
	return s
}

// ---- shared actions ----

// parseErrText renders a command.ParseError in the configured UI
// language; non-ParseErrors fall back to their English Error().
func (b *Bot) parseErrText(err error) string {
	var pe *command.ParseError
	if !errors.As(err, &pe) {
		return err.Error()
	}
	m := b.cfg.Msg
	allowed := strings.Join(command.Severities, "|")
	switch pe.Kind {
	case command.ErrKindUnknownSubcommand:
		return m.F(m.ErrUnknownSubcommand, pe.Arg)
	case command.ErrKindTakesNoArgs:
		return m.F(m.ErrTakesNoArgs, pe.Arg)
	case command.ErrKindSeverityNeedsValue:
		return m.F(m.ErrSeverityNeedsValue, allowed)
	case command.ErrKindInvalidSeverity:
		return m.F(m.ErrInvalidSeverity, pe.Arg, allowed)
	case command.ErrKindTitleRequired:
		return m.ErrTitleRequired
	case command.ErrKindVisibilityConflict:
		return m.ErrVisibilityConflict
	case command.ErrKindUnknownFlag:
		return m.F(m.ErrUnknownFlag, pe.Arg)
	default:
		return err.Error()
	}
}

// caseErrText localizes the user-actionable case errors; everything
// else keeps its English Error() (system failures aimed at logs).
func (b *Bot) caseErrText(err error) string {
	switch {
	case errors.Is(err, cases.ErrNotCaseChannel):
		return b.cfg.Msg.ErrNotCaseChannel
	case errors.Is(err, store.ErrNotOpen):
		return b.cfg.Msg.ErrCaseNotOpen
	default:
		return err.Error()
	}
}

func (b *Bot) runNew(ctx context.Context, args command.NewArgs, userID, responseURL string) {
	res, err := b.cases.NewCase(ctx, cases.NewRequest{
		Title:      args.Title,
		Severity:   args.Severity,
		Visibility: args.Visibility,
		OpenedBy:   userID,
	})
	if err != nil {
		b.logf("bot: new case: %v", err)
		b.respond(ctx, responseURL, b.cfg.Msg.F(b.cfg.Msg.CaseOpenFailed, err))
		return
	}
	text := b.cfg.Msg.F(b.cfg.Msg.CaseOpenedNotice, res.Case.ID, res.Case.ChannelID)
	for _, w := range res.Warnings {
		text += "\n:warning: " + w
	}
	b.respond(ctx, responseURL, text)

	// The kickoff and slash response already happened above, so the
	// briefing runs inline in this (already dispatched) goroutine —
	// its latency never blocks case creation, and running inline
	// avoids being skipped by a concurrent shutdown drain.
	b.runBriefing(ctx, res.Case)
}

// runBriefing posts relevant past knowledge into a new case
// channel. Best-effort: silent when there is no knowledge or none
// is relevant; failures are logged, never surfaced (they must not
// compete with the kickoff).
func (b *Bot) runBriefing(ctx context.Context, c *store.Case) {
	docs, err := b.store.ListAllKnowledge()
	if err != nil {
		b.logf("bot: briefing list knowledge: %v", err)
		return
	}
	if len(docs) == 0 {
		return // empty corpus (e.g. first-ever case): no noise
	}

	// Budget guard: if all summaries don't fit, narrow by the new
	// case's title words.
	summaries := toSummaries(docs)
	if overBudget(summaries) {
		if narrowed, err := b.store.SearchKnowledge(strings.Fields(c.Title), nil, ""); err == nil && len(narrowed) > 0 {
			summaries = toSummaries(narrowed)
		}
	}

	briefing, err := b.analyzer.Briefing(ctx, c.Title, c.Severity, summaries)
	if err != nil {
		b.logf("bot: briefing: %v", err)
		return
	}
	if strings.TrimSpace(briefing) == "" || briefing == analysis.NoBriefing {
		return // model found nothing relevant
	}
	b.postOrLog(ctx, c.ChannelID, b.cfg.Msg.BriefingHeader+"\n"+briefing)
}

func toSummaries(docs []store.KnowledgeDoc) []analysis.KnowledgeSummary {
	out := make([]analysis.KnowledgeSummary, 0, len(docs))
	for _, d := range docs {
		out = append(out, analysis.KnowledgeSummary{
			TacticID: d.TacticID, Title: d.Title, Category: d.Category, Summary: d.Summary,
		})
	}
	return out
}

// overBudget approximates whether the joined summaries are too large
// for one briefing prompt (rough heuristic; the analysis layer also
// guards). ~50 summaries of a sentence each is the practical line.
func overBudget(summaries []analysis.KnowledgeSummary) bool {
	total := 0
	for _, s := range summaries {
		total += len(s.Title) + len(s.Summary) + len(s.Category) + len(s.TacticID)
	}
	return total > 60000 // chars; well under the model window but bounds fan-in
}

// runExport writes all knowledge to storage.
func (b *Bot) runExport(ctx context.Context, channelID, userID, responseURL string) {
	m := b.cfg.Msg
	if b.export == nil {
		b.userError(ctx, channelID, userID, responseURL, m.ExportNotConfigured)
		return
	}
	b.respond(ctx, responseURL, m.F(m.ExportStarted, b.export.Backend()))
	n, err := b.export.ExportAll(ctx)
	if err != nil {
		b.logf("bot: export: %v", err)
		b.userError(ctx, channelID, userID, responseURL, m.F(m.ExportFailed, err))
		return
	}
	b.userError(ctx, channelID, userID, responseURL, m.F(m.ExportDone, n))
}

func (b *Bot) runClose(ctx context.Context, channelID, userID, responseURL string) {
	if _, err := b.cases.Close(ctx, channelID, userID); err != nil {
		b.userError(ctx, channelID, userID, responseURL,
			b.cfg.Msg.F(b.cfg.Msg.CloseFailed, b.caseErrText(err)))
		return
	}
	// Success is announced by the closing message Close() posts.
	// The postmortem follows inline in this (already dispatched)
	// goroutine so graceful shutdown keeps tracking it; concurrent
	// runs per case are bounded by ErrPMRunning.
	b.runPM(ctx, channelID, userID, responseURL)
}

func (b *Bot) runStatus(ctx context.Context, channelID, userID, responseURL string) {
	text, err := b.cases.Status(ctx, channelID)
	if err != nil {
		b.userError(ctx, channelID, userID, responseURL,
			b.cfg.Msg.F(b.cfg.Msg.StatusFailed, b.caseErrText(err)))
		return
	}
	// Metadata block replies immediately; the LLM situation summary
	// follows as a channel post.
	b.userError(ctx, channelID, userID, responseURL, text+"\n"+b.cfg.Msg.StatusGenerating)

	c, err := b.store.CaseByChannel(channelID)
	if err != nil {
		b.logf("bot: status summary lookup: %v", err)
		return
	}
	summary, err := b.analyzer.StatusSummary(ctx, c)
	if err != nil {
		b.logf("bot: status summary: %v", err)
		b.userError(ctx, channelID, userID, responseURL,
			b.cfg.Msg.F(b.cfg.Msg.StatusLLMFailed, err))
		return
	}
	if _, err := b.api.PostMessage(ctx, channelID, slack.MsgOptionText(summary, false)); err != nil {
		b.logf("bot: post status summary: %v", err)
	}
}

// runPM executes the postmortem for the case bound to channelID:
// guard (case exists, has messages, no run in flight) → progress
// post → five-stage analysis → finalize (store report + replace
// knowledge with fresh tactic IDs) → translated compact summary
// with the full Markdown report attached as a snippet.
func (b *Bot) runPM(ctx context.Context, channelID, userID, responseURL string) {
	m := b.cfg.Msg
	c, err := b.store.CaseByChannel(channelID)
	if errors.Is(err, store.ErrNotFound) {
		b.userError(ctx, channelID, userID, responseURL, m.F(m.PMFailed, m.ErrNotCaseChannel))
		return
	}
	if err != nil {
		b.logf("bot: pm lookup: %v", err)
		return
	}
	if n, err := b.store.CountMessages(c.ID); err != nil || n == 0 {
		b.userError(ctx, channelID, userID, responseURL, m.F(m.PMFailed, m.PMNoMessages))
		return
	}

	runID, err := b.store.BeginPMRun(c.ID)
	if errors.Is(err, store.ErrPMRunning) {
		b.userError(ctx, channelID, userID, responseURL, m.F(m.PMFailed, m.PMAlreadyRunning))
		return
	}
	if err != nil {
		b.logf("bot: begin pm run: %v", err)
		return
	}

	if _, err := b.api.PostMessage(ctx, channelID, slack.MsgOptionText(m.PMStarted, false)); err != nil {
		b.logf("bot: pm progress post: %v", err)
	}

	rep, err := b.analyzer.RunPostmortem(ctx, c)
	if err != nil {
		b.logf("bot: postmortem case #%d: %v", c.ID, err)
		if ferr := b.store.FailPMRun(runID, err.Error()); ferr != nil {
			b.logf("bot: fail pm run: %v", ferr)
		}
		b.postOrLog(ctx, channelID, m.F(m.PMFailed, err))
		return
	}

	// Canonical English artifacts for storage.
	reportJSON, err := json.Marshal(rep)
	if err != nil {
		b.logf("bot: marshal report: %v", err)
		reportJSON = []byte("{}")
	}
	englishMD := analysis.RenderMarkdown(rep, &msg.EN)

	createdAt := b.now().UTC()
	err = b.store.FinalizePMRun(runID, c.ID, string(reportJSON), englishMD,
		len(rep.Tactics), func(i int, tacticID string) store.KnowledgeRow {
			doc, derr := knowledge.Build(rep.Tactics[i], tacticID, rep.Channel, rep.Participants, createdAt)
			if derr != nil {
				b.logf("bot: build knowledge doc %s: %v", tacticID, derr)
			}
			tags, _ := json.Marshal(rep.Tactics[i].Tags)
			return store.KnowledgeRow{
				TacticID:   tacticID,
				Title:      rep.Tactics[i].Title,
				Category:   rep.Tactics[i].Category,
				Confidence: rep.Tactics[i].Confidence,
				TagsJSON:   string(tags),
				Summary:    doc.Summary,
				DocJSON:    doc.JSON,
				DocMD:      doc.Markdown,
			}
		})
	if err != nil {
		b.logf("bot: finalize pm run: %v", err)
		b.postOrLog(ctx, channelID, m.F(m.PMFailed, err))
		return
	}

	// Channel-facing output in the configured language.
	translated := b.analyzer.Translate(ctx, rep)
	translatedMD := analysis.RenderMarkdown(translated, m)
	compact := b.pmCompact(translated)

	_, err = b.api.UploadFile(ctx, slack.UploadFileParameters{
		Channel:        channelID,
		Filename:       fmt.Sprintf("ir-%04d-postmortem.md", c.ID),
		Title:          m.F(m.RptTitle, c.ID, translated.Summary.Title),
		Content:        translatedMD,
		FileSize:       len(translatedMD),
		InitialComment: compact,
		SnippetType:    "markdown",
	})
	if err != nil {
		b.logf("bot: upload pm report: %v", err)
		b.postOrLog(ctx, channelID, compact+"\n"+m.F(m.PMUploadFailed, err))
	}

	// Auto-export this case's knowledge to storage (outside the
	// finalize tx, which already committed). Best-effort: export
	// failure must never fail the postmortem.
	if b.export != nil {
		if n, eerr := b.export.ExportCase(ctx, c.ID); eerr != nil {
			b.logf("bot: auto-export case #%d: %v", c.ID, eerr)
		} else {
			b.logf("bot: auto-exported %d knowledge document(s) for case #%d", n, c.ID)
		}
	}
}

// pmCompact builds the channel summary post for a (translated)
// report. Kept well under Slack's limits by construction.
func (b *Bot) pmCompact(rep *analysis.Report) string {
	m := b.cfg.Msg
	lines := []string{
		m.F(m.PMCompactHeader, rep.CaseID, rep.Summary.Title),
		m.F(m.PMCompactSeverity, rep.Summary.Severity),
		m.F(m.PMCompactScore, rep.Review.OverallScore),
		m.F(m.PMCompactTactics, len(rep.Tactics)),
	}
	appendTop := func(label string, items []string) {
		if len(items) == 0 {
			return
		}
		lines = append(lines, "*"+label+"*")
		for i, it := range items {
			if i == 2 {
				break
			}
			lines = append(lines, "• "+it)
		}
	}
	appendTop(m.RptStrengths, rep.Review.Strengths)
	appendTop(m.RptImprovements, rep.Review.Improvements)
	if rep.Truncated {
		lines = append(lines, m.F(m.RptTruncated, rep.AnalyzedMessages, rep.TotalMessages))
	}
	lines = append(lines, m.PMCompactSee)
	return strings.Join(lines, "\n")
}

// postOrLog posts to the channel, falling back to the log.
func (b *Bot) postOrLog(ctx context.Context, channelID, text string) {
	if _, err := b.api.PostMessage(ctx, channelID, slack.MsgOptionText(text, false)); err != nil {
		b.logf("bot: post: %v (text: %s)", err, text)
	}
}

// respond replies to a slash invocation via response_url when
// available; otherwise it logs (modal flows without a case channel
// have nowhere better in Phase 1).
func (b *Bot) respond(ctx context.Context, responseURL, text string) {
	if responseURL == "" {
		b.logf("bot: result (no response_url): %s", text)
		return
	}
	if err := b.api.PostResponse(ctx, responseURL, text); err != nil {
		b.logf("bot: respond: %v", err)
	}
}

// userError prefers response_url, falling back to an ephemeral
// message in the channel (modal flows carry no response_url).
func (b *Bot) userError(ctx context.Context, channelID, userID, responseURL, text string) {
	if responseURL != "" {
		b.respond(ctx, responseURL, text)
		return
	}
	if err := b.api.PostEphemeral(ctx, channelID, userID, slack.MsgOptionText(text, false)); err != nil {
		b.logf("bot: ephemeral: %v (text: %s)", err, text)
	}
}

// deny audits an ACL denial; silent unless notify_denied is set.
func (b *Bot) deny(ctx context.Context, d store.Denial, responseURL string) {
	b.logf("bot: denied %s at %s (%s)", d.UserID, d.Entrypoint, d.Reason)
	if err := b.store.InsertDenial(d); err != nil {
		b.logf("bot: audit denial: %v", err)
	}
	if !b.cfg.NotifyDenied {
		return
	}
	notice := b.cfg.Msg.DeniedNotice
	if responseURL != "" {
		b.respond(ctx, responseURL, notice)
	} else if d.ChannelID != "" {
		if err := b.api.PostEphemeral(ctx, d.ChannelID, d.UserID, slack.MsgOptionText(notice, false)); err != nil {
			b.logf("bot: notify denied: %v", err)
		}
	}
}

func firstWord(s string) string {
	for i, r := range s {
		if r == ' ' {
			return s[:i]
		}
	}
	return s
}
