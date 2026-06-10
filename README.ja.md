# ir-hub

インシデントレスポンス・ライフサイクルハブ — IR ライフサイクル全体を LLM で
支援するワンパッケージの Slack ChatOps Bot。案件チャネル開設、対応中の支援、
ポストモーテム分析、知見の蓄積と再利用までを一気通貫でカバーします。

[English README is here](README.md)

> **Status: pre-release.** Phase 1(Bot 基盤+ライフサイクル管理)を
> 開発中です。承認済み設計は [RFP](docs/ja/ir-hub-rfp.ja.md) を参照。

## コンセプト

```
/ir-hub new ──→ 案件チャネル ──→ 対応活動 ──→ /ir-hub close
     │             (状況サマリ / Q&A 支援)         │
     │                                            ▼
     └── 初動ブリーフィング ◀── 知見ストア ◀── ポストモーテム
         (過去案件から)        (JSON + Markdown,   (自動+再実行可)
                                local / GCS / S3)
```

- **Slack 上の案件ライフサイクル** — `/ir-hub new` で案件ごとの専用
  チャネルを開設、`/ir-hub close` でポストモーテムを実行し知見を登録
- **デュアルモードのスラッシュコマンド** — 引数ありで直接実行、
  引数なしの `/ir-hub` はモーダルから操作を選択
- **対応中支援** — オンデマンド状況サマリ(`/ir-hub status`)と
  知見 Q&A(`@ir-hub <質問>`)
- **知見の再利用** — 確定知見はポストモーテム時にインデキシングされ、
  JSON + Markdown ペアとしてローカル / GCS / S3 ストレージへ出力。
  IR チーム外もストレージ参照で利用可能
- **ACL 内蔵** — ユーザー単位+Slack User Group 単位の Whitelist +
  Blacklist。数万メンバー規模のワークスペースを想定した設計
- **Vertex AI Gemini**(ADC 認証)

## 前提条件

- Go 1.26+
- Socket Mode を有効化した Slack アプリ(必要スコープは
  [RFP §5](docs/ja/ir-hub-rfp.ja.md) を参照)
- Vertex AI API を有効化した GCP プロジェクトと Application Default
  Credentials: `gcloud auth application-default login`

## インストール

```sh
git clone https://github.com/nlink-jp/ir-hub.git
cd ir-hub
make build          # → dist/ir-hub
```

## 設定

[`config.example.toml`](config.example.toml) を
`~/.config/ir-hub/config.toml` にコピーして編集します。全フィールドは
`IRHUB_*` 環境変数で上書き可能です。Slack トークンは環境変数のみで
指定します(設定ファイルには書きません):

| 環境変数 | 説明 |
|---|---|
| `IRHUB_SLACK_APP_TOKEN` | App-level トークン(`connections:write`) |
| `IRHUB_SLACK_BOT_TOKEN` | Bot トークン |

## 使い方

```sh
ir-hub --version
# サブコマンド(serve, export, ...)は Phase 1 開発で追加されます。
```

## ビルド

```sh
make build          # 現在のプラットフォーム → dist/ir-hub
make build-all      # 5 プラットフォームのクロスコンパイル
make test           # または go test ./...
make package        # リリース zip 作成; darwin は署名+notarize
```

> macOS リリースは **Developer ID 署名+Apple notarize 済み**です。
> Windows / Linux バイナリは未署名です。

## ドキュメント

- [RFP(承認済み設計)](docs/ja/ir-hub-rfp.ja.md) /
  [English](docs/en/ir-hub-rfp.md)

## ライセンス

[MIT](LICENSE)
