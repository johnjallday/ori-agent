// Build with your assistant: the active conversation surface for the shared
// Create Workspace wizard (#addFolderModal).
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
    modelFailure: 'I couldn’t reach my model just now. Set up manually, or try again.',
    draftFailure:
      'Your edits are kept in this dialog, but I couldn’t save the shared draft. Try again before continuing or closing.',
    gateFailurePrefix: 'Not yet — ',
    unavailable: 'I can’t help right now — the form still works.',
    manualPreserved:
      'Your manual draft was kept. This reply did not change it; tell me what to do next.',
    role: 'Personal Assistant',
    manual: 'Set up manually',
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
    resumeQuestion: name => `Resume building ${name || 'your workspace'}?`,
    // Add folder: the composer's folder context, in the pane's own words.
    addFolder: 'Add folder',
    removeFolder: 'Remove folder',
    folderTitle: 'Add a folder',
    folderDisclosure:
      'Ori records file and folder names, kinds, counts and project markers locally. File contents are not read. Send shares this metadata with your configured model. When you create the workspace, Ori links the folder to it; nothing in it is moved or changed.',
    folderLoading: 'Loading folder choices…',
    folderPickerLabel: 'Pick another folder…',
    folderPathLabel: 'or paste a folder path',
    folderPathPlaceholder: '~/Projects/my-app',
    folderPathButton: 'Use this path',
    folderPathChecking: 'Checking…',
    folderPathEmpty: 'Type or paste a folder path first.',
    folderCancel: 'Cancel',
    folderNoChoices: 'No folder can be chosen here.',
    folderListOnly: 'Pick a folder from the list for now.',
    folderLooking: 'Looking at folder metadata locally… No file contents are read.',
    folderAttached:
      'Folder attached — I can see its file and folder names, not their contents. Tell me what this workspace should do, or just press Send.',
    folderCancelled: 'No folder was chosen. Your draft is unchanged.',
    folderSend: 'Set it up from the attached folder.',
    folderSendHint: 'Empty Send: “Set it up from the attached folder”',
    folderSummary: (files, partial) =>
      `${files} ${files === 1 ? 'file' : 'files'} observed · ${partial ? 'partial' : 'bounded'} look · contents not read`,
    // Add link: one address, read for what it says about itself.
    addLink: 'Add link',
    removeLink: 'Remove link',
    linkLabel: 'Link to attach',
    linkPlaceholder: 'https://',
    linkAttach: 'Attach',
    linkChecking: 'Checking…',
    linkCancel: 'Cancel',
    linkEmpty: 'Paste a link first.',
    linkDisclosure:
      'Ori fetches the page’s title and description once, or a GitHub repository’s public details. Nothing else is read. Send shares that with your configured model.',
    linkAttached:
      'Link attached — I can see what it says about itself, not the page. Tell me what this workspace should do, or just press Send.',
    linkSend: 'Set it up from the attached link.',
    linkSendHint: 'Empty Send: “Set it up from the attached link”',
    bothSend: 'Set it up from what I attached.',
    bothSendHint: 'Empty Send: “Set it up from what I attached”'
  };

  // linkSummaryText is the one line under an attached link: what kind of
  // thing it is and what Ori could see of it.
  function linkSummaryText(observation) {
    if (!observation || typeof observation !== 'object') return '';
    const host = text(observation.host);
    const repo = observation.repo && typeof observation.repo === 'object' ? observation.repo : null;
    if (observation.kind === 'github_repo' && repo) {
      if (repo.reachable !== 'yes') return 'GitHub repository · not verified';
      const parts = ['GitHub repository'];
      if (text(repo.language)) parts.push(text(repo.language));
      const issues = Math.max(0, Number(repo.open_issues) || 0);
      parts.push(`${issues.toLocaleString('en-US')} open ${issues === 1 ? 'issue' : 'issues'}`);
      if (repo.archived === true) parts.push('archived');
      return parts.join(' · ');
    }
    // A document is never opened, so it is known by its address either way.
    if (observation.kind === 'document') return `Document · ${host} · not read`;
    if (observation.readable !== true) return `Could only read the address · ${host}`;
    return `Page · ${host}`;
  }

  // A reason code ("desktop_off") is for code, never for the user; a note
  // that is a single identifier is treated as no note at all.
  const REASON_CODE = /^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$/;

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

  // folderChooserView is the Add folder sheet's render decision for the
  // server's choices: one button per known folder, the picker when a dialog
  // can open, and the sentence that explains a missing one. It is the
  // composer's own rule (personal-assistant-folder-chooser.js), restated here
  // because the pane is not a module.
  function folderChooserView(payload) {
    const chips = (Array.isArray(payload?.chips) ? payload.chips : [])
      .filter(chip => chip && typeof chip.id === 'string' && chip.id.trim())
      .map(chip => ({ id: chip.id.trim(), label: text(chip.label || chip.id) }));
    const pickerVisible = payload?.picker_available === true;
    let note = text(payload?.picker_note);
    if (REASON_CODE.test(note)) note = '';
    if (!note && !pickerVisible) note = chips.length ? COPY.folderListOnly : COPY.folderNoChoices;
    const choices = chips.map(chip => ({ label: chip.label, mode: 'chip', chip: chip.id }));
    if (pickerVisible) choices.push({ label: COPY.folderPickerLabel, mode: 'picker' });
    return { choices, note };
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
    // Add folder: whether the server offers it, whether a pick or removal
    // is in flight, the note under the chip, and whether the sheet is open.
    folders: false,
    folderBusy: false,
    folderNote: '',
    chooserOpen: false,
    // Add link: whether the server offers it, whether an attach or removal is
    // in flight, the note under the chip, and whether the field is open.
    links: false,
    linkBusy: false,
    linkNote: '',
    linkFieldOpen: false,
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
    renderFolder(view);
    if (els.transcript && typeof els.transcript.scrollHeight === 'number') {
      els.transcript.scrollTop = els.transcript.scrollHeight;
    }
  }

  function attachedFolder() {
    const observation = state.session?.folder?.observation;
    return observation && typeof observation === 'object' ? observation : null;
  }

  function attachedLink() {
    const observation = state.session?.link?.observation;
    return observation && typeof observation === 'object' ? observation : null;
  }

  // attachmentsLocked is true while a turn, a folder request, or a link
  // request is in flight: one request at a time moves the session's version.
  function attachmentsLocked(view) {
    return Boolean(view?.busy) || state.folderBusy || state.linkBusy;
  }

  // emptySendText is what an empty Send asks for, from what is attached.
  function emptySendText() {
    const folder = attachedFolder();
    const link = attachedLink();
    if (folder && link) return COPY.bothSend;
    if (link) return COPY.linkSend;
    if (folder) return COPY.folderSend;
    return '';
  }

  // renderLink draws the link half of the attachments: the Add link button
  // when the server offers it, the attached link's chip and summary, and the
  // empty-Send hint the link owns (alone, or with a folder).
  function renderLink(view) {
    const { els } = state;
    if (!els.addLink) return;
    const link = attachedLink();
    const locked = attachmentsLocked(view);
    els.addLink.hidden = !state.links;
    els.addLink.disabled = locked;
    els.linkChip.hidden = !link;
    const name = text(link?.title) || text(link?.repo?.full_name) || text(link?.host);
    els.linkName.textContent = name;
    els.linkIcon.className = link?.kind === 'github_repo' ? 'bi bi-github' : 'bi bi-link-45deg';
    els.linkRemove.disabled = locked;
    els.linkRemove.setAttribute(
      'aria-label',
      name ? `${COPY.removeLink} ${name}` : COPY.removeLink
    );
    const summary = state.linkNote || linkSummaryText(link);
    if (els.linkSummary.textContent !== summary) els.linkSummary.textContent = summary;
    els.linkSummary.hidden = !summary;
    const empty = !text(els.input?.value);
    els.linkHint.textContent = attachedFolder() ? COPY.bothSendHint : COPY.linkSendHint;
    els.linkHint.hidden = !link || !empty;
    els.linkInput.disabled = state.linkBusy;
    els.linkAttach.disabled = state.linkBusy;
    els.linkAttach.textContent = state.linkBusy ? COPY.linkChecking : COPY.linkAttach;
  }

  // renderFolder draws the composer's attachments from the session: the Add
  // folder button when the server offers it, the attached folder's chip and
  // summary, and the empty-Send hint while the reply box is empty.
  function renderFolder(view) {
    renderLink(view);
    const { els } = state;
    if (!els.addFolder) return;
    const folder = attachedFolder();
    const locked = attachmentsLocked(view);
    els.addFolder.hidden = !state.folders;
    els.addFolder.disabled = locked;
    els.folderChip.hidden = !folder;
    const name = text(folder?.folder) || '';
    els.folderName.textContent = name;
    els.folderRemove.disabled = locked;
    els.folderRemove.setAttribute(
      'aria-label',
      name ? `${COPY.removeFolder} ${name}` : COPY.removeFolder
    );
    const summary =
      state.folderNote ||
      (folder
        ? COPY.folderSummary(
            Math.max(0, Number(folder.files) || 0),
            folder.coverage?.partial === true
          )
        : '');
    // A live region is written only when its text changes, so a keystroke in
    // the reply box never re-announces the folder.
    if (els.folderSummary.textContent !== summary) els.folderSummary.textContent = summary;
    els.folderSummary.hidden = !summary;
    // With a link attached too, the link's hint speaks for both.
    els.folderHint.hidden = !folder || Boolean(attachedLink()) || Boolean(text(els.input?.value));
  }

  // setFolderPathBusy locks the path field while the controller has the
  // folder-picker endpoint check a typed path; the sheet stays open so a
  // refusal can be read where it was typed.
  function setFolderPathBusy(busy) {
    const { els } = state;
    if (!els.folderPath) return;
    const locked = Boolean(busy);
    els.folderPath.disabled = locked;
    els.folderPathUse.disabled = locked;
    els.folderPathUse.textContent = locked ? COPY.folderPathChecking : COPY.folderPathButton;
  }

  function submitComposer() {
    if (state.busy || state.folderBusy || state.linkBusy) return;
    const value = String(state.els.input?.value || '')
      .slice(0, MAX_MESSAGE_LENGTH)
      .trim();
    if (!value) {
      // An empty Send with something attached asks for that, in words.
      const fallback = emptySendText();
      if (!fallback) return;
      emit('workspace-build-turn', { text: fallback });
      return;
    }
    state.els.input.value = '';
    emit('workspace-build-turn', { text: value });
  }

  function submitLink() {
    if (state.busy || state.folderBusy || state.linkBusy || state.els.linkAttach?.disabled) return;
    const url = text(state.els.linkInput?.value);
    if (!url) {
      linkFieldStatus(COPY.linkEmpty, true);
      state.els.linkInput?.focus?.();
      return;
    }
    emit('workspace-build-link', { url });
  }

  function submitFolderPath() {
    if (state.busy || state.folderBusy || state.els.folderPathUse?.disabled) return;
    const path = text(state.els.folderPath?.value);
    if (!path) {
      folderChooserStatus(COPY.folderPathEmpty, true);
      state.els.folderPath?.focus?.();
      return;
    }
    emit('workspace-build-folder', { mode: 'path', path });
  }

  function chooseFolderChoice(button) {
    if (state.busy || state.folderBusy || button.disabled) return;
    const mode = text(button.dataset.buildFolderMode);
    if (!mode) return;
    const detail = { mode };
    if (mode === 'chip') detail.chip = text(button.dataset.buildFolderChip);
    emit('workspace-build-folder', detail);
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
    state.folders = options.folders === true;
    state.folderBusy = false;
    state.folderNote = '';
    state.chooserOpen = false;
    state.links = options.links === true;
    state.linkBusy = false;
    state.linkNote = '';
    state.linkFieldOpen = false;

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
    const folderEls = renderAttachments(composer);
    const row = createElement('div', 'workspace-build-pane__composer-row');
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
    row.appendChild(label);
    row.appendChild(input);
    row.appendChild(send);
    composer.appendChild(row);
    root.appendChild(transcript);
    root.appendChild(status);
    root.appendChild(composer);
    state.els = { transcript, showEarlier, log, status, composer, input, send, ...folderEls };

    listen(root, 'click', event => {
      const control = event.target?.closest?.('[data-build-control]');
      if (control) {
        const kind = control.dataset.buildControl;
        if (kind === 'collapse') emit('workspace-build-collapse');
        else if (kind === 'start-over') emit('workspace-build-start-over');
        else if (kind === 'show-earlier') {
          state.expanded = true;
          render();
        } else if (kind === 'add-folder') {
          if (!attachmentsLocked(state) && !control.disabled) {
            closeLinkField({ focus: false });
            emit('workspace-build-folder-open');
          }
        } else if (kind === 'remove-folder') {
          if (!attachmentsLocked(state) && !control.disabled) emit('workspace-build-folder-remove');
        } else if (kind === 'folder-cancel') closeFolderChooser();
        else if (kind === 'folder-path') submitFolderPath();
        else if (kind === 'add-link') {
          if (!attachmentsLocked(state) && !control.disabled) openLinkField();
        } else if (kind === 'remove-link') {
          if (!attachmentsLocked(state) && !control.disabled) emit('workspace-build-link-remove');
        } else if (kind === 'link-cancel') closeLinkField();
        else if (kind === 'link-attach') submitLink();
        return;
      }
      const folderChoice = event.target?.closest?.('[data-build-folder-mode]');
      if (folderChoice) {
        chooseFolderChoice(folderChoice);
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
    listen(input, 'input', () => renderFolder({ busy: state.busy }));
    // Enter in the path field checks the path; it must never submit the
    // reply form around it.
    listen(folderEls.folderPath, 'keydown', event => {
      if (event.key !== 'Enter' || event.isComposing) return;
      event.preventDefault?.();
      event.stopPropagation?.();
      submitFolderPath();
    });
    listen(folderEls.chooser, 'keydown', event => {
      if (event.key !== 'Escape') return;
      event.preventDefault?.();
      event.stopPropagation?.();
      closeFolderChooser();
    });
    // Enter in the link field attaches; it must never submit the reply form
    // around it. Escape closes only the field.
    listen(folderEls.linkInput, 'keydown', event => {
      if (event.isComposing) return;
      if (event.key === 'Enter') {
        event.preventDefault?.();
        event.stopPropagation?.();
        submitLink();
      } else if (event.key === 'Escape') {
        event.preventDefault?.();
        event.stopPropagation?.();
        closeLinkField();
      }
    });
    listen(folderEls.linkInput, 'input', () => linkFieldStatus(''));
    listen(state.buildWithButton, 'click', () => emit('workspace-build-expand'));

    if (options.opening !== false) showLine(COPY.opening);
    root.hidden = false;
    syncBuildWithButton();
    render();
    return true;
  }

  // renderAttachments builds the composer's attachments row, the line under
  // it, and the Add folder sheet, into composer. Returns the elements the
  // pane drives. Every id is build-scoped; the chooser's are the pane's own.
  function renderAttachments(composer) {
    const attachments = createElement('div', 'workspace-build-pane__attachments');
    const addFolder = createElement('button', 'workspace-build-pane__chip');
    addFolder.type = 'button';
    addFolder.dataset.buildControl = 'add-folder';
    addFolder.setAttribute('aria-expanded', 'false');
    addFolder.setAttribute('aria-controls', 'workspaceBuildFolderChooser');
    addFolder.hidden = true;
    const icon = createElement('i', 'bi bi-folder2-open');
    icon.setAttribute('aria-hidden', 'true');
    addFolder.appendChild(icon);
    addFolder.appendChild(createElement('span', '', COPY.addFolder));
    const folderChip = createElement('span', 'workspace-build-folder');
    folderChip.id = 'workspaceBuildFolder';
    folderChip.hidden = true;
    const folderName = createElement('span', 'workspace-build-folder__name');
    folderName.id = 'workspaceBuildFolderName';
    const folderRemove = createElement('button', 'workspace-build-folder__remove', '×');
    folderRemove.type = 'button';
    folderRemove.dataset.buildControl = 'remove-folder';
    folderRemove.setAttribute('aria-label', COPY.removeFolder);
    folderChip.appendChild(folderName);
    folderChip.appendChild(folderRemove);
    const folderHint = createElement('span', 'workspace-build-folder__hint', COPY.folderSendHint);
    folderHint.hidden = true;
    attachments.appendChild(addFolder);
    attachments.appendChild(folderChip);
    attachments.appendChild(folderHint);
    const linkEls = renderLinkAttachment(attachments);
    const folderSummary = createElement('p', 'workspace-build-folder__summary');
    folderSummary.id = 'workspaceBuildFolderSummary';
    folderSummary.setAttribute('role', 'status');
    folderSummary.setAttribute('aria-live', 'polite');
    folderSummary.hidden = true;

    const chooser = createElement('section', 'workspace-build-folder-chooser');
    chooser.id = 'workspaceBuildFolderChooser';
    chooser.hidden = true;
    const chooserTitle = createElement(
      'h3',
      'workspace-build-folder-chooser__title',
      COPY.folderTitle
    );
    chooserTitle.id = 'workspaceBuildFolderChooserTitle';
    chooser.setAttribute('aria-labelledby', chooserTitle.id);
    chooser.appendChild(chooserTitle);
    chooser.appendChild(
      createElement('p', 'workspace-build-folder-chooser__disclosure', COPY.folderDisclosure)
    );
    const choices = createElement('div', 'workspace-build-folder-chooser__choices');
    chooser.appendChild(choices);
    const chooserNote = createElement('p', 'workspace-build-folder-chooser__note');
    chooserNote.hidden = true;
    chooser.appendChild(chooserNote);
    // Any other folder: a typed path, which the controller has the
    // folder-picker endpoint check before the build ever hears of it.
    const entry = createElement('div', 'folder-path-entry');
    const entryLabel = createElement('label', 'folder-path-entry__label', COPY.folderPathLabel);
    entryLabel.setAttribute('for', 'workspaceBuildFolderPath');
    const entryRow = createElement('div', 'folder-path-entry__row');
    const folderPath = createElement('input', 'modern-input folder-path-entry__input');
    folderPath.id = 'workspaceBuildFolderPath';
    folderPath.type = 'text';
    folderPath.placeholder = COPY.folderPathPlaceholder;
    folderPath.setAttribute('autocomplete', 'off');
    folderPath.setAttribute('autocapitalize', 'off');
    folderPath.setAttribute('spellcheck', 'false');
    const folderPathUse = createElement(
      'button',
      'modern-btn modern-btn-secondary folder-path-entry__use',
      COPY.folderPathButton
    );
    folderPathUse.type = 'button';
    folderPathUse.dataset.buildControl = 'folder-path';
    entryRow.appendChild(folderPath);
    entryRow.appendChild(folderPathUse);
    const folderPathStatus = createElement('p', 'folder-path-entry__status');
    folderPathStatus.id = 'workspaceBuildFolderPathStatus';
    folderPathStatus.setAttribute('role', 'status');
    folderPathStatus.setAttribute('aria-live', 'polite');
    folderPathStatus.hidden = true;
    folderPath.setAttribute('aria-describedby', folderPathStatus.id);
    entry.appendChild(entryLabel);
    entry.appendChild(entryRow);
    entry.appendChild(folderPathStatus);
    chooser.appendChild(entry);
    const actions = createElement('div', 'workspace-build-folder-chooser__actions');
    const cancel = createElement('button', 'modern-btn modern-btn-secondary', COPY.folderCancel);
    cancel.type = 'button';
    cancel.dataset.buildControl = 'folder-cancel';
    actions.appendChild(cancel);
    chooser.appendChild(actions);

    composer.appendChild(attachments);
    composer.appendChild(folderSummary);
    composer.appendChild(linkEls.linkSummary);
    composer.appendChild(chooser);
    composer.appendChild(linkEls.linkField);
    return {
      ...linkEls,
      addFolder,
      folderChip,
      folderName,
      folderRemove,
      folderHint,
      folderSummary,
      chooser,
      chooserChoices: choices,
      chooserNote,
      chooserCancel: cancel,
      folderPath,
      folderPathUse,
      folderPathStatus
    };
  }

  // renderLinkAttachment adds the Add link button, the attached link's chip,
  // and its hint to the attachments row, and builds the summary line and the
  // inline link field the caller places. Every id is build-scoped.
  function renderLinkAttachment(attachments) {
    const addLink = createElement('button', 'workspace-build-pane__chip');
    addLink.type = 'button';
    addLink.dataset.buildControl = 'add-link';
    addLink.setAttribute('aria-expanded', 'false');
    addLink.setAttribute('aria-controls', 'workspaceBuildLinkField');
    addLink.hidden = true;
    const addIcon = createElement('i', 'bi bi-link-45deg');
    addIcon.setAttribute('aria-hidden', 'true');
    addLink.appendChild(addIcon);
    addLink.appendChild(createElement('span', '', COPY.addLink));
    const linkChip = createElement('span', 'workspace-build-folder workspace-build-link');
    linkChip.id = 'workspaceBuildLink';
    linkChip.hidden = true;
    const linkIcon = createElement('i', 'bi bi-link-45deg');
    linkIcon.setAttribute('aria-hidden', 'true');
    const linkName = createElement('span', 'workspace-build-folder__name');
    linkName.id = 'workspaceBuildLinkName';
    const linkRemove = createElement('button', 'workspace-build-folder__remove', '×');
    linkRemove.type = 'button';
    linkRemove.dataset.buildControl = 'remove-link';
    linkRemove.setAttribute('aria-label', COPY.removeLink);
    linkChip.appendChild(linkIcon);
    linkChip.appendChild(linkName);
    linkChip.appendChild(linkRemove);
    const linkHint = createElement('span', 'workspace-build-folder__hint', COPY.linkSendHint);
    linkHint.hidden = true;
    attachments.appendChild(addLink);
    attachments.appendChild(linkChip);
    attachments.appendChild(linkHint);

    const linkSummary = createElement('p', 'workspace-build-folder__summary');
    linkSummary.id = 'workspaceBuildLinkSummary';
    linkSummary.setAttribute('role', 'status');
    linkSummary.setAttribute('aria-live', 'polite');
    linkSummary.hidden = true;

    const linkField = createElement('section', 'workspace-build-link-field');
    linkField.id = 'workspaceBuildLinkField';
    linkField.hidden = true;
    const fieldLabel = createElement('label', 'folder-path-entry__label', COPY.linkLabel);
    fieldLabel.setAttribute('for', 'workspaceBuildLinkInput');
    const fieldRow = createElement('div', 'folder-path-entry__row');
    const linkInput = createElement('input', 'modern-input folder-path-entry__input');
    linkInput.id = 'workspaceBuildLinkInput';
    linkInput.type = 'url';
    linkInput.placeholder = COPY.linkPlaceholder;
    linkInput.maxLength = 2000;
    linkInput.setAttribute('inputmode', 'url');
    linkInput.setAttribute('autocomplete', 'off');
    linkInput.setAttribute('autocapitalize', 'off');
    linkInput.setAttribute('spellcheck', 'false');
    const linkAttach = createElement(
      'button',
      'modern-btn modern-btn-secondary folder-path-entry__use',
      COPY.linkAttach
    );
    linkAttach.type = 'button';
    linkAttach.dataset.buildControl = 'link-attach';
    const linkCancel = createElement('button', 'modern-btn modern-btn-secondary', COPY.linkCancel);
    linkCancel.type = 'button';
    linkCancel.dataset.buildControl = 'link-cancel';
    fieldRow.appendChild(linkInput);
    fieldRow.appendChild(linkAttach);
    fieldRow.appendChild(linkCancel);
    const linkStatus = createElement('p', 'folder-path-entry__status');
    linkStatus.id = 'workspaceBuildLinkStatus';
    linkStatus.setAttribute('role', 'status');
    linkStatus.setAttribute('aria-live', 'polite');
    linkStatus.hidden = true;
    const linkDisclosure = createElement(
      'p',
      'workspace-build-folder-chooser__disclosure',
      COPY.linkDisclosure
    );
    linkDisclosure.id = 'workspaceBuildLinkDisclosure';
    linkInput.setAttribute('aria-describedby', `${linkDisclosure.id} ${linkStatus.id}`);
    linkField.appendChild(fieldLabel);
    linkField.appendChild(fieldRow);
    linkField.appendChild(linkStatus);
    linkField.appendChild(linkDisclosure);
    return {
      addLink,
      linkChip,
      linkIcon,
      linkName,
      linkRemove,
      linkHint,
      linkSummary,
      linkField,
      linkInput,
      linkAttach,
      linkCancel,
      linkStatus
    };
  }

  // openLinkField shows the inline link field with focus in it. It closes the
  // Add folder sheet: one attachment control is open at a time.
  function openLinkField() {
    const { els } = state;
    if (!els.linkField || attachmentsLocked(state)) return false;
    closeFolderChooser({ focus: false });
    state.linkFieldOpen = true;
    els.linkField.hidden = false;
    els.addLink.setAttribute('aria-expanded', 'true');
    linkFieldStatus('');
    els.linkInput.focus?.();
    return true;
  }

  function closeLinkField({ focus = true } = {}) {
    const { els } = state;
    const wasOpen = state.linkFieldOpen;
    state.linkFieldOpen = false;
    if (!els.linkField) return;
    els.linkField.hidden = true;
    els.addLink.setAttribute('aria-expanded', 'false');
    linkFieldStatus('');
    if (focus && wasOpen) els.addLink.focus?.();
  }

  // linkFieldStatus is the link field's own line: why an address was refused,
  // or nothing. The typed address stays so it can be fixed.
  function linkFieldStatus(message, failed = false) {
    const status = state.els.linkStatus;
    if (!status) return;
    const body = text(message);
    status.textContent = body;
    status.hidden = !body;
    status.classList.toggle('is-error', Boolean(failed && body));
  }

  // setLinkBusy locks the attachments and the field while an attach or a
  // removal is in flight. The field stays open, so a refusal is read where the
  // address was typed.
  function setLinkBusy(busy, note = '') {
    state.linkBusy = Boolean(busy);
    state.linkNote = text(note);
    render();
  }

  // linkAttachedDone closes the field and clears it once the session holds
  // the link.
  function linkAttachedDone() {
    if (state.els.linkInput) state.els.linkInput.value = '';
    closeLinkField({ focus: false });
  }

  // openFolderChooser shows the Add folder sheet. Without a payload it shows
  // that choices are loading, with Cancel owning focus so Escape closes only
  // the sheet; with the server's choices it draws them and focuses the first.
  function openFolderChooser(payload) {
    const { els } = state;
    if (!els.chooser || state.busy || state.folderBusy) return false;
    state.chooserOpen = true;
    els.chooser.hidden = false;
    els.addFolder.setAttribute('aria-expanded', 'true');
    els.chooserChoices.textContent = '';
    folderChooserStatus('');
    if (payload === undefined) {
      els.chooserChoices.appendChild(createElement('p', '', COPY.folderLoading));
      els.chooserNote.hidden = true;
      els.chooserCancel.focus?.();
      return true;
    }
    const view = folderChooserView(payload);
    let first = null;
    for (const choice of view.choices) {
      const button = createElement('button', 'workspace-build-chip', choice.label);
      button.type = 'button';
      button.dataset.buildFolderMode = choice.mode;
      if (choice.chip) button.dataset.buildFolderChip = choice.chip;
      els.chooserChoices.appendChild(button);
      if (!first) first = button;
    }
    els.chooserNote.textContent = view.note;
    els.chooserNote.hidden = !view.note;
    (first || els.folderPath).focus?.();
    return true;
  }

  function closeFolderChooser({ focus = true } = {}) {
    const { els } = state;
    state.chooserOpen = false;
    if (!els.chooser) return;
    els.chooser.hidden = true;
    els.addFolder.setAttribute('aria-expanded', 'false');
    if (focus) els.addFolder.focus?.();
  }

  // folderChooserStatus is the path field's own line: what the server said
  // about a typed path, or nothing.
  function folderChooserStatus(message, failed = false) {
    const status = state.els.folderPathStatus;
    if (!status) return;
    const body = text(message);
    status.textContent = body;
    status.hidden = !body;
    status.classList.toggle('is-error', Boolean(failed && body));
  }

  // setFolderBusy locks the attachments while a pick or a removal is in
  // flight, and leaves a note under the chip (the local look, a cancelled
  // dialog) until the next change.
  function setFolderBusy(busy, note = '') {
    state.folderBusy = Boolean(busy);
    state.folderNote = text(note);
    if (state.folderBusy) closeFolderChooser({ focus: false });
    render();
  }

  // applySession replaces what the pane shows with the server's session. The
  // stored transcript is the authority; pane-only lines keep their place.
  function applySession(session) {
    const before = attachedFolder()?.id || '';
    const linkBefore = attachedLink()?.id || '';
    state.session = session && typeof session === 'object' ? session : null;
    if (state.session?.assistant) state.assistant = state.session.assistant;
    // A note is about the attachment it was written under; a new one, or
    // none, starts clean.
    if ((attachedFolder()?.id || '') !== before) state.folderNote = '';
    if ((attachedLink()?.id || '') !== linkBefore) state.linkNote = '';
    render();
  }

  function setBusy(busy, options = {}) {
    state.busy = Boolean(busy);
    state.pendingText = state.busy ? text(options.pendingText) : '';
    render();
    // Focus returns to the composer after a turn, never to a step heading the
    // wizard just advanced to (FR a11y: advancing never steals focus).
    if (!state.busy) focusComposer();
  }

  // focusComposer puts focus in the composer unless the user has moved it
  // into the form: only the page, the dialog itself, or the pane give it up.
  function focusComposer() {
    if (!state.root || state.collapsed || state.busy) return false;
    const active = document.activeElement;
    const dialog = state.root.closest?.('.modal') || null;
    if (active && active !== document.body && active !== dialog && !state.root.contains?.(active)) {
      return false;
    }
    state.els.input?.focus?.();
    return true;
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
    state.folders = false;
    state.folderBusy = false;
    state.folderNote = '';
    state.chooserOpen = false;
    state.links = false;
    state.linkBusy = false;
    state.linkNote = '';
    state.linkFieldOpen = false;
  }

  window.WorkspaceBuildPane = {
    COPY,
    VISIBLE_ENTRIES,
    mount,
    applySession,
    setBusy,
    focusComposer,
    showLine,
    removeLine,
    collapse,
    expand,
    destroy,
    openFolderChooser,
    closeFolderChooser,
    folderChooserOpen: () => Boolean(state.root && state.chooserOpen),
    folderChooserStatus,
    setFolderBusy,
    setFolderPathBusy,
    openLinkField,
    closeLinkField,
    linkFieldOpen: () => Boolean(state.root && state.linkFieldOpen),
    linkFieldStatus,
    setLinkBusy,
    linkAttachedDone,
    isMounted: () => Boolean(state.root),
    isCollapsed: () => Boolean(state.root && state.collapsed),
    assistantName: () => assistantName(state.assistant),
    paneView,
    folderChooserView,
    linkSummaryText
  };
})();
