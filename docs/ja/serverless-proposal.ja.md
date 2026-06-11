# ir-hub サーバーレス版 — 設計提案

> **Status: 提案(ドラフト・未承認)。** 本書はサーバーレス版の設計と
> 決定事項を整理するものです。これに基づくコードはまだ書いていません。

## 1. 目的

現行の **スタンドアロン版**(Socket Mode + 内蔵 SQLite、VM 稼働)を
そのまま維持しつつ、Cloud Run 上で **scale-to-zero** 動作する
**サーバーレス版**を追加する。アイドル時はほぼ無課金になり、ir-hub の
低頻度・インシデント駆動の使い方に適する。両版は **単一の共有コア**から
ビルドし、異なるのは 3 つの I/O 層のみ。

## 2. 2 つのエディション

| | スタンドアロン(現行) | サーバーレス(提案) |
|---|---|---|
| Slack transport | Socket Mode(WebSocket) | HTTP Events API + Interactivity + Slash(Request URL) |
| データベース | 内蔵 SQLite | Firestore(マネージド・サーバーレス) |
| 非同期処理 | プロセス内 goroutine | Cloud Tasks → worker エンドポイント |
| ホスト | 常時起動 VM | Cloud Run(scale-to-zero) |
| scale-to-zero | 不可 | **可**(アイドル = \$0) |
| コスト | VM 並(例: e2-micro) | 月数百円程度、インシデント駆動 |
| 適性 | シンプル・自己ホスト | 最安・GCP ネイティブ |

両版はフォークではなく、分析・知見・ACL・コマンドのロジックを共有する。
違いは配線で、ビルド時または設定で切り替える。

## 3. 共有コア(無変更で移植)

以下のパッケージは pure logic か既に interface 駆動で、無変更でサーバーレス
版に移植できる:

`analysis`, `knowledge`, `acl`, `command`, `modal`, `defang`,
`sanitize`, `msg`, `llm`, `userdir`, `channelname`, `storage`,
`export`、および `slackapi.API` interface(Web API 呼び出しは両版で同一)。

重要なのは、bot の **ハンドラ関数が既に transport 非依存**な点。`runNew`、
`runPM`、`runClose`、`runStatus`、`runQA`、`runExport`、`runReopen` は
すべて `(ctx, channelID, userID, …string)` という素の引数を取り、store +
analyzer + Slack Web API を呼ぶだけで、Socket Mode に触れない。これが
共有コアを現実的にしている。

## 4. 抽象化すべき 3 つの I/O 接合点

作業は共有コアに 3 つの interface を導入し、各 2 実装を用意すること:

1. **Store** — 現在は全消費者(`bot`、`cases`、`analysis`、`ingest`、
   `export`)が具体型 `*store.Store` を取る。`Store` interface(約 19
   メソッド、11 が hot)を導入し、`sqlite` と `firestore` 実装を持つ。
   最大の変更点。

2. **Transport** — 薄い `Socket` interface は既存(`bot/socket.go`)。
   イベント受信を一般化し、同じハンドラ関数を Socket Mode ループからも
   HTTP リクエストハンドラからも到達可能にする(Slack ペイロード解析→
   `runX` 呼び出し)。3 秒 ACK は「即 HTTP 200 を返し、処理は非同期」になる。

3. **Job runner** — `dispatch(fast, fn)` の goroutine+semaphore 機構を
   `JobRunner` interface に: `inproc`(現行 goroutine)と `cloudtasks`
   (ハンドラ名 + 文字列引数を載せた HTTP タスクを enqueue)。ハンドラは
   既に単純な serializable 引数を取るので、タスクのペイロードは
   `{"op":"pm","channel":"C…","user":"U…"}` のような小さな JSON。

## 5. 本当に難しい 3 点(Firestore)

Firestore は SQL ではないため、SQLite 固有の 3 機構を再設計する必要がある。
いずれも解決可能で、ir-hub の小規模さがそれを容易にする。

| 難所 | 理由 | 提案する解決策 |
|---|---|---|
| **`SearchKnowledge`(LIKE)** | Firestore に部分一致/LIKE 検索がない | 全知見をロードしてメモリ上でフィルタ。コーパスは小規模(数十件)で、RFP も「全文コンテキストロード、ベクトル RAG 不使用」を規定しているため、これは設計と*合致*する(対立しない)。Q&A は現状も「全件ロード」フォールバックがある。 |
| **`FinalizePMRun`(単一 tx: 知見置換 + 日次連番 `tac-YYYYMMDD-NNN`)** | Firestore tx で `MAX()` スキャンできない | 日次カウンタ文書を Firestore トランザクション内でインクリメント。1 案件の知見書き込み(≤約 10 件)は単一 Firestore tx(500 文書上限)に収まる。決定的 ID を維持。 |
| **case `AUTOINCREMENT`(`ir-0042-…` チャネル名を駆動)** | Firestore に `LastInsertId` がない | 単一の `case_seq` カウンタ文書を case 作成時にトランザクションでインクリメント。人間可読の連番を維持(UUID に変えない — 連番はユーザー可視)。 |

ir-hub の規模ではいずれも整合性の妥協を強いない: 1 インシデントは少数文書の
書き込みで、Firestore のトランザクション上限に十分収まる。

## 6. 提案するサーバーレスアーキテクチャ

```
Slack ──HTTP POST──▶ Cloud Run(ingress、scale-to-zero)
  (events / slash /        │  1. signing secret 検証
   interactivity)          │  2. 3 秒以内に 200 返却
                           │  3. Cloud Task を enqueue(op + 引数)
                           ▼
                     Cloud Tasks
                           │
                           ▼
                  Cloud Run(worker)  ──▶ Firestore(状態 + 知見)
                  runPM / runQA / …   ──▶ Vertex AI(ポストモーテム)
                                      ──▶ GCS/S3(知見エクスポート)
```

- **Ingress** は安価で高速: 検証・ACK・enqueue のみ。イベント間は
  scale-to-zero 可能で、Slack は非 2xx 時にリトライする。
- **Worker** が実ハンドラを実行(PM は数分かかり得る — Cloud Run の
  リクエストタイムアウトは最大 60 分で十分)。これも scale-to-zero。
- **取り込み**: HTTP の `message` イベントコールバックが*同じ*
  `ingest.HandleMessage(*slackevents.MessageEvent)` 経路に流れる — ロジック
  変更なし。再接続 backfill は Slack 自身のイベント配信 + リトライに置換
  (よりシンプル)。

## 7. 技術選定

| 決定 | 選択肢 | 推奨 |
|---|---|---|
| リポジトリ構成 | (a) 同一リポジトリ、interface + ビルド/設定切替; (b) 別リポジトリ + 共有ライブラリ | **(a)** — 単一コア・2 配線、drift を防ぐ |
| データベース | Firestore; Cloud SQL(Postgres); Cloud Run+litestream(SQLite 維持だが scale-to-zero 不可) | **Firestore** — 真に scale-to-zero できる唯一の選択肢 |
| 非同期 | Cloud Tasks; Pub/Sub; Workflows | **Cloud Tasks** — HTTP ネイティブ、リトライ簡潔、per-task |
| 知見検索 | メモリフィルタ; Algolia/Typesense | 現規模では **メモリフィルタ**、大規模化時に再検討 |
| ホスト | Cloud Run; Cloud Functions(2nd gen = Cloud Run) | **Cloud Run** |
| Slack 認証 | signing secret(HTTP)が Socket の app-level token を置換 | `[slack] signing_secret` 追加、本版では `app_token` を廃止 |

## 8. フェーズ計画

フェーズ S1–S2 は*現行*コードの純リファクタ(interface を導入し、
SQLite/Socket/goroutine 実装をその背後に維持)。挙動変更なしでスタンド
アロン版に取り込め、以降をすべて de-risk する。

| フェーズ | 作業 | リスク |
|---|---|---|
| **S1** | `Store` interface 導入。現 SQLite が 1 実装に。挙動変更なし。 | 低(リファクタ) |
| **S2** | `Transport` + `JobRunner` interface 導入。Socket Mode + goroutine が実装に。 | 低(リファクタ) |
| **S3** | Firestore `Store` 実装: case-seq と tactic-ID のカウンタ、メモリ知見検索、schema バージョン文書。 | 中 |
| **S4** | HTTP transport: signing-secret 検証、event/slash/interactivity 解析、3 秒 ACK パターン。 | 中 |
| **S5** | Cloud Tasks `JobRunner`: ingress が enqueue、worker が op で dispatch。 | 中 |
| **S6** | Cloud Run デプロイ(ingress + worker)、scale-to-zero 検証、実コスト測定、IaC。 | 中 |

概算工数: 約 2–3 週間。S1–S2 はスタンドアロン版のテスト容易性も向上させる
ため、どのみち実施する価値がある。

## 9. コスト試算(オーダー)

- **スタンドアロン(VM):** e2-micro ≈ \$7/月(または無料枠対象)、24/7 課金。
- **サーバーレス(scale-to-zero):** Cloud Run アイドル = \$0; Firestore は
  低トラフィックなら無料枠内/近辺; Cloud Tasks 無料枠でカバー; Vertex AI は
  ポストモーテム実行時のみ課金。現実的に **月数百円以下**で、その大半は
  インシデント時の LLM 呼び出し(これは両版とも支払う)。

サーバーレス版はアイドルコストで勝り、VM 版は運用の簡潔さとコールド
スタート無しで勝る。

## 10. 未決事項(要承認)

1. **リポジトリ構成** — 同一リポジトリ + interface 切替(推奨)vs 別
   リポジトリ `ir-hub-serverless`。
2. **データベース** — Firestore(推奨)vs Cloud SQL。
3. **S1–S2 を今やるか** — サーバーレス版の本格着手前でも、interface
   リファクタをスタンドアロン版に即時実施するか(現行版も改善・de-risk
   される)。
4. **大規模時の知見検索** — メモリフィルタを許容するか、大規模化を見込んで
   全文検索サービスを計画するか。

## 11. リスク

- **Store interface の面が広い**(約 19 メソッド)。抽象化は SQLite 固有性
  (`sql.NullString`、`LIKE` 文字列等)を漏らしてはならない。意図ベースで
  interface を設計することで緩和。
- **Firestore の結果整合性**(list クエリ)— 知見/case の一覧では問題ないが、
  tactic-ID カウンタと case-seq は正確性のためトランザクション必須。
- **コールドスタート**がアイドル後の最初のイベントに遅延を加える。ACK は
  小さく保たれる ingress で行うため、ユーザー可視の 3 秒ルールは安全だが、
  アイドル後の*最初*の PM はコールドスタートを払う。
- **2 つのコードパスの保守** — コア共有で最小化。実装のみ異なる。CI は両版を
  ビルド/テストする必要がある。
