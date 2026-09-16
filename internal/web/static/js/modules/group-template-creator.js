// Group Templates inside the shared Workspace / Group creator.
//
// sessions.js stays the one modal controller. This module owns only the
// Group-template chooser and the managed Home review/commit protocol against
// /api/workspaces/group-templates. General keeps its reviewed Group Manager
// roster; a managed template creates or reuses exactly one program Home and
// nothing else. All state lives on the creator context, so a new dialog
// generation can never be touched by a stale response.
(function () {
  'use strict';

  const LIST_URL = '/api/workspaces/group-templates';
  const REVIEW_URL = '/api/workspaces/group-templates/review';
  const COMMIT_URL = '/api/workspaces/group-templates/commit';
  const GENERAL_ID = 'general';
  const USABLE = ['creatable', 'reusable'];

  function isManaged(entry) {
    return entry?.kind === 'managed_home';
  }

  function isReusable(entry) {
    return isManaged(entry) && entry?.availability?.state === 'reusable';
  }

  function isSelectable(entry) {
    return Boolean(entry) && (!isManaged(entry) || USABLE.includes(entry?.availability?.state));
  }

  // Mirrors the server's declaration rule for Home display names.
  function nameProblem(value) {
    const name = String(value || '').trim();
    if (!name) return 'Group name is required';
    if (new TextEncoder().encode(name).length > 120) return 'Group name must be 120 bytes or fewer';
    if (/[/\\]/.test(name) || name.toLowerCase().includes('://')) {
      return 'Group name cannot contain a path or protocol';
    }
    for (const character of name) {
      const code = character.codePointAt(0);
      if (code < 32 || code === 127) return 'Group name cannot contain control characters';
    }
    return '';
  }

  function roleLabels(entry, required) {
    return (Array.isArray(entry?.home_roles) ? entry.home_roles : [])
      .filter(role => Boolean(role?.required) === required)
      .map(role => String(role?.label || '').trim())
      .filter(Boolean);
  }

  function providerLabel(entry) {
    const provider = entry?.provider;
    if (!provider) return '';
    if (provider.kind === 'plugin') {
      return `Plugin: ${provider.plugin_id}${provider.plugin_version ? ` ${provider.plugin_version}` : ''}`;
    }
    return provider.template_name ? `Your template: ${provider.template_name}` : 'Your template';
  }

  const AVAILABILITY_COPY = {
    creatable: 'Ready to create',
    reusable: 'Group exists',
    unavailable: 'Unavailable',
    existing_only_missing: 'Needs an existing group',
    conflict: 'Source conflict',
    ambiguous: 'Needs attention',
    incompatible: 'Needs migration'
  };

  function availabilityLabel(entry) {
    return AVAILABILITY_COPY[entry?.availability?.state] || 'Unavailable';
  }

  function unavailableNote(entry) {
    switch (entry?.availability?.state) {
      case 'unavailable':
        return entry?.home?.state === 'exists'
          ? `Its source is not available right now. The existing group “${entry.home.name}” stays readable.`
          : 'Its source plugin or template is not available right now.';
      case 'existing_only_missing':
        return 'This template can only use a group that already exists.';
      case 'conflict':
        return 'Two trusted sources define this group differently. Nothing can be created until they agree.';
      case 'ambiguous':
        return 'More than one group claims this program. Open the groups to resolve it.';
      case 'incompatible':
        return 'An existing group for this program needs its guided migration first.';
      default:
        return '';
    }
  }

  // The canonical request fields. Nothing else is ever sent: ownership, source,
  // parent, team, and project fields are server-derived or out of scope.
  function reviewRequest(entry, name) {
    return {
      group_template_id: String(entry?.id || ''),
      revision: String(entry?.revision || ''),
      name: String(name || '').trim()
    };
  }

  function commitRequest(entry, name, token, idempotencyKey) {
    return {
      ...reviewRequest(entry, name),
      group_review_token: String(token || ''),
      idempotency_key: String(idempotencyKey || '')
    };
  }

  function reviewKey(entry, name) {
    const request = reviewRequest(entry, name);
    return JSON.stringify([request.group_template_id, request.revision, request.name]);
  }

  function stateFor(context) {
    if (!context) return null;
    if (!context.groupTemplates) {
      context.groupTemplates = {
        status: 'idle',
        items: [],
        catalogUnavailable: false,
        selectedId: GENERAL_ID,
        names: {},
        review: null,
        pending: null,
        error: '',
        loadToken: 0,
        // Staged Home role fills per managed entry: entry id → Map(role id →
        // { mode, name, provider, model }). Nothing here is sent until the
        // Home-only commit has succeeded.
        fills: {},
        seeded: {}
      };
    }
    return context.groupTemplates;
  }

  function selectedEntry(context) {
    const state = context?.groupTemplates;
    if (!state) return null;
    return state.items.find(item => item.id === state.selectedId) || null;
  }

  function selectedManaged(context) {
    const entry = selectedEntry(context);
    return isManaged(entry) ? entry : null;
  }

  // Only an ordinary Group creator offers a choice. Guided setup describes its
  // one fixed template read-only (its own setup action stays the only
  // mutation owner), and a selected-members Group cannot adopt workspaces into
  // a program.
  function chooserMode(manager) {
    const context = manager?.workspaceCreatorContext;
    if (!context || manager.importModeEnabled || context.kind !== 'group') return 'hidden';
    if (context.mode === 'selected-members') return 'general-only';
    if (context.mode === 'guided') return context.guided?.groupTemplateId ? 'fixed' : 'hidden';
    if (context.mode !== 'ordinary') return 'hidden';
    return 'full';
  }

  // The template a guided setup is preparing, once the catalog lists it. A
  // source without a Group Template (for example a legacy declaration) has none.
  function fixedEntry(manager) {
    const context = manager?.workspaceCreatorContext;
    if (chooserMode(manager) !== 'fixed') return null;
    const entry = context.groupTemplates?.items.find(
      item => item.id === context.guided.groupTemplateId
    );
    return isManaged(entry) ? entry : null;
  }

  // Resolves one catalog entry by ID for surfaces outside the creator (guided
  // setup, a project's destination card). Presentation only: the entry never
  // replaces the caller's own review or its server-derived Home identity.
  async function catalogEntry(id) {
    if (!id) return null;
    const response = await fetch(LIST_URL, { headers: { Accept: 'application/json' } });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body?.error || 'Group templates could not be loaded.');
    const items = Array.isArray(body.group_templates) ? body.group_templates : [];
    const entry = items.find(item => item.id === id);
    return isManaged(entry) ? entry : null;
  }

  function templateMeta(entry) {
    if (!entry) return '';
    const provider = providerLabel(entry);
    return `Template: ${entry.name}${provider ? ` · ${provider}` : ''}`;
  }

  function projectRoleLabels(entry) {
    return Array.isArray(entry?.project_roles_note) ? entry.project_roles_note : [];
  }

  // One wording for a managed template's roles on every surface.
  function roleSummary(entry) {
    const required = roleLabels(entry, true);
    const optional = roleLabels(entry, false);
    const projectRoles = projectRoleLabels(entry);
    return [
      required.length ? `Set up after: ${required.join(', ')} (required)` : '',
      optional.length ? `Optional: ${optional.join(', ')}` : '',
      projectRoles.length ? `Stays project-local: ${projectRoles.join(', ')}` : ''
    ].filter(Boolean);
  }

  function createsCopy(existingName) {
    return existingName
      ? `Reuses the existing group “${existingName}” unchanged.`
      : 'Creates one group only — no project, team, schedule, or tool access.';
  }

  // Only an ordinary Group has a Blueprint step. Selected-member grouping is
  // General-only and guided setup's blueprint is fixed, so neither offers a
  // choice there.
  function hasBlueprintStep(manager) {
    return chooserMode(manager) === 'full';
  }

  function managedActive(manager) {
    return (
      chooserMode(manager) === 'full' && Boolean(selectedManaged(manager?.workspaceCreatorContext))
    );
  }

  // The group creator's step list when a managed template is selected: it
  // stages Home role fills on a Team step of its own. Every other creator
  // (General, guided setup, selected members) keeps the list sessions.js
  // derives, so this returns null for them.
  function wizardSteps(manager) {
    return managedActive(manager) ? [1, 2, 3, 4] : null;
  }

  // ---- Home role staffing ---------------------------------------------------
  //
  // The review/commit endpoint creates exactly one Home and refuses every team
  // field. Staffing is therefore a separate, immediate follow-on: the wizard
  // stages fills here and, only after a successful commit, sends one
  // PUT /api/workspaces/{home}/roles/{role} per staged role. A program Home's
  // prompt is applied server-side, so no fill ever carries one.

  const FILL_CREATE = 'create';
  const FILL_ASSIGN = 'assign';

  function homeRoles(entry) {
    return (Array.isArray(entry?.home_roles) ? entry.home_roles : []).filter(role =>
      String(role?.role_id || '').trim()
    );
  }

  function roleDefaultName(role) {
    return String(role?.default_name || role?.label || '').trim();
  }

  function resetFills(context) {
    const state = context?.groupTemplates;
    if (!state) return;
    state.fills = {};
    state.seeded = {};
  }

  function fillsFor(context, entry) {
    const state = stateFor(context);
    if (!state || !entry) return new Map();
    if (!state.fills[entry.id]) state.fills[entry.id] = new Map();
    return state.fills[entry.id];
  }

  function normalizeFill(fill) {
    const mode = fill?.mode === FILL_ASSIGN ? FILL_ASSIGN : FILL_CREATE;
    return {
      mode,
      name: String(fill?.name || '').trim(),
      provider: mode === FILL_CREATE ? String(fill?.provider || '').trim() : '',
      model: mode === FILL_CREATE ? String(fill?.model || '').trim() : ''
    };
  }

  // Every required Home role starts as Create under its default name; optional
  // roles start empty. When a saved agent already has that name, the role starts
  // as Assign instead — a Create under a taken name is refused by the server, so
  // the wizard never proposes one. Seeding waits for the saved agents to load
  // (or fail) and runs once per template selection, so a role the user cleared
  // stays cleared.
  function seedFills(manager) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    const entry = selectedManaged(context);
    if (!state || !entry || !managedActive(manager) || state.seeded[entry.id]) return false;
    const saved = manager.groupTemplateSavedAgentsState?.();
    if (saved === 'idle' || saved === 'loading') return false;
    const fills = fillsFor(context, entry);
    for (const role of homeRoles(entry)) {
      if (!role.required || fills.has(role.role_id) || !roleEditable(entry, role)) continue;
      const name = roleDefaultName(role);
      if (!name) continue;
      const attachable = manager.findAttachableSavedAgent?.(name);
      fills.set(
        role.role_id,
        attachable
          ? normalizeFill({ mode: FILL_ASSIGN, name: String(attachable.name || name) })
          : normalizeFill({ mode: FILL_CREATE, name })
      );
    }
    state.seeded[entry.id] = true;
    return true;
  }

  // What the catalog verified about one required role on an existing Home. It
  // reports required roles only, and only when it could read the Home's roster.
  function reportedRole(entry, roleId) {
    const roles = entry?.required_home_roles?.roles;
    return (Array.isArray(roles) ? roles : []).find(role => role?.role_id === roleId) || null;
  }

  function reuseVerified(entry) {
    return entry?.required_home_roles?.verification === 'verified';
  }

  // Whether the wizard may stage a fill for this role. A new Home's roles are
  // all staffable. An existing Home is reused unchanged except for required
  // roles the catalog verified as empty: a filled role keeps its holder, an
  // optional role's state is not reported here, and an unverified roster
  // stages nothing at all.
  function roleEditable(entry, role) {
    if (!isReusable(entry)) return true;
    if (!reuseVerified(entry)) return false;
    const reported = reportedRole(entry, role?.role_id);
    return Boolean(reported) && reported.state !== 'filled';
  }

  // Mirrors group-template-status.js groupTemplateTeamFact ("Could not be
  // verified"); this classic script cannot import that module.
  const UNVERIFIED_ROLE_REASON =
    'Could not be verified. Nothing is staffed here; set this role up from the group page.';

  function roleReadOnlyReason(entry, role) {
    if (roleEditable(entry, role)) return '';
    if (!reuseVerified(entry)) return UNVERIFIED_ROLE_REASON;
    if (reportedRole(entry, role?.role_id)?.state === 'filled') return 'Already filled';
    return 'Optional roles on an existing group are set up from the group page.';
  }

  // Another staged role already uses this agent name. One agent fills at most
  // one role, and two Creates under one name would collide on the server.
  function fillNameProblem(manager, roleId, name) {
    const key = String(name || '')
      .trim()
      .toLowerCase();
    if (!key) return '';
    const context = manager?.workspaceCreatorContext;
    const entry = selectedManaged(context);
    if (!entry) return '';
    for (const role of homeRoles(entry)) {
      const fill = fillsFor(context, entry).get(role.role_id);
      if (role.role_id !== roleId && fill && fill.name.toLowerCase() === key) {
        return `${role.label || role.role_id} is already staffed by “${fill.name}”. Choose another name.`;
      }
    }
    return '';
  }

  // Shown in place of the Create form's prompt box for a program Home role.
  function promptNote(entry) {
    const provider = providerLabel(entry) || 'this template';
    return `Instructions for this role come from ${provider} and are applied by Ori.`;
  }

  function emptyRequiredWarnings(manager, entry) {
    const fills = fillsFor(manager?.workspaceCreatorContext, entry);
    return homeRoles(entry)
      .filter(role => role.required && !fills.has(role.role_id) && roleEditable(entry, role))
      .map(
        role =>
          `This group will have no ${String(role.label || role.role_id)} until you set one up.`
      );
  }

  function setFill(manager, roleId, fill) {
    const context = manager?.workspaceCreatorContext;
    const entry = selectedManaged(context);
    const role = homeRoles(entry).find(item => item.role_id === roleId);
    const next = normalizeFill(fill);
    if (!role || !next.name || context?.groupTemplates?.pending) return false;
    if (!roleEditable(entry, role) || fillNameProblem(manager, roleId, next.name)) return false;
    fillsFor(context, entry).set(roleId, next);
    return true;
  }

  function clearFill(manager, roleId) {
    const context = manager?.workspaceCreatorContext;
    const entry = selectedManaged(context);
    if (!entry || context?.groupTemplates?.pending) return false;
    return fillsFor(context, entry).delete(roleId);
  }

  function stagedFill(manager, roleId) {
    const context = manager?.workspaceCreatorContext;
    const entry = selectedManaged(context);
    return entry ? fillsFor(context, entry).get(roleId) || null : null;
  }

  // The fills that will be sent, in declaration order.
  function staffingPlan(manager) {
    const context = manager?.workspaceCreatorContext;
    const entry = selectedManaged(context);
    if (!entry || !managedActive(manager)) return [];
    const fills = fillsFor(context, entry);
    return homeRoles(entry)
      .filter(role => fills.has(role.role_id) && roleEditable(entry, role))
      .map(role => ({
        roleId: role.role_id,
        label: String(role.label || role.role_id),
        required: Boolean(role.required),
        fill: { ...fills.get(role.role_id) }
      }));
  }

  // The roster projection WorkspaceRoleRoster draws. Program Home roles carry
  // no `proposed` block: their setup is never disclosed to the browser.
  function teamRoster(manager) {
    const context = manager?.workspaceCreatorContext;
    const entry = selectedManaged(context);
    const fills = entry ? fillsFor(context, entry) : new Map();
    const roles = homeRoles(entry).map(role => {
      const readOnlyReason = roleReadOnlyReason(entry, role);
      // An existing holder is shown as it is; the wizard never replaces it.
      const holder =
        readOnlyReason && reportedRole(entry, role.role_id)?.state === 'filled'
          ? reportedRole(entry, role.role_id).agent || null
          : null;
      const fill = readOnlyReason ? null : fills.get(role.role_id);
      // An empty row already says Missing or Optional in its tag; a filled
      // row's tag names its agent, so the marker moves into the description.
      const marker = fill || holder ? (role.required ? 'Required' : 'Optional') : '';
      const row = {
        role_id: role.role_id,
        label: String(role.label || role.role_id),
        description: [marker, String(role.description || '')].filter(Boolean).join(' · '),
        scope: 'home',
        required: Boolean(role.required),
        primary: Boolean(role.primary),
        state: fill || holder ? 'filled' : 'empty',
        agent: holder || (fill ? { name: fill.name } : null),
        source: holder ? 'group' : fill?.mode === FILL_ASSIGN ? 'assigned' : 'created'
      };
      if (readOnlyReason) {
        row.read_only = true;
        row.read_only_reason = readOnlyReason;
      }
      return row;
    });
    const filled = roles.filter(role => role.state === 'filled').length;
    return {
      roles,
      total_count: roles.length,
      filled_count: filled,
      empty_count: roles.length - filled
    };
  }

  function outcomeLine(label, fill) {
    if (!fill) return `${label} · Not staffed`;
    return fill.mode === FILL_ASSIGN
      ? `${label} · Assign “${fill.name}”`
      : `${label} · Create “${fill.name}”`;
  }

  function staffCountLabel(count) {
    return `${count} role${count === 1 ? '' : 's'}`;
  }

  const TEAM_TITLE = 'Staff the group';
  const TEAM_DESCRIPTION =
    'The blueprint proposes these group roles. Required ones are prefilled; you can rename, assign a saved agent, or leave a role empty.';

  // Draws the Team step for a managed template. The ordinary team layout's
  // pieces that describe a workspace roster are emptied here; the ordinary
  // path re-renders them whenever it is the active creator again.
  function renderTeam(manager) {
    if (!managedActive(manager) || manager.wizardStep !== 3) return;
    seedFills(manager);
    const entry = selectedManaged(manager.workspaceCreatorContext);
    const title = document.getElementById('wizardStep3Title');
    const description = document.getElementById('wizardStep3Description');
    const heading = document.getElementById('workspaceTeamHeading');
    const summary = document.getElementById('workspaceTeamSummary');
    const eyebrow = document.getElementById('workspaceTeamEyebrow');
    if (title) title.textContent = TEAM_TITLE;
    if (description) description.textContent = TEAM_DESCRIPTION;
    if (eyebrow) eyebrow.textContent = 'Resulting group team';
    if (heading) heading.textContent = `Group roles · ${String(entry?.name || 'Group template')}`;
    if (summary) {
      const saved = manager.groupTemplateSavedAgentsState?.();
      const staffable = homeRoles(entry).some(role => roleEditable(entry, role));
      summary.textContent = !staffable
        ? 'Nothing to staff here: this group’s roles are already filled or are set up from the group page.'
        : saved === 'idle' || saved === 'loading'
          ? 'Checking your saved agents before proposing names…'
          : isReusable(entry)
            ? 'The group is reused unchanged. Only its empty required roles can be staffed here, right after you confirm on Review.'
            : 'Nothing is created or staffed until you confirm on Review. Each role is filled right after the group exists.';
    }
    for (const id of ['workspaceAssistantProgramCreate', 'workspaceBlankAgentlessChoice']) {
      const node = document.getElementById(id);
      if (node) node.hidden = true;
    }
    for (const id of [
      'workspaceTeamIssues',
      'workspaceTeamBatchActions',
      'workspaceTeamRoster',
      'workspaceTeamModelNote'
    ]) {
      document.getElementById(id)?.replaceChildren();
    }
    const layout = document.getElementById('workspaceTeamLayout');
    const picker = document.getElementById('existingAgentRosterPanel');
    const assigning = Boolean(manager.groupTemplateRoleAssigning);
    layout?.classList?.remove?.('is-assistant-program');
    // The saved-agent picker only appears beside the roster while a role is
    // being assigned; otherwise the roster takes the whole step.
    layout?.classList?.toggle?.('is-roster-only', !assigning);
    if (picker) picker.hidden = !assigning;
    const container = document.getElementById('workspaceRoleRoster');
    const component = window.WorkspaceRoleRoster;
    if (!container || !component) return;
    container.hidden = false;
    component.render(container, teamRoster(manager), {
      title: 'Group roles',
      agentHref: null,
      onRequestCreate: (roleId, row) => manager.openGroupTemplateRoleSetup?.(roleId, row),
      onEdit: (roleId, row, opener) => manager.openGroupTemplateRoleSetup?.(roleId, row, opener),
      onAssign: (roleId, row) => manager.openGroupTemplateRoleAssign?.(roleId, row),
      onClear: (roleId, row) => manager.clearGroupTemplateRole?.(roleId, row)
    });
  }

  function currentName() {
    return String(document.getElementById('folderNameInput')?.value || '').trim();
  }

  async function load(manager, { force = false } = {}) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    if (!state || (!force && state.status !== 'idle')) return;
    const token = ++state.loadToken;
    state.status = 'loading';
    render(manager);
    try {
      const response = await fetch(LIST_URL, { headers: { Accept: 'application/json' } });
      const body = await response.json().catch(() => ({}));
      if (manager.workspaceCreatorContext !== context || state.loadToken !== token) return;
      if (!response.ok) throw new Error(body?.error || 'Group templates could not be loaded.');
      state.items = Array.isArray(body.group_templates) ? body.group_templates : [];
      state.catalogUnavailable = Boolean(
        body.catalog_unavailable || body.dependency_state_unavailable
      );
      if (!state.items.some(item => item.id === GENERAL_ID)) {
        state.items.unshift({ id: GENERAL_ID, kind: 'ordinary_group', name: 'General' });
      }
      const selected = state.items.find(item => item.id === state.selectedId);
      if (!isSelectable(selected)) state.selectedId = GENERAL_ID;
      state.status = 'ready';
    } catch (error) {
      if (manager.workspaceCreatorContext !== context || state.loadToken !== token) return;
      state.status = 'error';
      state.items = [{ id: GENERAL_ID, kind: 'ordinary_group', name: 'General' }];
      state.selectedId = GENERAL_ID;
      state.error = error?.message || 'Group templates could not be loaded.';
    }
    manager.refreshWizardChrome?.();
  }

  function select(manager, id) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    const next = state?.items.find(item => item.id === id);
    if (!state || !isSelectable(next) || state.pending) return false;
    if (chooserMode(manager) === 'general-only' && isManaged(next)) return false;
    const previous = selectedEntry(context);
    if (previous?.id === next.id) return false;
    const input = document.getElementById('folderNameInput');
    // Each template keeps its own typed name so switching never carries one
    // template's name into another's consent.
    state.names[previous?.id || GENERAL_ID] = String(input?.value || '');
    state.selectedId = next.id;
    state.review = null;
    state.error = '';
    // A staffing plan belongs to the template it was made for.
    resetFills(context);
    if (input) {
      if (isReusable(next)) input.value = String(next.home?.name || '');
      else if (state.names[next.id] !== undefined) input.value = state.names[next.id];
      else if (isManaged(next)) input.value = String(next.proposed_group_name || '');
      else input.value = '';
    }
    manager.clearWorkspaceNameError?.();
    if (isManaged(next)) manager.resetTemplateAgentReview?.();
    // Choosing stays on the Blueprint step (arrow keys browse options); Continue
    // moves on. A choice made from a later step never strands a now-hidden one.
    const steps = manager.creatorWizardSteps?.() || [];
    if (steps.length && !steps.includes(manager.wizardStep)) manager.wizardStep = steps[0];
    manager.refreshWizardChrome?.();
    return true;
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  // The chooser's own creator stages Home role fills, so its card says so;
  // guided setup's fixed card keeps the Home-only wording of its own flow.
  function staffingSummary(entry, existingName) {
    const required = roleLabels(entry, true);
    return {
      creates: existingName
        ? `Reuses the existing group “${existingName}” unchanged; you choose who fills its empty roles.`
        : 'Creates one group and fills the roles you choose — no project, schedule, or tool access.',
      roles: roleSummary(entry).map(line =>
        required.length && line.startsWith('Set up after: ')
          ? `Group roles: ${required.join(', ')} (required)`
          : line
      )
    };
  }

  function appendSummary(body, entry, existingName, { staffing = false } = {}) {
    const provider = providerLabel(entry);
    if (provider) body.append(el('small', 'workspace-group-template-provider', provider));
    if (entry.description) body.append(el('small', '', String(entry.description)));
    const summary = staffing
      ? staffingSummary(entry, existingName)
      : { creates: createsCopy(existingName), roles: roleSummary(entry) };
    body.append(el('small', 'workspace-group-template-creates', summary.creates));
    for (const line of summary.roles) body.append(el('small', '', line));
  }

  // Guided setup shows its one template as a read-only card: no radio, no
  // alternative, and nothing here can review or create.
  function renderFixed(manager, state) {
    const container = document.getElementById('workspaceGroupTemplateFixed');
    const list = document.getElementById('workspaceGroupTemplateFixedOptions');
    const status = document.getElementById('workspaceGroupTemplateFixedStatus');
    if (!container || !list) return;
    const context = manager.workspaceCreatorContext;
    const entry = fixedEntry(manager);
    list.replaceChildren();
    if (entry) {
      const review = context.guided?.state?.review;
      const existingName = review?.existing
        ? String(review.input?.name || '')
        : isReusable(entry)
          ? String(entry.home?.name || '')
          : '';
      const card = el('div', 'workspace-group-template-option is-fixed');
      card.dataset.groupTemplateId = entry.id;
      const body = el('span', 'workspace-group-template-body');
      const head = el('span', 'workspace-group-template-head');
      head.append(el('strong', '', String(entry.name || 'Group template')));
      body.append(head);
      appendSummary(body, entry, existingName);
      card.append(body);
      list.append(card);
    }
    const message = state.status === 'loading' ? 'Checking this group template…' : '';
    // A template the catalog does not list is simply not described; guided
    // setup still reviews its own group.
    container.hidden = !entry && !message;
    if (status) {
      status.textContent = message;
      status.hidden = !message;
    }
  }

  function render(manager) {
    const container = document.getElementById('workspaceGroupTemplateChoice');
    const step = document.getElementById('workspaceGroupBlueprintStep');
    const fixed = document.getElementById('workspaceGroupTemplateFixed');
    const mode = chooserMode(manager);
    // The chooser lives on the Blueprint step; guided setup's fixed card lives
    // on Details. Every other context shows neither.
    if (container) container.hidden = mode !== 'full';
    if (step) step.hidden = mode !== 'full';
    if (fixed && mode !== 'fixed') fixed.hidden = true;
    if (mode !== 'full' && mode !== 'fixed') return;
    const context = manager.workspaceCreatorContext;
    const state = stateFor(context);
    if (state.status === 'idle') void load(manager);
    if (mode === 'fixed') {
      renderFixed(manager, state);
      return;
    }

    const list = document.getElementById('workspaceGroupTemplateOptions');
    const status = document.getElementById('workspaceGroupTemplateStatus');
    if (!container || !list) return;
    // Re-rendering replaces the radios; keep keyboard focus on the same option.
    const active = document.activeElement;
    const focusedId = active?.name === 'workspace-group-template' ? String(active.value) : '';
    list.replaceChildren();
    let refocus = null;
    const items = state.items.length
      ? state.items
      : [{ id: GENERAL_ID, kind: 'ordinary_group', name: 'General' }];
    for (const entry of items) {
      const managed = isManaged(entry);
      const disabled = !isSelectable(entry) || Boolean(state.pending);
      const label = el('label', 'workspace-group-template-option');
      label.dataset.groupTemplateId = entry.id;
      label.classList.toggle('is-disabled', disabled);
      const radio = el('input');
      radio.type = 'radio';
      radio.name = 'workspace-group-template';
      radio.value = entry.id;
      radio.checked = entry.id === state.selectedId;
      radio.disabled = disabled;
      if (focusedId && entry.id === focusedId) refocus = radio;
      const body = el('span', 'workspace-group-template-body');
      const head = el('span', 'workspace-group-template-head');
      head.append(el('strong', '', String(entry.name || 'Group template')));
      if (managed) {
        head.append(
          el(
            'span',
            `workspace-group-template-badge is-${entry.availability?.state || 'unavailable'}`,
            availabilityLabel(entry)
          )
        );
      }
      body.append(head);
      if (managed) {
        appendSummary(body, entry, isReusable(entry) ? String(entry.home?.name || '') : '', {
          staffing: true
        });
        const note = unavailableNote(entry);
        if (note) body.append(el('small', 'workspace-group-template-note', note));
      } else {
        body.append(
          el('small', '', 'A home for related workspaces with a reviewed Group Manager.')
        );
      }
      label.append(radio, body);
      list.append(label);
    }
    refocus?.focus?.();
    if (status) {
      const message =
        state.status === 'loading'
          ? 'Checking group templates…'
          : state.status === 'error'
            ? `${state.error} General is still available.`
            : state.catalogUnavailable
              ? 'Some group templates could not be checked. General is still available.'
              : '';
      status.textContent = message;
      status.hidden = !message;
    }
  }

  // Applied after sessions.js has synced the ordinary Group presentation.
  function syncPresentation(manager) {
    render(manager);
    const blueprintStep = hasBlueprintStep(manager);
    const workspaceBlueprint = document.getElementById('workspaceBlueprintStepBody');
    const step1Title = document.getElementById('wizardStep1Title');
    const step1Description = document.getElementById('wizardStep1Description');
    const recap = document.getElementById('workspaceGroupBlueprintRecap');
    const recapName = document.getElementById('workspaceGroupBlueprintRecapName');
    // Step 1 is one section: the Workspace picker or the Group blueprints.
    const groupKind = manager?.workspaceCreatorContext?.kind === 'group';
    if (workspaceBlueprint) workspaceBlueprint.hidden = groupKind;
    if (step1Title) {
      step1Title.textContent = groupKind ? 'Choose a group blueprint' : 'Choose a blueprint';
    }
    if (step1Description) {
      step1Description.textContent = groupKind
        ? 'Start with General, or build the group an installed plugin defines.'
        : 'Start with a proven workspace shape, or begin with a clean slate.';
    }
    if (recap) recap.hidden = !blueprintStep;
    if (recapName && blueprintStep) {
      recapName.textContent = String(
        selectedEntry(manager.workspaceCreatorContext)?.name || 'General'
      );
    }
    const active = managedActive(manager);
    const entry = active ? selectedManaged(manager.workspaceCreatorContext) : null;
    const input = document.getElementById('folderNameInput');
    const description = document.getElementById('workspaceDetailsDescriptionCard');
    const parentCard = document.getElementById('workspaceCreatorDestinationCard');
    const notice = document.getElementById('workspaceGroupDetailsNotice');
    const step2Description = document.getElementById('wizardStep2Description');
    const step4Title = document.getElementById('wizardStep4Title');
    const step4Description = document.getElementById('wizardStep4Description');
    if (input) {
      input.readOnly = Boolean(entry && isReusable(entry));
      input.setAttribute('aria-readonly', String(input.readOnly));
    }
    if (!active) {
      // Hand the shared Team layout back to the ordinary roster unchanged.
      const eyebrow = document.getElementById('workspaceTeamEyebrow');
      if (eyebrow?.dataset?.defaultText) eyebrow.textContent = eyebrow.dataset.defaultText;
      document.getElementById('workspaceTeamLayout')?.classList?.remove?.('is-roster-only');
      return;
    }
    if (description) description.hidden = true;
    if (parentCard) parentCard.hidden = true;
    if (notice) {
      notice.hidden = false;
      notice.replaceChildren(
        el(
          'strong',
          '',
          isReusable(entry) ? 'This group already exists.' : 'Only the group is created here.'
        ),
        el(
          'span',
          '',
          isReusable(entry)
            ? 'It will be reused unchanged: no rename and no project. Next, choose who fills its empty roles.'
            : 'Next, choose who fills its roles. Projects, project teams, and tool access are never created here.'
        )
      );
    }
    if (step2Description) {
      step2Description.textContent = isReusable(entry)
        ? 'Review the existing group this template uses.'
        : 'Name the group this template creates. You can rename it later.';
    }
    const step3Name = document.querySelector?.('[data-workspace-creator-step-name="3"]');
    if (step3Name) step3Name.textContent = 'Team';
    if (manager.wizardStep >= 3) seedFills(manager);
    renderTeam(manager);
    const staffed = staffingPlan(manager).length;
    if (step4Title) {
      step4Title.textContent = isReusable(entry)
        ? staffed
          ? 'Reuse and staff this group'
          : 'Reuse this group'
        : staffed
          ? 'Create and staff this group'
          : 'Create this group only';
    }
    if (step4Description) {
      step4Description.textContent = 'Nothing is created or changed until you confirm below.';
    }
  }

  function identityProblem(manager) {
    const entry = selectedManaged(manager?.workspaceCreatorContext);
    if (!entry) return null;
    return nameProblem(currentName());
  }

  // Receipts are HTML strings built from source-supplied text; never fall back
  // to raw text when the caller has no escaper of its own.
  const HTML_ESCAPES = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  function escape(manager, value) {
    const text = String(value ?? '');
    return manager?.escapeHtml
      ? manager.escapeHtml(text)
      : text.replace(/[&<>"']/g, character => HTML_ESCAPES[character]);
  }

  function renderReceipt(manager) {
    const context = manager.workspaceCreatorContext;
    const state = stateFor(context);
    const entry = selectedManaged(context);
    if (!entry) return '';
    const name = currentName();
    const review =
      state.review && state.review.key === reviewKey(entry, name) ? state.review : null;
    const plan = staffingPlan(manager);
    const staffed = plan.length;
    let status = 'Preparing a review… nothing has been created.';
    if (review?.status === 'ready') {
      // The server reviews the group alone ("nothing else is created"); the
      // role fills are separate requests, so say where they come in.
      status = [
        review.data?.summary || '',
        staffed ? 'The roles listed below are then staffed one at a time.' : ''
      ]
        .filter(Boolean)
        .join(' ');
    }
    if (review?.status === 'error') status = review.error;
    if (state.pending?.uncertain) {
      status =
        'The confirmed request did not return. Retry the same confirmed change; Ori will not create a second group.';
    }
    const reuse = review?.status === 'ready' ? Boolean(review.data?.reuse) : isReusable(entry);
    const groupName = reuse ? review?.data?.home_name || entry.home?.name || name : name;
    const outcome = reuse
      ? staffed
        ? `This existing group will be reused unchanged and ${joinLabels(plan.map(item => item.label))} will be staffed.`
        : 'The existing group is reused unchanged'
      : staffed
        ? `One group is created, then ${staffCountLabel(staffed)} staffed`
        : 'One group is created, initially unstaffed';
    return `
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">${reuse ? 'Existing group' : 'Group'}</span>
          <strong class="workspace-review-identity-name">${escape(manager, groupName || 'Untitled group')}</strong>
          <span class="workspace-review-card-meta">${escape(manager, templateMeta(entry))}</span>
        </div>
        <div class="workspace-review-card-actions">
          <button type="button" class="workspace-wizard-inline-action" data-wizard-edit-step="2">Edit</button>
        </div>
      </div>
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">What will happen</span>
          <strong>${escape(manager, outcome)}</strong>
          <span class="workspace-review-card-note ${review?.status === 'error' ? 'is-error' : ''}" data-group-template-review-status role="status" aria-live="polite">${escape(manager, status)}</span>
        </div>
      </div>
      ${rolesCardHTML(manager, entry)}`;
  }

  function rolesCardHTML(manager, entry) {
    if (!entry) return '';
    if (managedActive(manager) && selectedManaged(manager.workspaceCreatorContext) === entry) {
      return staffingCardHTML(manager, entry);
    }
    const required = roleLabels(entry, true);
    const optional = roleLabels(entry, false);
    const projectRoles = projectRoleLabels(entry);
    if (!required.length && !optional.length && !projectRoles.length) return '';
    return `
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">Group roles</span>
          ${required.length ? `<span class="workspace-review-card-meta">${escape(manager, `Required, set up after: ${required.join(', ')}`)}</span>` : ''}
          ${optional.length ? `<span class="workspace-review-card-meta">${escape(manager, `Optional: ${optional.join(', ')}`)}</span>` : ''}
          ${projectRoles.length ? `<span class="workspace-review-card-note">${escape(manager, `Stays project-local: ${projectRoles.join(', ')}. Each project keeps its own team.`)}</span>` : ''}
        </div>
      </div>`;
  }

  // The creator's own receipt: one outcome line per Home role, in declaration
  // order, so the consequence of confirming is explicit before anything exists.
  function staffingCardHTML(manager, entry) {
    const roles = homeRoles(entry);
    const projectRoles = projectRoleLabels(entry);
    if (!roles.length && !projectRoles.length) return '';
    const fills = fillsFor(manager.workspaceCreatorContext, entry);
    const lines = roles.map(role => {
      const label = String(role.label || role.role_id);
      if (roleEditable(entry, role)) return outcomeLine(label, fills.get(role.role_id));
      const holder = reportedRole(entry, role.role_id);
      return holder?.state === 'filled'
        ? `${label} · Already filled by “${String(holder.agent?.name || 'its holder')}”`
        : `${label} · Not staffed here`;
    });
    const warnings = emptyRequiredWarnings(manager, entry);
    return `
      <div class="workspace-review-card" data-group-template-staffing>
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">Group roles</span>
          ${lines.map(line => `<span class="workspace-review-card-meta" data-group-template-role-outcome>${escape(manager, line)}</span>`).join('')}
          ${warnings.map(line => `<span class="workspace-review-card-note is-warning" data-group-template-role-warning role="note">${escape(manager, line)}</span>`).join('')}
          ${roles.length ? `<span class="workspace-review-card-note">${escape(manager, 'Roles are filled one at a time right after the group exists. A role that cannot be filled never undoes the group.')}</span>` : ''}
          ${projectRoles.length ? `<span class="workspace-review-card-note">${escape(manager, `Stays project-local: ${projectRoles.join(', ')}. Each project keeps its own team.`)}</span>` : ''}
        </div>
        ${roles.length ? '<div class="workspace-review-card-actions"><button type="button" class="workspace-wizard-inline-action" data-wizard-edit-step="3">Edit team</button></div>' : ''}
      </div>`;
  }

  async function ensureReview(manager) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    const entry = selectedManaged(context);
    if (!state || !entry || state.pending) return;
    const name = currentName();
    const key = reviewKey(entry, name);
    if (state.review?.key === key && state.review.status !== 'error') return;
    const review = { key, status: 'loading', data: null, error: '' };
    state.review = review;
    manager.refreshWorkspaceReview?.();
    try {
      const response = await fetch(REVIEW_URL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(reviewRequest(entry, name))
      });
      const body = await response.json().catch(() => ({}));
      if (manager.workspaceCreatorContext !== context || state.review !== review) return;
      if (!response.ok || !body.group_template_review?.review_token) {
        throw new Error(
          body?.group_template?.summary ||
            body?.group_requirement?.summary ||
            (typeof body?.error === 'string' ? body.error : '') ||
            'This group could not be reviewed.'
        );
      }
      review.status = 'ready';
      review.data = body.group_template_review;
    } catch (error) {
      if (manager.workspaceCreatorContext !== context || state.review !== review) return;
      review.status = 'error';
      review.error = `${error?.message || 'This group could not be reviewed.'} Nothing was created.`;
    }
    manager.refreshWizardChrome?.();
  }

  function canSubmit(manager) {
    const state = manager?.workspaceCreatorContext?.groupTemplates;
    const entry = selectedManaged(manager?.workspaceCreatorContext);
    if (!state || !entry) return true;
    if (state.pending) return true;
    return state.review?.status === 'ready' && state.review.key === reviewKey(entry, currentName());
  }

  function ctaLabel(manager) {
    const state = manager?.workspaceCreatorContext?.groupTemplates;
    const entry = selectedManaged(manager?.workspaceCreatorContext);
    if (!entry) return '';
    if (state?.pending?.uncertain) return 'Retry confirmed change';
    const reuse = state?.review?.status === 'ready' ? state.review.data?.reuse : isReusable(entry);
    const name = reuse ? state?.review?.data?.home_name || entry.home?.name : currentName();
    const staffed = (state?.pending?.staffing || staffingPlan(manager)).length;
    if (reuse) {
      return staffed
        ? `Use existing group “${name}” and staff ${staffCountLabel(staffed)}`
        : `Use existing group “${name}”`;
    }
    return staffed
      ? `Create group and staff ${staffCountLabel(staffed)}`
      : `Create group “${name || 'Untitled'}” only`;
  }

  // Fallback when the group's folder cannot be resolved for navigation: the
  // same landing words, as a toast on the page the user is already on.
  function showFollowUp(manager, result, folder, notice) {
    const slug = String(folder?.folder_slug || '').trim();
    const options = { title: notice.title, duration: 9000 };
    if (slug) {
      options.action = {
        label: 'Open group',
        onClick: () => {
          window.location.href = landingURL(slug, notice.roleId);
        }
      };
    }
    const toast = window.Toast?.[notice.tone];
    if (typeof toast === 'function') toast.call(window.Toast, notice.message, options);
    else manager.showToast?.(notice.message, notice.tone);
  }

  function roleURL(homeId, roleId) {
    return `/api/workspaces/${encodeURIComponent(homeId)}/roles/${encodeURIComponent(roleId)}`;
  }

  // One role, one request. A 409 saying the role is already filled means an
  // earlier attempt landed and its response was lost, so it counts as staffed.
  async function fillRole(homeId, item) {
    const fill = normalizeFill(item.fill);
    try {
      const response = await fetch(roleURL(homeId, item.roleId), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({
          mode: fill.mode,
          name: fill.name,
          provider: fill.provider,
          model: fill.model
        })
      });
      const body = await response.json().catch(() => ({}));
      if (response.ok) return { ok: true };
      const message = String(body?.message || body?.error || '').trim();
      if (response.status === 409 && /already fills this role/i.test(message)) {
        return { ok: true, alreadyFilled: true };
      }
      return {
        ok: false,
        error: message || `${item.label} could not be staffed (${response.status}).`
      };
    } catch (error) {
      return {
        ok: false,
        error: `${item.label} could not be staffed: ${error?.message || 'the request did not return'}.`
      };
    }
  }

  // Fills run in declaration order after the commit. A failure never stops the
  // remaining roles and never undoes the group or an earlier fill.
  async function fillRoles(homeId, staffing) {
    const outcomes = [];
    for (const item of staffing) {
      outcomes.push({ roleId: item.roleId, label: item.label, ...(await fillRole(homeId, item)) });
    }
    return outcomes;
  }

  function joinLabels(labels) {
    if (labels.length <= 1) return labels.join('');
    return `${labels.slice(0, -1).join(', ')} and ${labels[labels.length - 1]}`;
  }

  function reportedFilled(entry, roleId) {
    const roles = entry?.required_home_roles?.roles;
    return (Array.isArray(roles) ? roles : []).some(
      role => role?.role_id === roleId && role?.state === 'filled'
    );
  }

  // The role the group page should open: the first fill that failed, otherwise
  // the first required Home role nobody was staged to fill.
  function landingRole(entry, staffing, outcomes) {
    const failed = outcomes.find(outcome => !outcome.ok);
    if (failed) return failed.roleId;
    const staged = new Set(staffing.map(item => item.roleId));
    const empty = homeRoles(entry).find(
      role => role.required && !staged.has(role.role_id) && !reportedFilled(entry, role.role_id)
    );
    return empty ? empty.role_id : '';
  }

  // The words the user reads once the group exists, on the group page or in the
  // fallback toast: which group, which roles were staffed, and what did not.
  function landingNotice(result, entry, staffing, outcomes, requestedName = '') {
    const name = String(result?.home_name || 'Group');
    const created = Boolean(result?.created_by_this_operation);
    const nameNote =
      !created && String(requestedName || '').trim() && String(requestedName).trim() !== name
        ? ' The name you entered was not applied.'
        : '';
    const lead = created ? `${name} is ready.` : `${name} reused.${nameNote}`;
    const staffed = outcomes.filter(outcome => outcome.ok).map(outcome => outcome.label);
    const failed = outcomes.filter(outcome => !outcome.ok);
    const staged = new Set(staffing.map(item => item.roleId));
    const unstaffed = homeRoles(entry)
      .filter(
        role => role.required && !staged.has(role.role_id) && !reportedFilled(entry, role.role_id)
      )
      .map(role => String(role.label || role.role_id));
    const parts = [lead];
    if (staffed.length) parts.push(`${joinLabels(staffed)} staffed.`);
    if (failed.length) {
      parts.push(
        `${joinLabels(failed.map(outcome => outcome.label))} ${failed.length === 1 ? 'was' : 'were'} not staffed: ${failed[0].error}`
      );
    }
    if (unstaffed.length) {
      parts.push(
        `${joinLabels(unstaffed)} ${unstaffed.length === 1 ? 'is' : 'are'} not staffed yet.`
      );
    }
    return {
      tone: failed.length ? 'warning' : 'success',
      title: created ? 'Group created' : 'Existing group reused',
      message: parts.join(' '),
      roleId: landingRole(entry, staffing, outcomes),
      error: failed.length ? String(failed[0].error || '') : ''
    };
  }

  // Read once by the group page (group-template-status.js
  // consumeGroupTemplateLanding). Keep the key identical to
  // GROUP_TEMPLATE_LANDING_KEY there.
  const LANDING_KEY = 'ori:group-template-landing';

  function storeLandingNotice(workspaceId, notice) {
    try {
      window.sessionStorage.setItem(
        LANDING_KEY,
        JSON.stringify({
          workspace_id: String(workspaceId || ''),
          created_at: Date.now(),
          tone: notice.tone,
          title: notice.title,
          message: notice.message,
          role_id: notice.roleId,
          error: notice.error
        })
      );
      return true;
    } catch (_error) {
      // Storage can be unavailable; the group page still shows its own status.
      return false;
    }
  }

  function landingURL(slug, roleId) {
    const path = `/workspaces/${encodeURIComponent(slug)}`;
    return roleId ? `${path}?role=${encodeURIComponent(roleId)}` : path;
  }

  async function submit(manager) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    const entry = selectedManaged(context);
    if (!state || !entry || manager.isCreatingFolder) return false;
    const name = currentName();
    const problem = nameProblem(name);
    if (problem) {
      manager.goToWizardStep?.(2);
      manager.setWorkspaceNameError?.(problem);
      return false;
    }
    if (!state.pending) {
      if (!canSubmit(manager)) {
        await ensureReview(manager);
        return false;
      }
      state.pending = {
        request: commitRequest(entry, name, state.review.data.review_token, crypto.randomUUID()),
        // The staffing plan is part of what the user confirmed; a retry of a
        // lost commit sends exactly these fills afterward.
        staffing: staffingPlan(manager),
        uncertain: false
      };
    }
    const pending = state.pending;
    const createBtn = document.getElementById('createFolderBtn');
    manager.isCreatingFolder = true;
    context.submitting = true;
    if (createBtn) {
      createBtn.disabled = true;
      createBtn.textContent = 'Confirming…';
    }
    try {
      let response;
      let body;
      try {
        response = await fetch(COMMIT_URL, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
          body: JSON.stringify(pending.request)
        });
        body = await response.json().catch(() => ({}));
      } catch (networkError) {
        pending.uncertain = true;
        throw networkError;
      }
      if (manager.workspaceCreatorContext !== context) return false;
      if (response.status === 400 || response.status === 409) {
        // A definite refusal consumed nothing: return to a fresh review.
        state.pending = null;
        state.review = {
          key: reviewKey(entry, name),
          status: 'error',
          data: null,
          error: `${body?.group_template?.summary || body?.group_requirement?.summary || body?.error || 'This review changed.'} Review it again.`
        };
        state.status = 'idle';
        void load(manager, { force: true });
        return false;
      }
      if (!response.ok || !body?.group_template?.home_workspace_id) {
        pending.uncertain = true;
        throw new Error(
          body?.group_template?.summary ||
            body?.error ||
            'The confirmed group could not be verified.'
        );
      }
      const result = body.group_template;
      // Only a Home this operation created receives the launcher's post-create
      // adoption (Map settle/focus without coordinate writes). A reused Home is
      // never re-placed, re-parented, or treated as newly created. Managed
      // templates are unavailable to selected-member launchers, so no member
      // move can ride this callback.
      if (
        result.created_by_this_operation &&
        typeof context.onCreated === 'function' &&
        !context.selection
      ) {
        try {
          await context.onCreated({
            folder: { id: result.home_workspace_id, name: result.home_name, kind: 'group' },
            groupId: result.home_workspace_id,
            placed: [],
            failed: [],
            uncertain: []
          });
        } catch (callbackError) {
          console.warn(
            'Group was created but its launcher follow-up could not complete:',
            callbackError
          );
        }
      }
      const staffing = Array.isArray(pending.staffing) ? pending.staffing : [];
      if (createBtn && staffing.length) createBtn.textContent = 'Staffing roles…';
      const outcomes = await fillRoles(result.home_workspace_id, staffing);
      await manager.refreshWorkspaceSurfacesAfterOrdinaryGroupCreate?.();
      const folder = (manager.folders || []).find(
        item => String(item?.id) === String(result.home_workspace_id)
      );
      state.pending = null;
      const notice = landingNotice(result, entry, staffing, outcomes, pending.request.name);
      const modalElement = document.getElementById('addFolderModal');
      if (modalElement) bootstrap.Modal.getInstance(modalElement)?.hide();
      manager.resetAddWorkspaceModalForm?.();
      const slug = String(folder?.folder_slug || '').trim();
      if (slug) {
        // The group page is where its roles are managed, so the user lands
        // there instead of reading about it in a toast; the page shows the
        // same words once it has loaded.
        storeLandingNotice(result.home_workspace_id, notice);
        window.location.href = landingURL(slug, notice.roleId);
        return true;
      }
      showFollowUp(manager, result, folder, notice);
      return true;
    } catch (error) {
      if (manager.workspaceCreatorContext === context) {
        manager.showWorkspaceCreateError?.(
          `${error?.message || 'The confirmed request did not return.'} Retry the same confirmed change; Ori will not create a second group.`
        );
      }
      return false;
    } finally {
      manager.isCreatingFolder = false;
      if (context) context.submitting = false;
      if (manager.workspaceCreatorContext === context) manager.refreshWizardChrome?.();
    }
  }

  window.GroupTemplateCreator = {
    GENERAL_ID,
    isManaged,
    isReusable,
    isSelectable,
    nameProblem,
    providerLabel,
    availabilityLabel,
    reviewRequest,
    commitRequest,
    reviewKey,
    stateFor,
    selectedManaged,
    chooserMode,
    hasBlueprintStep,
    fixedEntry,
    catalogEntry,
    templateMeta,
    roleSummary,
    rolesCardHTML,
    managedActive,
    wizardSteps,
    seedFills,
    setFill,
    clearFill,
    stagedFill,
    staffingPlan,
    fillNameProblem,
    promptNote,
    teamRoster,
    renderTeam,
    fillRoles,
    landingNotice,
    landingURL,
    load,
    select,
    render,
    syncPresentation,
    identityProblem,
    renderReceipt,
    ensureReview,
    canSubmit,
    ctaLabel,
    submit
  };
})();
