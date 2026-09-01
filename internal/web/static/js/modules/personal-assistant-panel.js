const STATUS_ENDPOINT = '/api/personal-assistant';
const HANDOFF_LIMIT = 400;

export function personalAssistantPanelView(personalAssistant) {
  const state = String(personalAssistant?.state || 'unavailable');
  const known = state !== 'unavailable';
  const available = state === 'active' || state === 'paused';
  const name = String(personalAssistant?.display_name || '').trim() || 'Personal assistant';
  return {
    state,
    known,
    available,
    helpOnly: known,
    name,
    role: 'Personal Assistant',
    paused: state === 'paused',
    repair: state === 'repair_needed',
    needsHire: state === 'needs_hire' || state === 'hiring',
    placeholder: available
      ? `Ask ${name} a question or describe what you want help with`
      : 'Finish personal assistant setup before sending work'
  };
}

export function boundedAssistantHandoff(value) {
  return Array.from(String(value || '').trim())
    .slice(0, HANDOFF_LIMIT)
    .join('');
}

export function canSubmitAssistantWork({ available, pending, text }) {
  return available === true && pending !== true && String(text || '').trim().length > 0;
}

const state = {
  view: personalAssistantPanelView(null),
  personalAssistant: null,
  pending: false,
  open: false,
  draft: '',
  lastTrigger: null,
  els: null
};

function setStatus(message) {
  if (state.els?.status) state.els.status.textContent = String(message || '');
}

function assistantAvatarMarkup(name, appearance) {
  if (window.AgentAvatar?.markup) {
    return window.AgentAvatar.markup({ name, appearance: appearance || {} }, { size: 40 });
  }
  return '<span class="agent-avatar agent-avatar--generated">P</span>';
}

function renderIdentity() {
  const { els, view, personalAssistant } = state;
  if (!els) return;
  els.launcher.hidden = !view.available;
  els.launcherName.textContent = view.name;
  els.title.textContent = view.name;
  els.input.placeholder = view.placeholder;
  els.input.disabled = !view.available;
  els.send.disabled = !view.available || state.pending;
  const avatar = assistantAvatarMarkup(view.name, personalAssistant?.appearance);
  els.launcherAvatar.innerHTML = avatar;
  els.panelAvatar.innerHTML = avatar;
  els.panel.dataset.relationshipState = view.state;
  if (view.paused) {
    setStatus('Paused proactively. Direct questions still use the same confirmation gates.');
  }
}

function moveSharedWorkActivity() {
  const activity = document.getElementById('homeAssistantThinkingModal');
  if (!activity || !state.els?.activityMount || !state.view.available) return;
  if (activity.parentElement !== state.els.activityMount) {
    state.els.activityMount.appendChild(activity);
  }
  activity.hidden = false;
  activity.dataset.homeAssistantPanelScope = 'personal-assistant';
}

function applyPersonalAssistant(personalAssistant) {
  state.personalAssistant = personalAssistant || null;
  state.view = personalAssistantPanelView(personalAssistant);
  if (state.view.helpOnly && window.OriGuide?.setHelpOnly) {
    window.OriGuide.setHelpOnly({
      available: state.view.available,
      assistantName: state.view.name
    });
  }
  renderIdentity();
  moveSharedWorkActivity();
  if (state.view.available && window.OriAskRouting?.setPersonalAssistantIdentity) {
    window.OriAskRouting.setPersonalAssistantIdentity(state.view.name);
  }
  try {
    document.dispatchEvent(
      new CustomEvent('personal-assistant:status', {
        detail: { personalAssistant: state.personalAssistant, view: state.view }
      })
    );
  } catch (_) {
    // Status events are a local coordination seam, never a requirement to render.
  }
  return state.view;
}

async function refresh() {
  try {
    const response = await fetch(STATUS_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`status ${response.status}`);
    const payload = await response.json();
    return applyPersonalAssistant(payload?.personal_assistant || null);
  } catch (_) {
    // Keep the existing Help surface intact until relationship status is known.
    setStatus('Personal assistant status is unavailable. Reload to try again.');
    return state.view;
  }
}

function close() {
  if (!state.open || !state.els) return;
  state.open = false;
  state.draft = state.els.input.value;
  state.els.panel.hidden = true;
  state.els.launcher.setAttribute('aria-expanded', 'false');
  const trigger = state.lastTrigger;
  state.lastTrigger = null;
  if (trigger && document.contains(trigger) && trigger.focus) trigger.focus();
}

function open(trigger) {
  if (!state.view.available || !state.els) return false;
  if (window.OriGuide?.close) window.OriGuide.close();
  moveSharedWorkActivity();
  state.open = true;
  state.lastTrigger = trigger || document.activeElement;
  state.els.panel.hidden = false;
  state.els.launcher.setAttribute('aria-expanded', 'true');
  state.els.input.value = state.draft;
  state.els.input.focus();
  // Rename/pause/repair changes are server-owned. Refresh on every open rather
  // than trusting the hire-time name or local storage.
  void refresh();
  return true;
}

function prefill(text) {
  if (!open(state.els?.launcher)) return false;
  const bounded = boundedAssistantHandoff(text);
  state.draft = bounded;
  state.els.input.value = bounded;
  state.els.input.dispatchEvent(new Event('input', { bubbles: true }));
  state.els.input.focus();
  setStatus(`Review this request, then press Send to confirm it goes to ${state.view.name}.`);
  return true;
}

function routeContext() {
  if (window.OriGuide?._collectContext) {
    const context = window.OriGuide._collectContext();
    return { ...context, origin: 'personal_assistant_panel' };
  }
  return {
    surface: window.location.pathname === '/' ? 'home' : 'app',
    page_path: window.location.pathname,
    origin: 'personal_assistant_panel'
  };
}

function submit(event) {
  event?.preventDefault();
  moveSharedWorkActivity();
  const text = String(state.els?.input?.value || '').trim();
  if (!canSubmitAssistantWork({ available: state.view.available, pending: state.pending, text })) {
    return false;
  }
  if (!window.OriAskRouting || typeof window.OriAskRouting.submit !== 'function') {
    setStatus('The work controller is unavailable on this page. Nothing was submitted.');
    return false;
  }
  if (window.OriAskRouting.setPersonalAssistantIdentity) {
    window.OriAskRouting.setPersonalAssistantIdentity(state.view.name);
  }
  state.pending = true;
  state.els.send.disabled = true;
  setStatus(`Sent to ${state.view.name}. Anything consequential still requires confirmation.`);
  const operation = Promise.resolve(
    window.OriAskRouting.submit(text, {
      routeContext: routeContext(),
      openThinkingModal: false
    })
  );
  state.draft = '';
  state.els.input.value = '';
  // Planning can intentionally remain pending while the user reviews a choice;
  // the existing work controller owns that lifecycle. Release only this
  // composer's duplicate-submit guard after delegation has been accepted.
  state.pending = false;
  state.els.send.disabled = false;
  operation.catch(() =>
    setStatus('The request could not be routed. Nothing ran without confirmation.')
  );
  return true;
}

function init() {
  const panel = document.getElementById('personalAssistantPanel');
  const launcher = document.getElementById('personalAssistantLauncher');
  if (!panel || !launcher) return;
  state.els = {
    panel,
    launcher,
    launcherName: document.getElementById('personalAssistantLauncherName'),
    launcherAvatar: document.getElementById('personalAssistantLauncherAvatar'),
    panelAvatar: document.getElementById('personalAssistantPanelAvatar'),
    title: document.getElementById('personalAssistantPanelTitle'),
    close: document.getElementById('personalAssistantClose'),
    form: document.getElementById('personalAssistantForm'),
    input: document.getElementById('personalAssistantInput'),
    send: document.getElementById('personalAssistantSend'),
    status: document.getElementById('personalAssistantPanelStatus'),
    activityMount: document.getElementById('personalAssistantActivityMount')
  };
  launcher.addEventListener('click', () => (state.open ? close() : open(launcher)));
  state.els.close?.addEventListener('click', close);
  state.els.form?.addEventListener('submit', submit);
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && state.open) close();
  });
  window.addEventListener('personal-assistant:status', event => {
    if (event.detail?.personalAssistant) applyPersonalAssistant(event.detail.personalAssistant);
  });
  void refresh();
}

const api = { init, open, close, prefill, refresh, applyPersonalAssistant, _state: state };
if (typeof window !== 'undefined') window.PersonalAssistantPanel = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
