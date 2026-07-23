# Compatibility Spike — Google Account Connection Hub

Status: **paper + shipped-code findings complete; live validation checklist pending.**
Owner account: enrolled in the Google Workspace Developer Preview Program (confirmed 2026-07-22).
Feeds PRD `tasks/prd-google-account-connection.md` FR 15 and Open Questions 1–5.

FR 15 gates *product* implementation (Groups 4–6) on this spike, but explicitly allows the
**connection-domain foundation (Group 2)** to proceed first. Group 2 is underway in parallel.

---

## 1. Resolved without a live account (docs + shipped #248 code — high confidence)

### Endpoints & client type
| Product | MCP endpoint | OAuth client | Secret | Source |
|---|---|---|---|---|
| Gmail | native adapter (no MCP) | **Desktop** (Ori-owned) | none (PKCE) | PRD decision 3 |
| Calendar | `https://calendarmcp.googleapis.com/mcp/v1` | **Web** (operator-owned) | required | docs + `internal/calendar/preset.go:15` |
| Drive | `https://drivemcp.googleapis.com/mcp/v1` | **Web** (operator-owned) | required | docs |

### Redirect URI — the "paste the auth code" wrinkle is a NON-issue for Ori
Google's setup docs show hosted callbacks (`claude.ai/api/mcp/auth_callback`, `antigravity.google/oauth-callback`)
because those are *those vendors'* first-party callbacks. Ori registers its **own** redirect and already does so:

- `internal/mcp/oauth.go:160` → `http://localhost:<port>/api/mcp/oauth/callback`, shipped working against
  `calendarmcp.googleapis.com` in #248. So Ori uses a real browser→localhost redirect, **not** a copy-paste-code flow.
- Drive reuses this exact seam (the `ConnectorPreset` struct in `preset.go` is a credential-free near-clone).

### Scopes (Open Q2 resolved)
- **Gmail:** `openid email profile gmail.readonly` initially; `gmail.send` is a separate explicit upgrade.
- **Calendar:** `calendar.calendarlist.readonly`, `calendar.events.freebusy`, `calendar.events.readonly` — all read-only / **sensitive** tier.
- **Drive:** requires **BOTH** `drive.readonly` (restricted) **and** `drive.file` (non-sensitive). `drive.readonly` alone is
  **not** sufficient → the PRD's "does readonly work without file" question is answered: no. Disclose both; allowlist unchanged.

### Drive tool set (Open Q3 resolved on paper — snapshot still to confirm live)
Exactly **8 tools**. Allowlist maps perfectly:

| Tool | V1 disposition |
|---|---|
| `search_files` | ALLOW |
| `list_recent_files` | ALLOW |
| `get_file_metadata` | ALLOW |
| `read_file_content` | ALLOW |
| `download_file_content` | ALLOW |
| `create_file` | DENY (mutation) |
| `copy_file` | DENY (mutation) |
| `get_file_permissions` | DENY (permission/sharing read) |

A hypothetical 9th tool must fail closed (FR 67). Only `create_file`/`copy_file` are true writes; `get_file_permissions`
is a read we still deny (exposes ACLs).

### Verification tiers (Group 8 input, Open Q4 partial)
- `gmail.readonly` + `drive.readonly` = **restricted** → restricted-scope OAuth app verification, and docs state that
  **transmitting** restricted-scope data to servers triggers a security assessment (CASA). That is exactly the cloud-LLM path.
- Calendar's three scopes = read-only / **sensitive** → verification but no CASA.
- `drive.file` = non-sensitive.

---

## 2. THE crux — unresolved, needs the live enrolled account

**The shipped MCP OAuth seam yields an access/refresh token bound to no verifiable Google `sub`.**
`internal/mcp/oauth.go` captures only `code → token`; `oauthSessionInfo` = `{ServerName, UserID(local), RedirectURL}`.
No ID token, no `sub`, no userinfo. Today's Calendar Ops is a **standalone** grant with no Google identity.

The PRD's "one identity → N product grants keyed on `sub`" model (FR 22–23: *don't attach an MCP grant without a
verifiable subject*) therefore hinges on a question only a live enrolled account can answer:

> Can Ori obtain a verifiable Google `sub` for a Calendar/Drive **MCP** grant?

Candidate approaches to test live, in preference order:
1. **Add `openid email` to the MCP client's authorization request** and check whether the calendarmcp/drivemcp
   authorization server returns an **ID token** (→ parse `sub`). Cleanest if supported.
2. **Call a userinfo endpoint** with the MCP access token (or the same client against Google's userinfo) to resolve `sub`.
3. **Neither works** → per FR 23, Ori must NOT attach the grant to the identity. Fallback: Calendar/Drive stay
   standalone (as today), and the "unified identity" promise degrades to "identity + independently-authorized products."
   This is the material risk to the epic's headline UX.

---

## 3. Live validation checklist (run with the enrolled account)

Prereqs: a Google Cloud project with the Calendar API + Calendar MCP API (and Drive equivalents) enabled, an OAuth
consent screen configured, and Web + Desktop OAuth clients created. Ori shows the exact redirect URI during Connect.

- [ ] **L1 — Gmail Desktop (no preview needed):** Ori-owned Desktop client → loopback `127.0.0.1:<port>`, auth-code + PKCE
  `S256`, offline access; confirm the ID token carries a valid `sub`, and record the exact granted scope set.
- [ ] **L2 — Calendar MCP:** operator Web client + secret → confirm `http://localhost:<port>/api/mcp/oauth/callback`
  is accepted, the three calendar scopes are granted, and **whether an ID token / `sub` can be obtained** (approaches 1–2 above).
- [ ] **L3 — Drive MCP:** same as L2 against `drivemcp.googleapis.com`; confirm `drive.readonly` + `drive.file` are both
  requested/granted and record any additional scope Google injects.
- [ ] **L4 — Drive `tools/list` snapshot:** capture the live tool list; prove the 5-tool allowlist works end to end and
  that denied/unknown tools fail closed.
- [ ] **L5 — `sub` match:** with two different Google accounts, confirm Ori can detect a mismatched subject on
  reconnect/repair and refuse to attach it (FR 22–23, 46).
- [ ] **L6 — Verification lead time:** capture Google's current restricted-scope verification + CASA determination for
  `gmail.readonly` / `drive.readonly` and the supported cloud-model data paths (Open Q4).

## 4. Impact on the epic vs PRD assumptions

- No PRD scope change. Redirect-URI risk retired. Drive scope/tool questions resolved on paper.
- **Sharpened risk:** MCP-grant identity (`sub`) is the single live-gated unknown; Groups 5–6 acceptance depends on L2/L3/L5.
- If approaches 1–2 both fail, raise a product decision before Group 5: keep Calendar/Drive standalone vs block the
  unified-identity claim for those products.
