# Group Templates v1

Status: implemented host feature (domain-neutral). Combined declarations remain
supported unchanged. Independently contributed Homes are locally accepted with the
unpublished Music Project Management 0.1.0 and REAPER 0.8.0 candidates described in
[Limitations](#limitations).

## Purpose

**Create Group** offers Group Templates alongside the ordinary General group. A
Group Template creates, or reuses, one exact Assistant Program Home. The Home may
come from a legacy combined project blueprint or directly from a trusted plugin's
`assistant_program_homes` declaration. A separate project plugin can then attach
its project-local team through `assistant_project`.

Group Templates are host-side projections over declarations the host already
trusts ([Template Group Requirements](template-group-requirements.md)); users do
not author a second Group Template format. The independent contribution forms
require `independent_program_homes_v1`. Existing combined forms retain their prior
feature requirements and meaning.

## Group Templates versus Workspace templates

| | Workspace (project) template | Group Template |
| --- | --- | --- |
| Chosen in | Create Workspace → Blueprint | Create Group → Blueprint |
| Creates | a project workspace, its team, optional project files | exactly one group (program Home); the wizard then staffs the Home roles the user chose |
| Source | library, plugin blueprint, source-linked variant | projected from an eligible combined blueprint or a plugin-level independent Home declaration; never edited |
| Team | reviewed project roster | none in the commit; the wizard staffs chosen Home roles immediately afterward through `PUT /api/workspaces/{id}/roles/{roleID}` |

A source blueprint's `agents[]` roster and `assistant_program.roles[]` may still
carry a `type` key from before the agent type was retired. The key is accepted
and ignored.

**General** is the ordinary group. It keeps its reviewed Group Manager roster
(Blueprint → Details → Group Roster → Review) and never joins a program. A
managed template goes Blueprint → Details → Team → Review. Its commit creates no
agent; the Team step's choices are applied afterward (see
[Staffing after the commit](#staffing-after-the-commit)). Selected-member
grouping and guided setup have no choice to make, so they start at Details and
have no Team step.

## Eligibility and catalog

A managed entry is contributed by either:

- a combined blueprint with Assistant Program schema 2, a `required` or
  `recommended` `group_requirement`, and a trusted plugin, ready source-linked
  variant, or user-quest owner; or
- a trusted plugin-level `assistant_program_homes` item with Home schema 1,
  Home version 1, an embedded Assistant Program schema-2 Home declaration, and
  `independent_program_homes_v1`.

Library copies without an owner, combined declarations without
`group_requirement` (for example reviewed REAPER 0.5.0 and 0.5.1), and policy
`none` are not listed. Independent Home declarations need no project blueprint.

Entries are deduplicated by the exact source-scoped key (plugin or attachment plus
program ID). A combined blueprint and its variants share one entry. Two sources
that disagree about Home content for the same key produce a `conflict` entry and
nothing can be created from it.

`GET /api/workspaces/group-templates` returns General first, then managed
entries. Each managed entry reports:

- `availability.state`: `creatable`, `reusable`, `existing_only_missing`,
  `unavailable`, `conflict`, `ambiguous` or `incompatible`, plus a reason and
  allowed actions.
- The existing Home's ID and current name, when present.
- Verified required Home-role progress.
- `home_roles[]`: each Home role's ID, label, description, required and primary
  flags, and `default_name` — the declaration's `default_primary_name` for the
  primary role, otherwise the label. No prompt, skills or agent type are listed.

## Identity

Four identities are kept apart:

| Identity | What it is | Used for |
| --- | --- | --- |
| Group template ID | `group-template:<32 hex>`, a digest of the source-scoped key. Owner-free and opaque. | Selecting a catalog entry; presentation lookups |
| Program key | `{owner, plugin_id \| template_id+attachment_id, program_id}` | Finding the one Home (`AssistantProgramStore.FindStation`) |
| Workspace UUID | the group's workspace ID | Routes, parenting, roles |
| Group name | user-editable display text | Display only; rename never changes identity |

**One Home per program key.** Creation runs under the global provision mutex. A
concurrent or later request for the same key reuses the existing Home and never
renames, reparents or restaffs it.

**Provenance.** A Home first created by a Group Template records inert
`assistant_program_state.group_template` (schema 1). It holds the template ID
and revision, source kind and revisions, plugin owner snapshot, Home digest,
review digest and `created_at`. It has no key, name or paths. It is written only
in the same save that creates the Home, and never on reuse or read.

Display never depends on provenance. Type and provider come from the Home's own
program key and declaration snapshot, so Homes created by guided setup or by a
project's destination card look the same.

## Confirmations stay separate

Each surface keeps its own review and commit. A shared presentation never
carries another surface's token.

| Surface | Review and commit | Name | Writes provenance |
| --- | --- | --- | --- |
| Create Group → Group template | `POST /api/workspaces/group-templates/{review,commit}` | typed by the user (1–120 bytes, no path/protocol/control characters) | yes, on first creation |
| Project blueprint destination card | `POST /api/workspaces/group-requirement/home/{review,commit}` | declared `default_home_name` | no |
| Guided setup quest | setup-journey `review_create_group` / `create_group` | typed by the user | no |
| Group coordinator | `PUT /api/workspaces/{id}/roles/{roleID}` (group-owned; called by the group page and by the Create Group wizard after a Group Template commit) | — | — |
| Project | `POST /api/workspaces` with its own group placement review | — | — |

The Group Template review and commit bodies are decoded strictly (4 KiB, no
unknown fields, no trailing data). They accept only `group_template_id`,
`revision` and `name`; commit adds `group_review_token` and `idempotency_key`.
Owner, source, plugin, program, parent, team and project fields are refused
with `400`.

The server re-derives the catalog, re-resolves the representative source and
requires an equal revision and Home digest. A stale selection returns `409`.
The input digest is domain-separated, so a direct-recovery receipt can never
satisfy a Group Template commit, or the reverse.

The commit response reports:

- `home_created`
- `created_by_this_operation`
- `name_applied`
- `idempotent_replay`

A lost response is retried with the identical request. For this Home-only
operation, a replay after the 15-minute review lifetime still returns the
recorded result.

A Home created by a reviewed Home operation is recorded as owned by this data
directory (`workspace_allowlist.json`), like any other local creation. Its
staffed roles therefore survive a restart.

### Staffing after the commit

The Create Group wizard's **Team** step stages fills for the template's Home
roles: every required role starts as Create under its `default_name` (or as
Assign when a saved agent already has that name), and optional roles start
empty. Only after the commit above succeeds and reports a Home workspace ID does
the wizard send one `PUT /api/workspaces/{home}/roles/{roleID}` per staged role,
in declaration order, with `mode`, `name`, `provider` and `model` — never a
prompt, which the role endpoint applies from the Home's own declaration. The
commit request, its digest and its receipts are unchanged; staffing is not part
of that confirmation. A failed fill never undoes the group or an earlier fill, a
`409` saying the role is already filled counts as staffed, and no fill is sent
when the commit fails. When reusing an existing Home, only required roles the
catalog verified as empty can be staffed; filled roles keep their holder.

The wizard then opens the group page. It passes `?role=<roleID>` for the first
role that failed or was left empty, which opens that role's setup form once, and
leaves a one-time notice (sessionStorage) that the page shows as a toast.

## Presentation

- **Create Group** opens on a **Blueprint** step ("Choose a group blueprint")
  listing General and managed entries, with General selected. Each entry shows
  the template name, provider (`Plugin: <id> <version>` or `Your template`), a
  "what this creates" line, required and optional Home roles, and project-local
  roles. Unavailable entries are disabled with a reason. Choosing an entry stays
  on the step, so arrow keys can browse; **Continue** moves to Details, which
  shows the chosen blueprint with an Edit link. A managed template then shows
  **Team** ("Staff the group"), drawn with the shared role roster, and a Review
  receipt with one outcome per Home role ("Create", "Assign" or "Not staffed")
  and a warning for each empty required role. Create stays enabled either way.
- **Guided setup** has no Blueprint step. It shows its template as a fixed,
  read-only card on Details with the same wording, and its own review/commit
  actions.
- **Project destination card and final review** name the template behind the
  proposed or reused group. Home coordination is read-only in the project team;
  only project-local staffing enters project creation.
- **Selected-members grouping** (Map, Hub, Home bulk actions) is General-only.
  A managed template cannot adopt selected workspaces.
- **Group page** shows three independent facts from
  `GET /api/workspaces/{id}/group-template`:
  - **Group:** created.
  - **Coordinator:** `ready`, `incomplete`, `unverified` or
    `migration_required`, counting only verified bindings, instances and saved
    agents.
  - **Integration:** `available`, `unavailable` (with a reason) or `unknown`.
  
  "Set up <role>" opens the existing group-owned role form. For a program Home
  role that form has no prompt box: it says the instructions come from the
  template and are applied by Ori, and sends no `system_prompt`. A
  `?role=<roleID>` link opens an empty role's form once and is then removed from
  the URL. Ordinary groups report General.
- **Lists and Map.** `GET /api/workspaces` includes a bounded `group_template`
  summary (`kind`, `name`, `provider_kind`, `plugin_id`) for Homes only. The Map
  district header shows the template type. Type is never inferred from names,
  tags, agents or parentage.

All source-supplied text is rendered as text.

## Plugin lifecycle

| Change | Group | Coordinator | Integration | Group Template entry |
| --- | --- | --- | --- | --- |
| Rename (`POST /api/workspaces/{id}/rename`) | same ID, new name | unchanged | unchanged | `reusable` with the new name |
| Home provider disabled | readable | existing binding unchanged; new Home-role staffing refused | `unavailable` / `plugin_enable_required` | `unavailable` |
| Home provider uninstalled | readable | existing binding unchanged; managed skill unavailable | `unavailable` / `plugin_install_required` | not listed |
| Project provider disabled/uninstalled | readable | Home binding and Home management unchanged | Home `available`; affected project actions unavailable | Home entry unchanged |
| Plugin re-enabled or reinstalled with the same Home digest | readable | unchanged | `available` | `reusable` |
| Sources disagree | readable | unchanged | `unavailable` / `home_declaration_conflict` | `conflict` |
| Legacy schema-1 Home | readable | `migration_required` | `unavailable` / `home_incompatible` | `incompatible` |

No lifecycle change migrates, redeclares, restaffs, deletes or grants anything
automatically.

## Limitations

- The published reviewed REAPER floor remains 0.6.1 and still uses the combined
  declaration. The independent local candidates are Music Project Management
  `8f4abd0b283fefe23653a2cf81deb800123c4bde` (0.1.0) and REAPER
  `68488b4d62978f22bfdff26c3554cebb6b4cf396` (0.8.0, blueprint v9). They are
  unpublished compatibility evidence, not releases or registry inputs.
- Disposable Chromium acceptance covers music-only, Music-first, REAPER-first,
  REAPER-only standalone, two linked projects, one reviewed inert handoff,
  provider removal/reinstall, and a controlled restart. It does not configure a
  model, open REAPER, or verify live DAW control.
- There is no ownership migration. Existing combined Homes remain under their
  recorded keys and are not adopted by name, rewritten, relinked, or converted.
- The destination card uses the declared name; its receipt has no name input.
  Rename the Home afterwards, or create it from Group Templates.
- Delivery order is a compatible Ori release, then Music Project Management,
  then a compatible REAPER release. The reviewed floor changes only after
  separately authorized publication verification.
