# Personal assistant: conversation-first contract

This note records the characterized baseline and the implementation seam for
`assistant-conversation-first`. It is not a claim that the improvements below
have shipped. Folder consent and canonical history remain governed by
[assistant-chat-folder-context.md](assistant-chat-folder-context.md) and
[assistant-workspace-awareness.md](assistant-workspace-awareness.md).

## Characterized baseline

At `af5f4f22`, a built `wt demo` with synthetic Albums/Album-1…Album-5 and
Logic Sketch metadata reproduced ordinary chat and Tree + Chat on Home and
Settings. Route/Ask, selection, canonical storage, reload and explicit generic
Review used the real host. Only model prose/generation came from a loopback
fixture; the unavailable Daily Brief was a browser presentation fixture.

- History is chronological already; reversing messages is not the fix.
- Short answers can be partially clipped after menus/context/status settle.
- Atomic long answers land at their last paragraph, not their beginning.
- A scrolled-up reader's row offset changes when status above history retires,
  even when `scrollTop` is unchanged. Browser anchoring is not sufficient.
- Hydration appends and follows each row before decorating it. The latest answer
  can end up outside the viewport after the complete hydration batch.
- Busy heading/routing copy is above history; the composer independently reports
  the send. Today's unavailable/retry footer is outside its folded sections.
- Explicit Review saves a canonical review but creates no workspace. Multiple
  candidates start unselected. Discussion checks are not setup scope.

The new browser characterization observes geometry before screenshot/click
scrolling. Run `python3 scripts/assistant-workspace-demo.py --conversation-first
--port 8954`; optional `--evidence-dir tasks/evidence/<feature>/<stage>` keeps
runner evidence inside the authorized worktree. Existing runner defaults remain
unchanged. Fixture text is not vendor-model quality evidence.

## One personal-transcript owner

Use the existing dashboard/conversation seams, not another conversation store:

1. Capture intent **before** a personal user/assistant/context DOM mutation.
   Actual Send resumes following; replayed user rows do not imply new Send.
2. Treat accepted answer, context insertion before its user row, preview
   retirement, menus/sources, proposal/discussion controls, status teardown and
   composer resizing as one settling batch.
3. A following short answer stays visible together with nearby controls. A long
   answer opens at its beginning and becomes a readable anchored state.
4. Intentional scroll-away during a request overrides following. Preserve a
   connected visible row and its relative offset through insertions/reflow;
   bounded-history trimming falls back to a surviving neighbour, then a clamped
   container offset. Do not preserve just the old numeric `scrollTop`.
5. Only an actual incoming answer can produce `New reply`. Revealing that target
   acknowledges it; polling, metadata and resize are not additional replies.
6. Hydrate in one batch and reveal the newest exchange once. Reopening the same
   drawer preserves its reading position. Adoption of the first canonical ID
   does not reset intent. New/switch invalidates delayed work.
7. Automatic arrival never focuses anything or scrolls the tree/page. Explicit
   Review and keyboard reflow recovery retain their focus contracts and move
   only the drawer's scroll container. No nested transcript scrolling.

Personal-only presentation must not suppress non-personal Ask Ori routing,
planning, confirmation, execution progress or actionable request failures.

## Supported effects and presentation boundaries

| Path | Known before explicit Review | Canonical reviewed effect / visual |
| --- | --- | --- |
| Generic blank/built-in blueprint workspace | Candidate and supported workspace type; blueprint availability is host-owned | One proposed workspace; existing creator links the chosen source and seeds its disclosed first task. Unknown artwork provenance uses neutral art. |
| Specialized project without a Home provider | Capability plus an available exact plan | One workspace, standalone only when the host supplies that destination; no guessed parent. |
| Project in existing group/Home | A named subject is only a reference; placement is resolved again | One new building **inside** an existing district, not a proposed new district. Generic groups and provider-owned Homes have different compatibility guards. |
| Specialized project requiring new Home | Available plan and validated new-destination disclosure | Proposed group plus the exact disclosed project effect; only declared providers can create a Home. No generic create-group operation is added. |
| Collection/portfolio in new or existing Home | Root candidate plus portfolio capability and available plan | Library/collection setup in the disclosed Home. Project workspaces are established when opened, not one per observed album at confirmation. No album-count-to-building-count inference. |
| Supporting folder in existing project | **Not offered by ordinary ReviewOptions**; available only after host placement choice for a verified project | Folder-to-existing-building link, not new workspace. Confirmation adds a supporting directory reference/read access; primary entry, blueprint, mode, roster and tasks stay unchanged. |
| Missing/unsupported/expired/foreign/pending options | Absent options or conservative read failure | No invented setup action. Saved metadata stays discussable. Existing review is its own canonical handoff, not a replacement suggestion. |

`FolderReviewOption.workspace_type` is prose, not a kind discriminator. Minimal
closed host-authored display metadata may be added later; it must not become
input authority. Subject names, folder names and assistant prose never choose
operation/destination/art. Supporting and destination visuals may wait for
validated placement/Review when facts are not safely available earlier.

Expand/select is local and read-only. Explicit Review uses the established opaque
references; only canonical reviewed confirmation executes. Completed links come
from verified receipts. One authoritative renderer moves into chronological
review slots; it is never cloned. No plugin/SDK/manifest changes are required.
Live specialized Home/portfolio execution needs separately verified candidates;
host fixtures are not evidence of that integration being activated.
