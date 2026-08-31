// home-plugin-updates.js — cached plugin update notifications for Home's
// existing Updates flyout. This module only reads /api/plugins/updates; source
// resolution and every update mutation remain on the Plugins page.

// homePluginUpdatesView converts an already-normalized cache snapshot into the
// small Home presentation model. Keeping this pure makes count and ordering
// behavior testable without a DOM or network.
export function homePluginUpdatesView(snapshot) {
  const updates = Array.isArray(snapshot?.updates)
    ? snapshot.updates.filter(update => update?.available === true)
    : [];
  const count = updates.length;

  return {
    count,
    visible: count > 0,
    title: count === 1 ? '1 plugin update' : `${count} plugin updates`,
    updates
  };
}

function mountHomePluginUpdates() {
  if (typeof document === 'undefined' || typeof window === 'undefined') return;

  const section = document.getElementById('homePluginUpdates');
  const title = document.getElementById('homePluginUpdatesTitle');
  const body = document.getElementById('homePluginUpdatesBody');
  const meta = document.getElementById('homePluginUpdatesMeta');
  const helper = window.PluginUpdateNotifications;
  if (!section || !title || !body || !meta || !helper) return;

  const dispatchCount = count => {
    window.oriPluginUpdateCount = count;
    window.dispatchEvent(
      new CustomEvent('ori:plugin-updates-changed', {
        detail: { count }
      })
    );
  };

  const render = payload => {
    const snapshot = helper.normalize(payload);
    const view = homePluginUpdatesView(snapshot);
    section.hidden = !view.visible;
    title.textContent = view.title;
    const checkedAt = new Date(snapshot.lastSuccessfulCheckAt);
    meta.textContent = Number.isNaN(checkedAt.getTime())
      ? 'Cached update status'
      : `Last checked ${checkedAt.toLocaleString()}`;
    body.replaceChildren();

    if (!view.visible) {
      dispatchCount(0);
      return;
    }

    for (const update of view.updates) {
      const item = document.createElement('article');
      item.className = 'home-plugin-update-item';

      const name = document.createElement('strong');
      name.className = 'home-plugin-update-name';
      name.textContent = update.name || 'Unnamed plugin';

      const detail = document.createElement('span');
      detail.className = 'home-plugin-update-detail';
      detail.textContent = helper.pluginNotice(update)?.detail || 'A source update is available.';

      item.append(name, detail);
      body.append(item);
    }

    dispatchCount(view.count);
  };

  const controller = helper.createController({
    load: async () => {
      const response = await fetch('/api/plugins/updates', {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) throw new Error(`Plugin update status request failed (${response.status})`);
      return response.json();
    },
    onSnapshot: render,
    // A failed refresh intentionally leaves the last successful Home state and
    // count untouched, matching the cache's per-plugin failure isolation.
    onError: () => {}
  });

  controller.start();
  window.addEventListener('beforeunload', () => controller.stop(), { once: true });
}

mountHomePluginUpdates();
