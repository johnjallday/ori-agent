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

// The reading of the songs is under way: reading them, or writing up what it
// learned. Nothing else is asked until it ends.
export function chatReading(learning) {
  return learning?.status === 'running' || learning?.status === 'summarizing';
}

// chatTicketOf is what the ticket card would hold, or null when it would be
// empty: the host's first ticket (started or not) and the actions the host
// allows. Look further alone still makes a card; an absent title never does.
function chatTicketOf(view) {
  const allowed = id => view.actions.some(action => action.id === id);
  const ticket = view.nextTicket;
  const start = allowed('start_next');
  const further = allowed('look_further');
  if (!ticket?.title && !further) return null;
  return {
    title: ticket?.title || '',
    started: ticket?.started === true,
    start: start && ticket?.started !== true,
    further
  };
}

// chatFollowUp decides what the drawer draws under the receipt, from the
// refreshed view alone, as an ordered list of cards. The Commander asks one
// thing at a time:
//
//   reading            the learning card only; the next decision waits for the result
//   question           the song question only (a stopped reading's reason rides in it);
//                      neither the goal nor the ticket is shown while it is pending
//   otherwise          the result or the notice that applies, then the next decision:
//                      the goal when the host offers one, else the ticket, else Look further
//
// A reading that stopped, or an update-only offer, is informational: it never
// blocks an independently available goal or ticket. When one is available the
// notice becomes a collapsed disclosure inside that decision's card (so it stays
// reachable without a second primary prompt); when none is, the notice is the card.
//
// Returns {cards: [{kind, ...}]}; kinds are learning, question, update, goal,
// ticket and further. Never fabricates a dismissal or a read: every card comes
// from a field the host sent.
export function chatFollowUp(view) {
  const offer = view?.songsOffer || null;
  const learning = view?.learning || null;
  if (!view) return { cards: [] };
  if (chatReading(learning)) return { cards: [{ kind: 'learning', learning }] };
  const question = offer && !offer.update ? offer : null;
  if (question) {
    // A reading that stopped before consent was granted is asked again: the
    // decision is the one question, with the reason beside it.
    const stopped = learning?.status === 'stopped' ? learning : null;
    return { cards: [{ kind: 'question', offer: question, stopped }] };
  }
  const ticket = chatTicketOf(view);
  const realTicket = ticket && (ticket.title !== '' || ticket.start);
  let decision = null;
  if (view.goalOffer) decision = { kind: 'goal', offer: view.goalOffer };
  else if (ticket) decision = { kind: ticket.title ? 'ticket' : 'further', ticket };
  const independent = view.goalOffer || realTicket ? decision : null;
  const cards = [];
  // The notice (what stopped, or which plugin to update) or the result.
  let notice = null;
  if (learning?.status === 'stopped') notice = { kind: 'learning', learning };
  else if (offer?.update) notice = { kind: 'update', offer };
  // A reading that ended says what it actually read, whatever follows it.
  if (learning && learning.status !== 'stopped') cards.push({ kind: 'learning', learning });
  if (notice && independent) decision = { ...decision, details: notice };
  else if (notice) cards.push(notice);
  if (decision) cards.push(decision);
  return { cards };
}

// chatPlacementLine is the receipt's one sentence, from where the run actually
// put the folder: a Home it made, or one it already had.
export function chatPlacementLine(view, setup) {
  const workspace = String(view?.workspace ?? '').trim() || 'This folder';
  const home = setup.created ? `your new ${setup.home.name}` : `your ${setup.home.name}`;
  return `${workspace} is set up in ${home}.`;
}
