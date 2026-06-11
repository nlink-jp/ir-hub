# ir-hub サーバーレス版 — 設計提案

> **Status: 提案(ドラフト・未承認)。** 本書はサーバーレス版の設計と
> 決定事項を整理するものです。これに基づくコードはまだ書いていません。

## 1. 目的

現行の **スタンドアロン版**(Socket Mode + 内蔵 SQLite、VM 稼働)を
そのまま維持しつつ、Cloud Run 上で **scale-to-zero** 動作する
**サーバーレス版**を追加する。アイドル時はほぼ無課金になり、ir-hub の
低頻度・インシデント駆動の使い方に適する。両版は **単一の共有コア**から
ビルドする。

## 2. 採用方式: SQLite を維持し、レプリし、HTTP で動かす

核心の決定: **SQLite から移行しない。** 代わりに、Cloud Run を単一
インスタンスで動かし、SQLite DB を [litestream](https://litestream.io) で
オブジェクトストレージにレプリケーションし、Slack transport を HTTP に
切り替えてイベント間に scale-to-zero できるようにする。

これにより SQLite の強みを保ちつつ、**Firestore の難所 3 点を丸ごと回避**
する(§6 参照)— `SearchKnowledge` の LIKE 書き換えも、`FinalizePMRun` の
トランザクション再設計も、AUTOINCREMENT 置換も不要で、**`store` パッケージは
無変更で移植**できる。ir-hub の極めて低いリクエストレートと緩い遅延要求が、
この方式の唯一のコスト — コールドスタート時の DB リストア数秒 — を許容可能に
する。

| | スタンドアロン(現行) | サーバーレス(提案) |
|---|---|---|
| Slack transport | Socket Mode(WebSocket) | HTTP Events API + Interactivity + Slash |
| データベース | 内蔵 SQLite | **内蔵 SQLite + litestream → GCS** |
| 非同期処理 | プロセス内 goroutine | Cloud Tasks → worker エンドポイント(同一サービス) |
| ホスト | 常時起動 VM | Cloud Run、`max-instances=1`、scale-to-zero |
| scale-to-zero | 不可 | **可**(アイドル = \$0) |

## 3. 共有コア(無変更で移植)

pure logic と interface 駆動のパッケージは無変更で移植する:
`analysis`, `knowledge`, `acl`, `command`, `modal`, `defang`,
`sanitize`, `msg`, `llm`, `userdir`, `channelname`, `storage`,
`export`、`slackapi.API` interface — **そして決定的に `store` パッケージ
全体**(SQLite 維持)。

bot の **ハンドラ関数は既に transport 非依存** — `runNew`、`runPM`、
`runClose`、`runStatus`、`runQA`、`runExport`、`runReopen` は素の
`(ctx, channelID, userID, …string)` 引数を取り、store + analyzer + Slack
Web API を呼ぶだけで、Socket 固有部分はない。

## 4. 抽象化すべき 2 つの I/O 接合点

SQLite を維持するため、導入する interface は **2 つだけ**(Firestore 版なら
第 3 の `Store` interface が要った):

1. **Transport** — 薄い `Socket` interface は既存(`bot/socket.go`)。
   イベント受信を一般化し、同じハンドラを Socket Mode ループ(スタンド
   アロン)からも HTTP リクエストハンドラ(サーバーレス)からも到達可能に
   する。3 秒 ACK は「即 HTTP 200 を返し、処理は非同期」になる。

2. **Job runner** — `dispatch(fast, fn)` の goroutine+semaphore 機構を
   `JobRunner` interface に: `inproc`(現行 goroutine、スタンドアロン)と
   `cloudtasks`(ハンドラ名 + 文字列引数を載せた HTTP タスクを enqueue、
   サーバーレス)。ハンドラは既に単純な serializable 引数を取るので、タスクの
   ペイロードは `{"op":"pm","channel":"C…","user":"U…"}` のような小さな JSON。

`store` パッケージは **interface も変更も不要**。

## 5. アーキテクチャ(単一サービス・単一ライター)

非自明な制約: **SQLite は単一ライターなので、ingress と worker は同一の
Cloud Run サービス/インスタンスに置く必要がある。** 別サービスにすると
SQLite ファイルを共有できない。したがって:

```
Slack ──HTTP──▶ Cloud Run サービス(max-instances=1, scale-to-zero)
                  │
  /slack/*(ingress パス)         /tasks/run(worker パス)
  • signing secret 検証           • 唯一の DB 触接パス
  • 3 秒以内に 200 返却            • ハンドラ dispatch(runPM, runQA…)
  • Cloud Task を enqueue ─────▶  • Vertex AI / GCS export
  • DB アクセスなし                  ▲
                                     │
                          Cloud Tasks(同一サービス宛て)
                                     │
   litestream  ◀── 起動時 restore / 継続 WAL replicate ──▶ GCS
```

- **ingress パスは DB に一切触れない** — 検証・ACK・enqueue のみ。これにより
  コールドスタートでも 3 秒応答が速い(ACK に restore 不要)。
- **worker パスが唯一の DB 消費者** — そのコールドスタートで
  `litestream restore` を実行。Cloud Tasks 経由(非同期・リトライ付き)なので
  3 秒ルールに縛られない。
- **`max-instances=1`** が単一 SQLite ライターを保証。Cloud Tasks は同一
  サービスを宛先にするので worker は同じインスタンスで動く。`modernc` SQLite +
  `SetMaxOpenConns(1)`(既設定)がインスタンス内の書き込みを直列化。
- **取り込み**(`message` イベント)も enqueue → worker の
  `ingest.HandleMessage` 経路を通り、ingress は DB フリーを維持。
- **litestream** は起動時に restore し、WAL を継続的に GCS へストリーム、
  `SIGTERM`(Cloud Run の停止シグナル)で flush。損失ウィンドウは秒単位で、
  定期的な DB 全体 sync よりはるかに良い。

## 6. なぜ Firestore 経路に勝るか

Firestore 版は SQLite 固有の 3 機構を再設計する必要があった。SQLite 維持は
**それらをすべて回避**する:

| 難所(Firestore) | SQLite + litestream では |
|---|---|
| `SearchKnowledge` LIKE に Firestore 等価物なし | 無変更 — SQLite LIKE が動く |
| `FinalizePMRun` 単一 tx + 日次連番 `tac-YYYYMMDD-NNN` | 無変更 — SQLite トランザクションが動く |
| case `AUTOINCREMENT` が `ir-0042-…` チャネル名を駆動 | 無変更 — AUTOINCREMENT が動く |
| schema バージョンマイグレーション | 無変更 |

これが、採用方式が**より小さな変更**でもある理由。

## 7. 設計上の詰めどころ(実装に持ち越す)

1. **コールドスタート遅延 vs slash 3 秒。** `min-instances=0` だと、アイドル後の
   最初の slash コマンドはコンテナのコールドスタートを払う。3 秒を超えると
   Slack は失敗を表示し、ユーザーは再実行する。events と interactivity は
   Slack が自動リトライするので、ユーザーが叩く slash のみが露出。問題化した
   場合の緩和: `min-instances=1`(常時ウォーム、ただし完全 scale-to-zero を
   失い小さな固定費が発生)— 実測で決める調整項目。
2. **インスタンス交代。** デプロイ/インスタンス置換時、瞬間的に 2 インスタンスが
   重なり得る。litestream は競合ライターを検出。`max-instances=1` と短いデプロイで
   リスクは小さいが、切替時は先に書き込みをドレインする必要がある。
3. **worker タイムアウト。** ポストモーテムは数分かかる。Cloud Run の
   リクエストタイムアウト(最大 60 分)でカバー — worker リクエストがその間
   開いたままになり、インスタンスを生かす。

## 8. フェーズ計画

フェーズ S1–S2 は*現行*コードの純リファクタ(2 つの interface を導入し、
Socket Mode + goroutine をその背後に維持)。挙動変更なしでスタンドアロン版に
取り込め、テスト容易性も向上させ、以降を de-risk する。

| フェーズ | 作業 | リスク |
|---|---|---|
| **S1** | `Transport` interface 導入。イベント受信を一般化しハンドラを transport 非依存に。Socket Mode が 1 実装に。 | 低(リファクタ) |
| **S2** | `JobRunner` interface 導入。goroutine dispatch が `inproc` 実装に。 | 低(リファクタ) |
| **S3** | HTTP transport 実装: signing-secret 検証、event/slash/interactivity 解析、即 200 ACK、DB アクセスなしの ingress。 | 中 |
| **S4** | Cloud Tasks `JobRunner` 実装: ingress が enqueue、`/tasks/run` worker が op で dispatch。 | 中 |
| **S5** | litestream 統合: 起動時 restore、継続 replicate、SIGTERM flush。`store` コード無変更。 | 中 |
| **S6** | Cloud Run デプロイ(単一サービス、`max-instances=1`)、scale-to-zero + コールドスタート + コスト検証、IaC。 | 中 |

`store` パッケージと全 pure-logic パッケージは終始無変更。概算工数: Firestore
経路より小さい — store 抽象化も Firestore 実装も不要。

## 9. コスト試算(オーダー)

- **スタンドアロン(VM):** e2-micro ≈ \$7/月(または無料枠)、24/7 課金。
- **サーバーレス(scale-to-zero):** Cloud Run アイドル = \$0; GCS が
  レプリ DB + WAL を保持(この規模で月数円); Cloud Tasks 無料枠でカバー;
  Vertex AI はポストモーテム実行時のみ課金。現実的に **月数百円以下**で、その
  大半はインシデント時の LLM 呼び出し(両版とも支払う)。slash 遅延のために
  `min-instances=1` が必要なら、小さな常時ウォームインスタンス 1 つのコストを加算。

## 10. 検討した代替(却下)

- **Firestore**(マネージド・真のサーバーレス DB): `SearchKnowledge`、
  `FinalizePMRun`、case 連番の再設計を強い、`store` interface も要するため却下。
  ir-hub の規模では利点なしに変更が大幅に増える。
- **Socket Mode 維持 + Cloud Run `min=1`**(litestream、HTTP 化なし): Socket
  Mode の常時接続が scale-to-zero を妨げ、サーバーレスの節約なしに VM 並の
  コストになるため却下。

## 11. 未決事項(要承認)

1. **リポジトリ構成** — 同一リポジトリ + interface 切替(推奨)vs 別
   リポジトリ `ir-hub-serverless`。
2. **S1–S2 を今やるか** — transport/job-runner リファクタはスタンドアロン版を
   改善し、サーバーレスを de-risk する。本格着手前にやる価値があるか?
3. **`min-instances` ポリシー** — 0 で開始(最安、稀な slash コールドスタートを
   許容)し実測で見直すか、最初から 1 か。
