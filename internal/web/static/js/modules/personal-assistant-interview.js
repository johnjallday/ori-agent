import { reviewTextValid } from './personal-hq-knowledge.js';

const INTERVIEW_API = '/api/personal-assistant/knowledge/interview';
const editor = new TextEncoder();

function node(tag, text, className) {
  const element = document.createElement(tag);
  if (text !== undefined) element.textContent = text;
  if (className) element.className = className;
  return element;
}

function newRetryID() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  if (!globalThis.crypto?.getRandomValues) throw new Error('Secure retry IDs are unavailable.');
  return Array.from(globalThis.crypto.getRandomValues(new Uint8Array(16)), value =>
    value.toString(16).padStart(2, '0')
  ).join('');
}

export function reviewedInterviewRows(answers, profile) {
  if (!Array.isArray(answers) || answers.length !== 3)
    throw new Error('Reload the three questions.');
  const rows = [];
  for (const answer of answers) {
    if (answer.text === '') continue;
    if (!reviewTextValid(answer.text))
      throw new Error('Use one exact line of at most 500 UTF-8 bytes. Keep secrets in Vault.');
    const row = {
      row_id: answer.id,
      destination: answer.destination,
      category: answer.category,
      text: answer.text
    };
    if (answer.destination === 'profile') {
      if (answer.id !== 'communication' || !profile?.updated_at)
        throw new Error('A current global profile is required for a communication preference.');
      row.preference = answer.preference;
      row.expected_profile_value = profile.preferences?.[answer.preference] || '';
      row.expected_profile_updated_at = profile.updated_at;
    }
    rows.push(row);
  }
  return rows;
}

function safeSelectedDescription(row, profile) {
  if (row.destination === 'profile') {
    return `Global profile · ${row.preference.replaceAll('_', ' ')} · currently ${profile?.preferences?.[row.preference] || 'not set'}`;
  }
  return `Personal HQ · ${row.category.replaceAll('_', ' ')}`;
}

export function mountPersonalAssistantInterview(root = document) {
  const shell = root.getElementById('personalHQInterview');
  if (!shell) return null;
  const status = root.getElementById('personalHQInterviewStatus');
  const start = root.getElementById('personalHQInterviewStart');
  const defer = root.getElementById('personalHQInterviewDefer');
  const build = root.getElementById('personalHQInterviewBuild');
  const form = root.getElementById('personalHQInterviewForm');
  const questions = root.getElementById('personalHQInterviewQuestions');
  const review = root.getElementById('personalHQInterviewReview');
  const final = root.getElementById('personalHQInterviewFinal');
  const selected = root.getElementById('personalHQInterviewSelected');
  const back = root.getElementById('personalHQInterviewBack');
  const save = root.getElementById('personalHQInterviewSave');
  let snapshot;
  let prepared;
  let retry;
  let resetPartial = false;
  let busy = false;

  function message(text, warning = false) {
    status.textContent = text;
    status.classList.toggle('is-warning', warning);
  }

  async function api(url, options) {
    const response = await fetch(url, { credentials: 'same-origin', ...options });
    const data = await response.json().catch(() => ({}));
    if (!response.ok || data.error) {
      const error = new Error(
        data.error || 'Interview is unavailable. Your unsaved answers are still here.'
      );
      error.status = response.status;
      error.partial = data.interview;
      throw error;
    }
    return data;
  }

  function setQuestionRow(question) {
    const fieldset = node('fieldset', undefined, 'reviewed-interview-question');
    fieldset.dataset.questionId = question.id;
    const legend = node('legend', question.prompt);
    const hint = node('p', question.hint || 'Optional — skip if this does not apply.');
    const label = node('label', `Your words — ${question.prompt}`);
    const input = node('textarea');
    input.className = 'form-control';
    input.rows = 2;
    input.maxLength = 500;
    input.id = `interview-answer-${question.id}`;
    label.htmlFor = input.id;
    const count = node('small', '0 / 500 UTF-8 bytes');
    input.addEventListener('input', () => {
      count.textContent = `${editor.encode(input.value).length} / 500 UTF-8 bytes`;
      final.hidden = true; // the visible final review must be regenerated after an edit
    });
    const skip = node('button', 'Skip this question', 'modern-btn modern-btn-secondary');
    skip.type = 'button';
    skip.addEventListener('click', () => {
      input.value = '';
      count.textContent = '0 / 500 UTF-8 bytes';
      final.hidden = true;
      message('Skipped. Nothing from that question will be saved.');
      skip.focus();
    });
    fieldset.append(legend, hint, label, input, count);
    if (question.id === 'communication') {
      const destinationLabel = node('label', 'Save this as');
      const destination = node('select');
      destination.className = 'form-select';
      destination.id = `interview-destination-${question.id}`;
      destinationLabel.htmlFor = destination.id;
      for (const [value, title] of [
        ['personal_hq', 'Personal HQ work-style fact'],
        ['profile', 'Global communication preference (all workspaces)']
      ]) {
        const option = node('option', title);
        option.value = value;
        if (value === 'profile' && !snapshot?.profile?.updated_at) option.disabled = true;
        destination.append(option);
      }
      const preferenceLabel = node('label', 'Global field');
      const preference = node('select');
      preference.id = `interview-preference-${question.id}`;
      preference.className = 'form-select';
      preferenceLabel.htmlFor = preference.id;
      for (const [value, title] of [
        ['response_style', 'Response style'],
        ['units', 'Units'],
        ['language', 'Language']
      ]) {
        const option = node('option', title);
        option.value = value;
        preference.append(option);
      }
      preference.hidden = true;
      preferenceLabel.hidden = true;
      destination.addEventListener('change', () => {
        preference.hidden = destination.value !== 'profile';
        preferenceLabel.hidden = preference.hidden;
        final.hidden = true;
      });
      preference.addEventListener('change', () => (final.hidden = true));
      fieldset.append(destinationLabel, destination, preferenceLabel, preference);
    }
    if (question.id === 'person_or_routine') {
      const categoryLabel = node('label', 'Which kind of fact?');
      const category = node('select');
      category.className = 'form-select';
      category.id = 'interview-person-category';
      categoryLabel.htmlFor = category.id;
      for (const [value, title] of [
        ['routines', 'Routine'],
        ['people', 'Person']
      ]) {
        const option = node('option', title);
        option.value = value;
        category.append(option);
      }
      category.addEventListener('change', () => (final.hidden = true));
      fieldset.append(categoryLabel, category);
    }
    fieldset.append(skip);
    questions.append(fieldset);
  }

  function readAnswers() {
    return snapshot.questions.map(question => {
      const field = questions.querySelector(`[data-question-id="${question.id}"]`);
      return {
        id: question.id,
        text: field.querySelector('textarea').value,
        category:
          question.id === 'person_or_routine'
            ? field.querySelector('select').value
            : question.category,
        destination:
          question.id === 'communication' ? field.querySelector('select').value : 'personal_hq',
        preference:
          question.id === 'communication'
            ? field.querySelector('#interview-preference-communication').value
            : ''
      };
    });
  }

  function reviewAnswers() {
    try {
      prepared = reviewedInterviewRows(readAnswers(), snapshot.profile);
      selected.replaceChildren();
      if (!prepared.length) {
        selected.append(
          node('li', 'All three answers skipped. No fact or preference will be saved.')
        );
      }
      for (const row of prepared) {
        const line = node('li');
        const quoted = node('strong', `“${row.text}”`);
        line.append(quoted, node('span', ` — ${safeSelectedDescription(row, snapshot.profile)}`));
        selected.append(line);
      }
      final.hidden = false;
      message('Review the exact wording and destination. Nothing has been saved yet.');
      final.querySelector('h3')?.focus();
    } catch (error) {
      message(error.message, true);
      const first = questions.querySelector('textarea');
      first?.focus();
    }
  }

  async function load() {
    shell.setAttribute('aria-busy', 'true');
    try {
      snapshot = await api(INTERVIEW_API);
      build.hidden = true;
      start.hidden = snapshot.status === 'completed';
      defer.hidden = snapshot.status === 'completed';
      if (snapshot.status === 'completed') {
        message('Interview complete. Review or change saved facts below.');
      } else {
        message(
          snapshot.status === 'deferred'
            ? 'Deferred. Come back when you are ready; nothing was saved by deferring.'
            : 'Optional. Start when ready; your answers stay on this page until you explicitly save.'
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

  start.addEventListener('click', () => {
    if (!snapshot?.questions || busy) return;
    if (!questions.childElementCount) snapshot.questions.forEach(setQuestionRow);
    form.hidden = false;
    start.hidden = true;
    defer.hidden = false;
    message('Answer any questions you like. Skip the rest, then review before saving.');
    questions.querySelector('textarea')?.focus();
  });
  defer.addEventListener('click', async () => {
    if (busy) return;
    busy = true;
    try {
      await api(`${INTERVIEW_API}/defer`, { method: 'POST' });
      form.hidden = true;
      final.hidden = true;
      start.hidden = false;
      message('Deferred. Any unsaved answer drafts remain only in this page, not Ori memory.');
      start.focus();
    } catch (error) {
      message(error.message, true);
    } finally {
      busy = false;
    }
  });
  review.addEventListener('click', reviewAnswers);
  back.addEventListener('click', () => {
    final.hidden = true;
    questions.querySelector('textarea')?.focus();
  });
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (busy || final.hidden || !prepared) return;
    const serialized = JSON.stringify(prepared);
    if (retry?.serialized !== serialized || retry?.version !== snapshot.state_version) {
      try {
        retry = { id: newRetryID(), serialized, version: snapshot.state_version };
      } catch (error) {
        message(error.message, true);
        return;
      }
    }
    busy = true;
    save.disabled = true;
    message('Saving the reviewed rows to their canonical destinations…');
    try {
      const data = await api(`${INTERVIEW_API}/save`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          state_version: snapshot.state_version,
          request_id: retry.id,
          reset_partial: resetPartial,
          rows: prepared
        })
      });
      if (data.interview?.status !== 'completed')
        throw new Error('Interview completion was not verified.');
      form.hidden = true;
      final.hidden = true;
      start.hidden = true;
      defer.hidden = true;
      message(
        prepared.length
          ? `Saved ${prepared.length} reviewed fact(s). Review them in your dossier below.`
          : 'Interview finished with all questions skipped. Nothing was saved.'
      );
      document.dispatchEvent(new CustomEvent('personal-assistant-knowledge-changed'));
      await load();
    } catch (error) {
      if (error.partial?.saved_rows?.length) {
        message(
          `Saved ${error.partial.saved_rows.join(', ')}. The remaining row was not saved. Refresh its destination, review again, and choose Save these facts.`,
          true
        );
        resetPartial = true;
        retry = undefined;
        final.hidden = true;
        try {
          snapshot = await api(INTERVIEW_API);
        } catch {
          // Keep the previous snapshot and drafts visible if the refresh fails.
        }
        review.focus();
      } else {
        message(error.message, true);
      }
    } finally {
      busy = false;
      save.disabled = false;
    }
  });
  void load();
  return { load };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => mountPersonalAssistantInterview());
  } else {
    mountPersonalAssistantInterview();
  }
}
