import { openFolderInFiles } from './files-arrival.js';

// Search grants are not workspace access. Project only current connections
// from the local owner's Files view; opening this page never requests a grant.
// "Search <folder> in Files" opens the merged Files scoped to that folder.
const mount = document.getElementById('workspaceSearchLocations');
const workspace = document.body.dataset.workspaceId;
async function connected() {
  const response = await fetch('/api/file-search/sources', {
    cache: 'no-store',
    headers: { 'X-Ori-Search': '1' }
  });
  if (!response.ok) return [];
  // An unavailable folder still names its workspace, but has nothing to search.
  return (await response.json()).filter(
    s => s.workspace?.id === workspace && s.state !== 'unavailable'
  );
}
let revision = 0;
async function refresh() {
  const ticket = ++revision;
  const sources = await connected().catch(() => []);
  if (ticket !== revision) return;
  mount.replaceChildren(
    ...sources.map(source => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'btn btn-sm btn-outline-secondary me-2 mb-2';
      button.textContent = `Search ${source.label} in Files`;
      button.onclick = async () => {
        button.disabled = true;
        try {
          if (
            !(await connected()).some(
              s => s.id === source.id && s.workspace.link === source.workspace.link
            )
          )
            throw Error();
          if (!(await openFolderInFiles(source.id))) throw Error();
        } catch {
          mount.textContent =
            'This folder connection is no longer available. Reopen the workspace to check again.';
        }
      };
      return button;
    })
  );
  mount.hidden = !sources.length;
}
if (mount && workspace) {
  void refresh();
  window.addEventListener('pageshow', refresh);
  window.addEventListener('focus', refresh);
  const channel =
    typeof BroadcastChannel === 'function' ? new BroadcastChannel('ori-file-search-v1') : null;
  channel?.addEventListener('message', refresh);
}
