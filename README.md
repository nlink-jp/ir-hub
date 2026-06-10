# ir-hub

Incident-response lifecycle hub — a one-package Slack ChatOps bot that
supports the full IR lifecycle with LLMs: case channel creation,
in-flight response support, postmortem analysis, and knowledge
accumulation and reuse.

[日本語版 README はこちら](README.ja.md)

> **Status: pre-release.** Phase 1 (bot foundation + lifecycle
> management) is under development. See the
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
  channel per incident; `/ir-hub close` runs the postmortem and
  registers the extracted knowledge
- **Dual-mode slash command** — pass arguments for direct execution,
  or run bare `/ir-hub` to pick the action from a modal
- **In-flight support** — on-demand status summaries
  (`/ir-hub status`) and knowledge Q&A (`@ir-hub <question>`)
- **Knowledge reuse** — finalized knowledge is indexed at postmortem
  time and exported as JSON + Markdown pairs to local / GCS / S3
  storage, where teams outside IR can consume it
- **ACL built in** — whitelist + blacklist by user and by Slack User
  Group; designed for workspaces with tens of thousands of members
- **Powered by Vertex AI Gemini** (ADC authentication)

## Prerequisites

- Go 1.26+
- A Slack app with Socket Mode enabled (see the
  [RFP §5](docs/en/ir-hub-rfp.md#5-required-api-scopes--permissions)
  for the required scopes)
- GCP project with the Vertex AI API enabled and Application Default
  Credentials: `gcloud auth application-default login`

## Installation

```sh
git clone https://github.com/nlink-jp/ir-hub.git
cd ir-hub
make build          # → dist/ir-hub
```

## Configuration

Copy [`config.example.toml`](config.example.toml) to
`~/.config/ir-hub/config.toml` and edit. Any field can be overridden
with `IRHUB_*` environment variables. Slack tokens are environment
variables only — never written to the config file:

| Variable | Description |
|---|---|
| `IRHUB_SLACK_APP_TOKEN` | App-level token (`connections:write`) |
| `IRHUB_SLACK_BOT_TOKEN` | Bot token |

## Usage

```sh
ir-hub --version
# Subcommands (serve, export, ...) arrive with Phase 1 development.
```

## Building

```sh
make build          # current platform → dist/ir-hub
make build-all      # 5-platform cross-compile
make test           # or: go test ./...
make package        # release zips; darwin builds signed + notarized
```

> macOS releases are **Developer ID signed and Apple-notarized**.
> Windows / Linux binaries are unsigned.

## Documentation

- [RFP (approved design)](docs/en/ir-hub-rfp.md) /
  [日本語](docs/ja/ir-hub-rfp.ja.md)

## License

[MIT](LICENSE)
