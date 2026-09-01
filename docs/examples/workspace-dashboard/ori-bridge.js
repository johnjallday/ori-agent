/*
 * Minimal Ori dashboard bridge. Copy this file into your dashboard folder
 * unchanged and load it before your own script.
 *
 * A dashboard runs in a sandboxed iframe with `connect-src 'none'`, so it
 * cannot fetch() anything at all. Every byte of data arrives over postMessage
 * from the Ori host, using the envelope below.
 *
 * Handshake:
 *   host  -> frame   ori.surface.challenge  { challenge, surface }
 *   frame -> host    ori.surface.ready      { challenge, supported_protocols }
 *   host  -> frame   ori.surface.init       { surface, features }
 *
 * After init, the frame may send requests; the host answers each with an
 * ori.surface.response carrying the same request_id.
 *
 * Exposes a global `Ori` with:
 *   Ori.whenReady()                  -> Promise resolved once the host is ready
 *   Ori.invoke(operationId, input)   -> Promise of that operation's result
 */
window.Ori = (function () {
  const PROTOCOL_VERSION = 1;
  const pending = new Map();
  let bridgeId = '';
  let hostWindow = null;
  let nextRequestId = 1;

  let markReady;
  const readyPromise = new Promise(function (resolve) {
    markReady = resolve;
  });

  function send(type, requestId, payload) {
    // A sandboxed document has an opaque origin and cannot name the host's
    // origin as targetOrigin. The host validates bridge_id on its side.
    hostWindow.postMessage(
      {
        protocol_version: PROTOCOL_VERSION,
        bridge_id: bridgeId,
        type: type,
        request_id: String(requestId),
        payload: payload || {}
      },
      '*'
    );
  }

  window.addEventListener('message', function (event) {
    const message = event.data;
    if (!message || typeof message !== 'object') return;
    if (message.protocol_version !== PROTOCOL_VERSION) return;
    // Accept the first bridge id we are handed, then require it to match.
    if (bridgeId && message.bridge_id !== bridgeId) return;

    if (message.type === 'ori.surface.challenge') {
      bridgeId = message.bridge_id;
      hostWindow = event.source;
      send('ori.surface.ready', 'ready', {
        challenge: message.payload.challenge,
        supported_protocols: [PROTOCOL_VERSION]
      });
      return;
    }
    if (message.type === 'ori.surface.init') {
      markReady(message.payload);
      return;
    }
    if (message.type === 'ori.surface.response') {
      const entry = pending.get(message.request_id);
      if (!entry) return;
      pending.delete(message.request_id);
      if (message.payload.ok) {
        entry.resolve(message.payload.result);
      } else {
        const error = message.payload.error || {};
        entry.reject(new Error(error.message || 'The Ori host refused that request'));
      }
      return;
    }
    if (message.type === 'ori.surface.host.invalidated') {
      pending.forEach(function (entry) {
        entry.reject(new Error('This dashboard session ended. Reload the workspace.'));
      });
      pending.clear();
    }
  });

  function request(type, payload) {
    return new Promise(function (resolve, reject) {
      if (!hostWindow) {
        reject(new Error('The Ori host is not ready yet'));
        return;
      }
      const requestId = String(nextRequestId++);
      pending.set(requestId, { resolve: resolve, reject: reject });
      send(type, requestId, payload);
    });
  }

  return {
    whenReady: function () {
      return readyPromise;
    },
    invoke: function (operationId, input) {
      return request('ori.surface.operation.invoke', {
        operation_id: operationId,
        input: input || {}
      });
    }
  };
})();
