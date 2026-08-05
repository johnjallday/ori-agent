// Workspace-scoped agent detail page.
//
// Renders a genuine detail "home" for workspace-local agents (entry/manager
// agents defined in a workspace's config.json). These agents are not part of
// the global agent store, so /agents/<name> would 404 — this page is served at
// /workspaces/{id}/agents/{name} and assembles its data from workspace-scoped
// endpoints.

const ROLE_LABELS = {
  orchestrator: 'Orchestrator',
  coordinator: 'Coordinator',
  worker: 'Worker',
  specialist: 'Specialist',
  researcher: 'Researcher',
  reviewer: 'Reviewer'
};

export class WorkspaceAgentDetailPage {
  constructor(workspaceId, agentName) {
    this.workspaceId = String(workspaceId || '').trim();
    this.agentName = String(agentName || '').trim();
    this.el = id => document.getElementById(id);
  }

  async init() {
    if (!this.workspaceId || !this.agentName) {
      this.showError('This page needs both a workspace and an agent name.');
      return;
    }

    document.title = `${this.agentName} · Workspace Agent - Ori Agent`;
    this.setText('wad-breadcrumb-agent', this.agentName);

    // Workspace name + entry-agent status (best-effort; failure is non-fatal).
    await this.loadWorkspace();

    let profile;
    try {
      profile = await this.loadProfile();
    } catch (error) {
      console.error('Failed to load workspace agent profile:', error);
      this.showError('Could not load this agent from the workspace.');
      return;
    }

    if (!profile) {
      this.showError(
        `“${this.agentName}” is not attached to this workspace. Open the workspace to review its roster.`
      );
      return;
    }

    this.renderIdentity(profile);
    this.renderModel(profile);
    this.reveal();

    // Secondary sections load independently and degrade gracefully.
    this.loadPrompt();
    this.loadSkills();
    this.loadMCP();
  }

  // ---- Data loading -------------------------------------------------------

  async loadWorkspace() {
    this.isEntryAgent = false;
    try {
      const res = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`);
      if (!res.ok) return;
      const data = await res.json();
      const ws = data?.workspace || data;
      const name = String(ws?.name || '').trim();
      if (name) {
        this.setText('wad-breadcrumb-workspace', name);
        const link = this.el('wad-breadcrumb-workspace');
        if (link) link.title = name;
      }
      const entry = String(ws?.entry_agent_name || '').trim();
      this.isEntryAgent = entry !== '' && this.normalize(entry) === this.normalize(this.agentName);
    } catch (_error) {
      // Breadcrumb name + entry-agent badge are advisory.
    }
  }

  async loadProfile() {
    const res = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/agents`);
    if (!res.ok) {
      throw new Error(`agents list failed: ${res.status}`);
    }
    const data = await res.json();
    const agents = Array.isArray(data?.agents) ? data.agents : [];
    const target = this.normalize(this.agentName);
    return agents.find(a => this.normalize(a?.name) === target) || null;
  }

  async loadPrompt() {
    const [base, effective] = await Promise.all([
      this.fetchBasePrompt(),
      this.fetchEffectivePrompt()
    ]);

    // base === null means the editable prompt could not be loaded (e.g. the
    // agent has no workspace-local config); fall back to read-only display.
    this.promptEditable = base !== null;
    this.currentPrompt = base === null ? '' : base;
    this.renderPromptView(this.currentPrompt, this.promptEditable);
    this.renderRefinement(effective);
    this.bindPromptEditor();
  }

  async fetchBasePrompt() {
    try {
      const res = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agents/${encodeURIComponent(
          this.agentName
        )}/system-prompt`
      );
      if (!res.ok) return null;
      const data = await res.json();
      return String(data?.system_prompt || '');
    } catch (error) {
      console.error('Failed to load system prompt:', error);
      return null;
    }
  }

  async fetchEffectivePrompt() {
    try {
      const res = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agents/${encodeURIComponent(
          this.agentName
        )}/effective-prompt`
      );
      if (!res.ok) return null;
      const body = await res.json();
      return body?.data || body;
    } catch (error) {
      console.error('Failed to load effective prompt:', error);
      return null;
    }
  }

  async loadSkills() {
    try {
      const res = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/skill-bindings`
      );
      if (!res.ok) throw new Error(`skill-bindings failed: ${res.status}`);
      const data = await res.json();
      const bindings = Array.isArray(data?.bindings) ? data.bindings : [];
      this.renderChips(
        'wad-skills',
        'wad-skills-count',
        bindings,
        b => ({
          label: b?.skill_name,
          enabled: b?.enabled !== false
        }),
        'No skills are bound to this workspace.'
      );
    } catch (error) {
      console.error('Failed to load skill bindings:', error);
      this.renderChipsError('wad-skills', 'wad-skills-count');
    }
  }

  async loadMCP() {
    try {
      const res = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/mcp-bindings`
      );
      if (!res.ok) throw new Error(`mcp-bindings failed: ${res.status}`);
      const data = await res.json();
      const bindings = Array.isArray(data?.bindings) ? data.bindings : [];
      this.renderChips(
        'wad-mcp',
        'wad-mcp-count',
        bindings,
        b => ({
          label: b?.alias || b?.server_name,
          enabled: b?.enabled !== false
        }),
        'No MCP servers are bound to this workspace.'
      );
    } catch (error) {
      console.error('Failed to load MCP bindings:', error);
      this.renderChipsError('wad-mcp', 'wad-mcp-count');
    }
  }

  // ---- Rendering ----------------------------------------------------------

  renderIdentity(profile) {
    const name = String(profile?.name || this.agentName).trim();
    const avatar = this.el('wad-avatar');
    if (avatar) {
      avatar.textContent = this.initials(name);
      avatar.style.setProperty('--wad-hue', String(this.hue(name)));
    }
    this.setText('wad-name', name);

    const role = this.roleLabel(profile?.role);
    const type = String(profile?.type || '').trim();
    const subtitleParts = [];
    if (role) subtitleParts.push(role);
    if (type) subtitleParts.push(type);
    this.setText('wad-subtitle', subtitleParts.join(' · ') || 'Workspace agent');

    const badges = this.el('wad-badges');
    if (badges) {
      const chips = [];
      if (this.isEntryAgent) {
        // Commander-slot label (PRD FR21/FR22): "Commander" when this agent's
        // own role is orchestrator, "Acting Commander" otherwise.
        const commanderLabel =
          String(profile?.role || '')
            .trim()
            .toLowerCase() === 'orchestrator'
            ? 'Commander'
            : 'Acting Commander';
        chips.push(`<span class="wad-badge is-leader">${this.escape(commanderLabel)}</span>`);
      }
      chips.push('<span class="wad-badge is-muted">Workspace-local</span>');
      const provider = String(profile?.provider || '').trim();
      if (provider) {
        chips.push(`<span class="wad-badge">${this.escape(provider)}</span>`);
      }
      badges.innerHTML = chips.join('');
    }
  }

  renderModel(profile) {
    this.setText('wad-model', String(profile?.model || '').trim() || 'Not set');
    this.setText('wad-provider', String(profile?.provider || '').trim() || 'Not set');
  }

  renderPromptView(prompt, editable) {
    const view = this.el('wad-prompt-view');
    const value = String(prompt || '').trim();
    if (view) {
      if (value) {
        view.textContent = value;
        view.classList.remove('is-empty');
      } else {
        view.textContent = editable
          ? 'No system prompt set. Click Edit to add one.'
          : 'No system prompt set.';
        view.classList.add('is-empty');
      }
    }
    // Only expose the Edit affordance when the prompt is actually editable.
    this.el('wad-prompt-edit')?.classList.toggle('d-none', !editable);
  }

  renderRefinement(effective) {
    const wrap = this.el('wad-refinement-wrap');
    const body = this.el('wad-refinement');
    const refinement = String(effective?.refinement || '').trim();
    const note = String(effective?.note || '').trim();

    if (wrap && body && refinement) {
      body.textContent = refinement;
      wrap.classList.remove('d-none');
    } else if (wrap) {
      wrap.classList.add('d-none');
    }
    this.setText('wad-prompt-note', note);
  }

  bindPromptEditor() {
    if (this._promptBound) return;
    this._promptBound = true;
    this.el('wad-prompt-edit')?.addEventListener('click', () => this.enterEditMode());
    this.el('wad-prompt-cancel')?.addEventListener('click', () => this.exitEditMode());
    this.el('wad-prompt-save')?.addEventListener('click', () => this.savePrompt());
  }

  enterEditMode() {
    const editor = this.el('wad-prompt-editor');
    if (editor) editor.value = this.currentPrompt || '';
    this.togglePromptEditing(true);
    this.setPromptStatus('');
    editor?.focus();
  }

  exitEditMode() {
    this.togglePromptEditing(false);
    this.setPromptStatus('');
  }

  togglePromptEditing(editing) {
    this.el('wad-prompt-view')?.classList.toggle('d-none', editing);
    this.el('wad-prompt-editor')?.classList.toggle('d-none', !editing);
    this.el('wad-prompt-edit')?.classList.toggle('d-none', editing);
    this.el('wad-prompt-cancel')?.classList.toggle('d-none', !editing);
    this.el('wad-prompt-save')?.classList.toggle('d-none', !editing);
  }

  async savePrompt() {
    const editor = this.el('wad-prompt-editor');
    if (!editor) return;
    const value = editor.value;

    this.setPromptSaving(true);
    this.setPromptStatus('Saving…');
    try {
      const res = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agents/${encodeURIComponent(
          this.agentName
        )}/system-prompt`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ system_prompt: value })
        }
      );
      if (!res.ok) {
        const detail = await this.readError(res);
        throw new Error(detail || `save failed: ${res.status}`);
      }
      const data = await res.json();
      this.currentPrompt = String(data?.system_prompt ?? value.trim());
      this.renderPromptView(this.currentPrompt, true);
      this.togglePromptEditing(false);
      this.setPromptStatus('Saved.', 'success');
    } catch (error) {
      console.error('Failed to save system prompt:', error);
      this.setPromptStatus(error?.message || 'Could not save the system prompt.', 'error');
    } finally {
      this.setPromptSaving(false);
    }
  }

  setPromptSaving(saving) {
    const save = this.el('wad-prompt-save');
    const cancel = this.el('wad-prompt-cancel');
    if (save) {
      save.disabled = saving;
      save.textContent = saving ? 'Saving…' : 'Save';
    }
    if (cancel) cancel.disabled = saving;
  }

  setPromptStatus(message, kind) {
    const status = this.el('wad-prompt-status');
    if (!status) return;
    status.textContent = message || '';
    status.classList.remove('is-error', 'is-success');
    if (kind === 'error') status.classList.add('is-error');
    if (kind === 'success') status.classList.add('is-success');
  }

  async readError(res) {
    try {
      const data = await res.json();
      return String(data?.error || data?.message || '').trim();
    } catch (_error) {
      return '';
    }
  }

  renderChips(listId, countId, bindings, map, emptyLabel) {
    const list = this.el(listId);
    const count = this.el(countId);
    if (!list) return;

    const items = (bindings || []).map(map).filter(it => String(it?.label || '').trim() !== '');

    if (count) count.textContent = items.length ? `(${items.length})` : '';

    if (!items.length) {
      list.innerHTML = `<div class="wad-empty">${this.escape(emptyLabel)}</div>`;
      return;
    }

    list.innerHTML = items
      .map(it => {
        const enabled = it.enabled !== false;
        return `<span class="wad-chip${enabled ? '' : ' is-disabled'}" title="${
          enabled ? 'Enabled' : 'Disabled'
        }"><span class="wad-chip-dot"></span>${this.escape(it.label)}</span>`;
      })
      .join('');
  }

  renderChipsError(listId, countId) {
    const list = this.el(listId);
    const count = this.el(countId);
    if (count) count.textContent = '';
    if (list) list.innerHTML = `<div class="wad-empty">Could not load this list.</div>`;
  }

  reveal() {
    this.el('wad-loading')?.classList.add('d-none');
    this.el('wad-error')?.classList.add('d-none');
    this.el('wad-content')?.classList.remove('d-none');
  }

  showError(detail) {
    this.el('wad-loading')?.classList.add('d-none');
    this.el('wad-content')?.classList.add('d-none');
    const err = this.el('wad-error');
    if (err) err.classList.remove('d-none');
    if (detail) this.setText('wad-error-detail', detail);
  }

  // ---- Helpers ------------------------------------------------------------

  normalize(value) {
    return String(value || '')
      .trim()
      .toLowerCase();
  }

  roleLabel(role) {
    const key = this.normalize(role);
    if (!key) return '';
    return ROLE_LABELS[key] || role.charAt(0).toUpperCase() + role.slice(1);
  }

  initials(name) {
    const words = String(name || '')
      .trim()
      .split(/\s+/)
      .filter(Boolean);
    if (!words.length) return '?';
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
    return (words[0][0] + words[words.length - 1][0]).toUpperCase();
  }

  hue(name) {
    let hash = 0;
    const str = String(name || '');
    for (let i = 0; i < str.length; i += 1) {
      hash = (hash << 5) - hash + str.charCodeAt(i);
      hash |= 0;
    }
    return Math.abs(hash) % 360;
  }

  setText(id, value) {
    const node = this.el(id);
    if (node) node.textContent = String(value == null ? '' : value);
  }

  escape(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }
}

export default WorkspaceAgentDetailPage;
