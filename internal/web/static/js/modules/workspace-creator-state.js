// Small, page-local state helpers for the shared Workspace / Group creator.
//
// This deliberately owns no DOM and no network work. sessions.js remains the
// one modal controller and mutation owner; callers get a bounded operation
// context rather than adding another global wizard controller.
(function () {
  'use strict';

  const WORKSPACE_STEPS = ['blueprint', 'details', 'team', 'review'];
  const GROUP_STEPS = ['details', 'roster', 'review'];
  const IMPORT_STEPS = ['details'];

  function kind(value) {
    return String(value || '') === 'group' ? 'group' : 'workspace';
  }

  function modeFor(options) {
    if (options?.importMode || options?.mode === 'import') return 'import';
    if (options?.mode === 'guided') return 'guided';
    if (options?.mode === 'specialist') return 'specialist';
    if (options?.mode === 'selected-members' || options?.selection) return 'selected-members';
    return 'ordinary';
  }

  function cloneDraft(draft) {
    return { ...(draft || {}) };
  }

  function selectionFor(value) {
    if (!value || typeof value !== 'object') return null;
    const ids = Array.isArray(value.ids)
      ? value.ids.map(id => String(id || '').trim()).filter(Boolean)
      : [];
    const names = Array.isArray(value.names)
      ? value.names.map(name => String(name || '').trim())
      : [];
    return ids.length ? { ids, names } : null;
  }

  function fixedKindFor(options, mode) {
    if (mode === 'import') return 'workspace';
    if (mode === 'selected-members') return 'group';
    const requested = String(options?.fixedKind || '').trim();
    return requested === 'workspace' || requested === 'group' ? requested : '';
  }

  function nameKey(value) {
    return String(value || '')
      .trim()
      .toLowerCase();
  }

  // A team lock keeps named blueprint agents from being renamed in this
  // creator, with a one-line reason. It is presentation plus a save guard; the
  // server still validates the reviewed roster.
  function normalizeTeamLock(value) {
    if (!value || typeof value !== 'object') return null;
    const agentNames = (Array.isArray(value.agentNames) ? value.agentNames : [])
      .map(name => String(name || '').trim())
      .filter(Boolean);
    const reason = String(value.reason || '').trim();
    return agentNames.length && reason ? { agentNames, reason } : null;
  }

  // teamLockReason returns the lock reason when originalName is locked, or ''.
  function teamLockReason(context, originalName) {
    const lock = normalizeTeamLock(context?.teamLock);
    if (!lock || !nameKey(originalName)) return '';
    return lock.agentNames.some(name => nameKey(name) === nameKey(originalName)) ? lock.reason : '';
  }

  // lockedRenameRefusal returns the reason to refuse saving a new name for a
  // locked agent, or '' when the save may proceed.
  function lockedRenameRefusal(context, originalName, requestedName) {
    const reason = teamLockReason(context, originalName);
    return reason && nameKey(requestedName) !== nameKey(originalName) ? reason : '';
  }

  function createCreatorContext(options = {}) {
    const mode = modeFor(options);
    const fixedKind = fixedKindFor(options, mode);
    const requestedKind = kind(options.kind);
    return {
      generation: Math.max(1, Number(options.generation) || 1),
      mode,
      kind: fixedKind || requestedKind,
      fixedKind,
      entryPoint: String(options.entryPoint || '').trim(),
      blueprint: String(options.blueprint || '').trim(),
      postCreateAction: String(options.postCreateAction || '').trim(),
      mapOrigin: Boolean(options.mapOrigin),
      // A caller that already owns the destination — a group page's Build —
      // locks the parent, so the wizard preselects it, refuses to place the
      // workspace anywhere else, and skips the placement question entirely.
      parentId: String(options.parentId || '').trim(),
      parentName: String(options.parentName || '').trim(),
      parentLocked: Boolean(options.parentLocked) && String(options.parentId || '').trim() !== '',
      selection: selectionFor(options.selection),
      invoker: options.invoker || null,
      // name pre-fills the Details step; a caller that already knows what the
      // workspace is for (a folder the assistant was shown) supplies it, and
      // the user can still change it before creating.
      name: String(options.name || '').trim(),
      // folderOfferId ties the create to a "show me a folder" offer, so the
      // server can attach that folder after creation. Only the identifier
      // travels; the server holds the folder itself.
      folderOfferId: String(options.folderOfferId || '').trim(),
      // blueprintNote is the one-line reason the preferred blueprint was not
      // used (it is not installed), shown under the name field.
      blueprintNote: String(options.blueprintNote || '').trim(),
      onCreated: typeof options.onCreated === 'function' ? options.onCreated : null,
      guided: options.guided && typeof options.guided === 'object' ? options.guided : null,
      teamLock: normalizeTeamLock(options.teamLock),
      // stayAfterCreate keeps the current page after a Workspace is created, so
      // a caller such as a setup quest can continue instead of navigating away.
      stayAfterCreate: Boolean(options.stayAfterCreate),
      // stageBlueprintRoles proposes the blueprint's whole team once in the draft.
      stageBlueprintRoles: Boolean(options.stageBlueprintRoles),
      blueprintRolesStaged: false,
      // buildSession is the assistant's build session for this open, or null
      // for the manual wizard. buildFirstMessage is a sentence the opener
      // already heard (the assistant's Ask tab), posted as the first turn.
      buildSession: null,
      buildFirstMessage: String(options.buildFirstMessage || '').trim(),
      // buildResume opens straight into the open build: the user already
      // chose Resume (from the assistant's Today).
      buildResume: Boolean(options.buildResume),
      drafts: {
        workspace: cloneDraft(options.drafts?.workspace),
        group: cloneDraft(options.drafts?.group)
      },
      review: options.review || null,
      submitting: false,
      knownCreatedGroup: null
    };
  }

  function creatorSteps(context) {
    if (context?.mode === 'import') return [...IMPORT_STEPS];
    // Guided Home preparation has its own reviewed setup owner and deliberately
    // cannot add a roster in this dialog.
    if (context?.kind === 'group' && context?.mode !== 'guided') return [...GROUP_STEPS];
    return context?.kind === 'group' ? ['details', 'review'] : [...WORKSPACE_STEPS];
  }

  function switchCreatorKind(context, nextKind) {
    if (!context || context.fixedKind) return { changed: false, context };
    const next = kind(nextKind);
    if (context.kind === next) return { changed: false, context };
    return {
      changed: true,
      context: {
        ...context,
        generation: Math.max(1, Number(context.generation) || 1) + 1,
        kind: next,
        review: null,
        submitting: false,
        drafts: {
          workspace: cloneDraft(context.drafts?.workspace),
          group: cloneDraft(context.drafts?.group)
        }
      }
    };
  }

  // Do not build a Group request by removing fields from a Workspace request:
  // future Workspace fields would then leak silently. This explicit allowlist is
  // the client counterpart to the server's Group project/team restrictions.
  function buildOrdinaryGroupPayload(values = {}) {
    const payload = {
      name: String(values.name || '').trim(),
      kind: 'group',
      // This declares a reviewed Group Roster. sessions.js adds only the
      // final roster receipt/overrides at Create; nothing creates an agent
      // while the person is editing this draft.
      group_roster: true,
      create_template_agents: true
    };
    const description = String(values.description || '').trim();
    const parentID = String(values.parent_id || '').trim();
    const color = String(values.color || '').trim();
    if (description) payload.description = description;
    if (parentID) payload.parent_id = parentID;
    if (color) payload.color = color;
    return payload;
  }

  // ----- Build with your assistant -----

  // The openers that start the creator as a conversation with the assistant.
  // Every other opener — a folder offer's Adjust…, specialist setup, a guided
  // journey, import, a group template, a blueprint deep link, a page that
  // locks the parent — keeps today's manual wizard unchanged.
  const BUILD_ENTRY_POINTS = [
    'home_cockpit_create',
    'workspace_map_build',
    'workspace_hub_create',
    'personal_assistant_ask'
  ];

  function buildEntryPointEligible(options = {}) {
    const entryPoint = String(options.entryPoint || '').trim();
    if (!BUILD_ENTRY_POINTS.includes(entryPoint)) return false;
    if (options.importMode || modeFor(options) !== 'ordinary') return false;
    if (kind(options.kind) === 'group' || String(options.fixedKind || '') === 'group') return false;
    if (options.parentLocked) return false;
    // A blueprint chosen by the opener (a deep link) or a folder offer is a
    // decision already made; the conversation would only second-guess it.
    if (String(options.blueprint || '').trim()) return false;
    if (String(options.folderOfferId || '').trim()) return false;
    return true;
  }

  // buildModeEligible is the whole offer rule: an eligible opener, a hired
  // assistant (active or paused, the same rule Today uses), and a model the
  // server can reach for it.
  function buildModeEligible(options = {}) {
    if (!buildEntryPointEligible(options)) return false;
    const state = String(options.assistantState || '').trim();
    if (state !== 'active' && state !== 'paused') return false;
    return Boolean(options.availability && options.availability.available === true);
  }

  // buildStepFor is the furthest wizard step a build draft has earned. The
  // wizard's own gates are the facts passed in; the model's opinion of
  // readiness is not one of them. The result never goes below the current
  // step: the assistant advances the wizard, it never pulls the user back.
  function buildStepFor(draft, facts = {}) {
    const current = Math.min(4, Math.max(1, Number(facts.current) || 1));
    const value = draft && typeof draft === 'object' ? draft : {};
    let step = 1;
    const hasBlueprint = value.blank === true || String(value.template_id || '').trim() !== '';
    if (hasBlueprint && facts.blueprintReady !== false) {
      step = 2;
      const hasIdentity =
        String(value.name || '').trim() !== '' && String(value.description || '').trim() !== '';
      if (hasIdentity && facts.nameValid !== false && facts.inputsValid !== false) {
        step = 3;
        if (facts.teamSet || facts.agentless) step = 4;
      }
    }
    return Math.max(current, step);
  }

  // The draft fields a build turn can report as set, mapped to the create
  // request keys the wizard applies. "team" is applied through the team draft,
  // not through this patch.
  const BUILD_PATCH_FIELDS = {
    name: ['name'],
    description: ['description'],
    inputs: ['blueprint_inputs'],
    parent: ['parent_id'],
    color: ['color'],
    tags: ['tags']
  };

  // The server leaves an emptied field out of the draft. When a turn reports
  // it set one of these and the key is gone, the assistant cleared it.
  const BUILD_CLEARED_VALUES = {
    description: '',
    parent_id: '',
    color: '',
    tags: []
  };

  function clonePlain(value) {
    if (value === null || typeof value !== 'object') return value;
    return JSON.parse(JSON.stringify(value));
  }

  // buildPatchFromSession picks the fields a session reports as just set from
  // its draft. With no field list it picks every field the draft holds, which
  // is how a resumed build restores the whole form.
  function buildPatchFromSession(session, fields) {
    const draft = session?.draft && typeof session.draft === 'object' ? session.draft : {};
    const wanted = Array.isArray(fields)
      ? fields
      : ['blueprint', ...Object.keys(BUILD_PATCH_FIELDS)].filter(field => {
          if (field === 'blueprint') return draft.blank === true || 'template_id' in draft;
          return BUILD_PATCH_FIELDS[field].some(key => key in draft);
        });
    const patch = {};
    for (const field of wanted) {
      if (field === 'blueprint') {
        if (draft.blank === true) patch.blank = true;
        else if ('template_id' in draft) patch.template_id = String(draft.template_id || '');
        continue;
      }
      for (const key of BUILD_PATCH_FIELDS[field] || []) {
        if (key in draft) patch[key] = clonePlain(draft[key]);
        else if (Array.isArray(fields) && key in BUILD_CLEARED_VALUES) {
          patch[key] = clonePlain(BUILD_CLEARED_VALUES[key]);
        }
      }
    }
    return patch;
  }

  window.WorkspaceCreatorState = {
    createCreatorContext,
    creatorSteps,
    switchCreatorKind,
    buildOrdinaryGroupPayload,
    teamLockReason,
    lockedRenameRefusal,
    BUILD_ENTRY_POINTS,
    buildEntryPointEligible,
    buildModeEligible,
    buildStepFor,
    buildPatchFromSession
  };
})();
