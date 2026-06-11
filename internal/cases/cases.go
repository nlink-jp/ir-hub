// Package cases implements the case lifecycle use-cases: opening a
// case (channel creation + kickoff), closing it, and reporting
// metadata status. LLM-backed analysis arrives in Phase 2; nothing
// here calls a model.
package cases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/channelname"
	"github.com/nlink-jp/ir-hub/internal/command"
	"github.com/nlink-jp/ir-hub/internal/msg"
	"github.com/nlink-jp/ir-hub/internal/slackapi"
	"github.com/nlink-jp/ir-hub/internal/store"
	"github.com/nlink-jp/ir-hub/internal/userdir"
)

// ErrNotCaseChannel is returned when close/status runs in a channel
// that is not bound to a case.
var ErrNotCaseChannel = errors.New("this channel is not an ir-hub case channel")

// Config carries the channel-related settings the service needs.
type Config struct {
	DefaultVisibility string // "public" | "private"
	NamePrefix        string
	// Msg is the user-facing message catalog; nil means English.
	Msg *msg.Catalog
}

// Service implements the case lifecycle.
type Service struct {
	api      slackapi.API
	store    *store.Store
	cfg      Config
	now      func() time.Time
	resolver *userdir.Resolver
}

// Option configures a Service.
type Option func(*Service)

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithResolver injects a user-ID → display-name resolver for the
// status metadata. Without it, status shows raw IDs.
func WithResolver(res *userdir.Resolver) Option {
	return func(s *Service) { s.resolver = res }
}

// New creates a Service.
func New(api slackapi.API, st *store.Store, cfg Config, opts ...Option) *Service {
	if cfg.Msg == nil {
		cfg.Msg = &msg.EN
	}
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
		res.Warnings = append(res.Warnings, s.cfg.Msg.F(s.cfg.Msg.WarnInviteFailed, req.OpenedBy, err))
	}
	if _, err := s.api.PostMessage(ctx, ch.ID,
		slack.MsgOptionText(s.kickoffText(c.ID, req, private), false)); err != nil {
		res.Warnings = append(res.Warnings, s.cfg.Msg.F(s.cfg.Msg.WarnKickoffFailed, err))
	}

	res.Case, err = s.store.CaseByID(c.ID)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) kickoffText(id int64, req NewRequest, private bool) string {
	m := s.cfg.Msg
	lines := []string{
		m.F(m.KickoffHeader, id, req.Title),
		m.F(m.KickoffSeverity, req.Severity),
		m.F(m.KickoffOpenedBy, req.OpenedBy),
		m.KickoffCloseHint,
	}
	if private {
		lines = append(lines, m.KickoffPrivateNote)
	}
	return strings.Join(lines, "\n")
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
		s.cfg.Msg.F(s.cfg.Msg.CaseClosed, c.ID, closedBy), false)); err != nil {
		// The state change already happened; surface but don't undo.
		return nil, fmt.Errorf("case closed but posting the closing note failed: %w", err)
	}
	return s.store.CaseByID(c.ID)
}

// Reopen transitions the closed case bound to channelID back to open
// and posts a note.
func (s *Service) Reopen(ctx context.Context, channelID, reopenedBy string) (*store.Case, error) {
	c, err := s.store.CaseByChannel(channelID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotCaseChannel
	}
	if err != nil {
		return nil, err
	}
	if err := s.store.ReopenCase(c.ID); err != nil {
		return nil, err
	}
	if _, err := s.api.PostMessage(ctx, channelID, slack.MsgOptionText(
		s.cfg.Msg.F(s.cfg.Msg.CaseReopened, c.ID, reopenedBy), false)); err != nil {
		return nil, fmt.Errorf("case reopened but posting the note failed: %w", err)
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

	m := s.cfg.Msg
	lines := []string{
		m.F(m.StatusHeader, c.ID, c.Title),
		m.F(m.StatusState, c.State),
		m.F(m.StatusSeverity, c.Severity),
		m.F(m.StatusOpened, c.OpenedAt.UTC().Format("2006-01-02 15:04 MST"), s.resolver.Resolve(ctx, c.OpenedBy)),
	}
	if c.State == store.StateClosed {
		lines = append(lines,
			m.F(m.StatusClosed, c.ClosedAt.UTC().Format("2006-01-02 15:04 MST"), s.resolver.Resolve(ctx, c.ClosedBy)),
			m.F(m.StatusDuration, formatDuration(c.ClosedAt.Sub(c.OpenedAt), m)))
	} else {
		lines = append(lines, m.F(m.StatusOpenFor, formatDuration(s.now().Sub(c.OpenedAt), m)))
	}
	lines = append(lines, m.F(m.StatusMessages, count))
	return strings.Join(lines, "\n"), nil
}

func formatDuration(d time.Duration, m *msg.Catalog) string {
	if d < time.Minute {
		return m.DurationLessThanMinute
	}
	d = d.Round(time.Minute)
	h := d / time.Hour
	mm := (d % time.Hour) / time.Minute
	if h == 0 {
		return fmt.Sprintf("%dm", mm)
	}
	return fmt.Sprintf("%dh%02dm", h, mm)
}
