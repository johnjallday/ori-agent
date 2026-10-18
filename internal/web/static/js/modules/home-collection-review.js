import { exactName, mountCollectionCandidates } from './home-collection-candidates.js';
import {
  arrangementID,
  arrangementPresentation,
  renderArrangementHistory
} from './workspace-arrangement.js';

const plural = (count, one, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;

// describeEffect names one reviewed collection effect in words, with the
// member counts before and after it.
export function describeEffect(effect) {
  const before = effect.before_name;
  const after = effect.after_name;
  switch (effect.kind) {
    case 'create_collection':
      return `New collection “${after}” · ${plural(effect.after_members, 'member')}`;
    case 'rename_collection':
      return `Rename collection “${before}” to “${after}” · ${plural(effect.after_members, 'member')}`;
    case 'set_members':
      return `Change the members of “${after}” · ${effect.before_members} → ${plural(effect.after_members, 'member')}`;
    case 'remove_collection':
      return `Remove collection “${before}” · its ${plural(effect.before_members, 'member')} stay in the catalog`;
    case 'select_activation':
      return `Select ${plural(effect.after_members, 'project')} for separate activation reviews`;
    default:
      return 'Unknown change';
  }
}

export function collectionReviewPage(value, id, token = null) {
  const kinds = [
    'create_collection',
    'rename_collection',
    'set_members',
    'remove_collection',
    'select_activation'
  ];
  const integer = n => Number.isSafeInteger(n) && n >= 0 && n <= 5000;
  const text = (s, max) => typeof s === 'string' && s.length <= max;
  if (
    !value ||
    value.arrangement_id !== id ||
    !arrangementID(value.review_token) ||
    (token && value.review_token !== token) ||
    !Number.isSafeInteger(value.catalog_revision) ||
    value.catalog_revision < 1 ||
    typeof value.expires_at !== 'string' ||
    !Number.isFinite(Date.parse(value.expires_at)) ||
    !text(value.goal, 2000) ||
    !text(value.rationale, 2000) ||
    !integer(value.unchanged) ||
    !Array.isArray(value.needs_review) ||
    value.needs_review.length > 32 ||
    !value.needs_review.every(c => c && text(c.reason, 500) && integer(c.count)) ||
    !Array.isArray(value.effects) ||
    value.effects.length > 32 ||
    !value.effects.every(
      e =>
        e &&
        kinds.includes(e.kind) &&
        (e.kind === 'select_activation'
          ? arrangementID(e.queue_id) &&
            e.collection_id === '' &&
            e.before_members === 0 &&
            e.after_members >= 1 &&
            e.after_members <= 100
          : arrangementID(e.collection_id) && !e.queue_id) &&
        text(e.before_name, 128) &&
        text(e.after_name, 128) &&
        integer(e.before_members) &&
        integer(e.after_members)
    ) ||
    !integer(value.operation) ||
    value.operation >= Math.max(1, value.effects.length) ||
    !integer(value.offset) ||
    value.offset % 50 ||
    !integer(value.total) ||
    !Array.isArray(value.members) ||
    value.members.length > 50 ||
    !value.members.every(
      m =>
        m &&
        arrangementID(m.id) &&
        text(m.name, 512) &&
        text(m.availability, 160) &&
        typeof m.before === 'boolean' &&
        typeof m.after === 'boolean' &&
        (m.before || m.after)
    ) ||
    value.members.length !== Math.min(50, value.total - value.offset) ||
    (value.next_offset != null && value.next_offset !== value.offset + value.members.length) ||
    value.offset + value.members.length < value.total !== (value.next_offset != null) ||
    !value.coverage ||
    !text(value.coverage.note, 2000) ||
    (value.unknowns != null &&
      (!Array.isArray(value.unknowns) ||
        value.unknowns.length > 16 ||
        !value.unknowns.every(s => text(s, 2000))))
  )
    return null;
  return value;
}

// Explicit owner UI only. No model tools, automatic retry or durable consent.
export function mountCollectionReview(
  container,
  {
    ref,
    request,
    isCurrent,
    reload,
    reviewAllowed = true,
    onApplied = () => {},
    onRevised = () => {}
  }
) {
  container.className = `${container.className || ''} workspace-collection-review`;
  const document = container.ownerDocument;
  const node = (tag, text, cls = '') => {
    const el = document.createElement(tag);
    el.textContent = text;
    el.className = cls;
    return el;
  };
  const start = node('button', 'Review collection metadata', 'btn btn-sm btn-outline-secondary');
  start.type = 'button';
  start.hidden = !reviewAllowed;
  const decline = node('button', 'Decline proposal', 'btn btn-sm btn-outline-secondary');
  decline.type = 'button';
  const reopen = node('button', 'Reopen saved history', 'btn btn-sm btn-link');
  reopen.type = 'button';
  const status = node('p', '', 'small');
  status.setAttribute('role', 'status');
  const body = node('section', '', 'border rounded p-3 my-2 text-break');
  body.hidden = true;
  body.setAttribute('aria-label', 'Review exact collection metadata');
  container.append(start, decline, reopen, status, body);
  let unsaved = () => false;
  let pending = false,
    generation = 0,
    review = null,
    key = '',
    settled = false;
  const current = () => container.isConnected && isCurrent();
  const lock = value => {
    pending = value;
    start.setAttribute('aria-disabled', String(value || settled || !reviewAllowed));
    decline.setAttribute('aria-disabled', String(value || settled));
    reopen.setAttribute('aria-disabled', String(value));
  };
  const failed = () => {
    review = null;
    settled = true;
    body.replaceChildren();
    body.hidden = true;
    status.textContent =
      'Collection review or its saved outcome could not be confirmed. Reopen saved history before trying again. Unsupported workspace-creation proposals require separate reviews; nothing is applied automatically.';
  };
  const show = page => {
    body.replaceChildren();
    body.hidden = false;
    const activation = page.effects.some(effect => effect.kind === 'select_activation');
    body.append(
      node('h4', 'Review exact collection metadata', 'h6'),
      node('p', page.goal),
      node(
        'p',
        activation
          ? 'Save the listed logical collection metadata and prepare the exact selected projects for independent activation reviews. Queue preparation creates no workspace or project link, grants no file access, installs no integration, and activates nothing. Each project still needs its own exact file choice, provider/staffing disclosures and explicit confirmation. Unsupported projects may remain catalog-only or be skipped.'
          : 'Only logical collection names and exact saved-catalog membership will change. No files move, no workspaces or project links are created, and no projects are activated.',
        'small'
      )
    );
    body.append(
      node(
        'p',
        `${page.coverage.partial ? 'Partial evidence. ' : ''}${page.coverage.note}`,
        'small'
      )
    );
    body.append(node('p', `Proposal rationale: ${page.rationale}`, 'small'));
    body.append(
      node(
        'p',
        `${page.unchanged} items explicitly left unchanged. Unmentioned items are not added to these collections.`,
        'small'
      )
    );
    for (const caution of page.needs_review)
      body.append(
        node('p', `Left for review (${caution.count} items): ${caution.reason}`, 'small')
      );
    for (const unknown of page.unknowns || [])
      body.append(node('p', `Unknown: ${unknown}`, 'small'));
    const effects = node('ol', '', 'small');
    for (const effect of page.effects) effects.append(node('li', describeEffect(effect)));
    if (!page.effects.length) effects.append(node('li', 'No collection changes proposed.'));
    body.append(effects);
    const select = node('select', '', 'form-select form-select-sm');
    select.setAttribute('aria-label', 'Collection effect');
    page.effects.forEach((effect, index) => {
      const option = node('option', describeEffect(effect));
      option.value = String(index);
      select.append(option);
    });
    select.value = String(page.operation);
    select.disabled = page.effects.length === 0;
    body.append(
      select,
      node(
        'p',
        `${page.offset + (page.total ? 1 : 0)}–${page.offset + page.members.length} of ${page.total} exact members.`,
        'small'
      )
    );
    const list = node('ul', '');
    for (const member of page.members)
      list.append(
        node(
          'li',
          `${member.before && member.after ? 'Keep' : member.after ? 'Add' : 'Remove'}: ${exactName(member, page.members)} · ${member.availability}`
        )
      );
    body.append(list);
    const previous = node('button', 'Previous members', 'btn btn-sm btn-outline-secondary');
    previous.type = 'button';
    previous.disabled = page.offset === 0;
    const next = node('button', 'Next members', 'btn btn-sm btn-outline-secondary');
    next.type = 'button';
    next.disabled = page.next_offset == null;
    body.append(previous, next);
    const label = node('label', '', 'd-block my-2');
    const choice = node('input', '');
    choice.type = 'checkbox';
    label.append(
      choice,
      node(
        'span',
        activation
          ? ' I confirm the listed metadata changes and exact activation-review selection, including other pages. This does not approve project creation.'
          : ' I confirm all listed collection effects and their exact membership, including other pages.'
      )
    );
    const confirm = node(
      'button',
      activation ? 'Save metadata and prepare activation reviews' : 'Apply collection metadata',
      'btn btn-sm btn-primary'
    );
    confirm.type = 'button';
    confirm.setAttribute('aria-disabled', 'true');
    body.append(
      label,
      confirm,
      node(
        'p',
        `Review expires ${new Date(page.expires_at).toLocaleString()}. Changes require a fresh review.`,
        'small text-body-secondary'
      )
    );
    const effect = page.effects[page.operation];
    const excluded = new Set();
    const added = new Map();
    const correctionChecks = [];
    let proposedName = null;
    const dirty = () =>
      added.size > 0 ||
      excluded.size > 0 ||
      (proposedName && proposedName.value !== effect.after_name);
    unsaved = dirty;
    const editSummary = node('p', '', 'small');
    const save = node('button', 'Save correction — not apply', 'btn btn-sm btn-outline-secondary');
    save.type = 'button';
    save.setAttribute('aria-disabled', 'true');
    const changed = () => {
      choice.checked = false;
      confirm.setAttribute('aria-disabled', 'true');
      save.setAttribute('aria-disabled', String(!dirty() || pending || settled));
      editSummary.textContent = dirty()
        ? `${proposedName && proposedName.value !== effect.after_name ? `Name: ${effect.after_name} → ${proposedName.value}. ` : ''}${excluded.size} proposed members excluded; ${added.size} saved members added. Save a new revision, then request a fresh review; nothing is applied.`
        : '';
    };
    if (
      effect &&
      ['create_collection', 'rename_collection', 'set_members', 'select_activation'].includes(
        effect.kind
      )
    ) {
      const editor = node('details', '');
      editor.append(node('summary', 'Correct this proposal'));
      if (['create_collection', 'rename_collection'].includes(effect.kind)) {
        const nameLabel = node('label', 'Proposed collection name');
        proposedName = node('input', '');
        proposedName.type = 'text';
        proposedName.value = effect.after_name;
        proposedName.maxLength = 128;
        proposedName.addEventListener('input', changed);
        nameLabel.append(proposedName);
        editor.append(nameLabel);
      }
      if (['create_collection', 'set_members', 'select_activation'].includes(effect.kind)) {
        editor.append(
          node(
            'p',
            effect.kind === 'select_activation'
              ? 'Edit only the projects selected for future activation reviews. Other collection memberships are unchanged. Keep 1–100 catalog-only projects; this correction cannot approve their creation.'
              : 'Exclude proposed members on this page only. Other intentions are unchanged; overlapping memberships remain independent.',
            'small'
          )
        );
        for (const member of page.members.filter(member => member.after)) {
          const memberLabel = node('label', '');
          const check = node('input', '');
          check.type = 'checkbox';
          correctionChecks.push(check);
          check.addEventListener('change', () => {
            if (check.checked) excluded.add(member.id);
            else excluded.delete(member.id);
            changed();
          });
          memberLabel.append(check, node('span', ` Exclude ${exactName(member, page.members)}`));
          editor.append(memberLabel);
        }
      }
      if (['create_collection', 'set_members', 'select_activation'].includes(effect.kind)) {
        mountCollectionCandidates(editor, {
          page,
          request,
          added,
          isCurrent: () => current() && !settled && review === page,
          isBusy: () => pending,
          setBusy: lock,
          changed,
          failed: () => {
            settled = true;
            choice.checked = false;
            choice.disabled = true;
          }
        });
      }
      const discard = node('button', 'Discard unsaved correction', 'btn btn-sm btn-link');
      discard.type = 'button';
      discard.addEventListener('click', () => {
        if (!pending && current() && !settled) show(page);
      });
      editor.append(editSummary, save, discard);
      body.append(editor);
    }
    // Structural intent changes: each saves a new proposal revision, never an
    // apply, and is refused while a member correction is unsaved.
    const structural = [];
    const intent = node('details', '');
    intent.append(
      node('summary', 'Change what this proposal intends'),
      node(
        'p',
        'Each change saves a new proposal revision and needs a fresh review. Nothing is applied, queued or created.',
        'small'
      )
    );
    const intentAction = (label, payload) => {
      const button = node('button', label, 'btn btn-sm btn-outline-secondary me-2 mb-2');
      button.type = 'button';
      structural.push(button);
      button.addEventListener('click', () => {
        if (dirty()) {
          status.textContent = 'Save or discard the unsaved correction first.';
          return;
        }
        void submit({ review_token: page.review_token, operation: page.operation, ...payload });
      });
      intent.append(button);
    };
    if (effect?.kind === 'select_activation')
      intentAction('Keep these projects catalog-only instead', { convert: 'catalog_only' });
    if (
      ['create_collection', 'set_members'].includes(effect?.kind) &&
      !page.effects.some(item => item.kind === 'select_activation')
    )
      intentAction('Also select catalog-only members for activation reviews', {
        convert: 'select_activation'
      });
    page.needs_review.forEach((caution, index) => {
      intent.append(
        node('p', `Open question (${caution.count} items): ${caution.reason}`, 'small')
      );
      intentAction(`Keep question ${index + 1} items unchanged`, {
        resolve_question: index,
        resolution: 'unchanged'
      });
      intentAction(`Dismiss question ${index + 1}`, {
        resolve_question: index,
        resolution: 'dismiss'
      });
    });
    if (page.unchanged > 0)
      intentAction(`Release ${page.unchanged} explicitly unchanged items`, {
        release_unchanged: true
      });
    if (structural.length) body.append(intent);
    const submit = async payload => {
      if (pending || settled || !current() || review !== page) return;
      choice.checked = false;
      confirm.setAttribute('aria-disabled', 'true');
      if (Date.parse(page.expires_at) <= Date.now()) {
        failed();
        return;
      }
      if (proposedName) proposedName.readOnly = true;
      correctionChecks.forEach(check => {
        check.disabled = true;
      });
      structural.forEach(button => button.setAttribute('aria-disabled', 'true'));
      lock(true);
      const run = generation;
      try {
        const result = await request(
          `/arrangements/${encodeURIComponent(ref.id)}/collection-correction`,
          { method: 'POST', body: JSON.stringify(payload) }
        );
        if (!current() || run !== generation) return;
        if (
          !arrangementPresentation(result) ||
          result.arrangement.id !== ref.id ||
          !result.collection_correction ||
          result.collection_receipt
        )
          throw new Error('Unconfirmed correction');
        settled = true;
        review = null;
        body.replaceChildren();
        renderArrangementHistory(body, result);
        onRevised();
        status.textContent =
          'Correction saved as a new proposal revision. No collection changes applied. Reopen saved history and request a fresh review before applying.';
      } catch (_) {
        if (current() && run === generation) {
          settled = true;
          review = null;
          choice.disabled = true;
          select.disabled = true;
          previous.disabled = true;
          next.disabled = true;
          save.setAttribute('aria-disabled', 'true');
          status.textContent =
            'Correction outcome could not be confirmed. Your entered correction remains shown, not approved. Reopen saved history before another request; nothing is retried automatically.';
        }
      } finally {
        lock(false);
      }
    };
    save.addEventListener('click', async () => {
      if (pending || settled || !current() || review !== page || !dirty()) return;
      const payload = {
        review_token: page.review_token,
        operation: page.operation,
        exclude_ids: [...excluded]
      };
      if (added.size) payload.add_ids = [...added.keys()];
      if (proposedName && proposedName.value !== effect.after_name)
        payload.name = proposedName.value;
      await submit(payload);
    });
    choice.addEventListener('change', () =>
      confirm.setAttribute('aria-disabled', String(!choice.checked || pending || dirty()))
    );
    const load = async (operation, offset) => {
      if (pending || !current() || settled) return;
      if (dirty()) {
        select.value = String(page.operation);
        status.textContent = 'Save or discard the unsaved correction before changing pages.';
        return;
      }
      lock(true);
      choice.checked = false;
      confirm.setAttribute('aria-disabled', 'true');
      const run = generation;
      try {
        const value = await request(
          `/arrangements/${encodeURIComponent(ref.id)}/collection-review/${encodeURIComponent(page.review_token)}?operation=${operation}&offset=${offset}`,
          { cache: 'no-store' }
        );
        if (!current() || run !== generation) return;
        const fresh = collectionReviewPage(value, ref.id, page.review_token);
        if (
          !fresh ||
          fresh.catalog_revision !== page.catalog_revision ||
          fresh.operation !== operation ||
          fresh.offset !== offset ||
          JSON.stringify(fresh.effects) !== JSON.stringify(page.effects)
        )
          throw new Error('Changed review');
        review = fresh;
        show(fresh);
      } catch (_) {
        if (current() && run === generation) failed();
      } finally {
        lock(false);
      }
    };
    select.addEventListener('change', () => {
      void load(Number(select.value), 0);
    });
    previous.addEventListener('click', () => {
      if (page.offset > 0) void load(page.operation, page.offset - 50);
    });
    next.addEventListener('click', () => {
      if (page.next_offset != null) void load(page.operation, page.next_offset);
    });
    confirm.addEventListener('click', async () => {
      if (pending || settled || !current() || review !== page || !choice.checked || dirty()) return;
      choice.checked = false;
      confirm.setAttribute('aria-disabled', 'true');
      if (Date.parse(page.expires_at) <= Date.now()) {
        failed();
        return;
      }
      lock(true);
      const run = generation;
      try {
        const result = await request(
          `/arrangements/${encodeURIComponent(ref.id)}/collection-commit`,
          {
            method: 'POST',
            body: JSON.stringify({
              review_token: page.review_token,
              idempotency_key: key,
              confirm: true
            })
          }
        );
        if (!current() || run !== generation) return;
        if (
          !arrangementPresentation(result) ||
          result.arrangement.id !== ref.id ||
          !result.collection_receipt
        )
          throw new Error('Unconfirmed save');
        settled = true;
        review = null;
        body.replaceChildren();
        renderArrangementHistory(body, result);
        onApplied();
        status.textContent = activation
          ? 'Metadata and activation-review selection saved. Resume the saved review queue to review each project independently; no files changed, workspaces were created, or projects activated.'
          : 'Collection metadata saved to canonical Home history. No files changed or workspaces were created or reparented.';
      } catch (_) {
        if (current() && run === generation) failed();
      } finally {
        lock(false);
      }
    });
  };
  start.addEventListener('click', async () => {
    if (pending || settled || review || !current() || !reviewAllowed) return;
    lock(true);
    const run = ++generation;
    body.hidden = true;
    try {
      const result = await request(
        `/arrangements/${encodeURIComponent(ref.id)}/collection-review`,
        {
          method: 'POST',
          body: JSON.stringify({
            expected_revision: ref.catalog_revision,
            expected_record_revision: ref.revision
          })
        }
      );
      if (!current() || run !== generation) return;
      review = collectionReviewPage(result, ref.id);
      if (!review) throw new Error('Invalid review');
      key = globalThis.crypto.randomUUID();
      show(review);
      start.setAttribute('aria-disabled', 'true');
      status.textContent = 'Review prepared. No collection changes have been applied.';
    } catch (_) {
      if (current() && run === generation) failed();
    } finally {
      lock(false);
      if (review) start.setAttribute('aria-disabled', 'true');
    }
  });
  decline.addEventListener('click', async () => {
    if (pending || settled || !current()) return;
    if (unsaved()) {
      status.textContent = 'Save or discard the unsaved correction before declining the proposal.';
      return;
    }
    lock(true);
    const run = ++generation;
    try {
      const result = await request(`/arrangements/${encodeURIComponent(ref.id)}/decline`, {
        method: 'POST',
        body: JSON.stringify({
          expected_revision: review?.catalog_revision || ref.catalog_revision,
          expected_record_revision: ref.revision
        })
      });
      if (!current() || run !== generation) return;
      if (
        !arrangementPresentation(result) ||
        result.arrangement.id !== ref.id ||
        result.arrangement.state !== 'cancelled' ||
        result.arrangement.revision !== ref.revision ||
        result.collection_receipt
      )
        throw new Error('Unconfirmed decline');
      settled = true;
      review = null;
      body.hidden = false;
      body.replaceChildren();
      renderArrangementHistory(body, result);
      onRevised();
      status.textContent =
        'Proposal declined. Its history is retained; no collection changes or project creation were applied.';
    } catch (_) {
      if (current() && run === generation) {
        failed();
        status.textContent =
          'Decline outcome could not be confirmed. Reopen saved history before another action; nothing is retried automatically.';
      }
    } finally {
      lock(false);
    }
  });
  reopen.addEventListener('click', async () => {
    if (pending || !current()) return;
    generation++;
    review = null;
    settled = true;
    body.replaceChildren();
    body.hidden = true;
    await reload();
  });
}
