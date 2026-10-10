# Personal Assistant conversation context

Canonical Messages remain the transcript. Migration 078 adds a **disposable,
Session-owned** recap checkpoint, not another conversation store, Personal HQ
memory, a saved Ticket, current source evidence, or permission. The existing
conversation owner and configured **system model** remain authoritative.

## Grounding and roles

The model selects exact complete source sentences/lines, identified by canonical
message ID. The host validates source text, role/import status, closed categories,
unchanged prior items, and quotas. Categories distinguish user goals, constraints,
corrections, tentative assistant options, unresolved questions, historical
findings, and imported history. Invented IDs, promoted assistant/imported roles,
negation-dropping fragments, secrets, commands, approvals and source markers are
refused. Selection is not a guarantee of exhaustive or semantically ideal recall.

Recap and targeted excerpts enter the answer as **user-role historical reference
data**, never system instructions. Latest exact user corrections and the current
request take precedence. Prior findings require fresh authorized reads before
claims about current sources, installation, grants or readiness. Reference data
cannot authorize research, setup, execution or global-memory writes.

## Bounded work

- Recent provider selection: at most 40 text rows, 16,000 runes, 6,001-rune
  projections per message, and bounded typed local metadata.
- Incremental summary: at most 32 older rows, 12,000 total runes and 1,000 per
  source. The first omitted batch generates; subsequent work waits for at least
  16 new omitted rows or a nearly full batch. No full-transcript summarization.
- Recap: version 1, at most 16 items, 4,000 JSON runes / 16,000 bytes and 400 runes
  per quote. Unsafe credential-shaped chunks are withheld from model input.
- Summary call: direct configured-model request, temperature zero, 1,800 output
  tokens, 12-second deadline, no tools, MCP/native workspace principal, inherited
  agent loadout, provider fallback or external catalog lookup.
- Older retrieval: at most 128 same-thread 1,000-rune candidates; bounded request
  terms choose up to four 400-rune excerpts. This is substring relevance, not
  vector/cross-conversation search or exhaustive recall.
- Final recap/excerpts plus history share the existing **24,000-rune** allowance.
  Current request and provider message ordering are preserved.

Invalid, oversized, tool-calling or unavailable summaries do not block an ordinary
answered turn. Eligible prior context/recent history remains usable with an honest
limitation. Cancellation and changed canonical revisions prevent stale writes.

## Canonical ownership and lifecycle

Production reads check authenticated user, current relationship/HQ/profile/state
version, Session metadata and revision **before bodies**, inside the transaction.
Checkpoint writes compare revision, mutation epoch, high-water/range and previous
generation atomically. Concurrent writers have one winner; a slower turn cannot
replace a newer checkpoint or recreate a deleted conversation.

Message edits/deletes/import changes and Session owner changes invalidate derived
context through mutation epochs/triggers. Local append preserves eligible covered
recaps but invalidates in-flight saves. Relationship changes discard ineligible
checkpoints. Existing Session deletion cascades; HQ deletion invalidates orphaned
recaps. Settings Reset explicitly clears checkpoints even with foreign keys off.
Portable message/Session codecs export neither checkpoints nor epochs; imported
history remains untrusted and must be rebuilt under current ownership.

## Display and exact-read contracts

Provider, display, and reviewed-source reads have different bounds. Display allows
200 text rows / 240,000 total runes and explicitly marks shortened projections as
`content_truncated`. Shortened text never claims equality with a saved-draft
source. Draft reviews read one exact canonical message with oversized-value
refusal, not silent clipping. Latest and exact older folder/review events use the
existing bounded typed local-event owner; imported events confer no authority.

The response exposes only `recent_exact`, `recap_used`, `older_used`,
`older_omitted`, `recap_unavailable` and `stale_discarded`. No recap quotes, range
IDs, tokens or private source text appear in status/URLs/logs. Existing unsaved,
model and scope errors retain notice priority. New conversation starts clean;
reopening uses the same canonical Session rather than browser-supplied history.

Controlled provider/SQLite/browser tests prove these boundaries and input
composition, **not vendor-model reasoning quality**. The opt-in
`scripts/assistant-workspace-demo.py --discovery-continuity` first uses `wt demo`,
then restarts only its verified disposable host/data through the checked-in
isolation entrypoint. It never uses personal state or provider credentials.
