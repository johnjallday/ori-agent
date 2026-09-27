// Build with your assistant: the conversation pane beside the Create Workspace
// wizard (#addFolderModal).
//
// The pane only presents the build session and reports what the user did. It
// never talks to the server and never touches the wizard: sessions.js owns the
// session requests and applies each draft to the form through the wizard's
// own setters. Everything the user does here is emitted as a DOM event on the
// modal, so the two stay one controller with one source of truth.
//
// Its ids are build-scoped (workspaceBuild*). The global assistant panel is
// mounted on every page, and a second copy of its ids broke the page once
// (#350), so nothing here reuses a personalAssistant* id.
(function () {
  'use strict';

  // Every fixed sentence the pane shows, in one place. The assistant's own
  // turns come from the session; these are the lines that must read the same
  // every time, including when no model is reachable.
  const COPY = {
    opening:
      'What should this workspace do? Tell me roughly — I’ll set it up and you check it at the end.',
    modelFailure: 'I couldn’t reach my model just now. Keep going on the form, or try again.',
    gateFailurePrefix: 'Not yet — ',
    unavailable: 'I can’t help right now — the form still works.',
    role: 'Personal Assistant',
    manual: 'I’ll do it myself',
    startOver: 'Start over',
    tryAgain: 'Try again',
    send: 'Send',
    busy: 'Setting up…',
    placeholder: 'Type a reply…',
    showEarlier: 'Show earlier',
    resume: 'Resume',
    chooseFolder: 'Choose a folder',
    noFolder: 'No folder',
    folderChosen: 'I chose a folder.',
    folderNotChosen: 'No folder was linked. Try again, or go on without one.',
    folderLater:
      'This blueprint can’t link a folder you already have — after you create it, use “Explore a folder” on Home.',
    fallbackName: 'Your assistant',
    chosenBy: name => `Chosen by ${name}`,
    buildWith: name => `Build with ${name}`,
    replyLabel: name => `Reply to ${name}`,
    resumeQuestion: name => `Resume building ${name || 'your workspace'}?`
  };

  // The transcript shows the most recent entries; older ones stay one click
  // away so a long build never pushes the composer off screen.
  const VISIBLE_ENTRIES = 12;
  const MAX_MESSAGE_LENGTH = 2000;
  const EVENT_HOST_ID = 'addFolderModal';

  function text(value) {
    return String(value ?? '').trim();
  }

  function assistantName(assistant) {
    return text(assistant?.display_name || assistant?.name) || COPY.fallbackName;
  }

  function normalizeChoices(choices) {
    return (Array.isArray(choices) ? choices : [])
      .map(choice => ({ id: text(choice?.id), label: text(choice?.label) }))
      .filter(choice => choice.id && choice.label)
      .slice(0, 4);
  }

  function normalizeEntry(entry) {
    const role = ['assistant', 'user', 'form'].includes(entry?.role) ? entry.role : '';
    const body = text(entry?.text);
    if (!role || !body) return null;
    return {
      role,
      text: body,
      choices: role === 'assistant' ? normalizeChoices(entry.choices) : [],
      chosen: text(entry?.chosen),
      local: Boolean(entry?.local),
      action: Boolean(entry?.action),
      lineId: text(entry?.id)
    };
  }

  // paneView is the pure description of what the pane shows for a session. It
  // owns the ordering of fixed lines, the windowing, and which question — if
  // any — can still be answered, so those rules are testable without a DOM.
  //
  // localLines are pane-only lines (the opening line, a gate refusal, the
  // resume question) that are not part of the stored transcript. Each is
  // anchored after the stored entry count it was shown at, so a later session
  // update keeps it in place instead of dropping or repeating it.
  function paneView({
    session = null,
    localLines = [],
    expanded = false,
    busy = false,
    pendingText = ''
  } = {}) {
    const stored = (Array.isArray(session?.transcript) ? session.transcript : [])
      .map(normalizeEntry)
      .filter(Boolean);
    const entries = [];
    const locals = (Array.isArray(localLines) ? localLines : [])
      .map(line => ({ after: Math.max(0, Number(line?.after) || 0), entry: normalizeEntry(line) }))
      .filter(line => line.entry);
    const placeLocals = count => {
      for (const line of locals) {
        if (line.after === count) entries.push({ ...line.entry, local: true });
      }
    };
    placeLocals(0);
    stored.forEach((entry, index) => {
      entries.push(entry);
      placeLocals(index + 1);
    });
    // A line anchored past the stored transcript (the session was replaced by
    // a shorter one) still shows, at the end.
    for (const line of locals) {
      if (line.after > stored.length) entries.push({ ...line.entry, local: true });
    }
    // What the user just sent shows at once, before the session returns it.
    const pending = text(pendingText);
    if (busy && pending) entries.push({ role: 'user', text: pending, choices: [], local: true });

    // Only the newest assistant line can still be answered, and only while
    // nothing has been chosen for it and no turn is in flight.
    let activeIndex = -1;
    for (let index = entries.length - 1; index >= 0; index -= 1) {
      const entry = entries[index];
      if (entry.role === 'user') break;
      if (entry.role !== 'assistant') continue;
      if (entry.choices.length && !entry.chosen) activeIndex = index;
      break;
    }
    let latestAssistant = -1;
    for (let index = entries.length - 1; index >= 0; index -= 1) {
      if (entries[index].role === 'assistant') {
        latestAssistant = index;
        break;
      }
    }

    const hiddenCount = expanded ? 0 : Math.max(0, entries.length - VISIBLE_ENTRIES);
    return {
      entries: entries.map((entry, index) => ({
        ...entry,
        index,
        latest: index === latestAssistant,
        choicesActive: index === activeIndex && !busy
      })),
      firstVisible: hiddenCount,
      hiddenCount,
      composerDisabled: Boolean(busy),
      status: busy ? COPY.busy : latestAssistant >= 0 ? entries[latestAssistant].text : '',
      busy: Boolean(busy)
    };
  }

  function createElement(tag, className, content) {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (content !== undefined) element.textContent = content;
    return element;
  }

  function avatarMarkup(assistant) {
    const name = assistantName(assistant);
    if (window.AgentAvatar?.markup) {
      return window.AgentAvatar.markup(
        { name, appearance: assistant?.appearance || {} },
        { size: 32 }
      );
    }
    return '';
  }

  const state = {
    root: null,
    host: null,
    buildWithButton: null,
    assistant: null,
    session: null,
    localLines: [],
    lineSequence: 0,
    pendingText: '',
    expanded: false,
    busy: false,
    collapsed: false,
    listeners: [],
    els: {}
  };

  function emit(type, detail = {}) {
    const host = state.host || document.getElementById(EVENT_HOST_ID);
    if (!host || typeof host.dispatchEvent !== 'function') return;
    host.dispatchEvent(new CustomEvent(type, { bubbles: false, detail }));
  }

  function listen(element, type, handler) {
    if (!element?.addEventListener) return;
    element.addEventListener(type, handler);
    state.listeners.push(() => element.removeEventListener?.(type, handler));
  }

  function storedCount() {
    return Array.isArray(state.session?.transcript) ? state.session.transcript.length : 0;
  }

  function renderHeader() {
    const name = assistantName(state.assistant);
    const head = createElement('header', 'workspace-build-pane__head');
    const avatar = createElement('span', 'workspace-build-pane__avatar');
    avatar.setAttribute('aria-hidden', 'true');
    avatar.innerHTML = avatarMarkup(state.assistant);
    const who = createElement('div', 'workspace-build-pane__who');
    const nameEl = createElement('strong', 'workspace-build-pane__name', name);
    nameEl.id = 'workspaceBuildPaneName';
    who.appendChild(nameEl);
    who.appendChild(createElement('span', 'workspace-build-pane__role', COPY.role));
    const actions = createElement('div', 'workspace-build-pane__head-actions');
    const startOver = createElement('button', 'workspace-build-pane__link', COPY.startOver);
    startOver.type = 'button';
    startOver.dataset.buildControl = 'start-over';
    const manual = createElement('button', 'workspace-build-pane__link', COPY.manual);
    manual.type = 'button';
    manual.dataset.buildControl = 'collapse';
    actions.appendChild(startOver);
    actions.appendChild(manual);
    head.appendChild(avatar);
    head.appendChild(who);
    head.appendChild(actions);
    return head;
  }

  function renderEntry(entry, index) {
    const name = assistantName(state.assistant);
    const item = createElement('li', `workspace-build-entry workspace-build-entry--${entry.role}`);
    item.dataset.buildEntry = String(index);
    if (entry.latest) item.classList.add('is-latest');
    if (entry.role === 'form') {
      item.appendChild(createElement('span', 'workspace-build-entry__note', entry.text));
      return item;
    }
    const bubble = createElement('p', 'workspace-build-entry__bubble', entry.text);
    bubble.id = `workspaceBuildEntry${index}`;
    if (entry.role === 'assistant') {
      const speaker = createElement('span', 'visually-hidden', `${name}: `);
      bubble.prepend(speaker);
    }
    item.appendChild(bubble);
    if (entry.role === 'assistant' && entry.choices.length) {
      const group = createElement('div', 'workspace-build-chips');
      group.setAttribute('role', 'group');
      group.setAttribute('aria-labelledby', bubble.id);
      for (const choice of entry.choices) {
        const chip = createElement('button', 'workspace-build-chip', choice.label);
        chip.type = 'button';
        chip.dataset.buildChoice = choice.id;
        if (entry.action) chip.dataset.buildLocalAction = '1';
        const chosen = entry.chosen === choice.id;
        chip.setAttribute('aria-pressed', chosen ? 'true' : 'false');
        if (chosen) chip.classList.add('is-chosen');
        chip.disabled = !entry.choicesActive;
        group.appendChild(chip);
      }
      item.appendChild(group);
    }
    return item;
  }

  function render() {
    const { els } = state;
    if (!state.root || !els.log) return;
    const view = paneView({
      session: state.session,
      localLines: state.localLines,
      expanded: state.expanded,
      busy: state.busy,
      pendingText: state.pendingText
    });
    els.log.textContent = '';
    view.entries.forEach((entry, index) => {
      if (index < view.firstVisible) return;
      els.log.appendChild(renderEntry(entry, index));
    });
    els.showEarlier.hidden = view.hiddenCount === 0;
    els.status.textContent = view.status;
    els.status.classList.toggle('is-busy', view.busy);
    els.input.disabled = view.composerDisabled;
    els.send.disabled = view.composerDisabled;
    state.root.classList.toggle('is-busy', view.busy);
    if (els.transcript && typeof els.transcript.scrollHeight === 'number') {
      els.transcript.scrollTop = els.transcript.scrollHeight;
    }
  }

  function submitComposer() {
    if (state.busy) return;
    const value = String(state.els.input?.value || '')
      .slice(0, MAX_MESSAGE_LENGTH)
      .trim();
    if (!value) return;
    state.els.input.value = '';
    emit('workspace-build-turn', { text: value });
  }

  function chooseChip(button) {
    if (state.busy || button.disabled) return;
    const id = text(button.dataset.buildChoice);
    if (!id) return;
    // A chip answers once. Lock the whole group immediately so a double
    // click cannot send the same answer twice before the session returns.
    button
      .closest?.('.workspace-build-chips')
      ?.querySelectorAll?.('button')
      ?.forEach(chip => {
        chip.disabled = true;
      });
    button.setAttribute('aria-pressed', 'true');
    button.classList.add('is-chosen');
    if (button.dataset.buildLocalAction) {
      state.localLines = state.localLines.map(line =>
        (line.choices || []).some(choice => choice.id === id) ? { ...line, chosen: id } : line
      );
      emit('workspace-build-action', { action: id });
      return;
    }
    emit('workspace-build-turn', { choiceId: id, label: text(button.textContent) });
  }

  function syncBuildWithButton() {
    const button = state.buildWithButton;
    if (!button) return;
    button.textContent = COPY.buildWith(assistantName(state.assistant));
    button.hidden = !(state.root && state.collapsed && !state.withdrawn);
  }

  // mount renders the pane for one modal open. It is safe to call again: the
  // previous mount is destroyed first, so a reopened modal never carries the
  // last build's listeners.
  function mount(options = {}) {
    destroy();
    const root =
      options.root ||
      (typeof options.rootId === 'string' ? document.getElementById(options.rootId) : null) ||
      document.getElementById('workspaceBuildPane');
    if (!root) return false;
    state.root = root;
    state.host = options.host || document.getElementById(EVENT_HOST_ID);
    state.buildWithButton =
      options.buildWithButton || document.getElementById('workspaceBuildWithBtn');
    state.assistant = options.assistant || null;
    state.session = null;
    state.localLines = [];
    state.pendingText = '';
    state.expanded = false;
    state.busy = false;
    state.collapsed = false;
    state.withdrawn = false;

    root.textContent = '';
    root.appendChild(renderHeader());
    const transcript = createElement('div', 'workspace-build-pane__transcript');
    const showEarlier = createElement('button', 'workspace-build-pane__earlier', COPY.showEarlier);
    showEarlier.type = 'button';
    showEarlier.dataset.buildControl = 'show-earlier';
    showEarlier.hidden = true;
    const log = createElement('ol', 'workspace-build-pane__log');
    log.setAttribute('aria-label', `Conversation with ${assistantName(state.assistant)}`);
    transcript.appendChild(showEarlier);
    transcript.appendChild(log);
    const status = createElement('p', 'workspace-build-pane__status');
    status.id = 'workspaceBuildStatus';
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    const composer = createElement('form', 'workspace-build-pane__composer');
    const label = createElement(
      'label',
      'visually-hidden',
      COPY.replyLabel(assistantName(state.assistant))
    );
    label.setAttribute('for', 'workspaceBuildComposerInput');
    const input = createElement('textarea', 'modern-input workspace-build-pane__input');
    input.id = 'workspaceBuildComposerInput';
    input.rows = 2;
    input.maxLength = MAX_MESSAGE_LENGTH;
    input.setAttribute('maxlength', String(MAX_MESSAGE_LENGTH));
    input.placeholder = COPY.placeholder;
    const send = createElement(
      'button',
      'modern-btn modern-btn-primary workspace-build-pane__send',
      COPY.send
    );
    send.type = 'submit';
    composer.appendChild(label);
    composer.appendChild(input);
    composer.appendChild(send);
    root.appendChild(transcript);
    root.appendChild(status);
    root.appendChild(composer);
    state.els = { transcript, showEarlier, log, status, composer, input, send };

    listen(root, 'click', event => {
      const control = event.target?.closest?.('[data-build-control]');
      if (control) {
        const kind = control.dataset.buildControl;
        if (kind === 'collapse') emit('workspace-build-collapse');
        else if (kind === 'start-over') emit('workspace-build-start-over');
        else if (kind === 'show-earlier') {
          state.expanded = true;
          render();
        }
        return;
      }
      const chip = event.target?.closest?.('[data-build-choice]');
      if (chip) chooseChip(chip);
    });
    listen(composer, 'submit', event => {
      event.preventDefault?.();
      submitComposer();
    });
    listen(input, 'keydown', event => {
      if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
      event.preventDefault?.();
      submitComposer();
    });
    listen(state.buildWithButton, 'click', () => emit('workspace-build-expand'));

    if (options.opening !== false) showLine(COPY.opening);
    root.hidden = false;
    syncBuildWithButton();
    render();
    return true;
  }

  // applySession replaces what the pane shows with the server's session. The
  // stored transcript is the authority; pane-only lines keep their place.
  function applySession(session) {
    state.session = session && typeof session === 'object' ? session : null;
    if (state.session?.assistant) state.assistant = state.session.assistant;
    render();
  }

  function setBusy(busy, options = {}) {
    state.busy = Boolean(busy);
    state.pendingText = state.busy ? text(options.pendingText) : '';
    render();
    // Focus returns to the composer after a turn, never to a step heading the
    // wizard just advanced to (FR a11y: advancing never steals focus).
    if (!state.busy && state.root && !state.collapsed) {
      const active = document.activeElement;
      if (!active || active === document.body || state.root.contains?.(active)) {
        state.els.input?.focus?.();
      }
    }
  }

  // showLine adds a pane-only assistant line, optionally with chips that
  // trigger a local action (Resume, Start over, Try again) instead of a turn.
  // It returns the line's id, for removeLine.
  function showLine(message, options = {}) {
    const body = text(message);
    if (!body) return '';
    const chips = normalizeChoices(options.chips);
    state.lineSequence += 1;
    const id = `line-${state.lineSequence}`;
    state.localLines.push({
      id,
      role: 'assistant',
      text: body,
      choices: chips,
      action: chips.length > 0,
      after: storedCount()
    });
    render();
    return id;
  }

  function removeLine(id) {
    const wanted = text(id);
    if (!wanted) return;
    state.localLines = state.localLines.filter(line => line.id !== wanted);
    render();
  }

  // collapse hides the pane. A withdrawn pane (the assistant cannot help, or
  // the user chose Group) also hides the button that would reopen it.
  function collapse(options = {}) {
    if (!state.root) return;
    state.collapsed = true;
    state.withdrawn = Boolean(options.withdraw);
    state.root.hidden = true;
    syncBuildWithButton();
  }

  function expand() {
    if (!state.root) return;
    state.collapsed = false;
    state.withdrawn = false;
    state.root.hidden = false;
    syncBuildWithButton();
    render();
  }

  function destroy() {
    for (const release of state.listeners.splice(0)) release();
    if (state.root) {
      state.root.textContent = '';
      state.root.hidden = true;
    }
    if (state.buildWithButton) state.buildWithButton.hidden = true;
    state.root = null;
    state.host = null;
    state.buildWithButton = null;
    state.session = null;
    state.localLines = [];
    state.pendingText = '';
    state.els = {};
    state.busy = false;
    state.collapsed = false;
    state.withdrawn = false;
  }

  window.WorkspaceBuildPane = {
    COPY,
    VISIBLE_ENTRIES,
    mount,
    applySession,
    setBusy,
    showLine,
    removeLine,
    collapse,
    expand,
    destroy,
    isMounted: () => Boolean(state.root),
    isCollapsed: () => Boolean(state.root && state.collapsed),
    assistantName: () => assistantName(state.assistant),
    paneView
  };
})();
