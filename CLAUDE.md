# CLAUDE.md — ir-hub

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Project overview

Incident-response lifecycle hub for the `nlink-jp`
cybersecurity-series. A resident Slack bot (Socket Mode, single Go
binary) that opens a dedicated channel per case, supports the
response in flight (status summaries, knowledge Q&A), runs an LLM
postmortem on close, and accumulates the extracted knowledge for
reuse — including by teams outside IR via exported JSON + Markdown.

Coexists permanently with ai-ir2 (one-shot analysis of exported
data) and ir-tracker (live web UI); theory, prompts, and defenses
are carried over but the code is rebuilt here.

## Non-negotiable rules

- **Tests are mandatory** — write them with the implementation
- **Never `go build` directly** — always `make build` (outputs to `dist/`)
- **Docs in sync** — update `README.md` and `README.ja.md` together
- **Small, typed commits** — `feat:`, `fix:`, `test:`, `chore:`, `docs:`, `refactor:`, `security:`
- **Security first** — ACL enforced at every entry point,
  prompt-injection defence via `nlk/guard`, IoC defanging in
  LLM-bound text, no secrets/PII in code or docs
- **ACL is fail-safe** — with no whitelist configured, everything is
  denied. Never weaken this default.

## Build & test

```sh
make build          # → dist/ir-hub
make test           # or: go test ./...
make build-all      # cross-compile 5 platforms
```

## Configuration

Settings load order: built-in defaults → TOML file → env vars → CLI flags.

- **Config file**: `~/.config/ir-hub/config.toml` (or flag)
- **Env vars**: `IRHUB_*`; Slack tokens are env-var only
  (`IRHUB_SLACK_APP_TOKEN`, `IRHUB_SLACK_BOT_TOKEN`)

Schema: see `config.example.toml` — sections `[gcp]`, `[model]`,
`[channel]`, `[acl]`, `[storage]`, `[db]`.

## Key dependencies (planned)

- `github.com/slack-go/slack` — Socket Mode + Web API
- `google.golang.org/genai` — Vertex AI Gemini SDK (NOT the
  deprecated vertexai/genai)
- `github.com/nlink-jp/nlk` — `guard` / `backoff` / `jsonfix`
  (verify API signatures against the real files before use)
- `github.com/spf13/cobra`, `github.com/BurntSushi/toml`
- SQLite driver (pure-Go preferred to keep CGO_ENABLED=0 cross-compile)

## Design constraints (from the RFP — canonical scope source)

- Slash command ACK and `trigger_id` use within **3 seconds**: all
  LLM work is async (immediate ACK, posted results)
- Socket Mode reconnects routinely; dedupe redelivered envelopes
- Knowledge retrieval: **no chunk+vector RAG.** Index at postmortem
  finalization (tags/summary), narrow via FTS + tags, load narrowed
  docs full-text into the context
- Per org ADR-001: Gemini 2.5 until Gemini 3 GA
- Out of scope: automatic alert ingestion (SIEM / mail-triage)

## Design references

- [`docs/en/ir-hub-rfp.md`](docs/en/ir-hub-rfp.md) /
  [`docs/ja/ir-hub-rfp.ja.md`](docs/ja/ir-hub-rfp.ja.md)
  — approved RFP; canonical source for scope decisions

## Communication Language

All communication between contributors and Claude Code is conducted
in **Japanese**.
