// Read-only workspace detail surface. Share Home's qualified keys and read
// adapters, not its editor, menus, tab persistence or cross-workspace index.
import {
  loadFilePreview,
  loadLinkedPage,
  loadNote,
  loadWorkspaceContents,
  responseError,
  sectionKey
} from './home-tree-sources.js';
import {
  findWorkspaceNode,
  directMembers,
  flattenTree,
  isGroupNode,
  readWorkspaceTree
} from './workspace-detail-members.js';
import { iconHTML, rowIconName } from './home-tree-icons.js';
import { escapeHtml as esc, renderMarkdown } from './note-editor.js';

const sections = [
  ['files', 'Files'],
  ['notes', 'Notes']
];

const linkedBranch = row => row.kind === 'linked' || row.kind === 'linkedFolder';
const loadable = row => row.kind === 'section' || linkedBranch(row);

export async function readPreview(row, options = {}) {
  if (row.kind === 'linkedFile') return { kind: 'none' };
  if (row.kind === 'file')
    return loadFilePreview(row.workspaceId, row.meta.path, { ...options, size: row.meta.size });
  if (row.kind !== 'note') throw new Error('Unsupported item');
  const note = await loadNote(row.meta.noteId, options);
  if (note.workspaceId !== row.workspaceId || note.id !== row.meta.noteId)
    throw new Error('This note is no longer in this workspace.');
  return { kind: 'markdown', text: note.content };
}

export function previewHTML(value, name) {
  if (value.tooLarge) return '<p>This file is too large to preview here.</p>';
  if (value.kind === 'markdown') {
    // Only document formatting belongs here. Embedded resources in Markdown
    // must not make requests on behalf of the reader or mount a live form.
    const purifier = globalThis.window?.DOMPurify;
    if (purifier?.sanitize)
      return purifier.sanitize(renderMarkdown(value.text), {
        ALLOWED_TAGS: [
          'p',
          'br',
          'hr',
          'h1',
          'h2',
          'h3',
          'h4',
          'h5',
          'h6',
          'em',
          'strong',
          'del',
          'pre',
          'code',
          'ol',
          'ul',
          'li',
          'blockquote',
          'table',
          'thead',
          'tbody',
          'tr',
          'td',
          'th',
          'a'
        ],
        ALLOWED_ATTR: ['href', 'title']
      });
    // Without marked, the shared renderer only formats escaped text.
    if (!globalThis.window?.marked?.parse) return renderMarkdown(value.text);
    return `<pre>${esc(value.text)}</pre>`;
  }
  if (value.kind === 'text') return `<pre>${esc(value.text)}</pre>`;
  if (value.kind === 'image') return `<img src="${esc(value.url)}" alt="${esc(name)}">`;
  return '<p>No preview available</p>';
}

export class WorkspaceFilesView {
  constructor(host, root) {
    this.host = host;
    this.root = root;
    this.expanded = new Set([root.id]);
    this.sections = new Map();
    this.members = new Map();
    this.membershipVersion = 0;
    this.linkedKey = sectionKey(root.id, 'members');
    this.focusKey = root.id;
    this.previewVersion = 0;
    this.opening = new Set();
  }

  enter(root) {
    const renamed = this.root.name !== root.name;
    this.root = root;
    if (this.controller) {
      if (renamed) {
        this.tree.setAttribute('aria-label', `${root.name} contents`);
        this.paintTree();
        this.paintPreview();
      }
      return;
    }
    this.controller = new AbortController();
    this.host.innerHTML = `<aside class="wf-index"><h2 class="wf-kicker">Contents</h2><div class="wf-tree" role="tree" aria-label="${esc(root.name)} contents"></div></aside><article class="wf-preview" aria-label="Item preview"><button type="button" class="wf-back" data-files-back>${iconHTML('back')}Back to files</button><span class="wf-kicker">Preview</span><p class="wf-crumb"></p><h2 tabindex="-1" aria-live="polite" aria-atomic="true"></h2><div class="wf-body"></div></article>`;
    this.tree = this.host.querySelector('[role="tree"]');
    this.article = this.host.querySelector('article');
    const options = { signal: this.controller.signal };
    this.host.addEventListener('click', e => this.click(e), options);
    this.tree.addEventListener('keydown', e => this.keydown(e), options);
    this.paintTree();
    this.paintPreview();
    this.syncExpanded();
  }

  // On re-entry only re-read sections reached through live, expanded parents.
  // A remembered key is not permission to fetch a row or a member.
  syncExpanded() {
    for (const row of this.rows) {
      if (!this.expanded.has(row.id)) continue;
      if (row.kind === 'members' && !this.membershipStatus) void this.loadMembers();
      if (loadable(row) && !this.sections.has(row.id)) void this.load(row);
    }
    this.restoreSelection();
  }

  restoreSelection() {
    if (!this.saved) return;
    const row = this.rows.find(r => r.id === this.saved.key);
    const pending =
      this.membershipStatus === 'loading' ||
      [...this.sections.values()].some(s => !s.rows && !s.error);
    if (!row && pending) return;
    const saved = this.saved;
    this.saved = null;
    this.focusKey =
      row?.id || saved.ancestors.find(key => this.rows.some(r => r.id === key)) || this.root.id;
    if (row && saved.hadPreview) {
      this.showPreview = saved.showPreview;
      void this.select(row, { restore: true });
    } else {
      this.branchSelection = !row || saved.wasSelected ? this.focusKey : null;
      this.paintTree();
    }
    this.tree.scrollTop = saved.scroll;
  }

  leave() {
    if (!this.controller) return;
    // Store keys, never the content or the membership snapshot. A fast second
    // switch while revalidating must not replace the original saved position.
    if (!this.saved && this.tree) {
      const key = this.selected?.id || this.focusKey;
      const ancestors = [];
      let r = this.rows.find(row => row.id === key);
      while (r?.parent) {
        ancestors.push(r.parent);
        r = this.rows.find(row => row.id === r.parent);
      }
      this.saved = {
        key,
        ancestors,
        hadPreview: !!this.selected,
        wasSelected: this.branchSelection === key,
        scroll: this.tree.offsetHeight ? this.tree.scrollTop : this.previewScroll,
        showPreview: this.showPreview
      };
    }
    this.stopWatchingLink();
    this.controller.abort();
    this.controller = null;
    this.membershipVersion++;
    this.members.clear();
    this.membershipStatus = null;
    this.sections.clear();
    this.previewVersion++;
    this.selected = this.value = this.error = null;
    this.showPreview = false;
    this.host.replaceChildren();
    this.tree = this.article = null;
  }

  // Only the confirmed root's subtree is retained. Neither the global tree
  // nor source-folder labels are an authority for an item's owner.
  membershipRead(rootId, group) {
    if (!this.controller || rootId !== this.root.id || !this.membershipStatus) return;
    ++this.membershipVersion;
    const ok = group?.id === rootId && isGroupNode(group);
    this.membershipStatus = ok ? 'ready' : 'failed';
    this.members = new Map((ok ? flattenTree([group]) : []).map(node => [node.id, node]));
    const allowed = key => {
      const owner = key.split('/')[0];
      return owner === rootId || this.members.has(owner);
    };
    if (this.watchedOwner && !allowed(this.watchedOwner)) this.stopWatchingLink();
    for (const key of this.sections.keys()) if (!allowed(key)) this.sections.delete(key);
    for (const key of this.expanded) if (!allowed(key)) this.expanded.delete(key);
    if (this.selected && !allowed(this.selected.id)) {
      ++this.previewVersion;
      this.selected = this.value = this.error = null;
      this.focusKey = this.linkedKey;
    }
    this.paintTree();
    this.paintPreview();
    if (this.rows) this.syncExpanded();
  }

  async loadMembers() {
    const version = ++this.membershipVersion;
    this.membershipStatus = 'loading';
    this.paintTree();
    let group;
    try {
      group = findWorkspaceNode(
        await readWorkspaceTree({ signal: this.controller.signal }),
        this.root.id
      );
    } catch {
      /* No confirmed membership; leave retry in the branch. */
    }
    if (version === this.membershipVersion) this.membershipRead(this.root.id, group);
  }

  // Flatten visible rows only. A section is queried only when disclosed.
  visibleRows() {
    const rows = [];
    const visit = (row, level, parent) => {
      rows.push({ ...row, level, parent });
      if (!this.expanded.has(row.id)) return;
      const child = entry => visit(entry, level + 1, row.id);
      const memberRows = owner =>
        directMembers(this.members.get(owner)).forEach(node =>
          child({ id: node.id, kind: isGroupNode(node) ? 'group' : 'workspace', label: node.name })
        );
      if (row.kind === 'workspace' || row.kind === 'group') {
        for (const [section, label] of sections)
          child({
            id: sectionKey(row.id, section),
            workspaceId: row.id,
            kind: 'section',
            section,
            label
          });
        if (row.kind === 'group') {
          if (row.id === this.root.id)
            child({ id: this.linkedKey, kind: 'members', label: 'Linked workspaces' });
          else memberRows(row.id);
        }
      } else if (row.kind === 'members') {
        if (
          this.membershipStatus === 'ready' &&
          directMembers(this.members.get(this.root.id)).length
        )
          memberRows(this.root.id);
        else
          child({
            id: `${row.id}/status`,
            kind: this.membershipStatus === 'failed' ? 'retry' : 'status',
            label:
              this.membershipStatus === 'failed'
                ? 'Could not load linked workspaces. Retry'
                : this.membershipStatus === 'ready'
                  ? 'No linked workspaces'
                  : 'Loading…'
          });
      } else if (loadable(row)) {
        const state = this.sections.get(row.id);
        if (state?.rows?.length) state.rows.forEach(child);
        else
          child({
            id: `${row.id}/status`,
            kind: state?.error ? 'retry' : 'status',
            label: state?.error
              ? linkedBranch(row)
                ? 'Folder unavailable. Retry'
                : `Could not load ${row.label.toLowerCase()}. Retry`
              : state?.rows
                ? linkedBranch(row)
                  ? 'Empty folder'
                  : `No ${row.label.toLowerCase()} yet`
                : 'Loading…'
          });
      } else row.children?.forEach(child);
    };
    visit(
      {
        id: this.root.id,
        kind: this.root.kind === 'group' ? 'group' : 'workspace',
        label: this.root.name
      },
      1,
      null
    );
    return rows;
  }

  paintTree() {
    const focused = this.tree.contains(document.activeElement);
    this.rows = this.visibleRows();
    if (!this.rows.some(r => r.id === this.focusKey)) this.focusKey = this.root.id;
    this.tree.innerHTML = this.rows
      .map((r, i) => {
        const branch =
          linkedBranch(r) ||
          ['workspace', 'group', 'section', 'members', 'folder'].includes(r.kind);
        const expanded = this.expanded.has(r.id);
        return `<div role="treeitem" tabindex="${r.id === this.focusKey ? 0 : -1}" data-row="${i}" data-key="${esc(r.id)}" aria-level="${r.level}" aria-selected="${(this.selected?.id || this.branchSelection) === r.id}" ${branch ? `aria-expanded="${expanded}"` : ''} style="--wf-level:${r.level}">${branch ? iconHTML(expanded ? 'caretDown' : 'caretRight', { size: 12 }) : '<span class="wf-spacer"></span>'}${iconHTML(r.kind === 'members' ? 'linkedFolders' : rowIconName(r))}<span>${esc(r.label)}</span>${r.kind === 'linked' ? '<small class="wf-source-label">Linked folder</small>' : ''}</div>`;
      })
      .join('');
    if (focused) this.focusRow(this.focusKey);
  }

  focusRow(key) {
    const index = this.rows.findIndex(r => r.id === key);
    if (index < 0) return;
    this.focusKey = key;
    const old = this.tree.querySelector('[tabindex="0"]');
    if (old) old.tabIndex = -1;
    const el = this.tree.children[index];
    el.tabIndex = 0;
    el.focus({ preventScroll: true });
    // Scroll within Contents, not the entire workspace page.
    const box = el.getBoundingClientRect(),
      tree = this.tree.getBoundingClientRect();
    if (box.top < tree.top) this.tree.scrollTop += box.top - tree.top;
    else if (box.bottom > tree.bottom) this.tree.scrollTop += box.bottom - tree.bottom;
  }

  click(event) {
    if (event.target.closest('[data-files-back]')) {
      this.showPreview = false;
      this.host.classList.remove('wf-reading');
      this.tree.scrollTop = this.previewScroll || 0;
      this.focusRow(this.selected?.id || this.focusKey);
      return;
    }
    if (event.target.closest('[data-open-linked]')) {
      void this.openSelected();
      return;
    }
    if (event.target.closest('[data-refresh-links]') && this.selected) {
      this.refreshLinks(this.selected.workspaceId);
      this.focusRow(this.focusKey);
      return;
    }
    const target = event.target.closest('[data-row]');
    if (target && this.tree.contains(target)) {
      const row = this.rows[Number(target.dataset.row)];
      this.focusKey = row.id;
      this.activate(row);
    } else if (event.target.closest('[data-retry-preview]') && this.selected)
      this.select(this.selected);
  }

  keydown(event) {
    const target = event.target.closest('[data-row]');
    if (!target) return;
    const i = Number(target.dataset.row),
      row = this.rows[i];
    switch (event.key) {
      case 'ArrowDown':
        this.focusRow(this.rows[i + 1]?.id);
        break;
      case 'ArrowUp':
        this.focusRow(this.rows[i - 1]?.id);
        break;
      case 'Home':
        this.focusRow(this.rows[0].id);
        break;
      case 'End':
        this.focusRow(this.rows.at(-1).id);
        break;
      case 'ArrowRight':
        if (target.getAttribute('aria-expanded') === 'false') this.activate(row);
        else if (this.rows[i + 1]?.parent === row.id) this.focusRow(this.rows[i + 1].id);
        break;
      case 'ArrowLeft':
        if (this.expanded.has(row.id)) this.activate(row);
        else this.focusRow(row.parent);
        break;
      case ' ':
      case 'Enter':
        this.activate(row);
        break;
      default:
        return;
    }
    event.preventDefault();
  }

  activate(row) {
    this.saved = null;
    if (row.kind === 'file' || row.kind === 'note' || row.kind === 'linkedFile') {
      void this.select(row);
      return;
    }
    if (row.kind === 'retry') {
      if (row.parent === this.linkedKey) void this.loadMembers();
      else void this.load(this.rows.find(r => r.id === row.parent));
      return;
    }
    if (row.kind === 'status') return;
    if (this.expanded.has(row.id)) {
      // Never reuse a cached link or its children on a new disclosure.
      if (loadable(row)) {
        const descendants = new Set([row.id]);
        for (const visible of this.rows) {
          if (descendants.has(visible.parent)) descendants.add(visible.id);
        }
        for (const key of descendants) this.sections.delete(key);
      }
      this.expanded.delete(row.id);
      if (this.selected && !this.visibleRows().some(r => r.id === this.selected.id)) {
        ++this.previewVersion;
        this.selected = this.value = this.error = null;
        this.branchSelection = row.id;
        this.paintPreview();
      }
    } else {
      this.expanded.add(row.id);
    }
    this.paintTree();
    this.syncExpanded();
  }

  stopWatchingLink() {
    this.unwatch?.();
    this.unwatch = this.watchedOwner = null;
  }

  // Share the existing stream for the active owner. A directory update revokes
  // even in-flight listings before the source summary is requested again.
  watchLink(owner) {
    if (this.watchedOwner === owner) return;
    this.stopWatchingLink();
    this.watchedOwner = owner;
    this.unwatch = globalThis.window?.workspaceRealtime?.subscribeToWorkspace(owner, event => {
      const data = event.data?.data || event.data;
      if (event.type === 'workspace.updated' && data?.action?.startsWith('directory_'))
        this.refreshLinks(owner);
    });
  }

  refreshLinks(owner) {
    if (!this.controller) return;
    for (const key of this.sections.keys()) {
      if (
        key === sectionKey(owner, 'files') ||
        key.startsWith(`${owner}/l/`) ||
        key.startsWith(`${owner}/ld/`)
      )
        this.sections.delete(key);
    }
    if (this.selected?.kind === 'linkedFile' && this.selected.workspaceId === owner) {
      ++this.previewVersion;
      this.selected = this.value = this.error = null;
      this.focusKey = sectionKey(owner, 'files');
      this.paintPreview();
    }
    this.paintTree();
    this.syncExpanded();
  }

  async load(row) {
    const pending = {};
    this.sections.set(row.id, pending);
    this.paintTree();
    let result;
    try {
      const options = { fetchImpl: this.scopedFetch() };
      if (linkedBranch(row)) this.watchLink(row.workspaceId);
      result = linkedBranch(row)
        ? await loadLinkedPage(row, options)
        : await loadWorkspaceContents(row.workspaceId, row.section, options);
    } catch (error) {
      result = { error };
    }
    if (this.sections.get(row.id) !== pending) return;
    this.sections.set(row.id, result);
    this.paintTree();
    this.syncExpanded();
  }

  scopedFetch() {
    const { signal } = this.controller;
    return (url, init) => fetch(url, { ...init, signal });
  }

  async select(row, { restore = false } = {}) {
    const version = ++this.previewVersion;
    this.selected = row;
    this.openResult = null;
    if (row.kind === 'linkedFile') this.watchLink(row.workspaceId);
    this.branchSelection = null;
    if (!restore) {
      this.previewScroll = this.tree?.scrollTop || 0;
      this.showPreview = true;
    }
    this.value = this.error = null;
    this.paintTree();
    this.paintPreview();
    if (!restore && globalThis.window?.matchMedia('(max-width: 700px)').matches) {
      this.article.querySelector('h2').focus({ preventScroll: true });
      this.host.scrollIntoView({ behavior: 'instant', block: 'start' });
    }
    try {
      const value = await readPreview(row, { fetchImpl: this.scopedFetch() });
      if (version !== this.previewVersion) return;
      this.value = value;
    } catch (error) {
      if (version !== this.previewVersion) return;
      this.error = error;
    }
    this.paintPreview();
  }

  async openSelected() {
    const row = this.selected;
    const version = this.previewVersion;
    if (
      !this.controller ||
      row?.kind !== 'linkedFile' ||
      !row.meta.selection ||
      this.opening.has(row.id) ||
      this.openResult?.stale ||
      (row.workspaceId !== this.root.id && !this.members.has(row.workspaceId)) ||
      !this.rows.some(
        current => current.id === row.id && current.meta?.selection === row.meta.selection
      )
    )
      return;
    this.opening.add(row.id);
    this.openResult = null;
    this.paintPreview();
    let result;
    try {
      const response = await this.scopedFetch()(
        `/api/workspaces/${encodeURIComponent(row.workspaceId)}/directories/${encodeURIComponent(row.meta.dirId)}/open`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: row.meta.path, selection: row.meta.selection })
        }
      );
      if (!response.ok) throw await responseError(response);
      result = { message: (await response.json()).message };
    } catch (err) {
      result = {
        message: err.message || 'Open request failed.',
        stale: [404, 409].includes(err.status)
      };
    } finally {
      this.opening.delete(row.id);
    }
    if (version === this.previewVersion) this.openResult = result;
    if (this.controller && this.selected?.id === row.id) this.paintPreview();
  }

  paintPreview() {
    const row = this.selected;
    this.host.classList.toggle('wf-reading', !!row && !!this.showPreview);
    const owner =
      row?.workspaceId === this.root.id ? this.root.name : this.members.get(row?.workspaceId)?.name;
    this.article.querySelector('.wf-crumb').textContent = row
      ? [
          owner,
          row.kind === 'note' ? 'Notes' : 'Files',
          row.meta.dirName,
          row.meta.path || row.label
        ]
          .filter(Boolean)
          .join(' / ')
      : '';
    this.article.querySelector('h2').textContent = row?.label || 'Select a file or note';
    this.article.querySelector('.wf-body').innerHTML = !row
      ? '<p>Open a section in Contents to start reading.</p>'
      : this.error
        ? `<p>${esc(this.error.message)}</p><button type="button" data-retry-preview>Retry preview</button>`
        : this.value
          ? previewHTML(this.value, row.label)
          : '<p role="status">Loading preview…</p>';
    if (row?.kind === 'linkedFile' && row.meta.selection) {
      const pending = this.opening.has(row.id);
      this.article
        .querySelector('.wf-body')
        .insertAdjacentHTML(
          'beforeend',
          `<button type="button" class="wf-action" data-open-linked ${pending || this.openResult?.stale ? 'disabled' : ''}>Open in default app</button><p role="status">${esc(pending ? 'Requesting open…' : this.openResult?.message || '')}</p>${this.openResult?.stale ? '<button type="button" class="wf-action" data-refresh-links>Refresh Files</button>' : ''}`
        );
    }
  }
}
