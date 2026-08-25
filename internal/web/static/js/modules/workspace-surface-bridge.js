const PROTOCOL_VERSION = 1;
const MAX_MESSAGE_BYTES = 64 * 1024;
const MAX_MESSAGE_DEPTH = 16;
const MAX_REQUEST_ID = 64;

const FRAME_REQUESTS = new Set([
  'ori.surface.operation.invoke',
  'ori.surface.state.get',
  'ori.surface.state.set',
  'ori.surface.state.delete',
  'ori.surface.host.confirm',
  'ori.surface.host.ask_ori',
  'ori.surface.host.open_setup',
  'ori.surface.host.close',
  'ori.surface.status_changed'
]);

function encodedBytes(value) {
  try {
    return new TextEncoder().encode(JSON.stringify(value)).length;
  } catch (_err) {
    return Infinity;
  }
}

function boundedDepth(value, depth = 0, seen = new Set()) {
  if (depth > MAX_MESSAGE_DEPTH) return false;
  if (value === null || typeof value !== 'object') return true;
  if (seen.has(value)) return false;
  seen.add(value);
  const entries = Array.isArray(value) ? value : Object.values(value);
  if (entries.length > 256) return false;
  for (const child of entries) {
    if (!boundedDepth(child, depth + 1, seen)) return false;
  }
  seen.delete(value);
  return true;
}

function validEnvelope(message) {
  if (!message || Object.getPrototypeOf(message) !== Object.prototype) return false;
  const keys = Object.keys(message).sort();
  const allowed = ['bridge_id', 'payload', 'protocol_version', 'request_id', 'type'];
  if (keys.length !== allowed.length || !keys.every((key, index) => key === allowed[index])) {
    return false;
  }
  if (message.protocol_version !== PROTOCOL_VERSION) return false;
  if (typeof message.bridge_id !== 'string' || message.bridge_id.length === 0) return false;
  if (
    typeof message.request_id !== 'string' ||
    message.request_id.length === 0 ||
    message.request_id.length > MAX_REQUEST_ID
  ) {
    return false;
  }
  if (typeof message.type !== 'string') return false;
  if (!message.payload || Object.getPrototypeOf(message.payload) !== Object.prototype) return false;
  return encodedBytes(message) <= MAX_MESSAGE_BYTES && boundedDepth(message);
}

function secureToken(cryptoImpl = globalThis.crypto) {
  if (!cryptoImpl || typeof cryptoImpl.getRandomValues !== 'function') {
    throw new Error('secure browser randomness is unavailable');
  }
  const bytes = new Uint8Array(32);
  cryptoImpl.getRandomValues(bytes);
  let binary = '';
  bytes.forEach(value => {
    binary += String.fromCharCode(value);
  });
  if (typeof btoa === 'function') {
    return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '');
  }
  return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
}

export class ParentSurfaceBridge {
  constructor(options = {}) {
    if (!options.frameWindow || typeof options.frameWindow.postMessage !== 'function') {
      throw new Error('an exact frame window is required');
    }
    this.frameWindow = options.frameWindow;
    this.eventTarget = options.eventTarget || globalThis.window || null;
    this.bridgeId = options.bridgeId || secureToken(options.crypto);
    this.challenge = options.challenge || secureToken(options.crypto);
    this.surface = Object.freeze({ ...(options.surface || {}) });
    this.features = Object.freeze({ ...(options.features || {}) });
    this.onRequest =
      typeof options.onRequest === 'function'
        ? options.onRequest
        : async () => ({
            ok: false,
            error: { code: 'host_intent_unavailable', message: 'That host action is unavailable.' }
          });
    this.ready = false;
    this.destroyed = false;
    this._boundMessage = event => {
      void this.receive(event);
    };
  }

  start() {
    if (this.destroyed) return;
    if (this.eventTarget && typeof this.eventTarget.addEventListener === 'function') {
      this.eventTarget.addEventListener('message', this._boundMessage);
    }
    this._send('ori.surface.challenge', 'handshake', {
      challenge: this.challenge,
      surface: this.surface
    });
  }

  async receive(event) {
    if (this.destroyed || !event || event.source !== this.frameWindow) return false;
    const message = event.data;
    if (!validEnvelope(message) || message.bridge_id !== this.bridgeId) return false;

    if (message.type === 'ori.surface.ready') {
      if (this.ready) return false;
      const payload = message.payload;
      if (
        payload.challenge !== this.challenge ||
        !Array.isArray(payload.supported_protocols) ||
        !payload.supported_protocols.includes(PROTOCOL_VERSION)
      ) {
        return false;
      }
      this.challenge = '';
      this.ready = true;
      this._send('ori.surface.init', message.request_id, {
        surface: this.surface,
        features: this.features
      });
      return true;
    }

    if (!this.ready || !FRAME_REQUESTS.has(message.type)) return false;
    let response;
    try {
      response = await this.onRequest({
        type: message.type,
        requestId: message.request_id,
        payload: message.payload
      });
    } catch (_err) {
      response = {
        ok: false,
        error: { code: 'host_request_failed', message: 'Ori could not complete that request.' }
      };
    }
    if (!response || typeof response !== 'object') {
      response = {
        ok: false,
        error: { code: 'host_request_failed', message: 'Ori could not complete that request.' }
      };
    }
    this._send('ori.surface.response', message.request_id, response);
    return true;
  }

  visibility(visible) {
    if (!this.ready || this.destroyed) return;
    this._send('ori.surface.host.visibility', 'visibility', { visible: Boolean(visible) });
  }

  invalidate(code = 'session_invalidated') {
    if (this.ready && !this.destroyed) {
      this._send('ori.surface.host.invalidated', 'invalidated', {
        code,
        message: 'This plugin surface is no longer available.'
      });
    }
    this.destroy();
  }

  destroy() {
    if (this.destroyed) return;
    this.destroyed = true;
    this.ready = false;
    if (this.eventTarget && typeof this.eventTarget.removeEventListener === 'function') {
      this.eventTarget.removeEventListener('message', this._boundMessage);
    }
  }

  _send(type, requestId, payload) {
    const message = {
      protocol_version: PROTOCOL_VERSION,
      bridge_id: this.bridgeId,
      type,
      request_id: String(requestId || 'host').slice(0, MAX_REQUEST_ID),
      payload: payload || {}
    };
    if (encodedBytes(message) > MAX_MESSAGE_BYTES || !boundedDepth(message)) return false;
    // An opaque sandbox origin cannot be named as targetOrigin. Security comes
    // from sending to the exact contentWindow plus bridge/challenge checks.
    this.frameWindow.postMessage(message, '*');
    return true;
  }
}

export const WORKSPACE_SURFACE_BRIDGE = Object.freeze({
  PROTOCOL_VERSION,
  MAX_MESSAGE_BYTES,
  MAX_MESSAGE_DEPTH,
  FRAME_REQUESTS
});
