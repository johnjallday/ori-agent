# Compatibility Spike — Google Account Connection Hub

Status: **COMPLETE for planning purposes.** Paper + shipped-code findings, plus a
live public-discovery pass (2026-07-23) that resolved the crux without an account.
Only two low-priority live confirmations remain and they fold into later groups.
Owner account: enrolled in the Google Workspace Developer Preview Program (2026-07-22).
Feeds PRD `tasks/prd-google-account-connection.md` FR 15 and Open Questions 1–5.

FR 15 gates *product* implementation (Groups 4–6) on this spike but explicitly
allows the connection-domain foundation (Group 2) to proceed first. Group 2 is
built (PR #250).

---

## 1. Findings

### Endpoints & client type
| Product | MCP endpoint | OAuth client | Secret |
|---|---|---|---|
| Gmail | native adapter (no MCP) | **Desktop** (Ori-owned) | none (PKCE) |
| Calendar | `https://calendarmcp.googleapis.com/mcp/v1` | **Web** (operator-owned) | required |
| Drive | `https://drivemcp.googleapis.com/mcp/v1` | **Web** (operator-owned) | required |

### Redirect URI — the "paste the auth code" wrinkle is a NON-issue
Ori registers its own redirect and already does so: `internal/mcp/oauth.go:160` →
`http://localhost:<port>/api/mcp/oauth/callback`, shipped working against
`calendarmcp.googleapis.com` in #248. Drive reuses this seam.

### Live discovery pass (2026-07-23, no account required)
The MCP resource-server metadata is served at the **resource-specific** path
`…/.well-known/oauth-protected-resource/mcp/v1` (the root well-known 404s), and
`initialize` / `tools/list` work **unauthenticated** (auth is enforced only on
tool *calls*). Captured directly:

- **Calendar** metadata → `authorization_servers: ["https://accounts.google.com/"]`,
  12 calendar `scopes_supported`. Tools (9): `list_events`, `get_event`,
  `list_calendars`, `suggest_time`, `create_event`, `update_event`, `delete_event`,
  `respond_to_event`, `search_events` (mutations gated by Ori's existing Calendar
  confirmation flow).
- **Drive** metadata → `authorization_servers: ["https://accounts.google.com/"]`,
  `scopes_supported: [drive, drive.readonly, drive.file]`. Tools (8), with the
  server's own `readOnlyHint`:

| Tool | readOnly | V1 |
|---|---|---|
| `search_files`, `list_recent_files`, `get_file_metadata`, `read_file_content`, `download_file_content` | true | **ALLOW** |
| `get_file_permissions` | true | **DENY** (ACL read — allowlist by name, not by readOnlyHint) |
| `create_file`, `copy_file` | false | **DENY** (writes) |

The five-tool allowlist maps exactly; a 9th tool must fail closed (FR 67). Note
`get_file_permissions` is read-only yet denied — so enforcement is the explicit
five-name allowlist, never a trust of `readOnlyHint`.

### Scopes (Open Q2 resolved)
- **Gmail:** `openid email profile gmail.readonly` initially; `gmail.send` a separate upgrade.
- **Calendar:** `calendar.calendarlist.readonly`, `calendar.events.freebusy`, `calendar.events.readonly` — read-only / **sensitive**.
- **Drive:** **both** `drive.readonly` (restricted) **and** `drive.file` (non-sensitive). `drive.readonly` alone is not sufficient.

### Verification tiers (Group 8 input, Open Q4 partial)
`gmail.readonly` + `drive.readonly` = **restricted** → restricted-scope OAuth app
verification, and transmitting restricted-scope data to servers triggers a
security assessment (the cloud-LLM path). Calendar's scopes = **sensitive** (no CASA).

---

## 2. The identity crux — RESOLVED (was the one live-gated unknown)

Question: can Ori obtain a verifiable Google `sub` for a Calendar/Drive **MCP** grant?
The shipped MCP OAuth seam captures only `code → access/refresh token` (no `sub`),
so this gated the whole "one identity → N product grants keyed on `sub`" model
(FR 22–23).

**Answer: YES.** Both MCP resource servers name their authorization server as
`https://accounts.google.com/` — the standard Google OIDC provider. Ori therefore
adds `openid email` to the authorization request alongside the product scopes;
Google returns an **ID token with `sub`** (and email) in the token response, which
Ori validates with the go-oidc verifier already built in Group 2. This is spike
approach #1, now confirmed by discovery.

Mechanics for Group 5/6 to implement:
- Add `openid email` to the MCP authorization request (the resource's
  `scopes_supported` lists only product scopes, but `openid` is an auth-server
  scope handled by accounts.google.com, not the resource — requesting it is valid).
- Read `id_token` from the token-exchange response `Extra("id_token")`, verify it,
  extract `sub`, and reject a `sub` that differs from the active identity (FR 23, 46).

Residual (belt-and-suspenders, not a blocker): confirm during Group 5
implementation against the live account that the token response for the combined
`openid + calendar` scope set actually carries `id_token`. Standard OIDC says it
will; verify at implementation time, not as a gate.

---

## 3. Remaining live items (low priority — fold into later groups)

Nothing blocks Groups 2–6 anymore. Two items still want the live account and
attach naturally to the group that needs them:

- **L1 — Gmail Desktop end-to-end (→ Group 3):** Ori-owned Desktop client, loopback
  `127.0.0.1`, auth-code + PKCE `S256`, `openid`; confirm the ID token `sub` and the
  exact granted scope set. Standard Google OAuth (no preview); verified when Group 3
  implements the Connect-Google flow.
- **L6 — verification lead time (→ Group 8):** capture Google's current
  restricted-scope verification + CASA determination for `gmail.readonly` /
  `drive.readonly` and the supported cloud-model data paths.

Resolved by discovery (no live run needed): L2/L3 auth-server + scopes, L4 Drive
tool snapshot, and the L5 subject-mismatch logic (unit-tested in Group 2's
`VerifyReconnectSubject`; end-to-end confirmation rides along with Group 5).

## 4. Impact on the epic

- No PRD scope change. The headline risk (MCP-grant identity) is **retired**: the
  auth server is standard Google OIDC, so `sub` is obtainable for Calendar/Drive.
- Group 5/6 gain a concrete recipe: add `openid email`, read+verify `id_token`,
  subject-match against the active identity.
- Only L1 (Gmail, Group 3) and L6 (verification, Group 8) still touch the live
  account, each inside the group that owns it.
