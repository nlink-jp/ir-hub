# ir-hub Serverless Edition — Design Proposal

> **Status: Proposal (draft, not yet approved).** This document
> frames the design and decisions for a serverless variant. No code
> has been written against it.

## 1. Goal

Keep the current **Standalone Edition** (Socket Mode + embedded
SQLite, runs on a VM) as-is, and add a **Serverless Edition** that
runs scale-to-zero on Cloud Run so it costs almost nothing while
idle — a good fit for ir-hub's low-frequency, incident-driven usage.
Both editions are built from **one shared core**.

## 2. Chosen approach: keep SQLite, replicate it, run on HTTP

The key decision: **do not move off SQLite.** Instead, run a single
Cloud Run instance, replicate the SQLite database to object storage
with [litestream](https://litestream.io), and switch Slack transport
to HTTP so the instance can scale to zero between events.

This keeps SQLite's strengths and **sidesteps the three hard
Firestore problems entirely** (see §6) — there is no `SearchKnowledge`
LIKE rewrite, no `FinalizePMRun` transaction redesign, no
AUTOINCREMENT replacement, and **the `store` package ports
unchanged**. ir-hub's very low request rate and relaxed latency
needs make the one cost of this approach — a cold-start database
restore of a few seconds — acceptable.

| | Standalone (current) | Serverless (proposed) |
|---|---|---|
| Slack transport | Socket Mode (WebSocket) | HTTP Events API + Interactivity + Slash |
| Database | embedded SQLite | **embedded SQLite + litestream → GCS** |
| Async work | in-process goroutines | Cloud Tasks → worker endpoint (same service) |
| Host | always-on VM | Cloud Run, `max-instances=1`, scale-to-zero |
| Scale-to-zero | no | **yes** (idle = \$0) |

## 3. Shared core (ports unchanged)

Pure-logic and interface-driven packages move over without change:
`analysis`, `knowledge`, `acl`, `command`, `modal`, `defang`,
`sanitize`, `msg`, `llm`, `userdir`, `channelname`, `storage`,
`export`, the `slackapi.API` interface — **and crucially the entire
`store` package** (SQLite stays).

The bot's **handler functions are already transport-agnostic** —
`runNew`, `runPM`, `runClose`, `runStatus`, `runQA`, `runExport`,
`runReopen` take plain `(ctx, channelID, userID, …string)` args and
call store + analyzer + the Slack Web API. Nothing about them is
Socket-specific.

## 4. The two I/O seams to abstract

Because SQLite stays, only **two** interfaces are introduced (the
Firestore edition would have needed a third — a `Store` interface):

1. **Transport** — a thin `Socket` interface already exists
   (`bot/socket.go`). Generalize event intake so the same handlers are
   reachable from either the Socket Mode loop (standalone) or HTTP
   request handlers (serverless). The 3-second ack becomes "return
   HTTP 200 immediately, do work async."

2. **Job runner** — the `dispatch(fast, fn)` goroutine+semaphore
   mechanism becomes a `JobRunner` interface: `inproc` (current
   goroutines, standalone) and `cloudtasks` (enqueue an HTTP task with
   the handler name + its string args, serverless). Handlers already
   take simple serializable args, so a task payload is small JSON like
   `{"op":"pm","channel":"C…","user":"U…"}`.

The `store` package needs **no interface and no change**.

## 5. Architecture (single service, single writer)

The non-obvious constraint: **SQLite is single-writer, so ingress and
worker must live in the same Cloud Run service / instance.** Two
separate services couldn't share the SQLite file. So:

```
Slack ──HTTP──▶ Cloud Run service (max-instances=1, scale-to-zero)
                  │
  /slack/* (ingress paths)        /tasks/run (worker path)
  • verify signing secret         • the only DB-touching path
  • return 200 within 3s          • handler dispatch (runPM, runQA…)
  • enqueue Cloud Task ───────▶   • Vertex AI / GCS export
  • NO database access               ▲
                                     │
                          Cloud Tasks (same service target)
                                     │
   litestream  ◀── restore on start / continuous WAL replicate ──▶ GCS
```

- **ingress paths touch no database** — they verify, ack, and enqueue.
  This keeps the 3-second response fast even on a cold start (no
  restore needed to ack).
- **the worker path is the only DB consumer** — its cold start runs
  `litestream restore`; because it's invoked via Cloud Tasks (async,
  retried), it is not bound by the 3-second rule.
- **`max-instances=1`** guarantees one SQLite writer. Cloud Tasks
  targets the same service, so the worker runs on the same instance.
  `modernc` SQLite with `SetMaxOpenConns(1)` (already configured)
  serializes writes within the instance.
- **ingestion** (`message` events) also routes through enqueue → the
  worker's `ingest.HandleMessage` path, so ingress stays DB-free.
- **litestream** restores on startup and streams the WAL to GCS
  continuously, flushing on `SIGTERM` (Cloud Run's shutdown signal).
  Loss window is seconds, far better than a periodic full-DB sync.

## 6. Why this beats the Firestore route

A Firestore edition would have to redesign three SQLite-specific
mechanisms. Keeping SQLite **avoids all of them**:

| Hard part (Firestore) | With SQLite + litestream |
|---|---|
| `SearchKnowledge` LIKE has no Firestore equivalent | unchanged — SQLite LIKE works |
| `FinalizePMRun` single tx + day-scoped `tac-YYYYMMDD-NNN` | unchanged — SQLite transaction works |
| case `AUTOINCREMENT` drives `ir-0042-…` channel names | unchanged — AUTOINCREMENT works |
| schema-version migrations | unchanged |

This is why the chosen approach is also the **smaller change**.

## 7. Open design points (carried into implementation)

1. **Cold-start latency vs. slash 3s.** With `min-instances=0`, the
   first slash command after idle pays a container cold start; if it
   exceeds 3s, Slack shows a failure and the user re-runs. Events and
   interactivity are retried by Slack automatically, so only
   user-typed slash is exposed. Mitigation if it bites:
   `min-instances=1` (always-warm, but loses full scale-to-zero, ~a
   small fixed cost) — a knob, decided by measurement.
2. **Instance handoff.** During a deploy or instance replacement, two
   instances can momentarily overlap. litestream detects competing
   writers; combined with `max-instances=1` and brief deploys the risk
   is small, but the cutover must drain writes first.
3. **Worker timeout.** A postmortem takes minutes; Cloud Run request
   timeout (up to 60 min) covers it — the worker request stays open
   for the duration, keeping the instance alive.

## 8. Phased plan

Phases S1–S2 are pure refactors of the *current* code (introduce two
interfaces, keep Socket Mode + goroutines behind them). They ship in
the Standalone Edition with no behavior change and improve its
testability, de-risking the rest.

| Phase | Work | Risk |
|---|---|---|
| **S1** | Introduce the `Transport` interface; generalize event intake so handlers are transport-agnostic. Socket Mode becomes one impl. | low (refactor) |
| **S2** | Introduce the `JobRunner` interface; goroutine dispatch becomes the `inproc` impl. | low (refactor) |
| **S3** | HTTP transport impl: signing-secret verification, event/slash/interactivity parsing, immediate-200 ack, ingress with no DB access. | medium |
| **S4** | Cloud Tasks `JobRunner` impl: ingress enqueues, `/tasks/run` worker dispatches by op. | medium |
| **S5** | litestream integration: restore on start, continuous replicate, SIGTERM flush. `store` code unchanged. | medium |
| **S6** | Cloud Run deploy (single service, `max-instances=1`), scale-to-zero + cold-start + cost validation, IaC. | medium |

The `store` package and all pure-logic packages are untouched
throughout. Rough effort: smaller than the Firestore route — no store
abstraction, no Firestore impl.

## 9. Cost estimate (order of magnitude)

- **Standalone (VM):** e2-micro ≈ \$7/mo (or free-tier), billed 24/7.
- **Serverless (scale-to-zero):** Cloud Run idle = \$0; GCS holds the
  replicated DB + WAL (cents/mo at this size); Cloud Tasks free tier
  covers the volume; Vertex AI billed only when a postmortem runs.
  Realistically **a few hundred yen/month or less**, dominated by
  incident-time LLM calls (which both editions pay anyway). If
  `min-instances=1` is needed for slash latency, add the cost of one
  small always-warm instance.

## 10. Alternatives considered (and rejected)

- **Firestore** (managed, true serverless DB): rejected because it
  forces redesigning `SearchKnowledge`, `FinalizePMRun`, and case
  sequence numbers, and requires a `store` interface — much larger
  change for no benefit at ir-hub's scale.
- **Keep Socket Mode + Cloud Run `min=1`** (litestream, no HTTP
  switch): rejected because Socket Mode's always-on connection
  prevents scale-to-zero, so it costs like a VM with none of the
  serverless savings.

## 11. Open decisions (need sign-off)

1. **Repo layout** — same repo with interface switch (recommended) vs
   a separate `ir-hub-serverless` repo.
2. **Do S1–S2 now** — the transport/job-runner refactor improves the
   Standalone Edition and de-risks serverless; worth doing before
   committing to the full serverless build?
3. **`min-instances` policy** — start at 0 (cheapest, accept rare
   slash cold-start) and revisit by measurement, or 1 from the start.
