export function decodePathSegment(segment) {
  const value = String(segment || '');
  try {
    return decodeURIComponent(value);
  } catch (_) {
    return value;
  }
}

export function readWorkspaceNotesRoute(pathname) {
  const path = String(pathname || '').split(/[?#]/)[0];
  const parts = path.split('/').filter(Boolean);
  const idx = parts.indexOf('workspaces');
  if (idx < 0 || idx + 2 >= parts.length || parts[idx + 2] !== 'notes') {
    return { workspaceSlug: '', noteId: '' };
  }
  return {
    workspaceSlug: decodePathSegment(parts[idx + 1]),
    noteId: idx + 3 < parts.length ? decodePathSegment(parts[idx + 3]) : ''
  };
}

export function readFocusedNoteRoute(pathname) {
  const path = String(pathname || '').split(/[?#]/)[0];
  const parts = path.split('/').filter(Boolean);
  if (parts[0] !== 'notes' || parts.length < 2) return { noteId: '' };
  return { noteId: decodePathSegment(parts[1]) };
}

export function notePath(noteId, hash = '') {
  const id = String(noteId || '').trim();
  const path = id ? `/notes/${encodeURIComponent(id)}` : '/workspaces';
  return appendHash(path, hash);
}

export function workspaceNotesPath(workspaceSlug, hash = '') {
  const slug = String(workspaceSlug || '').trim();
  const path = slug ? `/workspaces/${encodeURIComponent(slug)}/notes` : '/workspaces';
  return appendHash(path, hash);
}

export function workspaceNotePath(workspaceSlug, noteId, hash = '') {
  if (!String(workspaceSlug || '').trim()) return workspaceNotesPath('', hash);
  const id = String(noteId || '').trim();
  if (!id) return workspaceNotesPath(workspaceSlug, hash);
  return appendHash(`${workspaceNotesPath(workspaceSlug)}/${encodeURIComponent(id)}`, hash);
}

export function workspaceNotePathForNote(note, hash = '') {
  const workspaceSlug = note?.workspace_slug || note?.folder_slug || '';
  const noteId = note?.id || '';
  if (!workspaceSlug || !noteId) return '';
  return workspaceNotePath(workspaceSlug, noteId, hash);
}

export function notePathForNote(note, hash = '') {
  const noteId = note?.id || '';
  if (!noteId) return '';
  return notePath(noteId, hash);
}

export function appendHash(path, hash = '') {
  if (!hash) return path;
  const value = String(hash);
  if (!value || value === '#') return path;
  if (value.startsWith('#')) return `${path}${value}`;
  return `${path}#${encodeURIComponent(value)}`;
}

if (typeof window !== 'undefined') {
  window.NoteRoutes = {
    decodePathSegment,
    readWorkspaceNotesRoute,
    readFocusedNoteRoute,
    notePath,
    workspaceNotesPath,
    workspaceNotePath,
    workspaceNotePathForNote,
    notePathForNote,
    appendHash
  };
}
