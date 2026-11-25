// Read-only workspace detail surface. Share Home's qualified keys and read
// adapters, not its editor, menus, tab persistence or cross-workspace index.
import { loadFilePreview, loadNote, loadSection, sectionKey } from './home-tree-sources.js';
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

export async function readPreview(row, options = {}) {
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
  }

  enter(root) {
    const renamed = this.root.name !== root.name;
    this.root = root;
    if (this.controller) {
      if (renamed) this.paintTree();
      return;
    }
    this.controller = new AbortController();
    this.host.innerHTML = `<aside class="wf-index"><h2 class="wf-kicker">Contents</h2><div class="wf-tree" role="tree" aria-label="${esc(root.name)} contents"></div></aside><article class="wf-preview" aria-label="Item preview"><span class="wf-kicker">Preview</span><p class="wf-crumb"></p><h2 tabindex="-1" aria-live="polite" aria-atomic="true"></h2><div class="wf-body"></div></article>`;
    this.tree = this.host.querySelector('[role="tree"]');
    this.article = this.host.querySelector('article');
    const options = { signal: this.controller.signal };
    this.host.addEventListener('click', e => this.click(e), options);
    this.tree.addEventListener('keydown', e => this.keydown(e), options);
    this.paintTree();
    this.paintPreview();
  }

  leave() {
    if (!this.controller) return;
    this.controller.abort();
    this.controller = null;
    this.membershipVersion++;
    this.members.clear();
    this.membershipStatus = null;
    this.sections.clear();
    this.previewVersion++;
    this.selected = this.value = this.error = null;
    this.host.replaceChildren();
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
    for (const key of this.sections.keys()) if (!allowed(key)) this.sections.delete(key);
    for (const key of this.expanded) if (!allowed(key)) this.expanded.delete(key);
    if (this.selected && !allowed(this.selected.id)) {
      ++this.previewVersion;
      this.selected = this.value = this.error = null;
      this.focusKey = this.linkedKey;
    }
    this.paintTree();
    this.paintPreview();
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
      } else if (row.kind === 'section') {
        const state = this.sections.get(row.id);
        if (state?.rows?.length) state.rows.forEach(child);
        else
          child({
            id: `${row.id}/status`,
            kind: state?.error ? 'retry' : 'status',
            label: state?.error
              ? `Could not load ${row.label.toLowerCase()}. Retry`
              : state?.rows
                ? `No ${row.label.toLowerCase()} yet`
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
        const branch = ['workspace', 'group', 'section', 'members', 'folder'].includes(r.kind);
        const expanded = this.expanded.has(r.id);
        return `<div role="treeitem" tabindex="${r.id === this.focusKey ? 0 : -1}" data-row="${i}" data-key="${esc(r.id)}" aria-level="${r.level}" aria-selected="${this.selected?.id === r.id}" ${branch ? `aria-expanded="${expanded}"` : ''} style="--wf-level:${r.level}">${branch ? iconHTML(expanded ? 'caretDown' : 'caretRight', { size: 12 }) : '<span class="wf-spacer"></span>'}${iconHTML(r.kind === 'members' ? 'linkedFolders' : rowIconName(r))}<span>${esc(r.label)}</span></div>`;
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
    el.scrollIntoView({ block: 'nearest' });
  }

  click(event) {
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
    if (row.kind === 'file' || row.kind === 'note') {
      void this.select(row);
      return;
    }
    if (row.kind === 'retry') {
      if (row.parent === this.linkedKey) void this.loadMembers();
      else void this.load(this.rows.find(r => r.id === row.parent));
      return;
    }
    if (row.kind === 'status') return;
    if (this.expanded.has(row.id)) this.expanded.delete(row.id);
    else {
      this.expanded.add(row.id);
      if (row.kind === 'members' && !this.membershipStatus) void this.loadMembers();
      if (row.kind === 'section' && !this.sections.has(row.id)) void this.load(row);
    }
    this.paintTree();
  }

  async load(row) {
    const pending = {};
    this.sections.set(row.id, pending);
    this.paintTree();
    let result;
    try {
      result = await loadSection(row.workspaceId, row.section, {
        limit: Infinity,
        fetchImpl: this.scopedFetch()
      });
    } catch (error) {
      result = { error };
    }
    if (this.sections.get(row.id) !== pending) return;
    this.sections.set(row.id, result);
    this.paintTree();
  }

  scopedFetch() {
    const { signal } = this.controller;
    return (url, init) => fetch(url, { ...init, signal });
  }

  async select(row) {
    const version = ++this.previewVersion;
    this.selected = row;
    this.value = this.error = null;
    this.paintTree();
    this.paintPreview();
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

  paintPreview() {
    const row = this.selected;
    const owner =
      row?.workspaceId === this.root.id ? this.root.name : this.members.get(row?.workspaceId)?.name;
    this.article.querySelector('.wf-crumb').textContent = row
      ? [owner, row.kind === 'note' ? 'Notes' : 'Files', row.meta.path || row.label].join(' / ')
      : '';
    this.article.querySelector('h2').textContent = row?.label || 'Select a file or note';
    this.article.querySelector('.wf-body').innerHTML = !row
      ? '<p>Open a section in Contents to start reading.</p>'
      : this.error
        ? `<p>${esc(this.error.message)}</p><button type="button" data-retry-preview>Retry preview</button>`
        : this.value
          ? previewHTML(this.value, row.label)
          : '<p role="status">Loading preview…</p>';
  }
}
