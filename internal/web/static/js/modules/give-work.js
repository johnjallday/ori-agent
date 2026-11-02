/*
 * give-work.js — the one way a person hands a workspace something to do
 * (tasks/prd-760 FR-12 to FR-15).
 *
 * What (required), details, who, due, and Start now. Nothing about output specs
 * or contracts lives here: those are behind Advanced on a run.
 *
 *   nobody chosen      -> Backlog
 *   an agent chosen    -> Ready, and nothing runs yet
 *   Start now          -> Ready, then one run (needs an agent)
 *
 * A start the server refuses leaves the ticket Ready. The dialog says why and
 * offers Start this, which repeats the same request under the same key, so it
 * can never make a second ticket.
 *
 * Every door calls openGiveWork. The classic map script reaches it through
 * window.OriGiveWork. It talks to the server through the ticket client
 * (window.WorkspaceHubTickets), so a new ticket refreshes the work count like
 * any other.
 */

function escapeHTML(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** A key for one opening of the dialog, so a retry cannot make a second ticket. */
export function newGiveWorkKey() {
  const random =
    typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return `give-work:${random}`;
}

/** Start now is only offered once someone is chosen to do the work (FR-12). */
export function canStartNow(fields) {
  return String((fields && fields.who) || '').trim() !== '';
}

/**
 * Turns the form's fields into the ticket client's input, or returns an error
 * naming the field. The state follows from who was chosen (FR-13).
 */
// dueDateISO is a picked day as the end of that day in the person's own time
// zone, so it reads as that day everywhere and is overdue only once it is over.
export function dueDateISO(day) {
  const text = String(day || '').trim();
  if (!/^\d{4}-\d{2}-\d{2}$/.test(text)) return null;
  const end = new Date(`${text}T23:59:59`);
  return Number.isNaN(end.getTime()) ? null : end.toISOString();
}

export function giveWorkRequest(fields, key) {
  const what = String((fields && fields.what) || '').trim();
  if (!what) return { error: 'Say what needs doing.', field: 'what' };
  const who = String(fields.who || '').trim();
  const due = String(fields.due || '').trim();
  return {
    input: {
      state: who ? 'ready' : 'backlog',
      title: what,
      description: String(fields.details || '').trim(),
      assignee: who,
      start: Boolean(fields.startNow) && who !== '',
      // A date input gives a day; the API takes an instant.
      dueDate: dueDateISO(due),
      idempotencyKey: key
    }
  };
}

/** What to tell the person once the server has answered. */
export function giveWorkOutcome(ticket) {
  const number = (ticket && ticket.displayNumber) || 'This ticket';
  if (ticket && ticket.start && ticket.start.error) {
    return {
      kind: 'start_refused',
      message: `${number} is Ready, but it could not start: ${ticket.start.error}`
    };
  }
  if (ticket && ticket.start && ticket.start.started) {
    return { kind: 'started', message: `${number} has started.` };
  }
  const place = (ticket && ticket.stateLabel) || 'the board';
  return { kind: 'saved', message: `${number} is in ${place}.` };
}

/**
 * A selector that finds a door again after a re-render replaced it: its id, or
 * its first data attribute. '' when it has neither.
 */
export function doorSelector(element) {
  if (!element || typeof element.getAttribute !== 'function') return '';
  const quote = value => String(value).replace(/["\\]/g, '\\$&');
  const id = element.getAttribute('id');
  if (id) return `[id="${quote(id)}"]`;
  const attribute = Array.from(element.attributes || []).find(item =>
    item.name.startsWith('data-')
  );
  return attribute ? `[${attribute.name}="${quote(attribute.value)}"]` : '';
}

/** The dialog's markup. `agents` are names; `assignee` is preselected. */
export function giveWorkHTML({ workspaceName = '', agents = [], assignee = '' } = {}) {
  const names = [...new Set((agents || []).map(name => String(name || '').trim()).filter(Boolean))];
  const chosen = String(assignee || '').trim();
  if (chosen && !names.includes(chosen)) names.push(chosen);
  const whoField = names.length
    ? '<label class="give-work-label" for="giveWorkWho">Who <span class="give-work-optional">(optional)</span></label>' +
      '<select class="form-select" id="giveWorkWho" name="who">' +
      '<option value="">Nobody yet</option>' +
      names
        .map(
          name =>
            `<option value="${escapeHTML(name)}"${name === chosen ? ' selected' : ''}>${escapeHTML(name)}</option>`
        )
        .join('') +
      '</select>'
    : '<p class="give-work-note">No agents here yet, so this goes to the Backlog.</p>';
  return (
    '<form class="give-work-form" method="dialog" novalidate>' +
    '<h2 class="give-work-title" id="giveWorkTitle">Give work' +
    (workspaceName ? ` <span class="give-work-where">to ${escapeHTML(workspaceName)}</span>` : '') +
    '</h2>' +
    '<label class="give-work-label" for="giveWorkWhat">What needs doing?</label>' +
    '<input class="form-control" id="giveWorkWhat" name="what" type="text" maxlength="300" required autocomplete="off">' +
    '<label class="give-work-label" for="giveWorkDetails">Details <span class="give-work-optional">(optional)</span></label>' +
    '<textarea class="form-control" id="giveWorkDetails" name="details" rows="3"></textarea>' +
    whoField +
    '<label class="give-work-label" for="giveWorkDue">Due <span class="give-work-optional">(optional)</span></label>' +
    '<input class="form-control" id="giveWorkDue" name="due" type="date">' +
    '<label class="give-work-start"><input type="checkbox" name="startNow"> Start now</label>' +
    '<p class="give-work-status" data-give-work-status role="status" aria-live="polite"></p>' +
    '<div class="give-work-actions">' +
    '<button type="button" class="modern-btn modern-btn-secondary" data-give-work-cancel>Cancel</button>' +
    '<button type="button" class="modern-btn modern-btn-primary" data-give-work-retry hidden>Start this</button>' +
    '<button type="submit" class="modern-btn modern-btn-primary" data-give-work-submit>Give work</button>' +
    '</div>' +
    '</form>'
  );
}

function ticketClient() {
  const client = typeof window !== 'undefined' ? window.WorkspaceHubTickets : null;
  return client && client.api ? client.api : null;
}

/**
 * Opens Give work for one workspace.
 *
 * options: { workspaceId, workspaceName, agents, assignee, trigger, onSaved }.
 * `trigger` gets focus back on close. `onSaved(ticket)` runs after any save.
 * Returns the dialog element, or null when the page cannot show one.
 */
export function openGiveWork(options = {}) {
  if (typeof document === 'undefined') return null;
  const workspaceId = String(options.workspaceId || '').trim();
  const api = ticketClient();
  if (!workspaceId || !api) return null;

  const dialog = document.createElement('dialog');
  dialog.className = 'give-work';
  dialog.setAttribute('aria-labelledby', 'giveWorkTitle');
  dialog.innerHTML = giveWorkHTML(options);
  document.body.appendChild(dialog);

  const form = dialog.querySelector('form');
  const what = form.elements.what;
  const who = form.elements.who || null;
  const startNow = form.elements.startNow;
  const startLabel = dialog.querySelector('.give-work-start');
  const status = dialog.querySelector('[data-give-work-status]');
  const submit = dialog.querySelector('[data-give-work-submit]');
  const retry = dialog.querySelector('[data-give-work-retry]');
  const cancel = dialog.querySelector('[data-give-work-cancel]');
  const doorKey = doorSelector(options.trigger);
  let key = newGiveWorkKey();
  let lastInput = null;
  let busy = false;
  let closed = false;

  const fields = () => ({
    what: what.value,
    details: form.elements.details.value,
    who: who ? who.value : '',
    due: form.elements.due.value,
    startNow: startNow.checked
  });

  const syncStartNow = () => {
    const available = canStartNow(fields());
    startNow.disabled = !available;
    startLabel.classList.toggle('is-disabled', !available);
    if (!available) startNow.checked = false;
  };

  const setStatus = (message, tone = '') => {
    status.textContent = message;
    status.dataset.tone = tone;
  };

  const close = () => {
    if (closed) return;
    closed = true;
    if (dialog.open) dialog.close();
    dialog.remove();
    // The page may have re-rendered behind the dialog, so the door can be a new
    // element by now.
    let door = options.trigger || null;
    if (door && door.isConnected === false && doorKey) door = document.querySelector(doorKey);
    if (door && typeof door.focus === 'function') door.focus();
  };

  const send = async input => {
    if (busy) return;
    busy = true;
    submit.disabled = true;
    retry.disabled = true;
    setStatus('Saving…');
    try {
      const ticket = await api.create(workspaceId, input);
      const outcome = giveWorkOutcome(ticket);
      // Wait for the page to redraw, so focus returns to the door that is there
      // afterwards. Its failure is not this save's failure.
      if (typeof options.onSaved === 'function') {
        try {
          await options.onSaved(ticket);
        } catch (_error) {
          // The ticket is saved; the page shows its own loading errors.
        }
      }
      if (outcome.kind === 'start_refused') {
        // The ticket exists. Lock the form so the only way forward is the same
        // request again, or closing.
        Array.from(form.elements).forEach(element => {
          if (element.matches('input, textarea, select')) element.disabled = true;
        });
        submit.hidden = true;
        retry.hidden = false;
        cancel.textContent = 'Close';
        setStatus(outcome.message, 'error');
        retry.disabled = false;
        retry.focus();
        return;
      }
      if (typeof window !== 'undefined' && typeof window.notifyToast === 'function') {
        window.notifyToast(outcome.message, 'success');
      }
      close();
    } catch (error) {
      setStatus((error && error.message) || 'Could not save this.', 'error');
      if (error && error.field === 'title') what.focus();
    } finally {
      busy = false;
      submit.disabled = false;
      retry.disabled = false;
    }
  };

  form.addEventListener('submit', event => {
    event.preventDefault();
    const request = giveWorkRequest(fields(), key);
    if (request.error) {
      setStatus(request.error, 'error');
      what.focus();
      return;
    }
    lastInput = request.input;
    send(request.input);
  });
  // Different words are different work, so they get their own key.
  what.addEventListener('input', () => {
    key = newGiveWorkKey();
  });
  retry.addEventListener('click', () => {
    if (lastInput) send(lastInput);
  });
  cancel.addEventListener('click', close);
  dialog.addEventListener('cancel', event => {
    event.preventDefault();
    close();
  });
  // Esc belongs to this dialog. The page's own Esc handling would otherwise
  // re-render behind it and replace the door focus must return to.
  dialog.addEventListener('keydown', event => {
    if (event.key === 'Escape') event.stopPropagation();
  });
  if (who) who.addEventListener('change', syncStartNow);

  syncStartNow();
  if (typeof options.what === 'string') what.value = options.what;
  dialog.showModal();
  what.focus();
  return dialog;
}

if (typeof window !== 'undefined') {
  window.OriGiveWork = { open: openGiveWork };
}
