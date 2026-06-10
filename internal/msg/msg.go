// Package msg holds the user-facing message catalogs. Every string
// a Slack user can see — modal labels, kickoff/close/status posts,
// bot replies, command-parse errors — lives here in English and
// Japanese variants, selected by the `language` config key.
//
// Log messages are NOT in the catalog: logs stay English.
package msg

import "fmt"

// Languages supported by the catalog.
const (
	LangEN = "en"
	LangJA = "ja"
)

// Catalog is the full set of user-facing strings. Fields ending in
// a format verb comment are fmt templates; keep verb order intact
// when translating.
type Catalog struct {
	// Modal: action picker.
	ModalTitle             string
	ModalActionLabel       string
	ModalActionPlaceholder string
	ModalActionNew         string
	ModalActionClose       string
	ModalActionStatus      string
	ModalNext              string
	ModalCancel            string

	// Modal: new-case form.
	ModalNewTitle            string
	ModalNewTitleLabel       string
	ModalNewTitlePlaceholder string
	ModalSeverityLabel       string
	ModalVisibilityLabel     string
	ModalVisibilityPrivate   string
	ModalVisibilityPublic    string
	ModalOpenCase            string
	ModalTitleEmpty          string

	// Case lifecycle posts.
	KickoffHeader      string // %04d case id, %s title
	KickoffSeverity    string // %s severity
	KickoffOpenedBy    string // %s user id
	KickoffCloseHint   string
	KickoffPrivateNote string
	CaseClosed         string // %04d case id, %s user id
	WarnInviteFailed   string // %s user id, %v error
	WarnKickoffFailed  string // %v error

	// Status post.
	StatusHeader           string // %04d case id, %s title
	StatusState            string // %s state
	StatusSeverity         string // %s severity
	StatusOpened           string // %s time, %s user id
	StatusClosed           string // %s time, %s user id
	StatusDuration         string // %s duration
	StatusOpenFor          string // %s duration
	StatusMessages         string // %d count
	DurationLessThanMinute string

	// Bot replies.
	CaseOpenedNotice string // %04d case id, %s channel id
	CaseOpenFailed   string // %v error
	ModalOpenFailed  string
	DeniedNotice     string
	MentionNotReady  string
	CloseFailed      string // %v error (already localized where possible)
	StatusFailed     string // %v error
	ErrNotCaseChannel string
	ErrCaseNotOpen    string

	// Command-parse errors.
	ErrUnknownSubcommand  string // %s subcommand
	ErrTakesNoArgs        string // %s subcommand
	ErrSeverityNeedsValue string // %s allowed list
	ErrInvalidSeverity    string // %s value, %s allowed list
	ErrTitleRequired      string
	ErrVisibilityConflict string
	ErrUnknownFlag        string // %s flag
}

// For returns the catalog for a language ("ja" → JA, anything else
// → EN).
func For(lang string) *Catalog {
	if lang == LangJA {
		return &JA
	}
	return &EN
}

// F is fmt.Sprintf — a tiny alias so call sites read as
// cat.F(cat.KickoffHeader, …) without importing fmt everywhere.
func (c *Catalog) F(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

var EN = Catalog{
	ModalTitle:             "ir-hub",
	ModalActionLabel:       "Action",
	ModalActionPlaceholder: "Select an action",
	ModalActionNew:         "Open a new case",
	ModalActionClose:       "Close this case",
	ModalActionStatus:      "Show case status",
	ModalNext:              "Next",
	ModalCancel:            "Cancel",

	ModalNewTitle:            "New case",
	ModalNewTitleLabel:       "Title",
	ModalNewTitlePlaceholder: "e.g. DB outage in production",
	ModalSeverityLabel:       "Severity",
	ModalVisibilityLabel:     "Channel visibility",
	ModalVisibilityPrivate:   "Private channel",
	ModalVisibilityPublic:    "Public channel",
	ModalOpenCase:            "Open case",
	ModalTitleEmpty:          "Title must not be empty",

	KickoffHeader:   ":rotating_light: *Case #%04d opened: %s*",
	KickoffSeverity: "• Severity: `%s`",
	KickoffOpenedBy: "• Opened by: <@%s>",
	KickoffCloseHint: "• Close with `/ir-hub close` when the response is done — " +
		"the postmortem will run from there (Phase 2).",
	KickoffPrivateNote: "• This channel is *private*: it cannot be converted to public " +
		"later, and ir-hub must remain a member to keep ingesting messages.",
	CaseClosed:        ":white_check_mark: *Case #%04d closed* by <@%s>.",
	WarnInviteFailed:  "could not invite <@%s>: %v",
	WarnKickoffFailed: "could not post kickoff message: %v",

	StatusHeader:           "*Case #%04d: %s*",
	StatusState:            "• State: `%s`",
	StatusSeverity:         "• Severity: `%s`",
	StatusOpened:           "• Opened: %s by <@%s>",
	StatusClosed:           "• Closed: %s by <@%s>",
	StatusDuration:         "• Duration: %s",
	StatusOpenFor:          "• Open for: %s",
	StatusMessages:         "• Ingested messages: %d",
	DurationLessThanMinute: "less than a minute",

	CaseOpenedNotice: ":white_check_mark: Case #%04d opened: <#%s>",
	CaseOpenFailed:   ":warning: could not open the case: %v",
	ModalOpenFailed:  ":warning: could not open the dialog, please retry",
	DeniedNotice: "You are not authorized to use ir-hub. " +
		"Contact the IR team if you believe this is a mistake.",
	MentionNotReady: "Knowledge Q&A is not available yet (coming in a later phase). " +
		"Use `/ir-hub status` for the current case state.",
	CloseFailed:       ":warning: close failed: %v",
	StatusFailed:      ":warning: status failed: %v",
	ErrNotCaseChannel: "this channel is not an ir-hub case channel",
	ErrCaseNotOpen:    "this case is not open",

	ErrUnknownSubcommand:  "unknown subcommand %q (expected: new, close, status)",
	ErrTakesNoArgs:        "%q takes no arguments",
	ErrSeverityNeedsValue: "--severity requires a value (%s)",
	ErrInvalidSeverity:    "invalid severity %q (expected: %s)",
	ErrTitleRequired:      "new requires a title: /ir-hub new <title> [--severity <lv>] [--private|--public]",
	ErrVisibilityConflict: "--private and --public are mutually exclusive",
	ErrUnknownFlag:        "unknown flag %q",
}

var JA = Catalog{
	ModalTitle:             "ir-hub",
	ModalActionLabel:       "操作",
	ModalActionPlaceholder: "操作を選択",
	ModalActionNew:         "新規案件を開設",
	ModalActionClose:       "この案件をクローズ",
	ModalActionStatus:      "案件ステータスを表示",
	ModalNext:              "次へ",
	ModalCancel:            "キャンセル",

	ModalNewTitle:            "新規案件",
	ModalNewTitleLabel:       "タイトル",
	ModalNewTitlePlaceholder: "例: 本番環境での情報漏えい",
	ModalSeverityLabel:       "重要度",
	ModalVisibilityLabel:     "チャネル公開範囲",
	ModalVisibilityPrivate:   "プライベートチャネル",
	ModalVisibilityPublic:    "パブリックチャネル",
	ModalOpenCase:            "案件を開設",
	ModalTitleEmpty:          "タイトルを入力してください",

	KickoffHeader:   ":rotating_light: *案件 #%04d を開設: %s*",
	KickoffSeverity: "• 重要度: `%s`",
	KickoffOpenedBy: "• 起票者: <@%s>",
	KickoffCloseHint: "• 対応が完了したら `/ir-hub close` でクローズしてください — " +
		"ポストモーテムはそこから実行されます(Phase 2)。",
	KickoffPrivateNote: "• このチャネルは*プライベート*です: 後から public へは変更できません。" +
		"メッセージ取り込み継続のため ir-hub をメンバーから外さないでください。",
	CaseClosed:        ":white_check_mark: *案件 #%04d をクローズしました*(<@%s>)",
	WarnInviteFailed:  "<@%s> を招待できませんでした: %v",
	WarnKickoffFailed: "キックオフメッセージを投稿できませんでした: %v",

	StatusHeader:           "*案件 #%04d: %s*",
	StatusState:            "• 状態: `%s`",
	StatusSeverity:         "• 重要度: `%s`",
	StatusOpened:           "• 開設: %s(<@%s>)",
	StatusClosed:           "• クローズ: %s(<@%s>)",
	StatusDuration:         "• 所要時間: %s",
	StatusOpenFor:          "• 経過時間: %s",
	StatusMessages:         "• 取り込みメッセージ数: %d",
	DurationLessThanMinute: "1分未満",

	CaseOpenedNotice: ":white_check_mark: 案件 #%04d を開設しました: <#%s>",
	CaseOpenFailed:   ":warning: 案件を開設できませんでした: %v",
	ModalOpenFailed:  ":warning: ダイアログを開けませんでした。もう一度お試しください",
	DeniedNotice: "ir-hub を利用する権限がありません。" +
		"心当たりがない場合は IR チームへお問い合わせください。",
	MentionNotReady: "知見 Q&A はまだ利用できません(後続フェーズで提供予定)。" +
		"案件の状況確認には `/ir-hub status` を利用してください。",
	CloseFailed:       ":warning: クローズできませんでした: %v",
	StatusFailed:      ":warning: ステータスを取得できませんでした: %v",
	ErrNotCaseChannel: "このチャネルは ir-hub の案件チャネルではありません",
	ErrCaseNotOpen:    "この案件は open 状態ではありません",

	ErrUnknownSubcommand:  "未知のサブコマンド %q です(利用可能: new, close, status)",
	ErrTakesNoArgs:        "%q は引数を取りません",
	ErrSeverityNeedsValue: "--severity には値が必要です(%s)",
	ErrInvalidSeverity:    "不正な重要度 %q です(利用可能: %s)",
	ErrTitleRequired:      "new にはタイトルが必要です: /ir-hub new <タイトル> [--severity <lv>] [--private|--public]",
	ErrVisibilityConflict: "--private と --public は同時に指定できません",
	ErrUnknownFlag:        "未知のフラグ %q です",
}
