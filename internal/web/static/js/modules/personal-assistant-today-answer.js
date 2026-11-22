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

export function renderTodayAnswer(row, data, { isCurrent = () => false } = {}) {
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

if (typeof window !== 'undefined') window.PersonalAssistantTodayAnswer = { render: renderTodayAnswer };
