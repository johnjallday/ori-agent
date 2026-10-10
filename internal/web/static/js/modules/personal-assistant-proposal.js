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
  unknown: 'Type, placement and effects are rechecked in the setup review.'
};

export function proposalView(option = {}, project = {}) {
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
  card.append(image, text);
  return card;
}
