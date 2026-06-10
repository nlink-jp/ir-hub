// Package slackapi defines the Slack Web API surface that ir-hub
// consumes, as a narrow interface so business logic can be tested
// against fakes. Adapter wraps the real *slack.Client.
//
// The interface deliberately reuses slack-go's types — mapping them
// to in-house types would add translation code without adding
// testability.
package slackapi

import (
	"context"

	"github.com/slack-go/slack"
)

// API is the subset of the Slack Web API used by ir-hub.
type API interface {
	// CreateConversation creates a public or private channel.
	CreateConversation(ctx context.Context, name string, private bool) (*slack.Channel, error)
	// InviteUsers invites users to a channel.
	InviteUsers(ctx context.Context, channelID string, userIDs ...string) error
	// PostMessage posts to a channel and returns the message ts.
	PostMessage(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error)
	// PostEphemeral posts a message visible only to one user.
	PostEphemeral(ctx context.Context, channelID, userID string, opts ...slack.MsgOption) error
	// OpenView opens a modal for the trigger ID (3-second validity).
	OpenView(ctx context.Context, triggerID string, view slack.ModalViewRequest) error
	// GetUserGroups lists the workspace's user groups.
	GetUserGroups(ctx context.Context) ([]slack.UserGroup, error)
	// GetUserGroupMembers lists member user IDs of a user group.
	GetUserGroupMembers(ctx context.Context, groupID string) ([]string, error)
	// GetConversationHistory pages through channel history.
	GetConversationHistory(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	// AuthTest returns the bot's own identity.
	AuthTest(ctx context.Context) (*slack.AuthTestResponse, error)
	// PostResponse posts an ephemeral reply to a slash command's
	// response_url — works even when the bot is not a member of the
	// originating channel.
	PostResponse(ctx context.Context, responseURL, text string) error
}

// Adapter implements API on a real *slack.Client.
type Adapter struct {
	c *slack.Client
}

var _ API = (*Adapter)(nil)

// NewAdapter wraps a slack client.
func NewAdapter(c *slack.Client) *Adapter { return &Adapter{c: c} }

func (a *Adapter) CreateConversation(ctx context.Context, name string, private bool) (*slack.Channel, error) {
	return a.c.CreateConversationContext(ctx, slack.CreateConversationParams{
		ChannelName: name,
		IsPrivate:   private,
	})
}

func (a *Adapter) InviteUsers(ctx context.Context, channelID string, userIDs ...string) error {
	_, err := a.c.InviteUsersToConversationContext(ctx, channelID, userIDs...)
	return err
}

func (a *Adapter) PostMessage(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
	_, ts, err := a.c.PostMessageContext(ctx, channelID, opts...)
	return ts, err
}

func (a *Adapter) PostEphemeral(ctx context.Context, channelID, userID string, opts ...slack.MsgOption) error {
	_, err := a.c.PostEphemeralContext(ctx, channelID, userID, opts...)
	return err
}

func (a *Adapter) OpenView(ctx context.Context, triggerID string, view slack.ModalViewRequest) error {
	_, err := a.c.OpenViewContext(ctx, triggerID, view)
	return err
}

func (a *Adapter) GetUserGroups(ctx context.Context) ([]slack.UserGroup, error) {
	return a.c.GetUserGroupsContext(ctx)
}

func (a *Adapter) GetUserGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	return a.c.GetUserGroupMembersContext(ctx, groupID)
}

func (a *Adapter) GetConversationHistory(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	return a.c.GetConversationHistoryContext(ctx, params)
}

func (a *Adapter) AuthTest(ctx context.Context) (*slack.AuthTestResponse, error) {
	return a.c.AuthTestContext(ctx)
}

func (a *Adapter) PostResponse(ctx context.Context, responseURL, text string) error {
	return slack.PostWebhookContext(ctx, responseURL, &slack.WebhookMessage{
		Text:         text,
		ResponseType: "ephemeral",
	})
}
