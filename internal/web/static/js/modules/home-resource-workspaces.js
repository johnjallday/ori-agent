// A Home's resource workspaces (one optional REAPER Studio in v1): whether
// it can be set up, the one that exists, and what its plugin lacks. Every
// name and description comes from the Home package's declaration; this
// module only words the states. Setting one up opens the ordinary Create
// Workspace flow with this Home locked as its parent, so the server's review
// decides everything; nothing here creates, inspects or reads a resource.

import { PROVIDER_UPGRADED_EVENT } from './assistant-program.js';

// setUpURL opens the shared Create Workspace wizard (it lives on pages with
// the create dialog) preselected with the blueprint and this Home.
export function setUpURL(templateId, homeId) {
  const params = new URLSearchParams({ create: '1', blueprint: String(templateId || '') });
  if (homeId) params.set('parent', String(homeId));
  return `/?${params.toString()}`;
}

// resourceWorkspaceView turns one summary row into what the card shows.
export function resourceWorkspaceView(summary, homeId) {
  const label = String(summary?.attachment?.label || '').trim() || 'Resource workspace';
  const description = String(summary?.attachment?.description || '').trim();
  const workspace = summary?.workspace && summary.workspace.url ? summary.workspace : null;
  const open = workspace
    ? { text: `Open ${label}`, href: String(workspace.url), primary: true }
    : null;
  const plugins = { text: 'Manage plugins', href: '/plugins', primary: false };
  switch (summary?.state) {
    case 'available':
      return { label, description, note: '', actions: open ? [open] : [] };
    case 'not_created':
      return summary.template_id
        ? {
            label,
            description,
            note: 'Setting it up creates the workspace only. Nothing is read until you inspect it.',
            actions: [
              {
                text: `Set up ${label}`,
                href: setUpURL(summary.template_id, homeId),
                primary: true
              }
            ]
          }
        : { label, description, note: '', actions: [] };
    case 'provider_missing':
      return {
        label,
        description,
        note: `The plugin that provides ${label} is not installed.`,
        actions: open ? [open, plugins] : [plugins]
      };
    case 'provider_disabled':
      return {
        label,
        description,
        note: `The plugin that provides ${label} is switched off. What it saved stays readable.`,
        actions: open ? [open, plugins] : [plugins]
      };
    case 'provider_incompatible':
      return {
        label,
        description,
        note: `Update the plugin that provides ${label} to set it up.`,
        actions: open ? [open, plugins] : [plugins]
      };
    case 'home_unavailable':
      return {
        label,
        description,
        note: "This Home's package is unavailable. What was saved stays readable.",
        actions: open ? [open] : []
      };
    default:
      return { label, description, note: '', actions: [] };
  }
}

export class HomeResourceWorkspacesPanel {
  constructor({
    workspaceId,
    root = null,
    fetchImpl = globalThis.fetch?.bind(globalThis),
    documentImpl = globalThis.document
  } = {}) {
    this.workspaceId = String(workspaceId || '');
    this.fetchImpl = fetchImpl;
    this.document = documentImpl;
    this.root = root || this.document?.getElementById('homeResourceWorkspaces') || null;
    this.summaries = [];
  }

  async init() {
    if (!this.root || !this.workspaceId) return;
    this.document?.addEventListener?.(PROVIDER_UPGRADED_EVENT, () => void this.load());
    await this.load();
  }

  async load() {
    try {
      const response = await this.fetchImpl(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/assistant-program/resource-workspaces`,
        { headers: { Accept: 'application/json' } }
      );
      if (!response.ok) {
        this.summaries = [];
      } else {
        const body = await response.json().catch(() => ({}));
        const data = body?.data ?? body;
        this.summaries = Array.isArray(data?.resource_workspaces) ? data.resource_workspaces : [];
      }
    } catch (_) {
      this.summaries = [];
    }
    this.render();
  }

  render() {
    const root = this.root;
    if (!root) return;
    root.replaceChildren();
    root.hidden = this.summaries.length === 0;
    for (const summary of this.summaries) {
      root.append(this.card(resourceWorkspaceView(summary, this.workspaceId)));
    }
  }

  card(view) {
    const doc = this.document;
    const card = doc.createElement('article');
    card.className = 'home-resource-workspace';
    const title = doc.createElement('h3');
    title.className = 'home-resource-workspace-title';
    title.textContent = view.label;
    card.append(title);
    if (view.description) {
      const description = doc.createElement('p');
      description.className = 'home-resource-workspace-description';
      description.textContent = view.description;
      card.append(description);
    }
    if (view.note) {
      const note = doc.createElement('p');
      note.className = 'home-resource-workspace-note';
      note.textContent = view.note;
      card.append(note);
    }
    if (view.actions.length) {
      const actions = doc.createElement('div');
      actions.className = 'home-resource-workspace-actions';
      for (const action of view.actions) {
        const link = doc.createElement('a');
        link.className = `modern-btn ${action.primary ? 'modern-btn-primary' : 'modern-btn-secondary'}`;
        link.href = action.href;
        link.textContent = action.text;
        actions.append(link);
      }
      card.append(actions);
    }
    return card;
  }
}
