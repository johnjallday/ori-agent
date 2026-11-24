import { destinationPlace } from './destination-place.js';

// Search grants are not workspace access. Project only current connections
// from the local owner's Files view; opening this page never requests a grant.
const mount = document.getElementById('workspaceSearchLocations');
const workspace = document.body.dataset.workspaceId;
async function connected() {
  const response = await fetch('/api/file-search/sources', {
    cache: 'no-store',
    headers: { 'X-Ori-Search': '1' }
  });
  if (!response.ok) return [];
  return (await response.json()).filter(s => s.workspace?.id === workspace);
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
          const response = await destinationPlace('desk')?.save({
            source: source.id,
            term: '',
            selected: '',
            generation: 0,
            pages: 1
          });
          if (!response?.ok) throw Error();
          window.location.assign('/search');
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
