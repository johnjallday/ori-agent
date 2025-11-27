/**
 * Agent Canvas API helper to standardize fetch calls and errors.
 */

async function handleResponse(response) {
  if (response.ok) {
    // Attempt JSON, fallback to text
    const contentType = response.headers.get('content-type') || '';
    if (contentType.includes('application/json')) {
      return response.json();
    }
    return response.text();
  }

  const text = await response.text();
  const message = text || `${response.status} ${response.statusText}`;
  throw new Error(message);
}

export async function apiGet(url) {
  const resp = await fetch(url);
  return handleResponse(resp);
}

export async function apiPost(url, body) {
  const resp = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse(resp);
}

export async function apiPut(url, body) {
  const resp = await fetch(url, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse(resp);
}

export async function apiDelete(url) {
  const resp = await fetch(url, { method: 'DELETE' });
  return handleResponse(resp);
}
