# RFP: ir-hub

> Generated: 2026-06-10
> Status: Approved (2026-06-10)

## 1. Problem Statement

Security incident response (IR) teams operate on Slack, opening a dedicated
channel per incident and conducting response activities there. However,
in-flight situation awareness, post-incident postmortems, and the accumulation
and reuse of lessons learned are fragmented; existing tools such as ai-ir2
remain one-shot utilities that are not embedded in the lifecycle. **ir-hub**
is a one-package integrated system that supports the entire incident response
lifecycle with LLMs — from channel creation through response support,
postmortem, and knowledge accumulation, search, and reuse. Accumulated
knowledge is published in a form that teams outside IR (other teams in the
user organization, other security teams) can use as reference data when
planning their own measures.

**Target users:** Security IR teams running ChatOps-style incident response
on Slack. Secondarily, other internal teams and security teams that consume
the exported knowledge.

## 2. Functional Specification

### Commands / API Surface

Form factor: **a resident bot as a single Go binary (Slack Socket Mode)**.

Slash commands are **dual-mode**:

- `/ir-hub <subcommand> [args]` — direct execution
- `/ir-hub` (no arguments) — opens a Block Kit modal to choose the action via
  buttons and forms (usable without remembering parameters)

| Operation | Command | Behavior |
|---|---|---|
| Open case | `/ir-hub new <title> [--severity <lv>] [--private\|--public]` | Create response channel, register metadata, auto-post an initial briefing from similar past cases and related knowledge |
| Status | `/ir-hub status` | Generate and post a summary of current status, open items, and next actions from the channel conversation |
| Close | `/ir-hub close` | Close the case → automatically run the postmortem (lesson extraction + response evaluation) → post results to the channel and register them in the knowledge store |
| Re-run PM | `/ir-hub pm` | Manually (re-)run the postmortem; repeatable if the result is unsatisfactory |
| Export | `/ir-hub export` | Export finalized knowledge and case data as JSON + Markdown to storage |
| Knowledge Q&A | `@ir-hub <question>` | Answer from accumulated knowledge plus the current case context (knowledge reuse during response) |

### Access Control (ACL)

The target workspace contains a large population beyond IR members
(on the order of 50,000 user IDs), so ir-hub implements an ACL restricting
who can run commands and instruct the bot:

- Implements **both Whitelist and Blacklist**
- Entries can be specified **per user and per mention group (Slack User Group)**
- Evaluation order: Blacklist first → Whitelist match. With no Whitelist
  defined, **deny all** (fail-safe default)
- Enforced at both entry points: slash commands and `@ir-hub` mentions
  (Slack cannot restrict command visibility, so the app must always check)
- Responses to unauthorized users default to **silent + audit log**;
  notification replies are opt-in via config (noise control in a 50k-ID
  workspace)
- User Group membership expansion is cached with a TTL to limit API load
- ACL definitions live in config.toml (runtime admin commands are a future
  extension)

### Input / Output

- **Input:** Slack events (Socket Mode), slash commands, mentions
- **Runtime state:** embedded DB (SQLite) — case metadata, ingested messages,
  intermediate analysis results, knowledge index
- **Finalized output:** knowledge documents (structured JSON + human-readable
  Markdown pairs) written to a pluggable storage backend (local / GCS / S3).
  Other teams simply read this storage — a deliberately simple integration
  structure

### Knowledge Indexing and Retrieval

To avoid LLM load explosion, knowledge is **indexed at finalization time**:

1. **At knowledge finalization (postmortem completion):** an index entry
   (tags, summary, attack category, and other metadata) is registered in the
   DB alongside the knowledge document
2. **At query time:** agentic free-text search (FTS) + tag search narrow the
   candidates → only the narrowed candidates are loaded into the context and
   the expert model is queried
3. While the corpus is small, loading everything remains viable (narrowing
   degenerates to identity), so both modes coexist in the same architecture

### Configuration

- `config.toml` (organization-standard Vertex AI config pattern) + environment
  variable overrides
- Slack tokens (App-level token / Bot token) via environment variables or the
  OS keychain
- Default channel visibility (public / private) set in config, overridable
  with `/ir-hub new` flags

### External Dependencies

- Slack API (Socket Mode, Web API)
- Vertex AI Gemini (ADC authentication)
- Output storage: local filesystem / GCS / S3 (pluggable)

## 3. Design Decisions

### Why Go (single binary)

- Best fit for the requirements: resident bot + embedded DB + one-package
  delivery
- Leverages in-house track record with slack-go (Socket Mode),
  google.golang.org/genai, SQLite, and nlk (guard / backoff / jsonfix)
- Follows the ir-timeline pattern (single binary + embedded DB)

### Knowledge retrieval: index narrowing + expert model

Chunking + vector search (RAG) is not adopted. Candidates are narrowed via
the index built at finalization time (tags + FTS), and the **full text of the
narrowed knowledge documents is loaded into the context to query an expert
model** (precision first; same philosophy as virtual-reviewer). Embedding
vector search remains a future option.

### Relationship to existing tools: permanent coexistence (separation of use)

| Tool | Role | Relationship to ir-hub |
|---|---|---|
| ai-ir2 | One-shot postmortem analysis of exported data | Analysis theory, prompt design, and defenses (nonce tagging, IoC defanging) are carried over; the tool itself coexists |
| ir-tracker | Live situation awareness (Web UI) | Segment analysis / situation awareness theory carried over; coexists |
| ir-timeline | Manual timeline recording | Referenced for the single-binary + embedded-DB pattern; coexists |
| chatops-series | Slack I/O (scat / stail / swrite / slack-router) | Referenced for Slack integration patterns; ir-hub maintains its own Socket Mode connection |

Theory, prompts, and defenses are carried over, but the code is rebuilt as
ir-hub and delivered as one package.

### Out of Scope

- **Automatic alert ingestion (SIEM / mail-triage integration)** — cases are
  opened only by a human via `/ir-hub new`. Automatic case creation is a
  future extension

## 4. Development Plan

### Phase 1: Core (bot foundation + lifecycle management)

- Socket Mode resident bot skeleton (reconnection, event dedup, async 3-second ACK)
- `/ir-hub new | close | status` (metadata management only, no LLM) + modal
  when invoked without arguments
- Automatic case channel creation (public / private)
- ACL (Whitelist / Blacklist, users + User Groups, caching)
- Embedded DB (SQLite) schema, continuous message ingestion
- config.toml + tests
- **Review point:** works standalone as an "ACL-enabled case channel
  management bot"

### Phase 2: LLM analysis (postmortem + in-flight support)

- Automatic postmortem on close + manual `/ir-hub pm` re-run
- Knowledge document generation (JSON + Markdown) + **indexing at
  finalization (tags, summary, category)**
- On-demand status summary (LLM-backed `/ir-hub status`)
- Prompt injection defense (nonce-tagged XML wrapping), IoC defanging
  (carried over from ai-ir2)
- **Review point:** E2E review with real incident data

### Phase 3: Knowledge reuse + release

- Knowledge Q&A (`@ir-hub`): FTS + tag narrowing → context load → answer
- Automatic initial briefing (similar cases and related knowledge on
  `/ir-hub new`)
- Storage export (local / GCS / S3 pluggable backends) + `/ir-hub export`
- Complete docs EN/JA, signing + notarization, release
- **Review point:** integrated scenario test across the full lifecycle

## 5. Required API Scopes / Permissions

### Slack

| Type | Scope | Purpose |
|---|---|---|
| App-level token | `connections:write` | Socket Mode connection |
| Bot token | `commands` | Receive slash commands |
| Bot token | `chat:write` | Post to channels |
| Bot token | `app_mentions:read` | Receive `@ir-hub` mentions |
| Bot token | `users:read` | Resolve user info (ACL, reports) |
| Bot token | `usergroups:read` | Expand User Group membership for ACL |
| Bot token | `channels:manage` `channels:read` `channels:history` `channels:join` | Create / ingest public channels |
| Bot token | `groups:write` `groups:read` `groups:history` | Create / ingest private channels |
| Bot token | `files:write` | Post long reports as snippets |

Modals (`views.open`) require no additional scope (trigger_id based).

### GCP

- Vertex AI API enabled, Application Default Credentials
- `roles/aiplatform.user`
- When using the GCS output backend: `roles/storage.objectCreator` on the
  target bucket

### AWS (when using the S3 output backend)

- `s3:PutObject` on the target bucket / prefix

## 6. Series Placement

**Series: cybersecurity-series**

**Reason:** Matches the definition of AI-augmented security tools (threat
intel, IR, risk assessment). It belongs to the same IR domain as ai-ir2 /
ir-tracker / ir-timeline, making its lifecycle positioning within the series
clear (live operations integration vs one-shot analysis). The Slack-bot form
factor also overlaps with chatops-series, but domain takes precedence.

## 7. External Platform Constraints

### Slack

- **Slash commands must be ACKed within 3 seconds**; `trigger_id` also
  expires in 3 seconds. All LLM processing must be asynchronous: immediate
  ACK followed by posted results
- `chat.postMessage` is rate-limited to roughly 1 message/second/channel
- `conversations.history` has rate tier limits (ingestion requires
  pagination + backoff)
- Message body limit 40,000 chars; Block Kit section text limit 3,000 chars.
  Long postmortem reports must be split or posted as snippets (files)
- Channel names: lowercase, max 80 chars, unique workspace-wide including
  archived channels (naming convention should include sequence numbers /
  dates)
- Socket Mode disconnects/reconnects routinely; event redelivery (envelope
  duplication) must be deduplicated
- Slash command visibility extends to the entire workspace (cannot be
  restricted on the Slack side); the ACL is enforced in the app

### Vertex AI Gemini

- The 1M-token context window bounds the full-context-load approach; index
  narrowing keeps normal queries within this limit
- 429 rate limiting can occur (exponential backoff via nlk/backoff)
- The cost of full-context loading can be mitigated with Vertex AI context
  caching
- **Per organization ADR-001, use Gemini 2.5 until Gemini 3 GA** (migrate
  after GA; thought-signature echo-back support required)

### Storage

- GCS uses ADC; S3 uses AWS credentials — authentication differs per backend
  and is absorbed at the pluggable-backend boundary

---

## Discussion Log

- **2026-06-10** Project concept presented: integrated support for IR teams'
  Slack ChatOps. Support the full lifecycle — case channel operations →
  postmortem → knowledge reuse — with LLMs. Problem recognition: ai-ir2
  remains a standalone tool and is not actively leveraged
- **Integration hub vs rebuild:** compared orchestrating existing tools
  (ai-ir2 / ir-tracker / ir-timeline) against a rebuild; decided to **carry
  over the theory but rebuild and deliver as one package**
- **Purpose of knowledge reuse:** beyond use during incident response,
  explicitly include making lessons available to non-IR teams (other internal
  teams, other security teams) as reference data for planning measures
- **Tool name:** decided on `ir-hub` (candidates: ir-hub / ir-ops /
  ir-lifecycle / ir-companion)
- **Form factor:** resident bot (Socket Mode). Valued: no inbound exposure
  required; can run locally / on-prem
- **Knowledge store:** runtime in an embedded DB; finalized knowledge as
  JSON + Markdown baseline exported to storage (GCS / S3 / local). Keep the
  integration structure as simple as possible
- **LLM:** Vertex AI Gemini (organization standard)
- **Postmortem trigger:** automatic on close + manual re-run
- **In-flight support scope:** adopted automatic initial briefing, on-demand
  status summary, and RAG-style Q&A. Periodic automatic summaries rejected
  (noise concern)
- **Implementation language:** Go single binary (resident + embedded DB +
  one-package requirements)
- **Knowledge retrieval:** initially chose full-context load + expert model
  (expected higher precision than chunking + vector search). Then, considering
  the risk of LLM load explosion, revised to a staged design: **indexing at
  knowledge finalization + agentic free-text search + tag search to narrow
  candidates → load only the narrowed candidates into the context**. With a
  small corpus this degenerates to full load, so one architecture serves both
- **Out of scope:** only automatic alert ingestion (SIEM / mail integration)
  explicitly excluded. Web UI etc. left as future possibilities
- **Positioning vs existing tools:** permanent coexistence (separation of
  use). ai-ir2 = one-shot analysis of exported data; ir-hub = live operations
- **Channel visibility:** selectable via config (default in config + flag
  override)
- **Series placement:** cybersecurity-series
- **Slash command UX:** to address forgotten parameters, adopted dual mode —
  with args → direct execution; without args → modal-based action selection
- **ACL:** given ~50,000 IDs in the workspace, decided to implement both
  Whitelist and Blacklist, specifiable per user and per mention group
  (User Group)
