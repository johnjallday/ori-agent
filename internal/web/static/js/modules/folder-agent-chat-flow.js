// folder-agent-chat-flow.js — which cards the Commander's drawer draws under a
// folder agent's report once a music folder is set up. Pure functions over the
// normalized report view (folderAgentReportView); nothing here touches the DOM
// or the network, so the choice of presentation can be tested on its own.
//
// The drawer shows a short setup receipt and, below it, one follow-up at a time.
// Every other report (a look, a plan, a running or stopped setup, a folder that
// was not placed in a Home) keeps today's single card.

// An app route the chat may link: the app's own workspace pages only. The
// receipt's Home and the destination come from the host, but a link is only
// ever drawn when it is one of these.
export function chatAppRoute(route) {
  const value = String(route ?? '');
  if (!value.startsWith('/workspaces/')) return '';
  if (value.includes('//') || value.includes('\\')) return '';
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(value)) return '';
  return value;
}

// chatSetupOf recognises a completed music setup from structured facts and
// returns where the folder was placed, or null when the view cannot establish
// it. A completed run that placed the folder in a Home is the evidence: the
// receipt's Home (only the project-library Home step writes one) on a run that
// ended `done`. These facts stay true after the owner answers "Not now", so the
// recognition never depends on a pending song offer, nor on any display copy
// (folder, agent or workspace names).
export function chatSetupOf(view) {
  if (!view || view.state !== 'set_up') return null;
  const setup = view.setup;
  if (!setup || setup.status !== 'done') return null;
  const home = setup.receipt?.home;
  const name = String(home?.name ?? '').trim();
  if (!name) return null;
  return {
    home: { name, route: chatAppRoute(home.route) },
    created: setup.receipt.homeCreated === true
  };
}

// chatDestination is the one place the receipt's link opens: the Home's Map
// where the host named a landing (the same place a watched run opens), else the
// folder's own workspace. Returns {route, name} or null when there is no safe
// route to offer.
export function chatDestination(view) {
  const landing = chatAppRoute(view?.landing?.route);
  if (landing) {
    const name = String(view.landing.name ?? '').trim();
    return { route: landing, name: name || String(view.workspace ?? '') };
  }
  const route = chatAppRoute(view?.route);
  return route ? { route, name: String(view?.workspace ?? '') } : null;
}

// chatPlacementLine is the receipt's one sentence, from where the run actually
// put the folder: a Home it made, or one it already had.
export function chatPlacementLine(view, setup) {
  const workspace = String(view?.workspace ?? '').trim() || 'This folder';
  const home = setup.created ? `your new ${setup.home.name}` : `your ${setup.home.name}`;
  return `${workspace} is set up in ${home}.`;
}
