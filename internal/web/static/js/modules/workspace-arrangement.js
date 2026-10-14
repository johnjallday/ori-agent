// Canonical, inert history shared by PA and Home. Never parse model prose into
// this projection, infer live authority, or turn an operation name into a route.
const MAX_BYTES = 64 * 1024;
const STATES = new Set(['draft', 'needs_more_evidence', 'stale', 'cancelled']);
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
  return {
    id: record.id,
    revision: record.revision,
    title:
      kind === 'clarification'
        ? 'Saved discussion — clarification'
        : 'Saved arrangement — not applied',
    origin: ORIGINS[record.origin],
    savedAt:
      typeof record.created_at === 'string' && Number.isFinite(Date.parse(record.created_at))
        ? new Date(record.created_at).toLocaleString()
        : 'Save date unavailable',
    goal: record.goal,
    rationale: record.rationale,
    clarification,
    redacted: kind === 'clarification' && !clarification,
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

export function renderArrangementHistory(container, history) {
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
  card.append(
    node(
      'p',
      'Saved intent only. Nothing here applies an arrangement, moves files, creates workspaces or grants access. Review and apply are not available here.',
      'small text-body-secondary'
    )
  );
  if (view.contextChanged || view.state === 'stale')
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
export function mountArrangementHistory(container, { id, load, isCurrent = () => true } = {}) {
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
  const expire = () => {
    button.disabled = true;
    button.setAttribute('aria-disabled', 'true');
    body.textContent =
      'Context changed. Reopen saved history from the current conversation or Home.';
  };
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
      renderArrangementHistory(body, history);
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
