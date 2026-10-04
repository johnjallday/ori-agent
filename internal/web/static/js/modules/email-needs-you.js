// email-needs-you.js — the "Needs you" panel on a workspace with a linked
// mailbox (Email Ops).
//
// GET /api/workspaces/{id}/email/needs-you returns the inbox sorted into what
// needs the user, what is worth knowing, and what can be ignored, each with a
// one-line reason. The user's own call ("Not important", "This needs me") and
// "Track" go to …/needs-you/mark and …/needs-you/track, then the list is read
// again (the server caches the mailbox read, so this is quick).
//
// Every workspace page asks; one without a mailbox answers {"linked": false}
// and the panel stays hidden. Pure helpers are exported for
// email-needs-you.test.js.

const LOADING_DELAY_MS = 300;

const KIND_LABELS = Object.freeze({
  reply: 'Reply',
  deadline: 'Deadline',
  decision: 'Decision',
  info: 'FYI'
});

export function kindLabel(kind) {
  return Object.hasOwn(KIND_LABELS, kind) ? KIND_LABELS[kind] : '';
}

// ageLabel is a compact "how long ago": 5m, 3h, 2d.
export function ageLabel(now, iso) {
  const then = Date.parse(iso || '');
  if (!Number.isFinite(then)) return '';
  const minutes = Math.max(0, Math.round((now - then) / 60000));
  if (minutes < 60) return `${Math.max(1, minutes)}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

// listView turns the server list into what the panel draws.
export function listView(list) {
  const needs = Array.isArray(list?.needs_you) ? list.needs_you : [];
  const fyi = Array.isArray(list?.fyi) ? list.fyi : [];
  const ignorable = Array.isArray(list?.ignorable) ? list.ignorable : [];
  return {
    needs,
    fyi,
    ignorable,
    empty: needs.length === 0,
    count: needs.length,
    note:
      list && list.explained === false && needs.length > 0
        ? 'These reasons come from Ori’s rules. With an AI model set in Settings, Ori explains each one.'
        : ''
  };
}

// Answers that mean "no list belongs here" rather than "the list failed".
const HIDDEN_CODES = new Set(['not_linked', 'workspace_unavailable', 'unavailable']);

// errorView explains a failed read, with the one action that fixes it.
// hidden is true when the workspace has no mailbox to list.
export function errorView(status, body) {
  const code = String(body?.error || '');
  if (status >= 400 && HIDDEN_CODES.has(code)) return { hidden: true };
  const message =
    String(body?.message || '').trim() ||
    'Ori couldn’t read your mail just now. Try again in a moment.';
  switch (code) {
    case 'vault_locked':
      return { message, action: { label: 'Open Vaults', href: '/vaults' } };
    case 'reconnect':
    case 'account_unavailable':
      return { message, action: { label: 'Set up email', href: '/?setup=email' } };
    default:
      return { message, action: { label: 'Try again', retry: true } };
  }
}

function el(tag, attrs = {}, text) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === false || value === null || value === undefined) continue;
    if (key === 'className') node.className = value;
    else node.setAttribute(key, value === true ? '' : String(value));
  }
  if (text !== undefined) node.textContent = text;
  return node;
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch (_) {
    return {};
  }
}

// createNeedsYouPanel binds the panel to one workspace. fetch is injectable.
export function createNeedsYouPanel(root, workspaceId, deps = {}) {
  const fetchImpl = deps.fetchImpl || ((...args) => globalThis.fetch(...args));
  const now = deps.now || (() => Date.now());
  const base = `/api/workspaces/${encodeURIComponent(workspaceId)}/email/needs-you`;
  let generation = 0;
  let busyThread = '';

  function header(count) {
    const head = el('div', { className: 'needs-you__head' });
    const title = el('h2', { className: 'needs-you__title', id: 'needsYouTitle' }, 'Needs you');
    if (count > 0) title.append(el('span', { className: 'needs-you__count' }, String(count)));
    const refresh = el(
      'button',
      { type: 'button', className: 'btn btn-sm btn-outline-secondary needs-you__refresh' },
      'Refresh'
    );
    refresh.addEventListener('click', () => void load());
    head.append(title, refresh);
    return head;
  }

  function renderLoading() {
    root.textContent = '';
    root.append(
      header(0),
      el('p', { className: 'needs-you__status', role: 'status' }, 'Reading your inbox…')
    );
    root.hidden = false;
  }

  function renderError(view) {
    if (view.hidden) {
      root.hidden = true;
      root.textContent = '';
      return;
    }
    root.textContent = '';
    root.append(header(0));
    const box = el('div', { className: 'needs-you__problem', role: 'alert' });
    box.append(el('p', {}, view.message));
    if (view.action?.href) {
      box.append(
        el('a', { href: view.action.href, className: 'btn btn-sm btn-primary' }, view.action.label)
      );
    } else if (view.action?.retry) {
      const retry = el(
        'button',
        { type: 'button', className: 'btn btn-sm btn-primary' },
        view.action.label
      );
      retry.addEventListener('click', () => void load());
      box.append(retry);
    }
    root.append(box);
    root.hidden = false;
  }

  function actionButton(label, onClick, item) {
    const button = el(
      'button',
      {
        type: 'button',
        className: 'btn btn-sm btn-link needs-you__action',
        'aria-label': `${label}: ${item.subject || 'this email'}`,
        disabled: busyThread === item.thread_id
      },
      label
    );
    button.addEventListener('click', () => void onClick(item));
    return button;
  }

  function row(item, actions) {
    const li = el('li', { className: 'needs-you__row', 'data-thread': item.thread_id });
    const meta = el('div', { className: 'needs-you__meta' });
    const label = kindLabel(item.kind);
    if (label && item.bucket === 'needs_you') {
      meta.append(
        el('span', { className: `needs-you__kind needs-you__kind--${item.kind}` }, label)
      );
    }
    meta.append(el('span', { className: 'needs-you__from' }, item.from || ''));
    const age = ageLabel(now(), item.last_message_at);
    if (age)
      meta.append(el('time', { className: 'needs-you__age', datetime: item.last_message_at }, age));
    if (item.tracked) meta.append(el('span', { className: 'needs-you__tracked' }, 'Tracked'));
    li.append(meta);
    li.append(el('p', { className: 'needs-you__subject' }, item.subject || '(no subject)'));
    if (item.why) li.append(el('p', { className: 'needs-you__why' }, item.why));
    const bar = el('div', { className: 'needs-you__actions' });
    for (const action of actions) bar.append(action);
    li.append(bar);
    return li;
  }

  function group(title, items, makeActions) {
    const details = el('details', { className: 'needs-you__group' });
    details.append(el('summary', {}, `${title} (${items.length})`));
    const list = el('ul', { className: 'needs-you__list' });
    for (const item of items) list.append(row(item, makeActions(item)));
    details.append(list);
    return details;
  }

  function renderList(list) {
    const view = listView(list);
    root.textContent = '';
    root.append(header(view.count));
    if (view.empty) {
      root.append(el('p', { className: 'needs-you__empty' }, 'Nothing needs you right now.'));
    } else {
      const ul = el('ul', { className: 'needs-you__list' });
      for (const item of view.needs) {
        const actions = [];
        if (!item.tracked) actions.push(actionButton('Track', track, item));
        actions.push(actionButton('Not important', it => mark(it, 'ignorable'), item));
        ul.append(row(item, actions));
      }
      root.append(ul);
    }
    if (view.note) root.append(el('p', { className: 'needs-you__note' }, view.note));
    if (view.fyi.length) {
      root.append(
        group('Worth knowing', view.fyi, item => [
          actionButton('This needs me', it => mark(it, 'needs_you'), item),
          actionButton('Not important', it => mark(it, 'ignorable'), item)
        ])
      );
    }
    if (view.ignorable.length) {
      root.append(
        group('Probably ignorable', view.ignorable, item => [
          actionButton('This needs me', it => mark(it, 'needs_you'), item)
        ])
      );
    }
    root.hidden = false;
  }

  // load reads the list. loading is when to show "Reading your inbox…":
  // 'now' (Refresh), 'late' (only if the read is slow, so a workspace without a
  // mailbox never flashes the panel), or 'never' (after an action, keeping the
  // current list on screen until the new one arrives).
  async function load({ loading = 'now' } = {}) {
    const current = ++generation;
    let timer = null;
    if (loading === 'now') renderLoading();
    else if (loading === 'late') {
      timer = setTimeout(() => current === generation && renderLoading(), LOADING_DELAY_MS);
    }
    try {
      const response = await fetchImpl(base, { headers: { Accept: 'application/json' } });
      const body = await readJSON(response);
      clearTimeout(timer);
      if (current !== generation) return;
      if (!response.ok) {
        renderError(errorView(response.status, body));
        return;
      }
      if (body.linked === false) {
        renderError({ hidden: true });
        return;
      }
      renderList(body.list || {});
    } catch (_) {
      clearTimeout(timer);
      if (current === generation) renderError(errorView(0, {}));
    }
  }

  async function post(path, payload) {
    const response = await fetchImpl(`${base}/${path}`, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!response.ok) {
      const body = await readJSON(response);
      throw new Error(body.message || 'That didn’t work. Try again.');
    }
  }

  // act runs one row action. Every action button waits while it runs, and the
  // list is read again afterwards whether it worked or not.
  async function act(item, path, payload, done) {
    if (busyThread) return;
    busyThread = item.thread_id;
    for (const button of root.querySelectorAll('.needs-you__action')) button.disabled = true;
    try {
      await post(path, { thread_id: item.thread_id, ...payload });
      if (done) globalThis.window?.Toast?.success?.(done);
    } catch (err) {
      globalThis.window?.Toast?.error?.(err.message);
    } finally {
      busyThread = '';
      await load({ loading: 'never' });
    }
  }

  const mark = (item, bucket) => act(item, 'mark', { bucket });
  const track = item => act(item, 'track', {}, 'Added to follow-ups.');

  return { load };
}

function initialize() {
  const root = globalThis.document?.getElementById?.('workspaceNeedsYouMount');
  if (!root) return;
  const workspaceId = String(
    globalThis.window?.currentWorkspaceId || globalThis.document.body?.dataset?.workspaceId || ''
  );
  if (!workspaceId) return;
  const panel = createNeedsYouPanel(root, workspaceId);
  void panel.load({ loading: 'late' });
  // Connecting a mailbox from the setup card fills the panel without a reload.
  globalThis.window?.addEventListener?.(
    'ori:email-setup-connected',
    () => void panel.load({ loading: 'late' })
  );
}

if (globalThis.document?.readyState === 'loading') {
  globalThis.document.addEventListener('DOMContentLoaded', initialize, { once: true });
} else if (globalThis.document?.body) {
  initialize();
}
