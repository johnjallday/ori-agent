// Workspace detail reader. Browsing never launches an app or changes a file.
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
import { chooseFolderColor, folderColor } from './workspace-folder-colors.js';
import {
  canDrop,
  countItems,
  isFolder,
  reviewItems,
  trashAccessHelp,
  trashable,
  trashLabel
} from './workspace-files-actions.js';
import { openRowMenu, closeRowMenu } from './home-tree-menu.js';
import { escapeHtml as esc, renderMarkdown } from './note-editor.js';

const sections = [
  ['files', 'Files'],
  ['notes', 'Notes']
];

const linkedBranch = row => row.kind === 'linked' || row.kind === 'linkedFolder';
const loadable = row => row.kind === 'section' || linkedBranch(row);

async function linkedRequest(row, action, selection, fetchImpl, destination = {}) {
  const base = `/api/workspaces/${encodeURIComponent(row.workspaceId)}/directories/${encodeURIComponent(row.meta.dirId)}`;
  const response = await fetchImpl(`${base}/${action}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: row.meta.path, selection, ...destination })
  });
  if (!response.ok) throw await responseError(response);
  return response.json();
}

export async function readPreview(row, options = {}) {
  if (row.kind === 'linkedFile')
    return row.meta.previewSelection
      ? linkedRequest(row, 'preview', row.meta.previewSelection, options.fetchImpl || fetch)
      : { kind: 'none' };
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
    this.picked = new Map(); // visible rows and the exact tokens originally selected
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
    this.host.innerHTML = `<aside class="wf-index"><h2 class="wf-kicker">Contents</h2><p class="wf-select-hint" hidden>Drag items to a folder, ⌘/Ctrl-click to select, or Shift-click a range.</p><button class="wf-action" type="button" data-color-picked hidden>Folder color…</button><button class="wf-action" type="button" data-move-picked hidden>Move to…</button><button class="wf-action" type="button" data-trash-picked hidden></button><div class="wf-trash-status" role="status"></div><button class="wf-action" type="button" data-refresh-trash-folder hidden>Refresh folder</button><div class="wf-tree" role="tree" aria-multiselectable="true" aria-label="${esc(root.name)} contents"></div></aside><article class="wf-preview" aria-label="Item preview"><button type="button" class="wf-back" data-files-back>${iconHTML('back')}Back to files</button><span class="wf-kicker">Preview</span><p class="wf-crumb"></p><h2 tabindex="-1" aria-live="polite" aria-atomic="true"></h2><div class="wf-body"></div></article>`;
    this.tree = this.host.querySelector('[role="tree"]');
    this.article = this.host.querySelector('article');
    const options = { signal: this.controller.signal };
    this.host.addEventListener('click', e => this.click(e), options);
    this.tree.addEventListener('keydown', e => this.keydown(e), options);
    this.tree.addEventListener('contextmenu', e => this.contextMenu(e), options);
    for (const type of ['dragstart', 'dragover', 'dragleave', 'drop', 'dragend'])
      this.tree.addEventListener(type, e => this.drag(e), options);
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
    closeRowMenu({ restoreFocus: false });
    this.picked.clear();
    this.pickAnchor = this.trashRefreshParent = this.dragRows = null;
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
    const live = new Map(this.rows.filter(trashable).map(r => [r.id, r.meta.trashSelection]));
    for (const [id, r] of this.picked)
      if (live.get(id) !== r.meta.trashSelection) this.picked.delete(id);
    const bulk = this.host.querySelector('[data-trash-picked]');
    bulk.hidden =
      !this.picked.size ||
      (this.picked.size === 1 && this.selected?.id === this.picked.keys().next().value);
    const picks = [...this.picked.values()];
    bulk.textContent = trashLabel(picks);
    this.host.querySelector('[data-color-picked]').hidden =
      !picks.length || !picks.every(r => r.meta?.colorKey);
    const move = this.host.querySelector('[data-move-picked]');
    move.hidden = bulk.hidden || !picks.every(r => r.meta.moveSelection);
    move.disabled = !!this.trashing;
    bulk.disabled = !!this.trashing;
    this.host.querySelector('.wf-select-hint').hidden = !live.size;
    this.host.querySelector('[data-refresh-trash-folder]').hidden =
      !this.trashRefreshParent ||
      !this.rows.some(r => r.id === this.trashRefreshParent && this.expanded.has(r.id));
    // Disclosure, relinking or a membership update can prune the last pick
    // without a click. Don't leave a stale selection summary in Preview.
    if (!this.selected) this.paintPreview();
    if (!this.rows.some(r => r.id === this.focusKey)) this.focusKey = this.root.id;
    this.tree.innerHTML = this.rows
      .map((r, i) => {
        const branch =
          linkedBranch(r) ||
          ['workspace', 'group', 'section', 'members', 'folder'].includes(r.kind);
        const expanded = this.expanded.has(r.id);
        const color = folderColor(r);
        return `<div role="treeitem" ${color ? `data-folder-color="${color.name}" aria-description="${color.name} folder"` : ''} tabindex="${r.id === this.focusKey ? 0 : -1}" data-row="${i}" ${r.meta?.moveSelection ? 'draggable="true"' : ''} data-key="${esc(r.id)}" aria-level="${r.level}" aria-selected="${this.picked.size ? this.picked.has(r.id) : (this.selected?.id || this.branchSelection) === r.id}" ${branch ? `aria-expanded="${expanded}"` : ''} style="--wf-level:${r.level};${color ? `--wf-folder-color:${color.hex}` : ''}">${branch ? iconHTML(expanded ? 'caretDown' : 'caretRight', { size: 12 }) : '<span class="wf-spacer"></span>'}${iconHTML(r.kind === 'members' ? 'linkedFolders' : rowIconName(r))}<span>${esc(r.label)}</span>${r.kind === 'linked' ? '<small class="wf-source-label">Linked folder</small>' : ''}</div>`;
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
    if (event.target.closest('[data-color-picked]')) {
      this.colorRows([...this.picked.values()]);
      return;
    }
    if (event.target.closest('[data-open-linked]')) {
      void this.openSelected();
      return;
    }
    if (
      event.target.closest(
        '[data-trash-linked], [data-trash-picked], [data-move-linked], [data-move-picked]'
      )
    ) {
      const picks = event.target.closest('[data-trash-picked], [data-move-picked]')
        ? [...this.picked.values()]
        : [this.selected];
      void this.changeItems(
        picks,
        !!event.target.closest('[data-move-linked], [data-move-picked]')
      );
      return;
    }
    if (event.target.closest('[data-refresh-trash-folder]')) {
      const parent = this.rows.find(r => r.id === this.trashRefreshParent);
      this.trashRefreshParent = null;
      this.host.querySelector('.wf-trash-status').textContent = '';
      if (parent && this.expanded.has(parent.id)) {
        if (this.selected?.parent === parent.id) {
          ++this.previewVersion;
          this.selected = this.value = this.error = null;
        }
        void this.load(parent);
        this.focusRow(parent.id);
      }
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
      if (trashable(row) && (event.metaKey || event.ctrlKey || event.shiftKey))
        this.pick(row, event.shiftKey ? 'range' : 'toggle');
      else this.activate(row);
    } else if (event.target.closest('[data-retry-preview]') && this.selected)
      this.select(this.selected);
  }

  pick(row, mode) {
    const sameFolder = [...this.picked.values()].every(r => r.parent === row.parent);
    if (!sameFolder || mode === 'only') this.picked.clear();
    if (mode === 'range' && sameFolder && this.pickAnchor?.parent === row.parent) {
      const files = this.rows.filter(r => r.parent === row.parent && trashable(r));
      const start = files.findIndex(
        r =>
          r.id === this.pickAnchor.id &&
          r.meta.trashSelection === this.pickAnchor.meta.trashSelection
      );
      const end = files.findIndex(r => r.id === row.id);
      this.picked.clear();
      if (start !== -1)
        for (const r of files.slice(Math.min(start, end), Math.max(start, end) + 1))
          this.picked.set(r.id, r);
      else this.pickAnchor = row;
      this.picked.set(row.id, row);
    } else {
      if (mode === 'toggle' && this.picked.has(row.id)) this.picked.delete(row.id);
      else this.picked.set(row.id, row);
      this.pickAnchor = row;
    }
    ++this.previewVersion;
    this.selected = this.value = this.error = this.branchSelection = null;
    this.showPreview = false;
    this.focusKey = row.id;
    this.paintTree();
    this.paintPreview();
    this.focusRow(row.id);
  }

  destination(row) {
    if (!row || !linkedBranch(row)) return null;
    const destinationSelection =
      row.meta.destinationSelection || this.sections.get(row.id)?.destinationSelection;
    return destinationSelection ? { ...row, destinationSelection } : null;
  }

  drag(event) {
    const el = event.target.closest('[data-row]');
    const row = el && this.rows[Number(el.dataset.row)];
    if (event.type === 'dragstart') {
      if (!row?.meta?.moveSelection || this.trashing) return;
      this.dragRows = this.picked.has(row.id) ? [...this.picked.values()] : [row];
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('application/x-ori-tree-selection', 'move');
      return;
    }
    if (!this.dragRows) return; // never interpret external filenames or paths
    this.tree.querySelector('.wf-drop-target')?.classList.remove('wf-drop-target');
    if (event.type === 'dragend') {
      this.dragRows = null;
      return;
    }
    if (!event.dataTransfer?.types.includes('application/x-ori-tree-selection')) return;
    const dest = this.destination(row);
    if (!canDrop(this.dragRows, dest)) return;
    if (event.type === 'dragover') {
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      el.classList.add('wf-drop-target');
    } else if (event.type === 'drop') {
      event.preventDefault();
      const rows = this.dragRows;
      this.dragRows = null;
      void this.changeItems(rows, true, dest);
    }
  }

  colorRows(rows) {
    if (
      !this.controller ||
      !rows.length ||
      !rows.every(
        r =>
          r.meta?.colorKey &&
          this.rows.some(live => live.id === r.id && live.meta?.colorKey === r.meta.colorKey)
      )
    )
      return;
    chooseFolderColor(rows, this.controller.signal, message => {
      this.host.querySelector('.wf-trash-status').textContent = message;
      this.paintTree();
    });
  }

  contextMenu(event, keyboard = false) {
    const el = event.target.closest('[data-row]');
    const row = el && this.rows[Number(el.dataset.row)];
    if ((!trashable(row) && !row?.meta?.colorKey) || this.trashing) return;
    event.preventDefault();
    const box = el.getBoundingClientRect();
    if (trashable(row) && !this.picked.has(row.id)) this.pick(row, 'only');
    else this.focusRow(row.id);
    const rows = trashable(row) ? [...this.picked.values()] : [row];
    openRowMenu({
      items: [
        ...(rows.every(r => r.meta.colorKey) ? [{ action: 'color', label: 'Folder color…' }] : []),
        ...(rows.every(r => r.meta.moveSelection) ? [{ action: 'move', label: 'Move to…' }] : []),
        ...(rows.every(trashable)
          ? [{ action: 'trash', label: trashLabel(rows), danger: true }]
          : [])
      ],
      label: rows.length === 1 ? `Actions for ${row.label}` : `Actions for ${countItems(rows)}`,
      at: keyboard ? { x: box.left + 16, y: box.bottom } : { x: event.clientX, y: event.clientY },
      findOrigin: () => this.tree?.querySelector(`[data-key="${CSS.escape(row.id)}"]`),
      event,
      onChoose: action =>
        action === 'color' ? this.colorRows(rows) : void this.changeItems(rows, action === 'move')
    });
  }

  keydown(event) {
    const target = event.target.closest('[data-row]');
    if (!target) return;
    const i = Number(target.dataset.row),
      row = this.rows[i];
    switch (event.key) {
      case 'ContextMenu':
      case 'F10':
        if (event.key === 'F10' && !event.shiftKey) return;
        this.contextMenu(event, true);
        return;
      case 'Backspace':
      case 'Delete':
        if (!trashable(row) || (this.picked.size && !this.picked.has(row.id))) return;
        if (!event.repeat)
          void this.changeItems(this.picked.size ? [...this.picked.values()] : [row]);
        break;
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
        if (trashable(row) && (event.metaKey || event.ctrlKey || event.shiftKey)) {
          this.pick(row, event.shiftKey ? 'range' : 'toggle');
          break;
        }
      // fall through to ordinary activation
      case 'Enter':
        this.focusKey = row.id;
        this.activate(row);
        break;
      default:
        return;
    }
    event.preventDefault();
  }

  activate(row) {
    if (row.kind === 'status') return;
    this.saved = null;
    this.picked.clear();
    this.pickAnchor = null;
    if (row.kind === 'file' || row.kind === 'note' || row.kind === 'linkedFile') {
      void this.select(row);
      return;
    }
    if (isFolder(row) && trashable(row)) {
      this.picked.set(row.id, row);
      this.pickAnchor = row;
      ++this.previewVersion;
      this.selected = this.value = this.error = null;
      this.showPreview = false;
    }
    if (row.kind === 'retry') {
      if (row.parent === this.linkedKey) void this.loadMembers();
      else void this.load(this.rows.find(r => r.id === row.parent));
      return;
    }
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
    this.picked.clear();
    this.pickAnchor = null;
    if (trashable(row)) {
      this.pickAnchor = row;
      this.picked.set(row.id, row);
    }
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

  async changeItems(rows, move = false, dropped = null) {
    const controller = this.controller;
    const field = move ? 'moveSelection' : 'trashSelection';
    const live = row =>
      controller === this.controller &&
      trashable(row) &&
      !this.opening.has(row.id) &&
      (row.workspaceId === this.root.id || this.members.has(row.workspaceId)) &&
      !!row.meta[field] &&
      this.rows.some(r => r.id === row.id && r.meta?.[field] === row.meta[field]);
    if (!controller || this.trashing || !rows.length) return;
    const status = this.host.querySelector('.wf-trash-status');
    if (!rows.every(r => live(r) && r.parent === rows[0].parent)) {
      status.textContent = 'Selection unavailable. Select the items again.';
      return;
    }
    const destinations = move
      ? this.rows.map(r => this.destination(r)).filter(d => canDrop(rows, d))
      : null;
    if (move && !destinations.length) {
      status.textContent =
        'Expand another folder in this linked tree to choose it as a destination.';
      return;
    }
    this.trashing = true;
    const parent = rows[0].parent;
    let moved = 0;
    try {
      const choice = await reviewItems(rows, controller.signal, destinations, dropped);
      if (!choice) return;
      const target = move
        ? [choice.meta.dirName, choice.meta.path].filter(Boolean).join(' / ')
        : 'Trash';
      this.trashRefreshParent = null;
      for (const row of rows) {
        if (!live(row)) throw new Error('The selection changed. Refresh Files and select again.');
        if (
          move &&
          !this.rows.some(
            r =>
              r.id === choice.id &&
              this.destination(r)?.destinationSelection === choice.destinationSelection
          )
        )
          throw new Error('The destination changed. Select it again.');
        status.textContent = `Moving ${row.label} to ${target}…`;
        await linkedRequest(
          row,
          move ? 'move-item' : isFolder(row) ? 'trash-folder' : 'trash-file',
          row.meta[field],
          this.scopedFetch(),
          move
            ? { destination: choice.meta.path, destination_selection: choice.destinationSelection }
            : {}
        );
        moved++;
        if (controller !== this.controller) return;
        const state = this.sections.get(row.parent);
        if (state?.rows)
          state.rows = state.rows.filter(
            r => r.id !== row.id || r.meta?.trashSelection !== row.meta.trashSelection
          );
        if (
          this.selected?.id === row.id &&
          this.selected.meta.trashSelection === row.meta.trashSelection
        ) {
          ++this.previewVersion;
          this.selected = this.value = this.error = null;
          this.showPreview = false;
        }
        if (isFolder(row)) {
          for (const key of this.expanded)
            if (key === row.id || key.startsWith(`${row.id}/`)) this.expanded.delete(key);
          for (const key of this.sections.keys())
            if (key === row.id || key.startsWith(`${row.id}/`)) this.sections.delete(key);
          if (
            this.selected?.workspaceId === row.workspaceId &&
            this.selected.meta.dirId === row.meta.dirId &&
            this.selected.meta.path?.startsWith(`${row.meta.path}/`)
          ) {
            ++this.previewVersion;
            this.selected = this.value = this.error = null;
            this.showPreview = false;
          }
        }
        if (this.focusKey === row.id) this.focusKey = parent;
        this.paintTree();
      }
      status.textContent = `${rows.length === 1 ? rows[0].label : countItems(rows)} moved to ${target}.`;
    } catch (err) {
      if (controller === this.controller) {
        this.trashRefreshParent = parent;
        status.textContent = `${moved} of ${rows.length} confirmed moved. Stopped at ${rows[moved]?.label || 'file'}: ${err.message || 'Could not confirm the move.'}`;
        const message = status.textContent;
        const accessCode = move ? null : err.code;
        const help = trashAccessHelp(accessCode);
        if (help) status.insertAdjacentHTML('beforeend', help);
        // On a narrow screen Contents is hidden while reading one file.
        if (this.selected && this.selected.id === rows[moved]?.id) {
          this.openResult = { message, accessCode, stale: [404, 409].includes(err.status) };
          this.paintPreview();
        }
      }
    } finally {
      this.trashing = false;
      if (controller === this.controller) {
        if (move && moved) this.refreshLinks(rows[0].workspaceId);
        else this.paintTree();
        if (moved && this.focusKey === parent) this.focusRow(parent);
      }
    }
  }

  async openSelected() {
    const row = this.selected;
    const version = this.previewVersion;
    const selection = row?.meta?.selection;
    if (
      !this.controller ||
      row?.kind !== 'linkedFile' ||
      !selection ||
      this.opening.has(row.id) ||
      this.openResult?.stale ||
      (row.workspaceId !== this.root.id && !this.members.has(row.workspaceId)) ||
      this.trashing ||
      !this.rows.some(current => current.id === row.id && current.meta?.selection === selection)
    )
      return;
    this.opening.add(row.id);
    this.openResult = null;
    this.paintPreview();
    let result;
    try {
      result = await linkedRequest(row, 'open', selection, this.scopedFetch());
    } catch (err) {
      result = {
        message: err.message || 'File action failed.',
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
    this.article.querySelector('h2').textContent =
      row?.label || (this.picked.size ? `${this.picked.size} selected` : 'Select a file or note');
    this.article.querySelector('.wf-body').innerHTML = !row
      ? this.picked.size
        ? '<p>Right-click a selected file for actions.</p>'
        : '<p>Open a section in Contents to start reading.</p>'
      : this.error
        ? `<p>${esc(this.error.message)}</p><button type="button" data-retry-preview>Retry preview</button>`
        : this.value
          ? previewHTML(this.value, row.label)
          : '<p role="status">Loading preview…</p>';
    if (row?.kind === 'linkedFile' && (row.meta.selection || row.meta.trashSelection)) {
      const pending = this.opening.has(row.id);
      const stale = this.openResult?.stale || [404, 409].includes(this.error?.status);
      const disabled = pending || stale ? 'disabled' : '';
      this.article
        .querySelector('.wf-body')
        .insertAdjacentHTML(
          'afterbegin',
          `<div class="wf-actions">${row.meta.moveSelection ? `<button type="button" class="wf-action" data-move-linked ${disabled}>Move to…</button>` : ''}${row.meta.selection ? `<button type="button" class="wf-action" data-open-linked ${disabled}>${esc(row.meta.openLabel || 'Open in default app')}</button>` : ''}${row.meta.trashSelection ? `<button type="button" class="wf-action" data-trash-linked ${disabled}>Move to Trash…</button>` : ''}${stale ? '<button type="button" class="wf-action" data-refresh-links>Refresh Files</button>' : ''}</div><p role="status">${esc(pending ? 'Working…' : this.openResult?.message || '')}</p>${pending ? '' : trashAccessHelp(this.openResult?.accessCode)}`
        );
    }
  }
}
