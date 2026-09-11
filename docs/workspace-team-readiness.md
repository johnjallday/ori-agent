# Workspace Team Readiness

This document defines the Create Workspace staffing contract. It applies to the
interactive four-step wizard only when that wizard sends an explicit versioned
team intent. Existing workspaces and callers that omit the intent keep their
existing behavior.

## Scope and terminology

A **role** is a blueprint-declared slot. A role is filled only by a verified
current group binding or by one explicit `role_staffing` entry. A saved agent
shown as a suggestion, an unrelated extra teammate, and a blueprint proposal do
not fill a role.

**Team staffed** means every required role has a verified holder. It does not
mean the workspace is operationally ready. Model validation, plugin lifecycle,
folder grants, setup wizards, watchers, schedules, and automation approvals keep
their existing owners and status language.

This is an intentional creation-only departure from the earlier
`workspace-role-vacancies` policy: a strict wizard request cannot create a real
blueprint workspace while a required role is empty. Existing workspaces may
still clear and refill roles through the current warnings and per-role
endpoints. There is no migration or automatic restaffing.

## Authoritative declarations and identity

The server, not the browser, derives requiredness and role identity.

| Blueprint source | Role identity | Requiredness | Primary |
|---|---|---|---|
| Ordinary template `agents` | `projecttemplates.AgentRoleIDs`, derived from the normalized declared name with deterministic collision suffixes | First normalized agent only | First normalized agent only |
| Assistant Program | Declaration `roles[].id` | Declaration `roles[].required` | Declaration `roles[].primary` within its scope |
| Blank | Synthetic ordinary roster containing `Ask Ori`; stable role ID `ask-ori` | Required | Primary |

Ordinary role IDs identify the declared slot, not the eventual holder. A
role-specific Create choice may give its new agent another name without changing
the role ID. The older whole-roster `template_agent_overrides` path can rename a
template entry and therefore changes name-derived identity; a strict
`role_staffing` request does not combine that path with role fills. Assistant
Program IDs are declaration-owned and remain stable when labels change.

A plan revision binds the reviewed role declarations and create/reuse facts.
Reordering an ordinary roster preserves its name-derived IDs, while removing or
renaming a declaration changes the current plan and requires another review.

## Versioned wizard request

The wizard sends this object on every non-import create:

```json
{
  "team_intent": {
    "version": 1,
    "mode": "staffed",
    "plan_revision": "<revision returned by template-agent-plan>"
  },
  "role_staffing": []
}
```

`mode` has exactly two values:

- `staffed` — every authoritative required role must be satisfied. Optional
  roles may be absent. `role_staffing` is mandatory even when it is an empty
  array. A current inherited group binding may satisfy only its matching
  group-scoped role.
- `agentless` — an explicit Blank-only choice. `role_staffing` must be an empty
  array and no entry agent, saved extra, template override, or team seeding
  request may be combined with it.

Field handling is fail-closed:

| Input | Meaning |
|---|---|
| `team_intent` absent | Legacy compatibility path; not proof that the caller reviewed staffing |
| `team_intent: null`, a non-object, `{}`, unknown key, unknown version, unknown mode, or blank revision | `400` structured `team_intent_invalid` conflict |
| Strict intent with `role_staffing` absent or `null` | `400` structured conflict; the reviewed staffing data is missing |
| Strict `staffed` with malformed, duplicate, unknown, stale, or insufficient fills | `409` structured `team_readiness` conflict |
| Strict `agentless` with a real `template_id`/`template_path` or any staffing choice | `400` structured conflict |

The server derives roles from the current resolved blueprint and verifies the
current plan revision. Client-supplied role IDs select slots, but no
client-supplied required/primary flags or recommendation score is accepted.
Malformed strict intent never falls back to legacy behavior.

A readiness conflict includes stable role IDs, labels, scope/ownership,
actionable reasons, required progress, and a fresh plan when the declarations
changed. The wizard keeps unrelated valid edits, refreshes saved identities,
and focuses the first affected role.

## Validation and side-effect ordering

Strict validation occurs before any new workspace, agent definition, workspace
folder, folder grant, plugin operation, watcher, or automation write:

1. Decode and validate `team_intent` and require explicit staffing data.
2. Resolve the current authoritative template (or the synthetic Blank roster),
   selected group composition, and plan revision.
3. Normalize role fills; reject unknown roles, duplicate holders, invalid modes,
   setup fields on Assign, and a role already owned by another scope.
4. Verify every assigned saved definition still exists and is attachable. Verify
   inherited group bindings against the current station binding, instance,
   workspace snapshot, and saved definition.
5. Reject Create-name collisions and ensure every required role is either a
   verified inherited binding or a valid staged fill.
6. Preserve the existing independent group-placement review/claim boundary.
7. Seed ordinary/Blank fills before workspace persistence, retaining the current
   rollback rule: only definitions created by this request can be removed.
8. Persist the workspace and folder/provenance, establish an Assistant Program
   project link where applicable, then revalidate and commit assistant scopes
   through the existing Review/Commit adapter.

Blueprint dependency readiness, group placement, staffing, folder provisioning,
and activation remain separate decisions. A staffing assignment never grants a
folder, enables a watcher, installs a plugin, changes a saved prompt/model/tools,
or approves automation.

## Group-owned roles

A group-scoped role belongs to the canonical group workspace. A project may
show a verified inherited holder read-only, but it does not include that holder
in project-local `role_staffing`, attach the holder again, clear the binding, or
use it to satisfy a project-scoped primary role.

The non-circular recovery path is:

1. The passive template-agent plan projects the exact existing Home or a
   proposed, not-yet-created Home on Details. It reports required Home-role
   progress only from current binding, instance, workspace snapshot, and saved
   definition evidence.
2. If no canonical group exists and policy allows `offer_create`, Details offers
   an inert Home-only review. A separate explicit commit creates or reuses only
   the canonical group; it does not create the project or either team. Required
   `existing_only` never receives this action.
3. Refresh the plan and exact group roster. If a required group role is empty,
   **Set Up <role>** suspends the workspace creator for a bounded live Home-role
   action. The canonical agent form may Create, or the user may explicitly
   Assign one saved definition. `GET /api/workspaces/{homeID}/roles` verifies
   the editable target and one role-specific `PUT` commits immediately. If old
   storage still names a holder whose saved definition was deleted, the role is
   empty with `needs_clear: true`; setup exposes only an explicit role-specific
   `DELETE` until a fresh roster proves the stale assignment is gone.
4. Resume Details with the project draft intact and re-read canonical state.
   Team remains blocked until the holder is verified; no clicked button or
   submitted name is treated as success.
5. Project placement receives its own fresh receipt and confirmation.

The live staffing adapter derives authority from the route target. A genuine
Assistant Program station can mutate only declaration-owned Home roles without
requiring a child link. A linked child can mutate only project roles; passing a
Home role ID through a child or a project role ID through a station fails before
creating an agent. Clear uses the same target rule. Create-name collisions,
missing assigned definitions, malformed holders, and binding revision changes
fail closed.

Home creation and Home staffing survive creator cancellation because they were
separately confirmed durable consequences. Modal suspension itself is not
cancellation: workspace name/details, tags/colour, source/composition, valid
project fills, connection selection, and Map intent remain in memory. Neither
operation grants runtime readiness or project access.

## Blank

Blank's template-agent-plan is a synthetic, host-owned one-role roster for
`Ask Ori`. The normal `staffed` path must persist exactly what Team showed:
Create makes the chosen definition from the synthetic proposal, Assign attaches
the selected saved definition unchanged, and the holder becomes the entry agent.
New strict Blank workspaces retain synthetic provenance so an empty role remains
visible and fillable after creation.

`agentless` is a separate Blank-only selection labelled **Create without
agents**. Review states that chat and agent work require adding an agent later.
It creates or attaches zero definitions and suppresses fallback manager seeding.
An empty roster alone never implies this intent. Selecting a role fill or saved
agent returns the draft to `staffed`; switching to any real blueprint clears
`agentless`; reopening starts from `staffed`. Legacy Blank requests that omit
`team_intent` retain their current default of seeding/reusing `Ask Ori`.

## Assistant Program completion and recovery

Assistant Program scopes remain separately revisioned. The callback is required
for a strict request that needs local assistant staffing; an unavailable
callback fails before workspace persistence. Structural checks, current saved
agent checks, name collisions, current group bindings, and model selection can
be checked without writes before creation. The adapter still reviews and
revalidates each scope immediately before its commit.

Once an Assistant Program workspace and link are durable, scope commits are not
pretended to be one cross-store transaction. If a later scope fails, earlier
successful bindings and the workspace remain. The create response keeps the
workspace ID and reports observed state:

```json
{
  "success": true,
  "folder": { "id": "...", "folder_slug": "..." },
  "team_completion": {
    "state": "incomplete",
    "workspace_id": "...",
    "team_staffed": false,
    "applied_role_ids": ["..."],
    "missing_required_roles": [{ "role_id": "...", "label": "..." }],
    "requested_unapplied_role_ids": ["..."],
    "action": { "label": "Complete team setup", "href": "/workspaces/<slug>" }
  }
}
```

`state` is `complete` only when all requested fills are observed and all
required roles are verified, `incomplete` when durable observed state proves a
missing requested/required role, and `unknown` when the durable workspace exists
but its roster cannot be read back. The browser latches the workspace ID, opens
that existing workspace for recovery, and never replays workspace creation.
The live Roles roster derives persistent required progress from current
bindings, so filling the remaining role clears the guidance naturally. It never
summarizes success from the submitted payload alone.

Failures before workspace persistence keep the existing rollback ownership:
new request-owned definitions may be compensated; saved/reused definitions are
never edited or deleted. Folder/plugin/automation failures remain their own
warnings and do not rewrite staffing truth.

## Compatibility matrix

| Caller or state | Contract |
|---|---|
| Four-step Create Workspace wizard | Sends strict `team_intent` and explicit `role_staffing`; required roles block Team → Review and submit |
| Legacy POST with intent absent and `role_staffing` absent | Historical auto-seeding/entry behavior |
| Legacy POST with intent absent and `role_staffing` present (including empty) | Historical vacancy semantics retained as a compatibility exception |
| `CreateFromTemplate` and Personal HQ coordinator | Intent absent; historical template behavior retained |
| Import | No new staffing gate; imported records keep the import contract |
| Existing workspace Roles roster | Fill/clear remains available, including current clear warnings |
| Existing workspace created before this feature | No migration or automatic restaffing |
| Real blueprint plus `agentless` or a general team opt-out | Rejected; choose Blank for intentional agentless creation |
| Blank, intent absent | Historical default Ask Ori seeding |
| Blank, strict `staffed` | Required synthetic role must be explicitly Created or Assigned |
| Blank, strict `agentless` | Zero agents and a persistent fillable synthetic role |

Legacy absence is compatibility, not authorization: new interactive callers
must not omit the object to bypass readiness.

## Acceptance summary

- A saved `Downloads Curator` is a suggestion until its role-specific action is
  clicked. The Downloads Janitor's first and only ordinary role is required and
  primary; assignment reuses the saved definition unchanged.
- Suggestions use deterministic name/role-label matching, explain only that
  match, exclude unattachable/occupied definitions, and never call a model.
- Optional vacancies and extra teammates do not block; extras do not satisfy
  declared roles.
- Deleted/stale saved definitions, changed plan revisions, duplicate holders,
  unknown roles, and malformed strict intent fail closed before creation.
- Valid group bindings satisfy only their declared group role. Missing bindings
  use the group-owned preparation/roster path.
- Partial assistant scope completion preserves one workspace and actual
  bindings, then routes to persistent per-role recovery.
- Staffing copy says `team staffed`, never `fully ready`.

## Group-first creator evidence

The group-first implementation started from Ori feature HEAD and `origin/dev`
`aa76b6e9ce3a569bc5514b71032a86ec7587ab4b`. A disposable, non-executable
workspace-group plugin fixture now exercises the generic contract through the
real server and browser. The clean first-time path proves passive Blueprint and
Details reads are inert; explicit Home commit creates exactly one empty group;
Home-role Create targets only that group; modal suspension preserves a staged
project fill; Team shows separate group/project leadership and required
progress; and final confirmation creates one exact linked child with only its
project role. Crafted inverse-scope role IDs are rejected without agent or
workspace mutation.

This host evidence does not prove the companion release. The requested REAPER
plugin 0.5.2 candidate is a separate repository deliverable based on canonical
v0.5.1 commit `972c33fb50b813c73beaafd779c71ced266477fa`.
No authorized 0.5.2 source, tag, release, branch, or artifact was available when
this host slice was implemented. Historical 0.6.0 candidate bytes are evidence
only and must not be presented as 0.5.2 acceptance. No live provider, personal
folder, or REAPER command is implied by team-staffed status.
