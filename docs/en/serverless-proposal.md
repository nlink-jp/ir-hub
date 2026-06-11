# ir-hub Serverless Edition — Design Proposal

> **Status: Proposal (draft, not yet approved).** This document
> frames the design and decisions for a serverless variant. No code
> has been written against it.

## 1. Goal

Keep the current **Standalone Edition** (Socket Mode + embedded
SQLite, runs on a VM) as-is, and add a **Serverless Edition** that
runs scale-to-zero on Cloud Run so it costs almost nothing while
idle — a good fit for ir-hub's low-frequency, incident-driven usage.
Both editions are built from **one shared core**; only three I/O
layers differ.

## 2. The two editions

| | Standalone (current) | Serverless (proposed) |
|---|---|---|
| Slack transport | Socket Mode (WebSocket) | HTTP Events API + Interactivity + Slash (Request URLs) |
| Database | embedded SQLite | Firestore (managed, serverless) |
| Async work | in-process goroutines | Cloud Tasks → worker endpoint |
| Host | always-on VM | Cloud Run (scale-to-zero) |
| Scale-to-zero | no | **yes** (idle = \$0) |
| Cost | ~VM (e.g. e2-micro) | ~hundreds of yen/mo, incident-driven |
| Best for | simple, self-hosted, offline-ish | cheapest, GCP-native |

The editions are not a fork: they share the same analysis, knowledge,
ACL, and command logic. The difference is wiring, selected at build
or config time.

## 3. Shared core (ports unchanged)

These packages are pure logic or already interface-driven and move to
the serverless edition without change:

`analysis`, `knowledge`, `acl`, `command`, `modal`, `defang`,
`sanitize`, `msg`, `llm`, `userdir`, `channelname`, `storage`,
`export`, plus the `slackapi.API` interface (all Web API calls are
identical in both editions).

Critically, the bot's **handler functions are already
transport-agnostic** — `runNew`, `runPM`, `runClose`, `runStatus`,
`runQA`, `runExport`, `runReopen` all take plain `(ctx, channelID,
userID, …string)` arguments. They call store + analyzer + the Slack
Web API; none of them touch Socket Mode. This is what makes a shared
core realistic.

## 4. The three I/O seams to abstract

The work is introducing three interfaces in the shared core, with two
implementations each:

1. **Store** — today every consumer (`bot`, `cases`, `analysis`,
   `ingest`, `export`) takes the concrete `*store.Store`. Introduce a
   `Store` interface (≈19 methods, 11 hot) with `sqlite` and
   `firestore` implementations. This is the largest change.

2. **Transport** — a thin `Socket` interface already exists
   (`bot/socket.go`). Generalize the event intake so the same handler
   functions are reachable from either the Socket Mode loop or HTTP
   request handlers (parse the Slack payload → call `runX`). The
   3-second ack becomes "return HTTP 200 immediately, do work async."

3. **Job runner** — the `dispatch(fast, fn)` goroutine+semaphore
   mechanism becomes a `JobRunner` interface: `inproc` (current
   goroutines) and `cloudtasks` (enqueue an HTTP task carrying the
   handler name + its string args). Handlers already take simple
   serializable args, so the task payload is small JSON like
   `{"op":"pm","channel":"C…","user":"U…"}`.

## 5. The three genuinely hard parts (Firestore)

Firestore is not SQL; three SQLite-specific mechanisms need redesign.
All are solvable, and ir-hub's small scale makes them easier.

| Hard part | Why | Proposed solution |
|---|---|---|
| **`SearchKnowledge` (LIKE)** | Firestore has no substring/LIKE search | Load all knowledge and filter in memory. The corpus is small (tens of docs), and the RFP already mandates "full-text context load, no vector RAG," so this *matches* the intended design rather than fighting it. Q&A already falls back to "load all" today. |
| **`FinalizePMRun` (single tx: replace knowledge + day-scoped tactic-ID `tac-YYYYMMDD-NNN`)** | Firestore tx can't `MAX()`-scan for the next sequence | A per-day counter document incremented inside a Firestore transaction; the (≤~10) knowledge writes for one case fit a single Firestore tx (500-doc limit). Deterministic IDs are preserved. |
| **Case `AUTOINCREMENT` (drives `ir-0042-…` channel names)** | Firestore has no `LastInsertId` | A single `case_seq` counter document, incremented transactionally on case creation. Human-readable sequence numbers are preserved (don't switch to UUIDs — the numbering is user-facing). |

None forces a consistency downgrade for ir-hub's volume: a single
incident writes a handful of docs, well within Firestore's
transactional limits.

## 6. Proposed serverless architecture

```
Slack ──HTTP POST──▶ Cloud Run (ingress, scale-to-zero)
  (events / slash /        │  1. verify signing secret
   interactivity)          │  2. return 200 within 3s
                           │  3. enqueue Cloud Task (op + args)
                           ▼
                     Cloud Tasks
                           │
                           ▼
                  Cloud Run (worker)  ──▶ Firestore (state + knowledge)
                  runPM / runQA / …   ──▶ Vertex AI (postmortem)
                                      ──▶ GCS/S3 (knowledge export)
```

- **Ingress** is cheap and fast: verify, ack, enqueue. It can
  scale-to-zero between events; Slack retries on non-2xx.
- **Worker** runs the actual handler (PM can take minutes — Cloud Run
  request timeout is up to 60 min, ample). Also scales to zero.
- **Ingestion**: the HTTP `message` event callback feeds the *same*
  `ingest.HandleMessage(*slackevents.MessageEvent)` path — no logic
  change. Reconnect-backfill is replaced by Slack's own event
  delivery + retries (simpler).

## 7. Technology choices

| Decision | Options | Recommendation |
|---|---|---|
| Repo layout | (a) same repo, interfaces + build/config switch; (b) separate repo + shared library | **(a)** — one core, two wirings; avoids drift |
| Database | Firestore; Cloud SQL (Postgres); Cloud Run+litestream (keeps SQLite, but no scale-to-zero) | **Firestore** — the only option that truly scales to zero |
| Async | Cloud Tasks; Pub/Sub; Workflows | **Cloud Tasks** — HTTP-native, simple retries, per-task |
| Knowledge search | in-memory filter; Algolia/Typesense | **in-memory** at current scale; revisit if the corpus grows large |
| Host | Cloud Run; Cloud Functions (2nd gen = Cloud Run) | **Cloud Run** |
| Slack auth | signing secret (HTTP) replaces the Socket app-level token | add `[slack] signing_secret`, drop `app_token` for this edition |

## 8. Phased plan

Phases S1–S2 are pure refactors of the *current* code (introduce
interfaces, keep the SQLite/Socket/goroutine implementations behind
them). They ship in the Standalone Edition with no behavior change
and de-risk everything after.

| Phase | Work | Risk |
|---|---|---|
| **S1** | Introduce the `Store` interface; current SQLite becomes one impl. No behavior change. | low (refactor) |
| **S2** | Introduce `Transport` + `JobRunner` interfaces; Socket Mode + goroutines become impls. | low (refactor) |
| **S3** | Firestore `Store` impl: counters for case-seq and tactic-IDs, in-memory knowledge search, schema-version doc. | medium |
| **S4** | HTTP transport: signing-secret verification, event/slash/interactivity parsing, 3s-ack pattern. | medium |
| **S5** | Cloud Tasks `JobRunner`: ingress enqueues, worker endpoint dispatches by op. | medium |
| **S6** | Cloud Run deploy (ingress + worker), scale-to-zero validation, real cost measurement, IaC. | medium |

Rough effort: ~2–3 weeks. S1–S2 are worth doing regardless, as they
improve testability of the Standalone Edition too.

## 9. Cost estimate (order of magnitude)

- **Standalone (VM):** e2-micro ≈ \$7/mo (or free-tier eligible),
  billed 24/7.
- **Serverless (scale-to-zero):** Cloud Run idle = \$0; Firestore at
  low traffic is within/near the free tier; Cloud Tasks free tier
  covers this volume; Vertex AI is billed only when a postmortem
  runs. Realistically **a few hundred yen/month or less**, dominated
  by incident-time LLM calls — which both editions pay anyway.

The serverless edition wins on idle cost; the VM wins on operational
simplicity and no per-request cold starts.

## 10. Open decisions (need sign-off)

1. **Repo layout** — same repo with interface switch (recommended) vs
   a separate `ir-hub-serverless` repo.
2. **Database** — Firestore (recommended) vs Cloud SQL.
3. **Scope of S1–S2 now** — do the interface refactor in the
   Standalone Edition immediately (improves it, de-risks serverless)
   even before committing to the full serverless build?
4. **Knowledge search at scale** — accept in-memory filtering, or plan
   for a full-text service if the corpus is expected to grow large.

## 11. Risks

- **Store interface surface is wide** (~19 methods); the abstraction
  must not leak SQLite-isms (e.g. `sql.NullString`, `LIKE` strings).
  Mitigated by designing the interface around intent, not SQL.
- **Firestore eventual consistency** on list queries — fine for
  knowledge/case listing, but tactic-ID counters and case-seq must use
  transactions for correctness.
- **Cold starts** add latency to the first event after idle; ack
  happens on the ingress which stays small, so the user-visible 3s
  rule is safe, but the *first* PM after idle pays a cold start.
- **Two code paths to maintain** — kept minimal by sharing the core;
  only impls differ. CI must build/test both editions.
