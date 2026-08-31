// Plugin update notifications — normalization, copy, and a deduplicated cache
// poller for the legacy Plugins page. This module never renders HTML and never
// calls the source-resolving update endpoint.
(function (root) {
  'use strict';

  const POLL_INTERVAL_MS = 5 * 60 * 1000;

  function text(value) {
    return typeof value === 'string' ? value : '';
  }

  function normalize(payload) {
    const source = payload && typeof payload === 'object' ? payload : {};
    const byName = new Map();
    const updates = Array.isArray(source.updates) ? source.updates : [];
    updates.forEach(candidate => {
      if (!candidate || typeof candidate !== 'object') return;
      const name = text(candidate.name).trim();
      if (!name) return;
      byName.set(name, {
        name,
        installedVersion: text(candidate.installed_version ?? candidate.installedVersion),
        availableVersion: text(candidate.available_version ?? candidate.availableVersion),
        componentsChanged:
          candidate.components_changed === true || candidate.componentsChanged === true,
        available: candidate.available === true
      });
    });
    return {
      updates: [...byName.values()].sort((a, b) =>
        a.name < b.name ? -1 : a.name > b.name ? 1 : 0
      ),
      checking: source.checking === true,
      lastSuccessfulCheckAt: text(source.last_successful_check_at ?? source.lastSuccessfulCheckAt)
    };
  }

  function indexUpdates(snapshot) {
    const index = new Map();
    normalize(snapshot).updates.forEach(update => index.set(update.name, update));
    return index;
  }

  function hasVersionChange(update) {
    return !!(
      update &&
      update.availableVersion &&
      update.availableVersion !== update.installedVersion
    );
  }

  function pluginNotice(update) {
    if (!update || !update.available) return null;
    if (hasVersionChange(update)) {
      return {
        label: 'Update available · ' + update.availableVersion,
        detail: 'Source version ' + update.availableVersion + ' is available.'
      };
    }
    if (update.componentsChanged) {
      return {
        label: 'Update available · components changed',
        detail: 'The plugin’s trusted component footprint changed.'
      };
    }
    return {
      label: 'Update available',
      detail: 'The recorded source differs from the installed plugin.'
    };
  }

  function presentation(snapshot) {
    const normalized = normalize(snapshot);
    const available = normalized.updates.filter(update => update.available);
    const count = available.length;
    let state;
    let title;
    let detail;

    if (normalized.checking && !normalized.lastSuccessfulCheckAt) {
      state = 'pending';
      title = 'Checking installed plugins for updates…';
      detail = 'This happens in the background and never installs anything.';
    } else if (count === 0) {
      state = 'empty';
      title = 'No plugin updates available';
      detail = normalized.checking
        ? 'Refreshing the cached update check now.'
        : 'Ori will keep checking installed plugin sources in the background.';
    } else if (count === 1) {
      state = 'available';
      title = '1 plugin update is available';
      const update = available[0];
      if (hasVersionChange(update)) {
        detail = update.name + ' ' + update.availableVersion + ' is ready to review.';
      } else if (update.componentsChanged) {
        detail = update.name + ' has a trusted-component change ready to review.';
      } else {
        detail = update.name + ' has a source change ready to review.';
      }
    } else {
      state = 'available';
      title = count + ' plugin updates are available';
      detail = 'Review each update below and apply it manually when you are ready.';
    }

    const signature = JSON.stringify({
      state,
      title,
      detail,
      updates: available.map(update => [
        update.name,
        update.installedVersion,
        update.availableVersion,
        update.componentsChanged
      ])
    });
    return { state, title, detail, count, signature };
  }

  function escapeHTML(value) {
    return String(value == null ? '' : value).replace(
      /[&<>"']/g,
      character =>
        ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]
    );
  }

  function createController(options) {
    const config = options || {};
    const load = typeof config.load === 'function' ? config.load : async () => ({});
    const onSnapshot = typeof config.onSnapshot === 'function' ? config.onSnapshot : function () {};
    const onError = typeof config.onError === 'function' ? config.onError : function () {};
    const schedule =
      typeof config.setIntervalFn === 'function'
        ? config.setIntervalFn
        : (callback, delay) => root.setInterval(callback, delay);
    const cancel =
      typeof config.clearIntervalFn === 'function'
        ? config.clearIntervalFn
        : timer => root.clearInterval(timer);
    const intervalMs = Number(config.intervalMs) > 0 ? Number(config.intervalMs) : POLL_INTERVAL_MS;
    let timer = null;
    let inFlight = null;

    function refresh() {
      if (inFlight) return inFlight;
      inFlight = Promise.resolve()
        .then(() => load())
        .then(payload => {
          const snapshot = normalize(payload);
          onSnapshot(snapshot, presentation(snapshot));
          return snapshot;
        })
        .catch(error => {
          onError(error);
          return null;
        })
        .finally(() => {
          inFlight = null;
        });
      return inFlight;
    }

    function start() {
      if (timer !== null) return false;
      void refresh();
      timer = schedule(() => void refresh(), intervalMs);
      return true;
    }

    function stop() {
      if (timer === null) return false;
      cancel(timer);
      timer = null;
      return true;
    }

    return { refresh, start, stop, isStarted: () => timer !== null };
  }

  root.PluginUpdateNotifications = {
    POLL_INTERVAL_MS,
    normalize,
    indexUpdates,
    pluginNotice,
    presentation,
    escapeHTML,
    createController
  };
})(window);
