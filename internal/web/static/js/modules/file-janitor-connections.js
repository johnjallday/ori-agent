// file-janitor-connections.js — the "Sends to" and "Receives from" sections of a
// workspace's Filing Settings, and the card that turns a connection on.
//
// This module only draws. The console owns every request and every piece of
// state; here each name the server sent reaches the page as text, never as markup,
// and every control is a real button, checkbox or select with a visible label.
//
// The card is the one place the permission is explained (FR 5): the two
// workspaces with a one-way line between them, what it allows, what it never
// allows, that every send waits for approval, and that either side can turn it
// off. The words follow the mockup's first screen.

const BANNED_WORDS = ['linked', 'batch', 'candidate', 'proposal', 'exact', 'explicitly', 'draft'];
export { BANNED_WORDS };

// The kinds, in the fixed display order the server also uses.
export const KINDS = [
  { id: 'documents', label: 'Documents' },
  { id: 'images', label: 'Images' },
  { id: 'audio', label: 'Audio' },
  { id: 'video', label: 'Video' },
  { id: 'archives', label: 'Archives' },
  { id: 'installers', label: 'Installers' },
  { id: 'data', label: 'Data' },
  { id: 'other', label: 'Other' }
];

const BIDI_CONTROLS = /[‎‏‪-‮⁦-⁩]/g;
// eslint-disable-next-line no-control-regex
const CONTROL_CHARS = /[\u0000-\u001f\u007f-\u009f]/g;

// workspaceName cleans a workspace name the same way file names are cleaned: a
// name is typed by a person and could carry a direction override.
export function workspaceName(value, fallback = 'That workspace') {
  const raw = typeof value === 'string' ? value : '';
  const cleaned = raw.replace(CONTROL_CHARS, '').replace(BIDI_CONTROLS, '').trim();
  return cleaned || fallback;
}

export function kindLabel(id) {
  const kind = KINDS.find(item => item.id === id);
  return kind ? kind.label : String(id || '');
}

// kindsPhrase is "Documents", "Documents and Data", "Documents, Data and Other".
export function kindsPhrase(ids) {
  const labels = (ids || []).map(kindLabel).filter(Boolean);
  if (labels.length <= 1) return labels.join('');
  return labels.slice(0, -1).join(', ') + ' and ' + labels[labels.length - 1];
}

// preTicked is the kinds the card ticks when it opens: Documents only for a
// workspace made from the guided Documents suggestion, nothing otherwise (FR 6).
export function preTicked(receiver) {
  return receiver && receiver.guided_documents ? ['documents'] : [];
}

// carriedBy maps each kind another connection already carries to that
// connection's receiver name, so the card can say why a kind is taken (FR 7).
export function carriedBy(connections, exceptReceiverId) {
  const out = {};
  ((connections && connections.sends_to) || []).forEach(view => {
    if (!view.on || view.receiver_workspace_id === exceptReceiverId) return;
    (view.kinds || []).forEach(kind => {
      out[kind] = workspaceName(view.receiver_name);
    });
  });
  return out;
}

function builder(doc) {
  return (tag, className, text) => {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  };
}

function actionButton(h, label, className, onClick, disabled) {
  const b = h('button', className, label);
  b.setAttribute('type', 'button');
  if (disabled) {
    b.disabled = true;
    b.setAttribute('aria-disabled', 'true');
  }
  b.addEventListener('click', () => {
    if (!b.disabled) onClick();
  });
  return b;
}

function statusLine(h, view, side) {
  if (!view.on) {
    return h('p', 'fj-connection-ended', view.ended || 'Ended.');
  }
  const kinds = kindsPhrase(view.kinds);
  const text =
    side === 'sender'
      ? 'On · ' +
        kinds +
        ' files you approve on Review go to the top level of ' +
        workspaceName(view.receiver_name) +
        '.'
      : 'On · ' +
        workspaceName(view.sender_name) +
        ' can send ' +
        kinds +
        ' files here. Each send waits for approval there.';
  return h('p', 'fj-connection-on', text);
}

function connectionRow(h, view, side, ctx) {
  const row = h('div', 'fj-connection');
  row.setAttribute('data-connection-id', view.id || '');
  const text = h('div', 'fj-connection-text');
  const other = side === 'sender' ? view.receiver_name : view.sender_name;
  text.appendChild(h('span', 'fj-connection-name', workspaceName(other)));
  text.appendChild(statusLine(h, view, side));
  row.appendChild(text);
  const label = view.on ? 'Turn off' : 'Remove';
  const off = actionButton(
    h,
    ctx.busyId === view.id ? 'Turning off…' : label,
    'fj-btn fj-btn-secondary',
    () => ctx.onTurnOff && ctx.onTurnOff(view),
    !!ctx.busyId
  );
  off.setAttribute(
    'aria-label',
    (view.on ? 'Turn off ' : 'Remove ') +
      (side === 'sender'
        ? 'sending to ' + workspaceName(view.receiver_name)
        : 'receiving from ' + workspaceName(view.sender_name))
  );
  row.appendChild(off);
  return row;
}

// renderSendsTo draws the sender's "Sends to" section: each connection with Turn
// off, and Connect a workspace (or the card, when it is open).
export function renderSendsTo(doc, connections, ctx = {}) {
  const h = builder(doc);
  const section = h('div', 'fj-setting fj-setting-sends');
  section.setAttribute('data-section', 'sends-to');
  const heading = h('h4', 'fj-setting-heading', 'Sends to');
  heading.id = 'fileJanitorSendsToTitle';
  section.appendChild(heading);
  section.setAttribute('aria-labelledby', heading.id);
  const views = (connections && connections.sends_to) || [];
  if (!views.length) {
    section.appendChild(
      h(
        'p',
        'fj-setting-help',
        'Send files you approve on Review to another workspace’s folder. One way only, and nothing moves until you press File.'
      )
    );
  }
  const list = h('div', 'fj-connection-list');
  views.forEach(view => list.appendChild(connectionRow(h, view, 'sender', ctx)));
  if (views.length) section.appendChild(list);

  if (ctx.card) {
    section.appendChild(renderConnectCard(doc, connections, ctx.card, ctx));
  } else {
    const start = actionButton(
      h,
      views.some(view => view.on) ? 'Connect another workspace' : 'Connect a workspace',
      'fj-btn fj-btn-secondary',
      () => ctx.onConnectStart && ctx.onConnectStart(),
      !!ctx.busyId
    );
    start.id = 'fileJanitorConnectStart';
    section.appendChild(start);
  }
  return section;
}

// renderReceivesFrom draws the receiver's "Receives from" section (FR 41), or
// nothing when no workspace sends here.
export function renderReceivesFrom(doc, connections, ctx = {}) {
  const views = (connections && connections.receives_from) || [];
  if (!views.length) return null;
  const h = builder(doc);
  const section = h('div', 'fj-setting fj-setting-receives');
  section.setAttribute('data-section', 'receives-from');
  const heading = h('h4', 'fj-setting-heading', 'Receives from');
  heading.id = 'fileJanitorReceivesFromTitle';
  section.appendChild(heading);
  section.setAttribute('aria-labelledby', heading.id);
  const list = h('div', 'fj-connection-list');
  views.forEach(view => list.appendChild(connectionRow(h, view, 'receiver', ctx)));
  section.appendChild(list);
  return section;
}

// renderConnectCard is the card that turns a connection on. card holds what the
// person has chosen so far: { receiverId, kinds: [ids], error }.
export function renderConnectCard(doc, connections, card, ctx = {}) {
  const h = builder(doc);
  const sender = workspaceName(connections && connections.workspace_name, 'This workspace');
  const receivers = (connections && connections.receivers) || [];
  const box = h('div', 'fj-connect-card');
  box.id = 'fileJanitorConnectCard';
  box.setAttribute('role', 'group');

  if (!receivers.length) {
    box.setAttribute('aria-label', 'Connect a workspace');
    box.appendChild(
      h(
        'p',
        'fj-setting-help',
        'No other workspace has Filing set up on its folder yet. Set up Filing in the workspace you want to send to, then come back here.'
      )
    );
    box.appendChild(
      actionButton(
        h,
        'Close',
        'fj-btn fj-btn-secondary',
        () => ctx.onConnectCancel && ctx.onConnectCancel()
      )
    );
    return box;
  }

  const receiver = receivers.find(item => item.workspace_id === card.receiverId) || receivers[0];
  const receiverName = workspaceName(receiver.name);
  box.setAttribute('aria-label', 'Let ' + sender + ' send files to ' + receiverName);

  const title = h(
    'h5',
    'fj-connect-title',
    'Let ' + sender + ' send files to ' + receiverName + '?'
  );
  title.id = 'fileJanitorConnectTitle';
  box.appendChild(title);
  box.appendChild(
    h(
      'p',
      'fj-setting-help',
      'One way, between these two folders only. Putting workspaces in a group never turns this on.'
    )
  );

  if (receivers.length > 1) {
    const field = h('div', 'fj-connect-field');
    const label = h('label', 'fj-label', 'Send to');
    label.setAttribute('for', 'fileJanitorConnectReceiver');
    const select = h('select', 'fj-input');
    select.id = 'fileJanitorConnectReceiver';
    receivers.forEach(option => {
      const item = h('option', '', workspaceName(option.name));
      item.value = option.workspace_id;
      if (option.workspace_id === receiver.workspace_id) item.selected = true;
      select.appendChild(item);
    });
    select.addEventListener(
      'change',
      () => ctx.onReceiverChange && ctx.onReceiverChange(select.value)
    );
    field.appendChild(label);
    field.appendChild(select);
    box.appendChild(field);
  }

  // The two workspaces with a one-way line between them.
  const diagram = h('div', 'fj-connect-diagram');
  diagram.setAttribute('role', 'img');
  diagram.setAttribute(
    'aria-label',
    sender + ' sends to ' + receiverName + ', one way. Nothing comes back.'
  );
  const from = h('div', 'fj-connect-end fj-connect-from');
  from.appendChild(h('span', 'fj-connect-end-name', sender));
  from.appendChild(h('span', 'fj-connect-end-help', 'Filing reviews what arrives here'));
  const line = h('div', 'fj-connect-line');
  line.appendChild(h('span', 'fj-connect-line-label', 'sends to'));
  line.appendChild(h('span', 'fj-connect-arrow', ''));
  line.appendChild(h('span', 'fj-connect-line-help', 'one way, nothing comes back'));
  const to = h('div', 'fj-connect-end fj-connect-to');
  to.appendChild(h('span', 'fj-connect-end-name', receiverName));
  to.appendChild(h('span', 'fj-connect-end-help', 'Arrivals land at the top level'));
  diagram.appendChild(from);
  diagram.appendChild(line);
  diagram.appendChild(to);
  box.appendChild(diagram);

  const lists = h('div', 'fj-connect-lists');
  const allows = h('section', 'fj-connect-list fj-connect-allows');
  allows.appendChild(h('h6', 'fj-connect-list-title', 'What this allows'));
  const allowItems = h('ul', 'fj-disclosure-list');
  allowItems.appendChild(
    h(
      'li',
      'fj-disclosure-item',
      'Moving files you approve from ' + sender + ' into ' + receiverName + '.'
    )
  );
  if (ctx.agentName) {
    allowItems.appendChild(
      h(
        'li',
        'fj-disclosure-item',
        workspaceName(ctx.agentName, 'The Filing agent') +
          ' suggesting which files to send. A suggestion moves nothing.'
      )
    );
  }
  allows.appendChild(allowItems);
  const never = h('section', 'fj-connect-list fj-connect-never');
  never.appendChild(h('h6', 'fj-connect-list-title', 'What it never allows'));
  const neverItems = h('ul', 'fj-disclosure-list');
  [
    'Reading, renaming, replacing or deleting anything already in ' + receiverName + '.',
    'Sending anything from ' + receiverName + ' back to ' + sender + '.',
    'Moving a file you did not approve.'
  ].forEach(text => neverItems.appendChild(h('li', 'fj-disclosure-item', text)));
  never.appendChild(neverItems);
  lists.appendChild(allows);
  lists.appendChild(never);
  box.appendChild(lists);

  const facts = h('dl', 'fj-connect-facts');
  [
    [
      'Your approval',
      'Every send waits for you on Review. Undo all puts sent files back in ' + sender + '.'
    ],
    [
      'Turning it off',
      'From either workspace, at any time. Files already sent stay where they are.'
    ]
  ].forEach(([term, detail]) => {
    const row = h('div', 'fj-connect-fact');
    row.appendChild(h('dt', 'fj-connect-fact-term', term));
    row.appendChild(h('dd', 'fj-connect-fact-detail', detail));
    facts.appendChild(row);
  });
  box.appendChild(facts);

  // Which kinds this connection carries. At least one; a kind another
  // connection carries is shown taken, with where it goes.
  const kinds = h('fieldset', 'fj-connect-kinds');
  kinds.appendChild(h('legend', 'fj-label', 'Which kinds of file can go to ' + receiverName));
  const taken = carriedBy(connections, receiver.workspace_id);
  const chosen = new Set(card.kinds || []);
  KINDS.forEach(kind => {
    const option = h('label', 'fj-checkbox fj-connect-kind');
    const input = h('input', '');
    input.type = 'checkbox';
    input.value = kind.id;
    input.id = 'fileJanitorConnectKind-' + kind.id;
    input.checked = chosen.has(kind.id);
    if (taken[kind.id]) {
      input.disabled = true;
      input.checked = false;
    }
    input.addEventListener(
      'change',
      () => ctx.onKindToggle && ctx.onKindToggle(kind.id, input.checked)
    );
    option.appendChild(input);
    option.appendChild(h('span', 'fj-checkbox-label', kind.label));
    if (taken[kind.id])
      option.appendChild(h('span', 'fj-checkbox-help', 'Already goes to ' + taken[kind.id]));
    kinds.appendChild(option);
  });
  box.appendChild(kinds);

  const refusal = card.error || receiver.refusal;
  if (refusal) {
    const note = h('p', 'fj-connect-refusal', refusal);
    note.id = 'fileJanitorConnectRefusal';
    note.setAttribute('role', 'alert');
    box.appendChild(note);
  }

  const actions = h('div', 'fj-connect-actions');
  actions.appendChild(
    actionButton(
      h,
      'Not now',
      'fj-btn fj-btn-secondary',
      () => ctx.onConnectCancel && ctx.onConnectCancel(),
      !!ctx.busyId
    )
  );
  const noKind = chosen.size === 0 || [...chosen].every(id => taken[id]);
  const allow = actionButton(
    h,
    ctx.busyId === 'connect' ? 'Turning on…' : 'Allow sending to ' + receiverName,
    'fj-btn fj-btn-primary',
    () =>
      ctx.onConnectAllow &&
      ctx.onConnectAllow(
        receiver.workspace_id,
        [...chosen].filter(id => !taken[id])
      ),
    !!ctx.busyId || noKind || !!receiver.refusal
  );
  allow.id = 'fileJanitorConnectAllow';
  actions.appendChild(allow);
  if (noKind && !receiver.refusal) {
    actions.appendChild(h('p', 'fj-setting-help', 'Tick at least one kind of file to send.'));
  }
  box.appendChild(actions);
  return box;
}

// ARRIVAL_PAGE is how many arrived names the list shows at a time (FR 37).
export const ARRIVAL_PAGE = 5;

// arrivedWhen is "today, 10:42" or a date, in the person's own clock.
export function arrivedWhen(iso, now = new Date()) {
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return '';
  const time = when.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  if (when.toDateString() === now.toDateString()) return 'today, ' + time;
  return when.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ', ' + time;
}

export function arrivedTitle(arrival) {
  const count = arrival?.count || 0;
  return (
    (count === 1 ? '1 file arrived from ' : count + ' files arrived from ') +
    workspaceName(arrival?.sender_name)
  );
}

// renderArrivals draws the receiving workspace's strip and list (FR 36 to 39), or
// nothing when nothing that arrived is still loose. ctx: shown (arrival id ->
// names shown), busyId, message, now, onUndo, onDismiss, onShowMore.
export function renderArrivals(doc, arrivals, ctx = {}) {
  const list = Array.isArray(arrivals) ? arrivals : [];
  if (!list.length && !ctx.message) return null;
  const h = builder(doc);
  const section = h('section', 'fj-arrivals');
  section.setAttribute('aria-label', 'What arrived from other workspaces');
  for (const arrival of list) {
    const sender = workspaceName(arrival.sender_name);
    const card = h('div', 'fj-arrival');
    card.setAttribute('data-arrival-id', arrival.id);
    card.setAttribute('role', 'group');
    const strip = h('div', 'fj-arrival-strip');
    const icon = h('span', 'fj-arrival-icon', '↓');
    icon.setAttribute('aria-hidden', 'true');
    strip.appendChild(icon);
    const text = h('div', 'fj-arrival-text');
    const title = h('strong', 'fj-arrival-title', arrivedTitle(arrival));
    title.id = 'fileJanitorArrival-' + arrival.id;
    card.setAttribute('aria-labelledby', title.id);
    text.appendChild(title);
    const when = arrivedWhen(arrival.arrived_at, ctx.now);
    if (when) text.appendChild(h('span', 'fj-arrival-when', when));
    text.appendChild(
      h(
        'p',
        'fj-arrival-help',
        'You sent them from the ' +
          sender +
          ' review. They are loose at the top level until you file them here.'
      )
    );
    strip.appendChild(text);
    const actions = h('div', 'fj-arrival-actions');
    const busy = Boolean(ctx.busyId);
    if (ctx.onReview) {
      const help = h(
        'p',
        'fj-arrival-help',
        'Review local filing scans this workspace’s folder. Choose a destination here and confirm separately to move anything.'
      );
      help.id = 'fileJanitorArrivalReviewHelp-' + arrival.id;
      text.appendChild(help);
      const review = actionButton(
        h,
        'Review local filing',
        'fj-btn fj-btn-secondary',
        () => ctx.onReview(arrival),
        busy || ctx.reviewBusy
      );
      review.setAttribute('aria-describedby', help.id);
      actions.appendChild(review);
    }
    const undo = actionButton(
      h,
      ctx.busyId === arrival.id ? 'Putting files back…' : 'Undo this send',
      'fj-btn fj-btn-secondary',
      () => ctx.onUndo && ctx.onUndo(arrival),
      busy
    );
    undo.id = 'fileJanitorUndoSend-' + arrival.id;
    actions.appendChild(undo);
    const dismiss = actionButton(
      h,
      'Dismiss',
      'fj-link',
      () => ctx.onDismiss && ctx.onDismiss(arrival),
      busy
    );
    dismiss.id = 'fileJanitorDismissArrival-' + arrival.id;
    dismiss.setAttribute('aria-label', 'Dismiss: ' + arrivedTitle(arrival));
    actions.appendChild(dismiss);
    strip.appendChild(actions);
    card.appendChild(strip);

    const names = Array.isArray(arrival.names) ? arrival.names : [];
    const shown = Math.min(
      names.length,
      Math.max(ARRIVAL_PAGE, (ctx.shown || {})[arrival.id] || 0)
    );
    const items = h('ul', 'fj-arrival-files');
    for (const name of names.slice(0, shown)) {
      const item = h('li', 'fj-arrival-file');
      item.appendChild(h('span', 'fj-arrival-name', workspaceName(name, '(unreadable name)')));
      item.appendChild(h('span', 'fj-tag fj-arrival-from', 'from ' + sender));
      items.appendChild(item);
    }
    card.appendChild(items);
    const more = (arrival.count || 0) - shown;
    if (more > 0) {
      if (shown < names.length) {
        const button = actionButton(h, 'and ' + more + ' more', 'fj-more', () =>
          ctx.onShowMore ? ctx.onShowMore(arrival) : undefined
        );
        button.id = 'fileJanitorArrivalMore-' + arrival.id;
        card.appendChild(button);
      } else {
        card.appendChild(h('p', 'fj-sub', 'and ' + more + ' more'));
      }
    }
    section.appendChild(card);
  }
  if (ctx.message) {
    const note = h('p', 'fj-arrival-message', ctx.message);
    note.setAttribute('role', 'status');
    section.appendChild(note);
  }
  return section;
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  window.FileJanitorConnections = {
    renderSendsTo,
    renderReceivesFrom,
    renderConnectCard,
    renderArrivals,
    preTicked,
    kindsPhrase
  };
  document.dispatchEvent(new CustomEvent('ori:filing-connections-ready'));
}
