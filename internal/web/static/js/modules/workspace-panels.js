import { chromeText as text } from './workspace-command-chrome.js';
import { workspaceOverlayCoordinator } from './workspace-overlay-coordinator.js';
import { writeRailSection } from './workspace-command-rail.js';

const SECTIONS = {
  settings: [
    ['general', 'General'],
    ['automation', 'Goal & automation'],
    ['tools', 'Tools & connections'],
    ['advanced', 'Advanced']
  ],
  resources: [
    ['notes', 'Notes'],
    ['files', 'Files & linked folders'],
    ['memory', 'Memory'],
    ['sessions', 'Conversations']
  ]
};

// A persistent presentation host. Every save, editor and protected operation is
// delegated to its existing owner; drafts here are never a second workspace store.
export class WorkspacePanels {
  constructor(command, coordinator = workspaceOverlayCoordinator()) {
    this.command = command;
    this.coordinator = coordinator;
    this.node = null;
    this.kind = '';
    this.section = '';
    this.id = '';
    this.dirty = false;
    this.dirtyFields = new Set();
    this.busy = false;
  }

  get page() {
    return this.command.page || {};
  }

  ensure() {
    if (this.node) return this.node;
    const node = document.createElement('div');
    node.className = 'ws-cmd ws-workspace-panels';
    node.hidden = true;
    node.innerHTML =
      '<div class="ws-panels-backdrop" data-panel-close></div>' +
      '<section class="ws-panels-dialog" role="dialog" aria-modal="true" tabindex="-1">' +
      '<header><h2 data-panel-title></h2><button type="button" class="ws-cmd-nav-btn" data-panel-close aria-label="Close workspace panel">Close</button></header>' +
      '<nav data-panel-nav aria-label="Workspace panel sections"></nav>' +
      '<div data-panel-content></div><p data-panel-status role="status" aria-live="polite"></p></section>';
    document.body.appendChild(node);
    node.addEventListener('click', event => this.click(event));
    node.addEventListener('change', event => {
      const checkbox = event.target.closest('[data-panel-note-select]');
      if (checkbox)
        this.page.toggleNoteSelection?.(
          checkbox.getAttribute('data-panel-note-select'),
          checkbox.checked
        );
    });
    node.addEventListener('submit', event => {
      const form = event.target.closest('[data-panel-field]');
      if (!form) return;
      event.preventDefault();
      void this.save(form);
    });
    node.addEventListener('input', event => {
      const form = event.target.closest('[data-panel-field]');
      if (form) {
        this.dirty = true;
        this.dirtyFields.add(form.getAttribute('data-panel-field'));
      }
    });
    node.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        this.close();
      }
    });
    // Legacy Bootstrap editors do not register with the coordinator. Register
    // only while this host is open, using their real lifecycle (never fake hide).
    this.onChildShow = event => {
      if (!this.kind || !event.target?.classList?.contains('modal')) return;
      const child = event.target;
      this.coordinator.open({
        id: 'workspace-panel-child:' + child.id,
        kind: 'modal',
        container: child,
        canEscape: () => false,
        trigger: null,
        onClose: info => {
          child.style.visibility = info.reason === 'suspended' ? 'hidden' : '';
          child.inert = info.reason === 'suspended';
        },
        onResume: () => {
          child.style.visibility = '';
          child.inert = false;
        }
      });
    };
    this.onChildHidden = event => {
      const key = 'workspace-panel-child:' + event.target?.id;
      if (this.coordinator.openIds().includes(key)) {
        this.coordinator.close(key);
        this.resolveChildOpener()?.focus?.({ preventScroll: true });
      }
    };
    document.addEventListener('show.bs.modal', this.onChildShow);
    document.addEventListener('hidden.bs.modal', this.onChildHidden);
    this.node = node;
    return node;
  }

  open(kind, trigger, section = '') {
    if (!SECTIONS[kind]) return;
    if (this.kind && !this.confirmDiscard()) return;
    this.ensure();
    if (this.kind) this.coordinator.close('workspace-panels');
    this.kind = kind;
    this.id = this.command.workspaceId();
    this.section = section || SECTIONS[kind][0][0];
    if (kind === 'resources') writeRailSection(this.command.railStorage(), this.id, this.section);
    this.trigger = trigger;
    this.dirty = false;
    this.dirtyFields.clear();
    this.node.hidden = false;
    this.render();
    this.coordinator.open({
      id: 'workspace-panels',
      kind: 'modal',
      container: this.node,
      canEscape: () => !this.busy && !this.dirty,
      trigger: null,
      onClose: info => {
        this.node.hidden = true;
        if (info.reason === 'closed') this.kind = '';
      },
      onResume: () => {
        this.node.hidden = false;
        this.refreshResources();
        const target = this.resolveChildOpener() || this.node.querySelector('.ws-panels-dialog');
        target?.focus({ preventScroll: true });
      }
    });
    this.node.querySelector('.ws-panels-dialog').focus();
  }

  confirmDiscard() {
    if (this.busy) return false;
    return !this.dirty || window.confirm('Discard unsaved workspace changes?');
  }

  close() {
    if (!this.confirmDiscard()) return false;
    const selector =
      this.kind === 'resources'
        ? this.command.viewMode === 'map'
          ? '[data-cmd-map-resources]'
          : '[data-cmd-resources]'
        : '[data-cmd-settings]';
    this.coordinator.close('workspace-panels');
    this.dirty = false;
    // kind was cleared by onClose; prefer the original door's identity.
    const resource = this.trigger?.getAttribute('data-cmd-resource-section');
    const replacement = resource
      ? Array.from(
          this.command.container?.querySelectorAll('[data-cmd-resource-section]') || []
        ).find(node => node.getAttribute('data-cmd-resource-section') === resource)
      : null;
    const door = this.trigger?.isConnected
      ? this.trigger
      : replacement || this.command.container?.querySelector(selector);
    door?.focus?.({ preventScroll: true });
    return true;
  }

  // A workspace identity change cannot carry old drafts or approvals into the
  // new workspace. Pending writes finish against their captured original owner.
  syncIdentity() {
    if (this.kind && this.id !== this.command.workspaceId()) {
      this.coordinator.close('workspace-panels');
      this.dirty = false;
      this.id = '';
    }
  }

  render() {
    const title = this.kind === 'settings' ? 'Workspace settings' : 'Resources';
    this.node.querySelector('[data-panel-title]').textContent = title;
    this.node.querySelector('[role="dialog"]').setAttribute('aria-label', title);
    this.node.querySelector('[data-panel-nav]').innerHTML = SECTIONS[this.kind]
      .map(
        ([key, label]) =>
          `<button type="button" class="ws-cmd-view-btn${key === this.section ? ' is-active' : ''}" data-panel-section="${key}" aria-pressed="${key === this.section}">${label}</button>`
      )
      .join('');
    const content = this.content();
    this.node.querySelector('[data-panel-content]').innerHTML = content;
    if (this.kind === 'resources') this.resourceHTML = content;
    this.status('');
  }

  button(action, label, id = '') {
    return `<button type="button" class="ws-cmd-nav-btn" data-panel-action="${action}" data-panel-id="${text(id)}">${text(label)}</button>`;
  }

  content() {
    const c = this.command;
    const p = this.page;
    const ws = p.workspace || {};
    const button = (...args) => this.button(...args);
    if (this.kind === 'settings') {
      if (this.section === 'general') {
        const fields = [
          ['name', 'Name', ws.name || ''],
          ['description', 'Description', ws.description || ''],
          ['tags', 'Tags (comma separated)', c.workspaceTags().join(', ')]
        ];
        return (
          '<p>Each field saves separately. Description describes this workspace; intent adds planning and setup context.</p>' +
          fields
            .map(
              ([key, label, value]) =>
                `<form data-panel-field="${key}"><label for="ws-panel-${key}">${label}</label>` +
                (key === 'description'
                  ? `<textarea id="ws-panel-${key}" name="value" rows="3">${text(value)}</textarea>`
                  : `<input id="ws-panel-${key}" name="value" value="${text(value)}">`) +
                `<button type="submit" class="ws-cmd-nav-btn">Save ${label.split(' (')[0].toLowerCase()}</button></form>`
            )
            .join('') +
          button('owner', 'Intent & setup context', 'intent')
        );
      }
      if (this.section === 'automation')
        return (
          '<p>Workspace goal, manager behavior and schedules have separate settings. Opening them does not start work or enable routines.</p>' +
          button('goal', 'Goal & findings') +
          button('owner', 'Goal automation settings', 'mission') +
          button('owner', 'Manager behavior & planning policy', 'settings') +
          button('schedules', 'Schedules') +
          button('triggers', 'Triggers')
        );
      if (this.section === 'tools')
        return (
          '<p>These connections belong to the workspace. Agent loadout and grants remain in Team.</p>' +
          c
            .toolsTabs()
            .map(tab => button('tools', tab.label, tab.key))
            .join('') +
          `<a class="ws-cmd-nav-btn" href="${text(c.workflowHref())}">Orchestration Skills</a>`
        );
      return (
        '<p>Setup details and moving this workspace retain their existing review and consent steps.</p>' +
        button('setup', 'Setup & template details') +
        button('continuity', 'Moving this workspace') +
        `<a class="ws-cmd-nav-btn" href="${text(c.workspaceRoute('/diagnostics'))}">Diagnostics</a>` +
        '<div data-cmd-continuity-settings-mount></div>' +
        c.groupTemplateStatusHTML() +
        c.buildSummaryHTML(ws)
      );
    }
    const known = p.initialListsLoaded === true;
    const unavailable = key => !known || p[key + 'Loading'] || p[key + 'LoadFailed'];
    if (this.section === 'memory')
      return (
        '<p>Memory is what agents are shown. Your notes folder is separate.</p>' +
        button('owner', 'Open memory manager', 'memory')
      );
    if (this.section === 'files')
      return (
        '<h3>Files</h3>' +
        button('files', 'Browse workspace files') +
        button('upload', 'Upload files') +
        '<h3>Linked folders</h3>' +
        button('folder-add', 'Link folder') +
        (!unavailable('directories')
          ? c.folderRailItems(c.folderRowData()).join('') || '<p>No linked folders yet.</p>'
          : '<p>Folder information is not yet available.</p>')
      );
    if (this.section === 'sessions')
      return (
        button('session-new', 'New conversation') +
        (!unavailable('sessions')
          ? (p.sessions || [])
              .map(session =>
                button(
                  'session',
                  session.title || session.name || 'Untitled conversation',
                  session.id
                )
              )
              .join('') || '<p>No conversations yet.</p>'
          : '<p>Conversations are not yet available.</p>')
      );
    return (
      button('note-new', 'New note') +
      button('note-select', 'Select all notes') +
      button('note-copy', 'Copy selected notes') +
      button('note-delete', 'Delete selected notes') +
      (!unavailable('notes')
        ? (p.notes || [])
            .map(
              note =>
                '<label class="ws-panel-note"><input type="checkbox" data-panel-note-select="' +
                text(note.id) +
                '"' +
                (p.selectedNoteIds?.has(String(note.id)) ? ' checked' : '') +
                '>Select note</label>' +
                button('note', note.title || note.name || 'Untitled note', note.id)
            )
            .join('') || '<p>No notes yet.</p>'
        : '<p>Notes are not yet available.</p>')
    );
  }

  refreshResources() {
    if (this.kind !== 'resources' || this.node.hidden) return;
    const content = this.node.querySelector('[data-panel-content]');
    const html = this.content();
    if (html === this.resourceHTML) return;
    this.resourceHTML = html;
    const active = document.activeElement;
    const action = content.contains(active) ? active.getAttribute('data-panel-action') : null;
    const id = action ? active.getAttribute('data-panel-id') : '';
    content.innerHTML = html;
    if (action)
      Array.from(content.querySelectorAll('[data-panel-action]'))
        .find(
          node =>
            node.getAttribute('data-panel-action') === action &&
            node.getAttribute('data-panel-id') === id
        )
        ?.focus?.({ preventScroll: true });
  }

  status(message) {
    this.node.querySelector('[data-panel-status]').textContent = message;
  }

  async save(form) {
    if (this.busy || this.id !== this.command.workspaceId()) return;
    const field = form.getAttribute('data-panel-field');
    if (
      field === 'name' &&
      [...this.dirtyFields].some(key => key !== 'name') &&
      !window.confirm(
        'Renaming may reopen this workspace. Discard the other unsaved fields and rename?'
      )
    )
      return;
    const value = form.elements.value.value;
    this.busy = true;
    form.querySelector('button').disabled = true;
    try {
      if (field === 'tags') await this.page.saveWorkspaceTagList(value.split(','));
      else {
        const result = await this.page.saveWorkspaceIdentityField(field, value, {
          currentValue: String(this.page.workspace?.[field] || '')
        });
        if (!result || result.error || result.invalid)
          throw new Error('Could not save. Review the field and try again.');
      }
      this.dirtyFields.delete(field);
      this.dirty = this.dirtyFields.size > 0;
      this.status('Saved.');
    } catch (error) {
      this.status(error.message || 'Could not save. Your draft is unchanged.');
    } finally {
      this.busy = false;
      form.querySelector('button').disabled = false;
    }
  }

  click(event) {
    if (event.target.closest('[data-panel-close]')) {
      this.close();
      return;
    }
    const section = event.target.closest('[data-panel-section]');
    if (section) {
      if (!this.confirmDiscard()) return;
      this.section = section.getAttribute('data-panel-section');
      if (this.kind === 'resources')
        writeRailSection(this.command.railStorage(), this.id, this.section);
      this.dirty = false;
      this.dirtyFields.clear();
      this.render();
      this.node.querySelector(`[data-panel-section="${this.section}"]`)?.focus();
      return;
    }
    for (const [attribute, kind] of [
      ['data-cmd-project-action', 'run'],
      ['data-cmd-project-surface', 'open'],
      ['data-cmd-project-setup', 'setup']
    ]) {
      const button = event.target.closest('[' + attribute + ']');
      if (button) {
        this.command.runProjectEntryAction(kind, button.getAttribute(attribute), button);
        return;
      }
    }
    const folder = event.target.closest('[data-cmd-open-section="folders"]');
    if (folder) {
      this.command.openRailItem(
        'folders',
        folder.getAttribute('data-cmd-item-id'),
        folder.getAttribute('data-cmd-item-source')
      );
      return;
    }
    const relink = event.target.closest('[data-cmd-relink-anchor]');
    if (relink) {
      void this.page.promptRelinkDirectory?.(
        relink.getAttribute('data-cmd-relink-anchor'),
        'reference',
        relink
      );
      return;
    }
    const action = event.target.closest('[data-panel-action]');
    if (!action) return;
    this.run(
      action.getAttribute('data-panel-action'),
      action.getAttribute('data-panel-id'),
      action
    );
  }

  resolveChildOpener() {
    if (this.childOpener?.isConnected) return this.childOpener;
    const key = this.childOpenerKey;
    return key
      ? Array.from(this.node?.querySelectorAll('[data-panel-action]') || []).find(
          node =>
            node.getAttribute('data-panel-action') === key.action &&
            node.getAttribute('data-panel-id') === key.id
        )
      : null;
  }

  run(action, id, trigger) {
    this.childOpener = trigger;
    this.childOpenerKey = { action, id: id || '' };
    const c = this.command;
    const p = this.page;
    switch (action) {
      case 'owner':
        c.openStatModal(id, trigger);
        break;
      case 'tools':
        c.activeToolsTab = id;
        c.openStatModal('tools', trigger);
        break;
      case 'goal':
        window.workspaceMission?.openGoalModal?.();
        break;
      case 'schedules':
        if (this.close()) p.showSchedulesModal?.();
        break;
      case 'triggers':
        c.openStatModal('triggers', trigger);
        break;
      case 'setup':
        this.openNative('setupWizardDialog', () => window.SetupWizard?.open?.());
        break;
      case 'continuity':
        this.openNative('workspaceContinuityDialog', () =>
          window.WorkspaceContinuityStatus?.open?.(trigger)
        );
        break;
      case 'note-new':
        p.showNoteModal?.();
        break;
      case 'note':
        c.openRailItem('notes', id);
        break;
      case 'note-select':
        c.handleNoteAction('select-all');
        break;
      case 'note-copy':
        c.handleNoteAction('copy');
        break;
      case 'note-delete':
        c.handleNoteAction('delete');
        break;
      case 'files':
        p.openWorkspaceFilesExplorer?.();
        break;
      case 'upload':
        p.showFileModal?.();
        break;
      case 'folder-add': {
        const entry = p.showAddDirectoryModal?.(trigger);
        if (entry?.dialog) this.adoptNative(entry.dialog);
        break;
      }
      case 'folder':
        c.openRailItem('folders', id, 'reference');
        break;
      case 'session-new':
        if (this.close()) p.createNewSession?.();
        break;
      case 'session':
        if (this.close()) p.openSession?.(id);
        break;
      default:
        break;
    }
  }

  adoptNative(dialog) {
    const key = 'workspace-native-child:' + (dialog.id || 'folder-path');
    this.coordinator.open({ id: key, kind: 'modal', container: dialog, canEscape: () => false });
    dialog.addEventListener(
      'close',
      () => {
        this.coordinator.close(key);
        this.childOpener?.focus?.({ preventScroll: true });
      },
      { once: true }
    );
  }

  openNative(id, open) {
    const dialog = document.getElementById(id);
    if (!dialog) {
      this.status('This manager is unavailable.');
      return;
    }
    const key = 'workspace-native-child:' + id;
    this.coordinator.open({ id: key, kind: 'modal', container: dialog, canEscape: () => false });
    dialog.addEventListener(
      'close',
      () => {
        this.coordinator.close(key);
        this.childOpener?.focus?.({ preventScroll: true });
      },
      { once: true }
    );
    open();
    if (!dialog.open && !dialog.hasAttribute('open')) {
      this.coordinator.close(key);
      this.status('No setup is required here, or its status is not yet available.');
    }
  }

  destroy() {
    if (this.kind) this.coordinator.close('workspace-panels');
    document.removeEventListener('show.bs.modal', this.onChildShow);
    document.removeEventListener('hidden.bs.modal', this.onChildHidden);
    this.node?.remove();
    this.node = null;
  }
}
