// Package slackapitest provides a configurable fake slackapi.API
// for tests of packages that depend on the Slack Web API boundary.
package slackapitest

import (
	"context"
	"sync"

	"github.com/slack-go/slack"
)

// Fake implements slackapi.API. Each method delegates to the
// corresponding Fn field when set and records the call name in
// Calls. Unset Fns return zero values.
type Fake struct {
	mu    sync.Mutex
	Calls []string

	CreateConversationFn     func(ctx context.Context, name string, private bool) (*slack.Channel, error)
	InviteUsersFn            func(ctx context.Context, channelID string, userIDs ...string) error
	PostMessageFn            func(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error)
	PostEphemeralFn          func(ctx context.Context, channelID, userID string, opts ...slack.MsgOption) error
	OpenViewFn               func(ctx context.Context, triggerID string, view slack.ModalViewRequest) error
	GetUserGroupsFn          func(ctx context.Context) ([]slack.UserGroup, error)
	GetUserGroupMembersFn    func(ctx context.Context, groupID string) ([]string, error)
	GetConversationHistoryFn func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	AuthTestFn               func(ctx context.Context) (*slack.AuthTestResponse, error)
	PostResponseFn           func(ctx context.Context, responseURL, text string) error
}

func (f *Fake) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, name)
}

// CallNames returns a copy of the recorded call sequence.
func (f *Fake) CallNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.Calls))
	copy(out, f.Calls)
	return out
}

func (f *Fake) CreateConversation(ctx context.Context, name string, private bool) (*slack.Channel, error) {
	f.record("CreateConversation")
	if f.CreateConversationFn != nil {
		return f.CreateConversationFn(ctx, name, private)
	}
	ch := &slack.Channel{}
	ch.ID = "CFAKE"
	ch.Name = name
	return ch, nil
}

func (f *Fake) InviteUsers(ctx context.Context, channelID string, userIDs ...string) error {
	f.record("InviteUsers")
	if f.InviteUsersFn != nil {
		return f.InviteUsersFn(ctx, channelID, userIDs...)
	}
	return nil
}

func (f *Fake) PostMessage(ctx context.Context, channelID string, opts ...slack.MsgOption) (string, error) {
	f.record("PostMessage")
	if f.PostMessageFn != nil {
		return f.PostMessageFn(ctx, channelID, opts...)
	}
	return "1718000000.000001", nil
}

func (f *Fake) PostEphemeral(ctx context.Context, channelID, userID string, opts ...slack.MsgOption) error {
	f.record("PostEphemeral")
	if f.PostEphemeralFn != nil {
		return f.PostEphemeralFn(ctx, channelID, userID, opts...)
	}
	return nil
}

func (f *Fake) OpenView(ctx context.Context, triggerID string, view slack.ModalViewRequest) error {
	f.record("OpenView")
	if f.OpenViewFn != nil {
		return f.OpenViewFn(ctx, triggerID, view)
	}
	return nil
}

func (f *Fake) GetUserGroups(ctx context.Context) ([]slack.UserGroup, error) {
	f.record("GetUserGroups")
	if f.GetUserGroupsFn != nil {
		return f.GetUserGroupsFn(ctx)
	}
	return nil, nil
}

func (f *Fake) GetUserGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	f.record("GetUserGroupMembers")
	if f.GetUserGroupMembersFn != nil {
		return f.GetUserGroupMembersFn(ctx, groupID)
	}
	return nil, nil
}

func (f *Fake) GetConversationHistory(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	f.record("GetConversationHistory")
	if f.GetConversationHistoryFn != nil {
		return f.GetConversationHistoryFn(ctx, params)
	}
	return &slack.GetConversationHistoryResponse{}, nil
}

func (f *Fake) AuthTest(ctx context.Context) (*slack.AuthTestResponse, error) {
	f.record("AuthTest")
	if f.AuthTestFn != nil {
		return f.AuthTestFn(ctx)
	}
	return &slack.AuthTestResponse{UserID: "UBOT"}, nil
}

func (f *Fake) PostResponse(ctx context.Context, responseURL, text string) error {
	f.record("PostResponse")
	if f.PostResponseFn != nil {
		return f.PostResponseFn(ctx, responseURL, text)
	}
	return nil
}
