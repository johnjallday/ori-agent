import { workspacePageURL } from './workspace-routes.js';

/** Declaration-owned destinations. The host is independent of secondary links. */
export class AssistantProgramEntry {
  constructor({ workspaceId, workspaceSlug, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = String(workspaceId || '').trim();
    this.workspaceSlug = String(workspaceSlug || '').trim();
    this.fetchImpl = typeof fetchImpl === 'function' ? (...args) => fetchImpl(...args) : null;
    this.visible = false;
    this.program = null;
    this.observer = null;
  }

  async init() {
    if (!this.workspaceId || !this.workspaceSlug || typeof this.fetchImpl !== 'function') return;
    try {
      const response = await this.fetchImpl(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/assistant-program`,
        { headers: { Accept: 'application/json' } }
      );
      if (!response.ok) return;
      this.program = await response.json();
      this.visible = Boolean(this.program?.available || this.program?.activation_needed);
      if (this.visible) this.install();
    } catch (_) {
      // Optional contributions fail closed. No declaration means no guessed link.
    }
  }

  install() {
    this.render();
    if (typeof MutationObserver !== 'function' || !document.body) return;
    this.observer?.disconnect();
    this.observer = new MutationObserver(() => this.render());
    this.observer.observe(document.body, { childList: true, subtree: true });
  }

  render() {
    if (!this.visible) return;
    const host = document.querySelector('[data-cmd-program-destinations]');
    if (!host) return;
    const homeName = String(this.program?.declaration?.station_name || '').trim() || 'Team Home';
    this.addLink(host, 'assistantProgramEntry', 'assistant', homeName, `Open ${homeName}`);
    if (this.program?.resource_workspace) {
      this.addLink(
        host,
        'resourceCatalogEntry',
        'catalog',
        'Catalog',
        'Open this workspace’s catalog'
      );
    }
  }

  addLink(host, key, route, label, accessibleName) {
    const attribute = 'data-' + key.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase());
    if (host.querySelector(`[${attribute}]`)) return;
    const link = document.createElement('a');
    link.className = 'ws-cmd-view-btn ws-cmd-view-link';
    link.dataset[key] = '';
    link.href = workspacePageURL(this.workspaceSlug, [route]);
    // Literal text: these names belong to a declaration, not to this renderer.
    link.textContent = label;
    link.setAttribute('aria-label', accessibleName);
    host.appendChild(link);
  }

  destroy() {
    this.observer?.disconnect();
    this.observer = null;
  }
}
