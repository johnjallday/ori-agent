import { createLibraryClient } from './library-setup.js';
import { readFinderSelection } from './music-map-finder.js';
import { workspacePageURL } from './workspace-routes.js';

const connected = (entry, workspace) => ({
  id: entry,
  workspace_id: workspace,
  connection: 'connected'
});
function read(home, row) {
  return readFinderSelection(home, row, createLibraryClient(home));
}
async function profileID() {
  try {
    const res = await fetch('/api/user/profile', {
      headers: { Accept: 'application/json' },
      cache: 'no-store'
    });
    return res.ok ? (await res.json()).profile?.id || '' : '';
  } catch {
    return '';
  }
}
function current(cmd, home, token) {
  return (
    cmd.musicResumeTurn === token &&
    cmd.active &&
    cmd.viewMode === 'map' &&
    cmd.workspaceId() === home &&
    cmd.isMusicHomeMap()
  );
}

function notice(cmd, text) {
  cmd.musicMapNotice = text;
  const node = cmd.container?.querySelector('[data-music-map-notice]');
  if (node) node.textContent = text;
}
function unavailable(cmd, text) {
  cmd.detachmentSongCards?.delete(cmd.detachmentSongCardId);
  window.OriWorkspaceMap?.setSelectedId(cmd.detachmentMapEl, [], '');
  cmd.selectDetachmentMember('', null);
  notice(cmd, text);
  cmd.container?.querySelector('[data-music-find]')?.focus();
}

// Capture only presentation and the two opaque IDs in *this history entry*.
// No metadata, file paths, review tokens or setup intent cross the navigation.
export async function resumeMapSong(cmd, id, entry, action) {
  const home = cmd.workspaceId();
  const token = (cmd.musicResumeTurn = Symbol());
  try {
    const [{ view }, owner] = await Promise.all([read(home, connected(entry, id)), profileID()]);
    if (!current(cmd, home, token) || cmd.detachmentSongCardId !== id) return;
    const route =
      action === 'notes'
        ? workspacePageURL(cmd.workspaceSlug(), ['assistant'], {
            hash: `project-session=${encodeURIComponent(entry)}`
          })
        : view.workspaceRoute;
    if (!route) throw new Error();
    if (owner) {
      window.history.replaceState(
        {
          ...window.history.state,
          musicMap: {
            home,
            owner,
            entry,
            workspace: id,
            query: cmd.musicQuery,
            camera: window.OriWorkspaceMap.getCamera(),
            grouped: !!cmd.musicFolders,
            scroll: cmd.musicFolders?.scrollTop || 0
          }
        },
        ''
      );
      // A Back restored from the browser's page cache doesn't boot the map.
      window.addEventListener(
        'pageshow',
        e => {
          if (e.persisted) void restoreMapSong(cmd);
        },
        { once: true }
      );
    }
    cmd.navigateTo(route);
  } catch {
    if (current(cmd, home, token))
      unavailable(cmd, 'The selected project link could not be verified. Find the project again.');
  }
}

export async function restoreMapSong(cmd) {
  const saved = window.history?.state?.musicMap,
    home = cmd.workspaceId();
  if (!saved || saved.home !== home || !saved.owner || !cmd.isMusicHomeMap()) return;
  const map = window.OriWorkspaceMap;
  if (
    !cmd.active ||
    cmd.viewMode !== 'map' ||
    map?.getScopeGroupId() !== home ||
    map.getLayoutState().status === 'loading'
  )
    return;
  const state = { ...window.history.state };
  delete state.musicMap;
  window.history.replaceState(state, '');
  // A back-forward cached page still contains its old card. Hide it before
  // the profile/link reads, rather than displaying stale notes or actions.
  cmd.detachmentSongRead = Symbol();
  cmd.detachmentSongCardId = '';
  cmd.paintDetachmentSongCard();
  const token = (cmd.musicResumeTurn = Symbol());
  try {
    if ((await profileID()) !== saved.owner || !current(cmd, home, token)) return;
    cmd.musicQuery = {
      text: String(saved.query?.text || '').slice(0, 120),
      stage: String(saved.query?.stage || '').slice(0, 16)
    };
    const row = connected(saved.entry, saved.workspace);
    const { view } = await read(home, row);
    if (!current(cmd, home, token)) return;
    if (saved.grouped && !cmd.musicFolders) await cmd.toggleMusicFolders();
    if (!current(cmd, home, token)) return;
    const focus = cmd.revealMusicProject(row, view);
    if (!focus) throw new Error();
    if (saved.camera) map.restoreCamera(cmd.detachmentMapEl, saved.camera);
    focus();
    if (cmd.musicFolders && Number.isFinite(saved.scroll))
      cmd.musicFolders.scrollTop = saved.scroll;
  } catch {
    if (current(cmd, home, token)) {
      if (saved.camera) map.restoreCamera(cmd.detachmentMapEl, saved.camera);
      unavailable(
        cmd,
        'Your previous selection is unavailable. Your project search is still here.'
      );
    }
  }
}
