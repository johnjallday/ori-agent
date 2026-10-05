// One native disclosure per canonical message. Opening it is local UI only;
// the draft/memory controllers still own eligibility, review and confirmation.
const MENU = '.personal-assistant-message__menu';

export function messageActions(row) {
  if (!row?.dataset?.messageId || !row.firstElementChild) return null;
  const bubble = row.firstElementChild;
  const existing = bubble.querySelector('.personal-assistant-message__menu-items');
  if (existing) return existing;
  const doc = row.ownerDocument;
  const actions = doc.createElement('div');
  actions.className = 'personal-assistant-message__actions';
  const menu = doc.createElement('details');
  menu.className = 'personal-assistant-message__menu';
  const summary = doc.createElement('summary');
  summary.className = 'personal-assistant-message__toggle';
  summary.textContent = 'Message actions';
  const items = doc.createElement('div');
  items.className = 'personal-assistant-message__menu-items';
  menu.append(summary, items);
  actions.append(menu);
  bubble.append(actions);
  menu.addEventListener('keydown', event => {
    if (event.key !== 'Escape' || !menu.open) return;
    event.preventDefault();
    event.stopPropagation();
    menu.open = false;
    summary.focus();
  });
  menu.addEventListener('toggle', () => {
    if (!menu.open) return;
    for (const other of doc.querySelectorAll(`${MENU}[open]`)) {
      if (other !== menu) other.open = false;
    }
  });
  return items;
}

// A review closes its message disclosure. Return focus to the still-visible
// summary rather than to a button inside a now-closed disclosure.
export function messageReviewTrigger(trigger) {
  const menu = trigger?.closest?.(MENU);
  if (!menu) return trigger;
  menu.open = false;
  return menu.querySelector('summary');
}

if (typeof document !== 'undefined') {
  document.addEventListener('click', event => {
    for (const menu of document.querySelectorAll(`${MENU}[open]`)) {
      if (!menu.contains(event.target)) menu.open = false;
    }
  });
}
