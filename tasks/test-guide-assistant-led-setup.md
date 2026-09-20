# Assistant-led File Janitor setup — manual test guide

## Scope and safety

Test only from the `assistant-led-setup` feature worktree with an isolated demo sandbox. Never select a real Downloads/Desktop folder or reuse personal files.

```bash
source scripts/wt.sh
wt demo 8951
```

The demo prints its temporary sandbox and removes it on normal exit. Create disposable fixture folders only inside that sandbox or another `mktemp` directory. The supported desktop-picker proof requires macOS, the helper built from this checkout's `cmd/folder-picker`, a foreground browser, and permission to interact with the native dialog.

Do not treat a typed path, mocked response, helper timeout, or killed helper as evidence of picker selection or cancellation.

## Fresh no-model, pre-HQ path

1. Skip ordinary app onboarding in the isolated sandbox.
2. Hire a Personal Assistant with no provider/model configured.
3. Leave Personal HQ unbuilt (`needs_hq`).
4. Open `/?quest=tidy-downloads`, or open the Personal Assistant and revisit File Janitor setup.
5. Verify the review names the exact workspace create/reuse decision and File Curator model state.
6. Verify the review says:
   - metadata only: names, types, sizes, and dates;
   - no content read or model transfer;
   - folder access is next and does not start monitoring;
   - monitoring is a later decision;
   - every move/Trash action needs separate File Janitor approval.
7. Select **Set up for me** once. Reload and repeat the acceptance request if desired.

Expected: one workspace/team and one durable run exist. The card stops at **Choose folder**. No folder grant, watcher, scheduler, scan, task execution, file move, Trash action, or provider call has occurred.

## Consent 1 — reviewed workspace preparation

This is the **Set up for me** decision above. Confirm that **Not now** persists recommendation deferral and that manual customization opens the standard File Janitor creator before a target exists.

## Consent 2 — native folder access

1. Make a disposable inbox with two old, settled files and one just-written file.
2. Click **Choose folder**.
3. In the foreground native dialog, select that exact disposable inbox.
4. Separately repeat from a fresh run and cancel the native dialog.
5. Repeat once with the helper unavailable.

Expected after selection:

- focus returns to the exact connected setup trigger;
- `Filed` exists inside the selected inbox;
- the original files are unchanged and still in place;
- the card says folder access is saved and monitoring is off;
- no raw native path appears in the assisted setup response/card;
- watcher, daily schedule, and initial scan are not active yet.

Expected after cancellation: the same folder checkpoint remains, no error is presented as a grant, and reopening/reloading does not reopen the picker.

Expected when unavailable: an actionable unavailable error is shown, no grant is recorded, and setup can be retried.

> Current coding-agent run: foreground selection, cancellation, and nested focus were **NOT RUN**. Helper build/launch/unavailable behavior and the downstream real domains were exercised separately; this guide does not relabel a manual path as picker proof.

## Consent 3 — monitoring and first metadata scan

1. At the monitoring review, verify the actual watcher events, five-minute debounce, 30-second settling requirement, daily `09:00` schedule, timezone, metadata-only mode, and `Filed` exclusion.
2. Verify **Finish later** leaves monitoring off and survives reload.
3. Select **Start monitoring and prepare review**.

Expected:

- approval is saved before activation;
- watcher and scheduler must both report healthy;
- exactly one operation-bound metadata scan runs;
- no provider/content read is used;
- settled eligible files produce one batch, while new/settling files are counted as excluded;
- the card gives the real counts/time and says nothing moved;
- **Review proposed filing** opens the existing File Janitor console at the exact batch;
- every candidate starts unselected.

For an empty inbox, expect a durable **no new eligible files** result and no fabricated empty batch.

## Consent 4 — file operations remain separate

In the existing review console, select no candidates at first. Confirm the approval action is disabled. Choosing a destination, selecting files, previewing, and approving moves/Trash are the existing File Janitor flow and are outside setup consent. Setup completion must not create an approval token or move/delete anything.

## Deferral, manual takeover, and restart

1. At the folder or monitoring checkpoint, select **Finish later**.
2. Close/reopen the Assistant, navigate to another page, and restart the isolated server using the same sandbox.
3. Confirm the same run/workspace IDs and **saved for later** state return with no continuation.
4. Choose **Continue manually**, complete a folder step in the existing workspace controls, and explicitly **Resume setup**.
5. Confirm the canonical manual grant is adopted and the card asks for a fresh monitoring review rather than opening the picker or overwriting settings.

If monitoring was already active, Finish later must say it remains active and offer the existing workspace control for pausing it. Generic resume must never unpause a user-paused workspace.

## Existing setup matrix

Exercise these fixtures independently:

- one supported partial File Janitor;
- a ready active workspace;
- a ready paused workspace;
- several owned candidates;
- migrated legacy Downloads Janitor provenance;
- unsupported/custom provenance or missing wizard;
- customized/content-enabled privacy;
- unreadable workspace state.

Expected: partial state can be explicitly adopted; ready state only opens current status; several candidates require a choice; custom privacy/provenance routes to manual review without resetting it; unreadable state fails closed.

## Failures and lifecycle

- Deny folder permission: no monitoring/scan and a safe retry.
- Use a folder already owned by another workspace: only an owned conflict workspace route may be returned; never expose another owner's route/path.
- Change root, timezone, privacy, or pause state after monitoring review: stale consent is refused and refreshed.
- Induce watcher or scheduler failure: no healthy/activity claim; setup remains actionable.
- Induce initial scan failure: wizard mission completion remains once-only, monitoring is safely paused unless it was already active before setup, and the card names scan attention.
- Allow timed retries: at most two automatic retries after the initial repeat-safe attempt, with persisted backoff. Scope/consent drift cancels the timer.
- Revoke/relink access: Ori does not regrant it; a verified manual relink is adopted only on explicit resume and gets a fresh review.
- Remove/archive the capability/workspace or lose Assistant identity: the run is invalidated and does not recreate resources.
- Run reviewed Start Fresh: reset admission fences setup work and the assistant setup tables are cleared by existing reset policy.

## Evidence from this implementation run

- `tasks/screenshots/assistant-led-setup/assisted-first-review-real-domain.png`
- `tasks/screenshots/assistant-led-setup/assisted-first-review-console-unselected.png`
- `tasks/screenshots/assistant-led-setup/deferred-monitoring-after-restart.png`
- `tasks/screenshots/assistant-led-setup/resumed-manual-folder-after-restart.png`

The real-domain first-result demo used the existing manual API for the folder grant so it could exercise adoption, watcher/scheduler health, scanning, exact batch routing, and unchanged files without fabricating a native picker result. Native foreground picker proof remains the explicit limitation above.

## Acceptance evidence map

“Automated” below means an assertion against the real domain/store unless the row explicitly says mocked UI. Screenshots are supporting evidence, not substitutes for domain receipts.

| AC | Evidence | Result / limitation |
|---|---|---|
| AC-01 | `tests/assistant-led-setup.spec.ts`; coordinator and scanner suites | PASS: pre-HQ, no-model setup reaches a real metadata batch with no provider call. |
| AC-02 | `internal/assistantsetup/service_test.go`, HTTP tests, deferral/reload demo | PASS: GET is inert and recommendation deferral persists without resource creation. |
| AC-03 | Real-domain Playwright golden path and first-review screenshots | PASS downstream domains with unchanged files and no file action. Native selection itself remains NOT RUN and is not claimed. |
| AC-04 | Assistant setup, strict `sessionhttp`, persistence-reopen, lost-response, and race tests | PASS: stable run/workspace/profile/operation identities and no duplicate consequences. |
| AC-05 | Path-selection, HTTP, and coordinator cancellation/expiry/unavailable tests | PASS automated unavailable/denial/cancellation semantics. Real foreground Cancel/focus remains NOT RUN. |
| AC-06 | Folder-operation and coordinator tests; restart demo | PASS: grant is paused, `Filed` is disclosed, and no watcher/schedule/scan starts. |
| AC-07 | Automation consent, retry, and rollback tests | PASS deterministic failure/retry behavior; live induced watcher failure remains NOT RUN. |
| AC-08 | `scan_operation_test.go`, scanner tests, coordinator projections | PASS for empty/ineligible/settling receipts without fabricated batches. |
| AC-09 | Coordinator/progression tests | PASS: readiness/mission evidence remains separate from first-scan failure and once-only completion. |
| AC-10 | Resolver and existing-state matrix tests; partial-adoption restart demo | PASS for partial, ready/paused, ambiguous, legacy, unsupported, and unreadable states. |
| AC-11 | Reviewed creator, root-agent storage, rollback, and stale-plan tests | PASS: strict current plan and operation provenance; no same-name heuristic reuse. |
| AC-12 | Folder conflict, owner scope, symlink/root-generation, and route-redaction tests | PASS: no widened access, raw path, or foreign workspace route. |
| AC-13 | Durable run/store tests and restart demo | PASS: Close, Finish later, and Resume remain distinct and restart-safe. |
| AC-14 | Manual-grant adoption and stale-review tests; restart demo | PASS: canonical manual progress is adopted and changed scope gets a new review. |
| AC-15 | Invalidation, revoke/relink, deletion, identity-loss, shutdown, and reset-fence tests | PASS automated; live access-revocation UI remains NOT RUN. |
| AC-16 | Metadata-only scanner/provider spies, privacy tests, hostile-text and route tests | PASS: custom privacy routes manual and no content/instruction is executed. |
| AC-17 | Persisted fake-clock retry tests and operation-bound File Janitor retry receipt | PASS: initial attempt plus two retries; manual pause clears authority and stops timers. |
| AC-18 | `tests/assistant-led-setup.a11y.spec.ts` and JS focus/announcement tests | PASS keyboard, labels, reduced motion, and 390px layout. Real nested native-picker focus remains NOT RUN. |
| AC-19 | Full Go/JS suites, exact-batch console tests, and smoke comparison | PASS affected legacy/manual/review/history behavior. Four unrelated smoke failures reproduce on `origin/dev`; see validation notes. |
| AC-20 | Unavailable/read-error, lifecycle invalidation, stale review, and projection tests | PASS: stale/unavailable authority does not become success or enable a consequence. |

## Diff-focused security and recovery review

- Owner ID, run ID, workspace ID, revision, and operation ID are re-resolved at each write; foreign resources return bounded not-found/conflict responses.
- Assisted bodies accept only opaque revisions/tokens. A native path is resolved server-side from the scoped picker token and never appears in the assisted response, route, coordinator tables, or logs.
- Reviewed creation re-resolves the built-in blueprint and exact team plan before writes. Same-name profiles without matching provenance are not adopted.
- Monitoring and scan consent bind root generation, metadata-only privacy, schedule/timezone, watcher recipe, run revision, and current state. Directory/settings drift fails closed.
- Review found one retry-boundary weakness: after coordinator-owned approval/rollback, the visible monitoring state legitimately changes. Retries now use a path-free File Janitor receipt binding run, operation, review, and stable authority digest; every manual pause/settings/folder action clears it. Regression tests prove a manual pause cancels the retry instead of being mistaken for coordinator authority.
- Initial scan effects and `no_eligible` outcomes are atomic with operation receipts. Exact first-review links use the receipt batch ID rather than “latest.”
- New persistence uses existing SQLite and atomic File Janitor stores; it creates no new broadly permissioned directory/file path. Scoped `gosec` reported only the repository's unchanged baseline findings and no finding on a changed hunk.
- Same-origin/CORS behavior remains the host's existing policy; this feature adds no weaker alternate route. Consequential requests additionally require current opaque proposal/run/review or picker authority.

## Assisted-versus-manual observation

The default assisted card requires three setup decisions: **Set up for me**, native folder selection, and **Start monitoring and prepare review**. It requires no app-page navigation before the result; file moves/Trash remain a fourth, separate decision in the existing console. The manual baseline uses the ordinary workspace creator and workspace wizard/settings surfaces instead of the one shared card.

No trustworthy end-to-end time comparison or participant comprehension study was captured. The ~1.2-second automated golden-path result excludes native OS interaction and uses the documented manual-grant bridge, so it is test duration, not product time-to-value. Native picker/settling waits and production conversion claims remain intentionally unmeasured.

## Final validation notes

- `make test` — PASS when run alone. An earlier parallel invocation was invalid because another make target pruned the Go build cache mid-compile.
- `make test-js` — PASS, 3,176 tests.
- `make lint-new` — PASS, zero new findings.
- Scoped `gosec` over changed packages — 12 findings, all on unchanged baseline lines; changed-hunk check found zero new alerts.
- `go test -race ./internal/assistantsetup ./internal/filejanitor -count=1` — PASS.
- `npm run lint`, `npm run format:check`, `git diff --check`, `make vet` — PASS.
- `tests/assistant-led-setup.spec.ts` plus `tests/assistant-led-setup.a11y.spec.ts` — PASS, 4/4 against a fresh isolated server.
- `npm run test:smoke -- --workers=1` — 47/51 PASS. The four failures (one Task Output Contracts setup and three Settings Workspace Directory cases) reproduce unchanged against a fresh `origin/dev` server, so they are recorded as pre-existing rather than changed here.

Permission sweep: existing `scripts/demo-server.sh`, Go/Node test targets, and Playwright commands were sufficient. No repeated approval friction justified a new script or broader allowlist; no transient PID, port, or sandbox path should be authorized.
