# Email setup card

The "Set up email" card connects a mailbox from an email address and an app
password. It replaces Google sign-in as the default way to set up email: release
builds ship no Google OAuth client, and most mail providers accept an app
password over IMAP.

Every surface that offers email setup links to `/?setup=email`: the Home email
capability card, the connect-a-source starter mission, a task blocked on email,
and the Email Ops workspace's connect button and Setup Wizard step.
`email-setup.js` loads on every page and opens the card for that URL, or for any
click on a link to it, without leaving the page.

## Flow

1. **Detect.** `GET /api/email-setup/detect?address=` (`internal/emailsetup/detect.go`)
   works out who hosts the address. Consumer domains are known by name
   (gmail.com, icloud.com, yahoo.*, aol.com, fastmail.com, outlook.com…). Any
   other domain is identified from its MX records, which is how a custom domain
   on Google Workspace, Microsoft 365, iCloud, or Fastmail is recognised. The
   profile says where to create an app password and which extra fields to show:
   a username for iCloud on a custom domain, a server for an unknown host.
2. **Connect.** `POST /api/email-setup/connect` (`emailsetup.Service.Connect`):
   - signs in over IMAP before anything is saved (`mailbox.IMAPProvider.CheckLogin`).
     iCloud tries the name before the @ and then the full address, as Apple
     documents;
   - keeps the login in the vault Ori unlocks by itself (below);
   - finds the user's Email Ops workspace, or creates it from the `email-ops`
     blueprint through the same workspace creation the library uses
     (`sessionhttp.Handler.CreateFromTemplate`);
   - links the mailbox read and search only, records the Setup Wizard's mailbox
     step, and completes the connect-a-source mission (`server/email_setup_card.go`).

Both endpoints sit behind the same local-origin guard as the Google connection
endpoints. The password is never echoed, logged, or placed in an error; failures
carry a stable code and the field they concern.

## Where the login lives

Every vault is encrypted with a password and locks when Ori restarts. A login
the morning brief reads with unattended cannot depend on someone typing that
password, so the card keeps it in **the remembered vault** (`vault.Keyring`): an
ordinary vault named "Email" whose random password is stored in the
installation secret store (the macOS Keychain on a Mac) under
`remembered_vault`, where Ori already keeps provider API keys. Startup unlocks
it (`ServerBuilder.wireEmailSetup`); deleting the vault forgets the password.
The vault file stays encrypted and portable. When no secret store is available,
the card refuses rather than saving a login Ori could not read after a restart.

Sandboxed servers and tests that run the card set
`ORI_DISABLE_NATIVE_SECRET_STORE=1` and `ORI_VAULT_PASSPHRASE`, because a `HOME`
override does not isolate the Keychain.

## Readiness

`emailReadinessEvaluator` treats a password-connected account as needing no
Google connection. A workspace with nothing linked and no working Google
connection gets `set_up_email` (the card) as its next step, and a linked account
in a locked vault gets `repair_vault` rather than a Google repair. The Home email
card judges the user's Email Ops workspace, where mail is linked, rather than
the HQ (#441).

## Not covered

- Outlook, Hotmail, and Microsoft 365: Microsoft no longer accepts passwords for
  mail apps.
- Google Workspace accounts whose admin blocks app passwords; the card warns
  before the user goes looking.
- Sending from a password-connected account (SMTP) is not wired yet; the send
  broker still uses the Gmail provider.
