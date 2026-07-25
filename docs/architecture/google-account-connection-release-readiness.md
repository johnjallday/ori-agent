# Google Account Connection — Release Readiness

Status: **pre-public**. The connection feature is functionally complete but its
public availability is **gated on Google OAuth verification** (below). This
document records the operator/compliance facts a release needs.

## 1. Feature flags & operator configuration (FR 75)

| Env var | Default | Effect |
| --- | --- | --- |
| `ORI_GOOGLE_CONNECTION_CLIENT_ID` / `_SECRET` | unset | The Ori identity OAuth client (Desktop). Unset ⇒ Connect returns `not_configured` (config-disabled, not a runtime error). |
| `ORI_GOOGLE_CALENDAR_ENABLED` | on | A falsey value (`0/false/no/off`) hard-disables Calendar — all its MCP tools are denied at listing **and** execution, regardless of any grant or workspace binding. |
| `ORI_GOOGLE_DRIVE_ENABLED` | on | Same, for Drive. |

**Runtime-unavailable vs config-disabled** are distinct: a Developer-Preview
account that isn't enrolled surfaces the product as *Provider unavailable* with a
prerequisite link (runtime), whereas a falsey feature flag hard-disables it
(config). Drive/Calendar each gate independently.

**Official-build client injection.** The identity client resolves in the order
**env → embedded → none** (`connections.ResolveOAuthClient`). Official releases
bake in a verified client at build time by passing the credentials to the build
(never committing them):

```bash
make build \
  ORI_GOOGLE_EMBEDDED_CLIENT_ID='…apps.googleusercontent.com' \
  ORI_GOOGLE_EMBEDDED_CLIENT_SECRET='GOCSPX-…'
```

These land in `connections.embeddedClientID/Secret` via `-ldflags -X`. A
from-source/dev build leaves them empty (stays "unconfigured"), and a self-hosted
operator can always override the baked-in client at runtime with the
`ORI_GOOGLE_CONNECTION_*` env vars. The resolved **source** (`env`/`embedded`/
`none`) is logged at startup and is safe to surface in the UI — the id/secret are
never echoed.

Calendar/Drive MCP servers use the **operator's own Web OAuth client**, entered
during setup and stored only in the vault — never shipped in the build.

## 2. Secret boundary (FR 35, 95 · Success Metric 7)

- Browser responses use the `PublicConnection` projection, built by explicit
  field-copy; it carries no `CredentialRef`, vault id, or token. Regression test:
  `TestProject_NeverLeaksSecrets` fails if a token-bearing field is ever added.
- Tokens live only in the vault (Gmail EmailAccount) or the MCP OAuth credential
  seam (Calendar/Drive); the connection record stores only opaque references.
- Logs carry connection id / product key / safe error category only — no tokens,
  codes, raw ID tokens, or Drive/email content.

## 3. Consent audit (FR 96 · Success Metric 16)

`connections/consent.json` is an append-only, token/secret/content-free trail:
each record is `{product, action (granted|withdrawn), data_path=cloud-model,
subject, timestamp}`. It is reconciled against live grants on status load and on
both disconnect paths, so enabling a product records a grant and
disabling/disconnecting records a withdrawal. Viewable at
`GET /api/connections/google/consent`.

## 4. Error taxonomy (FR 94)

User-facing categories and where they surface:

| Category | Surface |
| --- | --- |
| Not configured (no operator client) | Connect → `not_configured` |
| No active identity | product enable → `no_identity` |
| Local callback listener unavailable | OAuth callback error page |
| Invalid / replayed / expired state | callback → single-use state rejected |
| Account mismatch (reconnect returns a different sub) | `ErrSubjectMismatch` → repair rejected, Switch Account offered |
| Rate limited / quota | grant health `rate_limited` (card pill), no reconnect prompt |
| Provider unavailable / preview-ineligible | grant health `provider_unavailable` + prerequisite link |
| Admin blocked | grant health `admin_blocked` |
| Reconnect required (revoked/expired credential) | grant health `reconnect_required`, reconciled without a browser |

## 5. Google-data disclosure & Limited Use (FR 5.x, Design)

- Before any product content reaches a cloud model, the user has affirmatively
  enabled that product; the Drive setup surface additionally discloses the
  broad-read nature of `drive.readonly` (plus a possible `drive.file` grant) and
  states Ori requests no write access.
- Ori's use of Gmail/Drive data conforms to Google's **Limited Use** requirements:
  data is used only to provide the user-facing feature, is not sold, and is not
  used for ads or to train generalized models.

## 6. Verification plan — REQUIRED before public availability

Public availability is gated on completing, with Google:

1. **Brand verification** of the OAuth consent screen (name, logo, domains).
2. **Restricted-scope verification** for `gmail.readonly` and `drive.readonly`
   (both are restricted scopes) — includes the in-product Google-data
   disclosure and a demo video.
3. A **CASA (Cloud Application Security Assessment)** / third-party security
   assessment, required for restricted scopes.

**CASA determination per model path** (engineering determination — final scope is
Google's assessor's call; recorded here to drive the submission):

| Data path | Determination |
| --- | --- |
| **Local model** (Ollama / LM Studio / mlx_lm) — content never leaves the device | **Likely out of CASA cloud-data scope.** Restricted-scope data stays on-device and is not transmitted to any third party. Document the on-device boundary and the fact that no sub-processor receives the data; still declare the data flow in the submission. |
| **Cloud model** (OpenAI / Anthropic) — content is sent to a third-party model API | **In scope.** Restricted-scope content is transmitted to a sub-processor. Requires: the CASA assessment itself, a documented sub-processor/DPA relationship with the model provider, encryption in transit (TLS, already enforced), and the Limited-Use attestation. This path is what makes a public shared client CASA-bound. |

**Recommendation:** pursue verification/CASA against the **cloud-model** path (the
binding one). A local-model-only distribution can credibly argue reduced scope,
but the shared public client must assume the cloud path.

## 7. Verification submission checklist

- [ ] OAuth **consent screen**: production app name, verified logo, homepage +
      privacy-policy URLs on a verified domain.
- [ ] **Scopes** requested limited to `openid`, `email`, `profile`,
      `gmail.readonly`, and (for Drive) `drive.readonly` — justify each; do **not**
      request write scopes.
- [ ] **In-product disclosure** screenshot/recording showing the Google-data
      disclosure + affirmative consent before content reaches a model (the card +
      Drive setup surfaces satisfy this).
- [ ] **Demo video** of the full connect → enable → use flow for each restricted
      scope.
- [ ] **Limited-Use** attestation in the privacy policy (no sale, no ads, no
      generalized-model training).
- [ ] **CASA** assessment engaged with an authorized assessor for the cloud-model
      path; remediation tracked to closed.
- [ ] Redirect/loopback + Desktop-vs-Web client types documented per product.

## 8. First-release feature-flag gating

For the first release (self-hosted, pre-public-verification):

| Product | Ship state | Flag |
| --- | --- | --- |
| **Gmail** | **On** — the shippable-today product (standard Gmail API) | `ORI_GOOGLE_CALENDAR/DRIVE_ENABLED` do not affect Gmail |
| **Calendar** | **Preview** — depends on Google's Calendar MCP Developer Preview | `ORI_GOOGLE_CALENDAR_ENABLED` (default on; operators without Preview access see *Provider unavailable*) |
| **Drive** | **Preview** — depends on Google's Drive MCP Developer Preview | `ORI_GOOGLE_DRIVE_ENABLED` (default on; read-only fail-closed regardless) |

An operator with no Developer-Preview enrollment gets a graceful *Provider
unavailable* on Calendar/Drive (runtime), not an error. To hard-disable a product
for a build, set its flag falsey (config).

## 9. Go / no-go

| Capability | Ships now (self-hosted) | Blocked on |
| --- | --- | --- |
| Identity + **Gmail** (own/embedded client, testing mode ≤100 users) | ✅ | — |
| Public sign-in (shared verified client, unlimited users) | ❌ | §6 verification + §7 CASA |
| **Calendar / Drive** | ⚠️ preview only | Google GA of the MCP APIs + operator Preview enrollment |

Until §6–§7 are complete, the feature ships **self-hosted / preview-only** and is
not offered for public sign-in.
