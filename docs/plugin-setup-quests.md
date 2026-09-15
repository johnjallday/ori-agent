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

User-owned templates have a separate blank authoring flow under **Templates →
select a user template → Setup quest**. It edits only bounded display copy for
the same five fixed ordered stages and selects one host-reviewed integration.
The template must already have a usable project connection, constructible
project entry/skeleton, current Assistant Program, File-only runtime mode, and
required runtime-mode wizard step. Preview and save are inert. Deliberately
opening the saved quest creates its durable binding and permanently locks its
protected definition; display metadata and the ordinary Agents roster remain
editable. Duplicates and quest-bearing folder imports are rehosted with fresh
local IDs. There is no plugin-quest copy or standalone quest sharing/export
flow. See [User-owned setup quests](user-setup-quests.md).

Catalog discovery performs reads only. Opening a quest may create an inert
progress row; it does not install/enable anything, build a group, create a
workspace, start an application, or grant access. Confirmed consequences still
use the existing revision checks, review receipts and idempotent owner adapters.

Routes are compiled by Ori:

```text
GET /api/setup-quests
GET /api/setup-quests/{pluginID}/{questID}
GET /api/setup-quests/{pluginID}/{questID}/runs/{runID}
GET /api/user-template-setup-quests/{templateID}/{attachmentID}
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

## Host-owned setup quests

Ori can also ship a setup quest for one of its **built-in** templates. The
first is **Set up Email Ops** (`email_ops_setup`), which takes a user from the
goal "set up email" to a linked, readable mailbox. Host quests are compiled
into Ori from `internal/hostquests/`. No plugin, template file, or request
can add, replace, or edit one. The full contract is in
[Host-owned setup quests](architecture/host-setup-quests.md).

A host quest uses a different compiled **shape** from plugin and user quests.
The shape is inferred from its step kinds; there is no field to author:

| Shape | Steps |
| --- | --- |
| Specialist (plugin and user quests) | integration → project → workspace → staffing → summary |
| Account link (host quests) | workspace_create → account_connect → account_link → summary |

The Email Ops steps are:

1. **Review your Email Ops team** opens the shared Workspace creator on its
   Team step. The Inbox role is staffed and locked, because mail access is
   granted to the agent named Inbox. The step completes when an Email Ops
   workspace exists; the quest never creates one itself.
2. **Connect Gmail** reads the Google connection. Settings opens in a new tab
   and **Check again** re-reads it. The quest never starts OAuth.
3. **Link the mailbox** is a reviewed consequence. The review names the
   workspace and account; confirming links the connection's existing Gmail
   credential with read and search only, through the same linker as the
   workspace's own **Connect email** action.
4. **Email Ops is ready** offers the workspace, and inbox triage only when a
   system model can run. Triage is always a separate task the user starts.

Entry points all open the same saved progress:

- The assistant's **Email** capability shows **Set up email** while email is
  not set up and no Email Ops workspace exists. Available and revoked email
  keep their existing actions.
- **Templates → Email Ops → Open Guided Setup**, labelled "Ori built-in ·
  read-only."
- **Create Workspace → Email Ops → Open Guided Setup**.
- The link `/?setup=quest&source=host&quest=email_ops_setup`.
- A quiet **Resume** card in Home's Quests flyout while the quest is started,
  unfinished, not dismissed, and never completed. **Not now** records the
  quest's dismissal, and opening the quest again clears it. Closing the quest
  window only hides it, so an unfinished quest can be resumed from the card.

Routes mirror the plugin family without `children` or `preparation`, and add
one read that never creates progress:

```text
GET  /api/host-setup-quests/{questID}
GET  /api/host-setup-quests/{questID}/status
GET  /api/host-setup-quests/{questID}/runs/{runID}
POST /api/host-setup-quests/{questID}/open | /dismiss
POST /api/host-setup-quests/{questID}/runs/{runID}/open | /dismiss
POST /api/host-setup-quests/{questID}/runs/{runID}/actions/{actionID}
```

The catalog lists host quests with `source: "host"` and `ownership: "host"`.
A built-in template names its quest with `setup_quest`, and the match needs
both sides, so neither the template nor the quest can claim the other alone.

### Why plugin and user quests stay five-step

Plugin and user-template validators require the specialist shape explicitly.
A plugin or user declaration that uses the account-link kinds is rejected, and
the plugin JSON schema is unchanged. Three reasons:

- **The consequences are host-reviewed.** Connecting an account and linking a
  mailbox touch credentials and a vault. Those adapters exist only for Ori's
  own readiness owner, and a declaration cannot point them at another account,
  vault, or workspace.
- **Existing progress stays valid.** Specialist runs, receipts, and review
  tokens were written against the five-step order. Widening what a plugin may
  declare would let an updated declaration change the meaning of saved steps.
- **Older hosts fail closed.** A plugin that required a new shape would need a
  new host feature flag first; `setup_quests_v1` promises five steps.

## Validation

```bash
go test ./internal/plugin ./internal/projecttemplates ./internal/setupjourney ./internal/setupjourneyhttp ./internal/specialist
go test ./internal/hostquests ./internal/server -run 'EmailOps|HostQuest|SetupJourney'
make test-js
PLAYWRIGHT_BASE_URL=http://localhost:8976 npx playwright test tests/plugin-setup-quests.spec.ts tests/email-setup-quest.spec.ts
```

Browser tests mock consequence endpoints and exercise desktop/mobile discovery
from Plugins, the picker and Templates, shared progress, existing-group reuse,
optional preparation, read-only plugin editors and catalog failure/retry. They do not
install plugins or access/control REAPER. Full Go tests, ratcheted lint and
scoped security checks remain the repository's delivery gates.
