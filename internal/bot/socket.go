package bot

import (
	"context"

	"github.com/slack-go/slack/socketmode"
)

// Socket is the slim boundary to the Socket Mode client so the bot
// loop can be tested with a fake.
type Socket interface {
	Events() <-chan socketmode.Event
	Ack(req socketmode.Request, payload ...any)
	Run(ctx context.Context) error
}

// SocketAdapter implements Socket on a real *socketmode.Client.
type SocketAdapter struct {
	c *socketmode.Client
}

var _ Socket = (*SocketAdapter)(nil)

// NewSocketAdapter wraps a socketmode client.
func NewSocketAdapter(c *socketmode.Client) *SocketAdapter { return &SocketAdapter{c: c} }

func (a *SocketAdapter) Events() <-chan socketmode.Event { return a.c.Events }

func (a *SocketAdapter) Ack(req socketmode.Request, payload ...any) {
	a.c.Ack(req, payload...)
}

func (a *SocketAdapter) Run(ctx context.Context) error { return a.c.RunContext(ctx) }
