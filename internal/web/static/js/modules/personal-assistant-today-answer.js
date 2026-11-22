// Only the live, saved personal conversation receives this list. Transcript
// replay renders plain history, never current row controls.
export const NEEDS_YOU = '/?panel=today';

export function todayAnswerDestination(value) {
  if (typeof value !== 'string' || value !== value.trim()) return '';
  const pattern = /^\/workspaces\/[a-z0-9][a-z0-9-]{0,79}\?(follow_up|ticket)=([a-z0-9._%-]+)$/i;
  const match = pattern.exec(value);
  if (!match) return '';
  try {
    return /^[a-z0-9][a-z0-9._:-]{0,127}$/i.test(decodeURIComponent(match[2])) ? value : '';
  } catch (_) {
    return '';
  }
}

export function renderTodayAnswer(row, data, { isCurrent = () => false, select } = {}) {
  if (!row || !data?.conversation?.stored || !data.today || !isCurrent()) return null;
  const doc = row.ownerDocument;
  const card = doc.createElement('section');
  card.className = 'pa-today-answer';
  card.setAttribute('aria-label', 'Needs you · Today');
  const rows = Array.isArray(data.today.rows) ? data.today.rows : [];
  if (rows.length) {
    const list = doc.createElement('ul');
    for (const item of rows.slice(0, 8)) {
      const li = doc.createElement('li');
      const href = todayAnswerDestination(item.href);
      const label = doc.createElement(href ? 'a' : 'span');
      if (href) label.href = href;
      label.textContent = item.title || 'Today item';
      li.append(label);
      const meta = doc.createElement('small');
      meta.textContent = [item.kind, item.owner].filter(Boolean).join(' · ');
      li.append(meta);
      if (item.selection && select) {
        const button = control(doc, 'Select for snooze', () => {
          if (isCurrent()) select(item);
        });
        button.setAttribute(
          'aria-label',
          `Select ${label.textContent} in ${item.owner} for snooze`
        );
        li.append(button);
      }
      list.append(li);
    }
    card.append(list);
  }
  if (data.today.truncated) {
    const notice = doc.createElement('small');
    notice.textContent = 'Showing the first 8 records. More are available on Home.';
    card.append(notice);
  }
  const home = doc.createElement('a');
  home.href = NEEDS_YOU;
  home.textContent = 'Open Needs you';
  card.append(home);
  (row.firstElementChild || row).append(card);
  return card;
}

function control(doc, label, click, primary = false) {
  const button = doc.createElement('button');
  button.type = 'button';
  button.className = primary ? 'btn btn-sm btn-primary' : 'btn btn-sm btn-outline-secondary';
  button.textContent = label;
  button.addEventListener('click', click);
  return button;
}

export function renderSnoozeCard(host, view, options = {}) {
  const { isCurrent, send, refresh } = options;
  if (!host || !view || !isCurrent?.()) return;
  const doc = host.ownerDocument;
  const status = doc.createElement('p');
  status.setAttribute('role', 'status');
  status.textContent = view.message || '';
  host.replaceChildren(status);
  let busy = false;
  const action = async body => {
    if (busy || !isCurrent()) return;
    busy = true;
    for (const button of host.querySelectorAll('button')) button.disabled = true;
    try {
      const next = await send(body);
      if (isCurrent()) renderSnoozeCard(host, next, options);
      if (next?.status === 'saved') {
        const refreshed = await Promise.resolve()
          .then(() => refresh?.())
          .catch(() => false);
        if (!refreshed && isCurrent()) {
          renderSnoozeCard(
            host,
            {
              ...next,
              message:
                'Snooze saved, but Today couldn’t be refreshed. Open Needs you to check the latest list.'
            },
            options
          );
        }
      }
    } catch (_) {
      if (isCurrent()) {
        status.textContent = 'Could not check that snooze. Reopen Needs you before trying again.';
      }
    }
  };
  if (Array.isArray(view.choices)) {
    const list = doc.createElement('ul');
    for (const choice of view.choices) {
      const item = doc.createElement('li');
      item.textContent = [choice.title, choice.owner].filter(Boolean).join(' · ');
      const href = todayAnswerDestination(choice.href);
      if (href) {
        const open = doc.createElement('a');
        open.href = href;
        open.textContent = `Open ${choice.title} in ${choice.owner || 'its workspace'}`;
        item.append(open);
      }
      if (choice.selection && view.status === 'choose') {
        item.append(
          control(doc, `Choose ${choice.owner}: ${choice.title}`, () => {
            void action({
              action: 'review',
              view: view.view,
              selection: choice.selection,
              when: view.when
            });
          })
        );
      }
      list.append(item);
    }
    host.append(list);
  }
  if (view.status === 'review') {
    const heading = doc.createElement('h3');
    heading.textContent = 'Review snooze';
    const details = doc.createElement('p');
    details.textContent = `${view.title} · ${view.owner}`;
    const time = doc.createElement('time');
    time.dateTime = view.until;
    time.textContent = (view.suggested ? 'Suggested: ' : '') + view.until_label;
    host.append(heading, details, time);
    const correction = doc.createElement('form');
    const label = doc.createElement('label');
    label.textContent = 'Change the date or time';
    const input = doc.createElement('input');
    input.type = 'text';
    input.required = true;
    input.maxLength = 40;
    input.placeholder = 'YYYY-MM-DD HH:MM';
    input.className = 'form-control form-control-sm';
    label.append(input);
    const apply = control(doc, 'Review new time', () => {});
    apply.type = 'submit';
    correction.append(label, apply);
    correction.addEventListener('submit', event => {
      event.preventDefault();
      void action({
        action: 'review',
        view: view.view,
        selection: view.selection,
        when: input.value
      });
    });
    const footer = doc.createElement('div');
    footer.className = 'pa-snooze-controls';
    const confirm = control(
      doc,
      'Confirm snooze',
      () => {
        void action({ action: 'confirm', review: view.review });
      },
      true
    );
    confirm.disabled = !view.can_confirm;
    input.addEventListener('input', () => {
      confirm.disabled = !view.can_confirm || Boolean(input.value.trim());
    });
    footer.append(
      control(doc, 'Cancel', () => {
        void action({ action: 'cancel', review: view.review });
      }),
      confirm
    );
    host.append(correction, footer);
  }
  if (view.status === 'saved') {
    const time = doc.createElement('time');
    time.dateTime = view.until;
    time.textContent = `Snoozed until ${view.until_label}`;
    host.append(time);
    const href = todayAnswerDestination(view.href);
    if (href) {
      const open = doc.createElement('a');
      open.href = href;
      open.textContent = `Open ${view.title} in ${view.owner}`;
      host.append(open);
    }
  }
  if (
    ['stale', 'conflict', 'unknown', 'handoff'].includes(view.status) ||
    (view.status === 'saved' && view.message !== 'Snooze saved.')
  ) {
    const home = doc.createElement('a');
    home.href = NEEDS_YOU;
    home.textContent = 'Open Needs you';
    host.append(home);
  }
}

export function createTodayAnswerController() {
  let current;
  return {
    render(row, data, options) {
      current = null;
      const card = renderTodayAnswer(row, data, {
        ...options,
        select(item) {
          if (!current || !current.valid()) return;
          current.selection = item.selection;
          current.notice.textContent = `Selected ${item.title} in ${item.owner}. Ask when to snooze it.`;
          options.focus?.();
        }
      });
      if (card && data.today.view) {
        const notice = card.ownerDocument.createElement('small');
        notice.setAttribute('role', 'status');
        card.append(notice);
        current = {
          id: data.today.view,
          card,
          notice,
          valid: () => card.isConnected && options.isCurrent()
        };
      }
      return card;
    },
    ref() {
      if (!current?.valid()) return null;
      return { view: current.id, selection: current.selection };
    },
    renderSnooze(row, view, options) {
      if (!row || !options.isCurrent()) return;
      const host = row.ownerDocument.createElement('section');
      host.className = 'pa-today-answer pa-snooze-card';
      host.setAttribute('aria-label', 'Follow-up snooze');
      (row.firstElementChild || row).append(host);
      const selection = current;
      renderSnoozeCard(host, view, {
        ...options,
        refresh() {
          for (const button of selection?.card.querySelectorAll('button') || [])
            button.disabled = true;
          if (current === selection) current = null;
          return options.refresh?.();
        }
      });
    }
  };
}

if (typeof window !== 'undefined')
  window.PersonalAssistantTodayAnswer = createTodayAnswerController();
