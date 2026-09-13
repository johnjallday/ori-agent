# Release candidates and stable releases

This is the canonical release procedure. Older local release-manager prompts
that say to merge all of `dev`, tag stable directly, or delete/recreate published
tags are obsolete. Planning and feature delivery are unchanged.

## The flow

```text
Feature PRs → dev ─────────────────────────────────→ more feature PRs
               │ at least 10 unshipped PRs + green CI
               ▼
        release/vX.Y.Z → vX.Y.Z-rc.1 → install and test
               │          ↑ fixes produce rc.2, rc.3…
               │ explicit approval of an exact tested RC
               ▼
             main → vX.Y.Z → stable installer checks → publish
               │
        release branch → merge-back PR to dev (merge commit, NOT squash)
```

**RC means release candidate.** It is installable, but not the stable update.
There is at most one active candidate branch. Reaching another ten PRs while
an RC is being tested holds only the next candidate, never development.

The candidate branch starts at the validated `dev` commit plus one VERSION-only
commit. `VERSION` contains the final numeric `vX.Y.Z`; installed binaries get
their actual version (`X.Y.Z-rc.N` or `X.Y.Z`) from the build tag. Ordinary patch
versions increment automatically; editorial major/minor version changes are not
part of this first RC workflow.

## One-time rollout and GitHub settings

1. Land the implementation through its feature PR to `dev`.
2. GitHub schedules and manual entry points run from the default branch, `main`.
   **Merging to dev alone does not activate this workflow.** With separate
   approval, land a workflow-only bootstrap PR on `main` containing the new
   lifecycle's complete diff (including scripts/tests, workflows, Makefile,
   GoReleaser configuration, installer changes and documentation). Do not
   merge unrelated, untested dev features just to activate automation. Merge
   those bootstrap commits back into `dev` with a merge commit before cutting
   the first RC. Keep `AUTO_RELEASE_HOLD=1` during this coordinated rollout.
3. `RELEASE_PAT` must be allowed to push `release/**`, update `main`, create tags,
   and open PRs. It must also be allowed to trigger Actions. The default
   `GITHUB_TOKEN` does not trigger new workflows from its pushes/PRs.
4. In **Settings → Environments → release**, add a required reviewer. Promotion
   already requires a manual dispatch with an exact RC and an explicit
   `confirm_tested` checkbox, even if environment protection is absent; a
   required reviewer adds GitHub-enforced approval after validation.
5. Allow pushes/PR CI on `release/**`; require the **CI** workflow on candidate
   heads. Do not exclude candidate branches from Actions with a repository rule.
   Preserve the required-check names currently configured for `dev`.
6. Require review for release-branch fixes and protect `main`/version tags from
   casual edits. Operators with permission to change workflows/tags remain
   trusted; a repository script is not a substitute for GitHub permissions.
7. Release merge-back PRs must use **Create a merge commit**. Keep that merge
   method enabled; do not squash or rebase those PRs. Feature PRs still squash
   into `dev` as usual.
8. Clear the hold only when ready to enable candidate creation. No live GitHub
   setting, release, tag, or branch is changed merely by running local tests.

```bash
# Operator commands; these mutate repository settings when explicitly run.
gh variable set AUTO_RELEASE_HOLD --body 1
gh variable delete AUTO_RELEASE_HOLD
```

The hold is checked at evaluation, preparation, promotion, build authorization
and immediately before publication. It does not cancel jobs already running.

## Prepare a candidate

The daily **Auto Release** workflow counts squash-merged PR subjects absent from
the last stable tag. It requires the exact source commit's successful **CI** push
run, not merely an unrelated successful check. Fetch/API failures fail closed.

```bash
# Read readiness without pushing or publishing:
./scripts/release-ready.sh

# Evaluate now; below 10 PRs this will hold:
./scripts/release.sh candidate

# Prepare sooner, without bypassing CI or an existing candidate:
./scripts/release.sh candidate --force
```

The shell commands confirm before dispatching Actions on `main`; non-interactive
use requires `--yes`. They do not release local working-tree changes. Existing
`--pre-release` is an alias for `candidate`. Direct stable-version arguments and
`--skip-checks` are deliberately rejected.

Preparation atomically creates `release/vX.Y.Z` and `vX.Y.Z-rc.1`. It changes
neither `dev` nor `main`. The candidate branch receives CI; the tag triggers
**Release**. GoReleaser first creates a **draft**. After both DMGs, the MSI and
Linux packages are built, the reusable smoke workflow downloads the actual
draft installers and probes their installed server with disposable data.
It requires a healthy response and the exact embedded tag version.

Current runtime coverage: macOS Intel and Apple Silicon DMGs, Windows amd64 MSI,
Linux amd64 DEB and RPM. Linux arm64 packages are built but not runtime-tested in
this gate. Smoke checks do not replace manual UX, real integrations, upgrade or
migration testing. Windows MSI ProductVersion is numeric (without `-rc.N`),
while the installer filename and server retain the RC suffix; same-version
upgrades are supported by the existing WiX configuration. macOS bundle metadata
also remains numeric; the DMG filename and embedded binaries retain the RC suffix.

Any build or smoke failure leaves a draft; nothing becomes stable or Latest.
After installer success, the workflow attaches `rc-test-report-vX.Y.Z-rc.N.md`
and links it from the release notes, then publishes the RC as a **prerelease**,
excluded from normal stable update checks. Report-generation/attachment failures
leave a draft for retry; edited reports are never overwritten. Wait for both
candidate CI and its full Release workflow to succeed before promotion.

## Install and test the RC

Follow [RC_TEST_PROTOCOL.md](RC_TEST_PROTOCOL.md) and complete the report attached
to this exact prerelease. It includes a core baseline, pinned change inventory,
path-based risk suggestions and previous-RC retest scope. An agent/reviewer must
fill feature-specific steps from the actual diffs before testing; generated
cards start NOT RUN / HOLD and are not proof of coverage. Keep completed results
in a separately named, sanitized review record. The promotion checkbox is human
attestation that the report was completed and reviewed, not an automatic report
validator.

1. Open GitHub Releases and select the exact `vX.Y.Z-rc.N` prerelease.
2. Download its platform installer. Record the tag and commit, not just “dev”.
3. Use a disposable OS user/VM for installer and native-app tests, with separate
   test profiles and workspaces. `ORI_DATA_DIR` alone does not isolate installers,
   startup services or external paths. Never reset/migrate a real profile for RC
   testing; use the protocol's previous-stable fixture for upgrade coverage.
4. Verify the displayed version includes the expected RC suffix.
5. Exercise the PRs included in the candidate plus these core paths:
   - Launch, onboarding and workspace creation/opening.
   - Agent setup, chat and the integrations affected by this batch.
   - Save, restart, reopen and verify persisted state.
   - Negative cases, permissions, and any changed migrations/upgrade paths.
6. Record observed results and an APPROVE/HOLD decision against that exact RC.
   Share a sanitized completed-report link with the release approver. Keep
   developing on `dev` normally.

Inspect the batch using its frozen branch, not current `dev`:

```bash
git fetch origin --tags
git log <previous-stable-tag>..origin/release/vX.Y.Z --oneline
gh run list --workflow release.yml --branch vX.Y.Z-rc.1
```

## Fix a candidate

Use an isolated fix worktree/branch based on the active `release/vX.Y.Z`, and open
a stabilization PR **targeting that release branch**. Do not merge all of `dev`
into it: later features belong to the next batch. `wt pr` remains a feature
helper targeting `dev`; use an explicitly reviewed `gh pr create --base
release/vX.Y.Z` for stabilization PRs.

After that PR merges and branch CI is green:

```bash
./scripts/release.sh candidate vX.Y.Z
```

This tags the current candidate head with the next `-rc.N`. An unchanged head
reuses the existing tag instead of inventing another candidate; retry failed
Release jobs when the problem was infrastructure. Old tags are immutable.
A moved branch or a newer candidate invalidates promotion of an older RC.

## Promote the tested candidate

```bash
./scripts/release.sh promote vX.Y.Z-rc.2
# Compatibility spelling:
./scripts/create-release.sh vX.Y.Z-rc.2
```

Alternatively: **Actions → Promote Release → Run workflow**, select `main`,
enter the exact RC tag and attest that its test report was completed and
reviewed. Include the completed-report URL in the release-environment approval
comment when approving.

The workflow checks the published prerelease, latest RC, release-branch head,
exact candidate CI and successful installer workflow. It repeats those checks
after environment approval. It atomically advances `main` and creates the stable
tag at the **same source commit as the approved RC**. Advancing `dev` never
invalidates or changes that selection. A changed `main` or candidate refuses
instead of force-overwriting history.

Stable installers are rebuilt with the final version, so they are not claimed
to be byte-identical to RC installers. Their actual packages go through the same
blocking smoke gate before the draft becomes the stable **Latest** release.
Raw stable tags without a promotion receipt are rejected by the build workflow.

## Merge back and continue

After stable publication, Release opens an idempotent `release/vX.Y.Z → dev` PR.
Merge it with a **merge commit**, preserving new dev work and all stabilization
fixes. Conflicts require review and a new CI run; do not resolve them by replacing
all of dev with the release tree. Do not edit the shipped release branch to
resolve conflicts: use a separate integration branch incorporating both parents,
then PR that branch to dev with a merge commit.

The next candidate waits until the previous stable revision is an ancestor of
`dev`. Feature PRs continue throughout. The cadence and DevOps dashboard compare
the stable tag's ancestry rather than publication time, so work merged while you
were testing is correctly included in the next batch.

After merge-back, delete the temporary release branch if desired. Keep RC and
stable tags for provenance. Automatic release pruning is intentionally not part
of this workflow.

## Recovery

- **Build/smoke failed:** inspect `gh run view <id> --log-failed`, then
  `gh run rerun <id> --failed` for an infrastructure retry. Do not delete/retag.
  Product fixes require a new RC and testing.
- **Promotion already created the stable tag:** inspect/retry its Release run;
  do not dispatch a second promotion or move the tag.
- **Stable published but merge-back PR creation failed:** the release remains
  published. Retry only the failed `sync-dev` job. Creating another stable tag
  is neither necessary nor safe.
- **Release branch diverged / multiple candidates / API unavailable:** stop and
  inspect. The gate will not guess, merge unrelated work, or overwrite refs.
- **Rollback:** retain published tags and downloads. Use a separately approved
  corrective candidate/release; never silently replace a published version.

## Local validation of lifecycle changes

```bash
make test-release                 # offline temp Git remotes + installed-server fixture
bash scripts/devops-cli.test.sh    # ancestry-based dashboard regression
```

Workflow YAML also needs actionlint. For a delivery PR, run the repository's
normal `make test`, `make lint-new`, scoped security checks where applicable,
and `make test-js` gates. Local tests do not prove GitHub permissions, live
prerelease behavior or cross-platform installers; confirm those during rollout.
