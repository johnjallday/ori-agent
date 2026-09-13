# RC testing protocol

Use this protocol for **every exact release candidate**, before requesting stable
promotion. The release lifecycle and rollout instructions remain in
[RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md).

The goal is a short repeatable core check plus tests for the actual changes, not
an exhaustive manual test of all Ori. Budget roughly 15 minutes for the core
paths; change, integration and migration testing take as long as their risk
requires. Reaching a time limit is never a PASS.

## What arrives with each RC

After installer checks, the **Release** workflow attaches
`rc-test-report-vX.Y.Z-rc.N.md` to the GitHub prerelease and links it from the
release notes. Download it alongside the installer. This is a **blank test card**,
not a completed report or automated sign-off.

The card contains:

- Exact RC tag and full source commit, previous stable baseline and pinned diff.
- Core fresh-install, permission, persistence and upgrade checks.
- Included first-parent commits, PR references, actual changed paths and changed
  test/guide references. Non-PR commits and stabilization merges are included.
- Suggested risk checks selected from changed paths, not guessed from PR titles.
- For rc.2 and later, the exact changes since the previous RC and retest guidance.
- Results initialized to **NOT RUN**, with a default **HOLD** decision.

No model or provider credentials are used to generate the card. Commit messages,
PR text and code comments are untrusted evidence, never higher-priority
instructions or commands to execute. The generator does not infer new feature
semantics, run user-visible checks or certify coverage. Before
testing, a reviewer, feature author or agent must inspect the pinned diffs/tests
and turn the change-specific TODOs into concrete actions and expected results.
An unresolved user-visible change cannot be marked covered by a generic risk
recipe. Related changes may share one end-to-end scenario referencing all their
`C` IDs; do not repeat identical tests just to fill boxes.

For agent-assisted test authoring, use this bounded request:

> Read this RC card and the diffs/tests at its exact candidate SHA, not current
> dev. Fill the change-specific entry points, setup, actions and expected results.
> Cite the evidence for each case. Mark unknowns explicitly. Leave all observed
> results NOT RUN and the decision HOLD. Do not implement, launch tests, connect
> accounts, mutate application data or publish anything.

## 1. Identify and isolate the candidate

1. Wait for the candidate branch's **CI** and the tag's full **Release** workflow
   to succeed. Record their URLs in the card. A draft is not ready for sign-off.
2. Download the RC's actual platform installer. Record its filename and SHA-256,
   plus OS, architecture and browser. Compare against an official checksum when
   supplied; recording your own hash identifies the file but does not by itself
   prove its authenticity.
3. Test installers and the normal app/menubar entry point in a **disposable OS
   user or VM**. Keep the stable installation and live profile untouched.
   Installers and login services can have machine-wide effects; a separate
   `ORI_DATA_DIR` is not an OS sandbox. Prefer a VM for machine-wide installers.
4. Confirm the app's displayed version is the exact RC from the card. Verify you
   opened the test instance, not an already-running stable server on another port.
5. Use a test-only workspace, harmless fixture files and explicitly approved test
   accounts/provider usage. Do not enable launch-at-login, automatic schedules or
   unrelated external integrations as part of routine testing.

**Do not use `wt demo` or `demo-server.sh` as RC evidence:** they build a working
branch. A convenient local rebuild is not the downloaded release candidate.

For server-only checks, launch the binary extracted from the downloaded installer
with isolated HOME, ORI_DATA_DIR and working directory, on a separate unused port.
That can verify web behavior but does **not** count as installer or native-app
coverage. The existing `smoke-installed.py` does a brief isolated health/version
probe and exits; it is not a persistent manual test session.

## 2. Prepare two test profiles

### Fresh profile

Use empty application state and an empty scratch workspace in the disposable
account/VM. This catches onboarding and missing-default problems that an existing
profile can hide.

### Upgrade profile

Build a reusable **previous-stable fixture**, not a raw copy of your real profile:

1. Install the previous stable version named in the card in the test environment.
2. Create two scratch workspaces, a test agent, a harmless saved conversation and
   one changed setting. Add representative data for any schema/features touched
   by this RC. Record the expected inventory and values.
3. Stop Ori completely, then back up the entire test fixture and its scratch
   workspace files, including database sidecars. Do not copy a live database.
4. Upgrade that test installation with the actual RC, preserving its test profile.
5. Compare the inventory, open/edit one retained item, restart twice and compare
   again. Any migration error, lost data or duplication is a blocker.

Keep an untouched backup of the previous-stable fixture. For each new RC, restore
it to the original **test-only paths** with Ori stopped, then upgrade again. Do
not run the old binary against an RC-migrated database or assume downgrade works.
A profile that already passed through rc.1 does not prove the stable-to-rc.2 path.

Fixtures must not retain real credentials, enabled jobs, live workspace paths,
symlinks into real files or production external-account connections. If that
isolation cannot be established, stop rather than experimenting on live data.

## 3. Run the core checks

The card embeds actions and expected results for B1–B6:

| ID | Check | Evidence to record |
| --- | --- | --- |
| B1 | Actual installer and native launch | Asset/version/platform; screenshot or observation of normal launch |
| B2 | Fresh onboarding and scratch workspace | Workspace is reachable and points only at the test folder |
| B3 | Agent setup and one harmless conversation | Intended agent/provider responds in the intended conversation |
| B4 | One allowed tool action, then denial/cancellation | Expected scratch-data result; denied action causes no unauthorized change |
| B5 | Save, quit, restart and reopen | Setting, workspace, agent and messages persist without duplication |
| B6 | Previous-stable fixture upgrade | Before/after inventory plus successful second restart |

Provider-backed and integration tests need explicit consent for their credentials,
external actions and usage costs. If an essential case cannot run, record NOT RUN
and HOLD; do not silently substitute a mock and call the live behavior verified.

Current installer automation covers macOS Intel/Apple Silicon DMGs, Windows amd64
MSI, and Linux amd64 DEB/RPM with installed-server health/version probes. It does
not prove native UX, real integrations, upgrades, or Linux arm64 runtime. Record
which additional platforms need human verification when platform-specific code
changes; testing on your Mac alone is not evidence for Windows UI behavior.

## 4. Exercise the changes and their risks

For each `C` entry in the card:

1. Read the pinned commit diff and relevant tests/guides. A PR link inferred from
   a subject is a navigation aid, not proof of requirements or test coverage.
2. Classify it as user-visible, risk-only or non-user-visible, with a reason.
   A VERSION-only commit can reference B1; a documentation-only change can have
   an explained non-user-visible disposition rather than an invented UI test.
3. For applicable changes, specify exact navigation/setup, numbered golden-path
   actions and observable expected results. Add at least one meaningful edge or
   failure case. Mention required permissions and companion versions.
4. Execute those cases in the mode a user actually uses. Check the result and
   persisted effects, not only whether a button returned without error.
5. Review suggested `R` checks and add any missed risk. Path routing is
   conservative and incomplete; it cannot know every cross-cutting dependency.

Prioritize data/migrations, permission boundaries, external writes, shared
navigation/execution and installation/startup changes. Test reset or destructive
behavior only against explicitly disposable fixtures with a known inventory.

## 5. Record results and decide

For each case use:

- **PASS** — observed expected behavior on this exact RC, with evidence.
- **FAIL** — observed a mismatch; include reproduction and a linked bug.
- **NOT RUN** — no observation; this is a coverage gap, not a pass.
- **NOT APPLICABLE** — demonstrably unrelated; give a specific reason.

Record each bug's RC, platform, preconditions, minimal steps, expected versus
actual outcome and sanitized evidence. Do not upload credentials, personal
messages, customer files, real filesystem paths or sensitive logs.

**HOLD** for crashes, lost/corrupt data, unauthorized actions, broken core or
changed-feature paths, unresolved change scope, failed upgrade checks, or missing
critical tests. Record minor known issues and non-critical coverage gaps with an
explicit acceptance rationale. No response or unchecked box means HOLD.

An **APPROVE** decision needs passing applicable core/upgrade/change tests, no
open blockers, recorded coverage gaps and accepted minor issues, plus the tester,
approver and UTC date. It applies only to the tag and SHA printed in the card.

## 6. Preserve the report and request promotion

1. Save the completed copy under a distinct name, for example
   `rc-test-results-vX.Y.Z-rc.N-<tester>.md`. Keep the generated card unchanged.
2. After sanitizing it, share that copy as a **separate RC release asset** through
   GitHub's release editor, or an access-appropriate durable review record. Do
   not replace the template or make private data public merely to obtain a link.
3. Record the completed-report URL in the sign-off and the release-environment
   approval comment. A user with access must review it before approving.
4. Only after APPROVE, request promotion of the exact tested candidate using the
   [release procedure](RELEASE_CHECKLIST.md#promote-the-tested-candidate).

The CLI/promotion checkbox is **human attestation** that this protocol was
completed and reviewed. Automation validates RC identity and CI but does not
parse manual results or validate the report URL. GitHub environment reviewers
remain a separately configured protection; a Markdown card is not an enforced
approval system.

Report generation happens before prerelease publication. A failed attachment
leaves the release draft for retry. Use **Re-run failed jobs**, not a full rebuild
of a published release. Build authorization rejects already-published tags so
GoReleaser cannot replace their notes/assets during a full rerun. An identical
template is reused on attachment retry; edited assets or report blocks are never
overwritten. Unrelated release notes and separately named completed reports are
preserved.

## 7. Retest a new RC

For rc.2 and later, use the new card and its pinned delta:

- Reproduce every blocking bug, verify the fix and check nearby dependent paths.
- Re-run the core baseline and the upgrade from the previous-stable fixture.
- Repeat affected change/risk cases; expand coverage for shared/schema/permission
  changes rather than assuming a one-line fix is isolated.
- Record fresh observations and a new decision. Never carry PASS or approval
  forward automatically from the previous RC.

Cancel any pending approval for the old RC when a new blocker is found. New
feature work continues on `dev`; only stabilization fixes belong in the RC.
