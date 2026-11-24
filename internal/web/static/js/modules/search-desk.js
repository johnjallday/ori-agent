import { destinationPlace } from './destination-place.js';

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
  const place = compact ? null : destinationPlace('desk');
  let restoring = false,
    leaving = false,
    pending = null;
  function snapshot() {
    if (pending && pending.term === term.value && pending.source === scope) return pending;
    const hit = hits.find(h => h.id === selected);
    return {
      term: term.value,
      source: scope,
      selected: hit?.id || '',
      generation: hit?.generation || 0,
      pages: Math.max(1, Math.ceil(hits.length / 25))
    };
  }
  // A browser may discard its unload request. Checkpoint the last settled
  // descriptor while the document is live; never send hits or path strings.
  let lastCheckpoint = '';
  function checkpoint() {
    if (!place || leaving) return;
    const descriptor = snapshot(),
      key = JSON.stringify(descriptor);
    if (lastCheckpoint !== key) {
      lastCheckpoint = key;
      void place.save(descriptor);
    }
  }
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
    pending = null;
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
  function invalidate(saved = snapshot()) {
    clear();
    sources = [];
    $('sources')?.replaceChildren();
    $('coverage').textContent = 'Checking current sources…';
    void query(false, saved);
  }
  async function preference(target) {
    await request('preference', { expected: document.body.dataset.landing, next: target });
    channel?.postMessage('landing');
    location.replace(target === 'search' ? '/search' : '/?home=1');
  }
  async function setupReviewedFolder(review) {
    const { reviewExisting, openFolderReview } = await import('./folder-search-review.js');
    if (!review.matches?.length) {
      openFolderReview(review);
      return;
    }
    const connection = await reviewExisting(review);
    if (!connection) return;
    scope = connection.source;
    term.value = '';
    channel?.postMessage('changed');
    await query();
    term.focus();
  }
  async function choose(kind, button) {
    if (document.body.dataset.workspaceReady === 'true') {
      const { chooseSearchFolder } = await import('./folder-search-review.js');
      const review = await chooseSearchFolder(kind);
      if (review) await setupReviewedFolder(review);
      else {
        notice.textContent = 'No folder chosen.';
        button.focus();
      }
      return;
    }
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
    if (compact) return;
    const names = scopeNames(ss);
    if (document.body.dataset.landing !== 'first')
      $('title').textContent = scope ? names.get(scope) || 'Unavailable location' : 'All locations';
    if (same && ss.length) return;
    const all = { id: '', count: ss.reduce((n, s) => n + s.count, 0) };
    $('locationList').replaceChildren(
      ...[all, ...ss].map(s => {
        const label = names.get(s.id) || 'All locations';
        const button = element('button', '');
        button.type = 'button';
        button.dataset.scope = s.id;
        button.setAttribute('aria-label', `Search in ${label}`);
        button.setAttribute('aria-pressed', String(scope === s.id));
        const icon = element('span', s.id ? '' : '▱');
        if (s.id) icon.className = 'location-icon';
        icon.setAttribute('aria-hidden', 'true');
        const name = element('span', label);
        name.className = 'rail-label';
        const count = element('span', String(s.count));
        count.className = 'rail-count';
        button.append(icon, name, count);
        return button;
      })
    );
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
        if (document.body.dataset.workspaceReady === 'true' && s.state !== 'unavailable') {
          if (s.workspace) {
            const open = element('a', `Open ${s.workspace.name}`);
            open.href = `/workspaces/${encodeURIComponent(s.workspace.slug)}`;
            li.append(open);
          } else
            li.append(
              act('Connect to workspace', async () =>
                setupReviewedFolder(await request('review-source', { id: s.id }))
              )
            );
        }
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
          pick.className = 'file-row';
          pick.setAttribute('aria-pressed', String(h.id === selected));
          const file = element('span', '');
          file.className = 'fname';
          const ext = h.name
            .match(/\.([a-z0-9]{1,6})$/i)?.[1]
            .slice(0, 4)
            .toUpperCase();
          const badge = element('span', ext || 'FILE');
          badge.className = 'badge';
          badge.setAttribute('aria-hidden', 'true');
          file.append(badge, title);
          const path = element('span', [names.get(h.source), h.parent].filter(Boolean).join(' / '));
          path.className = 'path';
          const date = element(
            'time',
            new Date(h.modified).toLocaleDateString(undefined, {
              year: 'numeric',
              month: 'short',
              day: 'numeric'
            })
          );
          date.className = 'date';
          date.dateTime = h.modified;
          date.title = new Date(h.modified).toLocaleString();
          pick.append(file, path, date);
          li.append(pick);
        } else {
          li.append(title);
          li.append(
            element(
              'span',
              `${names.get(h.source)}${h.parent ? ' / ' + h.parent : ''} · ${new Date(h.modified).toLocaleString()}`
            )
          );
          addActions(li, h);
        }
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
        const info = element('div', ''),
          buttons = element('div', '');
        buttons.className = 'actions';
        const path = element(
          'p',
          [names.get(hit.source), hit.parent, hit.name].filter(Boolean).join(' / ')
        );
        path.className = 'path';
        info.append(element('small', 'Selected file'), element('strong', hit.name), path);
        addActions(buttons, hit);
        panel.append(info, buttons);
      }
    }
  }
  function addActions(host, hit) {
    if (hit.reader) host.append(act(`Open in ${hit.reader}`, () => dispatch(hit, 'open')));
    host.append(act('Reveal in Finder', () => dispatch(hit, 'reveal')));
  }
  function select(hit) {
    selected = hit.id;
    checkpoint();
    render();
    notice.textContent = `Selected ${hit.name}, ${[scopeNames(sources).get(hit.source), hit.parent].filter(Boolean).join(' / ')}`;
  }
  async function query(append = false, saved = null) {
    clearTimeout(timer);
    if (compact && root.closest('[hidden]')) return;
    pending = saved;
    const stamp = ++version,
      value = term.value;
    try {
      const updated = append ? [...hits] : [];
      let cursor = append ? next : '',
        p;
      const pages = saved?.pages || 1;
      for (let i = 0; i < pages; i++) {
        p = await request('query', {
          term: value,
          ...(scope ? { source: scope } : {}),
          cursor,
          limit: 25
        });
        if (stamp !== version) return;
        updated.push(...p.hits);
        cursor = p.next;
        if (!cursor) break;
      }
      const changed = JSON.stringify(updated) !== JSON.stringify(hits) || next !== (p.next || '');
      pending = null;
      hits = updated;
      next = p.next || '';
      visibleTerm = value;
      map(p.sources);
      if (saved?.selected) {
        selected =
          hits.find(h => h.id === saved.selected && h.generation === saved.generation)?.id || '';
        notice.textContent = selected
          ? 'Selection restored.'
          : 'The selected file is no longer in these results.';
      } else if (!hits.some(h => h.id === selected)) selected = '';
      if (changed || saved) render();
      checkpoint();
      if ($('retry')) $('retry').hidden = true;
      const included = sources.filter(s => !scope || s.id === scope);
      const labels = scopeNames(sources);
      const names = included.map(s => labels.get(s.id)).join(', ');
      const partial = included.some(s => s.state !== 'ready');
      $('coverage').textContent =
        (hits.length
          ? !compact && !value && !next && !partial
            ? `${hits.length} ${hits.length === 1 ? 'file' : 'files'} ${scope ? 'in ' + names : 'across ' + included.length + ' ' + (included.length === 1 ? 'location' : 'locations')}`
            : `${hits.length} shown`
          : sources.length
            ? value
              ? `No matches in ${names}`
              : `No files in ${names}`
            : 'Choose a folder to search its filenames.') +
        (partial ? ' · Coverage incomplete.' : '');
      if (sources.some(s => ['mapping', 'queued'].includes(s.state)) && !append)
        timer = setTimeout(() => query(false, snapshot()), 250);
    } catch (e) {
      if (stamp !== version) return;
      clear();
      sources = [];
      $('sources')?.replaceChildren();
      if (!compact) {
        map([]);
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
      notice.textContent =
        scope && e.status === 409
          ? 'This location changed. Select another location to search.'
          : 'Could not load current results. Retry the search.';
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
  function changeScope(value) {
    scope = value;
    $('scope').value = scope;
    for (const button of $('locationList').querySelectorAll('[data-scope]'))
      button.setAttribute('aria-pressed', String(button.dataset.scope === scope));
    clear();
    notice.textContent = '';
    void query();
  }
  $('scope')?.addEventListener('change', () => changeScope($('scope').value));
  $('locationList')?.addEventListener('click', e => {
    const b = e.target.closest('[data-scope]');
    if (b) changeScope(b.dataset.scope);
  });
  $('addLocation')?.addEventListener('click', () => {
    $('manage').open = true;
    $('choices').querySelector('button').focus();
  });
  $('retry')?.addEventListener('click', () => query());
  $('more').addEventListener('click', () => query(true));
  if (!compact)
    window.addEventListener('keydown', e => {
      if ($('folderConsent')?.open) return;
      if (
        ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') ||
        (e.key === '/' && e.target !== term && !e.metaKey && !e.ctrlKey && !e.altKey)
      ) {
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
  async function arrive() {
    leaving = false;
    restoring = true;
    clear();
    const stamp = version;
    const saved = await place?.read();
    restoring = false;
    if (version !== stamp || leaving) return;
    if (saved) {
      term.value = saved.term;
      scope = saved.source;
    }
    await query(false, saved);
  }
  window.addEventListener('pageshow', e => {
    if (e.persisted) void arrive();
  });
  window.addEventListener('focus', () => {
    if (!leaving && !restoring) invalidate();
  });
  window.addEventListener('pagehide', () => {
    if (!restoring && !leaving) void place?.save(snapshot());
    leaving = true;
    clear();
  });
  if (!compact && document.body.dataset.landing !== 'first') term.focus();
  void arrive();
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
