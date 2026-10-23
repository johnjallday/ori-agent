// The assistant's offer to hire an agent for the work a request described
// (assistant-hires-from-a-request): the shared Hire card (agent-hire-card.js)
// drawn under the reply that offered it, headed the way the folder hire card
// is (folder-plan-lines.js hireView). Hire and start is the card's own
// POST /api/agents/hire with the proposal's id as the request id; Not now puts
// the offer aside; Adjust opens the workspace form prefilled. The offer itself
// hires, creates and starts nothing, and a reload draws the same card from the
// conversation's saved proposals in whatever state they are in now.
import './folder-plan-lines.js';
import './agent-hire-card.js';

const HIRE_ACTION = 'hire_agent';
const STATES = ['offered', 'declined', 'adjusted', 'hired'];

const NOT_CURRENT = 'Reopen the conversation this offer belongs to. Nothing was hired.';

function text(value) {
  return String(value ?? '')
    .split(/\s+/)
    .filter(Boolean)
    .join(' ');
}

/** The first paragraph of a reply that carries a hire card: the one line of prose above it. */
export function lead(reply) {
  const paragraphs = String(reply || '')
    .trim()
    .split(/\n\s*\n/);
  return (paragraphs[0] || '').trim();
}

function normalize(view, extra = {}) {
  const proposalId = text(view?.proposal_id);
  if (!proposalId) return null;
  const openRoles = Number(view.open_roles);
  return {
    proposalId,
    state: STATES.includes(view.state) ? view.state : 'offered',
    conversationId: text(extra.conversationId ?? view.conversation_id),
    stateVersion: Number(view.state_version ?? extra.stateVersion ?? 0) || 0,
    messageId: text(view.message_id),
    role: text(view.role),
    roleLabel: text(view.role_label) || text(view.role),
    tagline: text(view.tagline),
    name: text(view.name),
    domain: text(view.domain),
    brief: text(view.brief),
    workspaceName: text(view.workspace_name),
    blueprint: text(view.blueprint),
    blueprintLabel: text(view.blueprint_label),
    openRoles: openRoles > 0 ? openRoles : 0,
    hireId: text(view.hire_id),
    agentName: text(view.agent_name),
    workspaceId: text(view.workspace_id),
    workspaceRoute: text(view.workspace_route),
    catalog: extra.catalog || view.catalog || null
  };
}

/** The proposal a reply's hire_agent confirmation carries, or null for any other reply. */
export function proposalFromConfirmation(confirmation) {
  if (!confirmation || confirmation.action_type !== HIRE_ACTION) return null;
  return normalize(confirmation.arguments || {});
}

/** A saved proposal from a conversation reload, with the catalog the reload carries. */
export function proposalFromReload(view, catalog, conversationId) {
  return normalize(view, { catalog, conversationId });
}

/** The headline's segments, Hire <name> for <workspace>, as the folder hire card writes it. */
export function headlineSegments(proposal) {
  const lines = globalThis.OriFolderPlanLines;
  const view = lines?.hireView?.(
    {
      hire: { mode: 'create', agent_name: proposal.name, role_label: proposal.roleLabel },
      brief_default: proposal.brief
    },
    proposal.workspaceName
  );
  if (Array.isArray(view?.headline) && view.headline.length) return view.headline;
  return [
    { text: 'Hire ', strong: false },
    { text: proposal.name, strong: true },
    { text: ' for ', strong: false },
    { text: proposal.workspaceName, strong: true }
  ];
}

/** The role line under the headline. */
export function detailText(proposal) {
  return [proposal.roleLabel ? `${proposal.roleLabel} role` : '', proposal.tagline]
    .filter(Boolean)
    .join(' · ');
}

/** What the card says once the offer is settled, by its state. */
export function settledText(proposal, result) {
  switch (proposal.state) {
    case 'declined':
      return 'Put aside.';
    case 'adjusted':
      return 'Taken to the workspace form. Nothing is created until you press Create there.';
    case 'hired': {
      const hire = globalThis.OriAgentHire;
      const outcome = result || { agent_name: proposal.agentName || proposal.name };
      if (typeof hire?.hiredMessage === 'function') return hire.hiredMessage(outcome);
      return `Hired. ${text(outcome.agent_name) || 'Your agent'} is taking a first look; your assistant will report back.`;
    }
    default:
      return '';
  }
}

/** The word on the primary button for a card's view (Hire and start, Try again). */
export function primaryLabel(view) {
  return text(view?.buttonLabel) || 'Hire and start';
}

function makeButton(doc, label, primary) {
  const button = doc.createElement('button');
  button.type = 'button';
  button.textContent = label;
  if (primary) button.dataset.hirePrimary = '';
  return button;
}

function announceHired(result) {
  if (typeof CustomEvent !== 'function' || typeof globalThis.dispatchEvent !== 'function') return;
  globalThis.dispatchEvent(new CustomEvent('ori:agent-hired', { detail: { ...(result || {}) } }));
}

// render draws the offer into the reply row's bubble. options:
//   isCurrent() — whether the reply still belongs to the open conversation
//   decline(proposal), adjust(proposal) — Not now and Adjust; both may be
//     async and may reject with a sentence to show; the card then stays
//   onHired(result, proposal) — after Hire and start finished
//   takenNames(), fetchImpl, doc — passed to the shared card
export function render(row, confirmationOrProposal, options = {}) {
  // A reply's confirmation, a reload's view, or a proposal already normalized.
  const proposal = confirmationOrProposal?.action_type
    ? proposalFromConfirmation(confirmationOrProposal)
    : confirmationOrProposal?.proposalId
      ? confirmationOrProposal
      : normalize(confirmationOrProposal || {});
  if (!proposal || !row?.ownerDocument) return null;
  const doc = row.ownerDocument;
  const bubble = row.firstElementChild || row;
  bubble.querySelector?.('.personal-assistant-hire')?.remove();
  // The card needs the bubble's full width; prose keeps its own.
  if (bubble.style) bubble.style.maxWidth = '100%';

  let current = proposal;
  const card = doc.createElement('section');
  card.className = 'personal-assistant-hire';
  card.setAttribute('role', 'group');
  card.dataset.hireProposalId = proposal.proposalId;
  card.dataset.state = proposal.state;
  const headline = doc.createElement('h4');
  headline.className = 'personal-assistant-hire__headline';
  const detail = doc.createElement('p');
  detail.className = 'personal-assistant-hire__detail';
  const host = doc.createElement('div');
  host.className = 'personal-assistant-hire__card';
  const actions = doc.createElement('div');
  actions.className = 'personal-assistant-hire__actions';
  const status = doc.createElement('p');
  status.className = 'personal-assistant-hire__status';
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  card.append(headline, detail, host, actions, status);
  bubble.append(card);

  function paint() {
    const segments = headlineSegments(current);
    headline.textContent = '';
    for (const segment of segments) {
      const node = doc.createElement(segment.strong ? 'strong' : 'span');
      node.textContent = segment.text;
      headline.append(node);
    }
    card.setAttribute('aria-label', segments.map(segment => segment.text).join(''));
    detail.textContent = detailText(current);
  }

  let mounted = null;
  function settle(state, result) {
    current = { ...current, state };
    card.dataset.state = state;
    actions.remove();
    if (state !== 'hired') {
      mounted?.destroy?.();
      mounted = null;
      host.remove();
    } else if (host.querySelectorAll) {
      for (const field of host.querySelectorAll('input, textarea, button')) field.disabled = true;
    }
    status.textContent = settledText(current, result);
    const route = text(result?.workspace_route) || current.workspaceRoute;
    if (state === 'hired' && route) {
      const link = doc.createElement('a');
      link.className = 'personal-assistant-hire__link';
      link.href = route;
      link.textContent = `Open ${text(result?.workspace_name) || current.workspaceName || 'the workspace'}`;
      status.append(doc.createElement('br'), link);
    }
    paint();
  }

  if (proposal.state !== 'offered') {
    settle(proposal.state);
    return card;
  }

  const primary = makeButton(doc, 'Hire and start', true);
  const later = makeButton(doc, 'Not now', false);
  const adjust = makeButton(doc, 'Adjust', false);
  actions.append(primary, later, adjust);
  paint();

  const hire = globalThis.OriAgentHire;
  if (typeof hire?.mountHireCard === 'function') {
    mounted = hire.mountHireCard(host, {
      catalog: proposal.catalog || { entries: [] },
      prefill: {
        role: proposal.role,
        name: proposal.name,
        domain: proposal.domain,
        brief: proposal.brief,
        workspaceName: proposal.workspaceName
      },
      requestId: proposal.proposalId,
      appearance: false,
      idPrefix: `assistantHire${proposal.proposalId.replace(/[^A-Za-z0-9]/g, '').slice(0, 12)}`,
      takenNames: options.takenNames,
      fetchImpl: options.fetchImpl,
      doc,
      onChange: view => {
        const fields = view?.fields || {};
        const entry = (proposal.catalog?.entries || []).find(item => item.slug === fields.role);
        current = {
          ...current,
          role: text(fields.role) || current.role,
          roleLabel: text(entry?.display_name) || current.roleLabel,
          tagline: entry ? text(entry.tagline) : current.tagline,
          name: text(fields.name),
          domain: text(fields.domain),
          brief: text(fields.brief),
          workspaceName: text(fields.workspaceName)
        };
        primary.textContent = primaryLabel(view);
        primary.disabled = Boolean(view?.busy);
        later.disabled = Boolean(view?.busy);
        adjust.disabled = Boolean(view?.busy);
        if (view?.busy) primary.setAttribute('aria-busy', 'true');
        else primary.removeAttribute?.('aria-busy');
        if (current.state === 'offered') paint();
      },
      onHired: result => {
        settle('hired', result);
        announceHired(result);
        if (typeof options.onHired === 'function') options.onHired(result, current);
      }
    });
  }

  const isCurrent = () => (typeof options.isCurrent === 'function' ? options.isCurrent() : true);

  primary.addEventListener('click', async () => {
    if (primary.disabled) return;
    if (!isCurrent()) {
      status.textContent = NOT_CURRENT;
      return;
    }
    status.textContent = '';
    await mounted?.submit?.();
  });

  async function settleThrough(handler, state, button) {
    if (button.disabled) return;
    if (!isCurrent()) {
      status.textContent = NOT_CURRENT;
      return;
    }
    button.disabled = true;
    try {
      if (typeof handler === 'function') await handler(current);
      settle(state);
    } catch (error) {
      button.disabled = false;
      status.textContent = text(error?.message) || 'That did not go through. Try again.';
    }
  }
  later.addEventListener('click', () => settleThrough(options.decline, 'declined', later));
  adjust.addEventListener('click', () => settleThrough(options.adjust, 'adjusted', adjust));
  return card;
}

function defaultRowFor(messageId, doc) {
  const escaped = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(messageId) : messageId;
  return doc.querySelector(`#homeAssistantConversation [data-message-id="${escaped}"]`);
}

// hydrate draws a reloaded conversation's saved offers under their replies, in
// the state each is in now. It returns how many it drew.
export function hydrate(conversationId, proposals, catalog, options = {}) {
  if (!Array.isArray(proposals) || !proposals.length) return 0;
  const doc = options.doc || document;
  const rowFor = typeof options.rowFor === 'function' ? options.rowFor : defaultRowFor;
  let drawn = 0;
  for (const view of proposals) {
    const proposal = proposalFromReload(view, catalog, conversationId);
    if (!proposal?.messageId) continue;
    const row = rowFor(proposal.messageId, doc);
    if (row && render(row, proposal, options)) drawn += 1;
  }
  return drawn;
}

/** The message ids a reload's offers sit under, so their replies show only their lead line. */
export function offeredMessageIds(proposals) {
  return new Set(
    (Array.isArray(proposals) ? proposals : []).map(view => text(view?.message_id)).filter(Boolean)
  );
}

// The drawer's classic controller (dashboard.js) and the conversation module
// reach the offer through this global; OriAssistantHire is the assistant's own
// hire (personal-assistant-hire.js), a different thing.
if (typeof window !== 'undefined') {
  window.PersonalAssistantHireOffer = {
    render,
    hydrate,
    lead,
    offeredMessageIds,
    proposalFromConfirmation
  };
}
