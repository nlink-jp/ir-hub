# ir-hub

インシデントレスポンス・ライフサイクルハブ — IR ライフサイクル全体を LLM で
支援するワンパッケージの Slack ChatOps Bot。案件チャネル開設、対応中の支援、
ポストモーテム分析、知見の蓄積と再利用までを一気通貫でカバーします。

[English README is here](README.md)

> **Status: pre-release.** Phase 1(Bot 基盤+ライフサイクル管理、LLM なし)
> を実装済み。LLM ポストモーテムは Phase 2、知見再利用は Phase 3 で追加
> されます。承認済み設計は [RFP](docs/ja/ir-hub-rfp.ja.md) を参照。

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
  開設、`/ir-hub close` でクローズ(Phase 2 以降はポストモーテムを自動実行)
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
| `/ir-hub status` | 案件メタデータを投稿: 状態、severity、経過時間、取り込みメッセージ数 |
| `/ir-hub close` | 案件をクローズ(案件チャネル内で実行) |
| `@ir-hub <質問>` | 知見 Q&A — 回答は Phase 3 から。Phase 1 では案内を返信 |

## 前提条件

- Go 1.26+(ビルド)
- Socket Mode を有効化した Slack アプリ(下記)

## Slack アプリの設定

> 完全な手引き — スコープの正当性説明、管理者承認、トークンの取り扱い、
> 動作検証チェックリスト、トラブルシューティング — は
> [Slack アプリ構成ハンドブック](docs/ja/slack-app-setup.ja.md) を参照。

以下のマニフェストからアプリを作成します(App settings → App Manifest):

```yaml
display_information:
  name: ir-hub
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
allow_groups = ["ir-team"]      # Slack User Group のハンドル
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

## 既知の制限(Phase 1)

- **メッセージの編集・削除は取り込まれません**(`message_changed` /
  `message_deleted` はスキップ)。生イベント JSON を保存しているため、
  後続フェーズで拡張可能です。
- **案件の連番に欠番が生じることがあります**: チャネル作成に失敗した番号は
  監査のため意図的に残します。
- **private 案件チャネル**は後から public に変換できません。また取り込みの
  継続には ir-hub がメンバーのままである必要があります。
- LLM 機能(`/ir-hub pm`、状況サマリ、Q&A、ブリーフィング、知見出力)は
  Phase 2–3 で追加されます。

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
