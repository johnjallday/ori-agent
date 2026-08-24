const PROTOCOL_VERSION = 1;
const MAX_CONTEXT_BYTES = 2000;

function secureID(cryptoImpl = globalThis.crypto) {
  if (!cryptoImpl || typeof cryptoImpl.getRandomValues !== 'function') {
    throw new Error('secure browser randomness is unavailable');
  }
  const bytes = new Uint8Array(16);
  cryptoImpl.getRandomValues(bytes);
  return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
}

function plainObject(value) {
  return Boolean(value) && Object.getPrototypeOf(value) === Object.prototype;
}

export class OriWorkspaceSurfaceSDK {
  constructor(options = {}) {
    this.eventTarget = options.eventTarget || globalThis.window || null;
    this.parentWindow = options.parentWindow || this.eventTarget?.parent || null;
    this.crypto = options.crypto || globalThis.crypto;
    this.bridgeId = '';
    this.surface = null;
    this.features = Object.freeze({});
    this.ready = false;
    this.invalidated = false;
    this.pending = new Map();
    this.listeners = new Map();
    this._boundMessage = event => this._receive(event);
  }

  start() {
    if (!this.eventTarget || !this.parentWindow || this.parentWindow === this.eventTarget) {
      throw new Error('Workspace Surface SDK must run inside an Ori frame');
    }
    this.eventTarget.addEventListener('message', this._boundMessage);
    return this;
  }

  destroy() {
    this.eventTarget?.removeEventListener?.('message', this._boundMessage);
    this.invalidated = true;
    this.ready = false;
    const error = new Error('Workspace Surface session ended');
    error.code = 'session_invalidated';
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }

  on(type, listener) {
    if (typeof listener !== 'function') return () => {};
    const listeners = this.listeners.get(type) || new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
    return () => listeners.delete(listener);
  }

  invoke(operationId, input = {}) {
    return this._request('ori.surface.operation.invoke', {
      operation_id: String(operationId || ''),
      input
    });
  }

  getState(key) {
    return this._request('ori.surface.state.get', { key: String(key || '') });
  }

  setState(key, value, options = {}) {
    return this._request('ori.surface.state.set', {
      key: String(key || ''),
      schema_version: Number(options.schemaVersion || 1),
      expected_revision: String(options.expectedRevision || '0'),
      value
    });
  }

  deleteState(key, expectedRevision) {
    return this._request('ori.surface.state.delete', {
      key: String(key || ''),
      expected_revision: String(expectedRevision || '0')
    });
  }

  confirm(confirmationId) {
    return this._request('ori.surface.host.confirm', {
      confirmation_id: String(confirmationId || '')
    });
  }

  askOri(context) {
    const text = String(context || '');
    if (new TextEncoder().encode(text).length > MAX_CONTEXT_BYTES) {
      const error = new Error('Ask Ori context is too large');
      error.code = 'input_too_large';
      return Promise.reject(error);
    }
    return this._request('ori.surface.host.ask_ori', { context: text });
  }

  openSetup() {
    return this._request('ori.surface.host.open_setup', {});
  }

  close() {
    return this._request('ori.surface.host.close', {});
  }

  statusChanged() {
    return this._request('ori.surface.status_changed', {});
  }

  _request(type, payload) {
    if (!this.ready || this.invalidated) {
      const error = new Error('Workspace Surface bridge is not ready');
      error.code = this.invalidated ? 'session_invalidated' : 'bridge_not_ready';
      return Promise.reject(error);
    }
    const requestId = secureID(this.crypto);
    return new Promise((resolve, reject) => {
      this.pending.set(requestId, { resolve, reject });
      this.parentWindow.postMessage(
        {
          protocol_version: PROTOCOL_VERSION,
          bridge_id: this.bridgeId,
          type,
          request_id: requestId,
          payload: plainObject(payload) ? payload : {}
        },
        '*'
      );
    });
  }

  _receive(event) {
    if (!event || event.source !== this.parentWindow || !plainObject(event.data)) return;
    const message = event.data;
    if (message.protocol_version !== PROTOCOL_VERSION || !plainObject(message.payload)) return;

    if (message.type === 'ori.surface.challenge') {
      if (this.bridgeId || typeof message.bridge_id !== 'string' || !message.payload.challenge)
        return;
      this.bridgeId = message.bridge_id;
      this.parentWindow.postMessage(
        {
          protocol_version: PROTOCOL_VERSION,
          bridge_id: this.bridgeId,
          type: 'ori.surface.ready',
          request_id: message.request_id,
          payload: {
            challenge: message.payload.challenge,
            sdk_version: '1.0.0',
            supported_protocols: [PROTOCOL_VERSION]
          }
        },
        '*'
      );
      return;
    }

    if (!this.bridgeId || message.bridge_id !== this.bridgeId) return;
    if (message.type === 'ori.surface.init') {
      this.surface = Object.freeze({ ...(message.payload.surface || {}) });
      this.features = Object.freeze({ ...(message.payload.features || {}) });
      this.ready = true;
      this._emit('ready', { surface: this.surface, features: this.features });
      return;
    }
    if (message.type === 'ori.surface.host.visibility') {
      this._emit('visibility', { visible: Boolean(message.payload.visible) });
      return;
    }
    if (message.type === 'ori.surface.host.invalidated') {
      this._emit('invalidated', message.payload);
      this.destroy();
      return;
    }
    if (message.type !== 'ori.surface.response') return;
    const pending = this.pending.get(message.request_id);
    if (!pending) return;
    this.pending.delete(message.request_id);
    if (message.payload.ok) {
      pending.resolve(message.payload.result);
      return;
    }
    const error = new Error(message.payload.error?.message || 'Ori could not complete the request');
    error.code = message.payload.error?.code || 'host_request_failed';
    error.confirmationId = message.payload.error?.confirmation_id || '';
    pending.reject(error);
  }

  _emit(type, detail) {
    for (const listener of this.listeners.get(type) || []) {
      try {
        listener(detail);
      } catch (_err) {
        // One plugin listener cannot break bridge processing for the others.
      }
    }
  }
}

export function createWorkspaceSurfaceSDK(options = {}) {
  return new OriWorkspaceSurfaceSDK(options).start();
}

if (typeof window !== 'undefined') {
  window.OriWorkspaceSurfaceSDK = Object.freeze({
    create: createWorkspaceSurfaceSDK,
    SDK: OriWorkspaceSurfaceSDK,
    protocolVersion: PROTOCOL_VERSION
  });
}
