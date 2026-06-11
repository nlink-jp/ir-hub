# ir-hub デプロイガイド

ir-hub は **常駐型・単一インスタンス**の Slack Bot です。本ガイドは
その理由と、永続的に動かす方法 — 推奨ホストである GCE VM の手順例
付き — を説明します。

## アーキテクチャ上の制約(最初に読む)

設計から導かれる制約で、すべてのデプロイ判断を規定します:

1. **単一インスタンスのみ。** ir-hub は **Socket Mode**(単一の長命
   WebSocket)で Slack に接続します。2 つ起動すると両方が全イベントを
   受信し、案件の二重作成・ポストモーテムの二重実行・二重取り込みが
   起きます。**水平スケールは厳禁**です。リクエストでスケールする
   サービスではなく、低トラフィックの常駐プロセスです。

2. **SQLite DB は永続的なローカルファイルシステムが必須。** ランタイム
   状態(案件メタデータ・取り込みメッセージ・ポストモーテム実行・知見
   インデックス)は内蔵 SQLite(`db.path`、既定
   `~/.local/share/ir-hub/ir-hub.db`)に保存されます。再起動を生き残り、
   かつ **実ローカル FS** であるディスク上に置く必要があります(SQLite は
   WAL・ファイルロック・ランダムアクセスを使う)。

3. **DB をオブジェクトストレージ(GCS FUSE / S3 マウント)に置かない。**
   それらは SQLite が要求するロック・ランダム書き込みのセマンティクスを
   提供せず、DB が破損します。(オブジェクトストレージは *export* 先
   — `[storage]` — としては適切です。あちらは書き込み専用の文書 blob で、
   DB 本体とは異なります。)

## 推奨: GCE VM + 永続ディスク

小さな常時起動 VM が ir-hub の常駐性に合致し、SQLite DB を永続ディスク上に
保持できます。

### 1. プロビジョニング

- 小さな VM で十分(例: `e2-small`)。ir-hub はほぼアイドルです。
- GCE のブートディスクは元々永続なので、ホームディレクトリ配下の既定
  `db.path` は再起動を生き残ります。明確に分離したい場合は専用の永続
  ディスクをアタッチし、`db.path` をそこ(例:
  `/var/lib/ir-hub/ir-hub.db`)に向けます。
- VM のサービスアカウントに Vertex AI(ポストモーテム)と、GCS に
  エクスポートするなら対象バケットへのアクセスを付与します。
  Application Default Credentials が自動的にそのサービスアカウントへ
  解決され、鍵ファイル不要です。

### 2. インストール

```sh
# VM 上で
curl -L -o ir-hub.zip \
  https://github.com/nlink-jp/ir-hub/releases/latest/download/ir-hub-vX.Y.Z-linux-amd64.zip
unzip ir-hub.zip
sudo install ir-hub /usr/local/bin/ir-hub

sudo mkdir -p /etc/ir-hub /var/lib/ir-hub
sudo cp config.example.toml /etc/ir-hub/config.toml
sudo chmod 600 /etc/ir-hub/config.toml   # トークンをここに置く場合
```

`/etc/ir-hub/config.toml` を編集: `db.path = "/var/lib/ir-hub/ir-hub.db"`、
`[gcp] project`、`[acl]` 許可リスト、そして `[slack]` トークン(または
下記のとおり環境変数)を設定します。

### 3. systemd で実行

`/etc/systemd/system/ir-hub.service`:

```ini
[Unit]
Description=ir-hub Slack bot
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/ir-hub serve --config /etc/ir-hub/config.toml
Restart=always
RestartSec=5
User=ir-hub
# トークンを設定ファイルでなく環境変数で渡す(環境変数が優先):
Environment=IRHUB_SLACK_APP_TOKEN=xapp-...
Environment=IRHUB_SLACK_BOT_TOKEN=xoxb-...
# 書き込み可能パスを限定。
StateDirectory=ir-hub
ReadWritePaths=/var/lib/ir-hub

[Install]
WantedBy=multi-user.target
```

```sh
sudo useradd --system --no-create-home ir-hub
sudo chown -R ir-hub:ir-hub /var/lib/ir-hub
sudo systemctl daemon-reload
sudo systemctl enable --now ir-hub
journalctl -u ir-hub -f          # ログ(英語)
```

ir-hub は自動再接続し、再接続後は取りこぼしを backfill するため、
`Restart=always` ポリシーは安全です。

### 4. データベースのバックアップ

知見ベースは貴重です。`db.path` をバックアップしてください:

- **ディスクスナップショット:** GCE 永続ディスクのスナップショットを
  スケジュール。シンプルで WAL に対し十分クラッシュ整合的です。
- **ファイルコピー:** `sqlite3 ir-hub.db ".backup /backup/ir-hub.db"` を
  タイマーで実行し、オンライン整合コピーを取得。
- エクスポート済み知見ドキュメント(`[storage]`)は副次的で人間可読な
  コピーです。DB バックアップの代替ではありませんが有用です。

## なぜ Cloud Run(等のエフェメラル FS コンテナ)を使わないか

Cloud Run の FS は **エフェメラル**で、コールドスタート・再デプロイ・
スケールのたびに消去され、SQLite DB を破壊します。Cloud Run は既定で
オートスケールもし、上記の単一インスタンス制約に反します。

どうしても Cloud Run を使う場合、可能ではありますが **サポート対象外**です:

- インスタンスを 1 に固定: `--min-instances=1 --max-instances=1` +
  CPU 常時割り当て(Socket Mode はリクエスト間もプロセス稼働が必要)。
- SQLite DB を [litestream](https://litestream.io) で GCS に継続
  レプリケーション: 起動時に `litestream restore`、その後
  `litestream replicate` を `ir-hub serve` と並走。エフェメラル FS で
  SQLite を永続化する唯一の安全策です。**GCS FUSE から DB をマウント
  しないこと。**

常時起動 VM はこの複雑さをすべて回避できるため、推奨としています。

## 別ホストへの移行

DB は単一ファイルです。ir-hub を移すには、サービスを停止し、`db.path`
(と `config.toml`)を新ホストにコピーして起動します。スキーマ
マイグレーションは起動時に自動実行されるため、新しいバイナリは古い DB を
その場でアップグレードします。
