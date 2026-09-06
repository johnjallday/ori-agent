# DevOps Explore: advisory launch contract

Explore precedes Capture and Plan. It offers eight reusable investigation prompts,
not a new delivery pipeline. The global `e` action works without a selected Issue.

## Findings and selected design

The existing `prompt_planner_selection` UI can be reused with an advisory label.
The existing `wt plan` launch cannot: it creates Issue snapshots/task starters and
persists Issue-bound sessions. Extending Herdr state with a third session family
would add identity, retry, migration and cleanup machinery without improving the
first increment's question-and-answer experience.

Use a **fresh foreground native Claude/Pi process** instead. The user can answer
questions and explore alternatives, then quit to return to the same picker view.
Never pass continue/resume, create a worktree, call `wt plan`, or register a
feature, planner, continuation, Overnight participant, or PR owner. A failed
process reports its exit status; repeating Explore starts a new session, not an
idempotent/resumed handoff. No persistent Ori session state is needed.

Local CLI help and installed Pi documentation were inspected during development:

- Pi supports `--tools read,grep,find,ls`, `--no-session`, disabled resource/context
  discovery, ignored project trust, startup offline mode, `--model`, and
  `--thinking`. Offline mode disables startup network tasks, not model requests.
- Claude supports `--safe-mode` (customizations disabled), `--restricted`
  (working-directory file access and restricted configuration), explicit
  Read/Glob/Grep tools, `--strict-mcp-config`, `--permission-mode dontAsk`,
  `--model`, and `--effort`. `--no-session-persistence` is print-mode only.
- Older native versions may lack these controls. Check the installed help before
  launch and fail closed, with upgrade or `--print` guidance, rather than silently
  falling back to unrestricted tools.

These are verified CLI contracts, not proof of live provider behavior. Tests and
manual reports must distinguish parser/argv/terminal checks from model-backed
integration checks.

## Evidence and permissions

Read/search-only model tools cannot run `gh` or `git`. After launch confirmation,
collect a small snapshot using fixed, bounded read commands: checkout status,
worktrees, local branch/recent-change evidence, open Issues, and dev-targeted PRs.
Include authoritative task-list progress from checked-out feature worktrees.
Mark source failures and truncation explicitly; absence of evidence is not zero
work. No background polling, fetch, Issue write, or automatic follow-up action.

Optional context and all snapshot contents are inert data, not executable shell
or instruction overrides. Prompts require source citations, uncertainty labels,
and a concrete next step; interactive-choice asks three questions before making
recommendations. Do not inspect credentials or ignored local configuration.

The launch preview explains that repository/GitHub evidence and subsequent file
reads go to the selected provider and may incur usage. Do not persist snapshots
or prompts in Ori files or logs. Pi sessions are ephemeral; Claude interactive
sessions may be stored in its normal user-local history. Native authentication
and runtime housekeeping are outside Ori's no-repository-mutation contract.
Read-only tool selection is **not an OS sandbox or a confidentiality boundary**;
Pi's file reads are not restricted to the repository, managed Claude policy may
still apply, and a person can use native interactive controls. Never advertise
stronger isolation than these controls provide.

## CLI and cancellation

`explore` opens the same prompt menu as `e`. `explore <preset> --print` prints only
the composed prompt without GitHub, a native agent, or live evidence collection.
A scripted launch requires explicit `--kind claude|pi` and `--yes`; it uses the
native print mode. The interview preset requires a terminal for a launch.
Interactive launch reuses model/thinking selection without changing defaults.
Preview, display, cancel and EOF do not collect evidence or contact a provider.
After a failed launch, keep the picker and selection; offer display-only recovery.
