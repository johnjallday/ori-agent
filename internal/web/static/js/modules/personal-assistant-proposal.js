// Read-only presentation of host facts. No prose/type-label parsing, routes,
// access grants, offer allocation or execution belongs here.
const bounded = value => {
  const text = Array.from(String(value || '').trim());
  return text.length > 120 ? `${text.slice(0, 119).join('')}…` : text.join('');
};
const effects = {
  workspace: 'Review one workspace and its placement.',
  home_library:
    'Review a new Home and its collection library. Entries are not promised child workspaces.',
  library: 'Review adding this collection to a Home library. Entries are not new workspaces.',
  supporting_folder:
    'Review linking this folder to the verified existing workspace. No new workspace.',
  structure:
    'Review one group with a workspace for each folder, each linked where it is. Nothing is moved, copied or read.',
  unknown: 'Type, placement and effects are rechecked in the setup review.'
};

// How many member rows a card shows before "Show all".
export const STRUCTURE_ROWS = 6;

// structureOption wraps a bare structure record (the attach response, an offer)
// as the option a proposal is rendered from.
export function structureOption(structure) {
  return {
    structure,
    presentation: { version: 1, state: 'proposed', kind: 'structure', effect: 'group_workspaces' }
  };
}

// A folder that holds several projects: the host's own proposal of one group
// and its member workspaces. Every string is the host's; nothing here is read
// out of prose, and an availability reason only ever becomes a fixed short note.
function structureView(option) {
  const p = option.presentation;
  const s = option.structure;
  if (
    p?.version !== 1 ||
    p.state !== 'proposed' ||
    p.kind !== 'structure' ||
    p.effect !== 'group_workspaces' ||
    s?.version !== 1 ||
    !['home', 'group'].includes(s.group?.type) ||
    !Array.isArray(s.members)
  )
    return null;
  const members = s.members
    .filter(member => member?.included)
    .map(member => ({
      name: bounded(member.name),
      type: bounded(member.type_label),
      note: member.availability ? 'plain for now' : ''
    }));
  const existing = s.group.destination_state === 'existing';
  return {
    kind: 'structure',
    state: 'proposed',
    art: 'district',
    label: 'Proposed workspace group',
    subject: bounded(s.group.name) || 'Selected folder',
    scope: `${members.length} workspace${members.length === 1 ? '' : 's'}`,
    description: effects.structure,
    destination: existing
      ? `Adds to your existing ${bounded(s.group.name)}`
      : s.group.type === 'home'
        ? 'Creates this Home'
        : 'Creates this group',
    action: 'Review group setup',
    members,
    notes: (Array.isArray(s.notes) ? s.notes : []).map(bounded).filter(Boolean),
    omitted: Number.isInteger(s.omitted) && s.omitted > 0 ? s.omitted : 0
  };
}

export function proposalView(option = {}, project = {}) {
  const structure = structureView(option);
  if (structure) return structure;
  const p = option.presentation;
  const allowed = p?.version === 1 && ['proposed', 'unavailable'].includes(p.state);
  const validKind =
    allowed &&
    ((p.kind === 'workspace' && p.effect === 'workspace') ||
      (p.kind === 'group' && ['home_library', 'library'].includes(p.effect)) ||
      (p.kind === 'supporting_folder' &&
        p.effect === 'supporting_folder' &&
        option.operation === 'link_supporting_folder' &&
        option.destination_id));
  const kind = validKind ? p.kind : 'unknown';
  const effect = validKind ? p.effect : 'unknown';
  const unavailable = allowed && p.state === 'unavailable';
  const label = unavailable
    ? 'Setup unavailable'
    : kind === 'workspace'
      ? 'Proposed workspace'
      : kind === 'group'
        ? effect === 'home_library'
          ? 'Proposed workspace group'
          : 'Proposed collection'
        : kind === 'supporting_folder'
          ? 'Supporting folder'
          : 'Optional setup';
  const place =
    allowed && ['existing', 'new', 'standalone'].includes(p.destination_state)
      ? p.destination_state
      : 'unknown';
  const name = place !== 'unknown' ? bounded(p.destination_name) : '';
  return {
    kind,
    state: unavailable ? 'unavailable' : 'proposed',
    art: kind === 'group' ? 'district' : kind === 'supporting_folder' ? 'support' : 'neutral',
    label,
    subject: bounded(project.name) || 'Selected folder',
    scope: project.root ? 'Whole folder' : 'One observed folder',
    description: effects[effect],
    destination:
      place === 'standalone'
        ? 'Standalone workspace'
        : name
          ? `${place === 'existing' ? 'Existing' : 'Proposed new'} destination · ${name}`
          : 'Destination not chosen here',
    action: unavailable
      ? 'Review availability'
      : kind === 'workspace'
        ? 'Review workspace setup'
        : kind === 'group'
          ? 'Review collection setup'
          : kind === 'supporting_folder'
            ? 'Review supporting folder'
            : 'Review setup'
  };
}

// This structured operation comes only from the host's verified placement
// response, not a suggestion, named subject or a discussion checkbox.
export function placementProposalView(option, subject) {
  const support = option.operation === 'link_supporting_folder' && option.destination_id;
  return proposalView(
    {
      ...option,
      presentation: {
        version: 1,
        state: 'proposed',
        kind: support
          ? 'supporting_folder'
          : option.operation === 'create_project_workspace'
            ? 'workspace'
            : 'unknown',
        effect: support
          ? 'supporting_folder'
          : option.operation === 'create_project_workspace'
            ? 'workspace'
            : 'unknown',
        destination_state: 'unknown'
      }
    },
    { name: subject }
  );
}

export function renderProposal(view, doc, art) {
  const card = doc.createElement('section');
  card.className = 'personal-assistant-proposal';
  card.dataset.proposalKind = view.kind;
  card.dataset.proposalState = view.state;
  card.setAttribute('aria-label', view.label);
  const image = doc.createElement('div');
  image.className = 'personal-assistant-proposal__art';
  image.setAttribute('aria-hidden', 'true');
  // Only the shared curated literal SVG library can supply markup. A name,
  // server label, custom blueprint, model SVG/URL or arbitrary art key cannot.
  if (art) image.innerHTML = art.svgForVariant(view.art, { context: 'chat' });
  const text = doc.createElement('div');
  for (const [className, value] of [
    ['label', view.label],
    ['subject', view.subject],
    ['scope', view.scope],
    ['description', view.description],
    ['destination', view.destination]
  ]) {
    const node = doc.createElement('p');
    node.className = `personal-assistant-proposal__${className}`;
    node.textContent = value;
    text.append(node);
  }
  if (view.members?.length) text.append(...renderMembers(view, doc));
  card.append(image, text);
  return card;
}

// The member workspaces of a proposed group: a plain list, name then type, with
// a short note where the workspace starts plain. Rows past STRUCTURE_ROWS stay
// hidden until "Show all" is pressed, which reveals them and removes itself.
function renderMembers(view, doc) {
  const list = doc.createElement('ul');
  list.className = 'personal-assistant-proposal__members';
  const rows = view.members.map((member, index) => {
    const row = doc.createElement('li');
    row.className = 'personal-assistant-proposal__member';
    const name = doc.createElement('span');
    name.className = 'personal-assistant-proposal__member-name';
    name.textContent = member.name;
    const type = doc.createElement('span');
    type.className = 'personal-assistant-proposal__member-type';
    type.textContent = member.note ? `${member.type} · ${member.note}` : member.type;
    row.append(name, type);
    row.hidden = index >= STRUCTURE_ROWS;
    return row;
  });
  list.append(...rows);
  const nodes = [list];
  if (rows.length > STRUCTURE_ROWS) {
    const more = doc.createElement('button');
    more.type = 'button';
    more.className = 'personal-assistant-proposal__more';
    more.textContent = `Show all ${rows.length}`;
    more.addEventListener?.('click', () => {
      for (const row of rows) row.hidden = false;
      more.hidden = true;
    });
    nodes.push(more);
  }
  if (view.omitted) {
    const omitted = doc.createElement('p');
    omitted.className = 'personal-assistant-proposal__note';
    omitted.textContent = `${view.omitted} more folder${view.omitted === 1 ? ' is' : 's are'} not listed and get no workspace here.`;
    nodes.push(omitted);
  }
  for (const text of view.notes || []) {
    const note = doc.createElement('p');
    note.className = 'personal-assistant-proposal__note';
    note.textContent = text;
    nodes.push(note);
  }
  return nodes;
}
