/* global escapeHtml */

const escapeTaskHtml = window.escapeHtml || function fallbackEscapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
};

function formatRelativeDate(dateString) {
  if (!dateString) return '—';

  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return '—';

  const now = new Date();
  const diffMs = now - date;
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return date.toLocaleDateString();
}

function formatDateTime(dateString) {
  if (!dateString) return '—';
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString([], {
    dateStyle: 'medium',
    timeStyle: 'short'
  });
}

function getStatusClass(status) {
  const normalized = String(status || '').trim().toLowerCase();
  if (normalized === 'completed') return 'completed';
  if (normalized === 'in_progress') return 'in_progress';
  if (normalized === 'blocked') return 'blocked';
  if (normalized === 'failed' || normalized === 'timeout') return 'failed';
  return 'pending';
}

function getDisplayStatus(status) {
  const normalized = String(status || '').trim().toLowerCase();
  const labels = {
    pending: 'Pending',
    assigned: 'Assigned',
    in_progress: 'In Progress',
    completed: 'Completed',
    failed: 'Failed',
    blocked: 'Blocked',
    cancelled: 'Cancelled',
    timeout: 'Timed Out'
  };
  return labels[normalized] || 'Pending';
}

function summarizeText(value, maxLength = 220) {
  const normalized = String(value || '').replace(/\s+/g, ' ').trim();
  if (!normalized) return '';
  if (normalized.length <= maxLength) return normalized;

  const candidate = normalized.slice(0, maxLength - 1);
  const boundary = candidate.lastIndexOf(' ');
  const trimmed = boundary >= Math.floor(maxLength * 0.55)
    ? candidate.slice(0, boundary)
    : candidate;
  return `${trimmed.trim()}...`;
}

function normalizeResultText(value) {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value, null, 2);
  } catch (_error) {
    return String(value);
  }
}

const assistQuestionPromptPattern = /^\s*(?:[-*]\s*)?(?:\d+)[.)]\s*(.+?)\s*$/;
const assistLetteredOptionPattern = /^\s*(?:[-*]\s*)?([A-Z])[.)]\s*(.+)$/;

function cleanAssistText(value) {
  return String(value ?? '')
    .replace(/\[(.*?)\]\((.*?)\)/g, '$1')
    .replace(/[*_`#>]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

function cleanAssistFieldLabel(value) {
  return cleanAssistText(value).replace(/[,:;.!?]+$/g, '').trim();
}

function ensureAssistQuestion(value) {
  const cleaned = cleanAssistText(value).replace(/[.!]+$/g, '').trim();
  if (!cleaned) return '';
  return cleaned.endsWith('?') ? cleaned : `${cleaned}?`;
}

function buildAssistFieldId(value, index) {
  const token = cleanAssistFieldLabel(value)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
  return token || `field_${index + 1}`;
}

function splitAssistOptionEvidence(value) {
  const cleaned = cleanAssistText(value);
  if (!cleaned) {
    return { label: '', description: '' };
  }

  const match = cleaned.match(/^(.*)\(([^()]+)\)$/);
  if (!match) {
    return { label: cleaned, description: '' };
  }

  const label = cleanAssistText(match[1]);
  const description = cleanAssistText(match[2]);
  if (!label || !description) {
    return { label: cleaned, description: '' };
  }

  return {
    label,
    description: description.endsWith('.') || description.endsWith('!') || description.endsWith('?')
      ? description
      : `${description}.`
  };
}

function extractAssistInlineOptions(question) {
  const cleanedQuestion = cleanAssistFieldLabel(question);
  const matches = cleanedQuestion.match(/\sor\s/gi);
  if (!matches || matches.length !== 1) return [];

  const parts = cleanedQuestion.split(/\sor\s/i);
  if (parts.length !== 2) return [];

  const left = cleanAssistFieldLabel(parts[0]);
  const right = cleanAssistFieldLabel(parts[1]);
  if (!left || !right) return [];
  if (left.toLowerCase().includes('should i') || left.toLowerCase().includes('want me to')) return [];

  return [
    { value: left, label: left, description: '', key: 'A' },
    { value: right, label: right, description: '', key: 'B' }
  ];
}

function buildDerivedAssistField(question, index, options = []) {
  const prompt = ensureAssistQuestion(question);
  const label = cleanAssistFieldLabel(question);
  if (!prompt || !label) return null;

  const lower = label.toLowerCase();
  const field = {
    id: buildAssistFieldId(label, index),
    label,
    description: prompt,
    evidence: '',
    type: 'text',
    placeholder: '',
    required: false,
    options: []
  };

  if (options.length >= 2) {
    field.type = 'select';
    field.options = options.map((option, optionIndex) => ({
      value: cleanAssistText(option.value || option.label),
      label: cleanAssistText(option.label || option.value),
      description: cleanAssistText(option.description),
      key: String(option.key || String.fromCharCode(65 + (optionIndex % 26))).trim()
    })).filter((option) => option.value && option.label);
    return field.options.length >= 2 ? field : null;
  }

  const inlineOptions = extractAssistInlineOptions(prompt);
  if (inlineOptions.length >= 2) {
    field.type = 'select';
    field.options = inlineOptions;
    return field;
  }

  if (lower.includes('how many') && lower.includes('shelf')) {
    field.type = 'number';
    field.placeholder = '3';
    return field;
  }

  if (lower.includes('goal') || lower.includes('status') || lower.includes('level of detail')) {
    field.type = 'textarea';
    field.placeholder = lower.includes('goal')
      ? 'Describe the build goal and intended outcome'
      : lower.includes('status')
        ? 'Describe where you are now and what is already decided'
        : 'Explain how detailed the plan should be';
    return field;
  }

  if (lower.includes('room') || lower.includes('space')) {
    field.placeholder = 'Garage, office, pantry, living room';
  }

  return field;
}

function extractAssistQuestionBlocks(text) {
  const lines = String(text || '').split(/\r?\n/);
  const blocks = [];

  for (let index = 0; index < lines.length;) {
    const promptMatch = lines[index].match(assistQuestionPromptPattern);
    if (!promptMatch || promptMatch.length < 2) {
      index += 1;
      continue;
    }

    const question = ensureAssistQuestion(promptMatch[1]);
    if (!question) {
      index += 1;
      continue;
    }

    const options = [];
    let nextIndex = index + 1;

    for (; nextIndex < lines.length; nextIndex += 1) {
      const rawLine = lines[nextIndex];
      if (assistQuestionPromptPattern.test(rawLine)) {
        break;
      }

      const optionMatch = rawLine.match(assistLetteredOptionPattern);
      if (optionMatch && optionMatch.length >= 3) {
        const parsed = splitAssistOptionEvidence(optionMatch[2]);
        if (!parsed.label) continue;
        options.push({
          value: parsed.label,
          label: parsed.label,
          description: parsed.description,
          key: optionMatch[1]
        });
        continue;
      }

      const continuation = cleanAssistText(rawLine);
      if (!continuation || options.length === 0) {
        continue;
      }

      const lastOption = options[options.length - 1];
      const merged = splitAssistOptionEvidence(`${lastOption.label} ${continuation}`);
      lastOption.value = merged.label || lastOption.value;
      lastOption.label = merged.label || lastOption.label;
      lastOption.description = merged.description || lastOption.description;
    }

    if (options.length >= 2) {
      blocks.push({ question, options });
      index = nextIndex;
      continue;
    }

    index += 1;
  }

  return blocks;
}

function extractAssistQuestionsFromText(text) {
  const questions = [];
  const seen = new Set();
  const lines = String(text || '').split(/\r?\n/);

  const addQuestion = (value) => {
    const normalized = ensureAssistQuestion(value);
    if (!normalized) return;
    const key = normalized.toLowerCase();
    if (seen.has(key)) return;
    seen.add(key);
    questions.push(normalized);
  };

  lines.forEach((rawLine) => {
    const trimmed = String(rawLine || '').trim();
    if (!trimmed) return;

    const promptMatch = trimmed.match(assistQuestionPromptPattern);
    const candidate = promptMatch && promptMatch.length >= 2 ? promptMatch[1] : trimmed;
    if (!candidate.includes('?')) return;

    candidate.split('?').forEach((part) => {
      const fragment = cleanAssistText(part);
      if (fragment.length < 8 || fragment.length > 180) return;
      addQuestion(fragment);
    });
  });

  if (questions.length > 0) {
    return questions;
  }

  cleanAssistText(text).split('?').forEach((part) => {
    const fragment = cleanAssistText(part);
    if (fragment.length < 8 || fragment.length > 180) return;
    addQuestion(fragment);
  });

  return questions;
}

function deriveAssistWorkflowStepFromText(...texts) {
  const sources = texts.map((value) => String(value || '').trim()).filter(Boolean);
  if (sources.length === 0) return null;

  for (const source of sources) {
    const blocks = extractAssistQuestionBlocks(source);
    if (blocks.length > 0) {
      const fields = blocks
        .map((block, index) => buildDerivedAssistField(block.question, index, block.options))
        .filter(Boolean);
      if (fields.length === 0) {
        continue;
      }

      return {
        stepType: 'ask_form',
        title: 'Answer the questions',
        summary: 'Work through each question below, then continue the task.',
        freeTextAllowed: true,
        fields
      };
    }
  }

  const seen = new Set();
  const questions = [];
  sources.forEach((source) => {
    extractAssistQuestionsFromText(source).forEach((question) => {
      const key = question.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      questions.push(question);
    });
  });

  if (questions.length === 0) {
    return null;
  }

  const fields = questions
    .map((question, index) => buildDerivedAssistField(question, index))
    .filter(Boolean);

  if (fields.length === 0) {
    return null;
  }

  if (fields.length === 1 && fields[0].type === 'text') {
    return null;
  }

  return {
    stepType: 'ask_form',
    title: 'Answer the questions',
    summary: 'Work through each question below, then continue the task.',
    freeTextAllowed: true,
    fields
  };
}

function normalizeAssistOptionToken(value) {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

function isAssistOtherOption(option) {
  const normalized = normalizeAssistOptionToken(option?.label || option?.value || '');
  if (!normalized) return false;
  return normalized === 'other' ||
    normalized === 'something else' ||
    normalized === 'custom' ||
    normalized === 'another option' ||
    normalized === 'not listed' ||
    normalized.startsWith('other ') ||
    normalized.endsWith(' other');
}

function buildAssistSelectState(workflowStep, selectedFieldValues = {}) {
  const optionValues = {};
  const customValues = {};

  if (!workflowStep || workflowStep.stepType !== 'ask_form' || !Array.isArray(workflowStep.fields)) {
    return { optionValues, customValues };
  }

  workflowStep.fields.forEach((field) => {
    if (field?.type !== 'select' || !Array.isArray(field.options) || field.options.length === 0) {
      return;
    }

    const savedValue = String(selectedFieldValues[field.id] || '').trim();
    if (!savedValue) return;

    const matchedOption = field.options.find((option) => (
      String(option?.value || '').trim() === savedValue ||
      String(option?.label || '').trim() === savedValue
    ));
    if (matchedOption) {
      optionValues[field.id] = String(matchedOption.value || matchedOption.label || '').trim();
      return;
    }

    const otherOption = field.options.find((option) => isAssistOtherOption(option));
    if (!otherOption) return;

    optionValues[field.id] = String(otherOption.value || otherOption.label || '').trim();
    customValues[field.id] = savedValue;
  });

  return { optionValues, customValues };
}

const resultWorkflowHeadingPattern = /^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$/;
const resultWorkflowPlainSectionPattern = /^\s*((?:phase|part)\s+\d+\b.*)$/i;
const resultWorkflowStandaloneStepPattern = /^\s*step\s+\d+\s*[:\-–—.]\s*(.+)$/i;
const resultWorkflowOrderedItemPattern = /^\s{0,3}\d+[.)]\s+(.+)$/;
const resultWorkflowCheckboxItemPattern = /^\s{0,3}[-*+]\s+\[(?: |x|X)\]\s+(.+)$/;
const resultWorkflowBulletItemPattern = /^\s{0,3}[-*+•]\s+(.+)$/;
const resultWorkflowRulePattern = /^\s*(?:[-*_]\s*){3,}$/;
const RESULT_WORKFLOW_MAX_SUBTASKS = 24;

function cleanResultWorkflowText(value) {
  return String(value ?? '')
    .replace(/\[(.*?)\]\((.*?)\)/g, '$1')
    .replace(/`{1,3}([^`]+)`{1,3}/g, '$1')
    .replace(/\*\*(.*?)\*\*/g, '$1')
    .replace(/__(.*?)__/g, '$1')
    .replace(/\*(.*?)\*/g, '$1')
    .replace(/_(.*?)_/g, '$1')
    .replace(/\s+/g, ' ')
    .trim();
}

function normalizeResultWorkflowToken(value) {
  return cleanResultWorkflowText(value)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim();
}

function appendResultWorkflowText(base, next) {
  const left = cleanResultWorkflowText(base);
  const right = cleanResultWorkflowText(next);
  if (!left) return right;
  if (!right) return left;
  return `${left} ${right}`.replace(/\s+/g, ' ').trim();
}

function trimResultWorkflowLabel(value, maxLength = 180) {
  const cleaned = cleanResultWorkflowText(value);
  if (!cleaned || cleaned.length <= maxLength) return cleaned;
  const candidate = cleaned.slice(0, maxLength - 1);
  const boundary = candidate.lastIndexOf(' ');
  const trimmed = boundary >= Math.floor(maxLength * 0.55)
    ? candidate.slice(0, boundary)
    : candidate;
  return `${trimmed.trim()}...`;
}

function isResultWorkflowReferenceSection(value) {
  const token = normalizeResultWorkflowToken(value);
  if (!token) return false;
  return token.includes('note') ||
    token.includes('tip') ||
    token.includes('material') ||
    token.includes('supply') ||
    token.includes('cut list') ||
    token.includes('dimension') ||
    token.includes('measurement') ||
    token.includes('reference') ||
    token.includes('budget') ||
    token.includes('cost');
}

function buildResultWorkflowDraftTitle(taskLabel) {
  const cleaned = trimResultWorkflowLabel(taskLabel, 104) || 'Workflow Draft';
  if (/\bworkflow\b/i.test(cleaned)) {
    return cleaned;
  }
  return trimResultWorkflowLabel(`${cleaned} - Workflow`, 120) || 'Workflow Draft';
}

function buildResultWorkflowDraft(taskLabel, resultText, sourceTaskId, defaultAssignmentValue = '') {
  const lines = String(resultText || '').split(/\r?\n/);
  if (lines.length === 0) return null;

  const actions = [];
  const notes = [];
  const sectionLabels = [];
  const seenSections = new Set();
  let currentSection = '';
  let currentSectionIsReference = false;
  let lastAction = null;
  let lastNoteIndex = -1;

  const rememberSection = (section) => {
    const cleaned = trimResultWorkflowLabel(section, 96);
    if (!cleaned) return;
    const key = cleaned.toLowerCase();
    if (seenSections.has(key)) return;
    seenSections.add(key);
    sectionLabels.push(cleaned);
  };

  const pushNote = (value) => {
    const cleaned = cleanResultWorkflowText(value);
    if (!cleaned) return;
    notes.push(cleaned);
    lastNoteIndex = notes.length - 1;
    lastAction = null;
  };

  const pushAction = (value) => {
    const cleaned = trimResultWorkflowLabel(value, 220);
    if (!cleaned) return;
    const action = {
      text: cleaned,
      section: currentSection
    };
    actions.push(action);
    if (currentSection && !currentSectionIsReference) {
      rememberSection(currentSection);
    }
    lastAction = action;
    lastNoteIndex = -1;
  };

  lines.forEach((rawLine) => {
    const line = String(rawLine || '').replace(/\t/g, '  ');
    const trimmed = line.trim();

    if (!trimmed) {
      lastAction = null;
      return;
    }

    if (resultWorkflowRulePattern.test(trimmed)) {
      lastAction = null;
      lastNoteIndex = -1;
      return;
    }

    const headingMatch = trimmed.match(resultWorkflowHeadingPattern);
    if (headingMatch && headingMatch[2]) {
      currentSection = trimResultWorkflowLabel(headingMatch[2], 120);
      currentSectionIsReference = isResultWorkflowReferenceSection(currentSection);
      lastAction = null;
      lastNoteIndex = -1;
      return;
    }

    const orderedMatch = trimmed.match(resultWorkflowOrderedItemPattern);
    if (orderedMatch && orderedMatch[1]) {
      if (currentSectionIsReference) {
        pushNote(orderedMatch[1]);
      } else {
        pushAction(orderedMatch[1]);
      }
      return;
    }

    const checkboxMatch = trimmed.match(resultWorkflowCheckboxItemPattern);
    if (checkboxMatch && checkboxMatch[1]) {
      if (currentSectionIsReference) {
        pushNote(checkboxMatch[1]);
      } else {
        pushAction(checkboxMatch[1]);
      }
      return;
    }

    const bulletMatch = trimmed.match(resultWorkflowBulletItemPattern);
    if (bulletMatch && bulletMatch[1]) {
      if (currentSectionIsReference) {
        pushNote(bulletMatch[1]);
      } else {
        pushAction(bulletMatch[1]);
      }
      return;
    }

    const standaloneStepMatch = trimmed.match(resultWorkflowStandaloneStepPattern);
    if (standaloneStepMatch && standaloneStepMatch[1]) {
      if (currentSectionIsReference) {
        pushNote(standaloneStepMatch[1]);
      } else {
        pushAction(standaloneStepMatch[1]);
      }
      return;
    }

    const plainSectionMatch = trimmed.match(resultWorkflowPlainSectionPattern);
    if (plainSectionMatch && plainSectionMatch[1]) {
      currentSection = trimResultWorkflowLabel(plainSectionMatch[1], 120);
      currentSectionIsReference = isResultWorkflowReferenceSection(currentSection);
      lastAction = null;
      lastNoteIndex = -1;
      return;
    }

    const continuation = cleanResultWorkflowText(trimmed);
    if (!continuation) return;

    if (!currentSectionIsReference && lastAction) {
      lastAction.text = trimResultWorkflowLabel(appendResultWorkflowText(lastAction.text, continuation), 220);
      return;
    }

    if (currentSectionIsReference && lastNoteIndex >= 0) {
      notes[lastNoteIndex] = appendResultWorkflowText(notes[lastNoteIndex], continuation);
      return;
    }

    pushNote(continuation);
  });

  if (actions.length === 0) {
    return null;
  }

  const truncatedCount = Math.max(actions.length - RESULT_WORKFLOW_MAX_SUBTASKS, 0);
  const selectedActions = actions.slice(0, RESULT_WORKFLOW_MAX_SUBTASKS);
  const subtasks = selectedActions.map((action, index) => {
    const detailParts = [];
    if (action.section) {
      detailParts.push(`Section: ${action.section}`);
    }
    if (index === 0 && sourceTaskId) {
      detailParts.push(`Use input task ${sourceTaskId} as the starting context.`);
    }

    const inputTaskIds = [];
    if (index === 0 && sourceTaskId) {
      inputTaskIds.push(`task:${sourceTaskId}`);
    } else if (index > 0) {
      inputTaskIds.push(`step:${index}`);
    }

    return {
      description: trimResultWorkflowLabel(action.text, 180) || `Step ${index + 1}`,
      details: detailParts.join('\n'),
      assignmentValue: defaultAssignmentValue,
      inputTaskIds
    };
  });

  const normalizedTaskLabel = trimResultWorkflowLabel(taskLabel, 120) || 'this task';
  const detailsParts = [
    `Draft generated from the latest result for "${normalizedTaskLabel}".`,
    'Review the generated steps, assignments, and dependencies before saving.'
  ];

  if (sectionLabels.length > 0) {
    detailsParts.push(`Sections detected: ${sectionLabels.join(' • ')}`);
  }

  if (notes.length > 0) {
    detailsParts.push('Reference notes from the result:');
    notes.slice(0, 6).forEach((note) => {
      detailsParts.push(`- ${note}`);
    });
    if (notes.length > 6) {
      detailsParts.push(`- ${notes.length - 6} more note${notes.length - 6 === 1 ? '' : 's'} remain in the original result.`);
    }
  }

  if (truncatedCount > 0) {
    detailsParts.push(`${truncatedCount} additional step${truncatedCount === 1 ? '' : 's'} were omitted from the draft to keep the workflow reviewable.`);
  }

  return {
    title: buildResultWorkflowDraftTitle(taskLabel),
    details: detailsParts.join('\n'),
    priority: 3,
    assignmentValue: defaultAssignmentValue,
    subtasks
  };
}

export class WorkspaceTaskPage {
  constructor(workspaceId, taskId) {
    this.workspaceId = workspaceId;
    this.taskId = taskId;
    this.workspace = null;
    this.task = null;
    this.tasks = [];
    this.availableAgents = [];
    this.currentBlockedTask = null;
    this.taskAssistResponseExpanded = false;
    this.assistActiveFieldId = '';
    this.assistReviewMode = false;
    this.workspaceRealtimeUnsubscribe = null;
    this.pendingRefreshTimer = null;
    this.titleEditInProgress = false;
    this.workflowDraftPending = false;
    this.resultNoteSaving = false;
    this.savedResultNote = null;
    this.savedResultNoteResult = '';
    this.elements = {};
  }

  async init() {
    this.cacheElements();
    this.bindEvents();
    await this.loadData();
    this.setupRealtime();
  }

  cacheElements() {
    this.elements = {
      root: document.getElementById('workspaceTaskPageRoot'),
      alert: document.getElementById('workspace-task-page-alert'),
      loading: document.getElementById('workspace-task-page-loading'),
      empty: document.getElementById('workspace-task-page-empty'),
      content: document.getElementById('workspace-task-page-content'),
      workspaceName: document.getElementById('workspace-task-workspace-name'),
      title: document.getElementById('workspace-task-title'),
      titleEditBtn: document.getElementById('workspace-task-title-edit'),
      breadcrumbTitle: document.getElementById('workspace-task-breadcrumb-title'),
      copyIdBtn: document.getElementById('workspace-task-copy-id'),
      copyLinkBtn: document.getElementById('workspace-task-copy-link'),
      deleteBtn: document.getElementById('workspace-task-delete'),
      subtitle: document.getElementById('workspace-task-subtitle'),
      status: document.getElementById('workspace-task-status'),
      heroActions: document.getElementById('workspace-task-hero-actions'),
      heroPriority: document.getElementById('workspace-task-hero-priority'),
      heroPriorityCopy: document.getElementById('workspace-task-hero-priority-copy'),
      heroPriorityActions: document.getElementById('workspace-task-hero-priority-actions'),
      liveBadge: document.getElementById('workspace-task-live-badge'),
      overview: document.getElementById('workspace-task-overview'),
      snapshot: document.getElementById('workspace-task-snapshot'),
      relationshipsCard: document.getElementById('workspace-task-relationships-card'),
      relationships: document.getElementById('workspace-task-relationships'),
      outputCard: document.getElementById('workspace-task-output-card'),
      outputCopyBtn: document.getElementById('workspace-task-output-copy'),
      outputSaveNoteBtn: document.getElementById('workspace-task-output-save-note'),
      outputNoteStatus: document.getElementById('workspace-task-output-note-status'),
      output: document.getElementById('workspace-task-output'),
      workflowCard: document.getElementById('workspace-task-workflow-card'),
      workflowActions: document.getElementById('workspace-task-workflow-actions'),
      workflowAddStepBtn: document.getElementById('workspace-task-workflow-add-step'),
      workflowRunAllBtn: document.getElementById('workspace-task-workflow-run-all'),
      workflowEmpty: document.getElementById('workspace-task-workflow-empty'),
      workflowGenerateBtn: document.getElementById('workspace-task-workflow-generate'),
      workflowSteps: document.getElementById('workspace-task-workflow-steps'),
      scheduleCard: document.getElementById('workspace-task-schedule-card'),
      schedule: document.getElementById('workspace-task-schedule'),
      stepsCard: document.getElementById('workspace-task-steps-card'),
      steps: document.getElementById('workspace-task-steps'),
      contextCard: document.getElementById('workspace-task-context-card'),
      context: document.getElementById('workspace-task-context'),
      blockedContextCard: document.getElementById('workspace-task-blocked-context-card'),
      blockedReason: document.getElementById('workspace-task-blocked-reason'),
      blockedRequestWrap: document.getElementById('workspace-task-blocked-request-wrap'),
      blockedRequestPreview: document.getElementById('workspace-task-blocked-request-preview'),
      blockedRequestToggle: document.getElementById('workspace-task-blocked-request-toggle'),
      blockedRequest: document.getElementById('workspace-task-blocked-request'),
      assistCard: document.getElementById('workspace-task-assist-card'),
      assistKnown: document.getElementById('workspace-task-assist-known'),
      assistNeeds: document.getElementById('workspace-task-assist-needs'),
      assistNext: document.getElementById('workspace-task-assist-next'),
      assistQuestionWrap: document.getElementById('workspace-task-assist-question-wrap'),
      assistQuestion: document.getElementById('workspace-task-assist-question'),
      assistFormWrap: document.getElementById('workspace-task-assist-form-wrap'),
      assistFormFields: document.getElementById('workspace-task-assist-form-fields'),
      assistMessage: document.getElementById('workspace-task-assist-message'),
      assistAgent: document.getElementById('workspace-task-assist-agent'),
      assistMoreActions: document.querySelector('.workspace-task-assist-more-actions'),
      assistSwitchWrap: document.querySelector('.workspace-task-assist-switch'),
      assistRetryBtn: document.getElementById('workspace-task-assist-retry'),
      assistContinueBtn: document.getElementById('workspace-task-assist-continue'),
      assistSwitchBtn: document.getElementById('workspace-task-assist-switch'),
      assistFailBtn: document.getElementById('workspace-task-assist-fail')
    };
  }

  bindEvents() {
    this.elements.titleEditBtn?.addEventListener('click', () => this.startTitleEdit());
    this.elements.title?.addEventListener('dblclick', () => this.startTitleEdit());
    this.elements.copyIdBtn?.addEventListener('click', () => this.copyToClipboard(this.taskId, 'Task ID copied'));
    this.elements.copyLinkBtn?.addEventListener('click', () => this.copyToClipboard(window.location.href, 'Link copied'));
    this.elements.deleteBtn?.addEventListener('click', () => this.deleteTask());
    this.elements.outputCopyBtn?.addEventListener('click', () => this.copyCurrentResult());
    this.elements.outputSaveNoteBtn?.addEventListener('click', () => this.saveCurrentResultAsNote());
    this.elements.blockedRequestToggle?.addEventListener('click', () => this.toggleAssistResponseExpanded());
    this.elements.workflowGenerateBtn?.addEventListener('click', () => this.handleGenerateSteps());
    this.elements.workflowAddStepBtn?.addEventListener('click', () => this.handleAddStep());
    this.elements.workflowRunAllBtn?.addEventListener('click', () => this.handleRunAllSteps());
    this.elements.assistRetryBtn?.addEventListener('click', () => this.submitTaskAssist('retry'));
    this.elements.assistContinueBtn?.addEventListener('click', () => this.submitTaskAssist('continue_with_instruction'));
    this.elements.assistSwitchBtn?.addEventListener('click', () => this.submitTaskAssist('switch_agent_retry'));
    this.elements.assistFailBtn?.addEventListener('click', () => this.submitTaskAssist('mark_failed'));
    this.elements.assistAgent?.addEventListener('change', () => this.updateAssistSwitchButtonState());
  }

  async loadData() {
    this.setState('loading');
    this.setAlert('');

    try {
      const [workspace, taskResponse, agents] = await Promise.all([
        this.fetchWorkspace(),
        this.fetchTask(),
        this.fetchAgents().catch(() => [])
      ]);

      this.workspace = workspace || null;
      this.tasks = Array.isArray(workspace?.tasks) ? workspace.tasks : [];
      this.availableAgents = Array.isArray(agents) ? agents : [];

      const workspaceTask = this.tasks.find((item) => String(item?.id || '') === this.taskId) || null;
      this.task = taskResponse || workspaceTask;

      if (!this.task || String(this.task.workspace_id || this.workspaceId) !== this.workspaceId) {
        this.setState('empty');
        return;
      }

      if (!workspaceTask) {
        this.tasks = this.task ? [this.task] : [];
      }

      this.render();
      this.setState('content');
    } catch (error) {
      console.error('Failed to load workspace task page:', error);
      this.setAlert(error?.message || 'Failed to load this task page.');
      this.setState('empty');
    }
  }

  async fetchWorkspace() {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`);
    if (!response.ok) {
      throw new Error('Failed to load workspace details.');
    }
    return response.json();
  }

  async fetchTask() {
    const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(this.taskId)}`);
    if (response.status === 404) return null;
    if (!response.ok) {
      throw new Error('Failed to load task details.');
    }
    return response.json();
  }

  async fetchAgents() {
    const response = await fetch('/api/agents');
    if (!response.ok) {
      throw new Error('Failed to load agent list.');
    }

    const payload = await response.json();
    return Array.isArray(payload?.agents) ? payload.agents : [];
  }

  setupRealtime() {
    if (this.workspaceRealtimeUnsubscribe || !window.workspaceRealtime || typeof window.workspaceRealtime.subscribeToWorkspace !== 'function') {
      return;
    }

    this.workspaceRealtimeUnsubscribe = window.workspaceRealtime.subscribeToWorkspace(this.workspaceId, (event) => {
      this.handleRealtimeEvent(event);
    });
  }

  handleRealtimeEvent(event) {
    const eventType = String(event?.type || '').trim();
    if (!eventType.startsWith('task.')) {
      return;
    }

    const payload = event?.data?.data || event?.data || {};
    const eventTaskId = String(payload?.task_id || payload?.id || payload?.task?.id || '').trim();
    if (eventTaskId && eventTaskId !== this.taskId) {
      return;
    }

    if (this.elements.root) {
      this.elements.root.classList.remove('workspace-task-page-flash');
      void this.elements.root.offsetWidth;
      this.elements.root.classList.add('workspace-task-page-flash');
    }

    window.clearTimeout(this.pendingRefreshTimer);
    this.pendingRefreshTimer = window.setTimeout(() => {
      this.loadData();
    }, 180);
  }

  setState(state) {
    const isLoading = state === 'loading';
    const isEmpty = state === 'empty';
    const isContent = state === 'content';

    if (this.elements.loading) this.elements.loading.hidden = !isLoading;
    if (this.elements.empty) this.elements.empty.hidden = !isEmpty;
    if (this.elements.content) this.elements.content.hidden = !isContent;
  }

  setAlert(message = '') {
    if (!this.elements.alert) return;
    const normalized = String(message || '').trim();
    this.elements.alert.textContent = normalized;
    this.elements.alert.classList.toggle('d-none', !normalized);
  }

  startTitleEdit() {
    if (this.titleEditInProgress || !this.elements.title) return;

    const titleElement = this.elements.title;
    const actionsContainer = titleElement.parentElement?.querySelector('.workspace-task-page-title-actions');
    const currentValue = this.getTaskDisplayLabel();
    this.titleEditInProgress = true;

    const input = document.createElement('textarea');
    input.className = 'workspace-task-page-title-input';
    input.rows = 1;
    input.value = currentValue;
    input.setAttribute('aria-label', 'Edit task title');

    const editActions = document.createElement('div');
    editActions.className = 'workspace-task-page-title-edit-actions';
    editActions.innerHTML = `
      <button type="button" class="workspace-task-page-edit-save" aria-label="Save title">Save</button>
      <button type="button" class="workspace-task-page-edit-cancel" aria-label="Cancel editing">Cancel</button>
      <span class="workspace-task-page-edit-hint">Enter to save, Esc to cancel</span>
    `;

    const syncHeight = () => {
      input.style.height = 'auto';
      input.style.height = `${Math.max(input.scrollHeight, 70)}px`;
    };

    titleElement.style.display = 'none';
    if (actionsContainer) actionsContainer.style.display = 'none';
    titleElement.insertAdjacentElement('afterend', editActions);
    titleElement.insertAdjacentElement('afterend', input);
    syncHeight();
    input.focus();
    input.select();

    const finishEdit = async (save) => {
      if (!this.titleEditInProgress) return;
      this.titleEditInProgress = false;

      const nextValue = input.value.trim();
      input.remove();
      editActions.remove();
      titleElement.style.display = '';
      if (actionsContainer) actionsContainer.style.display = '';

      if (!save || nextValue === currentValue) {
        return;
      }

      if (!nextValue) {
        this.notify('error', 'Task title cannot be empty.');
        return;
      }

      try {
        await this.updateTaskFields({ description: nextValue });
        this.notify('success', 'Task title updated');
      } catch (error) {
        console.error('Failed to update task title:', error);
        this.notify('error', error?.message || 'Failed to update task title');
      }
    };

    editActions.querySelector('.workspace-task-page-edit-save')?.addEventListener('mousedown', (e) => {
      e.preventDefault();
      finishEdit(true);
    });
    editActions.querySelector('.workspace-task-page-edit-cancel')?.addEventListener('mousedown', (e) => {
      e.preventDefault();
      finishEdit(false);
    });

    input.addEventListener('input', syncHeight);
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        finishEdit(false);
        return;
      }

      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        finishEdit(true);
      }
    });
    input.addEventListener('blur', (e) => {
      if (editActions.contains(e.relatedTarget)) return;
      finishEdit(true);
    });
  }

  async updateTaskFields(patch) {
    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(this.taskId)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to update task');
    }

    const updatedTask = await response.json();
    this.task = updatedTask || this.task;
    if (Array.isArray(this.tasks)) {
      this.tasks = this.tasks.map((task) => (
        String(task?.id || '') === String(this.taskId)
          ? { ...task, ...(updatedTask || {}) }
          : task
      ));
    }
    this.render();
    return updatedTask;
  }

  getTaskDisplayLabel(task = this.task) {
    return String(task?.description || task?.name || 'Untitled Task').trim() || 'Untitled Task';
  }

  escapeHtml(value) {
    return escapeTaskHtml(value);
  }

  getTaskHref(taskId) {
    return `/workspaces/${encodeURIComponent(this.workspaceId)}/task/${encodeURIComponent(taskId)}`;
  }

  getTaskHumanLoop(task = this.task) {
    const humanLoop = task?.context?.human_loop;
    return humanLoop && typeof humanLoop === 'object' ? humanLoop : null;
  }

  getTaskStatusPresentation(task = this.task) {
    const humanLoop = this.getTaskHumanLoop(task);
    const blocked = String(task?.status || '').trim().toLowerCase() === 'blocked' ||
      String(humanLoop?.state || '').trim().toLowerCase() === 'blocked' ||
      Boolean(humanLoop?.reason) ||
      Boolean(humanLoop?.question);

    return {
      isBlocked: blocked,
      label: blocked ? 'Blocked' : getDisplayStatus(task?.status),
      className: blocked ? 'blocked' : getStatusClass(task?.status),
      reason: String(humanLoop?.reason || '').trim()
    };
  }

  normalizeAssistFieldValues(value) {
    const result = {};
    if (!Array.isArray(value)) return result;

    value.forEach((item) => {
      const id = String(item?.id || '').trim();
      const fieldValue = String(item?.value || '').trim();
      if (id && fieldValue) {
        result[id] = fieldValue;
      }
    });

    return result;
  }

  normalizeAssistWorkflowStep(value) {
    if (!value || typeof value !== 'object') return null;

    const stepType = String(value.step_type || value.stepType || '').trim().toLowerCase();
    if (stepType !== 'ask_choice' && stepType !== 'ask_form') return null;

    const normalized = {
      stepType,
      title: String(value.title || '').trim(),
      summary: String(value.summary || '').trim(),
      freeTextAllowed: value.free_text_allowed !== false && value.freeTextAllowed !== false,
      choices: [],
      fields: []
    };

    if (stepType === 'ask_choice' && Array.isArray(value.choices)) {
      normalized.choices = value.choices
        .map((choice, index) => {
          const id = String(choice?.id || '').trim() || `choice-${index + 1}`;
          const label = String(choice?.label || '').trim();
          if (!label) return null;
          return {
            id,
            label,
            description: String(choice?.description || '').trim(),
            number: String(choice?.number || '').trim()
          };
        })
        .filter(Boolean);
    }

    if (stepType === 'ask_form' && Array.isArray(value.fields)) {
      normalized.fields = value.fields
        .map((field, index) => {
          const id = String(field?.id || '').trim() || `field-${index + 1}`;
          const label = String(field?.label || '').trim() || `Question ${index + 1}`;
          const type = String(field?.type || 'text').trim().toLowerCase();
          const options = Array.isArray(field?.options)
            ? field.options.map((option, optionIndex) => ({
              value: String(option?.value || '').trim(),
              label: String(option?.label || option?.value || '').trim(),
              description: String(option?.description || '').trim(),
              key: String(option?.key || '').trim() || String.fromCharCode(65 + (optionIndex % 26))
            })).filter((option) => option.value && option.label)
            : [];

          return {
            id,
            label,
            description: String(field?.description || '').trim(),
            evidence: String(field?.evidence || '').trim(),
            type,
            placeholder: String(field?.placeholder || '').trim(),
            required: field?.required !== false,
            options
          };
        })
        .filter(Boolean);
    }

    return normalized;
  }

  buildBlockedTaskState(task = this.task) {
    const humanLoop = this.getTaskHumanLoop(task) || {};
    const workflowStep = this.normalizeAssistWorkflowStep(
      humanLoop?.workflow_step || task?.context?.planning_workflow_step
    ) || this.normalizeAssistWorkflowStep(
      deriveAssistWorkflowStepFromText(humanLoop?.question, humanLoop?.agent_response)
    );
    const selectedFieldValues = this.normalizeAssistFieldValues(humanLoop?.field_values);
    const selectState = buildAssistSelectState(workflowStep, selectedFieldValues);

    return {
      taskId: String(task?.id || '').trim(),
      blockId: String(humanLoop?.block_id || '').trim(),
      currentAgent: String(task?.to || '').trim(),
      reasonCode: String(humanLoop?.reason_code || '').trim().toLowerCase(),
      reason: String(humanLoop?.reason || 'This task needs your input before it can continue.').trim(),
      question: String(humanLoop?.question || '').trim(),
      response: String(humanLoop?.agent_response || '').trim(),
      suggestedActions: Array.isArray(humanLoop?.suggested_actions)
        ? humanLoop.suggested_actions.map((action) => String(action || '').trim()).filter(Boolean)
        : [],
      workflowStep,
      selectedChoiceId: '',
      selectedChoiceLabel: '',
      selectedChoiceNumber: '',
      selectedFieldValues,
      selectedFieldOptionValues: selectState.optionValues,
      selectedFieldCustomValues: selectState.customValues
    };
  }

  getAvailableAgentNames() {
    const names = new Set();

    if (Array.isArray(this.availableAgents)) {
      this.availableAgents.forEach((item) => {
        const name = typeof item === 'string'
          ? String(item || '').trim()
          : String(item?.name || '').trim();
        if (name) names.add(name);
      });
    }

    return Array.from(names).sort((left, right) => left.localeCompare(right));
  }

  isRunnableAgentName(agentName) {
    const normalizedTarget = String(agentName || '').trim().toLowerCase();
    if (!normalizedTarget || normalizedTarget === 'unassigned') return true;

    return this.getAvailableAgentNames().some((name) => String(name || '').trim().toLowerCase() === normalizedTarget);
  }

  getAssignableAgentNames(currentAgent = '') {
    const runnable = this.getAvailableAgentNames();
    if (runnable.length > 0) {
      return runnable;
    }

    const fallback = this.getWorkspaceAgentNames();
    const normalizedCurrent = String(currentAgent || '').trim();
    if (normalizedCurrent && !fallback.some((name) => String(name || '').trim().toLowerCase() === normalizedCurrent.toLowerCase())) {
      fallback.unshift(normalizedCurrent);
    }
    return fallback;
  }

  getWorkspaceAgentNames() {
    const names = new Set();

    if (Array.isArray(this.workspace?.agent_instances)) {
      this.workspace.agent_instances.forEach((instance) => {
        const name = String(instance?.role || instance?.name || '').trim();
        if (name) names.add(name);
      });
    }

    if (Array.isArray(this.workspace?.agents)) {
      this.workspace.agents.forEach((name) => {
        const normalized = String(name || '').trim();
        if (normalized) names.add(normalized);
      });
    }

    if (this.task?.to) {
      names.add(String(this.task.to).trim());
    }

    return Array.from(names).filter(Boolean);
  }

  isAssistActionSuggested(action) {
    const suggestions = Array.isArray(this.currentBlockedTask?.suggestedActions)
      ? this.currentBlockedTask.suggestedActions
      : [];
    if (suggestions.length === 0) return true;
    return suggestions.includes(action);
  }

  getParentTask() {
    const parentTaskId = String(this.task?.parent_task_id || '').trim();
    if (!parentTaskId) return null;
    return this.tasks.find((item) => String(item?.id || '') === parentTaskId) || null;
  }

  getSubtasks() {
    return this.tasks.filter((item) => String(item?.parent_task_id || '').trim() === String(this.task?.id || ''));
  }

  getInputTasks() {
    const inputIds = Array.isArray(this.task?.input_task_ids) ? this.task.input_task_ids : [];
    if (inputIds.length === 0) return [];

    return inputIds
      .map((taskId) => this.tasks.find((item) => String(item?.id || '') === String(taskId || '').trim()) || null)
      .filter(Boolean);
  }

  render() {
    const statusInfo = this.getTaskStatusPresentation();
    this.currentBlockedTask = statusInfo.isBlocked ? this.buildBlockedTaskState() : null;
    this.taskAssistResponseExpanded = false;
    this.assistReviewMode = false;

    this.renderHero(statusInfo);
    this.renderHeroActions(statusInfo);
    this.renderHeroPriority(statusInfo);
    this.renderOverview();
    this.renderSnapshot(statusInfo);
    this.renderRelationships();
    this.renderWorkflow();
    this.renderOutput();
    this.renderSchedule();
    this.renderExecutionSteps();
    this.renderContext();
    this.renderBlockedState(statusInfo);
  }

  renderHero(statusInfo) {
    const taskTitle = this.getTaskDisplayLabel();
    const detailsSummary = summarizeText(this.task?.details || this.currentBlockedTask?.reason || '', 280);

    if (this.elements.workspaceName) {
      this.elements.workspaceName.textContent = String(this.workspace?.name || 'Workspace').trim() || 'Workspace';
    }
    if (this.elements.title) {
      this.elements.title.textContent = taskTitle;
    }
    document.title = `${taskTitle} - Ori Agent`;
    if (this.elements.subtitle) {
      this.elements.subtitle.textContent = detailsSummary || 'No additional details provided.';
    }
    if (this.elements.breadcrumbTitle) {
      this.elements.breadcrumbTitle.textContent = summarizeText(taskTitle, 40) || 'Task';
    }
    if (this.elements.status) {
      this.elements.status.textContent = statusInfo.label;
      this.elements.status.dataset.state = statusInfo.className;
    }
    if (this.elements.liveBadge) {
      const isLive = statusInfo.className === 'in_progress' && this.workspaceRealtimeUnsubscribe;
      this.elements.liveBadge.hidden = !isLive;
    }
  }

  renderHeroActions(statusInfo) {
    if (!this.elements.heroActions) return;

    const status = String(this.task?.status || '').trim().toLowerCase();
    const hasAgent = Boolean(this.task?.to);
    const buttons = [];

    if (status === 'pending' && hasAgent) {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn workspace-task-page-hero-btn-primary" data-action="execute">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8,5.14V19.14L19,12.14L8,5.14Z"/></svg>Run
      </button>`);
    }

    if (status === 'failed' || status === 'completed') {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="execute">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/></svg>Re-run
      </button>`);
    }

    if ((status === 'pending' || status === 'assigned' || status === 'in_progress') && !statusInfo.isBlocked) {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="complete">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/></svg>Mark Complete
      </button>`);
    }

    this.elements.heroActions.innerHTML = buttons.join('');

    this.elements.heroActions.querySelectorAll('[data-action]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const action = btn.dataset.action;
        if (action === 'execute') this.executeTask();
        if (action === 'complete') this.completeTask();
      });
    });
  }

  renderHeroPriority(statusInfo) {
    if (!this.elements.heroPriority || !this.elements.heroPriorityCopy || !this.elements.heroPriorityActions) {
      return;
    }

    const blocked = statusInfo.isBlocked && this.currentBlockedTask;
    if (!blocked) {
      this.elements.heroPriority.hidden = true;
      this.elements.heroPriorityCopy.textContent = '';
      this.elements.heroPriorityActions.innerHTML = '';
      return;
    }

    const workflowStep = this.currentBlockedTask?.workflowStep || null;
    const hasAgentResponse = Boolean(String(this.currentBlockedTask?.response || '').trim());
    const secondaryLabel = hasAgentResponse ? 'View Agent Request' : 'Why Paused?';

    this.elements.heroPriority.hidden = false;
    this.elements.heroPriorityCopy.textContent = this.getBlockedHeroSummary(workflowStep);
    this.elements.heroPriorityActions.innerHTML = `
      <button type="button" class="workspace-task-page-hero-btn workspace-task-page-hero-btn-primary" data-hero-priority-action="assist">
        ${this.escapeHtml(this.getBlockedHeroPrimaryActionLabel(workflowStep))}
      </button>
      <button type="button" class="workspace-task-page-hero-btn" data-hero-priority-action="context">
        ${this.escapeHtml(secondaryLabel)}
      </button>
    `;

    this.elements.heroPriorityActions.querySelectorAll('[data-hero-priority-action]').forEach((button) => {
      button.addEventListener('click', () => {
        const action = String(button.getAttribute('data-hero-priority-action') || '').trim();
        if (action === 'assist') {
          this.scrollToSection(this.elements.assistCard, { focusTarget: () => this.getAssistFocusTarget() });
          return;
        }
        if (action === 'context') {
          const focusTarget = this.elements.blockedRequestToggle && !this.elements.blockedRequestToggle.classList.contains('d-none')
            ? this.elements.blockedRequestToggle
            : null;
          this.scrollToSection(this.elements.blockedContextCard, { focusTarget });
        }
      });
    });
  }

  getBlockedHeroSummary(workflowStep) {
    const question = String(this.currentBlockedTask?.question || '').trim();
    if (question) {
      return question;
    }
    return this.getAssistNeedsSummary(workflowStep);
  }

  getBlockedHeroPrimaryActionLabel(workflowStep) {
    if (this.currentBlockedTask?.reasonCode === 'assigned_agent_missing' &&
        this.isAssistActionSuggested('switch_agent_retry') &&
        !this.isAssistActionSuggested('continue_with_instruction')) {
      return 'Switch Agent';
    }
    if (workflowStep?.stepType === 'ask_form') {
      return 'Answer Questions';
    }
    if (workflowStep?.stepType === 'ask_choice') {
      return 'Choose Next Step';
    }
    return 'Review And Continue';
  }

  scrollToSection(element, { focusTarget = null } = {}) {
    if (!element) return;

    element.scrollIntoView({ behavior: 'smooth', block: 'start' });

    const target = typeof focusTarget === 'function' ? focusTarget() : focusTarget;
    if (!target || typeof target.focus !== 'function') return;

    window.setTimeout(() => {
      target.focus({ preventScroll: true });
    }, 180);
  }

  getAssistFocusTarget() {
    if (!this.elements.assistCard) return this.elements.assistContinueBtn || null;

    return this.elements.assistCard.querySelector(
      '[data-assist-choice-id]:not([disabled]), [data-assist-field-id]:not([disabled]), textarea:not([disabled]), select:not([disabled]), button:not([disabled])'
    ) || this.elements.assistContinueBtn || null;
  }

  renderOverview() {
    if (!this.elements.overview) return;

    const progress = this.task?.progress;
    const templateRef = this.task?.template_ref;
    const executionMode = String(this.task?.execution_mode || 'auto').replace(/_/g, ' ');
    const orchestrationMode = String(this.task?.orchestration_mode || '').replace(/_/g, ' ');
    const isBlocked = Boolean(this.currentBlockedTask);

    const items = [
      {
        title: 'Requested By',
        value: String(this.task?.from || 'Workspace').trim() || 'Workspace'
      },
      {
        title: 'Assigned To',
        value: String(this.task?.to || 'Unassigned').trim() || 'Unassigned'
      },
      {
        title: 'Execution Mode',
        value: executionMode ? executionMode.replace(/\b\w/g, (char) => char.toUpperCase()) : 'Auto'
      }
    ];

    if (orchestrationMode) {
      items.push({
        title: 'Orchestration',
        value: orchestrationMode.replace(/\b\w/g, (char) => char.toUpperCase())
      });
    }

    if (progress && (progress.current_step || Number.isFinite(progress.percentage))) {
      const progressLabel = [
        Number.isFinite(Number(progress.percentage)) ? `${Number(progress.percentage)}% complete` : '',
        String(progress.current_step || '').trim(),
        Number(progress.total_steps) > 0
          ? `${Number(progress.completed_steps || 0)}/${Number(progress.total_steps)} steps`
          : ''
      ].filter(Boolean).join(' • ');
      items.push({
        title: 'Progress',
        value: progressLabel || 'Progress available'
      });
    }

    if (templateRef?.template_name || templateRef?.step_name) {
      items.push({
        title: 'Template',
        value: [templateRef.template_name, templateRef.step_name].filter(Boolean).join(' / ')
      });
    }

    const detailsValue = String(this.task?.details || '').trim();
    const blockedDetailsRedundant = this.isBlockedDetailsRedundant(detailsValue);

    if (!isBlocked || (detailsValue && !blockedDetailsRedundant)) {
      items.push({
        title: isBlocked ? 'Original Brief' : 'Task Details',
        value: detailsValue || 'No extra task details were provided.',
        full: true,
        editable: true,
        disclosure: isBlocked,
        disclosureHint: 'Hidden while this task is waiting for input.'
      });
    }

    this.elements.overview.innerHTML = items.map((item) => {
      if (item.disclosure) {
        return `
          <article class="workspace-task-overview-item${item.full ? ' full' : ''}">
            <details class="workspace-task-overview-disclosure">
              <summary class="workspace-task-overview-disclosure-toggle">
                <span class="workspace-task-overview-disclosure-heading">${this.escapeHtml(item.title)}</span>
                <span class="workspace-task-overview-disclosure-hint">${this.escapeHtml(item.disclosureHint || '')}</span>
              </summary>
              <div class="workspace-task-overview-disclosure-body">
                <div class="workspace-task-overview-title">
                  Task Details
                  ${item.editable ? `<button type="button" class="workspace-task-overview-edit-btn" data-edit-field="details" aria-label="Edit details" title="Edit details">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/></svg>
                  </button>` : ''}
                </div>
                <div class="workspace-task-overview-value">${this.escapeHtml(item.value)}</div>
              </div>
            </details>
          </article>
        `;
      }

      return `
        <article class="workspace-task-overview-item${item.full ? ' full' : ''}">
          <div class="workspace-task-overview-title">
            ${this.escapeHtml(item.title)}
            ${item.editable ? `<button type="button" class="workspace-task-overview-edit-btn" data-edit-field="details" aria-label="Edit details" title="Edit details">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/></svg>
            </button>` : ''}
          </div>
          <div class="workspace-task-overview-value">${this.escapeHtml(item.value)}</div>
        </article>
      `;
    }).join('');

    this.elements.overview.querySelectorAll('[data-edit-field="details"]').forEach((btn) => {
      btn.addEventListener('click', () => this.startDetailsEdit(btn));
    });
  }

  normalizeComparableText(value) {
    return String(value || '').replace(/\s+/g, ' ').trim().toLowerCase();
  }

  isBlockedDetailsRedundant(detailsValue) {
    if (!this.currentBlockedTask) return false;

    const normalizedDetails = this.normalizeComparableText(detailsValue);
    if (!normalizedDetails) return false;

    return [
      this.currentBlockedTask.reason,
      this.currentBlockedTask.question
    ].some((candidate) => normalizedDetails === this.normalizeComparableText(candidate));
  }

  startDetailsEdit(triggerBtn) {
    const article = triggerBtn.closest('.workspace-task-overview-item');
    if (!article) return;

    const valueEl = article.querySelector('.workspace-task-overview-value');
    if (!valueEl) return;

    const currentValue = String(this.task?.details || '').trim();
    const textarea = document.createElement('textarea');
    textarea.className = 'form-control workspace-task-overview-edit-textarea';
    textarea.rows = 4;
    textarea.value = currentValue;

    const actions = document.createElement('div');
    actions.className = 'workspace-task-page-title-edit-actions';
    actions.style.marginTop = '0.5rem';
    actions.innerHTML = `
      <button type="button" class="workspace-task-page-edit-save" aria-label="Save details">Save</button>
      <button type="button" class="workspace-task-page-edit-cancel" aria-label="Cancel editing">Cancel</button>
    `;

    valueEl.style.display = 'none';
    triggerBtn.style.display = 'none';
    valueEl.insertAdjacentElement('afterend', actions);
    valueEl.insertAdjacentElement('afterend', textarea);
    textarea.focus();

    const finish = async (save) => {
      const nextValue = textarea.value.trim();
      textarea.remove();
      actions.remove();
      valueEl.style.display = '';
      triggerBtn.style.display = '';

      if (!save || nextValue === currentValue) return;

      try {
        await this.updateTaskFields({ details: nextValue });
        this.notify('success', 'Task details updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update details');
      }
    };

    actions.querySelector('.workspace-task-page-edit-save')?.addEventListener('mousedown', (e) => { e.preventDefault(); finish(true); });
    actions.querySelector('.workspace-task-page-edit-cancel')?.addEventListener('mousedown', (e) => { e.preventDefault(); finish(false); });
    textarea.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { e.preventDefault(); finish(false); }
    });
    textarea.addEventListener('blur', (e) => {
      if (actions.contains(e.relatedTarget)) return;
      finish(true);
    });
  }

  renderSnapshot(statusInfo) {
    if (!this.elements.snapshot) return;

    const currentAgent = String(this.task?.to || 'Unassigned').trim() || 'Unassigned';
    const currentAgentUnavailable = Boolean(this.task?.to) && !this.isRunnableAgentName(currentAgent);
    const statusOptions = ['pending', 'assigned', 'in_progress', 'completed', 'failed', 'blocked', 'cancelled'];
    const agentNames = this.getAssignableAgentNames(currentAgent);

    const snapshotItems = [
      ['Created', formatDateTime(this.task?.created_at)],
      ['Started', formatDateTime(this.task?.started_at)],
      ['Completed', formatDateTime(this.task?.completed_at)],
      ['Task Type', this.getSubtasks().length > 0 ? 'Parent Task' : 'Leaf Task']
    ];

    if (this.task?.schedule_name) {
      snapshotItems.push(['Schedule', String(this.task.schedule_name).trim()]);
    }

    const statusSelectHtml = `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">Status</span>
        <select id="workspace-task-snapshot-status" class="workspace-task-snapshot-select workspace-task-page-status-inline" data-state="${this.escapeHtml(statusInfo.className)}">
          ${statusOptions.map((s) => `<option value="${this.escapeHtml(s)}" ${s === (this.task?.status || 'pending') ? 'selected' : ''}>${this.escapeHtml(getDisplayStatus(s))}</option>`).join('')}
        </select>
      </div>
    `;

    const agentSelectHtml = `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">Agent</span>
        <select id="workspace-task-snapshot-agent" class="workspace-task-snapshot-select">
          <option value="" ${!this.task?.to ? 'selected' : ''}>Unassigned</option>
          ${currentAgentUnavailable
            ? `<option value="${this.escapeHtml(currentAgent)}" selected disabled>${this.escapeHtml(`${currentAgent} (Unavailable)`)}</option>`
            : ''}
          ${agentNames
            .filter((name) => String(name || '').trim() && String(name || '').trim().toLowerCase() !== currentAgent.toLowerCase())
            .map((name) => `<option value="${this.escapeHtml(name)}">${this.escapeHtml(name)}</option>`)
            .join('')}
          ${!currentAgentUnavailable
            ? agentNames
              .filter((name) => String(name || '').trim().toLowerCase() === currentAgent.toLowerCase())
              .map((name) => `<option value="${this.escapeHtml(name)}" selected>${this.escapeHtml(name)}</option>`)
              .join('')
            : ''}
        </select>
      </div>
    `;

    const currentPriority = Number(this.task?.priority) || 3;
    const priorityLabels = { 1: '1 - Highest', 2: '2 - High', 3: '3 - Medium', 4: '4 - Low', 5: '5 - Lowest' };
    const prioritySelectHtml = `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">Priority</span>
        <select id="workspace-task-snapshot-priority" class="workspace-task-snapshot-select">
          ${[1, 2, 3, 4, 5].map((p) => `<option value="${p}" ${p === currentPriority ? 'selected' : ''}>${this.escapeHtml(priorityLabels[p])}</option>`).join('')}
        </select>
      </div>
    `;

    const staticItemsHtml = snapshotItems.map(([label, value]) => `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">${this.escapeHtml(label)}</span>
        <span class="workspace-task-snapshot-value">${this.escapeHtml(value || '—')}</span>
      </div>
    `).join('');

    const summaryCopy = currentAgentUnavailable
      ? 'This task is assigned to an unavailable agent. Reassign it before retrying execution.'
      : (statusInfo.isBlocked || statusInfo.className === 'in_progress'
      ? 'The main page already shows the important execution state. Open this only if you need to adjust controls or inspect metadata.'
      : 'Open this only when you need to change task controls or inspect metadata.');

    this.elements.snapshot.innerHTML = `
      <div class="workspace-task-snapshot-summary">
        <div class="workspace-task-snapshot-summary-copy">${this.escapeHtml(summaryCopy)}</div>
        <div class="workspace-task-snapshot-chip-row">
          <span class="workspace-task-snapshot-chip workspace-task-snapshot-chip-status" data-state="${this.escapeHtml(statusInfo.className)}">${this.escapeHtml(statusInfo.label)}</span>
          <span class="workspace-task-snapshot-chip">${this.escapeHtml(currentAgentUnavailable ? `${currentAgent} (Unavailable)` : currentAgent)}</span>
          <span class="workspace-task-snapshot-chip">${this.escapeHtml(priorityLabels[currentPriority] || `Priority ${currentPriority}`)}</span>
        </div>
      </div>

      <details class="workspace-task-snapshot-disclosure">
        <summary class="workspace-task-snapshot-disclosure-toggle">Show full task controls and metadata</summary>
        <div class="workspace-task-snapshot-disclosure-body">
          ${statusSelectHtml}
          ${agentSelectHtml}
          ${prioritySelectHtml}
          ${staticItemsHtml}
        </div>
      </details>
    `;

    const statusSelect = document.getElementById('workspace-task-snapshot-status');
    const agentSelect = document.getElementById('workspace-task-snapshot-agent');

    statusSelect?.addEventListener('change', async () => {
      try {
        await this.updateTaskFields({ status: statusSelect.value });
        this.notify('success', 'Status updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update status');
      }
    });

    agentSelect?.addEventListener('change', async () => {
      try {
        await this.updateTaskFields({ to: agentSelect.value || '' });
        this.notify('success', 'Agent updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update agent');
      }
    });

    const prioritySelect = document.getElementById('workspace-task-snapshot-priority');
    prioritySelect?.addEventListener('change', async () => {
      try {
        await this.updateTaskFields({ priority: Number(prioritySelect.value) || 3 });
        this.notify('success', 'Priority updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update priority');
      }
    });
  }

  renderRelationships() {
    if (!this.elements.relationships || !this.elements.relationshipsCard) return;

    const parentTask = this.getParentTask();
    const inputTasks = this.getInputTasks();
    const groups = [];

    if (parentTask) {
      groups.push({
        title: 'Parent Task',
        tasks: [parentTask]
      });
    }

    if (inputTasks.length > 0) {
      groups.push({
        title: 'Input Tasks',
        tasks: inputTasks
      });
    }

    if (groups.length === 0) {
      this.elements.relationshipsCard.hidden = true;
      this.elements.relationships.innerHTML = '';
      return;
    }

    this.elements.relationshipsCard.hidden = false;
    this.elements.relationships.innerHTML = groups.map((group) => `
      <section class="workspace-task-relationship-group">
        <div class="workspace-task-relationship-title">${this.escapeHtml(group.title)}</div>
        <div class="workspace-task-related-links">
          ${group.tasks.map((task) => `
            <a href="${this.getTaskHref(task.id)}" class="workspace-task-related-link">
              <span class="workspace-task-related-link-title">${this.escapeHtml(this.getTaskDisplayLabel(task))}</span>
              <span class="workspace-task-related-link-meta">${this.escapeHtml(getDisplayStatus(task.status))} • ${this.escapeHtml(String(task.to || 'Unassigned').trim() || 'Unassigned')}</span>
            </a>
          `).join('')}
        </div>
      </section>
    `).join('');
  }

  getOrderedSteps() {
    return this.getSubtasks().sort((a, b) => {
      const aIndex = Number.isFinite(a?.subtask_index) && a.subtask_index > 0 ? a.subtask_index : Number.MAX_SAFE_INTEGER;
      const bIndex = Number.isFinite(b?.subtask_index) && b.subtask_index > 0 ? b.subtask_index : Number.MAX_SAFE_INTEGER;
      if (aIndex !== bIndex) return aIndex - bIndex;
      const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
      const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
      if (aTime !== bTime) return aTime - bTime;
      return String(a?.id || '').localeCompare(String(b?.id || ''));
    });
  }

  hasResultForSteps() {
    const result = normalizeResultText(this.task?.result).trim();
    return Boolean(result);
  }

  renderWorkflow() {
    if (!this.elements.workflowCard) return;

    const steps = this.getOrderedSteps();
    const hasSteps = steps.length > 0;
    const canGenerate = !hasSteps && this.hasResultForSteps();
    const showCard = hasSteps || canGenerate || this.workflowDraftPending;

    if (!showCard) {
      this.elements.workflowCard.hidden = true;
      if (this.elements.workflowSteps) this.elements.workflowSteps.innerHTML = '';
      if (this.elements.workflowEmpty) this.elements.workflowEmpty.hidden = true;
      if (this.elements.workflowActions) this.elements.workflowActions.hidden = true;
      return;
    }

    this.elements.workflowCard.hidden = false;

    if (this.elements.workflowActions) {
      this.elements.workflowActions.hidden = !hasSteps;
    }
    if (this.elements.workflowRunAllBtn) {
      const anyRunning = steps.some((step) => String(step?.status || '') === 'in_progress');
      const anyUnassigned = steps.some((step) => !step.to || step.to === 'unassigned');
      this.elements.workflowRunAllBtn.disabled = anyRunning || anyUnassigned;
      this.elements.workflowRunAllBtn.title = anyUnassigned
        ? 'Assign all steps before running the workflow'
        : anyRunning
          ? 'A step is already running'
          : 'Run all steps in sequence';
    }

    if (this.elements.workflowEmpty) {
      if (hasSteps) {
        this.elements.workflowEmpty.hidden = true;
      } else if (this.workflowDraftPending) {
        this.elements.workflowEmpty.hidden = true;
      } else {
        this.elements.workflowEmpty.hidden = false;
      }
    }
    if (this.elements.workflowGenerateBtn) {
      this.elements.workflowGenerateBtn.disabled = this.workflowDraftPending || !canGenerate;
    }

    if (!this.elements.workflowSteps) return;

    if (this.workflowDraftPending && !hasSteps) {
      this.elements.workflowSteps.innerHTML = `
        <div class="workspace-task-workflow-generating">
          <span class="workspace-task-workflow-spinner" aria-hidden="true"></span>
          <span>Detecting steps from the result…</span>
        </div>
      `;
      return;
    }

    if (!hasSteps) {
      this.elements.workflowSteps.innerHTML = '';
      return;
    }

    this.elements.workflowSteps.innerHTML = steps
      .map((step, index) => this.renderStepRow(step, index + 1))
      .join('');
    this.bindStepRowEvents();
  }

  renderStepRow(step, fallbackNumber) {
    const stepId = String(step?.id || '');
    const status = getStatusClass(step?.status);
    const isRunning = status === 'in_progress';
    const isCompleted = status === 'completed';
    const isFailed = status === 'failed';
    const stepNumber = Number.isFinite(step?.subtask_index) && step.subtask_index > 0
      ? step.subtask_index
      : fallbackNumber;
    const title = String(step?.description || step?.name || `Step ${stepNumber}`).trim() || `Step ${stepNumber}`;
    const agentName = String(step?.to || '').trim();
    const isAssigned = agentName && agentName !== 'unassigned';
    const result = normalizeResultText(step?.result).trim();
    const error = String(step?.error || '').trim();

    const classes = ['workspace-task-workflow-step'];
    if (isRunning) classes.push('is-running');
    if (isCompleted) classes.push('is-completed');
    if (isFailed) classes.push('is-failed');

    const actionLabel = isRunning
      ? 'Running…'
      : isCompleted
        ? '↻ Re-run'
        : isFailed
          ? '↻ Retry'
          : '▶ Run';
    const actionDisabled = isRunning || !isAssigned;
    const actionTitle = !isAssigned
      ? 'Assign an agent to this step before running'
      : isRunning
        ? 'Step is already running'
        : isCompleted
          ? 'Run this step again'
          : 'Run this step now';

    const resultBlock = result || error
      ? `<details class="workspace-task-workflow-step-result"${(isRunning || isFailed) ? ' open' : ''}>
           <summary>${error ? 'Show error' : 'Show result'}</summary>
           <pre class="workspace-task-workflow-step-result-body${error ? ' workspace-task-workflow-step-error' : ''}">${this.escapeHtml(error || result)}</pre>
         </details>`
      : '';

    const stepHref = this.getTaskHref(stepId);

    return `
      <article class="${classes.join(' ')}" data-step-id="${this.escapeHtml(stepId)}">
        <div class="workspace-task-workflow-rail">
          <span class="workspace-task-workflow-step-pip">${stepNumber}</span>
        </div>
        <div class="workspace-task-workflow-step-body">
          <div class="workspace-task-workflow-step-header">
            <a href="${stepHref}" class="workspace-task-workflow-step-title" title="Open this step">${this.escapeHtml(title)}</a>
            <div class="workspace-task-workflow-step-actions">
              <button type="button"
                      class="modern-btn modern-btn-secondary workspace-task-workflow-step-action-btn"
                      data-action="run-step"
                      ${actionDisabled ? 'disabled' : ''}
                      title="${this.escapeHtml(actionTitle)}">
                ${actionLabel}
              </button>
              <button type="button"
                      class="modern-btn modern-btn-secondary workspace-task-workflow-step-action-btn"
                      data-action="delete-step"
                      title="Remove this step">×</button>
            </div>
          </div>
          <div class="workspace-task-workflow-step-meta">
            <span class="workspace-task-workflow-step-status" data-state="${this.escapeHtml(status)}">${this.escapeHtml(getDisplayStatus(step?.status))}</span>
            <span class="workspace-task-workflow-step-agent">
              <span class="workspace-task-workflow-step-agent-name">${this.escapeHtml(isAssigned ? agentName : 'Unassigned')}</span>
            </span>
          </div>
          ${resultBlock}
        </div>
      </article>
    `;
  }

  bindStepRowEvents() {
    if (!this.elements.workflowSteps) return;
    this.elements.workflowSteps.querySelectorAll('[data-step-id]').forEach((row) => {
      const stepId = row.getAttribute('data-step-id');
      if (!stepId) return;
      row.querySelector('[data-action="run-step"]')?.addEventListener('click', (event) => {
        event.preventDefault();
        this.handleRunStep(stepId);
      });
      row.querySelector('[data-action="delete-step"]')?.addEventListener('click', (event) => {
        event.preventDefault();
        this.handleDeleteStep(stepId);
      });
    });
  }

  async handleRunStep(stepId) {
    const id = String(stepId || '').trim();
    if (!id) return;
    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: id })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to run step');
      }
      this.notify('info', 'Step started');
      await this.refreshAfterStepChange();
      this.pollStepCompletion(id);
    } catch (error) {
      console.error('Failed to run step:', error);
      this.notify('error', error?.message || 'Failed to run step');
    }
  }

  async handleDeleteStep(stepId) {
    const id = String(stepId || '').trim();
    if (!id) return;
    if (!window.confirm('Delete this step? This cannot be undone.')) return;
    try {
      const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(id)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete step');
      this.notify('success', 'Step deleted');
      await this.refreshAfterStepChange();
    } catch (error) {
      console.error('Failed to delete step:', error);
      this.notify('error', error?.message || 'Failed to delete step');
    }
  }

  async handleRunAllSteps() {
    if (!this.task?.id) return;
    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: this.task.id })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to run workflow');
      }
      this.notify('info', 'Workflow started');
      await this.refreshAfterStepChange();
      this.pollStepCompletion(this.task.id);
    } catch (error) {
      console.error('Failed to run workflow:', error);
      this.notify('error', error?.message || 'Failed to run workflow');
    }
  }

  pollStepCompletion(taskId, maxAttempts = 60, intervalMs = 3000) {
    const id = String(taskId || '').trim();
    if (!id) return;
    if (this.workflowPollTimer) {
      clearTimeout(this.workflowPollTimer);
      this.workflowPollTimer = null;
    }
    let attempts = 0;
    const tick = async () => {
      attempts++;
      if (attempts > maxAttempts) return;
      try {
        await this.refreshAfterStepChange();
        const target = String(this.task?.id || '') === id
          ? this.task
          : this.tasks.find((t) => String(t?.id || '') === id);
        const status = String(target?.status || '');
        if (status === 'completed' || status === 'failed' || status === 'cancelled' || status === 'timeout') {
          this.workflowPollTimer = null;
          return;
        }
      } catch (_error) {
        // network blip — keep polling
      }
      this.workflowPollTimer = setTimeout(tick, intervalMs);
    };
    this.workflowPollTimer = setTimeout(tick, intervalMs);
  }

  async refreshAfterStepChange() {
    try {
      const [workspace, taskResponse] = await Promise.all([
        this.fetchWorkspace(),
        this.fetchTask().catch(() => null)
      ]);
      this.workspace = workspace || this.workspace;
      this.tasks = Array.isArray(workspace?.tasks) ? workspace.tasks : this.tasks;
      if (taskResponse) {
        this.task = taskResponse;
      }
      this.render();
    } catch (error) {
      console.error('Failed to refresh task data:', error);
    }
  }

  async handleAddStep() {
    const description = window.prompt('Step title');
    if (!description) return;
    const trimmed = description.trim();
    if (!trimmed) return;

    const fallbackAgent = String(this.task?.to || '').trim();
    const existingSteps = this.getOrderedSteps();
    const nextIndex = existingSteps.length + 1;

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          description: trimmed,
          to: fallbackAgent && fallbackAgent !== 'unassigned' ? fallbackAgent : '',
          parent_task_id: this.task?.id || '',
          subtask_index: nextIndex
        })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to add step');
      }
      this.notify('success', 'Step added');
      await this.refreshAfterStepChange();
    } catch (error) {
      console.error('Failed to add step:', error);
      this.notify('error', error?.message || 'Failed to add step');
    }
  }

  async handleGenerateSteps() {
    await this.createWorkflowDraftFromResult();
  }

  isStructuredData(text) {
    const trimmed = text.trim();
    if ((trimmed.startsWith('{') && trimmed.endsWith('}')) ||
        (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
      try { JSON.parse(trimmed); return true; } catch (_e) { /* not json */ }
    }
    return false;
  }

  renderMarkdownOrPre(text) {
    if (this.isStructuredData(text)) {
      return `<pre class="workspace-task-page-code-block">${this.escapeHtml(text)}</pre>`;
    }
    if (typeof marked !== 'undefined' && typeof marked.parse === 'function') {
      return `<div class="workspace-task-page-prose">${marked.parse(text)}</div>`;
    }
    return `<pre class="workspace-task-page-code-block">${this.escapeHtml(text)}</pre>`;
  }

  renderOutput() {
    if (!this.elements.output || !this.elements.outputCard) return;

    const result = normalizeResultText(this.task?.result).trim();
    const error = String(this.task?.error || '').trim();

    if (!result && !error) {
      this.elements.outputCard.hidden = true;
      this.elements.output.innerHTML = '';
      this.savedResultNote = null;
      this.savedResultNoteResult = '';
      this.updateResultActionButtons('', false);
      this.renderResultNoteStatus();
      return;
    }

    if (this.savedResultNote && result && this.savedResultNoteResult && this.savedResultNoteResult !== result) {
      this.savedResultNote = null;
      this.savedResultNoteResult = '';
    }

    const blocks = [];
    if (result) {
      blocks.push(`
        <div class="workspace-task-page-mini-label">Result</div>
        ${this.renderMarkdownOrPre(result)}
      `);
    }
    if (error) {
      blocks.push(`
        <div class="workspace-task-page-mini-label">Error</div>
        <pre class="workspace-task-page-code-block">${this.escapeHtml(error)}</pre>
      `);
    }

    this.elements.outputCard.hidden = false;
    this.elements.output.innerHTML = blocks.join('');
    this.updateResultActionButtons(result || error, Boolean(result));
    this.renderResultNoteStatus();
  }

  updateResultActionButtons(outputText, canSaveNote) {
    const hasOutput = Boolean(String(outputText || '').trim());
    if (this.elements.outputCopyBtn) {
      this.elements.outputCopyBtn.disabled = !hasOutput;
    }

    if (this.elements.outputSaveNoteBtn) {
      const label = this.elements.outputSaveNoteBtn.querySelector('span');
      if (label) {
        label.textContent = this.resultNoteSaving
          ? 'Saving...'
          : this.savedResultNote
            ? 'Saved'
            : 'Save as Note';
      }
      this.elements.outputSaveNoteBtn.disabled = !canSaveNote || this.resultNoteSaving || Boolean(this.savedResultNote);
      this.elements.outputSaveNoteBtn.classList.toggle('is-saved', Boolean(this.savedResultNote));
    }
  }

  renderResultNoteStatus() {
    if (!this.elements.outputNoteStatus) return;

    if (!this.savedResultNote) {
      this.elements.outputNoteStatus.hidden = true;
      this.elements.outputNoteStatus.innerHTML = '';
      return;
    }

    const noteId = String(this.savedResultNote?.id || '').trim();
    const noteName = String(this.savedResultNote?.name || 'Saved note').trim() || 'Saved note';
    this.elements.outputNoteStatus.hidden = false;
    this.elements.outputNoteStatus.innerHTML = `
      <div class="workspace-task-output-note-status-inner">
        <span class="workspace-task-output-note-status-icon" aria-hidden="true"><i class="bi bi-journal-check"></i></span>
        <span class="workspace-task-output-note-status-copy">Saved as <strong>${this.escapeHtml(noteName)}</strong></span>
        ${noteId ? `<button type="button" class="workspace-task-page-text-button workspace-task-output-note-open" data-action="open-result-note" data-note-id="${this.escapeHtml(noteId)}">Open note</button>` : ''}
      </div>
    `;

    this.elements.outputNoteStatus
      .querySelector('[data-action="open-result-note"]')
      ?.addEventListener('click', () => this.openSavedResultNote(noteId));
  }

  getCurrentResultText() {
    return normalizeResultText(this.task?.result).trim();
  }

  getCurrentOutputText() {
    return this.getCurrentResultText() || String(this.task?.error || '').trim();
  }

  async copyCurrentResult() {
    const outputText = this.getCurrentOutputText();
    if (!outputText) {
      this.notify('warning', 'No result is available to copy.');
      return;
    }

    await this.copyToClipboard(outputText, 'Result copied');
  }

  buildResultNoteTitle(resultText) {
    const heading = String(resultText || '')
      .split(/\r?\n/)
      .map((line) => {
        const match = line.match(/^\s{0,3}#{1,3}\s+(.+?)\s*#*\s*$/);
        return match ? String(match[1] || '').trim() : '';
      })
      .find(Boolean);

    const fallback = this.getTaskDisplayLabel();
    const cleaned = String(heading || fallback || 'Task Result')
      .replace(/\[(.*?)\]\((.*?)\)/g, '$1')
      .replace(/[*_`#>]/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();

    if (!cleaned) return 'Task Result';
    return cleaned.length > 96 ? `${cleaned.slice(0, 93).trim()}...` : cleaned;
  }

  buildResultNoteContent(resultText, title) {
    const taskTitle = this.getTaskDisplayLabel();
    const sourceHref = this.getTaskHref(this.taskId);
    const status = getDisplayStatus(this.task?.status);
    const savedAt = formatDateTime(new Date().toISOString());
    const completedAt = formatDateTime(this.task?.completed_at);
    const agent = String(this.task?.to || '').trim();
    const originalRequest = String(this.task?.description || '').trim();
    const assistMessage = String(this.task?.context?.user_assist_message || '').trim();

    const lines = [
      `# ${title}`,
      '',
      `Saved from Ori task result on ${savedAt}.`,
      '',
      `- Source task: [${taskTitle}](${sourceHref})`,
      `- Status: ${status}`
    ];

    if (agent) lines.push(`- Agent: ${agent}`);
    if (completedAt !== '—') lines.push(`- Completed: ${completedAt}`);

    if (originalRequest) {
      lines.push('', '## Original Request', '', originalRequest);
    }

    if (assistMessage) {
      lines.push('', '## Clarification', '', assistMessage);
    }

    lines.push('', '## Result', '', resultText);
    return lines.join('\n');
  }

  async saveCurrentResultAsNote() {
    if (this.resultNoteSaving || this.savedResultNote) return;

    const resultText = this.getCurrentResultText();
    if (!resultText) {
      this.notify('warning', 'No result is available to save.');
      return;
    }

    const title = this.buildResultNoteTitle(resultText);
    const content = this.buildResultNoteContent(resultText, title);
    this.resultNoteSaving = true;
    this.updateResultActionButtons(resultText, true);

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: title, content })
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to create note');
      }

      const payload = await response.json().catch(() => ({}));
      this.savedResultNote = payload?.note || { name: title };
      this.savedResultNoteResult = resultText;
      this.notify('success', 'Result saved as a note');
      this.renderResultNoteStatus();
    } catch (error) {
      console.error('Failed to save result as note:', error);
      this.notify('error', error?.message || 'Failed to save result as note');
    } finally {
      this.resultNoteSaving = false;
      this.updateResultActionButtons(resultText, true);
    }
  }

  async openSavedResultNote(noteId) {
    const id = String(noteId || '').trim();
    if (!id) return;

    const modalEl = document.getElementById('noteEditorModal');
    if (modalEl && window.sessionManager && typeof window.sessionManager.openNoteEditor === 'function') {
      try {
        await window.sessionManager.openNoteEditor(id);
        return;
      } catch (error) {
        console.error('Failed to open saved result note:', error);
        this.notify('warning', 'The note was saved, but the editor could not be opened.');
      }
    }

    window.location.href = `/workspaces/${encodeURIComponent(this.workspaceId)}#notes`;
  }

  getCurrentTaskAssignmentValue() {
    if (this.task?.assigned_node_id) {
      return `node:${this.task.assigned_node_id}`;
    }
    if (this.task?.to) {
      return `node:${this.task.to}-node-1`;
    }
    return '';
  }

  buildAssignmentValueForAgentName(agentName, fallbackValue = '') {
    const normalizedName = String(agentName || '').trim();
    if (!normalizedName) return fallbackValue;
    return `node:${normalizedName}-node-1`;
  }

  buildAutoParseWorkflowPrompt(resultText) {
    const taskLabel = this.getTaskDisplayLabel();
    return [
      'Turn this completed task result into a concrete workflow draft.',
      'Prefer one executable step per numbered step or checklist item.',
      'Keep notes, tips, materials, and reference sections in the parent task details instead of separate executable steps when possible.',
      `Original task title: ${taskLabel}`,
      '',
      'Task result:',
      resultText
    ].join('\n');
  }

  async buildWorkflowDraftFromAutoParse(resultText, fallbackAssignmentValue = '') {
    const response = await fetch('/api/orchestration/tasks/auto-parse', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: this.workspaceId,
        description: this.buildAutoParseWorkflowPrompt(resultText)
      })
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to draft workflow from result');
    }

    const parsed = await response.json();
    const steps = Array.isArray(parsed?.tasks) ? parsed.tasks.filter(Boolean) : [];
    if (steps.length === 0) {
      return null;
    }

    const limitedSteps = steps.slice(0, RESULT_WORKFLOW_MAX_SUBTASKS);
    const stepRefById = new Map();
    limitedSteps.forEach((step, index) => {
      stepRefById.set(String(step?.id || `step-${index + 1}`), `step:${index + 1}`);
    });

    const subtasks = limitedSteps.map((step, index) => {
      const dependsOn = Array.isArray(step?.depends_on) ? step.depends_on.map((value) => String(value || '').trim()).filter(Boolean) : [];
      const inputTaskIds = [];

      dependsOn.forEach((stepId) => {
        const ref = stepRefById.get(stepId);
        if (ref && !inputTaskIds.includes(ref)) {
          inputTaskIds.push(ref);
        }
      });

      if (inputTaskIds.length === 0) {
        if (index === 0 && this.task?.id) {
          inputTaskIds.push(`task:${this.task.id}`);
        } else if (index > 0) {
          inputTaskIds.push(`step:${index}`);
        }
      }

      const detailParts = [];
      if (step?.details) {
        detailParts.push(String(step.details).trim());
      }
      if (dependsOn.length > 0) {
        detailParts.push(`Depends on: ${dependsOn.join(', ')}`);
      }

      return {
        description: trimResultWorkflowLabel(step?.title || step?.description || `Step ${index + 1}`, 180) || `Step ${index + 1}`,
        details: detailParts.join('\n'),
        assignmentValue: this.buildAssignmentValueForAgentName(step?.agent_name, fallbackAssignmentValue),
        inputTaskIds
      };
    });

    const detailParts = [];
    if (parsed?.details) {
      detailParts.push(String(parsed.details).trim());
    }
    if (parsed?.reasoning) {
      detailParts.push(`Draft rationale: ${String(parsed.reasoning).trim()}`);
    }
    detailParts.push('Review the generated steps, assignments, and dependencies before saving.');
    if (steps.length > RESULT_WORKFLOW_MAX_SUBTASKS) {
      detailParts.push(`${steps.length - RESULT_WORKFLOW_MAX_SUBTASKS} additional parsed step${steps.length - RESULT_WORKFLOW_MAX_SUBTASKS === 1 ? '' : 's'} were omitted from the draft.`);
    }

    return {
      title: buildResultWorkflowDraftTitle(parsed?.title || this.getTaskDisplayLabel()),
      details: detailParts.filter(Boolean).join('\n'),
      priority: Number.isInteger(parsed?.priority) ? parsed.priority : 3,
      assignmentValue: this.buildAssignmentValueForAgentName(parsed?.agent_name, fallbackAssignmentValue),
      subtasks
    };
  }

  async deriveWorkflowDraftFromResult(resultText) {
    const fallbackAssignmentValue = this.getCurrentTaskAssignmentValue();
    const deterministicDraft = buildResultWorkflowDraft(
      this.getTaskDisplayLabel(),
      resultText,
      this.task?.id,
      fallbackAssignmentValue
    );

    if (deterministicDraft && deterministicDraft.subtasks.length >= 2) {
      return deterministicDraft;
    }

    try {
      const autoParsedDraft = await this.buildWorkflowDraftFromAutoParse(resultText, fallbackAssignmentValue);
      if (autoParsedDraft && autoParsedDraft.subtasks.length > 0) {
        return autoParsedDraft;
      }
    } catch (error) {
      console.debug('Auto-parse workflow draft unavailable:', error);
    }

    return deterministicDraft;
  }

  agentNameFromAssignmentValue(value) {
    const v = String(value || '').trim();
    if (!v.startsWith('node:')) return '';
    return v.slice('node:'.length).replace(/-node-\d+$/, '');
  }

  async createWorkflowDraftFromResult() {
    if (this.workflowDraftPending) return;
    if (!this.task?.id) {
      this.notify('error', 'Task is not loaded yet.');
      return;
    }

    const resultText = normalizeResultText(this.task?.result).trim();
    if (!resultText) {
      this.notify('warning', 'No task result is available to turn into steps.');
      return;
    }

    if (this.getOrderedSteps().length > 0) {
      this.notify('warning', 'This task already has steps.');
      return;
    }

    this.workflowDraftPending = true;
    this.renderWorkflow();

    try {
      const draft = await this.deriveWorkflowDraftFromResult(resultText);
      if (!draft || !Array.isArray(draft.subtasks) || draft.subtasks.length === 0) {
        throw new Error('Could not detect actionable steps in this result yet.');
      }

      const fallbackAgent = String(this.task?.to || '').trim();
      const parentTaskId = String(this.task.id);
      let createdCount = 0;

      for (let index = 0; index < draft.subtasks.length; index++) {
        const subtask = draft.subtasks[index];
        const description = String(subtask?.description || '').trim() || `Step ${index + 1}`;
        const details = String(subtask?.details || '').trim();
        const agentFromDraft = this.agentNameFromAssignmentValue(subtask?.assignmentValue);
        const to = agentFromDraft || (fallbackAgent && fallbackAgent !== 'unassigned' ? fallbackAgent : '');

        const response = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            workspace_id: this.workspaceId,
            description,
            details,
            to,
            parent_task_id: parentTaskId,
            subtask_index: index + 1,
            priority: 3
          })
        });

        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || `Failed to create step ${index + 1}`);
        }
        createdCount += 1;
      }

      this.notify('success', `Added ${createdCount} step${createdCount === 1 ? '' : 's'} to this task.`);
      await this.refreshAfterStepChange();
    } catch (error) {
      console.error('Failed to generate steps from result:', error);
      this.notify('error', error?.message || 'Failed to generate steps');
    } finally {
      this.workflowDraftPending = false;
      this.renderWorkflow();
    }
  }

  renderSchedule() {
    if (!this.elements.schedule || !this.elements.scheduleCard) return;

    const isScheduled = this.task?.schedule_enabled || this.task?.schedule;
    const history = Array.isArray(this.task?.execution_history) ? this.task.execution_history : [];
    const executionCount = Number(this.task?.execution_count) || 0;
    const failureCount = Number(this.task?.failure_count) || 0;

    if (!isScheduled && history.length === 0 && executionCount === 0) {
      this.elements.scheduleCard.hidden = true;
      this.elements.schedule.innerHTML = '';
      return;
    }

    this.elements.scheduleCard.hidden = false;

    const stats = [];
    stats.push({ label: 'Total Runs', value: String(executionCount) });
    stats.push({ label: 'Failures', value: String(failureCount) });
    if (this.task?.next_run) {
      stats.push({ label: 'Next Run', value: formatDateTime(this.task.next_run) });
    }
    if (this.task?.last_run) {
      stats.push({ label: 'Last Run', value: formatDateTime(this.task.last_run) });
    }

    const statsHtml = `
      <div class="workspace-task-schedule-stats">
        ${stats.map((s) => `
          <div class="workspace-task-schedule-stat">
            <div class="workspace-task-schedule-stat-label">${this.escapeHtml(s.label)}</div>
            <div class="workspace-task-schedule-stat-value">${this.escapeHtml(s.value)}</div>
          </div>
        `).join('')}
      </div>
    `;

    let historyHtml = '';
    if (history.length > 0) {
      const recentRuns = history.slice(-10).reverse();
      historyHtml = `
        <div class="workspace-task-page-mini-label">Recent runs</div>
        <div class="workspace-task-schedule-history">
          ${recentRuns.map((run) => {
            const runStatus = String(run?.status || 'completed').trim().toLowerCase();
            const statusClass = getStatusClass(runStatus);
            return `
              <div class="workspace-task-schedule-run">
                <span>${this.escapeHtml(formatDateTime(run?.completed_at || run?.started_at))}</span>
                <span class="workspace-task-schedule-run-status" data-state="${this.escapeHtml(statusClass)}">${this.escapeHtml(getDisplayStatus(runStatus))}</span>
              </div>
            `;
          }).join('')}
        </div>
      `;
    }

    this.elements.schedule.innerHTML = statsHtml + historyHtml;
  }

  renderExecutionSteps() {
    if (!this.elements.steps || !this.elements.stepsCard) return;

    const steps = Array.isArray(this.task?.execution_steps) ? this.task.execution_steps : [];
    if (steps.length === 0) {
      this.elements.stepsCard.hidden = true;
      this.elements.steps.innerHTML = '';
      return;
    }

    this.elements.stepsCard.hidden = false;
    this.elements.steps.innerHTML = steps.map((step, index) => {
      const resultText = String(step?.result || '').trim();
      const errorText = String(step?.error || '').trim();
      const isLongResult = resultText.length > 400;
      const isLongError = errorText.length > 400;

      return `
        <article class="workspace-task-step">
          <div class="workspace-task-step-index">${index + 1}</div>
          <div>
            <div class="workspace-task-step-title-row">
              <span class="workspace-task-step-title">${this.escapeHtml(String(step?.title || `Step ${index + 1}`).trim())}</span>
              ${step?.tag ? `<span class="workspace-task-step-tag">${this.escapeHtml(String(step.tag).trim())}</span>` : ''}
              <span class="workspace-task-step-status" data-state="${this.escapeHtml(getStatusClass(step?.status))}">${this.escapeHtml(getDisplayStatus(step?.status))}</span>
            </div>
            ${step?.detail ? `<div class="workspace-task-step-copy">${this.escapeHtml(String(step.detail).trim())}</div>` : ''}
            ${resultText ? (isLongResult
              ? `<details class="workspace-task-collapsible mt-2"><summary class="workspace-task-collapsible-toggle">Show result</summary><pre class="workspace-task-page-code-block">${this.escapeHtml(resultText)}</pre></details>`
              : `<pre class="workspace-task-page-code-block mt-2">${this.escapeHtml(resultText)}</pre>`)
            : ''}
            ${errorText ? (isLongError
              ? `<details class="workspace-task-collapsible mt-2"><summary class="workspace-task-collapsible-toggle">Show error</summary><pre class="workspace-task-page-code-block">${this.escapeHtml(errorText)}</pre></details>`
              : `<pre class="workspace-task-page-code-block mt-2">${this.escapeHtml(errorText)}</pre>`)
            : ''}
          </div>
        </article>
      `;
    }).join('');
  }

  renderContext() {
    if (!this.elements.context || !this.elements.contextCard) return;

    const context = this.task?.context && typeof this.task.context === 'object'
      ? { ...this.task.context }
      : {};
    delete context.human_loop;

    const contextText = Object.keys(context).length > 0
      ? normalizeResultText(context)
      : '';

    if (!contextText) {
      this.elements.contextCard.hidden = true;
      this.elements.context.innerHTML = '';
      return;
    }

    this.elements.contextCard.hidden = false;
    this.elements.context.innerHTML = `
      <pre class="workspace-task-page-code-block">${this.escapeHtml(contextText)}</pre>
    `;
  }

  renderBlockedState(statusInfo) {
    const blocked = statusInfo.isBlocked && this.currentBlockedTask;

    if (!blocked) {
      if (this.elements.assistCard) this.elements.assistCard.hidden = true;
      if (this.elements.blockedContextCard) this.elements.blockedContextCard.hidden = true;
      return;
    }

    this.renderBlockedContext();
    this.renderAssistCard();
  }

  renderBlockedContext() {
    if (!this.elements.blockedContextCard) return;

    const response = String(this.currentBlockedTask?.response || '').trim();
    const responsePreview = summarizeText(response, 260);

    this.elements.blockedContextCard.hidden = false;
    if (this.elements.blockedReason) {
      this.elements.blockedReason.textContent = this.currentBlockedTask?.reason || 'This task is waiting for your input.';
    }

    if (this.elements.blockedRequestWrap) {
      const hasResponse = Boolean(response);
      this.elements.blockedRequestWrap.classList.toggle('d-none', !hasResponse);
    }
    if (this.elements.blockedRequestPreview) {
      this.elements.blockedRequestPreview.textContent = responsePreview || '';
    }
    if (this.elements.blockedRequest) {
      this.elements.blockedRequest.textContent = response || '';
      this.elements.blockedRequest.classList.toggle('d-none', !this.taskAssistResponseExpanded || !response);
    }
    if (this.elements.blockedRequestToggle) {
      const hasLongResponse = response.length > 0 && response !== responsePreview;
      this.elements.blockedRequestToggle.classList.toggle('d-none', !hasLongResponse);
      this.elements.blockedRequestToggle.textContent = this.taskAssistResponseExpanded ? 'Hide full request' : 'View full request';
      this.elements.blockedRequestToggle.setAttribute('aria-expanded', this.taskAssistResponseExpanded ? 'true' : 'false');
    }
  }

  renderAssistCard() {
    if (!this.elements.assistCard) return;

    const workflowStep = this.currentBlockedTask?.workflowStep || null;
    this.elements.assistCard.hidden = false;

    if (this.elements.assistKnown) {
      this.elements.assistKnown.textContent = summarizeText(
        this.currentBlockedTask?.response || this.currentBlockedTask?.reason || 'The task is paused waiting on your input.',
        190
      ) || 'The task is paused waiting on your input.';
    }
    if (this.elements.assistNeeds) {
      this.elements.assistNeeds.textContent = this.getAssistNeedsSummary(workflowStep);
    }
    if (this.elements.assistNext) {
      this.elements.assistNext.textContent = this.getAssistNextSummary(workflowStep);
    }
    if (this.elements.assistQuestionWrap) {
      const showQuestion = Boolean(this.currentBlockedTask?.question) && workflowStep?.stepType !== 'ask_form';
      this.elements.assistQuestionWrap.classList.toggle('d-none', !showQuestion);
    }
    if (this.elements.assistQuestion) {
      this.elements.assistQuestion.textContent = this.currentBlockedTask?.question || '';
    }
    if (this.elements.assistContinueBtn) {
      const primaryLabel = this.getAssistPrimaryActionLabel(workflowStep);
      this.elements.assistContinueBtn.textContent = primaryLabel;
      this.elements.assistContinueBtn.setAttribute('aria-label', primaryLabel);
      this.elements.assistContinueBtn.hidden = !this.isAssistActionSuggested('continue_with_instruction');
    }
    if (this.elements.assistMessage) {
      this.elements.assistMessage.placeholder = this.getAssistMessagePlaceholder(workflowStep);
    }
    if (this.elements.assistRetryBtn) {
      this.elements.assistRetryBtn.hidden = !this.isAssistActionSuggested('retry');
    }
    if (this.elements.assistFailBtn) {
      this.elements.assistFailBtn.hidden = !this.isAssistActionSuggested('mark_failed');
    }
    if (this.elements.assistSwitchWrap) {
      this.elements.assistSwitchWrap.hidden = !this.isAssistActionSuggested('switch_agent_retry');
    }
    if (this.elements.assistMoreActions) {
      const hasMoreActions = this.isAssistActionSuggested('switch_agent_retry') ||
        this.isAssistActionSuggested('retry') ||
        this.isAssistActionSuggested('mark_failed');
      this.elements.assistMoreActions.hidden = !hasMoreActions;
      if (!hasMoreActions) {
        this.elements.assistMoreActions.open = false;
      }
    }

    this.populateAssistAgents(this.currentBlockedTask?.currentAgent || '');
    this.renderWorkflowStepUI(workflowStep);
    this.updateAssistSwitchButtonState();
  }

  getAssistNeedsSummary(workflowStep) {
    if (this.currentBlockedTask?.reasonCode === 'assigned_agent_missing') {
      return 'Pick a runnable agent for this task before you retry execution.';
    }
    if (workflowStep?.stepType === 'ask_form' && Array.isArray(workflowStep.fields) && workflowStep.fields.length > 0) {
      return `Answer ${workflowStep.fields.length} question${workflowStep.fields.length === 1 ? '' : 's'} so the agent can continue.`;
    }
    if (workflowStep?.stepType === 'ask_choice' && Array.isArray(workflowStep.choices) && workflowStep.choices.length > 0) {
      return `Choose 1 of ${workflowStep.choices.length} next-step options or add your own guidance.`;
    }
    if (this.currentBlockedTask?.question) {
      return summarizeText(this.currentBlockedTask.question, 180);
    }
    return 'Add the missing detail the agent asked for.';
  }

  getAssistNextSummary(workflowStep) {
    if (this.currentBlockedTask?.reasonCode === 'assigned_agent_missing') {
      return 'Use the reassignment control to switch this task to an available agent.';
    }
    if (workflowStep?.stepType === 'ask_form') {
      return 'Continue sends your answers and any extra guidance back to the assigned agent.';
    }
    if (workflowStep?.stepType === 'ask_choice') {
      return 'Pick the best path, optionally add guidance, then continue the task.';
    }
    return 'Retry, continue with guidance, switch agents, or mark the task failed.';
  }

  getAssistPrimaryActionLabel(workflowStep) {
    if (workflowStep?.stepType === 'ask_form') {
      return 'Send Answers';
    }
    if (workflowStep?.stepType === 'ask_choice') {
      return this.currentBlockedTask?.selectedChoiceId ? 'Continue With Selected Path' : 'Send Choice Or Guidance';
    }
    if (this.currentBlockedTask?.question) {
      return 'Send Guidance';
    }
    return 'Continue Task';
  }

  getAssistMessagePlaceholder(workflowStep) {
    if (this.currentBlockedTask?.reasonCode === 'assigned_agent_missing') {
      return 'Optional context to send along with the new agent assignment...';
    }
    if (workflowStep?.stepType === 'ask_form') {
      return 'Add any constraints or context the form does not cover...';
    }
    if (workflowStep?.stepType === 'ask_choice') {
      return 'Add preferences, constraints, or context before the agent continues...';
    }
    return 'Add clarification, constraints, or context before continuing...';
  }

  populateAssistAgents(currentAgent = '') {
    if (!this.elements.assistAgent) return;

    const currentNormalized = String(currentAgent || '').trim();
    const currentUnavailable = Boolean(currentNormalized) && !this.isRunnableAgentName(currentNormalized);
    const options = [
      currentUnavailable
        ? '<option value="" selected>Select an available agent</option>'
        : '<option value="">Keep current assignment</option>'
    ];

    if (currentUnavailable) {
      options.push(`<option value="${this.escapeHtml(currentNormalized)}" disabled>${this.escapeHtml(`${currentNormalized} (Current assignment unavailable)`)}</option>`);
    }

    this.getAssignableAgentNames(currentAgent).forEach((agentName) => {
      const normalized = String(agentName || '').trim();
      if (!normalized || normalized.toLowerCase() === currentNormalized.toLowerCase()) return;
      options.push(`<option value="${this.escapeHtml(normalized)}">${this.escapeHtml(normalized)}</option>`);
    });

    this.elements.assistAgent.innerHTML = options.join('');
  }

  renderWorkflowStepUI(workflowStep) {
    if (!this.elements.assistFormWrap || !this.elements.assistFormFields) return;

    if (!workflowStep) {
      this.assistActiveFieldId = '';
      this.assistReviewMode = false;
      this.elements.assistFormWrap.classList.add('d-none');
      this.elements.assistFormFields.innerHTML = '';
      return;
    }

    this.elements.assistFormWrap.classList.remove('d-none');

    if (workflowStep.stepType === 'ask_choice') {
      this.assistActiveFieldId = '';
      this.assistReviewMode = false;
      this.renderChoiceWorkflow(workflowStep);
      return;
    }

    if (workflowStep.stepType === 'ask_form') {
      this.renderFormWorkflow(workflowStep);
      return;
    }

    this.elements.assistFormWrap.classList.add('d-none');
    this.elements.assistFormFields.innerHTML = '';
  }

  renderChoiceWorkflow(workflowStep) {
    const selectedChoiceId = String(this.currentBlockedTask?.selectedChoiceId || '').trim();
    this.elements.assistFormFields.innerHTML = `
      <div class="workspace-task-assist-option-group" role="radiogroup" aria-label="Choose a next step">
        ${workflowStep.choices.map((choice, index) => `
          <button
            type="button"
            class="workspace-task-assist-option${selectedChoiceId === choice.id ? ' is-selected' : ''}"
            data-assist-choice-id="${this.escapeHtml(choice.id)}"
            aria-pressed="${selectedChoiceId === choice.id ? 'true' : 'false'}">
            <span class="workspace-task-assist-option-card">
              <span class="workspace-task-assist-option-key">${this.escapeHtml(choice.number || String.fromCharCode(65 + (index % 26)))}</span>
              <span class="workspace-task-assist-option-copy">
                <span class="workspace-task-assist-option-label">${this.escapeHtml(choice.label)}</span>
                ${choice.description ? `<span class="workspace-task-assist-option-description">${this.escapeHtml(choice.description)}</span>` : ''}
              </span>
            </span>
          </button>
        `).join('')}
      </div>
    `;

    this.elements.assistFormFields.querySelectorAll('[data-assist-choice-id]').forEach((button) => {
      button.addEventListener('click', () => {
        const choiceId = String(button.getAttribute('data-assist-choice-id') || '').trim();
        const choice = workflowStep.choices.find((item) => item.id === choiceId);
        if (!choice || !this.currentBlockedTask) return;
        this.currentBlockedTask.selectedChoiceId = choice.id;
        this.currentBlockedTask.selectedChoiceLabel = choice.label;
        this.currentBlockedTask.selectedChoiceNumber = choice.number || '';
        this.renderChoiceWorkflow(workflowStep);
      });
    });
  }

  renderFormWorkflow(workflowStep) {
    const selectedValues = this.currentBlockedTask?.selectedFieldValues || {};
    const fields = Array.isArray(workflowStep.fields) ? workflowStep.fields.filter(Boolean) : [];
    if (fields.length === 0) {
      this.assistActiveFieldId = '';
      this.assistReviewMode = false;
      this.elements.assistFormFields.innerHTML = '';
      return;
    }

    const currentFieldIndex = fields.findIndex((field) => field.id === this.assistActiveFieldId);
    const firstUnansweredIndex = fields.findIndex((field) => !String(selectedValues[field.id] || '').trim());
    const activeIndex = currentFieldIndex >= 0
      ? currentFieldIndex
      : (firstUnansweredIndex >= 0 ? firstUnansweredIndex : 0);
    const activeField = fields[activeIndex];
    const answeredCount = fields.filter((field) => String(selectedValues[field.id] || '').trim()).length;
    const progressPercent = fields.length > 0 ? Math.round((answeredCount / fields.length) * 100) : 0;
    const reviewMode = fields.length > 1 && this.assistReviewMode;

    this.assistActiveFieldId = activeField?.id || '';

    if (fields.length === 1) {
      this.assistReviewMode = false;
      this.elements.assistFormFields.innerHTML = this.renderAssistFieldMarkup(
        activeField,
        activeIndex,
        String(selectedValues[activeField.id] || '').trim()
      );
    } else {
      this.elements.assistFormFields.innerHTML = `
        <div class="workspace-task-assist-deck">
          <div class="workspace-task-assist-deck-rail" aria-label="Blocked task questions">
            ${fields.map((field, index) => {
              const fieldValue = String(selectedValues[field.id] || '').trim();
              const isActive = index === activeIndex;
              const isAnswered = Boolean(fieldValue);
              const meta = field.type === 'select' && Array.isArray(field.options)
                ? `${field.options.length} choice${field.options.length === 1 ? '' : 's'}`
                : field.type === 'number'
                  ? 'Number'
                  : field.type === 'textarea'
                    ? 'Detailed answer'
                    : 'Short answer';

              return `
                <button
                  type="button"
                  class="workspace-task-assist-deck-tab${isActive ? ' is-active' : ''}${isAnswered ? ' is-answered' : ''}"
                  data-assist-field-tab="${this.escapeHtml(field.id)}"
                  aria-pressed="${isActive ? 'true' : 'false'}">
                  <span class="workspace-task-assist-deck-tab-number">${index + 1}</span>
                  <span class="workspace-task-assist-deck-tab-copy">
                    <span class="workspace-task-assist-deck-tab-title">${this.escapeHtml(this.getAssistFieldPrompt(field))}</span>
                    <span class="workspace-task-assist-deck-tab-meta">${this.escapeHtml(meta)}</span>
                  </span>
                  <span class="workspace-task-assist-deck-tab-state">${isAnswered ? 'Answered' : 'Open'}</span>
                </button>
              `;
            }).join('')}
          </div>

          <div class="workspace-task-assist-deck-panel">
            <div class="workspace-task-assist-deck-progress-row">
              <div>
                <div class="workspace-task-assist-deck-kicker">${reviewMode ? 'Final Review' : `Question ${activeIndex + 1} of ${fields.length}`}</div>
                <div class="workspace-task-assist-deck-progress-copy" data-assist-progress-count>${reviewMode
                  ? (answeredCount === fields.length ? 'All answers captured' : `${fields.length - answeredCount} answers still missing`)
                  : `${answeredCount} answered so far`}</div>
              </div>
              <div class="workspace-task-assist-deck-progress-bar" aria-hidden="true">
                <span data-assist-progress-fill style="width: ${progressPercent}%"></span>
              </div>
            </div>

            ${reviewMode
              ? this.renderAssistReviewMarkup(fields)
              : `<div class="workspace-task-assist-deck-stage">
                  ${this.renderAssistFieldMarkup(activeField, activeIndex, String(selectedValues[activeField.id] || '').trim(), { active: true })}
                </div>`}

            <div class="workspace-task-assist-deck-nav">
              <button type="button" class="modern-btn modern-btn-secondary" data-assist-field-nav="${reviewMode ? 'review-back' : 'prev'}" ${reviewMode ? '' : (activeIndex === 0 ? 'disabled' : '')}>${reviewMode ? 'Back To Questions' : 'Previous'}</button>
              ${reviewMode
                ? '<div class="workspace-task-assist-deck-nav-note">Use Send Answers below or edit any item in this review.</div>'
                : `<button type="button" class="modern-btn modern-btn-secondary" data-assist-field-nav="next">${activeIndex === fields.length - 1 ? 'Review Answers' : 'Next Question'}</button>`}
            </div>
          </div>
        </div>
      `;
    }

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-tab]').forEach((button) => {
      button.addEventListener('click', () => {
        this.assistReviewMode = false;
        this.assistActiveFieldId = String(button.getAttribute('data-assist-field-tab') || '').trim();
        this.renderFormWorkflow(workflowStep);
        this.focusActiveAssistField();
      });
    });

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-nav]').forEach((button) => {
      button.addEventListener('click', () => {
        const direction = String(button.getAttribute('data-assist-field-nav') || '').trim();
        if (direction === 'review-back') {
          this.assistReviewMode = false;
          this.renderFormWorkflow(workflowStep);
          this.focusActiveAssistField();
          return;
        }
        if (direction === 'next' && activeIndex === fields.length - 1) {
          this.assistReviewMode = true;
          this.renderFormWorkflow(workflowStep);
          this.focusActiveAssistField();
          return;
        }
        const step = direction === 'prev' ? -1 : 1;
        const nextIndex = Math.max(0, Math.min(fields.length - 1, activeIndex + step));
        this.assistReviewMode = false;
        this.assistActiveFieldId = fields[nextIndex]?.id || this.assistActiveFieldId;
        this.renderFormWorkflow(workflowStep);
        this.focusActiveAssistField();
      });
    });

    this.elements.assistFormFields.querySelectorAll('[data-assist-review-edit-id]').forEach((button) => {
      button.addEventListener('click', () => {
        this.assistReviewMode = false;
        this.assistActiveFieldId = String(button.getAttribute('data-assist-review-edit-id') || '').trim();
        this.renderFormWorkflow(workflowStep);
        this.focusActiveAssistField();
      });
    });

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-id]').forEach((fieldElement) => {
      const syncValue = () => {
        const fieldId = String(fieldElement.getAttribute('data-assist-field-id') || '').trim();
        if (!fieldId) return;
        if (fieldElement instanceof HTMLInputElement && fieldElement.type === 'radio' && !fieldElement.checked) {
          return;
        }
        if (fieldElement instanceof HTMLInputElement && fieldElement.type === 'radio') {
          const optionValue = String(fieldElement.getAttribute('data-assist-option-value') || fieldElement.value || '').trim();
          const isCustomOption = String(fieldElement.getAttribute('data-assist-custom-option') || '').trim() === 'true';
          this.setAssistFormFieldOptionValue(fieldId, optionValue);
          if (isCustomOption) {
            const existingCustomValue = String(this.currentBlockedTask?.selectedFieldCustomValues?.[fieldId] || '').trim();
            this.setAssistFormFieldValue(fieldId, existingCustomValue);
          } else {
            this.setAssistFormCustomFieldValue(fieldId, '');
            this.setAssistFormFieldValue(fieldId, optionValue);
          }
          this.renderFormWorkflow(workflowStep);
          this.syncAssistFormProgress(workflowStep);
          this.focusActiveAssistField();
          return;
        }
        this.setAssistFormFieldValue(fieldId, fieldElement.value);
        this.syncAssistFormProgress(workflowStep);
      };

      fieldElement.addEventListener('input', syncValue);
      fieldElement.addEventListener('change', syncValue);
    });

    this.elements.assistFormFields.querySelectorAll('[data-assist-custom-field-id]').forEach((fieldElement) => {
      const syncCustomValue = () => {
        const fieldId = String(fieldElement.getAttribute('data-assist-custom-field-id') || '').trim();
        if (!fieldId) return;
        this.setAssistFormCustomFieldValue(fieldId, fieldElement.value);
        this.setAssistFormFieldValue(fieldId, fieldElement.value);
        this.syncAssistFormProgress(workflowStep);
      };

      fieldElement.addEventListener('input', syncCustomValue);
      fieldElement.addEventListener('change', syncCustomValue);
    });
  }

  renderAssistFieldMarkup(field, index, value, { active = false } = {}) {
    if (!field) return '';

    const requiredMark = field.required ? ' <span aria-hidden="true">*</span>' : '';
    const questionIntro = `
      <div class="workspace-task-assist-field-question">
        <span class="workspace-task-assist-field-number">${index + 1}</span>
        <div>
          <div class="workspace-task-assist-field-prompt">${this.escapeHtml(this.getAssistFieldPrompt(field))}</div>
          ${field.description && this.getAssistFieldPrompt(field) !== field.description
            ? `<div class="workspace-task-assist-field-hint">${this.escapeHtml(field.description)}</div>`
            : ''}
          ${field.evidence ? `<div class="workspace-task-assist-field-evidence">${this.escapeHtml(field.evidence)}</div>` : ''}
        </div>
      </div>
    `;

    if (field.type === 'select' && Array.isArray(field.options) && field.options.length > 0) {
      const selectedOptionValue = String(this.currentBlockedTask?.selectedFieldOptionValues?.[field.id] || '').trim();
      const customValue = String(this.currentBlockedTask?.selectedFieldCustomValues?.[field.id] || '').trim();
      const otherOption = field.options.find((option) => isAssistOtherOption(option));
      const otherOptionValue = String(otherOption?.value || otherOption?.label || '').trim();
      const showCustomInput = Boolean(otherOptionValue) && selectedOptionValue === otherOptionValue;

      return `
        <article class="workspace-task-assist-field${active ? ' is-active' : ''}">
          ${questionIntro}
          <div class="workspace-task-assist-option-group" role="radiogroup" aria-label="${this.escapeHtml(field.label)}">
            ${field.options.map((option) => `
              <label class="workspace-task-assist-option">
                <input
                  class="workspace-task-assist-option-input"
                  type="radio"
                  name="workspace-task-assist-field-${this.escapeHtml(field.id)}"
                  value="${this.escapeHtml(option.value)}"
                  data-assist-field-id="${this.escapeHtml(field.id)}"
                  data-assist-option-value="${this.escapeHtml(option.value)}"
                  data-assist-custom-option="${isAssistOtherOption(option) ? 'true' : 'false'}"
                  ${selectedOptionValue === option.value ? 'checked' : ''}>
                <span class="workspace-task-assist-option-card">
                  <span class="workspace-task-assist-option-key">${this.escapeHtml(option.key || option.value)}</span>
                  <span class="workspace-task-assist-option-copy">
                    <span class="workspace-task-assist-option-label">${this.escapeHtml(option.label)}</span>
                    ${option.description ? `<span class="workspace-task-assist-option-description">${this.escapeHtml(option.description)}</span>` : ''}
                  </span>
                </span>
              </label>
            `).join('')}
          </div>
          ${showCustomInput ? `
            <div class="workspace-task-assist-custom-input">
              <label class="form-label" for="workspace-task-custom-field-${this.escapeHtml(field.id)}">Tell the agent the right answer</label>
              <input
                id="workspace-task-custom-field-${this.escapeHtml(field.id)}"
                class="form-control"
                type="text"
                data-assist-custom-field-id="${this.escapeHtml(field.id)}"
                value="${this.escapeHtml(customValue)}"
                placeholder="Type the custom answer here...">
            </div>
          ` : ''}
        </article>
      `;
    }

    if (field.type === 'textarea') {
      return `
        <article class="workspace-task-assist-field${active ? ' is-active' : ''}">
          ${questionIntro}
          <label class="form-label" for="workspace-task-field-${this.escapeHtml(field.id)}">${this.escapeHtml(field.label)}${requiredMark}</label>
          <textarea id="workspace-task-field-${this.escapeHtml(field.id)}" class="form-control" rows="3" data-assist-field-id="${this.escapeHtml(field.id)}" placeholder="${this.escapeHtml(field.placeholder || 'Type your answer...')}">${this.escapeHtml(value)}</textarea>
        </article>
      `;
    }

    const inputType = field.type === 'number' ? 'number' : 'text';
    return `
      <article class="workspace-task-assist-field${active ? ' is-active' : ''}">
        ${questionIntro}
        <label class="form-label" for="workspace-task-field-${this.escapeHtml(field.id)}">${this.escapeHtml(field.label)}${requiredMark}</label>
        <input id="workspace-task-field-${this.escapeHtml(field.id)}" class="form-control" type="${inputType}" data-assist-field-id="${this.escapeHtml(field.id)}" value="${this.escapeHtml(value)}" placeholder="${this.escapeHtml(field.placeholder || 'Type your answer...')}">
      </article>
    `;
  }

  renderAssistReviewMarkup(fields) {
    const answeredCount = fields.filter((field) => Boolean(this.getAssistFieldAnswerValue(field))).length;

    return `
      <div class="workspace-task-assist-review">
        <div class="workspace-task-assist-review-banner">
          <div class="workspace-task-assist-review-title">Review what will be sent back to the agent.</div>
          <div class="workspace-task-assist-review-copy">${answeredCount === fields.length
            ? 'Everything requested has an answer. You can send this now or edit any item below.'
            : `${fields.length - answeredCount} item${fields.length - answeredCount === 1 ? '' : 's'} still need attention before you continue.`}</div>
        </div>
        <div class="workspace-task-assist-review-list">
          ${fields.map((field, index) => {
            const answerValue = this.getAssistFieldAnswerValue(field);
            const answered = Boolean(answerValue);
            return `
              <article class="workspace-task-assist-review-item${answered ? ' is-answered' : ''}">
                <div class="workspace-task-assist-review-item-top">
                  <div class="workspace-task-assist-review-item-title">
                    <span class="workspace-task-assist-review-item-number">${index + 1}</span>
                    <span>${this.escapeHtml(this.getAssistFieldPrompt(field))}</span>
                  </div>
                  <button type="button" class="workspace-task-assist-review-edit" data-assist-review-edit-id="${this.escapeHtml(field.id)}">Edit</button>
                </div>
                <div class="workspace-task-assist-review-answer${answered ? '' : ' is-empty'}">
                  ${this.escapeHtml(answerValue || 'No answer yet')}
                </div>
              </article>
            `;
          }).join('')}
        </div>
      </div>
    `;
  }

  getAssistFieldAnswerValue(field) {
    if (!field || !this.currentBlockedTask) return '';

    const selectedValue = String(this.currentBlockedTask.selectedFieldValues?.[field.id] || '').trim();
    if (!selectedValue) return '';

    if (field.type === 'select' && Array.isArray(field.options) && field.options.length > 0) {
      const option = field.options.find((item) => String(item?.value || '').trim() === selectedValue);
      if (option) {
        return String(option.label || option.value || '').trim();
      }
    }

    return selectedValue;
  }

  syncAssistFormProgress(workflowStep) {
    if (!workflowStep || workflowStep.stepType !== 'ask_form' || !this.elements.assistFormFields) return;

    const fields = Array.isArray(workflowStep.fields) ? workflowStep.fields : [];
    if (fields.length <= 1) return;

    const selectedValues = this.currentBlockedTask?.selectedFieldValues || {};
    const answeredCount = fields.filter((field) => String(selectedValues[field.id] || '').trim()).length;
    const progressPercent = fields.length > 0 ? Math.round((answeredCount / fields.length) * 100) : 0;

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-tab]').forEach((button) => {
      const fieldId = String(button.getAttribute('data-assist-field-tab') || '').trim();
      const isAnswered = Boolean(String(selectedValues[fieldId] || '').trim());
      button.classList.toggle('is-answered', isAnswered);
      const state = button.querySelector('.workspace-task-assist-deck-tab-state');
      if (state) {
        state.textContent = isAnswered ? 'Answered' : 'Open';
      }
    });

    const progressCopy = this.elements.assistFormFields.querySelector('[data-assist-progress-count]');
    if (progressCopy) {
      progressCopy.textContent = this.assistReviewMode
        ? (answeredCount === fields.length ? 'All answers captured' : `${fields.length - answeredCount} answers still missing`)
        : `${answeredCount} answered so far`;
    }

    const progressFill = this.elements.assistFormFields.querySelector('[data-assist-progress-fill]');
    if (progressFill) {
      progressFill.style.width = `${progressPercent}%`;
    }
  }

  focusActiveAssistField() {
    if (!this.elements.assistFormFields) return;

    const stage = this.elements.assistFormFields.querySelector('.workspace-task-assist-deck-stage') || this.elements.assistFormFields;
    const target = stage.querySelector(
      '[data-assist-review-edit-id], [data-assist-custom-field-id], textarea[data-assist-field-id], input[data-assist-field-id]:not([type="radio"]), input[type="radio"]:checked, input[data-assist-field-id][type="radio"]'
    );

    if (target && typeof target.focus === 'function') {
      target.focus({ preventScroll: true });
      if (target instanceof HTMLInputElement && target.type === 'text') {
        target.select();
      }
    }
  }

  getAssistFieldPrompt(field) {
    const description = String(field?.description || '').trim();
    if (description) {
      return description.endsWith('?') ? description : `${description}?`;
    }
    const label = String(field?.label || '').trim();
    return label.endsWith('?') ? label : `${label}?`;
  }

  setAssistFormFieldValue(fieldId, value) {
    if (!this.currentBlockedTask || !fieldId) return;

    const normalizedValue = String(value || '').trim();
    if (!this.currentBlockedTask.selectedFieldValues || typeof this.currentBlockedTask.selectedFieldValues !== 'object') {
      this.currentBlockedTask.selectedFieldValues = {};
    }

    if (!normalizedValue) {
      delete this.currentBlockedTask.selectedFieldValues[fieldId];
      return;
    }

    this.currentBlockedTask.selectedFieldValues[fieldId] = normalizedValue;
  }

  setAssistFormFieldOptionValue(fieldId, value) {
    if (!this.currentBlockedTask || !fieldId) return;

    const normalizedValue = String(value || '').trim();
    if (!this.currentBlockedTask.selectedFieldOptionValues || typeof this.currentBlockedTask.selectedFieldOptionValues !== 'object') {
      this.currentBlockedTask.selectedFieldOptionValues = {};
    }

    if (!normalizedValue) {
      delete this.currentBlockedTask.selectedFieldOptionValues[fieldId];
      return;
    }

    this.currentBlockedTask.selectedFieldOptionValues[fieldId] = normalizedValue;
  }

  setAssistFormCustomFieldValue(fieldId, value) {
    if (!this.currentBlockedTask || !fieldId) return;

    const normalizedValue = String(value || '').trim();
    if (!this.currentBlockedTask.selectedFieldCustomValues || typeof this.currentBlockedTask.selectedFieldCustomValues !== 'object') {
      this.currentBlockedTask.selectedFieldCustomValues = {};
    }

    if (!normalizedValue) {
      delete this.currentBlockedTask.selectedFieldCustomValues[fieldId];
      return;
    }

    this.currentBlockedTask.selectedFieldCustomValues[fieldId] = normalizedValue;
  }

  collectAssistFormFieldValues() {
    const workflowStep = this.currentBlockedTask?.workflowStep;
    if (!workflowStep || workflowStep.stepType !== 'ask_form' || !Array.isArray(workflowStep.fields)) {
      return [];
    }

    const selectedValues = this.currentBlockedTask?.selectedFieldValues || {};
    return workflowStep.fields
      .map((field) => {
        const value = String(selectedValues[field.id] || '').trim();
        if (!value) return null;
        return {
          id: field.id,
          label: field.label,
          value
        };
      })
      .filter(Boolean);
  }

  updateAssistSwitchButtonState() {
    if (!this.elements.assistSwitchBtn) return;
    const selectedAgent = String(this.elements.assistAgent?.value || '').trim();
    this.elements.assistSwitchBtn.disabled = !selectedAgent;
  }

  toggleAssistResponseExpanded() {
    this.taskAssistResponseExpanded = !this.taskAssistResponseExpanded;
    this.renderBlockedContext();
  }

  setAssistButtonsDisabled(disabled) {
    [
      this.elements.assistRetryBtn,
      this.elements.assistContinueBtn,
      this.elements.assistSwitchBtn,
      this.elements.assistFailBtn
    ].forEach((button) => {
      if (button) button.disabled = disabled;
    });
  }

  async deleteTask() {
    const taskTitle = this.getTaskDisplayLabel();
    if (!confirm(`Delete "${taskTitle}"? This cannot be undone.`)) return;

    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.taskId)}?workspace_id=${encodeURIComponent(this.workspaceId)}`,
        { method: 'DELETE' }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to delete task');
      }
      window.location.href = `/workspaces/${encodeURIComponent(this.workspaceId)}`;
    } catch (error) {
      console.error('Failed to delete task:', error);
      this.notify('error', error?.message || 'Failed to delete task');
    }
  }

  async executeTask() {
    const status = String(this.task?.status || '').trim().toLowerCase();
    const isRerun = status === 'completed' || status === 'failed';
    const label = isRerun ? 'Re-run' : 'Run';

    if (isRerun && !confirm(`${label} this task? Previous results will be replaced.`)) return;

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: this.taskId })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute task');
      }
      this.notify('success', `Task ${label.toLowerCase()} started`);
      await this.loadData();
    } catch (error) {
      console.error('Failed to execute task:', error);
      this.notify('error', error?.message || 'Failed to execute task');
    }
  }

  async completeTask() {
    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.taskId)}/complete`,
        { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to complete task');
      }
      this.notify('success', 'Task marked as complete');
      await this.loadData();
    } catch (error) {
      console.error('Failed to complete task:', error);
      this.notify('error', error?.message || 'Failed to complete task');
    }
  }

  async copyToClipboard(text, successMessage = 'Copied') {
    try {
      await navigator.clipboard.writeText(text);
      this.notify('success', successMessage);
    } catch (_error) {
      this.notify('error', 'Failed to copy');
    }
  }

  notify(kind, message) {
    if (window.Toast && typeof window.Toast[kind] === 'function') {
      window.Toast[kind](message);
      return;
    }

    this.setAlert(message);
  }

  async submitTaskAssist(action) {
    if (!this.currentBlockedTask?.taskId) return;

    const selectedAgent = String(this.elements.assistAgent?.value || '').trim();
    const message = String(this.elements.assistMessage?.value || '').trim();
    const workflowStep = this.currentBlockedTask.workflowStep;
    const selectedChoiceId = String(this.currentBlockedTask.selectedChoiceId || '').trim();
    const fieldValues = this.collectAssistFormFieldValues();

    if (action === 'switch_agent_retry' && !selectedAgent) {
      this.notify('warning', 'Select an agent before switching and retrying.');
      return;
    }

    if (action === 'switch_agent_retry' &&
        selectedAgent.toLowerCase() === String(this.currentBlockedTask.currentAgent || '').trim().toLowerCase()) {
      this.notify('warning', 'Choose a different agent before switching.');
      return;
    }

    if (action === 'continue_with_instruction' &&
        workflowStep?.stepType === 'ask_choice' &&
        !selectedChoiceId &&
        !message) {
      this.notify('warning', 'Choose a next step or add guidance before continuing.');
      return;
    }

    if (action === 'continue_with_instruction' &&
        workflowStep?.stepType === 'ask_form') {
      const requiredFields = Array.isArray(workflowStep.fields)
        ? workflowStep.fields.filter((field) => field?.required !== false)
        : [];
      const missingRequired = requiredFields.filter((field) => !fieldValues.some((item) => item.id === field.id));
      if (missingRequired.length > 0) {
        this.notify('warning', 'Answer the required questions before continuing.');
        return;
      }

      if (fieldValues.length === 0 && !message) {
        this.notify('warning', 'Answer at least one question or add guidance before continuing.');
        return;
      }
    }

    const payload = {
      action,
      block_id: this.currentBlockedTask.blockId || undefined,
      message: message || undefined,
      agent: selectedAgent || undefined,
      choice_id: selectedChoiceId || undefined,
      choice_label: this.currentBlockedTask.selectedChoiceLabel || undefined,
      choice_number: this.currentBlockedTask.selectedChoiceNumber || undefined,
      field_values: fieldValues.length > 0 ? fieldValues : undefined
    };

    this.setAssistButtonsDisabled(true);
    this.setAlert('');

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(this.currentBlockedTask.taskId)}/assist`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to update task');
      }

      this.notify('success', 'Task updated');
      if (this.elements.assistMessage) {
        this.elements.assistMessage.value = '';
      }
      await this.loadData();
    } catch (error) {
      console.error('Failed to submit task assistance:', error);
      this.notify('error', error?.message || 'Failed to update task');
    } finally {
      this.setAssistButtonsDisabled(false);
      this.updateAssistSwitchButtonState();
    }
  }
}
