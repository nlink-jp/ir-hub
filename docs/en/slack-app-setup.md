# ir-hub Slack App Setup Handbook

This handbook walks through everything that must be configured on
the Slack side to run ir-hub: app creation, scopes (with the
justification for each — useful when your workspace requires app
approval), tokens, event subscriptions, installation, verification,
and troubleshooting.

The Slack-side configuration is half of the deployment; the other
half (config.toml, ACL, storage) is covered in the
[README](../../README.md).

---

## 1. Architecture at a glance

```
Slack workspace                      Your infrastructure
┌──────────────────────┐             ┌──────────────────────┐
│  /ir-hub  @ir-hub    │  WebSocket  │  ir-hub serve        │
│  case channels       │◀───────────▶│  (single binary)     │
│  (events, commands)  │ Socket Mode │  └─ SQLite (local)   │
└──────────────────────┘             └──────────────────────┘
```

ir-hub uses **Socket Mode**: the bot dials out to Slack over a
WebSocket. Consequences worth knowing before you start:

- **No public HTTPS endpoint is required.** No Request URL, no
  inbound firewall rule, no TLS certificate. The bot can run on a
  laptop, an on-prem server, or a cloud VM equally well.
- **Two tokens are required**: an *app-level token* (`xapp-…`) for
  the WebSocket connection and a *bot token* (`xoxb-…`) for Web API
  calls.
- **Exactly one `serve` process per app.** Slack allows up to 10
  concurrent Socket Mode connections per app, but ir-hub assumes a
  single writer to its SQLite database. Do not run two instances
  against the same DB or the same app.

## 2. Prerequisites

- Permission to create Slack apps in the workspace, or a contact
  with your Slack admin team. In workspaces with **Admin-approved
  apps** enabled (common at 50k-member scale), expect an approval
  step — section 4's scope table is written to be pasted into such
  a request.
- A machine to run `ir-hub serve` with outbound HTTPS (443) access
  to `slack.com` / `wss-primary.slack.com`.

## 3. Create the app from the manifest

1. Open https://api.slack.com/apps → **Create New App** →
   **From a manifest**.
2. Select the target workspace.
3. Paste the manifest below (YAML tab) and create.

```yaml
display_information:
  name: ir-hub
  description: Incident-response lifecycle hub
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

Notes:

- The slash command name (`/ir-hub`) must be unique in the
  workspace. If it collides with another app, rename it here — the
  binary does not care about the command name.
- `should_escape: false` keeps user/channel references in command
  text as plain text; ir-hub parses titles literally.
- With Socket Mode enabled, the manifest needs **no Request URLs**
  for events or interactivity.

## 4. Scopes — what each one is for

Every scope below is actively used by Phase 1. If your security
team asks "why does this app need X", this is the answer key.

| Scope | Used by | Why |
|---|---|---|
| `commands` | `/ir-hub` | Receive slash-command invocations |
| `chat:write` | kickoff/close/status posts | Post messages as the bot into case channels |
| `app_mentions:read` | `@ir-hub` | Receive mentions (knowledge Q&A from Phase 3; polite notice in Phase 1) |
| `users:read` | ACL, reports | Resolve user IDs for audit logs and display |
| `usergroups:read` | ACL | Expand User Group handles (`allow_groups` / `deny_groups`) into member lists |
| `channels:manage` | `/ir-hub new --public` | Create **public** case channels and invite the opener |
| `channels:read` | case lookups | Read public-channel metadata |
| `channels:history` | ingestion | Read public case-channel history for reconnect backfill |
| `channels:join` | recovery | Re-join a public case channel if the bot was removed |
| `groups:write` | `/ir-hub new --private` | Create **private** case channels and invite the opener |
| `groups:read` | case lookups | Read private-channel metadata |
| `groups:history` | ingestion | Read private case-channel history for reconnect backfill |
| `files:write` | long reports | Post long postmortem reports as snippets (Phase 2; requested now to avoid a reinstall) |

What ir-hub deliberately does **not** request:

- No `users:read.email` — no email access.
- No `im:*` / `mpim:*` — ir-hub does not read DMs.
- No `channels:history` outside case channels is ever *used*: the
  scope technically permits any public channel the bot is a member
  of, but ir-hub only joins channels it creates, and the ingest
  layer additionally drops messages from channels not bound to an
  open case.
- No user token (`xoxp-…`) at all — everything runs as the bot.

## 5. Generate the tokens

### 5.1 App-level token (`xapp-…`)

1. App settings → **Basic Information** → **App-Level Tokens** →
   **Generate Token and Scopes**.
2. Name it (e.g. `socket`), add the scope **`connections:write`**,
   generate.
3. Copy the `xapp-1-…` value → this is `IRHUB_SLACK_APP_TOKEN`.

### 5.2 Bot token (`xoxb-…`)

1. App settings → **Install App** → **Install to Workspace** →
   authorize.
2. Copy the **Bot User OAuth Token** (`xoxb-…`) → this is
   `IRHUB_SLACK_BOT_TOKEN`.

### 5.3 Token handling rules

- Hand tokens to ir-hub via environment variables, or via the
  `[slack]` section of `config.toml` with the file `chmod 600`
  (ir-hub warns at startup otherwise).
- Never commit tokens to a repository, paste them into Slack
  messages, or bake them into container images.
- If a token leaks: **App settings → OAuth & Permissions → Revoke**
  (bot token) / **Basic Information → App-Level Tokens → Revoke**,
  regenerate, and update the deployment. Treat any channel content
  the token could read as potentially exposed.

## 6. Installation and admin approval

In workspaces with admin-approved apps, the install step in 5.2
becomes a request. Include in the request:

- The scope table from section 4 (copy it verbatim).
- The fact that the app is **internal, Socket Mode, no public
  endpoint, single workspace**.
- Who operates it (the IR team) and that command access is further
  restricted in-app by an allowlist ACL (Slack cannot restrict who
  *sees* a slash command, so the app enforces this itself and
  audit-logs denials).

After any **scope change** later, Slack requires a **reinstall**
(Install App → Reinstall to Workspace), which may re-trigger the
approval flow. This is why `files:write` (needed in Phase 2) is
requested from day one.

## 7. Configure and start ir-hub

Covered in detail in the [README](../../README.md); the short
version:

```sh
mkdir -p ~/.config/ir-hub
cp config.example.toml ~/.config/ir-hub/config.toml
chmod 600 ~/.config/ir-hub/config.toml
# edit: [acl] allow_groups / allow_users — REQUIRED (deny-all by default)

export IRHUB_SLACK_APP_TOKEN=xapp-...   # pragma: allowlist secret
export IRHUB_SLACK_BOT_TOKEN=xoxb-...   # pragma: allowlist secret
ir-hub serve
```

Startup failures you may see, by design:

- `IRHUB_SLACK_APP_TOKEN is required` — token missing.
- `acl: unknown user group handle(s): …` — a `[acl]` group handle
  doesn't exist in the workspace (typo guard; fix the handle).
- `slack auth test: invalid_auth` — token revoked, truncated, or
  from another workspace.

## 8. Verification checklist

Run through this after first installation (a quiet test workspace
is fine — the steps are non-destructive):

1. **Connect**: `ir-hub serve` logs `authenticated as ir-hub (U…)`
   then `connected`.
2. **ACL deny path**: from a user NOT in the allowlist, run
   `/ir-hub status`. Expect: no visible response (silent), one row
   added to the audit log, a `denied` line in the server log.
3. **Modal**: from an allowed user, run `/ir-hub` with no
   arguments. Expect: action-picker modal; choosing *Open a new
   case* pushes the parameter form; submitting with an empty title
   shows an inline validation error.
4. **Case creation**: `/ir-hub new Test incident --severity low
   --private`. Expect: channel `ir-0001-test-incident` created, you
   are invited, kickoff message posted, ephemeral confirmation in
   the invoking channel.
5. **Ingestion**: post a few messages in the case channel, then
   `/ir-hub status`. Expect: `Ingested messages` matches.
6. **Backfill**: stop `serve`, post 2–3 more messages in the case
   channel, restart `serve`, run `/ir-hub status`. Expect: the
   count includes the messages sent while the bot was down.
7. **Close**: `/ir-hub close` in the case channel. Expect: closing
   message; a second `/ir-hub close` reports the case is not open.

## 9. Operations notes

- **Reconnects are normal.** Socket Mode disconnects routinely;
  ir-hub reconnects and backfills automatically. Frequent
  `connection error` lines, however, usually mean an egress proxy
  or firewall is interfering with WebSockets.
- **One process, one DB.** Run a single `serve` per app and per
  SQLite file.
- **Bot membership in case channels is load-bearing.** If someone
  removes ir-hub from a private case channel, ingestion stops and
  the bot cannot re-enter on its own — re-invite it manually.
- **Channel names**: `ir-<seq>-<slug>`, globally unique thanks to
  the DB sequence. Slack also forbids names colliding with
  *archived* channels; the sequence number makes this a non-issue
  in practice.
- **ACL changes** (config file) require a `serve` restart. User
  Group *membership* changes propagate automatically within
  `group_cache_ttl` seconds (default 300).

## 10. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `invalid_auth` at startup | Wrong/revoked bot token, or token from another workspace | Reissue via Install App, update env/config |
| `not_allowed_token_type` | App-level and bot tokens swapped | `xapp-` → `IRHUB_SLACK_APP_TOKEN`, `xoxb-` → `IRHUB_SLACK_BOT_TOKEN` |
| `/ir-hub` shows `dispatch_failed` | `serve` not running, or Socket Mode disabled in app settings | Start `serve`; check **Settings → Socket Mode** is on |
| Slash command works but no modal | Interactivity disabled | Enable **Interactivity & Shortcuts** (manifest sets this; verify it survived edits) |
| Modal opens but times out occasionally | `trigger_id` 3-second window missed (slow ACL group resolution on cold cache) | Usually transient; persistent cases → check egress latency to slack.com |
| Messages not ingested | Event subscriptions missing | Check **Event Subscriptions → bot events** has `message.channels` + `message.groups`; reinstall if you just added them |
| Mentions ignored for everyone | `app_mention` event missing, or all users denied | Check event subscription; check `[acl]` allow lists are non-empty |
| `missing_scope` on `/ir-hub new` | Scope added to manifest but app not reinstalled | **Install App → Reinstall to Workspace** |
| `name_taken` when creating a case | Channel name collision (should not happen with sequences) | Check for a manually created channel matching the pattern; case is marked `failed`, just run `new` again |
| Everyone is denied | Allow lists empty (deny-all default) or group handle typo (the latter fails startup) | Populate `[acl] allow_users` / `allow_groups` |
| Warning about config permissions | Config file readable by group/other | `chmod 600 ~/.config/ir-hub/config.toml` |

## 11. Multi-environment guidance

Run separate Slack apps per environment (e.g. `ir-hub-dev` in a
sandbox workspace, `ir-hub` in production). Tokens, the SQLite DB,
and the config file form one unit per environment; never share any
of the three across environments. A sandbox workspace (free tier is
fine) is strongly recommended for rehearsing upgrades, since scope
changes require reinstall + possible re-approval in production.
