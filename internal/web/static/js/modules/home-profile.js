// The Home profile card: what a Home knows about where its owner works, under
// the rows the Home's package declares. The package gives the title, the
// introduction and each row's label; this module only words states. A value
// Ori found is shown as Detected and stays a hint until the owner confirms it.
// Nothing here names an application: every name comes from the server.

export const READ_ONLY_NOTE =
  'Saved values are readable; the Home provider is unavailable for changes.';

const KNOWN_KINDS = ['apps', 'main_app', 'templates', 'defaults'];

// "A", "A and B", "A, B and C".
export function joinNames(names = []) {
  const list = names.map(name => String(name || '').trim()).filter(Boolean);
  if (list.length <= 1) return list[0] || '';
  return `${list.slice(0, -1).join(', ')} and ${list[list.length - 1]}`;
}

// A stored time as a short date, or '' when there is none.
export function profileDate(value, locale = undefined, timeZone = undefined) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleDateString(locale, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    ...(timeZone ? { timeZone } : {})
  });
}

function visibleApps(view) {
  return (view?.profile?.apps || []).filter(app => app && !app.hidden);
}

// profileView is the card as a whole: hidden unless the Home's package
// declares a profile, and the rows in declared order.
export function profileView(view) {
  if (!view?.available) return { visible: false, rows: [] };
  const rows = (Array.isArray(view.fields) ? view.fields : [])
    .filter(field => field && KNOWN_KINDS.includes(field.kind))
    .map(field => ({
      id: String(field.id || field.kind),
      kind: field.kind,
      label: String(field.label || '')
    }));
  const readOnly = Boolean(view.read_only);
  return {
    visible: rows.length > 0,
    title: String(view.title || ''),
    intro: String(view.intro || ''),
    readOnly,
    readOnlyNote: readOnly ? READ_ONLY_NOTE : '',
    rows,
    detect: detectView(view, rows)
  };
}

// The Detect button exists while a row can use what it finds. It says "Detect"
// until the first look and "Detect again" after.
export function detectView(view, rows = profileView(view).rows) {
  const useful = rows.some(row => row.kind === 'apps' || row.kind === 'main_app');
  return {
    visible: useful,
    label: view?.profile?.detected_at ? 'Detect again' : 'Detect',
    disabled: Boolean(view?.read_only)
  };
}

// appRowView words one found application. Detected and Confirmed are distinct:
// a detected value is never shown as something the owner said.
export function appRowView(app, { readOnly = false, locale, timeZone } = {}) {
  const confirmed = Boolean(app?.confirmed_at);
  const found = app?.detected !== false;
  const date = profileDate(app?.confirmed_at, locale, timeZone);
  let detail;
  if (confirmed)
    detail = found ? `Confirmed ${date}.` : `Confirmed ${date}. Not found on this Mac now.`;
  else
    detail = found
      ? 'Found on this Mac. A hint until you confirm it.'
      : 'Not found on this Mac now.';
  return {
    id: String(app?.id || ''),
    name: String(app?.name || ''),
    version: String(app?.version || ''),
    badge: confirmed ? 'Confirmed' : 'Detected',
    badgeKind: confirmed ? 'confirmed' : 'detected',
    detail,
    canConfirm: !confirmed,
    canHide: true,
    disabled: Boolean(readOnly)
  };
}

// appsView is the row of found applications: the visible ones, the ones the
// owner said are not theirs, and one sentence when there is nothing to list.
export function appsView(view, options = {}) {
  const readOnly = Boolean(view?.read_only);
  const profile = view?.profile || null;
  const rows = visibleApps(view).map(app => appRowView(app, { ...options, readOnly }));
  const hidden = (profile?.apps || [])
    .filter(app => app?.hidden)
    .map(app => ({ id: String(app.id || ''), name: String(app.name || ''), disabled: readOnly }));
  let empty = '';
  if (rows.length === 0) {
    if (!profile?.detected_at) {
      empty = readOnly
        ? 'Nothing has been looked for yet.'
        : 'Nothing has been looked for yet. Detect checks the Applications folders on this Mac and reads nothing inside an app.';
    } else if (hidden.length > 0) {
      empty = 'Nothing else was found on this Mac.';
    } else {
      empty = 'None was found on this Mac. Detect again after you install one.';
    }
  }
  return { rows, hidden, empty };
}

// mainAppView is the owner's main application: a pick among the found ones,
// with where the current value came from.
export function mainAppView(view, { locale, timeZone } = {}) {
  const readOnly = Boolean(view?.read_only);
  const apps = visibleApps(view);
  const options = apps.map(app => ({ id: String(app.id || ''), name: String(app.name || '') }));
  const main = view?.profile?.main_app || null;
  const current = main ? apps.find(app => app.id === main.id) : null;
  const base = {
    options,
    value: current ? main.id : '',
    disabled: readOnly || options.length === 0
  };
  if (!current) {
    let note;
    if (options.length === 0) {
      note = view?.profile?.detected_at
        ? 'Nothing to pick from yet.'
        : 'Detect first, then pick one.';
    } else if (options.length === 1) {
      note = `Not chosen. ${options[0].name} was found. Pick it if it is yours.`;
    } else {
      note = `Not chosen. ${joinNames(options.map(option => option.name))} were found. Pick one.`;
    }
    return { ...base, badge: '', badgeKind: '', note, canConfirm: false };
  }
  const date = profileDate(main.confirmed_at, locale, timeZone);
  if (main.source === 'owner') {
    return {
      ...base,
      badge: 'Chosen by you',
      badgeKind: 'confirmed',
      note: `Chosen by you ${date}.`,
      canConfirm: false
    };
  }
  if (main.confirmed_at) {
    return {
      ...base,
      badge: 'Confirmed',
      badgeKind: 'confirmed',
      note: `Confirmed ${date}.`,
      canConfirm: false
    };
  }
  const why =
    main.reason === 'library_majority'
      ? `Most of the projects in your library are ${current.name} projects.`
      : `${current.name} is the only one found on this Mac.`;
  return {
    ...base,
    badge: 'Detected',
    badgeKind: 'detected',
    note: `${why} A hint until you confirm it.`,
    canConfirm: !readOnly
  };
}

// What the status line says after an action finished.
export function profileStatus(action, view) {
  switch (action) {
    case 'detect': {
      const found = visibleApps(view).map(app => app.name);
      return found.length ? `Found ${joinNames(found)}.` : 'Nothing was found on this Mac.';
    }
    case 'confirm':
      return 'Confirmed.';
    case 'hide':
      return 'Hidden. Agents no longer see it.';
    case 'show':
      return 'Shown again as a hint.';
    case 'main_app':
      return view?.profile?.main_app ? 'Saved.' : 'Cleared.';
    default:
      return 'Saved.';
  }
}

async function payload(response) {
  try {
    return await response.json();
  } catch (_) {
    return {};
  }
}

function element(tag, className = '', text = '') {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text) node.textContent = text;
  return node;
}

function button(label, action, id = '', disabled = false) {
  const node = element('button', 'modern-btn modern-btn-secondary modern-btn-sm', label);
  node.type = 'button';
  node.dataset.profileAction = action;
  if (id) node.dataset.profileId = id;
  node.disabled = disabled;
  return node;
}

function badge(label, kind) {
  return element('span', `home-profile-badge is-${kind}`, label);
}

// HomeProfilePanel mounts the card on the Home page. It reads once on load and
// after each action; it never detects or reads anything by itself.
export class HomeProfilePanel {
  constructor({ workspaceId, program, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = String(workspaceId || '').trim();
    this.program = program || {};
    this.fetchImpl = (...args) => fetchImpl(...args);
    this.view = null;
    this.busy = false;
  }

  get panel() {
    return globalThis.document?.getElementById('homeProfilePanel') || null;
  }

  url(path = '') {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/assistant-program/profile${path}`;
  }

  async request(path = '', options = {}) {
    const response = await this.fetchImpl(this.url(path), {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const result = await payload(response);
    if (!response.ok) {
      const error = new Error(result?.message || 'The profile could not be saved. Try again.');
      error.code = result?.code || '';
      error.status = response.status;
      throw error;
    }
    return result;
  }

  post(path, body = {}) {
    return this.request(path, {
      method: 'POST',
      body: JSON.stringify({ request_id: `profile-${globalThis.crypto.randomUUID()}`, ...body })
    });
  }

  // Only a Home has a profile; a linked project's page asks for nothing.
  async init() {
    const panel = this.panel;
    if (!panel || !this.workspaceId || this.program?.is_station === false) return;
    panel.addEventListener('click', event => void this.onClick(event));
    panel.addEventListener('change', event => void this.onChange(event));
    try {
      this.view = await this.request();
    } catch (_) {
      this.view = null;
    }
    this.render();
  }

  status(message) {
    const line = globalThis.document?.getElementById('homeProfileStatus');
    if (line) line.textContent = message || '';
  }

  render() {
    const panel = this.panel;
    if (!panel) return;
    const card = profileView(this.view);
    panel.hidden = !card.visible;
    if (!card.visible) return;
    document.getElementById('homeProfileTitle').textContent = card.title;
    document.getElementById('homeProfileIntro').textContent = card.intro;
    const readOnly = document.getElementById('homeProfileReadOnly');
    readOnly.hidden = !card.readOnly;
    readOnly.textContent = card.readOnlyNote;
    const detect = document.getElementById('homeProfileDetect');
    detect.hidden = !card.detect.visible;
    detect.textContent = card.detect.label;
    detect.disabled = this.busy || card.detect.disabled;
    const rows = document.getElementById('homeProfileRows');
    rows.replaceChildren(...card.rows.map(row => this.renderRow(row)).filter(Boolean));
  }

  renderRow(row) {
    const section = element('section', 'home-profile-row');
    section.dataset.kind = row.kind;
    const heading = element('h3', '', row.label);
    heading.id = `homeProfileRow-${row.kind}`;
    section.setAttribute('aria-labelledby', heading.id);
    section.append(heading);
    if (row.kind === 'apps') this.renderApps(section);
    else if (row.kind === 'main_app') this.renderMainApp(section, heading.id);
    else return null;
    return section;
  }

  renderApps(section) {
    const view = appsView(this.view);
    if (view.rows.length) {
      const list = element('ul', 'home-profile-apps');
      for (const app of view.rows) {
        const item = element('li', 'home-profile-app');
        item.dataset.appId = app.id;
        const name = element('span', 'home-profile-app-name', app.name);
        if (app.version) name.append(' ', element('span', 'home-profile-app-version', app.version));
        const actions = element('span', 'home-profile-actions');
        const off = this.busy || app.disabled;
        if (app.canConfirm) actions.append(button('Confirm', 'confirm-app', app.id, off));
        actions.append(button('Not mine', 'hide-app', app.id, off));
        item.append(
          name,
          badge(app.badge, app.badgeKind),
          element('span', 'home-profile-detail', app.detail),
          actions
        );
        list.append(item);
      }
      section.append(list);
    }
    if (view.empty) section.append(element('p', 'home-profile-note', view.empty));
    if (view.hidden.length) {
      const hidden = element('p', 'home-profile-note home-profile-hidden');
      hidden.append('Not yours: ');
      view.hidden.forEach((app, index) => {
        if (index) hidden.append(', ');
        hidden.append(
          app.name,
          ' ',
          button('Show again', 'show-app', app.id, this.busy || app.disabled)
        );
      });
      section.append(hidden);
    }
  }

  renderMainApp(section, labelId) {
    const view = mainAppView(this.view);
    const line = element('div', 'home-profile-main');
    const select = element('select', 'form-select home-profile-select');
    select.id = 'homeProfileMainApp';
    select.setAttribute('aria-labelledby', labelId);
    select.disabled = this.busy || view.disabled;
    const none = element('option', '', 'Not chosen');
    none.value = '';
    select.append(none);
    for (const option of view.options) {
      const node = element('option', '', option.name);
      node.value = option.id;
      select.append(node);
    }
    select.value = view.value;
    line.append(select);
    if (view.badge) line.append(badge(view.badge, view.badgeKind));
    if (view.canConfirm) line.append(button('Confirm', 'confirm-main', view.value, this.busy));
    section.append(line, element('p', 'home-profile-note', view.note));
  }

  // One owner action: disable the card, post, show the new card, say what
  // happened. A profile that changed somewhere else is re-read, never
  // overwritten.
  async run(message, action, work) {
    if (this.busy || this.view?.read_only) return;
    this.busy = true;
    this.panel?.setAttribute('aria-busy', 'true');
    this.render();
    this.status(message);
    try {
      this.view = await work();
      this.status(profileStatus(action, this.view));
    } catch (error) {
      if (error.code === 'home_profile_changed' || error.code === 'home_read_only') {
        try {
          this.view = await this.request();
        } catch (_) {
          // Keep the card as it was; the message below says what happened.
        }
      }
      this.status(
        error.code === 'home_profile_changed'
          ? 'This profile changed somewhere else, so nothing was saved. It has been refreshed.'
          : error.message || 'The profile could not be saved. Try again.'
      );
    } finally {
      this.busy = false;
      this.panel?.setAttribute('aria-busy', 'false');
      this.render();
    }
  }

  fields(body) {
    return this.post('/fields', { if_revision: Number(this.view?.revision || 0), ...body });
  }

  async onClick(event) {
    const trigger = event.target?.closest?.('[data-profile-action]');
    if (!trigger || trigger.disabled) return;
    const id = trigger.dataset.profileId || '';
    switch (trigger.dataset.profileAction) {
      case 'detect':
        await this.run('Looking in the Applications folders…', 'detect', () =>
          this.post('/detect')
        );
        break;
      case 'confirm-app':
        await this.run('Saving…', 'confirm', () => this.fields({ confirm_apps: [id] }));
        break;
      case 'hide-app':
        await this.run('Saving…', 'hide', () => this.fields({ hide_apps: [id] }));
        break;
      case 'show-app':
        await this.run('Saving…', 'show', () => this.fields({ show_apps: [id] }));
        break;
      case 'confirm-main':
        await this.run('Saving…', 'confirm', () => this.fields({ main_app: id }));
        break;
      default:
    }
  }

  async onChange(event) {
    if (event.target?.id !== 'homeProfileMainApp') return;
    const value = String(event.target.value || '');
    await this.run('Saving…', 'main_app', () => this.fields({ main_app: value }));
  }
}
