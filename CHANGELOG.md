# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.2.0] - 2026-06-11

### Added

- **Phase 2: LLM postmortems + in-flight support (Vertex AI Gemini).**
  - `/ir-hub pm` (slash + modal action) and automatic postmortem on
    `/ir-hub close`: five analysis stages — summary, participant
    activity, role inference, tactic extraction (4 in parallel), and
    a process review consuming only their structured outputs —
    ported from ai-ir2's theory
  - Knowledge documents: each tactic becomes a canonical-English
    JSON + Markdown pair, indexed (tactic ID `tac-YYYYMMDD-NNN`,
    tags, category, summary) in SQLite at postmortem finalization;
    re-runs replace the case's knowledge atomically
  - Channel output: compact summary post + full Markdown report
    attached as a snippet (translated to the configured language;
    PostMessage fallback when the upload fails)
  - `/ir-hub status` now follows the metadata block with an LLM
    situation summary (current status / open items / next actions)
  - Security: nonce-tag prompt-injection defense (nlk/guard) with
    the defense preamble first, advisory injection-pattern logging,
    and IoC defanging both before and after the model
  - Robustness: tolerant JSON decoding (nlk/jsonfix + type coercion
    + enum normalization), token-budgeted newest-N input with
    truncation notes, bot-post exclusion, transient-only LLM retries
    with backoff, single-run-per-case guard, stale-run cleanup at
    startup
  - Config: `[analysis]` section (`request_timeout`,
    `max_input_tokens`); `gcp.project` is now required for `serve`
  - DB: versioned schema migrations (v1 → v2 adds `pm_runs` and
    `knowledge` tables; existing databases migrate automatically)

## [0.1.0] - 2026-06-10

### Added

- **Phase 1: bot foundation + case lifecycle management (no LLM).**
  - `ir-hub serve`: resident Slack bot over Socket Mode with
    automatic reconnect, redelivered-envelope dedup, immediate acks
    (3-second rule), and graceful shutdown draining in-flight work
  - `/ir-hub new <title> [--severity] [--private|--public]`: creates
    the case channel (sequence-numbered name; slugs keep Unicode
    letters so Japanese titles stay readable), invites the opener,
    posts a kickoff message
  - `/ir-hub close` and `/ir-hub status` (metadata summary)
  - Bare `/ir-hub` opens a Block Kit modal: action picker → new-case
    form with validation
  - ACL: whitelist + blacklist by user ID and Slack User Group
    (handle or `S…` ID) with TTL-cached membership; deny-by-default
    without a whitelist; silent audit-logged denials (ephemeral
    notify opt-in); unknown group handles/IDs fail startup
  - Continuous message ingestion from open case channels into
    embedded SQLite (pure-Go driver), plus history backfill after
    reconnects with rate-limit handling
  - Strict TOML config (`unknown keys are errors`) with `IRHUB_*`
    env overrides; Slack tokens via the `[slack]` section or
    environment variables (env wins), with a startup warning when
    the config file is group/other readable (expected 0600)
  - Japanese UI support: `language = "en" | "ja"` switches all
    user-facing messages (modals, lifecycle posts, errors) via an
    en/ja message catalog with completeness and fmt-verb-parity
    tests; logs stay English
- Project scaffold: cobra CLI skeleton with `--version`, Makefile
  (`build` / `build-all` / `package` / `test` / `clean` → `dist/`),
  Developer ID codesign + notarize scripts, configuration template
- Approved project RFP (`docs/en/ir-hub-rfp.md` /
  `docs/ja/ir-hub-rfp.ja.md`)

### Docs

- Slack App Setup Handbook (`docs/en/slack-app-setup.md` /
  `docs/ja/slack-app-setup.ja.md`): manifest walkthrough, per-scope
  justifications for admin approval, token handling, verification
  checklist, troubleshooting
- Recommended App description copy (short / long, EN + JA) embedded
  in the manifests in the README and handbook
