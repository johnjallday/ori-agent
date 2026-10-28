// Ordinary creator transport only. Material choices stay in sessions.js and
// its existing setters; accepted effects belong to the server operation. No
// setup fallback, model call, name-based lookup or automatic POST retry.
const referenceKey = 'ori.workspaceCreationOperation.v1';
const states = new Set([
  'reviewed',
  'accepted',
  'complete',
  'refused',
  'abandoned',
  'reconciliation_required'
]);

function validReference(reference) {
  return Boolean(
    reference &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(reference.id) &&
    /^[0-9a-f]{64}$/.test(reference.digest)
  );
}

export function savedCreationReference(storage) {
  const text = storage.getItem(referenceKey);
  if (!text) return null;
  try {
    const value = JSON.parse(text);
    if (validReference(value)) return { id: value.id, digest: value.digest };
  } catch {
    // A corrupt reference is not permission to start another operation.
  }
  throw new Error(
    'The saved creation reference is unreadable. Preserve it and reconcile the original operation before creating again.'
  );
}

export function retainCreationReference(storage, reference) {
  if (!validReference(reference)) throw new Error('The reviewed creation reference is invalid.');
  const previous = savedCreationReference(storage);
  if (previous && (previous.id !== reference.id || previous.digest !== reference.digest))
    throw new Error('Read the previous creation outcome before starting another operation.');
  storage.setItem(referenceKey, JSON.stringify({ id: reference.id, digest: reference.digest }));
}

export function releaseCreationReference(storage, reference) {
  const previous = savedCreationReference(storage);
  if (!previous || previous.id !== reference.id || previous.digest !== reference.digest)
    return false;
  storage.removeItem(referenceKey);
  return true;
}

const endpoint = reference =>
  `/api/workspaces/create-operations/${encodeURIComponent(reference.id)}`;

function operationView(body, reference, confirming = false) {
  const operation = body?.[confirming ? 'creation_operation' : 'operation'];
  if (
    !operation ||
    operation.id !== reference.id ||
    operation.review_digest !== reference.digest ||
    !states.has(operation.state)
  )
    return null;
  const result = confirming ? body : body.result;
  if (operation.state === 'complete') {
    const folder = result?.folder;
    if (
      result?.success !== true ||
      typeof folder?.id !== 'string' ||
      !folder.id ||
      typeof folder.name !== 'string' ||
      !folder.name.trim() ||
      (folder.agent_instances != null && !Array.isArray(folder.agent_instances)) ||
      folder.shared_data?.workspace_creation_operation_id !== reference.id ||
      folder.shared_data?.workspace_creation_operation_digest !== reference.digest
    )
      return null;
  }
  return {
    state: operation.state,
    operation,
    result: operation.state === 'complete' ? result : null,
    refusal: operation.state === 'refused' ? (confirming ? body : body.refusal) || null : null,
    message:
      typeof body.error === 'string'
        ? body.error
        : typeof body.message === 'string'
          ? body.message
          : typeof body.refusal?.error === 'string'
            ? body.refusal.error
            : '',
    observedWorkspace: body.observed_workspace || null
  };
}

export async function observeCreationOperation(fetcher, reference) {
  if (!validReference(reference)) throw new Error('The reviewed creation reference is invalid.');
  try {
    const response = await fetcher(endpoint(reference), { cache: 'no-store' });
    const body = await response.json();
    return (
      (response.ok && operationView(body, reference)) || {
        state: 'uncertain',
        message:
          'The original creation outcome could not be verified. Read saved status; do not create again.'
      }
    );
  } catch {
    return {
      state: 'uncertain',
      message:
        'The original creation outcome is unreadable. Read saved status; do not create again.'
    };
  }
}

export async function reviewCreationOperation(fetcher, id, material) {
  const response = await fetcher('/api/workspaces/create-operations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id, request: material })
  });
  const body = await response.json();
  const reference = { id, digest: body.operation?.review_digest };
  const view = validReference(reference) && operationView(body, reference);
  if (!response.ok || !view || view.state !== 'reviewed')
    throw new Error(
      typeof body.error === 'string'
        ? body.error
        : 'The exact creation review could not be saved. Nothing new was confirmed.'
    );
  return reference;
}

export async function confirmCreationOperation(fetcher, storage, reference) {
  // If the browser cannot retain the opaque recovery identity, refuse before
  // dispatch. The stored reference is not a resource receipt or material draft.
  retainCreationReference(storage, reference);
  try {
    const response = await fetcher(`${endpoint(reference)}/confirm`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ review_digest: reference.digest })
    });
    const body = await response.json();
    const view = operationView(body, reference, true);
    if (view && view.state !== 'reviewed') return view;
  } catch {
    // Only observation follows a missing or malformed reply, never submission.
  }
  const observed = await observeCreationOperation(fetcher, reference);
  // A read that still sees reviewed may race an in-flight validator. It is
  // not proof that the dispatched request cannot subsequently accept effects.
  return observed.state === 'reviewed'
    ? {
        state: 'uncertain',
        message:
          'Confirmation delivery is uncertain. Keep this operation and read its status; do not create again.'
      }
    : observed;
}

export async function abandonCreationOperation(fetcher, reference) {
  if (!validReference(reference)) throw new Error('The reviewed creation reference is invalid.');
  const response = await fetcher(`${endpoint(reference)}/abandon`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}'
  });
  const body = await response.json();
  const view = operationView(body, reference);
  if (!response.ok || view?.state !== 'abandoned')
    throw new Error(
      typeof body.error === 'string'
        ? body.error
        : 'The review could not be abandoned. Read its original status; closing is not undo.'
    );
  return view;
}
