# Plugin-owned setup quests

A setup quest is a **pre-workspace declaration**, not a plugin executable or a
workspace's setup wizard. Ori renders and executes its bounded host primitives:

1. Verify the integration (with separate install/replacement and enable reviews).
2. Build or reuse its canonical group/Home.
3. Optionally prepare the application; file-only work remains supported.
4. Create or import a workspace through the shared creator.

The template's existing `setup_wizard` continues **after a workspace exists**.
Project files, staffing, operating mode, permissions and exact-project live
verification keep their existing owners and confirmations. Putting workspace
creation inside `setup_wizard` would make setup depend on the workspace it must
create.

## One saved setup, multiple entry points

- **Plugins → Guided Setup** opens the plugin's quest.
- **Create Workspace → select its template → Open Guided Setup** opens the same
  quest. Merely selecting a template does not start it, and ordinary template
  creation remains available.
- **Templates → select a template → Open Guided Setup** shows its quest title,
  description and ownership, then opens the same saved setup. Plugin templates
  and quest declarations are read-only; user-owned templates retain their normal
  editors. Loading/failure/missing-quest states remove the launch link, and a
  failed catalog read can be retried without blocking the template library.
- The accepted assistant's setup action is an alias of that quest. None of
  the other entry points requires an accepted assistant offer or creates one.

A visual editor for user-owned quests is separate future work, captured in
[Issue #464](https://github.com/johnjallday/ori-agent/issues/464). This change does
not add quest-authoring controls or silently duplicate plugin declarations.

Catalog discovery performs reads only. Opening a quest may create an inert
progress row; it does not install/enable anything, build a group, create a
workspace, start an application, or grant access. Confirmed consequences still
use the existing revision checks, review receipts and idempotent owner adapters.

Routes are compiled by Ori:

```text
GET /api/setup-quests
GET /api/setup-quests/{pluginID}/{questID}
GET /api/setup-quests/{pluginID}/{questID}/runs/{runID}
```

The scoped root also exposes the existing `open`, `dismiss`, `children`,
`runs/{runID}/preparation`, and `runs/{runID}/actions/{actionID}` surfaces with the
same HTTP methods and bounded request bodies as the assistant alias. IDs in a
path select installed data only. Current-user identity comes from the host;
bodies cannot select an owner, plugin, quest, source, adapter or permission scope.
Discovery returns `plugin_id`, `id`, title/description, qualified `template_id`,
and `ownership` (`plugin` or `host_compatibility`). It never returns a
plugin-authored route or executor.

## Authoring contract

In `.ori-plugin/plugin.json`:

- Include `setup_quests_v1` in `requires_host_features`, retaining other required
  features. Older hosts must refuse this contribution, not ignore the new block.
- Add `setup_quests`, at most eight declarations. Each uses schema/version `1`
  initially, a stable unique ID, plain display copy and the existing five fixed
  step kinds. The launch screens above are a view over those canonical owners,
  **not** a new arbitrary step executor.
- Each quest names one blueprint contributed by this same plugin and the
  expected assistant-program ID. That blueprint's manifest must explicitly set
  `"setup_quest": "<quest-id>"`, contain the matching assistant program, and
  declare project connection support. Foreign, missing or orphaned references
  reject resolution.

The declarative schemas are
[`setup-quest-v1.schema.json`](../internal/plugin/schema/setup-quest-v1.schema.json)
and
[`workspace-surface-v1.schema.json`](../internal/plugin/schema/workspace-surface-v1.schema.json).
The Go normalizer additionally enforces serialized byte limits, plain text and
cross-document ownership. Unknown fields—including commands, URLs, routes,
adapters and `owner_plugin_id`—are rejected. No template reference can import a
quest from another plugin.

V1 discovery/execution is restricted to **host-reviewed integrations**. The
installed owner's integration key, blueprint and program must match its reviewed
registry entry. Installing an arbitrary marketplace contribution with a quest
block does not add an executor or enter the reviewed catalog. Disabling a plugin
or losing installation verification cannot bypass the integration gate.

## REAPER rollout and compatibility

Published REAPER **v0.5.0 does not declare a quest**. Ori provides a clearly
labeled compatibility setup for that version and pre-install bootstrap. Its
frozen original declaration lives at
[`internal/specialist/compatibility/reaper-setup.json`](../internal/specialist/compatibility/reaper-setup.json),
rather than inline in the specialist registry.

For the first plugin-owned release, the publisher can use that JSON object as
the `setup_quests[0]` declaration, add `setup_quests_v1`, and add
`"setup_quest": "reaper_setup"` to
`blueprints/reaper-song/template.json`. Keep the original quest version and step
IDs when extracting the unchanged definition. Retain the template's
`setup_wizard`, runtime requirements and team/program declarations. A release's
plugin/blueprint versions and Ori's reviewed source/artifact identity are
separate contracts and must be updated through the normal reviewed-release
process.

This host change **does not publish a plugin release, change the reviewed v0.5.0
pin, or rewrite anyone's installed plugin**. Plugin ownership is reported only
when an installed contribution actually supplies a valid declaration. A declared
but invalid/missing quest does not silently revert to compatibility copy.

## Progress and migration boundaries

New progress is keyed by current user plus exact plugin/quest identity, using a
reserved internal namespace in the existing durable root store. It is not an
assistant relationship. The server reuses one unambiguous legacy root belonging
to that same user and the compiled legacy specialist/quest mapping. Its immutable
identity, children, timestamps, review receipts, operation journal and linked
resources are not copied or reassigned. Multiple matching legacy roots fail
closed instead of picking one or creating another group.

Changing entry points therefore does not repeat completed consequences. A child
inherits the shared integration/Home identity, **not** its parent's project
folder, selected mode, team or permissions. Canonical owners still derive all
readiness. Unsupported declaration-version changes remain blocked until an
explicit host migration exists; saved progress remains intact.

## Validation

```bash
go test ./internal/plugin ./internal/projecttemplates ./internal/setupjourney ./internal/setupjourneyhttp ./internal/specialist
make test-js
PLAYWRIGHT_BASE_URL=http://localhost:8976 npx playwright test tests/plugin-setup-quests.spec.ts
```

Browser tests mock consequence endpoints and exercise desktop/mobile discovery
from Plugins, the picker and Templates, shared progress, existing-group reuse,
optional preparation, read-only plugin editors and catalog failure/retry. They do not
install plugins or access/control REAPER. Full Go tests, ratcheted lint and
scoped security checks remain the repository's delivery gates.
