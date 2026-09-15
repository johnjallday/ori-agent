# Group Templates v1

Status: implemented host feature (domain-neutral). Demonstrated with the
published REAPER plugin 0.5.2 in a disposable server. Positive guided REAPER
acceptance is not demonstrated; see [Limitations](#limitations).

## Purpose

**Create Group** offers Group Templates alongside the ordinary General group. A
Group Template creates, or reuses, the one Assistant Program Home that a trusted
project blueprint declares. For example, a music plugin can offer a
"Music Production Home" that its song projects then join.

Group Templates are a host-side projection over declarations the host already
trusts ([Template Group Requirements](template-group-requirements.md)). They are
not a second template format. Nothing is authored for them, and no plugin needs a
new host feature flag.

## Group Templates versus Workspace templates

| | Workspace (project) template | Group Template |
| --- | --- | --- |
| Chosen in | Create Workspace → Blueprint | Create Group → Blueprint |
| Creates | a project workspace, its team, optional project files | exactly one group (program Home), initially unstaffed |
| Source | library, plugin blueprint, source-linked variant | derived from an eligible project blueprint; never edited |
| Team | reviewed project roster | none at creation; group roles are staffed afterward |

A source blueprint's `agents[]` roster and `assistant_program.roles[]` may still
carry a `type` key from before the agent type was retired. The key is accepted
and ignored.

**General** is the ordinary group. It keeps its reviewed Group Manager roster
(Blueprint → Details → Group Roster → Review) and never joins a program. A
managed template goes Blueprint → Details → Review and creates no agent.
Selected-member grouping and guided setup have no choice to make, so they start
at Details.

## Eligibility and catalog

A blueprint contributes a managed entry only when all of these hold:

- Its Assistant Program uses schema 2.
- Its `group_requirement` policy is `required` or `recommended`.
- It has a trusted owner key: a plugin, a ready source-linked variant of a
  plugin blueprint, or a user template with a setup-quest attachment.

Library copies without an owner, legacy declarations without
`group_requirement` (for example reviewed REAPER 0.5.0 and 0.5.1), and policy
`none` are not listed.

Entries are deduplicated by the source-scoped key (plugin or attachment plus
program ID). A blueprint and its variants share one entry. Two sources that
declare different Home content for the same key produce a `conflict` entry and
nothing can be created from it.

`GET /api/workspaces/group-templates` returns General first, then managed
entries. Each managed entry reports:

- `availability.state`: `creatable`, `reusable`, `existing_only_missing`,
  `unavailable`, `conflict`, `ambiguous` or `incompatible`, plus a reason and
  allowed actions.
- The existing Home's ID and current name, when present.
- Verified required Home-role progress.

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
| Group coordinator | `PUT /api/workspaces/{id}/roles/{roleID}` (group-owned) | — | — |
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

## Presentation

- **Create Group** opens on a **Blueprint** step ("Choose a group blueprint")
  listing General and managed entries, with General selected. Each entry shows
  the template name, provider (`Plugin: <id> <version>` or `Your template`), a
  "what this creates" line, required and optional Home roles, and project-local
  roles. Unavailable entries are disabled with a reason. Choosing an entry stays
  on the step, so arrow keys can browse; **Continue** moves to Details, which
  shows the chosen blueprint with an Edit link.
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
  
  "Set up <role>" opens the existing group-owned role form. Ordinary groups
  report General.
- **Lists and Map.** `GET /api/workspaces` includes a bounded `group_template`
  summary (`kind`, `name`, `provider_kind`, `plugin_id`) for Homes only. The Map
  district header shows the template type. Type is never inferred from names,
  tags, agents or parentage.

All source-supplied text is rendered as text.

## Plugin lifecycle

| Change | Group | Coordinator | Integration | Group Template entry |
| --- | --- | --- | --- | --- |
| Rename (`POST /api/workspaces/{id}/rename`) | same ID, new name | unchanged | unchanged | `reusable` with the new name |
| Plugin disabled | readable | unchanged; staffing still allowed (uses the Home's own snapshot) | `unavailable` / `plugin_enable_required` | `unavailable` |
| Plugin uninstalled | readable | unchanged | `unavailable` / `plugin_install_required` | not listed |
| Plugin re-enabled or reinstalled with the same Home digest | readable | unchanged | `available` | `reusable` |
| Sources disagree | readable | unchanged | `unavailable` / `home_declaration_conflict` | `conflict` |
| Legacy schema-1 Home | readable | `migration_required` | `unavailable` / `home_incompatible` | `incompatible` |

No lifecycle change migrates, redeclares, restaffs, deletes or grants anything
automatically.

## Limitations

- **Exact REAPER evidence is limited to 0.5.2 in a disposable server.** The
  published release (`#sha=9fde099d…`, blueprint `reaper-song` v6, artifact
  sha256 `999dda37…`, 8,780,098 bytes) lists **Music Production Home**:
  - Music Portfolio Manager required, Sample Library Manager optional.
  - Producer, Mix Engineer and Songwriter project-local.

  `tests/reaper-group-templates.spec.ts` (opt-in,
  `ORI_REAPER_052_ACCEPTANCE=1`) covers creating the group, setting up its
  coordinator, adding two songs and reusing the renamed group. No live REAPER
  was involved. REAPER 0.5.0 and 0.5.1 declare no group requirement and are not
  listed.
- **Positive guided REAPER acceptance was blocked at the time.** The reviewed
  integration pin was 0.5.0, which declares no group requirement. The pin has
  since moved to the published 0.6.0 release (blueprint v7, which declares one).
  A guided quest for any other source is still refused by the
  reviewed-integration gate, which is unchanged.
- **Destination card uses the declared name.** Its receipt has no name input.
  Rename the group afterwards, or create it from Group Templates.
- **Guided Home creation is not recorded as locally owned.** It does not add the
  Home to the data directory's allowlist. With an unconfirmed workspace root,
  its staffed roles can still be dropped on restart.
