# Personal HQ Assistant — Code-Facing Contracts (v1)

**Status:** Frozen for implementation · **Source PRD:** `tasks/prd-personal-hq-assistant.md`
**Scope:** This document freezes the non-negotiable data, privacy, security, scheduling, and
interaction contracts that Groups 2–7 implement against. Do **not** begin a dependent
implementation subtask while its contract here is unresolved. Where this document and the PRD
disagree, this document wins for code shape; contradictions discovered during implementation are
reconciled here first (task 1.11).

Related existing code this feature integrates with (do not re-derive):
`internal/personalhq/` (designation, setup), `internal/dailybrief/` (snapshot → analysis →
synthesis → scheduler), `internal/vault/` + `internal/vaulthttp/email_oauth.go` (email accounts,
OAuth), `internal/projecttemplates/` (template + roster), `internal/workspace/opportunities.go`
(Action Center), `internal/workspace/memory.go` (MEMORY.md), the note store, and
`internal/agenthttp` Home assistant routing.

---

## 1. Ownership boundary (task 1.2) — NON-NEGOTIABLE

| Surface | Owns | Must NOT |
|---|---|---|
| **Home** | The daily operating surface: the single Morning/Daily Brief, app-wide attention items, Action Center notifications, the primary Ask Ori entry point, and a **bounded projection** of Personal HQ data (due/stale/high-priority follow-ups, grounded email items, reply proposals, EOD journal prompt). | — |
| **Personal HQ workspace** | Durable control plane: account grants, assistant rules, **full** follow-up history + management, journals + history, permissions, specialist configuration, provisioning/upgrade state. | Add a second Daily Brief, a general-purpose action feed, or a competing Ask Ori entry point. Never seed a competing Morning Brief `ScheduledTask` — `internal/dailybrief.Scheduler` is the only one. |
| **Project workspace** | Executes the tasks/artifacts for a specific project. | — |

Rules:
- Every projected Home item MUST deep-link to its authoritative source or management surface.
- A Personal HQ follow-up MAY link to a project task but MUST NOT silently duplicate/move/delete it.
- Personal HQ is a **profile designation** (`internal/personalhq`), pointing at *any* eligible
  workspace — not necessarily one created from `personal-ops`. Code MUST NOT assume the template.
- If no HQ is designated, Home keeps current behavior and offers Build My HQ only when a Personal
  HQ capability is requested.

---

## 2. Follow-up model (tasks 1.3, 1.8) — dedicated SQLite domain

**Why a dedicated domain, not Action Center opportunities:** `workspace.Opportunity`
(`internal/workspace/opportunities.go`) is a *mission finding* — system observation, title-based
`DedupKey` (sha256 of normalized title + workspace), lifecycle new/snoozed/resolved/dismissed,
never gated by autonomy. A follow-up is a *personal commitment/dependency* with direction, an
external source (email thread), a reminder window, and task links. Different identity, dedup key,
lifecycle, and provenance → **new `internal/followup` package with its own SQLite tables.**

### 2.1 Types (`internal/followup/types.go`)
```
FollowUp {
  ID            string      // stable UUID, canonical
  UserID        string
  WorkspaceID   string      // the designated Personal HQ workspace
  Category      Category    // i_owe | waiting_on | needs_decision | recurring_check_in
  Direction     Direction   // outbound (I owe) | inbound (waiting on someone) | none
  Title         string      // bounded, <=200 chars, sanitized (never raw email HTML)
  Detail        string      // bounded, <=1000 chars, sanitized
  Counterparty  string      // person/org the loop is with, bounded, optional
  Source        SourceRef   // origin (email_thread | manual | journal), see §5.2
  Provenance    Provenance  // explicit | inferred | manual
  Confidence    string      // low | medium | high (inference only)
  Status        Status      // candidate | active | snoozed | completed | dismissed | reopened
  DueAt         *time.Time  // optional
  SnoozedUntil  *time.Time
  LastNudgedAt  *time.Time  // reminder idempotency (§2.4)
  RelatedTaskRef *TaskRef   // {WorkspaceID, TaskID} — link, never ownership
  CreatedAt, UpdatedAt time.Time
  CompletedAt, DismissedAt *time.Time
}
```
`DedupKey` (task 6.5): `sha256(userID | source.EntityType | source.EntityID)` for sourced items;
manual items are never auto-deduped. Reprocessing the same email thread updates (or ignores) the
existing record by this key — it never creates a second follow-up.

### 2.2 Lifecycle
`candidate → active` (user confirms an inferred candidate) · `active → snoozed → active` ·
`active → completed` · `active/candidate → dismissed` · `completed/dismissed → reopened → active`.
Only unambiguous **explicit** commitments auto-enter `active`; inferred items below the confidence
threshold enter `candidate` and require user confirmation (task 6.4).

### 2.3 Capture policy (task 1.8, 6.3)
- v1 categories only: `i_owe`, `waiting_on`, `needs_decision`, `recurring_check_in`.
- Explicit commitment detected in a source ("I'll send the deck Friday", "waiting on Dana's quote")
  → `active` if unambiguous, else `candidate`.
- **Never** capture from: newsletters, FYI/no-action mail, already-completed threads, weak
  suggestions, or ordinary project tasks (those live in the project workspace).
- Confidence threshold: `high` ⇒ auto-active; `medium`/`low` ⇒ `candidate`. Threshold value is a
  named constant so it can be tuned.

### 2.4 Stale / reminder rules (task 1.8, 6.6)
- **Stale** = `active` AND (`DueAt` passed) OR (no `DueAt` AND `UpdatedAt` older than the staleness
  window; default 7 days, named constant).
- Reminders delivered via **Action Center** (`workspace.Opportunity`-adjacent notification path,
  not a new channel). Idempotent per `(followUpID, evaluation-window)` via `LastNudgedAt`: at most
  one nudge per window. No nudge for snoozed/completed/dismissed.

### 2.5 Home projection (task 6.9)
- Home shows only **due, stale, or explicitly high-priority** follow-ups, deterministic order
  (due/stale first, then priority, then oldest `UpdatedAt`), capped at **`HomeFollowUpCap = 5`**
  with a "view all in Personal HQ" deep link. Full history + management lives only in the HQ panel.

---

## 3. Email / mailbox contracts (tasks 1.4, 1.5, 1.9, 3.x)

### 3.1 Staged Gmail OAuth & consent (task 1.4)
Least-privilege, staged — do **not** request full `https://mail.google.com/` up front.
- **Stage 1 (connect):** read/search only — `gmail.readonly`. Sufficient for triage + brief +
  draft composition (drafting is local, §4). Consent copy: *"Ori will read and search your mail to
  brief you and help draft replies. It cannot send anything without your explicit confirmation."*
- **Stage 2 (send upgrade):** requested only when the user first confirms a send — adds
  `gmail.send`. Separate consent screen. Reconnect/scope-upgrade flow lives in
  `internal/vaulthttp/email_oauth.go` (extend, don't fork).
- Revoked/expired token → distinct `disconnected` / `expired` state surfaced to UI for repair;
  never a silent empty inbox.

### 3.2 Permission precedence (task 1.5) — MOST RESTRICTIVE WINS
Effective mailbox access = the intersection (most restrictive) of **all** layers:
1. OAuth scope actually granted (readonly vs send).
2. Vault account grant (`internal/vault/store_grants.go`) — which workspace/agent may use the account.
3. Workspace email binding (`internal/workspace/http_handlers_email.go`).
4. Per-agent access entry — declaring a workspace-level MCP/email capability MUST NOT implicitly
   authorize every agent. Email access is granted to a **specific agent instance** (the Inbox agent).
5. Broker action policy (§4) for any mutation.
A connected Vault account is **not** universal authorization. Absence at any layer denies.

### 3.3 Mailbox runtime (task 3.x) — provider-neutral
New `internal/mailbox` package. Callers above the interface never see Gmail-specific fields.
```
MailboxProvider interface {
  SearchThreads(ctx, account, Query) (ThreadPage, error)   // bounded
  GetThread(ctx, account, threadID) (Thread, error)
}
```
- `Query`: bounded page size (`maxThreadsPerQuery`), bounded lookback window, label filters,
  excludes spam/trash/drafts. Stable provider IDs, pagination cursor.
- `Thread`/`Message`/`Participant`: normalized, small text representation only. HTML stripped,
  active content removed, snippets bounded, quoted history separated where practical.
- **All message-derived text is UNTRUSTED** (`internal/mailbox/sanitize.go`): sanitize, never let
  message text issue tool/policy instructions, preserve source refs. Prompt-injection fixtures required.
- Typed error classes: `healthy-empty` vs `disconnected` vs `expired` vs `permission-denied` vs
  `rate-limited` (+retry-after) vs `timed-out` vs `partial`. Each maps to a distinct UI state.
- Gmail impl (`internal/mailbox/gmail.go`) via internal-only Vault credential resolver — tokens
  never appear in HTTP responses, logs, errors, or audit details.
- Read-only agent tools `mail_search_threads` / `mail_get_thread` (`internal/chathttp/
  workspace_mail_tools.go`), exposed only to authorized Personal HQ agent instances.

---

## 4. Reply proposals & send broker (tasks 1.9, 5.x)

- A v1 **reply proposal is LOCAL** until an exact payload is confirmed. Drafting/editing never
  mutates Gmail (no provider-side drafts in v1).
- **Every send** traverses the centralized broker (`internal/mailbox/broker.go`) — the single
  send entry point for Home, HQ chat, scheduled work, delegated agents, and CLI-backed agents.
  There is **no** unbrokered native-MCP send path.
- Confirmation binds the exact `{account, recipients, subject, body, attachments-policy, source
  thread}` via a short-lived, **single-use** token/hash. Editing invalidates it (reconfirm required).
  Reject replay + concurrent consumption + expiry.
- Broker **re-evaluates** all §3.2 permission layers immediately before creating AND before
  consuming a confirmation.
- Gmail send: correct MIME, reply headers/threading, idempotency protection where supported.
- Audit events (metadata only, §6): proposed, edited, confirmation-created, confirmation-denied,
  expired, send-attempted, sent, failed.

---

## 5. Shared contract shapes (task 1.9)

### 5.1 Provisioning/upgrade state (task 2.x)
Personal HQ gains a **provisioning version** distinct from template `builtin_version` (which is a
library concept, never an existing-workspace migration). Stored per HQ workspace:
`{provisioning_version int, last_upgrade_outcome, last_upgrade_error, updated_at}`. Upgrade =
pure plan (current→target, additions, conflicts, preserved customizations, blockers, retryable
prior failure) then idempotent apply (revalidate designation/access, apply only approved missing
capabilities, record each step, resume after partial failure). Preserves user-edited prompts,
models, files, tasks, connections, settings, and unrelated agents.

### 5.2 SourceRef extension (task 4.1)
Extend the existing `dailybrief.SourceRef` allowlist model with `EntityType = "email_thread"`,
carrying `{account_id, provider_thread_id, timestamp}` and a validated open-route (§6). The
existing allowlist-validation of synthesis output (`Snapshot.AllRefs`) applies unchanged: the
model may only reference refs present in the snapshot.

### 5.3 Roster (task 2.5, 2.7)
v1 operational roster with **stable workspace-local role identity** (role → agent instance ID):
**Personal Chief of Staff** (entry, orchestrator), **Inbox** (email triage + draft; holds the
mailbox grant), **Journal** (EOD reflection → Note + opt-in memory). **No Calendar role shown in
v1.** Name collisions with existing user agents are handled by generating a collision-safe
instance, never silently reusing an incompatible same-name global agent.

### 5.4 API error + audit envelope
API errors: typed code + safe message, **no secrets/tokens/full content**. Audit rows: actor,
action, target ref, outcome, timestamp — **metadata only**.

---

## 6. Retention / deletion matrix (task 1.6)

| Data | Storage | Retention | On account disconnect |
|---|---|---|---|
| Cached mail snippets/thread projections | bounded cache, account+workspace isolated | expiry per cache TTL | deleted |
| Source metadata (refs, IDs, timestamps) | SQLite | tied to owning record | user keep/delete **choice** |
| Reply proposals | local store | expiry after N days or on send | deleted |
| Pending send confirmations | local store | short-lived single-use | invalidated |
| Audit metadata | SQLite | retained (compliance) | retained (metadata only) |
| Follow-ups | `internal/followup` SQLite | user-owned; retained | source metadata per keep/delete choice; record kept |
| Journals (saved) | Note store | user-owned Notes | never auto-deleted |

The account-disconnect flow MUST present an explicit **keep vs delete** choice for source-derived
data and MUST NOT delete user-owned follow-ups/journals/Notes unexpectedly.

### 6.1 Open-route validation
Every deep link (email thread, follow-up, journal Note) resolves through a **validated** route
that authorizes the current user + never embeds account tokens + rejects arbitrary destinations
(extend `hrefForRef`).

---

## 7. EOD journal contract (task 1.7, 7.x)

- Schedule settings: IANA timezone, selected days, local time, enabled, prompt-notification,
  config revision. Default: enabled, weekdays, user's HQ timezone, end-of-workday local time.
- **Independent scheduler** (`internal/personalhq/journal_scheduler.go`) with the same timezone
  guarantees as the Daily Brief scheduler — local-date keys, DST gap/fold, one prompt per
  workspace/local-day, catch-up after downtime, clean start/stop. **No generic seeded cron task.**
- Grounded snapshot: completed tasks + sourced email-thread summaries + follow-up status changes +
  user notes. **Excludes** full transcripts, unrestricted email bodies, unsupported inferred wins.
- Proposal is editable; **no write side effect until the user saves.** Saved journal = a dated
  Personal HQ **Note** (existing note store). Dismissed/ignored prompts create **no** Note or memory.
- Memory promotion is **explicit + selective** — the default save path MUST NOT write to
  `MEMORY.md`, and MUST NOT create parseable template-example follow-ups in memory.

---

## 8. Threat / test matrix (task 1.10) — every item needs coverage

Hostile email instructions (prompt injection) · malicious HTML / active content · quoted-content
confusion · expired OAuth · revoked OAuth mid-session · rate limits (+retry-after) · provider
timeouts · denied Vault grant · wrong-workspace / wrong-agent access · confirmation replay ·
concurrent/double-click confirmation · payload tampering (recipient/subject/body) · partial
snapshot sources · DST gap/fold · schedule change · disconnect keep/delete cleanup · secret/token
redaction in logs/errors/audit/browser storage · manifest-refresh vs workspace-upgrade isolation.

Testing rules: unit tests beside code; **fakes** for Gmail (never a real mailbox, never commit
credentials); focused suites per group; final gate = `make fmt`, `make vet`, `make test-unit`,
`make test-js`, `npm run lint`, `npm run format:check`, affected Playwright, `make test`.

---

## 9. Requirement traceability (task 1.11)

This contract covers the PRD's functional-requirement groups: surface ownership/routing (§1),
provisioning/upgrade (§5.1, §5.3), mailbox runtime + OAuth + permissions (§3), reply proposals +
broker (§4), grounded brief integration (§5.2), structured follow-ups (§2), and the EOD journal
(§7); with cross-cutting retention/privacy (§6) and the threat matrix (§8). No contradiction with
the PRD was found that required rewording the PRD. Any future contradiction is resolved by editing
this section first, then the PRD.
