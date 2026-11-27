// The launcher owns chat. The scoped map supplies a sibling disclosure only
// while mounted; neither opening nor refreshing it touches the map camera.
import { linkedAgents } from './linked-agents.js';
import { escapeHtml } from './workspace-detail-members.js';

const keyOf = name =>
  String(name || '')
    .trim()
    .toLowerCase();

export function deckMembers({ own, commander, group, groupName, songIds }) {
  const cards = new Map();
  const add = (name, place) => {
    const key = keyOf(name);
    if (!key || key === keyOf(commander)) return;
    if (!cards.has(key)) cards.set(key, { key, name, places: [] });
    cards.get(key).places.push(place);
  };
  own.forEach(({ name }) => add(name, { own: true, name: groupName }));
  linkedAgents(group).forEach(({ name, places }) => places.forEach(place => add(name, place)));
  return [...cards.values()].map(card => ({
    ...card,
    where:
      card.places.length === 1
        ? card.places[0].name
        : `${card.places.length} ${card.places.every(p => songIds.has(p.id)) ? 'songs' : 'workspaces'}`
  }));
}

export function teamSnapshot(view) {
  const page = view.page;
  const panel = page.membersPanel;
  const group = !panel?.readFailed && panel?.group?.id === view.workspaceId() ? panel.group : null;
  const own = view.agentGroups().agents;
  const cards = deckMembers({
    own,
    commander:
      own.find(agent => page.isWorkspaceEntryAgent?.(agent.name))?.name ||
      page.workspace?.entry_agent_name,
    group,
    groupName: page.workspace?.name || 'This group',
    songIds: new Set((panel?.songRosterRows || []).map(row => row.workspace_id))
  });
  return { cards, incomplete: !group };
}

// Use the canonical, workspace-scoped projection, including its stale and
// cross-scope flags. A label alone is never permission to create an agent.
export function deckSlots(view) {
  const roster = view.roleRoster;
  if (!roster || roster.workspace_id !== view.workspaceId()) return [];
  const ws = view.page?.workspace;
  const status = view.groupTemplateStatus;
  const readOnly =
    ws?.read_only ||
    roster.read_only ||
    (status?.kind === 'managed_home' && status.integration?.state !== 'available');
  return (globalThis.window?.WorkspaceRoleRoster?.rowsFrom(roster) || [])
    .filter(row => row.state === 'empty')
    .map(row => ({ row, canHire: !readOnly && !row.readOnly && !row.needsClear }));
}

function slotsHTML(slots) {
  if (!slots.length) return '';
  return (
    '<h3>Open roles</h3>' +
    slots
      .map(
        ({ row, canHire }) =>
          `<div class="map-team-slot"><span>${escapeHtml(row.label)}</span>${canHire ? `<button type="button" data-team-hire="${escapeHtml(row.roleId)}" aria-label="Hire for ${escapeHtml(row.label)}">Hire</button>` : ''}</div>`
      )
      .join('')
  );
}

function destination(card, place) {
  return place.own ? { unitName: card.name } : { id: place.id };
}

function cardHTML(view, card, expanded) {
  const map = globalThis.window?.OriWorkspaceMap;
  const waits = view.currentMemberFolderAgents().waits;
  const waiting = p => keyOf(waits[p.id]?.agent) === card.key;
  const available = p => map?.canRevealTeamPlace(view.detachmentMapEl, destination(card, p));
  const many = card.places.length > 1;
  const key = escapeHtml(card.key);
  const portrait = card.places[0].own
    ? view.agentCharacterHTML({ name: card.name }, 'map')
    : view.memberAgentPortraitHTML(card.name, false);
  const button = `<button type="button" class="map-team-card" data-team-agent="${key}" ${many ? `aria-expanded="${expanded}"` : ''}><span class="map-team-face" aria-hidden="true">${portrait}</span><span><strong>${escapeHtml(card.name)}</strong><small>${escapeHtml(card.where)}</small>${card.places.some(waiting) ? '<small class="map-team-wait">Has a question for you</small>' : ''}${!many && !available(card.places[0]) ? '<small>Cannot show on the map right now</small>' : ''}</span></button>`;
  if (!many || !expanded) return button;
  return (
    button +
    '<ul class="map-team-places">' +
    card.places
      .map(
        p =>
          `<li><button type="button" data-team-agent="${key}" data-team-place="${escapeHtml(p.own ? '' : p.id)}">${escapeHtml(p.name)}${waiting(p) ? ' · Has a question for you' : ''}${available(p) ? '' : ' · Cannot show on the map right now'}</button></li>`
      )
      .join('') +
    '</ul>'
  );
}

export function mountMapTeamDeck(view, dock) {
  const tab = document.createElement('button');
  tab.type = 'button';
  tab.className = 'map-team-tab';
  tab.setAttribute('aria-expanded', 'false');
  tab.setAttribute('aria-controls', 'mapTeamDeck');
  const list = document.createElement('section');
  list.id = 'mapTeamDeck';
  list.className = 'map-team-deck';
  list.setAttribute('aria-label', 'Map team');
  list.hidden = true;
  dock.append(tab, list);
  let previous = '';
  let expanded = '';
  const status = document.createElement('p');
  status.setAttribute('role', 'status');

  // Find clear vertical space on the pill's side. A selected song's card and
  // the map controls remain usable, even when the page is scrolled on a phone.
  const position = () => {
    if (list.hidden) return;
    const base = dock.getBoundingClientRect();
    const top = Math.max(8, document.querySelector('.navbar')?.getBoundingClientRect().bottom || 0);
    const left = base.right - list.offsetWidth;
    let ranges = [[top, base.top - 6]];
    for (const node of view.container.querySelectorAll(
      '.ws-map-control-dock, .ws-cmd-map-quest-dock > :not([hidden])'
    )) {
      const r = node.getBoundingClientRect();
      if (!r.height || left >= r.right + 4 || base.right <= r.left - 4) continue;
      ranges = ranges
        .flatMap(([a, b]) => [
          [a, Math.min(b, r.top - 6)],
          [Math.max(a, r.bottom + 6), b]
        ])
        .filter(([a, b]) => b > a);
    }
    const [a, b] = ranges.sort(
      (x, y) => Math.min(380, y[1] - y[0]) - Math.min(380, x[1] - x[0]) || y[1] - x[1]
    )[0] || [top, base.top];
    list.style.bottom = `${base.bottom - b}px`;
    const height = view.detachmentMapEl?.clientHeight || 380;
    list.style.maxHeight = `${Math.min(height, b - a, 380)}px`;
  };
  let memberDock;
  function update() {
    const nextDock = view.container.querySelector('.ws-cmd-map-quest-dock');
    if (memberDock !== nextDock) {
      if (memberDock) observer.unobserve(memberDock);
      memberDock = nextDock;
      if (memberDock) observer.observe(memberDock);
    }
    const { cards, incomplete } = teamSnapshot(view);
    tab.textContent = `Team ${cards.length}`;
    if (list.hidden) return;
    const html =
      '<h2>Map team</h2>' +
      cards.map(card => cardHTML(view, card, card.key === expanded)).join('') +
      (incomplete
        ? '<p>Linked workspace membership is unavailable.</p>'
        : !cards.length
          ? '<p>No other agents work here.</p>'
          : '') +
      slotsHTML(deckSlots(view));
    if (html !== previous) {
      const focus = list.contains(document.activeElement) ? document.activeElement.dataset : null;
      list.innerHTML = html;
      list.append(status);
      previous = html;
      if (focus) {
        const next = [...list.querySelectorAll('button')].find(
          el =>
            el.dataset.teamAgent === focus.teamAgent &&
            el.dataset.teamPlace === focus.teamPlace &&
            el.dataset.teamHire === focus.teamHire
        );
        (next || tab).focus({ preventScroll: true });
      }
    }
    position();
  }
  function close(focus = false) {
    list.hidden = true;
    expanded = '';
    status.textContent = '';
    tab.setAttribute('aria-expanded', 'false');
    if (focus) tab.focus({ preventScroll: true });
  }
  list.addEventListener('click', event => {
    const hire = event.target.closest('button[data-team-hire]');
    if (hire) {
      const slot = deckSlots(view).find(s => s.row.roleId === hire.dataset.teamHire);
      if (slot?.canHire) {
        close(true);
        view.openRoleCreateModal(slot.row.roleId, slot.row);
      } else {
        update();
        status.textContent = 'This role is no longer available to hire.';
      }
      return;
    }
    const el = event.target.closest('button[data-team-agent]');
    if (!el) return;
    // Re-resolve by identity, never by a stale array offset.
    const { cards } = teamSnapshot(view);
    const card = cards.find(c => c.key === el.dataset.teamAgent);
    const requested = el.dataset.teamPlace;
    if (card && requested === undefined && card.places.length > 1) {
      expanded = expanded === card.key ? '' : card.key;
      status.textContent = '';
      update();
      return;
    }
    const place =
      requested === undefined
        ? card?.places[0]
        : card?.places.find(p => (p.own ? '' : p.id) === requested);
    if (
      !place ||
      !window.OriWorkspaceMap?.revealTeamPlace(view.detachmentMapEl, destination(card, place))
    ) {
      update();
      status.textContent = 'Cannot show this place on the map right now.';
      return;
    }
    close(true);
    view.detachmentMapEl.scrollIntoView({ block: 'center', behavior: 'instant' });
    if (place.own) {
      view.selectedAgentKey = view.normalizeAgentKey(card.name);
      view.agentSelectionInitialized = true;
    }
    const state = view.currentMemberFolderAgents();
    if (keyOf(state.waits[place.id]?.agent) === card.key) {
      view.memberCommanderSet().show({ workspaceId: place.id, count: 1, fromDeck: true }, state);
    }
  });
  const click = () => {
    if (!list.hidden) return close(true);
    list.hidden = false;
    tab.setAttribute('aria-expanded', 'true');
    update();
  };
  const key = event => {
    if (event.key !== 'Escape' || list.hidden) return;
    event.stopPropagation();
    event.preventDefault();
    close(true);
  };
  const outside = event => {
    if (!list.hidden && !list.contains(event.target) && tab !== event.target) close();
  };
  tab.addEventListener('click', click);
  document.addEventListener('keydown', key, true);
  document.addEventListener('click', outside, true);
  document.addEventListener('scroll', position, true);
  window.addEventListener('resize', position);
  // Includes programmatic drawer opening and late launcher identity changes.
  const observer = new ResizeObserver(() => {
    if (!dock.offsetWidth) close();
    else position();
  });
  observer.observe(dock);
  return {
    update,
    destroy() {
      observer.disconnect();
      document.removeEventListener('keydown', key, true);
      document.removeEventListener('click', outside, true);
      document.removeEventListener('scroll', position, true);
      window.removeEventListener('resize', position);
      tab.remove();
      list.remove();
    }
  };
}
