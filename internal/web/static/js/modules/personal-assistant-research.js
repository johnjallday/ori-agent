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
      /\s/.test(text) ||
      Array.from(text).some(ch => ch.codePointAt(0) < 32)
    )
      return '';
    const url = new URL(text);
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) return '';
    if (url.port && url.port !== (url.protocol === 'https:' ? '443' : '80')) return '';
    const host = url.hostname.toLowerCase();
    if (
      !host.includes('.') ||
      /(?:^|\.)(?:localhost|local|internal|lan|home|test|invalid)$/.test(host)
    )
      return '';
    if (/^(?:0|10|127|169\.254|192\.168|172\.(?:1[6-9]|2\d|3[01]))\./.test(host)) return '';
    if (
      /^(?:file|javascript|data):/i.test(text) ||
      /(?:api[_-]?key|token|password|secret|signature|auth|session)=/i.test(url.search)
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

export function renderResearchResponse(row, data, { post, approve, isCurrent = () => false } = {}) {
  if (!row?.ownerDocument) return;
  const bubble = row.firstElementChild || row;
  const doc = row.ownerDocument;
  const capability = data?.research_capability;
  if (capability && !capability.broker_tools) {
    const note = doc.createElement('p');
    note.className = 'personal-assistant-research__note';
    note.textContent = `${capability.provider || 'Current provider'} / ${capability.model || 'no model'}: snapshot-only; automatic catalog tools are unavailable. `;
    if (capability.manual_discovery_href === '/skills') {
      const link = doc.createElement('a');
      link.href = '/skills';
      link.textContent = 'Browse capabilities';
      note.append(link);
    }
    bubble.append(note);
  }
  const result = data?.research_result;
  if (result) {
    const note = doc.createElement('p');
    note.className = 'personal-assistant-research__note';
    const state = String(result.availability || 'unavailable').replaceAll('_', ' ');
    note.textContent = `Research: ${state}${result.reason ? ' · ' + String(result.reason).replaceAll('_', ' ') : ''}. ${result.scope || ''}`;
    bubble.append(note);
    for (const candidate of (result.candidates || []).slice(0, 8)) {
      const item = doc.createElement('p');
      item.className = 'personal-assistant-research__candidate';
      const ready = candidate.readiness || {};
      const values = ['installed', 'configured', 'enabled', 'granted', 'verified'].map(
        key => `${key}: ${ready[key] || 'unknown'}`
      );
      item.textContent = `${String(candidate.name || 'Candidate').slice(0, 120)} — ${values.join(' · ')}. Dependencies: ${ready.dependencies?.state || 'unknown'}.`;
      bubble.append(item);
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
        status.textContent = 'Looking up the approved source and preparing a comparison…';
        await approve({ review: current, context: refs, folder, conversation });
        status.textContent =
          'Lookup review submitted. The reply below reports the actual outcome; no installation was approved.';
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
  });
  actions.append(confirm, cancel);
  box.append(heading, disclosure, label, status, actions);
  bubble.append(box);
}

if (typeof window !== 'undefined')
  window.PersonalAssistantResearch = { renderResponse: renderResearchResponse };
