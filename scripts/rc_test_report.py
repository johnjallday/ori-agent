"""Deterministic, read-only RC test-card generation from pinned Git objects.

No model, PR-body execution, live-dev diff, or inferred PASS. Path-based recipes
are prompts for review, not an assertion of complete feature coverage.
"""

from dataclasses import dataclass
import html
import re
from urllib.parse import quote


REPOSITORY = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*\Z")
MAX_COMMITS = 250
MAX_PATHS = 2000

BASELINE = (
    ("B1", "Install/launch the actual RC in a disposable OS user or VM; inspect its displayed version.",
     "App opens normally; displayed version matches this exact RC. No live install/profile is replaced."),
    ("B2", "With an empty test profile, complete onboarding and create/open a scratch workspace.",
     "Onboarding finishes; the workspace is reachable and uses only the chosen test folder."),
    ("B3", "Create/configure a test agent, select an explicitly approved provider, and send a harmless message.",
     "A response arrives in the intended conversation with no cross-agent/workspace leakage."),
    ("B4", "Run one explicitly permitted tool against scratch data; then deny or cancel a second action.",
     "The allowed action has the expected result; the denied action makes no unauthorized change."),
    ("B5", "Change a harmless setting, save a conversation, quit fully, reopen, and revisit the workspace.",
     "Settings, agent configuration, workspace and saved messages survive restart without duplicates."),
    ("B6", "Create the previous-stable fixture, stop it, back it up, then install this RC over that TEST installation.",
     "The prior workspace/agent/conversation inventory survives upgrade and a second restart."),
)


@dataclass(frozen=True)
class Recipe:
    id: str
    title: str
    patterns: tuple
    action: str
    expected: str


RECIPES = (
    Recipe("R-DATA", "Data, migrations and settings", ("internal/database/", "internal/config/", "internal/settings", "internal/session/"),
           "Restore the stopped previous-stable test fixture to its original test-only paths. Upgrade, compare the recorded inventory, edit one item, then restart twice.",
           "No missing/duplicated data, migration error or lost settings; new edits persist. Never downgrade the migrated database."),
    Recipe("R-PERM", "Permissions, identity and reset", ("internal/auth", "internal/connections/", "internal/settingsreset/", "internal/resetstate/", "internal/runtimecapability/", "internal/permissions"),
           "Identify the changed permission/reset boundary from the diff. In disposable data, cancel/deny it first; then grant only the specific permission needed for a harmless operation.",
           "Denial/cancellation leaves files and state unchanged; allowed work stays within its approved scope. Reset tests never target live data."),
    Recipe("R-AGENT", "Agents and conversations", ("internal/agent", "internal/chat", "internal/llm/", "internal/web/static/js/agent", "internal/web/static/js/chat"),
           "Open /agents, configure two test agents and use each in a separate scratch conversation. Exercise the changed behavior, switch conversations and reload.",
           "The selected agent/model and conversation stay correct; the changed action works without leaking messages or settings between agents."),
    Recipe("R-WORK", "Workspaces, map and execution", ("internal/workspace", "internal/workspacerun/", "internal/orchestration", "internal/web/static/js/workspace", "internal/web/static/js/map", "internal/web/static/js/district"),
           "Open /workspaces. Create two scratch workspaces, exercise the changed map/task action, switch between them, reload and reopen the result.",
           "Selection and changes belong to the correct workspace; task/result state and layout remain consistent after reload. Cancel any tool action before it can touch real data."),
    Recipe("R-TOOL", "Plugins and external integrations", ("internal/plugin", "internal/mcp/", "internal/setupjourney/", "internal/reviewedintegration/", "internal/connections/", "internal/calendar", "internal/email"),
           "Record the exact companion/plugin version. Use a test account or fixture to exercise the changed integration, including unavailable/disconnected and denied-permission cases.",
           "The real supported integration succeeds when authorized and fails clearly when unavailable. No duplicate external writes, credential exposure or unintended fallback."),
    Recipe("R-UI", "Shared UI and navigation", ("internal/web/",),
           "Use the actual browser UI: keyboard-navigate the affected control, resize the window, follow its entry/return links and repeat after reload.",
           "The feature is reachable; focus, labels and errors are usable; content and navigation do not disappear at the tested viewport."),
    Recipe("R-RUNTIME", "Installers, startup and dependencies", ("build/", "cmd/", "internal/menubar/", "internal/server/", "go.mod", "go.sum", "package.json", "package-lock.json", ".goreleaser.yaml"),
           "Test fresh launch and previous-stable upgrade with the actual package on each affected OS. Exercise the normal app/menubar entry point, quit and restart.",
           "Installation, native startup and shutdown work; no orphan process, port takeover or live-profile mutation. A healthy server probe alone is not a native-UI test."),
    Recipe("R-DEV", "Development and release tooling", (".github/", "scripts/", "Makefile", ".agents/", ".claude/"),
           "Read the changed command contract and its tests. Exercise read-only/preview and refusal paths against a disposable repository or mocked services; record exact argv and expected effects.",
           "No real release, push, Issue write, setting change or agent launch occurs without separate authorization. Local simulation does not prove live GitHub permissions."),
)


def inline(value):
    """Keep repository text inert even inside Markdown tables or HTML wrappers."""
    visible = "".join(char if char.isprintable() else f"\\u{ord(char):04x}" for char in value)
    escaped = html.escape(visible)
    # Inline HTML alone does not disable Markdown parsing inside the element.
    # Encode its delimiters too, so titles cannot inject links/images/checks.
    for char in "\\`*_[]!~|@":
        escaped = escaped.replace(char, f"&#{ord(char)};")
    return f"<code>{escaped}</code>"


def test_path(path):
    return (path.startswith("tests/") or path.endswith("_test.go")
            or ".test." in path or ".spec." in path
            or path.startswith("docs/testing/") or "test-guide" in path)


def recipe_matches(recipe, path):
    if test_path(path):
        return False
    return any(path.startswith(pattern) if pattern.endswith("/") or pattern.startswith("internal/")
               else path == pattern for pattern in recipe.patterns)


def paths_between(git, before, after):
    paths = git("diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z",
                before, after, "--").split("\0")
    paths = sorted(path for path in paths if path)
    if len(paths) > MAX_PATHS:
        raise ValueError("RC diff is too large for an honest test card; split/review the batch explicitly")
    return paths


def path_list(paths, limit=30):
    lines = [f"- {inline(path)}" for path in paths[:limit]]
    if len(paths) > limit:
        lines.append(f"- **{len(paths) - limit} more paths:** inspect the linked full diff; this list is abbreviated.")
    return lines or ["- None in this range."]


def build_report(repo, tag, previous_tag, repository, git):
    """Return (asset_name, Markdown); refs are validated before reading evidence."""
    if not REPOSITORY.fullmatch(repository):
        raise ValueError("Expected a GitHub owner/repository identity")
    version, sha, _ = repo.candidate(tag)
    if previous_tag != repo.previous_stable(version, sha):
        raise ValueError("Test-card baseline must be the previous stable ancestor of this RC")
    previous_sha = repo.tags[previous_tag]
    earlier = [value for value in repo.candidates(version) if value != tag]
    previous_rc = earlier[-1] if earlier else None
    if previous_rc and not repo.ancestor(repo.tags[previous_rc], sha):
        raise ValueError("Previous RC is not an ancestor; reconcile candidate history before generating a report")

    base = f"https://github.com/{repository}"
    protocol = f"{base}/blob/{sha}/docs/RC_TEST_PROTOCOL.md"
    compare = f"{base}/compare/{previous_sha}...{sha}"
    commits = git("rev-list", "--reverse", "--first-parent", f"{previous_sha}..{sha}").splitlines()
    if len(commits) > MAX_COMMITS:
        raise ValueError("Too many candidate commits for a reviewable test card; split/review the batch explicitly")
    paths = paths_between(git, previous_sha, sha)
    evidence = []
    for commit in commits:
        subject = git("show", "-s", "--format=%s", commit)
        parent = git("show", "-s", "--format=%P", commit).split()[0]
        changed = paths_between(git, parent, commit)
        # A commit message supplies only a navigation hint, never release
        # membership or instructions. Membership comes from pinned ancestry.
        match = re.search(r"\(#([1-9][0-9]*)\)$", subject) or re.match(r"Merge pull request #([1-9][0-9]*)\b", subject)
        reference = f"[PR #{match[1]}]({base}/pull/{match[1]}) (subject reference)" if match else "No PR reference in subject"
        evidence.append((commit, subject, changed, reference))

    lines = [
        f"# RC test report — {tag}", "",
        "**Blank test card, not a certificate. Decision: HOLD until completed and reviewed.**",
        "All manual results start NOT RUN. Generation and green CI do not prove the product was manually tested.", "",
        f"- RC: `{tag}`", f"- Exact candidate commit: `{sha}`",
        f"- Previous stable: `{previous_tag}` (`{previous_sha}`)",
        f"- Previous RC: `{previous_rc}`" if previous_rc else "- Previous RC: none (first candidate)",
        f"- [Full frozen change range]({compare})", f"- [Testing protocol at this exact commit]({protocol})",
        f"- [RC installers]({base}/releases/tag/{tag})", "",
        "## Tester and environment", "",
        "| Field | Record before sign-off |", "| --- | --- |",
        "| Tester / UTC date | TODO |", "| OS / architecture / browser | TODO |",
        "| Installer filename / downloaded SHA-256 | TODO |",
        "| Running app version / test port | TODO |",
        f"| Upgrade source | {previous_tag}; record fixture identity and pre-upgrade inventory |",
        "| Test account / disposable profile / workspace | Record a non-sensitive identifier; no real paths or secrets |",
        "| Provider / companion versions / authorized external actions | TODO; use test accounts and explicit consent |", "",
        "## Automated evidence to check", "",
        "- [ ] Exact candidate CI and its full Release workflow are green; record run URLs below.",
        "- [ ] Downloaded package is from this RC, not current dev, wt demo or a local rebuild.",
        "- CI run URL: TODO", "- Release/installer run URL: TODO",
        "- Automated runtime coverage: macOS Intel/Apple Silicon DMGs, Windows amd64 MSI, Linux amd64 DEB/RPM.",
        "- Not proven by those checks: Linux arm64 runtime, native app UX, real integrations, upgrade/data behavior.", "",
        "## Core checks — run for every RC", "",
        "Use a disposable OS user/VM for installation and native-app checks. A separate ORI_DATA_DIR alone",
        "does not isolate system installers, startup services, credentials or external workspace paths.",
        "Do not use production profiles, accounts, scheduled jobs or real workspace files.", "",
        "| ID | Action | Expected result | Result / evidence |", "| --- | --- | --- | --- |",
    ]
    for case_id, action, expected in BASELINE:
        lines.append(f"| {case_id} | {action} | {expected} | NOT RUN |")

    lines += ["", "## Change-specific review — complete before testing", "",
              "This inventory is derived from first-parent commits and their actual changed paths in the frozen range,",
              "not from PR titles or current dev. Merge diffs are measured against the first parent. Reverted changes",
              "can appear in the inventory even when absent from the net diff: review them rather than assuming coverage.",
              "PR links are subject references only; inspect the pinned commit diff as the authoritative evidence.",
              "For EVERY entry, supply a concrete test or an explained non-user-visible disposition. Unknown scope = HOLD.", ""]
    for index, (commit, subject, changed, reference) in enumerate(evidence, 1):
        selected = [recipe.id for recipe in RECIPES if any(recipe_matches(recipe, path) for path in changed)]
        lines += [f"### C{index}: {inline(subject)}", "",
                  f"- [Pinned commit diff]({base}/commit/{commit}) — `{commit}`; {reference}",
                  f"- Suggested areas: {', '.join(selected) if selected else 'unmapped / documentation / test-only — reviewer must classify'}.",
                  "- Changed paths:", *["  " + line for line in path_list(changed)],
                  "- Disposition: TODO — user-visible / risk-only / non-user-visible, with reason.",
                  "- Exact entry/navigation and setup: TODO (derive from the diff and relevant tests/guides).",
                  "- Golden path: TODO numbered actions → observable expected result.",
                  "- Negative/edge case: TODO action → expected refusal/recovery behavior.",
                  "- Result / evidence / linked bug: NOT RUN.", ""]
        tests = [path for path in changed if test_path(path)]
        if tests:
            lines += ["Changed test/guide references (not execution evidence):",
                      *[f"- [View at this commit]({base}/blob/{commit}/{quote(path, safe='/')}) — {inline(path)} (may be deleted; inspect diff)."
                        for path in tests[:10]], ""]
            if len(tests) > 10:
                lines += [f"{len(tests) - 10} additional test paths are in the full diff.", ""]

    lines += ["## Suggested risk checks from the net changed paths", "",
              "Path-based suggestions are deliberately conservative. Adapt them to the observed code, add missing",
              "feature-specific checks above, and record NOT APPLICABLE with a reason only when genuinely unrelated.", ""]
    for recipe in RECIPES:
        matches = [path for path in paths if recipe_matches(recipe, path)]
        if matches:
            lines += [f"### {recipe.id}: {recipe.title}", "", *path_list(matches, limit=8),
                      f"- Action: {recipe.action}", f"- Expected: {recipe.expected}",
                      "- Result / evidence: NOT RUN.", ""]

    lines += ["## Retest scope", ""]
    if previous_rc:
        before = repo.tags[previous_rc]
        delta = paths_between(git, before, sha)
        lines += [f"[Changes since {previous_rc}]({base}/compare/{before}...{sha})", "", *path_list(delta), "",
                  "Re-run every core check, each reported bug's reproduction, and affected/dependent change cases.",
                  "Repeat the upgrade test from the previous-STABLE fixture, not an already-migrated RC profile.",
                  "Previous RC PASS results and approval never carry over automatically."]
    else:
        lines += ["First candidate: run the core checks and all applicable change/risk cases."]
    lines += ["", "## Findings and decision", "",
              "| Bug / reproduction / expected vs actual | Severity | RC + platform | Evidence | Retest result |",
              "| --- | --- | --- | --- | --- |", "| TODO (or explicitly None found) | TODO | TODO | TODO | NOT RUN |", "",
              "- Blocking bugs: NOT REVIEWED.", "- Untested platforms/integrations and coverage gaps: NOT REVIEWED.",
              "- Accepted non-blocking issues and rationale: NONE RECORDED.",
              "- Core verdict: NOT RUN.", "- Upgrade verdict: NOT RUN.",
              "- Changed-feature/risk verdict: NOT RUN.", "- Decision: **HOLD**.",
              "- Approver / UTC date / completed-report URL: TODO.", "",
              "Crashes, data loss, unauthorized actions, broken core paths, unresolved change scope or missing",
              "critical tests block approval. Non-critical gaps/issues require explicit acceptance with a reason.",
              "Save a completed copy under a distinct rc-test-results filename. Never overwrite this generated card.",
              "Remove secrets, personal messages, local paths and customer data before sharing the results.",
              "The promotion checkbox is human attestation; automation does not parse this report or award a PASS.",
              "After an APPROVE decision, promote only the exact RC named at the top using the documented release flow.", ""]
    return f"rc-test-report-{tag}.md", "\n".join(lines)


def report_links(repository, tag, sha, asset_name):
    base = f"https://github.com/{repository}"
    return ("<!-- ori-rc-test-report -->\n"
            "## Test this release candidate\n\n"
            f"1. Download the installer for **{tag}** (commit `{sha}`).\n"
            f"2. [Download the RC test report]({base}/releases/download/{tag}/{asset_name}) and "
            f"follow the [testing protocol]({base}/blob/{sha}/docs/RC_TEST_PROTOCOL.md).\n"
            "3. Complete the core, upgrade and change-specific checks. Every manual result starts NOT RUN.\n"
            "4. Keep a separate completed results copy; record APPROVE or HOLD before requesting promotion.\n\n"
            "This prerelease is not stable. Development on dev continues while you test.\n"
            "<!-- /ori-rc-test-report -->")


def add_report_links(body, block):
    """Idempotently append only our block. Never replace human-edited content."""
    start, end = "<!-- ori-rc-test-report -->", "<!-- /ori-rc-test-report -->"
    if start not in body and end not in body:
        return body.rstrip() + "\n\n" + block + "\n"
    if body.count(start) != 1 or body.count(end) != 1:
        raise ValueError("Ambiguous RC report block in release notes; preserve it for manual review")
    first, last = body.index(start), body.index(end) + len(end)
    if body[first:last] != block:
        raise ValueError("RC report block was edited; refusing to overwrite release notes")
    return body
