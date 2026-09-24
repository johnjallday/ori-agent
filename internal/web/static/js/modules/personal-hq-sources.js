const STATUS = new Set(['available', 'healthy_empty', 'not_configured', 'unavailable', 'revoked']);

export function safeDossierRoute(route) {
  if (typeof route !== 'string' || !route.startsWith('/') || route.startsWith('//')) return false;
  try {
    const decoded = decodeURIComponent(route);
    return (
      !decoded.startsWith('//') &&
      !decoded.includes('\\') &&
      !Array.from(decoded).some(character => character.charCodeAt(0) < 32)
    );
  } catch {
    return false;
  }
}

export function sourceCardSummary(kind, source) {
  const status = STATUS.has(source?.status) ? source.status : 'unavailable';
  if (kind === 'saved_apps') {
    if (
      status === 'available' &&
      source?.observed_at &&
      !Number.isNaN(Date.parse(source.observed_at))
    ) {
      const date = new Date(source.observed_at).toLocaleDateString(undefined, {
        year: 'numeric',
        month: 'short',
        day: 'numeric'
      });
      return `Saved app observation · ${date}. Suggestions require your review. This is not a new scan.`;
    }
    if (status === 'not_configured')
      return 'No durable app observation is saved. Ori will not scan for one here; tell your assistant directly instead.';
    return 'Saved app evidence is unavailable. No recent scan or review status is assumed.';
  }
  if (kind === 'file_janitor') {
    if (status === 'available')
      return 'Eligible approved filing decisions exist. Ori can propose a narrow filing hypothesis for your review.';
    if (status === 'healthy_empty')
      return 'Approved folder access is ready; no currently supported three-move pattern. No suggestion is inferred.';
    if (status === 'not_configured')
      return 'No eligible built-in File Janitor is selected in Personal HQ’s Daily Brief scope.';
    if (status === 'revoked')
      return 'File Janitor folder read access is missing or revoked. Review its setup before checking for suggestions.';
    return 'File Janitor evidence is unavailable or its ownership, scope or folder access could not be verified.';
  }
  if (status === 'available' || status === 'healthy_empty')
    return 'Connected for existing assistant workflows. Reviewed memory suggestions from this source are not enabled here.';
  if (status === 'revoked')
    return 'Connection was revoked. Ori cannot read it; repair it in its own settings if you want to use existing workflows.';
  if (status === 'not_configured')
    return 'Not connected. Reviewed memory suggestions from this source are not enabled here.';
  return 'Connection state unavailable. No reviewed memory suggestion is inferred from it.';
}

// Email/Calendar capability disclosures describe established workflows, not
// this feature's future (#533/#534) learning producers. Keep server-owned copy
// as text and do not turn a capability card into learning consent.
export function sourceCapabilityDisclosures(kind, card) {
  if (!['email', 'calendar'].includes(kind) || !card) return [];
  const ready = card.status === 'available' || card.status === 'healthy_empty';
  return [
    ['Can read', card.can_read],
    ['Can propose in existing workflows', card.can_propose],
    ['Confirmation', card.requires_confirmation]
  ]
    .filter(([, value]) => typeof value === 'string' && value.trim())
    .map(([label, value]) => ({
      label: ready || label === 'Confirmation' ? label : `After setup · ${label}`,
      text: value.slice(0, 280)
    }));
}

function node(tag, text, className) {
  const element = document.createElement(tag);
  if (text !== undefined) element.textContent = text;
  if (className) element.className = className;
  return element;
}

export function renderDossierSources(container, sources, capabilities) {
  if (!container) return;
  const cards = Array.isArray(capabilities?.cards) ? capabilities.cards : [];
  const byKey = new Map(cards.map(card => [card.key, card]));
  const entries = [
    {
      key: 'saved_apps',
      label: 'Saved apps',
      data: sources?.saved_apps,
      action: 'Tell Ori directly',
      route: '#personalHQInterview'
    },
    {
      key: 'file_janitor',
      label: 'File Janitor',
      data: sources?.file_janitor,
      action: 'Review workspaces and brief scope',
      route: '/'
    },
    { key: 'email', label: 'Email', data: byKey.get('email') },
    { key: 'calendar', label: 'Calendar', data: byKey.get('calendar') }
  ];
  container.replaceChildren();
  for (const entry of entries) {
    const card = node('div', undefined, 'reviewed-dossier-source');
    card.append(node('strong', entry.label), node('p', sourceCardSummary(entry.key, entry.data)));
    const disclosures = sourceCapabilityDisclosures(entry.key, entry.data);
    if (disclosures.length) {
      const details = node('dl', undefined, 'reviewed-dossier-capabilities');
      for (const item of disclosures) {
        details.append(node('dt', item.label), node('dd', item.text));
      }
      card.append(details);
    }
    const status = entry.data?.status;
    if (status === 'not_configured' || status === 'revoked') {
      const route = entry.route || entry.data?.action_route;
      if ((route?.startsWith('#') && entry.key === 'saved_apps') || safeDossierRoute(route)) {
        const link = node('a', entry.action || entry.data?.action_label || 'Review setup');
        link.href = route;
        card.append(link);
      }
    }
    container.append(card);
  }
}
