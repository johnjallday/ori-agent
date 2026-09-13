// Small, page-local state helpers for the shared Workspace / Group creator.
//
// This deliberately owns no DOM and no network work. sessions.js remains the
// one modal controller and mutation owner; callers get a bounded operation
// context rather than adding another global wizard controller.
(function () {
  'use strict';

  const WORKSPACE_STEPS = ['blueprint', 'details', 'team', 'review'];
  const GROUP_STEPS = ['details', 'review'];
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
      selection: selectionFor(options.selection),
      invoker: options.invoker || null,
      onCreated: typeof options.onCreated === 'function' ? options.onCreated : null,
      guided: options.guided && typeof options.guided === 'object' ? options.guided : null,
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
    return context?.kind === 'group' ? [...GROUP_STEPS] : [...WORKSPACE_STEPS];
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
      create_template_agents: false
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
    buildOrdinaryGroupPayload
  };
})();
