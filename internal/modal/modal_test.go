package modal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/command"
	"github.com/nlink-jp/ir-hub/internal/msg"
)

var meta = Metadata{ChannelID: "C123", UserID: "U001"}

func TestBuildActionPicker(t *testing.T) {
	view := BuildActionPicker(meta, &msg.EN)

	if view.CallbackID != CallbackAction {
		t.Errorf("CallbackID = %q", view.CallbackID)
	}
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Type   string `json:"type"`
		Blocks []struct {
			Type    string `json:"type"`
			BlockID string `json:"block_id"`
			Element struct {
				Type    string `json:"type"`
				Options []struct {
					Value string `json:"value"`
				} `json:"options"`
			} `json:"element"`
		} `json:"blocks"`
		PrivateMetadata string `json:"private_metadata"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "modal" {
		t.Errorf("type = %q", decoded.Type)
	}
	if len(decoded.Blocks) != 1 || decoded.Blocks[0].BlockID != "action" {
		t.Fatalf("blocks = %+v", decoded.Blocks)
	}
	if got := decoded.Blocks[0].Element.Type; got != "static_select" {
		t.Errorf("element type = %q", got)
	}
	if n := len(decoded.Blocks[0].Element.Options); n != 3 {
		t.Errorf("options = %d, want 3", n)
	}

	var gotMeta Metadata
	if err := json.Unmarshal([]byte(decoded.PrivateMetadata), &gotMeta); err != nil {
		t.Fatalf("private_metadata: %v", err)
	}
	if gotMeta != meta {
		t.Errorf("meta = %+v", gotMeta)
	}
}

func TestBuildNewCaseJapaneseLabels(t *testing.T) {
	view := BuildNewCase(meta, "private", &msg.JA)
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"新規案件", "タイトル", "重要度", "チャネル公開範囲", "案件を開設", "キャンセル"} {
		if !strings.Contains(s, want) {
			t.Errorf("JA view missing %q", want)
		}
	}
}

func TestBuildNewCase(t *testing.T) {
	view := BuildNewCase(meta, "private", &msg.EN)
	if view.CallbackID != CallbackNew {
		t.Errorf("CallbackID = %q", view.CallbackID)
	}
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Blocks []struct {
			BlockID string `json:"block_id"`
			Element struct {
				Type          string `json:"type"`
				MaxLength     int    `json:"max_length"`
				InitialOption *struct {
					Value string `json:"value"`
				} `json:"initial_option"`
			} `json:"element"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(decoded.Blocks))
	}
	byID := map[string]int{}
	for i, blk := range decoded.Blocks {
		byID[blk.BlockID] = i
	}
	title := decoded.Blocks[byID["title"]]
	if title.Element.Type != "plain_text_input" || title.Element.MaxLength != 150 {
		t.Errorf("title element = %+v", title.Element)
	}
	sev := decoded.Blocks[byID["severity"]]
	if sev.Element.InitialOption == nil || sev.Element.InitialOption.Value != command.DefaultSeverity {
		t.Errorf("severity initial = %+v", sev.Element.InitialOption)
	}
	vis := decoded.Blocks[byID["visibility"]]
	if vis.Element.Type != "radio_buttons" {
		t.Errorf("visibility element type = %q", vis.Element.Type)
	}
	if vis.Element.InitialOption == nil || vis.Element.InitialOption.Value != "private" {
		t.Errorf("visibility initial = %+v", vis.Element.InitialOption)
	}
}

// fakeView assembles a slack.View the way Slack delivers a
// view_submission payload.
func fakeView(callbackID, privateMetadata string, values map[string]map[string]slack.BlockAction) slack.View {
	v := slack.View{}
	v.CallbackID = callbackID
	v.PrivateMetadata = privateMetadata
	v.State = &slack.ViewState{Values: values}
	return v
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

func TestParseAction(t *testing.T) {
	view := fakeView(CallbackAction, encodeMeta(meta), map[string]map[string]slack.BlockAction{
		"action": {"value": selected("close")},
	})
	action, gotMeta, err := ParseAction(view)
	if err != nil {
		t.Fatalf("ParseAction: %v", err)
	}
	if action != ActionClose || gotMeta != meta {
		t.Errorf("action = %q, meta = %+v", action, gotMeta)
	}

	view = fakeView(CallbackAction, encodeMeta(meta), map[string]map[string]slack.BlockAction{
		"action": {"value": selected("nuke")},
	})
	if _, _, err := ParseAction(view); err == nil {
		t.Error("unexpected action must error")
	}

	view = fakeView(CallbackAction, "not json", nil)
	if _, _, err := ParseAction(view); err == nil {
		t.Error("broken metadata must error")
	}
}

func TestParseNewCase(t *testing.T) {
	view := fakeView(CallbackNew, encodeMeta(meta), map[string]map[string]slack.BlockAction{
		"title":      {"value": typed("  DB outage  ")},
		"severity":   {"value": selected("high")},
		"visibility": {"value": selected("public")},
	})
	args, gotMeta, fieldErrs, err := ParseNewCase(view, &msg.EN)
	if err != nil {
		t.Fatalf("ParseNewCase: %v", err)
	}
	if len(fieldErrs) != 0 {
		t.Fatalf("fieldErrs = %v", fieldErrs)
	}
	if gotMeta != meta {
		t.Errorf("meta = %+v", gotMeta)
	}
	want := command.NewArgs{Title: "DB outage", Severity: "high", Visibility: command.VisibilityPublic}
	if args != want {
		t.Errorf("args = %+v, want %+v", args, want)
	}
}

func TestParseNewCaseEmptyTitle(t *testing.T) {
	view := fakeView(CallbackNew, encodeMeta(meta), map[string]map[string]slack.BlockAction{
		"title":      {"value": typed("   ")},
		"severity":   {"value": selected("low")},
		"visibility": {"value": selected("private")},
	})
	_, _, fieldErrs, err := ParseNewCase(view, &msg.EN)
	if err != nil {
		t.Fatalf("ParseNewCase: %v", err)
	}
	if fieldErrs["title"] == "" {
		t.Errorf("fieldErrs = %v, want title error", fieldErrs)
	}
}

func TestParseNewCaseDefaults(t *testing.T) {
	// Missing severity/visibility selections fall back safely.
	view := fakeView(CallbackNew, encodeMeta(meta), map[string]map[string]slack.BlockAction{
		"title": {"value": typed("x")},
	})
	args, _, fieldErrs, err := ParseNewCase(view, &msg.EN)
	if err != nil || len(fieldErrs) != 0 {
		t.Fatalf("ParseNewCase: %v %v", err, fieldErrs)
	}
	if args.Severity != command.DefaultSeverity {
		t.Errorf("severity = %q", args.Severity)
	}
	if args.Visibility != command.VisibilityDefault {
		t.Errorf("visibility = %v", args.Visibility)
	}
}
