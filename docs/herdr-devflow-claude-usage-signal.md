# Claude usage-signal contract for Overnight Runs

An Overnight Run may put the whole Mac to sleep. It may only do that on evidence
strong enough to be worth that consequence: a *structured* statement that this
exact Claude session exhausted its **included five-hour window**, together with
an **absolute reset timestamp Claude itself reported**. Terminal text, a generic
`blocked` status, and "detection time plus five hours" are all explicitly
unacceptable — see FR62–FR68 in
`tasks/prd-herdr-overnight-agent-completion.md`.

This document records what the installed, supported interfaces actually provide,
as verified on **Claude Code 2.1.220** and **Herdr 0.7.5 (protocol 17, schema
version 1)**. It is the answer to the PRD's open questions 1 and 2.

## What is not available

Checked and ruled out, so nobody re-derives them later:

- **Herdr's socket API carries no usage vocabulary.** Its 89 request methods and
  25 event types contain no usage, limit, reset, quota, billing, or credit
  field. `AgentInfo.agent_status` is `idle | working | blocked | done | unknown`,
  and `blocked` says nothing about *why*. `state_labels` is a free-form string
  map with no defined key for a limit, and `terminal_title` is raw terminal text
  that FR64 rules out.
- **Herdr's Claude integration reports session identity only.** The installed
  hook (`~/.claude/hooks/herdr-agent-state.sh`, integration version 7) fires on
  `SessionStart` and calls `pane.report_agent_session` with the native session id
  and transcript path. It carries no usage data.
- **Claude Code has no usage query.** There is no `claude usage --json`, no
  usage hook event, and nothing written under `~/.claude/` that records the
  window state. `~/.claude/sessions/<pid>.json` carries pid, `sessionId`, `cwd`,
  `status`, and `version` — liveness and identity, not usage. Anthropic closed
  the request to expose usage this way as not planned
  ([claude-code#38380](https://github.com/anthropics/claude-code/issues/38380)).
- **Transcripts do not record the limit.** `~/.claude/projects/*/*.jsonl` system
  entries observed in practice are `turn_duration`, `away_summary`, and
  `local_command` only.

## What is available

Two documented, structured, local interfaces cover the requirement between them.

### 1. `StopFailure` hook, matcher `rate_limit` — *the turn stopped, and why*

Claude Code fires the `StopFailure` hook when a turn ends because of an API
error, and the matcher filters on the error class. The supported classes are:

```
rate_limit  overloaded  authentication_failed  oauth_org_not_allowed
billing_error  invalid_request  model_not_found  server_error
max_output_tokens  unknown
```

Registering the hook with `"matcher": "rate_limit"` means it fires *only* for a
subscription rate limit. The classification FR48 and FR64 require therefore
comes from the matcher itself, not from parsing a payload: a `billing_error`, an
`authentication_failed`, an `overloaded`, or a `max_output_tokens` failure
cannot reach a hook registered this way.

The hook receives the common input fields, including **`session_id`** and
`transcript_path`, which bind the event to the exact native Claude session Herdr
recorded through `pane.report_agent_session`. That is the exact-session binding
FR62 requires.

The event-specific payload fields are not documented. The adapter must therefore
depend on the matcher and the common fields only, and must treat any
event-specific field as absent.

### 2. `statusLine` payload — *which window, and when it resets*

Claude Code invokes the configured `statusLine` command on every render and
passes session JSON on stdin. It includes:

| Field | Meaning |
| --- | --- |
| `rate_limits.five_hour.used_percentage` | 0–100 consumption of the **included five-hour window** |
| `rate_limits.five_hour.resets_at` | **Unix epoch seconds** when that window resets |
| `rate_limits.seven_day.used_percentage` | consumption of the weekly cap |
| `rate_limits.seven_day.resets_at` | epoch seconds when the weekly cap resets |
| `context_window.used_percentage` | context consumption, a different exhaustion |
| `session_id` | the exact native Claude session |
| `version` | the Claude Code version, for the supported-version gate |

`resets_at` is an absolute epoch timestamp reported by Claude. It satisfies FR65
directly and removes any temptation to add five hours to a detection time.

The five-hour window, the seven-day window, and the context window are three
separate fields, so distinguishing an included-session limit from a weekly cap
and from context exhaustion is structural rather than heuristic.

### The plan-backed and no-credit proof

The documentation states that `rate_limits` **appears only for Claude.ai
subscribers (Pro/Max), after the first API response in the session**, and that
each window may be independently absent.

That gives FR66–FR68 a positive test rather than an inference:

- `rate_limits.five_hour` present ⇒ the session is running on an included
  Claude.ai subscription allowance.
- `rate_limits` absent ⇒ API-key/pay-as-you-go, an unsupported version, or a
  session that has not yet made an API call. All three are ineligible, and the
  run stays awake.

No interface offers to spend credits, and none is consulted that could. A
`billing_error` is a distinct `StopFailure` class that never reaches a
`rate_limit`-matched hook.

## The composed signal

An Overnight Run may enter the sleep sequence only when **all** of these hold:

1. A `rate_limit` `StopFailure` was recorded for the exact `session_id` the run
   enrolled.
2. The most recent statusLine record for that same `session_id` reports
   `rate_limits.five_hour` present and exhausted.
3. That record is fresh enough to describe the current window.
4. `five_hour.resets_at` is in the future and strictly newer than the last reset
   this participant already consumed.
5. Herdr independently reports that exact session as not working.

Any missing, stale, malformed, or mismatched element leaves the run awake in a
visible degraded state. Weekly exhaustion, context exhaustion, a billing error,
an authentication failure, and an absent `rate_limits` object all stop
unattended continuation instead of scheduling a wake.

## Delivery constraint, and why installation must be explicit

Both interfaces are *push*: Claude Code invokes them, and neither can be polled.
Ori therefore cannot read usage on demand — it can only read what a Claude-side
hook and statusLine script have already persisted to a user-local file.

Installing those requires writing to the user's Claude configuration. FR119–FR121
and the setup requirements forbid doing that implicitly, so:

- Setup and doctor **discover** whether the Ori usage recorder is installed and
  report readiness. Neither installs it as a side effect.
- Installation is an explicit user action, and it must **compose with**, not
  replace, an existing `statusLine` command — a user who already has one must
  keep its output.
- Until the recorder is installed and has observed a window, Overnight readiness
  fails closed and the affected agents stay ineligible with a plain reason.

## Version gate

The contract above was verified against Claude Code 2.1.220 and Herdr 0.7.5.
The recorder writes the observed Claude Code `version` alongside every sample,
and the adapter refuses samples from a version it does not recognize as
supported rather than assuming the payload shape held.
