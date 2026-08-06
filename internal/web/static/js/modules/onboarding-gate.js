// Shared first-run gate for modules that could expose workspace-derived data.
// ES module instances are cached by the browser, so every importer observes one
// memoized onboarding-status request instead of racing independent fetches.

export const ONBOARDING_GATE_LOADING = 'loading';
export const ONBOARDING_GATE_REQUIRED = 'required';
export const ONBOARDING_GATE_READY = 'ready';
export const ONBOARDING_GATE_UNAVAILABLE = 'unavailable';

let injectedFetch = null;
const SHARED_STATUS_PROMISE = '__oriOnboardingStatusPromise';
const SHARED_WORKSPACE_TREE_PROMISE = '__oriInitialWorkspaceTreePromise';

function sharedScope() {
  return typeof window !== 'undefined' ? window : globalThis;
}

export function onboardingGateDecision(status) {
  if (status && status.needs_onboarding === true) {
    return {
      state: ONBOARDING_GATE_REQUIRED,
      allowWorkspaceHydration: false,
      message: 'Finish setup to load your workspaces.'
    };
  }
  if (status && status.needs_onboarding === false) {
    return {
      state: ONBOARDING_GATE_READY,
      allowWorkspaceHydration: true,
      message: ''
    };
  }
  return {
    state: ONBOARDING_GATE_UNAVAILABLE,
    allowWorkspaceHydration: false,
    message: 'Ori could not confirm onboarding status.'
  };
}

function unavailableStatus(error) {
  return {
    needs_onboarding: null,
    onboarding_status_unavailable: true,
    onboarding_status_error: String(error?.message || error || 'Unknown error').slice(0, 200)
  };
}

export function loadOnboardingStatus({ force = false, fetchImpl = null } = {}) {
  const scope = sharedScope();
  if (force) delete scope[SHARED_STATUS_PROMISE];
  if (scope[SHARED_STATUS_PROMISE]) return scope[SHARED_STATUS_PROMISE];

  const request = fetchImpl || injectedFetch || globalThis.fetch;
  scope[SHARED_STATUS_PROMISE] = Promise.resolve()
    .then(async () => {
      if (typeof request !== 'function') throw new Error('Fetch is unavailable');
      const response = await request('/api/onboarding/status');
      if (!response || !response.ok) {
        throw new Error(
          `Onboarding status request failed${response ? ` (${response.status})` : ''}`
        );
      }
      const status = await response.json();
      if (onboardingGateDecision(status).state === ONBOARDING_GATE_UNAVAILABLE) {
        throw new Error('Onboarding status response is invalid');
      }
      return status;
    })
    .catch(error => unavailableStatus(error));

  return scope[SHARED_STATUS_PROMISE];
}

// Home and the global session manager both need the initial workspace tree.
// Cache that one bootstrap read so Map/Tree and Create Workspace consume the
// same payload; later mutation and realtime refreshes intentionally use their
// owning modules' uncached fetch paths.
export function loadInitialWorkspaceTree({ fetchImpl = null } = {}) {
  const scope = sharedScope();
  if (scope[SHARED_WORKSPACE_TREE_PROMISE]) return scope[SHARED_WORKSPACE_TREE_PROMISE];

  const request = fetchImpl || globalThis.fetch;
  scope[SHARED_WORKSPACE_TREE_PROMISE] = Promise.resolve()
    .then(async () => {
      if (typeof request !== 'function') throw new Error('Fetch is unavailable');
      const response = await request('/api/workspaces?tree=true');
      if (!response || !response.ok) {
        throw new Error(`Workspace tree request failed${response ? ` (${response.status})` : ''}`);
      }
      const payload = await response.json();
      if (!payload || !Array.isArray(payload.folders)) {
        throw new Error('Workspace tree response is invalid');
      }
      return payload;
    })
    .catch(error => {
      delete scope[SHARED_WORKSPACE_TREE_PROMISE];
      throw error;
    });

  return scope[SHARED_WORKSPACE_TREE_PROMISE];
}

// Narrow test seam; production callers must use the shared request above.
export function resetOnboardingGateForTests(fetchImpl = null) {
  delete sharedScope()[SHARED_STATUS_PROMISE];
  delete sharedScope()[SHARED_WORKSPACE_TREE_PROMISE];
  injectedFetch = fetchImpl;
}

if (typeof window !== 'undefined') {
  window.OriOnboardingGate = Object.freeze({
    loadOnboardingStatus,
    onboardingGateDecision,
    loadInitialWorkspaceTree
  });
}
