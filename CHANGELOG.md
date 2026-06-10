# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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
