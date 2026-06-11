# ir-hub

Incident-response lifecycle hub — a one-package Slack ChatOps bot that
supports the full IR lifecycle with LLMs: case channel creation,
in-flight response support, postmortem analysis, and knowledge
accumulation and reuse.

[日本語版 README はこちら](README.ja.md)

> **Status: feature-complete (Phases 1–3).** Case lifecycle
> management, LLM postmortems (auto on close, re-runnable), LLM
> status summaries, knowledge-document generation, and knowledge
> reuse — `@ir-hub` Q&A, new-case briefings, and pluggable storage
> export (local / GCS / S3). See the [RFP](docs/en/ir-hub-rfp.md)
> for the approved design.

## Concept

```
/ir-hub new ──→ case channel ──→ response ──→ /ir-hub close
     │              (status / Q&A support)         │
     │                                             ▼
     └── initial briefing ◀── knowledge store ◀── postmortem
         from past cases       (JSON + Markdown,    (auto + re-runnable)
                                local / GCS / S3)
```

- **Case lifecycle on Slack** — `/ir-hub new` opens a dedicated
  channel per incident; `/ir-hub close` closes the case and runs the
  postmortem automatically
- **LLM postmortems (ai-ir2 theory, rebuilt)** — five analysis
  stages (summary, participant activity, role inference, tactic
  extraction, process review) on Vertex AI Gemini, with nonce-tag
  prompt-injection defense and IoC defanging before AND after the
  model. Analysis is English-canonical; channel posts are translated
  to the configured language
- **Knowledge documents** — each extracted tactic becomes a JSON +
  Markdown pair, indexed (tags / category / summary) in the DB at
  postmortem finalization; re-runs replace a case's knowledge
- **Knowledge reuse** — `@ir-hub <question>` answers from accumulated
  knowledge (citing tactic IDs); `/ir-hub new` posts a briefing of
  relevant past tactics; knowledge is exported as JSON + Markdown to
  local / GCS / S3 storage (`/ir-hub export` and automatically after
  each postmortem) for teams outside IR to consume
- **Dual-mode slash command** — pass arguments for direct execution,
  or run bare `/ir-hub` to pick the action from a modal
- **ACL built in** — whitelist + blacklist by user ID and by Slack
  User Group, fail-safe (no whitelist = deny all), denials are
  audit-logged silently; designed for workspaces with tens of
  thousands of members
- **Continuous ingestion** — messages in open case channels are
  stored in an embedded SQLite DB in real time, with an automatic
  history backfill after reconnects
- **Single Go binary** — Socket Mode resident bot; no inbound
  endpoint, no runtime dependencies

## Commands

| Command | Effect |
|---|---|
| `/ir-hub` | Open a modal to pick the action (no parameters to remember) |
| `/ir-hub new <title> [--severity low\|medium\|high\|critical] [--private\|--public]` | Create the case channel, invite you, post a kickoff message |
| `/ir-hub status` | Post case metadata followed by an LLM situation summary (current status / open items / next actions) |
| `/ir-hub close` | Close the case and run the postmortem automatically (inside the case channel) |
| `/ir-hub reopen` | Reopen a closed case (resumes message ingestion) |
| `/ir-hub pm` | Run (or re-run) the postmortem manually — replaces the case's knowledge documents |
| `/ir-hub export` | Export all knowledge documents to the configured storage backend |
| `@ir-hub <question>` | Knowledge Q&A — answers from accumulated knowledge, citing tactic IDs |

The postmortem posts a compact summary (severity, process score,
strengths/improvements digest, tactic count) and attaches the full
Markdown report as a snippet.

## Prerequisites

- Go 1.26+ (build)
- A Slack app with Socket Mode enabled (setup below)
- A GCP project with the Vertex AI API enabled and Application
  Default Credentials (`gcloud auth application-default login`) —
  the postmortem and status analyses run on Gemini
- For knowledge export: a local directory (default), or a GCS bucket
  (ADC) / S3 bucket (default AWS credential chain). If the cloud
  client can't initialize, export is disabled and the bot keeps
  running.

## Slack app setup

> Full walkthrough — scopes with justifications, admin approval,
> token handling, verification checklist, troubleshooting — in the
> [Slack App Setup Handbook](docs/en/slack-app-setup.md).

Create an app from this manifest (App settings → App Manifest):

```yaml
display_information:
  name: ir-hub
  description: >-
    Incident-response lifecycle hub: opens a channel per case, tracks
    the response, runs postmortems, and reuses the lessons learned.
  long_description: |-
    ir-hub supports security incident-response teams across the full
    lifecycle of a case.

    - /ir-hub new opens a dedicated case channel, invites the opener,
      and posts a kickoff briefing
    - /ir-hub status summarizes the current state of the case
    - /ir-hub close ends the response and runs an automated postmortem
    - Lessons learned are accumulated as knowledge and reused on
      future incidents

    Access is restricted to the IR team by an in-app allowlist, and
    denied attempts are audit-logged. Messages are ingested only from
    case channels the app itself creates.

    Operated by: <your IR team>  /  Questions: #<your-contact-channel>
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

Then generate:

1. **App-level token** (Basic Information → App-Level Tokens) with
   the `connections:write` scope → `IRHUB_SLACK_APP_TOKEN`
2. **Bot token** (Install App) → `IRHUB_SLACK_BOT_TOKEN`

## Installation

```sh
git clone https://github.com/nlink-jp/ir-hub.git
cd ir-hub
make build          # → dist/ir-hub
```

## Configuration

Copy [`config.example.toml`](config.example.toml) to
`~/.config/ir-hub/config.toml` and edit (or pass `--config`). Any
field can be overridden with `IRHUB_*` environment variables
(e.g. `IRHUB_ACL_ALLOW_GROUPS=ir-team,secops`). Unknown keys in the
file are an error, so typos fail fast.

Set `language = "ja"` (or `IRHUB_LANGUAGE=ja`) to switch every
user-facing message — modals, kickoff/status/close posts, errors —
to Japanese. Logs stay English.

Slack tokens go in the `[slack]` section of the config file or in
the environment (environment wins):

| Variable | Description |
|---|---|
| `IRHUB_SLACK_APP_TOKEN` | App-level token (`xapp-…`, `connections:write`) |
| `IRHUB_SLACK_BOT_TOKEN` | Bot token (`xoxb-…`) |

When tokens live in the file, keep it private (`chmod 600`); ir-hub
prints a warning at startup if the file is group/other readable.

**ACL is deny-by-default**: with no `allow_users` / `allow_groups`
configured, every command and mention is denied (and audit-logged).
Populate the allow lists before starting:

```toml
[acl]
allow_groups = ["ir-team"]      # User Group handles or IDs ("S…")
deny_users   = []               # deny wins over allow
notify_denied = false           # true: tell denied users ephemerally
```

Unknown group handles fail startup — another typo guard.

## Running

```sh
export IRHUB_SLACK_APP_TOKEN=xapp-...   # pragma: allowlist secret
export IRHUB_SLACK_BOT_TOKEN=xoxb-...   # pragma: allowlist secret
ir-hub serve
```

The bot reconnects automatically; after each reconnect it backfills
missed messages from `conversations.history`.

## Knowledge export

Configure the storage backend in `[storage]`. Knowledge documents
are written as `<tactic-id>-<slug>.json` and `.md` pairs (under the
local `local_path`, or the S3 `s3_prefix`), both manually
(`/ir-hub export`) and automatically after each postmortem.

```toml
[storage]
backend    = "local"          # local | gcs | s3
local_path = "./knowledge"
# gcs_bucket = "my-ir-knowledge"        # backend = "gcs" (uses ADC)
# s3_bucket  = "my-ir-knowledge"        # backend = "s3" (default AWS chain)
# s3_prefix  = "ir-hub/"
```

GCS uses Application Default Credentials; S3 uses the default AWS
credential chain. Re-exports overwrite in place by deterministic
path.

## Known limitations

- **Message edits/deletions are not ingested** (`message_changed` /
  `message_deleted` are skipped). Raw event JSON is stored, so later
  phases can extend handling.
- **Case sequence numbers may have gaps**: a failed channel creation
  consumes a number by design (kept for audit).
- **Private case channels** cannot be converted to public later, and
  ir-hub must remain a member to keep ingesting.
- **Very long cases are truncated for analysis**: when a case
  exceeds `analysis.max_input_tokens`, the newest messages within
  budget are analyzed and the truncation is noted in the report.
- **Participants appear as Slack user IDs** in reports and knowledge
  documents (no display-name resolution yet).
- **Knowledge retrieval uses tag/keyword (LIKE) narrowing**, not
  vector search — well-suited to hundreds of documents; a larger
  corpus would warrant FTS or embeddings.
- **Re-exported objects can orphan**: a postmortem re-run assigns
  fresh tactic IDs, so a previous run's exported objects may remain
  in storage until manually cleaned.

## Building

```sh
make build          # current platform → dist/ir-hub
make build-all      # 5-platform cross-compile (CGO-free)
make test           # or: go test ./...
make package        # release zips; darwin builds signed + notarized
```

> macOS releases are **Developer ID signed and Apple-notarized**.
> Windows / Linux binaries are unsigned.

## Documentation

- [Slack App Setup Handbook](docs/en/slack-app-setup.md) /
  [日本語](docs/ja/slack-app-setup.ja.md)
- [RFP (approved design)](docs/en/ir-hub-rfp.md) /
  [日本語](docs/ja/ir-hub-rfp.ja.md)

## License

[MIT](LICENSE)
