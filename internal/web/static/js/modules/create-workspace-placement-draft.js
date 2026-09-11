(function initCreateWorkspacePlacementDraft(global) {
  'use strict';

  function normalizedComposition(value) {
    return value === 'grouped' || value === 'standalone' ? value : '';
  }

  function createDraft({ templateKey = '', policy = '', composition = '' } = {}) {
    return {
      policy: String(policy || ''),
      templateKey: String(templateKey || ''),
      composition: normalizedComposition(composition),
      generation: 0,
      status: 'idle',
      projection: null,
      projectionError: '',
      settledFingerprint: '',
      review: null,
      preparedHome: null
    };
  }

  function begin(draft) {
    if (!draft) return null;
    draft.generation = Number(draft.generation || 0) + 1;
    draft.status = 'loading';
    draft.projection = null;
    draft.projectionError = '';
    return {
      generation: draft.generation,
      templateKey: String(draft.templateKey || ''),
      composition: normalizedComposition(draft.composition)
    };
  }

  function owns(draft, ticket) {
    return Boolean(
      draft &&
      ticket &&
      Number(draft.generation || 0) === Number(ticket.generation || 0) &&
      String(draft.templateKey || '') === String(ticket.templateKey || '') &&
      normalizedComposition(draft.composition) === normalizedComposition(ticket.composition)
    );
  }

  function projectionFingerprint(projection) {
    if (!projection || typeof projection !== 'object') return '';
    const roles = Array.isArray(projection.required_home_roles?.roles)
      ? projection.required_home_roles.roles.map(role => ({
          role_id: String(role?.role_id || ''),
          state: String(role?.state || ''),
          agent_name: String(role?.agent?.name || '')
        }))
      : [];
    return JSON.stringify({
      source_revision: String(projection.source_revision || ''),
      state: String(projection.state || ''),
      policy: String(projection.policy || ''),
      selected_composition: String(projection.selected_composition || ''),
      home_exists: projection.home?.exists === true,
      home_workspace_id: String(projection.home?.workspace_id || ''),
      home_name: String(projection.home?.name || ''),
      proposed_name: String(projection.home?.proposed_name || ''),
      verification: String(projection.required_home_roles?.verification || ''),
      roles
    });
  }

  function resolve(draft, ticket, projection) {
    if (!owns(draft, ticket)) return false;
    const fingerprint = projectionFingerprint(projection);
    if (draft.settledFingerprint && draft.settledFingerprint !== fingerprint) {
      draft.review = null;
    }
    draft.status = 'ready';
    draft.projection = projection && typeof projection === 'object' ? projection : null;
    draft.projectionError = '';
    draft.settledFingerprint = fingerprint;
    return true;
  }

  function reject(draft, ticket, message) {
    if (!owns(draft, ticket)) return false;
    draft.status = 'error';
    draft.projection = null;
    draft.projectionError = String(message || 'Could not check the required destination.');
    return true;
  }

  function setComposition(draft, composition) {
    if (!draft) return false;
    const normalized = normalizedComposition(composition);
    if (draft.policy === 'required' && normalized !== 'grouped') return false;
    if (draft.policy === 'none' && normalized !== 'standalone') return false;
    if (draft.composition === normalized) return false;
    draft.composition = normalized;
    draft.generation = Number(draft.generation || 0) + 1;
    draft.status = 'idle';
    draft.projection = null;
    draft.projectionError = '';
    draft.settledFingerprint = '';
    draft.review = null;
    draft.preparedHome = null;
    return true;
  }

  function invalidateReview(draft) {
    if (!draft || !draft.review) return false;
    draft.review = null;
    return true;
  }

  global.CreateWorkspacePlacementDraft = Object.freeze({
    createDraft,
    begin,
    owns,
    resolve,
    reject,
    setComposition,
    invalidateReview,
    projectionFingerprint
  });
})(typeof window !== 'undefined' ? window : globalThis);
