import { ParentSurfaceBridge } from './workspace-surface-bridge.js';
import { workspaceOverlayCoordinator } from './workspace-overlay-coordinator.js';

const HOST_ICON_CLASSES = Object.freeze({
  puzzle: 'bi-puzzle',
  sliders: 'bi-sliders2-vertical',
  waveform: 'bi-soundwave',
  folder: 'bi-folder2-open',
  compass: 'bi-compass'
});

const STATE_TONES = Object.freeze({
  checking: 'loading',
  ready: 'clear',
  attention: 'attention',
  degraded: 'degraded',
  unavailable: 'degraded',
  disabled: 'degraded'
});

function text(value) {
  return typeof value === 'string' ? value : '';
}

function workspaceIDFromPage() {
  if (typeof window === 'undefined') return '';
  return String(window.currentWorkspaceId || document.body?.dataset?.workspaceId || '');
}

function createElement(documentRef, tag, className, content) {
  const node = documentRef.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

export class WorkspaceSurfaceHost {
  constructor(options = {}) {
    this.workspaceId = options.workspaceId || workspaceIDFromPage();
    this.fetch = options.fetch || globalThis.fetch?.bind(globalThis);
    this.document = options.document || globalThis.document || null;
    this.window = options.window || globalThis.window || null;
    this.coordinator = options.coordinator || workspaceOverlayCoordinator();
    this.Bridge = options.Bridge || ParentSurfaceBridge;
    this.surfaces = [];
    this.active = null;
    this.loading = null;
  }

  async loadCatalog() {
    if (!this.workspaceId || !this.fetch) {
      this.surfaces = [];
      return this.surfaces;
    }
    if (this.loading) return this.loading;
    this.loading = this._request(
      '/api/workspaces/' + encodeURIComponent(this.workspaceId) + '/surfaces'
    )
      .then(payload => {
        this.surfaces = Array.isArray(payload?.surfaces) ? payload.surfaces : [];
        this._notifyChanged();
        return this.surfaces;
      })
      .catch(() => {
        this.surfaces = [];
        this._notifyChanged();
        return this.surfaces;
      })
      .finally(() => {
        this.loading = null;
      });
    return this.loading;
  }

  stations() {
    return this.surfaces.map(surface => ({
      key: text(surface.key),
      label: text(surface.label) || 'Plugin surface',
      icon: HOST_ICON_CLASSES[surface.icon?.value] || 'bi-puzzle',
      state: () => this.stationState(surface),
      action: trigger => {
        void this.open(surface.key, trigger);
      }
    }));
  }

  stationState(surface) {
    const status = surface?.status || {};
    const state = text(status.state) || (surface?.available === false ? 'unavailable' : 'checking');
    return {
      applies: true,
      value: text(status.value) || (state === 'checking' ? 'Checking…' : 'Unavailable'),
      description: text(status.description) || 'Plugin surface status',
      tone: STATE_TONES[state] || 'degraded'
    };
  }

  async open(surfaceKey, trigger = null) {
    if (this.active) await this.close();
    const surface = this.surfaces.find(item => item?.key === surfaceKey);
    if (!surface || surface.available === false || !this.document || !this.fetch) return false;

    const opened = await this._request(
      '/api/workspaces/' +
        encodeURIComponent(this.workspaceId) +
        '/surfaces/' +
        encodeURIComponent(surface.key) +
        '/sessions',
      { method: 'POST' }
    );
    if (!opened?.session || !opened?.frame_url) return false;

    const modal = createElement(this.document, 'section', 'workspace-surface-modal');
    modal.setAttribute('role', 'dialog');
    modal.setAttribute('aria-modal', 'true');
    modal.setAttribute('aria-labelledby', 'workspaceSurfaceTitle');

    const backdrop = createElement(this.document, 'button', 'workspace-surface-backdrop');
    backdrop.type = 'button';
    backdrop.setAttribute('aria-label', 'Close ' + text(surface.label));
    const panel = createElement(this.document, 'div', 'workspace-surface-panel');
    const header = createElement(this.document, 'header', 'workspace-surface-header');
    const identity = createElement(this.document, 'div', 'workspace-surface-identity');
    identity.appendChild(
      createElement(this.document, 'span', 'workspace-surface-eyebrow', 'Plugin workspace surface')
    );
    const title = createElement(
      this.document,
      'h2',
      'workspace-surface-title',
      text(surface.label)
    );
    title.id = 'workspaceSurfaceTitle';
    identity.appendChild(title);
    const close = createElement(this.document, 'button', 'workspace-surface-close', 'Close');
    close.type = 'button';
    close.setAttribute('aria-label', 'Close ' + text(surface.label));
    header.appendChild(identity);
    header.appendChild(close);

    const frame = createElement(this.document, 'iframe', 'workspace-surface-frame');
    frame.title = text(surface.label);
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.setAttribute('credentialless', '');
    frame.setAttribute('referrerpolicy', 'no-referrer');
    frame.src = opened.frame_url;
    const width = Math.max(320, Math.min(1600, Number(surface.modal?.width || 720)));
    const height = Math.max(240, Math.min(1200, Number(surface.modal?.height || 560)));
    panel.style.setProperty('--workspace-surface-width', width + 'px');
    panel.style.setProperty('--workspace-surface-height', height + 'px');
    panel.appendChild(header);
    panel.appendChild(frame);
    modal.appendChild(backdrop);
    modal.appendChild(panel);
    this.document.body.appendChild(modal);

    const overlayId = 'workspace-surface:' + surface.key;
    const bridge = new this.Bridge({
      frameWindow: frame.contentWindow,
      eventTarget: this.window,
      surface: {
        key: text(surface.key),
        plugin_id: text(surface.plugin?.id),
        capability_id: text(surface.capability_id),
        surface_id: text(surface.surface_id),
        label: text(surface.label)
      },
      features: {
        operation: true,
        confirmation: false,
        state: false,
        ask_ori: false,
        open_setup: false,
        close: true
      },
      onRequest: request => this._handleFrameRequest(request)
    });

    this.active = {
      surface,
      session: opened.session,
      modal,
      frame,
      bridge,
      overlayId,
      trigger,
      closing: false
    };
    this.coordinator.open({
      id: overlayId,
      kind: 'modal',
      trigger,
      container: modal,
      onClose: () => {
        this._teardownActive();
      }
    });
    frame.addEventListener('load', () => bridge.start(), { once: true });
    close.addEventListener('click', () => {
      void this.close();
    });
    backdrop.addEventListener('click', () => {
      void this.close();
    });
    close.focus();
    return true;
  }

  async close() {
    const active = this.active;
    if (!active || active.closing) return false;
    active.closing = true;
    if (!this.coordinator.close(active.overlayId)) this._teardownActive();
    return true;
  }

  setDocumentVisible(visible) {
    this.active?.bridge?.visibility(Boolean(visible));
  }

  async _handleFrameRequest(request) {
    const active = this.active;
    if (!active) {
      return {
        ok: false,
        error: {
          code: 'session_invalidated',
          message: 'This plugin surface is no longer available.'
        }
      };
    }
    if (request.type === 'ori.surface.operation.invoke') {
      const payload = request.payload || {};
      try {
        const response = await this._request('/api/workspace-surfaces/operations', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            session: active.session,
            operation_id: text(payload.operation_id),
            input: payload.input || {}
          })
        });
        return { ok: true, result: response?.output };
      } catch (error) {
        return {
          ok: false,
          error: {
            code: text(error?.code) || 'service_unavailable',
            message: text(error?.message) || 'The plugin service could not complete that operation.'
          }
        };
      }
    }
    if (request.type === 'ori.surface.host.close') {
      // Let ParentSurfaceBridge post the correlated response before teardown.
      this.window?.setTimeout?.(() => {
        void this.close();
      }, 0);
      return { ok: true, result: { closed: true } };
    }
    return {
      ok: false,
      error: { code: 'host_intent_unavailable', message: 'That host action is unavailable.' }
    };
  }

  _teardownActive() {
    const active = this.active;
    if (!active) return;
    this.active = null;
    active.bridge.destroy();
    active.modal.remove();
    if (active.session && this.fetch) {
      void this.fetch('/api/workspace-surfaces/sessions', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session: active.session })
      }).catch(() => {});
    }
  }

  async _request(path, options = {}) {
    const response = await this.fetch(path, {
      credentials: 'same-origin',
      ...options
    });
    if (response.ok) {
      if (response.status === 204) return null;
      return response.json();
    }
    let payload = {};
    try {
      payload = await response.json();
    } catch (_err) {
      // Use the bounded generic fallback below.
    }
    const error = new Error(text(payload.message) || 'Workspace surface request failed.');
    error.code = text(payload.code) || 'surface_unavailable';
    throw error;
  }

  _notifyChanged() {
    if (!this.document || typeof this.document.dispatchEvent !== 'function') return;
    const EventCtor = this.window?.CustomEvent || globalThis.CustomEvent;
    if (typeof EventCtor === 'function') {
      this.document.dispatchEvent(new EventCtor('ori:workspace-surfaces-changed'));
    }
  }
}
