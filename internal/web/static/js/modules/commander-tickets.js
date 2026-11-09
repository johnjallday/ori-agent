// commander-tickets.js — the Commander narrates its tickets in its balloon
// (PRD commander-speaks-on-map, FR21–FR25). The map's activity layer hands
// over the Commander unit's line ("On it.", "Thinking it through…", "I need
// your input.", "Back to it.", "Done."), the ticket it is on and its newest
// result; this module turns that into balloon items: working, waiting for
// the owner (with the question and its choices) and done (with the result).
//
// Answers go through the endpoint the ticket page already uses
// (POST /api/orchestration/tasks/{id}/assist); no new endpoint.

export const COMMANDER_ACTIVITY_EVENT = 'ori:commander-activity';

const ASSIST_URL = id => `/api/orchestration/tasks/${encodeURIComponent(id)}/assist`;

function text(value) {
  return typeof value === 'string' ? value.trim() : '';
}

const OPTION_LINE = /^\s*(?:[-*•]|\(?[0-9]{1,2}[.)]|\(?[A-Za-z][.)])\s+/;
const QUESTION_MAX = 240;

// agentQuestion is the agent's own ask in its reply: the lines before its
// listed options, as one sentence, bounded. '' when it has none.
export function agentQuestion(response) {
  const lines = String(response || '')
    .split(/\r?\n/)
    .map(line => line.trim());
  const ask = [];
  for (const line of lines) {
    if (OPTION_LINE.test(line)) break;
    if (line) ask.push(line);
  }
  const words = ask.join(' ').replace(/\s+/g, ' ').trim();
  return words.length > QUESTION_MAX ? `${words.slice(0, QUESTION_MAX - 1)}…` : words;
}

// ticketWaitView reads what a waiting ticket asks (task.context.human_loop):
// the question, the listed choices, and whether a one-line answer is taken.
// A form (several fields) is answered on the ticket itself.
export function ticketWaitView(task) {
  if (!task || !text(task.id)) return null;
  const loop =
    task.context?.human_loop && typeof task.context.human_loop === 'object'
      ? task.context.human_loop
      : {};
  const step = loop.workflow_step || task.context?.planning_workflow_step || null;
  const stepType = text(step?.step_type).toLowerCase();
  const choices =
    stepType === 'ask_choice' && Array.isArray(step.choices)
      ? step.choices
          .map((choice, index) => ({
            id: text(choice?.id) || `choice-${index + 1}`,
            label: text(choice?.label),
            number: text(choice?.number),
            recommended: choice?.recommended === true
          }))
          .filter(choice => choice.label)
      : [];
  const form = stepType === 'ask_form';
  return {
    taskId: text(task.id),
    title: text(task.title) || text(task.description).split('\n')[0],
    blockId: text(loop.block_id),
    // The agent's own words come first; the host's generic question is the
    // fallback ("I still need confirmation to continue…").
    question:
      agentQuestion(loop.agent_response) ||
      text(loop.question) ||
      text(step?.title) ||
      text(loop.reason) ||
      'This ticket needs your input before it can continue.',
    choices,
    form,
    // The ticket page takes a one-line answer unless the step forbids it.
    freeText: !form && step?.free_text_allowed !== false
  };
}

// answerTicket sends the owner's choice or line the way the ticket page does.
export async function answerTicket(wait, { choice, message } = {}, fetchImpl = globalThis.fetch) {
  if (!wait?.taskId) return { ok: false, error: 'That ticket is gone.' };
  const body = { action: 'continue_with_instruction' };
  if (wait.blockId) body.block_id = wait.blockId;
  if (choice) {
    body.choice_id = choice.id;
    body.choice_label = choice.label;
    if (choice.number) body.choice_number = choice.number;
  }
  const line = text(message);
  if (line) body.message = line;
  if (!choice && !line) return { ok: false, error: 'Choose one or answer in a line.' };
  try {
    const response = await fetchImpl(ASSIST_URL(wait.taskId), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body)
    });
    if (response.ok) return { ok: true, error: '' };
    let payload = null;
    try {
      payload = await response.json();
    } catch (_) {
      payload = null;
    }
    return { ok: false, error: text(payload?.error) || 'That answer could not be sent.' };
  } catch (_) {
    return { ok: false, error: 'That answer could not be sent.' };
  }
}

function el(doc, tag, className, words) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (words != null) node.textContent = words;
  return node;
}

// renderTicketWait draws a waiting ticket: its question, its choices as
// buttons, a one-line answer when taken, else Open the ticket.
export function renderTicketWait(doc, wait, { onChoice, onAnswer, onOpen, error = '' } = {}) {
  const box = el(doc, 'section', 'commander-ticket commander-ticket--waiting');
  box.dataset.taskId = wait.taskId;
  if (wait.title) box.append(el(doc, 'p', 'commander-ticket__title', wait.title));
  box.append(el(doc, 'p', 'commander-ticket__question', wait.question));
  if (wait.choices.length) {
    const list = el(doc, 'div', 'commander-ticket__choices');
    wait.choices.forEach(choice => {
      const button = el(
        doc,
        'button',
        'btn btn-sm ' + (choice.recommended ? 'btn-primary' : 'folder-agent-card__secondary'),
        choice.label
      );
      button.type = 'button';
      button.dataset.commanderChoice = choice.id;
      button.addEventListener('click', () => onChoice?.(choice, button));
      list.append(button);
    });
    box.append(list);
  }
  if (wait.freeText) {
    const row = el(doc, 'div', 'commander-ticket__answer');
    const input = doc.createElement('input');
    input.type = 'text';
    input.maxLength = 500;
    input.className = 'form-control form-control-sm commander-ticket__answer-input';
    input.setAttribute('aria-label', 'Your answer');
    input.placeholder = wait.choices.length ? 'Or answer in one line' : 'Answer in one line';
    const send = el(doc, 'button', 'btn btn-sm folder-agent-card__secondary', 'Answer');
    send.type = 'button';
    send.dataset.commanderAnswer = wait.taskId;
    send.addEventListener('click', () => {
      const line = text(input.value);
      if (line) onAnswer?.(line, send);
    });
    row.append(input, send);
    box.append(row);
  }
  const open = el(doc, 'button', 'btn btn-sm btn-link commander-ticket__open', 'Open the ticket');
  open.type = 'button';
  open.dataset.activityOpenTask = wait.taskId;
  open.addEventListener('click', () => onOpen?.(wait.taskId));
  box.append(open);
  if (error) {
    const alert = el(doc, 'p', 'folder-agent-card__error', error);
    alert.setAttribute('role', 'alert');
    box.append(alert);
  }
  return box;
}

// renderTicketResult draws a finished ticket: the result's first line, Open
// the result and Dismiss.
export function renderTicketResult(doc, result, { onOpen, onDismiss } = {}) {
  const box = el(doc, 'section', 'commander-ticket commander-ticket--done');
  box.dataset.parcelId = result.parcelId;
  if (result.title) box.append(el(doc, 'p', 'commander-ticket__title', result.title));
  if (result.firstLine) box.append(el(doc, 'p', 'commander-ticket__result', result.firstLine));
  const row = el(doc, 'div', 'commander-ticket__actions');
  const open = el(doc, 'button', 'btn btn-sm btn-primary', 'Open the result');
  open.type = 'button';
  open.dataset.commanderOpenResult = result.parcelId;
  open.addEventListener('click', () => onOpen?.(result));
  const dismiss = el(doc, 'button', 'btn btn-sm folder-agent-card__secondary', 'Dismiss');
  dismiss.type = 'button';
  dismiss.addEventListener('click', () => onDismiss?.(result));
  row.append(open, dismiss);
  box.append(row);
  return box;
}

function firstLine(value) {
  return text(String(value || '').split('\n')[0]).slice(0, 200);
}

// commanderTicketItems turns the Commander's activity into balloon items.
// activity: {agent, line, blocked, finishing, taskId, parcel}; titleOf(id)
// and taskOf(id) read the page's tickets; dismissed holds result ids the
// owner has opened or dismissed. actions: {answer, open, openResult,
// dismiss, refresh}.
export function commanderTicketItems(
  activity,
  {
    titleOf = () => '',
    taskOf = () => null,
    resultOf = parcel => (parcel?.ref_id ? taskOf(parcel.ref_id)?.result : ''),
    dismissed = new Set(),
    answered = new Set(),
    actions = {},
    errors = {}
  } = {}
) {
  if (!activity) return [];
  const items = [];
  const agent = text(activity.agent);
  const taskId = text(activity.taskId);
  const title = taskId ? text(titleOf(taskId)) : '';
  // Answered here, not yet resumed on the stream: the question is gone, the
  // Commander is getting back to it (no stale controls in between).
  const resuming = activity.blocked && taskId && answered.has(taskId);
  if (resuming) activity = { ...activity, blocked: false, line: 'Back to it.' };
  if (activity.blocked && taskId) {
    const wait = ticketWaitView(taskOf(taskId)) || {
      taskId,
      title,
      blockId: '',
      question: 'This ticket needs your input before it can continue.',
      choices: [],
      form: false,
      freeText: false
    };
    const asking = wait.question ? `I need your input: ${wait.question}` : 'I need your input';
    items.push({
      kind: 'ticket_waiting',
      agent,
      line: 'I need your input · Answer',
      headline: wait.title || title || '',
      announce: 'I need your input',
      signature: `wait|${taskId}|${wait.question}|${wait.choices.map(c => c.id).join(',')}|${errors[taskId] || ''}`,
      words: asking,
      body: doc =>
        renderTicketWait(doc, wait, {
          error: errors[taskId] || '',
          onChoice: (choice, button) => actions.answer?.(wait, { choice }, button),
          onAnswer: (message, button) => actions.answer?.(wait, { message }, button),
          onOpen: id => actions.open?.(id)
        })
    });
  } else if (activity.line && !activity.finishing) {
    // A run starting says what it works on; then its step lines.
    const line = activity.line === 'On it.' && title ? `Working on: ${title}` : activity.line;
    items.push({
      kind: 'ticket_working',
      agent,
      line,
      headline: title ? `Working on: ${title}` : '',
      announce: line,
      signature: `work|${taskId}|${line}`,
      body: doc => {
        const box = el(doc, 'section', 'commander-ticket commander-ticket--working');
        box.append(el(doc, 'p', 'commander-ticket__line', line));
        if (taskId) {
          const open = el(
            doc,
            'button',
            'btn btn-sm btn-link commander-ticket__open',
            'Open the ticket'
          );
          open.type = 'button';
          open.addEventListener('click', () => actions.open?.(taskId));
          box.append(open);
        }
        return box;
      }
    });
  } else if (activity.line && activity.finishing) {
    items.push({
      kind: 'ticket_working',
      agent,
      line: activity.line,
      headline: title,
      announce: activity.line,
      signature: `end|${taskId}|${activity.line}`,
      body: doc => el(doc, 'p', 'commander-ticket__line', activity.line)
    });
  }
  const parcel = activity.parcel;
  if (parcel?.id && !dismissed.has(parcel.id)) {
    const done = {
      parcelId: parcel.id,
      title: text(parcel.title),
      firstLine: firstLine(parcel.summary || resultOf(parcel) || ''),
      failed: parcel.outcome === 'failed' || parcel.outcome === 'timeout'
    };
    const label = done.failed ? 'This one didn’t work out' : 'Done';
    items.push({
      kind: 'result',
      agent,
      line: done.title ? `${label}: ${done.title}` : label,
      headline: done.title,
      announce: done.title ? `${label}: ${done.title}` : label,
      signature: `done|${parcel.id}|${done.firstLine}`,
      body: doc =>
        renderTicketResult(doc, done, {
          onOpen: result => actions.openResult?.(result),
          onDismiss: result => actions.dismiss?.(result)
        })
    });
  }
  return items;
}
