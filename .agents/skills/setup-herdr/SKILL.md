---
name: setup-herdr
description: Configure, diagnose, and operate Ori's repository-local Herdr devflow bridge through scripts/wt.sh. Use when the user asks to set up Herdr for Ori, connect wt worktrees to Herdr, choose Pi, Claude, or Codex agents, repair a Herdr handoff, inspect or prompt managed agents, configure one-time continuations, enable the standalone macOS wake service, or prepare an Overnight Run.
---

# Set Up Herdr

Ground every action in the current Ori checkout. Work conversationally: discover the user's desired outcome, inspect current state, explain the smallest necessary mutation, execute authorized changes, and verify the resulting capability.

## Establish the source of truth

1. Resolve the repository with `git rev-parse --show-toplevel` and work from that root.
2. Read applicable `AGENTS.md` instructions before changing files or starting worktrees.
3. Verify that `.herdr/devflow.toml`, `scripts/wt.sh`, and `scripts/herdr-devflow.sh` exist. If they do not, stop and explain that this is not a bridge-enabled Ori checkout.
4. Read the matching section of `docs/herdr-devflow.md`: **Setup** for bridge readiness; **Starting a feature** and **Selecting the exact agent** for feature work; **One-time continuations** for scheduling; **Guarded cleanup** for `wt done`; and the complete **Overnight Runs** section for unattended work. Also read `docs/architecture/herdr-standalone-wake-v1-contract.md` before wake installation or troubleshooting.
5. Treat `.herdr/devflow.toml` and the helper's current `--help` output as authoritative when documentation and remembered commands disagree.

`wt` is a sourced zsh function, not a standalone executable. Run it from the repository root in the same shell invocation, for example:

```zsh
source scripts/wt.sh; wt herd doctor
```

Do not search broad home-directory paths for another checkout. If no Ori checkout is available from the workspace or user-provided path, ask the user to open one or provide its path.

## Select the requested lane

Separate these capabilities instead of treating them as one installation:

1. **Basic bridge** — install or refresh the stable helper, linked Herdr plugin, local state, and macOS continuation dispatcher.
2. **Agent integration** — inspect or explicitly install Herdr's Pi, Claude, and/or Codex integration.
3. **Feature agents** — hand an existing Ori feature worktree to Herdr, add roles, or repair a saved binding.
4. **One-time continuation** — schedule one prompt for an existing managed feature and role.
5. **Wake support** — install and verify the privileged standalone macOS Herdr Wake Service.
6. **Overnight Run** — supervise an explicitly ordered set of eligible Claude agents within the documented safety boundary.

Ask only for a choice that materially changes the result. Common choices are Pi, Claude, Codex, or a specific combination for integrations; the exact feature and role; and whether whole-Mac wake or Overnight behavior is wanted.

## Preserve authorization boundaries

- Run read-only discovery without asking for confirmation.
- Treat a direct request to "set up Herdr" as authorization for `wt herd setup` after resolving the exact checkout and briefly stating what it changes. Request any platform or sandbox approval the command requires.
- Do not infer permission to install Pi/Claude/Codex integrations, edit `.herdr/devflow.toml`, create a feature worktree, install the root wake service, schedule a prompt, or start an Overnight Run. Obtain explicit intent for each applicable lane.
- Never type, request, store, or relay an administrator password. A wake installation may invoke the normal macOS authorization UI; the user approves it there.
- Never invoke `pmset` directly, use `pmset cancelall`, replace the fixed wake owner, or remove foreign wake events.
- Never guess a pane, workspace, agent, feature, role, schedule, or native-session identifier. Inspect current structured state first.
- Never report wake readiness from registration alone. Require `wt herd wake doctor` and the command's direct verification evidence.
- Never claim `--stay-awake` protects against lid close, logout, shutdown, restart, power loss, or forced sleep.
- Do not use `wt done --herdr-override` to bypass a known active agent or unresolved schedule.

## Run the read-only preflight

Use current command output rather than remembered state:

```zsh
git status --short
command -v herdr
source scripts/wt.sh; wt herd doctor
source scripts/wt.sh; wt herd target
source scripts/wt.sh; wt herd status --json
herdr integration status
```

Interpret the results carefully:

- `wt herd doctor` may exit nonzero while still returning useful per-check recovery steps. Report each `FAIL` and relevant `WARN`; do not collapse them into "Herdr is broken."
- `wt herd status` printing `No open agents.` with exit 0 is healthy.
- Socket, plugin-action, or bridge-query failures can be sandbox permission problems even when they do not literally say `Operation not permitted`. Retry the same read with the required local-socket permission before declaring Herdr unavailable.
- If an authorized `wt herd doctor` passes and direct `herdr status server` / `herdr agent list` reads succeed, the basic bridge may be reported ready even when a sandbox still blocks `wt herd target` or `wt herd status`. Report the blocked observation separately. Do not perform a handoff, targeting, rebind, or schedule operation until its exact workspace, feature, role, and live agent evidence is available.
- Use `herdr agent list` and `herdr pane list` when structured bridge reads remain blocked or live target recovery is needed. Treat their returned identifiers as ephemeral.
- A `PASS` diagnostic can include a recovery string that documents an optional command. Do not present that string as required remediation. For integrations, act only on `herdr integration status` and the integration the user explicitly requested.
- Do not repair findings during discovery unless the user's requested lane authorizes that change.

## Configure the basic bridge

1. Summarize the preflight and current defaults from `.herdr/devflow.toml`, including the primary role and kind.
2. Explain that setup copies a stable helper and plugin to user-local runtime storage, initializes local state, and on macOS registers the `com.ori.herdr-devflow` LaunchAgent.
3. Run:

   ```zsh
   source scripts/wt.sh; wt herd setup
   ```

4. Preserve the configured primary kind unless the user asks to change it.
5. Re-run `wt herd doctor`. Use its printed recovery command for each remaining failed check.

Setup never installs Herdr itself and never installs or rewrites a Pi/Claude/Codex integration. Do not describe it as doing so.

## Configure agent integrations

First inspect:

```zsh
herdr integration status
```

If the requested integration is missing and the user explicitly selected it, run only the matching command:

```zsh
herdr integration install pi
herdr integration install claude
herdr integration install codex
```

Do not install both merely because both executables exist. Re-run `herdr integration status` and `wt herd doctor` afterward. If native session restore is unavailable, state that limitation instead of pretending scheduled continuation can restore a missing conversation.

## Connect or operate feature agents

- Do not create a worktree just to test Herdr.
- For a new planned feature, follow the repository's PRD/task workflow and let `wt start <feature> [--kind KIND]` own worktree creation and Herdr handoff.
- For an existing worktree that the user explicitly wants connected, use the exact feature, absolute worktree path, branch, and optional kind with `wt herd handoff`.
- If a completed Git worktree exists but handoff failed, use the printed `wt herd retry` command; do not recreate the worktree.
- Use feature-scoped roles for normal control:

  ```zsh
  source scripts/wt.sh; wt herd add reviewer --kind claude --feature <feature>
  source scripts/wt.sh; wt herd prompt builder "<prompt>" --feature <feature>
  source scripts/wt.sh; wt herd focus builder --feature <feature>
  source scripts/wt.sh; wt herd read builder --lines 160 --feature <feature>
  ```

- Use `--target` only as an explicit recovery escape hatch after inspecting live agents. If a saved role needs repair, bind the exact observed target:

  ```zsh
  source scripts/wt.sh; wt herd rebind builder --target <live-target> --feature <feature>
  ```

An `agent_pane_not_found` result means the target is stale or guessed. Refresh the live roster; do not retry a placeholder identifier.

## Configure a one-time continuation

Require an exact feature, managed role, absolute future time with timezone understood, and prompt. Then inspect the saved role and unresolved schedules before creating another one.

```zsh
source scripts/wt.sh; wt herd status --feature <feature>
source scripts/wt.sh; wt herd schedule list --feature <feature>
source scripts/wt.sh; wt herd continue builder --feature <feature> --at "YYYY-MM-DD HH:MM" --prompt "<exact prompt>"
source scripts/wt.sh; wt herd schedule list --feature <feature>
```

A continuation prompts only an existing managed agent; it does not create a workspace, pane, process, or replacement conversation. If the saved role is missing, inspect and explicitly rebind it before scheduling.

Add `--wake` only when the user explicitly wants whole-Mac wake and the standalone service is already verified healthy. If it is not healthy, enter the wake-support lane first rather than silently creating a non-waking schedule.

## Configure wake support

Wake support is macOS-only and changes root-owned system state. Read the wake contract, explain the fixed files and `com.ori.herdr-wake` ownership, then run installation only after explicit approval:

```zsh
source scripts/wt.sh; wt herd wake install
source scripts/wt.sh; wt herd wake status --json
source scripts/wt.sh; wt herd wake doctor
```

Do not pass `--yes` unless the user explicitly requests non-interactive installation and the surrounding execution environment can safely show or obtain macOS authorization. Do not claim readiness unless installation, daemon, protocol, allowed UID, and self-test checks pass.

Uninstall only after inspecting and resolving every active wake-enabled continuation and Overnight Run. Report exactly what is removed and that unrelated wake events, agents, worktrees, and the user dispatcher remain untouched.

## Prepare an Overnight Run

1. Read the complete Overnight Run and wake sections in `docs/herdr-devflow.md`.
2. Inspect `wt herd doctor`, Claude usage readiness, wake readiness, external power, exact saved sessions, active runs, and unresolved continuations.
3. Require the user to name every Claude agent in the desired order. Never enroll all agents implicitly.
4. Begin with `--dry-run` and show the resolved start, deadline, timezone, queue, and resume ceiling.
5. Start only after the helper's own consequence summary and user confirmation. Do not bypass that gate with `--confirm` unless the user explicitly approved the exact rendered plan.

Overnight automation may implement planned work, run validations, update checklists, and create planned milestone commits. It must stop before a Demo, credentials or external authorization, PR creation, merge, deploy, release, destructive cleanup, or `wt done`.

## Finish with evidence

Report:

- the repository and config used;
- what was inspected and what was changed;
- bridge, Herdr binary/schema/socket, plugin, integration, scheduler, and wake status as applicable;
- live agents or the healthy absence of agents;
- exact remaining warnings and their recovery commands;
- any optional next lane, clearly labeled as not yet performed.

Do not call the setup complete while a required `FAIL` remains. Distinguish optional warnings, such as unconfigured wake support, from failures in the capability the user requested.
