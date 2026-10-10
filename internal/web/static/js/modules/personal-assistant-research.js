// Host receipts and tab-local exact lookup reviews. Model prose never creates
// links, tokens, setup buttons or authority. Offers are closure-only, not saved.

const KINDS = {
  installed_skill_metadata: 'Installed skill metadata',
  mcp_configuration_metadata: 'MCP configuration metadata',
  builtin_listing: 'Compiled listing',
  cached_listing: 'Cached listing',
  skill_catalog_listing: 'Skill catalog listing',
  public_search_listing: 'Search listing/snippet',
  public_document: 'Public document'
};
const OPERATIONS = new Set([
  'skills_catalog',
  'public_document',
  'mcp_catalog_refresh',
  'web_search'
]);

export function publicResearchLink(value) {
  try {
    const text = String(value || '');
    if (
      text.length > 2000 ||
      !/^https?:\/\//i.test(text) ||
      /[\s\p{Cf}]/u.test(text) ||
      /%(?:0[0-9a-f]|1[0-9a-f]|7f)/i.test(text) ||
      Array.from(text).some(ch => ch.codePointAt(0) < 32)
    )
      return '';
    const url = new URL(text);
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) return '';
    if (url.port && url.port !== (url.protocol === 'https:' ? '443' : '80')) return '';
    const host = url.hostname.toLowerCase();
    if (
      !host.includes('.') ||
      host.includes(':') ||
      /(?:^|\.)(?:localhost|local|internal|lan|home|test|invalid)$/.test(host)
    )
      return '';
    if (/^(?:0|10|127|169\.254|192\.168|172\.(?:1[6-9]|2\d|3[01]))\./.test(host)) return '';
    if (
      /^(?:file|javascript|data):/i.test(text) ||
      url.search.includes(';') ||
      Array.from(url.searchParams.keys()).some(key =>
        /(?:^key$|^sig$|api[_-]?key|token|password|secret|signature|auth|session|x-amz|x-goog)/i.test(
          key
        )
      )
    )
      return '';
    return text;
  } catch (_) {
    return '';
  }
}

export function researchSourcesView(attribution, { historical = false } = {}) {
  return (Array.isArray(attribution?.research) ? attribution.research : [])
    .slice(0, 12)
    .filter(
      ref =>
        ref &&
        /^S[1-9]\d{0,8}$/.test(ref.key) &&
        KINDS[ref.kind] &&
        ['metadata', 'document'].includes(ref.level)
    )
    .map(ref => ({
      key: ref.key,
      name: String(ref.name || 'Source').slice(0, 120),
      href: publicResearchLink(ref.url),
      detail: [
        KINDS[ref.kind],
        historical || attribution?.historical
          ? 'historical observation, not a fresh read'
          : String(ref.freshness || 'unknown freshness').replaceAll('_', ' '),
        ref.truncated ? 'bounded/partial excerpt' : '',
        ref.cited ? 'cited by this reply' : 'read this turn'
      ]
        .filter(Boolean)
        .join(' · ')
    }));
}

export function renderResearchSources(row, attribution, options = {}) {
  if (!row?.ownerDocument) return false;
  const bubble = row.firstElementChild || row;
  bubble.querySelector?.('.personal-assistant-research__sources')?.remove();
  const sources = researchSourcesView(attribution, options);
  if (!sources.length) return false;
  const doc = row.ownerDocument;
  const details = doc.createElement('details');
  details.className = 'personal-assistant-research__sources';
  const summary = doc.createElement('summary');
  summary.textContent = `Research sources (${sources.length})`;
  const list = doc.createElement('ul');
  for (const source of sources) {
    const item = doc.createElement('li');
    const name = doc.createElement(source.href ? 'a' : 'span');
    name.textContent = `[${source.key}] ${source.name}`;
    if (source.href) {
      name.href = source.href;
      name.target = '_blank';
      name.rel = 'noopener noreferrer';
    }
    const qualifier = doc.createElement('small');
    qualifier.textContent = source.detail;
    item.append(name, qualifier);
    list.append(item);
  }
  details.append(summary, list);
  bubble.append(details);
  return true;
}

export function researchReviewView(review, now = Date.now()) {
  if (
    !review ||
    !OPERATIONS.has(review.lookup?.operation) ||
    !/^[A-Za-z0-9_-]{43}$/.test(review.token || '') ||
    !/^[a-f0-9]{64}$/.test(review.digest || '')
  )
    return null;
  const expires = Date.parse(review.expires_at);
  if (!Number.isFinite(expires) || expires <= now) return null;
  const document = review.lookup.operation === 'public_document';
  const registry = review.lookup.operation === 'mcp_catalog_refresh';
  const value =
    document || registry ? String(review.lookup.url || '') : String(review.lookup.query || '');
  if (document || registry ? !publicResearchLink(value) : !value || value.length > 256) return null;
  if (!publicResearchLink(review.destination)) return null;
  return {
    value,
    document,
    registry,
    destination: review.destination,
    redirects: String(review.redirect_policy || ''),
    expires
  };
}

// These are navigation only. The existing owners have no safe exact-candidate
// review handoff, so never preselect a package, credentials, agent or workspace.
// No result ID is dispatched to an installer or encoded in a destination.
export function researchCandidateNavigation(candidate, now = Date.now()) {
  const receipt = candidate?.receipt;
  if (
    !/^[a-f0-9]{32}$/.test(candidate?.id || '') ||
    receipt?.candidate_id !== candidate.id ||
    !/^[a-f0-9]{32}$/.test(receipt.source_id || '') ||
    !['metadata', 'document'].includes(receipt.level) ||
    !['available', 'partial'].includes(receipt.availability) ||
    ['stale', 'cached'].includes(receipt.freshness)
  )
    return null;
  const read = Date.parse(receipt.read_at);
  if (!Number.isFinite(read) || read > now + 30000 || now - read > 300000) return null;
  if (['skill_catalog', 'installed_skill'].includes(candidate.kind))
    return { href: '/skills', label: 'Browse Skills setup' };
  if (['mcp_catalog', 'configured_mcp'].includes(candidate.kind))
    return { href: '/mcp', label: 'Browse MCP configuration' };
  return null;
}

export function researchCapabilityView(capability) {
  if (!capability) return '';
  if (!capability.provider || !capability.model)
    return 'No system conversation model is available. Choose one in Settings; agent profile settings do not select it.';
  const model = `${String(capability.provider).slice(0, 64)} / ${String(capability.model).slice(0, 128)}`;
  const tools = capability.broker_tools
    ? 'brokered metadata tools; public lookups require exact review'
    : 'snapshot-only; automatic catalog tools are unavailable';
  const search =
    capability.broader_search === 'configured_review_required'
      ? 'Broader search requires a separate exact review.'
      : 'Broader search is disabled/unconfigured.';
  return `System conversation model: ${model} — ${tools}. ${search}`;
}

export function renderResearchResponse(row, data, { post, approve, isCurrent = () => false } = {}) {
  if (!row?.ownerDocument) return;
  const bubble = row.firstElementChild || row;
  const doc = row.ownerDocument;
  const capability = data?.research_capability;
  if (capability) {
    const note = doc.createElement('p');
    note.className = 'personal-assistant-research__note personal-assistant-research__capability';
    note.textContent = researchCapabilityView(capability) + ' ';
    if (capability.manual_discovery_href === '/skills') {
      const link = doc.createElement('a');
      link.href = '/skills';
      link.textContent = 'Browse capabilities';
      note.append(link);
    }
    if (!capability.provider || !capability.model) {
      const settings = doc.createElement('a');
      settings.href = '/settings';
      settings.textContent = 'Choose system model';
      note.append(' ', settings);
    }
    bubble.append(note);
  }
  const result = data?.research_result;
  if (result) {
    const note = doc.createElement('p');
    note.className = 'personal-assistant-research__note';
    const state = String(result.availability || 'unavailable').replaceAll('_', ' ');
    note.setAttribute('role', 'status');
    note.textContent = `Research: ${state}${result.reason ? ' · ' + String(result.reason).replaceAll('_', ' ') : ''}. ${result.scope || ''}`;
    bubble.append(note);
    const candidates = Array.isArray(result.candidates)
      ? result.candidates
          .filter(candidate => candidate && typeof candidate === 'object')
          .slice(0, 8)
      : [];
    const list = doc.createElement('ul');
    list.className = 'personal-assistant-research__candidates';
    list.setAttribute('aria-label', 'Candidate evidence and independent readiness');
    const navigationScopes = new Set();
    for (const candidate of candidates) {
      const item = doc.createElement('li');
      item.className = 'personal-assistant-research__candidate';
      const ready = candidate.readiness || {};
      const observation = value =>
        ['observed', 'not_observed'].includes(value) ? value.replaceAll('_', ' ') : 'unknown';
      const values = ['installed', 'configured', 'enabled', 'granted', 'verified'].map(
        key => `${key}: ${observation(ready[key])}`
      );
      item.textContent = `${String(candidate.name || 'Candidate').slice(0, 120)} — ${values.join(' · ')}. Dependencies: ${ready.dependencies?.state === 'declared' ? 'declared, not verified' : 'unknown'}.`;
      const dependencies = ready.dependencies || {};
      const names = [
        ...(Array.isArray(dependencies.mcp_servers) ? dependencies.mcp_servers : []),
        ...(Array.isArray(dependencies.tools) ? dependencies.tools : [])
      ].slice(0, 6);
      if (names.length) {
        const declared = doc.createElement('small');
        declared.textContent =
          'Declared requirements: ' +
          names.map(name => String(name).slice(0, 80)).join(', ') +
          '. A declaration is not operational verification.';
        item.append(declared);
      }
      const navigation = researchCandidateNavigation(candidate);
      if (navigation) {
        const handoff = doc.createElement('p');
        handoff.className = 'personal-assistant-research__handoff';
        const link = doc.createElement('a');
        link.href = navigation.href;
        link.textContent = navigation.label + ' (new tab)';
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        navigationScopes.add(navigation.href);
        handoff.append(link, ' — manual only; no package or access target chosen.');
        item.append(handoff);
      }
      list.append(item);
    }
    if (candidates.length) bubble.append(list);
    if (navigationScopes.size) {
      const boundaries = doc.createElement('details');
      boundaries.className = 'personal-assistant-research__setup';
      const heading = doc.createElement('summary');
      heading.textContent = 'Setup stays separate — no action approved';
      const disclosure = doc.createElement('p');
      const storage = navigationScopes.has('/skills')
        ? ' Skill files would be stored in Workspace Directory / Skills; no workspace or agent receives access here.'
        : ' Connection configuration and credentials are reviewed on the MCP page; no workspace or agent access is selected here.';
      disclosure.textContent =
        'Manual navigation, not an exact-candidate review. Nothing is preselected or confirmed.' +
        storage +
        ' Installation, enablement, script trust, credentials and workspace/agent access remain separate decisions on that page; no target is inferred from this discussion. Keep this conversation and draft here.';
      boundaries.append(heading, disclosure);
      bubble.append(boundaries);
    }
  }
  let review = data?.research_review;
  let view = researchReviewView(review);
  if (!view || !post || !approve || !data?.conversation?.stored) return;
  const box = doc.createElement('section');
  box.className = 'personal-assistant-research__review';
  box.setAttribute('aria-label', 'Review exact public lookup');
  const heading = doc.createElement('h4');
  heading.textContent = 'Review before looking up';
  const disclosure = doc.createElement('p');
  disclosure.textContent = `Only this exact ${view.document || view.registry ? 'URL' : 'query'} goes to ${view.destination}. Redirects: ${view.redirects}. No conversation, Profile/HQ memory, files, cookies or credentials are sent. This does not install, enable, trust or grant anything.`;
  const label = doc.createElement('label');
  label.textContent = view.document || view.registry ? 'Exact public URL' : 'Exact public query';
  const input = doc.createElement('input');
  input.type = 'text';
  input.value = view.value;
  input.maxLength = view.document || view.registry ? 2000 : 256;
  input.disabled = view.registry;
  label.append(input);
  const status = doc.createElement('p');
  status.setAttribute('role', 'status');
  status.textContent = 'Expires in five minutes; every follow-up needs a separate review.';
  const actions = doc.createElement('div');
  actions.className = 'personal-assistant-research__actions';
  const confirm = doc.createElement('button');
  confirm.type = 'button';
  confirm.textContent = 'Approve exact lookup';
  const cancel = doc.createElement('button');
  cancel.type = 'button';
  cancel.textContent = 'Cancel';
  const refs = data.research_context;
  const folder = data.research_folder_context;
  const conversation = { id: data.conversation.id };
  let busy = false;
  const request = () => ({
    conversation,
    context: refs,
    ...(folder ? { folder_context: folder } : {})
  });
  const validContext = () => isCurrent() && row.isConnected !== false;
  const restoreFocus = trigger => {
    if (!validContext()) return;
    if (doc.activeElement && doc.activeElement !== trigger && doc.activeElement !== doc.body)
      return;
    doc.getElementById?.('personalAssistantInput')?.focus?.({ preventScroll: true });
  };
  input.addEventListener('input', () => {
    if (review) {
      const old = review;
      review = null;
      post('/api/home-assistant/research/cancel', { ...request(), token: old.token }).catch(
        () => {}
      );
    }
    confirm.textContent = 'Review edited lookup';
    status.textContent =
      'Edited. Nothing is sent until a new exact review is prepared and approved.';
  });
  confirm.addEventListener('click', async () => {
    if (busy) return;
    if (!validContext()) {
      status.textContent =
        'Conversation or context changed. Prepare a new review; nothing was sent.';
      return;
    }
    busy = true;
    confirm.disabled = cancel.disabled = true;
    try {
      if (!review) {
        const lookup = {
          ...data.research_review.lookup,
          ...(view.document ? { url: input.value } : { query: input.value })
        };
        const prepared = await post('/api/home-assistant/research/review', {
          ...request(),
          lookup
        });
        if (!validContext()) throw new Error('stale');
        const next = researchReviewView(prepared?.review);
        if (!next || next.value !== input.value) throw new Error('changed');
        review = prepared.review;
        view = next;
        confirm.textContent = 'Approve exact lookup';
        status.textContent = 'Exact edited lookup prepared. Review it, then approve.';
      } else {
        if (!researchReviewView(review)) throw new Error('expired');
        const current = review;
        review = null;
        input.disabled = true;
        confirm.hidden = cancel.hidden = true;
        status.textContent = 'Preparing the reviewed lookup and a comparison…';
        await approve({ review: current, context: refs, folder, conversation });
        status.textContent = validContext()
          ? 'Review submitted. Only the saved reply establishes an actual source outcome; no installation was approved.'
          : 'The context changed. Reopen the original conversation for its saved outcome; nothing was retargeted.';
        restoreFocus(confirm);
      }
    } catch (_) {
      status.textContent =
        'The review expired, changed, or became unavailable. Nothing was installed; prepare a new review.';
      review = null;
      confirm.textContent = 'Review edited lookup';
    } finally {
      busy = false;
      confirm.disabled = cancel.disabled = false;
    }
  });
  cancel.addEventListener('click', async () => {
    if (busy) return;
    busy = true;
    confirm.disabled = cancel.disabled = true;
    const old = review;
    review = null;
    try {
      if (old)
        await post('/api/home-assistant/research/cancel', { ...request(), token: old.token });
    } catch (_) {
      /* Expired or stale offers also fail closed at approval. */
    }
    status.textContent = 'Cancelled. Nothing was sent; keep discussing.';
    input.disabled = true;
    actions.hidden = true;
    restoreFocus(cancel);
  });
  actions.append(confirm, cancel);
  box.append(heading, disclosure, label, status, actions);
  bubble.append(box);
}

if (typeof window !== 'undefined')
  window.PersonalAssistantResearch = { renderResponse: renderResearchResponse };
