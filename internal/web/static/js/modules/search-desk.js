const base = '/api/file-search/';
export async function request(op, body) {
  const res = await fetch(base + op, {
    method: body === undefined ? 'GET' : 'POST',
    cache: 'no-store',
    headers: { 'X-Ori-Search': '1', 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) })
  });
  if (!res.ok) {
    const text = (await res.text()).trim();
    throw new Error(
      text.length < 140 && !text.includes('<') && text
        ? text
        : 'Search is unavailable. Please try again.'
    );
  }
  return res.status === 204 ? null : res.json();
}
export function scopeNames(sources) {
  return new Map(
    sources.map(s => [
      s.id,
      sources.some(x => x.id !== s.id && x.label === s.label)
        ? `${s.label} · ${s.id.slice(0, 6)}`
        : s.label
    ])
  );
}
function init() {
  const $ = id => document.getElementById(id),
    term = $('term'),
    notice = $('notice');
  let version = 0,
    timer,
    next = '',
    hits = [],
    sources = [],
    selected = '',
    visibleTerm = '',
    busy = false;
  const channel =
    typeof BroadcastChannel === 'function' ? new BroadcastChannel('ori-file-search-v1') : null;
  function element(tag, text) {
    const node = document.createElement(tag);
    node.textContent = text;
    return node;
  }
  function act(label, fn) {
    const b = element('button', label);
    b.type = 'button';
    b.addEventListener('click', () => run(fn, b));
    return b;
  }
  async function run(fn, button) {
    if (busy) return;
    busy = true;
    notice.textContent = '';
    try {
      await fn();
    } catch (e) {
      notice.textContent = e.message;
      button?.focus();
    } finally {
      busy = false;
    }
  }
  function clear() {
    version++;
    clearTimeout(timer);
    hits = [];
    selected = '';
    next = '';
    $('more').hidden = true;
    $('hits').replaceChildren();
  }
  function invalidate() {
    clear();
    sources = [];
    $('sources').replaceChildren();
    $('coverage').textContent = 'Checking current sources…';
    void query();
  }
  async function preference(target) {
    await request('preference', { expected: document.body.dataset.landing, next: target });
    channel?.postMessage('landing');
    location.replace(target === 'search' ? '/search' : '/?home=1');
  }
  async function choose(kind, button) {
    const { intent } = await request('begin', { kind });
    const source = await request('select', { intent });
    if (!source) {
      notice.textContent = 'No folder chosen.';
      button.focus();
      return;
    }
    if (document.body.dataset.landing === 'first') {
      await preference('search');
      return;
    }
    channel?.postMessage('changed');
    term.focus();
    await query();
  }
  function map(ss) {
    const same = JSON.stringify(sources) === JSON.stringify(ss);
    sources = ss;
    if (same && ss.length) return;
    const names = scopeNames(ss);
    $('emptyMap').textContent = ss.length ? '' : 'No folders mapped yet.';
    $('sources').replaceChildren(
      ...ss.map(s => {
        const li = element('li', '');
        li.append(
          element('strong', names.get(s.id)),
          element('span', `${s.state} · ${s.count} files`)
        );
        if (s.reason) li.append(element('span', s.reason));
        if (s.mapped_at)
          li.append(
            element('span', `Last mapped ${new Date(s.mapped_at * 1000).toLocaleString()}`)
          );
        if (!['queued', 'mapping'].includes(s.state))
          li.append(
            act('Refresh', async () => {
              clear();
              await request('refresh', { id: s.id, generation: s.generation });
              channel?.postMessage('changed');
              await query();
            })
          );
        li.append(
          act('Remove from search', async () => {
            await request('remove', { id: s.id });
            channel?.postMessage('changed');
            invalidate();
            term.focus();
          })
        );
        return li;
      })
    );
  }
  async function dispatch(hit, op) {
    await request(op, { id: hit.id, generation: hit.generation });
    notice.textContent = op === 'open' ? 'Sent to the document reader.' : 'Requested in Finder.';
  }
  function render() {
    const names = scopeNames(sources);
    $('hits').replaceChildren(
      ...hits.map(h => {
        const li = element('li', '');
        li.dataset.id = h.id;
        li.classList.toggle('current', h.id === selected);
        li.append(
          element('strong', h.name),
          element(
            'span',
            `${names.get(h.source)}${h.parent ? ' / ' + h.parent : ''} · ${new Date(h.modified).toLocaleString()}`
          )
        );
        if (h.reader) li.append(act(`Open in ${h.reader}`, () => dispatch(h, 'open')));
        li.append(act('Reveal in Finder', () => dispatch(h, 'reveal')));
        return li;
      })
    );
    $('more').hidden = !next;
  }
  async function query(append = false) {
    clearTimeout(timer);
    const stamp = ++version,
      value = term.value;
    try {
      const p = await request('query', { term: value, cursor: append ? next : '', limit: 25 });
      if (stamp !== version) return;
      const updated = append ? [...hits, ...p.hits] : p.hits;
      const changed = JSON.stringify(updated) !== JSON.stringify(hits) || next !== (p.next || '');
      hits = updated;
      next = p.next || '';
      visibleTerm = value;
      map(p.sources);
      if (!hits.some(h => h.id === selected)) selected = '';
      if (changed) render();
      const names = [...scopeNames(sources).values()].join(', ');
      const partial = sources.some(s => s.state !== 'ready');
      $('coverage').textContent =
        (hits.length
          ? `${hits.length} shown`
          : sources.length
            ? value
              ? `No matches in ${names}`
              : `No files in ${names}`
            : 'Choose a folder to search its filenames.') +
        (partial ? ' · Coverage incomplete.' : '');
      if (sources.some(s => ['mapping', 'queued'].includes(s.state)) && !append)
        timer = setTimeout(() => query(), 250);
    } catch (e) {
      if (stamp !== version) return;
      clear();
      sources = [];
      $('sources').replaceChildren();
      $('emptyMap').textContent = 'Map unavailable.';
      $('coverage').textContent = e.message;
      notice.textContent = 'Could not load current results. Retry the search.';
    }
  }
  $('choices').addEventListener('click', event => {
    const b = event.target.closest('[data-kind]');
    if (b) void run(() => choose(b.dataset.kind, b), b);
  });
  for (const [id, target] of [
    ['skip', 'later'],
    ['homeDefault', 'later'],
    ['searchDefault', 'search']
  ])
    $(id)?.addEventListener('click', () => run(() => preference(target), $(id)));
  term.addEventListener('input', () => {
    clear();
    $('coverage').textContent = 'Searching…';
    timer = setTimeout(() => query(), 60);
  });
  term.addEventListener('keydown', e => {
    if (visibleTerm !== term.value) return;
    let i = hits.findIndex(h => h.id === selected);
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && hits.length) {
      e.preventDefault();
      i = Math.min(hits.length - 1, Math.max(0, i + (e.key === 'ArrowDown' ? 1 : -1)));
      selected = hits[i].id;
      render();
      notice.textContent = `Selected ${hits[i].name}, ${hits[i].parent || 'source folder'}`;
    }
    if (e.key === 'Enter' && i >= 0) {
      e.preventDefault();
      void run(() => dispatch(hits[i], e.shiftKey || !hits[i].reader ? 'reveal' : 'open'), term);
    }
  });
  $('more').addEventListener('click', () => query(true));
  window.addEventListener('keydown', e => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      term.focus();
    }
  });
  channel?.addEventListener('message', e => {
    if (e.data === 'landing') {
      location.reload();
      return;
    }
    invalidate();
  });
  window.addEventListener('pageshow', () => invalidate());
  window.addEventListener('focus', () => query());
  window.addEventListener('pagehide', () => clear());
  if (document.body.dataset.landing === 'search') term.focus();
  void query();
}
if (typeof document !== 'undefined' && document.getElementById('desk')) init();
