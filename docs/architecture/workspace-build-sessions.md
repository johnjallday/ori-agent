# Workspace build sessions ("Build with your assistant")

The Personal Assistant builds a workspace **with** the user: Create Workspace opens with
a conversation pane beside the wizard, the assistant fills the form as the user talks, and
the user confirms with the ordinary Create. PRD: `tasks/prd-build-with-your-assistant.md`.
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
- The **user confirms** with the ordinary Create. `ready` from the model is advisory only;
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
| Placement (`parent_id`) | Empty, or one of the user's groups; never for a blueprint that sets up its own group                                                                                                                   | `not one of the user's groups`, `this blueprint sets up its own group at Create`                                                                              |
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

### Storage

One sidecar file per HQ: `<HQ folder>/.ori/workspace-build-v1.json`, written through the
Personal Assistant knowledge store (locked, atomic replace, a Windows variant). Bounds:

- at most **one open build** per user; creating while one is open returns it (`resumed`);
- the transcript keeps the last **40 entries / 32 KiB**;
- an open build untouched for **7 days** is read as abandoned;
- at most **8 builds** are kept, and a created or abandoned one is settled to its id,
  status, name, and counters (its conversation, draft, and team state are dropped), so
  the file stays far below its 512 KiB limit;
- every host-written line is cut to the store's 2,000-character bound, and the lists a
  model can grow (refusals, saved teammates) are capped;
- every mutation is versioned; a stale `version` is a `409` the client reloads from.

On create, `build_session_id` marks the session `created` and stores a `BuildSummary` in
the workspace's template provenance. An unknown or closed id never blocks the create.

## Client

| Piece                                                         | File                                                                      |
| ------------------------------------------------------------- | ------------------------------------------------------------------------- |
| The pane (transcript, chips, composer, status line, collapse) | `internal/web/static/js/modules/create-workspace-build-pane.js`           |
| Transport, apply-to-wizard, tags, draft sync, create          | `internal/web/static/js/modules/sessions.js` (the `WorkspaceBuild` block) |
| Eligibility, step math, patch extraction                      | `internal/web/static/js/modules/workspace-creator-state.js`               |
| Team patch apply / serialize / restore                        | `internal/web/static/js/modules/create-workspace-team-draft.js`           |
| Today "Finish building …"                                     | `internal/web/static/js/modules/personal-assistant-home.js`               |
| Ask tab / home-assistant hand-off                             | `internal/web/static/js/modules/dashboard.js` (`openBuildWithAssistant`)  |
| "How this was set up"                                         | `internal/web/static/js/modules/workspace-command.js`                     |

- **Eligible openers**: Home "New Workspace" (`home_cockpit_create`), the Map's create
  pad (`workspace_map_build`, placement preserved), the Workspaces hub
  (`workspace_hub_create`), and the assistant's Ask tab (`personal_assistant_ask`, the
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

The model receives only what the user typed in the pane, the draft, and the catalog,
saved-agent, and group metadata above — never folder contents, notes, or other
workspaces' data. Logs and events carry ids, counts, and field or section names, never
message text or draft values.

## Tests

- Go: `internal/sessionhttp/workspace_build_session_test.go` (fake provider on both the
  structured and plain paths, the validation table, retry/substitute, stale versions, form
  edits, create → provenance), `internal/personalassistant/workspace_build_store_test.go`
  (persistence, bounds, expiry), `internal/agenthttp/home_assistant_ask_test.go`
  (`build_workspace` replaces `create_workspace` without writing the store).
- JS: `create-workspace-build-pane.test.js`, `sessions.test.js`,
  `workspace-creator-state.test.js`, `create-workspace-team-draft.test.js`,
  `personal-assistant-home.test.js`, `workspace-command.test.js`.
- Playwright: `tests/create-workspace-build-mode.spec.ts` with a scripted fake of the
  build endpoints (golden path, form-edit acknowledgement, resume, manual fallbacks, the
  `build_workspace` hand-off). Run it against a fresh sandbox.
- Live demos: `scripts/demo-build-assistant.mjs` drives the real pane against a demo
  server with a real model; `scripts/smoke.sh build-session <url> seed` hires an assistant
  and seeds two saved agents and a system model (by default the Codex CLI with
  `gpt-5.6-luna`, which needs the demo server started with `CODEX_HOME="$HOME/.codex"`).
