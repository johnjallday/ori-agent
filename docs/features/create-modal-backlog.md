# Create-Workspace Modal — Future Ideas Backlog

Bigger improvements to the Create Workspace wizard that are worth their own
PRD/spec rather than a quick patch. Captured 2026-07-13 after the create-modal
polish pass (name-first input, Blank entry agent, briefing role-dot chips,
reuse/create integration, quick-win bundle) that shipped in PR #202.

Live modal source (for whoever picks these up):
- Markup: `internal/web/templates/components/workspaces/create-workspace-modal.tmpl`
- Logic: `internal/web/static/js/modules/sessions.js` (wizard + create flow),
  `internal/web/static/js/modules/project-templates-manage.js` (blueprint picker + briefing)
- Styles: `internal/web/static/css/workspace-construct.css` (tactical skin),
  `internal/web/static/css/sessions.css` (base layout)
- Template agent plan/seed backend: `internal/sessionhttp/template_agents.go`,
  `internal/sessionhttp/project_templates.go`, `internal/sessionhttp/workspace_handler.go`

---

## 1. Smart name suggestions per blueprint

**Problem:** Name is now the first input on step 1 (name-first), but it starts
empty — a blank stare. Picking a blueprint only prefills the *template's* name
(e.g. "Content Production") via `prefillTemplateValue`, which is generic and
collides across workspaces.

**Proposal:** Each blueprint suggests a sensible, unique default name when the
name field is still empty/auto-filled — e.g. Reaper Song → "Untitled Song 3",
Research Project → "Research — <topic?>". Suggestion should:
- never clobber a name the user typed (reuse the existing autofill-guard),
- de-duplicate against existing workspaces (needs a client-visible list or a
  suggest endpoint),
- ideally derive from the blueprint's domain, not just its display name.

**Notes:** Consider a `name_suggestion` field on the template manifest, or a
lightweight `/api/workspaces/suggest-name?template_id=` endpoint that checks
existing slugs. Ties into the folder-slug preview shipped in the quick-win pass.

---

## 2. Blueprint search / filter

**Problem:** The blueprint grid is a flat set of cards (8 built-ins + user
templates). It doesn't scale past ~8–10; there's no way to find one by name or
tag. Templates already carry `tags`.

**Proposal:** Add a search box + tag filter above the grid (reuse the existing
`OriTagFilterBar` / unified-tag components if they fit). Filter built-ins and
"Your templates" together. Keep Blank pinned as the first card.

**Notes:** Only worth building once the library grows (user templates + future
marketplace). Low urgency today; revisit when card count climbs.

---

## 3. Unified post-create "next steps"

**Problem:** After create, several things can happen (open workspace, the
non-blocking "Find tools" panel from #118, auto-started starter tasks, the
"open project" flow for scaffolded templates). It's fragmented.

**Proposal:** A single post-create confirmation / "what now" surface that
consolidates: open the workspace, add tools (find-tools), review seeded agents,
start the first quest. One coherent hand-off instead of scattered affordances.

**Notes:** Should respect the current auto-open behaviors so it doesn't add a
click for the common path. Coordinate with `find-tools-addon-relocation` and the
starter-task onboarding (`template-starter-task-onboarding-prd`).

---

## Done in the polish pass (for context — not backlog)
- Name is the first input on step 1, editable on step 2 (single relocated field).
- Blank blueprint seeds a reviewable "Workspace Manager" entry agent.
- Briefing AGENTS row renders as role-hued dot chips (matches ADD-ONS).
- Template Agents panel: reused agents shown compact/locked with a
  "Make a workspace copy" fork; master toggle relabeled "Attach".
- Quick-win bundle: template-aware model-status copy, Blank briefing copy,
  optional description, inline name validation, folder-slug preview,
  Enter-to-advance / Enter-to-create.
