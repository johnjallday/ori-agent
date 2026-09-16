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
      onCreated: typeof options.onCreated === 'function' ? options.onCreated : null,
      guided: options.guided && typeof options.guided === 'object' ? options.guided : null,
      teamLock: normalizeTeamLock(options.teamLock),
      // stayAfterCreate keeps the current page after a Workspace is created, so
      // a caller such as a setup quest can continue instead of navigating away.
      stayAfterCreate: Boolean(options.stayAfterCreate),
      // stageBlueprintRoles proposes the blueprint's whole team once in the draft.
      stageBlueprintRoles: Boolean(options.stageBlueprintRoles),
      blueprintRolesStaged: false,
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

  window.WorkspaceCreatorState = {
    createCreatorContext,
    creatorSteps,
    switchCreatorKind,
    buildOrdinaryGroupPayload,
    teamLockReason,
    lockedRenameRefusal
  };
})();
