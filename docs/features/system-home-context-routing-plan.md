# System Home Context Routing Plan

## Overview

The home surface should evolve from **agent-first routing** into **context-first routing**.

Today, the home flow primarily asks:

> Which agent should answer this?

The intended harness model for home should instead ask:

1. Does this request need durable workspace context?
2. If yes, which workspace is the best fit?
3. If there is no clear fit, should the user choose or create one?
4. Once the workspace is known, what execution path should happen inside it?

This keeps responsibilities clean:

- **Home harness:** find the right context.
- **Workspace harness:** execute inside that context.

The workspace run model is already aligned with this direction. Workspaces own the durable context that matters for real work: notes, files, tasks, sessions, tools, artifacts, and history. The missing layer is cross-workspace selection before execution starts.

## Current Situation

### What already exists

- Home routing already classifies prompts and can identify broad route modes such as utility requests, specialist handoffs, and workspace-oriented prompts.
- Workspace runs already provide the durable execution harness once a workspace is known.
- Workspaces already store useful intent metadata:
  - workspace name
  - description
  - `workspace_bootstrap.goal`
  - `workspace_bootstrap.systems`
  - `workspace_bootstrap.capabilities`
  - `workspace_bootstrap.context`
  - canonical `Workspace Description` note
- Workspace entry agents already provide the natural handoff point once the right workspace is selected.

### Main gap

The home flow is still mostly **agent-first**:

1. classify the prompt
2. find a matching agent
3. sometimes recommend creating a workspace for complex work

There is no first-class stage that asks:

> Which existing workspace should own this request?

That means home can still route into generic chat when the stronger product behavior would be to move the user into the right workspace first.

## Target Architecture

### Intake sequence

The home harness should follow this sequence:

1. **Classify context mode**
   - `direct`
   - `workspace`
   - `scratch`
2. **Resolve workspace when needed**
   - confident existing workspace match
   - ambiguous workspace candidates
   - no suitable workspace
3. **Choose execution path**
   - selected workspace entry agent / manager
   - specialist flow when the request truly belongs outside workspace context
   - direct tool or assistant response when no specialist is needed
   - workspace creation path when no workspace fits
4. **Delegate execution**
   - once the workspace is known, continue through the existing workspace harness instead of inventing a second execution layer at home

### Orthogonal routing decisions

Context selection and execution choice are separate decisions:

| Decision | Example values | Question answered |
| --- | --- | --- |
| `context_mode` | `direct`, `workspace`, `scratch` | Where should this request live? |
| `handoff_policy` | `assistant`, `specialist`, `tool` | Who or what should handle it after context is known? |

The current implementation already exposes route modes such as `utility_direct`, `workspace_task`, and `specialist_handoff`. During migration, those values can remain as compatibility fields, but the new design should avoid treating "workspace" and "specialist" as mutually exclusive outcomes. A request may belong in a workspace and still require a specialist inside that workspace.

### Responsibility split

| Layer | Responsibility |
| --- | --- |
| Home harness | Detect whether context is needed and choose the best workspace |
| Workspace manager / entry agent | Decide what should happen inside the workspace |
| Workspace run harness | Execute, validate, and record the work |

## Proposed API Shape

Extend the home route response with orthogonal routing fields plus a `workspace_resolution` object whenever a request is workspace-oriented.

```json
{
  "route_mode": "workspace_task",
  "context_mode": "workspace",
  "handoff_policy": "assistant",
  "workspace_resolution": {
    "state": "confident",
    "selected_workspace_id": "ws-launch",
    "selected_workspace_name": "Launch Ops",
    "confidence": 0.91,
    "reasons": [
      "matched workspace goal",
      "matched known systems"
    ],
    "candidates": [
      {
        "id": "ws-launch",
        "name": "Launch Ops",
        "score": 0.91
      },
      {
        "id": "ws-marketing",
        "name": "Marketing Site",
        "score": 0.42
      }
    ]
  }
}
```

Recommended `workspace_resolution.state` values:

- `not_needed`
- `confident`
- `ambiguous`
- `no_fit`
- `needs_repair`

Recommended routing precedence:

1. If the request already carries a valid `context.workspace_id`, keep that workspace by default.
2. Only run global workspace resolution when there is no active workspace, or when the user explicitly asks to switch context.
3. Utility/direct requests bypass workspace resolution entirely.

## Workspace Resolution Strategy

### Start deterministic

The first version should be explainable and deterministic rather than embedding-driven.

Use these signals:

1. Explicit workspace name mention in the prompt.
2. Workspace name overlap.
3. Workspace description overlap.
4. `workspace_bootstrap.goal` overlap.
5. `workspace_bootstrap.systems` overlap.
6. `workspace_bootstrap.capabilities` overlap.
7. `workspace_bootstrap.context` overlap.
8. Canonical `Workspace Description` note overlap.
9. Recency as a weak tiebreaker only.

### Candidate retrieval

The current global workspace index is sufficient for identity lookup, but not for semantic routing because it only stores lightweight fields such as workspace id, name, parent id, folder path, and update time.

Recommended v1 retrieval strategy:

1. Build a bounded candidate set from direct-use workspaces that are currently eligible for routing.
2. Score lightweight metadata first:
   - name
   - description
   - bootstrap fields
3. Only inspect richer note content for the strongest candidates when needed to separate close matches.

This gives v1 a simple path without committing to embeddings or a large cross-workspace scan forever. If workspace counts become large, move the resolver onto a dedicated projection or richer index rather than overloading the current global workspace index.

### Why deterministic first

- The repo already has structured workspace metadata.
- Explainable routing is important for user trust and debugging.
- A deterministic baseline will reveal whether the metadata quality is good enough before adding embeddings or LLM ranking.
- The emitted reasons can later become training and evaluation data.

### Feedback loop

Persisted intake traces can also improve later routing without introducing a black-box ranker. The first feedback pass should stay narrow:

1. Reuse an exact prior user correction for the same prompt when the corrected workspace is still routable.
2. Keep the correction visible in the recorded reasons so the decision remains explainable.
3. Reuse similar historical corrections only under strict guardrails:
   - enough signal tokens are present
   - prompt overlap is high
   - one corrected workspace is the clear winner
   - weaker matches need repeated supporting history
4. Keep broader semantic or embedding-based reuse deferred until trace volume is large enough to evaluate it safely.

### What not to use as the main selector

- newest workspace
- workspace that happens to contain the matched agent
- auto-created scratch workspace

Those can remain fallback behaviors in narrow cases, but they are poor primary signals for choosing the right durable context.

### Workspace eligibility and readiness

Not every workspace is a valid routing target.

Before a candidate can be selected, the resolver should verify:

1. The workspace is a direct-use workspace, not an organizational group.
2. The workspace is eligible for routing under the chosen product policy:
   - default recommendation: route only to active workspaces in v1
   - if archived or inactive workspaces are ever included, make that explicit in the UI
3. The workspace has a valid entry agent / manager available for handoff.

If the best workspace match is not ready to accept the request, return a repair state instead of silently falling back to global chat. Example cases:

- missing entry agent
- referenced entry agent no longer exists
- workspace requires repair before routing can proceed

## Home UX States

### Confident match

Example:

> This belongs in `Launch Ops`. I will continue there.

Actions:

- Continue into selected workspace
- Always expose a lightweight "Choose another workspace" escape hatch

### Ambiguous match

Example:

> I found a few plausible workspaces. Which one should I use?

Actions:

- show top 2-3 workspace options
- allow opening workspace details if needed
- record user override

### No fit

Example:

> I could not find an existing workspace that clearly fits this request.

Actions:

- Create workspace
- Continue without workspace only when that is truly acceptable

### Needs repair

Example:

> `Launch Ops` looks like the right workspace, but it needs repair before I can continue there.

Actions:

- Open workspace repair flow
- Show why handoff is blocked
- Do not silently fall back to unrelated global chat

### Utility request

Example:

> What time is it in Tokyo?

Behavior:

- bypass workspace resolution entirely
- keep the existing fast path

## Handoff Behavior

Once a workspace is selected:

1. Open or target the workspace context.
2. Use the workspace entry agent / manager as the first actor.
3. Let the workspace-side harness decide whether the request becomes:
   - a planning step
   - a task
   - a specialist handoff
   - a direct response inside workspace context

The home layer should not become a second workspace orchestrator. Its job ends once the request is in the right durable context.

If the user is already inside a valid workspace, the home harness should treat that as the default context rather than re-running global selection. Explicit user requests to switch workspaces are the exception.

## Delivery Plan

### Phase 1: Add workspace resolution to the backend

- Add a dedicated workspace resolver service.
- Add orthogonal `context_mode` and `handoff_policy` fields while preserving existing `route_mode` behavior during migration.
- Score candidate workspaces from existing structured metadata.
- Apply routing precedence so a valid existing workspace context wins by default.
- Filter candidates to eligible direct-use workspaces.
- Return repair state when the best match cannot accept a handoff.
- Extend `/api/home-assistant/route` with `workspace_resolution`.
- Add tests for:
  - confident match
  - ambiguous match
  - no fit
  - utility bypass
  - active workspace precedence
  - ineligible group exclusion
  - missing entry-agent repair state

### Phase 2: Change home routing order

- For workspace-oriented requests:
  1. classify context mode
  2. resolve workspace
  3. resolve handoff policy
  4. resolve workspace handoff target
- Keep generic agent matching for requests that are not workspace-scoped.
- Avoid falling directly into generic chat when a confident workspace match exists.

### Phase 3: Update home UI

- Render workspace routing outcomes in the thinking modal.
- Add UX for:
  - confident workspace handoff
  - ambiguous candidate selection
  - no-fit workspace creation
  - repair-required workspace handoff
- Keep a visible correction path for every confident auto-selection.
- Route confirmed requests through the workspace assistant path, not generic chat.

### Phase 4: Add no-fit workspace creation

- Seed workspace creation from the original prompt.
- Pre-fill the existing workspace bootstrap fields where possible.
- After creation, continue into the new workspace instead of ending the flow at creation.

### Phase 5: Add observability and evaluation

- Persist a lightweight home intake trace containing:
  - prompt
  - route mode
  - workspace candidates
  - selected workspace
  - confidence
  - reasons
  - user override, if any
  - final handoff target
- Build a small fixture set of realistic prompts and expected routing outcomes.
- Track:
  - confident match accuracy
  - ambiguity rate
  - override rate
  - wrong-workspace correction rate

## Recommended First Implementation Slice

The smallest useful vertical slice is:

1. Backend workspace resolver.
2. Extended route response.
3. Orthogonal `context_mode` and `handoff_policy` fields.
4. Deterministic candidate scoring with reasons.
5. Candidate eligibility checks and repair detection.
6. Tests for resolver behavior.
7. A minimal home UI that can:
   - auto-handoff on a confident match
   - ask the user to choose when ambiguous
   - offer workspace creation when there is no fit
   - surface repair when the chosen workspace is not ready

This slice is enough to validate whether the new intake model feels right before adding more advanced ranking or analytics.

## Non-Goals For The First Pass

- embeddings or vector search
- LLM-only workspace selection
- automatic cross-workspace summarization
- auto-creating workspaces without user confirmation
- replacing the workspace harness with a second execution layer at home

## Acceptance Criteria

1. From home, a workspace-scoped prompt can land in an existing relevant workspace without the user manually navigating there first.
2. If two or more workspaces are plausible, Ori asks instead of guessing.
3. If no workspace fits, Ori proposes creating one with useful seeded metadata.
4. Utility prompts still bypass workspace routing.
5. Once a workspace is selected, the request continues through the workspace manager / workspace harness path rather than a global chat detour.
6. Every routing choice is explainable from recorded evidence.
7. A valid current workspace is preserved by default unless the user explicitly asks to switch.
8. A confident auto-selection always includes a visible correction path.
9. Ineligible or broken workspace targets are surfaced as repair states rather than silently bypassed.

## Validation Notes

- Intake traces are persisted in SQLite so routing quality can be evaluated after the request finishes. A later UI can surface those metrics without changing the trace contract again.
- Exact prior workspace corrections are reused as a deterministic feedback signal for repeated prompts; fuzzy learning is intentionally deferred until there is enough trace data to evaluate it safely.
- The deterministic resolver is only as good as workspace metadata quality. Thin or stale descriptions will still create ambiguous or no-fit outcomes until the metadata itself improves.
- Frontend assets are embedded into the Go server binary. During local development, JavaScript changes require restarting the running server before browser verification reflects them.

## Key Design Principle

Selecting a workspace should not mean immediately running the task.

It should mean:

> enter the right context, then let the workspace manager decide the next step.

That preserves the value of the workspace harness instead of making the home surface responsible for orchestration it should not own.
