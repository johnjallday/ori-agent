# Away Dispatcher

The Away Dispatcher is repository tooling that starts only owner-listed,
pre-written plans while this Mac is unattended. It chooses **when** a plan may
start; it does not choose work, approve a delivery gate, open or merge a pull
request, push `dev`/`main`, release, or run `wt done`.

## Requirements

- macOS with the `dev` worktree present
- `jq`, `gh`, Git, zsh, and the repository-local Herdr bridge on launchd's PATH
- a completed task list for each queue entry
- the fixed-purpose `pmset` LaunchDaemon helper below
- the pinned and phone-verified herdr-remote setup described below

Run the normal bridge preflight before preparing away work:

```zsh
source scripts/wt.sh
wt herd doctor
herdr integration status
```

## Record unattended agent intent in every plan

An away-eligible task list must record both values explicitly near the existing
implementation instructions:

```markdown
Implementation agent: `pi`
Implementation model: `openai/model-id`
```

`Implementation agent` must be `claude`, `codex`, or `pi`. The model is an
opaque, non-empty value passed as one argument to `wt start --model`. The
dispatcher never falls back to `.herdr/devflow.toml`; a missing/invalid agent or
missing model is a visible skip.

## Write the authorization queue

The queue is the gitignored dev-worktree file `tasks/away-queue.md`. Only slugs
listed here may start:

```markdown
# September trip
- 431-unit-inspector-polish
- 438-trigger-webhook-retries (after: 431-unit-inspector-polish)
- workspace-memory-export        backend-only owner note
```

Use one `- <slug>` per line. An optional dependency clause must immediately
follow the slug: `(after: slug-a, slug-b)`. Remaining text is a human note.
Malformed list lines are reported as `parse-error` and never guessed. A
dependency is not satisfied by an open PR; it must have a merged PR against
`dev` or a branch head already contained in `origin/dev`.

The task list's `## Relevant Files` bullets are the predicted footprint. Any
intersection with an active worktree/branch's actual `origin/dev...branch` diff
hard-blocks that plan. There are no exceptions; serialize known shared-file
work with `after:`.

## Install the fixed-purpose wake helper

A 30-minute LaunchAgent needs the Mac awake before each tick. Away mode chains
one-shot `pmset schedule wakeorpoweron` events owned by
`com.ori.wt-away-tick`. This Mac did not activate a narrowly scoped
`/etc/sudoers.d` rule once the administrator timestamp expired. The installer's
original self-test therefore produced a cached-timestamp false positive; it now
calls `sudo -k` before testing so that failure cannot recur.

The selected fallback is an explicit one-time administrator action:

```bash
./scripts/away/install-pmset-helper.sh
```

It installs root-owned files
`/Library/PrivilegedHelperTools/com.ori.wt-away-pmset` and
`/Library/LaunchDaemons/com.ori.wt-away-pmset.plist`. The unprivileged client
writes a mode-`0600`, nonce-bearing request under
`/Library/Application Support/Ori/AwayDispatcher/<uid>/`; the root helper
requires that directory to be mode `0700` and owned by that exact numeric user.
It accepts only these two operations with a strictly parsed timestamp and fixed
owner:

```text
pmset schedule wakeorpoweron <timestamp> com.ori.wt-away-tick
pmset schedule cancel wakeorpoweron <timestamp> com.ori.wt-away-tick
```

The helper cannot select an executable, pmset action, event type, or owner. It
does not permit `cancelall`, repeating events, sleep, shutdown, or power-setting
changes. The installer removes the inactive sudoers drop-in and invalidates its
own sudo timestamp before proving the complete unprivileged
schedule/read-back/exact-cancel protocol. Installed verification on 2026-08-28
confirmed the helper and plist are `root:wheel` modes `0755`/`0644`, the request
directory is owner-only mode `0700`, the system daemon is loaded, the stale
sudoers file is absent, and the unprivileged mutation self-test passes.

After disarming, remove the helper if it is no longer wanted:

```bash
./scripts/away/install-pmset-helper.sh --remove
```

## Launch schedule and wake ownership

`wt away arm` renders and bootstraps
`~/Library/LaunchAgents/com.ori.wt-away-tick.plist`. Its installed
`ProgramArguments` contain `/bin/zsh` and the absolute dev-worktree path to
`scripts/away-tick.sh`; `StartInterval` is 1800 seconds. Standard output/error
logs are under the arming shell's `$TMPDIR`.

The dispatcher stores the exact pending event in the gitignored
`tasks/.away-wake.json` before calling `pmset`, then reads `pmset -g sched` back
and requires exactly one matching type, second, and owner. A tick cancels only
that tracked event before scheduling its successor. If state and `pmset`
disagree, cleanup fails closed; it never uses an owner-wide cancellation and
never touches Apple or third-party wake events.

`wt away disarm` removes the authorization flag first, unloads/removes only the
Away LaunchAgent, releases the scoped idle-sleep assertion, and exactly cancels
its tracked wake. Already-running agents continue.

## Scoped active-work caffeinate assertion

Scheduled wakes make a tick run, but do not keep the Mac awake while a newly
dispatched agent works. The dispatcher therefore manages a separate
`com.ori.wt-away-caffeinate` user LaunchAgent whose only command is:

```text
/usr/bin/caffeinate -i
```

The `-i` flag prevents idle **system** sleep without forcing the display or disk
to remain awake. A successful dispatch acquires it immediately. Later ticks
retain it only while Herdr reports a dispatcher-started worktree as `working`;
if Herdr is temporarily unreadable, a known active dispatch conservatively
retains the assertion. It is unloaded and its plist removed when no dispatched
agent is working, on `wt away disarm`, or whenever a disarmed tick reconciles
stale state. `wt away status` reports whether the assertion is loaded.

The assertion is intentionally not held for the entire armed period: an idle
queue may sleep and rely on the exact chained wake. Keep the Mac connected to
AC power while away. A closed laptop lid may still force sleep depending on the
Mac's clamshell/power configuration; caffeinate does not override hardware lid
sleep policy.

## Operate and inspect

```zsh
source scripts/wt.sh
wt away arm
wt away status
wt away tick       # safe manual cycle
wt away disarm
```

`status` shows armed state, LaunchAgent/wake state, scoped caffeinate assertion,
active dispatcher-started branches with parent-group progress, every queue
verdict, and the last ledger entry. Every tick appends one compact JSON object to
`tasks/away-ledger.jsonl`, including disarmed no-ops. A slug that has ever been
recorded as dispatched is never dispatched again, even if its branch later
disappears.

At most three ledger-dispatched, branch-present, not-merged agents count as
active. At capacity, the tick records `at-capacity` and starts nothing.

While armed, the first successful tick on each local calendar day sends one
Telegram digest containing that day's dispatches, non-draft open PRs on those
branches, blocked Herdr agents, and the current standing skips. Delivery uses
the private Telegram Bot API credentials installed by herdr-remote, but only
after confirming that the local relay is reachable. An unreachable relay or
Telegram API does not fail the tick: the structured digest is recorded in that
tick's `notifications` ledger field and retried later that day.

When no dispatcher-started agent remains active, a transition to an exhausted
queue or to all remaining entries being blocked by dependencies/overlap sends
one immediate alert. `tasks/.away-notify-state.json` records successful daily
and transition delivery so a standing condition is not repeated every 30
minutes. The file is owner-only and contains no credentials.

## herdr-remote: Telegram-only away surface

herdr-remote is community software with authority to read agent output and send
agent input. Do not arm until the reviewed pin, token-protected loopback relay,
bundled local event plugin, and private-account Telegram service pass the gate
below. Away mode does not expose a public relay endpoint.

Security review on 2026-08-28 examined herdr-remote release `v0.7.5`, pinned
to commit `ea5a8e2a9820e84d0ca27278b46cbb6e33045916` (the annotated tag is not
cryptographically signed), and herdr-push commit
`f4fdb06d5413ac2d96ca225ea33f288f41bfc001` (that repository has no release).
The relay, Telegram bot, installers, event hooks, and related tests were read;
68 relay tests, 1 Telegram integration test, 41 Telegram dashboard tests, and
3 herdr-push tests passed from the pinned sources.

The unmodified pins are **not approved for public relay exposure**:

- With `HERDR_RELAY_TOKEN` set, `trusted_websocket_origin()` accepts every
  origin, so `HERDR_TRUSTED_ORIGINS` is not an enforced second boundary.
- `install-service.sh` does not persist `HERDR_TRUSTED_ORIGINS` into its
  generated service configuration.
- herdr-push constructs `/push?d=...` by concatenation and cannot correctly
  preserve a relay `?token=...` query, so the reviewed plugin and mandatory
  token configuration are incompatible without a bounded patch.

The owner chose a Telegram-only loopback deployment after this review. The
reviewed source is installed at `~/.local/share/herdr-remote/v0.7.5` and its
HEAD must remain the exact v0.7.5 commit above with a clean worktree. The
`herdr-remote.relay` bundled UDP event plugin is linked from that same pinned
checkout. The relay must bind only to `127.0.0.1:8375` and retain token
authentication. The Telegram bot reaches Telegram through its outbound Bot API
connection, so no Cloudflare tunnel, public WebSocket endpoint, or separate
herdr-push plugin is installed. This removes the unsafe origin-path and
token-URL combinations rather than patching third-party code.

Install from the reviewed pin with the checked-in wrapper:

```bash
./scripts/away/install-herdr-telegram.sh
```

The wrapper verifies or creates the exact pinned checkout, links the bundled
event plugin, accepts only a recent direct `private` bot conversation, and
delegates service creation to the pinned upstream installer. The BotFather
token is entered only at the wrapper's hidden prompt. The wrapper supplies the
upstream choices itself and forces cloudflared installation/configuration to
**no**, so no Cloudflare prompt is shown. On any post-install pin, permission,
service, private-chat, listener, or no-tunnel failure, it removes the user
services and preserves configuration for diagnosis.

`wt away arm` repeats the non-secret verification and refuses to arm unless:

- source HEAD is the reviewed commit and its checkout is clean;
- `config.env` is owner-controlled and `secrets.env` is mode `0600`;
- relay and Telegram LaunchAgents are loaded, with no tunnel LaunchAgent;
- the configured relay is token-protected `127.0.0.1:8375` and `lsof` reports
  no listener on another address;
- the bundled plugin is linked from the reviewed checkout; and
- Telegram is enabled for one positive-ID `private` chat.

Installed verification on 2026-08-28:

- LaunchAgents `com.herdr-remote.relay` and `com.herdr-remote.telegram` are
  running; `com.herdr-remote.tunnel` is absent.
- `lsof` reports TCP `127.0.0.1:8375` and plugin UDP `127.0.0.1:8376`, with no
  non-loopback listener.
- `config.env` is owner-controlled mode `0644`; `secrets.env` is owner-only
  mode `0600`; relay token and private positive chat ID checks pass.
- Telegram bot `@ori_away_bot` connected to the relay. The upstream
  three-second smoke test raced first-time environment startup and returned
  early even though both services became ready; the wrapper now applies a
  bounded 15-second safety-gate wait before deciding setup failed.

The off-network phone verification passed on 2026-08-28: `/status` reported a
connected relay; `/agents` listed the live agents; `/read away-dispatcher`
returned the exact pane output; `/reply away-dispatcher` delivered a harmless
ACK prompt to that exact agent; and Telegram sent its subsequent `finished`
notification. The web dashboard and menu bar are not part of v1.

## Return ritual

1. `wt away disarm`.
2. Review `tasks/away-ledger.jsonl` and `wt away status`.
3. Review and manually merge PRs to `dev` in dependency order.
4. Run `wt done <slug>` for each merged worktree from an interactive shell.
5. Optionally remove the fixed-purpose wake helper.
