// workspace-followups.js — the follow-up management panel for a workspace that
// owns follow-ups (the Email Ops workspace, per the Mail spin-off). This is the
// mutation surface: HQ surfaces follow-ups read-only, and completing/dismissing
// happens here. Additive: no-op without #workspaceFollowupMount or when the
// workspace has no follow-ups.
//
// Follow-up mutations reuse the existing /api/personal-hq/followups/* endpoints,
// which are keyed by follow-up id + user, so they operate correctly regardless
// of which surface invokes them.

import { followUpView } from './personal-hq-onboarding.js';

// managementView maps a raw follow-up list to the panel's view-model: bounded
// card views and whether the panel should show at all.
export function managementView(items) {
  const views = (Array.isArray(items) ? items : []).map(followUpView).filter(Boolean);
  return { show: views.length > 0, count: views.length, items: views };
}

// followUpActionFor returns the {url, label} for a follow-up card button.
export function followUpActionFor(kind) {
  switch (kind) {
    case 'confirm':
      return { url: '/api/personal-hq/followups/confirm', label: 'confirm' };
    case 'dismiss':
      return { url: '/api/personal-hq/followups/dismiss', label: 'dismiss' };
    case 'complete':
      return { url: '/api/personal-hq/followups/complete', label: 'complete' };
    case 'snooze':
      return { url: '/api/personal-hq/followups/snooze', label: 'snooze' };
    default:
      return null;
  }
}

// renderManagementCard builds a single follow-up card with its mutation
// buttons. `act(kind, view)` is invoked on click; kept injectable for tests.
export function renderManagementCard(doc, view, act) {
  const card = doc.createElement('div');
  card.className = 'hq-followup-card';
  card.dataset.followupId = view.id;

  const cat = doc.createElement('span');
  cat.className = 'hq-followup-category';
  cat.textContent = view.counterparty ? `${view.category}: ${view.counterparty}` : view.category;
  card.appendChild(cat);

  const title = doc.createElement('p');
  title.className = 'hq-followup-title';
  title.textContent = view.title;
  card.appendChild(title);

  if (view.detail) {
    const detail = doc.createElement('p');
    detail.className = 'hq-followup-detail';
    detail.textContent = view.detail;
    card.appendChild(detail);
  }

  const actions = doc.createElement('div');
  actions.className = 'hq-followup-actions';

  const button = (text, primary, kind) => {
    const b = doc.createElement('button');
    b.type = 'button';
    b.className = `modern-btn ${primary ? 'modern-btn-primary' : 'modern-btn-secondary'} modern-btn-sm`;
    b.textContent = text;
    b.addEventListener('click', () => act(kind, view));
    return b;
  };

  if (view.isCandidate) {
    actions.append(
      button('Track this', true, 'confirm'),
      button('Not a follow-up', false, 'dismiss')
    );
  } else {
    actions.append(button('Done', true, 'complete'), button('Snooze 1 day', false, 'snooze'));
  }
  card.appendChild(actions);
  return card;
}

// renderManagementPanel populates the mount with a header + cards, or hides it.
export function renderManagementPanel(doc, mount, view, act) {
  if (!mount) return;
  if (!view.show) {
    mount.hidden = true;
    mount.innerHTML = '';
    return;
  }
  mount.innerHTML = '';
  const head = doc.createElement('div');
  head.className = 'workspace-followup-head';
  const h = doc.createElement('h2');
  h.className = 'workspace-followup-title';
  h.textContent = 'Follow-ups';
  const sub = doc.createElement('p');
  sub.className = 'workspace-followup-sub';
  sub.textContent = 'Track what you owe and what you are waiting on. Managed here in Email Ops.';
  head.append(h, sub);
  mount.appendChild(head);

  const list = doc.createElement('div');
  list.className = 'workspace-followup-list';
  view.items.forEach(v => list.appendChild(renderManagementCard(doc, v, act)));
  mount.appendChild(list);
  mount.hidden = false;
}

// wireWorkspaceFollowUps fetches the workspace's follow-ups and renders the
// management panel, re-fetching after each mutation. Dependencies are injected
// for testability.
export async function wireWorkspaceFollowUps({
  doc,
  workspaceId,
  mount,
  fetchImpl,
  postImpl,
  toast
}) {
  if (!mount || !workspaceId) return;
  const load = async () => {
    let items = [];
    try {
      const res = await fetchImpl(`/api/workspaces/${encodeURIComponent(workspaceId)}/followups`, {
        headers: { Accept: 'application/json' }
      });
      if (res && res.ok) {
        const data = await res.json();
        items = Array.isArray(data.followups) ? data.followups : [];
      }
    } catch (_) {
      return; // leave the panel as-is on a transient failure
    }
    const act = async (kind, view) => {
      const action = followUpActionFor(kind);
      if (!action) return;
      const body = { id: view.id };
      if (kind === 'snooze') {
        body.until = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
      }
      try {
        await postImpl(action.url, body);
        await load();
      } catch (_) {
        if (toast) toast(`Could not ${action.label} the follow-up.`, 'Error', 'danger');
      }
    };
    renderManagementPanel(doc, mount, managementView(items), act);
  };
  await load();
}

// Auto-wire on the workspace detail page.
(function () {
  if (typeof document === 'undefined') return;
  const mount = document.getElementById('workspaceFollowupMount');
  if (!mount) return;
  const workspaceId =
    (typeof window !== 'undefined' && window.currentWorkspaceId) ||
    document.body?.dataset?.workspaceId ||
    '';
  if (!workspaceId) return;

  const postJSON = async (url, body) => {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body || {})
    });
    if (!res.ok) throw new Error(`${url} ${res.status}`);
    return res.json();
  };
  const toast = (msg, title, kind) => {
    if (typeof window !== 'undefined' && typeof window.showToast === 'function') {
      window.showToast(msg, title, kind);
    }
  };

  wireWorkspaceFollowUps({
    doc: document,
    workspaceId: String(workspaceId).trim(),
    mount,
    fetchImpl: (u, o) => fetch(u, o),
    postImpl: postJSON,
    toast
  });
})();
