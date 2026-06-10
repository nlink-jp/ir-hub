// Package ingest stores Slack messages from open case channels into
// the embedded database: real-time message events while connected,
// and a conversations.history backfill on (re)connect to recover
// anything missed while disconnected. Duplicates between the two
// paths are absorbed by the store's (channel_id, ts) primary key.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/nlink-jp/ir-hub/internal/slackapi"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// Subtypes stored in Phase 1. message_changed / message_deleted are
// deliberately skipped (documented limitation); raw JSON is kept so
// later phases can extend handling without re-ingesting.
var allowedSubtypes = map[string]bool{
	"":                 true,
	"bot_message":      true,
	"file_share":       true,
	"thread_broadcast": true,
}

// backfillMinInterval gates reconnect storms: a backfill only
// starts when none is running and the previous one started at
// least this long ago.
const backfillMinInterval = 30 * time.Second

// historyPageLimit is the conversations.history page size.
const historyPageLimit = 200

// Sleeper abstracts waiting so tests run instantly.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

type realSleeper struct{}

func (realSleeper) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Ingester ingests case-channel messages.
type Ingester struct {
	api     slackapi.API
	store   *store.Store
	sleeper Sleeper
	now     func() time.Time
	logf    func(format string, v ...any)

	gate chan struct{} // capacity 1: held while a backfill runs
	last time.Time     // start time of the previous backfill (guarded by gate)
}

// Option configures an Ingester.
type Option func(*Ingester)

// WithSleeper injects a fake sleeper for tests.
func WithSleeper(s Sleeper) Option { return func(i *Ingester) { i.sleeper = s } }

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option { return func(i *Ingester) { i.now = now } }

// WithLogger overrides the log function.
func WithLogger(logf func(format string, v ...any)) Option {
	return func(i *Ingester) { i.logf = logf }
}

// New creates an Ingester.
func New(api slackapi.API, st *store.Store, opts ...Option) *Ingester {
	i := &Ingester{
		api:     api,
		store:   st,
		sleeper: realSleeper{},
		now:     time.Now,
		logf:    log.Printf,
		gate:    make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(i)
	}
	return i
}

// HandleMessage stores a real-time message event when it belongs to
// an open case channel and has a stored subtype. Unknown channels
// and skipped subtypes are silently ignored (the bot may be in
// other channels, and 50k-member workspaces are noisy).
func (i *Ingester) HandleMessage(ev *slackevents.MessageEvent) error {
	if !allowedSubtypes[ev.SubType] {
		return nil
	}
	c, err := i.store.CaseByChannel(ev.Channel)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.State != store.StateOpen {
		return nil
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = i.store.InsertMessage(store.Message{
		ChannelID: ev.Channel,
		TS:        ev.TimeStamp,
		CaseID:    c.ID,
		ThreadTS:  ev.ThreadTimeStamp,
		UserID:    ev.User,
		BotID:     ev.BotID,
		Subtype:   ev.SubType,
		Text:      ev.Text,
		Raw:       string(raw),
		Source:    store.SourceEvent,
	})
	return err
}

// Backfill catches up the history of every open case channel from
// the newest stored ts. Intended to be called on every Socket Mode
// "connected" event; calls are skipped while one is running or
// within backfillMinInterval of the previous start. Per-channel
// failures are logged and do not stop the remaining channels.
func (i *Ingester) Backfill(ctx context.Context) {
	select {
	case i.gate <- struct{}{}:
	default:
		i.logf("ingest: backfill already running, skipped")
		return
	}
	defer func() { <-i.gate }()

	if since := i.now().Sub(i.last); since < backfillMinInterval {
		i.logf("ingest: backfill ran %s ago, skipped", since.Round(time.Second))
		return
	}
	i.last = i.now()

	cases, err := i.store.ListOpenCases()
	if err != nil {
		i.logf("ingest: list open cases: %v", err)
		return
	}
	for _, c := range cases {
		if err := i.backfillChannel(ctx, &c); err != nil {
			if ctx.Err() != nil {
				return
			}
			i.logf("ingest: backfill %s (case #%d): %v", c.ChannelID, c.ID, err)
		}
	}
}

func (i *Ingester) backfillChannel(ctx context.Context, c *store.Case) error {
	oldest, err := i.store.MaxMessageTS(c.ChannelID)
	if err != nil {
		return err
	}
	cursor := ""
	for {
		resp, err := i.api.GetConversationHistory(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: c.ChannelID,
			Oldest:    oldest,
			Inclusive: false,
			Limit:     historyPageLimit,
			Cursor:    cursor,
		})
		if err != nil {
			var rle *slack.RateLimitedError
			if errors.As(err, &rle) {
				if serr := i.sleeper.Sleep(ctx, rle.RetryAfter); serr != nil {
					return serr
				}
				continue
			}
			return err
		}
		for idx := range resp.Messages {
			if err := i.insertHistoryMessage(c, &resp.Messages[idx]); err != nil {
				return err
			}
		}
		cursor = resp.ResponseMetaData.NextCursor
		if cursor == "" || !resp.HasMore {
			return nil
		}
	}
}

func (i *Ingester) insertHistoryMessage(c *store.Case, m *slack.Message) error {
	if !allowedSubtypes[m.SubType] {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = i.store.InsertMessage(store.Message{
		ChannelID: c.ChannelID,
		TS:        m.Timestamp,
		CaseID:    c.ID,
		ThreadTS:  m.ThreadTimestamp,
		UserID:    m.User,
		BotID:     m.BotID,
		Subtype:   m.SubType,
		Text:      m.Text,
		Raw:       string(raw),
		Source:    store.SourceBackfill,
	})
	return err
}
