/**
 * GitHub Account Connection — Settings card controller.
 *
 * Talks to /api/connections/github/{status,connect,disconnect} and renders the
 * Connected / Disconnected states.
 *
 * The token is write-only from this module's point of view: it is read out of
 * the input, POSTed, and the input is cleared immediately. The server never
 * returns it, so there is deliberately no code path here that could re-render
 * a stored token into the DOM.
 */
(function () {
  'use strict';

  const ERROR_HINTS = {
    invalid_token:
      'Check that you copied the whole token. Tokens are shown only once — if you have lost it, generate a new one.',
    insufficient_scope:
      'Edit the token at github.com/settings/personal-access-tokens — give it Issues (read and write) on that repository.',
    vault_locked: 'Unlock your vault from the Vault page, then reload this page.',
    rate_limited: 'GitHub is throttling this token. Wait for the reset and try again.',
    unavailable: 'GitHub could not be reached. Check your connection and try again.'
  };

  class GitHubConnectionManager {
    constructor() {
      this.el = {};
      this.busy = false;
    }

    init() {
      if (!document.getElementById('githubConnStatus')) return;
      this.cache();
      this.bind();
      this.refresh();
    }

    cache() {
      const ids = [
        'githubConnStatus',
        'githubConnStatusIndicator',
        'githubConnStatusText',
        'githubConnConnected',
        'githubConnDisconnected',
        'githubConnAvatar',
        'githubConnLogin',
        'githubConnMeta',
        'githubConnScopes',
        'githubConnLinked',
        'githubConnBadge',
        'githubConnConfirm',
        'githubConnTokenInput',
        'githubConnConnectBtn',
        'githubConnDisconnectBtn',
        'githubConnReplaceBtn',
        'githubConnError'
      ];
      ids.forEach(id => {
        this.el[id] = document.getElementById(id);
      });
    }

    bind() {
      this.el.githubConnConnectBtn?.addEventListener('click', () => this.connect());
      this.el.githubConnDisconnectBtn?.addEventListener('click', () => this.askDisconnect());
      this.el.githubConnReplaceBtn?.addEventListener('click', () => this.startReplace());
      // Enter in the token field is the natural submit gesture.
      this.el.githubConnTokenInput?.addEventListener('keydown', event => {
        if (event.key === 'Enter') {
          event.preventDefault();
          this.connect();
        }
      });
    }

    async request(path, options) {
      const response = await fetch(path, {
        headers: { 'Content-Type': 'application/json' },
        ...options
      });
      let payload = {};
      try {
        payload = await response.json();
      } catch {
        payload = {};
      }
      return { ok: response.ok, status: response.status, payload };
    }

    async refresh() {
      this.showError('');
      const { ok, payload } = await this.request('/api/connections/github/status', {
        method: 'GET'
      });
      if (!ok) {
        this.renderDisconnected();
        this.showError('Could not check the GitHub connection.');
        return;
      }
      if (payload.connected) {
        this.renderConnected(payload);
      } else {
        this.renderDisconnected();
        // A stored-but-broken token is a real problem worth surfacing;
        // simply never having connected is not.
        if (payload.error && payload.error_category !== 'not_connected') {
          this.showError(payload.error, payload.error_category);
        }
      }
    }

    async connect() {
      if (this.busy) return;
      const input = this.el.githubConnTokenInput;
      const token = (input?.value || '').trim();
      if (!token) {
        this.showError('Paste a personal access token first.');
        input?.focus();
        return;
      }

      this.setBusy(true, 'Connecting to GitHub…');
      const { ok, payload } = await this.request('/api/connections/github/connect', {
        method: 'POST',
        body: JSON.stringify({ token })
      });
      // Clear the field regardless of outcome so a token never lingers in
      // the DOM after the attempt.
      if (input) input.value = '';
      this.setBusy(false);

      if (!ok) {
        this.renderDisconnected();
        this.showError(payload.message || 'Could not connect to GitHub.', payload.error);
        return;
      }
      this.renderConnected(payload);
      this.showError('');
    }

    startReplace() {
      // Replacing is just connecting again: the server overwrites the stored
      // credential, and validates the new token before it does.
      this.renderDisconnected();
      this.el.githubConnTokenInput?.focus();
    }

    async askDisconnect() {
      const host = this.el.githubConnConfirm;
      if (!host) return;
      host.innerHTML = '';
      host.classList.remove('d-none');

      const title = document.createElement('div');
      title.className = 'gh-confirm-title';
      title.textContent = 'Disconnect GitHub?';

      // Name the workspaces this breaks rather than warning in the
      // abstract. "2 workspaces will stop working" is a decision the user
      // can make; "workspaces may be affected" is not.
      const linked = await this.fetchLinked();
      const note = document.createElement('p');
      note.className = 'gh-meta';
      note.style.margin = '0';
      note.textContent = linked.length
        ? `${linked.length} workspace${linked.length === 1 ? '' : 's'} use this connection and will stop working until you connect again. Nothing on GitHub is changed, and each workspace keeps its repository.`
        : 'Nothing on GitHub is changed. No workspaces currently use this connection.';

      const actions = document.createElement('div');
      actions.className = 'gh-confirm-actions';

      const confirmBtn = document.createElement('button');
      confirmBtn.type = 'button';
      confirmBtn.className = 'modern-btn modern-btn-danger';
      confirmBtn.textContent = 'Disconnect';
      confirmBtn.addEventListener('click', () => this.disconnect());

      const cancelBtn = document.createElement('button');
      cancelBtn.type = 'button';
      cancelBtn.className = 'modern-btn modern-btn-secondary';
      cancelBtn.textContent = 'Cancel';
      cancelBtn.addEventListener('click', () => host.classList.add('d-none'));

      actions.append(confirmBtn, cancelBtn);
      host.append(title, note);
      if (linked.length) host.append(this.buildLinkedList(linked));
      host.append(actions);
    }

    async fetchLinked() {
      const { ok, payload } = await this.request('/api/connections/github/linked', {
        method: 'GET'
      });
      if (!ok || !Array.isArray(payload.workspaces)) return [];
      return payload.workspaces;
    }

    buildLinkedList(linked) {
      const list = document.createElement('ul');
      list.className = 'gh-linked';
      linked.forEach(ws => {
        const item = document.createElement('li');
        const name = document.createElement('span');
        name.className = 'gh-linked-name';
        name.textContent = ws.name || 'Untitled workspace';
        const repo = document.createElement('span');
        repo.className = 'gh-perm';
        repo.textContent = ws.repo || '';
        item.append(name, document.createTextNode(' · '), repo);
        list.appendChild(item);
      });
      return list;
    }

    // renderLinked shows the dependent workspaces on the connected card, so
    // the connection's blast radius is visible before anyone opens the
    // disconnect dialog.
    async renderLinked() {
      const host = this.el.githubConnLinked;
      if (!host) return;
      const linked = await this.fetchLinked();
      host.innerHTML = '';
      if (!linked.length) {
        host.classList.add('d-none');
        return;
      }
      const label = document.createElement('div');
      label.className = 'gh-linked-label';
      label.textContent = `Used by ${linked.length} workspace${linked.length === 1 ? '' : 's'}`;
      host.append(label, this.buildLinkedList(linked));
      host.classList.remove('d-none');
    }

    async disconnect() {
      if (this.busy) return;
      this.setBusy(true, 'Disconnecting…');
      const { ok, payload } = await this.request('/api/connections/github/disconnect', {
        method: 'POST'
      });
      this.setBusy(false);
      this.el.githubConnConfirm?.classList.add('d-none');

      if (!ok) {
        this.showError(payload.message || 'Could not disconnect.');
        return;
      }
      this.renderDisconnected();
      this.showError('');
    }

    renderConnected(status) {
      this.el.githubConnConnected?.classList.remove('d-none');
      this.el.githubConnDisconnected?.classList.add('d-none');
      this.el.githubConnStatus?.classList.add('d-none');
      // Fire-and-forget: the card is useful before the workspace list
      // arrives, so it is not worth blocking the render on.
      void this.renderLinked();

      const login = status.login || '';
      if (this.el.githubConnLogin) this.el.githubConnLogin.textContent = login ? '@' + login : '-';
      if (this.el.githubConnAvatar) {
        this.el.githubConnAvatar.textContent = login ? login.charAt(0).toUpperCase() : '@';
      }
      if (this.el.githubConnMeta) {
        this.el.githubConnMeta.textContent =
          status.token_type === 'fine_grained'
            ? 'Fine-grained token'
            : status.token_type === 'classic'
              ? 'Classic token'
              : '';
      }

      const scopes = this.el.githubConnScopes;
      if (scopes) {
        scopes.innerHTML = '';
        (status.scopes || []).forEach(scope => {
          const pill = document.createElement('span');
          pill.className = 'gh-scope';
          pill.textContent = scope;
          scopes.appendChild(pill);
        });
      }
    }

    renderDisconnected() {
      this.el.githubConnConnected?.classList.add('d-none');
      this.el.githubConnDisconnected?.classList.remove('d-none');
      this.el.githubConnStatus?.classList.add('d-none');
      this.el.githubConnConfirm?.classList.add('d-none');
    }

    setBusy(busy, message) {
      this.busy = busy;
      [this.el.githubConnConnectBtn, this.el.githubConnDisconnectBtn, this.el.githubConnReplaceBtn]
        .filter(Boolean)
        .forEach(btn => {
          btn.disabled = busy;
        });
      if (busy && message) {
        this.el.githubConnStatus?.classList.remove('d-none');
        if (this.el.githubConnStatusText) this.el.githubConnStatusText.textContent = message;
      } else if (!busy) {
        this.el.githubConnStatus?.classList.add('d-none');
      }
    }

    showError(message, category) {
      const host = this.el.githubConnError;
      if (!host) return;
      if (!message) {
        host.classList.add('d-none');
        host.textContent = '';
        return;
      }
      const hint = category && ERROR_HINTS[category] ? ' ' + ERROR_HINTS[category] : '';
      host.textContent = message + hint;
      host.classList.remove('d-none');
    }
  }

  const manager = new GitHubConnectionManager();
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => manager.init());
  } else {
    manager.init();
  }
})();
