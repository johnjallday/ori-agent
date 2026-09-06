# DevOps Explore: what should I work on?

Explore precedes Capture and Plan. It offers eight reusable investigation prompts,
not a new delivery pipeline. The global `e` action works without a selected Issue.

## Use it from the menu

Run `./scripts/devops.sh`, then press **e — Explore next work** from any Issue
view, including an empty list. Choose a prompt, optionally add context such as
"45 minutes; no frontend", and inspect the preview. Choose **d** to display/copy
it, **l** to launch an advisor, or **q / Enter** to cancel. Launch asks for Claude
or Pi, model, thinking, and confirmation. Quit the native advisor normally to
return to the same picker selection, then press Enter. The line REPL also accepts
`e` / `explore`; redirected input can display/cancel, but interactive launch needs
a terminal.

| Preset | Question |
| --- | --- |
| `next` | Pick my next task |
| `quick-win` | Find a quick win |
| `finish` | Finish unfinished work |
| `ux` | Find user-experience friction |
| `reliability` | Find reliability gaps |
| `workflow` | Improve the development workflow |
| `missing` | Explore missing capabilities |
| `interview` | Ask me about time, energy, and desired outcome first |

```bash
./scripts/devops.sh explore                         # same menu; lists presets in a pipe
./scripts/devops.sh explore quick-win --context '45 minutes; no frontend' --print
./scripts/devops.sh explore next --kind pi           # confirm, foreground conversation
./scripts/devops.sh explore next --kind claude --model sonnet --thinking high
# Scripted request: explicit provider intent and confirmation, native print mode.
./scripts/devops.sh explore reliability --kind pi --thinking high --yes
```

`--print` means **print the prompt**, not ask a model for a response. It makes no
GitHub, native-agent, or evidence-collector call. It cannot be combined with
launch options. Model/thinking overrides require an explicit kind; repeat options,
unknown presets, missing values and terminal controls are rejected. Context is
literal text (up to 4000 characters); it is never evaluated as shell syntax.

## Dependencies and recovery

- Display: Bash 3.2+, Git, and this checkout's prompt files. No Python, gh, Claude,
  Pi, Herdr, or credentials needed. The existing Issue picker still requires gh;
  direct `explore` remains usable without it.
- Launch: Python 3, an authenticated native Claude or Pi CLI, and the advertised
  safety flags below. Optional gh authentication supplies live Issue/PR evidence;
  absent/failed GitHub reads are reported to the advisor as unavailable.
- Existing native model/thinking selectors are reused; feature defaults and
  `.herdr/devflow.toml` are neither inherited nor changed. Extension-only custom
  providers are unavailable with extension loading disabled. Use `--print` with
  a separately configured agent if the restricted native launch cannot meet your
  provider setup.
- A launch failure returns the native exit status and display-only recovery
  command. No unbounded automatic retry, hidden fallback agent, or resumed session.
  Older native clients must be upgraded if required safety flags are absent.

Tune the prompts in `scripts/devops-prompts/`; `common.md` supplies shared
boundaries and evidence-quality requirements. No generated prompt cache is stored.

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
Prompts and evidence are passed as literal process arguments and may be visible
to local process-inspection tools, as well as the selected provider. Read-only
tool selection is **not an OS sandbox or a confidentiality boundary**;
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

## Validation

```bash
bash scripts/devops-cli.test.sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/devops-explore.test.py -v
```

The DevOps suite includes the Explore checks: hermetic CLI/dependency tests,
native argv/capability/failure fixtures, bounded evidence and task progress, and a
real PTY matrix covering all five picker views both empty and populated. PTY
remote dashboard data and native responses are fixture-backed, not provider
endorsements. Set `EXPLORE_DEMO_TRANSCRIPT=/absolute/path/to/transcript.txt` for
append-only PTY transcripts. Development also verified the installed native help
contracts, real Git/GitHub collector, and actual `./scripts/devops.sh` menu through
context/display/cancel/return/quit with live dashboard data. Live model-backed
sessions remain a manual integration check.
