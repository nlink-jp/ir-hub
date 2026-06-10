# ir-hub

Incident-response lifecycle hub — a one-package Slack ChatOps bot that
supports the full IR lifecycle with LLMs: case channel creation,
in-flight response support, postmortem analysis, and knowledge
accumulation and reuse.

[日本語版 README はこちら](README.ja.md)

> **Status: pre-release.** Phase 1 (bot foundation + lifecycle
> management, no LLM yet) is implemented. LLM postmortems arrive in
> Phase 2, knowledge reuse in Phase 3. See the
> [RFP](docs/en/ir-hub-rfp.md) for the approved design.

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
  channel per incident; `/ir-hub close` closes the case (and will run
  the postmortem from Phase 2 on)
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
| `/ir-hub status` | Post case metadata: state, severity, open duration, ingested message count |
| `/ir-hub close` | Close the case (run inside the case channel) |
| `@ir-hub <question>` | Knowledge Q&A — answers arrive in Phase 3; Phase 1 replies with a notice |

## Prerequisites

- Go 1.26+ (build)
- A Slack app with Socket Mode enabled (setup below)

## Slack app setup

> Full walkthrough — scopes with justifications, admin approval,
> token handling, verification checklist, troubleshooting — in the
> [Slack App Setup Handbook](docs/en/slack-app-setup.md).

Create an app from this manifest (App settings → App Manifest):

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
allow_groups = ["ir-team"]      # Slack User Group handles
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

## Known limitations (Phase 1)

- **Message edits/deletions are not ingested** (`message_changed` /
  `message_deleted` are skipped). Raw event JSON is stored, so later
  phases can extend handling.
- **Case sequence numbers may have gaps**: a failed channel creation
  consumes a number by design (kept for audit).
- **Private case channels** cannot be converted to public later, and
  ir-hub must remain a member to keep ingesting.
- LLM features (`/ir-hub pm`, status summaries, Q&A, briefings,
  knowledge export) arrive in Phases 2–3.

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
