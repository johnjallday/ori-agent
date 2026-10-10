// One viewport owner for the personal drawer, not a conversation/store owner.
// All movements are container-local. A render batch never changes keyboard focus.
export function replyViewportTop({ top, height, viewportHeight, bottom, inset = 8 }) {
  return height > viewportHeight - inset * 2 ? Math.max(0, top - inset) : bottom;
}

export function survivingAnchor(anchors, contains) {
  return anchors?.find(anchor => contains(anchor.row)) || null;
}

export function createTranscript({ pane, transcript, jump, announce, frame, cancel, isOpen }) {
  let following = true;
  let hydrating = false;
  let anchors = [];
  let savedTop = pane.scrollTop;
  let expectedTop = null;
  let target = null;
  let unread = null;
  let dirty = false;
  let callback = null;
  let generation = 0;
  let pending = null;
  let userIntent = false;
  const scale = () => pane.getBoundingClientRect().height / pane.offsetHeight || 1;
  const offset = row =>
    (row.getBoundingClientRect().top - pane.getBoundingClientRect().top) / scale();
  const connected = row => transcript.contains(row);
  const bottom = () => Math.max(0, pane.scrollHeight - pane.clientHeight);
  const remember = () => {
    savedTop = pane.scrollTop;
    const rows = Array.from(transcript.children);
    const first = rows.findIndex(
      row => row.getBoundingClientRect().bottom > pane.getBoundingClientRect().top
    );
    anchors = rows.slice(Math.max(0, first)).map(row => ({ row, offset: offset(row) }));
    const active = transcript.ownerDocument?.activeElement;
    if (
      pane.contains(active) &&
      active?.closest?.(
        '#personalAssistantFolderSetupChoices, #personalAssistantFolderOffer, .personal-assistant-message__setup'
      )
    )
      anchors.unshift({ row: active, offset: offset(active) });
  };
  const move = top => {
    pane.scrollTop = Math.max(0, Math.min(bottom(), top));
    expectedTop = pane.scrollTop;
  };
  const revealed = row => {
    if (!connected(row)) return false;
    const top = offset(row);
    const height = row.getBoundingClientRect().height / scale();
    return (
      top >= -1 &&
      top < pane.clientHeight &&
      (height > pane.clientHeight - 16 || top + height <= pane.clientHeight + 1)
    );
  };
  const updateUnread = () => {
    if (unread && (!connected(unread) || revealed(unread))) unread = null;
    if (!unread && announce.textContent === 'New reply available.') announce.textContent = '';
    // An automatically acknowledged target must not hide a focused control.
    // Blur (or explicit activation) retires the control without stealing focus.
    jump.hidden = !unread && jump.ownerDocument?.activeElement !== jump;
  };
  const settle = () => {
    callback = null;
    if (!isOpen() || hydrating) return; // preserve intent until open/batch completion
    if (target && connected(target) && following) {
      const height = target.getBoundingClientRect().height / scale();
      move(
        replyViewportTop({
          top: pane.scrollTop + offset(target),
          height,
          viewportHeight: pane.clientHeight,
          bottom: bottom()
        })
      );
      if (height > pane.clientHeight - 16) following = false;
      target = null;
    } else if (following) {
      move(bottom());
    } else {
      const anchor = survivingAnchor(anchors, row => pane.contains(row));
      move(anchor ? pane.scrollTop + offset(anchor.row) - anchor.offset : savedTop);
    }
    dirty = false;
    userIntent = false;
    remember();
    updateUnread();
  };
  const changed = () => {
    dirty = true;
    if (callback !== null) cancel(callback);
    callback = null;
    if (!isOpen() || hydrating) return;
    const current = generation;
    callback = frame(() => {
      callback = frame(() => {
        if (current === generation) settle();
      });
    });
  };
  const beforeChange = () => {
    if (!dirty && isOpen()) remember();
    dirty = true;
  };
  const onScroll = () => {
    if (!isOpen() || hydrating || (dirty && !userIntent)) return;
    userIntent = false;
    if (expectedTop !== null && Math.abs(pane.scrollTop - expectedTop) < 2) return;
    expectedTop = null;
    following = bottom() - pane.scrollTop < 48;
    target = null; // intentional reading overrides a queued automatic reveal
    remember();
    updateUnread();
  };
  const intent = () => {
    expectedTop = null;
    userIntent = true;
  };
  const focusReading = event => {
    const control = event.target;
    if (
      !control?.closest?.(
        '#personalAssistantFolderSetupChoices, #personalAssistantFolderOffer, .personal-assistant-message__setup'
      )
    )
      return;
    // Returning to a native setup control while waiting is reader intent too.
    following = false;
    target = null;
    remember();
  };
  const reply = row => {
    if (!row || hydrating || !connected(row)) return;
    target = row;
    unread = row;
    announce.textContent = !following && !revealed(row) ? 'New reply available.' : 'Reply ready.';
    updateUnread();
    changed();
  };
  const showLatest = () => {
    const row =
      unread ||
      Array.from(transcript.children).findLast(row => row.dataset.messageRole === 'assistant');
    if (!row) return;
    const hadFocus = jump.ownerDocument?.activeElement === jump;
    following = true;
    target = row;
    settle(); // no document scrollIntoView, motion or focus theft
    // An explicit jump has a predictable keyboard continuation. Do not hide
    // its focused button while leaving focus on a disconnected/hidden control.
    if (hadFocus) {
      row.tabIndex = -1;
      row.focus({ preventScroll: true });
    }
    updateUnread();
  };
  const reset = () => {
    generation++;
    if (callback !== null) cancel(callback);
    callback = null;
    following = true;
    hydrating = false;
    expectedTop = null;
    userIntent = false;
    anchors = [];
    target = unread = null;
    pending?.remove();
    pending = null;
    dirty = false;
    jump.hidden = true;
    announce.textContent = '';
  };
  const pendingReply = busy => {
    beforeChange();
    if (!busy) {
      pending?.remove();
      pending = null;
    } else if (!pending) {
      pending = transcript.ownerDocument.createElement('div');
      pending.className = 'personal-assistant-transcript__pending';
      pending.dataset.pendingReply = '';
      pending.setAttribute('role', 'status');
      pending.textContent = 'Writing a reply…';
      transcript.append(pending);
    }
    changed();
  };
  const prepareAppend = () => {
    beforeChange();
    pending?.remove();
    pending = null;
  };
  const revealControl = (control, atStart = false) => {
    if (!isOpen() || !pane.contains(control)) return;
    const bounds = control.getBoundingClientRect();
    const top = offset(control);
    const height = bounds.height / scale();
    if (atStart) move(pane.scrollTop + top - 8);
    else if (top < 0) move(pane.scrollTop + top - 1);
    else if (top + height > pane.clientHeight)
      move(pane.scrollTop + top + height - pane.clientHeight + 1);
    following = false;
    remember();
    updateUnread();
  };
  pane.addEventListener('scroll', onScroll);
  pane.addEventListener('focusin', focusReading);
  pane.addEventListener('wheel', intent, { passive: true });
  pane.addEventListener('touchstart', intent, { passive: true });
  pane.addEventListener('keydown', intent);
  pane.addEventListener('pointerdown', intent);
  jump.addEventListener('click', showLatest);
  jump.addEventListener('blur', updateUnread);
  remember();
  return {
    beforeChange,
    changed,
    prepareAppend,
    pendingReply,
    reply,
    reset,
    revealControl,
    send() {
      beforeChange();
      following = true;
      announce.textContent = '';
      target = unread = null;
      updateUnread();
      changed();
    },
    beginHydration() {
      hydrating = true;
    },
    endHydration() {
      hydrating = false;
      following = true;
      target =
        Array.from(transcript.children).findLast(row => row.dataset.messageRole === 'assistant') ||
        null;
      changed();
    },
    isSettled: () => callback === null && !dirty,
    opened: changed,
    closed() {
      if (callback !== null) cancel(callback);
      callback = null;
    },
    dispose() {
      reset();
      pane.removeEventListener('scroll', onScroll);
      pane.removeEventListener('focusin', focusReading);
      pane.removeEventListener('wheel', intent);
      pane.removeEventListener('touchstart', intent);
      pane.removeEventListener('keydown', intent);
      pane.removeEventListener('pointerdown', intent);
      jump.removeEventListener('click', showLatest);
      jump.removeEventListener('blur', updateUnread);
    }
  };
}

function init() {
  const pane = document.getElementById('personalAssistantScroll');
  const transcript = document.getElementById('homeAssistantConversation');
  const jump = document.getElementById('personalAssistantNewReply');
  if (!pane || !transcript || !jump) return;
  const owner = createTranscript({
    pane,
    transcript,
    jump,
    announce: document.getElementById('personalAssistantReplyAnnouncement'),
    frame: callback => requestAnimationFrame(callback),
    cancel: id => cancelAnimationFrame(id),
    isOpen: () => document.getElementById('personalAssistantPanel')?.hidden === false
  });
  window.PersonalAssistantTranscript = owner;
  // Cached pre-mutation anchors handle independent Today/card polling and
  // late reflow. These observers never manufacture unread replies.
  const mutations = new MutationObserver(owner.changed);
  mutations.observe(pane, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    attributeFilter: ['hidden', 'open', 'class']
  });
  const resize = new ResizeObserver(owner.changed);
  resize.observe(pane);
  resize.observe(transcript);
  const suspend = owner.closed;
  owner.closed = () => {
    mutations.disconnect();
    resize.disconnect();
    suspend();
  };
  const reopen = () => {
    mutations.observe(pane, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ['hidden', 'open', 'class']
    });
    resize.observe(pane);
    resize.observe(transcript);
    owner.opened();
  };
  document.addEventListener('personal-assistant:opened', reopen);
  window.addEventListener('pageshow', event => {
    if (event.persisted) reopen();
  });
  window.addEventListener(
    'pagehide',
    event => {
      if (event.persisted) {
        owner.closed();
        return;
      }
      mutations.disconnect();
      resize.disconnect();
      owner.dispose();
    },
    false
  );
}
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
