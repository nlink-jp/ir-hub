package slackapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

// newTestAdapter spins up an httptest server that records the last
// request per Slack method and returns canned JSON. Form-encoded
// requests land in forms; JSON-body requests (e.g. views.open) land
// in bodies.
func newTestAdapter(t *testing.T, responses map[string]string) (*Adapter, *map[string]url.Values, *map[string][]byte) {
	t.Helper()
	forms := map[string]url.Values{}
	bodies := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			b, _ := io.ReadAll(r.Body)
			bodies[r.URL.Path] = b
		} else {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			forms[r.URL.Path] = r.Form
		}
		w.Header().Set("Content-Type", "application/json")
		if body, ok := responses[r.URL.Path]; ok {
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(`{"ok": true}`))
	}))
	t.Cleanup(srv.Close)
	c := slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))
	return NewAdapter(c), &forms, &bodies
}

func TestCreateConversation(t *testing.T) {
	a, got, _ := newTestAdapter(t, map[string]string{
		"/conversations.create": `{"ok": true, "channel": {"id": "C123", "name": "ir-0001-x"}}`,
	})
	ch, err := a.CreateConversation(context.Background(), "ir-0001-x", true)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if ch.ID != "C123" {
		t.Errorf("channel ID = %q, want C123", ch.ID)
	}
	form := (*got)["/conversations.create"]
	if form.Get("name") != "ir-0001-x" {
		t.Errorf("name = %q", form.Get("name"))
	}
	if form.Get("is_private") != "true" {
		t.Errorf("is_private = %q, want true", form.Get("is_private"))
	}
}

func TestInviteUsers(t *testing.T) {
	a, got, _ := newTestAdapter(t, map[string]string{
		"/conversations.invite": `{"ok": true, "channel": {"id": "C123"}}`,
	})
	if err := a.InviteUsers(context.Background(), "C123", "U1", "U2"); err != nil {
		t.Fatalf("InviteUsers: %v", err)
	}
	form := (*got)["/conversations.invite"]
	if form.Get("channel") != "C123" {
		t.Errorf("channel = %q", form.Get("channel"))
	}
	if form.Get("users") != "U1,U2" {
		t.Errorf("users = %q, want U1,U2", form.Get("users"))
	}
}

func TestPostMessage(t *testing.T) {
	a, got, _ := newTestAdapter(t, map[string]string{
		"/chat.postMessage": `{"ok": true, "channel": "C123", "ts": "1718000000.000100"}`,
	})
	ts, err := a.PostMessage(context.Background(), "C123", slack.MsgOptionText("hello", false))
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if ts != "1718000000.000100" {
		t.Errorf("ts = %q", ts)
	}
	if (*got)["/chat.postMessage"].Get("text") != "hello" {
		t.Errorf("text = %q", (*got)["/chat.postMessage"].Get("text"))
	}
}

func TestPostMessageError(t *testing.T) {
	a, _, _ := newTestAdapter(t, map[string]string{
		"/chat.postMessage": `{"ok": false, "error": "channel_not_found"}`,
	})
	if _, err := a.PostMessage(context.Background(), "CNOPE", slack.MsgOptionText("x", false)); err == nil {
		t.Fatal("PostMessage: want error from ok=false response")
	}
}

func TestOpenView(t *testing.T) {
	a, _, bodies := newTestAdapter(t, map[string]string{
		"/views.open": `{"ok": true, "view": {"id": "V1"}}`,
	})
	view := slack.ModalViewRequest{
		Type:  slack.ViewType("modal"),
		Title: slack.NewTextBlockObject(slack.PlainTextType, "ir-hub", false, false),
	}
	if err := a.OpenView(context.Background(), "trigger-1", view); err != nil {
		t.Fatalf("OpenView: %v", err)
	}
	var sent struct {
		TriggerID string `json:"trigger_id"`
		View      struct {
			Type string `json:"type"`
		} `json:"view"`
	}
	if err := json.Unmarshal((*bodies)["/views.open"], &sent); err != nil {
		t.Fatalf("views.open payload not JSON: %v", err)
	}
	if sent.TriggerID != "trigger-1" {
		t.Errorf("trigger_id = %q", sent.TriggerID)
	}
	if sent.View.Type != "modal" {
		t.Errorf("view.type = %q, want modal", sent.View.Type)
	}
}

func TestGetUserGroupMembers(t *testing.T) {
	a, got, _ := newTestAdapter(t, map[string]string{
		"/usergroups.users.list": `{"ok": true, "users": ["U1", "U2"]}`,
	})
	users, err := a.GetUserGroupMembers(context.Background(), "S123")
	if err != nil {
		t.Fatalf("GetUserGroupMembers: %v", err)
	}
	if len(users) != 2 || users[0] != "U1" {
		t.Errorf("users = %v", users)
	}
	if (*got)["/usergroups.users.list"].Get("usergroup") != "S123" {
		t.Errorf("usergroup = %q", (*got)["/usergroups.users.list"].Get("usergroup"))
	}
}

func TestGetConversationHistory(t *testing.T) {
	a, got, _ := newTestAdapter(t, map[string]string{
		"/conversations.history": `{"ok": true, "messages": [{"type": "message", "ts": "1718000000.000100", "text": "hi"}], "has_more": false}`,
	})
	resp, err := a.GetConversationHistory(context.Background(), &slack.GetConversationHistoryParameters{
		ChannelID: "C123", Oldest: "1717000000.000000", Limit: 200,
	})
	if err != nil {
		t.Fatalf("GetConversationHistory: %v", err)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].Timestamp != "1718000000.000100" {
		t.Errorf("messages = %+v", resp.Messages)
	}
	form := (*got)["/conversations.history"]
	if form.Get("oldest") != "1717000000.000000" {
		t.Errorf("oldest = %q", form.Get("oldest"))
	}
}

func TestAuthTest(t *testing.T) {
	a, _, _ := newTestAdapter(t, map[string]string{
		"/auth.test": `{"ok": true, "user_id": "UBOT", "bot_id": "B1", "user": "ir-hub"}`,
	})
	resp, err := a.AuthTest(context.Background())
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if resp.UserID != "UBOT" {
		t.Errorf("UserID = %q, want UBOT", resp.UserID)
	}
}
