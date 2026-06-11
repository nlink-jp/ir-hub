// Package modal builds the Block Kit modals shown when /ir-hub is
// invoked without arguments, and parses their view_submission
// payloads back into the same argument types the direct slash
// command path uses — so both entrances share one code path into
// the cases service.
//
// The invoking channel does not travel with view_submission events,
// so it is round-tripped through the view's private_metadata.
package modal

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/slack-go/slack"

	"github.com/nlink-jp/ir-hub/internal/command"
	"github.com/nlink-jp/ir-hub/internal/msg"
)

// Callback IDs routing view_submission events.
const (
	CallbackAction = "irhub_action"
	CallbackNew    = "irhub_new"
)

// Actions offered by the picker.
const (
	ActionNew    = "new"
	ActionClose  = "close"
	ActionStatus = "status"
	ActionPM     = "pm"
)

// Block / action IDs.
const (
	blockAction     = "action"
	blockTitle      = "title"
	blockSeverity   = "severity"
	blockVisibility = "visibility"
	actionIDValue   = "value"
)

// Metadata is round-tripped through private_metadata.
type Metadata struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
}

func encodeMeta(m Metadata) string {
	b, _ := json.Marshal(m) // struct of two strings cannot fail
	return string(b)
}

func decodeMeta(s string) (Metadata, error) {
	var m Metadata
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return Metadata{}, fmt.Errorf("decode private_metadata: %w", err)
	}
	return m, nil
}

func plainText(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.PlainTextType, s, false, false)
}

// BuildActionPicker is the first-stage modal: choose an action.
func BuildActionPicker(meta Metadata, cat *msg.Catalog) slack.ModalViewRequest {
	options := []*slack.OptionBlockObject{
		slack.NewOptionBlockObject(ActionNew, plainText(cat.ModalActionNew), nil),
		slack.NewOptionBlockObject(ActionClose, plainText(cat.ModalActionClose), nil),
		slack.NewOptionBlockObject(ActionStatus, plainText(cat.ModalActionStatus), nil),
		slack.NewOptionBlockObject(ActionPM, plainText(cat.ModalActionPM), nil),
	}
	sel := slack.NewOptionsSelectBlockElement(
		slack.OptTypeStatic, plainText(cat.ModalActionPlaceholder), actionIDValue, options...)

	return slack.ModalViewRequest{
		Type:            slack.ViewType("modal"),
		CallbackID:      CallbackAction,
		PrivateMetadata: encodeMeta(meta),
		Title:           plainText(cat.ModalTitle),
		Submit:          plainText(cat.ModalNext),
		Close:           plainText(cat.ModalCancel),
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			slack.NewInputBlock(blockAction, plainText(cat.ModalActionLabel), nil, sel),
		}},
	}
}

// BuildNewCase is the second-stage modal: parameters for "new".
// defaultVisibility preselects the configured channel visibility.
func BuildNewCase(meta Metadata, defaultVisibility string, cat *msg.Catalog) slack.ModalViewRequest {
	title := slack.NewPlainTextInputBlockElement(plainText(cat.ModalNewTitlePlaceholder), actionIDValue)
	title.MaxLength = 150

	sevOptions := make([]*slack.OptionBlockObject, 0, len(command.Severities))
	var sevInitial *slack.OptionBlockObject
	for _, s := range command.Severities {
		o := slack.NewOptionBlockObject(s, plainText(s), nil)
		sevOptions = append(sevOptions, o)
		if s == command.DefaultSeverity {
			sevInitial = o
		}
	}
	sev := slack.NewOptionsSelectBlockElement(
		slack.OptTypeStatic, plainText(cat.ModalSeverityLabel), actionIDValue, sevOptions...)
	sev.InitialOption = sevInitial

	visOptions := []*slack.OptionBlockObject{
		slack.NewOptionBlockObject("private", plainText(cat.ModalVisibilityPrivate), nil),
		slack.NewOptionBlockObject("public", plainText(cat.ModalVisibilityPublic), nil),
	}
	vis := slack.NewRadioButtonsBlockElement(actionIDValue, visOptions...)
	for _, o := range visOptions {
		if o.Value == defaultVisibility {
			vis.InitialOption = o
		}
	}

	return slack.ModalViewRequest{
		Type:            slack.ViewType("modal"),
		CallbackID:      CallbackNew,
		PrivateMetadata: encodeMeta(meta),
		Title:           plainText(cat.ModalNewTitle),
		Submit:          plainText(cat.ModalOpenCase),
		Close:           plainText(cat.ModalCancel),
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			slack.NewInputBlock(blockTitle, plainText(cat.ModalNewTitleLabel), nil, title),
			slack.NewInputBlock(blockSeverity, plainText(cat.ModalSeverityLabel), nil, sev),
			slack.NewInputBlock(blockVisibility, plainText(cat.ModalVisibilityLabel), nil, vis),
		}},
	}
}

func stateValue(view slack.View, blockID string) slack.BlockAction {
	if view.State == nil {
		return slack.BlockAction{}
	}
	return view.State.Values[blockID][actionIDValue]
}

// ParseAction extracts the chosen action from an irhub_action
// submission.
func ParseAction(view slack.View) (string, Metadata, error) {
	meta, err := decodeMeta(view.PrivateMetadata)
	if err != nil {
		return "", Metadata{}, err
	}
	action := stateValue(view, blockAction).SelectedOption.Value
	switch action {
	case ActionNew, ActionClose, ActionStatus, ActionPM:
		return action, meta, nil
	default:
		return "", Metadata{}, fmt.Errorf("unexpected action %q in submission", action)
	}
}

// ParseNewCase extracts NewArgs from an irhub_new submission.
// fieldErrors maps block IDs to user-facing messages (in the
// catalog's language) and is non-empty for input the user must fix
// (title empty); err reports structural problems.
func ParseNewCase(view slack.View, cat *msg.Catalog) (args command.NewArgs, meta Metadata, fieldErrors map[string]string, err error) {
	meta, err = decodeMeta(view.PrivateMetadata)
	if err != nil {
		return command.NewArgs{}, Metadata{}, nil, err
	}

	args.Title = strings.TrimSpace(stateValue(view, blockTitle).Value)
	if args.Title == "" {
		return command.NewArgs{}, meta, map[string]string{blockTitle: cat.ModalTitleEmpty}, nil
	}

	args.Severity = stateValue(view, blockSeverity).SelectedOption.Value
	if args.Severity == "" {
		args.Severity = command.DefaultSeverity
	}

	switch stateValue(view, blockVisibility).SelectedOption.Value {
	case "private":
		args.Visibility = command.VisibilityPrivate
	case "public":
		args.Visibility = command.VisibilityPublic
	default:
		args.Visibility = command.VisibilityDefault
	}
	return args, meta, nil, nil
}
