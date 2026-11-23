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
    const error = new Error(
      text.length < 140 && !text.includes('<') && text
        ? text
        : 'Search is unavailable. Please try again.'
    );
    error.status = res.status;
    throw error;
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
export function mountFileSearch(root, compact = false) {
  const $ = id => root.querySelector(compact ? `[data-file-role="${id}"]` : `#${id}`),
    term = $('term'),
    notice = $('notice');
  let version = 0,
    timer,
    next = '',
    hits = [],
    sources = [],
    selected = '',
    scope = '',
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
    if ($('selectedFile')) {
      $('selectedFile').replaceChildren();
      $('selectedFile').hidden = true;
    }
  }
  function invalidate() {
    clear();
    sources = [];
    $('sources')?.replaceChildren();
    $('coverage').textContent = 'Checking current sources…';
    void query();
  }
  async function preference(target) {
    await request('preference', { expected: document.body.dataset.landing, next: target });
    channel?.postMessage('landing');
    location.replace(target === 'search' ? '/search' : '/?home=1');
  }
  async function choose(kind, button) {
    if (kind === 'documents' || kind === 'downloads') {
      const dialog = $('folderConsent');
      $('consentTitle').textContent =
        `Allow Ori to search ${kind === 'documents' ? 'Documents' : 'Downloads'}?`;
      // Escape leaves returnValue unchanged, including after an earlier approval.
      dialog.returnValue = '';
      dialog.showModal();
      await new Promise(resolve => dialog.addEventListener('close', resolve, { once: true }));
      if (dialog.returnValue !== 'allow') {
        button.focus();
        return;
      }
    }
    const { intent } = await request('begin', { kind });
    let source;
    try {
      source = await request('select', { intent });
    } catch (error) {
      if (error.status !== 503) throw error;
      throw new Error(
        kind === 'custom'
          ? 'The folder chooser or selected folder is unavailable. Try Documents or Downloads.'
          : 'That folder is missing or Ori cannot access it. Try Choose another folder.'
      );
    }
    if (!source) {
      notice.textContent = 'No folder chosen.';
      button.focus();
      return;
    }
    if (document.body.dataset.landing === 'first') {
      channel?.postMessage('landing');
      location.replace('/');
      return;
    }
    channel?.postMessage('changed');
    term.focus();
    await query();
  }
  function map(ss) {
    const same = JSON.stringify(sources) === JSON.stringify(ss);
    sources = ss;
    if (compact || (same && ss.length)) return;
    const names = scopeNames(ss);
    $('scope').replaceChildren(
      new Option('All locations', ''),
      ...ss.map(s => new Option(names.get(s.id), s.id))
    );
    if (scope && !names.has(scope))
      $('scope').add(new Option('Location no longer available', scope));
    $('scope').value = scope;
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
        const title = element('strong', h.name);
        if (!compact) {
          const pick = act('', () => {
            select(h);
            term.focus();
          });
          pick.className = 'hit-name';
          pick.setAttribute('aria-pressed', String(h.id === selected));
          pick.append(title);
          li.append(pick);
        } else li.append(title);
        li.append(
          element(
            'span',
            `${names.get(h.source)}${h.parent ? ' / ' + h.parent : ''} · ${new Date(h.modified).toLocaleString()}`
          )
        );
        if (compact) addActions(li, h);
        return li;
      })
    );
    $('more').hidden = !next;
    if (!compact) {
      const hit = hits.find(h => h.id === selected),
        panel = $('selectedFile');
      panel.hidden = !hit;
      panel.replaceChildren();
      if (hit) {
        panel.append(
          element('small', 'SELECTED FILE'),
          element('h2', hit.name),
          element('p', [names.get(hit.source), hit.parent].filter(Boolean).join(' / '))
        );
        addActions(panel, hit);
      }
    }
  }
  function addActions(host, hit) {
    if (hit.reader) host.append(act(`Open in ${hit.reader}`, () => dispatch(hit, 'open')));
    host.append(act('Reveal in Finder', () => dispatch(hit, 'reveal')));
  }
  function select(hit) {
    selected = hit.id;
    render();
    notice.textContent = `Selected ${hit.name}, ${[scopeNames(sources).get(hit.source), hit.parent].filter(Boolean).join(' / ')}`;
  }
  async function query(append = false) {
    clearTimeout(timer);
    if (compact && root.closest('[hidden]')) return;
    const stamp = ++version,
      value = term.value;
    try {
      const p = await request('query', {
        term: value,
        ...(scope ? { source: scope } : {}),
        cursor: append ? next : '',
        limit: 25
      });
      if (stamp !== version) return;
      const updated = append ? [...hits, ...p.hits] : p.hits;
      const changed = JSON.stringify(updated) !== JSON.stringify(hits) || next !== (p.next || '');
      hits = updated;
      next = p.next || '';
      visibleTerm = value;
      map(p.sources);
      if (!hits.some(h => h.id === selected)) selected = '';
      if (changed) render();
      if ($('retry')) $('retry').hidden = true;
      const included = sources.filter(s => !scope || s.id === scope);
      const labels = scopeNames(sources);
      const names = included.map(s => labels.get(s.id)).join(', ');
      const partial = included.some(s => s.state !== 'ready');
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
      $('sources')?.replaceChildren();
      if (!compact) {
        $('retry').hidden = false;
        $('emptyMap').textContent = 'Locations unavailable.';
        if (scope && e.status === 409) {
          try {
            const current = await request('sources');
            if (version !== stamp + 1) return;
            map(current);
          } catch {
            /* Results remain unavailable; never widen the scope. */
          }
        }
      }
      $('coverage').textContent = e.message;
      notice.textContent = 'Could not load current results. Retry the search.';
    }
  }
  $('choices')?.addEventListener('click', event => {
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
    notice.textContent = '';
    $('coverage').textContent = 'Searching…';
    timer = setTimeout(() => query(), 60);
  });
  term.addEventListener('keydown', e => {
    if (visibleTerm !== term.value) return;
    let i = hits.findIndex(h => h.id === selected);
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && hits.length) {
      e.preventDefault();
      i = Math.min(hits.length - 1, Math.max(0, i + (e.key === 'ArrowDown' ? 1 : -1)));
      select(hits[i]);
    }
    if (e.key === 'Enter' && i >= 0) {
      e.preventDefault();
      void run(() => dispatch(hits[i], e.shiftKey || !hits[i].reader ? 'reveal' : 'open'), term);
    }
  });
  $('scope')?.addEventListener('change', () => {
    scope = $('scope').value;
    clear();
    notice.textContent = '';
    void query();
  });
  $('retry')?.addEventListener('click', () => query());
  $('more').addEventListener('click', () => query(true));
  if (!compact)
    window.addEventListener('keydown', e => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        term.focus();
      }
    });
  channel?.addEventListener('message', e => {
    if (!compact && e.data === 'landing') {
      location.reload();
      return;
    }
    invalidate();
  });
  window.addEventListener('pageshow', () => invalidate());
  window.addEventListener('focus', () => query());
  window.addEventListener('pagehide', () => clear());
  if (!compact && document.body.dataset.landing === 'search') term.focus();
  void query();
  return {
    show() {
      invalidate();
      term.focus();
    },
    hide() {
      term.value = '';
      clear();
      notice.textContent = '';
    }
  };
}
if (typeof document !== 'undefined' && document.getElementById('desk')) {
  const picker = document.getElementById('destinationPicker');
  picker?.addEventListener('change', () => {
    if (picker.value === '/?view=map') location.assign('/?view=map');
  });
  window.addEventListener('pageshow', () => {
    if (picker) picker.selectedIndex = 0;
  });
  mountFileSearch(document.getElementById('desk'));
}
