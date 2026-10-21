# Workspace build sessions ("Build with your assistant")

The Personal Assistant builds a workspace **with** the user: Create Workspace opens with
a conversation pane beside the wizard, the assistant fills the form as the user talks, and
the user confirms with ordinary Create or, for an attached File Janitor folder,
the separately disclosed **Create & prepare review** action. PRD: `tasks/prd-build-with-your-assistant.md`.
HTTP contract: [API reference](../api/API_REFERENCE.md#workspace-build-sessions-api).

## The one rule: the draft is the create request

A build session holds a draft that is exactly the body `POST /api/workspaces` will send
(`personalassistant.BuildDraft`: blueprint, name, description, inputs, placement, color,
tags, bootstrap, project connection, team keys). There is no second model of "what the
workspace will be":

```
user types ─► POST …/turns ─► model proposes a patch ─► host validates each field
                                                         │
             wizard renders it  ◄── session.draft ◄──────┘   (refusals go back to the model)
             through its own setters
                   │
user edits form ─► PATCH …/draft (said back in the transcript, seen on the next turn)
                   │
user: "create it" / clicks Create ─► POST /api/workspaces { …draft, build_session_id }
```

- The **model proposes**; it never writes the draft directly and never creates anything.
- The **host validates** every proposed field against the same facts the Create path uses.
- The **wizard renders** the accepted fields through its existing setters (card click,
  input events, the team draft's mutators), so every downstream gate and prefill runs as if
  the user had done it.
- The **user confirms** with ordinary Create. For attached File Janitor setup,
  `create_now` opens the current host review instead of supplying folder/monitoring
  consent; see [the reviewed preparation contract](file-janitor-creation-flow.md).
  `ready` means **Ready to create**, not operational readiness. For other builds,
  `create_now` just triggers the ordinary submit, and a closed gate is reported in the
  pane ("Not yet — …") instead of submitting. The host never honours `create_now` on the
  first turn: the user always sees a filled form before anything is created.

## Server

| Piece                                          | File                                                                               |
| ---------------------------------------------- | ---------------------------------------------------------------------------------- |
| Handlers, turn orchestration, finish-on-create | `internal/sessionhttp/workspace_build_session.go`                                  |
| Model request/response, prompt, retries        | `internal/sessionhttp/workspace_build_session_model.go`                            |
| Field validation (FR15)                        | `internal/sessionhttp/workspace_build_session_validate.go`                         |
| Form edits, abandon, slug occupancy            | `internal/sessionhttp/workspace_build_session_draft.go`                            |
| Add folder: choices, attach, detach, the link  | `internal/sessionhttp/workspace_build_session_folder.go`                           |
| Session types and bounds                       | `internal/personalassistant/workspace_build_types.go`                              |
| Sidecar store (+ Windows variant)              | `internal/personalassistant/workspace_build_store*.go`, `workspace_build_codec.go` |
| Wiring, model resolution                       | `internal/server/workspace_build.go`                                               |
| Provenance (`BuildSummary`)                    | `internal/workspace/template_provenance.go`                                        |

Routes live under `/api/workspaces/build-sessions…` and are dispatched inside
`HandleWorkspaces` (the `/api/workspaces/` subtree) rather than registered separately, so
Go 1.22's ServeMux never sees two conflicting patterns.

### Availability and the model

Build mode is available when the assistant relationship is `active` or `paused` and a
model resolves. Resolution: the assistant's global agent profile (its provider and
model; an empty provider is inferred only from `claude-`/`gemini-`/local model names,
and `anthropic` is read as `claude`), then the system model. The same resolver is used
for every turn.

Each turn is one model call, bounded at 60 s, temperature 0.2:

- Providers with structured output get a strict schema (`llm.GenerateSchema`) in which
  every field is present and `patch.set` lists the fields actually being set. Others get
  `Chat` with a JSON-only instruction, `llm.StripCodeFence`, and a strict decode
  (`DisallowUnknownFields`).
- A reply that neither changes the form nor asks a question is retried once with a
  nudge; a second one is replaced with the fixed "Which of these is closest?" and three
  blueprint chips. A model failure keeps the user's turn and offers **Try again**.
- The prompt carries the assistant's name, the job, the catalog (at most 60 blueprints,
  ranked by overlap with the conversation), saved agents and groups, the current draft,
  and the last turn's refusals. The Personal HQ blueprint is never offered.

### Validation (FR15)

Each proposed field is checked independently; accepted fields apply, refused ones are
recorded in `rejections` and shown to the model on its next turn. The pane says so too:
"(The form didn’t take X yet — I’ll adjust it.)".

| Field                   | Accepted when                                                                                                                                                                                          | Refusal reason                                                                                                                                                |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Blueprint               | Blank, or a catalog id this user can create from (not retired, not blocked by readiness). A namespaced plugin id may be named by its bare suffix. A new blueprint clears what belonged to the old one. | `not_available`                                                                                                                                               |
| Name                    | Non-empty, ≤ 80 characters, not a name the create path would refuse as taken                                                                                                                           | `empty`, `too_long`, `name_taken`                                                                                                                             |
| Description             | ≤ 1,000 characters                                                                                                                                                                                     | `too_long`                                                                                                                                                    |
| Inputs                  | The blueprint declares inputs, each id is one of them, each value passes the create path's own validator; not with an existing project                                                                 | per-input message                                                                                                                                             |
| Placement (`parent_id`) | Empty, or one of the user's groups; never for a blueprint that is always created inside its own group                                                                                                  | `not one of the user's groups`, `this blueprint is always created inside its own group`                                                                       |
| Folder question         | The blueprint can link an existing folder (the folder itself is chosen on the form)                                                                                                                    | `this blueprint cannot link an existing folder`                                                                                                               |
| Team                    | Roles are the blueprint's; `assign` names a saved agent; `create` names ≤ 100 characters and a model the app can resolve; agentless only for Blank                                                     | `not one of this blueprint's roles`, `not one of the user's saved agents: <name>`, `that model is not available`, `only a Blank workspace can have no agents` |
| Tags                    | Up to 8, each ≤ 32 characters (extras dropped)                                                                                                                                                         | —                                                                                                                                                             |
| Color                   | One of the wizard's swatches                                                                                                                                                                           | `not one of the offered colors`                                                                                                                               |

Without the catalog check an unknown `template_id` would silently create a Blank
workspace, which is why it is mandatory.

### Honesty in the transcript

The model's words are not trusted to describe the form:

- **Refusal note**: refused fields are named once after the reply.
- **Team receipt**: after a turn that changes the team, the host writes what is actually
  on the form ("On the form: Content Lead — a new agent, “Content Lead”."). A model that
  says "Assigned Luna" while proposing a new agent is corrected in the same bubble.
- **Saved agents**: a "create" whose name is already a saved agent is recorded as an
  assignment of that agent — what the form does — so the receipt never claims a new one.
- **Provenance**: "How this was set up" is written from the create request itself. The
  team line comes from its `role_staffing`, not from the model's explanation. The
  assistant's reason for a section the user later changed on the form is dropped. The
  automatic re-staff turn is never recorded as the user's request.
- **Edits during a turn**: fields the user changed on the form while the model was
  working keep the user's value; the rest of the reply applies.

### Attached folder (the pane's Add folder)

The pane's composer has the assistant composer's **Add folder**: a known-folder chip, the
native picker (a dialog the server opens), or a typed path that `/api/folder-picker/select-path`
checks first and answers with a token. The build endpoints never accept a path.
`POST …/{id}/folder` hands the choice to the composer's `FolderObservationService`
(`WorkspaceBuildDeps.Folders`, wired lazily in `internal/server/workspace_build.go`) with the
build itself as the selection's draft target (`build:<session id>`, a draft id no composer
request can name, since the composer's must parse as a UUID), and stores the resulting path-free
`foldercontext.Observation` on the session as `folder`. The attach is said in the transcript
as the user's own act ("You attached the folder “X”. It will be linked to the workspace when
you create it."), which the model hears as a form edit. The system prompt then carries the
observation inside `<folder_observation>…</folder_observation>` — names, kinds, counts,
markers, last-changed dates, coverage; never ids, never contents — and tells the model that
Create links the folder and that `ask_folder` is never to be set (validation refuses it with
`a folder is already attached; it is linked at Create`). `POST …/{id}/folder/detach` clears it.

Create (`POST /api/workspaces` with `build_session_id`) resolves the folder's root through
the service's own hold on the selection (`ResolveRoot`, the same 30-minute window the
composer has) before anything is created; a lost pick is a `409` with
`conflict.type: build_folder`, and nothing is left behind. Once the workspace folder exists,
the attached folder is linked in place as a directory reference (`AttachLinkedDirectory`) and
becomes the primary directory unless the create already set one outside the workspace folder
(an attached project keeps it; a scaffold inside the workspace folder is superseded). The link
runs after the scaffold or attached project and before the template's tool step, which reads
and saves the workspace itself. The `BuildSummary` records it as a `folder` decision.

### A recognized folder (narrated setup)

An attached folder is also **recognized**, read-only from the scan the attach already made
(`FolderObservationService.Recognize`, `internal/personalassistant/folder_build_recognition.go`):
`folder.recognition` says `project` (with the capability row's label, e.g. "<integration>
project", the session-file count, the suggested project blueprint, the Home provider and whether
that Home exists), `collection` (several projects: set up from Home's Explore a folder), or
`none`. A folder the user said never to ask about again is `none`. The pane says one line from
it (`folder-recognition-lines.js`, the same words Home's card uses); the prompt carries it as
`<folder_recognition>` reference data.

- **Catalog**: the build catalog never contains a Home carrier (a template with
  `ProgramHomeOwner`; `workspaceBuildCatalogEntries`, `offerableCatalog`). The Group creator keeps
  them.
- **Plan**: for a recognized single project every response carries `plan`
  (`FolderObservationService.BuildPlan`): the host's own `FolderSetupPlan` lines and digest, as
  Home's card plans the same folder (one plan, one digest from either entry,
  `folder_plan_identity_test.go`), the `needs` (integration and Home provider, each `ready`,
  `install`, `enable` or `update`), the Home (`new`/`existing`), the placement and the evidence —
  or `unavailable` with the reason and the plugin it waits on. Never stored. The host writes the
  needs once into the transcript (`Needs: … (installed) · … (install)`), and the pane's draft bar
  becomes **Plan so far**. With a ready plan the model may set only the name, description, goal
  and one extra saved teammate; everything else is refused as `set by the plan`, and the prompt
  carries `<setup_plan>` instead of the catalog.
- **A missing Home plugin**: a plan that waits on its Home's plugin (`provider_not_installed`,
  `provider_unverified`) sets `install_provider` on the response, and the pane offers
  **Install <plugin>** under the plan. `POST …/{id}/home-provider {version}` previews the release
  Home's folder card would install (version, source, the plugin manager's trust report), which
  stands in for the conversation and the composer while it is open; **Install and continue** sends `{confirm: true, reviewed_version}` and installs exactly that release
  through the same reviewed-release service (`buildHomeProviders` over
  `folderHomeProviderSetup`). The cached plan facts are dropped (`folderSetupHost.forgetAll`), so
  the response's plan is ready and says the plugin is installed; the transcript gets "Installed
  <plugin> <version>. Hire and start can run the plan now." The client names only the build. While the
  plan waits, the prompt tells the model the form offers the install, not to choose Blank or
  another blueprint unless the user asks, and not to ask about the group, and no
  `group_prerequisite` is sent (the plan sets the group up once installed).
- **Review in place**: "Review or edit draft" opens the plan review inside the dialog (the
  lines read-only, name and description editable, "Why this suggestion?"), not the wizard. While
  it is open it stands in for the conversation, with its own Hire and start.
- **The hire**: a ready plan carries `hire` (who the setup hires: `create`, `bind` or `pending`,
  the agent's name, role and model) and `brief_default`, outside its digest, read from the same
  host plan as Home's card. The pane's plan column is titled by the hire ("Hire <agent> for
  <project>"), with the agent's badge and role and the one question "What should it do with
  it?" above the "You'll get" sketch; the folder review, which a recognized folder opens
  directly, shows the same block (`renderReviewHire`, `#workspaceBuildPlanReviewHire`), titled the
  same, and both fields edit one brief. The field starts from the draft's goal, else
  `brief_default`, and keeps what was typed for this build. The button is **Hire and start**
  (**Set it up** for a plan that names no hire). The model is told the plan column answers the
  setup request: it never names the hired agent, counts the plan's steps or describes its
  controls, and its `<setup_plan>` carries no hire or brief.
- **Hire and start**: `POST …/{id}/setup {version, plan_digest, entry_name?, brief?}` writes the build's
  `setup` claim (offer, digest, request id) and runs **Home's own setup journey** on the build's
  held folder: the build's review target is `build:<session id>`, and Home's setup reads that
  folder only while this build stands behind it (`ConfigureBuildReviews`). The session is
  `preparing`; `GET …/{id}` returns `setup_run` (the run's lines with their states, a stop reason,
  project-file candidates). Refusals: `plan_changed` (the plan the user confirmed is no longer
  the host's), `no_plan`, `review_pending` (another folder setup waits on Home),
  `declined`, and `build_folder` (the pick is lost). An unnamed draft is named after its folder,
  as the plan says (the model never has to name it first). A stopped run leaves the build open with
  **Retry**; a finished one closes it as `created` with `created_workspace_id`,
  `home_workspace_id` and the host's `setup_receipt` ("Created <Home> and <workspace> inside it.
  Linked folder “<folder>”."), applies the draft's description and extra teammate (and its goal
  only when the run had no brief: the brief is the workspace's goal), and writes "How this was set
  up" from the plan lines and the receipt. The brief goes to the run as on Home's card
  (`FolderSetupInput.Brief`), and the hired agent's first look starts when the run finishes, under
  the same consent. The journey links the folder as a directory reference, as on Home (it does
  not mark it primary).
- **Pick lifetime**: `folder_expires_at` is when the server stops holding the pick (30
  minutes after the look). Under 5 minutes the pane counts down; once expired, Hire and start is
  disabled and **Add the folder again** is offered.
- **Map**: when the run finishes the dialog closes with the receipt as a toast, and the Map's
  automatic layout places the result (a new Home as a new district with the workspace inside, a
  workspace joining an existing Home inside it); nothing is asked and nothing is written to the
  layout. The setup's receipt and the hired agent's report say where the building sits in an
  "On the Map" row whose **Move** opens Home with that building selected and Move on
  (`/?move=<workspace id>`, `OriWorkspaceMap.beginMove`); Escape or leaving keeps the automatic
  layout. Home's Explore a folder and the assistant's Add folder end the same way.

### A blueprint that needs a group

A blueprint whose group policy is `required` sets `needs_home`; the draft bar names the group
apart from the workspace (`Group: <label> (new|existing) · Workspace: <name> · …`). Every response
also carries `group_prerequisite`: the form's own group-requirement projection
(`grouprequirements.Evaluate` through `buildTemplateGroupRequirementPlan`), only while the build
is open and when no ready plan sets the group up itself, nor one waiting on the Home plugin the
pane offers to install. The pane says it before Create:

- `home_creation_review_required`: "This blueprint needs <Home> first. Ori prepares the group
  with your confirmation, then creates the workspace inside it." and **Prepare the group first**,
  which runs the form's Home review and commit (`/api/workspaces/group-requirement/home/…`) in one
  press, then "Group ready: <Home>." and a **Create it** chip. Create refuses in the pane until the
  group is ready. Two confirmations, nothing else created by the first.
- `ready_grouped`: "Group ready: <Home>. The workspace is created inside it."
- `home_required` and other states are said, never offered.

The prompt rule says the same; a `parent_id` for such a blueprint is refused as `this blueprint is
always created inside its own group`. Choosing **Group** while a folder is attached brings the
pane back with "The attached folder will not be linked to a group." and **Keep building the
workspace** / **Continue as a group** (which removes the folder from the build).

### Reviewed File Janitor preparation

`janitor-review` reads the canonical edited draft/team and live folder hold;
`janitor-prepare` explicitly confirms its owner/version/root/terms revision.
The normal creator's gates still run before the first write. Attaching, ordinary
Create, and model output grant no File Janitor access. Manual monitoring-off is
explicit completed setup, not missing consent; opting in binds the actual
watch/debounce/exclusion and local daily schedule. Preparation creates/reuses
`Filed/` and performs one top-level metadata scan, never file-content inspection
or file-action approval.

A preparing claim reserves stable operation/workspace IDs and freezes edits and
late turns. Creation uses the actual draft, not a default one-role proposal.
Workspace/profile provenance proves exact replay; uncertain partial creation is
not retried by name. Canonical grant/mode/scan records own downstream retry.
`janitor-defer` retains the claim as abandoned, stops only provably matching
preparation, and permits a separate new build without making the old claim
create-capable again. Recovery links use a proven canonical folder slug, never
an unconfirmed reservation ID. Full request/recovery details live in the
[reviewed creation contract](file-janitor-creation-flow.md).

### Storage

Schema **3** retains the filename `workspace-build-v1.json`. Genuine v1 and v2 data
load without inventing preparation consent; v1 carrying v2 authority, a v1 or v2 file
carrying a folder recognition, a setup claim, a Home id or a setup receipt, and unknown
schemas are refused conservatively. One sidecar file per HQ: `<HQ folder>/.ori/workspace-build-v1.json`, written through the
Personal Assistant knowledge store (locked, atomic replace, a Windows variant). Bounds:

- at most **one open or preparing build** per user; creating while one exists
  returns it (`resumed`); preparing claims are frozen and do not expire as open drafts;
- the transcript keeps the last **40 entries / 32 KiB**;
- an open build untouched for **7 days** is read as abandoned;
- at most **8 builds** are kept, and a created or abandoned one is settled to its id,
  status, name, and counters (its conversation, draft, and team state are dropped), so
  the file stays far below its 512 KiB limit; a settled preparation additionally
  retains its bounded path-free claim (IDs/digests/mode/time), never a path/token;
- every host-written line is cut to the store's 2,000-character bound, and the lists a
  model can grow (refusals, saved teammates) are capped;
- an attached folder is kept only as its path-free observation (at most 8 KiB, validated
  on every write) and dropped when the build settles; the server's hold on the folder
  itself lasts 30 minutes and is never written anywhere;
- every mutation is versioned; a stale `version` is a `409` the client reloads from.

On create, `build_session_id` marks the session `created` and stores a `BuildSummary` in
the workspace's template provenance. An unknown or ordinary closed id does not block ordinary create. A retained
accepted preparation claim does: it must use its exact confirm/recovery boundary
and cannot be bypassed through ordinary create.

## Client

| Piece                                                          | File                                                                      |
| -------------------------------------------------------------- | ------------------------------------------------------------------------- |
| The pane (transcript, chips, composer, status line, collapse)  | `internal/web/static/js/modules/create-workspace-build-pane.js`           |
| Plan lines and the Recognized / Needs lines (shared with Home) | `folder-plan-lines.js`, `folder-recognition-lines.js`                     |
| Transport, apply-to-wizard, tags, draft sync, create           | `internal/web/static/js/modules/sessions.js` (the `WorkspaceBuild` block) |
| Eligibility, step math, patch extraction                       | `internal/web/static/js/modules/workspace-creator-state.js`               |
| Team patch apply / serialize / restore                         | `internal/web/static/js/modules/create-workspace-team-draft.js`           |
| Today "Finish building …"                                      | `internal/web/static/js/modules/personal-assistant-home.js`               |
| Assistant composer / home-assistant hand-off                   | `internal/web/static/js/modules/dashboard.js` (`openBuildWithAssistant`)  |
| "How this was set up"                                          | `internal/web/static/js/modules/workspace-command.js`                     |

- **Eligible openers**: Home "New Workspace" (`home_cockpit_create`), the Map's create
  pad (`workspace_map_build`, placement preserved), the Workspaces hub
  (`workspace_hub_create`), and the assistant's composer (`personal_assistant_ask`, the
  sentence becomes the first turn). Every other opener, import mode, a fixed kind, and
  Group all keep the manual wizard.
- **Applying a turn**: the fields listed in `applied` go through the wizard's own setters
  inside an "apply depth" so the resulting input/change events are not mistaken for the
  user's. Each gets a "Chosen by <Name>" tag, removed on the user's first edit of that
  field. The wizard advances to the furthest step the draft has earned under its own gates
  and never takes focus from the composer.
- **Form edits**: user edits are sent as a debounced `PATCH …/draft`; the assistant's own
  effects are sent with `sync: true`. A blueprint the user switches triggers an automatic
  turn ("(I changed the blueprint)", sent with `auto: true`, and queued until the turn in
  flight finishes) so the assistant re-staffs. The picker's own
  selections (its default after a reset or a catalog load) carry `programmatic: true` on
  `workspace-template-selected`, are never treated as the user's edit, and put the
  assistant's blueprint back if the late load reset it.
- **Resume**: an open build found on open asks "Resume building <name>?". Until the user
  answers, nothing on the form is written to the paused draft, and the dialog's create
  request does not name the build (a workspace made by hand meanwhile is not tied to it).
  Resume restores the draft, the team state, the step, and the transcript; Start over
  abandons and begins again.
- **Fallbacks**: not hired or no model → the manual wizard, and no build request beyond
  the two reads (`GET /api/personal-assistant`, `GET …/availability`). A build that becomes
  unavailable mid-session says "I can’t help right now — the form still works." and
  collapses, keeping the form.
- **Accessibility**: assistant messages are announced through a polite status line; chips
  are buttons in a `group` labelled by the question; focus starts in the composer, returns
  there after each turn and pane action, and never leaves a form field the user is in;
  the pane follows the wizard footer in document order; `prefers-reduced-motion` turns off
  the highlights and the staggered roster.

## Privacy (FR33)

The model receives only what the user typed in the pane, the draft, the catalog,
saved-agent, and group metadata above, and — when the user attached a folder — that
folder's observation: names, kinds, counts, markers, and dates, never file contents, paths,
or ids. It never receives notes or other workspaces' data. Logs and events carry ids,
counts, and field or section names, never message text, draft values, or folder names.

## Tests

- Go: `internal/sessionhttp/workspace_build_session_test.go` (fake provider on both the
  structured and plain paths, the validation table, retry/substitute, stale versions, form
  edits, create → provenance), `workspace_build_session_folder_test.go` (the chooser,
  attach/detach, the prompt, the refused folder question, the create-time plan and link),
  `internal/personalassistant/workspace_build_store_test.go` (persistence, bounds, expiry,
  the attached folder), `folder_observation_test.go` (`ResolveRoot`),
  `internal/agenthttp/home_assistant_ask_test.go` (`build_workspace` replaces
  `create_workspace` without writing the store). Narrated setup:
  `workspace_build_session_recognition_test.go`, `workspace_build_session_setup_test.go`,
  `workspace_build_session_group_test.go`, `internal/personalassistant/folder_build_*_test.go`,
  `folder_plan_identity_test.go`, `internal/server/workspace_build_test.go`.
- JS: `create-workspace-build-pane.test.js`, `sessions.test.js`,
  `workspace-creator-state.test.js`, `create-workspace-team-draft.test.js`,
  `personal-assistant-home.test.js`, `workspace-command.test.js`.
- Playwright: `tests/create-workspace-build-mode.spec.ts` with a scripted fake of the
  build endpoints (golden path, form-edit acknowledgement, resume, manual fallbacks, the
  `build_workspace` hand-off). Run it against a fresh sandbox.
  `tests/narrated-folder-setup.spec.ts` fakes nothing and sends no model turn: both entries on
  a song folder, through Hire and start, the automatic Map placement and its Move link, against
  `scripts/narrated-folder-demo.sh` (its header has the commands).
- Live demos: `scripts/demo-build-assistant.mjs` drives the real pane against a demo
  server with a real model; `scripts/smoke.sh build-session <url> seed` hires an assistant
  and seeds two saved agents and a system model (by default the Codex CLI with
  `gpt-5.6-luna`, which needs the demo server started with `CODEX_HOME="$HOME/.codex"`).
