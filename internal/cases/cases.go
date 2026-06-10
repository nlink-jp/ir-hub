// Package cases implements the case lifecycle use-cases: opening a
// case (channel creation + kickoff), closing it, and reporting
// metadata status. LLM-backed analysis arrives in Phase 2; nothing
// here calls a model.
package cases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/channelname"
	"github.com/nlink-jp/ir-hub/internal/command"
	"github.com/nlink-jp/ir-hub/internal/slackapi"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// ErrNotCaseChannel is returned when close/status runs in a channel
// that is not bound to a case.
var ErrNotCaseChannel = errors.New("this channel is not an ir-hub case channel")

// Config carries the channel-related settings the service needs.
type Config struct {
	DefaultVisibility string // "public" | "private"
	NamePrefix        string
}

// Service implements the case lifecycle.
type Service struct {
	api   slackapi.API
	store *store.Store
	cfg   Config
	now   func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// New creates a Service.
func New(api slackapi.API, st *store.Store, cfg Config, opts ...Option) *Service {
	s := &Service{api: api, store: st, cfg: cfg, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewRequest are the inputs for opening a case.
type NewRequest struct {
	Title      string
	Severity   string
	Visibility command.Visibility
	OpenedBy   string // Slack user ID; invited to the new channel
}

// NewResult reports the opened case plus non-fatal warnings (failed
// invite / kickoff post) the caller should surface to the user.
type NewResult struct {
	Case     *store.Case
	Warnings []string
}

// NewCase reserves a sequence number, creates the Slack channel,
// activates the case, invites the opener, and posts a kickoff
// message. Channel-creation failure marks the case failed and
// returns an error; the sequence gap is accepted by design.
func (s *Service) NewCase(ctx context.Context, req NewRequest) (*NewResult, error) {
	private := s.cfg.DefaultVisibility == "private"
	switch req.Visibility {
	case command.VisibilityPrivate:
		private = true
	case command.VisibilityPublic:
		private = false
	}
	visibility := "public"
	if private {
		visibility = "private"
	}

	c, err := s.store.CreateCase(req.Title, req.Severity, visibility, req.OpenedBy)
	if err != nil {
		return nil, err
	}

	name := channelname.Build(s.cfg.NamePrefix, c.ID, req.Title)
	ch, err := s.api.CreateConversation(ctx, name, private)
	if err != nil {
		if failErr := s.store.FailCase(c.ID); failErr != nil {
			return nil, fmt.Errorf("create channel %q: %v (also failed to mark case failed: %w)", name, err, failErr)
		}
		return nil, fmt.Errorf("create channel %q: %w", name, err)
	}
	if err := s.store.ActivateCase(c.ID, ch.ID, name); err != nil {
		return nil, err
	}

	res := &NewResult{}
	if err := s.api.InviteUsers(ctx, ch.ID, req.OpenedBy); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("could not invite <@%s>: %v", req.OpenedBy, err))
	}
	if _, err := s.api.PostMessage(ctx, ch.ID,
		slack.MsgOptionText(kickoffText(c.ID, req, private), false)); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("could not post kickoff message: %v", err))
	}

	res.Case, err = s.store.CaseByID(c.ID)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func kickoffText(id int64, req NewRequest, private bool) string {
	text := fmt.Sprintf(
		":rotating_light: *Case #%04d opened: %s*\n"+
			"• Severity: `%s`\n"+
			"• Opened by: <@%s>\n"+
			"• Close with `/ir-hub close` when the response is done — "+
			"the postmortem will run from there (Phase 2).",
		id, req.Title, req.Severity, req.OpenedBy)
	if private {
		text += "\n• This channel is *private*: it cannot be converted to public " +
			"later, and ir-hub must remain a member to keep ingesting messages."
	}
	return text
}

// Close transitions the case bound to channelID to closed and posts
// a closing note.
func (s *Service) Close(ctx context.Context, channelID, closedBy string) (*store.Case, error) {
	c, err := s.store.CaseByChannel(channelID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotCaseChannel
	}
	if err != nil {
		return nil, err
	}
	if err := s.store.CloseCase(c.ID, closedBy); err != nil {
		return nil, err
	}
	if _, err := s.api.PostMessage(ctx, channelID, slack.MsgOptionText(
		fmt.Sprintf(":white_check_mark: *Case #%04d closed* by <@%s>.", c.ID, closedBy), false)); err != nil {
		// The state change already happened; surface but don't undo.
		return nil, fmt.Errorf("case closed but posting the closing note failed: %w", err)
	}
	return s.store.CaseByID(c.ID)
}

// Status returns a human-readable metadata summary (mrkdwn) for the
// case bound to channelID.
func (s *Service) Status(ctx context.Context, channelID string) (string, error) {
	c, err := s.store.CaseByChannel(channelID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotCaseChannel
	}
	if err != nil {
		return "", err
	}
	count, err := s.store.CountMessages(c.ID)
	if err != nil {
		return "", err
	}

	text := fmt.Sprintf("*Case #%04d: %s*\n"+
		"• State: `%s`\n"+
		"• Severity: `%s`\n"+
		"• Opened: %s by <@%s>\n",
		c.ID, c.Title, c.State, c.Severity,
		c.OpenedAt.UTC().Format("2006-01-02 15:04 MST"), c.OpenedBy)
	if c.State == store.StateClosed {
		text += fmt.Sprintf("• Closed: %s by <@%s>\n",
			c.ClosedAt.UTC().Format("2006-01-02 15:04 MST"), c.ClosedBy)
		text += fmt.Sprintf("• Duration: %s\n", formatDuration(c.ClosedAt.Sub(c.OpenedAt)))
	} else {
		text += fmt.Sprintf("• Open for: %s\n", formatDuration(s.now().Sub(c.OpenedAt)))
	}
	text += fmt.Sprintf("• Ingested messages: %d", count)
	return text, nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	d = d.Round(time.Minute)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}
