# User-owned setup quests

Status: implementation contract for Issue #464. The product clarification
approved as **Go A** keeps all five v1 setup steps required, exactly once, in
their current order. This document records the checkout observed before product
code changed and the additive contract selected for implementation.

## Scope

A user-owned setup quest is one inert declaration embedded in one user-owned
project template. It edits bounded display copy and selects a host-reviewed
integration dependency. It is not a script, a second Setup Wizard, a plugin
fork, a reusable quest library, or an authority record.

The v1 order remains:

1. `integration_install`
2. `project_connect`
3. `workspace_setup`
4. `assistant_program_staffing`
5. `summary`

Every kind is present exactly once. The editor cannot remove or reorder it, and
`specialist.NormalizeSetupJourney`, the closed reader registry, action routing,
and readiness reconciliation remain authoritative.

## Observed pre-change request path

The feature spike traced the #466 checkout at `41d0a747`.

1. `projecttemplates.readManifest` reads `template.json` with a tolerant
   top-level decoder. It carries the plugin-only `setup_quest` string, while
   project connection, runtime, Setup Wizard, and Assistant Program blocks are
   normalized independently and fail closed with diagnostics.
2. `setupjourney.installedQuestCatalog` enumerates only host-reviewed registry
   entries. It resolves an installed plugin declaration, or the host
   compatibility declaration, and verifies integration key, plugin blueprint,
   Assistant Program, and the blueprint's `setup_quest` back-reference.
3. `setupjourneyhttp.Handler.ScopeQuest` derives the current user at the HTTP
   boundary, calls `Service.ForQuest(user, plugin, quest)`, and delegates to the
   same read/open/dismiss/child/action handlers as the accepted-assistant alias.
   Request bodies cannot supply owner, relationship, plugin, quest, source,
   adapter, or permission scope.
4. `Service.Read` resolves the selected declaration, then
   `SQLiteStore.CreateOrGetRoot` atomically creates at most one inert root for
   user + relationship + specialist + journey. Catalog reads and `ForQuest` do
   not create a row; the first runtime GET does. `Open` only records
   presentation history.
5. `scopeForRun` builds `ReadScope` from the declaration and root receipts. Each
   canonical read updates only bounded receipt IDs. Children retain their own
   project/mode state and read integration/Home identity from the root.
6. `ReviewedIntegrationAdapter` looks up the host registry by integration key,
   verifies pinned source, artifact/contribution identity, platform, blueprint,
   program, enablement, and review material. Before this feature,
   `entryMatchesScope` also requires the declaration's target blueprint/program
   to equal the reviewed plugin's blueprint/program. That coupling rejects an
   otherwise valid local target.
7. `installedProjectTemplateResolver` accepts only the installed plugin and
   version recorded by the integration step, then resolves an active plugin
   blueprint with matching program. It cannot resolve a library template.
8. `projectconnection.Service` additionally requires `Template.PluginOwner`.
   Its Home key is user + plugin + Assistant Program. Preview/commit digests
   include plugin template identity; project provenance and observation require
   the same plugin owner. `AssistantProgramStore.EnsureProjectStation` hydrates
   portable workspace provenance when needed, but also requires plugin
   provenance to derive that key.
9. The created project's normalized runtime contract and Setup Wizard are
   snapshotted into workspace provenance. `WorkspaceSetupAdapter` delegates to
   the canonical Setup Wizard/runtime services. The reviewed `file_only` choice
   completes without evaluating a live adapter and creates no runtime grant.
10. `AssistantStaffingAdapter` authorizes the exact Home, project link, current
    user, and expected Assistant Program. It creates reviewed scoped roles only;
    it does not inherit a parent's project, mode, tools, or permissions.

The characterization coverage now exercises the completed additive seams:
`internal/projecttemplates/user_setup_quest_characterization_test.go` proves a
constructible user template previews and commits through canonical Home/project
creation with typed user provenance, and the reviewed-integration adapter tests
prove installation identity remains separate from local target references.

## Eligible user template

`internal/projecttemplates/testdata/user-setup-quest-eligible/` is a hand-built
fixture, not a copied plugin blueprint or quest. It has no `PluginOwner` and
contains:

- a valid `project_connection` with at least one usable mode; the fixture
  supports both `existing_project` and `new_project`;
- an existing-project extension allowlist and, for new-project creation, a real
  skeleton plus a valid `project_entry` whose extension is accepted;
- a schema-v2 Assistant Program with at least one required primary Home role and
  one required primary project role, valid stages, and reflection bounds;
- a normalized runtime contract that offers `file_only`;
- a usable post-workspace `setup_wizard` containing a required `runtime_mode`
  step and summary. It remains distinct from the five-step pre-workspace quest;
- a known host-reviewed integration key selected when the user creates the
  quest. The integration's own plugin blueprint/program remain registry-owned
  installation expectations, not the local target.

The Templates page does not become an editor for project-connection, Assistant
Program, runtime, or Setup Wizard contracts. A user obtains an eligible target
by importing an already prepared folder or by preparing `template.json` and its
skeleton through the existing local folder workflow. The quest editor reports
specific missing prerequisites (connection mode, constructible entry/skeleton,
Assistant Program, `file_only` runtime mode, or runtime-mode wizard) instead of
manufacturing them or offering a broken launcher.

## Portable declaration and host projection

The on-disk manifest field is distinct from the plugin-only string:

```json
{
  "user_setup_quest": {
    "source": "user_template",
    "attachment_id": "uqatt_<host-generated-id>",
    "schema_version": 1,
    "version": 1,
    "id": "quest_<host-generated-id>",
    "title": "Set up my project",
    "description": "Connect a project and choose how Ori can help.",
    "integration_key": "<reviewed-integration-key>",
    "expected_blueprint_id": "canonical-local-template-id",
    "expected_assistant_program_id": "user-music-team",
    "workspace_launch": {
      "group_title": "Create a Home",
      "group_name": "My projects",
      "runtime_title": "Choose how Ori works",
      "runtime_instructions": "File-only remains available; live control is optional."
    },
    "steps": ["the five ordinary SetupJourneyStep objects in fixed order"]
  }
}
```

`attachment_id`, quest/step IDs, schema/version, target template ID, target
program ID, and step kinds are host generated or host verified. The authoring
request supplies display copy and one reviewed integration key; it cannot
supply user, ownership/source, plugin owner, route, adapter, command, URL,
filesystem path, runtime grant, or permission scope. The embedded identity is
portable inert data, not proof of ownership or lock state.

The source discriminator is `plugin` or `user_template`. Existing plugin URLs,
keys, JSON, relationship hashes, and assistant aliases stay unchanged. A user
selection is keyed by current user + canonical template ID + attachment ID +
quest ID. The current user is obtained once from the host; no query or body can
select another user. User declarations keep `OwnerPluginID` and
`Template.PluginOwner` empty.

Authoring endpoints are template subresources. They expose eligibility,
editable/locked state, normalized declaration, and a quest revision; preview
accepts the same bounded draft but persists nothing. User runtime routes are
source-explicit and delegate to the existing journey handlers after host
scoping. The combined catalog publishes an explicit source and ownership while
retaining the old plugin fields for plugin entries.

## Reviewed dependency versus user target

A selected integration key resolves a host-owned `reviewedintegration.Entry`.
Its expected plugin ID/version/source/artifact, contribution protocol,
blueprint, and program remain the evidence required to install or enable that
integration. Those reviewed blueprint/program IDs are carried separately from:

- target template ID: the selected user library template;
- target attachment ID: its host-generated stable quest attachment;
- target Assistant Program ID: the program embedded in that user template.

The integration adapter verifies the former without comparing them to the
latter. The project resolver uses the latter only after the integration receipt
proves the former. No marketplace quest registers an executor, and a local
quest cannot weaken source, artifact, contribution, platform, enablement, or
review checks.

## User-template provenance

The smallest additive workspace identity is typed, not a fabricated plugin:

- `AssistantProgramKey` retains its existing plugin encoding and equality.
  User keys add an omitted-when-empty `user_template` source and stable
  attachment identity; exactly one provenance form is valid.
- `TemplateProvenance` gains a user-template owner containing the canonical
  template and attachment identity. `PluginOwner` remains nil.
- Home lookup, project link, creation/observation digests, staffing, clone/JSON
  round trips, and canonical file-store hydration compare the typed key.

The Home key is current user + source + owner identity + program. Equal textual
quest/program IDs in plugin and user namespaces, or in two local templates,
therefore cannot share a Home, root, project link, receipt, or grant. Existing
plugin records decode with zero-valued additive fields and retain their exact
key and JSON representation.

## Digests and first-run lock

Two lowercase SHA-256 values have separate jobs:

- **definition digest**: canonical JSON of the normalized embedded
  `SetupJourney` plus stable attachment identity. It covers all display copy,
  IDs, fixed kinds/order, version, integration key, and target references.
- **execution-reference digest**: canonical JSON of the normalized target
  fields consumed by Home/project/runtime/staffing: template/attachment and
  Assistant Program identity/declaration, project connection and entry,
  runtime requirements, Setup Wizard, starter tasks, directory/capability/
  automation requirements, tool plugin references/sources, and declared
  capabilities. Mutable display metadata (template name, description, icon,
  tags, tagline/addons) and the ordinary template Agents editor are excluded.

A quest revision used for optimistic authoring is derived from the currently
normalized attachment (`absent` is a real revision state). Omission preserves
the field. An object is a validated replacement. Explicit `null` removes it
only before a run. A stale revision conflicts. A normalized no-op may succeed
when locked; any changed definition, attachment, target, or execution digest
conflicts. Incrementing `version` or replacing IDs is not an escape hatch.

The first runtime root creation is the permanent lock boundary, including a
root that is never opened, is later dismissed, or completes. SQLite stores one
minimal binding row with current user, template ID, attachment ID, quest ID,
definition digest, execution-reference digest, and root ID. It is lock metadata,
not an editable quest store or version history. Root and binding are claimed in
one SQLite transaction; old plugin rows are untouched.

Every later runtime read/review/commit re-resolves the current manifest and
compares both pinned digests before asking any canonical owner. Missing,
malformed, duplicated, moved, or externally changed data yields a declaration
conflict while preserving the root, receipts, workspaces, and grants.

## Filesystem/SQLite serialization and recovery

All supported manifest mutations and user-quest first-open claims take an
exclusive advisory file lock at a fixed private path under the active template
library. This is cross-process coordination (the existing wake coordinator
uses the same `flock` pattern), not a process-local check-then-write mutex.
Failure to open/acquire the lock or read SQLite binding state denies a
quest-bearing mutation or execution.

While holding that lock:

- save reads the current manifest, checks the optimistic quest revision and
  durable binding, validates the complete effective manifest, then publishes
  one same-directory temporary file with flush/close/atomic rename;
- first open reads and normalizes the published manifest, then atomically
  creates or validates both SQLite binding and root;
- every protected metadata mutation computes before/after execution digests
  and refuses a locked change before publication.

Save writes only the manifest; first open writes only SQLite. There is no
cross-store half-commit to repair: a crash before manifest rename leaves the old
file, a crash after rename leaves the complete new file, and a failed SQLite
claim creates neither binding nor root. The OS releases the advisory lock on
process death. On restart, the manifest and binding digests either match and
resume or mismatch and block. Direct OS writes cannot be prevented from the OS
user; they are detected before runtime owners are read or invoked.

## Mutation and lifecycle matrix

| Path | Before first run | After first run |
| --- | --- | --- |
| Setup-quest subresource save | validated replacement/removal with stale precondition; no-op allowed | normalized no-op allowed; changed/removal rejected |
| Metadata save | unrelated display fields allowed; effective quest/references revalidated | display-only changes allowed; any execution-digest change rejected |
| Tools setter | allowed when effective declaration remains valid | rejected if plugin references/sources change; true no-op allowed |
| Agents setter | allowed; not the Assistant Program editor | allowed; staffing still uses the locked Assistant Program |
| Generic file write/create/rename/delete involving `template.json` | rejected; manifest uses structured writers | rejected |
| Ordinary skeleton file operation | unchanged existing behavior | rejected because scaffold content is pinned by the execution digest |
| Duplicate | ordinary template behavior; a user quest gets fresh attachment, quest, and step IDs | same fresh inert copy; no root/receipt/grant is copied |
| Import | validates an inert embedded declaration and assigns fresh local identity | source lock metadata is never portable or imported |
| Plugin `setup_quest` string/import | no plugin quest copy or editable declaration is synthesized | unchanged/read-only plugin semantics |
| Delete template | allowed only without a run | rejected when a binding exists so resume is not orphaned |
| Recreate same template ID | normal ordinary template create | cannot attach or launch a replacement over the historical binding |
| Move/rename template root outside Ori | detected as missing or identity mismatch; no authority inferred | historical run preserved and blocked until exact data is restored |
| Change configured templates root | catalog may become unavailable; reads stay inert | historical run remains blocked, never rebound to a lookalike |
| Manual folder copy/attachment collision | ambiguous copies rejected until assigned a fresh identity by a supported operation | cannot select or inherit the original run |
| Out-of-band edit/damage | next validated operation sees current data | every runtime guard detects digest/identity mismatch and fails closed |

Unknown or behavior-bearing fields inside `user_setup_quest` are always invalid.
One bad user quest reports diagnostics on its template but does not hide the
rest of the library or plugin catalog.

## Acceptance test matrix

- Valid fixture: blank create, unsaved preview, save, process reload, same
  normalized declaration, zero progress/resource/plugin/grant writes.
- Validator: missing/duplicate/reordered/unknown kinds, IDs, oversized text or
  object, URL/markup/control copy, unknown executable fields, bad references,
  and mixed plugin/user ownership fail closed.
- Scope: equal plugin/user IDs, two users, two templates, foreign run IDs,
  forged source/owner fields, and duplicate attachment IDs never alias.
- Lock: both save/start race orders, two-tab stale save, null/removal, ID swap,
  reference changes, no-op, dismissed/completed/child roots, store reopen, lock
  failure, and raw same-version disk edits.
- Lifecycle: import, duplicate, delete/recreate, missing/moved root, generic
  manifest file routes, metadata/tools setters, and ordinary-template
  regressions follow the matrix above.
- Real owner path: user quest open, reviewed integration check, Home/project
  review and commit, file-only wizard choice, staffing/summary, alternate entry
  point resume, and store reopen reuse one root/Home/project with no implicit
  acceptance or live grant.
- Compatibility: plugin/host-compatibility discovery, old URLs and keys,
  assistant alias reuse, ordinary creation, provenance decoding, and existing
  migration behavior remain unchanged.

## File ownership

- `internal/projecttemplates`: embedded parsing, eligibility, canonical digests,
  optimistic/atomic manifest mutation, and copy/delete policy.
- `internal/setupjourney` and `internal/database`: source-aware selection,
  binding/root transaction, durable validation, and projection metadata.
- `internal/setupjourneyhttp` and `internal/server`: current-user route scoping,
  shared coordinator, resolver composition, and mutation guards.
- `internal/projectconnection` and `internal/workspace`: typed user provenance,
  canonical Home/project/program identity, hydration, and observation.
- `internal/web`: bounded editor/preview and ownership-aware shared links.

No code in this feature may relax the fixed-step validator, action allowlist,
review receipt/idempotency checks, reviewed integration source/artifact checks,
project selection ownership, Setup Wizard authority, staffing scope, or runtime
grant rules.
