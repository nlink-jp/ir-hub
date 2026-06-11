# ir-hub Slack アプリ構成ハンドブック

ir-hub を稼働させるために Slack 側で構成すべき内容をすべて説明します:
アプリ作成、スコープ(各スコープの正当性説明付き — ワークスペースで
アプリ承認制が有効な場合にそのまま申請に使えます)、トークン、イベント
サブスクリプション、インストール、動作検証、トラブルシューティング。

Slack 側の構成はデプロイの半分です。もう半分(config.toml、ACL、
ストレージ)は [README](../../README.ja.md) を参照してください。

---

## 1. アーキテクチャ概観

```
Slack ワークスペース                  自前のインフラ
┌──────────────────────┐             ┌──────────────────────┐
│  /ir-hub  @ir-hub    │  WebSocket  │  ir-hub serve        │
│  案件チャネル        │◀───────────▶│  (単一バイナリ)      │
│  (イベント、コマンド)│ Socket Mode │  └─ SQLite (ローカル)│
└──────────────────────┘             └──────────────────────┘
```

ir-hub は **Socket Mode** を使用し、Bot 側から Slack へ WebSocket で
接続します。開始前に知っておくべき帰結:

- **公開 HTTPS エンドポイントは不要。** Request URL、インバウンドの
  ファイアウォール開放、TLS 証明書のいずれも要りません。ラップトップ、
  オンプレサーバー、クラウド VM のどこでも稼働できます。
- **トークンは 2 種類必要**: WebSocket 接続用の *App-level トークン*
  (`xapp-…`)と、Web API 呼び出し用の *Bot トークン*(`xoxb-…`)。
- **`serve` プロセスはアプリごとに 1 つだけ。** Slack は 1 アプリにつき
  最大 10 の同時 Socket Mode 接続を許しますが、ir-hub は SQLite への
  単一ライターを前提としています。同じ DB・同じアプリに対して 2 つの
  インスタンスを起動しないでください。

## 2. 前提条件

- ワークスペースで Slack アプリを作成する権限、または Slack 管理者
  チームへの連絡経路。**アプリ管理者承認制**(5万 ID 規模では一般的)が
  有効な場合は承認ステップが入ります — セクション 4 のスコープ表は
  そのまま申請文書に貼れるように書いてあります。
- `ir-hub serve` を動かすマシン。`slack.com` /
  `wss-primary.slack.com` へのアウトバウンド HTTPS (443) が必要です。

## 3. マニフェストからアプリを作成

1. https://api.slack.com/apps → **Create New App** →
   **From a manifest**。
2. 対象ワークスペースを選択。
3. 以下のマニフェストを貼り付け(YAML タブ)て作成。

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

### 3.1 アプリ説明文(推奨文面)

About ページの説明文は、アプリを探すユーザーと承認申請をレビューする
管理者の両方が読む部分です — 重要なわりに書くのが面倒なので、推奨
文面を用意しました。上のマニフェストには日本語版を埋め込み済みです。

制約: `description`(short)は最大 **140 文字**、`long_description`
は **175〜4000 文字**。`<your IR team>` / `#<your-contact-channel>`
のプレースホルダーは必ず差し替えてください — 運用主体と問い合わせ先が
明記されていると管理者承認が目に見えて通りやすくなります。

**Short description(日本語):**

> インシデント対応のライフサイクルハブ。案件チャネルの開設、対応支援、
> ポストモーテム、知見の再利用を一気通貫で支援します。

**Short description(英語、131 字):**

> Incident-response lifecycle hub: opens a channel per case, tracks
> the response, runs postmortems, and reuses the lessons learned.

**Long description(日本語):** 上のマニフェスト内を参照。

**Long description(英語):**

> ir-hub supports security incident-response teams across the full
> lifecycle of a case.
>
> - /ir-hub new opens a dedicated case channel, invites the opener,
>   and posts a kickoff briefing
> - /ir-hub status summarizes the current state of the case
> - /ir-hub close ends the response and runs an automated postmortem
> - Lessons learned are accumulated as knowledge and reused on
>   future incidents
>
> Access is restricted to the IR team by an in-app allowlist, and
> denied attempts are audit-logged. Messages are ingested only from
> case channels the app itself creates.
>
> Operated by: <your IR team>  /  Questions: #<your-contact-channel>

About ページに表示されるのは 1 言語のみなので、ワークスペースの
主要言語に合わせて選んでください。`background_color` は任意の装飾
ですが、アプリカード上でインシデント対応らしく見える `"#7a1c1c"`
あたりを推奨します。

補足:

- スラッシュコマンド名(`/ir-hub`)はワークスペース内で一意である
  必要があります。他アプリと衝突する場合はここで変更してください —
  バイナリ側はコマンド名に依存しません。
- `should_escape: false` により、コマンドテキスト中のユーザー/チャネル
  参照はプレーンテキストのまま渡されます。ir-hub はタイトルを字義
  どおりにパースします。
- Socket Mode 有効時、イベントやインタラクティビティの
  **Request URL は不要**です。

## 4. スコープ — それぞれ何のためか

以下のスコープはすべて Phase 1 で実際に使用されます。セキュリティ
チームから「なぜ X が必要か」と聞かれたときの回答集です。

| スコープ | 使用箇所 | 理由 |
|---|---|---|
| `commands` | `/ir-hub` | スラッシュコマンドの受信 |
| `chat:write` | キックオフ/クローズ/ステータス投稿 | Bot として案件チャネルへ投稿 |
| `app_mentions:read` | `@ir-hub` | メンション受信(知見 Q&A は Phase 3、Phase 1 は案内を返信) |
| `users:read` | ACL、レポート | 監査ログ・表示用のユーザー ID 解決 |
| `usergroups:read` | ACL | User Group ハンドルまたは ID(`allow_groups` / `deny_groups`)のメンバー展開 |
| `channels:manage` | `/ir-hub new --public` | **public** 案件チャネルの作成と起票者の招待 |
| `channels:read` | 案件参照 | public チャネルのメタデータ取得 |
| `channels:history` | 取り込み | 再接続 backfill のための public 案件チャネル履歴読取り |
| `channels:join` | リカバリ | Bot が外された public 案件チャネルへの再参加 |
| `groups:write` | `/ir-hub new --private` | **private** 案件チャネルの作成と起票者の招待 |
| `groups:read` | 案件参照 | private チャネルのメタデータ取得 |
| `groups:history` | 取り込み | 再接続 backfill のための private 案件チャネル履歴読取り |
| `files:write` | 長文レポート | 長文ポストモーテムのスニペット投稿 |
| `reactions:write` | `@ir-hub` Q&A | 回答生成中であることを示す 👀 リアクションの付与・除去 |

ir-hub が意図的に**要求しない**もの:

- `users:read.email` なし — メールアドレスへのアクセスなし。
- `im:*` / `mpim:*` なし — DM は読みません。
- `channels:history` を案件チャネル以外で*使用*することはありません:
  スコープ上は Bot がメンバーの public チャネルすべてが対象ですが、
  ir-hub は自分が作成したチャネルにしか参加せず、さらに取り込み層が
  オープン中の案件に紐づかないチャネルのメッセージを破棄します。
- ユーザートークン(`xoxp-…`)は一切不使用 — すべて Bot として動作。

## 5. トークンの発行

### 5.1 App-level トークン(`xapp-…`)

1. アプリ設定 → **Basic Information** → **App-Level Tokens** →
   **Generate Token and Scopes**。
2. 名前を付け(例: `socket`)、スコープ **`connections:write`** を
   追加して生成。
3. `xapp-1-…` の値をコピー → これが `IRHUB_SLACK_APP_TOKEN`。

### 5.2 Bot トークン(`xoxb-…`)

1. アプリ設定 → **Install App** → **Install to Workspace** → 承認。
2. **Bot User OAuth Token**(`xoxb-…`)をコピー → これが
   `IRHUB_SLACK_BOT_TOKEN`。

### 5.3 トークンの取り扱いルール

- トークンは環境変数で渡すか、`config.toml` の `[slack]` セクションに
  書いて `chmod 600` で保護します(緩いパーミッションは起動時に警告)。
- トークンをリポジトリにコミットしない、Slack メッセージに貼らない、
  コンテナイメージに焼き込まない。
- 漏えい時: **App settings → OAuth & Permissions → Revoke**
  (Bot トークン)/ **Basic Information → App-Level Tokens → Revoke**
  で失効 → 再発行 → デプロイ更新。そのトークンで読めたチャネル内容は
  漏えいした可能性があるものとして扱います。

## 6. インストールと管理者承認

アプリ承認制のワークスペースでは、5.2 のインストールが申請になります。
申請には以下を含めてください:

- セクション 4 のスコープ表(そのまま転記)。
- このアプリが**社内用・Socket Mode・公開エンドポイントなし・単一
  ワークスペース**であること。
- 運用主体(IR チーム)と、コマンドの利用がアプリ内の許可リスト ACL で
  さらに制限されること(Slack はスラッシュコマンドの*可視性*を制限
  できないため、アプリ自身が強制し、拒否を監査ログに記録します)。

以後**スコープを変更**した場合は **再インストール**(Install App →
Reinstall to Workspace)が必須で、承認フローが再度走る可能性が
あります。Phase 2 で必要になる `files:write` を初日から要求している
のはこのためです。

## 7. ir-hub の設定と起動

詳細は [README](../../README.ja.md) を参照。要点のみ:

```sh
mkdir -p ~/.config/ir-hub
cp config.example.toml ~/.config/ir-hub/config.toml
chmod 600 ~/.config/ir-hub/config.toml
# 編集: [acl] allow_groups / allow_users — 必須(デフォルト全拒否)

export IRHUB_SLACK_APP_TOKEN=xapp-...   # pragma: allowlist secret
export IRHUB_SLACK_BOT_TOKEN=xoxb-...   # pragma: allowlist secret
ir-hub serve
```

設計どおり起動に失敗するケース:

- `IRHUB_SLACK_APP_TOKEN is required` — トークン未設定。
- `acl: unknown user group handle(s): …` — `[acl]` のグループハンドルが
  ワークスペースに存在しない(typo ガード。ハンドルを修正)。
- `slack auth test: invalid_auth` — トークンの失効・欠損・別ワーク
  スペースのもの。

## 8. 動作検証チェックリスト

初回インストール後に一通り実行します(検証用の静かなワークスペースで
構いません — 手順はすべて非破壊です):

1. **接続**: `ir-hub serve` のログに `authenticated as ir-hub (U…)` →
   `connected` が出る。
2. **ACL 拒否経路**: 許可リスト外のユーザーで `/ir-hub status` を実行。
   期待: 見える応答なし(silent)、監査ログに 1 行追加、サーバーログに
   `denied` 行。
3. **モーダル**: 許可ユーザーで引数なし `/ir-hub` を実行。期待: 操作
   選択モーダルが開き、*Open a new case* 選択でパラメータフォームに
   遷移、空タイトルで送信するとインラインのバリデーションエラー。
4. **案件作成**: `/ir-hub new Test incident --severity low
   --private`。期待: チャネル `ir-0001-test-incident` が作成され、
   自分が招待され、キックオフ投稿、実行チャネルにエフェメラル確認。
5. **取り込み**: 案件チャネルに数件投稿 → `/ir-hub status`。期待:
   `Ingested messages` が一致。
6. **Backfill**: `serve` を停止 → 案件チャネルに 2–3 件投稿 → `serve`
   再起動 → `/ir-hub status`。期待: 停止中のメッセージも件数に含まれる。
7. **クローズ**: 案件チャネルで `/ir-hub close`。期待: クローズ投稿。
   2 回目の `/ir-hub close` は「open でない」エラー。

## 9. 運用ノート

- **再接続は正常動作。** Socket Mode は日常的に切断されます。ir-hub は
  自動で再接続し backfill します。ただし `connection error` が頻発する
  場合は、egress プロキシやファイアウォールが WebSocket を妨げている
  ことが多いです。
- **1 プロセス・1 DB。** アプリごと・SQLite ファイルごとに `serve` は
  1 つだけ。
- **案件チャネルの Bot メンバーシップは生命線。** private 案件チャネルで
  誰かが ir-hub を外すと取り込みが止まり、Bot は自力で再参加できません。
  手動で再招待してください。
- **チャネル名**: `ir-<連番>-<slug>`。連番により一意です。Slack は
  *アーカイブ済み*チャネルとの名前衝突も禁止しますが、連番方式により
  実運用では問題になりません。
- **ACL の変更**(設定ファイル)は `serve` の再起動が必要です。User
  Group の*メンバーシップ*変更は `group_cache_ttl` 秒(デフォルト 300)
  以内に自動反映されます。

## 10. トラブルシューティング

| 症状 | 原因の見立て | 対処 |
|---|---|---|
| 起動時 `invalid_auth` | Bot トークンの誤り/失効、別ワークスペースのトークン | Install App から再発行し env/config を更新 |
| `not_allowed_token_type` | App-level と Bot のトークンを取り違え | `xapp-` → `IRHUB_SLACK_APP_TOKEN`、`xoxb-` → `IRHUB_SLACK_BOT_TOKEN` |
| `/ir-hub` が `dispatch_failed` | `serve` 停止中、または Socket Mode 無効 | `serve` 起動; **Settings → Socket Mode** を確認 |
| コマンドは動くがモーダルが開かない | Interactivity 無効 | **Interactivity & Shortcuts** を有効化(マニフェストでは有効。編集で消えていないか確認) |
| モーダルがまれにタイムアウト | `trigger_id` の 3 秒制限超過(コールドキャッシュでの ACL グループ解決が遅い) | 通常は一過性。継続する場合は slack.com への egress レイテンシを確認 |
| メッセージが取り込まれない | イベントサブスクリプション欠落 | **Event Subscriptions → bot events** に `message.channels` + `message.groups` があるか確認; 追加直後なら再インストール |
| メンションが全員無視される | `app_mention` イベント欠落、または全員拒否 | イベントサブスクリプション確認; `[acl]` の許可リストが空でないか確認 |
| `/ir-hub new` で `missing_scope` | マニフェストにスコープを足したが再インストールしていない | **Install App → Reinstall to Workspace** |
| 案件作成で `name_taken` | チャネル名衝突(連番方式では通常発生しない) | パターンに合致する手動作成チャネルがないか確認。案件は `failed` になるので `new` を再実行 |
| 全員が拒否される | 許可リスト空(デフォルト全拒否)またはグループハンドル typo(後者は起動失敗) | `[acl] allow_users` / `allow_groups` を設定 |
| 設定ファイルのパーミッション警告 | group/other に読める状態 | `chmod 600 ~/.config/ir-hub/config.toml` |

## 11. マルチ環境ガイダンス

環境ごとに別の Slack アプリを作成してください(例: サンドボックス
ワークスペースに `ir-hub-dev`、本番に `ir-hub`)。トークン・SQLite
DB・設定ファイルは環境ごとに 1 セットで、3 つのいずれも環境間で共有
しないこと。スコープ変更は本番では再インストール+再承認になりうる
ため、アップグレードのリハーサル用にサンドボックスワークスペース
(無料プランで可)を強く推奨します。
