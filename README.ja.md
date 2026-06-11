# ir-hub

インシデントレスポンス・ライフサイクルハブ — IR ライフサイクル全体を LLM で
支援するワンパッケージの Slack ChatOps Bot。案件チャネル開設、対応中の支援、
ポストモーテム分析、知見の蓄積と再利用までを一気通貫でカバーします。

[English README is here](README.md)

> **Status: 機能完成(Phase 1–3)。** 案件ライフサイクル管理、LLM
> ポストモーテム(クローズ時自動+再実行可)、LLM 状況サマリ、知見
> ドキュメント生成に加え、知見の再利用 — `@ir-hub` Q&A、初動
> ブリーフィング、プラガブルなストレージ出力(local / GCS / S3)。
> 承認済み設計は [RFP](docs/ja/ir-hub-rfp.ja.md) を参照。

## コンセプト

```
/ir-hub new ──→ 案件チャネル ──→ 対応活動 ──→ /ir-hub close
     │             (状況サマリ / Q&A 支援)         │
     │                                            ▼
     └── 初動ブリーフィング ◀── 知見ストア ◀── ポストモーテム
         (過去案件から)        (JSON + Markdown,   (自動+再実行可)
                                local / GCS / S3)
```

- **Slack 上の案件ライフサイクル** — `/ir-hub new` で案件ごとの専用チャネルを
  開設、`/ir-hub close` でクローズしポストモーテムを自動実行
- **LLM ポストモーテム(ai-ir2 理論を再構築)** — 5 段階分析(サマリ /
  参加者アクティビティ / ロール推定 / タクティック抽出 / プロセス評価)を
  Vertex AI Gemini で実行。ノンスタグによるプロンプトインジェクション防御と、
  LLM 前後両方での IoC defang。分析は英語カノニカル、チャネル投稿は
  設定言語へ翻訳
- **知見ドキュメント** — 抽出タクティックごとに JSON + Markdown ペアを生成し、
  ポストモーテム確定時に DB へインデックス(タグ / カテゴリ / 要約)登録。
  再実行で当該案件の知見は置換
- **知見の再利用** — `@ir-hub <質問>` で蓄積知見から回答(タクティック ID を
  引用)、`/ir-hub new` 時に関連する過去タクティックをブリーフィング投稿、
  知見を JSON + Markdown で local / GCS / S3 にエクスポート(`/ir-hub export`
  およびポストモーテム後に自動)。IR チーム外も参照可能
- **デュアルモードのスラッシュコマンド** — 引数ありで直接実行、引数なしの
  `/ir-hub` はモーダルから操作を選択
- **ACL 内蔵** — ユーザー ID 単位+Slack User Group 単位の Whitelist +
  Blacklist。フェイルセーフ(Whitelist 未設定 = 全拒否)、拒否は silent で
  監査ログに記録。数万メンバー規模のワークスペースを想定した設計
- **継続取り込み** — オープン中の案件チャネルのメッセージを内蔵 SQLite に
  リアルタイム保存。再接続後は履歴から自動 backfill
- **Go 単一バイナリ** — Socket Mode 常駐 Bot。インバウンド公開不要、
  ランタイム依存なし

## コマンド

| コマンド | 動作 |
|---|---|
| `/ir-hub` | モーダルで操作を選択(パラメータを覚える必要なし) |
| `/ir-hub new <title> [--severity low\|medium\|high\|critical] [--private\|--public]` | 案件チャネル作成、起票者を招待、キックオフ投稿 |
| `/ir-hub status` | 案件メタデータに続けて LLM 状況サマリ(現状 / 未解決事項 / 次アクション)を投稿 |
| `/ir-hub close` | 案件をクローズし、ポストモーテムを自動実行(案件チャネル内で実行) |
| `/ir-hub reopen` | クローズ済み案件を再オープン(メッセージ取り込みを再開) |
| `/ir-hub pm` | ポストモーテムを手動(再)実行 — 当該案件の知見ドキュメントを置換 |
| `/ir-hub export` | 全知見ドキュメントを設定済みストレージにエクスポート |
| `@ir-hub <質問>` | 知見 Q&A — 蓄積知見から回答(タクティック ID を引用) |

ポストモーテムはコンパクトな要約(重要度、プロセススコア、良かった点/
改善点ダイジェスト、タクティック数)を投稿し、全文 Markdown レポートを
スニペットとして添付します。

## 前提条件

- Go 1.26+(ビルド)
- Socket Mode を有効化した Slack アプリ(下記)
- Vertex AI API を有効化した GCP プロジェクトと Application Default
  Credentials(`gcloud auth application-default login`)—
  ポストモーテムと状況サマリは Gemini で実行されます
- 知見エクスポート用: ローカルディレクトリ(既定)、または GCS バケット
  (ADC)/ S3 バケット(AWS 既定認証チェーン)。クラウドクライアントが
  初期化できない場合はエクスポートを無効化して Bot は稼働継続します

## Slack アプリの設定

> 完全な手引き — スコープの正当性説明、管理者承認、トークンの取り扱い、
> 動作検証チェックリスト、トラブルシューティング — は
> [Slack アプリ構成ハンドブック](docs/ja/slack-app-setup.ja.md) を参照。

以下のマニフェストからアプリを作成します(App settings → App Manifest):

```yaml
display_information:
  name: ir-hub
  description: >-
    インシデント対応のライフサイクルハブ。案件チャネルの開設、対応支援、
    ポストモーテム、知見の再利用を一気通貫で支援します。
  long_description: |-
    ir-hub はセキュリティインシデント対応チームのためのライフサイクル
    ハブです。

    ・/ir-hub new — 案件専用チャネルを開設し、起票者を招待して
      キックオフを投稿
    ・/ir-hub status — 案件の現在状況を要約
    ・/ir-hub close — 対応を終了し、ポストモーテムを自動実行
    ・対応から得られた知見を蓄積し、以降の案件で再利用

    利用はアプリ内の許可リストにより IR チームに限定され、拒否は監査
    ログに記録されます。メッセージの取り込みは ir-hub 自身が作成した
    案件チャネルのみが対象です。

    運用: <your IR team> / 問い合わせ: #<your-contact-channel>
features:
  bot_user:
    display_name: ir-hub
    always_online: true
  slash_commands:
    - command: /ir-hub
      description: Incident response lifecycle hub
      usage_hint: "new <title> | close | status"
      should_escape: false
oauth_config:
  scopes:
    bot:
      - commands
      - chat:write
      - app_mentions:read
      - users:read
      - usergroups:read
      - channels:manage
      - channels:read
      - channels:history
      - channels:join
      - groups:write
      - groups:read
      - groups:history
      - files:write
      - reactions:write
settings:
  event_subscriptions:
    bot_events:
      - app_mention
      - message.channels
      - message.groups
  interactivity:
    is_enabled: true
  socket_mode_enabled: true
```

続いて以下を発行します:

1. **App-level トークン**(Basic Information → App-Level Tokens)に
   `connections:write` スコープ → `IRHUB_SLACK_APP_TOKEN`
2. **Bot トークン**(Install App)→ `IRHUB_SLACK_BOT_TOKEN`

## インストール

```sh
git clone https://github.com/nlink-jp/ir-hub.git
cd ir-hub
make build          # → dist/ir-hub
```

## 設定

[`config.example.toml`](config.example.toml) を
`~/.config/ir-hub/config.toml` にコピーして編集します(または `--config`)。
全フィールドは `IRHUB_*` 環境変数で上書き可能です
(例: `IRHUB_ACL_ALLOW_GROUPS=ir-team,secops`)。設定ファイル内の未知の
キーはエラーになるため、typo は即座に検出されます。

`language = "ja"`(または `IRHUB_LANGUAGE=ja`)を設定すると、ユーザーに
見えるすべてのメッセージ — モーダル、キックオフ/ステータス/クローズ投稿、
エラー — が日本語になります。ログは英語のままです。

Slack トークンは設定ファイルの `[slack]` セクションまたは環境変数で
指定します(環境変数が優先):

| 環境変数 | 説明 |
|---|---|
| `IRHUB_SLACK_APP_TOKEN` | App-level トークン(`xapp-…`、`connections:write`) |
| `IRHUB_SLACK_BOT_TOKEN` | Bot トークン(`xoxb-…`) |

トークンをファイルに書く場合は `chmod 600` で保護してください。
group/other に読めるパーミッションの場合、ir-hub は起動時に警告を
表示します。

**ACL はデフォルト全拒否**です。`allow_users` / `allow_groups` が未設定の
場合、すべてのコマンドとメンションは拒否され(監査ログに記録)、起動前に
許可リストの設定が必要です:

```toml
[acl]
allow_groups = ["ir-team"]      # User Group のハンドルまたは ID ("S…")
deny_users   = []               # deny が allow より優先
notify_denied = false           # true: 拒否をエフェメラルで通知
```

未知のグループハンドルは起動エラーになります(typo ガード)。

## 起動

```sh
export IRHUB_SLACK_APP_TOKEN=xapp-...   # pragma: allowlist secret
export IRHUB_SLACK_BOT_TOKEN=xoxb-...   # pragma: allowlist secret
ir-hub serve
```

Bot は自動再接続し、再接続後は `conversations.history` から取りこぼしを
backfill します。

## 知見エクスポート

`[storage]` でバックエンドを設定します。知見ドキュメントは
`<タクティック ID>-<slug>.json` / `.md` のペア(local は `local_path`
直下、S3 は `s3_prefix` 配下)として、手動(`/ir-hub export`)と
ポストモーテム後の自動の両方で書き出されます。

```toml
[storage]
backend    = "local"          # local | gcs | s3
local_path = "./knowledge"
# gcs_bucket = "my-ir-knowledge"        # backend = "gcs"(ADC 使用)
# s3_bucket  = "my-ir-knowledge"        # backend = "s3"(AWS 既定チェーン)
# s3_prefix  = "ir-hub/"
```

GCS は Application Default Credentials、S3 は AWS 既定認証チェーンを
使用します。再エクスポートは決定的パスで上書きします。

## 既知の制限

- **メッセージの編集・削除は取り込まれません**(`message_changed` /
  `message_deleted` はスキップ)。生イベント JSON を保存しているため、
  後続フェーズで拡張可能です。
- **案件の連番に欠番が生じることがあります**: チャネル作成に失敗した番号は
  監査のため意図的に残します。
- **private 案件チャネル**は後から public に変換できません。また取り込みの
  継続には ir-hub がメンバーのままである必要があります。
- **非常に長い案件は分析時に切り詰められます**: `analysis.max_input_tokens`
  を超える場合、予算内の新しいメッセージを分析対象とし、レポートに
  その旨を明記します。
- **知見検索はタグ/キーワード(LIKE)絞り込み**でベクトル検索ではありません。
  数百件規模に適しており、それ以上の規模では FTS や embedding が望ましく
  なります。
- **再エクスポートしたオブジェクトが孤立し得ます**: ポストモーテム再実行で
  タクティック ID が新しくなるため、前回エクスポート分が手動削除まで
  ストレージに残る場合があります。

## ビルド

```sh
make build          # 現在のプラットフォーム → dist/ir-hub
make build-all      # 5 プラットフォーム(CGO 不使用)
make test           # または go test ./...
make package        # リリース zip; darwin は署名+notarize
```

> macOS リリースは **Developer ID 署名+Apple notarize 済み**です。
> Windows / Linux バイナリは未署名です。

## ドキュメント

- [Slack アプリ構成ハンドブック](docs/ja/slack-app-setup.ja.md) /
  [English](docs/en/slack-app-setup.md)
- [RFP(承認済み設計)](docs/ja/ir-hub-rfp.ja.md) /
  [English](docs/en/ir-hub-rfp.md)

## ライセンス

[MIT](LICENSE)
