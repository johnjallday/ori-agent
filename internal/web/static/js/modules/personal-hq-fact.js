// The rules for one reviewed Personal HQ fact, shared by every surface that
// lets the user write one: the remembered-facts page and the assistant's
// Remember… review. The server validates the same rules again on every save.

export const MAX_FACT_BYTES = 500;

const encoder = new TextEncoder();

/** The fact's size the way the server measures it: UTF-8 bytes. */
export function factByteLength(text) {
  return encoder.encode(String(text ?? '')).length;
}

/**
 * Whether text is one exact, safe line: no leading, trailing, or repeated
 * whitespace, no control or bidirectional override characters, and at most
 * MAX_FACT_BYTES bytes. Nothing is trimmed or cut to make it fit.
 */
export function reviewTextValid(text) {
  return (
    typeof text === 'string' &&
    text.length > 0 &&
    text.trim() === text &&
    text === text.split(/\s+/u).join(' ') &&
    !Array.from(text).some(character => {
      const code = character.codePointAt(0);
      return (
        code < 32 ||
        (code >= 127 && code <= 159) ||
        (code >= 0x202a && code <= 0x202e) ||
        (code >= 0x2066 && code <= 0x2069)
      );
    }) &&
    factByteLength(text) <= MAX_FACT_BYTES
  );
}

/** A retry key for one user action. It identifies the action, not the fact. */
export function newRequestID() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  if (!globalThis.crypto?.getRandomValues)
    throw new Error('Secure retry IDs are unavailable. Reload on a secure connection.');
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
}
