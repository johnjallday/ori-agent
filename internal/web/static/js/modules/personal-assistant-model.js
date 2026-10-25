// Read-only next-message configuration. Never infer it from a roster profile or
// attach it to historical replies. A turn holds its displayed snapshot stable.

export function modelDescription(selection) {
  if (selection?.state === 'configured' && selection.provider && selection.model) {
    return `${selection.provider} / ${selection.model}`;
  }
  if (selection?.state === 'unconfigured') return 'not configured';
  if (selection?.state === 'unavailable') return 'unavailable';
  return 'unknown';
}

export function modelEndpoint(context = {}) {
  if (context.kind === 'workspace_entry') {
    const query = new URLSearchParams({ studio_id: context.workspaceId || '' });
    if (context.name) query.set('agent_name', context.name);
    if (context.sessionId) query.set('session_id', context.sessionId);
    return `/api/chat/model?${query}`;
  }
  const query = new URLSearchParams();
  if (context.sessionId) query.set('conversation_id', context.sessionId);
  return `/api/home-assistant/model${query.size ? `?${query}` : ''}`;
}

// Accept only the destinations this feature's server can issue, never an
// arbitrary server-supplied URL or a stale recipient's inferred editor.
export function modelSettingsHref(selection) {
  const href = String(selection?.settings_href || '');
  if (selection?.source === 'default' && href === '/settings#system-model') return href;
  if (selection?.source === 'agent' && /^\/workspaces\/[^/?#]+\?editAgentModel=[^&#]+$/.test(href))
    return href;
  return '';
}

export async function readModelSelection(context = {}, { signal } = {}) {
  const response = await fetch(modelEndpoint(context), {
    headers: { Accept: 'application/json' },
    cache: 'no-store',
    signal
  });
  if (!response.ok) throw new Error('Model configuration unavailable');
  return response.json();
}

const linkedNoteReads = new WeakMap();

export async function refreshLinkedModelNote(
  element,
  linked,
  { read = readDefaultSelection } = {}
) {
  if (!element) return;
  const ticket = (linkedNoteReads.get(element) || 0) + 1;
  linkedNoteReads.set(element, ticket);
  element.hidden = !linked;
  element.textContent = linked ? 'Conversation uses Default model: checking…' : '';
  if (!linked) return;
  let selection = null;
  try {
    selection = await read();
  } catch (_) {
    /* Unknown is not an old model. */
  }
  if (linkedNoteReads.get(element) !== ticket) return;
  element.textContent = `Conversation uses Default model: ${modelDescription(selection)}.`;
}

function readDefaultSelection() {
  return readModelSelection({}, { signal: AbortSignal.timeout(2500) });
}

export async function sharedModelSaveFeedback(
  mirror,
  { read = readDefaultSelection, emit = () => {} } = {}
) {
  if (!mirror) return { message: 'Agent saved successfully.', type: 'success' };
  let selection = null;
  try {
    selection = await read();
  } catch (_) {
    /* The save and read are distinct outcomes. */
  }
  if (mirror.mirrored) emit(selection);
  const current = selection
    ? `Conversation uses Default model: ${modelDescription(selection)}.`
    : 'The current conversational model could not be read. Check Default model in Settings.';
  if (!mirror.mirrored) {
    const reason = String(mirror.reason || 'Check Settings before retrying.').trim();
    return {
      message: `Agent saved, but the Default model update was not confirmed. ${reason}${/[.!?]$/.test(reason) ? '' : '.'} ${current}`,
      type: 'error'
    };
  }
  return {
    message: `Agent saved. Default model update saved. ${current}`,
    type: selection ? 'success' : 'error'
  };
}

export function createModelController({ load = readModelSelection, render = () => {} } = {}) {
  let context = {};
  let generation = 0;
  let request = null;
  let frozen = false;
  let value = null;
  let closed = true;
  const show = loading => render({ selection: value, loading, frozen, hidden: closed });

  async function refresh() {
    if (closed || frozen) return value;
    const ticket = ++generation;
    request?.abort();
    request = new AbortController();
    const active = request;
    value = null;
    show(true);
    // An unavailable metadata server must not hold a user's Send indefinitely.
    const timer = setTimeout(() => active.abort(), 2500);
    try {
      const loaded = await load({ ...context }, { signal: active.signal });
      if (ticket !== generation || active.signal.aborted) return null;
      value = loaded;
    } catch (_) {
      if (ticket !== generation) return null;
      value = null;
    } finally {
      clearTimeout(timer);
      if (ticket === generation) show(false);
    }
    return value;
  }

  return {
    refresh,
    setContext(next) {
      if (JSON.stringify(context) === JSON.stringify(next)) return false;
      const sameRecipient =
        context.kind === next.kind &&
        context.workspaceId === next.workspaceId &&
        context.name === next.name;
      context = { ...next };
      generation++;
      request?.abort();
      // A first turn can acquire a session ID while still answering. That does
      // not change who is answering or relabel the in-flight selection.
      if (!frozen || !sameRecipient) {
        value = null;
        show(false);
      }
      return true;
    },
    open() {
      closed = false;
      return refresh();
    },
    close() {
      closed = true;
      generation++;
      request?.abort();
      show(false);
    },
    async beforeSend() {
      await refresh();
      frozen = true;
      show(false);
    },
    setBusy(busy) {
      if (busy) {
        generation++;
        request?.abort();
        frozen = true;
        show(false);
        return;
      }
      if (!frozen) return;
      frozen = false;
      return refresh();
    },
    snapshot: () => ({ selection: value, frozen })
  };
}

function currentContext() {
  const recipient = window.PersonalAssistantRecipient?.current?.() || { kind: 'personal' };
  const entry = recipient.kind === 'workspace_entry';
  return {
    kind: recipient.kind,
    workspaceId: entry ? recipient.workspaceId : '',
    name: entry ? recipient.name : '',
    sessionId: entry
      ? window.PersonalAssistantRecipient?.sessionId?.() || ''
      : window.PersonalAssistantConversation?.currentId?.() || ''
  };
}

export function bindModelLifecycle(controller, { doc, win, context = currentContext }) {
  const sync = () => controller.setContext(context());
  const refresh = () => {
    sync();
    return controller.refresh();
  };
  doc.addEventListener('personal-assistant:opened', () => {
    sync();
    void controller.open();
  });
  doc.addEventListener('personal-assistant:closed', () => controller.close());
  for (const event of [
    'personal-assistant:recipient',
    'personal-assistant:conversation',
    'personal-assistant:entry-session'
  ]) {
    doc.addEventListener(event, () => {
      if (sync()) void controller.refresh();
    });
  }
  doc.addEventListener('personal-assistant:busy', event => {
    void controller.setBusy(event.detail?.busy === true);
  });
  doc.addEventListener('personal-assistant:status', refresh);
  win.EventBus?.on?.('systemModel:changed', refresh);
  win.addEventListener('systemModel:changed', refresh);
  win.addEventListener('focus', refresh);
  doc.addEventListener('visibilitychange', () => {
    if (!doc.hidden) void refresh();
  });
  return {
    beforeSend: async () => {
      sync();
      await controller.beforeSend();
    },
    afterSend: () => controller.setBusy(false),
    refresh,
    readDefault: readDefaultSelection,
    renderLinkedNote: refreshLinkedModelNote,
    saveFeedback: mirror =>
      sharedModelSaveFeedback(mirror, {
        emit: selection =>
          win.EventBus?.emit?.('systemModel:changed', {
            provider: selection?.provider || '',
            model: selection?.model || ''
          })
      }),
    describe: modelDescription
  };
}

function init() {
  const line = document.getElementById('personalAssistantModel');
  if (!line) return;
  const text = document.getElementById('personalAssistantModelText');
  const link = document.getElementById('personalAssistantModelSettings');
  const controller = createModelController({
    render({ selection, loading, frozen, hidden }) {
      line.hidden = hidden;
      text.textContent = `Model: ${loading ? 'checking…' : modelDescription(selection)}`;
      text.title = text.textContent;
      line.title = frozen
        ? 'Configuration checked before this turn. Not attribution for a historical reply.'
        : 'Configured for new messages. Provider connection has not been checked.';
      const href = loading ? '' : modelSettingsHref(selection);
      link.hidden = !href;
      link.removeAttribute('href');
      if (href) link.href = href;
      link.setAttribute(
        'aria-label',
        selection?.source === 'agent' ? 'Edit this agent’s model' : 'Edit Default model'
      );
    }
  });
  window.PersonalAssistantModel = bindModelLifecycle(controller, { doc: document, win: window });
  if (document.getElementById('personalAssistantPanel')?.hidden === false) {
    controller.setContext(currentContext());
    void controller.open();
  }
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
