# AGENTS.md — ir-hub

Contributor onboarding. For end-user documentation see
[README.md](README.md). For design rationale see the project RFP
under `docs/en/ir-hub-rfp.md`.

## Project summary

One-package incident-response lifecycle hub: a resident Slack bot
(Socket Mode) that creates a channel per case, supports the response
in flight, runs an LLM postmortem on close, and accumulates / reuses
the extracted knowledge. Vertex AI Gemini backend. Coexists with
ai-ir2 (one-shot export analysis) and ir-tracker (live web UI).

## Build & test

```sh
make build          # dist/ir-hub
make test           # or: go test ./...
make build-all      # 5-platform cross-compile (Linux x64/ARM, macOS x64/ARM, Windows x64)
make package        # release zips; darwin signed + notarized
make clean
```

Never invoke `go build` directly — `make build` is the only way to
keep `dist/` clean and the version string consistent (auto-derived
from `git describe --tags`).

## Directory layout

```
ir-hub/
├── main.go                 # cobra entry, version injection
├── cmd/root.go             # CLI surface (subcommands arrive in Phase 1)
├── internal/               # private packages (created during Phase 1)
├── scripts/                # codesign / notarize (org templates, verbatim)
├── config.example.toml     # copy to ~/.config/ir-hub/config.toml
├── docs/
│   ├── en/ ja/             # RFP, design docs
└── Makefile
```

Planned internal packages (Phase 1+): `internal/config`,
`internal/slackbot` (Socket Mode loop, dedup, async ACK),
`internal/acl`, `internal/store` (SQLite), `internal/lifecycle`,
`internal/analysis` (postmortem), `internal/knowledge`
(index + export backends).

## Configuration model

Sections in `config.example.toml`: `[gcp]` + `[model]` (org-standard
Vertex AI pattern), `[channel]` (visibility default, name prefix),
`[acl]` (allow/deny users + User Groups, cache TTL, notify_denied),
`[storage]` (local | gcs | s3), `[db]` (SQLite path). Env-var
overrides use the `IRHUB_*` prefix; Slack tokens are env-var only.

## Coding rules

- Go module: `github.com/nlink-jp/ir-hub`
- Tests live alongside the code (`cmd/root_test.go` etc.)
- Inject dependencies (Slack client, LLM client, clock) — design for
  testability; no package-level singletons
- Use `nlk` for prompt-injection defence (`guard`), Vertex AI retry
  (`backoff`), and LLM JSON parsing (`jsonfix`)
- ACL checks guard EVERY entry point (slash command, mention);
  deny-by-default when no whitelist is configured

## Release process

After Phase 3 completes:

1. Update `CHANGELOG.md`
2. Commit `chore: release vX.Y.Z` → tag → push
3. `gh release create` (no assets)
4. `make package` → upload zips one by one
5. Add as submodule under `nlink-jp/cybersecurity-series`
6. Update `nlink-jp/.github/profile/README.md` (alphabetical)
7. Run `check-org.sh`

## Gotchas

- **3-second rule**: Slack slash-command ACK and `trigger_id`
  consumption must happen within 3 s. Never call the LLM before
  ACKing.
- **Socket Mode redelivery**: envelopes are redelivered after
  reconnects; handlers must be idempotent / deduped.
- **Slash command visibility cannot be restricted by Slack** — the
  app-side ACL is the only gate. Default reply to denied users is
  silent + audit log (noise control in a ~50k-member workspace).
- **Channel names**: lowercase, ≤ 80 chars, unique workspace-wide
  including archived channels — include a sequence/date in the name.
- **Long reports**: Block Kit section limit is 3,000 chars; split or
  post as file snippets (`files:write`).
- Keep `CGO_ENABLED=0` cross-compile working: prefer a pure-Go
  SQLite driver.
- Per org ADR-001, use Gemini 2.5 until Gemini 3 GA; add ir-hub to
  the org migration list when it ships.
