// Canonical, inert history shared by PA and Home. Never parse model prose into
// this projection, infer live authority, or turn an operation name into a route.
const MAX_BYTES = 64 * 1024;
const STATES = new Set([
  'draft',
  'needs_more_evidence',
  'stale',
  'cancelled',
  'awaiting_user_review',
  'completed'
]);
const ORIGINS = {
  model: 'Assistant-authored',
  manual: 'Manually drafted',
  rule_based: 'Rule-based'
};
const OPERATIONS = {
  create_collection: 'Proposed logical collection',
  rename_collection: 'Proposed collection name',
  set_members: 'Proposed collection membership',
  remove_collection: 'Proposed collection removal',
  create_group: 'Proposed plain workspace group',
  select_activation: 'Projects selected for separate activation review'
};
// A PA arrangement's groups, each read from its own bound setup review.
const EXECUTION_STATES = new Set([
  'draft',
  'needs_more_evidence',
  'stale',
  'cancelled',
  'awaiting_user_review',
  'applying',
  'partially_applied',
  'completed'
]);
const GROUP_STATES = {
  not_reviewed: 'Not reviewed yet',
  awaiting_user_review: 'Review open in this conversation — nothing is created until Set up',
  applying: 'Setting up from its confirmed review',
  partially_applied: 'Stopped before finishing — use Continue on its setup card',
  completed: 'Created from its confirmed review',
  closed: 'Review closed — nothing was created'
};
const count = value => Number.isSafeInteger(value) && value >= 0 && value <= 5000;
const text = (value, limit) => typeof value === 'string' && value.length <= limit;
export const arrangementID = value =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 160 &&
  value !== '.' &&
  value !== '..' &&
  !/[\\/]/u.test(value) &&
  Array.from(value).every(
    character => character.charCodeAt(0) > 32 && character.charCodeAt(0) !== 127
  );

// executionPresentation validates a PA history's group list against the
// record's own operations. Any mismatch hides the whole card rather than
// showing a control for a group the record does not propose.
function executionPresentation(execution, operations, state) {
  if (execution == null) return null;
  if (
    typeof execution !== 'object' ||
    !EXECUTION_STATES.has(execution.state) ||
    typeof execution.reviewable !== 'boolean' ||
    (execution.declinable != null && typeof execution.declinable !== 'boolean') ||
    !Array.isArray(execution.groups) ||
    execution.groups.length > 32
  )
    return undefined;
  const seen = new Set();
  const groups = [];
  for (const group of execution.groups) {
    const op = operations[group?.operation];
    if (
      !group ||
      !Number.isSafeInteger(group.operation) ||
      seen.has(group.operation) ||
      op?.kind !== 'create_group' ||
      !Object.hasOwn(GROUP_STATES, group.state) ||
      !text(group.name, 320) ||
      !Number.isSafeInteger(group.members) ||
      group.members < 1 ||
      group.members > 24 ||
      (group.offer_id != null && !arrangementID(group.offer_id)) ||
      (group.workspaces != null &&
        (!Number.isSafeInteger(group.workspaces) ||
          group.workspaces < 0 ||
          group.workspaces > 24)) ||
      (group.route != null &&
        (!text(group.route, 512) || !/^\/workspaces\/[^/?#\s]+$/u.test(group.route))) ||
      (group.state === 'completed' && (!group.route || !(group.workspaces >= 1)))
    )
      return undefined;
    seen.add(group.operation);
    groups.push({
      operation: group.operation,
      name: group.name,
      members: group.members,
      state: group.state,
      label: GROUP_STATES[group.state],
      route: group.state === 'completed' ? group.route : '',
      workspaces: group.state === 'completed' ? group.workspaces : 0
    });
  }
  const open = ['draft', 'needs_more_evidence'].includes(state);
  const confirmed = groups.some(group =>
    ['applying', 'partially_applied', 'completed'].includes(group.state)
  );
  return {
    state: execution.state,
    groups,
    reviewable: execution.reviewable && open,
    // The server says whether a decline may be asked for (an imported or
    // superseded record cannot be); a confirmed group is never declinable.
    declinable: execution.declinable === true && open && !confirmed,
    created: groups.filter(group => group.state === 'completed').length
  };
}

export function arrangementPresentation(history) {
  if (!history || typeof history !== 'object') return null;
  let size;
  try {
    const encoder = new TextEncoder();
    size = encoder.encode(JSON.stringify(history)).length;
    if (encoder.encode(JSON.stringify(history.arrangement)).length > MAX_BYTES) return null;
  } catch (_) {
    return null;
  }
  // PA's 64-KiB summary has a small conversation/message wrapper; Home's
  // 64-KiB bound includes clarification and its entire detail projection.
  if (size > MAX_BYTES + (history.kind == null ? 2048 : 0)) return null;
  const record = history.arrangement;
  const kind = history.kind ?? 'proposal';
  if (
    !record ||
    !['proposal', 'clarification'].includes(kind) ||
    !arrangementID(record.id) ||
    !Number.isSafeInteger(record.revision) ||
    record.revision < 1 ||
    !STATES.has(record.state) ||
    !Object.hasOwn(ORIGINS, record.origin) ||
    record.review_available !== false ||
    !text(record.goal, 2000) ||
    !record.goal.trim() ||
    !text(record.rationale, 2000) ||
    !count(record.explicit_unchanged) ||
    !count(record.explicit_needs_review)
  )
    return null;
  const operations = record.proposed_operations;
  const questions = record.review_questions;
  const unknowns = record.unknowns;
  if (
    !Array.isArray(operations) ||
    operations.length > 32 ||
    record.operations !== operations.length ||
    !operations.every(
      op =>
        op &&
        Object.hasOwn(OPERATIONS, op.kind) &&
        count(op.members) &&
        (op.name == null || text(op.name, 320))
    ) ||
    !Array.isArray(questions) ||
    questions.length > 32 ||
    !questions.every(q => q && text(q.reason, 2000) && count(q.members)) ||
    !Array.isArray(unknowns) ||
    unknowns.length > 16 ||
    !unknowns.every(value => text(value, 2000))
  )
    return null;
  const coverage = record.coverage;
  if (!coverage || !text(coverage.note, 2000)) return null;
  const receipt = history.collection_receipt;
  const home = history.kind === 'proposal' && typeof history.catalog_current === 'boolean';
  if (['awaiting_user_review', 'completed'].includes(record.state) && !home) return null;
  if (record.state === 'completed' && !receipt) return null;
  if (
    receipt &&
    (!home ||
      !['completed', 'stale'].includes(record.state) ||
      !/^[a-f0-9]{64}$/u.test(receipt.digest) ||
      !Number.isSafeInteger(receipt.catalog_revision) ||
      receipt.catalog_revision < 1 ||
      typeof receipt.applied_at !== 'string' ||
      !Number.isFinite(Date.parse(receipt.applied_at)) ||
      !Array.isArray(receipt.effects) ||
      receipt.effects.length > 32 ||
      new Set(receipt.effects.map(effect => effect?.queue_id || effect?.collection_id)).size !==
        receipt.effects.length ||
      !receipt.effects.every(
        effect =>
          effect &&
          (effect.kind === 'select_activation'
            ? arrangementID(effect.queue_id) &&
              effect.collection_id === '' &&
              Number.isSafeInteger(effect.selected_count) &&
              effect.selected_count >= 1 &&
              effect.selected_count <= 100 &&
              effect.before_revision === 0 &&
              effect.after_revision === 0
            : arrangementID(effect.collection_id) &&
              !effect.queue_id &&
              !effect.selected_count &&
              Number.isSafeInteger(effect.before_revision) &&
              Number.isSafeInteger(effect.after_revision) &&
              (effect.kind === 'create_collection'
                ? effect.before_revision === 0 && effect.after_revision === 1
                : effect.kind === 'remove_collection'
                  ? effect.before_revision >= 1 && effect.after_revision === 0
                  : ['rename_collection', 'set_members'].includes(effect.kind) &&
                    effect.before_revision >= 1 &&
                    effect.after_revision === effect.before_revision + 1))
      ) ||
      receipt.effects.filter(effect => effect.kind === 'select_activation').length > 1)
  )
    return null;
  const revisionDiff = history.revision_diff;
  if (
    revisionDiff &&
    (!home ||
      !Number.isSafeInteger(revisionDiff.base_revision) ||
      revisionDiff.base_revision < 1 ||
      revisionDiff.revision !== revisionDiff.base_revision + 1 ||
      revisionDiff.revision > record.revision ||
      !Number.isSafeInteger(revisionDiff.changed_operations) ||
      revisionDiff.changed_operations < 0 ||
      revisionDiff.changed_operations > 32 ||
      !Number.isSafeInteger(revisionDiff.added_members) ||
      revisionDiff.added_members < 0 ||
      revisionDiff.added_members > 20000 ||
      !Number.isSafeInteger(revisionDiff.removed_members) ||
      revisionDiff.removed_members < 0 ||
      revisionDiff.removed_members > 20000 ||
      typeof revisionDiff.changed_exclusions !== 'boolean' ||
      typeof revisionDiff.changed_questions !== 'boolean')
  )
    return null;
  const discussion = history.revision_discussion;
  if (
    discussion &&
    (!home ||
      receipt ||
      !['needs_more_evidence', 'cancelled'].includes(record.state) ||
      discussion.revision < 2 ||
      discussion.revision !== record.revision ||
      !text(discussion.goal, 2000) ||
      !discussion.goal.trim() ||
      !text(discussion.answer, 32768) ||
      !discussion.answer.trim() ||
      !text(discussion.provider, 80) ||
      !discussion.provider ||
      !text(discussion.model, 512) ||
      !discussion.model ||
      typeof discussion.at !== 'string' ||
      !Number.isFinite(Date.parse(discussion.at)))
  )
    return null;
  const correction = history.collection_correction;
  if (
    correction &&
    (!home ||
      !Number.isSafeInteger(correction.revision) ||
      correction.revision < 2 ||
      correction.revision > record.revision ||
      !Number.isSafeInteger(correction.operation) ||
      correction.operation < 0 ||
      correction.operation >= 32 ||
      typeof correction.renamed !== 'boolean' ||
      !Number.isSafeInteger(correction.excluded) ||
      correction.excluded < 0 ||
      correction.excluded > 50 ||
      (correction.added != null &&
        (!Number.isSafeInteger(correction.added) ||
          correction.added < 0 ||
          correction.added > 50)) ||
      (correction.converted != null &&
        !['catalog_only', 'select_activation'].includes(correction.converted)) ||
      (correction.resolved != null && typeof correction.resolved !== 'boolean') ||
      (correction.released != null &&
        (!Number.isSafeInteger(correction.released) ||
          correction.released < 0 ||
          correction.released > 5000)) ||
      (!correction.renamed &&
        !correction.excluded &&
        !correction.added &&
        !correction.converted &&
        !correction.resolved &&
        !correction.released) ||
      typeof correction.at !== 'string' ||
      !Number.isFinite(Date.parse(correction.at)))
  )
    return null;
  const clarification = history.clarification ?? '';
  if (
    !text(clarification, 32768) ||
    (kind === 'proposal' && clarification) ||
    (kind === 'clarification' &&
      (operations.length ||
        questions.length ||
        record.explicit_unchanged ||
        record.explicit_needs_review ||
        record.rationale ||
        unknowns.length ||
        !['needs_more_evidence', 'stale'].includes(record.state) ||
        (!clarification && record.state !== 'stale')))
  )
    return null;
  // Only a PA proposal carries groups; a Home record never does.
  const execution = executionPresentation(history.execution, operations, record.state);
  if (execution === undefined || (execution && history.kind != null)) return null;
  const executionTitle = !execution
    ? ''
    : execution.state === 'completed'
      ? 'Arrangement applied — groups created from their reviews'
      : execution.state === 'partially_applied'
        ? 'Arrangement partly applied'
        : execution.state === 'applying'
          ? 'Arrangement — a reviewed group is being set up'
          : execution.state === 'awaiting_user_review'
            ? 'Saved arrangement — a group review is open'
            : '';
  return {
    id: record.id,
    revision: record.revision,
    execution,
    title: executionTitle
      ? executionTitle
      : kind === 'clarification'
        ? 'Saved discussion — clarification'
        : discussion && record.state !== 'cancelled'
          ? 'Saved revision question — prior proposal retained'
          : receipt
            ? receipt.effects.some(effect => effect.kind === 'select_activation')
              ? 'Saved metadata and activation-review selection'
              : 'Saved collection metadata receipt'
            : record.state === 'awaiting_user_review'
              ? 'Saved arrangement — awaiting review'
              : record.state === 'cancelled'
                ? 'Declined arrangement — not applied'
                : 'Saved arrangement — not applied',
    appliedEffects: receipt
      ? receipt.effects.filter(effect => effect.kind !== 'select_activation').length
      : null,
    queuedProjects:
      receipt?.effects.find(effect => effect.kind === 'select_activation')?.selected_count || 0,
    origin:
      ORIGINS[record.origin] +
      (correction
        ? ` · Owner correction, revision ${correction.revision}: ${
            correction.converted === 'catalog_only'
              ? 'activation selection removed; those projects stay catalog-only'
              : correction.converted === 'select_activation'
                ? 'collection members also selected for independent activation reviews'
                : correction.resolved
                  ? 'an open question was resolved'
                  : correction.released
                    ? `${correction.released} explicitly unchanged items released`
                    : `${correction.renamed ? 'renamed intention; ' : ''}${correction.excluded} proposed members excluded${correction.added ? `; ${correction.added} saved members added` : ''}`
          }`
        : ''),
    savedAt:
      typeof record.created_at === 'string' && Number.isFinite(Date.parse(record.created_at))
        ? new Date(record.created_at).toLocaleString()
        : 'Save date unavailable',
    goal: record.goal,
    rationale: record.rationale,
    clarification,
    revisionDiscussion: discussion || null,
    revisionDiff: revisionDiff || null,
    redacted: kind === 'clarification' && !clarification,
    home,
    state: record.state,
    contextChanged: history.context_current === false || history.catalog_current === false,
    coverageLabel: coverage.partial === true ? 'Partial evidence' : 'Bounded saved evidence',
    coverageNote: coverage.note,
    coverageFacts: [
      ['max_depth', 'Recorded depth limit'],
      ['max_entries', 'Observation entry limit'],
      ['tree_omitted', 'Tree nodes omitted'],
      ['skipped_links', 'Links skipped']
    ]
      .filter(([key]) => Number.isSafeInteger(coverage[key]) && coverage[key] > 0)
      .map(([key, label]) => `${label}: ${coverage[key]}`),
    operations: operations.map(
      op => `${OPERATIONS[op.kind]}${op.name ? `: ${op.name}` : ''} · ${op.members} member(s)`
    ),
    questions: questions.map(q => `${q.reason} · ${q.members} affected item(s)`),
    unknowns: [...unknowns],
    dispositions: `${record.explicit_unchanged} explicitly unchanged; ${record.explicit_needs_review} explicitly need review. Unmentioned items stay unchanged. Counts can overlap; they are not a complete inventory.`
  };
}

// renderGroups lists a PA arrangement's proposed groups with what each one's
// own setup review became. A control only asks the host to open that review;
// creating anything still needs Set up on the review card itself.
function renderGroups(card, view, actions, node) {
  const execution = view.execution;
  if (!execution?.groups.length) return;
  const section = node('section', null, 'mt-2');
  section.setAttribute('aria-label', 'Proposed groups');
  section.append(node('h5', 'Proposed groups', 'h6'));
  const list = node('ul', null, 'list-unstyled mb-2');
  for (const group of execution.groups) {
    const item = node('li', null, 'mb-2');
    item.append(
      node('span', group.name, 'fw-semibold'),
      node(
        'span',
        ` · ${group.members} folder${group.members === 1 ? '' : 's'} · ${group.label}`,
        'small'
      )
    );
    if (group.state === 'completed') {
      const link = node('a', 'Open group', 'btn btn-sm btn-link ms-1');
      link.href = group.route;
      link.setAttribute('aria-label', `Open group ${group.name}`);
      item.append(
        link,
        node(
          'span',
          `${group.workspaces} workspace${group.workspaces === 1 ? '' : 's'} read back from the receipt`,
          'small text-body-secondary'
        )
      );
    } else if (
      execution.reviewable &&
      ['not_reviewed', 'closed'].includes(group.state) &&
      typeof actions.reviewGroup === 'function'
    ) {
      const button = node('button', 'Review group', 'btn btn-sm btn-primary ms-2');
      button.type = 'button';
      button.setAttribute('aria-label', `Review group ${group.name}`);
      button.setAttribute('data-arrangement-operation', String(group.operation));
      button.addEventListener('click', () => actions.reviewGroup(group.operation, button));
      item.append(button);
    }
    list.append(item);
  }
  section.append(list);
  if (!execution.reviewable && execution.groups.some(group => group.state !== 'completed'))
    section.append(
      node(
        'p',
        'Review is not available from here: the proposal is history, or the folder it was made from is no longer the one attached to this conversation.',
        'small text-body-secondary'
      )
    );
  if (execution.declinable && typeof actions.decline === 'function') {
    const decline = node('button', 'Decline proposal', 'btn btn-sm btn-outline-secondary');
    decline.type = 'button';
    decline.addEventListener('click', () => actions.decline(decline));
    section.append(decline);
  }
  card.append(section);
}

export function renderArrangementHistory(container, history, actions = {}) {
  const view = arrangementPresentation(history);
  const document = container.ownerDocument;
  const node = (tag, value, className = '') => {
    const element = document.createElement(tag);
    element.className = className;
    if (value != null) element.textContent = value;
    return element;
  };
  container.replaceChildren();
  if (!view) {
    container.append(
      node(
        'p',
        'This saved arrangement could not be displayed safely. Reopen its canonical history.',
        'text-body-secondary'
      )
    );
    return false;
  }
  const card = node('section', null, 'border rounded p-3 my-2 text-break');
  card.setAttribute('aria-label', view.title);
  card.append(
    node('h4', view.title, 'h6'),
    node(
      'p',
      `${view.origin} · Revision ${view.revision} · ${view.savedAt}`,
      'small text-body-secondary'
    )
  );
  card.append(node('p', view.goal, 'fw-semibold'));
  if (view.revisionDiff) {
    const diff = view.revisionDiff;
    card.append(
      node(
        'p',
        `Model revision ${diff.base_revision} → ${diff.revision}: ${diff.changed_operations} intention slots changed; ${diff.added_members} member references added, ${diff.removed_members} removed.${diff.changed_exclusions ? ' Explicit exclusions changed.' : ''}${diff.changed_questions ? ' Questions or unknowns changed.' : ''} Counts describe proposal changes, not unique projects or applied effects.`,
        'small'
      )
    );
  }
  if (view.revisionDiscussion) {
    const discussion = view.revisionDiscussion;
    const section = node('section');
    section.append(
      node('h5', 'Revision needs an answer', 'h6'),
      node('p', `Requested change: ${discussion.goal}`),
      node('p', discussion.answer),
      node(
        'p',
        `Reply from ${discussion.provider} / ${discussion.model}. Prior intentions below are retained, not approved. No collection review is available until the question is resolved.`,
        'small'
      )
    );
    card.append(section);
  }
  card.append(
    node(
      'p',
      view.appliedEffects !== null
        ? `Canonical receipt: ${view.appliedEffects} logical-collection metadata change(s). ${view.queuedProjects ? `${view.queuedProjects} project(s) selected for independent activation review; queue preparation did not create or activate them. ` : ''}No source files changed, workspaces created or reparented, or projects activated. This history does not authorize another apply.`
        : view.execution
          ? view.execution.created
            ? `Canonical receipts: ${view.execution.created} ${view.execution.created === 1 ? 'group was' : 'groups were'} created, each from its own confirmed setup review. This history does not authorize another setup.`
            : 'Saved intent. A group is created only from its own setup card in this conversation, after you press Set up there; that card lists every consequence. Files are never moved, copied or read, and folders the arrangement did not select stay as they are.'
          : view.home
            ? 'Saved intent only. Nothing changes until you review the exact collection metadata and confirm it. Files are never moved, and this proposal creates no workspace and activates no project.'
            : 'Saved intent only. Nothing here applies an arrangement, moves files, creates workspaces or grants access. Review and apply are not available here.',
      'small text-body-secondary'
    )
  );
  // An applied receipt is history by definition, and the catalog it changed
  // (or a project it queued, once connected) moves on after it: that is a note,
  // not a warning about the record.
  if (view.appliedEffects !== null && (view.contextChanged || view.state === 'stale'))
    card.append(
      node(
        'p',
        'The catalog has changed since this was applied. This receipt stays as history.',
        'small text-body-secondary'
      )
    );
  else if (view.contextChanged || view.state === 'stale')
    card.append(
      node(
        'p',
        'Historical context has changed. This saved record is not current source authority.',
        'small text-warning-emphasis'
      )
    );
  if (view.state === 'cancelled')
    card.append(node('p', 'Cancelled draft — history only.', 'small text-body-secondary'));
  card.append(
    node('h5', view.coverageLabel, 'h6'),
    node('p', view.coverageNote || 'No additional coverage details were saved.', 'small')
  );
  if (view.coverageFacts.length)
    card.append(node('p', view.coverageFacts.join(' · '), 'small text-body-secondary'));
  card.append(
    node(
      'p',
      'These are saved observations, not a current filesystem inspection. Dates describe observed activity, not progress.',
      'small text-body-secondary'
    )
  );
  if (view.clarification.length > 1000) {
    const discussion = node('details');
    discussion.append(
      node('summary', 'Read full saved clarification'),
      node('p', view.clarification)
    );
    card.append(discussion);
  } else if (view.clarification) card.append(node('p', view.clarification));
  if (view.redacted)
    card.append(
      node(
        'p',
        'Discussion text was redacted after catalog history was forgotten.',
        'small text-body-secondary'
      )
    );
  if (view.rationale) card.append(node('p', view.rationale));
  const list = (title, items) => {
    if (!items.length) return;
    const details = node('details');
    details.append(node('summary', `${title} (${items.length})`));
    const rows = node('ul');
    for (const item of items) rows.append(node('li', item));
    details.append(rows);
    card.append(details);
  };
  renderGroups(card, view, actions, node);
  list('Proposed intentions', view.operations);
  list('Material unknowns', view.unknowns);
  list('Questions needing review', view.questions);
  card.append(node('p', view.dispositions, 'small text-body-secondary'));
  card.append(
    node(
      'p',
      'Logical collections are catalog references, not folders or workspace parents. Activation selections still require independent project reviews.',
      'small text-body-secondary'
    )
  );
  container.append(card);
  return true;
}

// Opening history performs only a caller-bound canonical GET. A removed row or
// switched owner context cannot receive a late response. No input or authority
// is recovered from assistant text, and errors never expose raw server messages.
// Optional caller actions (a PA conversation's Review group / Decline) receive
// the last canonical history; their messages are host-authored text only.
export function mountArrangementHistory(
  container,
  { id, load, isCurrent = () => true, actions = null, initial = null } = {}
) {
  if (!arrangementID(id) || typeof load !== 'function') return false;
  const document = container.ownerDocument;
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'btn btn-sm btn-outline-secondary';
  button.textContent = 'Open saved arrangement';
  const body = document.createElement('div');
  body.setAttribute('aria-live', 'polite');
  container.append(button, body);
  let pending = false;
  let shown = null;
  const expire = () => {
    button.disabled = true;
    button.setAttribute('aria-disabled', 'true');
    body.textContent =
      'Context changed. Reopen saved history from the current conversation or Home.';
  };
  const live = () => isCurrent() && container.isConnected !== false;
  async function act(task, trigger) {
    if (pending || !shown) return;
    if (!isCurrent()) {
      expire();
      return;
    }
    pending = true;
    trigger?.setAttribute('aria-disabled', 'true');
    body.setAttribute('aria-busy', 'true');
    let message = '';
    try {
      const result = await task(shown);
      if (!live()) return;
      if (result?.history?.arrangement?.id === id) render(result.history);
      message = typeof result?.message === 'string' ? result.message : '';
    } catch (error) {
      message =
        typeof error?.message === 'string' && error.message
          ? error.message
          : 'That did not complete. Nothing was created; reopen this conversation to try again.';
    } finally {
      pending = false;
      body.setAttribute('aria-busy', 'false');
      if (trigger?.isConnected) trigger.setAttribute('aria-disabled', 'false');
    }
    if (message && live()) {
      const note = document.createElement('p');
      note.className = 'small';
      note.setAttribute('role', 'status');
      note.textContent = message;
      body.append(note);
    }
  }
  const wrapped = {};
  if (typeof actions?.reviewGroup === 'function')
    wrapped.reviewGroup = (operation, trigger) =>
      act(history => actions.reviewGroup(operation, history), trigger);
  if (typeof actions?.decline === 'function')
    wrapped.decline = trigger => act(history => actions.decline(history), trigger);
  function render(history) {
    shown = renderArrangementHistory(body, history, wrapped) ? history : null;
  }
  // A reply's own saved summary may be shown at once; the canonical read
  // (which also lists each group's review state) still needs the button.
  if (initial?.arrangement?.id === id) {
    render(initial);
    if (shown) button.textContent = 'Refresh saved arrangement';
  }
  button.addEventListener('click', async () => {
    if (pending || button.disabled) return;
    if (!isCurrent()) {
      expire();
      return;
    }
    pending = true;
    // Native disabled blurs the trigger in Chromium. Keep it focusable while
    // guarding duplicate activation; never steal focus back after an await.
    button.setAttribute('aria-disabled', 'true');
    body.setAttribute('aria-busy', 'true');
    body.textContent = 'Reading saved history…';
    try {
      const history = await load();
      if (!isCurrent() || container.isConnected === false) return;
      if (history?.arrangement?.id !== id) throw new Error('Wrong canonical record');
      render(history);
      button.textContent = 'Refresh saved arrangement';
    } catch (_) {
      if (isCurrent() && container.isConnected !== false)
        body.textContent =
          'Saved history is unavailable. Reopen this conversation or Home to try again. Nothing was applied.';
    } finally {
      pending = false;
      body.setAttribute('aria-busy', 'false');
      button.setAttribute('aria-disabled', 'false');
      button.disabled = false;
      if (!isCurrent() && container.isConnected !== false) expire();
    }
  });
  return true;
}
