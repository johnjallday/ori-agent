import { FOLDER_QUEST_ACTION_URL } from './personal-assistant-folder.js';

const FILING_ROUTE = /^\/workspaces\/[a-z0-9][a-z0-9-]*\?panel=file-janitor$/;
const TICKETS_ROUTE = /^\/workspaces\/[a-z0-9][a-z0-9-]*\?panel=tasks$/;
const internalLink = url => typeof url === 'string' && /^\/(?!\/)[^\\\s]*$/.test(url);
const craftAmount = value => Number.isSafeInteger(value) && value >= 0;

export function filingExpansionView(status) {
  const quote = status?.filing_expansion;
  if (!quote || ['disabled', 'needs_hq'].includes(quote.state)) return { visible: false };

  const view = {
    visible: true,
    title: 'Build a Filing workspace',
    detail:
      'Put Filing on your map. Choose Downloads or another folder later, and approve a review of file names and metadata separately.',
    balance: craftAmount(quote.balance) ? `You have ${quote.balance} Craft.` : '',
    free: { label: 'Show a folder for free', href: FOLDER_QUEST_ACTION_URL }
  };
  if (quote.state === 'pending') {
    return {
      ...view,
      title: 'Filing purchase needs a check',
      detail:
        'Ori could not verify that your workspace is ready. Checking does not build another workspace or charge again.',
      action: { label: 'Check purchase status', checkOnly: true }
    };
  }
  if (quote.state === 'destination_unavailable') {
    return {
      ...view,
      title: 'Filing workspace unavailable',
      detail:
        'The workspace for this purchase cannot be opened. Ori will not rebuild it or charge again.'
    };
  }
  if (quote.state === 'already_built') {
    const destinations = (quote.destinations || []).filter(row => FILING_ROUTE.test(row.route));
    if (destinations.length) {
      return {
        ...view,
        title:
          destinations.length === 1 ? 'Open your Filing workspace' : 'Choose a Filing workspace',
        detail: 'Filing is already on your map. Open an existing workspace without spending Craft.',
        ...(craftAmount(quote.charged) && {
          price:
            quote.waived > 0
              ? `${quote.waived} Craft waived in Creative Mode.`
              : `${quote.charged} Craft spent.`
        }),
        destinations
      };
    }
  }
  if (
    !['affordable', 'insufficient'].includes(quote.state) ||
    ![quote.effective_cost, quote.base_cost, quote.balance].every(craftAmount)
  ) {
    return {
      ...view,
      message: 'A build price is unavailable right now. You can still show a folder for free.'
    };
  }
  view.price = quote.creative
    ? `0 Craft in Creative Mode (normally ${quote.base_cost}).`
    : `${quote.effective_cost} Craft to build.`;
  if (quote.state === 'insufficient') {
    view.message = `You need ${quote.effective_cost - quote.balance} more Craft.`;
    const eligible = (status.missions || []).find(
      mission =>
        mission.reward_craft > 0 &&
        !mission.locked &&
        !mission.in_progress &&
        !['completed', 'skipped'].includes(mission.status) &&
        internalLink(mission.action_url)
    );
    if (eligible) {
      view.earn = { label: `Earn Craft: ${eligible.title}`, href: eligible.action_url };
    } else if (TICKETS_ROUTE.test(quote.manual_earn_url)) {
      view.earn = { label: 'Earn Craft by running a ticket by hand', href: quote.manual_earn_url };
    }
  }
  if (quote.state === 'affordable') {
    view.action = {
      label: `Build for ${quote.effective_cost} Craft`,
      cost: quote.effective_cost,
      creative: quote.creative === true
    };
  }
  return view;
}

export async function confirmFilingExpansion(action, { ask, request, newID }) {
  if (!action) return null;
  if (action.checkOnly) return request({ check_only: true });
  if (
    !ask(
      `Build a Filing workspace for ${action.cost} Craft? This does not grant access to Downloads or any other folder.`
    )
  ) {
    return null;
  }
  return request({ request_id: newID(), cost: action.cost, creative: action.creative });
}

export function filingPurchaseNotice(outcome) {
  switch (outcome) {
    case 'refunded':
      return 'The workspace was not built. Any held Craft was returned.';
    case 'stale_price':
      return 'The price changed. Review the new price before building.';
    case 'insufficient':
      return 'Your balance changed. No new purchase was made.';
    case 'unavailable':
      return 'The purchase could not be verified. Check its status before trying again.';
    default:
      return '';
  }
}

export function renderFilingExpansion(
  box,
  view,
  startFirstLook,
  firstLookURL,
  { onAction, busy, notice } = {}
) {
  if (!box) return;
  const signature = JSON.stringify([view, busy, notice]);
  if (box.dataset.view === signature) return;
  box.dataset.view = signature;
  box.hidden = !view.visible;
  if (box.hidden) return;
  const el = role => box.querySelector(`[data-role="${role}"]`);
  const text = (role, value) => {
    const node = el(role);
    node.textContent = value || '';
    node.hidden = !value;
  };
  text('expansion-title', view.title);
  text('expansion-detail', view.detail);
  text('expansion-price', [view.price, view.balance, view.message].filter(Boolean).join(' '));
  text('expansion-notice', notice);
  const action = el('expansion-action');
  action.hidden = !view.action;
  action.disabled = !!busy;
  action.textContent = busy ? 'Working…' : view.action?.label || '';
  action.onclick = onAction;
  const earn = el('expansion-earn');
  earn.hidden = !view.earn;
  if (view.earn) {
    earn.href = view.earn.href;
    earn.textContent = view.earn.label;
  }
  earn.onclick = view.earn?.href.startsWith(firstLookURL) ? startFirstLook : null;
  el('expansion-free').href = view.free.href;
  const destinations = el('expansion-destinations');
  const routes = view.destinations || [];
  if (destinations.dataset.routes !== JSON.stringify(routes)) {
    destinations.dataset.routes = JSON.stringify(routes);
    const counts = new Map();
    for (const row of routes) counts.set(row.name, (counts.get(row.name) || 0) + 1);
    destinations.replaceChildren(
      ...routes.map(row => {
        const item = box.ownerDocument.createElement('li');
        const link = box.ownerDocument.createElement('a');
        link.href = row.route;
        const slug = row.route.split('/')[2].split('?')[0];
        link.textContent =
          counts.get(row.name) > 1 ? `Open ${row.name} (${slug})` : `Open ${row.name}`;
        item.append(link);
        return item;
      })
    );
  }
  destinations.hidden = !routes.length;
}
