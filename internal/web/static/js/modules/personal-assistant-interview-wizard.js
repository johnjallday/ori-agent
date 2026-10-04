import { reviewTextValid } from './personal-hq-knowledge.js';

const INTERVIEW_API = '/api/personal-assistant/knowledge/interview';
const MODAL_ID = 'personalHQInterviewWizard';
const REVIEW_STEP = 'review';

const STEP_NAMES = {
  priority: 'Priority',
  communication: 'Working style',
  person_or_routine: 'Person or routine'
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

export function stepName(question, index) {
  return STEP_NAMES[question.id] || `Question ${index + 1}`;
}

// Pure wizard state: one step per question in server order, then Review.
export function createInterviewWizardState(questions) {
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
    toAnswers: () => answers.map(answer => ({ ...answer }))
  };
}

function describeRow(row, profile) {
  if (row.destination === 'profile') {
    return `Global profile · ${row.preference.replaceAll('_', ' ')} · currently ${profile?.preferences?.[row.preference] || 'not set'}`;
  }
  return `Personal HQ · ${row.category.replaceAll('_', ' ')}`;
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
  let snapshot = initialSnapshot;
  const state = createInterviewWizardState(snapshot.questions);
  const ui = buildModal();
  let prepared;
  let retry;
  let resetPartial = false;
  let busy = false;

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

  function select(id, label, options, value, onChange) {
    const wrap = node('div', undefined, 'interview-wizard-field');
    const labelEl = node('label', label);
    const control = node('select', undefined, 'form-select');
    control.id = id;
    labelEl.htmlFor = id;
    for (const [optionValue, title, disabled] of options) {
      const option = node('option', title);
      option.value = optionValue;
      option.disabled = Boolean(disabled);
      control.append(option);
    }
    control.value = value;
    control.addEventListener('change', () => onChange(control));
    wrap.append(labelEl, control);
    return wrap;
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
    input.maxLength = 500;
    input.value = answer.text;
    input.setAttribute('aria-labelledby', heading.id);
    input.addEventListener('input', () => state.setAnswer(question.id, { text: input.value }));
    input.addEventListener('keydown', event => {
      if (event.key !== 'Enter' || event.isComposing) return;
      event.preventDefault(); // answers are a single line
      void goNext();
    });
    ui.stage.append(input);

    if (question.id === 'communication') {
      const preference = select(
        'interview-preference-communication',
        'Global field',
        [
          ['response_style', 'Response style'],
          ['units', 'Units'],
          ['language', 'Language']
        ],
        answer.preference,
        control => state.setAnswer(question.id, { preference: control.value })
      );
      preference.hidden = answer.destination !== 'profile';
      const destination = select(
        'interview-destination-communication',
        'Save this as',
        [
          ['personal_hq', 'Personal HQ work-style fact'],
          [
            'profile',
            'Global communication preference (all workspaces)',
            !snapshot?.profile?.updated_at
          ]
        ],
        answer.destination,
        control => {
          state.setAnswer(question.id, { destination: control.value });
          preference.hidden = control.value !== 'profile';
        }
      );
      ui.stage.append(destination, preference);
    }
    if (question.id === 'person_or_routine') {
      ui.stage.append(
        select(
          'interview-person-category',
          'Which kind of fact?',
          [
            ['routines', 'Routine'],
            ['people', 'Person']
          ],
          answer.category,
          control => state.setAnswer(question.id, { category: control.value })
        )
      );
    }
  }

  function renderReview() {
    const heading = node('h3', 'Review exactly what will be saved', 'interview-wizard-prompt');
    heading.id = 'interview-wizard-review-title';
    heading.tabIndex = -1;
    ui.stage.append(heading);
    try {
      prepared = reviewedInterviewRows(state.toAnswers(), snapshot.profile);
    } catch (error) {
      prepared = undefined;
      state.goTo(state.questions[0]?.id);
      message(error.message, true);
      render();
      return;
    }
    const list = node('ul', undefined, 'interview-wizard-review');
    if (!prepared.length) {
      list.append(node('li', 'All three answers skipped. Nothing will be saved.'));
    }
    for (const row of prepared) {
      const line = node('li');
      line.append(
        node('strong', `“${row.text}”`),
        node('span', ` — ${describeRow(row, snapshot.profile)}`)
      );
      list.append(line);
    }
    ui.stage.append(list);
    message('Review the exact wording and destination. Nothing has been saved yet.');
  }

  function renderButtons() {
    const review = state.isReview();
    ui.back.disabled = state.index === 0 || busy;
    ui.skip.hidden = review;
    ui.next.disabled = busy;
    if (review) {
      const count = prepared?.length ?? 0;
      ui.next.textContent = !count
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
      showDone(prepared.length);
      document.dispatchEvent(new CustomEvent('personal-assistant-knowledge-changed'));
    } catch (error) {
      if (error.partial?.saved_rows?.length) {
        message(
          `Saved ${error.partial.saved_rows.join(', ')}. The remaining row was not saved. Refresh its destination, review again, and choose Save these facts.`,
          true
        );
        resetPartial = true;
        retry = undefined;
        try {
          snapshot = await api(INTERVIEW_API);
        } catch {
          // Keep the previous snapshot and answers visible if the refresh fails.
        }
        state.goTo(state.questions[0]?.id);
        render();
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

  async function goNext() {
    if (busy) return;
    if (state.isReview()) {
      await submit();
      return;
    }
    message('');
    state.next();
    render();
  }

  ui.next.addEventListener('click', () => void goNext());
  ui.back.addEventListener('click', () => {
    if (busy) return;
    message('');
    state.back();
    render();
  });
  ui.skip.addEventListener('click', () => {
    if (busy || state.isReview()) return;
    const question = state.questions[state.index];
    state.setAnswer(question.id, { text: '' });
    state.next();
    message('Skipped. Nothing from that question will be saved.');
    render();
  });

  return new Promise(resolve => {
    document.body.append(ui.modal);
    ui.modal.addEventListener('shown.bs.modal', focusStep);
    ui.modal.addEventListener('hidden.bs.modal', () => {
      bootstrap.Modal.getInstance(ui.modal)?.dispose();
      ui.modal.remove();
      resolve();
    });
    render();
    bootstrap.Modal.getOrCreateInstance(ui.modal).show();
  });
}
