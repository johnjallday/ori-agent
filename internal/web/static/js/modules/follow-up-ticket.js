// follow-up-ticket.js — Make a ticket from a follow-up, and the ticket's state on
// the follow-up (PRD 760 FR-25, FR-26). A link, never ownership: nothing here
// closes either side.

const MAKE_TICKET_URL = '/api/personal-hq/followups/ticket';

// relatedTicketView is a follow-up's `related_ticket` for display, or null.
export function relatedTicketView(raw) {
  if (!raw || !raw.id) return null;
  const number = String(raw.display_number || '').trim();
  const state = String(raw.state_label || '').trim();
  const name = number ? `Ticket ${number}` : 'Ticket';
  return {
    id: raw.id,
    line: state ? `${name} · ${state}` : name,
    href: String(raw.href || '')
  };
}

// chooserWorkspaces is where an HQ follow-up's ticket can go: active workspaces,
// never a group and never HQ itself, because HQ holds commitments rather than
// workspace work.
export function chooserWorkspaces(payload) {
  const list = payload?.workspaces || payload?.folders || (Array.isArray(payload) ? payload : []);
  return list
    .filter(ws => {
      if (!ws?.id) return false;
      if (String(ws.kind || '').toLowerCase() === 'group') return false;
      if (ws.designation === 'personal_hq') return false;
      return !ws.status || ws.status === 'active';
    })
    .map(ws => ({ id: ws.id, name: ws.name || ws.id }));
}

// makeTicket asks the server for the ticket. It resolves to { ticket, created }
// or { choose: true } when the person must pick a workspace, and throws an Error
// whose message can be shown.
export async function makeTicket(fetchImpl, id, workspaceId = '') {
  const res = await fetchImpl(MAKE_TICKET_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ id, workspace_id: workspaceId })
  });
  let payload = null;
  try {
    payload = await res.json();
  } catch (_) {
    payload = null;
  }
  if (res.ok) return { ticket: payload?.ticket || null, created: payload?.created === true };
  if (res.status === 400 && payload?.details?.field === 'workspace_id') return { choose: true };
  throw new Error(payload?.message || payload?.error || 'Could not make a ticket.');
}

// renderRelatedTicket is the line a linked follow-up shows.
export function renderRelatedTicket(doc, raw) {
  const view = relatedTicketView(raw);
  if (!view) return null;
  const line = doc.createElement('p');
  line.className = 'hq-followup-ticket';
  if (view.href) {
    const link = doc.createElement('a');
    link.href = view.href;
    link.textContent = view.line;
    line.appendChild(link);
  } else {
    line.textContent = view.line;
  }
  return line;
}

function clearChooser(card) {
  const existing = card.querySelector('.hq-followup-chooser');
  if (existing) existing.remove();
}

async function renderChooser(doc, card, trigger, followUpId, { fetchImpl, onChanged, root }) {
  clearChooser(card);
  const row = doc.createElement('div');
  row.className = 'hq-followup-chooser';
  const message = doc.createElement('p');
  message.className = 'hq-followup-ticket-error';
  message.setAttribute('role', 'alert');
  message.hidden = true;
  const fail = text => {
    message.textContent = text;
    message.hidden = false;
  };

  let workspaces = [];
  try {
    const res = await fetchImpl('/api/workspaces', { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error('list');
    workspaces = chooserWorkspaces(await res.json());
  } catch (_) {
    fail('Could not load your workspaces.');
  }

  const close = () => {
    clearChooser(card);
    trigger.disabled = false;
    trigger.focus();
  };
  const cancel = doc.createElement('button');
  cancel.type = 'button';
  cancel.className = 'modern-btn modern-btn-secondary modern-btn-sm';
  cancel.textContent = 'Cancel';
  cancel.addEventListener('click', close);

  if (workspaces.length) {
    const label = doc.createElement('label');
    label.className = 'hq-followup-chooser-label';
    label.textContent = 'Which workspace owns this work?';
    const select = doc.createElement('select');
    select.className = 'form-select form-select-sm';
    workspaces.forEach(ws => {
      const option = doc.createElement('option');
      option.value = ws.id;
      option.textContent = ws.name;
      select.appendChild(option);
    });
    label.appendChild(select);
    const confirm = doc.createElement('button');
    confirm.type = 'button';
    confirm.className = 'modern-btn modern-btn-primary modern-btn-sm';
    confirm.textContent = 'Make the ticket';
    confirm.addEventListener('click', async () => {
      confirm.disabled = true;
      message.hidden = true;
      try {
        await makeTicket(fetchImpl, followUpId, select.value);
        await onChanged();
        focusTicketLine(root, followUpId);
      } catch (error) {
        confirm.disabled = false;
        fail(error.message);
      }
    });
    row.append(label, confirm, cancel, message);
    card.appendChild(row);
    select.focus();
    return;
  }
  if (message.hidden) fail('You have no workspace to put this in yet.');
  row.append(cancel, message);
  card.appendChild(row);
  cancel.focus();
}

// focusTicketLine puts focus on a follow-up's new ticket link after its list was
// rebuilt, so a keyboard user is not dropped back at the top of the page.
export function focusTicketLine(root, followUpId) {
  const cards = root?.querySelectorAll?.('[data-followup-id]') || [];
  const card = Array.from(cards).find(item => item.dataset.followupId === followUpId);
  card?.querySelector('.hq-followup-ticket a')?.focus();
}

// startMakeTicket runs the button's flow on one follow-up card. deps: { fetchImpl,
// onChanged, root }. onChanged reloads the list under root so the card shows the
// ticket's state.
export async function startMakeTicket(doc, card, trigger, followUpId, deps) {
  trigger.disabled = true;
  card.querySelector('.hq-followup-ticket-error')?.remove();
  try {
    const result = await makeTicket(deps.fetchImpl, followUpId);
    if (result.choose) {
      await renderChooser(doc, card, trigger, followUpId, deps);
      return;
    }
    await deps.onChanged();
    focusTicketLine(deps.root, followUpId);
  } catch (error) {
    trigger.disabled = false;
    const message = doc.createElement('p');
    message.className = 'hq-followup-ticket-error';
    message.setAttribute('role', 'alert');
    message.textContent = error.message;
    card.appendChild(message);
    trigger.focus();
  }
}
