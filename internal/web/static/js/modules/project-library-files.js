import { ReadEpoch } from './project-library-browser.js';
import { renderMarkdown } from './note-editor.js';

export const FILE_TEXT_LIMIT = 512 * 1024;
export const FILE_IMAGE_LIMIT = 2 * 1024 * 1024;

export function fileFailure(error) {
  return (
    {
      denied:
        'No authorized project folder is available. Discovery metadata is not a file-read grant. Use the explicit workspace or setup options in Overview.',
      unsupported:
        'Preview is not supported for this file or folder reference. Legacy references without a saved folder identity cannot be browsed here; use the existing workspace access options.',
      changed: 'The file, folder or permission changed. Refresh Files before continuing.',
      missing:
        'The project folder or file is missing. Reconnect the drive or review the existing workspace access.',
      oversized:
        'This file is too large to preview (text: 512 KiB; PNG/JPEG: 2 MiB and 16 million pixels).',
      timeout: 'The folder read timed out. Try again.'
    }[error?.code] ||
    error?.message ||
    'The project folder could not be read.'
  );
}

// No unbounded response.text/blob call. Check both the header and the actual
// stream; even a malformed response is never allowed to allocate without a cap.
export async function boundedPreview(response, signal) {
  if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    throw Object.assign(new Error(error.error || 'Preview unavailable'), { code: error.code });
  }
  const media = (response.headers.get('Content-Type') || '').split(';')[0];
  const image = ['image/png', 'image/jpeg'].includes(media);
  if (!image && media !== 'text/plain')
    throw Object.assign(new Error('Unsupported preview type'), { code: 'unsupported' });
  const limit = image ? FILE_IMAGE_LIMIT : FILE_TEXT_LIMIT;
  if (Number(response.headers.get('Content-Length')) > limit)
    throw Object.assign(new Error('Preview too large'), { code: 'oversized' });
  const reader = response.body.getReader();
  const chunks = [];
  let length = 0;
  try {
    while (true) {
      if (signal?.aborted) throw new DOMException('Read canceled', 'AbortError');
      const { done, value } = await reader.read();
      if (done) break;
      length += value.length;
      if (length > limit)
        throw Object.assign(new Error('Preview too large'), { code: 'oversized' });
      chunks.push(value);
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  const bytes = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.length;
  }
  return image
    ? { kind: 'image', blob: new Blob([bytes], { type: media }) }
    : { kind: 'text', text: new TextDecoder('utf-8', { fatal: true }).decode(bytes) };
}

export function safeFileMarkdown(text) {
  const sanitizer = globalThis.window?.DOMPurify;
  if (typeof sanitizer?.sanitize !== 'function') return null; // Inert text fallback, never unsanitized marked output.
  return sanitizer.sanitize(renderMarkdown(text), {
    FORBID_TAGS: [
      'img',
      'iframe',
      'svg',
      'math',
      'video',
      'audio',
      'source',
      'style',
      'link',
      'object',
      'embed'
    ],
    FORBID_ATTR: ['style', 'src', 'srcset', 'href']
  });
}

// One instance belongs to one selected entry and one live Files tab. It knows
// only relative names; the host resolves the independently authorized source.
export class LibraryFiles {
  constructor({ request, preview, changed = () => {}, signal }) {
    this.request = request;
    this.previewRequest = preview;
    this.changed = changed;
    this.folders = new Map();
    this.expanded = new Set(['']);
    this.reads = new Map();
    this.previewRead = new ReadEpoch();
    this.preview = null;
    this.dead = false;
    signal?.addEventListener('abort', () => this.destroy(), { once: true });
    if (signal?.aborted) this.destroy();
  }
  async load(path = '') {
    if (this.dead) return;
    if (!this.reads.has(path)) this.reads.set(path, new ReadEpoch());
    const turn = this.reads.get(path).begin();
    this.folders.set(path, { status: 'loading' });
    this.changed(this);
    try {
      const listing = await this.request(path, turn.signal);
      if (!turn.current() || this.dead) return;
      this.folders.set(path, { status: 'ready', ...listing });
    } catch (error) {
      if (!turn.current() || this.dead) return;
      this.folders.set(path, { status: 'error', error: fileFailure(error) });
      this.previewRead.cancel();
      this.preview = null;
    }
    if (turn.current() && !this.dead) this.changed(this);
  }
  async toggle(path) {
    if (this.dead) return;
    if (this.expanded.has(path)) {
      for (const key of [...this.expanded]) {
        if (key === path || key.startsWith(`${path}/`)) {
          this.expanded.delete(key);
          this.reads.get(key)?.cancel();
          this.reads.delete(key);
          this.folders.delete(key);
        }
      }
      this.changed(this);
      return;
    }
    if (this.expanded.size >= 32) return; // The UI explains this fixed client budget.
    this.expanded.add(path);
    await this.load(path);
  }
  async select(entry) {
    if (this.dead || entry.kind !== 'file') return;
    const turn = this.previewRead.begin();
    this.preview = { status: 'loading', path: entry.path, name: entry.name };
    this.changed(this);
    try {
      const value = await this.previewRequest(entry, turn.signal);
      if (!turn.current() || this.dead) return;
      this.preview = { status: 'ready', path: entry.path, name: entry.name, ...value };
    } catch (error) {
      if (!turn.current() || this.dead) return;
      this.preview = { status: 'error', name: entry.name, error: fileFailure(error) };
      if (['denied', 'changed', 'missing'].includes(error.code)) {
        this.folders.clear();
        this.expanded.clear();
      }
    }
    if (turn.current() && !this.dead) this.changed(this);
  }
  destroy() {
    this.dead = true;
    this.previewRead.cancel();
    for (const read of this.reads.values()) read.cancel();
    this.folders.clear();
    this.expanded.clear();
    this.preview = null;
  }
}

const element = (tag, text = '') => {
  const item = document.createElement(tag);
  item.textContent = text;
  return item;
};

export function mountLibraryFiles(container, options) {
  let objectURL = '';
  const button = (text, action) => {
    const item = element('button', text);
    item.type = 'button';
    item.addEventListener('click', () => void action());
    return item;
  };
  const state = new LibraryFiles({
    ...options,
    changed: () => {
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
        objectURL = '';
      }
      const focus = container.contains(document.activeElement)
        ? document.activeElement.dataset?.filePath
        : null;
      container.replaceChildren(
        element('p', 'Original project folder · read-only. This is not Ori-managed files.')
      );
      const refresh = button('Refresh Files', () => {
        if (objectURL) {
          URL.revokeObjectURL(objectURL);
          objectURL = '';
        }
        state.destroy();
        mountLibraryFiles(container, options);
      });
      refresh.className = 'modern-btn modern-btn-secondary';
      container.append(refresh);
      const tree = element('ul');
      tree.className = 'project-library-file-tree';
      const render = (path, parent) => {
        const folder = state.folders.get(path);
        if (!folder || folder.status === 'loading') {
          const loading = element('li', 'Loading folder…');
          loading.setAttribute('role', 'status');
          parent.append(loading);
          return;
        }
        if (folder.status === 'error') {
          const error = element('li', folder.error);
          error.setAttribute('role', 'status');
          const retry = button('Retry folder', () => state.load(path));
          retry.className = 'modern-btn modern-btn-secondary';
          const retryRow = element('li');
          retryRow.append(retry);
          parent.append(error, retryRow);
          return;
        }
        if (folder.truncated)
          parent.append(
            element(
              'li',
              `Partial listing: ${folder.entries.length} entries shown. More entries may exist (${String(folder.reason || 'limit').replaceAll('_', ' ')}).`
            )
          );
        if (!folder.entries.length)
          parent.append(
            element(
              'li',
              folder.truncated
                ? 'No displayable entries in this partial listing.'
                : 'This folder is empty.'
            )
          );
        for (const entry of folder.entries || []) {
          const row = element('li');
          if (entry.kind === 'folder') {
            const open = state.expanded.has(entry.path);
            const toggle = button(`${open ? '▾' : '▸'} ${entry.name}`, () =>
              state.toggle(entry.path)
            );
            toggle.dataset.filePath = entry.path;
            toggle.setAttribute('aria-expanded', String(open));
            toggle.setAttribute(
              'aria-label',
              `${open ? 'Collapse' : 'Expand'} folder ${entry.name}`
            );
            toggle.disabled = !open && state.expanded.size >= 32;
            row.append(toggle);
            if (open) {
              const children = element('ul');
              render(entry.path, children);
              row.append(children);
            }
          } else if (entry.kind === 'file') {
            const select = button(entry.name, () => state.select(entry));
            select.dataset.filePath = entry.path;
            row.append(select, element('small', ` ${entry.size || 0} bytes`));
          } else
            row.append(
              element(
                'span',
                `${entry.name} · ${entry.kind === 'link' ? 'Symbolic link — not followed' : 'Unsupported file type'}`
              )
            );
          parent.append(row);
        }
      };
      if (state.folders.size) render('', tree);
      container.append(tree);
      if (state.expanded.size >= 32)
        container.append(element('p', '32 folders expanded. Collapse one to inspect another.'));
      const preview = state.preview;
      if (preview) {
        const pane = element('section');
        pane.className = 'project-library-file-preview';
        pane.setAttribute('aria-label', 'File preview');
        pane.append(element('h4', preview.name));
        if (preview.status !== 'ready') {
          const status = element(
            'p',
            preview.status === 'loading' ? 'Loading bounded preview…' : preview.error
          );
          status.setAttribute('role', 'status');
          pane.append(status);
        } else if (preview.kind === 'image') {
          objectURL = URL.createObjectURL(preview.blob);
          const image = element('img');
          image.src = objectURL;
          image.alt = `Preview of ${preview.name}`;
          pane.append(image);
        } else {
          const markdown = /\.(md|markdown)$/i.test(preview.name)
            ? safeFileMarkdown(preview.text)
            : null;
          if (markdown !== null) {
            const body = element('div');
            body.innerHTML = markdown;
            pane.append(body);
          } else pane.append(element('pre', preview.text || 'This file is empty.'));
        }
        container.append(pane);
      }
      if (focus != null)
        [...container.querySelectorAll('[data-file-path]')]
          .find(item => item.dataset.filePath === focus)
          ?.focus({ preventScroll: true });
    }
  });
  options.signal?.addEventListener(
    'abort',
    () => {
      if (objectURL) URL.revokeObjectURL(objectURL);
      container.replaceChildren();
    },
    { once: true }
  );
  void state.load();
  return state;
}
