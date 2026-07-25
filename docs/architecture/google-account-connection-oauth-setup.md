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

## Notes
- This is the **identity/Gmail** client (Desktop). **Calendar/Drive** additionally
  need Developer-Preview enrollment plus your own **Web** OAuth client entered in
  their setup panels — a separate, per-instance client (a temporary Preview
  constraint, not per end-user).
- Public availability of a shared, verified client is gated on Google brand +
  restricted-scope verification and a CASA assessment — see
  `google-account-connection-release-readiness.md`.
