// Inert presentation of a canonical receipt. The caller supplies the existing
// receipt reader's view; this module owns no reads, mutations or persistence.
const WORKSPACE = /^\/workspaces\/[a-z0-9][a-z0-9-]*\/?$/;
const LIBRARY = /^\/workspaces\/[a-z0-9][a-z0-9-]*\/assistant#projectLibraryPanel$/;

export function setupCompletionView(offer, receipt) {
  if (
    offer?.status !== 'resolved' ||
    !receipt?.visible ||
    receipt.gone ||
    ['trashed', 'removed'].includes(offer.workspace_gone) ||
    (offer.setup && offer.setup.status !== 'done') ||
    !['project', 'home', 'group'].includes(offer.outcome?.kind)
  )
    return { ready: false };
  const route = String(offer.outcome.route || '');
  const href = receipt.batch ? route : String(receipt.route || '');
  if (!(receipt.home ? LIBRARY : WORKSPACE).test(href)) return { ready: false };
  const rows = Array.isArray(offer.outcome.receipt) ? offer.outcome.receipt : [];
  const name = String(
    receipt.homeName ||
      (receipt.group ? offer.subject?.name : rows.find(row => row.kind === 'workspace')?.name) ||
      offer.subject?.name ||
      'Workspace'
  ).trim();
  const facts = [];
  if (receipt.group) {
    const members = rows.filter(row => row.kind === 'workspace').length;
    if (members) facts.push(`${members} workspaces`);
  } else if (rows.some(row => row.kind === 'folder')) facts.push('Folder linked');
  const assigned = rows.filter(row => row.kind === 'agent' && row.assigned === true && row.name);
  if (assigned.length === 1) facts.push(`${assigned[0].name} assigned`);
  else if (assigned.length > 1) facts.push(`${assigned.length} agents assigned`);
  return {
    ready: true,
    name,
    headline: `✓ ${name} is ready`,
    facts: facts.join(' · '),
    href,
    openLabel: receipt.home ? `Open ${name}` : receipt.group ? receipt.openLabel : 'Open workspace'
  };
}

// Saved output only. No new summary, task or synthetic conversation turn.
export function completedTaskView(offer, firstLook, completion) {
  if (!completion?.ready || !firstLook?.visible || firstLook.state !== 'finished')
    return { visible: false };
  return {
    visible: true,
    title: offer.first_task?.recipe === 'folder_summary' ? 'Summary' : 'First task result',
    excerpt: String(firstLook.excerpt || ''),
    next: String(firstLook.next || offer.first_task?.next_request || '').trim(),
    actions: firstLook.actions.filter(action => action.href && action.id !== 'open')
  };
}

// The canonical context's offer reference binds its accepted observation to
// this review. A preview/replacement is not that binding, even in the same chat.
export function ownsSetupCompletion(state, offer, completion) {
  return Boolean(
    completion?.ready &&
    state?.conversationId &&
    state.conversationId === offer?.conversation_id &&
    state.offerId &&
    state.offerId === offer.id &&
    state.observation?.id &&
    state.accepted?.id === state.observation.id &&
    !state.preview
  );
}

// Collapse once per presentation transition, never over the reader's focus.
// Keeping a focused disclosure open for this mount also preserves reader intent.
export function completionDisclosureOpen({ sameState, open, focused, compact, hasPlan }) {
  if (sameState) return open;
  return focused || (!compact && !hasPlan);
}
