# Independent Program Homes v1

Status: findings-first contract for implementation

This document defines the fresh-setup contract that separates a reusable
Assistant Program Home from the project integration that attaches project-local
teams to it. It is additive. Existing combined Assistant Program schema 1 and 2
declarations and stored records remain readable with their existing meaning.
Nothing in this contract transfers, converts, relinks, resets, or adopts an
existing Home.

## 1. Source evidence and scope

The contract was revalidated on 2026-09-19 against these exact sources:

| Source | Evidence | Role in this change |
| --- | --- | --- |
| Ori | `https://github.com/johnjallday/ori-agent.git`, `origin/dev` and feature base `42d3a224296829cbfeeffc868353e29d43acecf5` | Trusted declaration decoding, catalog, reviews, persistence, links, lifecycle, and permissions. |
| Music Project Management | `/Users/jjdev/Projects/music-project-management`, unborn `main`, no remote or commit; initial `.gitignore`, `README.md`, and `SKILL.md` only | Independently installable Home declaration, Home roles/defaults, and one portable skill source. This is source evidence, not an authorized write target. |
| REAPER Plugin | `https://github.com/johnjallday/reaper-plugin.git`, current `main` and `v0.7.0` at `f689f42966cdaa0cab4f4b6573b20c2cba89193b` | Reaper Song project declaration, project roles, project connection, setup quest, capabilities, inputs, and skeleton. This is source evidence, not an authorized write target. |
| Reviewed REAPER floor | detached installed source `v0.6.1` at `e11ca2942279af02a9a035039b18b146ff9fc89d` | Compatibility evidence only; never edited and not treated as the latest candidate. |

The current REAPER 0.7.0 Reaper Song blueprint is version 8 and carries one
combined Assistant Program schema-2 declaration. That block owns both the Music
Production Home and the Producer/Mix Engineer/Songwriter project team. Current
Ori Group Templates are consequently derived from project blueprints, and the
program key derives its plugin owner from the project source. Those assumptions
must change explicitly; moving JSON between files is not enough.

The implementation remains domain-neutral in Ori. Concrete music copy, role
prompts, skill names, attachment allowlists, and project-team declarations come
from the two trusted packages. Ori supplies strict schemas and host-owned
consequence boundaries.

### Non-goals

This contract does not add:

- Home ownership transfer, declaration migration, record copying, legacy
  relinking, prompt replacement, agent conversion, reset behavior, or name-based
  adoption;
- a dummy project, fake project skeleton, generic setup quest for a Home, or an
  executable music-management service;
- transitive dependency downloading, silent package installation, guessed
  source URLs, publication, registry pin changes, or installation into real user
  state;
- recursive discovery, watchers, new DAW adapters, or additional sample-library
  behavior; or
- child filesystem, tool, transcript, memory, runtime, live-control, or direct
  child-agent authority for a Home role.

## 2. Ownership model

The split has three declarations with one owner each:

1. **Program Home contribution** — authored at plugin-contribution level by
   `music-project-management`; it owns the Home identity, Home display copy,
   Home roles, stages, reflection/learning defaults, and the closed list of
   project attachments it accepts.
2. **Project team declaration** — authored inside the REAPER project blueprint;
   it owns project-local roles and an exact reference to the Home provider it
   can join.
3. **Placement declaration** — authored beside that project team in the REAPER
   blueprint; it retains Required/Recommended/None behavior, missing-Home
   behavior, and standalone transformation without redefining the Home.

The trusted Home identity is:

```text
(owner_user_id, home_provider_plugin_id, home_program_id)
```

For this feature it is:

```text
(current owner, music-project-management, music-producer-assistant)
```

Workspace name, folder slug, tags, parentage, installed skill name, project
provider, and the former REAPER-owned key are not identity. A legacy Home named
"Music Production Home" under `(owner, reaper-plugin,
music-producer-assistant)` remains a different Home and is never adopted.

A connected project retains two independent provenances:

- **Home provenance:** the exact enabled Home provider, its installed generation
  and component fingerprint, the normalized Home declaration version/digest,
  and the stable Home program key/workspace ID.
- **Project provenance:** the exact enabled project provider, blueprint and
  project-team versions/digests, immutable creation snapshot, project workspace
  ID, and stable Assistant Project Link ID.

A link is valid only while its persisted evidence remains internally consistent.
Here, installed generation means the content generation: it advances when the
trusted component fingerprint changes, including a packaged skill tree digest,
but remains stable across explicit disable/enable runtime epochs. Runtime epochs still invalidate live surface
sessions independently. Provider availability may later make actions
unavailable, but it does not erase that evidence or stored user data.

## 3. Contribution contract

### 3.1 Host feature gate

The new host feature is:

```text
independent_program_homes_v1
```

A contribution that declares `assistant_program_homes`, and a blueprint that
declares `assistant_project`, must list this feature in
`requires_host_features`. An older Ori build fails the contribution before
registration. Unknown fields still fail strict manifest/template decoding; the
feature flag is not permission to partially accept a declaration.

The feature is additive to Workspace Surface contribution schema 1. It does not
change Workspace Surface protocol 1 or service transport. A content-only Home
package may contain no capability, service, executable artifact, setup quest, or
project blueprint; a validated Home contribution counts as its one Ori
component.

Combined blueprint `assistant_program` schema 1 and 2 remain accepted under the
existing feature and retain their current owner/key semantics. A single
blueprint cannot declare both the combined form and the split
`assistant_project` form. No reader synthesizes one form from the other.

### 3.2 Independently authored Home declaration

`.ori-plugin/plugin.json` gains a bounded `assistant_program_homes` array. One
entry has this closed shape (music copy abbreviated):

```json
{
  "schema_version": 1,
  "version": 1,
  "id": "music-producer-assistant",
  "station_name": "Music Production Home",
  "station_description": "A Home for portfolio coordination and reviewed learning.",
  "default_primary_name": "Portfolio Manager",
  "hire_title": "Staff your music production Home",
  "hire_description": "Add Home roles separately from each project's team.",
  "disabled_message": "Music project management is unavailable; stored coordination data remains readable.",
  "suggestion_required_capabilities": [],
  "roles": [
    {
      "id": "portfolio_manager",
      "label": "Music Portfolio Manager",
      "description": "Coordinates the portfolio without inheriting project access.",
      "required": true,
      "primary": true,
      "role": "orchestrator",
      "system_prompt": "Trusted bounded Home-role instructions.",
      "skills": ["music-project-management"]
    },
    {
      "id": "sample_library_manager",
      "label": "Sample Library Manager",
      "description": "Optional role for the existing reviewed sample-library boundary.",
      "required": false,
      "capability_id": "sample-library",
      "role": "specialist",
      "system_prompt": "Trusted bounded optional-role instructions."
    }
  ],
  "stages": [],
  "reflection": {},
  "allowed_project_attachments": [
    {
      "provider_plugin_id": "reaper-plugin",
      "blueprint_id": "reaper-song",
      "project_team_id": "reaper-song-team",
      "project_team_schema_version": 1,
      "min_project_team_version": 1,
      "max_project_team_version": 1
    }
  ]
}
```

`roles` contains Home roles only; there is no `scope` field because the owning
component fixes the scope. It requires at least one required role and exactly one
required primary. Optional roles remain non-primary Home capability roles. The
Portfolio Manager binds `music-project-management`; Home-only management does
not require `reaper_live_control`. The optional Sample Library Manager preserves
the existing host-reviewed add-on boundary and does not itself install a
capability, connect a root, scan, analyze, or copy.

`stages`, `reflection`, suggestion behavior, portfolio behavior, bounded
learning, and disabled copy remain Home-owned. The declaration is inert data: it
cannot contain a path, URL, source, command, service, operation, route, runtime
adapter, setup action, permission, filesystem root, schedule below existing
bounds, or executable hook.

`allowed_project_attachments` is an authorization allowlist, not an install
request. It identifies the project provider, blueprint, project-team ID, exact
team schema, and an inclusive semantic declaration-version range. It does not
name a Git URL, marketplace source, mutable plugin version, workspace, Home,
folder, command, or grant. The installed project generation and fingerprints are
bound separately by Ori when resolving and snapshotting an attachment.

### 3.3 Separate project-team declaration

A project blueprint gains one optional closed `assistant_project` block:

```json
{
  "schema_version": 1,
  "version": 1,
  "id": "reaper-song-team",
  "home": {
    "provider_plugin_id": "music-project-management",
    "program_id": "music-producer-assistant",
    "home_schema_version": 1,
    "min_home_version": 1,
    "max_home_version": 1
  },
  "roles": [
    {
      "id": "producer",
      "label": "Producer",
      "description": "Coordinates only this song project.",
      "required": true,
      "primary": true,
      "role": "orchestrator",
      "system_prompt": "Trusted bounded project-role instructions.",
      "skills": ["reaper-session-setup", "reaper-project-tidy"]
    }
  ]
}
```

All roles are project-scoped by ownership of the block. V1 requires at least one
role, every role required, and exactly one primary. It cannot declare Home roles,
Home copy, Home skills, stages, reflection, portfolio defaults, attachment
allowlists, capabilities owned by another provider, or a fallback Home. REAPER
retains Producer, Mix Engineer, and Songwriter here, with their existing
project-only prompts and REAPER skills.

The `home` reference is exact trusted intent, not proof that the companion is
installed. Therefore REAPER may be installed first. Plugin install validates the
reference's syntax, schema/version range, and internal consistency without
fetching or creating the Home provider. Grouped creation stays unavailable with
a closed companion-missing/incompatible reason until Ori resolves a matching
installed Home declaration.

### 3.4 Placement and standalone composition

Current `group_requirement` schema 1 continues to mean "the same trusted
blueprint owns the combined Assistant Program." Its interpretation is
unchanged.

Split blueprints use `group_requirement` schema 2. It preserves the existing
`policy`, `missing_home`, and `default_home_name` rules, but references the local
project-team declaration rather than claiming a Home declaration:

```json
{
  "schema_version": 2,
  "policy": "required",
  "assistant_project_id": "reaper-song-team",
  "missing_home": "offer_create",
  "default_home_name": "Music Production Home"
}
```

Ori derives the Home provider/program solely through the normalized
`assistant_project.home` reference. A browser cannot submit or override it.
`offer_create` means "offer reviewed create/reuse after the declared Home
provider is installed, enabled, and compatible." It never authorizes dependency
installation or a fallback declaration. When the provider is missing, the host
shows a reviewed host-owned install destination if one exists, otherwise safe
manual installation guidance with no guessed URL.

`standalone_composition` schema 1 keeps its current combined-v2 meaning. Split
blueprints use schema 2, whose `project_roles` must exactly cover the local
`assistant_project.roles`. Its transformation removes the project-team/Home
reference, grouped policy, project link, setup-quest ownership that depends on a
Home, and all Home behavior while retaining the REAPER project connection,
inputs, skeleton, runtime modes, capabilities, technical skills, setup, and
project-only ordinary agents. It never resolves or installs the music package
and never creates hidden program state.

### 3.5 Reciprocal authorization

A grouped attachment is accepted only when every condition holds:

1. The project blueprint is trusted, enabled, creation-ready, and carries a
   normalized split project-team declaration.
2. The referenced Home provider is independently installed, enabled, compatible,
   and contains exactly one matching normalized Home declaration.
3. The project reference accepts the Home schema/version.
4. The Home declaration contains an `allowed_project_attachments` entry matching
   the exact project provider, blueprint, project-team ID, schema, and version.
5. Both contributions require `independent_program_homes_v1`; their component
   fingerprints and declaration digests match the facts bound into review.
6. The current owner is authorized for both installed contributions and the
   target Home; the exact stable Home key resolves to zero or one compatible
   active Home.

Neither side's declaration alone grants attachment. A copied blueprint, a
same-named package, a user template claiming plugin IDs, a disabled/incompatible
provider, an unallowlisted project, a legacy Home, and a same-name Home all fail
closed. Project creation review binds both providers and is stale if either
generation, fingerprint, declaration digest/version, target Home, or owner state
changes.

## 4. Strict validation and limits

V1 uses the existing conservative identifier grammar and text controls. All
objects are closed and all declarations reject unknown fields and trailing JSON.
Normalization is all-or-nothing.

| Item | Limit/rule |
| --- | --- |
| Home contributions per package | 8 |
| Allowed attachments per Home | 8, unique by provider/blueprint/team |
| Home or project roles | 1–8, stable IDs unique within the assembled program |
| Stages, prompts, rubric, reflection bounds | Existing Assistant Program limits |
| Schema versions | Exactly `1` for new Home/project blocks; exactly `2` for split group/standalone blocks |
| Definition versions and ranges | Positive integers; minimum not greater than maximum; current version must be in range |
| IDs | Lower-case stable ASCII, 1–64 bytes |
| Display text | Existing Assistant Program bounds; valid UTF-8; no NUL, control data, or protocol text where currently forbidden |
| Skill names | Canonical registry IDs, unique and bounded; availability checked separately from declaration validity |
| Provider/blueprint references | Canonical IDs only; no source location or mutable display-name lookup |

Across the resolved Home plus one project team, role IDs must also be unique.
Home/project primaries are checked separately. The host does not merge two Home
declarations, union two attachment allowlists, choose among duplicate matching
providers, or accept a partially valid role set.

The trust report and installed component fingerprint include normalized Home
contributions, project-team declarations through blueprint definition digests,
attachment allowlists, referenced provider/program/version ranges, and packaged
skill ownership. A change to any authorization-, prompt-, skill-, role-, or
behavior-bearing field requires a fresh trust/update review.

## 5. Minimal Music Project Management package

The companion package uses this minimal layout:

```text
.claude-plugin/plugin.json
.ori-plugin/plugin.json
README.md
.gitignore
skills/
  music-project-management/
    SKILL.md
scripts/
  validate-package.py
```

The initial root `SKILL.md` content is moved, not copied, to
`skills/music-project-management/SKILL.md`; that file is the single canonical
skill source. `README.md` links to it and explains both portable skill use and
Ori package installation. The package contains the one Home contribution, no
project blueprint or skeleton, no MCP server, no Workspace Surface service, no
binary/artifact, no runtime process, and no setup quest. Its validation script
uses repository-local, non-installing checks for manifest identity, strict
shape/versions, skill frontmatter/name, contained paths, cross-references, and
absence of fake project/runtime components.

Installing the package and creating/staffing its Home remain separate explicit
consequences:

1. plugin trust review and installation;
2. plugin enablement when needed;
3. Create Group → Blueprint → Details → Team → Review for the exact Home;
4. one reviewed role fill per selected Home role; and
5. model-backed execution readiness, reported independently from deterministic
   declaration/Home/binding readiness.

Metadata reads perform none of those consequences.

## 6. Fresh-setup persistence rule

New Homes snapshot the independent Home provider identity, declaration
schema/version/digest, installed generation/fingerprint, and reviewed Group
Template provenance. New linked projects separately snapshot their project
provider/blueprint/team evidence and exact resolved Home target. Stored data
survives provider disable/removal and remains readable with honest availability.

Old combined declarations and records keep their recorded project-provider key.
A new independent declaration with the same program ID or display name does not
match them. No startup, list, install, enable, catalog refresh, Group Template
read, project review, retry, reinstall, or lifecycle action rewrites that
ownership. Migration/reset is outside this feature.

## 7. Cross-provider seam map

The host resolves the split declarations at explicit boundaries rather than
assembling a synthetic combined blueprint:

| Seam | Home provider supplies | Project provider supplies | Host rule |
| --- | --- | --- | --- |
| Plugin catalog and trust | Normalized Home declaration and attachment allowlist | Blueprint with project team/reference and placement | Reads validate and disclose only; no workspace, role, grant, install, or link is created. |
| Group Templates | Home display, Home roles, declaration version/digest, provider generation/fingerprint | Nothing | Managed Home entries are projected directly from enabled Home components. A project blueprint is no longer required to make the Home entry visible. |
| Group requirement | Referenced declaration and exact stable key when available | Schema-2 policy and local project-team ID | Resolution follows `assistant_project.home`; the browser cannot choose another provider or program. |
| Template variants | Nothing mutable; the compatible Home is freshly resolved | Source pins, split declarations, and schema-2 standalone transformation | The definition digest includes `assistant_project`; a variant cannot override either provider identity or authorization evidence. |
| Creation snapshot | Home declaration identity/version/digest and installed provider generation/fingerprint | Existing immutable template snapshot plus project-team version/digest and project provider evidence | Review binds both providers and target Home; commit re-resolves all facts and fails stale before creation. |
| Project connection and link | Existing/new exact Home workspace under the Home key | Project workspace, connection/root/entry, and project-team declaration | Link registration is reciprocal and idempotent; Home and project provenance remain separate. |
| Roster and staffing | Home roles, prompts, skills, role bindings | Project roles, prompts, skills, role bindings | Role IDs are unique across the resolved pair, but state remains scope-owned. Project creation never restaffs the Home or another project. |
| Setup quests | No project quest; Home onboarding uses the Group creator | Existing project setup quest and integration modes | A missing Home companion produces host-owned install/navigation guidance only. Quest execution cannot fetch it or manufacture a fallback. |
| Portfolio and handoff | Portfolio state and reviewed handoff intent | Exact linked child and its local roster | Home reads bounded link/project summaries and may create one confirmed, non-running child-owned Ticket. Same-workspace delegation is unchanged; no child agent, prompt, transcript, memory, tools, runtime, filesystem, or live-control authority crosses the link. |
| Sample library | Optional Home capability role and existing Home-owned roots/catalog state | Nothing | Existing reviewed root/scan/copy boundaries remain; declaring the optional role performs no sample operation. |
| Learning/reflection | Existing bounded receipts, approval policy, schedules and Home-role state | Project result summaries already admitted through exact links | Provider loss disables new execution, not stored evidence. No project-private context is exposed and no schedule is created or resumed by metadata reads. |

Availability is deliberately split. Declaration availability means the exact
installed and enabled provider component still matches its snapshot. Staffing
readiness means required role bindings and their exact declared skills resolve.
Execution readiness additionally requires a usable configured model. Project
integration/live readiness belongs to the project provider and does not gate
Home-only portfolio management. Stored snapshots, role bindings, portfolio
records, learning receipts and links remain readable when either provider is
unavailable.

Home roles may continue to delegate to roles in the same Home workspace through
the existing reviewed role boundary. The project link itself never grants direct
cross-workspace delegation. `Send to project` remains a host-owned Ticket write
with review, revalidation, idempotency and child ownership.

## 8. Skill installation and ownership

Plugin skill installation uses the shared personal skills directory today, while
runtime lookup gives repository and `.agents` sources precedence over that
personal directory. The safe bounded policy for this feature is:

1. Installation refuses if the destination exists without a valid Ori ownership
   receipt for the same plugin and skill. It never overwrites an independently
   installed same-name skill, a directory owned by another plugin, a symlink, or
   an edited former plugin copy.
2. A successful copy is published atomically with a receipt containing canonical
   plugin/skill identities and a digest of the copied tree. The reviewed source
   fingerprint is checked again after registration; a concurrent source change
   rolls the new components back instead of recording mismatched evidence. The
   receipt is data, not a precedence override.
3. Reinstall/update/removal first verifies the receipt and current tree digest.
   An edited copy is left in place and the operation refuses with recovery
   guidance. Update snapshots the exact verified installed bytes before removal;
   rollback restores only components actually removed and never re-reads mutable
   source bytes as the old generation. Removal atomically quarantines and
   re-verifies a destination before deletion. Rollback removes only a
   just-published, still-matching owned copy.
4. Removal deletes no per-agent skill state and never enables or disables a skill
   on an agent. Reinstall therefore cannot silently activate it.
5. The runtime manager must resolve the packaged personal copy for a declared
   role. If a repository or `.agents` source shadows the same canonical name,
   readiness reports a source conflict instead of silently executing that other
   source. General skill resolution precedence remains backward compatible.
6. The installed-plugin registry remains the restart inventory of claimed names;
   the destination receipt proves file ownership. Start Fresh may remove only a
   receipt-verified plugin copy for records written with the receipt schema.
   Schema-zero records retain the older conservative reset path, but ordinary
   update/uninstall never adopts an unreceipted destination. Existing reset
   refusal for duplicate claims and unexpected destinations remains in force and
   is tightened, not broadened, by receipts.

This policy does not introduce a dependency resolver, mutate portable source,
or co-own a directory. A user resolves a collision explicitly by renaming,
removing, or preserving the independent copy outside the reviewed operation.

## 9. Compatibility and installation matrix

| State | Catalog / setup result | Mutation rule |
| --- | --- | --- |
| Music only | Independent Home Group Template is available; Home can be created and staffed; no project or REAPER readiness is claimed. | Install, enable, Home create and each role fill remain separate confirmations. |
| REAPER first | Reaper Song remains installable; grouped creation reports the exact missing Home provider/program. Standalone remains available under its declared path. | Show a configured reviewed install destination if one exists, otherwise manual package-name guidance; never guess a URL or auto-install. |
| Music first | Home is available before any project integration. Later REAPER setup resolves/reuses it by exact key. | No duplicate Home or manager is created. |
| Companion disabled or removed | Snapshots and data remain readable; actions needing that provider are unavailable with provider-specific status. | Re-enable/reinstall is explicit; no relink, schedule restart, or grant revival occurs from reads. |
| Companion incompatible or ambiguous | Grouped setup fails closed with version/conflict detail safe for display. | No fallback declaration, partial role merge, or arbitrary candidate selection. |
| Older Ori | `independent_program_homes_v1` is unknown, so either new contribution is rejected before registration. | No partial package install. |
| Old combined declaration | Existing schema-1/2 catalog, key, Home and project behavior remain unchanged. | No synthesis into split form and no rewrite. |
| Independent same-name skill | Plugin install/update refuses before overwrite; runtime shadowing is reported as not ready. | Independent files and agent state are untouched. |
| Legacy same-name Home | It remains under the legacy project-provider key and may still be listed by old readers. | New setup creates/reuses only the independent key; names never authorize adoption. |

Cancellation before trust, Home, staffing, project, connection, or handoff commit
has no consequence. Every review token binds normalized input and relevant
provider generations, fingerprints, declaration digests/versions, owner and
workspace revisions. A changed fact expires the review before the first write.
Where an existing multi-step operation cannot be atomic, durable idempotency
receipts distinguish completed consequences; retry performs only a missing step
and never rolls back a pre-existing Home, project, root, link, role or user edit.

Release ordering is host support first, then a separately authorized and
published music package, then a compatible REAPER release, then any reviewed
registry/floor update based on verified publication. Local candidate paths and
hashes are test evidence only and never production install guidance.

## 10. Contract review and implementation tests

A bounded identity/ownership review found these principal risks and decisions:

- **Identity confusion:** project-provider IDs, display names and program IDs
  alone could select a legacy/foreign Home. Decision: stable Home key plus exact
  normalized declarations and two-sided attachment authorization.
- **TOCTOU across providers:** either plugin or target Home can change after
  preview. Decision: bind and revalidate both generations, fingerprints,
  declaration digests/versions, owner and target revisions at commit and on
  execution paths.
- **Authority laundering:** a trusted project could claim a Home, or a Home could
  accept every project. Decision: exact reciprocal ranges and installed
  provenance are mandatory; neither declaration grants authority alone.
- **Skill clobber/removal:** the current shared-directory adapter overwrites and
  removes by name. Decision: receipt plus tree digest, atomic publication, and
  fail-closed collision/edit behavior from section 8.
- **Cross-workspace privilege:** joining a portfolio could be mistaken for child
  tools or agent delegation. Decision: retain exact links and bounded reviewed
  Ticket handoff only; no ambient child authority.
- **Read-caused consequences:** catalog refresh could create/adopt a Home or
  install a dependency. Decision: all catalog and availability resolution is
  pure; consequences remain separately reviewed commands.

Required fixture-level host tests are: strict accepted/rejected content-only
Home contributions; host-feature gating; duplicate declarations and attachment
entries; project reference/range validation; reciprocal mismatch and role-ID
collision; Group Template projection without a blueprint; same-name legacy Home;
both install orders; stale two-provider review; split roster ownership; schema-2
standalone transformation; provider disable/removal; portfolio and sample-library
boundaries; and skill collision/edit/update/removal/rollback/restart. Existing
combined schema-1/2, General group, template variant, project connection and reset
suites remain regression gates.

Companion brief for Music Project Management: implement exactly the minimal
layout in section 5, one Home declaration and one canonical packaged skill,
with no blueprint, service, setup quest, capability installation or runtime.
Companion brief for REAPER: replace only the combined declaration with the
project-team/reference and schema-2 placement/standalone blocks while preserving
blueprint inputs, skeleton, connection, modes, capabilities, technical skills and
project quest behavior. Both briefs require the feature gate and strict versions.

## 11. Local candidate evidence and delivery status

The companion worktrees were later separately authorized and produced clean,
unpublished candidates:

- Music Project Management 0.1.0: commit
  `8f4abd0b283fefe23653a2cf81deb800123c4bde`, tree
  `5745acc5d3f319c20b5055ac1a244c9646ecc93e`.
- REAPER Plugin 0.8.0 / Reaper Song v9: commit
  `68488b4d62978f22bfdff26c3554cebb6b4cf396`, tree
  `f03299c92dbecef5c305efb5af03023a86e766bc`.

Exact committed-tree acceptance passed for Music-only, Music-first, REAPER-first,
and REAPER-only standalone state. Combined runs created two linked projects under
one Music-owned Home/manager, confirmed separate persisted provider evidence, and
created one reviewed non-running child Ticket. Lifecycle runs disabled, removed,
and reinstalled each provider and then restarted Ori against the same sandbox;
identities, links, rosters, Ticket, typed inputs, and `.rpp` bytes remained without
duplication. Standalone retained no Home or Assistant Program link across restart.

This is local compatibility evidence only. No candidate branch was pushed, no PR,
tag, release, registry/floor change, production install, model-backed execution,
or live REAPER action occurred. Publication remains blocked on the release order in
section 9 and separate human authorization.

### Source-delivery and production follow-up matrix

| Source | Exact local candidate | Target after human approval | Compatibility identity | Current gate |
| --- | --- | --- | --- | --- |
| Ori | `feature/music-project-management-home`, through `432d688c` plus the final acceptance/docs slice | `dev` | adds `independent_program_homes_v1`; Home/project contribution schema 1; preserves combined Assistant Program schema 1/2 | final diff review and human `wt pr` approval; no rollout implied |
| Music Project Management | `8f4abd0b283fefe23653a2cf81deb800123c4bde` | public repository `main` | package 0.1.0; Home schema/version 1; embedded Assistant Program schema 2; one canonical managed skill | separate push/PR authorization after compatible Ori is available |
| REAPER Plugin | `68488b4d62978f22bfdff26c3554cebb6b4cf396` | repository `main` | plugin/service 0.8.0; blueprint 9; project team schema/version 1; setup quest 3; `independent_program_homes_v1` | separate push/PR authorization after Ori and Music package availability |
| Reviewed integration registry/floor | unchanged published 0.6.1 floor | later Ori follow-up, if required | must use a published REAPER tag/commit and verified release artifact, never a local candidate or squash precursor | blocked on published compatible release evidence and separate approval |

A non-empty model was not configured in local acceptance. Live REAPER, Web Remote,
runner, DAW, publication, and production-state checks remain explicitly NOT RUN.
