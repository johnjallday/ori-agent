import {
  browserStorage,
  hasInterviewDraft,
  openInterviewWizard,
  reviewedInterviewRows
} from './personal-assistant-interview-wizard.js';

export { reviewedInterviewRows };

const INTERVIEW_API = '/api/personal-assistant/knowledge/interview';

export function mountPersonalAssistantInterview(root = document) {
  const shell = root.getElementById('personalHQInterview');
  if (!shell) return null;
  const status = root.getElementById('personalHQInterviewStatus');
  const start = root.getElementById('personalHQInterviewStart');
  const defer = root.getElementById('personalHQInterviewDefer');
  const build = root.getElementById('personalHQInterviewBuild');
  let snapshot;
  let busy = false;

  function message(text, warning = false) {
    status.textContent = text;
    status.classList.toggle('is-warning', warning);
  }

  async function api(url, options) {
    const response = await fetch(url, { credentials: 'same-origin', ...options });
    const data = await response.json().catch(() => ({}));
    if (!response.ok || data.error) {
      const error = new Error(data.error || 'Interview is unavailable. Try again in a moment.');
      error.status = response.status;
      throw error;
    }
    return data;
  }

  async function load() {
    shell.setAttribute('aria-busy', 'true');
    try {
      snapshot = await api(INTERVIEW_API);
      build.hidden = true;
      start.textContent = hasInterviewDraft(browserStorage())
        ? 'Resume interview'
        : 'Start interview';
      start.hidden = snapshot.status === 'completed';
      defer.hidden = snapshot.status === 'completed';
      if (snapshot.status === 'completed') {
        message('Interview complete. Review or change saved facts below.');
      } else {
        message(
          snapshot.status === 'deferred'
            ? 'Deferred. Come back when you are ready; nothing was saved by deferring.'
            : 'Optional. Start when ready; nothing is saved until you press Save.'
        );
      }
    } catch (error) {
      start.hidden = true;
      defer.hidden = true;
      build.hidden = error.status === 409 ? false : true;
      message(
        error.status === 409
          ? 'Build Personal HQ before saving reviewed assistant facts.'
          : 'Interview state is unavailable. Existing profile editing still works.',
        true
      );
    } finally {
      shell.setAttribute('aria-busy', 'false');
    }
  }

  async function openWizard() {
    if (!snapshot?.questions || busy) return;
    busy = true;
    try {
      await openInterviewWizard(snapshot);
    } finally {
      busy = false;
    }
    start.focus();
  }

  // The wizard can be opened from any page, so the card refreshes on its close
  // event rather than on its own Start click.
  document.addEventListener('personal-assistant-interview-closed', () => void load());
  start.addEventListener('click', () => void openWizard());
  defer.addEventListener('click', async () => {
    if (busy) return;
    busy = true;
    try {
      await api(`${INTERVIEW_API}/defer`, { method: 'POST' });
      await load();
      message('Deferred. Come back when you are ready; nothing was saved by deferring.');
      start.focus();
    } catch (error) {
      message(error.message, true);
    } finally {
      busy = false;
    }
  });
  // /profile#personalHQInterview: the browser jumps to the anchor before the
  // sections above it have loaded, so open the wizard, or once the card has
  // settled scroll to it.
  void load().then(() => {
    if (location.hash !== '#personalHQInterview') return;
    if (snapshot?.questions?.length && snapshot.status !== 'completed') void openWizard();
    else shell.scrollIntoView({ block: 'start' });
  });
  return { load };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => mountPersonalAssistantInterview());
  } else {
    mountPersonalAssistantInterview();
  }
}
