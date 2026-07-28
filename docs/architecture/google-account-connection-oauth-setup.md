# Google Account Connection — OAuth client setup (self-hosted)

The **Connect Google** card (`Settings → Google Account`) signs in through an
Ori-owned OAuth client read from two env vars. Official builds bake in a verified
client; a self-hosted instance sets **one** client, once, for the whole instance
(never per end-user). Until it's set, the card shows *"Google sign-in isn't
configured in this build yet."*

Everything below is **free** — Google does not bill for OAuth or for
Gmail/Drive/Calendar API usage (those are quota-limited, not charged). The only
cost anywhere is optional cloud-LLM usage over your data, billed to your own LLM
key and unrelated to Google.

## 1. Create / pick a Google Cloud project
[console.cloud.google.com](https://console.cloud.google.com) → project dropdown →
**New Project** → name it (e.g. "Ori") → Create. No billing required.

## 2. Enable the Gmail API
APIs & Services → **Library** → **Gmail API** → **Enable**. (Enable Calendar API /
Drive API too if you plan to use those products.)

## 3. Configure the OAuth consent screen
APIs & Services → **OAuth consent screen**:
- User type: **External** → Create
- Fill app name + your support email → Save
- **Test users → Add users → your own Gmail address.**
  ⚠️ #1 gotcha: while the app is in "Testing" mode only listed test users can
  sign in; without this you get *"Access blocked."*
- Scopes can be left as-is — the flow requests `openid`/`email`/`profile` and
  `gmail.readonly`, and a test user may consent to them unverified.

## 4. Create the OAuth client
APIs & Services → **Credentials** → **Create Credentials** → **OAuth client ID**:
- Application type: **Desktop app**
- Name it → **Create**

Copy the **Client ID** (`…apps.googleusercontent.com`) and **Client secret**
(`GOCSPX-…`). Desktop clients auto-allow the `http://localhost:…/api/connections/google/callback`
loopback redirect Ori uses, so there is nothing to register.

## 5. Start Ori with the two env vars
```bash
export ORI_GOOGLE_CONNECTION_CLIENT_ID="your-id.apps.googleusercontent.com"
export ORI_GOOGLE_CONNECTION_CLIENT_SECRET="GOCSPX-your-secret"
./bin/ori-agent          # same PORT you open the UI on
```
Menu-bar app: set these two vars in the environment it launches from.

## 6. Connect
Reload `/settings` → **Connect Google** → sign in as your test-user account →
consent. You're authenticated (tokens stored on-device in the vault). Then
**Enable Gmail** on the card; Personal HQ / Email Ops link that account with one
click, no re-auth.

## 7. The credential vault step
**Enable Gmail** resolves the vault that will hold the credential *before* it
opens Google, so a vault problem can never strand you after you've already
consented. Depending on your setup the card asks for exactly one thing first:

| Vault state | What the card does |
| --- | --- |
| One vault, unlocked | Selects it silently and remembers the choice |
| One vault, locked | Asks you to unlock it, then continues automatically |
| No vaults | Offers inline vault creation, then continues automatically |
| Several vaults, none chosen | Asks you to choose once, then remembers |
| Remembered vault missing | Offers repair: pick another vault or create one |

Cancelling any of these leaves Gmail disabled and never opens Google. After a
process restart, a password-protected vault is locked again — enabling or
relinking Gmail then asks for an unlock rather than failing.

## Troubleshooting

**"ORI_GOOGLE_CONNECTION_CLIENT_ID looks like an email address."**
The client ID is *not* your Google account address. Copy the value ending in
`.apps.googleusercontent.com` from Credentials → your OAuth client. Ori checks
the shape at startup and refuses to begin a flow it knows will fail; the secret
is never logged or returned.

**"Google sign-in isn't configured in this build yet."**
Neither env var is set and this build has no embedded client. See step 5.

**"Access blocked" at Google.** Your account is not on the consent screen's test
user list (step 3).

**Signed in, but Gmail is still disabled.** The result page says which local step
failed — a locked vault or a missing vault selection. Fix it from the link on
that page; you will not have to sign in with Google again.

## Validating a build

Automated coverage lives in three places:

| What | Where |
| --- | --- |
| Vault preflight, callback categories, client validation | `go test ./internal/connections/ ./internal/connectionshttp/` |
| Readiness ladder, credential lifecycle, upgrade fixtures, full journey | `go test ./internal/server/` |
| Failure classification and retry budget | `go test ./internal/llm/ ./internal/orchestrationhttp/` |
| Card behaviour in a real browser | `npx playwright test tests/google-account-email-ops.spec.ts` |

The Playwright suite needs a running server and is hermetic otherwise — it
scripts every vault state and never contacts Google:

```bash
./scripts/build-server.sh
SMOKE_DIR=$(mktemp -d)
cd "$SMOKE_DIR" && HOME="$SMOKE_DIR" ORI_DATA_DIR="$SMOKE_DIR" PORT=8931 \
  /path/to/bin/ori-agent &
PLAYWRIGHT_BASE_URL=http://localhost:8931 npx playwright test tests/google-account-email-ops.spec.ts
```

## Upgrading from an earlier build

Nothing is required of you. Specifically:

- An existing healthy Google connection stays connected — no reauthorization.
- A workspace whose Gmail binding predates this release keeps working: it is
  recognized as a native mailbox binding on the next read, which also fixes the
  `server gmail not found` failure without any migration step.
- A task that was blocked before the upgrade stays blocked, keeps its original
  failure, and runs only when you explicitly retry it. Repairing a connection
  never starts work on its own.
- Duplicate credential records from earlier builds are consolidated only when
  Ori can prove they are redundant (same account, same vault, Ori-created, and
  nothing else referencing them). Anything ambiguous is kept and reported as
  "skipped" — Ori will not delete a credential it cannot prove is a copy.

## Troubleshooting task failures

**"Your AI provider reports the account is out of quota or credit."**
This is your LLM provider's billing, unrelated to Google. Ori stops after one
attempt because retrying cannot change the answer. Fix it in the provider's
console, then press Retry on the task.

**A task says it will not repeat itself automatically.**
The attempt already used a tool that changes things — or one that failed partway
through, so its effect is unknown. Ori will not repeat that on its own; review
what happened and retry deliberately if it is safe.

## Notes
- These two env vars — `ORI_GOOGLE_CONNECTION_CLIENT_ID` and
  `ORI_GOOGLE_CONNECTION_CLIENT_SECRET` — are the **only** supported way to
  configure Gmail access. The former in-app *Personal HQ Email* OAuth settings
  (`/api/settings/email-oauth`) have been removed; existing vault email accounts
  are preserved and migrated from the Google Account card.
- This is the **identity/Gmail** client (Desktop). **Calendar/Drive** additionally
  need Developer-Preview enrollment plus your own **Web** OAuth client entered in
  their setup panels — a separate, per-instance client (a temporary Preview
  constraint, not per end-user).
- Public availability of a shared, verified client is gated on Google brand +
  restricted-scope verification and a CASA assessment — see
  `google-account-connection-release-readiness.md`.
