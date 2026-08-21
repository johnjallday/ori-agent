// workspace-capabilities.js — the built-in Workspace Capabilities catalog.
//
// It reads GET /api/workspaces/{id}/capabilities and renders one card per
// capability: what it does, the facts a user needs before granting anything,
// whether this workspace already has it, and an Install action wired to
// POST .../capabilities/{id}/install.
//
// One renderer, several surfaces. The Map's Tools modal and the Systems rail
// both mount the SAME card node (see workspace-command.js's shared-surface
// machinery), so there is exactly one catalog implementation rather than one
// per entry point.
//
// Everything shown here comes from the server's catalog response. The module
// never infers that a capability is installed from a workspace name, template,
// folder, or agent — installed state is the persisted install record and
// nothing else.
(function () {
  'use strict';

  const CATALOG_HOST_ID = 'workspace-capabilities-list';

  let workspaceId = '';
  let items = [];
  let loaded = false;
  let loadError = '';
  let pendingInstalls = new Set();

  function wsId() {
    if (workspaceId) return workspaceId;
    const resolved =
      (typeof window !== 'undefined' && window.currentWorkspaceId) ||
      (typeof document !== 'undefined' && document.body?.dataset?.workspaceId) ||
      '';
    if (resolved) {
      workspaceId = String(resolved);
      return workspaceId;
    }
    // Legacy/test fallback. Production workspace routes carry a slug here.
    const match =
      typeof window !== 'undefined' &&
      /^\/workspaces\/([^/?#]+)/.exec((window.location && window.location.pathname) || '');
    if (match) workspaceId = decodeURIComponent(match[1]);
    return workspaceId;
  }

  function el(tag, opts = {}, children = []) {
    const node = document.createElement(tag);
    if (opts.className) node.className = opts.className;
    if (opts.text !== undefined) node.textContent = opts.text;
    if (opts.attrs) {
      for (const [key, value] of Object.entries(opts.attrs)) node.setAttribute(key, value);
    }
    for (const child of children) {
      if (child) node.appendChild(child);
    }
    return node;
  }

  async function load({ force = false } = {}) {
    const id = wsId();
    if (!id) return [];
    if (loaded && !force) return items;
    loadError = '';
    try {
      const response = await fetch('/api/workspaces/' + encodeURIComponent(id) + '/capabilities', {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) {
        loadError = 'Capabilities could not be loaded.';
        items = [];
        loaded = true;
        return items;
      }
      const payload = await response.json();
      items = Array.isArray(payload && payload.capabilities) ? payload.capabilities : [];
      loaded = true;
    } catch {
      loadError = 'Capabilities could not be loaded.';
      items = [];
      loaded = true;
    }
    return items;
  }

  function find(capabilityId) {
    const want = String(capabilityId || '')
      .trim()
      .toLowerCase();
    if (!want) return null;
    return (
      items.find(item => {
        const def = (item && item.definition) || {};
        return String(def.id || '').toLowerCase() === want;
      }) || null
    );
  }

  function isInstalled(capabilityId) {
    const item = find(capabilityId);
    return !!(item && item.installed);
  }

  function statusFor(capabilityId) {
    const item = find(capabilityId);
    return (item && item.status) || null;
  }

  async function install(capabilityId) {
    const id = wsId();
    const capability = String(capabilityId || '').trim();
    if (!id || !capability) return { ok: false, error: 'Workspace is unavailable.' };
    if (pendingInstalls.has(capability))
      return { ok: false, error: 'Install already in progress.' };

    pendingInstalls.add(capability);
    renderAll();
    try {
      const response = await fetch(
        '/api/workspaces/' +
          encodeURIComponent(id) +
          '/capabilities/' +
          encodeURIComponent(capability) +
          '/install',
        { method: 'POST', headers: { Accept: 'application/json' } }
      );
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        return {
          ok: false,
          error: (payload && payload.message) || 'This capability could not be installed.'
        };
      }
      // Re-read the catalog rather than patching local state: the server owns
      // installed state and derived health, and a repeat install legitimately
      // returns the pre-existing record.
      await load({ force: true });
      notifyChanged();
      return { ok: true, alreadyInstalled: !!(payload && payload.already_installed) };
    } catch {
      return { ok: false, error: 'This capability could not be installed.' };
    } finally {
      pendingInstalls.delete(capability);
      renderAll();
    }
  }

  // Surfaces that derive from installed capabilities (the Map station, the
  // compact card) listen for this instead of polling, so an install updates them
  // in place without a page reload.
  function notifyChanged() {
    if (typeof document === 'undefined' || typeof document.dispatchEvent !== 'function') return;
    const CustomEventCtor = typeof window !== 'undefined' ? window.CustomEvent : null;
    if (typeof CustomEventCtor !== 'function') return;
    document.dispatchEvent(new CustomEventCtor('ori:capabilities-changed', { detail: { items } }));
  }

  function statusChip(item) {
    const status = item && item.status;
    if (!status || !status.state) return null;
    const label = status.detail || String(status.state).replace(/_/g, ' ');
    return el('span', {
      className: 'ws-capability-status ws-capability-status-' + String(status.state),
      text: label
    });
  }

  function capabilityCard(item) {
    const def = (item && item.definition) || {};
    const display = def.display || {};
    const installed = !!(item && item.installed);
    const available = item ? item.available !== false : false;
    const capabilityId = String(def.id || '');
    const busy = pendingInstalls.has(capabilityId);

    const head = el('div', { className: 'ws-capability-head' }, [
      el('h4', { className: 'ws-capability-name', text: display.name || capabilityId }),
      installed ? statusChip(item) : null
    ]);

    const body = [head];
    if (display.tagline) {
      body.push(el('p', { className: 'ws-capability-tagline', text: display.tagline }));
    }
    if (display.summary) {
      body.push(el('p', { className: 'ws-capability-summary', text: display.summary }));
    }

    // The facts the user needs before granting anything: one folder, metadata
    // only, approval required, fixed Filed/ destination (FR-18). They come from
    // the server's definition so this copy cannot drift from the behavior.
    const highlights = Array.isArray(display.highlights) ? display.highlights : [];
    if (highlights.length) {
      body.push(
        el(
          'ul',
          { className: 'ws-capability-facts' },
          highlights.map(fact => el('li', { text: fact }))
        )
      );
    }

    if (!available) {
      body.push(
        el('p', {
          className: 'ws-capability-unavailable',
          text: item.unavailable || 'This capability is not available in this version of Ori.'
        })
      );
    }

    const actions = el('div', { className: 'ws-capability-actions' });
    if (!available) {
      // Nothing to act on: an unresolvable capability is metadata only.
    } else if (installed) {
      actions.appendChild(
        el('span', { className: 'ws-capability-installed-label', text: 'Installed' })
      );
      const open = el('button', {
        className: 'modern-btn modern-btn-secondary modern-btn-sm',
        text: openActionLabel(item),
        attrs: { type: 'button', 'data-capability-open': capabilityId }
      });
      actions.appendChild(open);
    } else {
      const button = el('button', {
        className: 'modern-btn modern-btn-primary modern-btn-sm',
        text: busy ? 'Installing…' : 'Install ' + (display.name || capabilityId),
        attrs: { type: 'button', 'data-capability-install': capabilityId }
      });
      if (busy) button.disabled = true;
      actions.appendChild(button);
    }
    body.push(actions);

    return el(
      'article',
      {
        className: 'ws-capability-card' + (installed ? ' is-installed' : ''),
        attrs: { 'data-capability-id': capabilityId }
      },
      body
    );
  }

  // An installed-but-unconfigured capability opens straight to setup; a
  // configured one opens its own surface (FR-104).
  function openActionLabel(item) {
    const status = (item && item.status) || {};
    const name = ((item && item.definition && item.definition.display) || {}).name || 'capability';
    if (status.state === 'setup_needed' || status.configured === false) {
      return 'Set up ' + name;
    }
    return 'Open ' + name;
  }

  function renderInto(host) {
    if (!host) return;
    host.innerHTML = '';
    if (loadError) {
      host.appendChild(el('p', { className: 'ws-capability-empty', text: loadError }));
      return;
    }
    if (!loaded) {
      host.appendChild(
        el('p', { className: 'ws-capability-empty', text: 'Loading capabilities…' })
      );
      return;
    }
    if (!items.length) {
      host.appendChild(
        el('p', {
          className: 'ws-capability-empty',
          text: 'No built-in capabilities are available for this workspace.'
        })
      );
      return;
    }
    for (const item of items) host.appendChild(capabilityCard(item));
  }

  function catalogHost() {
    if (typeof document === 'undefined' || typeof document.getElementById !== 'function') {
      return null;
    }
    return document.getElementById(CATALOG_HOST_ID);
  }

  function renderAll() {
    renderInto(catalogHost());
  }

  // trigger is the element the user activated, forwarded so the capability's
  // surface can return focus to it when it closes. The catalog does not know
  // what a capability opens, only who asked (FR-120).
  function onOpen(capabilityId, trigger) {
    const handler = openHandlers[String(capabilityId || '').toLowerCase()];
    if (typeof handler === 'function') {
      handler(trigger);
      return true;
    }
    return false;
  }

  // Open handlers are registered by whichever surface owns a capability's
  // primary experience. File Janitor registers its console, so the
  // post-install action and the catalog's Open button are never dead buttons.
  const openHandlers = Object.create(null);

  function registerOpenHandler(capabilityId, handler) {
    const key = String(capabilityId || '')
      .trim()
      .toLowerCase();
    if (!key || typeof handler !== 'function') return;
    openHandlers[key] = handler;
  }

  function bindHost(host) {
    if (!host || host.dataset?.capabilitiesBound === 'true') return;
    if (host.dataset) host.dataset.capabilitiesBound = 'true';
    host.addEventListener('click', async event => {
      const target = event.target && event.target.closest ? event.target : null;
      if (!target) return;

      const installBtn = target.closest('[data-capability-install]');
      if (installBtn) {
        event.preventDefault();
        const id = installBtn.getAttribute('data-capability-install');
        const result = await install(id);
        if (!result.ok && result.error) {
          announce(result.error);
          return;
        }
        // Offer the capability's own surface immediately after a successful
        // install (FR-22) rather than leaving the user to find it.
        onOpen(id, installBtn);
        return;
      }

      const openBtn = target.closest('[data-capability-open]');
      if (openBtn) {
        event.preventDefault();
        onOpen(openBtn.getAttribute('data-capability-open'), openBtn);
      }
    });
  }

  function announce(message) {
    if (
      typeof window !== 'undefined' &&
      window.OriToast &&
      typeof window.OriToast.show === 'function'
    ) {
      window.OriToast.show(message, 'error');
      return;
    }
    const host = catalogHost();
    if (!host) return;
    const existing = host.querySelector('.ws-capability-error');
    if (existing) existing.remove();
    const node = el('p', { className: 'ws-capability-error', text: message });
    node.setAttribute('role', 'alert');
    host.insertBefore(node, host.firstChild);
  }

  async function init() {
    if (!wsId()) return;
    const host = catalogHost();
    if (host) {
      bindHost(host);
      renderInto(host);
    }
    await load();
    renderAll();
  }

  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    document.addEventListener('DOMContentLoaded', () => void init());
    // A completed setup changes derived health, so the catalog's status chips
    // must re-read rather than keep showing what was true before.
    document.addEventListener('ori:setup-status', () => {
      if (wsId() && loaded) void load({ force: true }).then(renderAll);
    });
  }

  window.WorkspaceCapabilities = {
    init,
    load,
    renderInto,
    bindHost,
    install,
    items: () => items.slice(),
    find,
    isInstalled,
    statusFor,
    registerOpenHandler,
    onOpen,
    // reload re-reads the catalog from the server. A capability's own surface
    // calls it after changing something the catalog reports — adding a
    // companion, for instance — so the record it renders from is the one the
    // server now holds rather than the snapshot from page load.
    reload: () => load({ force: true }),
    // Test hooks.
    _reset: () => {
      workspaceId = '';
      items = [];
      loaded = false;
      loadError = '';
      pendingInstalls = new Set();
      for (const key of Object.keys(openHandlers)) delete openHandlers[key];
    },
    _setItems: next => {
      items = Array.isArray(next) ? next : [];
      loaded = true;
    },
    _setWorkspace: id => {
      workspaceId = String(id || '');
    }
  };
})();
