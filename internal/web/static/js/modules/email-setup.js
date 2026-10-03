// email-setup.js — the "Set up email" card.
//
// Two questions: the address, then the app password. The server works out who
// hosts the address (GET /api/email-setup/detect), and the card shows only what
// that provider needs: where to make an app password, and a username or server
// field when the provider needs one. Connect (POST /api/email-setup/connect)
// checks the login before anything is saved, keeps it in Ori's Email vault, and
// links the mailbox to the user's Email Ops workspace, creating it when needed.
//
// Every surface that offers email setup links to /?setup=email. This module opens
// the card for that URL on load, and for any click on a link to it, so the link
// works in place on any page that loads the module. window.OriEmailSetup.open()
// opens it from code.
//
// Pure helpers are exported for email-setup.test.js.

export const EMAIL_SETUP_PARAM = 'email';
export const DETECT_URL = '/api/email-setup/detect';
export const CONNECT_URL = '/api/email-setup/connect';

// isEmailSetupHref reports whether href opens this card: same origin, root path,
// setup=email.
export function isEmailSetupHref(href, origin) {
  if (!href) return false;
  let url;
  try {
    url = new URL(href, origin);
  } catch (_) {
    return false;
  }
  return (
    url.origin === origin &&
    url.pathname === '/' &&
    url.searchParams.get('setup') === EMAIL_SETUP_PARAM
  );
}

// withoutSetupParam is the current URL once the card has consumed its deep link.
export function withoutSetupParam(href) {
  const url = new URL(href);
  url.searchParams.delete('setup');
  const query = url.searchParams.toString();
  return url.pathname + (query ? '?' + query : '') + url.hash;
}

// fieldsFor says which inputs the second step shows for a provider profile.
export function fieldsFor(profile) {
  return {
    username: Boolean(profile?.needs_username),
    server: Boolean(profile?.needs_server)
  };
}

// connectPayload is the request body. Optional fields are sent only when the
// card asked for them, so the server's own defaults apply otherwise.
export function connectPayload(profile, values) {
  const body = {
    address: String(profile?.address || ''),
    password: String(values?.password || '')
  };
  const fields = fieldsFor(profile);
  const username = String(values?.username || '').trim();
  if (fields.username && username) body.username = username;
  if (fields.server) {
    const host = String(values?.server || '').trim();
    if (host) body.imap_host = host;
    const port = Number.parseInt(String(values?.port || ''), 10);
    if (Number.isInteger(port) && port > 0) body.imap_port = port;
  }
  return body;
}

// failureView places a server failure: next to its field when it names one,
// otherwise at the top of the card.
export function failureView(failure) {
  const message = String(failure?.message || '').trim() || 'Email setup failed. Try again.';
  const field = ['address', 'password', 'username', 'server'].includes(failure?.field)
    ? failure.field
    : '';
  return { field, message, code: String(failure?.error || '') };
}

// receiptLines is what the done step lists: each thing that is now true.
export function receiptLines(result) {
  const label = String(result?.label || 'your mail provider');
  const address = String(result?.address || '');
  const workspace = result?.workspace || {};
  const name = String(workspace.name || 'Email Ops');
  return [
    `Signed in to ${label} as ${address}`,
    'Your login is saved in Ori’s Email vault, which this computer’s keychain unlocks',
    workspace.created
      ? `Created ${name} and linked your inbox to it`
      : `Linked your inbox to ${name}`,
    'Ori reads and searches your mail and drafts replies; nothing is sent without you'
  ];
}

// safeRoute keeps navigation on this origin.
export function safeRoute(route) {
  const value = String(route || '');
  return value.startsWith('/') && !value.startsWith('//') ? value : '';
}

const MODAL_WAIT_MS = 100;
const MODAL_WAIT_ATTEMPTS = 50;

const READ_ONLY_NOTE =
  'Ori reads and searches this inbox and can prepare drafts. It never sends, deletes, or changes anything without you.';

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

// createEmailSetupCard builds the modal once and returns its controller. fetch
// and bootstrap are injectable for tests.
export function createEmailSetupCard(deps = {}) {
  const fetchImpl = deps.fetchImpl || ((...args) => globalThis.fetch(...args));
  const doc = deps.document || globalThis.document;
  const state = {
    phase: 'address',
    profile: null,
    busy: false,
    result: null,
    failure: null,
    onDone: null
  };

  const root = el('div', {
    className: 'modal fade email-setup',
    id: 'emailSetupModal',
    tabindex: '-1',
    'aria-labelledby': 'emailSetupTitle',
    'aria-hidden': 'true'
  });
  const dialog = el('div', { className: 'modal-dialog modal-dialog-centered' });
  const frame = el('div', { className: 'modal-content email-setup__frame' });
  const header = el('div', { className: 'email-setup__header' });
  const heading = el('div', { className: 'email-setup__heading' });
  heading.append(
    el('p', { className: 'email-setup__eyebrow' }, 'Email'),
    el('h2', { className: 'email-setup__title', id: 'emailSetupTitle' }, 'Set up email')
  );
  const close = el('button', {
    type: 'button',
    className: 'btn-close',
    'data-bs-dismiss': 'modal',
    'aria-label': 'Close'
  });
  header.append(heading, close);
  const form = el('form', { className: 'email-setup__body', novalidate: true });
  frame.append(header, form);
  dialog.append(frame);
  root.append(dialog);

  let modal = null;
  function bootstrapModal() {
    if (modal) return modal;
    const Modal = deps.Modal || globalThis.bootstrap?.Modal;
    modal = Modal ? Modal.getOrCreateInstance(root) : null;
    return modal;
  }

  // Pages load Bootstrap with a deferred script placed after this module, so on
  // a deep link the card is asked to open before Bootstrap exists. Wait for it
  // briefly; a page that never loads it hands the card to Home instead.
  function whenModalReady(callback, attempts = MODAL_WAIT_ATTEMPTS) {
    const instance = bootstrapModal();
    if (instance || attempts <= 0) {
      callback(instance);
      return;
    }
    setTimeout(() => whenModalReady(callback, attempts - 1), MODAL_WAIT_MS);
  }

  function mount() {
    if (!root.isConnected && doc?.body) doc.body.appendChild(root);
  }

  function reset() {
    state.phase = 'address';
    state.profile = null;
    state.busy = false;
    state.result = null;
    state.failure = null;
  }

  // --- rendering -------------------------------------------------------------

  function render() {
    form.textContent = '';
    root.dataset.phase = state.phase;
    const status = el('p', {
      className: 'email-setup__status',
      role: 'status',
      'aria-live': 'polite'
    });
    if (state.failure && !state.failure.field) {
      status.classList.add('is-error');
      status.textContent = state.failure.message;
    }
    if (state.phase === 'address') renderAddress(status);
    else if (state.phase === 'password') renderPassword(status);
    else renderDone();
    const focus = form.querySelector('[data-autofocus]');
    if (focus && typeof focus.focus === 'function') focus.focus();
  }

  function fieldError(name) {
    return state.failure && state.failure.field === name ? state.failure.message : '';
  }

  function inputRow({
    name,
    label,
    type = 'text',
    value = '',
    hint = '',
    autocomplete,
    autofocus,
    inputmode
  }) {
    const id = `emailSetup-${name}`;
    const row = el('div', { className: 'email-setup__field' });
    row.append(el('label', { for: id }, label));
    const errorText = fieldError(name);
    const describedBy = [hint ? `${id}-hint` : '', errorText ? `${id}-error` : '']
      .filter(Boolean)
      .join(' ');
    const input = el('input', {
      id,
      name,
      type,
      className: 'form-control',
      autocomplete: autocomplete || 'off',
      inputmode,
      spellcheck: 'false',
      'aria-invalid': errorText ? 'true' : false,
      'aria-describedby': describedBy || false,
      'data-autofocus': autofocus || Boolean(errorText) || false
    });
    input.value = value;
    row.append(input);
    if (hint) row.append(el('p', { className: 'email-setup__hint', id: `${id}-hint` }, hint));
    if (errorText)
      row.append(el('p', { className: 'email-setup__error', id: `${id}-error` }, errorText));
    return row;
  }

  function actions(primaryLabel, secondary) {
    const row = el('div', { className: 'email-setup__actions' });
    if (secondary) row.append(secondary);
    const primary = el(
      'button',
      { type: 'submit', className: 'btn btn-primary', disabled: state.busy },
      state.busy ? 'Checking…' : primaryLabel
    );
    row.append(primary);
    return row;
  }

  function renderAddress(status) {
    form.append(
      el(
        'p',
        { className: 'email-setup__lead' },
        'Enter your email address. Ori works out the rest and asks only for what your provider needs.'
      ),
      inputRow({
        name: 'address',
        label: 'Email address',
        type: 'email',
        value: state.profile?.address || '',
        autocomplete: 'email',
        inputmode: 'email',
        autofocus: true
      }),
      status,
      actions('Continue')
    );
    form.onsubmit = event => {
      event.preventDefault();
      void detect(String(form.elements.address?.value || ''));
    };
  }

  function renderPassword(status) {
    const profile = state.profile || {};
    const fields = fieldsFor(profile);

    const who = el('div', { className: 'email-setup__who' });
    who.append(el('span', { className: 'email-setup__provider' }, profile.label || 'Email'));
    who.append(el('span', { className: 'email-setup__address' }, profile.address || ''));
    const change = el(
      'button',
      { type: 'button', className: 'btn btn-link email-setup__change' },
      'Change'
    );
    change.addEventListener('click', () => {
      state.phase = 'address';
      state.failure = null;
      render();
    });
    who.append(change);
    form.append(who);

    if (profile.warning) {
      form.append(el('p', { className: 'email-setup__warning', role: 'note' }, profile.warning));
    }

    const steps = el('div', { className: 'email-setup__steps' });
    if (profile.app_password_steps) steps.append(el('p', {}, profile.app_password_steps));
    const link = safeExternal(profile.app_password_url);
    if (link) {
      const anchor = el(
        'a',
        {
          href: link,
          target: '_blank',
          rel: 'noopener noreferrer',
          className: 'email-setup__link'
        },
        'Create an app password'
      );
      anchor.append(el('i', { className: 'bi bi-box-arrow-up-right', 'aria-hidden': 'true' }));
      steps.append(anchor);
    }
    if (steps.childNodes.length) form.append(steps);

    if (fields.username) {
      form.append(
        inputRow({
          name: 'username',
          label: 'Username',
          hint: profile.username_hint || '',
          autocomplete: 'username',
          autofocus: true
        })
      );
    }
    if (fields.server) {
      const server = inputRow({
        name: 'server',
        label: 'IMAP server',
        value: profile.imap_host || '',
        hint: 'Your provider’s help pages list it. Ori connects securely on port 993 unless you change it.',
        autofocus: !fields.username
      });
      const port = el('input', {
        id: 'emailSetup-port',
        name: 'port',
        type: 'text',
        inputmode: 'numeric',
        className: 'form-control email-setup__port',
        'aria-label': 'IMAP port'
      });
      port.value = String(profile.imap_port || 993);
      server.querySelector('input')?.after(port);
      form.append(server);
    }
    form.append(
      inputRow({
        name: 'password',
        label: 'App password',
        type: 'password',
        autocomplete: 'off',
        autofocus: !fields.username && !fields.server
      }),
      el('p', { className: 'email-setup__note' }, READ_ONLY_NOTE),
      status,
      actions('Connect')
    );
    form.onsubmit = event => {
      event.preventDefault();
      void connect({
        password: form.elements.password?.value,
        username: form.elements.username?.value,
        server: form.elements.server?.value,
        port: form.elements.port?.value
      });
    };
  }

  function renderDone() {
    const result = state.result || {};
    form.append(el('p', { className: 'email-setup__lead' }, 'Email is ready.'));
    const list = el('ul', { className: 'email-setup__receipt' });
    for (const line of receiptLines(result)) {
      const item = el('li');
      item.append(el('i', { className: 'bi bi-check2', 'aria-hidden': 'true' }));
      item.append(el('span', {}, line));
      list.append(item);
    }
    form.append(list);
    const row = el('div', { className: 'email-setup__actions' });
    const route = safeRoute(result.workspace?.route);
    const done = el(
      'button',
      { type: 'button', className: route ? 'btn btn-outline-secondary' : 'btn btn-primary' },
      'Done'
    );
    done.addEventListener('click', () => bootstrapModal()?.hide());
    row.append(done);
    if (route) {
      const open = el(
        'a',
        { href: route, className: 'btn btn-primary', 'data-autofocus': true },
        `Open ${result.workspace?.name || 'Email Ops'}`
      );
      row.append(open);
    }
    form.append(row);
    form.onsubmit = event => event.preventDefault();
  }

  function safeExternal(href) {
    const value = String(href || '');
    return /^https:\/\/[^\s/]+\//.test(value) ? value : '';
  }

  // --- requests --------------------------------------------------------------

  async function detect(address) {
    if (state.busy) return;
    state.busy = true;
    state.failure = null;
    state.profile = { address };
    render();
    try {
      const response = await fetchImpl(`${DETECT_URL}?address=${encodeURIComponent(address)}`, {
        headers: { Accept: 'application/json' }
      });
      const body = await readJSON(response);
      if (!response.ok) {
        state.failure = failureView(body);
        if (!state.failure.field) state.failure.field = 'address';
        return;
      }
      const profile = body.profile || {};
      if (profile.supported === false) {
        state.profile = profile;
        state.failure = {
          field: 'address',
          message: profile.unsupported || 'This address can’t be connected yet.'
        };
        return;
      }
      state.profile = profile;
      state.phase = 'password';
    } catch (_) {
      state.failure = { field: '', message: 'Ori couldn’t check that address. Try again.' };
    } finally {
      state.busy = false;
      render();
    }
  }

  async function connect(values) {
    if (state.busy) return;
    state.busy = true;
    state.failure = null;
    render();
    // Keep the typed values across the re-render while the request runs.
    restore(values);
    try {
      const response = await fetchImpl(CONNECT_URL, {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify(connectPayload(state.profile, values))
      });
      const body = await readJSON(response);
      if (!response.ok) {
        if (body.profile) state.profile = body.profile;
        state.failure = failureView(body);
        return;
      }
      state.result = body.result || {};
      state.phase = 'done';
      globalThis.window?.dispatchEvent?.(
        new CustomEvent('ori:email-setup-connected', { detail: state.result })
      );
    } catch (_) {
      state.failure = { field: '', message: 'Ori couldn’t reach the server. Try again.' };
    } finally {
      state.busy = false;
      render();
      if (state.phase === 'password') {
        // A refused password is retyped; any other failure keeps what was typed.
        const keep = state.failure?.field === 'password' ? '' : values?.password;
        restore({ ...values, password: keep });
      }
    }
  }

  function restore(values) {
    for (const name of ['username', 'server', 'port', 'password']) {
      const input = form.elements[name];
      if (input && values?.[name] !== undefined && values[name] !== null)
        input.value = values[name];
    }
  }

  // --- lifecycle -------------------------------------------------------------

  // Escape closes the card. Pages register their own capture-phase key
  // handlers, so the card takes Escape first, the way the setup journey does.
  globalThis.window?.addEventListener?.(
    'keydown',
    event => {
      if (event.key !== 'Escape' || !root.classList.contains('show')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      bootstrapModal()?.hide();
    },
    true
  );

  // A connect in flight finishes on the card that started it.
  root.addEventListener('hide.bs.modal', event => {
    if (state.busy) event.preventDefault();
  });

  root.addEventListener('hidden.bs.modal', () => {
    const connected = state.phase === 'done';
    const { onDone, result } = state;
    state.onDone = null;
    reset();
    // The password must not outlive the card.
    form.textContent = '';
    if (!connected) return;
    if (typeof onDone === 'function') onDone(result);
    else if (deps.reload !== false) globalThis.location?.reload?.();
  });

  function open(options = {}) {
    mount();
    reset();
    state.onDone = typeof options.onDone === 'function' ? options.onDone : null;
    render();
    whenModalReady(instance => {
      if (instance) {
        instance.show();
        return;
      }
      // No Bootstrap on this page: Home always has it.
      const location = globalThis.location;
      if (location && location.pathname !== '/') location.assign(`/?setup=${EMAIL_SETUP_PARAM}`);
    });
    return true;
  }

  return {
    open,
    root,
    get state() {
      return state;
    }
  };
}

function initialize() {
  const doc = globalThis.document;
  if (!doc?.body || globalThis.window?.OriEmailSetup) return;
  const card = createEmailSetupCard();
  globalThis.window.OriEmailSetup = { open: options => card.open(options) };

  // Any link to /?setup=email opens the card where the user is.
  doc.addEventListener('click', event => {
    if (event.defaultPrevented || event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = event.target?.closest?.('a[href]');
    if (!link || link.target === '_blank') return;
    if (!isEmailSetupHref(link.getAttribute('href'), globalThis.location.origin)) return;
    event.preventDefault();
    card.open();
  });

  const params = new URLSearchParams(globalThis.location.search);
  if (params.get('setup') === EMAIL_SETUP_PARAM) {
    globalThis.history?.replaceState?.({}, '', withoutSetupParam(globalThis.location.href));
    card.open();
  }
}

if (globalThis.document?.readyState === 'loading') {
  globalThis.document.addEventListener('DOMContentLoaded', initialize, { once: true });
} else if (globalThis.document?.body) {
  initialize();
}
