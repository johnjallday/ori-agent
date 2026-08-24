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
    this.confirm = options.confirm || (message => this.window?.confirm?.(message) ?? false);
    this.schedule =
      options.schedule || ((callback, delay) => this.window?.setTimeout?.(callback, delay));
    this.cancelSchedule = options.cancelSchedule || (timer => this.window?.clearTimeout?.(timer));
    this.surfaces = [];
    this.active = null;
    this.loading = null;
    this.mapVisible = false;
    this.documentVisible = this.document?.visibilityState !== 'hidden';
    this.pollTimer = null;
    this.deepLinkHandled = false;
    this._boundVisibility = () => {
      this.setDocumentVisible(this.document?.visibilityState !== 'hidden');
    };
    this.document?.addEventListener?.('visibilitychange', this._boundVisibility);
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
        this._reconcileActiveSurface();
        this._notifyChanged();
        this._openDeepLink();
        return this.surfaces;
      })
      .catch(() => {
        this.surfaces = [];
        this._reconcileActiveSurface();
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
      state: () =>
        this.stationState(this.surfaces.find(item => item?.key === surface.key) || surface),
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

  setMapVisible(visible) {
    this.mapVisible = Boolean(visible);
    this._schedulePolling();
  }

  setDocumentVisible(visible) {
    this.documentVisible = Boolean(visible);
    this.active?.bridge?.visibility(this.documentVisible);
    this._schedulePolling();
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
    const features = surface.features || {};
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
        confirmation: Boolean(features.confirmation),
        state: Boolean(features.state),
        ask_ori: Boolean(features.ask_ori),
        open_setup: Boolean(features.open_setup),
        close: Boolean(features.close)
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
      onClose: () => this._teardownActive()
    });
    frame.addEventListener('load', () => bridge.start(), { once: true });
    close.addEventListener('click', () => void this.close());
    backdrop.addEventListener('click', () => void this.close());
    close.focus();
    this._setDeepLink(surface.key);
    this._schedulePolling();
    return true;
  }

  async close() {
    const active = this.active;
    if (!active || active.closing) return false;
    active.closing = true;
    if (!this.coordinator.close(active.overlayId)) this._teardownActive();
    return true;
  }

  async _handleFrameRequest(request) {
    const active = this.active;
    if (!active)
      return this._bridgeError(
        'session_invalidated',
        'This plugin surface is no longer available.'
      );
    const payload = request.payload || {};
    switch (request.type) {
      case 'ori.surface.operation.invoke':
        return this._invokeOperation(text(payload.operation_id), payload.input || {});
      case 'ori.surface.state.get':
        return this._state('get', payload);
      case 'ori.surface.state.set':
        return this._state('set', payload);
      case 'ori.surface.state.delete':
        return this._state('delete', payload);
      case 'ori.surface.host.ask_ori':
        return this._askOri(text(payload.context));
      case 'ori.surface.host.open_setup':
        return this._openSetup();
      case 'ori.surface.status_changed':
        await this.loadCatalog();
        return { ok: true, result: { refreshed: true } };
      case 'ori.surface.host.close':
        this.window?.setTimeout?.(() => void this.close(), 0);
        return { ok: true, result: { closed: true } };
      default:
        return this._bridgeError('host_intent_unavailable', 'That host action is unavailable.');
    }
  }

  async _invokeOperation(operationId, input) {
    const active = this.active;
    const invoke = confirmationToken =>
      this._request('/api/workspace-surfaces/operations', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          session: active.session,
          operation_id: operationId,
          input,
          ...(confirmationToken ? { confirmation_token: confirmationToken } : {})
        })
      });
    try {
      const response = await invoke('');
      return { ok: true, result: response?.output };
    } catch (error) {
      if (error.code !== 'confirmation_required' || !error.confirmationId) {
        return this._bridgeError(error.code || 'service_unavailable', error.message);
      }
      const approved = await Promise.resolve(
        this.confirm(
          `${text(active.surface.label) || 'This plugin'} wants to run ${operationId}. Review this exact action before continuing.`
        )
      );
      if (!approved) {
        await this._request('/api/workspace-surfaces/confirmations', {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            session: active.session,
            confirmation_id: error.confirmationId
          })
        }).catch(() => {});
        return this._bridgeError('confirmation_cancelled', 'The plugin action was cancelled.');
      }
      try {
        const approval = await this._request('/api/workspace-surfaces/confirmations', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            session: active.session,
            confirmation_id: error.confirmationId
          })
        });
        const response = await invoke(text(approval?.confirmation_token));
        return { ok: true, result: response?.output };
      } catch (confirmedError) {
        return this._bridgeError(
          confirmedError.code || 'confirmation_invalid',
          confirmedError.message
        );
      }
    }
  }

  async _state(action, payload) {
    try {
      const result = await this._request('/api/workspace-surfaces/state', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          session: this.active.session,
          action,
          key: text(payload.key),
          ...(action === 'set'
            ? {
                schema_version: Number(payload.schema_version || 0),
                expected_revision: text(payload.expected_revision),
                value: payload.value
              }
            : {}),
          ...(action === 'delete' ? { expected_revision: text(payload.expected_revision) } : {})
        })
      });
      return { ok: true, result };
    } catch (error) {
      return this._bridgeError(error.code || 'state_invalid', error.message);
    }
  }

  async _askOri(context) {
    try {
      const intent = await this._intent('ask_ori', context);
      if (!this.window?.OriAskRouting || typeof this.window.OriAskRouting.submit !== 'function') {
        return this._bridgeError('ask_ori_unavailable', 'Ask Ori is not available.');
      }
      await this.window.OriAskRouting.submit(context, {
        routeContext: {
          surface: 'plugin_workspace_surface',
          workspace_id: intent.workspace_id,
          origin: 'plugin_workspace_surface',
          required_capabilities: intent.required_capabilities || [],
          plugin_context: intent.plugin_context || '',
          plugin_context_untrusted: true
        },
        openThinkingModal: true
      });
      return { ok: true, result: { submitted: true } };
    } catch (error) {
      return this._bridgeError(error.code || 'ask_ori_unavailable', error.message);
    }
  }

  async _openSetup() {
    try {
      const intent = await this._intent('open_setup', '');
      this.window?.setTimeout?.(() => {
        void this.close();
        this.window?.SetupWizard?.open?.();
        this._dispatch('ori:workspace-surface-open-setup', {
          providerId: intent.provider_id,
          requirementKey: intent.requirement_key
        });
      }, 0);
      return { ok: true, result: { opened: true } };
    } catch (error) {
      return this._bridgeError(error.code || 'intent_unavailable', error.message);
    }
  }

  _intent(type, context) {
    return this._request('/api/workspace-surfaces/intents', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ session: this.active.session, type, ...(context ? { context } : {}) })
    });
  }

  _bridgeError(code, message) {
    return {
      ok: false,
      error: {
        code: text(code) || 'host_request_failed',
        message: text(message) || 'Ori could not complete that request.'
      }
    };
  }

  _teardownActive() {
    const active = this.active;
    if (!active) return;
    this.active = null;
    active.bridge.destroy();
    active.modal.remove();
    this._setDeepLink('');
    this._schedulePolling();
    if (active.session && this.fetch) {
      void this.fetch('/api/workspace-surfaces/sessions', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session: active.session })
      }).catch(() => {});
    }
  }

  _reconcileActiveSurface() {
    if (!this.active) return;
    const current = this.surfaces.find(surface => surface?.key === this.active.surface?.key);
    if (
      !current ||
      current.available === false ||
      text(current.plugin?.generation) !== text(this.active.surface?.plugin?.generation)
    ) {
      this.active.bridge.invalidate('session_invalidated');
      void this.close();
      return;
    }
    this.active.surface = current;
  }

  _schedulePolling() {
    if (this.pollTimer) {
      this.cancelSchedule?.(this.pollTimer);
      this.pollTimer = null;
    }
    if (!this.documentVisible || (!this.mapVisible && !this.active)) return;
    const seconds = this.active
      ? Math.max(1, Math.min(60, Number(this.active.surface?.polling?.open_seconds || 1)))
      : Math.max(
          5,
          Math.min(60, ...this.surfaces.map(surface => Number(surface?.polling?.map_seconds || 60)))
        );
    this.pollTimer = this.schedule?.(async () => {
      this.pollTimer = null;
      await this.loadCatalog();
      this._schedulePolling();
    }, seconds * 1000);
  }

  _openDeepLink() {
    if (this.deepLinkHandled || this.active || !this.window?.location) return;
    this.deepLinkHandled = true;
    const key = new URLSearchParams(this.window.location.search || '').get('surface');
    if (key && this.surfaces.some(surface => surface?.key === key && surface.available !== false)) {
      void this.open(key, null);
    }
  }

  _setDeepLink(key) {
    if (!this.window?.history || !this.window?.location) return;
    const params = new URLSearchParams(this.window.location.search || '');
    if (key) params.set('surface', key);
    else params.delete('surface');
    const query = params.toString();
    const next = this.window.location.pathname + (query ? '?' + query : '');
    this.window.history.replaceState(null, '', next);
  }

  async _request(path, options = {}) {
    const response = await this.fetch(path, { credentials: 'same-origin', ...options });
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
    error.confirmationId = text(payload.confirmation_id);
    throw error;
  }

  _dispatch(type, detail) {
    const EventCtor = this.window?.CustomEvent || globalThis.CustomEvent;
    if (typeof EventCtor === 'function')
      this.document?.dispatchEvent?.(new EventCtor(type, { detail }));
  }

  _notifyChanged() {
    this._dispatch('ori:workspace-surfaces-changed');
  }
}
