import { newRequestID, secondBrainError } from './personal-assistant-second-brain.js';

export const NOTES_ENDPOINT = '/api/personal-assistant/second-brain';

export async function notesRequest(path = '', options = {}) {
  const response = await fetch(NOTES_ENDPOINT + path, { cache: 'no-store', ...options });
  const payload = await response.json().catch(() => null);
  if (!response.ok || !payload) {
    const error = new Error(secondBrainError(payload, 'Ori could not load your notes. Try again.'));
    error.status = response.status;
    error.retryAfterSeconds =
      Number(response.headers?.get?.('Retry-After')) || Number(payload?.retry_after_seconds) || 0;
    throw error;
  }
  return payload;
}

export function notesPost(path, body, requestID = newRequestID('notes')) {
  return notesRequest(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...body, request_id: requestID })
  });
}
