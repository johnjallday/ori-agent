import { arrangementID } from './workspace-arrangement.js';

// exactName is how a saved entry is named on screen: its name, plus a short
// form of its saved ID only when another entry in the same list shares it.
export function exactName(member, members) {
  const twins = members.filter(other => other.name === member.name).length;
  return twins > 1 ? `${member.name} (#${String(member.id).slice(0, 8)})` : member.name;
}

export function collectionCandidatePage(value, page, query, offset) {
  if (
    !value ||
    value.arrangement_id !== page.arrangement_id ||
    value.review_token !== page.review_token ||
    value.catalog_revision !== page.catalog_revision ||
    value.operation !== page.operation ||
    value.query !== query ||
    value.offset !== offset ||
    !Number.isSafeInteger(value.total) ||
    value.total < offset ||
    value.total > 5000 ||
    !Array.isArray(value.members) ||
    value.members.length !== Math.min(20, value.total - offset) ||
    !value.members.every(
      m =>
        m &&
        arrangementID(m.id) &&
        typeof m.name === 'string' &&
        m.name.length <= 512 &&
        typeof m.availability === 'string' &&
        m.availability.length <= 160
    ) ||
    new Set(value.members.map(m => m.id)).size !== value.members.length ||
    (value.next_offset != null && value.next_offset !== offset + value.members.length) ||
    offset + value.members.length < value.total !== (value.next_offset != null)
  )
    return null;
  return value;
}

// Explicit paged picker: no automatic loading, discovery, model call or save.
export function mountCollectionCandidates(
  container,
  { page, request, added, isCurrent, isBusy, setBusy, changed, failed }
) {
  const node = (tag, text) => {
    const el = container.ownerDocument.createElement(tag);
    el.textContent = text;
    if (tag === 'button') el.className = 'btn btn-sm btn-outline-secondary';
    return el;
  };
  const section = node('section', '');
  section.setAttribute('aria-label', 'Add saved catalog members');
  section.className = 'border rounded p-2 my-2 text-break';
  const label = node('label', 'Find saved catalog members by name');
  const query = node('input', '');
  query.type = 'search';
  query.maxLength = 160;
  query.className = 'form-control form-control-sm';
  label.append(query);
  const search = node('button', 'Find saved members');
  search.type = 'button';
  const status = node(
    'p',
    'Global exclusions and unresolved selections are not offered. Adding references never scans or activates projects. Select up to 50 additions per revision.'
  );
  status.setAttribute('role', 'status');
  status.tabIndex = -1;
  const results = node('div', '');
  const selected = node('ul', '');
  selected.setAttribute('aria-label', 'Unsaved member additions');
  const activation = page.effects[page.operation]?.kind === 'select_activation';
  section.append(
    node(
      'p',
      activation
        ? 'Choose up to 50 additions per revision. Already-linked projects are not offered. Preparing this selection will not approve project creation.'
        : 'Choose up to 50 additions per revision. These are logical references, not new workspaces.'
    ),
    label,
    search,
    status,
    results,
    selected
  );
  container.append(section);
  const selectionChanged = () => {
    selected.replaceChildren();
    const additions = [...added].map(([id, name]) => ({ id, name }));
    for (const addition of additions) {
      const { id } = addition;
      const row = node('li', `${exactName(addition, additions)} `);
      const remove = node('button', 'Undo addition');
      remove.type = 'button';
      remove.addEventListener('click', () => {
        if (!isCurrent() || isBusy()) return;
        added.delete(id);
        results.querySelectorAll('input').forEach(input => {
          if (input.value === id) input.checked = false;
        });
        selectionChanged();
        search.focus?.();
      });
      row.append(remove);
      selected.append(row);
    }
    changed();
  };
  const load = async (term, offset) => {
    if (!isCurrent() || isBusy()) return;
    setBusy(true);
    query.readOnly = true;
    try {
      const value = await request(
        `/arrangements/${encodeURIComponent(page.arrangement_id)}/collection-review/${encodeURIComponent(page.review_token)}/candidates?operation=${page.operation}&offset=${offset}&query=${encodeURIComponent(term)}`,
        { cache: 'no-store' }
      );
      if (!isCurrent() || !section.isConnected) return;
      const fresh = collectionCandidatePage(value, page, term, offset);
      if (!fresh) throw new Error('Changed catalog candidates');
      results.replaceChildren();
      status.textContent = `${offset + (fresh.total ? 1 : 0)}–${offset + fresh.members.length} of ${fresh.total} eligible saved entries. Global exclusions and unresolved selections are not offered.`;
      status.focus?.({ preventScroll: true });
      for (const member of fresh.members) {
        const item = node('label', '');
        item.className = 'd-block';
        const check = node('input', '');
        check.type = 'checkbox';
        check.value = member.id;
        check.checked = added.has(member.id);
        check.addEventListener('change', () => {
          if (!isCurrent() || isBusy() || (check.checked && added.size >= 50)) {
            check.checked = added.has(member.id);
            return;
          }
          if (check.checked) added.set(member.id, member.name);
          else added.delete(member.id);
          selectionChanged();
        });
        item.append(
          check,
          node('span', ` Add ${exactName(member, fresh.members)} · ${member.availability}`)
        );
        results.append(item);
      }
      for (const [text, target] of [
        ['Previous candidates', offset ? offset - 20 : null],
        ['Next candidates', fresh.next_offset]
      ]) {
        const button = node('button', text);
        button.type = 'button';
        button.disabled = target == null;
        button.addEventListener('click', () => {
          if (target != null) void load(term, target);
        });
        results.append(button);
      }
    } catch (_) {
      if (isCurrent() && section.isConnected) {
        status.textContent =
          'Saved member candidates could not be verified. Reopen saved history; unsaved choices are not applied.';
        failed();
      }
    } finally {
      setBusy(false);
      query.readOnly = false;
      changed();
    }
  };
  search.addEventListener('click', () => {
    void load(query.value, 0);
  });
}
