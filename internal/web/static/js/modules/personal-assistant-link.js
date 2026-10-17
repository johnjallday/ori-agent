// The composer's Add link: one link per personal conversation. The server
// reads what the address says about itself once and holds that observation;
// the drawer keeps only the id it was given and sends it with a turn. Nothing
// is recovered from browser storage, and a link never follows the user into
// another conversation.

const ENDPOINT = '/api/home-assistant/link-context';
const BUILD_ENDPOINT = '/api/workspaces/build-sessions';

export const LINK_COPY = {
  empty: 'Paste a link first.',
  checking: 'Checking…',
  refused: 'That link could not be used. Nothing was attached.',
  lost: 'The attached link was let go after 30 minutes unused. Attach it again; your message is kept.',
  send: 'Tell me about this link.',
  hint: 'Empty Send: “Tell me about this link”',
  remove: 'Remove link',
  buildUnavailable:
    'Create Workspace can’t build with your assistant right now. Nothing was created; your link is still attached here.',
  buildRefused: 'The link could not be added to a workspace build. Nothing was changed.'
};

/** What an attached link is called: its title, a repository's name, or its host. */
export function linkDisplayName(observation) {
  if (!observation || typeof observation !== 'object') return '';
  return String(observation.title || observation.repo?.full_name || observation.host || '').trim();
}

/** The one line under an attached link: what it is and what Ori could see. */
export function linkSummary(observation) {
  if (!observation || typeof observation !== 'object') return '';
  const host = String(observation.host || '').trim();
  const repo = observation.repo && typeof observation.repo === 'object' ? observation.repo : null;
  if (observation.kind === 'github_repo' && repo) {
    if (repo.reachable !== 'yes') return 'GitHub repository · not verified';
    const parts = ['GitHub repository'];
    if (String(repo.language || '').trim()) parts.push(String(repo.language).trim());
    const issues = Math.max(0, Number(repo.open_issues) || 0);
    parts.push(`${issues.toLocaleString('en-US')} open ${issues === 1 ? 'issue' : 'issues'}`);
    return parts.join(' · ');
  }
  if (observation.kind === 'document') return `Document · ${host} · not read`;
  if (observation.readable !== true) return `Could only read the address · ${host}`;
  return `Page · ${host}`;
}

/**
 * Pure state with injected I/O. A result that arrives after the conversation
 * changed, or after the link was removed or replaced, is dropped.
 */
export function createLinkController({ post, changed = () => {}, currentId }) {
  const state = {
    conversationId: currentId(),
    link: null,
    pending: false,
    notice: '',
    failed: false,
    generation: 0
  };
  const done = () => changed(state);

  async function select(url) {
    const address = String(url || '').trim();
    if (state.pending) return false;
    if (!address) {
      state.notice = LINK_COPY.empty;
      state.failed = true;
      done();
      return false;
    }
    const generation = ++state.generation;
    const owner = currentId();
    state.pending = true;
    state.notice = LINK_COPY.checking;
    state.failed = false;
    done();
    try {
      const body = { url: address };
      if (state.link?.id) body.replaces = state.link.id;
      const result = await post(`${ENDPOINT}/select`, body);
      if (generation !== state.generation || owner !== currentId()) return false;
      if (!result?.ok || !result.body?.link_id) {
        state.notice = String(result?.body?.error || '').trim() || LINK_COPY.refused;
        state.failed = true;
        return false;
      }
      state.link = { id: String(result.body.link_id), observation: result.body.observation || {} };
      state.conversationId = owner;
      state.notice = '';
      state.failed = false;
      return true;
    } catch (_) {
      if (generation === state.generation) {
        state.notice = LINK_COPY.refused;
        state.failed = true;
      }
      return false;
    } finally {
      if (generation === state.generation) {
        state.pending = false;
        done();
      }
    }
  }

  async function remove() {
    const link = state.link;
    if (!link || state.pending) return false;
    state.generation++;
    state.link = null;
    state.notice = '';
    state.failed = false;
    done();
    try {
      await post(`${ENDPOINT}/detach`, { link_id: link.id });
    } catch (_) {
      // The server lets an unused link go by itself.
    }
    return true;
  }

  /** The reference a turn carries, or null. Only the conversation that attached it. */
  function request() {
    if (!state.link || state.pending || state.conversationId !== currentId()) return null;
    return { link_id: state.link.id };
  }

  /** A link attached in a new draft follows that draft into the conversation it started. */
  function adopt(id) {
    if (state.link && !state.conversationId && id) {
      state.conversationId = String(id);
      done();
    }
  }

  /** A new conversation starts without a link. */
  function reset() {
    const link = state.link;
    state.generation++;
    state.link = null;
    state.pending = false;
    state.notice = '';
    state.failed = false;
    state.conversationId = currentId();
    done();
    if (link) void post(`${ENDPOINT}/detach`, { link_id: link.id }).catch(() => {});
  }

  /** The server no longer holds the link a turn named. */
  function lost() {
    state.generation++;
    state.link = null;
    state.pending = false;
    state.notice = LINK_COPY.lost;
    state.failed = true;
    done();
  }

  /** Whether the attached link belongs to the conversation on screen. */
  function shown() {
    return Boolean(state.link) && state.conversationId === currentId();
  }

  return { state, select, remove, request, adopt, reset, lost, shown };
}

/**
 * "Set up a workspace from this link": start (or join) the user's workspace
 * build, attach the same address to it, and open Create Workspace on it. The
 * build is started without a first message so its first turn always sees the
 * link; the pane offers "Set it up from the attached link" as an empty Send.
 */
export async function startLinkBuild(url, { request, resume, notify }) {
  const address = String(url || '').trim();
  if (!address) return false;
  let created;
  try {
    created = await request('POST', BUILD_ENDPOINT, { entry_point: 'personal_assistant_ask' });
  } catch (_) {
    created = { ok: false, body: {} };
  }
  const session = created?.body?.session;
  if (!created?.ok || !session?.id) {
    notify(LINK_COPY.buildUnavailable);
    return false;
  }
  let attached;
  try {
    attached = await request('POST', `${BUILD_ENDPOINT}/${encodeURIComponent(session.id)}/link`, {
      url: address,
      version: Number(session.version) || 0
    });
  } catch (_) {
    attached = { ok: false, body: {} };
  }
  if (!attached?.ok) {
    notify(String(attached?.body?.error || '').trim() || LINK_COPY.buildRefused);
    return false;
  }
  resume();
  return true;
}

// ----- the drawer -----

let controller = null;
let elements = null;
let available = false;

async function jsonRequest(method, url, body) {
  const options = { method, headers: { Accept: 'application/json' } };
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(body);
  }
  const response = await fetch(url, options);
  let data = {};
  try {
    data = await response.json();
  } catch (_) {
    data = {};
  }
  return { ok: response.ok, status: response.status, body: data || {} };
}

function currentConversationId() {
  return window.PersonalAssistantConversation?.currentId?.() || '';
}

function render() {
  if (!elements || !controller) return;
  const { state } = controller;
  const link = controller.shown() ? state.link : null;
  const busy = state.pending;
  elements.add.hidden = !available;
  elements.add.disabled = busy;
  elements.active.hidden = !link;
  const name = linkDisplayName(link?.observation);
  elements.name.textContent = name;
  elements.icon.className =
    link?.observation?.kind === 'github_repo' ? 'bi bi-github' : 'bi bi-link-45deg';
  elements.remove.setAttribute(
    'aria-label',
    name ? `${LINK_COPY.remove} ${name}` : LINK_COPY.remove
  );
  elements.remove.disabled = busy;
  const summary = link ? linkSummary(link.observation) : '';
  if (elements.summary.textContent !== summary) elements.summary.textContent = summary;
  elements.summary.hidden = !summary;
  elements.input.disabled = busy;
  elements.attach.disabled = busy;
  elements.attach.textContent = busy ? LINK_COPY.checking : 'Attach';
  const notice = String(state.notice || '');
  if (elements.status.textContent !== notice) elements.status.textContent = notice;
  elements.status.hidden = !notice;
  elements.status.classList.toggle('is-error', Boolean(state.failed && notice));
  updateSendHint();
}

function updateSendHint() {
  if (!elements) return;
  const empty = !String(document.getElementById('personalAssistantInput')?.value || '').trim();
  const folder = window.PersonalAssistantFolderContext?.hasFolder?.() === true;
  elements.hint.hidden = !(controller?.shown() && empty && !folder);
}

function openField() {
  if (!elements || controller?.state.pending) return;
  window.PersonalAssistantFolderContext?.close?.();
  elements.field.hidden = false;
  elements.add.setAttribute('aria-expanded', 'true');
  elements.input.focus();
}

function closeField({ restoreFocus = true } = {}) {
  if (!elements || elements.field.hidden) return;
  elements.field.hidden = true;
  elements.add.setAttribute('aria-expanded', 'false');
  if (controller && !controller.state.pending) {
    controller.state.notice = '';
    controller.state.failed = false;
  }
  render();
  if (restoreFocus) elements.add.focus();
}

async function attach() {
  if (!controller || controller.state.pending) return;
  if (await controller.select(elements.input.value)) {
    elements.input.value = '';
    closeField({ restoreFocus: false });
    document.getElementById('personalAssistantInput')?.focus();
  } else {
    elements.input.focus();
  }
}

async function refreshAvailability() {
  try {
    const result = await jsonRequest('GET', `${ENDPOINT}/availability`);
    available = Boolean(result.ok && result.body?.available === true);
  } catch (_) {
    available = false;
  }
  render();
}

function init() {
  const add = document.getElementById('personalAssistantLinkChip');
  if (!add || controller) return;
  elements = {
    add,
    field: document.getElementById('personalAssistantLinkField'),
    input: document.getElementById('personalAssistantLinkInput'),
    attach: document.getElementById('personalAssistantLinkAttach'),
    cancel: document.getElementById('personalAssistantLinkCancel'),
    status: document.getElementById('personalAssistantLinkStatus'),
    active: document.getElementById('personalAssistantActiveLink'),
    icon: document.getElementById('personalAssistantActiveLinkIcon'),
    name: document.getElementById('personalAssistantActiveLinkName'),
    remove: document.getElementById('personalAssistantRemoveLink'),
    summary: document.getElementById('personalAssistantLinkSummary'),
    hint: document.getElementById('personalAssistantLinkSendHint')
  };
  controller = createLinkController({
    post: (url, body) => jsonRequest('POST', url, body),
    changed: render,
    currentId: currentConversationId
  });
  add.addEventListener('click', () => (elements.field.hidden ? openField() : closeField()));
  elements.attach.addEventListener('click', () => void attach());
  elements.cancel.addEventListener('click', () => closeField());
  elements.input.addEventListener('input', () => {
    if (!controller.state.pending && controller.state.notice) {
      controller.state.notice = '';
      controller.state.failed = false;
      render();
    }
  });
  elements.input.addEventListener('keydown', event => {
    if (event.isComposing) return;
    if (event.key === 'Enter') {
      event.preventDefault();
      event.stopPropagation();
      void attach();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      closeField();
    }
  });
  elements.remove.addEventListener('click', async () => {
    if (await controller.remove()) elements.add.focus();
  });
  document.getElementById('personalAssistantInput')?.addEventListener('input', updateSendHint);
  document.addEventListener('personal-assistant:sent', updateSendHint);
  void refreshAvailability();
  render();
}

const api = {
  request: () => controller?.request() || null,
  hasLink: () => controller?.shown() === true,
  isPending: () => controller?.state.pending === true,
  sendText: () => LINK_COPY.send,
  adopt: id => controller?.adopt(id),
  reset: () => controller?.reset(),
  lost: () => controller?.lost(),
  refresh: () => render(),
  close: () => closeField({ restoreFocus: false }),
  startBuild: url =>
    startLinkBuild(url, {
      request: jsonRequest,
      notify: message => window.PersonalAssistantConversation?.notify?.(message),
      resume: async () => {
        const { resumeWorkspaceBuild } = await import('./personal-assistant-home.js');
        resumeWorkspaceBuild();
      }
    }),
  _controller: () => controller
};
if (typeof window !== 'undefined') window.PersonalAssistantLink = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
