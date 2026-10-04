import { reviewTextValid } from './personal-hq-knowledge.js';
import { folderChooserView } from './personal-assistant-folder-chooser.js';

const INTERVIEW_API = '/api/personal-assistant/knowledge/interview';
const FOLDER_DIGEST_API = '/api/personal-assistant/folder-digest';
// The one question a folder can answer: what the user is working on.
const FOLDER_QUESTION_ID = 'priority';
const MAX_ALTERNATES = 3;
const FOLDER_CHOOSER_LABEL = 'Show me instead';
const FOLDER_PICK_LABEL = 'Pick a folder…';
const FOLDER_CHOOSER_NOTE =
  'I only look at file names and types. This also leaves a setup suggestion for the folder on Home.';
const FOLDER_SCAN_CAPTION = "From the folder you showed me. Edit it if that's not quite right.";
const FOLDER_EARLIER_CAPTION =
  "From a folder you showed me earlier. Edit it if that's not quite right.";
const EARLIER_SOURCE = 'earlier';
const FOLDER_SCAN_FAILED = 'I could not look at that folder. Type your answer instead.';
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

// A suggestion is wording a folder proposed for question 1. Only its text, the
// folder's name and (one level of) alternates are kept, whether it came from
// the server or from a stored draft; anything without a text is no suggestion.
export function cleanSuggestion(value, nested = false) {
  if (!value || typeof value.text !== 'string' || value.text === '') return null;
  const clean = { text: value.text, folder: typeof value.folder === 'string' ? value.folder : '' };
  // Set by the wizard, never by the server: the folder was shown before this
  // interview was opened, so the caption says "earlier".
  if (!nested && value.source === EARLIER_SOURCE) clean.source = EARLIER_SOURCE;
  if (!nested && Array.isArray(value.alternates)) {
    const alternates = value.alternates
      .map(item => cleanSuggestion(item, true))
      .filter(Boolean)
      .slice(0, MAX_ALTERNATES);
    if (alternates.length) clean.alternates = alternates;
  }
  return clean;
}

// The caption under the box shows only while the text is exactly the stored
// suggestion. Once the text is edited the answer is the user's own.
export function suggestionCaptionVisible(answer) {
  return typeof answer?.suggestion?.text === 'string' && answer.suggestion.text === answer.text;
}

// Says where a suggestion came from: a folder shown just now in the wizard, or
// one shown on Home before the interview was opened.
export function suggestionCaptionText(suggestion) {
  return suggestion?.source === EARLIER_SOURCE ? FOLDER_EARLIER_CAPTION : FOLDER_SCAN_CAPTION;
}

// The line added to question 1's hint when a project is already remembered
// from a folder. Empty when there is nothing remembered.
export function rememberedProjectLine(text) {
  if (typeof text !== 'string') return '';
  const fact = text.trim().replace(/\.+$/u, '');
  if (!fact) return '';
  return `I already remember: “${fact}”. Add anything that matters more right now, or skip.`;
}

// Returns the answer a new suggestion leaves behind. It never overwrites what
// the user typed: an empty box, or one still holding an unedited suggestion,
// takes the new text; anything else keeps its text and holds the suggestion as
// `pending` for the user to choose.
export function applySuggestion(answer, suggestion) {
  const clean = cleanSuggestion(suggestion);
  if (!clean) return { ...answer };
  if (answer.text.trim() === '' || suggestionCaptionVisible(answer))
    return { ...answer, text: clean.text, suggestion: clean, pending: null };
  return { ...answer, pending: clean };
}

// The user chose the pending suggestion over what they typed.
export function usePendingSuggestion(answer) {
  if (!answer.pending) return { ...answer };
  return { ...answer, text: answer.pending.text, suggestion: answer.pending, pending: null };
}

// Promotes one alternate to the suggestion, keeping the rest (and the one it
// replaces) as alternates so the user can still change their mind.
export function alternateSuggestion(suggestion, index) {
  const alternates = suggestion?.alternates || [];
  const chosen = alternates[index];
  if (!chosen) return cleanSuggestion(suggestion);
  return cleanSuggestion({
    text: chosen.text,
    folder: chosen.folder,
    source: suggestion.source,
    alternates: [
      { text: suggestion.text, folder: suggestion.folder },
      ...alternates.filter((_, i) => i !== index)
    ]
  });
}

// Pure wizard state: one step per question in server order, then Review.
// `snapshot` may carry a suggestion from a folder shown before the interview.
export function createInterviewWizardState(questions, draft, snapshot) {
  const list = Array.isArray(questions) ? questions : [];
  const answers = list.map(question => ({
    id: question.id,
    text: '',
    destination: 'personal_hq',
    preference: question.id === 'communication' ? 'response_style' : '',
    category: question.category,
    // Only question 1 can be answered from a folder.
    ...(question.id === FOLDER_QUESTION_ID ? { suggestion: null, pending: null } : {})
  }));
  let index = 0;
  const reviewIndex = list.length;

  for (const saved of Array.isArray(draft?.answers) ? draft.answers : []) {
    const answer = answers.find(item => item.id === saved?.id);
    if (!answer) continue;
    if (typeof saved.text === 'string') answer.text = saved.text;
    if (answer.id === FOLDER_QUESTION_ID) {
      answer.suggestion = cleanSuggestion(saved.suggestion);
      answer.pending = cleanSuggestion(saved.pending);
    }
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

  // A folder shown earlier prefills question 1 on a fresh open only. A draft,
  // even one whose answer was cleared, is the user's own and always wins.
  const earlier = draft ? null : cleanSuggestion(snapshot?.suggestion);
  const folderAnswer = answers.find(answer => answer.id === FOLDER_QUESTION_ID);
  if (earlier && folderAnswer) {
    earlier.source = EARLIER_SOURCE;
    // With a project already remembered the box stays empty, and a different
    // folder's suggestion waits as a choice instead of filling it.
    if (rememberedProjectLine(snapshot?.remembered_project)) folderAnswer.pending = earlier;
    else Object.assign(folderAnswer, applySuggestion(folderAnswer, earlier));
  }

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

// Loads what the folder chooser may offer. A failed read means no chooser: the
// question then looks exactly as it does without this shortcut.
async function loadFolderChooser() {
  try {
    const response = await fetch(FOLDER_DIGEST_API, {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) return null;
    const data = await response.json();
    return folderChooserView(data?.folder_digest);
  } catch {
    return null;
  }
}

// Asks the server to look at a folder and word an answer from it. The body is a
// chip id or a picker flag, never a path. It never throws: the result says what
// happened, so a failure changes nothing in the wizard.
async function requestFolderSuggestion(body) {
  try {
    const response = await fetch(`${INTERVIEW_API}/suggest`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) return { error: data.error || data.message || FOLDER_SCAN_FAILED };
    return data;
  } catch {
    return { error: FOLDER_SCAN_FAILED };
  }
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
  const state = createInterviewWizardState(snapshot.questions, draft, snapshot);
  const ui = buildModal();
  let prepared;
  let retry;
  let resetPartial = false;
  let savedRowIds = null; // set after a partial save; null otherwise
  let busy = false;
  let folderChooser; // undefined until loaded; null when nothing can be chosen
  let folderChooserLoad; // the one read of the chooser, shared by every render

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

  function setChooserDisabled(disabled) {
    for (const button of ui.stage.querySelectorAll('.interview-wizard-chip'))
      button.disabled = disabled;
  }

  // Puts a changed question 1 answer in place, redraws the step (which moves
  // focus to the answer box) and announces what happened in the status line.
  function setFolderAnswer(answer, announcement) {
    state.setAnswer(FOLDER_QUESTION_ID, answer);
    persist();
    if (state.questions[state.index]?.id === FOLDER_QUESTION_ID) render();
    message(announcement);
  }

  // Offers a suggestion to question 1. Typed text is never replaced: the
  // suggestion then waits under the box as a button.
  function offerSuggestion(suggestion) {
    const answer = applySuggestion(state.answer(FOLDER_QUESTION_ID), suggestion);
    setFolderAnswer(
      answer,
      answer.pending
        ? `I found “${answer.pending.text}”. I kept what you typed; use the button under the box to switch.`
        : `Filled in “${answer.text}”.`
    );
  }

  // A scan records an offer and may set another aside, so Home's folder card
  // must redraw from the server. It only exists on Home; elsewhere this is a
  // no-op.
  function reloadHomeFolderCard() {
    try {
      void Promise.resolve(window.PersonalAssistantFolder?.reload?.()).catch(() => {});
    } catch {
      // Home's card is optional here.
    }
  }

  // Looks at one folder and, when it names a project, offers it as the answer.
  // `waiting` is the status line while the scan or the native dialog is open;
  // `trigger` is the button that was pressed.
  async function showFolder(body, waiting, trigger) {
    if (busy) return; // a second click while a scan runs does nothing
    busy = true;
    renderButtons();
    message(waiting);
    const result = await requestFolderSuggestion(body);
    busy = false;
    // Before anything else: the wizard may have been closed while the scan or
    // the dialog was open, and Home would otherwise keep showing an offer the
    // scan has already set aside.
    if (!result.cancelled) reloadHomeFolderCard();
    if (!ui.modal.isConnected) return;
    renderButtons();
    const suggestion = result.error || result.cancelled ? null : cleanSuggestion(result.suggestion);
    if (suggestion) {
      offerSuggestion(suggestion); // redraws the step and focuses the answer box
      return;
    }
    if (result.error) message(result.error, true);
    else if (result.cancelled) message('');
    else message(typeof result.message === 'string' ? result.message : FOLDER_SCAN_FAILED);
    // Nothing was redrawn, and the pressed button lost focus while it was
    // disabled: put focus back where the user was.
    if (trigger?.isConnected) trigger.focus();
  }

  function folderButton(label, onClick) {
    const button = node('button', label, 'interview-wizard-chip');
    button.type = 'button';
    button.disabled = busy;
    button.addEventListener('click', onClick);
    return button;
  }

  function drawFolderChooser(group, view) {
    if (!view || !(view.chips.length || view.pickerVisible)) return;
    const label = node('p', FOLDER_CHOOSER_LABEL, 'interview-wizard-chooser-label');
    label.id = 'interview-wizard-chooser-label';
    group.setAttribute('aria-labelledby', label.id);
    const chips = node('div', undefined, 'interview-wizard-chips');
    for (const chip of view.chips) {
      const button = folderButton(
        chip.label,
        () => void showFolder({ chip: chip.id }, `Looking at ${chip.label}…`, button)
      );
      button.dataset.chip = chip.id;
      chips.append(button);
    }
    if (view.pickerVisible) {
      const button = folderButton(
        FOLDER_PICK_LABEL,
        () => void showFolder({ picker: true }, 'Choose a folder in the dialog…', button)
      );
      button.dataset.picker = 'folder';
      chips.append(button);
    }
    if (view.filePickerVisible) {
      const button = folderButton(
        view.filePickerLabel,
        () => void showFolder({ file: true }, 'Choose a file in the dialog…', button)
      );
      button.dataset.picker = 'file';
      chips.append(button);
    }
    group.append(label, chips);
    // The server's note explains a missing dialog (a sandboxed session has none).
    if (view.note) group.append(node('p', view.note, 'interview-wizard-chooser-note'));
    // What a scan does is part of the group's description, so it is read with it.
    const note = node('p', FOLDER_CHOOSER_NOTE, 'interview-wizard-chooser-note');
    note.id = 'interview-wizard-chooser-note';
    group.setAttribute('aria-describedby', note.id);
    group.append(note);
    group.hidden = false;
  }

  // Under the answer box: where a filled-in answer came from, the folder's
  // other projects, and a suggestion that is waiting because the user had
  // already typed. Returns the caption's id when it shows, for the box to cite.
  function drawSuggestionExtras(extras, answer) {
    extras.replaceChildren();
    let captionId = '';
    if (suggestionCaptionVisible(answer)) {
      const caption = node(
        'p',
        suggestionCaptionText(answer.suggestion),
        'interview-wizard-caption'
      );
      caption.id = 'interview-answer-caption';
      captionId = caption.id;
      extras.append(caption);
      const alternates = answer.suggestion.alternates || [];
      if (alternates.length) {
        const group = node('div', undefined, 'interview-wizard-alternates');
        group.setAttribute('role', 'group');
        const label = node('p', 'Also in this folder:', 'interview-wizard-chooser-label');
        label.id = 'interview-wizard-alternates-label';
        group.setAttribute('aria-labelledby', label.id);
        const chips = node('div', undefined, 'interview-wizard-chips');
        alternates.forEach((alternate, index) => {
          const button = folderButton(alternate.text, () => {
            if (busy) return;
            const current = state.answer(FOLDER_QUESTION_ID);
            const next = applySuggestion(current, alternateSuggestion(current.suggestion, index));
            setFolderAnswer(next, `Filled in “${next.text}”.`);
          });
          button.dataset.alternate = String(index);
          chips.append(button);
        });
        group.append(label, chips);
        extras.append(group);
      }
    }
    if (answer.pending) {
      extras.append(
        node(
          'p',
          answer.pending.source === EARLIER_SOURCE
            ? 'From a folder you showed me earlier:'
            : 'From the folder you showed me:',
          'interview-wizard-caption'
        )
      );
      const use = folderButton(`Use “${answer.pending.text}”`, () => {
        if (busy) return;
        const next = usePendingSuggestion(state.answer(FOLDER_QUESTION_ID));
        setFolderAnswer(next, `Filled in “${next.text}”.`);
      });
      use.classList.add('interview-wizard-use');
      extras.append(use);
    }
    extras.hidden = !extras.childElementCount;
    return captionId;
  }

  // The chooser sits above the answer box. It stays hidden, and the question
  // stays as it always was, when the folders cannot be read or none exists.
  function renderFolderChooser() {
    const group = node('div', undefined, 'interview-wizard-chooser');
    group.setAttribute('role', 'group');
    group.hidden = true;
    if (folderChooser !== undefined) {
      drawFolderChooser(group, folderChooser);
      return group;
    }
    folderChooserLoad ??= loadFolderChooser();
    void folderChooserLoad.then(view => {
      folderChooser = view;
      if (group.isConnected) drawFolderChooser(group, view);
    });
    return group;
  }

  function renderQuestion(question) {
    const answer = state.answer(question.id);
    const heading = node('h3', question.prompt, 'interview-wizard-prompt');
    heading.id = `interview-wizard-prompt-${question.id}`;
    heading.tabIndex = -1;
    ui.stage.append(heading);
    const described = [];
    if (question.hint) {
      // The hint carries the example answer, so it is read with the field.
      const hint = node('p', question.hint, 'interview-wizard-hint');
      hint.id = `interview-wizard-hint-${question.id}`;
      described.push(hint.id);
      ui.stage.append(hint);
    }
    if (question.id === FOLDER_QUESTION_ID) {
      const remembered = rememberedProjectLine(snapshot?.remembered_project);
      if (remembered) {
        // Read with the field, like the hint it extends.
        const line = node('p', remembered, 'interview-wizard-hint');
        line.id = 'interview-wizard-remembered';
        described.push(line.id);
        ui.stage.append(line);
      }
      ui.stage.append(renderFolderChooser());
    }

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
    ui.stage.append(input, error, counter);
    const extras =
      question.id === FOLDER_QUESTION_ID
        ? node('div', undefined, 'interview-wizard-suggestion')
        : null;
    // The caption is part of the box's description for as long as it shows.
    const describe = () => {
      const captionId = extras ? drawSuggestionExtras(extras, answer) : '';
      input.setAttribute(
        'aria-describedby',
        [...described, ...(captionId ? [captionId] : []), error.id, counter.id].join(' ')
      );
    };
    if (extras) ui.stage.append(extras);
    describe();

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
      if (answer.suggestion && !suggestionCaptionVisible(answer)) {
        // An edited answer is the user's own: it no longer carries the folder.
        state.setAnswer(question.id, { suggestion: null });
        describe();
      }
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
    // A scan can keep the wizard busy for as long as a native dialog is open,
    // so every control that does nothing meanwhile says so, the chooser included.
    ui.skip.disabled = busy;
    ui.next.disabled = busy;
    setChooserDisabled(busy);
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
    // A skipped question 1 also lets go of what a folder proposed for it.
    if (question.id === FOLDER_QUESTION_ID)
      state.setAnswer(question.id, { suggestion: null, pending: null });
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
