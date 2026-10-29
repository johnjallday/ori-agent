// Read-only adapter to the existing requirement-owned creation journal.
// createFolder remains the sole frontend commit path. A retained reference is
// never a material draft or permission to dispatch/replay the creation POST.
const URL = '/api/workspaces/group-requirements/observe';
const STORAGE_KEY = 'ori:grouped-workspace-operation:v1';

export function groupedCreationReference(value) {
  const token = value?.group_review_token;
  const key = value?.idempotency_key;
  const digest = value?.input_digest;
  if (
    typeof token !== 'string' ||
    !token ||
    token.length > 200 ||
    typeof key !== 'string' ||
    !key ||
    key.length > 200 ||
    typeof digest !== 'string' ||
    !/^[a-f0-9]{64}$/.test(digest)
  ) {
    throw new Error(
      'The original grouped creation reference is unreadable. Do not submit a replacement creation.'
    );
  }
  return Object.freeze({ group_review_token: token, idempotency_key: key, input_digest: digest });
}

const sameReference = (left, right) =>
  left.group_review_token === right.group_review_token &&
  left.idempotency_key === right.idempotency_key &&
  left.input_digest === right.input_digest;

export function savedGroupedCreationReference(storage) {
  const raw = storage.getItem(STORAGE_KEY);
  return raw ? groupedCreationReference(JSON.parse(raw).reference) : null;
}

// The reviewed point is repair data for this exact reference only. It never
// authorizes creation and is dropped unless it is a finite point.
export function savedGroupedCreationGeometry(storage, value) {
  const raw = storage.getItem(STORAGE_KEY);
  if (!raw) return null;
  const record = JSON.parse(raw);
  if (!sameReference(groupedCreationReference(record.reference), groupedCreationReference(value)))
    return null;
  const candidate = record.geometry?.candidate;
  return Number.isFinite(candidate?.x) && Number.isFinite(candidate?.y)
    ? { candidate: { x: candidate.x, y: candidate.y } }
    : null;
}

export function retainGroupedCreationReference(storage, value, geometry = null) {
  const reference = groupedCreationReference(value);
  const previous = savedGroupedCreationReference(storage);
  if (previous && !sameReference(previous, reference)) {
    throw new Error(
      'Another grouped creation is retained. Read its original status before confirming anything else.'
    );
  }
  storage.setItem(STORAGE_KEY, JSON.stringify({ reference, geometry }));
  return reference;
}

export function releaseGroupedCreationReference(storage, value) {
  const reference = groupedCreationReference(value);
  const previous = savedGroupedCreationReference(storage);
  if (previous && sameReference(previous, reference)) storage.removeItem(STORAGE_KEY);
}

export async function observeGroupedCreation(fetcher, value) {
  const reference = groupedCreationReference(value);
  const response = await fetcher(URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(reference)
  });
  const body = await response.json().catch(() => ({}));
  const outcome = body?.group_requirement_operation;
  if (response.status !== 200 || outcome?.state !== 'complete') {
    return {
      state: outcome?.state || 'outcome_unknown',
      // No operation record. Alone this proves nothing about an in-flight
      // request; only a caller that already received that request's final
      // reply may treat it as "nothing was accepted".
      missing: response.status === 404,
      message:
        outcome?.summary ||
        'The original grouped creation is not observable. Reading status resumed nothing.'
    };
  }
  const folder = body.folder;
  if (
    typeof outcome.workspace_id !== 'string' ||
    !outcome.workspace_id ||
    folder?.id !== outcome.workspace_id ||
    folder.kind !== 'workspace' ||
    ['trashed', 'missing'].includes(folder.status) ||
    outcome.layout_subject_new !== true ||
    typeof outcome.operation_digest !== 'string' ||
    !/^[a-f0-9]{64}$/.test(outcome.operation_digest)
  ) {
    return {
      state: 'outcome_unknown',
      message:
        'The original grouped result could not be verified. No replacement creation or positioning was attempted.'
    };
  }
  return {
    state: 'complete',
    result: folder,
    proof: outcome,
    message: outcome.summary || 'Original workspace creation observed.'
  };
}
