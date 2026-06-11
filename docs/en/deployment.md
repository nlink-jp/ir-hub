# ir-hub Deployment Guide

ir-hub is a **resident, single-instance** Slack bot. This guide
explains why, and how to run it durably — with a worked example for a
GCE VM, the recommended host.

## Architectural constraints (read first)

These follow from the design and dictate every deployment decision:

1. **Single instance only.** ir-hub connects to Slack over **Socket
   Mode**, which is a single long-lived WebSocket. Running two
   instances makes both receive every event, causing duplicate case
   creation, double postmortems, and double ingestion. **Never scale
   ir-hub horizontally.** It is a steady, low-traffic resident
   process, not a request-scaled service.

2. **The SQLite database needs durable, local-filesystem storage.**
   Runtime state — case metadata, ingested messages, postmortem runs,
   and the knowledge index — lives in an embedded SQLite database
   (`db.path`, default `~/.local/share/ir-hub/ir-hub.db`). It must
   sit on a disk that survives restarts and is a **real local
   filesystem** (SQLite uses WAL, file locks, and random access).

3. **Do not put the database on object storage (GCS FUSE / S3
   mounts).** Those do not provide the locking and random-write
   semantics SQLite requires; the database will corrupt. (Object
   storage is fine for the *export* target — `[storage]` — which is
   write-only document blobs, just not for the DB itself.)

## Recommended: GCE VM + persistent disk

A small always-on VM matches ir-hub's resident nature and keeps the
SQLite database on a persistent disk.

### 1. Provision

- A small VM is plenty (e.g. `e2-small`); ir-hub is mostly idle.
- The boot disk is already persistent on GCE, so the default
  `db.path` under the home directory survives reboots. For clean
  separation you can attach a dedicated persistent disk and point
  `db.path` at it (e.g. `/var/lib/ir-hub/ir-hub.db`).
- Grant the VM's service account access to Vertex AI (postmortems)
  and, if you export to GCS, the target bucket. Application Default
  Credentials then resolve to that service account automatically — no
  key files.

### 2. Install

```sh
# On the VM
curl -L -o ir-hub.zip \
  https://github.com/nlink-jp/ir-hub/releases/latest/download/ir-hub-vX.Y.Z-linux-amd64.zip
unzip ir-hub.zip
sudo install ir-hub /usr/local/bin/ir-hub

sudo mkdir -p /etc/ir-hub /var/lib/ir-hub
sudo cp config.example.toml /etc/ir-hub/config.toml
sudo chmod 600 /etc/ir-hub/config.toml   # tokens may live here
```

Edit `/etc/ir-hub/config.toml`: set `db.path = "/var/lib/ir-hub/ir-hub.db"`,
your `[gcp] project`, `[acl]` allow-lists, and either the `[slack]`
tokens or supply them via the environment (below).

### 3. Run under systemd

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
# Tokens via the environment instead of the config file (env wins):
Environment=IRHUB_SLACK_APP_TOKEN=xapp-...
Environment=IRHUB_SLACK_BOT_TOKEN=xoxb-...
# Lock down the writable paths.
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
journalctl -u ir-hub -f          # logs (English; structured-ish)
```

ir-hub reconnects automatically and backfills missed messages after
each reconnect, so a `Restart=always` policy is safe.

### 4. Back up the database

The knowledge base is valuable — back up `db.path`:

- **Disk snapshots:** schedule GCE persistent-disk snapshots. Simple
  and crash-consistent enough for WAL.
- **File copy:** `sqlite3 ir-hub.db ".backup /backup/ir-hub.db"` on a
  timer for a consistent online copy.
- The exported knowledge documents (`[storage]`) are a secondary,
  human-readable copy — not a substitute for a DB backup, but useful.

## Why not Cloud Run (or other ephemeral-FS containers)

Cloud Run's filesystem is **ephemeral**: it is wiped on every cold
start, redeploy, and scale event, which would destroy the SQLite
database. Cloud Run also defaults to autoscaling, which violates the
single-instance rule above.

If you must use Cloud Run, it is possible but it is **not the
supported path**:

- Pin to exactly one instance: `--min-instances=1 --max-instances=1`
  with CPU always allocated (Socket Mode needs the process running
  between requests).
- Continuously replicate the SQLite database to GCS with
  [litestream](https://litestream.io): `litestream restore` on
  startup, then `litestream replicate` alongside `ir-hub serve`.
  litestream is the only safe way to persist SQLite on an ephemeral
  filesystem — **do not** mount the DB from GCS FUSE.

A plain always-on VM avoids all of this complexity, which is why it
is the recommendation.

## Migrating to a different host

The database is a single file. To move ir-hub, stop the service,
copy `db.path` (and `config.toml`) to the new host, and start it
there. Schema migrations run automatically on startup, so a newer
binary upgrades an older database in place.
