# RFP: ir-hub

> Generated: 2026-06-10
> Status: Approved (2026-06-10)

## 1. Problem Statement

セキュリティインシデントレスポンス(IR)チームは Slack 上で案件ごとのチャネルを
開設して対応活動を行うが、対応中の状況把握・クローズ後のポストモーテム・知見の
蓄積と再利用が分断されており、ai-ir2 等の既存ツールも単発実行に留まりライフ
サイクルに組み込まれていない。**ir-hub** は、チャネル開設から対応支援、ポスト
モーテム、知見の蓄積・検索・再利用までを LLM で一気通貫支援するワンパッケージの
統合システムである。蓄積された知見は IR チーム以外(ユーザー組織内の他チームや
他のセキュリティチーム)も施策検討の参照データとして利用できる形で提供する。

**対象ユーザー:** Slack 上で ChatOps 的にインシデント対応を行うセキュリティ
IR チーム。副次的に、エクスポートされた知見を参照する組織内他チーム・
他セキュリティチーム。

## 2. Functional Specification

### Commands / API Surface

動作形態: **Go 単一バイナリの常駐 Bot(Slack Socket Mode)**。

スラッシュコマンドは**デュアルモード**:

- `/ir-hub <サブコマンド> [引数]` — 直接実行
- `/ir-hub`(引数なし)— Block Kit モーダルを開き、操作をボタン・フォームで選択
  (パラメータを覚えていなくても使える)

| 操作 | コマンド | 動作 |
|---|---|---|
| 案件開設 | `/ir-hub new <title> [--severity <lv>] [--private\|--public]` | 対応チャネル作成、メタデータ登録、過去類似案件・関連知見から初動ブリーフィングを自動投稿 |
| 状況確認 | `/ir-hub status` | チャネル内会話から現在状況・未解決事項・次アクションのサマリを生成・投稿 |
| クローズ | `/ir-hub close` | 案件終了 → ポストモーテム(知見抽出+対応評価)を自動実行 → 結果をチャネル投稿+知見ストアに登録 |
| PM 再実行 | `/ir-hub pm` | ポストモーテムの手動(再)実行。結果に不満があればやり直し可能 |
| 知見出力 | `/ir-hub export` | 確定ナレッジ・案件データを JSON + Markdown でストレージへ出力 |
| 知見照会 | `@ir-hub <質問>` | 蓄積知見+当該案件の文脈から回答(対応中の知見再利用) |

### Access Control (ACL)

デプロイ先ワークスペースには IR メンバー以外のユーザーが大量に存在する
(約 5 万 ID 規模)前提で、コマンド実行・Bot への指示が可能なユーザーを
制限する ACL を実装する:

- **Whitelist + Blacklist の両方**を実装
- **ユーザー単位+メンショングループ(Slack User Group)単位**で指定可能
- 評価順: Blacklist 優先 → Whitelist 照合。Whitelist 未定義時は**全拒否**
  (安全側デフォルト)
- スラッシュコマンド・`@ir-hub` メンションの両入口で強制
  (Slack 側でコマンド可視性を制限できないため、アプリ側で必ず検査)
- 非許可ユーザーへの応答は **silent + ログ記録がデフォルト**、通知応答は
  config で opt-in(5 万 ID ワークスペースでのノイズ対策)
- User Group のメンバーシップ展開はキャッシュ+TTL で API 負荷を抑制
- ACL 定義は config.toml(ランタイム管理コマンドは将来拡張)

### Input / Output

- **入力:** Slack イベント(Socket Mode)、スラッシュコマンド、メンション
- **ランタイム状態:** 内蔵 DB(SQLite)— 案件メタデータ、メッセージ取り込み、
  分析中間結果、知見インデックス
- **確定出力:** 知見ドキュメント(構造化 JSON + 人間可読 Markdown のペア)を
  プラガブルなストレージバックエンド(ローカル / GCS / S3)へ出力。
  他チームはこのストレージを参照するだけのシンプルな情報連携構造とする

### Knowledge Indexing and Retrieval

LLM 負荷の爆発を避けるため、知見は**確定時にインデキシング**する:

1. **知見確定時(ポストモーテム完了時):** 知見ドキュメントと同時に
   インデックスエントリ(タグ、要約、攻撃カテゴリ等のメタデータ)を DB に登録
2. **照会時:** エージェンティックフリーテキスト検索(FTS)+タグ検索で候補を
   絞り込み → 絞った候補のみをコンテキストにロードして専門家モデルに質問
3. 知見が少ない初期はスモールコーパスとして全件ロードがそのまま成立する
   (絞り込みが恒等になるだけ)ため、同一アーキテクチャで両立する

### Configuration

- `config.toml`(組織標準の Vertex AI config パターン)+ 環境変数オーバーライド
- Slack トークン(App-level token / Bot token)は環境変数または OS keychain
- チャネル可視性(public / private)のデフォルトは config で指定、
  `/ir-hub new` のフラグで上書き可能

### External Dependencies

- Slack API(Socket Mode、Web API)
- Vertex AI Gemini(ADC 認証)
- 出力ストレージ: ローカルファイルシステム / GCS / S3(プラガブル)

## 3. Design Decisions

### なぜ Go(単一バイナリ)か

- 常駐 Bot + 内蔵 DB + ワンパッケージ提供という要件に最適
- slack-go(Socket Mode)、google.golang.org/genai、SQLite、
  nlk(guard / backoff / jsonfix)の組織内実績を活用できる
- ir-timeline(単一バイナリ+内蔵 DB)のパターンを踏襲

### 知見検索方式: インデックス絞り込み+専門家モデル

チャンキング+ベクトル検索(RAG)は採用しない。確定時に構築したインデックス
(タグ+FTS)で候補を絞り、**絞った知見ドキュメント全文をコンテキストに
ロードして専門家モデルに質問する**方式とする(精度優先、virtual-reviewer と
同思想)。embedding ベクトル検索は将来拡張の選択肢として残す。

### 既存ツールとの関係: 恒久併存(用途分離)

| ツール | 役割 | ir-hub との関係 |
|---|---|---|
| ai-ir2 | エクスポート済みデータの単発ポストモーテム分析 | 分析理論・プロンプト設計・防御策(ノンスタグ、IoC defang)を転用。ツールとしては併存 |
| ir-tracker | ライブ状況把握(Web UI) | セグメント分析・状況把握の理論を転用。併存 |
| ir-timeline | タイムライン手動記録 | 単一バイナリ+内蔵 DB の構成パターンを参照。併存 |
| chatops-series | Slack I/O(scat / stail / swrite / slack-router) | Slack 連携パターンを参照。ir-hub は自前で Socket Mode 接続 |

理論・プロンプト・防御策は転用するが、コードは ir-hub として再構築し
ワンパッケージで提供する。

### Out of Scope

- **アラート自動取込(SIEM / mail-triage 連携)** — 案件の起票は人間の
  `/ir-hub new` のみ。自動起票は将来拡張とする

## 4. Development Plan

### Phase 1: コア(Bot 基盤+ライフサイクル管理)

- Socket Mode 常駐 Bot 骨格(再接続・イベント重複排除・3 秒 ACK 非同期化)
- `/ir-hub new | close | status`(メタデータ管理のみ、LLM なし)+ 引数なし時のモーダル
- 案件チャネル自動作成(public / private)
- ACL(Whitelist / Blacklist、ユーザー+User Group、キャッシュ)
- 内蔵 DB(SQLite)スキーマ、メッセージ継続取り込み
- config.toml + テスト
- **レビューポイント:** 「ACL 付き案件チャネル管理 Bot」として単体で動作確認可能

### Phase 2: LLM 分析(ポストモーテム+対応中支援)

- クローズ時ポストモーテム自動実行+`/ir-hub pm` 手動再実行
- 知見ドキュメント生成(JSON + Markdown)+ **確定時インデキシング(タグ・要約・カテゴリ)**
- オンデマンド状況サマリ(`/ir-hub status` の LLM 化)
- プロンプトインジェクション防御(ノンスタグ XML ラッピング)、IoC defang
  (ai-ir2 理論の転用)
- **レビューポイント:** 実案件データで E2E レビュー可能

### Phase 3: 知見再利用+リリース

- 知見照会(`@ir-hub` Q&A): FTS+タグ検索で絞り込み → コンテキストロード → 回答
- 初動ブリーフィング自動投稿(`/ir-hub new` 時の類似案件・関連知見提示)
- ストレージ出力(ローカル / GCS / S3 プラガブルバックエンド)+ `/ir-hub export`
- docs EN/JA 完備、署名+notarize、リリース
- **レビューポイント:** ライフサイクル全体の統合シナリオテスト

## 5. Required API Scopes / Permissions

### Slack

| 種別 | スコープ | 用途 |
|---|---|---|
| App-level token | `connections:write` | Socket Mode 接続 |
| Bot token | `commands` | スラッシュコマンド受信 |
| Bot token | `chat:write` | チャネルへの投稿 |
| Bot token | `app_mentions:read` | `@ir-hub` メンション受信 |
| Bot token | `users:read` | ユーザー情報解決(ACL・レポート) |
| Bot token | `usergroups:read` | ACL の User Group メンバーシップ展開 |
| Bot token | `channels:manage` `channels:read` `channels:history` `channels:join` | public チャネルの作成・取り込み |
| Bot token | `groups:write` `groups:read` `groups:history` | private チャネルの作成・取り込み |
| Bot token | `files:write` | 長文レポートのスニペット投稿 |

モーダル(`views.open`)に追加スコープは不要(trigger_id 起点)。

### GCP

- Vertex AI API 有効化、Application Default Credentials
- `roles/aiplatform.user`
- GCS 出力バックエンド使用時: 対象バケットへの `roles/storage.objectCreator`

### AWS(S3 出力バックエンド使用時)

- 対象バケット / プレフィックスへの `s3:PutObject`

## 6. Series Placement

**Series: cybersecurity-series**

**Reason:** AI-augmented security tools(threat intel, IR, risk assessment)の
定義に合致する。ai-ir2 / ir-tracker / ir-timeline と同じ IR ドメインに属し、
シリーズ内でライフサイクル上の位置づけ(ライブ運用統合 vs 単発分析)が明確に
なる。Slack Bot という形態は chatops-series とも重なるが、ドメインを優先する。

## 7. External Platform Constraints

### Slack

- **スラッシュコマンドの ACK は 3 秒以内**、`trigger_id` の有効期限も 3 秒。
  LLM 処理は必ず非同期化し、即時 ACK +後追い投稿の設計とする
- `chat.postMessage` は約 1 メッセージ/秒/チャネルのレート制限
- `conversations.history` はレート Tier 制限あり(取り込みはページネーション+
  バックオフ必須)
- メッセージ本文 40,000 字、Block Kit section テキスト 3,000 字の上限。
  長文ポストモーテムレポートは分割投稿またはスニペット(ファイル)化
- チャネル名は小文字 80 字以内、アーカイブ済みを含めワークスペース内で重複不可
  (命名規則に連番・日付を含める設計とする)
- Socket Mode は切断・再接続が日常的に発生する。イベント再送(envelope 重複)の
  排他処理が必要
- スラッシュコマンドの可視性はワークスペース全員に及ぶ(Slack 側で制限不可)。
  ACL はアプリ側で強制する

### Vertex AI Gemini

- コンテキストウィンドウ 1M トークンが全文ロード方式の上限。インデックス
  絞り込みで通常照会をこの範囲に収める
- 429 レート制限が発生しうる(nlk/backoff で指数バックオフ)
- 全文コンテキストロードのコスト増は Vertex AI context caching で緩和可能
- **組織 ADR-001 により Gemini 3 GA まで 2.5 系を使用**(GA 後に移行、
  thought signature の echo back 対応が必要)

### ストレージ

- GCS は ADC、S3 は AWS 認証情報と、バックエンドごとに認証機構が異なる。
  プラガブル設計の境界で吸収する

---

## Discussion Log

- **2026-06-10** プロジェクト構想の提示: IR チームの Slack ChatOps 統合支援。
  案件チャネル運用 → ポストモーテム → 知見再利用のライフサイクル全体を LLM で
  支援する。ai-ir2 は単独ツールに留まり活用されていないという課題認識
- **統合ハブ vs 再構築:** 既存ツール(ai-ir2 / ir-tracker / ir-timeline)を
  オーケストレーションする案と再構築案を比較し、**理論は転用するが再構築して
  ワンパッケージで提供**する方針に決定
- **知見再利用の目的:** 案件対応時の活用に加え、IR チーム以外(組織内他チーム・
  他セキュリティチーム)が施策検討の参照データとして利用できることを目的に含める
- **ツール名:** `ir-hub` に決定(候補: ir-hub / ir-ops / ir-lifecycle / ir-companion)
- **動作形態:** 常駐 Bot(Socket Mode)を選択。インバウンド公開不要で
  ローカル / オンプレ実行可能な点を評価
- **知見ストア:** ランタイムは内蔵 DB、確定ナレッジは JSON + Markdown を
  ベースラインとしてストレージ(GCS / S3 / ローカル)に出力。できるだけ
  シンプルな情報連携構造とする方針
- **LLM:** Vertex AI Gemini(組織標準)
- **ポストモーテム起動:** クローズ時自動+手動再実行の両対応
- **対応中支援の範囲:** 初動ブリーフィング自動投稿・オンデマンド状況サマリ・
  RAG 的 Q&A 応答を採用。定期自動サマリ投稿は不採用(ノイズ懸念)
- **実装言語:** Go 単一バイナリ(常駐+内蔵 DB +ワンパッケージ要件)
- **知見検索方式:** 当初、全文コンテキストロード+専門家モデル方式
  (チャンキング+ベクトル検索より精度が高い想定)を選択。その後、LLM 負荷の
  爆発リスクを考慮し、**知見確定時インデキシング+エージェンティック
  フリーテキスト検索+タグ検索で候補絞り込み → 絞った候補のみコンテキスト
  ロード**する段階型に修正。スモールコーパス時は全件ロードと等価になるため
  同一アーキテクチャで両立
- **スコープ外:** アラート自動取込(SIEM / メール連携)のみを明示的に除外。
  Web UI 等は除外せず将来検討余地として残す
- **既存ツールとの位置づけ:** 恒久的に併存(用途分離)。ai-ir2 =
  エクスポート済みデータの単発分析、ir-hub = ライブ運用
- **チャネル可視性:** 設定で選択可能(config デフォルト+フラグ上書き)
- **シリーズ配置:** cybersecurity-series
- **スラッシュコマンド UX:** パラメータ忘れ対策として、引数あり → 直接実行、
  引数なし → モーダルで操作選択のデュアルモードを採用
- **ACL:** ワークスペースに約 5 万 ID が参加している前提で、Whitelist +
  Blacklist 両実装、ユーザー単位+メンショングループ(User Group)単位の
  指定をサポートすることを決定
