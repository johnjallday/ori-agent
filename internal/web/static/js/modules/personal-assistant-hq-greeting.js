// The assistant's greeting on a first visit to the Personal HQ, first thing in
// an empty conversation in the drawer: one short message, host-written from
// what the page says is running and what is off (no model call), with the three
// existing "Things to try" choices under it. It is a card, never a saved
// message, and it is not the assistant speaking in the thread: it sits outside
// the conversation, so it never counts as one and never hides itself.
//
// The page owns what to say (window.workspaceDetail.hqGreetingView) and what a
// choice does (runHQGreetingChoice); this module owns whether the drawer shows
// it right now. It never opens the drawer and never moves focus when it shows.
// It fetches nothing and is inert on every page but a pending HQ.
//
// While it shows, or waits to know whether it may, the Notes question is held
// (personal-assistant-second-brain.js asks holdsNotesQuestion): the greeting
// goes first, and the question shows itself once the greeting is gone.
import { HQ_LABELS as L } from './hq-labels.js';
import { RECIPIENT_PERSONAL } from './personal-assistant-recipient.js';
import { beginHQAttempt, handoffURL } from './hq-guided-attempt.js';

const text = value => String(value ?? '').trim();
const list = value => (Array.isArray(value) ? value : []).map(text).filter(Boolean);

/**
 * Whether the drawer shows, waits for, or drops the greeting. The first rule
 * that matches wins.
 *
 *   off   the page is not a pending HQ, or the page says there is nothing to greet
 *   hide  the conversation has a message: the person has started talking
 *   wait  not known yet (the page has not read its missions and Notes, the
 *         conversation is loading, or the recipient is not yet the assistant)
 *   show  greet
 */
export function hqGreetingGate({
  active = false,
  view = null,
  personal = false,
  loading = false,
  hasMessages = false
} = {}) {
  if (!active) return 'off';
  if (view?.show === false) return 'off';
  if (hasMessages) return 'hide';
  if (view == null) return 'wait';
  if (loading) return 'wait';
  if (!personal) return 'wait';
  return 'show';
}

/** The Notes question is held while the greeting shows or may still show. */
export const greetingHolds = gate => gate === 'show' || gate === 'wait';

/** "A", "A and B", "A, B and C". */
export function joinNames(names) {
  const items = list(names);
  if (items.length <= 1) return items[0] || '';
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

function fill(many, one, names) {
  const items = list(names);
  if (!items.length) return '';
  return items.length === 1
    ? one.replace('{name}', items[0])
    : many.replace('{names}', joinNames(items));
}

/**
 * The greeting's lines. A line with nothing to say is '' (left out), and the
 * line that invites pointing at the page only follows a line that names parts.
 */
export function greetingLines(view) {
  const running = fill(L.greetingRunningMany, L.greetingRunningOne, view?.running);
  return {
    title: L.greetingTitle,
    running,
    point: running ? L.greetingPoint : '',
    off: fill(L.greetingOffMany, L.greetingOffOne, view?.off),
    ask: L.greetingAsk,
    footer: L.greetingFooter
  };
}

/** The link a choice is, or '' when it is a button the page runs. */
export function greetingChoiceHref(step) {
  if (step?.action === 'folder') return '/';
  if (step?.action === 'home') return '/';
  return '';
}

const ICONS = { folder: 'folder2', brief: 'sun', note: 'pencil' };

function element(doc, tag, className = '', content = '') {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (content) node.textContent = content;
  return node;
}

/**
 * The greeting for one drawer. Everything it reads comes in as a getter, so a
 * test drives it without a page.
 */
export function createHQGreeting({
  mount = null,
  active = false,
  view = () => null,
  personal = () => false,
  loading = () => false,
  hasMessages = () => false,
  who = () => ({}),
  avatar = () => '',
  run = () => false,
  closeDrawer = () => {},
  drawerOpen = () => false,
  focusInput = () => {},
  dispatch = () => {},
  doc = typeof document === 'undefined' ? null : document,
  queue = typeof queueMicrotask === 'function' ? queueMicrotask : fn => Promise.resolve().then(fn)
} = {}) {
  if (!active || !mount || !doc) {
    return {
      gate: () => 'off',
      holdsNotesQuestion: () => false,
      evaluate() {},
      schedule() {},
      choose: () => false
    };
  }

  let renderKey = '';
  let held = null;
  let scheduled = false;

  function gate() {
    let current = null;
    try {
      current = view() ?? null;
    } catch (_error) {
      current = null;
    }
    return hqGreetingGate({
      active: true,
      view: current,
      personal: personal() === true,
      loading: loading() === true,
      hasMessages: hasMessages() === true
    });
  }

  function clear() {
    const hadFocus = Boolean(
      doc.activeElement && typeof mount.contains === 'function' && mount.contains(doc.activeElement)
    );
    renderKey = '';
    mount.hidden = true;
    mount.textContent = '';
    // The card the person was in has gone: keep their place in the drawer.
    if (hadFocus && drawerOpen()) focusInput();
  }

  function choiceRow(step) {
    const item = element(doc, 'li', step.resolved ? 'is-resolved' : '');
    const href = greetingChoiceHref(step);
    const control = element(doc, href ? 'a' : 'button', 'pa-hq-greeting__choice');
    if (href) control.setAttribute('href', href);
    else control.setAttribute('type', 'button');
    control.setAttribute('data-hq-greeting-choice', step.action);
    const mark = element(doc, 'span', 'pa-hq-greeting__mark');
    mark.setAttribute('aria-hidden', 'true');
    if (step.state === 'completed') mark.textContent = '✓';
    else if (step.state === 'skipped') mark.textContent = '—';
    else {
      const icon = element(doc, 'i', `bi bi-${ICONS[step.key] || 'circle'}`);
      mark.appendChild(icon);
    }
    const body = element(doc, 'span', 'pa-hq-greeting__choice-text');
    const title = element(doc, 'strong', '', text(step.title));
    if (step.resolved) {
      const status = element(
        doc,
        'span',
        'visually-hidden',
        ` — ${step.state === 'skipped' ? L.firstVisitSkipped : L.firstVisitCompleted}`
      );
      title.appendChild(status);
    }
    body.appendChild(title);
    body.appendChild(element(doc, 'small', '', text(step.hint)));
    control.appendChild(mark);
    control.appendChild(body);
    item.appendChild(control);
    return item;
  }

  function render(current) {
    const lines = greetingLines(current);
    const steps = Array.isArray(current?.steps) ? current.steps : [];
    const assistant = who() || {};
    const key = JSON.stringify([
      lines,
      text(assistant.name),
      steps.map(step => [step.key, step.action, step.state, step.title, step.hint, step.resolved])
    ]);
    if (key === renderKey && !mount.hidden) return;
    // Keep the person's place when a redraw replaces the control they are on.
    const focused =
      doc.activeElement && typeof mount.contains === 'function' && mount.contains(doc.activeElement)
        ? doc.activeElement.getAttribute?.('data-hq-greeting-choice')
        : '';
    renderKey = key;
    mount.textContent = '';
    const card = element(doc, 'article', 'pa-hq-greeting');
    card.setAttribute('aria-labelledby', 'paHQGreetingTitle');
    const name = text(assistant.name);
    if (name) {
      // The same line the thread draws above each message: face, then name.
      const author = element(doc, 'p', 'personal-assistant-message__author');
      const face = element(doc, 'span', 'personal-assistant-message__author-avatar');
      face.setAttribute('aria-hidden', 'true');
      face.innerHTML = String(avatar(assistant) || '');
      author.appendChild(face);
      author.appendChild(element(doc, 'span', '', name));
      card.appendChild(author);
    }
    const title = element(doc, 'h3', 'pa-hq-greeting__title', lines.title);
    title.id = 'paHQGreetingTitle';
    card.appendChild(title);
    for (const line of [lines.running, lines.point, lines.off]) {
      if (line) card.appendChild(element(doc, 'p', 'pa-hq-greeting__line', line));
    }
    const ask = element(doc, 'p', 'pa-hq-greeting__line pa-hq-greeting__ask', lines.ask);
    ask.id = 'paHQGreetingAsk';
    card.appendChild(ask);
    const choices = element(doc, 'ul', 'pa-hq-greeting__choices');
    choices.setAttribute('aria-labelledby', 'paHQGreetingAsk');
    for (const step of steps) choices.appendChild(choiceRow(step));
    card.appendChild(choices);
    card.appendChild(element(doc, 'p', 'pa-hq-greeting__footer', lines.footer));
    mount.appendChild(card);
    mount.hidden = false;
    if (focused) mount.querySelector?.(`[data-hq-greeting-choice="${focused}"]`)?.focus?.();
  }

  function evaluate() {
    scheduled = false;
    const now = gate();
    if (now === 'show') {
      let current = null;
      try {
        current = view();
      } catch (_error) {
        current = null;
      }
      if (current?.show === true) render(current);
      else clear();
    } else if (renderKey || !mount.hidden) clear();
    const holds = greetingHolds(now);
    if (holds !== held) {
      held = holds;
      try {
        dispatch(holds);
      } catch (_error) {
        // The Notes question is told as a courtesy; it also reads the hold itself.
      }
    }
  }

  // Many events arrive together (a recipient, a status, a repaint): answer once,
  // before the next paint, so a card never flashes for the frame in between.
  function schedule() {
    if (scheduled) return;
    scheduled = true;
    queue(evaluate);
  }

  // A choice hands off to its owner. The folder choices are links the browser
  // follows. "Save an idea" is drawn in the page the drawer covers, so the drawer
  // closes first (D2 forbids opening it for this, not closing it); the Daily
  // Brief and Notes open above the drawer and leave it where it is.
  function choose(action) {
    if (action === 'note') {
      closeDrawer({ restoreFocus: false });
      return run('note', null) === true;
    }
    if (action === 'brief') {
      const trigger = {
        focus: options =>
          (
            mount.querySelector?.('[data-hq-greeting-choice="brief"]') ||
            doc.getElementById?.('personalAssistantInput')
          )?.focus?.(options)
      };
      return run('brief', trigger) === true;
    }
    if (action === 'notes') {
      // The launcher, never the greeting's own button: the drawer remembers its
      // opener, and this button is about to be replaced.
      return run('notes', doc.getElementById?.('personalAssistantLauncher') || null) === true;
    }
    return false;
  }

  mount.addEventListener?.('click', event => {
    const control = event.target?.closest?.('[data-hq-greeting-choice]');
    if (control?.getAttribute('data-hq-greeting-choice') === 'folder') {
      control.setAttribute(
        'href',
        handoffURL('/', beginHQAttempt(doc.body?.dataset.guidedHq, 'workspace'))
      );
      closeDrawer({ restoreFocus: false });
      return;
    }
    if (!control || control.tagName === 'A') return;
    choose(control.getAttribute('data-hq-greeting-choice'));
  });

  return {
    gate,
    holdsNotesQuestion: () => greetingHolds(gate()),
    evaluate,
    schedule,
    choose
  };
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  const start = () => {
    const body = document.body;
    const active = body?.dataset?.hqDetails === '1' && body?.dataset?.hqFirstVisit === 'pending';
    const controller = createHQGreeting({
      mount: document.getElementById('personalAssistantHQGreeting'),
      active,
      view: () => window.workspaceDetail?.hqGreetingView?.() ?? null,
      personal: () => window.PersonalAssistantRecipient?.current?.()?.kind === RECIPIENT_PERSONAL,
      loading: () => window.PersonalAssistantConversation?.isLoading?.() === true,
      hasMessages: () =>
        Boolean(document.querySelector('#homeAssistantConversation [data-message-role]')),
      who: () => window.PersonalAssistantRecipient?.current?.() || {},
      avatar: who =>
        window.PersonalAssistantPanel?.avatarMarkup?.(who?.name || '', who?.appearance || {}, 20) ||
        (window.AgentAvatar?.markup
          ? window.AgentAvatar.markup(
              { name: who?.name || '', appearance: who?.appearance || {} },
              { size: 20 }
            )
          : ''),
      run: (action, trigger) => window.workspaceDetail?.runHQGreetingChoice?.(action, trigger),
      closeDrawer: options => window.PersonalAssistantPanel?.close?.(options),
      drawerOpen: () => window.PersonalAssistantPanel?.isOpen?.() === true,
      focusInput: () => document.getElementById('personalAssistantInput')?.focus?.(),
      dispatch: holds =>
        document.dispatchEvent(
          new CustomEvent('personal-assistant:hq-greeting', { detail: { holds } })
        )
    });
    // The Notes question reads this, whether or not the greeting is active.
    window.PersonalAssistantHQGreeting = {
      holdsNotesQuestion: () => controller.holdsNotesQuestion()
    };
    if (!active) return;
    const again = () => controller.schedule();
    for (const name of [
      'personal-assistant:opened',
      'personal-assistant:closed',
      'personal-assistant:recipient',
      'personal-assistant:status',
      'personal-assistant:conversation',
      'personal-assistant:conversation-loading',
      'ori:hq-first-visit'
    ]) {
      document.addEventListener(name, again);
    }
    window.addEventListener('pageshow', again);
    // A message arriving, or a cleared conversation, fires no event of its own.
    const thread = document.getElementById('homeAssistantConversation');
    if (thread && typeof MutationObserver === 'function') {
      new MutationObserver(again).observe(thread, { childList: true, subtree: true });
    }
    again();
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
}
