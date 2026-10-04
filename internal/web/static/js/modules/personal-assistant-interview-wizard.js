import { reviewTextValid } from './personal-hq-knowledge.js';

const INTERVIEW_API = '/api/personal-assistant/knowledge/interview';
const MODAL_ID = 'personalHQInterviewWizard';
const REVIEW_STEP = 'review';
const MAX_ANSWER_BYTES = 500;
const encoder = new TextEncoder();

export const ANSWER_COUNTER_THRESHOLD = 400;
export const DRAFT_KEY = 'ori.personalHQInterview.draft.v1';

const STEP_NAMES = {
  priority: 'Priority',
  communication: 'Working style',
  person_or_routine: 'Person or routine'
};
const CATEGORY_LABELS = {
  projects: 'Projects',
  how_you_work: 'How you work',
  routines: 'Routines',
  people: 'People'
};
const PREFERENCE_LABELS = {
  response_style: 'Response style',
  units: 'Units',
  language: 'Language'
};

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

// Returns null when the answer can be kept (an empty answer counts as skipped),
// otherwise one plain-language reason. It never changes the text.
export function interviewAnswerError(text) {
  if (text === '' || reviewTextValid(text)) return null;
  if (encoder.encode(text).length > MAX_ANSWER_BYTES)
    return 'That answer is too long. Please shorten it.';
  if (text.trim() !== text || text !== text.split(/\s+/u).join(' '))
    return 'Use one line with single spaces, and nothing extra at the start or end.';
  return 'This answer has characters Ori cannot save. Use plain text on one line.';
}

// Shows "N / 500" only once the answer is close to the limit.
export function answerCounterText(text) {
  const bytes = encoder.encode(text).length;
  return bytes > ANSWER_COUNTER_THRESHOLD ? `${bytes} / ${MAX_ANSWER_BYTES}` : '';
}

export function destinationLabel(answer, profile) {
  if (answer.destination === 'profile') {
    const current = profile?.preferences?.[answer.preference] || 'not set';
    const field = PREFERENCE_LABELS[answer.preference] || answer.preference;
    return `Your global profile · ${field} (currently: ${current})`;
  }
  return `Personal HQ · ${CATEGORY_LABELS[answer.category] || answer.category}`;
}

export function stepName(question, index) {
  return STEP_NAMES[question.id] || `Question ${index + 1}`;
}

// Drafts live in sessionStorage only. Storage can be missing or throw, so every
// call is guarded and a failure just means there is no draft.
export function browserStorage() {
  try {
    return globalThis.sessionStorage ?? null;
  } catch {
    return null;
  }
}

export function loadInterviewDraft(storage) {
  try {
    const raw = storage?.getItem(DRAFT_KEY);
    if (!raw) return null;
    const draft = JSON.parse(raw);
    return draft && Array.isArray(draft.answers) ? draft : null;
  } catch {
    return null;
  }
}

export function saveInterviewDraft(storage, draft) {
  try {
    storage.setItem(DRAFT_KEY, JSON.stringify(draft));
    return true;
  } catch {
    return false;
  }
}

export function clearInterviewDraft(storage) {
  try {
    storage?.removeItem(DRAFT_KEY);
  } catch {
    // Nothing to clear if storage is unavailable.
  }
}

export function hasInterviewDraft(storage) {
  const draft = loadInterviewDraft(storage);
  return Boolean(draft?.answers.some(answer => typeof answer?.text === 'string' && answer.text));
}

// Pure wizard state: one step per question in server order, then Review.
export function createInterviewWizardState(questions, draft) {
  const list = Array.isArray(questions) ? questions : [];
  const answers = list.map(question => ({
    id: question.id,
    text: '',
    destination: 'personal_hq',
    preference: question.id === 'communication' ? 'response_style' : '',
    category: question.category
  }));
  let index = 0;
  const reviewIndex = list.length;

  for (const saved of Array.isArray(draft?.answers) ? draft.answers : []) {
    const answer = answers.find(item => item.id === saved?.id);
    if (!answer) continue;
    if (typeof saved.text === 'string') answer.text = saved.text;
    if (answer.id === 'communication') {
      if (saved.destination === 'profile' || saved.destination === 'personal_hq')
        answer.destination = saved.destination;
      if (saved.preference in PREFERENCE_LABELS) answer.preference = saved.preference;
    }
    if (answer.id === 'person_or_routine' && ['routines', 'people'].includes(saved.category))
      answer.category = saved.category;
  }
  if (Number.isInteger(draft?.stepIndex))
    index = Math.min(Math.max(draft.stepIndex, 0), reviewIndex);

  return {
    questions: list,
    get index() {
      return index;
    },
    get reviewIndex() {
      return reviewIndex;
    },
    isReview: () => index === reviewIndex,
    next() {
      if (index < reviewIndex) index += 1;
      return index;
    },
    back() {
      if (index > 0) index -= 1;
      return index;
    },
    goTo(questionId) {
      if (questionId === REVIEW_STEP) {
        index = reviewIndex;
        return index;
      }
      const found = list.findIndex(question => question.id === questionId);
      if (found >= 0) index = found;
      return index;
    },
    answer(questionId) {
      return answers.find(answer => answer.id === questionId);
    },
    currentAnswer: () => answers[index],
    setAnswer(questionId, patch) {
      const answer = answers.find(item => item.id === questionId);
      if (answer) Object.assign(answer, patch);
      return answer;
    },
    toAnswers: () => answers.map(answer => ({ ...answer })),
    toDraft: () => ({ answers: answers.map(answer => ({ ...answer })), stepIndex: index })
  };
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

function radioGroup({ name, legend, options, value, onChange }) {
  const set = node('fieldset', undefined, 'interview-wizard-radios');
  set.append(node('legend', legend));
  for (const option of options) {
    const row = node('div', undefined, 'form-check');
    const input = node('input', undefined, 'form-check-input');
    input.type = 'radio';
    input.name = name;
    input.id = `${name}-${option.value}`;
    input.value = option.value;
    input.checked = option.value === value;
    input.disabled = Boolean(option.disabled);
    input.addEventListener('change', () => {
      if (input.checked) onChange(option.value);
    });
    const label = node('label', option.label, 'form-check-label');
    label.htmlFor = input.id;
    row.append(input, label);
    if (option.note) {
      const note = node('small', option.note, 'interview-wizard-radio-note');
      note.id = `${input.id}-note`;
      input.setAttribute('aria-describedby', note.id);
      row.append(note);
    }
    set.append(row);
  }
  return set;
}

function buildModal() {
  const modal = node('div', undefined, 'modal fade interview-wizard');
  modal.id = MODAL_ID;
  modal.tabIndex = -1;
  modal.setAttribute('aria-labelledby', `${MODAL_ID}Title`);
  modal.setAttribute('aria-hidden', 'true');

  const dialog = node('div', undefined, 'modal-dialog modal-dialog-centered');
  const content = node('div', undefined, 'modal-content interview-wizard-content');

  const header = node('div', undefined, 'modal-header');
  const title = node('h2', 'Tell your assistant what matters', 'modal-title');
  title.id = `${MODAL_ID}Title`;
  const close = node('button', undefined, 'btn-close');
  close.type = 'button';
  close.dataset.bsDismiss = 'modal';
  close.setAttribute('aria-label', 'Close');
  header.append(title, close);

  const progress = node('nav', undefined, 'interview-wizard-progress');
  progress.setAttribute('aria-label', 'Interview steps');
  const steps = node('ol', undefined, 'setup-wizard-steps interview-wizard-steps');
  progress.append(steps);

  const body = node('div', undefined, 'modal-body interview-wizard-body');
  const status = node('p', undefined, 'interview-wizard-status');
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  const stage = node('div', undefined, 'interview-wizard-stage');
  body.append(status, stage);

  const footer = node('div', undefined, 'modal-footer interview-wizard-footer');
  const note = node('p', 'Nothing is saved until you press Save.', 'interview-wizard-note');
  const actions = node('div', undefined, 'interview-wizard-actions');
  const back = node('button', 'Back', 'modern-btn modern-btn-secondary');
  const skip = node('button', 'Skip', 'modern-btn modern-btn-secondary');
  const next = node('button', 'Next', 'modern-btn modern-btn-primary');
  for (const button of [back, skip, next]) button.type = 'button';
  actions.append(back, skip, next);
  footer.append(note, actions);

  content.append(header, progress, body, footer);
  dialog.append(content);
  modal.append(dialog);
  return { modal, steps, status, stage, footer, back, skip, next };
}

// Opens the wizard. Resolves once the modal has closed and its DOM is gone.
export function openInterviewWizard(initialSnapshot) {
  document.getElementById(MODAL_ID)?.remove();
  const opener = document.activeElement;
  const storage = browserStorage();
  let snapshot = initialSnapshot;
  const draft = loadInterviewDraft(storage);
  const state = createInterviewWizardState(snapshot.questions, draft);
  const ui = buildModal();
  let prepared;
  let retry;
  let resetPartial = false;
  let savedRowIds = null; // set after a partial save; null otherwise
  let busy = false;

  function persist() {
    saveInterviewDraft(storage, state.toDraft());
  }

  function message(text, warning = false) {
    ui.status.textContent = text;
    ui.status.classList.toggle('is-warning', warning);
  }

  function renderStepper() {
    ui.steps.replaceChildren();
    const names = [...state.questions.map((question, i) => stepName(question, i)), 'Review'];
    names.forEach((name, i) => {
      const item = node('li', undefined, 'setup-wizard-step');
      if (i === state.index) {
        item.classList.add('setup-wizard-step-active');
        item.setAttribute('aria-current', 'step');
      } else if (i < state.index) {
        item.classList.add('setup-wizard-step-complete');
      }
      item.append(node('span', String(i + 1), 'setup-wizard-step-mark'), node('span', name));
      ui.steps.append(item);
    });
  }

  function renderQuestion(question) {
    const answer = state.answer(question.id);
    const heading = node('h3', question.prompt, 'interview-wizard-prompt');
    heading.id = `interview-wizard-prompt-${question.id}`;
    heading.tabIndex = -1;
    ui.stage.append(heading);
    if (question.hint) ui.stage.append(node('p', question.hint, 'interview-wizard-hint'));

    const input = node('textarea', undefined, 'form-control interview-wizard-input');
    input.id = `interview-answer-${question.id}`;
    input.rows = 2;
    input.value = answer.text;
    input.setAttribute('aria-labelledby', heading.id);
    const error = node('p', undefined, 'interview-wizard-error');
    error.id = `interview-answer-error-${question.id}`;
    error.setAttribute('role', 'alert');
    const counter = node('small', answerCounterText(answer.text), 'interview-wizard-counter');
    counter.id = `interview-answer-counter-${question.id}`;
    input.setAttribute('aria-describedby', `${error.id} ${counter.id}`);
    ui.stage.append(input, error, counter);

    const options = node('div', undefined, 'interview-wizard-options');
    options.hidden = answer.text.trim() === '';
    if (question.id === 'communication') {
      const hasProfile = Boolean(snapshot?.profile?.updated_at);
      const about = radioGroup({
        name: 'interview-preference',
        legend: 'What is it about?',
        value: answer.preference,
        options: [
          { value: 'response_style', label: PREFERENCE_LABELS.response_style },
          { value: 'units', label: PREFERENCE_LABELS.units },
          { value: 'language', label: PREFERENCE_LABELS.language }
        ],
        onChange: value => {
          state.setAnswer(question.id, { preference: value });
          persist();
        }
      });
      about.hidden = answer.destination !== 'profile';
      options.append(
        radioGroup({
          name: 'interview-destination',
          legend: 'Where should this apply?',
          value: answer.destination,
          options: [
            { value: 'personal_hq', label: 'Just my assistant' },
            {
              value: 'profile',
              label: 'Every workspace',
              disabled: !hasProfile,
              note: hasProfile ? '' : 'Save your profile first to use this.'
            }
          ],
          onChange: value => {
            state.setAnswer(question.id, { destination: value });
            about.hidden = value !== 'profile';
            persist();
          }
        }),
        about
      );
    }
    if (question.id === 'person_or_routine') {
      options.append(
        radioGroup({
          name: 'interview-category',
          legend: 'This is a',
          value: answer.category,
          options: [
            { value: 'routines', label: 'Routine' },
            { value: 'people', label: 'Person' }
          ],
          onChange: value => {
            state.setAnswer(question.id, { category: value });
            persist();
          }
        })
      );
    }
    if (options.childElementCount) ui.stage.append(options);

    input.addEventListener('input', () => {
      state.setAnswer(question.id, { text: input.value });
      error.textContent = '';
      input.removeAttribute('aria-invalid');
      counter.textContent = answerCounterText(input.value);
      options.hidden = input.value.trim() === '';
      persist();
    });
    input.addEventListener('keydown', event => {
      if (event.key !== 'Enter' || event.isComposing) return;
      event.preventDefault(); // answers are a single line
      void goNext();
    });
  }

  function rowSummary(question, index, row, answer) {
    const item = node('li', undefined, 'interview-wizard-review-row');
    const head = node('div', undefined, 'interview-wizard-review-head');
    head.append(node('span', stepName(question, index), 'interview-wizard-review-step'));
    if (savedRowIds && row) {
      head.append(
        node(
          'span',
          savedRowIds.includes(row.row_id) ? 'Saved' : 'Not saved',
          'interview-wizard-review-badge'
        )
      );
    }
    const edit = node('button', 'Edit', 'modern-btn modern-btn-secondary');
    edit.type = 'button';
    edit.setAttribute('aria-label', `Edit ${stepName(question, index)}`);
    edit.addEventListener('click', () => {
      if (busy) return;
      savedRowIds = null;
      message('');
      state.goTo(question.id);
      persist();
      render();
    });
    head.append(edit);
    item.append(head);
    if (row) {
      item.append(
        node('strong', `“${row.text}”`, 'interview-wizard-review-answer'),
        node('span', destinationLabel(answer, snapshot.profile), 'interview-wizard-review-where')
      );
    } else {
      item.append(node('em', 'Skipped', 'interview-wizard-review-skipped'));
    }
    return item;
  }

  function renderReview() {
    const heading = node('h3', 'Review exactly what will be saved', 'interview-wizard-prompt');
    heading.id = 'interview-wizard-review-title';
    heading.tabIndex = -1;
    ui.stage.append(heading);
    const answers = state.toAnswers();
    try {
      prepared = reviewedInterviewRows(answers, snapshot.profile);
    } catch (error) {
      prepared = undefined;
      const invalid = answers.find(answer => interviewAnswerError(answer.text));
      state.goTo(invalid?.id ?? state.questions[0]?.id);
      message(interviewAnswerError(invalid?.text ?? '') || error.message, true);
      render();
      return;
    }
    if (!prepared.length) {
      ui.stage.append(node('p', 'All three answers skipped. Nothing will be saved.'));
    }
    const list = node('ul', undefined, 'interview-wizard-review');
    state.questions.forEach((question, index) => {
      const row = prepared.find(item => item.row_id === question.id);
      list.append(rowSummary(question, index, row, answers[index]));
    });
    ui.stage.append(list);
    if (!savedRowIds)
      message('Review the exact wording and destination. Nothing has been saved yet.');
  }

  function renderButtons() {
    const review = state.isReview();
    ui.back.textContent = state.index === 0 ? 'Not now' : 'Back';
    ui.back.disabled = busy;
    ui.skip.hidden = review;
    ui.next.disabled = busy;
    if (review) {
      const count = prepared?.length ?? 0;
      ui.next.textContent = savedRowIds
        ? 'Retry'
        : !count
          ? 'Finish without saving'
          : count === 1
            ? 'Save 1 fact'
            : `Save ${count} facts`;
    } else {
      ui.next.textContent = 'Next';
    }
  }

  function focusStep() {
    const target = state.isReview()
      ? ui.stage.querySelector('h3')
      : ui.stage.querySelector('textarea');
    target?.focus();
  }

  function render() {
    ui.stage.replaceChildren();
    renderStepper();
    if (state.isReview()) renderReview();
    else renderQuestion(state.questions[state.index]);
    renderButtons();
    focusStep();
  }

  function close() {
    bootstrap.Modal.getInstance(ui.modal)?.hide();
  }

  async function submit() {
    if (busy || !prepared) return;
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
    renderButtons();
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
      clearInterviewDraft(storage);
      showDone(prepared.length);
      document.dispatchEvent(new CustomEvent('personal-assistant-knowledge-changed'));
    } catch (error) {
      if (error.partial?.saved_rows?.length) {
        resetPartial = true;
        retry = undefined;
        try {
          snapshot = await api(INTERVIEW_API);
        } catch {
          // Keep the previous snapshot and answers visible if the refresh fails.
        }
        savedRowIds = error.partial.saved_rows;
        render();
        message(
          `Saved ${savedRowIds.length} of ${prepared?.length ?? savedRowIds.length}. The rest were not saved. Press Retry to try them again.`,
          true
        );
      } else {
        message(error.message, true);
      }
    } finally {
      busy = false;
      if (ui.modal.isConnected && !ui.footer.hidden) renderButtons();
    }
  }

  function showDone(count) {
    ui.footer.hidden = true;
    ui.steps.replaceChildren();
    ui.stage.replaceChildren();
    const text = count
      ? `Saved ${count} ${count === 1 ? 'fact' : 'facts'}.`
      : 'Finished. Nothing was saved.';
    const heading = node('h3', text, 'interview-wizard-prompt');
    heading.tabIndex = -1;
    const done = node('button', 'Done', 'modern-btn modern-btn-primary');
    done.type = 'button';
    done.dataset.bsDismiss = 'modal';
    ui.stage.append(heading, done);
    message(text);
    heading.focus();
  }

  function showAnswerError(text) {
    const input = ui.stage.querySelector('textarea');
    ui.stage.querySelector('.interview-wizard-error').textContent = text;
    input.setAttribute('aria-invalid', 'true');
    input.focus();
  }

  async function goNext() {
    if (busy) return;
    if (state.isReview()) {
      await submit();
      return;
    }
    const question = state.questions[state.index];
    const problem = interviewAnswerError(state.answer(question.id).text);
    if (problem) {
      showAnswerError(problem);
      return;
    }
    message('');
    state.next();
    persist();
    render();
  }

  async function notNow() {
    busy = true;
    renderButtons();
    try {
      await api(`${INTERVIEW_API}/defer`, { method: 'POST' });
      close();
    } catch (error) {
      message(error.message, true);
    } finally {
      busy = false;
      if (ui.modal.isConnected) renderButtons();
    }
  }

  ui.next.addEventListener('click', () => void goNext());
  ui.back.addEventListener('click', () => {
    if (busy) return;
    if (state.index === 0) {
      void notNow();
      return;
    }
    savedRowIds = null;
    message('');
    state.back();
    persist();
    render();
  });
  ui.skip.addEventListener('click', () => {
    if (busy || state.isReview()) return;
    const question = state.questions[state.index];
    state.setAnswer(question.id, { text: '' });
    state.next();
    persist();
    message('Skipped. Nothing from that question will be saved.');
    render();
  });

  return new Promise(resolve => {
    document.body.append(ui.modal);
    ui.modal.addEventListener('shown.bs.modal', focusStep);
    ui.modal.addEventListener('hidden.bs.modal', () => {
      bootstrap.Modal.getInstance(ui.modal)?.dispose();
      ui.modal.remove();
      if (opener?.isConnected && !opener.hidden) opener.focus();
      document.dispatchEvent(new CustomEvent('personal-assistant-interview-closed'));
      resolve();
    });
    render();
    if (hasInterviewDraft(storage)) message('Restored your earlier answers.');
    bootstrap.Modal.getOrCreateInstance(ui.modal).show();
  });
}
