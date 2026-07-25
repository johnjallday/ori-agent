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

**CASA determination per model path** (to be completed and recorded here before
release):

| Data path | Determination |
| --- | --- |
| Local model (Ollama / LM Studio / mlx_lm) — content stays on device | _TBD: likely out of CASA's cloud-data scope; document the on-device boundary._ |
| Cloud model (OpenAI / Anthropic) — content sent to a third-party model | _TBD: in scope; CASA + the sub-processor relationship must be documented._ |

Until 1–3 are complete, the feature ships **disabled/preview-only** and is not
offered for public sign-in.
