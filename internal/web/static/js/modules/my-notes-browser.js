import { notesRequest } from './notes-api.js';
import { isCreatedByOri } from './notes-station.js';
import { previewParagraphs, secondBrainPreviewPath } from './personal-assistant-second-brain.js';

export function initialNotesFolder(view) {
  return isCreatedByOri(view) ? ['Inbox'] : [];
}

export function notesDirectoryQuery(ref, parts) {
  const params = new URLSearchParams({ ref });
  for (const part of parts) params.append('part', part);
  return '/browse?' + params;
}

export function notebookNoteActions(view, parts, name) {
  let preview = '';
  if (isCreatedByOri(view)) {
    const [first, project] = parts;
    if (parts.length === 1 && ['Inbox', 'Daily'].includes(first) && !/^readme\.md$/i.test(name)) {
      preview = secondBrainPreviewPath(first.toLowerCase(), name);
    } else if (parts.length === 2 && first === 'Projects' && name === 'README.md') {
      preview = secondBrainPreviewPath('project', project);
    }
    if (preview)
      preview =
        preview.slice(preview.indexOf('/preview/')) +
        '?' +
        new URLSearchParams({ ref: view.browse_ref });
  }
  // Use the existing vault-relative Obsidian handoff, never a fabricated link
  // for a plain Markdown folder (including an Ori notebook without Obsidian).
  const external =
    view?.source?.kind === 'obsidian'
      ? 'obsidian://open?vault=' +
        encodeURIComponent(view.source.name) +
        '&file=' +
        encodeURIComponent([...parts, name.replace(/\.(?:md|markdown)$/i, '')].join('/'))
      : '';
  return { preview, external };
}

function node(tag, text, className = '') {
  const el = document.createElement(tag);
  el.textContent = text;
  if (className) el.className = className;
  return el;
}

function control(text, onClick) {
  const el = node('button', text, 'modern-btn modern-btn-secondary modern-btn-sm');
  el.type = 'button';
  el.addEventListener('click', onClick);
  return el;
}

export class MyNotesBrowser {
  constructor(root, { openFolder }) {
    this.root = root;
    this.openFolder = openFolder;
  }

  clear() {
    this.listRequest?.abort();
    this.previewRequest?.abort();
    this.view = null;
    this.root.hidden = true;
    this.root.replaceChildren();
  }

  start(view) {
    this.view = view;
    return this.go(initialNotesFolder(view));
  }

  refreshInbox() {
    if (this.view && this.parts.length === 1 && this.parts[0] === 'Inbox') {
      void this.go(this.parts, { focus: false });
    }
  }

  async go(parts, { focus = true } = {}) {
    if (!this.view?.browse_ref) return;
    this.listRequest?.abort();
    this.previewRequest?.abort();
    const request = new AbortController();
    this.listRequest = request;
    this.parts = parts;
    this.root.hidden = false;
    this.render(parts, { loading: true });
    if (focus) this.focusHeading();
    let state;
    try {
      const { directory } = await notesRequest(notesDirectoryQuery(this.view.browse_ref, parts), {
        signal: request.signal
      });
      if (!Array.isArray(directory?.entries)) throw new Error();
      state = { directory };
    } catch (error) {
      state = {
        error:
          error.status === 404
            ? 'This folder may have been moved or removed. Go up to keep browsing.'
            : error.status === 409
              ? 'This notes connection changed. Refresh My Notes before browsing again.'
              : 'Ori could not list this folder. It may be unavailable or unreadable.'
      };
    }
    if (request.signal.aborted) return;
    const keepHeading = document.activeElement === this.heading;
    this.render(parts, state);
    if (keepHeading) this.focusHeading(false);
  }

  focusHeading(scroll = true) {
    this.heading.focus({ preventScroll: !scroll });
  }

  render(parts, { directory, error, loading }) {
    const breadcrumbs = node('nav', '', 'my-notes-crumbs');
    breadcrumbs.setAttribute('aria-label', 'Notebook folders');
    const rootName = this.view.source.name || 'Notebook';
    for (const [index, name] of [rootName, ...parts].entries()) {
      if (index) {
        const divider = node('span', '/');
        divider.setAttribute('aria-hidden', 'true');
        breadcrumbs.append(divider);
      }
      if (index === parts.length) {
        const current = node('span', name);
        current.setAttribute('aria-current', 'location');
        breadcrumbs.append(current);
      } else breadcrumbs.append(control(name, () => void this.go(parts.slice(0, index))));
    }
    this.heading = node('h2', parts.at(-1) || 'Notebook folders');
    this.heading.tabIndex = -1;
    const content = node('div', '');
    if (loading || error) {
      const line = node('p', error || 'Loading names…');
      line.setAttribute('role', error ? 'alert' : 'status');
      content.append(line);
      if (error) content.append(control('Try this folder again', () => void this.go(parts)));
    } else {
      if (directory.partial) {
        const line = node(
          'p',
          'Only some names could be listed. Open the notebook folder for the full contents.'
        );
        line.setAttribute('role', 'status');
        content.append(line);
      }
      if (directory.entries.length === 0 && !directory.partial)
        content.append(node('p', 'No folders or Markdown notes here.'));
      if (directory.entries.length > 0) {
        const list = node('ul', '', 'my-notes-filenames');
        for (const entry of directory.entries) {
          const row = node('li', '', 'my-notes-filename');
          if (entry.folder) {
            const link = control(entry.name, () => void this.go([...parts, entry.name]));
            link.classList.add('my-notes-child-folder');
            link.setAttribute('aria-label', entry.name);
            row.append(link);
          } else {
            row.append(node('span', entry.name));
            const actions = notebookNoteActions(this.view, parts, entry.name);
            const controls = node('div', '', 'my-notes-file-actions');
            if (actions.preview) {
              const preview = control(
                'Check Ori preview',
                () => void this.preview(row, actions.preview, preview)
              );
              preview.setAttribute('aria-label', `Check Ori preview for ${entry.name}`);
              preview.setAttribute('aria-expanded', 'false');
              controls.append(preview);
            }
            if (actions.external) {
              const link = node(
                'a',
                'Open in Obsidian',
                'modern-btn modern-btn-secondary modern-btn-sm'
              );
              link.href = actions.external;
              link.setAttribute('aria-label', `Open ${entry.name} in Obsidian`);
              controls.append(link);
            }
            if (!actions.external && !actions.preview)
              controls.append(control('Open notebook folder', () => void this.openFolder()));
            row.append(controls);
          }
          list.append(row);
        }
        content.append(list);
      }
    }
    this.root.replaceChildren(breadcrumbs, this.heading, content);
  }

  async preview(row, action, button) {
    const wasOpen = button.getAttribute('aria-expanded') === 'true';
    this.previewRequest?.abort();
    this.root.querySelector('[data-folder-preview]')?.remove();
    this.root.querySelector('[aria-expanded="true"]')?.setAttribute('aria-expanded', 'false');
    if (wasOpen) return;
    const request = new AbortController();
    this.previewRequest = request;
    const box = node('div', 'Checking whether Ori can preview this note…', 'my-notes-file-preview');
    box.dataset.folderPreview = '';
    box.setAttribute('role', 'status');
    button.setAttribute('aria-expanded', 'true');
    row.append(box);
    try {
      const { preview } = await notesRequest(action, { signal: request.signal });
      if (request.signal.aborted) return;
      box.replaceChildren(...previewParagraphs(preview.body).map(text => node('p', text)));
      if (preview.truncated) box.append(node('p', 'Only the beginning is shown.'));
    } catch (error) {
      if (request.signal.aborted) return;
      box.textContent = error.message;
      box.append(control('Open notebook folder', () => void this.openFolder()));
    }
  }
}
