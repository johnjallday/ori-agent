// Plugins page: install (by source or from a marketplace), list, enable/disable,
// and uninstall Claude Code- and Codex-compatible plugins via /api/plugins.
(function () {
  'use strict';

  const byId = id => document.getElementById(id);

  const updateNotifications = window.PluginUpdateNotifications;

  function esc(s) {
    return updateNotifications.escapeHTML(s);
  }

  // api keeps this page's throwing call style, but the request, the response
  // parsing, and the error wording come from the shared lifecycle module so
  // the Plugins page and the Create Workspace wizard cannot drift apart on
  // what a failure means.
  async function api(method, url, body) {
    const result = await window.PluginLifecycle.request(method, url, body);
    if (!result.ok) throw new Error(result.error);
    return result.data;
  }

  // notify surfaces feedback via the shared Toast module when present, falling
  // back to alert() for errors so failures are never swallowed. Success/info
  // messages stay silent if Toast is unavailable (no nagging alerts).
  function notify(message, type) {
    type = type || 'info';
    if (window.Toast && typeof window.Toast[type] === 'function') {
      window.Toast[type](message);
    } else if (window.Toast && typeof window.Toast.show === 'function') {
      window.Toast.show(message, type);
    } else if (type === 'error' || type === 'warning') {
      alert(message);
    }
  }

  // Last-loaded installed plugins, kept so actions can read prior state (e.g.
  // the version before an update) without an extra round-trip.
  let installedCache = [];
  let installedLoaded = false;
  let updateIndex = new Map();
  let lastNoticeSignature = '';

  function renderUpdateNotice(model) {
    if (!model || model.signature === lastNoticeSignature) return;
    lastNoticeSignature = model.signature;
    const notice = byId('pluginUpdateNotice');
    const marker = byId('pluginUpdateNoticeMarker');
    const title = byId('pluginUpdateNoticeTitle');
    const detail = byId('pluginUpdateNoticeDetail');
    if (!notice || !marker || !title || !detail) return;

    notice.dataset.state = model.state;
    title.textContent = model.title;
    detail.textContent = model.detail;
    marker.className = 'badge mt-1';
    if (model.state === 'available') {
      marker.classList.add('bg-warning', 'text-dark');
      marker.textContent = model.count === 1 ? '1 update' : model.count + ' updates';
    } else if (model.state === 'empty') {
      marker.classList.add('bg-success');
      marker.textContent = 'Up to date';
    } else {
      marker.classList.add('bg-secondary');
      marker.textContent = 'Checking';
    }
  }

  function renderInstalledPlugins() {
    const list = byId('pluginList');
    if (!list || !installedLoaded) return;
    list.innerHTML = installedCache.length
      ? installedCache.map(renderPlugin).join('')
      : '<p class="text-muted mb-0">No plugins installed yet.</p>';
  }

  const updateController = updateNotifications.createController({
    load: async () => api('GET', '/api/plugins/updates'),
    onSnapshot: (snapshot, model) => {
      updateIndex = updateNotifications.indexUpdates(snapshot);
      renderUpdateNotice(model);
      renderInstalledPlugins();
    }
  });

  window.loadPluginUpdateStatus = function () {
    return updateController.refresh();
  };

  // The list and cached status are intentionally independent: one failed
  // endpoint never prevents the other surface from refreshing.
  window.refreshPluginsPage = async function () {
    return Promise.allSettled([window.loadPlugins(), window.loadPluginUpdateStatus()]);
  };

  // ---- shared trust disclosure (used by source-install and marketplace-install) ----

  let pendingConfirm = null;

  const DEFAULT_TRUST_TITLE = 'This plugin will register:';
  const DEFAULT_TRUST_CONFIRM = 'Confirm install';

  // showTrust reveals the shared disclosure panel. opts lets callers retitle it
  // for context (e.g. an update re-confirmation, which is triggered from the
  // installed-plugins list far below) and scrolls it into view so the prompt
  // isn't missed.
  function showTrust(report, onConfirm, opts) {
    opts = opts || {};
    const titleEl = byId('pluginTrustTitle');
    const confirmEl = byId('pluginTrustConfirm');
    if (titleEl) titleEl.textContent = opts.title || DEFAULT_TRUST_TITLE;
    if (confirmEl) confirmEl.textContent = opts.confirmLabel || DEFAULT_TRUST_CONFIRM;
    renderTrustBody(report || {});
    pendingConfirm = onConfirm;
    const panel = byId('pluginTrust');
    panel.style.display = 'block';
    if (typeof panel.scrollIntoView === 'function') {
      panel.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }

  window.pluginConfirmInstall = async function () {
    if (!pendingConfirm) return;
    const fn = pendingConfirm;
    pendingConfirm = null;
    try {
      await fn();
    } catch (e) {
      notify('Action failed: ' + e.message, 'error');
    }
  };

  window.pluginCancelInstall = function () {
    pendingConfirm = null;
    byId('pluginTrust').style.display = 'none';
    byId('pluginTrustBody').innerHTML = '';
    const titleEl = byId('pluginTrustTitle');
    const confirmEl = byId('pluginTrustConfirm');
    if (titleEl) titleEl.textContent = DEFAULT_TRUST_TITLE;
    if (confirmEl) confirmEl.textContent = DEFAULT_TRUST_CONFIRM;
  };

  // The disclosure itself is built by the shared lifecycle module: it is the
  // last thing a user reads before a plugin can run commands on their machine,
  // so there is exactly one implementation of it, and both surfaces show the
  // same thing.
  function renderTrustInto(host, report) {
    if (!host) return;
    host.innerHTML = '';
    host.appendChild(window.PluginLifecycle.renderTrustReport(report));
  }

  function renderTrustBody(t) {
    renderTrustInto(byId('pluginTrustBody'), t);
  }

  // ---- installed plugins ----

  window.loadPlugins = async function () {
    const list = byId('pluginList');
    try {
      const data = await api('GET', '/api/plugins');
      installedCache = (data && data.plugins) || [];
      installedLoaded = true;
      renderInstalledPlugins();
    } catch (e) {
      if (list) {
        list.innerHTML =
          '<p class="text-danger mb-0">Failed to load plugins: ' + esc(e.message) + '</p>';
      }
    }
  };

  function renderPlugin(p) {
    const servers = (p.mcp_servers || []).map(esc).join(', ') || '&mdash;';
    const skills = (p.skills || []).map(esc).join(', ') || '&mdash;';
    const formatBadge = '<span class="badge bg-secondary">' + esc(p.format) + '</span>';
    const rawName = String(p.name == null ? '' : p.name);
    const name = esc(rawName);
    const installDirectory = String(p.install_dir == null ? '' : p.install_dir).trim();
    const update = updateIndex.get(rawName);
    const updateNotice = updateNotifications.pluginNotice(update);
    const updateBadge = updateNotice
      ? '<span class="badge bg-warning text-dark ms-2" title="' +
        esc(updateNotice.detail) +
        '">' +
        esc(updateNotice.label) +
        '</span>'
      : '';
    return (
      // flex-wrap so the action buttons drop below the plugin's details at a
      // narrow width instead of running off the edge of the screen — a
      // pre-existing gap this feature's own narrow-width demo caught.
      '<div class="d-flex flex-wrap align-items-start justify-content-between gap-2 border-bottom py-3">' +
      '<div style="min-width: 0; flex: 1 1 240px;">' +
      '<div class="fw-semibold">' +
      name +
      ' <span class="text-muted small">' +
      esc(p.version || '') +
      '</span> ' +
      formatBadge +
      updateBadge +
      '</div>' +
      (p.description ? '<div class="small text-muted">' + esc(p.description) + '</div>' : '') +
      (installDirectory
        ? '<div class="small mt-1"><span class="fw-semibold">Installed files:</span> <code class="plugin-install-directory text-break">' +
          esc(installDirectory) +
          '</code></div>'
        : '') +
      '<div class="small mt-1">MCP: ' +
      servers +
      ' &middot; Skills: ' +
      skills +
      '</div>' +
      // Lifecycle state in the same words the creation wizard uses, so a user
      // moving between the two surfaces is never told two different things
      // about one plugin.
      '<div class="small mt-1 ' +
      (p.enabled ? 'text-success' : 'text-warning') +
      '">' +
      esc(
        window.PluginLifecycle.capitalize(
          p.enabled
            ? window.PluginLifecycle.LIFECYCLE_LABELS.ENABLED
            : window.PluginLifecycle.LIFECYCLE_LABELS.DISABLED
        )
      ) +
      '</div>' +
      '</div>' +
      '<div class="d-flex flex-wrap gap-2">' +
      // Enable is the call to action while a plugin is disabled: it is the one
      // thing standing between an installed plugin and a usable one.
      '<button class="modern-btn ' +
      (p.enabled ? 'modern-btn-secondary' : 'modern-btn-primary') +
      '" data-plugin-action="toggle" data-plugin-name="' +
      name +
      '" data-plugin-enable="' +
      (p.enabled ? 'false' : 'true') +
      '">' +
      (p.enabled ? 'Disable' : 'Enable') +
      '</button>' +
      '<button class="modern-btn ' +
      (updateNotice ? 'modern-btn-primary' : 'modern-btn-secondary') +
      '" data-plugin-action="update" data-plugin-name="' +
      name +
      '">Update</button>' +
      '<button class="modern-btn modern-btn-secondary" data-plugin-action="uninstall" data-plugin-name="' +
      name +
      '">Uninstall</button>' +
      '</div>' +
      '</div>'
    );
  }

  function wireInstalledActions() {
    const list = byId('pluginList');
    if (!list || list.dataset.actionsWired) return;
    list.dataset.actionsWired = '1';
    list.addEventListener('click', event => {
      const button = event.target.closest('[data-plugin-action]');
      if (!button || !list.contains(button)) return;
      const name = button.getAttribute('data-plugin-name') || '';
      switch (button.getAttribute('data-plugin-action')) {
        case 'toggle':
          window.pluginToggle(name, button.getAttribute('data-plugin-enable') === 'true');
          break;
        case 'update':
          window.pluginUpdate(name);
          break;
        case 'uninstall':
          window.pluginUninstall(name);
          break;
      }
    });
  }

  // ---- install by source ----

  window.pluginPreview = async function () {
    const source = byId('pluginSource').value.trim();
    const format = byId('pluginFormat').value;
    if (!source) {
      notify('Enter a plugin source.', 'warning');
      return;
    }
    try {
      const data = await api('POST', '/api/plugins/install', { source, format, confirm: false });
      showTrust(data.trust, async () => {
        const res = await api('POST', '/api/plugins/install', { source, format, confirm: true });
        window.pluginCancelInstall();
        byId('pluginSource').value = '';
        await window.refreshPluginsPage();
        // Installing does not switch a plugin on, and a bare "Installed" reads
        // as if it did. Say which of the two states the plugin is actually in,
        // in the same words the creation wizard uses.
        const installed = (res && res.plugin) || {};
        const name = installed.name || source;
        const labels = window.PluginLifecycle.LIFECYCLE_LABELS;
        if (installed.enabled) {
          notify(name + ' is ' + labels.ENABLED + '.', 'success');
        } else {
          notify(name + ' is ' + labels.DISABLED + ' — enable it to use it.', 'warning');
        }
      });
    } catch (e) {
      notify('Preview failed: ' + e.message, 'error');
    }
  };

  window.pluginToggle = async function (name, enable) {
    try {
      await api(
        'POST',
        '/api/plugins/' + encodeURIComponent(name) + (enable ? '/enable' : '/disable')
      );
      await window.loadPlugins();
      notify(name + (enable ? ' enabled' : ' disabled'), 'success');
    } catch (e) {
      notify('Failed: ' + e.message, 'error');
    }
  };

  window.pluginUninstall = async function (name) {
    if (!confirm('Uninstall ' + name + '? This removes its MCP servers and skills.')) return;
    try {
      await api('DELETE', '/api/plugins/' + encodeURIComponent(name));
      await window.refreshPluginsPage();
      notify('Uninstalled ' + name, 'success');
    } catch (e) {
      notify('Uninstall failed: ' + e.message, 'error');
    }
  };

  window.pluginUpdate = async function (name) {
    const url = '/api/plugins/' + encodeURIComponent(name) + '/update';
    const prev = installedCache.find(p => p.name === name);
    const oldVersion = (prev && prev.version) || '';
    try {
      const data = await api('POST', url, { confirm: false });
      const doUpdate = async () => {
        const res = await api('POST', url, { confirm: true });
        window.pluginCancelInstall();
        await window.refreshPluginsPage();
        const newVersion = (res && res.plugin && res.plugin.version) || '';
        if (newVersion && oldVersion && newVersion !== oldVersion) {
          notify('Updated ' + name + ' to ' + newVersion, 'success');
        } else if (data.changed) {
          notify('Updated ' + name + ' — registered components changed', 'success');
        } else {
          notify(name + ' is already up to date', 'info');
        }
      };
      if (data.changed) {
        // The component set changed, so re-disclose and re-confirm. Title it for
        // the update (the panel lives in the install card, far from the Update
        // button) so the prompt is unmistakable.
        showTrust(data.trust, doUpdate, {
          title: 'Update ' + name + ' — this will now register:',
          confirmLabel: 'Confirm update'
        });
      } else {
        await doUpdate();
      }
    } catch (e) {
      notify('Update failed: ' + e.message, 'error');
    }
  };

  // ---- marketplaces (official browse + user-added) ----

  const officialState = {
    name: '',
    source: '',
    added: false,
    loaded: false,
    entries: [],
    installed: new Set()
  };

  async function getInstalledNames() {
    try {
      const data = await api('GET', '/api/plugins');
      return new Set(((data && data.plugins) || []).map(p => p.name));
    } catch (_) {
      return new Set();
    }
  }

  // loadMarketplaces drives the user-added list and records the official
  // marketplace's identity (name/source/added) for the browse modal. The
  // official catalog itself is loaded lazily when the modal opens, and is never
  // duplicated in the user-added list.
  window.loadMarketplaces = async function () {
    const el = byId('marketplaceList');
    let data;
    try {
      data = await api('GET', '/api/plugins/marketplaces');
    } catch (e) {
      if (el)
        el.innerHTML =
          '<p class="text-danger mb-0">Failed to load marketplaces: ' + esc(e.message) + '</p>';
      return;
    }
    const official = (data && data.official) || {};
    officialState.name = official.name || '';
    officialState.source = official.source || '';
    officialState.added = !!official.added;

    if (el) {
      const others = ((data && data.marketplaces) || []).filter(m => m.name !== officialState.name);
      el.innerHTML = others.length
        ? others.map(renderMarketplace).join('')
        : '<p class="text-muted mb-0">No marketplaces added.</p>';
    }
  };

  // ---- official browse (modal) ----

  function officialGridMessage(html) {
    const empty = byId('officialEmpty');
    const grid = byId('officialGrid');
    if (empty) empty.innerHTML = '';
    if (grid) grid.innerHTML = html;
  }

  // ensureOfficialLoaded makes the official catalog browsable on demand: it adds
  // the official marketplace if it isn't already (a one-time clone), then loads
  // and renders its entries. Called when the modal opens, so the user never has
  // to "add" anything — they just browse. Pass force=true to re-read (e.g. after
  // an install or via Refresh).
  async function ensureOfficialLoaded(force) {
    if (officialState.loaded && !force) {
      await refreshOfficialInstalled();
      return;
    }
    officialGridMessage('<p class="text-muted mb-0">Loading official plugins&hellip;</p>');
    try {
      if (!officialState.added) {
        await api('POST', '/api/plugins/marketplaces', { source: officialState.source });
        officialState.added = true;
      }
      const data = await api('GET', '/api/plugins/marketplaces');
      const off = ((data && data.marketplaces) || []).find(m => m.name === officialState.name);
      officialState.entries = (off && off.plugins) || [];
      officialState.installed = await getInstalledNames();
      officialState.loaded = true;

      if (!officialState.entries.length) {
        officialGridMessage(
          '<p class="text-muted mb-0">The official catalog is empty or could not be read.</p>'
        );
        return;
      }
      populateOfficialFilters();
      wireOfficialGrid();
      renderOfficialGrid();
    } catch (e) {
      officialGridMessage(
        '<p class="text-danger mb-0">Failed to load the official catalog: ' +
          esc(e.message) +
          '</p>'
      );
    }
  }

  async function refreshOfficialInstalled() {
    if (!officialState.loaded) return;
    officialState.installed = await getInstalledNames();
    renderOfficialGrid();
  }

  function fillSelect(sel, allLabel, values) {
    if (!sel) return;
    const current = sel.value;
    sel.innerHTML =
      '<option value="">' +
      esc(allLabel) +
      '</option>' +
      values.map(v => '<option value="' + esc(v) + '">' + esc(v) + '</option>').join('');
    if (values.indexOf(current) !== -1) sel.value = current;
  }

  function populateOfficialFilters() {
    const cats = new Set();
    const tags = new Set();
    officialState.entries.forEach(e => {
      if (e.category) cats.add(e.category);
      (e.tags || []).forEach(t => tags.add(t));
    });
    fillSelect(byId('officialCategory'), 'All categories', [...cats].sort());
    fillSelect(byId('officialTag'), 'All tags', [...tags].sort());
  }

  function filteredEntries() {
    const q = ((byId('officialSearch') && byId('officialSearch').value) || '').trim().toLowerCase();
    const cat = (byId('officialCategory') && byId('officialCategory').value) || '';
    const tag = (byId('officialTag') && byId('officialTag').value) || '';
    return officialState.entries.filter(e => {
      if (cat && e.category !== cat) return false;
      if (tag && (e.tags || []).indexOf(tag) === -1) return false;
      if (q) {
        const hay = [e.name, e.description, (e.tags || []).join(' '), (e.keywords || []).join(' ')]
          .join(' ')
          .toLowerCase();
        if (hay.indexOf(q) === -1) return false;
      }
      return true;
    });
  }

  window.officialFilter = function () {
    renderOfficialGrid();
  };

  function renderOfficialGrid() {
    const grid = byId('officialGrid');
    if (!grid) return;
    const items = filteredEntries();
    grid.innerHTML = items.length
      ? items.map(renderOfficialCard).join('')
      : '<p class="text-muted mb-0">No plugins match your filters.</p>';
  }

  function renderOfficialCard(e) {
    const installed = officialState.installed.has(e.name);
    const cat = e.category ? '<span class="badge bg-secondary">' + esc(e.category) + '</span>' : '';
    const author =
      e.author && e.author.name
        ? '<div class="small text-muted">by ' + esc(e.author.name) + '</div>'
        : '';
    const tags = (e.tags || [])
      .map(t => '<span class="badge bg-light text-dark border me-1">' + esc(t) + '</span>')
      .join('');
    const installBtn = installed
      ? '<button class="modern-btn modern-btn-secondary" disabled>Installed</button>'
      : '<button class="modern-btn modern-btn-primary" data-official-install="' +
        esc(e.name) +
        '">Install</button>';
    return (
      '<div class="col-md-6 col-lg-4">' +
      '<div class="modern-card h-100 p-3">' +
      '<div class="d-flex justify-content-between align-items-start gap-2">' +
      '<div class="fw-semibold">' +
      esc(e.name) +
      '</div>' +
      cat +
      '</div>' +
      author +
      (e.description ? '<div class="small text-muted mt-1">' + esc(e.description) + '</div>' : '') +
      (tags ? '<div class="mt-2">' + tags + '</div>' : '') +
      '<div class="d-flex gap-2 mt-3">' +
      installBtn +
      '<button class="modern-btn modern-btn-secondary" data-official-details="' +
      esc(e.name) +
      '">Details</button>' +
      '</div>' +
      '<div class="official-detail small mt-2" data-detail="' +
      esc(e.name) +
      '" style="display:none;"></div>' +
      '</div></div>'
    );
  }

  // Delegated click handling survives grid re-renders (the grid element persists;
  // only its innerHTML changes), so it is wired once.
  function wireOfficialGrid() {
    const grid = byId('officialGrid');
    if (!grid || grid.dataset.wired) return;
    grid.dataset.wired = '1';
    grid.addEventListener('click', function (ev) {
      const inst = ev.target.closest('[data-official-install]');
      if (inst) {
        officialInstall(inst.getAttribute('data-official-install'));
        return;
      }
      const conf = ev.target.closest('[data-official-confirm]');
      if (conf) {
        officialConfirmInstall(conf.getAttribute('data-official-confirm'));
        return;
      }
      const can = ev.target.closest('[data-official-cancel]');
      if (can) {
        const b = detailBox(can.getAttribute('data-official-cancel'));
        if (b) {
          b.style.display = 'none';
          b.dataset.loaded = '';
        }
        return;
      }
      const det = ev.target.closest('[data-official-details]');
      if (det) {
        toggleOfficialDetails(det.getAttribute('data-official-details'));
      }
    });
  }

  function detailBox(name) {
    const sel = window.CSS && CSS.escape ? CSS.escape(name) : name;
    return document.querySelector('.official-detail[data-detail="' + sel + '"]');
  }

  async function toggleOfficialDetails(name) {
    const box = detailBox(name);
    if (!box) return;
    if (box.style.display !== 'none') {
      box.style.display = 'none';
      return;
    }
    box.style.display = 'block';
    if (box.dataset.loaded) return;

    const e = officialState.entries.find(x => x.name === name) || {};
    let meta = '';
    if (e.author && (e.author.name || e.author.email)) {
      meta +=
        '<div>Author: ' +
        esc(e.author.name || '') +
        (e.author.email ? ' &lt;' + esc(e.author.email) + '&gt;' : '') +
        '</div>';
    }
    if (e.homepage)
      meta +=
        '<div>Homepage: <a href="' +
        esc(e.homepage) +
        '" target="_blank" rel="noopener">' +
        esc(e.homepage) +
        '</a></div>';
    if (e.category) meta += '<div>Category: ' + esc(e.category) + '</div>';
    box.innerHTML = meta + '<div class="text-muted mt-2">Loading what it will register…</div>';

    try {
      const data = await api('POST', '/api/plugins/marketplaces/install', {
        marketplace: officialState.name,
        plugin: name,
        confirm: false
      });
      box.innerHTML = meta + '<div class="fw-semibold mt-2">This plugin will register:</div>';
      box.appendChild(window.PluginLifecycle.renderTrustReport(data.trust));
      box.dataset.loaded = '1';
    } catch (err) {
      box.innerHTML =
        meta +
        '<div class="text-danger mt-2">Could not load details: ' +
        esc(err.message) +
        '</div>';
    }
  }

  // Install is confirmed inline inside the card, not via the page-level trust
  // panel — that panel renders behind the open modal and would be unclickable.
  async function officialInstall(name) {
    const box = detailBox(name);
    if (!box) return;
    box.dataset.loaded = '';
    box.style.display = 'block';
    box.innerHTML = '<div class="text-muted">Preparing install&hellip;</div>';
    try {
      const data = await api('POST', '/api/plugins/marketplaces/install', {
        marketplace: officialState.name,
        plugin: name,
        confirm: false
      });
      box.innerHTML = '<div class="fw-semibold">This plugin will register:</div>';
      box.appendChild(window.PluginLifecycle.renderTrustReport(data.trust));
      box.insertAdjacentHTML(
        'beforeend',
        '<div class="d-flex gap-2 mt-2">' +
          '<button class="modern-btn modern-btn-primary" data-official-confirm="' +
          esc(name) +
          '">Confirm install</button>' +
          '<button class="modern-btn modern-btn-secondary" data-official-cancel="' +
          esc(name) +
          '">Cancel</button>' +
          '</div>'
      );
    } catch (e) {
      box.innerHTML = '<div class="text-danger">Preview failed: ' + esc(e.message) + '</div>';
    }
  }

  async function officialConfirmInstall(name) {
    const box = detailBox(name);
    if (box) box.innerHTML = '<div class="text-muted">Installing&hellip;</div>';
    try {
      await api('POST', '/api/plugins/marketplaces/install', {
        marketplace: officialState.name,
        plugin: name,
        confirm: true
      });
      await window.refreshPluginsPage();
      window.loadMarketplaces();
      officialState.installed = await getInstalledNames();
      renderOfficialGrid(); // card rebuilds showing "Installed"
    } catch (e) {
      if (box)
        box.innerHTML = '<div class="text-danger">Install failed: ' + esc(e.message) + '</div>';
    }
  }

  // ---- user-added marketplaces ----

  function renderMarketplace(mp) {
    const name = esc(mp.name);
    const entries = (mp.plugins || [])
      .map(
        e =>
          '<li class="d-flex justify-content-between align-items-center py-1">' +
          '<span>' +
          esc(e.name) +
          (e.description
            ? ' <span class="text-muted small">' + esc(e.description) + '</span>'
            : '') +
          '</span>' +
          '<button class="modern-btn modern-btn-secondary" onclick="marketplaceInstall(\'' +
          name +
          "', '" +
          esc(e.name) +
          '\')">Install</button>' +
          '</li>'
      )
      .join('');
    return (
      '<div class="border-bottom py-2">' +
      '<div class="fw-semibold">' +
      name +
      ' <span class="text-muted small">' +
      esc(mp.source || '') +
      '</span></div>' +
      '<ul class="list-unstyled mb-0">' +
      (entries || '<li class="text-muted small">No plugins listed.</li>') +
      '</ul>' +
      '</div>'
    );
  }

  window.addMarketplace = async function () {
    const source = byId('marketplaceSource').value.trim();
    if (!source) {
      notify('Enter a marketplace source.', 'warning');
      return;
    }
    try {
      await api('POST', '/api/plugins/marketplaces', { source });
      byId('marketplaceSource').value = '';
      window.loadMarketplaces();
      notify('Marketplace added', 'success');
    } catch (e) {
      notify('Add marketplace failed: ' + e.message, 'error');
    }
  };

  window.marketplaceInstall = async function (marketplace, pluginName) {
    try {
      const data = await api('POST', '/api/plugins/marketplaces/install', {
        marketplace,
        plugin: pluginName,
        confirm: false
      });
      showTrust(data.trust, async () => {
        await api('POST', '/api/plugins/marketplaces/install', {
          marketplace,
          plugin: pluginName,
          confirm: true
        });
        window.pluginCancelInstall();
        await window.refreshPluginsPage();
        window.loadMarketplaces();
        refreshOfficialInstalled(); // reflect new install in the official modal grid
        notify('Installed ' + pluginName, 'success');
      });
    } catch (e) {
      notify('Preview failed: ' + e.message, 'error');
    }
  };

  document.addEventListener('DOMContentLoaded', function () {
    wireInstalledActions();
    window.loadPlugins();
    updateController.start();
    window.loadMarketplaces();
    // Load the official catalog lazily when its modal opens (auto-adds on first
    // open so the user just browses); the Refresh button re-reads it.
    const modalEl = byId('officialModal');
    if (modalEl)
      modalEl.addEventListener('shown.bs.modal', function () {
        ensureOfficialLoaded(false);
      });
    const refreshBtn = byId('officialRefreshBtn');
    if (refreshBtn)
      refreshBtn.onclick = function () {
        ensureOfficialLoaded(true);
      };
    window.addEventListener('beforeunload', () => updateController.stop(), { once: true });
  });
})();
