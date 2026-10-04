const INTERVIEW_API = '/api/personal-assistant/knowledge/interview';
const LINK_SELECTOR = 'a[href$="#personalHQInterview"]';

let launching = false;

async function fetchSnapshot() {
  const response = await fetch(INTERVIEW_API, { credentials: 'same-origin' });
  const data = await response.json().catch(() => ({}));
  if (!response.ok || data.error) throw new Error(data.error || `interview ${response.status}`);
  return data;
}

// Opens the wizard in place. Returns false when the caller should fall back to
// following the link, so the Profile card can explain what is wrong.
export async function launchInterviewWizard() {
  const wizard = await import('./personal-assistant-interview-wizard.js');
  const snapshot = await fetchSnapshot();
  if (snapshot.status === 'completed' || !snapshot.questions?.length) return false;
  await wizard.openInterviewWizard(snapshot);
  return true;
}

function modifiedClick(event) {
  return event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey;
}

async function onClick(event) {
  const link = event.target instanceof Element ? event.target.closest(LINK_SELECTOR) : null;
  if (!link || modifiedClick(event)) return; // let "open in new tab" work
  event.preventDefault();
  if (launching) return;
  launching = true;
  let opened = false;
  try {
    opened = await launchInterviewWizard();
  } catch {
    opened = false; // 409 (no Personal HQ yet) or a network error
  } finally {
    launching = false;
  }
  if (!opened) location.assign(link.href);
}

// Nothing is fetched until a link is clicked.
if (typeof document !== 'undefined') {
  document.addEventListener('click', event => void onClick(event));
}
