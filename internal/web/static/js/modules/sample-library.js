function text(value) {
  return String(value == null ? '' : value);
}
function operationKey(prefix) {
  return `${prefix}-${crypto.randomUUID()}`;
}
async function payload(response) {
  try {
    return await response.json();
  } catch (_) {
    return {};
  }
}

export class SampleLibraryPanel {
  constructor({ workspaceId, program, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = text(workspaceId).trim();
    this.program = program || {};
    this.fetchImpl = (...args) => fetchImpl(...args);
    this.state = null;
    this.roots = [];
    this.entries = [];
    this.selected = new Set();
    this.busy = false;
    this.busyControls = [];
    this.busyTrigger = null;
  }
  url(path = '') {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/sample-library${path}`;
  }
  async request(path, options = {}) {
    const response = await this.fetchImpl(this.url(path), {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const body = await payload(response);
    if (!response.ok)
      throw new Error(text(body.error || body.message || 'Sample Library request failed'));
    return body;
  }
  async init() {
    const panel = document.getElementById('sampleLibraryPanel');
    if (!panel || !this.program) return;
    panel.hidden = false;
    this.isStation = Boolean(this.program.is_station);
    document
      .getElementById('sampleLibraryRemove')
      ?.addEventListener('click', event => void this.removeCapability(event.currentTarget));
    document
      .getElementById('sampleLibraryAddRoot')
      ?.addEventListener('click', event => void this.connectRoot(event.currentTarget));
    document.getElementById('sampleLibrarySearchForm')?.addEventListener('submit', event => {
      event.preventDefault();
      void this.search(event.submitter);
    });
    document
      .getElementById('sampleLibraryCopy')
      ?.addEventListener('click', event => void this.copySelected(event.currentTarget));
    document.getElementById('sampleLibraryCollectionForm')?.addEventListener('submit', event => {
      event.preventDefault();
      void this.createCollection(event.submitter);
    });
    this.renderProjects();
    if (!this.isStation) {
      document.getElementById('sampleLibraryAddRoot')?.setAttribute('hidden', '');
      document.getElementById('sampleLibraryRemove')?.setAttribute('hidden', '');
      document.getElementById('sampleLibraryRoots')?.setAttribute('hidden', '');
      document.querySelector('.sample-library-copy')?.setAttribute('hidden', '');
      document.getElementById('sampleLibraryCollectionForm')?.setAttribute('hidden', '');
      this.state = {};
      this.setStatus(
        'Find samples from the active Home catalog. The project receives no library-folder access.'
      );
      await this.search(null, false);
      return;
    }
    await this.refresh();
  }
  setStatus(message) {
    const node = document.getElementById('sampleLibraryStatus');
    if (node) node.textContent = text(message);
  }
  beginBusy(trigger, message) {
    if (this.busy) return false;
    this.busy = true;
    this.setStatus(message);
    const panel = document.getElementById('sampleLibraryPanel');
    panel?.setAttribute('aria-busy', 'true');
    this.busyControls = [...(panel?.querySelectorAll('button, input, select') || [])].map(
      control => [control, control.disabled]
    );
    this.busyControls.forEach(([control]) => {
      control.disabled = true;
    });
    this.busyTrigger = trigger || null;
    return true;
  }
  endBusy() {
    const panel = document.getElementById('sampleLibraryPanel');
    panel?.setAttribute('aria-busy', 'false');
    this.busyControls.forEach(([control, disabled]) => {
      if (control.isConnected) control.disabled = disabled;
    });
    this.busyControls = [];
    this.busy = false;
    const status = document.getElementById('sampleLibraryStatus');
    const target = this.busyTrigger?.isConnected ? this.busyTrigger : status;
    this.busyTrigger = null;
    target?.focus?.();
  }
  async refresh() {
    try {
      const body = await this.request('');
      this.state = body.state;
      this.roots = Array.isArray(body.roots) ? body.roots : [];
      this.setStatus(
        this.roots.length
          ? `${this.roots.length} connected folder${this.roots.length === 1 ? '' : 's'}. Indexing runs only when requested.`
          : 'No sample folders connected. Connecting one does not scan it.'
      );
      this.renderRoots();
      await this.search(null, false);
    } catch (error) {
      this.state = null;
      this.roots = [];
      this.renderRoots();
      this.renderEntries([]);
      this.setStatus(
        'Sample Library is not configured yet. Add the optional capability from setup when you are ready.'
      );
    }
  }
  renderProjects() {
    const select = document.getElementById('sampleLibraryProject');
    if (!select) return;
    select.replaceChildren();
    const first = document.createElement('option');
    first.value = '';
    first.textContent = 'Choose a linked project';
    select.append(first);
    for (const project of this.program.projects || []) {
      const option = document.createElement('option');
      option.value = text(project.id);
      option.textContent = text(project.name || project.id);
      select.append(option);
    }
  }
  renderRoots() {
    const root = document.getElementById('sampleLibraryRoots');
    if (!root) return;
    root.replaceChildren();
    for (const item of this.roots) {
      const card = document.createElement('article');
      card.className = 'sample-library-root';
      const copy = document.createElement('div');
      const title = document.createElement('strong');
      title.textContent = `Sample folder ${item.id.slice(0, 8)}`;
      const status = document.createElement('span');
      status.textContent = `${text(item.completeness).replaceAll('_', ' ')} · analysis ${item.hash_enabled || item.tags_enabled ? 'enabled' : 'off'}`;
      copy.append(title, status);
      const actions = document.createElement('div');
      for (const [label, handler] of [
        ['Index metadata', event => this.indexRoot(item, event.currentTarget)],
        ['Analysis settings', event => this.analysisRoot(item, event.currentTarget)],
        ['Revoke access', event => this.revokeRoot(item, event.currentTarget)]
      ]) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'modern-btn modern-btn-secondary';
        button.textContent = label;
        button.addEventListener('click', handler);
        actions.append(button);
      }
      card.append(copy, actions);
      root.append(card);
    }
  }
  async removeCapability(trigger) {
    if (!this.beginBusy(trigger, 'Preparing the add-on removal review…')) return;
    try {
      const body = await this.raw(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/capabilities/sample-library/removal`
      );
      const summary = body.removal || {};
      const lines = [
        ...(summary.impacts || []),
        ...(summary.retained_audit || []).map(value => `Retained: ${value}`),
        'Source files and confirmed project copies are not deleted.'
      ];
      if (!(await this.confirm('Remove Sample Library?', lines, 'Remove add-on', trigger))) return;
      await this.raw(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/capabilities/sample-library`,
        { method: 'DELETE', body: '{}' }
      );
      await this.refresh();
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      this.endBusy();
    }
  }
  async connectRoot(trigger) {
    if (!this.beginBusy(trigger, 'Waiting for the trusted folder picker…')) return;
    try {
      const picked = await this.raw('/api/folder-picker/select-path', {
        method: 'POST',
        body: JSON.stringify({ title: 'Choose a sample folder' })
      });
      if (!picked.selected) return;
      const reviewed = await this.request('/roots/review', {
        method: 'POST',
        body: JSON.stringify({ selection_token: picked.selection_token })
      });
      const review = reviewed.review;
      if (
        !(await this.confirm(
          'Connect this sample folder?',
          review.disclosure || [],
          'Connect folder',
          trigger
        ))
      )
        return;
      await this.request('/roots/commit', {
        method: 'POST',
        body: JSON.stringify({
          review_token: review.token,
          selection_token: picked.selection_token,
          idempotency_key: operationKey('connect')
        })
      });
      await this.refresh();
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      this.endBusy();
    }
  }
  async raw(path, options = {}) {
    const response = await this.fetchImpl(path, {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const body = await payload(response);
    if (!response.ok) throw new Error(text(body.error || 'Request failed'));
    return body;
  }
  async indexRoot(root, trigger) {
    if (!this.beginBusy(trigger, 'Indexing approved metadata…')) return;
    try {
      await this.request(`/roots/${encodeURIComponent(root.id)}/index`, {
        method: 'POST',
        body: JSON.stringify({
          idempotency_key: operationKey('index'),
          catalog_revision: this.state.catalog_revision,
          root_revision: root.revision
        })
      });
      await this.refresh();
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      this.endBusy();
    }
  }
  async analysisRoot(root, trigger) {
    const enable = !(root.hash_enabled || root.tags_enabled);
    if (!this.beginBusy(trigger, 'Preparing the content-analysis review…')) return;
    try {
      const body = await this.request(`/roots/${encodeURIComponent(root.id)}/analysis/review`, {
        method: 'POST',
        body: JSON.stringify({ hash_enabled: enable, embedded_tags_enabled: enable })
      });
      const review = body.review;
      if (
        !(await this.confirm(
          enable ? 'Enable content analysis?' : 'Disable content analysis?',
          review.disclosure || [],
          enable ? 'Enable analysis' : 'Disable analysis',
          trigger
        ))
      )
        return;
      await this.request(`/roots/${encodeURIComponent(root.id)}/analysis/commit`, {
        method: 'POST',
        body: JSON.stringify({
          review_token: review.token,
          idempotency_key: operationKey('analysis'),
          hash_enabled: enable,
          embedded_tags_enabled: enable
        })
      });
      await this.refresh();
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      this.endBusy();
    }
  }
  async revokeRoot(root, trigger) {
    if (!this.beginBusy(trigger, 'Preparing the folder-access review…')) return;
    try {
      const body = await this.request(`/roots/${encodeURIComponent(root.id)}/revoke/review`, {
        method: 'POST',
        body: '{}'
      });
      const review = body.review;
      if (
        !(await this.confirm(
          'Revoke folder access?',
          review.disclosure || [],
          'Revoke access',
          trigger
        ))
      )
        return;
      await this.request(`/roots/${encodeURIComponent(root.id)}/revoke/commit`, {
        method: 'POST',
        body: JSON.stringify({
          review_token: review.token,
          idempotency_key: operationKey('revoke')
        })
      });
      await this.refresh();
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      this.endBusy();
    }
  }
  async search(trigger = null, manageBusy = true) {
    if (!this.state) return;
    if (manageBusy && !this.beginBusy(trigger, 'Searching the active catalog…')) return;
    const query = text(document.getElementById('sampleLibrarySearch')?.value).trim();
    try {
      const route = this.isStation ? '/search' : '/find';
      const body = await this.request(
        `${route}?q=${encodeURIComponent(query)}&sort=name&direction=asc`
      );
      this.entries = body.result?.entries || [];
      this.renderEntries(this.entries);
      this.setStatus(
        `${this.entries.length} active catalog result${this.entries.length === 1 ? '' : 's'}.`
      );
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      if (manageBusy) this.endBusy();
    }
  }
  renderEntries(entries) {
    const body = document.getElementById('sampleLibraryEntries');
    if (!body) return;
    body.replaceChildren();
    this.selected.clear();
    for (const entry of entries) {
      const row = document.createElement('tr');
      const selectCell = document.createElement('td');
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.setAttribute('aria-label', `Select ${text(entry.filename)}`);
      checkbox.addEventListener('change', () => {
        checkbox.checked ? this.selected.add(entry.id) : this.selected.delete(entry.id);
        this.updateCopyButton();
      });
      selectCell.append(checkbox);
      row.append(selectCell);
      for (const value of [
        entry.filename,
        entry.extension,
        `${Number(entry.size_bytes || 0).toLocaleString()} bytes`
      ]) {
        const cell = document.createElement('td');
        cell.textContent = text(value);
        row.append(cell);
      }
      body.append(row);
    }
    this.updateCopyButton();
  }
  updateCopyButton() {
    const button = document.getElementById('sampleLibraryCopy');
    if (button) button.disabled = this.selected.size === 0;
  }
  async copySelected(trigger) {
    const childId = text(document.getElementById('sampleLibraryProject')?.value);
    if (!childId) {
      this.setStatus('Choose one linked project.');
      document.getElementById('sampleLibraryProject')?.focus();
      return;
    }
    const entryIds = [...this.selected];
    if (!this.beginBusy(trigger, 'Preparing an exact sample-copy review…')) return;
    try {
      const body = await this.request('/copies/review', {
        method: 'POST',
        body: JSON.stringify({ child_workspace_id: childId, entry_ids: entryIds })
      });
      const review = body.review;
      const lines = [
        ...(review.disclosure || []),
        ...(review.items || []).map(
          item =>
            `${item.source_path} → ${item.destination_path}${item.collision_resolved ? ' (renamed to avoid a conflict)' : ''}`
        )
      ];
      if (!(await this.confirm('Copy selected samples?', lines, 'Copy samples', trigger))) return;
      await this.request('/copies/commit', {
        method: 'POST',
        body: JSON.stringify({
          child_workspace_id: childId,
          entry_ids: entryIds,
          review_token: review.token,
          idempotency_key: operationKey('copy')
        })
      });
      this.setStatus(
        `${entryIds.length} sample${entryIds.length === 1 ? '' : 's'} copied to the selected project. Source files were unchanged.`
      );
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      this.endBusy();
    }
  }
  async createCollection(trigger) {
    const input = document.getElementById('sampleLibraryCollectionName');
    const name = text(input?.value).trim();
    if (!name || !this.state) return;
    if (!this.beginBusy(trigger, 'Preparing the collection review…')) return;
    try {
      const proposal = { name, note: '', catalog_revision: this.state.catalog_revision };
      const reviewed = await this.request('/collections/review', {
        method: 'POST',
        body: JSON.stringify(proposal)
      });
      const review = reviewed.review;
      if (
        !(await this.confirm(
          'Create this collection?',
          review.disclosure || [],
          'Create collection',
          trigger
        ))
      )
        return;
      await this.request('/collections', {
        method: 'POST',
        body: JSON.stringify({
          ...proposal,
          review_token: review.token,
          idempotency_key: operationKey('collection')
        })
      });
      input.value = '';
      await this.refresh();
      this.setStatus(`Collection “${name}” created.`);
    } catch (error) {
      this.setStatus(error.message);
    } finally {
      this.endBusy();
    }
  }
  confirm(title, lines, confirmLabel, trigger) {
    return new Promise(resolve => {
      const dialog = document.createElement('dialog');
      dialog.className = 'assistant-program-hire-dialog sample-library-dialog';
      const form = document.createElement('form');
      form.method = 'dialog';
      const heading = document.createElement('h2');
      heading.id = `sampleLibraryDialogTitle-${operationKey('dialog')}`;
      heading.textContent = title;
      dialog.setAttribute('aria-labelledby', heading.id);
      const list = document.createElement('ul');
      for (const value of lines) {
        const item = document.createElement('li');
        item.textContent = text(value);
        list.append(item);
      }
      const actions = document.createElement('div');
      actions.className = 'assistant-program-dialog-actions';
      const cancel = document.createElement('button');
      cancel.type = 'button';
      cancel.className = 'modern-btn modern-btn-secondary';
      cancel.textContent = 'Cancel';
      const confirm = document.createElement('button');
      confirm.type = 'submit';
      confirm.className = 'modern-btn modern-btn-primary';
      confirm.textContent = confirmLabel;
      actions.append(cancel, confirm);
      form.append(heading, list, actions);
      dialog.append(form);
      document.body.append(dialog);
      let accepted = false;
      cancel.addEventListener('click', () => dialog.close());
      form.addEventListener('submit', () => {
        accepted = true;
      });
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          trigger?.focus();
          resolve(accepted);
        },
        { once: true }
      );
      dialog.showModal();
      cancel.focus();
    });
  }
}
