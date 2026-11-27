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
  const cards = deckMembers({
    own: view.agentGroups().agents,
    commander: page.workspace?.entry_agent_name,
    group,
    groupName: page.workspace?.name || 'This group',
    songIds: new Set((panel?.songRosterRows || []).map(row => row.workspace_id))
  });
  return { cards, incomplete: !group };
}

function cardHTML(view, card) {
  const portrait = card.places[0].own
    ? view.agentCharacterHTML({ name: card.name }, 'map')
    : view.memberAgentPortraitHTML(card.name, false);
  return `<div class="map-team-card"><span class="map-team-face" aria-hidden="true">${portrait}</span><span><strong>${escapeHtml(card.name)}</strong><small>${escapeHtml(card.where)}</small></span></div>`;
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
  function update() {
    const { cards, incomplete } = teamSnapshot(view);
    tab.textContent = `Team ${cards.length}`;
    if (list.hidden) return;
    const html =
      '<h2>Map team</h2>' +
      cards.map(card => cardHTML(view, card)).join('') +
      (incomplete
        ? '<p>Linked workspace membership is unavailable.</p>'
        : !cards.length
          ? '<p>No other agents work here.</p>'
          : '');
    if (html !== previous) {
      list.innerHTML = html;
      previous = html;
    }
    position();
  }
  function close(focus = false) {
    list.hidden = true;
    tab.setAttribute('aria-expanded', 'false');
    if (focus) tab.focus({ preventScroll: true });
  }
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
