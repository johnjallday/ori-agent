import { workspacePageURL } from './workspace-routes.js';

/**
 * Adds the generic Assistant destination only when the server says this
 * workspace participates in an assistant program. A MutationObserver keeps the
 * link present when WorkspaceCommandView re-renders its switch.
 */
export class AssistantProgramEntry {
  constructor({ workspaceId, workspaceSlug, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = String(workspaceId || '').trim();
    this.workspaceSlug = String(workspaceSlug || '').trim();
    this.fetchImpl = (...args) => fetchImpl(...args);
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
      const program = await response.json();
      this.program = program;
      this.visible = Boolean(program && (program.available || program.activation_needed));
      if (!this.visible) return;
      this.install();
    } catch (_) {
      // Optional contributions fail closed: no declaration means no link.
    }
  }

  install() {
    this.render();
    if (typeof MutationObserver !== 'function' || !document.body) return;
    this.observer = new MutationObserver(() => this.render());
    this.observer.observe(document.body, { childList: true, subtree: true });
  }

  render() {
    if (!this.visible) return;
    const switcher = document.querySelector('.ws-cmd-view-switch');
    if (!switcher || switcher.querySelector('[data-assistant-program-entry]')) return;
    const plans = switcher.querySelector('a[href$="/plans"]');
    const link = document.createElement('a');
    link.className = 'ws-cmd-view-btn ws-cmd-view-link';
    link.dataset.assistantProgramEntry = '';
    link.href = workspacePageURL(this.workspaceSlug, ['assistant']);
    link.textContent = this.program?.hired
      ? `Assistant · ${this.program.stage_label || 'Active'} L${this.program.level || 1}`
      : 'Assistant';
    link.setAttribute(
      'aria-label',
      this.program?.hired
        ? `Open shared assistant home, ${this.program.stage_label || 'active'}, level ${this.program.level || 1}`
        : 'Open shared assistant home'
    );
    switcher.insertBefore(link, plans || null);
  }

  destroy() {
    if (this.observer) this.observer.disconnect();
    this.observer = null;
  }
}
