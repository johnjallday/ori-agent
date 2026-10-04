# Needs you

"Needs you" is the Email Ops workspace's answer to "tell me what needs me". It
reads the last week of the linked mailbox and sorts each conversation into
**needs you**, **worth knowing**, or **probably ignorable**, with a one-line
reason. The panel sits at the top of the workspace page; the morning brief reads
the same list.

## Sorting

`internal/emailtriage` sorts in two passes.

1. **Rules** (`Classify`) decide from facts in the mail headers, with no model:
   - a conversation whose last message is from the user, or that the user has
     answered (IMAP `\Answered`), is handled and left out;
   - a mailing list or bulk sender (`List-Unsubscribe`, `List-Id`,
     `Precedence: bulk`, Gmail's Promotions, Social and Forums tabs) is
     ignorable;
   - an automated sender (`Auto-Submitted`, or `noreply@`, `alerts@` and the
     like) is worth knowing;
   - mail the user was only copied on is worth knowing;
   - anything else, a person writing to the user, needs them.

   The mailbox readers fetch these headers with the rest of the metadata
   (`mailbox/signals.go`). The IMAP reader never sets a flag: it opens the inbox
   read-only and fetches with `BODY.PEEK`.
2. **The system model** (`Explain`) is asked about the conversations the rules
   were unsure of, at most 12 per read: the ones that need the user, the
   automated ones, and the copied ones. It sees the sender, the subject, and a
   400-character snippet, and returns for each whether it needs the user, what
   kind of thing it is (reply, deadline, decision, info), and why. Email text is
   untrusted: the prompt says so, `<` and `>` are stripped, items are addressed
   by number, and an answer naming an unknown item or kind is dropped. The model
   can promote a conversation to needs you or demote it to worth knowing; it
   cannot mark anything ignorable.

Without a system model the list still works on rules alone, and the panel says
so.

## State

`email_triage_state` (migration 75) keeps one row per conversation, keyed by
workspace, account, and thread. A conversation is sorted again only when a new
message arrives, so the model is asked about each message once. The row also
keeps the user's own call ("Not important", "This needs me"), which overrides
the sort until the conversation changes, and the follow-up made from it. Rows
for conversations older than 30 days are pruned. The state is derived from mail,
so it lives in the data directory, not in the workspace folder.

## Endpoints

All three are scoped to a workspace the user owns, and read the mailbox linked
to it (`server/email_needs_you.go`).

- `GET /api/workspaces/{id}/email/needs-you` returns `{"linked": true, "list": …}`,
  or `{"linked": false}` for a workspace without a mailbox, which is every
  workspace but Email Ops. A locked vault, an account that needs reconnecting, or
  a mail server that does not answer is a coded error the panel explains with
  the one action that fixes it.
- `POST …/needs-you/mark` `{thread_id, bucket}` records the user's call.
- `POST …/needs-you/track` `{thread_id}` makes the conversation a follow-up in
  the workspace, once (`followup.SourceRef{Type: "email_thread"}`).

## Morning brief

The brief's mailbox source reads the list with `Service.Peek`, which never asks
the model, so building a brief never waits on one. Conversations that need the
user come first, then worth knowing; ignorable mail is left out. When a list
read is already running, the brief falls back to the plain inbox read.

## Not covered

- Drafting a reply. Drafting runs through the HQ, and sending from a
  password-connected account (SMTP) is not wired yet.
- Mail older than a week, and more than 25 conversations per read.
- The Email Ops chat agent reading the list. The panel and the brief are the
  surfaces.
