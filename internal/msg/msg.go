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
	CaseReopened       string // %04d case id, %s user id
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
	CaseOpenedNotice  string // %04d case id, %s channel id
	CaseOpenFailed    string // %v error
	ModalOpenFailed   string
	DeniedNotice      string
	CloseFailed       string // %v error (already localized where possible)
	ReopenFailed      string // %v error
	StatusFailed      string // %v error
	ErrNotCaseChannel string
	ErrCaseNotOpen    string
	ErrCaseNotClosed  string
	ModalActionReopen string

	// Knowledge Q&A + briefing.
	MentionEmptyQuestion string
	MentionAnswerFailed  string // %v error
	BriefingHeader       string

	// Knowledge export.
	ModalActionExport   string
	ExportStarted       string // %s backend name
	ExportDone          string // %d count
	ExportFailed        string // %v error
	ExportNotConfigured string

	// Command-parse errors.
	ErrUnknownSubcommand  string // %s subcommand
	ErrTakesNoArgs        string // %s subcommand
	ErrSeverityNeedsValue string // %s allowed list
	ErrInvalidSeverity    string // %s value, %s allowed list
	ErrTitleRequired      string
	ErrVisibilityConflict string
	ErrUnknownFlag        string // %s flag

	// Postmortem flow (bot posts).
	ModalActionPM     string
	PMStarted         string
	PMProgress        string // %d done, %d total
	PMNoMessages      string
	PMAlreadyRunning  string
	PMFailed          string // %v error
	PMCompactHeader   string // %04d case id, %s title
	PMCompactSeverity string // %s severity
	PMCompactScore    string // %d score
	PMCompactTactics  string // %d count
	PMCompactSee      string
	PMUploadFailed    string // %v error
	StatusGenerating  string
	StatusLLMFailed   string // %v error

	// Postmortem report rendering (Markdown).
	RptTitle         string // %04d case id, %s title
	RptSummary       string
	RptTimeline      string
	RptRootCause     string
	RptResolution    string
	RptReview        string
	RptScore         string // %d score (1-10)
	RptPhases        string
	RptCommunication string
	RptRoleClarity   string
	RptTools         string
	RptStrengths     string
	RptImprovements  string
	RptChecklist     string
	RptActivity      string
	RptRoles         string
	RptTactics       string
	RptTruncated     string // %d analyzed, %d total
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
		"the postmortem runs automatically from there.",
	KickoffPrivateNote: "• This channel is *private*: it cannot be converted to public " +
		"later, and ir-hub must remain a member to keep ingesting messages.",
	CaseClosed:        ":white_check_mark: *Case #%04d closed* by <@%s>.",
	CaseReopened:      ":arrows_counterclockwise: *Case #%04d reopened* by <@%s>.",
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
	CloseFailed:       ":warning: close failed: %v",
	ReopenFailed:      ":warning: reopen failed: %v",
	StatusFailed:      ":warning: status failed: %v",
	ErrNotCaseChannel: "this channel is not an ir-hub case channel",
	ErrCaseNotOpen:    "this case is not open",
	ErrCaseNotClosed:  "this case is not closed",
	ModalActionReopen: "Reopen this case",

	MentionEmptyQuestion: "Ask me a question, e.g. `@ir-hub how did we handle the last DB outage?`",
	MentionAnswerFailed:  ":warning: knowledge Q&A failed: %v",
	BriefingHeader:       ":books: *Related past knowledge*",

	ModalActionExport:   "Export knowledge",
	ExportStarted:       ":outbox_tray: Exporting knowledge to %s storage…",
	ExportDone:          ":white_check_mark: Exported %d knowledge document(s).",
	ExportFailed:        ":warning: export failed: %v",
	ExportNotConfigured: "Storage export is not configured.",

	ErrUnknownSubcommand:  "unknown subcommand %q (expected: new, close, reopen, status, pm, export)",
	ErrTakesNoArgs:        "%q takes no arguments",
	ErrSeverityNeedsValue: "--severity requires a value (%s)",
	ErrInvalidSeverity:    "invalid severity %q (expected: %s)",
	ErrTitleRequired:      "new requires a title: /ir-hub new <title> [--severity <lv>] [--private|--public]",
	ErrVisibilityConflict: "--private and --public are mutually exclusive",
	ErrUnknownFlag:        "unknown flag %q",

	ModalActionPM:     "Run postmortem",
	PMStarted:         ":hourglass_flowing_sand: Postmortem analysis started — this usually takes a few minutes.",
	PMProgress:        ":hourglass_flowing_sand: Analyzing the incident… (%d/%d stages complete)",
	PMNoMessages:      "this case has no ingested messages to analyze",
	PMAlreadyRunning:  "a postmortem run is already in progress for this case",
	PMFailed:          ":warning: postmortem failed: %v — retry with `/ir-hub pm`",
	PMCompactHeader:   ":memo: *Postmortem: Case #%04d — %s*",
	PMCompactSeverity: "• Assessed severity: `%s`",
	PMCompactScore:    "• Process score: %d/10",
	PMCompactTactics:  "• Extracted tactics: %d",
	PMCompactSee:      "The full report is attached as a snippet.",
	PMUploadFailed:    ":warning: could not attach the full report: %v — the report is stored; `/ir-hub pm` regenerates it",
	StatusGenerating:  "_(generating the situation summary…)_",
	StatusLLMFailed:   ":warning: situation summary failed: %v",

	RptTitle:         "# Postmortem: Case #%04d — %s",
	RptSummary:       "## Summary",
	RptTimeline:      "## Timeline",
	RptRootCause:     "## Root cause",
	RptResolution:    "## Resolution",
	RptReview:        "## Process review",
	RptScore:         "Overall score: %d/10",
	RptPhases:        "Phases",
	RptCommunication: "Communication",
	RptRoleClarity:   "Role clarity",
	RptTools:         "Tool appropriateness",
	RptStrengths:     "Strengths",
	RptImprovements:  "Improvements",
	RptChecklist:     "Next-incident checklist",
	RptActivity:      "## Participant activity",
	RptRoles:         "## Roles",
	RptTactics:       "## Extracted tactics",
	RptTruncated:     "_Note: the analysis covered the newest %d of %d messages._",
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
		"ポストモーテムが自動で実行されます。",
	KickoffPrivateNote: "• このチャネルは*プライベート*です: 後から public へは変更できません。" +
		"メッセージ取り込み継続のため ir-hub をメンバーから外さないでください。",
	CaseClosed:        ":white_check_mark: *案件 #%04d をクローズしました*(<@%s>)",
	CaseReopened:      ":arrows_counterclockwise: *案件 #%04d を再オープンしました*(<@%s>)",
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
	CloseFailed:       ":warning: クローズできませんでした: %v",
	ReopenFailed:      ":warning: 再オープンできませんでした: %v",
	StatusFailed:      ":warning: ステータスを取得できませんでした: %v",
	ErrNotCaseChannel: "このチャネルは ir-hub の案件チャネルではありません",
	ErrCaseNotOpen:    "この案件は open 状態ではありません",
	ErrCaseNotClosed:  "この案件は closed 状態ではありません",
	ModalActionReopen: "この案件を再オープン",

	MentionEmptyQuestion: "質問を入力してください。例: `@ir-hub 前回の DB 障害はどう対応した?`",
	MentionAnswerFailed:  ":warning: 知見 Q&A に失敗しました: %v",
	BriefingHeader:       ":books: *関連する過去の知見*",

	ModalActionExport:   "知見をエクスポート",
	ExportStarted:       ":outbox_tray: 知見を %s ストレージにエクスポート中…",
	ExportDone:          ":white_check_mark: %d 件の知見ドキュメントをエクスポートしました。",
	ExportFailed:        ":warning: エクスポートに失敗しました: %v",
	ExportNotConfigured: "ストレージ出力が設定されていません。",

	ErrUnknownSubcommand:  "未知のサブコマンド %q です(利用可能: new, close, reopen, status, pm, export)",
	ErrTakesNoArgs:        "%q は引数を取りません",
	ErrSeverityNeedsValue: "--severity には値が必要です(%s)",
	ErrInvalidSeverity:    "不正な重要度 %q です(利用可能: %s)",
	ErrTitleRequired:      "new にはタイトルが必要です: /ir-hub new <タイトル> [--severity <lv>] [--private|--public]",
	ErrVisibilityConflict: "--private と --public は同時に指定できません",
	ErrUnknownFlag:        "未知のフラグ %q です",

	ModalActionPM:     "ポストモーテムを実行",
	PMStarted:         ":hourglass_flowing_sand: ポストモーテム分析を開始しました — 通常数分かかります。",
	PMProgress:        ":hourglass_flowing_sand: インシデントを分析中… (%d/%d ステージ完了)",
	PMNoMessages:      "この案件には分析対象のメッセージがありません",
	PMAlreadyRunning:  "この案件のポストモーテムは既に実行中です",
	PMFailed:          ":warning: ポストモーテムに失敗しました: %v — `/ir-hub pm` で再実行できます",
	PMCompactHeader:   ":memo: *ポストモーテム: 案件 #%04d — %s*",
	PMCompactSeverity: "• 評価された重要度: `%s`",
	PMCompactScore:    "• プロセススコア: %d/10",
	PMCompactTactics:  "• 抽出タクティック: %d 件",
	PMCompactSee:      "全文レポートはスニペットとして添付しています。",
	PMUploadFailed:    ":warning: 全文レポートを添付できませんでした: %v — レポートは保存済みで、`/ir-hub pm` で再生成できます",
	StatusGenerating:  "_(状況サマリを生成中…)_",
	StatusLLMFailed:   ":warning: 状況サマリの生成に失敗しました: %v",

	RptTitle:         "# ポストモーテム: 案件 #%04d — %s",
	RptSummary:       "## サマリ",
	RptTimeline:      "## タイムライン",
	RptRootCause:     "## 根本原因",
	RptResolution:    "## 対応・解決",
	RptReview:        "## プロセス評価",
	RptScore:         "総合スコア: %d/10",
	RptPhases:        "フェーズ",
	RptCommunication: "コミュニケーション",
	RptRoleClarity:   "ロールの明確さ",
	RptTools:         "ツール適切性",
	RptStrengths:     "良かった点",
	RptImprovements:  "改善点",
	RptChecklist:     "次回への準備チェックリスト",
	RptActivity:      "## 参加者アクティビティ",
	RptRoles:         "## ロール",
	RptTactics:       "## 抽出タクティック",
	RptTruncated:     "_注: 分析対象は新しい %d 件のメッセージです(全 %d 件中)。_",
}
