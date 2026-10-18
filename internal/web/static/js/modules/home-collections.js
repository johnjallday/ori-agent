import { arrangementID } from './workspace-arrangement.js';

const integer = (n, max) => Number.isSafeInteger(n) && n >= 0 && n <= max;
const text = (s, max) => typeof s === 'string' && s.length <= max;
const card = c =>
  c &&
  arrangementID(c.id) &&
  text(c.name, 128) &&
  c.name.length > 0 &&
  Number.isSafeInteger(c.revision) &&
  c.revision > 0 &&
  integer(c.members, 5000) &&
  (c.arrangement_id == null || arrangementID(c.arrangement_id));

export function collectionCatalogPage(value, { pin = null, id = null, offset = 0 } = {}) {
  const size = id ? 50 : 20;
  if (
    !value ||
    !/^[a-f0-9]{64}$/.test(value.context_pin) ||
    (pin && value.context_pin !== pin) ||
    !Number.isSafeInteger(value.revision) ||
    value.revision < 1 ||
    value.offset !== offset ||
    !integer(offset, id ? 5000 : 100) ||
    offset % size ||
    !integer(value.total, id ? 5000 : 100)
  )
    return null;
  const rows = id ? value.members : value.collections;
  if (
    !Array.isArray(rows) ||
    rows.length !== Math.min(size, value.total - offset) ||
    new Set(rows.map(row => row?.id)).size !== rows.length ||
    offset + rows.length < value.total !== (value.next_offset != null) ||
    (value.next_offset != null && value.next_offset !== offset + rows.length)
  )
    return null;
  if (id) {
    if (
      !card(value.collection) ||
      value.collection.id !== id ||
      value.collection.members !== value.total ||
      !rows.every(
        row =>
          row &&
          arrangementID(row.id) &&
          text(row.name, 512) &&
          text(row.format, 160) &&
          ['available', 'unavailable', 'ambiguous', 'revoked_source', 'not_scanned'].includes(
            row.last_observed_availability
          ) &&
          (row.observed_at == null ||
            (typeof row.observed_at === 'string' && Number.isFinite(Date.parse(row.observed_at))))
      )
    )
      return null;
  } else if (!rows.every(card)) return null;
  return value;
}

// A catalog browser, never a selector that can approve activation or membership.
export function mountHomeCollections(container, { request, isCurrent }) {
  const document = container.ownerDocument;
  const node = (tag, text, cls = '') => {
    const el = document.createElement(tag);
    el.textContent = text;
    el.className = cls;
    return el;
  };
  const button = (text, action) => {
    const el = node('button', text);
    el.type = 'button';
    el.addEventListener('click', () => {
      if (!busy && current()) void action(el);
    });
    return el;
  };
  container.className = `${container.className || ''} home-collections`;
  container.replaceChildren();
  let epoch = 0,
    busy = false,
    listOffset = 0;
  const current = () => container.isConnected && isCurrent();
  const refresh = button('Refresh collections', el => load(null, 0, null, el));
  const status = node('p', '', 'home-collections__status');
  status.setAttribute('role', 'status');
  const content = node('div', '');
  container.append(refresh, status, content);
  const invalidate = () => {
    epoch++;
    busy = false;
    content.replaceChildren();
    status.textContent = '';
    refresh.setAttribute('aria-disabled', 'false');
  };
  const load = async (id, offset, pin, initiator = null) => {
    if (!current()) return;
    const run = ++epoch;
    busy = true;
    refresh.setAttribute('aria-disabled', 'true');
    status.textContent = 'Loading saved collection metadata…';
    try {
      const query = pin ? `?context_pin=${encodeURIComponent(pin)}&offset=${offset}` : '';
      const raw = await request(`/collections${id ? `/${encodeURIComponent(id)}` : ''}${query}`, {
        cache: 'no-store'
      });
      if (!current() || run !== epoch) return;
      const page = collectionCatalogPage(raw, { id, pin, offset });
      if (!page) throw new Error('Invalid saved catalog');
      const focus = initiator && document.activeElement === initiator;
      content.replaceChildren();
      const title = node('h4', id ? page.collection.name : 'Saved collections');
      title.tabIndex = -1;
      content.append(title);
      if (id) {
        content.append(node('p', page.collection.id, 'home-collections__identity'));
        content.append(
          button('Back to collections', el => load(null, listOffset, page.context_pin, el))
        );
        const members = node('ul', '', 'home-collections__members');
        for (const member of page.members) {
          const row = node('li', '');
          row.append(
            node('strong', member.name),
            node('span', member.id, 'home-collections__identity')
          );
          const observed = member.observed_at
            ? new Date(member.observed_at).toLocaleString()
            : 'date unknown';
          row.append(
            node(
              'span',
              `${member.format || 'Unknown format'} · Last observed: ${member.last_observed_availability.replaceAll('_', ' ')} · ${observed}`,
              'home-collections__observation'
            )
          );
          members.append(row);
        }
        content.append(members);
        if (!page.total)
          content.append(
            node(
              'p',
              'This collection is empty. No projects were activated or linked by saving it.'
            )
          );
      } else {
        listOffset = offset;
        const cards = node('ul', '', 'home-collections__cards');
        for (const collection of page.collections) {
          const row = node('li', '');
          row.append(
            node('h5', collection.name),
            node(
              'p',
              `${collection.members} saved member${collection.members === 1 ? '' : 's'} · Revision ${collection.revision}`
            ),
            node('span', collection.id, 'home-collections__identity')
          );
          const open = button('View members', el => load(collection.id, 0, page.context_pin, el));
          open.setAttribute('aria-label', `View members: ${collection.name}`);
          row.append(open);
          cards.append(row);
        }
        content.append(cards);
        if (!page.total)
          content.append(
            node(
              'p',
              'No logical collections yet. A confirmed collection-only review can save names and membership here. No files move.'
            )
          );
      }
      const count = node(
        'p',
        `${offset + (page.total ? 1 : 0)}–${offset + (id ? page.members.length : page.collections.length)} of ${page.total} ${id ? 'exact members' : 'saved collections'}.`,
        'home-collections__count'
      );
      content.append(count);
      const size = id ? 50 : 20;
      const previous = button('Previous page', el => {
        if (offset > 0) return load(id, offset - size, page.context_pin, el);
      });
      previous.disabled = offset === 0;
      const next = button('Next page', el => {
        if (page.next_offset != null) return load(id, page.next_offset, page.context_pin, el);
      });
      next.disabled = page.next_offset == null;
      content.append(previous, next);
      status.textContent =
        'Saved catalog metadata only. Availability is last-observed, not a current filesystem check. Collections are references, not folders or workspace parents.';
      if (focus) title.focus?.({ preventScroll: true });
    } catch (_) {
      if (!current() || run !== epoch) return;
      content.replaceChildren();
      status.textContent =
        'Saved collection metadata changed or could not be checked. Refresh collections to read the current catalog. Nothing was changed by this view.';
    } finally {
      if (run === epoch) {
        busy = false;
        refresh.setAttribute('aria-disabled', 'false');
      }
    }
  };
  return {
    invalidate,
    refresh: () => {
      invalidate();
      return load(null, 0, null);
    }
  };
}
