# AGENTS.md — ir-hub

> **Archived (2026-08-30)** — this repository is read-only and no longer
> maintained. Kept for reference only; do not plan new work here.

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
make verify-release  # gate: .notarized marker + freshness (run before upload)
make clean
```

Never invoke `go build` directly — `make build` is the only way to
keep `dist/` clean and the version string consistent (auto-derived
from `git describe --tags`).

## Directory layout

```
ir-hub/
├── main.go                 # cobra entry, version injection
├── cmd/
│   ├── root.go             # root command, --config persistent flag
│   └── serve.go            # `ir-hub serve` — wiring only, no logic
├── internal/
│   ├── config/             # strict TOML + IRHUB_* env (unknown keys error)
│   ├── msg/                # en/ja user-facing message catalog (language key)
│   ├── store/              # SQLite (modernc.org/sqlite): cases/messages/acl_denials/meta
│   ├── command/            # slash-command text parser (dependency-free)
│   ├── channelname/        # <prefix><%04d seq>[-<slug>] generator
│   ├── slackapi/           # Web API interface boundary + slack-go adapter
│   │   └── slackapitest/   # configurable fake for dependent tests
│   ├── acl/                # deny→allow evaluation, User Group TTL cache
│   ├── cases/              # lifecycle use-cases: New/Close/Status
│   ├── modal/              # Block Kit modals build + submission parse
│   ├── ingest/             # message events + reconnect backfill
│   ├── defang/             # IoC defanging (pure functions, ai-ir2 port)
│   ├── sanitize/           # advisory injection-pattern detection
│   ├── llm/                # LLM Client boundary + Vertex AI adapter
│   │   └── llmtest/        # marker-routed fake for pipeline tests
│   ├── analysis/           # postmortem + status + Q&A (reuse.go) + briefing
│   ├── knowledge/          # tactic → JSON+MD pair, slugs
│   ├── storage/            # Backend iface + local/gcs/s3 + storagetest fake
│   ├── export/             # knowledge export service (store → storage)
│   ├── userdir/            # user ID → "display name (ID)" resolver (cached)
│   └── bot/                # socketmode loop, ack/dedup/dispatch/shutdown, PM/Q&A/export wiring
├── scripts/                # codesign / notarize (org templates, verbatim)
├── config.example.toml     # copy to ~/.config/ir-hub/config.toml
├── docs/
│   ├── en/ ja/             # RFP, design docs
└── Makefile
```

Dependency direction: `bot → {acl, cases, modal, ingest, command,
analysis (Analyzer interface), export (Exporter interface),
knowledge}`; `analysis → {llm, defang, sanitize, knowledge, store,
msg}`; `export → {store, storage, knowledge}`; `storage → config`;
`cases/ingest/acl → slackapi (interface) + store`;
`userdir → slackapi`; `analysis/cases → userdir`. No package-level
singletons — everything is constructor-injected, clocks via
`func() time.Time`, waiting via a small Sleeper interface.

Knowledge retrieval (RFP): index at PM finalization (tags / category
/ summary), narrow via tag + keyword LIKE (`store.SearchKnowledge`),
load the narrowed docs' full text into the context — NO chunk+vector
RAG (see CLAUDE.md). FTS / embeddings are a future option if the
corpus outgrows LIKE.

## Configuration model

Sections in `config.example.toml`: `[gcp]` + `[model]` (org-standard
Vertex AI pattern; project REQUIRED for serve since Phase 2),
`[channel]` (visibility default, name prefix), `[acl]` (allow/deny
users + User Groups, cache TTL, notify_denied), `[analysis]`
(request_timeout, max_input_tokens), `[storage]` (local | gcs | s3),
`[db]` (SQLite path), `[slack]` (tokens; optional — env wins).
Env-var overrides use the `IRHUB_*` prefix. Load warns when the
config file is group/other readable (`cfg.Warnings`, printed by
serve).

## Coding rules

- Go module: `github.com/nlink-jp/ir-hub`
- Tests live alongside the code (`cmd/root_test.go` etc.)
- Inject dependencies (Slack client, LLM client, clock) — design for
  testability; no package-level singletons
- Use `nlk` for prompt-injection defence (`guard`), Vertex AI retry
  (`backoff`), and LLM JSON parsing (`jsonfix`)
- ACL checks guard EVERY entry point (slash command, mention);
  deny-by-default when no whitelist is configured
- Every user-visible string goes through `internal/msg` (en + ja).
  Never hardcode user-facing text in bot/cases/modal; add a Catalog
  field with BOTH translations (tests enforce completeness and
  fmt-verb parity). Logs stay English.

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
  consumption must happen within 3 s. The bot acks in handleEvent
  before dispatching; `views.open` and view pushes bypass the
  concurrency semaphore (`dispatch(true, …)`). Never put slow work
  before an ack.
- **View pushes over Socket Mode**: pass
  `slack.NewPushViewSubmissionResponse` / `NewErrorsViewSubmissionResponse`
  as the payload of `socketmode.Client.Ack()` — this is the
  documented pattern, do not call views.update instead.
- **Slash replies use response_url** (`slackapi.PostResponse`), not
  PostEphemeral — the bot is usually not a member of the channel
  where /ir-hub was typed.
- **Socket Mode redelivery**: envelopes are redelivered after
  reconnects; handlers must be idempotent / deduped.
- **Slash command visibility cannot be restricted by Slack** — the
  app-side ACL is the only gate. Default reply to denied users is
  silent + audit log (noise control in a ~50k-member workspace).
- **Channel names**: ≤ 80 chars, unique workspace-wide including
  archived channels. Slugs keep Unicode letters/digits (Japanese
  titles stay readable: `ir-0001-情報漏えい`); uniqueness comes from
  the DB sequence number, never from the slug.
- **Long reports**: Block Kit section limit is 3,000 chars; split or
  post as file snippets (`files:write`).
- Keep `CGO_ENABLED=0` cross-compile working: prefer a pure-Go
  SQLite driver.
- Per org ADR-001, use Gemini 2.5 until Gemini 3 GA; add ir-hub to
  the org migration list when it ships.
- **Analysis is English-canonical**: pm_runs/report and knowledge
  documents are stored in English; only channel-facing renders go
  through `analysis.Translate` (narrative fields only, field-level
  English fallback). Don't store translated artifacts.
- **The review stage must never see raw messages** — it consumes the
  structured outputs of the other four stages (ai-ir2 design; a test
  enforces it).
- **Defang twice**: conversation text before the LLM AND the model's
  raw response before JSON parsing (models refang). `defang.Text` is
  idempotent on already-defanged forms.
- **FinalizePMRun holds the single SQLite connection's write tx** —
  never put LLM calls inside it; build everything first.
- **Chained background work must run inline, not re-dispatched** —
  close→PM and new→briefing run inside the already-dispatched
  goroutine. A nested `dispatch()` is skipped once `Wait()` sets
  draining, so the follow-up would silently never run.
- **Q&A / briefing inputs are nonce-wrapped too** — the mention
  question (user input) and the knowledge docs/summaries
  (LLM-derived from user content) all go through `guard.Tag.Wrap`,
  defang first, preamble first. Treat them as untrusted.
- **Storage degrades, never crashes** — `storage.New` failure at
  serve is logged and export is left disabled (nil Exporter); the
  bot still runs. Auto-export failure after a PM is logged only,
  never fails the postmortem.
- **Single instance, durable local DB** — Socket Mode is one
  WebSocket, so ir-hub must run as exactly one instance (two would
  double every event). The SQLite DB needs a real persistent
  filesystem; never an object-store mount (GCS FUSE / S3 corrupts
  SQLite). Recommended host is an always-on VM, not an ephemeral-FS
  container (Cloud Run). See `docs/en/deployment.md`.
- **User-name resolution runs AFTER the LLM** — `userdir` resolves
  `display (ID)` post-analysis (in `analysis.resolveReport` and
  `cases.Status`). Never put names into a prompt. report.go renders
  the resolved string directly (no `<@ID>` — that reverts to a raw
  ID in the stored/exported copy). Resolver is nil-safe (raw ID
  fallback), so tests pass without one.
