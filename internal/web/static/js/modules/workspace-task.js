/* global escapeHtml */

import { taskExecutionViewsMethods, TRACE_PAGE_SIZE } from './workspace-task-execution-views.js';
import {
  artifactToCSVFence,
  buildTaskResultArtifact,
  parseDelimitedRecords,
  rowsToCSV
} from './task-result-artifacts.js';
import { taskSkillDraftMethods } from './workspace-task-skill-draft.js';
import { taskResultActionsMethods } from './workspace-task-result-actions.js';
import { showCanvasAgentPicker } from './agent-canvas-dialogs.js';
import { resolveTaskState, PRESENTATION_STATE } from './task-presentation.js';
import { fetchRelatedPlan, renderRelatedPlan } from './workspace-related-plan.js';

const escapeTaskHtml =
  window.escapeHtml ||
  function fallbackEscapeHtml(value) {
    return String(value ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  };

export function _formatRelativeDate(dateString) {
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

export function formatDateTime(dateString) {
  if (!dateString) return '—';
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString([], {
    dateStyle: 'medium',
    timeStyle: 'short'
  });
}

export function getStatusClass(status) {
  const normalized = String(status || '')
    .trim()
    .toLowerCase();
  if (normalized === 'completed' || normalized === 'success') return 'completed';
  if (normalized === 'in_progress') return 'in_progress';
  if (normalized === 'blocked' || normalized === 'waiting_for_choice') return 'blocked';
  if (normalized === 'cancelled') return 'cancelled';
  if (normalized === 'skipped') return 'cancelled';
  if (normalized === 'failed' || normalized === 'error' || normalized === 'timeout')
    return 'failed';
  return 'pending';
}

const KNOWN_STATUS_LABELS = {
  pending: 'Pending',
  assigned: 'Assigned',
  in_progress: 'In Progress',
  waiting_for_choice: 'Waiting for Choice',
  completed: 'Completed',
  success: 'Completed',
  failed: 'Failed',
  error: 'Failed',
  blocked: 'Blocked',
  cancelled: 'Cancelled',
  skipped: 'Skipped',
  timeout: 'Timed Out'
};

export function getDisplayStatus(status) {
  const normalized = String(status || '')
    .trim()
    .toLowerCase();
  if (KNOWN_STATUS_LABELS[normalized]) return KNOWN_STATUS_LABELS[normalized];
  // Shared resolver (FR110) decides only the fallback: a genuinely
  // unrecognized status is labeled "Unknown", never silently "Pending" (FR38)
  // — every status this file already knows about is covered above, so this
  // only changes behavior for a status no caller has ever seen before.
  return resolveTaskState({ status }) === PRESENTATION_STATE.UNKNOWN ? 'Unknown' : 'Pending';
}

export function summarizeText(value, maxLength = 220) {
  const normalized = String(value || '')
    .replace(/\s+/g, ' ')
    .trim();
  if (!normalized) return '';
  if (normalized.length <= maxLength) return normalized;

  const candidate = normalized.slice(0, maxLength - 1);
  const boundary = candidate.lastIndexOf(' ');
  const trimmed =
    boundary >= Math.floor(maxLength * 0.55) ? candidate.slice(0, boundary) : candidate;
  return `${trimmed.trim()}...`;
}

export function normalizeResultText(value) {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value, null, 2);
  } catch (_error) {
    return String(value);
  }
}

export const TASK_SKILL_RESULT_CONTEXT_MAX_CHARS = 2600;
export const TASK_SKILL_DETAILS_CONTEXT_MAX_CHARS = 1200;
export const TASK_SKILL_GENERATION_CONTEXT_MAX_CHARS = 6200;

export function stripSkillUnsafeMarkup(value) {
  return String(value ?? '')
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

export function trimTaskSkillText(value, maxLength = 900) {
  const normalized = String(value ?? '').trim();
  if (!normalized || normalized.length <= maxLength) return normalized;

  const candidate = normalized.slice(0, maxLength - 1);
  const boundary = candidate.lastIndexOf(' ');
  const trimmed =
    boundary >= Math.floor(maxLength * 0.55) ? candidate.slice(0, boundary) : candidate;
  return `${trimmed.trim()}...`;
}

export function buildTaskSkillNameSlug(value) {
  let slug = stripSkillUnsafeMarkup(value)
    .toLowerCase()
    .replace(/anthropic/g, 'provider')
    .replace(/claude/g, 'assistant')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-');

  if (!slug) slug = 'task-skill';
  if (slug.length > 64) {
    slug = slug.slice(0, 64).replace(/-+$/g, '');
  }
  return slug || 'task-skill';
}

export function extractGeneratedSkillPrompt(raw) {
  let text = String(raw || '').trim();
  if (!text) return '';

  if (text.startsWith('```')) {
    text = text.replace(/^```[a-zA-Z0-9_-]*\s*/u, '');
    text = text.replace(/\s*```$/u, '').trim();
  }

  const lower = text.toLowerCase();
  if (lower.startsWith('prompt:')) {
    text = text.slice('prompt:'.length).trim();
  }

  if (
    (text.startsWith('"') && text.endsWith('"')) ||
    (text.startsWith("'") && text.endsWith("'"))
  ) {
    text = text.slice(1, -1).trim();
  }

  return text;
}

export function stringifyTraceValue(value, maxLength = 900) {
  if (value === undefined || value === null) return '';

  let text = '';
  if (typeof value === 'string') {
    text = value;
  } else {
    try {
      text = JSON.stringify(value, null, 2);
    } catch (_error) {
      text = String(value);
    }
  }

  return trimTaskSkillText(text, maxLength);
}

export function getTaskEventData(event) {
  const data = event?.data;
  if (data?.data && typeof data.data === 'object') return data.data;
  return data && typeof data === 'object' ? data : {};
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
  return cleanAssistText(value)
    .replace(/[,:;.!?]+$/g, '')
    .trim();
}

function ensureAssistQuestion(value) {
  const cleaned = cleanAssistText(value)
    .replace(/[.!]+$/g, '')
    .trim();
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
    description:
      description.endsWith('.') || description.endsWith('!') || description.endsWith('?')
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
  if (left.toLowerCase().includes('should i') || left.toLowerCase().includes('want me to'))
    return [];

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
    field.options = options
      .map((option, optionIndex) => ({
        value: cleanAssistText(option.value || option.label),
        label: cleanAssistText(option.label || option.value),
        description: cleanAssistText(option.description),
        key: String(option.key || String.fromCharCode(65 + (optionIndex % 26))).trim()
      }))
      .filter(option => option.value && option.label);
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

  for (let index = 0; index < lines.length; ) {
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

  const addQuestion = value => {
    const normalized = ensureAssistQuestion(value);
    if (!normalized) return;
    const key = normalized.toLowerCase();
    if (seen.has(key)) return;
    seen.add(key);
    questions.push(normalized);
  };

  lines.forEach(rawLine => {
    const trimmed = String(rawLine || '').trim();
    if (!trimmed) return;

    const promptMatch = trimmed.match(assistQuestionPromptPattern);
    const candidate = promptMatch && promptMatch.length >= 2 ? promptMatch[1] : trimmed;
    if (!candidate.includes('?')) return;

    candidate.split('?').forEach(part => {
      const fragment = cleanAssistText(part);
      if (fragment.length < 8 || fragment.length > 180) return;
      addQuestion(fragment);
    });
  });

  if (questions.length > 0) {
    return questions;
  }

  cleanAssistText(text)
    .split('?')
    .forEach(part => {
      const fragment = cleanAssistText(part);
      if (fragment.length < 8 || fragment.length > 180) return;
      addQuestion(fragment);
    });

  return questions;
}

function deriveAssistWorkflowStepFromText(...texts) {
  const sources = texts.map(value => String(value || '').trim()).filter(Boolean);
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
  sources.forEach(source => {
    extractAssistQuestionsFromText(source).forEach(question => {
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
  return (
    normalized === 'other' ||
    normalized === 'something else' ||
    normalized === 'custom' ||
    normalized === 'another option' ||
    normalized === 'not listed' ||
    normalized.startsWith('other ') ||
    normalized.endsWith(' other')
  );
}

/**
 * Normalizes the structured repair the server attaches to a repair-gated block.
 *
 * A block with a repair is one that retrying cannot fix — a missing connection,
 * an exhausted provider quota, a bad API key. The view shows the repair and
 * suppresses Retry, because offering an action guaranteed to fail again is
 * worse than offering none.
 *
 * @returns {{code:string,label:string,url:string}|null}
 */
function normalizeBlockedRepair(raw) {
  const label = String(raw?.label || '').trim();
  if (!label) return null;
  return {
    code: String(raw?.code || '').trim(),
    label,
    url: String(raw?.url || '').trim()
  };
}

function buildAssistSelectState(workflowStep, selectedFieldValues = {}) {
  const optionValues = {};
  const customValues = {};

  if (
    !workflowStep ||
    workflowStep.stepType !== 'ask_form' ||
    !Array.isArray(workflowStep.fields)
  ) {
    return { optionValues, customValues };
  }

  workflowStep.fields.forEach(field => {
    if (field?.type !== 'select' || !Array.isArray(field.options) || field.options.length === 0) {
      return;
    }

    const savedValue = String(selectedFieldValues[field.id] || '').trim();
    if (!savedValue) return;

    const matchedOption = field.options.find(
      option =>
        String(option?.value || '').trim() === savedValue ||
        String(option?.label || '').trim() === savedValue
    );
    if (matchedOption) {
      optionValues[field.id] = String(matchedOption.value || matchedOption.label || '').trim();
      return;
    }

    const otherOption = field.options.find(option => isAssistOtherOption(option));
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
  const trimmed =
    boundary >= Math.floor(maxLength * 0.55) ? candidate.slice(0, boundary) : candidate;
  return `${trimmed.trim()}...`;
}

function isResultWorkflowReferenceSection(value) {
  const token = normalizeResultWorkflowToken(value);
  if (!token) return false;
  return (
    token.includes('note') ||
    token.includes('tip') ||
    token.includes('material') ||
    token.includes('supply') ||
    token.includes('cut list') ||
    token.includes('dimension') ||
    token.includes('measurement') ||
    token.includes('reference') ||
    token.includes('budget') ||
    token.includes('cost')
  );
}

function buildResultWorkflowDraftTitle(taskLabel) {
  const cleaned = trimResultWorkflowLabel(taskLabel, 104) || 'Workflow Draft';
  if (/\bworkflow\b/i.test(cleaned)) {
    return cleaned;
  }
  return trimResultWorkflowLabel(`${cleaned} - Workflow`, 120) || 'Workflow Draft';
}

function buildResultWorkflowDraft(
  taskLabel,
  resultText,
  sourceTaskId,
  defaultAssignmentValue = ''
) {
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

  const rememberSection = section => {
    const cleaned = trimResultWorkflowLabel(section, 96);
    if (!cleaned) return;
    const key = cleaned.toLowerCase();
    if (seenSections.has(key)) return;
    seenSections.add(key);
    sectionLabels.push(cleaned);
  };

  const pushNote = value => {
    const cleaned = cleanResultWorkflowText(value);
    if (!cleaned) return;
    notes.push(cleaned);
    lastNoteIndex = notes.length - 1;
    lastAction = null;
  };

  const pushAction = value => {
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

  lines.forEach(rawLine => {
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
      lastAction.text = trimResultWorkflowLabel(
        appendResultWorkflowText(lastAction.text, continuation),
        220
      );
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
    notes.slice(0, 6).forEach(note => {
      detailsParts.push(`- ${note}`);
    });
    if (notes.length > 6) {
      detailsParts.push(
        `- ${notes.length - 6} more note${notes.length - 6 === 1 ? '' : 's'} remain in the original result.`
      );
    }
  }

  if (truncatedCount > 0) {
    detailsParts.push(
      `${truncatedCount} additional step${truncatedCount === 1 ? '' : 's'} were omitted from the draft to keep the workflow reviewable.`
    );
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
    this.workspaceOutputDir = '';
    this.task = null;
    this.currentRun = null;
    this.workspaceRuns = [];
    this.tasks = [];
    this.taskEvents = [];
    this.availableAgents = [];
    this.currentBlockedTask = null;
    this.taskAssistResponseExpanded = false;
    this.assistActiveFieldId = '';
    this.assistReviewMode = false;
    this.workspaceRealtimeUnsubscribe = null;
    this.pendingRefreshTimer = null;
    this.titleEditInProgress = false;
    this.detailsEditInProgress = false;
    this.workflowDraftPending = false;
    this.resultNoteSaving = false;
    this.currentResultArtifact = null;
    this.resultArtifactNoteSaving = false;
    this.columnDesignerModalOpen = false;
    this.columnModalOverlay = null;
    this.columnModalBody = null;
    this.suggestionPreviewOpen = false;
    this.suggestionPromptEcho = null;
    this.savedResultNote = null;
    this.savedResultNoteResult = '';
    this.resultPromotionPending = false;
    this.resultPromotionSubmitting = false;
    this.resultPromotionDraft = null;
    this.resultResearchPendingSectionId = '';
    this.resultResearchDraft = null;
    this.resultResearchSubmitting = false;
    this.resultOutputSpecDraft = null;
    this.skillDraftGenerating = false;
    this.skillDraftSubmitting = false;
    this.skillDraftAbortController = null;
    this.skillDraftRequestId = 0;
    this.resultSectionMenu = null;
    this.scheduleSubmitting = false;
    // Execution-trace pagination/filter state. Persists across realtime
    // refreshes so a user reading a long trace doesn't get bumped back to
    // the first 50 entries when a new event arrives. Reset only on
    // navigation away from this task.
    this._traceVisibleCount = TRACE_PAGE_SIZE;
    this._traceFilter = 'all';
    this._runsTab = 'runs';
    this._cancelConfirmActive = false;
    this._cancelInFlight = false;
    // Live-activity tracking. Populated from realtime task.* events for the
    // current task; the live badge renders "Active Xs ago · <phase>" off this
    // so users can tell a still-spinning task is making progress.
    this._latestActivity = null;
    this._activityTickHandle = null;
    // Follow-up form lives inline under the result card; only one instance
    // open at a time. _followupSubmitting prevents double-submits.
    this._followupOpen = false;
    this._followupSubmitting = false;
    this.boundResultSectionMenuDocumentClick = event => {
      if (!this.resultSectionMenu || this.resultSectionMenu.contains(event.target)) return;
      this.closeResultSectionMenu();
    };
    this.boundResultSectionMenuKeydown = event => {
      if (event.key === 'Escape') {
        this.closeResultSectionMenu();
      }
    };
    this.boundResultSectionMenuScroll = () => this.closeResultSectionMenu();
    this.elements = {};
  }

  async init() {
    this.cacheElements();
    this.bindEvents();
    await this.loadData();
    this.setupRealtime();
  }

  // destroy releases the resources init() acquired so the page can be
  // safely torn down (e.g. inside an SPA host that swaps the route, or by
  // a future test harness). Today the page is rebuilt by full reload, but
  // the leak surface is still real once any caller mounts more than one
  // task page in the same document lifetime: the realtime subscription
  // would fire callbacks against a detached DOM, the skill-draft fetch
  // would resolve into a stale instance, and the debounced refresh timer
  // would call loadData() on a page that no longer exists.
  destroy() {
    if (typeof this.workspaceRealtimeUnsubscribe === 'function') {
      try {
        this.workspaceRealtimeUnsubscribe();
      } catch (_err) {
        // Subscriber teardown is best-effort; nothing useful to do here.
      }
      this.workspaceRealtimeUnsubscribe = null;
    }
    if (this.skillDraftAbortController) {
      try {
        this.skillDraftAbortController.abort();
      } catch (_err) {
        // AbortController.abort can throw on some polyfills; ignore.
      }
      this.skillDraftAbortController = null;
    }
    if (this.pendingRefreshTimer) {
      window.clearTimeout(this.pendingRefreshTimer);
      this.pendingRefreshTimer = null;
    }
    this.stopActivityTick();
    // Tear down menu listeners + drop the menu DOM node if a contextual
    // result-section menu is open.
    this.closeResultSectionMenu();
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
      breadcrumbTitle: document.getElementById('workspace-task-breadcrumb-title'),
      idValue: document.getElementById('workspace-task-id-value'),
      copyIdBtn: document.getElementById('workspace-task-copy-id'),
      copyLinkBtn: document.getElementById('workspace-task-copy-link'),
      deleteBtn: document.getElementById('workspace-task-delete'),
      heroOverflow: document.querySelector('.workspace-task-hero-overflow'),
      heroOverflowToggle: document.getElementById('workspace-task-hero-overflow-toggle'),
      heroOverflowMenu: document.getElementById('workspace-task-hero-overflow-menu'),
      subtitle: document.getElementById('workspace-task-subtitle'),
      detailsEditBtn: document.getElementById('workspace-task-details-edit'),
      status: document.getElementById('workspace-task-status'),
      heroActions: document.getElementById('workspace-task-hero-actions'),
      overview: document.getElementById('workspace-task-overview'),
      heroAgentWrap: document.getElementById('workspace-task-hero-agent-wrap'),
      heroAgent: document.getElementById('workspace-task-hero-agent'),
      outputFollowupBtn: document.getElementById('workspace-task-output-followup'),
      followupPanel: document.getElementById('workspace-task-followup-panel'),
      followupDescription: document.getElementById('workspace-task-followup-description'),
      followupDetails: document.getElementById('workspace-task-followup-details'),
      followupDetailsField: document.getElementById('workspace-task-followup-details-field'),
      followupDetailsToggle: document.getElementById('workspace-task-followup-details-toggle'),
      followupDetailsCollapsible: document.querySelector('.workspace-task-followup-collapsible'),
      followupAgent: document.getElementById('workspace-task-followup-agent'),
      followupError: document.getElementById('workspace-task-followup-error'),
      followupSubmit: document.getElementById('workspace-task-followup-submit'),
      followupCancel: document.getElementById('workspace-task-followup-cancel'),
      relationshipsCard: document.getElementById('workspace-task-relationships-card'),
      relationships: document.getElementById('workspace-task-relationships'),
      outputCard: document.getElementById('workspace-task-output-card'),
      outputShapeWrap: document.getElementById('workspace-task-output-shape-wrap'),
      outputShape: document.getElementById('workspace-task-output-shape'),
      outputCopyBtn: document.getElementById('workspace-task-output-copy'),
      outputSaveNoteBtn: document.getElementById('workspace-task-output-save-note'),
      outputCreateSkillBtn: document.getElementById('workspace-task-output-create-skill'),
      outputPromoteBtn: document.getElementById('workspace-task-output-promote'),
      outputOverflow: document.querySelector('.workspace-task-output-overflow'),
      outputOverflowToggle: document.getElementById('workspace-task-output-overflow-toggle'),
      outputOverflowMenu: document.getElementById('workspace-task-output-overflow-menu'),
      outputNoteStatus: document.getElementById('workspace-task-output-note-status'),
      output: document.getElementById('workspace-task-output'),
      skillModal: document.getElementById('workspace-task-skill-modal'),
      skillMeta: document.getElementById('workspace-task-skill-meta'),
      skillError: document.getElementById('workspace-task-skill-error'),
      skillAgentInput: document.getElementById('workspace-task-skill-agent'),
      skillNameInput: document.getElementById('workspace-task-skill-name'),
      skillDescriptionInput: document.getElementById('workspace-task-skill-description'),
      skillPromptInput: document.getElementById('workspace-task-skill-prompt'),
      skillGenerateBtn: document.getElementById('workspace-task-skill-generate'),
      skillSubmitBtn: document.getElementById('workspace-task-skill-submit'),
      resultPromoteModal: document.getElementById('workspace-task-result-promote-modal'),
      resultPromoteTitleInput: document.getElementById('workspace-task-result-promote-title'),
      resultPromoteMeta: document.getElementById('workspace-task-result-promote-meta'),
      resultPromoteGroups: document.getElementById('workspace-task-result-promote-groups'),
      resultPromoteSubmitBtn: document.getElementById('workspace-task-result-promote-submit'),
      resultResearchModal: document.getElementById('workspace-task-result-research-modal'),
      resultResearchSectionMeta: document.getElementById(
        'workspace-task-result-research-section-meta'
      ),
      resultResearchTitleInput: document.getElementById('workspace-task-result-research-title'),
      resultResearchAgentSelect: document.getElementById('workspace-task-result-research-agent'),
      resultResearchDetailsInput: document.getElementById('workspace-task-result-research-details'),
      resultResearchSectionInput: document.getElementById(
        'workspace-task-result-research-section-text'
      ),
      resultResearchLinkInput: document.getElementById(
        'workspace-task-result-research-link-source'
      ),
      resultResearchRunInput: document.getElementById('workspace-task-result-research-run-now'),
      resultResearchOpenInput: document.getElementById(
        'workspace-task-result-research-open-after-create'
      ),
      resultResearchSubmitBtn: document.getElementById('workspace-task-result-research-submit'),
      workflowCard: document.getElementById('workspace-task-workflow-card'),
      workflowActions: document.getElementById('workspace-task-workflow-actions'),
      workflowAddStepBtn: document.getElementById('workspace-task-workflow-add-step'),
      workflowRunAllBtn: document.getElementById('workspace-task-workflow-run-all'),
      workflowEmpty: document.getElementById('workspace-task-workflow-empty'),
      workflowGenerateBtn: document.getElementById('workspace-task-workflow-generate'),
      workflowSteps: document.getElementById('workspace-task-workflow-steps'),
      workspaceRunsCard: document.getElementById('workspace-task-workspace-runs-card'),
      workspaceRuns: document.getElementById('workspace-task-workspace-runs'),
      runsCard: document.getElementById('workspace-task-runs-card'),
      runsTabs: document.querySelectorAll('#workspace-task-runs-card [data-runs-tab]'),
      runsTabRuns: document.getElementById('workspace-task-runs-tab-runs'),
      runsTabTrace: document.getElementById('workspace-task-runs-tab-trace'),
      runsTabRunsCount: document.getElementById('workspace-task-runs-tab-runs-count'),
      runsTabTraceCount: document.getElementById('workspace-task-runs-tab-trace-count'),
      toolSummary: document.getElementById('workspace-task-tool-summary'),
      trace: document.getElementById('workspace-task-trace'),
      executionTrace: document.getElementById('workspace-task-execution-trace'),
      executionTraceControls: document.getElementById('workspace-task-execution-trace-controls'),
      executionTraceFilters: document.getElementById('workspace-task-execution-trace-filters'),
      executionTraceCount: document.getElementById('workspace-task-execution-trace-count'),
      scheduleCard: document.getElementById('workspace-task-schedule-card'),
      schedule: document.getElementById('workspace-task-schedule'),
      automationSummary: document.getElementById('workspace-task-automation-summary'),
      automationColumns: document.getElementById('workspace-task-automation-columns'),
      automationStorage: document.getElementById('workspace-task-automation-storage'),
      scheduleCardEditBtn: document.getElementById('workspace-task-schedule-card-edit'),
      scheduleOpenFolderBtn: document.getElementById('workspace-task-schedule-open-folder'),
      scheduleModal: document.getElementById('workspace-task-schedule-modal'),
      scheduleModalHeading: document.getElementById('workspace-task-schedule-heading'),
      scheduleModalMeta: document.getElementById('workspace-task-schedule-modal-meta'),
      scheduleError: document.getElementById('workspace-task-schedule-error'),
      scheduleEnabledInput: document.getElementById('workspace-task-schedule-enabled'),
      scheduleNameInput: document.getElementById('workspace-task-schedule-name'),
      scheduleTypeInput: document.getElementById('workspace-task-schedule-type'),
      scheduleTimeField: document.getElementById('workspace-task-schedule-time-field'),
      scheduleTimeLabel: document.getElementById('workspace-task-schedule-time-label'),
      scheduleTimeInput: document.getElementById('workspace-task-schedule-time'),
      scheduleDayField: document.getElementById('workspace-task-schedule-day-field'),
      scheduleDayInput: document.getElementById('workspace-task-schedule-day'),
      scheduleIntervalField: document.getElementById('workspace-task-schedule-interval-field'),
      scheduleIntervalValueInput: document.getElementById('workspace-task-schedule-interval-value'),
      scheduleIntervalUnitInput: document.getElementById('workspace-task-schedule-interval-unit'),
      scheduleOnceField: document.getElementById('workspace-task-schedule-once-field'),
      scheduleOnceInput: document.getElementById('workspace-task-schedule-once'),
      scheduleCronField: document.getElementById('workspace-task-schedule-cron-field'),
      scheduleCronInput: document.getElementById('workspace-task-schedule-cron'),
      scheduleSleepPolicyInput: document.getElementById('workspace-task-schedule-sleep-policy'),
      scheduleWakeMacInput: document.getElementById('workspace-task-schedule-wake-mac'),
      scheduleWakeFields: document.getElementById('workspace-task-schedule-wake-fields'),
      scheduleWakeLeadInput: document.getElementById('workspace-task-schedule-wake-lead'),
      scheduleWakeFallbackInput: document.getElementById('workspace-task-schedule-wake-fallback'),
      scheduleWakePermission: document.getElementById('workspace-task-schedule-wake-permission'),
      schedulePreview: document.getElementById('workspace-task-schedule-preview'),
      scheduleSubmitBtn: document.getElementById('workspace-task-schedule-submit'),
      scheduleRemoveBtn: document.getElementById('workspace-task-schedule-remove'),
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
    this.elements.title?.addEventListener('click', event => {
      // Don't hijack the user's text-selection drag — only treat a plain
      // click with no selection as an "open editor" intent.
      if (window.getSelection && String(window.getSelection() || '').length > 0) return;
      if (event.detail > 1) return; // double-click handled below
      this.startTitleEdit();
    });
    this.elements.title?.addEventListener('dblclick', () => this.startTitleEdit());
    // Keyboard parity for the removed pencil button: tabindex=0 + role=button
    // on the <h1> means keyboard users land here in tab order; Enter or Space
    // opens the inline editor.
    this.elements.title?.addEventListener('keydown', event => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        this.startTitleEdit();
      }
    });
    this.elements.detailsEditBtn?.addEventListener('click', () => this.startHeroDetailsEdit());
    this.elements.subtitle?.addEventListener('click', event => {
      if (window.getSelection && String(window.getSelection() || '').length > 0) return;
      if (event.detail > 1) return;
      this.startHeroDetailsEdit();
    });
    this.elements.subtitle?.addEventListener('dblclick', () => this.startHeroDetailsEdit());
    if (this.elements.runsTabs && this.elements.runsTabs.forEach) {
      this.elements.runsTabs.forEach(btn => {
        btn.addEventListener('click', () => {
          const next = btn.getAttribute('data-runs-tab') || 'runs';
          this.setRunsTab(next);
        });
      });
    }
    if (this.elements.heroAgent) {
      this.elements.heroAgent.addEventListener('change', async event => {
        const value = event.target.value || '';
        try {
          await this.updateTaskFields({ to: value });
          this.notify('success', value ? `Reassigned to ${value}` : 'Agent unassigned');
        } catch (error) {
          this.notify('error', error?.message || 'Failed to update agent');
        }
      });
    }
    this.elements.outputFollowupBtn?.addEventListener('click', () =>
      this.toggleFollowupPanel(true)
    );
    this.elements.followupCancel?.addEventListener('click', () => this.toggleFollowupPanel(false));
    this.elements.followupSubmit?.addEventListener('click', () => this.submitFollowupTask());
    this.elements.followupDetailsToggle?.addEventListener('click', () =>
      this.toggleFollowupDetails()
    );
    this.elements.copyIdBtn?.addEventListener('click', () =>
      this.copyToClipboard(this.taskId, 'Task ID copied')
    );
    this.elements.copyLinkBtn?.addEventListener('click', () => {
      this.copyToClipboard(window.location.href, 'Link copied');
      this.setHeroOverflowOpen(false);
    });
    this.elements.deleteBtn?.addEventListener('click', () => {
      this.setHeroOverflowOpen(false);
      this.deleteTask();
    });
    this.elements.heroOverflowToggle?.addEventListener('click', event => {
      event.stopPropagation();
      this.setHeroOverflowOpen(this.elements.heroOverflow?.dataset.open !== 'true');
    });
    document.addEventListener('click', event => {
      if (this.elements.heroOverflow?.dataset.open !== 'true') return;
      if (this.elements.heroOverflow.contains(event.target)) return;
      this.setHeroOverflowOpen(false);
    });
    document.addEventListener('keydown', event => {
      if (event.key === 'Escape' && this.elements.heroOverflow?.dataset.open === 'true') {
        this.setHeroOverflowOpen(false);
        this.elements.heroOverflowToggle?.focus();
      }
    });
    this.elements.outputCopyBtn?.addEventListener('click', () => {
      this.copyCurrentResult();
      this.setOutputOverflowOpen(false);
    });
    this.elements.outputSaveNoteBtn?.addEventListener('click', () =>
      this.saveCurrentResultAsNote()
    );
    this.initResultSelectionActions();
    this.elements.outputCreateSkillBtn?.addEventListener('click', () => {
      this.openSkillDraftModal();
      this.setOutputOverflowOpen(false);
    });
    this.elements.outputPromoteBtn?.addEventListener('click', () => {
      this.previewResultPromotion();
      this.setOutputOverflowOpen(false);
    });
    this.elements.outputOverflowToggle?.addEventListener('click', event => {
      event.stopPropagation();
      this.setOutputOverflowOpen(this.elements.outputOverflow?.dataset.open !== 'true');
    });
    document.addEventListener('click', event => {
      if (!this.elements.outputOverflow) return;
      if (this.elements.outputOverflow.dataset.open !== 'true') return;
      if (this.elements.outputOverflow.contains(event.target)) return;
      this.setOutputOverflowOpen(false);
    });
    document.addEventListener('keydown', event => {
      if (event.key === 'Escape' && this.elements.outputOverflow?.dataset.open === 'true') {
        this.setOutputOverflowOpen(false);
        this.elements.outputOverflowToggle?.focus();
      }
    });
    this.elements.skillGenerateBtn?.addEventListener('click', () =>
      this.generateSkillPromptFromTask(true)
    );
    this.elements.skillSubmitBtn?.addEventListener('click', () => this.submitTaskSkillDraft());
    this.elements.skillModal?.addEventListener('hidden.bs.modal', () => {
      if (this.skillDraftSubmitting) return;
      if (this.skillDraftAbortController) {
        this.skillDraftAbortController.abort();
        this.skillDraftAbortController = null;
      }
      this.skillDraftGenerating = false;
      this.updateSkillDraftButtons();
    });
    this.elements.resultPromoteSubmitBtn?.addEventListener('click', () =>
      this.submitResultPromotion()
    );
    this.elements.resultPromoteModal?.addEventListener('hidden.bs.modal', () => {
      if (this.resultPromotionSubmitting) return;
      this.resultPromotionDraft = null;
    });
    this.elements.resultResearchSubmitBtn?.addEventListener('click', () =>
      this.submitResultResearchDraft()
    );
    this.elements.resultResearchRunInput?.addEventListener('change', () =>
      this.updateResultResearchSubmitLabel()
    );
    this.elements.resultResearchModal?.addEventListener('hidden.bs.modal', () => {
      if (this.resultResearchSubmitting) return;
      this.resultResearchDraft = null;
    });
    this.elements.blockedRequestToggle?.addEventListener('click', () =>
      this.toggleAssistResponseExpanded()
    );
    this.elements.workflowGenerateBtn?.addEventListener('click', () => this.handleGenerateSteps());
    this.elements.workflowAddStepBtn?.addEventListener('click', () => this.handleAddStep());
    this.elements.workflowRunAllBtn?.addEventListener('click', () => this.handleRunAllSteps());
    this.elements.scheduleCardEditBtn?.addEventListener('click', () => this.openScheduleModal());
    this.elements.scheduleOpenFolderBtn?.addEventListener('click', () =>
      this.openWorkspaceOutputDir()
    );
    this.elements.scheduleTypeInput?.addEventListener('change', () =>
      this.updateScheduleModalFields()
    );
    this.elements.scheduleWakeMacInput?.addEventListener('change', () =>
      this.updateScheduleModalFields()
    );
    [
      this.elements.scheduleEnabledInput,
      this.elements.scheduleNameInput,
      this.elements.scheduleTimeInput,
      this.elements.scheduleDayInput,
      this.elements.scheduleIntervalValueInput,
      this.elements.scheduleIntervalUnitInput,
      this.elements.scheduleOnceInput,
      this.elements.scheduleCronInput,
      this.elements.scheduleSleepPolicyInput,
      this.elements.scheduleWakeMacInput,
      this.elements.scheduleWakeLeadInput,
      this.elements.scheduleWakeFallbackInput
    ].forEach(element => {
      element?.addEventListener('input', () => this.updateSchedulePreview());
      element?.addEventListener('change', () => this.updateSchedulePreview());
    });
    this.elements.scheduleSubmitBtn?.addEventListener('click', () => this.saveSchedule());
    this.elements.scheduleRemoveBtn?.addEventListener('click', () => this.removeSchedule());
    this.elements.assistRetryBtn?.addEventListener('click', () => this.submitTaskAssist('retry'));
    this.elements.assistContinueBtn?.addEventListener('click', () =>
      this.submitTaskAssist('continue_with_instruction')
    );
    this.elements.assistSwitchBtn?.addEventListener('click', () =>
      this.submitTaskAssist('switch_agent_retry')
    );
    this.elements.assistFailBtn?.addEventListener('click', () =>
      this.submitTaskAssist('mark_failed')
    );
    this.elements.assistAgent?.addEventListener('change', () =>
      this.updateAssistSwitchButtonState()
    );
  }

  async loadData() {
    this.setState('loading');
    this.setAlert('');

    try {
      const [workspace, taskResponse, agents, taskEvents, outputDir] = await Promise.all([
        this.fetchWorkspace(),
        this.fetchTask(),
        this.fetchAgents().catch(() => []),
        this.fetchTaskEvents().catch(() => []),
        this.fetchWorkspaceOutputDir().catch(() => '')
      ]);

      this.workspace = workspace || null;
      this.workspaceOutputDir = outputDir || '';
      this.tasks = Array.isArray(workspace?.tasks) ? workspace.tasks : [];
      this.availableAgents = Array.isArray(agents) ? agents : [];
      this.taskEvents = Array.isArray(taskEvents) ? taskEvents : [];

      const workspaceTask = this.tasks.find(item => String(item?.id || '') === this.taskId) || null;
      this.task = taskResponse || workspaceTask;

      if (!this.task || String(this.task.workspace_id || this.workspaceId) !== this.workspaceId) {
        this.setState('empty');
        return;
      }

      if (!workspaceTask) {
        this.tasks = this.task ? [this.task] : [];
      }

      // Where this task came from, when a Plan created it. Not awaited: most
      // tasks have no plan, and the page should not wait on a lookup that
      // usually returns nothing.
      void this.loadRelatedPlan();

      this.workspaceRuns = await this.fetchWorkspaceRunsForTask(this.task).catch(() => []);
      this.currentRun = this.findWorkspaceRun(this.task?.current_run_id);
      this.seedLiveActivityFromHistory();
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

  // fetchWorkspaceOutputDir returns the resolved default output directory for
  // this workspace (<workspace>/outputs), used to show where "Default output
  // folder" actually writes.
  async fetchWorkspaceOutputDir() {
    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(this.workspaceId)}/output-dir`
    );
    if (!response.ok) return '';
    const data = await response.json();
    return String(data?.output_dir || '').trim();
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

  async fetchTaskEvents() {
    const params = new URLSearchParams({
      workspace_id: this.workspaceId,
      task_id: this.taskId,
      limit: '200'
    });
    const response = await fetch(`/api/orchestration/events?${params.toString()}`);
    if (!response.ok) return [];

    const payload = await response.json().catch(() => ({}));
    return Array.isArray(payload?.events) ? payload.events : [];
  }

  async fetchCurrentRun(runId) {
    const normalizedRunId = String(runId || '').trim();
    if (!normalizedRunId) return null;

    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(this.workspaceId)}/runs/${encodeURIComponent(normalizedRunId)}`
    );
    if (response.status === 404) return null;
    if (!response.ok) {
      throw new Error('Failed to load latest workspace run.');
    }
    return response.json();
  }

  async fetchWorkspaceRunsForTask(task = this.task) {
    const runIds = new Set();
    const currentRunId = String(task?.current_run_id || '').trim();
    if (currentRunId) runIds.add(currentRunId);

    (Array.isArray(task?.execution_history) ? task.execution_history : []).forEach(entry => {
      const runId = String(entry?.run_id || '').trim();
      if (runId) runIds.add(runId);
    });

    if (!runIds.size) return [];

    const runs = await Promise.all(
      [...runIds].map(runId => this.fetchCurrentRun(runId).catch(() => null))
    );

    return runs
      .filter(Boolean)
      .sort((left, right) => this.workspaceRunTimestamp(right) - this.workspaceRunTimestamp(left));
  }

  findWorkspaceRun(runId) {
    const normalizedRunId = String(runId || '').trim();
    if (!normalizedRunId) return null;
    return (
      (Array.isArray(this.workspaceRuns) ? this.workspaceRuns : []).find(
        run => run?.id === normalizedRunId
      ) || null
    );
  }

  setupRealtime() {
    if (
      this.workspaceRealtimeUnsubscribe ||
      !window.workspaceRealtime ||
      typeof window.workspaceRealtime.subscribeToWorkspace !== 'function'
    ) {
      return;
    }

    this.workspaceRealtimeUnsubscribe = window.workspaceRealtime.subscribeToWorkspace(
      this.workspaceId,
      event => {
        this.handleRealtimeEvent(event);
      }
    );
  }

  handleRealtimeEvent(event) {
    const eventType = String(event?.type || '').trim();
    if (!eventType.startsWith('task.') && !eventType.startsWith('delegation.')) {
      return;
    }

    const payload = event?.data?.data || event?.data || {};
    const eventTaskId = String(payload?.task_id || payload?.id || payload?.task?.id || '').trim();

    // Was previously a hard early-return for sibling events. That broke the
    // relationships card (parent / inputs / "Used By"), which depends on the
    // graph neighbors' current status — when an upstream task completed, this
    // page never knew. Now we still schedule a refresh; render() will detect
    // via per-section fingerprints that nothing about THIS task changed and
    // skip the heavy sub-renders.
    const isSelfEvent = !eventTaskId || eventTaskId === this.taskId;
    // Creation-style events affect the workspace's task graph (a new
    // dependent could appear, a new subtask could be delegated). The new
    // task is not yet in this.tasks so eventTaskIsNeighbor would return
    // false; let these through unconditionally and rely on the render
    // diff to no-op when they really are unrelated.
    const isStructuralEvent =
      eventType === 'task.created' ||
      eventType === 'task.delegated' ||
      eventType === 'task.deleted' ||
      eventType === 'task.assigned';
    if (!isSelfEvent && !isStructuralEvent && !this.eventTaskIsNeighbor(eventTaskId)) {
      return;
    }

    if (isSelfEvent) {
      this.captureLiveActivity(eventType, payload);
    }

    // Flash class previously fired on every task.* event to mask the
    // layout thrash from a full re-render. With per-section selective
    // rendering it's no longer needed for routine updates. We leave it
    // unset here; significant lifecycle transitions surface on their own
    // through the status pill / hero priority changes.

    window.clearTimeout(this.pendingRefreshTimer);
    this.pendingRefreshTimer = window.setTimeout(() => {
      this.loadData();
    }, 180);
  }

  // captureLiveActivity records the most recent task.* event for this task
  // so the live badge can show what the agent is currently doing. Without
  // this, in_progress tasks are visually indistinguishable from frozen ones
  // — see slice 1 of the stuck-task UX work.
  captureLiveActivity(eventType, payload) {
    if (eventType === 'task.heartbeat') {
      // Heartbeats only refresh the freshness counter; preserve the last
      // phase label. If we have no label yet (page refreshed mid-run before
      // any phase event), keep the old behaviour and just record activity.
      const prev = this._latestActivity;
      this._latestActivity = { at: Date.now(), label: prev?.label || '' };
      this.renderLiveBadge();
      return;
    }

    const label = this.activityLabelFor(eventType, payload);
    if (label === null) return;
    this._latestActivity = { at: Date.now(), label };
    this.renderLiveBadge();
  }

  // activityLabelFor maps a task.* event into the human label rendered on
  // the live badge. Returns null for events that should not change the
  // label (callers decide whether to ignore the event entirely or just
  // refresh the timestamp).
  activityLabelFor(eventType, payload) {
    const data = payload && typeof payload === 'object' ? payload : {};
    switch (eventType) {
      case 'task.thinking': {
        const phase = String(data.phase || '').trim();
        if (phase === 'awaiting_llm') return 'Awaiting model response';
        if (phase === 'llm_returned') {
          const calls = Number(data.tool_call_count || 0);
          return calls > 0
            ? `Processing ${calls} tool call${calls === 1 ? '' : 's'}`
            : 'Processing model response';
        }
        if (phase === 'starting') return 'Analyzing task';
        return 'Thinking';
      }
      case 'task.tool_call': {
        const tool = String(data.tool_name || '').trim();
        return tool ? `Calling ${tool}` : 'Calling tool';
      }
      case 'task.tool_result': {
        const tool = String(data.tool_name || '').trim();
        const success = data.success !== false;
        if (tool) return success ? `${tool} returned` : `${tool} failed`;
        return 'Tool finished';
      }
      case 'task.started':
        return 'Started';
      case 'task.resumed':
        return 'Resumed';
      case 'task.assigned': {
        const mode = String(data.assignment_mode || '').trim();
        const agent = String(data.target_agent || data.agent || '').trim();
        if (mode === 'dynamic_delegation' && agent) return `Delegated to ${agent}`;
        return null;
      }
      case 'delegation.started':
        return 'Coordinator adapting';
      case 'delegation.completed':
        return 'Delegation resolved';
      case 'delegation.failed':
        return 'Delegation failed';
      case 'delegation.cap_hit':
        return 'Delegation reached its limit';
      default:
        return null;
    }
  }

  // seedLiveActivityFromHistory walks taskEvents (loaded by /api/orchestration/events)
  // backwards and lifts the most recent phase-bearing event into _latestActivity
  // so a page refresh during a running task immediately shows context instead
  // of a blank "Live" until the next event arrives.
  seedLiveActivityFromHistory() {
    if (!Array.isArray(this.taskEvents) || this.taskEvents.length === 0) return;
    if (this._latestActivity) return;
    for (let i = this.taskEvents.length - 1; i >= 0; i--) {
      const ev = this.taskEvents[i];
      if (!ev) continue;
      const type = String(ev.type || '').trim();
      const payload = ev?.data?.data || ev?.data || {};
      const evTaskId = String(payload?.task_id || ev?.task_id || '').trim();
      if (evTaskId && evTaskId !== this.taskId) continue;
      const label = this.activityLabelFor(type, payload);
      if (label === null) continue;
      const ts = ev.timestamp ? Date.parse(ev.timestamp) : NaN;
      this._latestActivity = {
        at: Number.isFinite(ts) ? ts : Date.now(),
        label
      };
      return;
    }
  }

  // formatRelativeAgo renders the freshness component of the live badge.
  // Sub-second flickers are rounded to "just now" to avoid the badge
  // visually thrashing on a fast tool sequence.
  formatRelativeAgo(timestampMs) {
    if (!timestampMs) return '';
    const elapsed = Math.max(0, Date.now() - timestampMs);
    const seconds = Math.floor(elapsed / 1000);
    if (seconds < 2) return 'just now';
    if (seconds < 60) return `${seconds}s ago`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes}m ago`;
    const hours = Math.floor(minutes / 60);
    return `${hours}h ago`;
  }

  renderLiveBadge() {
    // The former separate live badge has been folded into the status pill:
    // when the task is streaming we set data-live="true" on the pill, swap
    // its ::before icon for an inline pulsing dot, and replace the "In
    // Progress" label with the latest activity phase + relative timestamp.
    const pill = this.elements.status;
    if (!pill) return;

    const isRunning =
      String(this.task?.status || '')
        .trim()
        .toLowerCase() === 'in_progress';
    const isLive = isRunning && this.workspaceRealtimeUnsubscribe;
    if (!isLive) {
      pill.removeAttribute('data-live');
      this.stopActivityTick();
      return;
    }

    const activity = this._latestActivity;
    let text = 'Live';
    if (activity && activity.label) {
      const ago = this.formatRelativeAgo(activity.at);
      text = ago ? `${activity.label} · ${ago}` : activity.label;
    }
    pill.setAttribute('data-live', 'true');
    pill.innerHTML = `<span class="workspace-task-page-live-dot"></span>${this.escapeHtml(text)}`;

    this.startActivityTick();
  }

  startActivityTick() {
    if (this._activityTickHandle) return;
    // 2s cadence keeps the "Xs ago" counter live without burning CPU; the
    // badge text only changes once per second of real wall-clock anyway.
    this._activityTickHandle = window.setInterval(() => {
      const isRunning =
        String(this.task?.status || '')
          .trim()
          .toLowerCase() === 'in_progress';
      if (!isRunning) {
        this.stopActivityTick();
        return;
      }
      this.renderLiveBadge();
    }, 2000);
  }

  stopActivityTick() {
    if (this._activityTickHandle) {
      window.clearInterval(this._activityTickHandle);
      this._activityTickHandle = null;
    }
  }

  // eventTaskIsNeighbor: true when the event refers to a task that this
  // page's relationships or workflow card visualises (parent, an input
  // producer, a downstream consumer, or a child). Lets us refresh on
  // sibling events without re-rendering for every unrelated task.* event
  // on the workspace.
  eventTaskIsNeighbor(eventTaskId) {
    const id = String(eventTaskId || '').trim();
    if (!id) return false;
    const t = this.task;
    if (!t) return false;
    if (String(t.parent_task_id || '').trim() === id) return true;
    if (
      Array.isArray(t.input_task_ids) &&
      t.input_task_ids.some(x => String(x || '').trim() === id)
    )
      return true;
    for (const sibling of this.tasks) {
      if (!sibling || sibling.id === t.id) continue;
      if (String(sibling.parent_task_id || '').trim() === t.id && sibling.id === id) return true;
      const inputs = Array.isArray(sibling.input_task_ids) ? sibling.input_task_ids : [];
      if (sibling.id === id && inputs.some(x => String(x || '').trim() === t.id)) return true;
    }
    return false;
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
    const actionsContainer = titleElement.parentElement?.querySelector(
      '.workspace-task-page-title-actions'
    );
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

    const finishEdit = async save => {
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

    editActions
      .querySelector('.workspace-task-page-edit-save')
      ?.addEventListener('mousedown', e => {
        e.preventDefault();
        finishEdit(true);
      });
    editActions
      .querySelector('.workspace-task-page-edit-cancel')
      ?.addEventListener('mousedown', e => {
        e.preventDefault();
        finishEdit(false);
      });

    input.addEventListener('input', syncHeight);
    input.addEventListener('keydown', event => {
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
    input.addEventListener('blur', e => {
      if (editActions.contains(e.relatedTarget)) return;
      finishEdit(true);
    });
  }

  startHeroDetailsEdit() {
    if (this.detailsEditInProgress || !this.elements.subtitle) return;

    const subtitle = this.elements.subtitle;
    const editButton = this.elements.detailsEditBtn;
    const subtitleRow =
      subtitle.closest('.workspace-task-page-subtitle-row') || subtitle.parentElement;
    const currentValue = String(this.task?.details || '').trim();

    this.detailsEditInProgress = true;

    const editorWrap = document.createElement('div');
    editorWrap.className = 'workspace-task-page-subtitle-input-wrap';

    const textarea = document.createElement('textarea');
    textarea.className = 'workspace-task-page-subtitle-input';
    textarea.rows = 4;
    textarea.value = currentValue;
    textarea.placeholder =
      'Add source preferences, constraints, context, or anything the agent should know before running this task.';
    textarea.setAttribute('aria-label', 'Edit additional task details');

    const editActions = document.createElement('div');
    editActions.className = 'workspace-task-page-title-edit-actions';
    editActions.innerHTML = `
      <button type="button" class="workspace-task-page-edit-save" aria-label="Save additional details">Save</button>
      <button type="button" class="workspace-task-page-edit-cancel" aria-label="Cancel editing additional details">Cancel</button>
      <span class="workspace-task-page-edit-hint">Cmd/Ctrl+Enter to save, Esc to cancel</span>
    `;

    editorWrap.appendChild(textarea);
    editorWrap.appendChild(editActions);

    subtitle.style.display = 'none';
    if (editButton) editButton.style.display = 'none';
    subtitle.insertAdjacentElement('afterend', editorWrap);

    const syncHeight = () => {
      textarea.style.height = 'auto';
      textarea.style.height = `${Math.max(textarea.scrollHeight, 120)}px`;
    };

    syncHeight();
    textarea.focus();
    textarea.select();

    const finishEdit = async save => {
      if (!this.detailsEditInProgress) return;
      this.detailsEditInProgress = false;

      const nextValue = textarea.value.trim();
      editorWrap.remove();
      subtitle.style.display = '';
      if (editButton) editButton.style.display = '';
      if (subtitleRow) subtitleRow.classList.remove('is-editing');

      if (!save || nextValue === currentValue) {
        return;
      }

      try {
        await this.updateTaskFields({ details: nextValue });
        this.notify(
          'success',
          nextValue ? 'Additional details updated' : 'Additional details cleared'
        );
      } catch (error) {
        console.error('Failed to update task details:', error);
        this.notify('error', error?.message || 'Failed to update additional details');
      }
    };

    if (subtitleRow) subtitleRow.classList.add('is-editing');

    editActions
      .querySelector('.workspace-task-page-edit-save')
      ?.addEventListener('mousedown', e => {
        e.preventDefault();
        finishEdit(true);
      });
    editActions
      .querySelector('.workspace-task-page-edit-cancel')
      ?.addEventListener('mousedown', e => {
        e.preventDefault();
        finishEdit(false);
      });

    textarea.addEventListener('input', syncHeight);
    textarea.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        event.preventDefault();
        finishEdit(false);
        return;
      }

      if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        finishEdit(true);
      }
    });
    textarea.addEventListener('blur', event => {
      if (editActions.contains(event.relatedTarget)) return;
      finishEdit(true);
    });
  }

  async updateTaskFields(patch, options = {}) {
    const { deferRender = false } = options;
    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(this.taskId)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(this.parseResponseError(text, 'Failed to update task'));
    }

    const payload = await response.json();
    const updatedTask = payload?.task || (payload?.id ? payload : null);
    this.task = updatedTask ? { ...this.task, ...updatedTask } : { ...this.task, ...patch };
    if (Array.isArray(this.tasks)) {
      this.tasks = this.tasks.map(task =>
        String(task?.id || '') === String(this.taskId)
          ? { ...task, ...(updatedTask || patch) }
          : task
      );
    }
    // The synchronous render() pipeline runs 14 sub-renders (markdown, trace,
    // schedule, etc.) and on a task with sizeable execution history can stall
    // the main thread long enough that callers awaiting this method block
    // their visible UX (e.g. modal close). deferRender lets the caller close
    // the modal first and let the heavy re-render paint in the next frame.
    if (deferRender) {
      window.requestAnimationFrame(() => this.render());
    } else {
      this.render();
    }
    return this.task;
  }

  // setHeroOverflowOpen toggles the hero "more actions" menu (Copy link,
  // Delete). The menu is position: fixed because the hero card sets
  // overflow: hidden, which clipped the popover when it was anchored with
  // position: absolute. Coordinates are derived from the toggle's bounding
  // rect on each open; scroll/resize close the menu so the position can't
  // drift out of sync. Mirrors setOutputOverflowOpen.
  setHeroOverflowOpen(open) {
    const container = this.elements.heroOverflow;
    const toggle = this.elements.heroOverflowToggle;
    const menu = this.elements.heroOverflowMenu;
    if (!container || !toggle || !menu) return;

    const next = Boolean(open);
    container.dataset.open = next ? 'true' : 'false';
    toggle.setAttribute('aria-expanded', next ? 'true' : 'false');

    if (next) {
      menu.hidden = false;
      this.positionHeroOverflowMenu();
      this.bindHeroOverflowDismissHandlers();
    } else {
      menu.hidden = true;
      menu.style.top = '';
      menu.style.left = '';
      this.unbindHeroOverflowDismissHandlers();
    }
  }

  positionHeroOverflowMenu() {
    const toggle = this.elements.heroOverflowToggle;
    const menu = this.elements.heroOverflowMenu;
    if (!toggle || !menu) return;

    const toggleRect = toggle.getBoundingClientRect();
    const gap = 6;
    // Measure the menu now that it's visible. Clamp to the viewport so the
    // popover doesn't slip off-screen on narrow widths or near the edge.
    const menuRect = menu.getBoundingClientRect();
    const menuWidth = menuRect.width || 176;
    const menuHeight = menuRect.height || 100;
    const viewportPad = 8;

    let left = toggleRect.right - menuWidth;
    if (left < viewportPad) left = viewportPad;
    const maxLeft = window.innerWidth - menuWidth - viewportPad;
    if (left > maxLeft) left = maxLeft;

    let top = toggleRect.bottom + gap;
    if (top + menuHeight > window.innerHeight - viewportPad) {
      top = toggleRect.top - menuHeight - gap;
    }
    if (top < viewportPad) top = viewportPad;

    menu.style.top = `${Math.round(top)}px`;
    menu.style.left = `${Math.round(left)}px`;
  }

  bindHeroOverflowDismissHandlers() {
    if (this._heroOverflowDismissBound) return;
    this._heroOverflowDismiss = () => this.setHeroOverflowOpen(false);
    window.addEventListener('scroll', this._heroOverflowDismiss, { passive: true, capture: true });
    window.addEventListener('resize', this._heroOverflowDismiss);
    this._heroOverflowDismissBound = true;
  }

  unbindHeroOverflowDismissHandlers() {
    if (!this._heroOverflowDismissBound) return;
    window.removeEventListener('scroll', this._heroOverflowDismiss, { capture: true });
    window.removeEventListener('resize', this._heroOverflowDismiss);
    this._heroOverflowDismissBound = false;
  }

  // setOutputOverflowOpen drives the demoted-actions popover (Copy,
  // Create Skill, Create Workflow Task). The toggle's aria-expanded and
  // the container's data-open state are kept in sync, and the menu's
  // hidden attribute removes its items from the tab order when closed.
  //
  // The menu is position: fixed because its parent card sets
  // overflow: hidden to keep its gradient inside the rounded corners,
  // which previously clipped the popover. Coordinates are derived from
  // the toggle's bounding rect on each open; scroll/resize close the
  // menu so the position can't drift out of sync.
  setOutputOverflowOpen(open) {
    const container = this.elements.outputOverflow;
    const toggle = this.elements.outputOverflowToggle;
    const menu = this.elements.outputOverflowMenu;
    if (!container || !toggle || !menu) return;

    const next = Boolean(open);
    container.dataset.open = next ? 'true' : 'false';
    toggle.setAttribute('aria-expanded', next ? 'true' : 'false');
    container
      .closest('.workspace-task-page-card-header')
      ?.classList.toggle('is-output-menu-open', next);

    if (next) {
      menu.hidden = false;
      this.positionOutputOverflowMenu();
      this.bindOutputOverflowDismissHandlers();

      const firstItem = menu.querySelector('[role="menuitem"]:not([hidden]):not([disabled])');
      if (firstItem && typeof firstItem.focus === 'function') {
        window.requestAnimationFrame(() => firstItem.focus());
      }
    } else {
      menu.hidden = true;
      menu.style.top = '';
      menu.style.left = '';
      this.unbindOutputOverflowDismissHandlers();
    }
  }

  positionOutputOverflowMenu() {
    const toggle = this.elements.outputOverflowToggle;
    const menu = this.elements.outputOverflowMenu;
    if (!toggle || !menu) return;

    const toggleRect = toggle.getBoundingClientRect();
    const gap = 6;
    // Measure the menu now that it's visible. Clamp to the viewport so the
    // popover doesn't slip off-screen on narrow widths or near the edge.
    const menuRect = menu.getBoundingClientRect();
    const menuWidth = menuRect.width || 224;
    const menuHeight = menuRect.height || 132;
    const viewportPad = 8;

    let left = toggleRect.right - menuWidth;
    if (left < viewportPad) left = viewportPad;
    const maxLeft = window.innerWidth - menuWidth - viewportPad;
    if (left > maxLeft) left = maxLeft;

    let top = toggleRect.bottom + gap;
    if (top + menuHeight > window.innerHeight - viewportPad) {
      top = toggleRect.top - menuHeight - gap;
    }
    if (top < viewportPad) top = viewportPad;

    menu.style.top = `${Math.round(top)}px`;
    menu.style.left = `${Math.round(left)}px`;
  }

  bindOutputOverflowDismissHandlers() {
    if (this._outputOverflowDismissBound) return;
    this._outputOverflowDismiss = () => this.setOutputOverflowOpen(false);
    window.addEventListener('scroll', this._outputOverflowDismiss, {
      passive: true,
      capture: true
    });
    window.addEventListener('resize', this._outputOverflowDismiss);
    this._outputOverflowDismissBound = true;
  }

  unbindOutputOverflowDismissHandlers() {
    if (!this._outputOverflowDismissBound) return;
    window.removeEventListener('scroll', this._outputOverflowDismiss, { capture: true });
    window.removeEventListener('resize', this._outputOverflowDismiss);
    this._outputOverflowDismissBound = false;
  }

  // toggleFollowupPanel shows/hides the inline follow-up creation form
  // beneath the Result card. When opening, it pre-fills the agent picker
  // with the current task's agent (or the workspace's first available
  // agent) and focuses the description input so the user can type
  // immediately.
  toggleFollowupPanel(open) {
    const panel = this.elements.followupPanel;
    if (!panel) return;
    const next = open === undefined ? !this._followupOpen : Boolean(open);
    this._followupOpen = next;
    panel.hidden = !next;

    if (this.elements.followupError) this.elements.followupError.hidden = true;
    if (!next) return;

    this.populateFollowupAgentOptions();
    if (this.elements.followupDescription) {
      this.elements.followupDescription.value = '';
      // Defer focus to next frame so the panel is in the layout flow before
      // we try to scroll/focus into it.
      window.requestAnimationFrame(() => this.elements.followupDescription?.focus());
    }
    if (this.elements.followupDetails) this.elements.followupDetails.value = '';
    // Always collapse the "Add constraints" disclosure when the panel
    // re-opens, so successive follow-ups start with a clean two-field form.
    this.setFollowupDetailsOpen(false);
  }

  toggleFollowupDetails() {
    const container = this.elements.followupDetailsCollapsible;
    if (!container) return;
    this.setFollowupDetailsOpen(container.dataset.open !== 'true');
  }

  setFollowupDetailsOpen(open) {
    const container = this.elements.followupDetailsCollapsible;
    const toggle = this.elements.followupDetailsToggle;
    const field = this.elements.followupDetailsField;
    if (!container || !toggle || !field) return;

    const next = Boolean(open);
    container.dataset.open = next ? 'true' : 'false';
    toggle.setAttribute('aria-expanded', next ? 'true' : 'false');
    field.hidden = !next;
    if (next) {
      window.requestAnimationFrame(() => this.elements.followupDetails?.focus());
    }
  }

  populateFollowupAgentOptions() {
    const select = this.elements.followupAgent;
    if (!select) return;
    const currentAgent = String(this.task?.to || '').trim();
    const agents = this.getAssignableAgentNames(currentAgent);
    const options = ['<option value="">Unassigned (auto-assign on run)</option>'];
    let preselected = false;
    for (const name of agents) {
      const trimmed = String(name || '').trim();
      if (!trimmed) continue;
      const isCurrent = trimmed.toLowerCase() === currentAgent.toLowerCase();
      if (isCurrent) preselected = true;
      options.push(
        `<option value="${this.escapeHtml(trimmed)}" ${isCurrent ? 'selected' : ''}>${this.escapeHtml(trimmed)}</option>`
      );
    }
    if (currentAgent && !preselected && !this.isRunnableAgentName(currentAgent)) {
      // Source task is assigned to an agent that no longer exists; don't
      // auto-pick it for the follow-up.
    }
    select.innerHTML = options.join('');
  }

  setFollowupError(message = '') {
    const el = this.elements.followupError;
    if (!el) return;
    const trimmed = String(message || '').trim();
    el.textContent = trimmed;
    el.hidden = !trimmed;
  }

  async submitFollowupTask() {
    if (this._followupSubmitting) return;
    const desc = String(this.elements.followupDescription?.value || '').trim();
    if (!desc) {
      this.setFollowupError('Describe what the follow-up task should do.');
      this.elements.followupDescription?.focus();
      return;
    }
    if (!this.task?.id) {
      this.setFollowupError('Source task is not loaded yet — try again in a moment.');
      return;
    }
    this.setFollowupError('');

    const details = String(this.elements.followupDetails?.value || '').trim();
    const agent = String(this.elements.followupAgent?.value || '').trim();

    const submitBtn = this.elements.followupSubmit;
    const submitText = submitBtn?.querySelector('span');
    const originalText = submitText?.textContent || 'Create follow-up';
    this._followupSubmitting = true;
    if (submitBtn) submitBtn.disabled = true;
    if (submitText) submitText.textContent = 'Creating…';

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          description: desc,
          details: details || undefined,
          status: 'pending',
          to: agent || undefined,
          input_task_ids: [this.task.id]
        })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(this.parseResponseError(text, 'Failed to create follow-up task.'));
      }
      const payload = await response.json();
      const created = payload?.task || (payload?.id ? payload : null);
      const newTaskId = String(created?.id || '').trim();

      this.toggleFollowupPanel(false);
      this.notify('success', 'Follow-up task created');

      if (newTaskId) {
        // Send the user straight to the new task page so they can run /
        // edit it immediately. Using a full navigation (not history.push)
        // because each task page bootstraps its own controller.
        window.location.href = `/workspaces/${encodeURIComponent(this.workspaceId)}/task/${encodeURIComponent(newTaskId)}`;
      }
    } catch (error) {
      console.error('Failed to create follow-up task:', error);
      this.setFollowupError(error?.message || 'Failed to create follow-up task.');
    } finally {
      this._followupSubmitting = false;
      if (submitBtn) submitBtn.disabled = false;
      if (submitText) submitText.textContent = originalText;
    }
  }

  parseResponseError(text, fallback) {
    const value = String(text || '').trim();
    if (!value) return fallback;
    try {
      const payload = JSON.parse(value);
      return payload?.message || payload?.error || fallback;
    } catch (_error) {
      return value;
    }
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

  getRunHref(runId) {
    return `/workspaces/${encodeURIComponent(this.workspaceId)}/runs/${encodeURIComponent(runId)}`;
  }

  getTaskHumanLoop(task = this.task) {
    const humanLoop = task?.context?.human_loop;
    return humanLoop && typeof humanLoop === 'object' ? humanLoop : null;
  }

  getTaskStatusPresentation(task = this.task) {
    const humanLoop = this.getTaskHumanLoop(task);
    const status = String(task?.status || '')
      .trim()
      .toLowerCase();
    const humanLoopState = String(humanLoop?.state || '')
      .trim()
      .toLowerCase();
    const waiting = status === 'waiting_for_choice' || humanLoopState === 'waiting_for_choice';
    const blocked =
      status === 'blocked' ||
      waiting ||
      humanLoopState === 'blocked' ||
      Boolean(humanLoop?.reason) ||
      Boolean(humanLoop?.question);

    return {
      isBlocked: blocked,
      label: waiting
        ? 'Waiting for Choice'
        : blocked
          ? 'Needs Input'
          : getDisplayStatus(task?.status),
      className: blocked ? 'blocked' : getStatusClass(task?.status),
      reason: String(humanLoop?.reason || '').trim()
    };
  }

  normalizeAssistFieldValues(value) {
    const result = {};
    if (!Array.isArray(value)) return result;

    value.forEach(item => {
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

    const stepType = String(value.step_type || value.stepType || '')
      .trim()
      .toLowerCase();
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
            number: String(choice?.number || '').trim(),
            recommended: choice?.recommended === true
          };
        })
        .filter(Boolean);
    }

    if (stepType === 'ask_form' && Array.isArray(value.fields)) {
      normalized.fields = value.fields
        .map((field, index) => {
          const id = String(field?.id || '').trim() || `field-${index + 1}`;
          const label = String(field?.label || '').trim() || `Question ${index + 1}`;
          const type = String(field?.type || 'text')
            .trim()
            .toLowerCase();
          const options = Array.isArray(field?.options)
            ? field.options
                .map((option, optionIndex) => ({
                  value: String(option?.value || '').trim(),
                  label: String(option?.label || option?.value || '').trim(),
                  description: String(option?.description || '').trim(),
                  key:
                    String(option?.key || '').trim() || String.fromCharCode(65 + (optionIndex % 26))
                }))
                .filter(option => option.value && option.label)
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
    const workflowStep =
      this.normalizeAssistWorkflowStep(
        humanLoop?.workflow_step || task?.context?.planning_workflow_step
      ) ||
      this.normalizeAssistWorkflowStep(
        deriveAssistWorkflowStepFromText(humanLoop?.question, humanLoop?.agent_response)
      );
    const selectedFieldValues = this.normalizeAssistFieldValues(humanLoop?.field_values);
    const selectState = buildAssistSelectState(workflowStep, selectedFieldValues);

    return {
      taskId: String(task?.id || '').trim(),
      blockId: String(humanLoop?.block_id || '').trim(),
      currentAgent: String(task?.to || '').trim(),
      reasonCode: String(humanLoop?.reason_code || '')
        .trim()
        .toLowerCase(),
      reason: String(
        humanLoop?.reason || 'This task needs your input before it can continue.'
      ).trim(),
      question: String(humanLoop?.question || '').trim(),
      response: String(humanLoop?.agent_response || '').trim(),
      suggestedActions: Array.isArray(humanLoop?.suggested_actions)
        ? humanLoop.suggested_actions.map(action => String(action || '').trim()).filter(Boolean)
        : [],
      // A structured repair marks this block as repair-gated: the failure cannot
      // be fixed by running the task again, so the view offers the repair and
      // withholds Retry until the precondition is healthy (FR 56; Design).
      repair: normalizeBlockedRepair(humanLoop?.repair),
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
      this.availableAgents.forEach(item => {
        const name =
          typeof item === 'string' ? String(item || '').trim() : String(item?.name || '').trim();
        if (name) names.add(name);
      });
    }

    return Array.from(names).sort((left, right) => left.localeCompare(right));
  }

  isRunnableAgentName(agentName) {
    const normalizedTarget = String(agentName || '')
      .trim()
      .toLowerCase();
    if (!normalizedTarget || normalizedTarget === 'unassigned') return true;

    const matches = name =>
      String(name || '')
        .trim()
        .toLowerCase() === normalizedTarget;
    if (this.getAvailableAgentNames().some(matches)) return true;

    // A workspace-scoped agent (declared in the workspace, with a local
    // snapshot) is runnable for this workspace's tasks even when it isn't in
    // the global /api/agents registry. Check the workspace's declared agents
    // only — not getWorkspaceAgentNames(), which self-includes task.to and
    // would mask a genuinely-missing assignment.
    const declared = [
      ...(this.workspace?.agents || []),
      ...(this.workspace?.agent_instances || []).map(
        instance => instance?.role || instance?.name || ''
      )
    ];
    return declared.some(matches);
  }

  getAssignableAgentNames() {
    // Merge global agents (/api/agents) with the workspace's own declared
    // agents so workspace-scoped agents stay selectable even when global
    // agents exist. We intentionally read the declared agents directly
    // (workspace.agents + agent_instances) rather than getWorkspaceAgentNames(),
    // which self-includes task.to and would surface a genuinely-missing
    // assignment as a normal option (it's shown as a disabled "(Unavailable)"
    // entry instead).
    const names = new Set();
    const add = name => {
      const trimmed = String(name || '').trim();
      if (trimmed) names.add(trimmed);
    };
    this.getAvailableAgentNames().forEach(add);
    (this.workspace?.agents || []).forEach(add);
    (this.workspace?.agent_instances || []).forEach(instance =>
      add(instance?.role || instance?.name || '')
    );
    return Array.from(names).sort((left, right) => left.localeCompare(right));
  }

  getWorkspaceAgentNames() {
    const names = new Set();

    if (Array.isArray(this.workspace?.agent_instances)) {
      this.workspace.agent_instances.forEach(instance => {
        const name = String(instance?.role || instance?.name || '').trim();
        if (name) names.add(name);
      });
    }

    if (Array.isArray(this.workspace?.agents)) {
      this.workspace.agents.forEach(name => {
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
    return this.tasks.find(item => String(item?.id || '') === parentTaskId) || null;
  }

  getSubtasks() {
    return this.getChildTasks(this.task?.id || '');
  }

  getInputTasks() {
    const inputIds = Array.isArray(this.task?.input_task_ids) ? this.task.input_task_ids : [];
    if (inputIds.length === 0) return [];

    return inputIds
      .map(
        taskId =>
          this.tasks.find(item => String(item?.id || '') === String(taskId || '').trim()) || null
      )
      .filter(Boolean);
  }

  // getDependentTasks: reverse of getInputTasks. Returns the set of tasks
  // whose input_task_ids reference this task — i.e. tasks downstream that
  // consume this task's output. The relationships card uses this to show the
  // user "what depends on this," which previously required clicking through
  // every other task to discover the back-edge.
  //
  // Filters out the current task itself: legacy data may carry a self-input
  // edge (the task graph validator now rejects this on AddTask, but existing
  // stored tasks may still have it). Showing self-references would display
  // the current task in its own "Used By" list and is never useful.
  getDependentTasks() {
    const myId = String(this.task?.id || '').trim();
    if (!myId) return [];
    return this.tasks.filter(item => {
      if (String(item?.id || '').trim() === myId) return false;
      const inputs = Array.isArray(item?.input_task_ids) ? item.input_task_ids : [];
      return inputs.some(id => String(id || '').trim() === myId);
    });
  }

  render() {
    const statusInfo = this.getTaskStatusPresentation();
    this.currentBlockedTask = statusInfo.isBlocked ? this.buildBlockedTaskState() : null;
    this.taskAssistResponseExpanded = false;
    this.assistReviewMode = false;

    // Selective rendering: each sub-render is only invoked when its inputs
    // actually changed since the previous render. The first render after
    // page load (or after forceFullRender()) fills the cache and triggers
    // every sub-render exactly once. Subsequent renders triggered by
    // realtime events skip the sub-renders whose data is unchanged, which
    // on a busy workspace is the difference between 14 sub-renders fired
    // 12+ times per minute and just the 2-3 that actually need to update.
    const inputs = this._renderInputs(statusInfo);
    if (!this._renderCache) this._renderCache = {};
    const cache = this._renderCache;

    const dispatch = (key, fn) => {
      if (cache[key] === inputs[key]) return;
      cache[key] = inputs[key];
      fn();
    };

    dispatch('hero', () => this.renderHero(statusInfo));
    dispatch('heroActions', () => this.renderHeroActions(statusInfo));
    dispatch('heroAgent', () => this.renderHeroAgent(statusInfo));
    dispatch('overview', () => this.renderOverview());
    dispatch('relationships', () => this.renderRelationships());
    dispatch('workflow', () => this.renderWorkflow());
    dispatch('output', () => this.renderOutput());
    dispatch('outputShape', () => this.renderTaskOutputShape());
    dispatch('workspaceRuns', () => this.renderWorkspaceRunsCard());
    dispatch('runs', () => this.renderRunsCard());
    dispatch('schedule', () => this.renderSchedule());
    dispatch('context', () => this.renderContext());
    dispatch('blockedState', () => this.renderBlockedState(statusInfo));
  }

  // forceFullRender clears the per-section input cache so the next render()
  // invocation runs every sub-render unconditionally. Use after destructive
  // actions (delete, structural reset) where the prior cache no longer
  // describes the page.
  forceFullRender() {
    this._renderCache = null;
  }

  // _renderInputs builds a per-section input fingerprint. Each value must be
  // a string (typically a JSON-stringified array of the data the section
  // reads). Two renders with identical fingerprints are guaranteed to
  // produce identical DOM, so the dispatcher in render() can safely skip.
  //
  // Adding a new sub-render requires:
  //   1. Add an entry here returning a stable string of its inputs.
  //   2. Add a dispatch() call in render().
  //
  // If a sub-render reads data NOT covered by its fingerprint, that data
  // change won't trigger a re-render — keep this conservative.
  _renderInputs(statusInfo) {
    const t = this.task || {};
    const blocked = this.currentBlockedTask;
    const sigStatusInfo = JSON.stringify([
      statusInfo?.label,
      statusInfo?.className,
      statusInfo?.isBlocked,
      statusInfo?.waiting
    ]);
    const sigBlocked = blocked
      ? JSON.stringify([
          blocked.reason,
          blocked.reasonCode,
          blocked.question,
          blocked.response,
          blocked.workflowStep?.id,
          blocked.workflowStep?.stepType,
          blocked.currentAgent,
          blocked.suggestedActions,
          blocked.selectedChoiceId
        ])
      : 'null';
    const sigGraphNeighbors = this._taskGraphNeighborsFingerprint();
    const sigWorkflowSubtree = this._taskWorkflowSubtreeFingerprint();
    const storageOwner = this.getTaskResultStorageTask() || {};
    const sigAutomation = JSON.stringify([
      storageOwner?.id || '',
      storageOwner?.result_storage || null,
      storageOwner?.output_spec || null,
      storageOwner?.draft_output_spec || null,
      storageOwner?.output_contract || null,
      this.resultOutputSpecDraft || null,
      Array.isArray(this.resultContractDraft) ? this.resultContractDraft : null,
      Boolean(this.resultContractSuggesting),
      Boolean(this.resultContractSaving),
      Boolean(this.automationStorageToggleBusy),
      Boolean(this.automationStorageSaving),
      (this.workspace?.store_nodes || []).map(node => [
        node?.id,
        node?.canvas_node_id,
        node?.name,
        node?.base_dir
      ])
    ]);

    return {
      hero: JSON.stringify([
        t.id,
        t.status,
        t.description,
        t.details,
        t.priority,
        this.workspace?.name,
        sigStatusInfo
      ]),
      heroActions: JSON.stringify([t.id, t.status, t.execution_mode, sigStatusInfo]),
      heroAgent: JSON.stringify([
        t.id,
        t.to,
        t.status,
        (this.availableAgents || [])
          .map(a => a?.name || '')
          .sort()
          .join('|')
      ]),
      overview: JSON.stringify([
        t.id,
        t.from,
        t.to,
        t.execution_mode,
        t.orchestration_mode,
        t.template_ref,
        t.timeout,
        t.progress?.percentage,
        t.current_run_id,
        t.details,
        t.result_storage,
        this.getSubtasks().map(step => [step?.id, step?.subtask_index, step?.result_storage]),
        this.currentRun?.id,
        this.currentRun?.status,
        this.currentRun?.profile_snapshot?.id,
        this.currentRun?.report?.validation_status,
        this.currentRun?.started_at,
        (this.workspace?.store_nodes || []).map(node => [
          node?.id,
          node?.canvas_node_id,
          node?.name,
          node?.base_dir
        ]),
        sigStatusInfo
      ]),
      relationships: JSON.stringify([t.id, t.parent_task_id, t.input_task_ids, sigGraphNeighbors]),
      workflow: JSON.stringify([t.id, sigWorkflowSubtree, this.workflowDraftPending]),
      output: JSON.stringify([
        t.id,
        t.status,
        t.result,
        t.result_type,
        t.structured_result,
        t.context?.structured_output,
        Array.isArray(t.execution_history) ? t.execution_history.length : 0,
        t.execution_history?.[t.execution_history.length - 1]?.executed_at || '',
        t.execution_history?.[t.execution_history.length - 1]?.status || '',
        t.execution_history?.[t.execution_history.length - 1]?.summary || '',
        t.execution_history?.[t.execution_history.length - 1]?.result || ''
      ]),
      outputShape: JSON.stringify([
        t.id,
        t.output_spec || null,
        t.output_schema || null,
        t.output_contract || null
      ]),
      workspaceRuns: JSON.stringify([
        t.id,
        t.current_run_id,
        Array.isArray(t.execution_history)
          ? t.execution_history.map(entry => entry?.run_id || '').join('|')
          : '',
        this.workspaceRuns.map(run => [
          run?.id,
          run?.status,
          run?.profile_snapshot?.id,
          run?.executor?.kind,
          run?.executor?.ref,
          run?.report?.validation_status,
          run?.report?.summary,
          run?.created_at,
          run?.started_at,
          run?.finished_at
        ])
      ]),
      runs: JSON.stringify([
        t.id,
        t.execution_steps,
        t.execution_history?.length,
        t.execution_trace,
        sigWorkflowSubtree,
        this._runsTab || 'runs'
      ]),
      schedule: JSON.stringify([
        t.id,
        t.schedule,
        t.schedule_enabled,
        t.next_run,
        t.last_run,
        t.execution_count,
        t.failure_count,
        // Include the run-history length and the latest entry's identity so a
        // newly-recorded run forces the schedule card to re-render (otherwise
        // the cached fingerprint would skip rendering until something else on
        // the schedule changed).
        Array.isArray(t.execution_history) ? t.execution_history.length : 0,
        t.execution_history?.[t.execution_history.length - 1]?.executed_at || '',
        sigAutomation,
        // The Automation & output card's visibility now also depends on whether
        // this task declares a structured output shape, so a spec change must
        // re-run renderSchedule (sigAutomation tracks the storage owner's spec,
        // which can differ from this.task's on a workflow parent).
        t.output_spec || null,
        t.output_schema || null,
        t.output_contract || null
      ]),
      context: JSON.stringify([t.id, t.context || {}]),
      blockedState: JSON.stringify([t.id, sigStatusInfo, sigBlocked])
    };
  }

  // _taskGraphNeighborsFingerprint captures the renderable state of every
  // task that participates in this task's relationships card: parent, input
  // producers, downstream consumers, and direct children. Sibling status
  // changes flow through this fingerprint, so the relationships card stays
  // fresh when an upstream task completes.
  _taskGraphNeighborsFingerprint() {
    const t = this.task;
    if (!t) return '';
    const neighborIds = new Set();
    if (t.parent_task_id) neighborIds.add(String(t.parent_task_id).trim());
    for (const id of Array.isArray(t.input_task_ids) ? t.input_task_ids : []) {
      if (id) neighborIds.add(String(id).trim());
    }
    for (const sibling of this.tasks) {
      if (!sibling || sibling.id === t.id) continue;
      const inputs = Array.isArray(sibling.input_task_ids) ? sibling.input_task_ids : [];
      if (inputs.some(id => String(id || '').trim() === t.id)) {
        neighborIds.add(String(sibling.id).trim());
      }
      if (String(sibling.parent_task_id || '').trim() === t.id) {
        neighborIds.add(String(sibling.id).trim());
      }
    }
    const sortedIds = [...neighborIds].sort();
    const stamps = sortedIds.map(id => {
      const s = this.tasks.find(x => x?.id === id);
      if (!s) return [id, null];
      return [id, s.status, s.description, s.to, s.subtask_index];
    });
    return JSON.stringify(stamps);
  }

  // _taskWorkflowSubtreeFingerprint captures the recursive child tree
  // beneath this task. Workflow card sub-render is sensitive to any status
  // or assignment change at any depth.
  _taskWorkflowSubtreeFingerprint() {
    const t = this.task;
    if (!t) return '';
    const visited = new Set();
    const stamps = [];
    const collect = parentId => {
      if (!parentId || visited.has(parentId)) return;
      visited.add(parentId);
      for (const item of this.tasks) {
        if (!item) continue;
        if (String(item.parent_task_id || '').trim() !== parentId) continue;
        stamps.push([
          item.id,
          item.status,
          item.description,
          item.subtask_index,
          item.to,
          item.result ? item.result.length : 0
        ]);
        collect(String(item.id || '').trim());
      }
    };
    collect(String(t.id || '').trim());
    return JSON.stringify(stamps);
  }

  // loadRelatedPlan shows which Plan created this task, when one did.
  //
  // A failure or a missing link both render nothing: a task created directly
  // is the ordinary case, not an error worth reporting (FR-148).
  async loadRelatedPlan() {
    const related = await fetchRelatedPlan(this.workspaceId, 'task', this.taskId);
    renderRelatedPlan(document.getElementById('workspace-task-related-plan'), related, value =>
      escapeHtml(String(value ?? ''))
    );
  }

  renderHero(statusInfo) {
    const taskTitle = this.getTaskDisplayLabel();
    const detailsSummary = summarizeText(
      this.task?.details || this.currentBlockedTask?.reason || '',
      280
    );

    if (this.elements.workspaceName) {
      this.elements.workspaceName.textContent =
        String(this.workspace?.name || 'Workspace').trim() || 'Workspace';
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
    if (this.elements.idValue) {
      this.elements.idValue.textContent = String(this.task?.id || this.taskId || '').trim();
    }
    if (this.elements.status) {
      this.elements.status.textContent = statusInfo.label;
      this.elements.status.dataset.state = statusInfo.className;
    }
    if (statusInfo.className !== 'in_progress') {
      // Drop stale activity when the task transitions out of in_progress so a
      // subsequent re-run starts the badge clean instead of inheriting the
      // last phase from the previous run.
      this._latestActivity = null;
    }
    this.syncTaskTagsWidget();
    this.renderLiveBadge();
  }

  // Mounts the shared tag input widget into the hero and keeps it in sync
  // with the task without clobbering an edit mid-flight: renders happen on
  // every poll, so the widget is only reset when the lists actually differ.
  syncTaskTagsWidget() {
    const row = document.getElementById('workspace-task-tags-row');
    const mount = document.getElementById('workspace-task-tags-mount');
    if (!row || !mount) return;
    if (!this.task?.id) {
      row.hidden = true;
      return;
    }
    const tags = Array.isArray(this.task.tags) ? this.task.tags : [];
    if (!this.taskTagsWidget && window.OriTagInput?.createTagInput) {
      this.taskTagsWidget = window.OriTagInput.createTagInput({
        container: mount,
        initialTags: tags,
        onChange: next => {
          void this.saveTaskTags(next);
        }
      });
    } else if (this.taskTagsWidget) {
      const current = this.taskTagsWidget.getTags();
      const same =
        current.length === tags.length && current.every((tag, index) => tag === tags[index]);
      if (!same && !this._taskTagsSaving) this.taskTagsWidget.setTags(tags);
    }
    row.hidden = !this.taskTagsWidget;
  }

  async saveTaskTags(tags) {
    this._taskTagsSaving = true;
    try {
      await this.updateTaskFields({ tags }, { deferRender: true });
      // New tags should show up in suggestions everywhere right away.
      window.OriTagInput?.clearTagPoolCache?.();
    } catch (error) {
      if (window.Toast && typeof window.Toast.error === 'function') {
        window.Toast.error(error.message || 'Failed to update tags');
      }
    } finally {
      this._taskTagsSaving = false;
    }
  }

  renderHeroActions(statusInfo) {
    if (!this.elements.heroActions) return;

    // A pending Cancel inline-confirm is always re-rendered when render()
    // fires; status changes during the prompt invalidate the confirm and
    // we drop back to the normal action row.
    if (this._cancelConfirmActive) {
      const stillRunning =
        String(this.task?.status || '')
          .trim()
          .toLowerCase() === 'in_progress';
      if (!stillRunning) {
        this._cancelConfirmActive = false;
      }
    }

    const status = String(this.task?.status || '')
      .trim()
      .toLowerCase();
    const hasAgent = Boolean(this.task?.to) && this.task.to !== 'unassigned';
    const buttons = [];
    const hasSchedule = Boolean(this.task?.schedule);
    const scheduleLabel = hasSchedule ? 'Edit Schedule' : 'Schedule';

    // Run is always available for pending/assigned tasks; if no agent is
    // attached we'll surface an agent picker on click rather than hiding
    // the button (the previous gating left users with no obvious next
    // step on a pending+unassigned task — discoverability bug #5).
    if (status === 'pending' || status === 'assigned') {
      const runLabel = hasAgent ? 'Run' : 'Assign & Run';
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn workspace-task-page-hero-btn-primary" data-action="execute">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8,5.14V19.14L19,12.14L8,5.14Z"/></svg>${this.escapeHtml(runLabel)}
      </button>`);
    }

    if (status === 'failed' || status === 'completed') {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="execute">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/></svg>Re-run
      </button>`);
    }

    if (this.canCreateSkillFromTask()) {
      // Secondary (not primary): "Create Skill" is an advanced/power-user
      // action and shouldn't compete with Run/Re-run for attention on a
      // non-technical user's first read of the page.
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="create-skill">
        <i class="bi bi-magic" aria-hidden="true"></i>Create Skill
      </button>`);
    }

    // Mark Complete covers two cases: pre-run close-out (pending/assigned)
    // and manual close-out of a runaway in_progress task that the user
    // already finished offline. The completeTask() handler shows a confirm
    // dialog for in_progress to make the override explicit; pending and
    // assigned skip the prompt since no execution work is at stake.
    // Blocked (waiting_for_choice) is excluded because the server's status
    // transition table doesn't permit a direct jump to completed - the
    // task has to go through its resolution flow first.
    if (
      (status === 'pending' || status === 'assigned' || status === 'in_progress') &&
      !statusInfo.isBlocked
    ) {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="complete">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/></svg>Mark Complete
      </button>`);
    }

    if (status === 'in_progress' && !this._cancelConfirmActive) {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn workspace-task-page-hero-btn-danger" data-action="cancel-prompt">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M18,6H15V4A2,2 0 0,0 13,2H11A2,2 0 0,0 9,4V6H6V8H7V19A2,2 0 0,0 9,21H15A2,2 0 0,0 17,19V8H18V6M11,4H13V6H11V4M15,19H9V8H15V19Z"/></svg>Cancel
      </button>`);
    }

    buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="schedule">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M19,3H18V1H16V3H8V1H6V3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3M19,19H5V8H19V19M7,10H12V15H7V10Z"/></svg>${this.escapeHtml(scheduleLabel)}
    </button>`);

    if (status === 'in_progress' && this._cancelConfirmActive) {
      const disabled = this._cancelInFlight ? 'disabled' : '';
      buttons.push(`<span class="workspace-task-page-hero-cancel-confirm" role="alertdialog" aria-label="Confirm cancel">
        <span class="workspace-task-page-hero-cancel-confirm-label">Cancel this run?</span>
        <button type="button" class="workspace-task-page-hero-cancel-yes" data-action="cancel-confirm" ${disabled}>${this._cancelInFlight ? 'Cancelling…' : 'Yes'}</button>
        <button type="button" class="workspace-task-page-hero-cancel-no" data-action="cancel-dismiss" ${disabled}>No</button>
      </span>`);
    }

    this.elements.heroActions.innerHTML = buttons.join('');

    this.elements.heroActions.querySelectorAll('[data-action]').forEach(btn => {
      btn.addEventListener('click', () => {
        const action = btn.dataset.action;
        if (action === 'execute') this.executeTask();
        if (action === 'complete') this.completeTask();
        if (action === 'schedule') this.openScheduleModal();
        if (action === 'create-skill') this.openSkillDraftModal();
        if (action === 'cancel-prompt') {
          this._cancelConfirmActive = true;
          if (this._renderCache) delete this._renderCache.heroActions;
          this.renderHeroActions(statusInfo);
        }
        if (action === 'cancel-dismiss') {
          this._cancelConfirmActive = false;
          if (this._renderCache) delete this._renderCache.heroActions;
          this.renderHeroActions(statusInfo);
        }
        if (action === 'cancel-confirm') this.handleCancelTask();
      });
    });
  }

  // renderHeroAgent populates the inline "Agent" picker next to the status
  // pill. Visible whenever the task is in a state where reassignment is
  // meaningful: not actively running, not blocked. Replaces the old Quick
  // Controls aside which buried the only commonly-used override (reassign)
  // behind a disclosure.
  renderHeroAgent(statusInfo) {
    const wrap = this.elements.heroAgentWrap;
    const select = this.elements.heroAgent;
    if (!wrap || !select) return;

    const status = String(this.task?.status || '')
      .trim()
      .toLowerCase();
    const reassignable = status !== 'in_progress' && !statusInfo.isBlocked;
    if (!reassignable) {
      wrap.hidden = true;
      return;
    }

    const currentAgent = String(this.task?.to || '').trim();
    const currentAgentUnavailable =
      Boolean(currentAgent) && !this.isRunnableAgentName(currentAgent);
    const agentNames = this.getAssignableAgentNames(currentAgent);

    const options = [];
    options.push(`<option value="" ${!currentAgent ? 'selected' : ''}>Unassigned</option>`);
    if (currentAgentUnavailable) {
      options.push(
        `<option value="${this.escapeHtml(currentAgent)}" selected disabled>${this.escapeHtml(`${currentAgent} (Unavailable)`)}</option>`
      );
    }
    for (const name of agentNames) {
      const trimmed = String(name || '').trim();
      if (!trimmed) continue;
      const isCurrent =
        !currentAgentUnavailable && trimmed.toLowerCase() === currentAgent.toLowerCase();
      options.push(
        `<option value="${this.escapeHtml(trimmed)}" ${isCurrent ? 'selected' : ''}>${this.escapeHtml(trimmed)}</option>`
      );
    }

    select.innerHTML = options.join('');
    wrap.classList.toggle('is-unavailable', currentAgentUnavailable);
    wrap.hidden = false;

    // The change handler is bound once in bindEvents(); each render replaces
    // the <option> children in place, so the listener stays attached.
  }

  async handleCancelTask() {
    if (this._cancelInFlight) return;
    const id = String(this.task?.id || '').trim();
    if (!id) return;

    this._cancelInFlight = true;
    if (this._renderCache) delete this._renderCache.heroActions;
    this.renderHeroActions(this.getTaskStatusPresentation());

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(id)}/cancel`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}'
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to cancel run');
      }
      this.notify('success', 'Run cancelled');
      this._cancelConfirmActive = false;
      await this.loadData();
    } catch (error) {
      console.error('Failed to cancel task:', error);
      this.notify('error', error?.message || 'Failed to cancel run');
    } finally {
      this._cancelInFlight = false;
      if (this._renderCache) delete this._renderCache.heroActions;
      this.renderHeroActions(this.getTaskStatusPresentation());
    }
  }

  renderOverview() {
    if (!this.elements.overview) return;

    const progress = this.task?.progress;
    const isBlocked = Boolean(this.currentBlockedTask);
    const status = String(this.task?.status || '')
      .trim()
      .toLowerCase();

    // Keep the overview to the essentials a non-technical user cares about.
    // Configuration internals (execution mode, orchestration mode, template
    // ref) are intentionally omitted here — they remain in the collapsed
    // Developer details (raw context).
    const items = [];

    // Agent is shown as the hero picker whenever the task is reassignable
    // (not running, not blocked). Only repeat it in the overview grid when
    // that picker is hidden, so the agent is visible exactly once.
    const agentShownInHero = status !== 'in_progress' && !isBlocked;
    if (!agentShownInHero) {
      items.push({
        title: 'Agent',
        value: String(this.task?.to || 'Unassigned').trim() || 'Unassigned'
      });
    }

    // "Requested By" is only meaningful when something other than the
    // workspace itself created the task; hide the noisy default.
    const requestedBy = String(this.task?.from || '').trim();
    if (requestedBy && requestedBy.toLowerCase() !== 'workspace') {
      items.push({ title: 'Requested By', value: requestedBy });
    }

    if (progress && (progress.current_step || Number.isFinite(progress.percentage))) {
      const progressLabel = [
        Number.isFinite(Number(progress.percentage))
          ? `${Number(progress.percentage)}% complete`
          : '',
        String(progress.current_step || '').trim(),
        Number(progress.total_steps) > 0
          ? `${Number(progress.completed_steps || 0)}/${Number(progress.total_steps)} steps`
          : ''
      ]
        .filter(Boolean)
        .join(' • ');
      items.push({
        title: 'Progress',
        value: progressLabel || 'Progress available'
      });
    }

    const currentRunId = String(this.task?.current_run_id || '').trim();
    if (currentRunId) {
      const currentRun = this.currentRun || {};
      // Plain-language summary: status + when. The profile snapshot id,
      // validation status, and raw run id are developer details and live on
      // the run page / Developer tab, not here.
      const runBits = [
        this.formatWorkspaceRunStatus(currentRun.status),
        currentRun?.started_at ? formatDateTime(currentRun.started_at) : ''
      ].filter(Boolean);

      items.push({
        title: 'Latest Run',
        value: runBits.join(' • ') || 'Recorded',
        href: this.getRunHref(currentRunId)
      });
    } else {
      items.push({
        title: 'Latest Run',
        value: 'No run recorded yet.'
      });
    }

    const referenceURL = String(this.task?.reference_url || '').trim();
    items.push({
      title: 'Reference URL',
      value: referenceURL || 'Not set',
      href: referenceURL || '',
      external: Boolean(referenceURL),
      full: true,
      editable: true,
      editField: 'reference-url',
      editLabel: referenceURL ? 'Edit reference URL' : 'Add reference URL',
      alwaysShowEdit: !referenceURL
    });

    // Result Storage moved into the Automation card; the per-task storage
    // editor is now reachable from there. Keep the "Needs Review" hint here
    // because it's a Task Brief signal (validation outcome), not a config
    // concern that belongs alongside cadence/columns.
    const resultStorageTask = this.getTaskResultStorageTask();
    const needsReviewCount = this.countTaskNeedsReviewRuns(resultStorageTask);
    if (needsReviewCount > 0) {
      items.push({
        title: 'Needs Review',
        value: `${needsReviewCount} run${needsReviewCount === 1 ? '' : 's'} held from storage until reviewed.`
      });
    }

    const detailsValue = String(this.task?.details || '').trim();
    const blockedDetailsRedundant = this.isBlockedDetailsRedundant(detailsValue);

    const renderEditButton = item => {
      if (!item.editable) return '';
      const editField = item.editField || 'details';
      const editLabel = item.editLabel || `Edit ${item.title || 'field'}`;
      const visibilityClass = item.alwaysShowEdit ? ' is-visible' : '';
      return `<button type="button" class="workspace-task-overview-edit-btn${visibilityClass}" data-edit-field="${this.escapeHtml(editField)}" aria-label="${this.escapeHtml(editLabel)}" title="${this.escapeHtml(editLabel)}">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/></svg>
      </button>`;
    };

    if (!isBlocked || (detailsValue && !blockedDetailsRedundant)) {
      items.push({
        title: isBlocked ? 'Original Brief' : 'Task Details',
        value: detailsValue || 'No extra task details were provided.',
        full: true,
        editable: true,
        isBrief: true,
        disclosure: isBlocked,
        disclosureHint: 'Hidden while this task is waiting for input.'
      });
    }

    // The brief ("what this task does") leads the card so a reader starts with
    // intent; the run metadata (status, reference URL, agent, …) collapses into
    // a compact meta row beneath it instead of a tall stack of labelled rows.
    const briefItem = items.find(item => item.isBrief);
    const metaItems = items.filter(item => !item.isBrief);

    const renderBriefLead = () => {
      if (!briefItem) return '';
      // Blocked tasks keep the brief behind a disclosure (it can duplicate the
      // pause reason); just promoted into the lead slot.
      if (briefItem.disclosure) {
        return `
          <div class="workspace-task-overview-item workspace-task-brief-lead full">
            <details class="workspace-task-overview-disclosure">
              <summary class="workspace-task-overview-disclosure-toggle">
                <span class="workspace-task-overview-disclosure-heading">${this.escapeHtml(briefItem.title)}</span>
                <span class="workspace-task-overview-disclosure-hint">${this.escapeHtml(briefItem.disclosureHint || '')}</span>
              </summary>
              <div class="workspace-task-overview-disclosure-body">
                <div class="workspace-task-overview-value">${this.escapeHtml(briefItem.value)}</div>
              </div>
            </details>
          </div>
        `;
      }
      // No label: the card kicker ("Task") + heading ("What this task does")
      // already name this content, so the brief speaks for itself. The edit
      // pencil is absolutely positioned (CSS) and revealed on hover; the
      // .workspace-task-overview-item + .workspace-task-overview-value classes
      // are preserved so startDetailsEdit keeps working unchanged.
      return `
        <div class="workspace-task-overview-item workspace-task-brief-lead full">
          ${renderEditButton(briefItem)}
          <div class="workspace-task-overview-value workspace-task-brief-lead-value">${this.escapeHtml(briefItem.value)}</div>
        </div>
      `;
    };

    const renderMetaEntry = item => {
      const valueHtml = item.href
        ? `<a href="${this.escapeHtml(item.href)}" class="workspace-task-overview-link"${item.external ? ' target="_blank" rel="noopener noreferrer"' : ''}>${this.escapeHtml(item.value)}</a>`
        : this.escapeHtml(item.value);
      return `
        <span class="workspace-task-brief-meta-item">
          <span class="workspace-task-brief-meta-label">${this.escapeHtml(item.title)}</span>
          <span class="workspace-task-brief-meta-value">${valueHtml}</span>
          ${renderEditButton(item)}
        </span>
      `;
    };

    const metaHtml = metaItems.length
      ? `<div class="workspace-task-brief-meta">${metaItems.map(renderMetaEntry).join('')}</div>`
      : '';

    this.elements.overview.innerHTML = renderBriefLead() + metaHtml;

    this.elements.overview.querySelectorAll('[data-edit-field]').forEach(btn => {
      btn.addEventListener('click', () => {
        const field = btn.getAttribute('data-edit-field') || '';
        if (field === 'details') this.startDetailsEdit(btn);
        if (field === 'result-storage') this.openTaskStorageEditor();
        if (field === 'reference-url') this.openReferenceURLEditor();
      });
    });
  }

  getTaskResultStorageTask() {
    const subtasks = this.getSubtasks();
    return subtasks.length > 0 ? subtasks[subtasks.length - 1] : this.task;
  }

  countTaskNeedsReviewRuns(task = this.task) {
    const history = Array.isArray(task?.execution_history) ? task.execution_history : [];
    return history.filter(entry => {
      const validation = entry?.validation_result || entry?.validation || null;
      return (
        String(validation?.validation_status || '')
          .trim()
          .toLowerCase() === 'needs_review'
      );
    }).length;
  }

  describeTaskResultStorage(storage, sourceTask = this.task) {
    if (!storage || storage.enabled !== true) {
      return 'Not saving automatically.\nEdit this to save each run or append future runs to a CSV file.';
    }

    const writeMode = String(storage.write_mode || '')
      .trim()
      .toLowerCase();
    const format =
      String(storage.format || 'text')
        .trim()
        .toLowerCase() || 'text';
    const modeLabel =
      writeMode === 'append'
        ? 'Append each run to CSV'
        : `Save each run as a new ${format.toUpperCase()} file`;

    const defaultOutputDir = String(this.workspaceOutputDir || '').trim();
    let target = defaultOutputDir
      ? `Default output folder: ${defaultOutputDir}`
      : 'Default output folder';
    const storeNodeId = String(storage.store_node_id || '').trim();
    const filePath = String(storage.file_path || '').trim();
    const storageTarget = String(storage.storage_target || '').trim();
    const workspaceFolder = String(storage.workspace_folder || '').trim();
    if (storeNodeId) {
      target = `Store node: ${this.getStoreNodeDisplayLabel(storeNodeId)}`;
    } else if (storageTarget === 'workspace_folder') {
      target = `Workspace files: ${workspaceFolder || 'root'}`;
    } else if (filePath) {
      target = `Path: ${filePath}`;
    }

    const sourceTaskId = String(sourceTask?.id || '').trim();
    const currentTaskId = String(this.task?.id || '').trim();
    const sourceLabel =
      sourceTaskId && currentTaskId && sourceTaskId !== currentTaskId
        ? `Final workflow step: ${summarizeText(sourceTask?.description || sourceTaskId, 72)}`
        : '';
    const contractColumns = Array.isArray(sourceTask?.output_contract?.columns)
      ? sourceTask.output_contract.columns
      : [];
    const contractLabel =
      writeMode === 'append'
        ? contractColumns.length > 0
          ? `Output contract: ${contractColumns
              .map(column => String(column?.name || '').trim())
              .filter(Boolean)
              .join(', ')}`
          : 'No output contract defined. Runs will save without validation.'
        : '';

    return [modeLabel, target, contractLabel, sourceLabel].filter(Boolean).join('\n');
  }

  getStoreNodeDisplayLabel(storeNodeId) {
    const normalized = String(storeNodeId || '').trim();
    if (!normalized) return 'Unknown store node';
    const nodes = Array.isArray(this.workspace?.store_nodes) ? this.workspace.store_nodes : [];
    const node = nodes.find(
      item =>
        String(item?.id || '').trim() === normalized ||
        String(item?.canvas_node_id || '').trim() === normalized
    );
    if (!node) return normalized;

    const name = String(node.name || '').trim();
    const baseDir = String(node.base_dir || '').trim();
    if (name && baseDir) return `${name} (${baseDir})`;
    return name || baseDir || normalized;
  }

  async openTaskStorageEditor() {
    if (
      !window.taskModalController ||
      typeof window.taskModalController.openForEdit !== 'function'
    ) {
      this.notify('error', 'Task editor is not available on this page.');
      return;
    }

    try {
      await window.taskModalController.openForEdit(this.task, async () => {
        await this.loadData();
      });
      window.requestAnimationFrame(() => this.focusTaskModalAutoSave());
    } catch (error) {
      console.error('Failed to open task storage editor:', error);
      this.notify('error', error?.message || 'Failed to open task editor.');
    }
  }

  async openReferenceURLEditor() {
    if (
      !window.taskModalController ||
      typeof window.taskModalController.openForEdit !== 'function'
    ) {
      this.notify('error', 'Task editor is not available on this page.');
      return;
    }

    try {
      await window.taskModalController.openForEdit(this.task, async () => {
        await this.loadData();
      });
      window.requestAnimationFrame(() => this.focusTaskModalReferenceURL());
    } catch (error) {
      console.error('Failed to open task reference URL editor:', error);
      this.notify('error', error?.message || 'Failed to open task editor.');
    }
  }

  focusTaskModalReferenceURL() {
    const referenceInput = document.getElementById('taskModalReferenceURL');
    referenceInput?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    window.setTimeout(() => {
      if (referenceInput && typeof referenceInput.focus === 'function') {
        referenceInput.focus({ preventScroll: true });
        if (typeof referenceInput.select === 'function') {
          referenceInput.select();
        }
      }
    }, 180);
  }

  focusTaskModalAutoSave() {
    const automationSection = document.querySelector('.task-modal-automation');
    const autoSaveEnabled = document.getElementById('taskModalAutoSaveEnabled');
    const writeModeSelect = document.getElementById('taskModalAutoSaveWriteMode');
    const appendContractInput = document.querySelector(
      '#taskModalOutputContractRows [data-output-contract-name]'
    );
    const resultStorage = this.getTaskResultStorageTask()?.result_storage;
    const target =
      resultStorage?.write_mode === 'append' && appendContractInput
        ? appendContractInput
        : resultStorage?.enabled
          ? writeModeSelect
          : autoSaveEnabled;

    automationSection?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    window.setTimeout(() => {
      if (target && typeof target.focus === 'function') {
        target.focus({ preventScroll: true });
      }
    }, 180);
  }

  normalizeComparableText(value) {
    return String(value || '')
      .replace(/\s+/g, ' ')
      .trim()
      .toLowerCase();
  }

  formatWorkspaceRunStatus(status) {
    const normalized = String(status || '')
      .trim()
      .replace(/_/g, ' ');
    if (!normalized) return '';
    return normalized.replace(/\b\w/g, char => char.toUpperCase());
  }

  workspaceRunTimestamp(run) {
    const raw = run?.finished_at || run?.started_at || run?.created_at || '';
    const parsed = new Date(raw).getTime();
    return Number.isFinite(parsed) ? parsed : 0;
  }

  isBlockedDetailsRedundant(detailsValue) {
    if (!this.currentBlockedTask) return false;

    const normalizedDetails = this.normalizeComparableText(detailsValue);
    if (!normalizedDetails) return false;

    return [this.currentBlockedTask.reason, this.currentBlockedTask.question].some(
      candidate => normalizedDetails === this.normalizeComparableText(candidate)
    );
  }

  startDetailsEdit(triggerBtn) {
    if (this.detailsEditInProgress) return;

    const article = triggerBtn.closest('.workspace-task-overview-item');
    if (!article) return;

    const valueEl = article.querySelector('.workspace-task-overview-value');
    if (!valueEl) return;

    this.detailsEditInProgress = true;
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

    const finish = async save => {
      if (!this.detailsEditInProgress) return;
      this.detailsEditInProgress = false;

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

    actions.querySelector('.workspace-task-page-edit-save')?.addEventListener('mousedown', e => {
      e.preventDefault();
      finish(true);
    });
    actions.querySelector('.workspace-task-page-edit-cancel')?.addEventListener('mousedown', e => {
      e.preventDefault();
      finish(false);
    });
    textarea.addEventListener('keydown', e => {
      if (e.key === 'Escape') {
        e.preventDefault();
        finish(false);
      }
    });
    textarea.addEventListener('blur', e => {
      if (actions.contains(e.relatedTarget)) return;
      finish(true);
    });
  }

  renderRelationships() {
    if (!this.elements.relationships || !this.elements.relationshipsCard) return;

    // Three groups make the task's place in the dependency graph visible at
    // a glance: parent (containing workflow), upstream input producers, and
    // downstream consumers. Direction is conveyed via the kicker label and
    // arrow glyph on each group; status is conveyed via a colored dot in
    // each link card. The deep subtask tree continues to live in the
    // workflow card below — keeping that separation lets the relationships
    // card stay glanceable instead of becoming a graph viewer.
    const parentTask = this.getParentTask();
    const inputTasks = this.getInputTasks();
    const dependentTasks = this.getDependentTasks();
    const groups = [];

    if (parentTask) {
      groups.push({
        title: 'Parent Task',
        arrow: '↑',
        direction: 'up',
        tasks: [parentTask]
      });
    }

    if (inputTasks.length > 0) {
      groups.push({
        title: 'Receives Input From',
        arrow: '←',
        direction: 'in',
        tasks: this.sortWorkflowTasks(inputTasks)
      });
    }

    if (dependentTasks.length > 0) {
      groups.push({
        title: 'Used By',
        arrow: '→',
        direction: 'out',
        tasks: this.sortWorkflowTasks(dependentTasks)
      });
    }

    if (groups.length === 0) {
      this.elements.relationshipsCard.hidden = true;
      this.elements.relationships.innerHTML = '';
      return;
    }

    this.elements.relationshipsCard.hidden = false;
    const graphHtml = this.renderRelationshipsGraph({
      parentTask,
      inputTasks: groups.find(g => g.direction === 'in')?.tasks || [],
      dependentTasks: groups.find(g => g.direction === 'out')?.tasks || []
    });
    const groupsHtml = groups
      .map(
        group => `
      <section class="workspace-task-relationship-group" data-direction="${this.escapeHtml(group.direction)}">
        <div class="workspace-task-relationship-title">
          <span class="workspace-task-relationship-arrow" aria-hidden="true">${this.escapeHtml(group.arrow)}</span>
          ${this.escapeHtml(group.title)}
        </div>
        <div class="workspace-task-related-links">
          ${group.tasks
            .map(task => {
              const statusClass = getStatusClass(task?.status);
              const assignee = String(task?.to || 'Unassigned').trim() || 'Unassigned';
              return `
            <a href="${this.getTaskHref(task.id)}" class="workspace-task-related-link" data-status="${this.escapeHtml(statusClass)}">
              <span class="workspace-task-related-link-title">
                <span class="workspace-task-related-link-dot" data-state="${this.escapeHtml(statusClass)}" aria-hidden="true"></span>
                <span>${this.escapeHtml(this.getTaskDisplayLabel(task))}</span>
              </span>
              <span class="workspace-task-related-link-meta">${this.escapeHtml(getDisplayStatus(task.status))} · ${this.escapeHtml(assignee)}</span>
            </a>
          `;
            })
            .join('')}
        </div>
      </section>
    `
      )
      .join('');
    this.elements.relationships.innerHTML = graphHtml + groupsHtml;
  }

  /**
   * Build a small inline SVG showing this task's place in the dependency
   * graph: parent above, inputs to the left, consumers to the right, and
   * the current task in the center. Each neighbor is a clickable node
   * that navigates to that task's detail page; status is encoded via the
   * fill color matching the rest of the page's status palette. Returns
   * an empty string when the task has no neighbors so the relationships
   * card stays compact.
   */
  renderRelationshipsGraph({ parentTask, inputTasks, dependentTasks }) {
    const inputs = (inputTasks || []).slice(0, 4);
    const consumers = (dependentTasks || []).slice(0, 4);
    const totalNeighbors = (parentTask ? 1 : 0) + inputs.length + consumers.length;
    if (totalNeighbors === 0) return '';

    const width = 520;
    const height = 200;
    const cx = width / 2;
    const cy = height / 2;
    const sideX = 60;
    const r = 11;

    const colorForStatus = status => {
      const cls = getStatusClass(status);
      switch (cls) {
        case 'completed':
          return '#157347';
        case 'in_progress':
          return '#0c63e7';
        case 'failed':
          return '#c23b3b';
        case 'blocked':
          return '#b45309';
        case 'cancelled':
          return '#6b7280';
        default:
          return '#9ca3af';
      }
    };

    const nodeMarkup = (task, x, y, opts = {}) => {
      const fill = colorForStatus(task?.status);
      const id = String(task?.id || '').replace(/"/g, '&quot;');
      const labelRaw = this.getTaskDisplayLabel(task);
      const label = labelRaw.length > 22 ? labelRaw.slice(0, 21) + '…' : labelRaw;
      const labelY = opts.labelBelow ? y + r + 14 : y - r - 6;
      const isCurrent = Boolean(opts.isCurrent);
      const cls = isCurrent
        ? 'workspace-task-relgraph-node workspace-task-relgraph-node--current'
        : 'workspace-task-relgraph-node';
      const titleAttr = `${this.escapeHtml(labelRaw)} · ${this.escapeHtml(getDisplayStatus(task?.status))}`;
      const wrapStart = isCurrent ? '<g' : `<a href="${this.getTaskHref(id)}"`;
      const wrapEnd = isCurrent ? '</g>' : '</a>';
      return `
        ${wrapStart} class="${cls}">
          <title>${titleAttr}</title>
          <circle cx="${x}" cy="${y}" r="${r}" fill="${fill}" stroke="${isCurrent ? '#1f2937' : 'rgba(15,23,42,0.18)'}" stroke-width="${isCurrent ? 2.5 : 1}"></circle>
          <text x="${x}" y="${labelY}" text-anchor="middle" class="workspace-task-relgraph-label">${this.escapeHtml(label)}</text>
        ${wrapEnd}
      `;
    };

    const edge = (x1, y1, x2, y2) =>
      `<line x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" class="workspace-task-relgraph-edge"></line>`;

    const stackY = (count, idx) => {
      if (count === 1) return cy;
      const total = (count - 1) * 36;
      return cy - total / 2 + idx * 36;
    };

    const parts = [];

    if (parentTask) {
      const py = 30;
      parts.push(edge(cx, py, cx, cy - r));
      parts.push(nodeMarkup(parentTask, cx, py, { labelBelow: false }));
    }

    inputs.forEach((task, i) => {
      const y = stackY(inputs.length, i);
      parts.push(edge(sideX + r, y, cx - r, cy));
      parts.push(nodeMarkup(task, sideX, y, { labelBelow: true }));
    });

    consumers.forEach((task, i) => {
      const y = stackY(consumers.length, i);
      parts.push(edge(cx + r, cy, width - sideX - r, y));
      parts.push(nodeMarkup(task, width - sideX, y, { labelBelow: true }));
    });

    parts.push(nodeMarkup(this.task, cx, cy, { labelBelow: true, isCurrent: true }));

    const truncatedNote =
      (inputTasks?.length || 0) > inputs.length || (dependentTasks?.length || 0) > consumers.length
        ? '<div class="workspace-task-relgraph-truncated">Showing first 4 in each direction.</div>'
        : '';

    return `
      <div class="workspace-task-relgraph-wrap">
        <svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="xMidYMid meet" role="img" aria-label="Task dependency graph" class="workspace-task-relgraph">
          ${parts.join('')}
        </svg>
        ${truncatedNote}
      </div>
    `;
  }

  sortWorkflowTasks(tasks) {
    return [...tasks].sort((a, b) => {
      const aIndex =
        Number.isFinite(a?.subtask_index) && a.subtask_index > 0
          ? a.subtask_index
          : Number.MAX_SAFE_INTEGER;
      const bIndex =
        Number.isFinite(b?.subtask_index) && b.subtask_index > 0
          ? b.subtask_index
          : Number.MAX_SAFE_INTEGER;
      if (aIndex !== bIndex) return aIndex - bIndex;
      const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
      const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
      if (aTime !== bTime) return aTime - bTime;
      return String(a?.id || '').localeCompare(String(b?.id || ''));
    });
  }

  getChildTasks(parentTaskId) {
    const id = String(parentTaskId || '').trim();
    if (!id) return [];
    return this.sortWorkflowTasks(
      this.tasks.filter(item => String(item?.parent_task_id || '').trim() === id)
    );
  }

  getOrderedSteps() {
    return this.getSubtasks();
  }

  getWorkflowDescendantTasks(parentTaskId, visited = new Set()) {
    const id = String(parentTaskId || '').trim();
    if (!id || visited.has(id)) return [];
    visited.add(id);

    const children = this.getChildTasks(id);
    const out = [];
    children.forEach(child => {
      const childId = String(child?.id || '').trim();
      if (!childId || visited.has(childId)) return;
      out.push(child);
      out.push(...this.getWorkflowDescendantTasks(childId, visited));
    });
    return out;
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
      const visibleSteps = this.getWorkflowDescendantTasks(this.task?.id || '');
      const anyRunning = visibleSteps.some(step => String(step?.status || '') === 'in_progress');
      const anyUnassigned = visibleSteps.some(step => {
        const assignee = String(step?.to || '').trim();
        const assigned = Boolean(assignee) && assignee !== 'unassigned';
        const completed = getStatusClass(step?.status) === 'completed';
        return !assigned && !completed;
      });
      this.elements.workflowRunAllBtn.disabled = anyRunning || anyUnassigned;
      const reason = anyUnassigned
        ? 'Assign unfinished steps or mark manual steps done before running the workflow'
        : anyRunning
          ? 'A step is already running'
          : '';
      this.elements.workflowRunAllBtn.title = reason || 'Run all steps in sequence';
      // Mirror the disabled-reason inline so touch users (no hover, no
      // tooltip) understand why Run all is greyed out.
      this.renderWorkflowRunAllReason(reason);
    } else {
      this.renderWorkflowRunAllReason('');
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
      .map((step, index) => this.renderStepTree(step, index + 1, { visited: new Set() }))
      .join('');
    this.bindStepRowEvents();
  }

  renderStepTree(step, fallbackNumber, options = {}) {
    const stepId = String(step?.id || '').trim();
    const visited = options.visited instanceof Set ? options.visited : new Set();
    if (!stepId || visited.has(stepId)) {
      return this.renderStepRow(step, fallbackNumber, options);
    }

    visited.add(stepId);
    const localNumber =
      Number.isFinite(step?.subtask_index) && step.subtask_index > 0
        ? step.subtask_index
        : fallbackNumber;
    const numberLabel = options.parentNumber
      ? `${options.parentNumber}.${localNumber}`
      : String(localNumber);
    const children = this.getChildTasks(stepId).filter(child => {
      const childId = String(child?.id || '').trim();
      return childId && !visited.has(childId);
    });
    const childMarkup = children.length
      ? `<div class="workspace-task-workflow-substeps">
          ${children
            .map((child, childIndex) =>
              this.renderStepTree(child, childIndex + 1, {
                visited,
                depth: (options.depth || 0) + 1,
                parentNumber: numberLabel
              })
            )
            .join('')}
        </div>`
      : '';

    return this.renderStepRow(step, fallbackNumber, {
      ...options,
      childCount: children.length,
      childMarkup,
      numberLabel
    });
  }

  renderStepRow(step, fallbackNumber, options = {}) {
    const stepId = String(step?.id || '');
    const status = getStatusClass(step?.status);
    const isRunning = status === 'in_progress';
    const isCompleted = status === 'completed';
    const isFailed = status === 'failed';
    const isCancelled = status === 'cancelled';
    const stepNumber =
      Number.isFinite(step?.subtask_index) && step.subtask_index > 0
        ? step.subtask_index
        : fallbackNumber;
    const numberLabel = String(options.numberLabel || stepNumber);
    const title =
      String(step?.description || step?.name || `Step ${stepNumber}`).trim() ||
      `Step ${stepNumber}`;
    const agentName = String(step?.to || '').trim();
    const isAssigned = agentName && agentName !== 'unassigned';
    const result = normalizeResultText(step?.result).trim();
    const error = String(step?.error || '').trim();
    const childCount = Number.isFinite(options.childCount) ? options.childCount : 0;
    const childMarkup = String(options.childMarkup || '');
    const depth = Number.isFinite(options.depth) && options.depth > 0 ? options.depth : 0;

    const classes = ['workspace-task-workflow-step'];
    if (depth > 0) classes.push('is-nested');
    if (childCount > 0) classes.push('has-substeps');
    if (isRunning) classes.push('is-running');
    if (isCompleted) classes.push('is-completed');
    if (isFailed) classes.push('is-failed');
    if (isCancelled) classes.push('is-cancelled');

    const actionLabel = isRunning
      ? '■ Stop'
      : isCompleted
        ? isAssigned
          ? '↻ Re-run'
          : 'Done'
        : isFailed
          ? isAssigned
            ? '↻ Retry'
            : 'Mark done'
          : isAssigned
            ? '▶ Run'
            : 'Mark done';
    const actionName = isRunning ? 'cancel-step' : isAssigned ? 'run-step' : 'complete-step';
    const actionDisabled = !isRunning && !isAssigned && isCompleted;
    const actionTitle = isRunning
      ? 'Stop this running step'
      : isCompleted
        ? isAssigned
          ? 'Run this step again'
          : 'This checklist item is complete'
        : isAssigned
          ? isFailed
            ? 'Retry this step'
            : 'Run this step now'
          : 'Mark this checklist item done';
    const actionButtonClass = isRunning ? 'modern-btn-danger' : 'modern-btn-secondary';

    const checkTitle = isCompleted
      ? 'Step completed'
      : isRunning
        ? 'Step is already running'
        : 'Mark this step done';

    const resultBlock =
      result || error
        ? `<details class="workspace-task-workflow-step-result"${isRunning || isFailed ? ' open' : ''}>
           <summary>${error ? 'Show error' : 'Show result'}</summary>
           <pre class="workspace-task-workflow-step-result-body${error ? ' workspace-task-workflow-step-error' : ''}">${this.escapeHtml(error || result)}</pre>
         </details>`
        : '';

    const stepHref = this.getTaskHref(stepId);

    return `
      <article class="${classes.join(' ')}" data-step-id="${this.escapeHtml(stepId)}">
        <div class="workspace-task-workflow-rail">
          <button type="button"
                  class="workspace-task-workflow-step-check"
                  data-action="complete-step"
                  data-step-action-id="${this.escapeHtml(stepId)}"
                  ${isRunning || isCompleted ? 'disabled' : ''}
                  aria-label="${this.escapeHtml(checkTitle)}"
                  title="${this.escapeHtml(checkTitle)}">
            <span class="workspace-task-workflow-step-check-number">${this.escapeHtml(numberLabel)}</span>
            <span class="workspace-task-workflow-step-check-icon" aria-hidden="true">✓</span>
          </button>
        </div>
        <div class="workspace-task-workflow-step-body">
          <div class="workspace-task-workflow-step-header">
            <a href="${stepHref}" class="workspace-task-workflow-step-title" title="Open this step">${this.escapeHtml(title)}</a>
            <div class="workspace-task-workflow-step-actions">
              <button type="button"
                      class="modern-btn ${actionButtonClass} workspace-task-workflow-step-action-btn"
                      data-action="${this.escapeHtml(actionName)}"
                      data-step-action-id="${this.escapeHtml(stepId)}"
                      ${actionDisabled ? 'disabled' : ''}
                      title="${this.escapeHtml(actionTitle)}">
                ${actionLabel}
              </button>
              <button type="button"
                      class="modern-btn modern-btn-secondary workspace-task-workflow-step-action-btn"
                      data-action="delete-step"
                      data-step-action-id="${this.escapeHtml(stepId)}"
                      title="Remove this step">×</button>
            </div>
          </div>
          <div class="workspace-task-workflow-step-meta">
            <span class="workspace-task-workflow-step-status" data-state="${this.escapeHtml(status)}">${this.escapeHtml(getDisplayStatus(step?.status))}</span>
            <span class="workspace-task-workflow-step-agent">
              <span class="workspace-task-workflow-step-agent-name">${this.escapeHtml(isAssigned ? agentName : 'Manual')}</span>
            </span>
            ${childCount > 0 ? `<span class="workspace-task-workflow-step-count">${childCount} subtask${childCount === 1 ? '' : 's'}</span>` : ''}
          </div>
          ${resultBlock}
          ${childMarkup}
        </div>
      </article>
    `;
  }

  bindStepRowEvents() {
    if (!this.elements.workflowSteps) return;
    this.elements.workflowSteps
      .querySelectorAll('[data-step-action-id][data-action]')
      .forEach(button => {
        const stepId = button.getAttribute('data-step-action-id');
        if (!stepId) return;
        button.addEventListener('click', event => {
          event.preventDefault();
          const action = button.getAttribute('data-action');
          if (action === 'run-step') {
            this.handleRunStep(stepId);
          } else if (action === 'cancel-step') {
            this.handleCancelStep(stepId);
          } else if (action === 'complete-step') {
            this.handleCompleteStep(stepId);
          } else if (action === 'delete-step') {
            this.handleDeleteStep(stepId);
          }
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

  async handleCancelStep(stepId) {
    const id = String(stepId || '').trim();
    if (!id) return;
    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(id)}/cancel`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}'
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to stop step');
      }
      if (this.workflowPollTimer) {
        clearTimeout(this.workflowPollTimer);
        this.workflowPollTimer = null;
      }
      this.notify('success', 'Step stopped');
      await this.refreshAfterStepChange();
    } catch (error) {
      console.error('Failed to stop step:', error);
      this.notify('error', error?.message || 'Failed to stop step');
    }
  }

  async handleCompleteStep(stepId) {
    const id = String(stepId || '').trim();
    if (!id) return;
    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(id)}/complete`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}'
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(this.parseResponseError(text, 'Failed to mark step done'));
      }
      this.notify('success', 'Step marked done');
      await this.refreshAfterStepChange();
    } catch (error) {
      console.error('Failed to mark step done:', error);
      this.notify('error', error?.message || 'Failed to mark step done');
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
        const target =
          String(this.task?.id || '') === id
            ? this.task
            : this.tasks.find(t => String(t?.id || '') === id);
        const status = String(target?.status || '');
        if (
          status === 'completed' ||
          status === 'failed' ||
          status === 'cancelled' ||
          status === 'timeout'
        ) {
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
      this.workspaceRuns = await this.fetchWorkspaceRunsForTask(this.task).catch(() => []);
      this.currentRun = this.findWorkspaceRun(this.task?.current_run_id);
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
    if (
      (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
      (trimmed.startsWith('[') && trimmed.endsWith(']'))
    ) {
      try {
        JSON.parse(trimmed);
        return true;
      } catch (_e) {
        /* not json */
      }
    }
    return false;
  }

  renderMarkdownOrPre(text) {
    if (this.isStructuredData(text)) {
      return `<pre class="workspace-task-page-code-block">${this.escapeHtml(text)}</pre>`;
    }
    if (
      typeof marked !== 'undefined' &&
      typeof marked.parse === 'function' &&
      typeof DOMPurify !== 'undefined'
    ) {
      const safeHtml = DOMPurify.sanitize(marked.parse(text));
      return `<div class="workspace-task-page-prose">${safeHtml}</div>`;
    }
    return `<pre class="workspace-task-page-code-block">${this.escapeHtml(text)}</pre>`;
  }

  getArtifactSourceLabel(source) {
    const normalized = String(source || '')
      .trim()
      .toLowerCase();
    const labels = {
      run_history: 'Run history',
      output_schema: 'Structured output',
      structured_result: 'Structured result',
      markdown_table: 'Markdown table',
      fenced_csv: 'CSV block',
      csv: 'CSV',
      tsv: 'TSV',
      json: 'JSON'
    };
    return labels[normalized] || 'Result';
  }

  renderResultArtifact(artifact) {
    if (!artifact || !Array.isArray(artifact.columns) || !Array.isArray(artifact.rows)) return '';

    const rawColumns = artifact.columns;
    const rows = artifact.rows;

    // The preview shows only the task's actual output columns. Run bookkeeping
    // (run_id, executed_at, status, duration_ms, validation / storage status)
    // is still written to the CSV but dropped from the on-page preview, where
    // it was audit noise that buried the real data off-screen.
    const metaNames = new Set(
      this.getOutputSpecMetadataFields().map(field =>
        String(field.name || '')
          .trim()
          .toLowerCase()
      )
    );
    [
      'run_id',
      'executed_at',
      'status',
      'duration_ms',
      'validation_status',
      'storage_status'
    ].forEach(name => metaNames.add(name));
    const isRunMetaColumn = column =>
      metaNames.has(
        String(column || '')
          .trim()
          .toLowerCase()
      );
    const dataColumns = rawColumns.filter(column => !isRunMetaColumn(column));
    // Guard: if a task somehow declares only run metadata, keep showing every
    // column rather than rendering an empty table.
    const columns = dataColumns.length > 0 ? dataColumns : rawColumns;
    const hiddenMetaCount = dataColumns.length > 0 ? rawColumns.length - dataColumns.length : 0;

    const previewRows = rows.slice(0, 5);
    const hiddenRows = Math.max(0, rows.length - previewRows.length);
    const sourceLabel = this.getArtifactSourceLabel(artifact.source);
    const rowLabel = `${rows.length} row${rows.length === 1 ? '' : 's'}`;
    // The badge counts the full CSV (data + run-info), matching the row badge;
    // the footer note explains the columns the preview omits.
    const columnLabel = `${rawColumns.length} column${rawColumns.length === 1 ? '' : 's'}`;
    const savingLabel = this.resultArtifactNoteSaving ? 'Saving...' : 'Save CSV note';

    const truncationParts = [];
    if (hiddenRows > 0)
      truncationParts.push(`${hiddenRows} more row${hiddenRows === 1 ? '' : 's'}`);
    if (hiddenMetaCount > 0)
      truncationParts.push(`${hiddenMetaCount} run-info column${hiddenMetaCount === 1 ? '' : 's'}`);
    const truncationNote = truncationParts.length ? `${truncationParts.join(' · ')} in CSV` : '';

    const headHtml = columns
      .map(column => `<th scope="col">${this.escapeHtml(column)}</th>`)
      .join('');
    const rowsHtml = previewRows
      .map(
        row => `
        <tr>
          ${columns.map(column => `<td>${this.escapeHtml(row?.[column] ?? '')}</td>`).join('')}
        </tr>
      `
      )
      .join('');

    const appendCtx = this.getAppendCSVContext();
    const appendButtonLabel = appendCtx.configured
      ? `Append to ${appendCtx.label || 'CSV'}`
      : 'Append to CSV...';
    const appendButtonTitle = appendCtx.configured
      ? `Append this run's rows to ${appendCtx.label || 'the configured CSV file'}`
      : "Choose a CSV destination to append this run's rows";
    const appendBusy = Boolean(this.resultArtifactAppendBusy);
    const appendButton = `
      <button
        type="button"
        class="modern-btn modern-btn-primary workspace-task-output-action-btn"
        data-action="append-result-artifact-csv"
        title="${this.escapeHtml(appendButtonTitle)}"${appendBusy ? ' disabled' : ''}>
        <i class="bi bi-file-earmark-spreadsheet" aria-hidden="true"></i>
        <span data-role="append-label">${this.escapeHtml(appendBusy ? 'Appending...' : appendButtonLabel)}</span>
      </button>`;

    const chipHtml = appendCtx.configured
      ? `
      <div class="workspace-task-result-artifact-chip" data-role="append-chip">
        <span class="workspace-task-result-artifact-chip-icon" aria-hidden="true">
          <i class="bi bi-arrow-down-circle"></i>
        </span>
        <span class="workspace-task-result-artifact-chip-copy">
          <span class="workspace-task-result-artifact-chip-label">Auto-appending to</span>
          <strong>${this.escapeHtml(appendCtx.label || 'CSV file')}</strong>
          ${appendCtx.locationHint ? `<span class="workspace-task-result-artifact-chip-location">${this.escapeHtml(appendCtx.locationHint)}</span>` : ''}
        </span>
        <button type="button" class="workspace-task-result-artifact-chip-edit" data-action="edit-append-storage" aria-label="Edit auto-append destination" title="Edit auto-append destination">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
          </svg>
        </button>
      </div>`
      : '';

    const storeNodes = Array.isArray(this.workspace?.store_nodes) ? this.workspace.store_nodes : [];
    const storeNodeOptions = storeNodes
      .map(node => {
        const id = String(node?.id || '').trim();
        if (!id) return '';
        const label = this.getStoreNodeDisplayLabel(id);
        return `<option value="${this.escapeHtml(id)}">${this.escapeHtml(label)}</option>`;
      })
      .filter(Boolean)
      .join('');
    const appendDefaultPath = String(this.workspaceOutputDir || '').trim();
    const appendDefaultPathHint = appendDefaultPath
      ? `<span class="workspace-task-automation-storage-path" title="${this.escapeHtml(appendDefaultPath)}">${this.escapeHtml(appendDefaultPath)}</span>`
      : '';
    const chooserHtml = appendCtx.configured
      ? ''
      : `
      <div class="workspace-task-result-artifact-chooser" data-role="append-chooser" hidden>
        <div class="workspace-task-result-artifact-chooser-header">
          <div class="workspace-task-page-mini-label">Append destination</div>
          <button type="button" class="workspace-task-page-text-button" data-action="cancel-append-chooser">Cancel</button>
        </div>
        <div class="workspace-task-result-artifact-chooser-options" role="radiogroup" aria-label="Choose append destination">
          <label class="workspace-task-result-artifact-chooser-option">
            <input type="radio" name="workspace-task-append-target" value="default" checked />
            <span>Default output folder${appendDefaultPathHint}</span>
          </label>
          <label class="workspace-task-result-artifact-chooser-option${storeNodeOptions ? '' : ' is-disabled'}">
            <input type="radio" name="workspace-task-append-target" value="store"${storeNodeOptions ? '' : ' disabled'} />
            <span>Store node</span>
            <select data-role="append-store-node" class="workspace-task-result-artifact-chooser-select"${storeNodeOptions ? '' : ' disabled'}>
              ${storeNodeOptions || '<option value="">No store nodes available</option>'}
            </select>
          </label>
          <label class="workspace-task-result-artifact-chooser-option">
            <input type="radio" name="workspace-task-append-target" value="custom" />
            <span>Custom path</span>
            <input type="text" data-role="append-custom-path" placeholder="e.g. /Users/me/Documents/runs.csv" class="workspace-task-result-artifact-chooser-input" />
          </label>
        </div>
        <label class="workspace-task-result-artifact-chooser-auto">
          <input type="checkbox" data-role="append-automate" />
          <span>Also auto-append future runs (opens storage editor after)</span>
        </label>
        <div class="workspace-task-result-artifact-chooser-error" data-role="append-error" hidden></div>
        <div class="workspace-task-result-artifact-chooser-actions">
          <button type="button" class="modern-btn modern-btn-primary" data-action="submit-append-chooser">Append</button>
        </div>
      </div>`;

    const contractColumns = this.getResultContractColumns();

    // Bottom CTA on the artifact card is just a small link routing to the
    // Automation card, which owns the storage on/off toggle and column
    // designer. The label adapts so the user knows what they'll see when
    // they get there.
    let bottomLinkLabel;
    if (!appendCtx.configured) {
      bottomLinkLabel = 'Set up CSV storage in Automation →';
    } else if (contractColumns.length > 0) {
      bottomLinkLabel = 'Edit output columns in Automation →';
    } else {
      bottomLinkLabel = 'Design columns from this result →';
    }
    const bottomCtaHtml = `
      <div class="workspace-task-result-artifact-design-link">
        <button type="button" class="workspace-task-page-text-button" data-action="design-output-columns-from-result">
          ${this.escapeHtml(bottomLinkLabel)}
        </button>
      </div>`;

    return `
      <section class="workspace-task-result-artifact" aria-label="CSV-ready task result">
        ${chipHtml}
        <div class="workspace-task-result-artifact-header">
          <div>
            <div class="workspace-task-page-mini-label">Artifact</div>
            <h3>${this.escapeHtml(artifact.title || 'CSV-ready result')}</h3>
          </div>
          <div class="workspace-task-result-artifact-actions">
            <button type="button" class="modern-btn modern-btn-secondary workspace-task-output-action-btn" data-action="export-result-artifact-csv" title="Download the full stored dataset as a CSV spreadsheet">
              <i class="bi bi-download" aria-hidden="true"></i>
              <span>Export CSV</span>
            </button>
            <button type="button" class="modern-btn modern-btn-secondary workspace-task-output-action-btn" data-action="copy-result-artifact-csv">
              <i class="bi bi-clipboard" aria-hidden="true"></i>
              <span>Copy CSV</span>
            </button>
            <button type="button" class="modern-btn modern-btn-secondary workspace-task-output-action-btn" data-action="save-result-artifact-note"${this.resultArtifactNoteSaving ? ' disabled' : ''}>
              <i class="bi bi-table" aria-hidden="true"></i>
              <span>${this.escapeHtml(savingLabel)}</span>
            </button>
            ${appendButton}
          </div>
        </div>
        ${bottomCtaHtml}
        ${chooserHtml}
        <div class="workspace-task-result-artifact-meta">
          <span>${this.escapeHtml(sourceLabel)}</span>
          <span>${this.escapeHtml(rowLabel)}</span>
          <span>${this.escapeHtml(columnLabel)}</span>
        </div>
        <div class="workspace-task-result-artifact-table-wrap" role="region" aria-label="Artifact table preview" tabindex="0">
          <table class="workspace-task-result-artifact-table">
            <thead><tr>${headHtml}</tr></thead>
            <tbody>${rowsHtml}</tbody>
          </table>
        </div>
        ${truncationNote ? `<div class="workspace-task-result-artifact-truncation">${this.escapeHtml(truncationNote)}</div>` : ''}
      </section>
    `;
  }

  // getResultContractColumns returns the saved output_contract columns for the
  // task that owns result storage (the workflow's final step, or the task
  // itself), normalized to a plain array.
  getResultContractColumns() {
    const owner = this.getTaskResultStorageTask();
    const columns = owner?.output_spec?.contract?.columns || owner?.output_contract?.columns;
    return Array.isArray(columns) ? columns : [];
  }

  getActiveOutputSpec() {
    const owner = this.getTaskResultStorageTask();
    return owner?.output_spec || null;
  }

  getOutputSpecSchemaFields(spec = this.getActiveOutputSpec()) {
    const fields = Array.isArray(spec?.schema?.fields) ? spec.schema.fields : [];
    return fields
      .map(field => ({
        name: String(field?.name || '').trim(),
        type: String(field?.type || 'string').trim() || 'string',
        required: field?.required === true,
        description: String(field?.description || '').trim()
      }))
      .filter(field => field.name);
  }

  getOutputSpecMappingsByColumn(spec = this.getActiveOutputSpec()) {
    const mappings = Array.isArray(spec?.mappings) ? spec.mappings : [];
    const byColumn = new Map();
    mappings.forEach(mapping => {
      const csvColumn = String(mapping?.csv_column || '').trim();
      if (!csvColumn) return;
      byColumn.set(csvColumn.toLowerCase(), {
        schemaField: String(mapping?.schema_field || '').trim(),
        transform: ['identity', 'json_string'].includes(mapping?.transform)
          ? mapping.transform
          : 'identity',
        defaultValue: String(mapping?.default_value || '').trim()
      });
    });
    return byColumn;
  }

  getOutputSpecMetadataFields(spec = this.getActiveOutputSpec()) {
    const defaults = ['run_id', 'executed_at', 'status', 'duration_ms'];
    const fields = Array.isArray(spec?.metadata_policy?.fields) ? spec.metadata_policy.fields : [];
    const byName = new Map();
    fields.forEach(field => {
      const name = String(field?.name || '').trim();
      if (!name) return;
      byName.set(name, { name, include: field?.include !== false });
    });
    defaults.forEach(name => {
      if (!byName.has(name)) byName.set(name, { name, include: true });
    });
    return Array.from(byName.values());
  }

  renderOutputSpecMetadataEditor() {
    const fields = this.getOutputSpecMetadataFields(
      this.resultOutputSpecDraft || this.getActiveOutputSpec()
    );
    return `
      <div class="workspace-task-output-spec-metadata-editor">
        <div class="workspace-task-page-mini-label">Run info saved with each row</div>
        <div class="workspace-task-output-spec-metadata-list">
          ${fields
            .map(
              field => `
            <label class="workspace-task-output-spec-metadata-item">
              <input type="checkbox" data-role="result-metadata-field" data-field-name="${this.escapeHtml(field.name)}"${field.include ? ' checked' : ''} />
              <span>${this.escapeHtml(field.name)}</span>
            </label>
          `
            )
            .join('')}
        </div>
      </div>`;
  }

  // renderResultContractBlock renders one of three Automation-card states:
  //   1. Saved contract + not editing -> "Columns: ... (Edit)" badge.
  //   2. No contract + not editing  -> assistant-first column suggestion CTA.
  //   3. Editing                    -> reviewable draft columns with manual escape hatches.
  renderResultContractBlock() {
    const editing = Array.isArray(this.resultContractDraft);
    const columns = this.getResultContractColumns();
    if (!editing && columns.length > 0) {
      const preview = columns
        .map(column => String(column?.name || '').trim())
        .filter(Boolean)
        .slice(0, 6)
        .join(', ');
      const overflow = columns.length > 6 ? `, +${columns.length - 6} more` : '';
      return `
        <div class="workspace-task-result-contract" data-state="view">
          <div class="workspace-task-result-contract-summary">
            <span class="workspace-task-page-mini-label">Each run returns</span>
            <span class="workspace-task-result-contract-summary-list">${this.escapeHtml(preview + overflow)}</span>
            <span class="workspace-task-result-contract-projection">Saved as a JSON record per run · run info (run_id, executed_at, status…) is added automatically · export to CSV anytime</span>
          </div>
          <button type="button" class="workspace-task-page-text-button" data-action="edit-result-contract">Edit fields</button>
        </div>`;
    }
    if (!editing) {
      return `
        <div class="workspace-task-result-contract" data-state="empty">
          <div class="workspace-task-result-contract-warning">
            <i class="bi bi-magic" aria-hidden="true"></i>
            <span>Let the assistant design the fields each run should return, from the latest result.</span>
          </div>
          <button type="button" class="modern-btn modern-btn-primary" data-action="suggest-result-contract">Suggest fields</button>
        </div>`;
    }

    const rowsHtml = this.resultContractDraft
      .map((column, index) => this.renderResultContractRow(column, index))
      .join('');
    const saving = Boolean(this.resultContractSaving);
    const suggesting = Boolean(this.resultContractSuggesting);

    return `
      <div class="workspace-task-result-contract" data-state="edit">
        <div class="workspace-task-result-contract-header">
          <div>
            <div class="workspace-task-page-mini-label">What each run returns</div>
            <p class="workspace-task-result-contract-help">Define the fields each run should return as structured data. The assistant extracts them into one row, checks it, then saves it as a CSV row (plus run info) only when it matches.</p>
          </div>
          <div class="workspace-task-result-contract-header-actions">
            <button type="button" class="modern-btn modern-btn-secondary" data-action="suggest-result-contract"${suggesting ? ' disabled' : ''}>
              ${suggesting ? '<span class="workspace-task-spinner" aria-hidden="true"></span>' : '<i class="bi bi-magic" aria-hidden="true"></i>'}
              <span>${this.escapeHtml(suggesting ? 'Suggesting' : 'Ask assistant')}${suggesting ? '<span class="workspace-task-dots" aria-hidden="true"><span></span><span></span><span></span></span>' : ''}</span>
            </button>
          </div>
        </div>
        <div class="workspace-task-result-format-steps" aria-label="Result storage setup steps">
          <span class="is-complete">Storage on</span>
          <span class="is-active">Review fields</span>
          <span>Save</span>
        </div>
        <div class="workspace-task-result-contract-rows" data-role="result-contract-rows">
          ${rowsHtml || '<div class="workspace-task-result-contract-empty">No fields yet. Add one or ask the assistant to design them from the latest result.</div>'}
        </div>
        ${this.renderResultFormatPreview(this.resultContractDraft)}
        ${this.renderOutputSpecMetadataEditor()}
        <div class="workspace-task-result-contract-row-add">
          <button type="button" class="workspace-task-page-text-button" data-action="add-result-contract-row">+ Add field</button>
        </div>
        <div class="workspace-task-result-contract-error" data-role="result-contract-error" hidden></div>
        <div class="workspace-task-result-contract-actions">
          <button type="button" class="workspace-task-page-text-button" data-action="cancel-result-contract">Cancel</button>
          <button type="button" class="modern-btn modern-btn-primary" data-action="save-result-contract"${saving ? ' disabled' : ''}>${this.escapeHtml(saving ? 'Saving...' : 'Save output')}</button>
        </div>
      </div>`;
  }

  renderResultContractRow(column, index) {
    const types = ['string', 'number', 'boolean', 'date'];
    const optionsHtml = types
      .map(
        type =>
          `<option value="${type}"${column?.type === type ? ' selected' : ''}>${type}</option>`
      )
      .join('');
    const required = column?.required !== false;
    const schemaField = String(column?.schema_field || column?.schemaField || column?.name || '');
    const transforms = ['identity', 'json_string'];
    const transform = transforms.includes(column?.transform) ? column.transform : 'identity';
    const transformOptions = transforms
      .map(
        value =>
          `<option value="${value}"${transform === value ? ' selected' : ''}>${value === 'json_string' ? 'JSON string' : 'Identity'}</option>`
      )
      .join('');
    return `
      <div class="workspace-task-result-contract-row" data-result-contract-row="${index}">
        <label class="workspace-task-result-contract-field workspace-task-result-contract-field-name">
          <span>Field</span>
          <input type="text" data-role="result-contract-name" placeholder="pollen_count" value="${this.escapeHtml(column?.name || '')}" aria-label="Field name" />
        </label>
        <label class="workspace-task-result-contract-field">
          <span>Type</span>
          <select data-role="result-contract-type" aria-label="Column type">${optionsHtml}</select>
        </label>
        <label class="workspace-task-result-contract-required">
          <input type="checkbox" data-role="result-contract-required"${required ? ' checked' : ''} />
          <span>Require value</span>
        </label>
        <button type="button" class="workspace-task-result-contract-remove" data-action="remove-result-contract-row" data-row-index="${index}" aria-label="Remove column">&times;</button>
        <details class="workspace-task-result-contract-advanced">
          <summary>Advanced mapping</summary>
          <div class="workspace-task-result-contract-advanced-grid">
            <label class="workspace-task-result-contract-field">
              <span>Assistant field</span>
              <input type="text" data-role="result-contract-schema-field" placeholder="pollen_count" value="${this.escapeHtml(schemaField)}" aria-label="Assistant field" />
            </label>
            <label class="workspace-task-result-contract-field">
              <span>Transform</span>
              <select data-role="result-contract-transform" aria-label="Mapping transform">${transformOptions}</select>
            </label>
            <label class="workspace-task-result-contract-field workspace-task-result-contract-field-description">
              <span>Notes</span>
              <input type="text" data-role="result-contract-description" placeholder="Optional note for this column" value="${this.escapeHtml(column?.description || '')}" aria-label="Column notes" />
            </label>
          </div>
        </details>
      </div>`;
  }

  renderResultFormatPreview(columns = []) {
    const usableColumns = Array.isArray(columns)
      ? columns
          .map(column => ({
            name: String(column?.name || '').trim(),
            type: String(column?.type || 'string').trim() || 'string'
          }))
          .filter(column => column.name)
      : [];
    if (usableColumns.length === 0) return '';
    const metadataFields = this.getResultMetadataPolicyDraft(
      this.resultOutputSpecDraft || this.getActiveOutputSpec()
    )
      .fields.filter(field => field.include !== false)
      .map(field => ({ name: field.name, type: 'run_info', metadata: true }));
    const previewColumns = [...metadataFields, ...usableColumns].slice(0, 10);
    const hiddenCount = Math.max(
      0,
      metadataFields.length + usableColumns.length - previewColumns.length
    );
    const sampleRow = this.getResultFormatPreviewRow(usableColumns);
    const headerHtml = previewColumns
      .map(
        column =>
          `<th scope="col"${column.metadata ? ' data-kind="metadata"' : ''}>${this.escapeHtml(column.name)}</th>`
      )
      .join('');
    const rowHtml = previewColumns
      .map(column => {
        const value = column.metadata
          ? this.previewMetadataValue(column.name)
          : sampleRow[column.name] || this.previewValueForType(column.type);
        return `<td${column.metadata ? ' data-kind="metadata"' : ''}>${this.escapeHtml(value)}</td>`;
      })
      .join('');
    return `
      <div class="workspace-task-result-format-preview">
        <div class="workspace-task-result-format-preview-header">
          <div>
            <div class="workspace-task-page-mini-label">CSV preview</div>
            <span>First row shape after the assistant parses a run result.</span>
          </div>
          ${hiddenCount ? `<small>+${this.escapeHtml(hiddenCount)} more column${hiddenCount === 1 ? '' : 's'}</small>` : ''}
        </div>
        <div class="workspace-task-result-format-preview-table" role="region" aria-label="CSV preview" tabindex="0">
          <table>
            <thead><tr>${headerHtml}</tr></thead>
            <tbody><tr>${rowHtml}</tr></tbody>
          </table>
        </div>
      </div>`;
  }

  getResultFormatPreviewRow(columns = []) {
    const artifact = this.currentResultArtifact || buildTaskResultArtifact(this.task);
    const rows = Array.isArray(artifact?.rows) ? artifact.rows : [];
    const candidate = rows.find(row => row && typeof row === 'object') || {};
    const row = {};
    columns.forEach(column => {
      const value = this.getCaseInsensitiveValue(candidate, column.name);
      if (value !== undefined && value !== null && String(value).trim() !== '') {
        row[column.name] = String(value);
      }
    });
    return row;
  }

  previewMetadataValue(name) {
    const history = Array.isArray(this.task?.execution_history) ? this.task.execution_history : [];
    const latest = history.length ? history[history.length - 1] : null;
    switch (String(name || '').trim()) {
      case 'run_id':
        return latest?.run_id ? String(latest.run_id).slice(0, 8) : 'run_1234';
      case 'executed_at':
        return latest?.executed_at ? String(latest.executed_at).slice(0, 10) : '2026-05-22';
      case 'status':
        return latest?.status ? String(latest.status) : 'success';
      case 'duration_ms':
        return latest?.duration !== undefined && latest?.duration !== null
          ? String(latest.duration)
          : '1200';
      default:
        return '';
    }
  }

  previewValueForType(type) {
    switch (
      String(type || '')
        .trim()
        .toLowerCase()
    ) {
      case 'number':
        return '9.7';
      case 'boolean':
        return 'true';
      case 'date':
        return '2026-05-22';
      default:
        return 'parsed from result';
    }
  }

  // getAppendCSVContext returns the effective CSV-append storage for the task
  // owning this result. Used by the result-card chip + Append button to decide
  // whether the user is one-clicking into an already-configured destination
  // or needs to pick one ad hoc.
  getAppendCSVContext() {
    const owner = this.getTaskResultStorageTask();
    const storage = owner?.result_storage || null;
    const configured = Boolean(
      storage &&
      storage.enabled === true &&
      String(storage.write_mode || '')
        .trim()
        .toLowerCase() === 'append'
    );
    if (!configured) {
      return { configured: false, label: '', locationHint: '' };
    }

    const storeNodeId = String(storage.store_node_id || '').trim();
    const filePath = String(storage.file_path || '').trim();
    let label = '';
    let locationHint = '';
    if (storeNodeId) {
      const storeLabel = this.getStoreNodeDisplayLabel(storeNodeId);
      label = filePath || this.defaultAppendCsvFilename();
      locationHint = `in ${storeLabel}`;
    } else if (filePath) {
      const segments = filePath.split('/');
      label = segments[segments.length - 1] || filePath;
      if (segments.length > 1) {
        locationHint = filePath;
      }
    } else {
      label = 'default output folder';
    }
    return { configured: true, label, locationHint };
  }

  bindResultArtifactActions() {
    const root = this.elements.output;
    if (!root) return;

    root
      .querySelector('[data-action="export-result-artifact-csv"]')
      ?.addEventListener('click', () => this.exportResultArtifactCSV());
    root
      .querySelector('[data-action="copy-result-artifact-csv"]')
      ?.addEventListener('click', () => this.copyCurrentArtifactCSV());
    root
      .querySelector('[data-action="save-result-artifact-note"]')
      ?.addEventListener('click', () => this.saveCurrentArtifactAsNote());
    root
      .querySelector('[data-action="append-result-artifact-csv"]')
      ?.addEventListener('click', () => this.handleAppendArtifactClick());
    root
      .querySelector('[data-action="cancel-append-chooser"]')
      ?.addEventListener('click', () => this.toggleAppendChooser(false));
    root
      .querySelector('[data-action="submit-append-chooser"]')
      ?.addEventListener('click', () => this.submitAppendChooser());
    root
      .querySelectorAll('[data-action="edit-append-storage"]')
      .forEach(btn => btn.addEventListener('click', () => this.openTaskStorageEditor()));
    // This action can appear in more than one place (review panel + result
    // CTA), so bind every match rather than just the first.
    root
      .querySelectorAll('[data-action="design-output-columns-from-result"]')
      .forEach(btn => btn.addEventListener('click', () => this.designOutputColumnsFromResult()));
  }

  // bindAutomationColumnsActions wires events on the column-designer DOM
  // inside the Automation card. Called by renderAutomationColumns whenever
  // that container re-renders.
  bindAutomationColumnsActions(root = this.elements.automationColumns) {
    if (!root) return;
    root
      .querySelector('[data-action="edit-result-contract"]')
      ?.addEventListener('click', () => this.startResultContractEdit());
    root
      .querySelector('[data-action="cancel-result-contract"]')
      ?.addEventListener('click', () => this.cancelResultContractEdit());
    root
      .querySelector('[data-action="add-result-contract-row"]')
      ?.addEventListener('click', () => this.addResultContractRow());
    root.querySelector('[data-action="suggest-result-contract"]')?.addEventListener('click', () => {
      if (Array.isArray(this.resultContractDraft)) {
        this.suggestResultContractColumns();
        return;
      }
      this.startResultContractEdit({ suggest: true });
    });
    root
      .querySelector('[data-action="save-result-contract"]')
      ?.addEventListener('click', () => this.saveResultContractDraft());
    root.querySelectorAll('[data-action="remove-result-contract-row"]').forEach(btn => {
      btn.addEventListener('click', () => {
        const idx = Number(btn.getAttribute('data-row-index') || '-1');
        this.removeResultContractRow(idx);
      });
    });
  }

  // Read the live DOM rows back into the draft state so re-renders preserve
  // user edits. The designer is render-on-state-change rather than render-on-
  // input to keep focus stable while typing.
  syncResultContractDraftFromDOM() {
    if (!Array.isArray(this.resultContractDraft)) return;
    const rows = this._designerRoot()?.querySelectorAll?.('[data-result-contract-row]');
    if (!rows || rows.length === 0) return;
    const next = [];
    rows.forEach(row => {
      const name = row.querySelector('[data-role="result-contract-name"]')?.value?.trim() || '';
      const type = row.querySelector('[data-role="result-contract-type"]')?.value || 'string';
      const schemaField =
        row.querySelector('[data-role="result-contract-schema-field"]')?.value?.trim() || name;
      const transform =
        row.querySelector('[data-role="result-contract-transform"]')?.value || 'identity';
      const required = Boolean(
        row.querySelector('[data-role="result-contract-required"]')?.checked
      );
      const description =
        row.querySelector('[data-role="result-contract-description"]')?.value?.trim() || '';
      next.push({ name, type, schema_field: schemaField, transform, required, description });
    });
    this.resultContractDraft = next;
  }

  startResultContractEdit({ suggest = false } = {}) {
    const existing = this.getResultContractColumns();
    this.resultOutputSpecDraft = this.getActiveOutputSpec();
    const mappingsByColumn = this.getOutputSpecMappingsByColumn(this.resultOutputSpecDraft);
    this.resultContractDraft = existing.length
      ? existing.map(column => ({
          name: String(column?.name || ''),
          type: ['string', 'number', 'boolean', 'date'].includes(column?.type)
            ? column.type
            : 'string',
          schema_field:
            mappingsByColumn.get(
              String(column?.name || '')
                .trim()
                .toLowerCase()
            )?.schemaField || String(column?.name || ''),
          transform:
            mappingsByColumn.get(
              String(column?.name || '')
                .trim()
                .toLowerCase()
            )?.transform || 'identity',
          required: column?.required !== false,
          description: String(column?.description || '')
        }))
      : suggest
        ? []
        : [
            {
              name: '',
              type: 'string',
              schema_field: '',
              transform: 'identity',
              required: true,
              description: ''
            }
          ];
    this.resultContractSuggesting = false;
    this.setResultContractError('');
    this.refreshResultRender();
    if (suggest) {
      void this.suggestResultContractColumns();
    }
  }

  cancelResultContractEdit() {
    // In modal mode, cancelling just closes the modal (which clears the draft
    // and refreshes the inline card).
    if (this.columnDesignerModalOpen) {
      this.closeColumnDesignerModal();
      return;
    }
    this.resultContractDraft = null;
    this.resultOutputSpecDraft = null;
    this.resultContractSuggesting = false;
    this.resultContractSaving = false;
    this.setResultContractError('');
    this.refreshResultRender();
  }

  addResultContractRow() {
    this.syncResultContractDraftFromDOM();
    if (!Array.isArray(this.resultContractDraft)) {
      this.resultContractDraft = [];
    }
    this.resultContractDraft.push({
      name: '',
      type: 'string',
      schema_field: '',
      transform: 'identity',
      required: true,
      description: ''
    });
    this.refreshResultRender();
  }

  removeResultContractRow(index) {
    this.syncResultContractDraftFromDOM();
    if (!Array.isArray(this.resultContractDraft)) return;
    if (index < 0 || index >= this.resultContractDraft.length) return;
    this.resultContractDraft.splice(index, 1);
    this.refreshResultRender();
  }

  setResultContractError(message) {
    const error = this._designerRoot()?.querySelector?.('[data-role="result-contract-error"]');
    if (!error) return;
    if (!message) {
      error.hidden = true;
      error.textContent = '';
      return;
    }
    error.hidden = false;
    error.textContent = message;
  }

  // refreshResultRender re-renders the Automation card after a designer state
  // change. It routes through renderSchedule because that method owns the
  // card hidden/visible decision as well as the columns and destination blocks.
  refreshResultRender() {
    // When the column-designer modal is open, the designer lives in the modal
    // body — re-render there and rebind, leaving the inline card untouched.
    if (this.columnDesignerModalOpen && this.columnModalBody) {
      this.columnModalBody.innerHTML =
        this.renderResultContractBlock() + this.renderSuggestionInputsPreview();
      this.bindAutomationColumnsActions(this.columnModalBody);
      const preview = this.columnModalBody.querySelector('.workspace-task-suggestion-preview');
      preview?.addEventListener('toggle', () => {
        this.suggestionPreviewOpen = preview.open;
      });
      return;
    }
    if (typeof this.renderSchedule === 'function' && this.elements.scheduleCard) {
      try {
        if (this._renderCache) delete this._renderCache.schedule;
        this.renderSchedule();
        return;
      } catch (error) {
        console.warn('Failed to re-render Automation card; falling back to data reload:', error);
      }
    }
    void this.loadData();
  }

  // Get a trimmed sample of the current task result, used to ground the
  // model's contract suggestions. The Result card "Design columns" link
  // captures this before opening the designer so the suggestion request
  // includes prior-run text instead of relying on title/details alone.
  captureSuggestionSampleFromCurrentResult() {
    this.lastSuggestionResultSample = this.getResultSampleForSuggestion();
  }

  // designOutputColumnsFromResult is the one-click "turn this result into CSV
  // columns" entry point used by the Result card and the review panel. It
  // captures the visible result as the suggestion sample, reveals the
  // Automation card, opens its "Advanced settings" disclosure (the designer
  // lives there), turns on append-CSV storage if it's off (which asks the
  // assistant to suggest columns), and scrolls the designer into view.
  // designOutputColumnsFromResult opens the dedicated column-designer modal,
  // pre-seeded with the current result as the suggestion sample. Used by the
  // Result-card CTA, the result-artifact link, and the review panel's "Edit
  // format". Saving approves the output spec and turns on append-CSV storage,
  // so the next run returns structured JSON in the designed shape.
  designOutputColumnsFromResult() {
    this.openColumnDesignerModal({ suggest: this.getResultContractColumns().length === 0 });
  }

  // renderSuggestionInputsPreview renders a collapsed "What the assistant sees"
  // disclosure for the modal so users can inspect the inputs feeding the column
  // suggestion (including while it's running). The task/details/schedule/sample
  // are exactly what the request sends; the instruction line is a faithful
  // summary of the backend system prompt (kept short to avoid drift).
  renderSuggestionInputsPreview() {
    const busy = Boolean(this.resultContractSuggesting);
    const line = (label, value) =>
      value
        ? `<div class="workspace-task-suggestion-input"><span class="workspace-task-suggestion-input-label">${this.escapeHtml(label)}</span><span>${this.escapeHtml(value)}</span></div>`
        : '';
    const block = (label, value) =>
      `<div class="workspace-task-suggestion-input"><span class="workspace-task-suggestion-input-label">${this.escapeHtml(label)}</span><pre class="workspace-task-suggestion-sample">${this.escapeHtml(value)}</pre></div>`;

    // Once a suggestion has run, show the exact prompt the backend echoed back.
    const echo = this.suggestionPromptEcho;
    if (echo && (echo.system || echo.user)) {
      const meta = [
        echo.provider,
        echo.model,
        echo.reasoning_effort ? `reasoning ${echo.reasoning_effort}` : ''
      ]
        .filter(Boolean)
        .join(' · ');
      return `
        <details class="workspace-task-suggestion-preview"${this.suggestionPreviewOpen ? ' open' : ''}>
          <summary>Exact prompt sent${busy ? ' <span class="workspace-task-suggestion-busy"><span class="workspace-task-spinner" aria-hidden="true"></span> suggesting</span>' : ''}</summary>
          <div class="workspace-task-suggestion-preview-body">
            ${meta ? line('Model', meta) : ''}
            ${block('System prompt', echo.system || '')}
            ${block('User message', echo.user || '')}
          </div>
        </details>`;
    }

    // Before the first suggestion (no echo yet), show the inputs being sent.
    const owner = this.getTaskResultStorageTask();
    const title = String(this.task?.description || owner?.description || '').trim();
    const details = String(this.task?.details || '').trim();
    const sample = String(this.getResultSampleForSuggestion() || '').trim();
    const recent = this.getRecentExecutionSamplesForSuggestion() || [];
    const scheduleEnabled = Boolean(owner?.schedule_enabled || this.task?.schedule_enabled);
    const scheduleName = String(owner?.schedule_name || this.task?.schedule_name || '').trim();

    const instruction =
      'Reads the latest result below and proposes the structured fields it contains (3–8) that this task can produce every run. You review and edit them before saving. Run info (run_id, executed_at, status, duration_ms) is added automatically.';

    // The latest result is the primary basis for the suggestion, so show it
    // first; task/details/schedule are supporting context.
    const sampleBlock = sample
      ? block('Latest result (basis for fields)', sample)
      : `<div class="workspace-task-suggestion-input"><span class="workspace-task-suggestion-input-label">Latest result (basis for fields)</span><span>None captured yet — the assistant will fall back to the task title and details.</span></div>`;

    return `
      <details class="workspace-task-suggestion-preview"${this.suggestionPreviewOpen ? ' open' : ''}>
        <summary>What the assistant sees${busy ? ' <span class="workspace-task-suggestion-busy"><span class="workspace-task-spinner" aria-hidden="true"></span> suggesting</span>' : ''}</summary>
        <div class="workspace-task-suggestion-preview-body">
          <p class="workspace-task-suggestion-instruction">${this.escapeHtml(instruction)}</p>
          ${sampleBlock}
          <div class="workspace-task-suggestion-context-label">Supporting context</div>
          ${line('Task', title)}
          ${line('Details', details)}
          ${scheduleEnabled ? line('Schedule', scheduleName || 'enabled') : ''}
          ${recent.length ? line('Earlier runs sampled', String(recent.length)) : ''}
        </div>
      </details>`;
  }

  // _designerRoot returns the DOM container the column designer currently
  // renders into: the modal body when the modal is open, otherwise the inline
  // Automation-card container. Render/read/error helpers route through this so
  // the same designer logic drives both surfaces.
  _designerRoot() {
    return this.columnDesignerModalOpen && this.columnModalBody
      ? this.columnModalBody
      : this.elements.automationColumns;
  }

  openColumnDesignerModal({ suggest = false } = {}) {
    if (this.columnDesignerModalOpen) return;
    this.captureSuggestionSampleFromCurrentResult();

    const overlay = document.createElement('div');
    overlay.className = 'workspace-task-column-modal-overlay';
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-modal', 'true');
    overlay.setAttribute('aria-label', 'Define what each run returns');
    overlay.innerHTML = `
      <div class="workspace-task-column-modal" role="document">
        <div class="workspace-task-column-modal-header">
          <h2>Define what each run returns</h2>
          <button type="button" class="workspace-task-page-icon-btn" data-action="close-column-modal" aria-label="Close">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/></svg>
          </button>
        </div>
        <div class="workspace-task-column-modal-body" data-role="column-modal-body"></div>
      </div>`;
    document.body.appendChild(overlay);
    this.columnModalOverlay = overlay;
    this.columnModalBody = overlay.querySelector('[data-role="column-modal-body"]');
    this.columnDesignerModalOpen = true;

    overlay.addEventListener('mousedown', event => {
      if (event.target === overlay) this.closeColumnDesignerModal();
    });
    overlay
      .querySelector('[data-action="close-column-modal"]')
      ?.addEventListener('click', () => this.closeColumnDesignerModal());
    this._columnModalKeydown = event => {
      if (event.key === 'Escape') this.closeColumnDesignerModal();
    };
    document.addEventListener('keydown', this._columnModalKeydown);

    // Initializes resultContractDraft and, because the modal is open, renders
    // the editor into the modal body via refreshResultRender.
    this.startResultContractEdit({ suggest });
  }

  closeColumnDesignerModal() {
    if (!this.columnDesignerModalOpen) return;
    this.columnDesignerModalOpen = false;
    if (this._columnModalKeydown) {
      document.removeEventListener('keydown', this._columnModalKeydown);
      this._columnModalKeydown = null;
    }
    this.columnModalOverlay?.remove();
    this.columnModalOverlay = null;
    this.columnModalBody = null;
    // Drop the editing draft so the inline card doesn't render in edit mode,
    // then refresh the inline card to reflect any saved spec.
    this.resultContractDraft = null;
    this.resultOutputSpecDraft = null;
    this.refreshResultRender();
  }

  // setCsvStorageEnabled drives the Automation card's storage checkbox.
  // Checking flips append-mode storage on with sensible defaults and opens
  // the column designer for the natural next step. Unchecking flips storage
  // off while preserving file_path/store_node_id so re-enabling later
  // doesn't lose the destination.
  async setCsvStorageEnabled(checked, checkbox) {
    if (this.automationStorageToggleBusy) return;
    this.automationStorageToggleBusy = true;
    if (checkbox) checkbox.disabled = true;

    const owner = this.getTaskResultStorageTask();
    const ownerId = owner?.id || this.taskId;
    const existing = owner?.result_storage || {};
    const nextStorage = {
      enabled: Boolean(checked),
      format: 'jsonl',
      write_mode: checked ? 'append' : String(existing.write_mode || 'append'),
      file_path: String(existing.file_path || ''),
      store_node_id: String(existing.store_node_id || ''),
      storage_target: String(existing.storage_target || ''),
      workspace_folder: String(existing.workspace_folder || '')
    };

    if (checked) {
      this.captureSuggestionSampleFromCurrentResult();
    }

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/tasks/${encodeURIComponent(ownerId)}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ result_storage: nextStorage })
        }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Could not update CSV storage (HTTP ${response.status})`);
      }
      this.notify('success', checked ? 'CSV storage enabled.' : 'CSV storage disabled.');
      await this.loadData();
      if (checked) {
        this.startResultContractEdit({ suggest: this.getResultContractColumns().length === 0 });
      }
    } catch (error) {
      console.error('Failed to update CSV storage:', error);
      this.notify('error', error?.message || 'Could not update CSV storage.');
      if (checkbox) {
        checkbox.checked = !checked;
      }
    } finally {
      this.automationStorageToggleBusy = false;
      if (checkbox) checkbox.disabled = false;
      // Force a direct, un-gated re-render of the Automation sub-sections so
      // the storage destination editor (and the checkbox/columns) reflect the
      // new state immediately. loadData()'s render() is cache-gated and can
      // skip this section, which previously left it stale until a manual
      // refresh. Mirrors saveAutomationStorageDestination's finally.
      this.renderAutomationSections();
    }
  }

  // renderAutomationColumns renders the columns sub-section into its
  // dedicated container in the Automation card. Replaces the prior result-
  // card surface for the same designer.
  renderAutomationColumns() {
    const container = this.elements.automationColumns;
    if (!container) return;
    if (!this.task) {
      container.innerHTML = '';
      return;
    }
    const ctx = this.getAppendCSVContext();
    const busy = Boolean(this.automationStorageToggleBusy);
    const toggleHtml = `
      <label class="workspace-task-automation-storage-toggle">
        <input type="checkbox" data-action="toggle-csv-storage"${ctx.configured ? ' checked' : ''}${busy ? ' disabled' : ''} />
        <span>
          <strong>Save each run of this task to a dataset</strong>
          <span class="workspace-task-automation-storage-help">Turns on Append mode so every run is saved as a record in a shared JSONL file (export to CSV anytime).</span>
        </span>
      </label>`;
    container.innerHTML = toggleHtml + (ctx.configured ? this.renderResultContractBlock() : '');
    this.bindAutomationColumnsActions();
    container
      .querySelector('[data-action="toggle-csv-storage"]')
      ?.addEventListener('change', event => {
        const target = event?.target;
        if (!target) return;
        void this.setCsvStorageEnabled(target.checked, target);
      });
  }

  // renderAutomationStorage renders the storage sub-section. When storage
  // is off, it's just a short placeholder pointing back to the toggle above.
  // When on, it shows an inline destination editor (radio + path/store-node
  // inputs + Save) so the user can set where the CSV file lives without
  // bouncing through the full storage modal.
  renderAutomationStorage() {
    const container = this.elements.automationStorage;
    if (!container) return;
    if (!this.task) {
      container.innerHTML = '';
      return;
    }
    const owner = this.getTaskResultStorageTask();
    const storage = owner?.result_storage || {};
    const enabled = Boolean(
      storage.enabled && String(storage.write_mode || '').toLowerCase() === 'append'
    );

    if (!enabled) {
      container.innerHTML = `
        <div class="workspace-task-automation-storage-block" data-state="off">
          <div class="workspace-task-automation-storage-copy">
            <div class="workspace-task-page-mini-label">Storage</div>
            <div class="workspace-task-automation-storage-description">Not saving automatically. Turn on the toggle above to choose a destination.</div>
          </div>
        </div>`;
      return;
    }

    const storeNodes = Array.isArray(this.workspace?.store_nodes) ? this.workspace.store_nodes : [];
    const workspaceFolders = Array.isArray(this.workspace?.folders) ? this.workspace.folders : [];
    const storeNodeId = String(storage.store_node_id || '').trim();
    const filePath = String(storage.file_path || '').trim();
    const storageTarget = String(storage.storage_target || '').trim();
    const workspaceFolder = String(storage.workspace_folder || '').trim();
    let target = 'default';
    if (storeNodeId) target = 'store';
    else if (storageTarget === 'workspace_folder') target = 'workspace_folder';
    else if (filePath) target = 'custom';

    const storeOptions = storeNodes
      .map(node => {
        const id = String(node?.id || '').trim();
        if (!id) return '';
        const label = this.getStoreNodeDisplayLabel(id);
        const selected = id === storeNodeId ? ' selected' : '';
        return `<option value="${this.escapeHtml(id)}"${selected}>${this.escapeHtml(label)}</option>`;
      })
      .filter(Boolean)
      .join('');
    const folderOptions = [
      '<option value="">Workspace files</option>',
      ...workspaceFolders.map(folder => {
        const path = String(folder?.path || '').trim();
        if (!path) return '';
        const selected = path === workspaceFolder ? ' selected' : '';
        return `<option value="${this.escapeHtml(path)}"${selected}>${this.escapeHtml(path)}</option>`;
      })
    ]
      .filter(Boolean)
      .join('');
    const saving = Boolean(this.automationStorageSaving);
    const defaultPath = String(this.workspaceOutputDir || '').trim();
    const defaultPathHint = defaultPath
      ? `<span class="workspace-task-automation-storage-path" title="${this.escapeHtml(defaultPath)}">${this.escapeHtml(defaultPath)}</span>`
      : '';
    const openFolderLabel = this.fileManagerActionLabel();
    const openFolderBtn = `
      <button type="button" class="workspace-task-automation-storage-open-btn" data-action="open-output-dir" title="${this.escapeHtml(openFolderLabel)}" aria-label="${this.escapeHtml(openFolderLabel)}">
        <i class="bi bi-folder2-open" aria-hidden="true"></i>
        <span>${this.escapeHtml(openFolderLabel)}</span>
      </button>`;
    const fileName =
      this.normalizeDatasetFileName(storage.file_name) || this.defaultAppendCsvFilename(owner);

    container.innerHTML = `
      <div class="workspace-task-automation-storage-block" data-state="on">
        <div class="workspace-task-page-mini-label">Storage destination</div>
        <div class="workspace-task-automation-storage-options" role="radiogroup" aria-label="Storage destination">
          <label class="workspace-task-automation-storage-option">
            <input type="radio" name="workspace-task-automation-storage-target" value="default"${target === 'default' ? ' checked' : ''} />
            <span>Default output folder${defaultPathHint}${openFolderBtn}</span>
            <input type="text" data-role="automation-storage-filename" class="workspace-task-automation-storage-input" placeholder="file name (e.g. nyc_pollen.jsonl)" value="${this.escapeHtml(fileName)}" aria-label="Dataset file name" />
          </label>
          <label class="workspace-task-automation-storage-option${storeOptions ? '' : ' is-disabled'}">
            <input type="radio" name="workspace-task-automation-storage-target" value="store"${target === 'store' ? ' checked' : ''}${storeOptions ? '' : ' disabled'} />
            <span>Store node</span>
            <select data-role="automation-storage-store-node" class="workspace-task-automation-storage-select"${storeOptions ? '' : ' disabled'}>
              ${storeOptions || '<option value="">No store nodes available</option>'}
            </select>
          </label>
          <label class="workspace-task-automation-storage-option">
            <input type="radio" name="workspace-task-automation-storage-target" value="workspace_folder"${target === 'workspace_folder' ? ' checked' : ''} />
            <span>Workspace file folder</span>
            <select data-role="automation-storage-workspace-folder" class="workspace-task-automation-storage-select">
              ${folderOptions}
            </select>
          </label>
          <label class="workspace-task-automation-storage-option">
            <input type="radio" name="workspace-task-automation-storage-target" value="custom"${target === 'custom' ? ' checked' : ''} />
            <span>Custom path</span>
            <input type="text" data-role="automation-storage-path" class="workspace-task-automation-storage-input" placeholder="e.g. /Users/me/Documents/runs.csv" value="${this.escapeHtml(target === 'custom' ? filePath : '')}" />
          </label>
        </div>
        <div class="workspace-task-automation-storage-error" data-role="automation-storage-error" hidden></div>
        <div class="workspace-task-automation-storage-actions">
          <button type="button" class="modern-btn modern-btn-primary" data-action="save-automation-storage"${saving ? ' disabled' : ''}>${this.escapeHtml(saving ? 'Saving...' : 'Save destination')}</button>
          <button type="button" class="workspace-task-page-text-button" data-action="open-automation-storage-modal">Advanced settings</button>
        </div>
      </div>`;
    container
      .querySelector('[data-action="save-automation-storage"]')
      ?.addEventListener('click', () => this.saveAutomationStorageDestination());
    container
      .querySelector('[data-action="open-automation-storage-modal"]')
      ?.addEventListener('click', () => this.openTaskStorageEditor());
    container.querySelector('[data-action="open-output-dir"]')?.addEventListener('click', event => {
      event.preventDefault();
      event.stopPropagation();
      this.openWorkspaceOutputDir();
    });
  }

  // fileManagerActionLabel returns a platform-appropriate label for buttons
  // that reveal a path in the OS file manager.
  fileManagerActionLabel() {
    const platform = (navigator.platform || navigator.userAgent || '').toLowerCase();
    if (platform.includes('mac')) return 'Open in Finder';
    if (platform.includes('win')) return 'Open in Explorer';
    return 'Open folder';
  }

  // openWorkspaceOutputDir asks the server to open this workspace's default
  // outputs folder in the local file manager. The dir is created lazily if it
  // doesn't exist yet.
  async openWorkspaceOutputDir() {
    if (!this.workspaceId) return;
    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/output-dir/open`,
        { method: 'POST' }
      );
      if (!response.ok) {
        const message = await this.extractErrorMessage(response).catch(() => '');
        throw new Error(message || `HTTP ${response.status}`);
      }
    } catch (err) {
      if (window.Toast && typeof window.Toast.error === 'function') {
        window.Toast.error(`Couldn't open output folder: ${err.message || err}`);
      } else {
        console.error('Failed to open output folder', err);
      }
    }
  }

  async extractErrorMessage(response) {
    try {
      const data = await response.json();
      return data?.error || data?.message || '';
    } catch (_) {
      return '';
    }
  }

  // saveAutomationStorageDestination reads the inline destination editor and
  // PATCHes the task's result_storage. Keeps the existing enabled+append
  // state — this only updates where the file lives, not whether storage is
  // on.
  async saveAutomationStorageDestination() {
    if (this.automationStorageSaving) return;
    const container = this.elements.automationStorage;
    if (!container) return;
    const target =
      container.querySelector('input[name="workspace-task-automation-storage-target"]:checked')
        ?.value || 'default';
    const storeNodeId =
      container.querySelector('[data-role="automation-storage-store-node"]')?.value || '';
    const workspaceFolder =
      container.querySelector('[data-role="automation-storage-workspace-folder"]')?.value || '';
    const customPath =
      container.querySelector('[data-role="automation-storage-path"]')?.value?.trim() || '';
    const fileNameInput =
      container.querySelector('[data-role="automation-storage-filename"]')?.value?.trim() || '';

    const setError = message => {
      const error = container.querySelector('[data-role="automation-storage-error"]');
      if (!error) return;
      if (!message) {
        error.hidden = true;
        error.textContent = '';
        return;
      }
      error.hidden = false;
      error.textContent = message;
    };
    setError('');

    if (target === 'store' && !storeNodeId) {
      setError('Pick a store node or choose a different destination.');
      return;
    }
    if (target === 'custom' && !customPath) {
      setError('Enter a file path or choose Default output folder.');
      return;
    }

    const owner = this.getTaskResultStorageTask();
    const ownerId = owner?.id || this.taskId;
    const existing = owner?.result_storage || {};
    // Only persist file_name when the user actually customized it (i.e. it
    // differs from the description-derived default); otherwise leave it blank
    // so the filename keeps tracking the task description. A full custom path
    // carries its own filename, so file_name doesn't apply there.
    const derivedFileName = this.defaultAppendCsvFilename(owner);
    const normalizedFileName = this.normalizeDatasetFileName(fileNameInput);
    const customFileName =
      target !== 'custom' && normalizedFileName && normalizedFileName !== derivedFileName
        ? normalizedFileName
        : '';
    const nextStorage = {
      enabled: true,
      format: 'jsonl',
      write_mode: 'append',
      file_path: target === 'custom' ? customPath : '',
      store_node_id: target === 'store' ? storeNodeId : '',
      storage_target: target === 'workspace_folder' ? 'workspace_folder' : '',
      workspace_folder: target === 'workspace_folder' ? workspaceFolder : '',
      file_name: customFileName
    };
    // No-op if nothing changed; avoids a needless PATCH + reload roundtrip.
    if (
      String(existing.file_path || '') === nextStorage.file_path &&
      String(existing.store_node_id || '') === nextStorage.store_node_id &&
      String(existing.storage_target || '') === nextStorage.storage_target &&
      String(existing.workspace_folder || '') === nextStorage.workspace_folder &&
      String(existing.file_name || '') === nextStorage.file_name &&
      String(existing.format || '') === nextStorage.format
    ) {
      this.notify('info', 'Destination is already set.');
      return;
    }

    this.automationStorageSaving = true;
    this.renderAutomationStorage();

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/tasks/${encodeURIComponent(ownerId)}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ result_storage: nextStorage })
        }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Could not save destination (HTTP ${response.status})`);
      }
      this.notify('success', 'Storage destination updated.');
      await this.loadData();
    } catch (error) {
      console.error('Failed to save storage destination:', error);
      this.notify('error', error?.message || 'Could not save destination.');
    } finally {
      this.automationStorageSaving = false;
      this.renderAutomationStorage();
    }
  }

  renderAutomationSections() {
    this.renderAutomationSummary();
    this.renderAutomationColumns();
    this.renderAutomationStorage();
  }

  // renderAutomationSummary writes the always-visible, plain-language line that
  // sits above the collapsed "Advanced settings" disclosure. Non-technical
  // users see where results go (or that none are saved) without opening the
  // CSV/storage/columns controls.
  renderAutomationSummary() {
    const container = this.elements.automationSummary;
    if (!container) return;

    const ctx = this.getAppendCSVContext();
    if (!ctx.configured) {
      container.innerHTML = '<span>Results are not saved automatically.</span>';
      return;
    }

    // Show the full destination including the CSV filename the run actually
    // writes, mirroring the backend: an explicit file path is used as-is; a
    // directory (or the default output folder) gets the task's derived
    // <slug>.csv appended.
    const owner = this.getTaskResultStorageTask();
    const storage = owner?.result_storage || {};
    const storeNodeId = String(storage.store_node_id || '').trim();
    const filePath = String(storage.file_path || '').trim();
    const storageTarget = String(storage.storage_target || '').trim();
    const workspaceFolder = String(storage.workspace_folder || '').trim();
    const filePathIsFile =
      filePath && !filePath.endsWith('/') && this.basename(filePath).includes('.');
    const filename = filePathIsFile
      ? this.basename(filePath)
      : this.normalizeDatasetFileName(storage.file_name) || this.defaultAppendCsvFilename(owner);
    const joinFile = dir => `${String(dir || '').replace(/\/+$/, '')}/${filename}`;

    let dest;
    if (storeNodeId) {
      dest = `${this.getStoreNodeDisplayLabel(storeNodeId)} · ${filename}`;
    } else if (storageTarget === 'workspace_folder') {
      dest = joinFile(`Workspace files/${workspaceFolder}`);
    } else if (filePathIsFile) {
      dest = filePath;
    } else if (filePath) {
      dest = joinFile(filePath);
    } else {
      const dir = String(this.workspaceOutputDir || '').trim();
      dest = dir ? joinFile(dir) : `the default output folder (${filename})`;
    }

    container.innerHTML =
      '<span>Saves each run to:</span>' +
      `<span class="workspace-task-automation-summary-path" title="${this.escapeHtml(dest)}">${this.escapeHtml(dest)}</span>`;
  }

  basename(path) {
    const parts = String(path || '')
      .replace(/\/+$/, '')
      .split('/');
    return parts[parts.length - 1] || '';
  }

  // defaultAppendCsvFilename mirrors the backend (AppendJSONLFileName): the
  // task description, capped at 30 chars, with non [A-Za-z0-9_-] dropped and
  // spaces turned into underscores, plus a .jsonl suffix (the canonical append
  // format). The name is kept for its many call sites.
  defaultAppendCsvFilename(task = this.task) {
    let name = String(task?.description || '');
    if (name.length > 30) name = name.slice(0, 30);
    let slug = '';
    for (const ch of name) {
      if (/[A-Za-z0-9_-]/.test(ch)) slug += ch;
      else if (ch === ' ') slug += '_';
    }
    if (!slug) slug = 'task';
    return `${slug}.jsonl`;
  }

  // normalizeDatasetFileName strips whatever extension a user typed (.csv,
  // .json, …) and forces .jsonl — the dataset is JSONL, so the destination
  // filename should reflect that. Returns '' when there's nothing usable.
  normalizeDatasetFileName(name) {
    let trimmed = String(name || '').trim();
    if (!trimmed) return '';
    trimmed = trimmed.replace(/\.(jsonl|csv|json|txt|ndjson)$/i, '');
    let slug = '';
    for (const ch of trimmed) {
      if (/[A-Za-z0-9_-]/.test(ch)) slug += ch;
      else if (ch === ' ') slug += '_';
    }
    if (!slug) return '';
    return `${slug}.jsonl`;
  }

  // Get a trimmed sample of the current task result, used to ground the
  // model's contract suggestions in actual data. Prefers the snapshot
  // captured by the Result card's "Design columns" link so the sample
  // matches what the user was looking at, not a stale task field. The
  // auto-task endpoint truncates server-side too, but trimming here keeps
  // the request small.
  getResultSampleForSuggestion() {
    const captured = String(this.lastSuggestionResultSample || '').trim();
    if (captured) return captured.slice(0, 4000);
    const candidate = this.task?.result || this.currentResultArtifact?.csv || '';
    return String(candidate || '')
      .trim()
      .slice(0, 4000);
  }

  getRecentExecutionSamplesForSuggestion(limit = 5) {
    const history = Array.isArray(this.task?.execution_history) ? this.task.execution_history : [];
    return history
      .slice(-limit)
      .map(entry => String(entry?.result || entry?.summary || entry?.error || '').trim())
      .filter(Boolean)
      .map(sample => sample.slice(0, 1200));
  }

  suggestFallbackResultContractColumns() {
    const artifact = this.currentResultArtifact || buildTaskResultArtifact(this.task);
    const artifactColumns = Array.isArray(artifact?.columns) ? artifact.columns : [];
    if (artifactColumns.length > 0) {
      const rows = Array.isArray(artifact?.rows) ? artifact.rows.slice(0, 25) : [];
      return artifactColumns
        .map(column => String(column || '').trim())
        .filter(Boolean)
        .slice(0, 8)
        .map(name => ({
          name,
          type: this.inferResultContractColumnType(name, rows),
          required:
            rows.length > 0 ? rows.every(row => String(row?.[name] ?? '').trim() !== '') : true,
          description: 'Column from the latest task result'
        }));
    }

    // Try to parse a "Label: value" list (e.g. markdown bullets) out of the
    // result so a plain-text result still yields meaningful fields without the
    // assistant. Falls back to date+summary only when nothing parseable.
    const parsed = this.parseFieldsFromResultText(this.getResultSampleForSuggestion());
    if (parsed.length > 0) return parsed;

    return [
      { name: 'date', type: 'date', required: true, description: 'Run date' },
      { name: 'summary', type: 'string', required: true, description: 'Short result summary' }
    ];
  }

  // parseFieldsFromResultText extracts up to 8 fields from "Label: value" lines
  // in a result (bullets like "- Today's pollen index: 10.5" included), turning
  // each label into a slug field with a best-guess type. Lines that are URLs,
  // questions, or have no value are skipped.
  parseFieldsFromResultText(text) {
    const lines = String(text || '')
      .replace(/\r\n/g, '\n')
      .split('\n');
    const fields = [];
    const seen = new Set();
    for (const raw of lines) {
      // Strip leading list markers ("-", "*", "•", digits) and whitespace.
      const line = raw.replace(/^\s*[-*•\d.)]+\s*/, '').trim();
      const idx = line.indexOf(':');
      if (idx <= 0) continue;
      const label = line.slice(0, idx).trim();
      const value = line.slice(idx + 1).trim();
      if (!value || /^https?:\/\//i.test(value) || label.length > 40 || label.endsWith('?'))
        continue;
      // Slugify the label into a field name.
      const slug = label
        .toLowerCase()
        .replace(/['’]/g, '')
        .replace(/\([^)]*\)/g, ' ')
        .replace(/[^a-z0-9]+/g, '_')
        .replace(/^_+|_+$/g, '');
      if (!slug || slug === 'page' || slug === 'source_url' || seen.has(slug)) continue;
      seen.add(slug);
      let type = 'string';
      if (/^\d{4}-\d{2}-\d{2}/.test(value)) type = 'date';
      else if (/^-?\d+(\.\d+)?$/.test(value)) type = 'number';
      fields.push({ name: slug, type, required: false, description: `Parsed from "${label}"` });
      if (fields.length >= 8) break;
    }
    return fields;
  }

  inferResultContractColumnType(columnName, rows = []) {
    const values = rows
      .map(row => String(row?.[columnName] ?? '').trim())
      .filter(Boolean)
      .slice(0, 12);
    if (values.length === 0) return 'string';
    const booleanValues = new Set(['true', 'false', 'yes', 'no']);
    if (values.every(value => booleanValues.has(value.toLowerCase()))) return 'boolean';
    if (values.every(value => Number.isFinite(Number(value.replace(/,/g, ''))))) return 'number';
    if (values.every(value => !Number.isNaN(Date.parse(value)))) return 'date';
    return 'string';
  }

  async suggestResultContractColumns() {
    if (this.resultContractSuggesting) return;
    this.syncResultContractDraftFromDOM();
    this.resultContractSuggesting = true;
    this.setResultContractError('');
    this.refreshResultRender();

    // Don't let a slow model leave the designer spinning forever. Abort just
    // above the backend's 90s cap (the Codex CLI cold start can take ~50-90s;
    // warm calls are ~15s) so the backend's timeout governs and we don't kill
    // a call it would have completed.
    const controller = new AbortController();
    const timeoutId = window.setTimeout(() => controller.abort(), 95000);
    try {
      const owner = this.getTaskResultStorageTask();
      const storage = owner?.result_storage || {};
      const response = await fetch('/api/orchestration/tasks/output-spec/suggest', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        signal: controller.signal,
        body: JSON.stringify({
          title: this.task?.description || owner?.description || '',
          details: this.task?.details || '',
          workspace_id: this.workspaceId || '',
          task_id: owner?.id || this.taskId || '',
          schedule: owner?.schedule || this.task?.schedule || null,
          schedule_enabled: Boolean(owner?.schedule_enabled || this.task?.schedule_enabled),
          schedule_name: owner?.schedule_name || this.task?.schedule_name || '',
          result_storage: {
            enabled: true,
            format: 'jsonl',
            write_mode: 'append',
            file_path: String(storage.file_path || ''),
            store_node_id: String(storage.store_node_id || '')
          },
          result_sample: this.getResultSampleForSuggestion(),
          recent_execution_samples: this.getRecentExecutionSamplesForSuggestion()
        })
      });
      const text = await response.text();
      if (!response.ok) {
        throw new Error(text || `Suggestion failed (HTTP ${response.status})`);
      }
      const parsed = text ? JSON.parse(text) : {};
      // Capture the verbatim prompt the backend echoed back so the preview can
      // show exactly what was sent.
      if (parsed?.prompt && (parsed.prompt.system || parsed.prompt.user)) {
        this.suggestionPromptEcho = parsed.prompt;
      }
      const columns = Array.isArray(parsed?.output_contract?.columns)
        ? parsed.output_contract.columns
        : [];
      if (columns.length === 0) {
        throw new Error('No columns suggested.');
      }
      this.resultOutputSpecDraft = parsed?.output_spec || null;
      const mappingsByColumn = this.getOutputSpecMappingsByColumn(this.resultOutputSpecDraft);
      this.resultContractDraft = columns.map(column => ({
        name: String(column?.name || ''),
        type: ['string', 'number', 'boolean', 'date'].includes(column?.type)
          ? column.type
          : 'string',
        schema_field:
          mappingsByColumn.get(
            String(column?.name || '')
              .trim()
              .toLowerCase()
          )?.schemaField || String(column?.name || ''),
        transform:
          mappingsByColumn.get(
            String(column?.name || '')
              .trim()
              .toLowerCase()
          )?.transform || 'identity',
        required: column?.required !== false,
        description: String(column?.description || '')
      }));
      this.notify(
        'success',
        `Suggested ${columns.length} output field${columns.length === 1 ? '' : 's'}.`
      );
    } catch (error) {
      console.error('Failed to suggest result contract:', error);
      const timedOut = error?.name === 'AbortError';
      const hasDraftColumns =
        Array.isArray(this.resultContractDraft) &&
        this.resultContractDraft.some(column => String(column?.name || '').trim());
      if (!hasDraftColumns) {
        this.resultContractDraft = this.suggestFallbackResultContractColumns();
        this.resultOutputSpecDraft = null;
        this.notify(
          'warning',
          timedOut
            ? 'The assistant took too long, so a local result format draft was created from the result. Edit it or try again.'
            : 'Assistant suggestion was unavailable, so a local result format draft was created from the result.'
        );
      } else {
        const message = timedOut
          ? 'The assistant took too long. Edit the draft or try again.'
          : error?.message || 'Could not suggest columns.';
        this.setResultContractError(message);
        this.notify(timedOut ? 'warning' : 'error', message);
      }
    } finally {
      window.clearTimeout(timeoutId);
      this.resultContractSuggesting = false;
      this.refreshResultRender();
    }
  }

  async saveResultContractDraft() {
    if (this.resultContractSaving) return;
    this.syncResultContractDraftFromDOM();
    const draft = Array.isArray(this.resultContractDraft) ? this.resultContractDraft : [];
    const cleaned = draft
      .map(column => ({
        name: String(column?.name || '').trim(),
        type: ['string', 'number', 'boolean', 'date'].includes(column?.type)
          ? column.type
          : 'string',
        schema_field: String(
          column?.schema_field || column?.schemaField || column?.name || ''
        ).trim(),
        transform: ['identity', 'json_string'].includes(column?.transform)
          ? column.transform
          : 'identity',
        required: Boolean(column?.required),
        description: String(column?.description || '').trim()
      }))
      .filter(column => column.name);

    if (cleaned.length === 0) {
      this.setResultContractError('Add at least one column with a name.');
      return;
    }
    const seen = new Set();
    for (const column of cleaned) {
      const key = column.name.toLowerCase();
      if (seen.has(key)) {
        this.setResultContractError(`Duplicate column name "${column.name}".`);
        return;
      }
      seen.add(key);
    }
    this.setResultContractError('');

    const owner = this.getTaskResultStorageTask();
    const ownerId = owner?.id || this.taskId;
    const existingStorage = owner?.result_storage || {};
    const nextStorage = {
      enabled: true,
      format: 'jsonl',
      write_mode: 'append',
      file_path: String(existingStorage.file_path || ''),
      store_node_id: String(existingStorage.store_node_id || '')
    };

    this.resultContractSaving = true;
    this.refreshResultRender();

    try {
      const outputSpec = this.buildOutputSpecDraftFromColumns(cleaned);
      const draftResponse = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/tasks/${encodeURIComponent(ownerId)}/output-spec/draft`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            output_spec: outputSpec,
            overwrite: true
          })
        }
      );
      const draftText = await draftResponse.text();
      if (!draftResponse.ok) {
        throw new Error(draftText || `Draft save failed (HTTP ${draftResponse.status})`);
      }
      const approveResponse = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/tasks/${encodeURIComponent(ownerId)}/output-spec/approve`,
        { method: 'POST' }
      );
      const approveText = await approveResponse.text();
      if (!approveResponse.ok) {
        throw new Error(approveText || `Approve failed (HTTP ${approveResponse.status})`);
      }
      const storageResponse = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/tasks/${encodeURIComponent(ownerId)}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            result_storage: nextStorage
          })
        }
      );
      const storageText = await storageResponse.text();
      if (!storageResponse.ok) {
        throw new Error(storageText || `Storage save failed (HTTP ${storageResponse.status})`);
      }
      this.notify(
        'success',
        `Saved ${cleaned.length} output field${cleaned.length === 1 ? '' : 's'}. Future runs will return them.`
      );
      this.resultContractDraft = null;
      this.resultOutputSpecDraft = null;
      this.resultContractSaving = false;
      this.closeColumnDesignerModal();
      await this.loadData();
    } catch (error) {
      console.error('Failed to save result contract:', error);
      this.resultContractSaving = false;
      this.setResultContractError(error?.message || 'Save failed.');
      this.notify('error', error?.message || 'Save failed.');
      this.refreshResultRender();
    }
  }

  buildOutputSpecDraftFromColumns(columns) {
    const existingSpec =
      this.resultOutputSpecDraft && typeof this.resultOutputSpecDraft === 'object'
        ? JSON.parse(JSON.stringify(this.resultOutputSpecDraft))
        : null;
    const normalizedColumns = columns.map(column => ({
      name: String(column?.name || '').trim(),
      type: ['string', 'number', 'boolean', 'date'].includes(column?.type) ? column.type : 'string',
      schema_field: String(
        column?.schema_field || column?.schemaField || column?.name || ''
      ).trim(),
      transform: ['identity', 'json_string'].includes(column?.transform)
        ? column.transform
        : 'identity',
      required: column?.required !== false,
      description: String(column?.description || '').trim()
    }));
    const columnNames = normalizedColumns.map(column => column.name);
    const contractColumns = normalizedColumns.map(column => ({
      name: column.name,
      type: column.type,
      required: column.required,
      description: column.description
    }));
    const existingContractNames = Array.isArray(existingSpec?.contract?.columns)
      ? existingSpec.contract.columns.map(column => String(column?.name || '').trim())
      : [];
    const fieldsByName = new Map();
    normalizedColumns.forEach(column => {
      const name = column.schema_field || column.name;
      if (!name || fieldsByName.has(name.toLowerCase())) return;
      fieldsByName.set(name.toLowerCase(), {
        name,
        type: column.type === 'number' || column.type === 'boolean' ? column.type : 'string',
        required: column.required,
        description: column.description
      });
    });
    const fields = Array.from(fieldsByName.values());
    if (
      existingSpec &&
      existingContractNames.length === columnNames.length &&
      existingContractNames.every((name, index) => name === columnNames[index])
    ) {
      existingSpec.source = existingSpec.source || 'manual';
      existingSpec.contract = {
        ...(existingSpec.contract || {}),
        source: existingSpec.contract?.source || existingSpec.source || 'manual',
        columns: contractColumns
      };
      existingSpec.schema = {
        ...(existingSpec.schema || {}),
        name: existingSpec.schema?.name || 'task_result',
        description: existingSpec.schema?.description || 'One normalized task result row.',
        strict: existingSpec.schema?.strict !== false,
        fields
      };
      existingSpec.mappings = normalizedColumns.map(column => ({
        schema_field: column.schema_field || column.name,
        csv_column: column.name,
        transform: column.transform || 'identity'
      }));
      existingSpec.metadata_policy = this.getResultMetadataPolicyDraft(existingSpec);
      return existingSpec;
    }
    return {
      source: existingSpec?.source || 'manual',
      schema: {
        name: 'task_result',
        description: 'One normalized task result row.',
        strict: true,
        fields
      },
      contract: {
        source: existingSpec?.contract?.source || existingSpec?.source || 'manual',
        columns: contractColumns
      },
      mappings: normalizedColumns.map(column => ({
        schema_field: column.schema_field || column.name,
        csv_column: column.name,
        transform: column.transform || 'identity'
      })),
      metadata_policy: this.getResultMetadataPolicyDraft(existingSpec)
    };
  }

  getResultMetadataPolicyDraft(existingSpec = null) {
    const fieldsFromDOM = Array.from(
      this._designerRoot()?.querySelectorAll?.('[data-role="result-metadata-field"]') || []
    );
    if (fieldsFromDOM.length > 0) {
      return {
        fields: fieldsFromDOM
          .map(input => ({
            name: String(input.getAttribute('data-field-name') || '').trim(),
            include: Boolean(input.checked)
          }))
          .filter(field => field.name)
      };
    }
    const fields = this.getOutputSpecMetadataFields(
      existingSpec || this.resultOutputSpecDraft || this.getActiveOutputSpec()
    );
    return { fields };
  }

  // handleAppendArtifactClick is the entry point for the Append button.
  // Configured tasks short-circuit to a single API call; unconfigured tasks
  // reveal the inline destination chooser instead of opening the full editor.
  handleAppendArtifactClick() {
    if (this.resultArtifactAppendBusy) return;
    const ctx = this.getAppendCSVContext();
    if (ctx.configured) {
      void this.appendArtifactToCSV({ useStorage: true });
      return;
    }
    this.toggleAppendChooser(true);
  }

  toggleAppendChooser(show) {
    const chooser = this.elements.output?.querySelector?.('[data-role="append-chooser"]');
    if (!chooser) return;
    chooser.hidden = !show;
    if (show) {
      this.setAppendChooserError('');
      chooser.querySelector('input[name="workspace-task-append-target"]:checked')?.focus?.();
    }
  }

  setAppendChooserError(message) {
    const error = this.elements.output?.querySelector?.('[data-role="append-error"]');
    if (!error) return;
    if (!message) {
      error.hidden = true;
      error.textContent = '';
      return;
    }
    error.hidden = false;
    error.textContent = message;
  }

  async submitAppendChooser() {
    const chooser = this.elements.output?.querySelector?.('[data-role="append-chooser"]');
    if (!chooser) return;
    const target =
      chooser.querySelector('input[name="workspace-task-append-target"]:checked')?.value ||
      'default';
    const storeNodeId = chooser.querySelector('[data-role="append-store-node"]')?.value || '';
    const customPath =
      chooser.querySelector('[data-role="append-custom-path"]')?.value?.trim() || '';
    const automate = Boolean(chooser.querySelector('[data-role="append-automate"]')?.checked);

    if (target === 'store' && !storeNodeId) {
      this.setAppendChooserError('Pick a store node or choose a different destination.');
      return;
    }
    if (target === 'custom' && !customPath) {
      this.setAppendChooserError('Enter a CSV path (e.g. /Users/me/Documents/runs.csv).');
      return;
    }
    this.setAppendChooserError('');

    const payload = {};
    if (target === 'store') payload.storeNodeId = storeNodeId;
    if (target === 'custom') payload.filePath = customPath;
    const ok = await this.appendArtifactToCSV(payload);
    if (!ok) return;
    this.toggleAppendChooser(false);
    if (automate) {
      void this.openTaskStorageEditor();
    }
  }

  setResultArtifactAppendBusy(busy) {
    this.resultArtifactAppendBusy = Boolean(busy);
    const button = this.elements.output?.querySelector?.(
      '[data-action="append-result-artifact-csv"]'
    );
    if (!button) return;
    button.disabled = this.resultArtifactAppendBusy;
    const label = button.querySelector('[data-role="append-label"]');
    if (!label) return;
    if (this.resultArtifactAppendBusy) {
      label.textContent = 'Appending...';
      return;
    }
    const ctx = this.getAppendCSVContext();
    label.textContent = ctx.configured ? `Append to ${ctx.label || 'CSV'}` : 'Append to CSV...';
  }

  // appendArtifactToCSV posts the current artifact's CSV to the workspace
  // task's append-csv endpoint. Returns true on success so callers can chain
  // follow-up UI (closing the chooser, opening the storage editor, etc.).
  async appendArtifactToCSV({ useStorage = false, filePath = '', storeNodeId = '' } = {}) {
    const csv = String(this.currentResultArtifact?.csv || '').trim();
    if (!csv) {
      this.notify('warning', 'No CSV artifact is available to append.');
      return false;
    }
    if (this.resultArtifactAppendBusy) return false;

    this.setResultArtifactAppendBusy(true);
    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/tasks/${encodeURIComponent(this.taskId)}/results/append-csv`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            csv,
            use_storage: Boolean(useStorage),
            file_path: filePath,
            store_node_id: storeNodeId
          })
        }
      );

      const text = await response.text();
      if (!response.ok) {
        throw new Error(text || `Append failed (HTTP ${response.status})`);
      }
      let parsed = {};
      try {
        parsed = text ? JSON.parse(text) : {};
      } catch (_e) {
        /* ignore */
      }
      const rows = Number(parsed.appended_rows || 0);
      const label = parsed.label || parsed.file_path || 'CSV';
      const rowsLabel = rows === 1 ? '1 row' : `${rows} rows`;
      this.notify('success', `Appended ${rowsLabel} to ${label}`);
      return true;
    } catch (error) {
      console.error('Failed to append artifact CSV:', error);
      this.setAppendChooserError(error?.message || 'Append failed.');
      this.notify('error', error?.message || 'Append failed.');
      return false;
    } finally {
      this.setResultArtifactAppendBusy(false);
    }
  }

  async copyCurrentArtifactCSV() {
    const csv = String(this.currentResultArtifact?.csv || '').trim();
    if (!csv) {
      this.notify('warning', 'No CSV artifact is available to copy.');
      return;
    }
    await this.copyToClipboard(csv, 'CSV copied');
  }

  // exportResultArtifactCSV downloads the full stored dataset as a CSV. The
  // canonical dataset is JSONL on disk; the server derives a spreadsheet CSV
  // on demand (data columns first). Unlike "Copy CSV" — which copies the
  // in-page preview — this is the authoritative file with every appended run.
  async exportResultArtifactCSV() {
    if (!this.workspaceId || !this.taskId) return;
    const url = `/api/workspaces/${encodeURIComponent(this.workspaceId)}/tasks/${encodeURIComponent(this.taskId)}/results/export-csv`;
    try {
      const response = await fetch(url);
      if (!response.ok) {
        const message = await this.extractErrorMessage(response).catch(() => '');
        throw new Error(message || `HTTP ${response.status}`);
      }
      const blob = await response.blob();
      const disposition = response.headers.get('Content-Disposition') || '';
      const match = /filename="?([^"]+)"?/i.exec(disposition);
      const filename = (match && match[1]) || `${this.taskId}.csv`;

      const objectUrl = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = objectUrl;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(objectUrl);
      this.notify('success', 'CSV exported');
    } catch (err) {
      this.notify('error', `Couldn't export CSV: ${err.message || err}`);
    }
  }

  setResultArtifactNoteSaving(saving) {
    this.resultArtifactNoteSaving = Boolean(saving);
    const button = this.elements.output?.querySelector?.(
      '[data-action="save-result-artifact-note"]'
    );
    if (!button) return;
    button.disabled = this.resultArtifactNoteSaving;
    const label = button.querySelector('span');
    if (label) label.textContent = this.resultArtifactNoteSaving ? 'Saving...' : 'Save CSV note';
  }

  buildResultArtifactNoteTitle(artifact) {
    const taskTitle = this.getTaskDisplayLabel();
    const base = `${artifact?.title || 'CSV'} - ${taskTitle || 'Task Result'}`;
    const cleaned = base.replace(/\s+/g, ' ').trim();
    return cleaned.length > 96 ? `${cleaned.slice(0, 93).trim()}...` : cleaned;
  }

  buildResultArtifactNoteContent(artifact, title) {
    const taskTitle = this.getTaskDisplayLabel();
    const sourceHref = this.getTaskHref(this.taskId);
    const savedAt = formatDateTime(new Date().toISOString());
    const sourceLabel = this.getArtifactSourceLabel(artifact?.source);
    const csvFence = artifactToCSVFence(artifact);

    return [
      `# ${title}`,
      '',
      `Saved from Ori task artifact on ${savedAt}.`,
      '',
      `- Source task: [${taskTitle}](${sourceHref})`,
      `- Artifact source: ${sourceLabel}`,
      `- Rows: ${artifact?.rows?.length || 0}`,
      `- Columns: ${artifact?.columns?.length || 0}`,
      '',
      '## CSV',
      '',
      csvFence
    ].join('\n');
  }

  async saveCurrentArtifactAsNote() {
    const artifact = this.currentResultArtifact;
    if (this.resultArtifactNoteSaving || !artifact?.csv) return;

    const title = this.buildResultArtifactNoteTitle(artifact);
    const content = this.buildResultArtifactNoteContent(artifact, title);
    this.setResultArtifactNoteSaving(true);

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: title, content })
        }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to create CSV note');
      }

      this.notify('success', 'CSV saved as a note');
    } catch (error) {
      console.error('Failed to save CSV artifact as note:', error);
      this.notify('error', error?.message || 'Failed to save CSV note');
    } finally {
      this.setResultArtifactNoteSaving(false);
    }
  }

  getResultHeadingLevel(node) {
    if (!node || node.nodeType !== Node.ELEMENT_NODE) return 0;
    const match = String(node.tagName || '').match(/^H([1-6])$/i);
    return match ? Number(match[1]) : 0;
  }

  pickResultSectionHeadingLevel(headings) {
    const counts = new Map();
    headings.forEach(heading => {
      const level = this.getResultHeadingLevel(heading);
      if (!level) return;
      counts.set(level, (counts.get(level) || 0) + 1);
    });

    const repeatedLevels = Array.from(counts.entries())
      .filter(([, count]) => count > 1)
      .map(([level]) => level)
      .sort((a, b) => a - b);
    if (repeatedLevels.length > 0) return repeatedLevels[0];

    const levels = Array.from(counts.keys()).sort((a, b) => a - b);
    if (levels.length > 1 && counts.get(levels[0]) === 1) return levels[1];
    return levels[0] || 0;
  }

  buildResultSectionId(title, index) {
    const slug = String(title || '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 56);
    return `result-section-${index}-${slug || 'section'}`;
  }

  getResultSectionTitle(section) {
    const explicit = String(section?.dataset?.resultSectionTitle || '').trim();
    if (explicit) return explicit;
    const heading = section?.querySelector?.('h1, h2, h3, h4, h5, h6');
    return (
      String(heading?.textContent || '')
        .replace(/\s+/g, ' ')
        .trim() || 'Result section'
    );
  }

  getResultSectionText(section) {
    const content = section?.querySelector?.('.workspace-task-result-section-content') || section;
    return String(content?.innerText || '')
      .replace(/\n{3,}/g, '\n\n')
      .trim();
  }

  updateResultSectionResearchButton(section, state = {}) {
    const button = section?.querySelector?.('[data-action="research-result-section"]');
    if (!button) return;

    const createdTaskId = String(
      state.createdTaskId || section.dataset.followUpTaskId || ''
    ).trim();
    const isPending = Boolean(state.pending);
    const icon = button.querySelector('i');
    const label = button.querySelector('.visually-hidden');

    button.disabled = isPending;
    button.classList.toggle('is-pending', isPending);
    button.classList.toggle('has-follow-up', Boolean(createdTaskId));
    button.dataset.tooltip = isPending
      ? 'Creating research task...'
      : createdTaskId
        ? 'Open research follow-up'
        : 'Draft research follow-up';
    button.setAttribute('aria-label', button.dataset.tooltip);
    button.title = button.dataset.tooltip;

    if (icon) {
      icon.className = isPending
        ? 'bi bi-arrow-repeat'
        : createdTaskId
          ? 'bi bi-box-arrow-up-right'
          : 'bi bi-search';
    }
    if (label) {
      label.textContent = button.dataset.tooltip;
    }
  }

  createResultSectionAction(section) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'workspace-task-result-section-action';
    button.dataset.action = 'research-result-section';
    button.dataset.tooltip = 'Draft research follow-up';
    button.title = button.dataset.tooltip;
    button.setAttribute('aria-label', button.dataset.tooltip);
    button.innerHTML =
      '<i class="bi bi-search" aria-hidden="true"></i><span class="visually-hidden">Draft research follow-up</span>';
    button.addEventListener('click', event => {
      event.preventDefault();
      event.stopPropagation();
      void this.handleResearchResultSection(section);
    });
    return button;
  }

  enhanceResultSections() {
    this.closeResultSectionMenu();
    const prose = this.elements.output?.querySelector?.('.workspace-task-page-prose');
    if (!prose) return;

    const headings = Array.from(prose.querySelectorAll('h1, h2, h3, h4, h5, h6'));
    if (headings.length === 0) return;

    const sectionHeadingLevel = this.pickResultSectionHeadingLevel(headings);
    if (!sectionHeadingLevel) return;

    const nodes = Array.from(prose.childNodes);
    let _currentSection = null;
    let currentSectionBody = null;
    let sectionIndex = 0;

    nodes.forEach(node => {
      const headingLevel = this.getResultHeadingLevel(node);
      const startsSection = headingLevel > 0 && headingLevel <= sectionHeadingLevel;

      if (startsSection) {
        sectionIndex += 1;
        const title =
          String(node.textContent || '')
            .replace(/\s+/g, ' ')
            .trim() || `Section ${sectionIndex}`;
        const section = document.createElement('section');
        section.className = 'workspace-task-result-section';
        section.dataset.resultSectionId = this.buildResultSectionId(title, sectionIndex);
        section.dataset.resultSectionTitle = title;
        section.setAttribute('aria-label', `Result section: ${title}`);

        const body = document.createElement('div');
        body.className = 'workspace-task-result-section-content';

        section.appendChild(this.createResultSectionAction(section));
        section.appendChild(body);
        prose.insertBefore(section, node);

        _currentSection = section;
        currentSectionBody = body;
      }

      if (currentSectionBody) {
        currentSectionBody.appendChild(node);
      }
    });

    const sections = Array.from(prose.querySelectorAll('.workspace-task-result-section'));
    if (sections.length === 0) return;

    prose.classList.add('is-sectioned');
    sections.forEach(section => {
      section.addEventListener('contextmenu', event => {
        event.preventDefault();
        this.showResultSectionMenu(section, event.clientX, event.clientY);
      });
    });
  }

  closeResultSectionMenu() {
    if (this.resultSectionMenu) {
      this.resultSectionMenu.remove();
      this.resultSectionMenu = null;
    }
    document.removeEventListener('click', this.boundResultSectionMenuDocumentClick, true);
    document.removeEventListener('keydown', this.boundResultSectionMenuKeydown, true);
    window.removeEventListener('scroll', this.boundResultSectionMenuScroll, true);
  }

  showResultSectionMenu(section, clientX, clientY) {
    this.closeResultSectionMenu();

    const title = this.getResultSectionTitle(section);
    const menu = document.createElement('div');
    menu.className = 'workspace-task-result-section-menu';
    menu.setAttribute('role', 'menu');
    menu.setAttribute('aria-label', `Actions for ${title}`);
    menu.innerHTML = `
      <div class="workspace-task-result-section-menu-title">${this.escapeHtml(summarizeText(title, 72))}</div>
      <button type="button" class="workspace-task-result-section-menu-item" data-action="research-result-section" role="menuitem">
        <i class="bi bi-search" aria-hidden="true"></i>
        <span>Draft research task</span>
      </button>
      <button type="button" class="workspace-task-result-section-menu-item" data-action="copy-result-section" role="menuitem">
        <i class="bi bi-clipboard" aria-hidden="true"></i>
        <span>Copy section</span>
      </button>
    `;

    document.body.appendChild(menu);
    const viewportPadding = 10;
    const menuRect = menu.getBoundingClientRect();
    const left = Math.min(
      Math.max(clientX, viewportPadding),
      Math.max(viewportPadding, window.innerWidth - menuRect.width - viewportPadding)
    );
    const top = Math.min(
      Math.max(clientY, viewportPadding),
      Math.max(viewportPadding, window.innerHeight - menuRect.height - viewportPadding)
    );
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;

    menu.querySelector('[data-action="research-result-section"]')?.addEventListener('click', () => {
      this.closeResultSectionMenu();
      void this.handleResearchResultSection(section);
    });
    menu.querySelector('[data-action="copy-result-section"]')?.addEventListener('click', () => {
      this.closeResultSectionMenu();
      void this.copyResultSection(section);
    });

    this.resultSectionMenu = menu;
    setTimeout(() => {
      document.addEventListener('click', this.boundResultSectionMenuDocumentClick, true);
      document.addEventListener('keydown', this.boundResultSectionMenuKeydown, true);
      window.addEventListener('scroll', this.boundResultSectionMenuScroll, true);
    }, 0);
  }

  buildResultResearchTitle(sectionTitle) {
    const title =
      String(sectionTitle || 'Result section')
        .replace(/\s+/g, ' ')
        .trim() || 'Result section';
    const combined = `Research further: ${title}`;
    return combined.length > 160 ? `${combined.slice(0, 157).trim()}...` : combined;
  }

  buildResultResearchDetails(sectionTitle) {
    const sourceTitle = this.getTaskDisplayLabel();
    return [
      `Follow-up research created from completed task: ${sourceTitle}`,
      `Selected result section: ${sectionTitle}`,
      `Source task: ${this.task?.id || this.taskId}`,
      '',
      'Use the linked task result as context, but focus the answer on this selected section.',
      'Research it more deeply, add practical criteria and tradeoffs, and include citations when web research is available.'
    ].join('\n');
  }

  buildResultResearchDraft(section, sectionTitle, sectionText) {
    const currentAgent = String(this.task?.to || '').trim();
    return {
      sectionId: String(section?.dataset?.resultSectionId || '').trim(),
      sectionTitle,
      sourceTaskId: String(this.task?.id || this.taskId || '').trim(),
      title: this.buildResultResearchTitle(sectionTitle),
      agent: currentAgent && currentAgent !== 'unassigned' ? currentAgent : '',
      details: this.buildResultResearchDetails(sectionTitle),
      sectionText,
      linkSource: true,
      runNow: true,
      openAfterCreate: true
    };
  }

  renderResultResearchAgentOptions(selectedAgent = '') {
    if (!this.elements.resultResearchAgentSelect) return;

    const normalizedSelected = String(selectedAgent || '').trim();
    const options = ['<option value="">Unassigned manual task</option>'];
    const names = this.getAssignableAgentNames(normalizedSelected);
    if (
      normalizedSelected &&
      !names.some(
        name =>
          String(name || '')
            .trim()
            .toLowerCase() === normalizedSelected.toLowerCase()
      )
    ) {
      options.push(
        `<option value="${this.escapeHtml(normalizedSelected)}" selected>${this.escapeHtml(`${normalizedSelected} (Current)`)}</option>`
      );
    }
    names.forEach(agentName => {
      const normalized = String(agentName || '').trim();
      if (!normalized) return;
      options.push(
        `<option value="${this.escapeHtml(normalized)}" ${normalized.toLowerCase() === normalizedSelected.toLowerCase() ? 'selected' : ''}>${this.escapeHtml(normalized)}</option>`
      );
    });
    this.elements.resultResearchAgentSelect.innerHTML = options.join('');
  }

  populateResultResearchModal(draft) {
    if (!draft) return;

    if (this.elements.resultResearchSectionMeta) {
      const taskLabel = summarizeText(this.getTaskDisplayLabel(), 90);
      const sectionLabel = summarizeText(draft.sectionTitle, 90);
      this.elements.resultResearchSectionMeta.textContent = `${sectionLabel} • from ${taskLabel}`;
    }
    if (this.elements.resultResearchTitleInput) {
      this.elements.resultResearchTitleInput.value = draft.title;
    }
    this.renderResultResearchAgentOptions(draft.agent);
    if (this.elements.resultResearchDetailsInput) {
      this.elements.resultResearchDetailsInput.value = draft.details;
    }
    if (this.elements.resultResearchSectionInput) {
      this.elements.resultResearchSectionInput.value = draft.sectionText;
    }
    if (this.elements.resultResearchLinkInput) {
      this.elements.resultResearchLinkInput.checked = draft.linkSource !== false;
    }
    if (this.elements.resultResearchRunInput) {
      this.elements.resultResearchRunInput.checked = draft.runNow !== false;
    }
    if (this.elements.resultResearchOpenInput) {
      this.elements.resultResearchOpenInput.checked = draft.openAfterCreate !== false;
    }
    this.setResultResearchSubmitting(false);
    this.updateResultResearchSubmitLabel();
  }

  openResultResearchModal(draft) {
    if (!this.elements.resultResearchModal || typeof bootstrap === 'undefined') return;

    this.resultResearchDraft = draft;
    this.populateResultResearchModal(draft);
    const modal =
      typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.resultResearchModal)
        : bootstrap.Modal.getInstance(this.elements.resultResearchModal) ||
          new bootstrap.Modal(this.elements.resultResearchModal);
    modal.show();
    setTimeout(() => {
      this.elements.resultResearchTitleInput?.focus();
      this.elements.resultResearchTitleInput?.select();
    }, 120);
  }

  collectResultResearchDraft() {
    const draft = { ...(this.resultResearchDraft || {}) };
    draft.title = String(this.elements.resultResearchTitleInput?.value || '').trim();
    draft.agent = String(this.elements.resultResearchAgentSelect?.value || '').trim();
    draft.details = String(this.elements.resultResearchDetailsInput?.value || '').trim();
    draft.sectionText = String(this.elements.resultResearchSectionInput?.value || '').trim();
    draft.linkSource = this.elements.resultResearchLinkInput?.checked !== false;
    draft.runNow = this.elements.resultResearchRunInput?.checked !== false;
    draft.openAfterCreate = this.elements.resultResearchOpenInput?.checked !== false;
    return draft;
  }

  validateResultResearchDraft(draft) {
    if (!draft || typeof draft !== 'object') return 'Research draft is unavailable.';
    if (!String(draft.title || '').trim()) return 'Task title is required.';
    if (!String(draft.details || '').trim()) return 'Instructions are required.';
    if (!String(draft.sectionText || '').trim()) return 'Selected section text is required.';
    if (draft.runNow && !String(draft.agent || '').trim())
      return 'Choose an agent before running now, or turn off Run immediately.';
    return '';
  }

  buildResultResearchPayload(draft) {
    const details = [
      String(draft.details || '').trim(),
      '',
      'Selected section text:',
      String(draft.sectionText || '').trim()
    ]
      .join('\n')
      .trim();
    const payload = {
      workspace_id: this.workspaceId,
      description: draft.title,
      details,
      priority: Number.isFinite(this.task?.priority) ? this.task.priority : 3,
      to: draft.agent || undefined,
      input_task_ids: draft.linkSource
        ? [draft.sourceTaskId || this.task?.id || this.taskId].filter(Boolean)
        : []
    };

    const currentAgent = String(this.task?.to || '').trim();
    const currentAssignedNode = String(this.task?.assigned_node_id || '').trim();
    if (
      currentAssignedNode &&
      draft.agent &&
      draft.agent.toLowerCase() === currentAgent.toLowerCase()
    ) {
      payload.assigned_node_id = currentAssignedNode;
    }

    return payload;
  }

  async createResultResearchTask(draft) {
    const payload = this.buildResultResearchPayload(draft);

    const response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(data?.error || data?.message || 'Failed to create research task');
    }

    const createdTask = data.task || data;
    if (!createdTask?.id) {
      throw new Error('Research task was created without an id');
    }

    if (!draft.runNow) {
      return createdTask;
    }

    const executeResponse = await fetch('/api/orchestration/tasks/execute', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ task_id: createdTask.id })
    });
    if (!executeResponse.ok) {
      const text = await executeResponse.text();
      throw new Error(text || 'Research task was created but could not be started');
    }

    return createdTask;
  }

  setResultResearchSubmitting(isSubmitting) {
    this.resultResearchSubmitting = Boolean(isSubmitting);
    if (this.elements.resultResearchSubmitBtn) {
      this.elements.resultResearchSubmitBtn.disabled = this.resultResearchSubmitting;
      this.elements.resultResearchSubmitBtn.innerHTML = this.resultResearchSubmitting
        ? '<span class="spinner-border spinner-border-sm" aria-hidden="true"></span><span>Creating...</span>'
        : this.getResultResearchSubmitMarkup();
    }
  }

  getResultResearchSubmitMarkup() {
    const runNow = this.elements.resultResearchRunInput?.checked !== false;
    return runNow
      ? '<i class="bi bi-send" aria-hidden="true"></i><span>Create & Run</span>'
      : '<i class="bi bi-plus-circle" aria-hidden="true"></i><span>Create Draft</span>';
  }

  updateResultResearchSubmitLabel() {
    if (!this.elements.resultResearchSubmitBtn || this.resultResearchSubmitting) return;
    this.elements.resultResearchSubmitBtn.innerHTML = this.getResultResearchSubmitMarkup();
  }

  async handleResearchResultSection(section) {
    const sectionId = String(section?.dataset?.resultSectionId || '').trim();
    if (!section || !sectionId || this.resultResearchSubmitting) return;

    const existingFollowUpId = String(section.dataset.followUpTaskId || '').trim();
    if (existingFollowUpId) {
      window.location.href = this.getTaskHref(existingFollowUpId);
      return;
    }

    const sectionTitle = this.getResultSectionTitle(section);
    const sectionText = this.getResultSectionText(section);
    if (!sectionText) {
      this.notify('warning', 'No section text is available to research.');
      return;
    }

    const draft = this.buildResultResearchDraft(section, sectionTitle, sectionText);
    this.openResultResearchModal(draft);
  }

  async submitResultResearchDraft() {
    if (this.resultResearchSubmitting) return;

    const draft = this.collectResultResearchDraft();
    const validationError = this.validateResultResearchDraft(draft);
    if (validationError) {
      this.notify('warning', validationError);
      return;
    }

    const section = draft.sectionId
      ? Array.from(this.elements.output?.querySelectorAll?.('[data-result-section-id]') || []).find(
          item => String(item?.dataset?.resultSectionId || '') === String(draft.sectionId)
        )
      : null;

    this.resultResearchPendingSectionId = String(draft.sectionId || '').trim();
    this.setResultResearchSubmitting(true);
    this.updateResultSectionResearchButton(section, { pending: true });

    try {
      const createdTask = await this.createResultResearchTask(draft);
      const createdTaskId = String(createdTask?.id || '').trim();
      if (section && createdTaskId) {
        section.dataset.followUpTaskId = createdTaskId;
      }
      section?.classList.add('has-follow-up');
      this.updateResultSectionResearchButton(section, { createdTaskId });
      this.notify(
        'success',
        draft.runNow ? 'Research follow-up started' : 'Research follow-up created'
      );
      if (this.elements.resultResearchModal && typeof bootstrap !== 'undefined') {
        bootstrap.Modal.getInstance(this.elements.resultResearchModal)?.hide();
      }
      if (draft.openAfterCreate && createdTaskId) {
        window.location.href = this.getTaskHref(createdTaskId);
        return;
      }
    } catch (error) {
      console.error('Failed to start result section research:', error);
      this.notify('error', error?.message || 'Failed to start research follow-up');
      this.updateResultSectionResearchButton(section);
    } finally {
      this.resultResearchPendingSectionId = '';
      this.setResultResearchSubmitting(false);
    }
  }

  async copyResultSection(section) {
    const sectionText = this.getResultSectionText(section);
    if (!sectionText) {
      this.notify('warning', 'No section text is available to copy.');
      return;
    }
    await this.copyToClipboard(sectionText, 'Section copied');
  }

  // renderTaskOutputShape paints the "Expected output shape" card with the
  // task's declared schema fields, CSV contract columns, and the run-info
  // metadata flagged for CSV inclusion. Falls back to an empty-state when
  // no structured spec is configured (LLM returns free text).
  // hasStructuredOutputShape reports whether the task declares a structured
  // output — JSON schema fields or CSV contract columns. Drives both the
  // "Expected output shape" subsection and whether the Automation & output
  // card is shown at all when nothing else (schedule/storage) keeps it up.
  hasStructuredOutputShape() {
    const t = this.task || {};
    const spec = t.output_spec || null;
    const schemaSource = (spec && spec.schema) || t.output_schema || null;
    const contractSource = (spec && spec.contract) || t.output_contract || null;
    const schemaFields = Array.isArray(schemaSource?.fields) ? schemaSource.fields : [];
    const contractColumns = Array.isArray(contractSource?.columns) ? contractSource.columns : [];
    return schemaFields.length > 0 || contractColumns.length > 0;
  }

  renderTaskOutputShape() {
    if (!this.elements.outputShape || !this.elements.outputShapeWrap) return;

    const t = this.task || {};
    const spec = t.output_spec || null;
    const schemaSource = (spec && spec.schema) || t.output_schema || null;
    const contractSource = (spec && spec.contract) || t.output_contract || null;

    const schemaFields = Array.isArray(schemaSource?.fields) ? schemaSource.fields : [];
    const contractColumns = Array.isArray(contractSource?.columns) ? contractSource.columns : [];

    const hasAnyStructure = schemaFields.length > 0 || contractColumns.length > 0;

    // Hide just the subsection on free-text tasks. The "Expected output shape"
    // panel only carries meaning when the task declares a JSON schema or CSV
    // contract; a developer-flavored empty state doesn't belong here. The
    // surrounding Automation & output card stays governed by renderSchedule.
    if (!hasAnyStructure) {
      this.elements.outputShapeWrap.hidden = true;
      this.elements.outputShape.innerHTML = '';
      return;
    }
    this.elements.outputShapeWrap.hidden = false;

    // One shape, schema-first. The JSON schema is the canonical record shape;
    // fall back to the legacy CSV contract columns only for tasks that predate
    // the schema. The old separate "CSV columns" and "Run info" tables are
    // gone — CSV is derived from these fields at export time, so repeating them
    // here was the duplication.
    const shapeFields = (schemaFields.length ? schemaFields : contractColumns).map(field => ({
      name: String(field?.name || ''),
      type: String(field?.type || 'string'),
      required: Boolean(field?.required),
      description: String(field?.description || '')
    }));

    const rowsHtml = shapeFields
      .map(
        field => `
      <tr>
        <td>${this.escapeHtml(field.name)}</td>
        <td class="workspace-task-output-shape-col-type">${this.escapeHtml(field.type)}</td>
        <td class="workspace-task-output-shape-col-required ${field.required ? '' : 'is-optional'}">${field.required ? 'required' : 'optional'}</td>
        <td>${this.escapeHtml(field.description)}</td>
      </tr>
    `
      )
      .join('');

    this.elements.outputShape.innerHTML = `
      <p class="workspace-task-output-shape-note">Each run is stored as a JSON record with these fields. Use <strong>Export CSV</strong> on the dataset for a spreadsheet — its columns are derived from these fields, plus run info (run_id, executed_at, status…).</p>
      <table class="workspace-task-output-shape-table">
        <thead>
          <tr><th>Field</th><th>Type</th><th>Required</th><th>Description</th></tr>
        </thead>
        <tbody>${rowsHtml}</tbody>
      </table>`;
  }

  renderOutput() {
    if (!this.elements.output || !this.elements.outputCard) return;

    const result = normalizeResultText(this.task?.result).trim();
    const error = String(this.task?.error || '').trim();
    const artifact = buildTaskResultArtifact(this.task);
    this.currentResultArtifact = artifact;
    this.closeResultSectionMenu();

    if (!result && !error && !artifact) {
      this.elements.outputCard.hidden = true;
      this.elements.output.innerHTML = '';
      this.savedResultNote = null;
      this.savedResultNoteResult = '';
      this.currentResultArtifact = null;
      this.updateResultActionButtons('', false);
      this.renderResultNoteStatus();
      return;
    }

    if (
      this.savedResultNote &&
      result &&
      this.savedResultNoteResult &&
      this.savedResultNoteResult !== result
    ) {
      this.savedResultNote = null;
      this.savedResultNoteResult = '';
    }

    const blocks = [];
    blocks.push(this.renderWebSearchBadge());
    const latestStorageStatus = this.renderLatestStorageStatus();
    if (latestStorageStatus) {
      blocks.push(latestStorageStatus);
    }
    const reviewPanel = this.renderNeedsReviewPanel();
    if (reviewPanel) {
      blocks.push(reviewPanel);
    }
    if (result) {
      blocks.push(`
        <div class="workspace-task-page-mini-label">Result</div>
        ${this.renderMarkdownOrPre(result)}
      `);
    }
    // The accumulated run-history dataset (the CSV across every run) is
    // secondary to the latest result, so tuck it into a disclosure: collapsed
    // when there's a result to lead with, expanded when the dataset is the only
    // output this task produced.
    if (artifact) {
      const datasetRows = Array.isArray(artifact.rows) ? artifact.rows.length : 0;
      const datasetCols = Array.isArray(artifact.columns) ? artifact.columns.length : 0;
      const datasetMeta = [
        datasetRows ? `${datasetRows} row${datasetRows === 1 ? '' : 's'}` : '',
        datasetCols ? `${datasetCols} column${datasetCols === 1 ? '' : 's'}` : ''
      ]
        .filter(Boolean)
        .join(' · ');
      blocks.push(`
        <details class="workspace-task-result-artifact-disclosure"${result ? '' : ' open'}>
          <summary class="workspace-task-result-artifact-summary">
            <span class="workspace-task-result-artifact-summary-title">${this.escapeHtml(artifact.title || 'Dataset')}</span>
            ${datasetMeta ? `<span class="workspace-task-result-artifact-summary-meta">${this.escapeHtml(datasetMeta)}</span>` : ''}
          </summary>
          ${this.renderResultArtifact(artifact)}
        </details>
      `);
    }
    // Entry point to structure a plain-text result into CSV columns. The
    // artifact card carries its own "Design columns" link, so only offer this
    // when there's a result, no tabular artifact, and no columns yet.
    if (result && !artifact && this.getResultContractColumns().length === 0) {
      const ctaLabel = this.getAppendCSVContext().configured
        ? 'Define the structured output →'
        : 'Make this task return structured output →';
      blocks.push(`
        <div class="workspace-task-output-design-cta">
          <button type="button" class="workspace-task-page-text-button" data-action="design-output-columns-from-result">
            <i class="bi bi-table" aria-hidden="true"></i> ${this.escapeHtml(ctaLabel)}
          </button>
        </div>
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
    this.bindResultArtifactActions();
    this.bindOutputReviewActions();
    this.enhanceResultSections();
    this.enhanceResultItems();
    this.updateResultActionButtons(result || error, Boolean(result));
    this.renderResultNoteStatus();
  }

  // A pill at the top of the Result card stating whether this task's answer was
  // backed by a web search. usedWebSearch() inspects the tool-usage trace, so
  // the "used" notification only shows when the web_search tool actually ran;
  // otherwise we surface an explicit "No web search" tag so a reader never has
  // to guess whether the result drew on live web sources.
  renderWebSearchBadge() {
    if (this.usedWebSearch()) {
      return `
        <div class="workspace-task-websearch-badge" data-state="used" role="status">
          <i class="bi bi-globe2" aria-hidden="true"></i>
          <span>Web search used</span>
        </div>
      `;
    }
    return `
      <div class="workspace-task-websearch-badge" data-state="unused" role="status">
        <i class="bi bi-slash-circle" aria-hidden="true"></i>
        <span>No web search</span>
      </div>
    `;
  }

  renderLatestStorageStatus() {
    const sourceTask = this.getTaskResultStorageTask();
    const history = Array.isArray(sourceTask?.execution_history)
      ? sourceTask.execution_history
      : [];
    const latest = history.length > 0 ? history[history.length - 1] : null;
    const validation = latest?.validation_result || latest?.validation || null;
    const label = this.getValidationStatusLabel(validation);
    if (!label) return '';
    return `
      <div class="workspace-task-storage-status">
        <span>${this.escapeHtml(label)}</span>
        ${validation?.contract_version ? `<small>Contract ${this.escapeHtml(validation.contract_version)}</small>` : ''}
      </div>
    `;
  }

  getValidationStatusLabel(validation) {
    const validationStatus = String(validation?.validation_status || '')
      .trim()
      .toLowerCase();
    const storageStatus = String(validation?.storage_status || '')
      .trim()
      .toLowerCase();
    if (!validationStatus || validationStatus === 'not_applicable') return '';
    if (validationStatus === 'dismissed') return 'Dismissed';
    if (validationStatus === 'manually_approved' || storageStatus === 'manually_appended')
      return 'Manually Approved';
    if (validationStatus === 'needs_review' || storageStatus === 'skipped_invalid')
      return 'Needs Review';
    if (
      validationStatus === 'passed' &&
      (storageStatus === 'saved' || storageStatus === 'appended')
    )
      return 'Saved';
    if (validationStatus === 'passed') return 'Validated';
    return validationStatus.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase());
  }

  getNeedsReviewEntries(task = this.getTaskResultStorageTask()) {
    const history = Array.isArray(task?.execution_history) ? task.execution_history : [];
    return history
      .map((entry, index) => ({ entry, index }))
      .filter(({ entry }) => {
        const validation = entry?.validation_result || entry?.validation || null;
        return (
          String(validation?.validation_status || '')
            .trim()
            .toLowerCase() === 'needs_review'
        );
      });
  }

  getReviewContractColumns(task = this.getTaskResultStorageTask(), validation = null) {
    const columns =
      (Array.isArray(validation?.output_spec_snapshot?.contract?.columns)
        ? validation.output_spec_snapshot.contract.columns
        : null) ||
      (Array.isArray(task?.output_spec?.contract?.columns)
        ? task.output_spec.contract.columns
        : null) ||
      (Array.isArray(task?.output_contract?.columns) ? task.output_contract.columns : []);
    return columns
      .map(column => ({
        name: String(column?.name || '').trim(),
        type: String(column?.type || 'string').trim() || 'string',
        required: column?.required !== false,
        description: String(column?.description || '').trim()
      }))
      .filter(column => column.name);
  }

  getCaseInsensitiveValue(row, columnName) {
    if (!row || typeof row !== 'object') return '';
    if (Object.prototype.hasOwnProperty.call(row, columnName)) return row[columnName];
    const target = String(columnName || '').toLowerCase();
    const key = Object.keys(row).find(
      candidate => String(candidate || '').toLowerCase() === target
    );
    return key ? row[key] : '';
  }

  parseReviewDraftRow(rawOutput, columns = []) {
    const raw = String(rawOutput || '').trim();
    const emptyRow = {};
    columns.forEach(column => {
      emptyRow[column.name] = '';
    });
    if (!raw || columns.length === 0) {
      return { row: emptyRow, parsed: false };
    }

    if (/^[{[]/.test(raw)) {
      try {
        const decoded = JSON.parse(raw);
        let candidate = decoded;
        if (Array.isArray(candidate)) {
          candidate = candidate[0] || {};
        } else if (Array.isArray(candidate?.rows)) {
          candidate = candidate.rows[0] || {};
        } else if (Array.isArray(candidate?.data)) {
          candidate = candidate.data[0] || {};
        }
        if (candidate && typeof candidate === 'object' && !Array.isArray(candidate)) {
          const row = {};
          columns.forEach(column => {
            const value = this.getCaseInsensitiveValue(candidate, column.name);
            row[column.name] = value === undefined || value === null ? '' : String(value);
          });
          return { row, parsed: true };
        }
      } catch (_error) {
        // Raw CSV editing remains available below.
      }
    }

    const records = parseDelimitedRecords(raw, ',');
    if (records.length >= 2) {
      const header = records[0].map(value => String(value || '').trim());
      const values = records[1] || [];
      const rowByHeader = {};
      header.forEach((name, index) => {
        if (name) rowByHeader[name] = values[index] ?? '';
      });
      const row = {};
      columns.forEach(column => {
        row[column.name] = this.getCaseInsensitiveValue(rowByHeader, column.name);
      });
      return { row, parsed: true };
    }

    return { row: emptyRow, parsed: false };
  }

  renderReviewTableEditor(rawOutput, columns = [], outputFormat = 'csv') {
    if (!columns.length) return '';
    const { row } = this.parseReviewDraftRow(rawOutput, columns);
    const rawLabel = outputFormat === 'json' ? 'Raw JSON' : 'Raw CSV';
    const headerHtml = columns
      .map(
        column =>
          `<th scope="col">${this.escapeHtml(column.name)}<small>${this.escapeHtml(column.type)}${column.required ? ' required' : ''}</small></th>`
      )
      .join('');
    const rowHtml = columns
      .map(
        column => `
        <td>
          <input
            type="text"
            value="${this.escapeHtml(row[column.name] || '')}"
            data-review-table-input
            data-review-column="${this.escapeHtml(column.name)}"
            aria-label="${this.escapeHtml(column.name)}"
          >
        </td>
      `
      )
      .join('');

    return `
      <div class="workspace-task-review-mode-tabs" role="tablist" aria-label="Review editor mode">
        <button type="button" class="is-active" data-review-view-toggle="table">Edit row</button>
        <button type="button" data-review-view-toggle="raw">${this.escapeHtml(rawLabel)}</button>
      </div>
      <div class="workspace-task-review-table-pane" data-review-table-pane data-review-output-format="${this.escapeHtml(outputFormat)}">
        <div class="workspace-task-review-table-wrap" role="region" aria-label="Editable CSV row" tabindex="0">
          <table>
            <thead><tr>${headerHtml}</tr></thead>
            <tbody><tr>${rowHtml}</tr></tbody>
          </table>
        </div>
      </div>
    `;
  }

  renderNeedsReviewPanel() {
    const sourceTask = this.getTaskResultStorageTask();
    const entries = this.getNeedsReviewEntries(sourceTask);
    if (entries.length === 0) return '';

    const latest = entries[entries.length - 1];
    const validation = latest.entry?.validation_result || latest.entry?.validation || {};
    const errors = Array.isArray(validation.errors) ? validation.errors : [];
    const errorList =
      errors.length > 0
        ? errors
            .map(error => {
              const expected =
                Array.isArray(error?.expected) && error.expected.length
                  ? ` Expected: ${error.expected.join(', ')}.`
                  : '';
              const actual =
                Array.isArray(error?.actual) && error.actual.length
                  ? ` Actual: ${error.actual.join(', ')}.`
                  : '';
              return `<li>${this.escapeHtml(`${error?.message || error?.code || 'Validation failed'}${expected}${actual}`)}</li>`;
            })
            .join('')
        : '<li>Result did not match the output contract.</li>';
    const rawOutput = String(
      latest.entry?.result || latest.entry?.summary || this.task?.result || ''
    ).trim();
    const normalizedRow =
      validation?.normalized_row && typeof validation.normalized_row === 'object'
        ? validation.normalized_row
        : null;
    const reviewDraft = normalizedRow ? JSON.stringify(normalizedRow, null, 2) : rawOutput;
    const sourceTaskId = String(sourceTask?.id || this.taskId || '').trim();
    const outputSpecSnapshot = validation?.output_spec_snapshot || sourceTask?.output_spec || null;
    const contractColumns = this.getReviewContractColumns(sourceTask, validation);
    const reviewColumns = outputSpecSnapshot
      ? this.getOutputSpecSchemaFields(outputSpecSnapshot)
      : contractColumns;
    const contractColumnNames = contractColumns.map(column => column.name);
    const tableEditor = this.renderReviewTableEditor(
      reviewDraft,
      reviewColumns,
      outputSpecSnapshot ? 'json' : 'csv'
    );
    const rawHidden = tableEditor ? ' hidden' : '';
    const currentContractVersion = String(
      sourceTask?.output_spec?.version || sourceTask?.output_contract?.version || ''
    ).trim();
    const runContractVersion = String(validation?.contract_version || '').trim();
    const contractMismatchWarning =
      currentContractVersion && runContractVersion && currentContractVersion !== runContractVersion
        ? `<div class="workspace-task-review-warning">This run used contract ${this.escapeHtml(runContractVersion)}. The task now uses ${this.escapeHtml(currentContractVersion)}, so re-running may be cleaner than approving the old output.</div>`
        : '';
    const headerMismatch = errors.find(
      error => String(error?.code || '') === 'csv_header_mismatch'
    );
    const headerMismatchHtml = headerMismatch
      ? `
      <div class="workspace-task-review-reconcile">
        <div>
          <strong>Destination CSV uses different columns</strong>
          <span>The row is ready, but the existing file header does not match this result format.</span>
        </div>
        <dl>
          <div><dt>Expected</dt><dd>${this.escapeHtml((headerMismatch.expected || []).join(', ') || 'No expected header')}</dd></div>
          <div><dt>Actual</dt><dd>${this.escapeHtml((headerMismatch.actual || []).join(', ') || 'No existing header')}</dd></div>
        </dl>
        <div class="workspace-task-review-reconcile-actions">
          <button type="button" class="modern-btn modern-btn-primary" data-review-action="reproject_to_destination">Reorganize to match</button>
          <button type="button" class="modern-btn modern-btn-secondary" data-action="edit-append-storage">Change destination</button>
          <button type="button" class="modern-btn modern-btn-secondary" data-action="design-output-columns-from-result">Edit format</button>
        </div>
      </div>`
      : '';
    const normalizedPreviewHtml = normalizedRow
      ? `
      <details class="workspace-task-review-normalized" open>
        <summary>Normalized row</summary>
        <pre>${this.escapeHtml(JSON.stringify(normalizedRow, null, 2))}</pre>
      </details>`
      : '';
    const repairStatus = String(validation?.repair_status || '').trim();
    const storageStatus = String(validation?.storage_status || '').trim();

    return `
      <section class="workspace-task-review-panel" data-review-task-id="${this.escapeHtml(sourceTaskId)}" data-review-history-index="${this.escapeHtml(latest.index)}">
        <div class="workspace-task-page-mini-label">Needs Review</div>
        <div class="workspace-task-review-card">
          <div class="workspace-task-review-copy">
            <strong>${this.escapeHtml(entries.length)} run${entries.length === 1 ? '' : 's'} waiting before CSV save.</strong>
            <span>${contractColumnNames.length > 0 ? `Expected CSV columns: ${this.escapeHtml(contractColumnNames.join(', '))}` : 'The result needs to match the saved format before it can be written.'}</span>
          </div>
          <div class="workspace-task-review-status-row">
            ${runContractVersion ? `<span>Contract ${this.escapeHtml(runContractVersion)}</span>` : ''}
            ${storageStatus ? `<span>Storage ${this.escapeHtml(storageStatus.replace(/_/g, ' '))}</span>` : ''}
            ${repairStatus ? `<span>Repair ${this.escapeHtml(repairStatus.replace(/_/g, ' '))}</span>` : ''}
          </div>
          ${contractMismatchWarning}
          ${headerMismatchHtml}
          <ul class="workspace-task-review-errors">${errorList}</ul>
          ${normalizedPreviewHtml}
          ${tableEditor}
          <label class="workspace-task-review-editor" data-review-raw-pane${rawHidden}>
            <span>Edit the row before saving</span>
            <textarea rows="7" data-review-draft>${this.escapeHtml(reviewDraft)}</textarea>
          </label>
          <div class="workspace-task-review-actions">
            <button type="button" class="modern-btn modern-btn-primary" data-review-action="approve_append">Save row</button>
            <button type="button" class="modern-btn modern-btn-secondary" data-review-action="copy">Copy row</button>
            <button type="button" class="modern-btn modern-btn-secondary" data-review-action="retry_normalization">Retry parsing</button>
            <button type="button" class="modern-btn modern-btn-secondary" data-review-action="rerun">Re-run Task</button>
            <button type="button" class="modern-btn modern-btn-secondary" data-review-action="dismiss">Dismiss</button>
          </div>
        </div>
      </section>
    `;
  }

  bindOutputReviewActions() {
    this.elements.output?.querySelectorAll('[data-review-view-toggle]').forEach(button => {
      button.addEventListener('click', () => this.setReviewEditorMode(button));
    });
    this.elements.output?.querySelectorAll('[data-review-table-input]').forEach(input => {
      input.addEventListener('input', () => {
        const panel = input.closest('[data-review-task-id]');
        this.syncReviewRawFromTable(panel);
      });
    });
    this.elements.output?.querySelectorAll('[data-review-action]').forEach(button => {
      button.addEventListener('click', () => this.handleOutputReviewAction(button));
    });
  }

  setReviewEditorMode(button) {
    const panel = button?.closest('[data-review-task-id]');
    if (!panel) return;
    const mode = button.getAttribute('data-review-view-toggle') || 'table';
    const tablePane = panel.querySelector('[data-review-table-pane]');
    const rawPane = panel.querySelector('[data-review-raw-pane]');
    if (mode === 'table') {
      this.syncReviewTableFromRaw(panel);
      if (tablePane) tablePane.hidden = false;
      if (rawPane) rawPane.hidden = true;
    } else {
      this.syncReviewRawFromTable(panel);
      if (tablePane) tablePane.hidden = true;
      if (rawPane) rawPane.hidden = false;
    }
    panel.querySelectorAll('[data-review-view-toggle]').forEach(tab => {
      tab.classList.toggle('is-active', tab === button);
    });
  }

  syncReviewRawFromTable(panel) {
    if (!panel) return '';
    const inputs = Array.from(panel.querySelectorAll('[data-review-table-input]'));
    if (inputs.length === 0) return panel.querySelector('[data-review-draft]')?.value || '';
    const row = {};
    const columns = [];
    inputs.forEach(input => {
      const column = input.getAttribute('data-review-column') || '';
      if (!column) return;
      columns.push(column);
      row[column] = input.value || '';
    });
    const format =
      panel.querySelector('[data-review-table-pane]')?.getAttribute('data-review-output-format') ||
      'csv';
    const csv = format === 'json' ? JSON.stringify(row, null, 2) : rowsToCSV(columns, [row]);
    const textarea = panel.querySelector('[data-review-draft]');
    if (textarea) textarea.value = csv;
    return csv;
  }

  syncReviewTableFromRaw(panel) {
    if (!panel) return false;
    const inputs = Array.from(panel.querySelectorAll('[data-review-table-input]'));
    if (inputs.length === 0) return false;
    const columns = inputs
      .map(input => ({
        name: input.getAttribute('data-review-column') || '',
        type: 'string',
        required: false
      }))
      .filter(column => column.name);
    const textarea = panel.querySelector('[data-review-draft]');
    const { row, parsed } = this.parseReviewDraftRow(textarea?.value || '', columns);
    if (!parsed) return false;
    inputs.forEach(input => {
      const column = input.getAttribute('data-review-column') || '';
      input.value = row[column] || '';
    });
    return true;
  }

  async handleOutputReviewAction(button) {
    const action = button?.getAttribute('data-review-action') || '';
    const panel = button?.closest('[data-review-task-id]');
    const taskId = panel?.getAttribute('data-review-task-id') || this.taskId;
    const historyIndex = Number(panel?.getAttribute('data-review-history-index'));
    if (!taskId || !Number.isFinite(historyIndex)) return;

    if (action === 'copy') {
      if (!panel.querySelector('[data-review-table-pane]')?.hidden) {
        this.syncReviewRawFromTable(panel);
      }
      const draft = panel.querySelector('[data-review-draft]')?.value || '';
      await this.copyToClipboard(draft, 'Raw output copied');
      return;
    }

    if (action === 'rerun') {
      try {
        const response = await fetch(
          `/api/orchestration/tasks/${encodeURIComponent(taskId)}/review`,
          {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              action: 'rerun',
              history_index: historyIndex
            })
          }
        );
        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || 'Failed to re-run task');
        }
        this.notify('success', 'Task re-run started');
        await this.loadData();
      } catch (error) {
        console.error('Failed to re-run task:', error);
        this.notify('error', error?.message || 'Failed to re-run task');
      }
      return;
    }

    if (action === 'retry_normalization') {
      button.disabled = true;
      try {
        const response = await fetch(
          `/api/orchestration/tasks/${encodeURIComponent(taskId)}/review`,
          {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              action: 'retry_normalization',
              history_index: historyIndex
            })
          }
        );
        const payloadText = await response.text();
        let payload = null;
        if (payloadText) {
          try {
            payload = JSON.parse(payloadText);
          } catch (_error) {
            payload = null;
          }
        }
        if (!response.ok) {
          throw new Error(payload?.message || payloadText || 'Failed to retry normalization');
        }
        const updatedTask = payload?.task || null;
        const updatedEntry = Array.isArray(updatedTask?.execution_history)
          ? updatedTask.execution_history[historyIndex]
          : null;
        const status = String(updatedEntry?.validation_result?.validation_status || '')
          .trim()
          .toLowerCase();
        this.notify(
          status === 'needs_review' ? 'warning' : 'success',
          status === 'needs_review'
            ? 'Normalization retried; review is still needed.'
            : 'Normalization retried and stored.'
        );
        await this.loadData();
      } catch (error) {
        console.error('Failed to retry normalization:', error);
        this.notify('error', error?.message || 'Failed to retry normalization');
      } finally {
        button.disabled = false;
      }
      return;
    }

    if (action === 'reproject_to_destination') {
      // One-click recovery for a CSV header mismatch: ask the harness to rebuild
      // the result into the destination file's columns (deterministically for
      // known fields, via the assistant for the rest) and append it.
      button.disabled = true;
      try {
        const response = await fetch(
          `/api/orchestration/tasks/${encodeURIComponent(taskId)}/review`,
          {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              action: 'reproject_to_destination',
              history_index: historyIndex
            })
          }
        );
        const payloadText = await response.text();
        let payload = null;
        if (payloadText) {
          try {
            payload = JSON.parse(payloadText);
          } catch (_error) {
            payload = null;
          }
        }
        if (!response.ok) {
          throw new Error(payload?.message || payloadText || 'Failed to reorganize result');
        }
        const stored = Boolean(payload?.stored);
        this.notify(
          stored ? 'success' : 'warning',
          stored
            ? 'Reorganized to match the destination and saved.'
            : 'Reorganized, but the row still needs review.'
        );
        await this.loadData();
      } catch (error) {
        console.error('Failed to reorganize result:', error);
        this.notify('error', error?.message || 'Failed to reorganize result');
      } finally {
        button.disabled = false;
      }
      return;
    }

    if (!panel.querySelector('[data-review-table-pane]')?.hidden) {
      this.syncReviewRawFromTable(panel);
    }
    const draft = panel.querySelector('[data-review-draft]')?.value || '';
    const label = action === 'dismiss' ? 'Dismiss' : 'Approve append';
    if (action === 'dismiss' && !confirm('Dismiss this review without appending it?')) return;

    button.disabled = true;
    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(taskId)}/review`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            action,
            history_index: historyIndex,
            result: draft
          })
        }
      );
      const payloadText = await response.text();
      let payload = null;
      if (payloadText) {
        try {
          payload = JSON.parse(payloadText);
        } catch (_error) {
          payload = null;
        }
      }
      if (!response.ok) {
        const validationErrors = Array.isArray(payload?.validation_result?.errors)
          ? payload.validation_result.errors
              .map(error => error?.message || error?.code)
              .filter(Boolean)
              .join(' ')
          : '';
        throw new Error(validationErrors || payload?.message || payloadText || `${label} failed`);
      }
      const updatedTask = payload?.task || null;
      const updatedEntry = Array.isArray(updatedTask?.execution_history)
        ? updatedTask.execution_history[historyIndex]
        : null;
      const status = String(updatedEntry?.validation_result?.validation_status || '')
        .trim()
        .toLowerCase();
      this.notify(
        status === 'needs_review' ? 'warning' : 'success',
        action === 'dismiss'
          ? 'Review dismissed'
          : status === 'needs_review'
            ? 'Append blocked; review is still needed.'
            : 'Approved result appended'
      );
      await this.loadData();
    } catch (error) {
      console.error('Failed to resolve output review:', error);
      this.notify('error', error?.message || `${label} failed`);
    } finally {
      button.disabled = false;
    }
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
      this.elements.outputSaveNoteBtn.disabled =
        !canSaveNote || this.resultNoteSaving || Boolean(this.savedResultNote);
      this.elements.outputSaveNoteBtn.classList.toggle('is-saved', Boolean(this.savedResultNote));
    }

    if (this.elements.outputCreateSkillBtn) {
      const canCreateSkill = this.canCreateSkillFromTask();
      const label = this.elements.outputCreateSkillBtn.querySelector('span');
      if (label) {
        label.textContent = this.skillDraftGenerating
          ? 'Drafting...'
          : this.skillDraftSubmitting
            ? 'Creating...'
            : 'Create Skill';
      }
      this.elements.outputCreateSkillBtn.hidden = !canCreateSkill;
      this.elements.outputCreateSkillBtn.disabled =
        !canCreateSkill || this.skillDraftGenerating || this.skillDraftSubmitting;
    }

    if (this.elements.outputPromoteBtn) {
      const canPromote = this.canPromoteResultToWorkflow();
      const label = this.elements.outputPromoteBtn.querySelector('span');
      if (label) {
        label.textContent = this.resultPromotionPending
          ? 'Preparing...'
          : this.resultPromotionSubmitting
            ? 'Creating...'
            : 'Create Workflow Task';
      }
      this.elements.outputPromoteBtn.hidden = !canPromote;
      this.elements.outputPromoteBtn.disabled =
        !canPromote || this.resultPromotionPending || this.resultPromotionSubmitting;
    }
  }

  canPromoteResultToWorkflow(task = this.task) {
    if (!task || typeof task !== 'object') return false;
    if (
      String(task.status || '')
        .trim()
        .toLowerCase() !== 'completed'
    )
      return false;
    if (!normalizeResultText(task.result).trim()) return false;
    if (String(task.error || '').trim()) return false;

    const resultType = String(task.result_type || '')
      .trim()
      .toLowerCase();
    if (resultType === 'task_list') return true;

    const structuredResult = task.structured_result;
    if (
      structuredResult &&
      typeof structuredResult === 'object' &&
      Array.isArray(structuredResult.groups) &&
      structuredResult.groups.some(group => Array.isArray(group?.items) && group.items.length > 0)
    ) {
      return true;
    }

    return /^\s*[-*]\s+\[[ xX]\]\s+.+$/m.test(normalizeResultText(task.result));
  }

  countTaskListItems(taskList) {
    if (!taskList || !Array.isArray(taskList.groups)) return 0;
    return taskList.groups.reduce(
      (count, group) => count + (Array.isArray(group?.items) ? group.items.length : 0),
      0
    );
  }

  formatTaskListGroupPreviewTitle(title, groupIndex) {
    const fallbackTitle = `Group ${groupIndex + 1}`;
    const value = String(title || fallbackTitle).trim() || fallbackTitle;
    const normalized = value.toLowerCase();
    if (normalized === 'tasks' || normalized === 'task list') return value;
    if (/^\d+\.0\.?\s+/.test(value)) return value;
    return `${groupIndex + 1}.0 ${value}`;
  }

  cloneTaskList(taskList) {
    if (!taskList || typeof taskList !== 'object') return null;
    try {
      return JSON.parse(JSON.stringify(taskList));
    } catch (_error) {
      return null;
    }
  }

  async previewResultPromotion() {
    if (this.resultPromotionPending || this.resultPromotionSubmitting || !this.task?.id) return;

    this.resultPromotionPending = true;
    this.updateResultActionButtons(this.getCurrentResultText(), true);

    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.task.id)}/result/preview`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' }
        }
      );
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(
          payload?.error || payload?.message || 'This result is not a task list yet.'
        );
      }

      const taskList = payload?.task_list;
      if (!taskList || this.countTaskListItems(taskList) < 1) {
        throw new Error('This result does not include subtasks to create.');
      }

      this.resultPromotionDraft = this.cloneTaskList(taskList);
      this.renderResultPromotionPreview(this.resultPromotionDraft);
      this.openResultPromotionModal();
    } catch (error) {
      console.error('Failed to preview result promotion:', error);
      this.notify('error', error?.message || 'Failed to prepare workflow task');
    } finally {
      this.resultPromotionPending = false;
      this.updateResultActionButtons(this.getCurrentResultText(), true);
    }
  }

  openResultPromotionModal() {
    if (!this.elements.resultPromoteModal || typeof bootstrap === 'undefined') return;
    const modal =
      typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.resultPromoteModal)
        : bootstrap.Modal.getInstance(this.elements.resultPromoteModal) ||
          new bootstrap.Modal(this.elements.resultPromoteModal);
    modal.show();
  }

  setResultPromotionSubmitState(isSubmitting) {
    this.resultPromotionSubmitting = Boolean(isSubmitting);
    if (this.elements.resultPromoteSubmitBtn) {
      this.elements.resultPromoteSubmitBtn.disabled = this.resultPromotionSubmitting;
      this.elements.resultPromoteSubmitBtn.textContent = this.resultPromotionSubmitting
        ? 'Creating...'
        : 'Create';
    }
    this.updateResultActionButtons(this.getCurrentResultText(), true);
  }

  renderResultPromotionPreview(taskList) {
    if (
      !taskList ||
      !this.elements.resultPromoteTitleInput ||
      !this.elements.resultPromoteMeta ||
      !this.elements.resultPromoteGroups
    ) {
      return;
    }

    const itemCount = this.countTaskListItems(taskList);
    this.elements.resultPromoteTitleInput.value = String(
      taskList.parent_title || this.getTaskDisplayLabel() || 'Workflow task'
    ).trim();
    this.elements.resultPromoteMeta.textContent = `${itemCount} subtask${itemCount === 1 ? '' : 's'}`;

    const groups = Array.isArray(taskList.groups) ? taskList.groups : [];
    this.elements.resultPromoteGroups.innerHTML = groups
      .map((group, groupIndex) => {
        const title = String(group?.title || `Group ${groupIndex + 1}`).trim();
        const displayTitle = this.formatTaskListGroupPreviewTitle(title, groupIndex);
        const items = Array.isArray(group?.items) ? group.items : [];
        const itemMarkup = items
          .map((item, itemIndex) => {
            const itemTitle = String(item?.title || '').trim();
            const assignee = String(item?.assignee || '').trim();
            return `
          <label class="workspace-task-result-promote-item">
            <span class="workspace-task-result-promote-index">${groupIndex + 1}.${itemIndex + 1}</span>
            <input type="text"
                   class="form-control form-control-sm workspace-task-result-promote-input"
                   data-group-index="${groupIndex}"
                   data-item-index="${itemIndex}"
                   value="${this.escapeHtml(itemTitle)}">
            ${assignee ? `<span class="workspace-task-result-promote-assignee">@${this.escapeHtml(assignee)}</span>` : ''}
          </label>
        `;
          })
          .join('');

        return `
        <section class="workspace-task-result-promote-group">
          <div class="workspace-task-result-promote-group-title">${this.escapeHtml(displayTitle)}</div>
          <div class="workspace-task-result-promote-items">${itemMarkup}</div>
        </section>
      `;
      })
      .join('');
  }

  collectResultPromotionDraft() {
    const draft = this.cloneTaskList(this.resultPromotionDraft);
    if (!draft) return null;

    draft.parent_title = String(this.elements.resultPromoteTitleInput?.value || '').trim();
    this.elements.resultPromoteGroups
      ?.querySelectorAll('[data-group-index][data-item-index]')
      .forEach(input => {
        const groupIndex = Number(input.getAttribute('data-group-index'));
        const itemIndex = Number(input.getAttribute('data-item-index'));
        if (!Number.isInteger(groupIndex) || !Number.isInteger(itemIndex)) return;
        const item = draft.groups?.[groupIndex]?.items?.[itemIndex];
        if (!item) return;
        item.title = String(input.value || '').trim();
      });

    return draft;
  }

  validateResultPromotionDraft(taskList) {
    if (!taskList || typeof taskList !== 'object') return 'Task list preview is unavailable.';
    if (!String(taskList.parent_title || '').trim()) return 'Parent task title is required.';
    if (this.countTaskListItems(taskList) < 1) return 'At least one subtask is required.';

    const groups = Array.isArray(taskList.groups) ? taskList.groups : [];
    for (const group of groups) {
      const items = Array.isArray(group?.items) ? group.items : [];
      for (const item of items) {
        if (!String(item?.title || '').trim()) return 'Every subtask needs a title.';
      }
    }

    return '';
  }

  async submitResultPromotion() {
    if (this.resultPromotionSubmitting || !this.task?.id) return;

    const taskList = this.collectResultPromotionDraft();
    const validationError = this.validateResultPromotionDraft(taskList);
    if (validationError) {
      this.notify('error', validationError);
      return;
    }

    this.setResultPromotionSubmitState(true);

    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.task.id)}/promote-result`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ task_list: taskList })
        }
      );
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.error || payload?.message || 'Failed to create workflow task');
      }

      const subtaskCount = this.countTaskListItems(taskList);
      this.notify(
        'success',
        `Created workflow task with ${subtaskCount} subtask${subtaskCount === 1 ? '' : 's'}`
      );

      if (this.elements.resultPromoteModal && typeof bootstrap !== 'undefined') {
        bootstrap.Modal.getInstance(this.elements.resultPromoteModal)?.hide();
      }

      const parentTaskId = String(payload?.parent_task?.id || '').trim();
      if (parentTaskId) {
        window.location.href = this.getTaskHref(parentTaskId);
        return;
      }

      await this.refreshAfterStepChange();
    } catch (error) {
      console.error('Failed to promote task result:', error);
      this.notify('error', error?.message || 'Failed to create workflow task');
    } finally {
      this.setResultPromotionSubmitState(false);
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

  buildResultNoteTitle(resultText) {
    const heading = String(resultText || '')
      .split(/\r?\n/)
      .map(line => {
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
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: title, content })
        }
      );

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
    if (
      modalEl &&
      window.sessionManager &&
      typeof window.sessionManager.openNoteEditor === 'function'
    ) {
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
      const dependsOn = Array.isArray(step?.depends_on)
        ? step.depends_on.map(value => String(value || '').trim()).filter(Boolean)
        : [];
      const inputTaskIds = [];

      dependsOn.forEach(stepId => {
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
        description:
          trimResultWorkflowLabel(step?.title || step?.description || `Step ${index + 1}`, 180) ||
          `Step ${index + 1}`,
        details: detailParts.join('\n'),
        assignmentValue: this.buildAssignmentValueForAgentName(
          step?.agent_name,
          fallbackAssignmentValue
        ),
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
      detailParts.push(
        `${steps.length - RESULT_WORKFLOW_MAX_SUBTASKS} additional parsed step${steps.length - RESULT_WORKFLOW_MAX_SUBTASKS === 1 ? '' : 's'} were omitted from the draft.`
      );
    }

    return {
      title: buildResultWorkflowDraftTitle(parsed?.title || this.getTaskDisplayLabel()),
      details: detailParts.filter(Boolean).join('\n'),
      priority: Number.isInteger(parsed?.priority) ? parsed.priority : 3,
      assignmentValue: this.buildAssignmentValueForAgentName(
        parsed?.agent_name,
        fallbackAssignmentValue
      ),
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
      const autoParsedDraft = await this.buildWorkflowDraftFromAutoParse(
        resultText,
        fallbackAssignmentValue
      );
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

      // Atomic attach: post the whole batch of generated steps under the
      // existing parent task in a single call. The server validates the
      // graph as one batch and rolls back the entire draft on any
      // validation error, so the user never ends up with a half-created
      // step list to manually clean up.
      const subtaskPayloads = draft.subtasks.map((subtask, index) => {
        const description = String(subtask?.description || '').trim() || `Step ${index + 1}`;
        const details = String(subtask?.details || '').trim();
        const agentFromDraft = this.agentNameFromAssignmentValue(subtask?.assignmentValue);
        const to =
          agentFromDraft || (fallbackAgent && fallbackAgent !== 'unassigned' ? fallbackAgent : '');
        return {
          id: this._generateClientTaskId(),
          description,
          details,
          to,
          subtask_index: index + 1,
          priority: 3
        };
      });

      const response = await fetch('/api/orchestration/workflows', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          attach_to_parent_id: parentTaskId,
          subtasks: subtaskPayloads
        })
      });

      if (!response.ok) {
        throw await this._parseGraphError(response, 'Failed to generate steps');
      }

      const createdCount = subtaskPayloads.length;
      this.notify(
        'success',
        `Added ${createdCount} step${createdCount === 1 ? '' : 's'} to this task.`
      );
      await this.refreshAfterStepChange();
    } catch (error) {
      console.error('Failed to generate steps from result:', error);
      const summary =
        Array.isArray(error?.issues) && error.issues.length > 0
          ? error.issues[0]?.message || error.message
          : error?.message;
      this.notify('error', summary || 'Failed to generate steps');
    } finally {
      this.workflowDraftPending = false;
      this.renderWorkflow();
    }
  }

  /**
   * Generate a UUID for client-side task IDs so the workflow endpoint can
   * receive a fully-formed batch with sibling input refs already wired up.
   */
  _generateClientTaskId() {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
      return crypto.randomUUID();
    }
    const part = () =>
      Math.floor(Math.random() * 0xffffffff)
        .toString(16)
        .padStart(8, '0');
    return `${part()}-${part().slice(0, 4)}-${part().slice(0, 4)}-${part().slice(0, 4)}-${part()}${part().slice(0, 4)}`;
  }

  /**
   * Parse a non-2xx response from the task API and surface structured
   * graph-validation issues via error.issues when present.
   */
  async _parseGraphError(response, fallbackMessage) {
    let body = '';
    try {
      body = await response.text();
    } catch (_e) {
      body = '';
    }
    let parsed = null;
    if (body) {
      try {
        parsed = JSON.parse(body);
      } catch (_e) {
        parsed = null;
      }
    }
    const message = (parsed && (parsed.error || parsed.message)) || body || fallbackMessage;
    const err = new Error(message || fallbackMessage);
    if (parsed && Array.isArray(parsed.issues)) {
      err.issues = parsed.issues;
    }
    return err;
  }

  openScheduleModal() {
    if (!this.elements.scheduleModal || typeof bootstrap === 'undefined') {
      this.notify('error', 'Schedule editor is not available.');
      return;
    }

    this.populateScheduleModal();
    const modal =
      typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.scheduleModal)
        : bootstrap.Modal.getInstance(this.elements.scheduleModal) ||
          new bootstrap.Modal(this.elements.scheduleModal);
    modal.show();
  }

  populateScheduleModal() {
    const schedule = this.task?.schedule || null;
    const hasSchedule = Boolean(schedule);
    const scheduleType = this.inferScheduleFormType(schedule);
    const weekdayCron = this.parseWeekdayCron(schedule?.cron_expr || '');

    this.setScheduleError('');

    if (this.elements.scheduleModalHeading) {
      this.elements.scheduleModalHeading.textContent = hasSchedule
        ? 'Edit Repeat Schedule'
        : 'Repeat Task';
    }
    if (this.elements.scheduleModalMeta) {
      const taskLabel = summarizeText(this.getTaskDisplayLabel(), 90);
      this.elements.scheduleModalMeta.textContent = `Runs "${taskLabel}" again on a schedule. The task keeps its history and latest output updates after each run.`;
    }
    if (this.elements.scheduleEnabledInput) {
      this.elements.scheduleEnabledInput.checked = hasSchedule
        ? Boolean(this.task?.schedule_enabled)
        : true;
    }
    if (this.elements.scheduleNameInput) {
      this.elements.scheduleNameInput.value = this.task?.schedule_name || '';
    }
    if (this.elements.scheduleTypeInput) {
      this.elements.scheduleTypeInput.value = scheduleType;
    }
    if (this.elements.scheduleTimeInput) {
      this.elements.scheduleTimeInput.value =
        weekdayCron?.time || schedule?.time || schedule?.time_of_day || '09:00';
    }
    if (this.elements.scheduleDayInput) {
      const day = Number(schedule?.day_of_week);
      this.elements.scheduleDayInput.value =
        Number.isInteger(day) && day >= 0 && day <= 6 ? String(day) : '1';
    }
    this.populateScheduleIntervalFields(this.getScheduleIntervalMinutes(schedule) || 60);
    if (this.elements.scheduleOnceInput) {
      this.elements.scheduleOnceInput.value = this.formatLocalDatetimeInput(
        schedule?.run_at || schedule?.execute_at || ''
      );
    }
    if (this.elements.scheduleCronInput) {
      this.elements.scheduleCronInput.value = schedule?.cron_expr || '0 9 * * *';
    }
    if (this.elements.scheduleSleepPolicyInput) {
      this.elements.scheduleSleepPolicyInput.value = this.task?.sleep_policy || 'run_once_on_wake';
    }
    if (this.elements.scheduleWakeMacInput) {
      this.elements.scheduleWakeMacInput.checked = Boolean(this.task?.wake_mac_enabled);
    }
    if (this.elements.scheduleWakeLeadInput) {
      this.elements.scheduleWakeLeadInput.value = String(this.task?.wake_lead_minutes || 5);
    }
    if (this.elements.scheduleWakeFallbackInput) {
      this.elements.scheduleWakeFallbackInput.value =
        this.task?.wake_fallback_policy || 'run_on_next_wake';
    }
    if (this.elements.scheduleRemoveBtn) {
      this.elements.scheduleRemoveBtn.hidden = !hasSchedule;
    }

    this.updateScheduleModalFields();
  }

  updateScheduleModalFields() {
    const type = this.elements.scheduleTypeInput?.value || 'daily';
    const showTime = type === 'daily' || type === 'weekdays' || type === 'weekly';

    if (this.elements.scheduleTimeField) this.elements.scheduleTimeField.hidden = !showTime;
    if (this.elements.scheduleDayField) this.elements.scheduleDayField.hidden = type !== 'weekly';
    if (this.elements.scheduleIntervalField)
      this.elements.scheduleIntervalField.hidden = type !== 'interval';
    if (this.elements.scheduleOnceField) this.elements.scheduleOnceField.hidden = type !== 'once';
    if (this.elements.scheduleCronField) this.elements.scheduleCronField.hidden = type !== 'cron';
    if (this.elements.scheduleTimeLabel) {
      this.elements.scheduleTimeLabel.textContent =
        type === 'weekdays' ? 'Weekday time' : 'Time of day';
    }
    if (this.elements.scheduleWakeFields) {
      this.elements.scheduleWakeFields.hidden = !this.elements.scheduleWakeMacInput?.checked;
    }
    if (this.elements.scheduleWakePermission) {
      this.elements.scheduleWakePermission.textContent = this.elements.scheduleWakeMacInput?.checked
        ? 'Mac wake scheduling also needs to be enabled in Settings -> Device Capabilities.'
        : 'This task will only run while Ori is awake, or according to the selected sleep handling policy after Ori wakes.';
    }

    this.updateSchedulePreview();
  }

  updateSchedulePreview() {
    if (!this.elements.schedulePreview) return;

    try {
      const payload = this.buildScheduleUpdatePayload({ validate: false });
      const summary = this.describeSchedule(payload.schedule);
      const sleepText = this.describeSleepPolicy(payload.sleep_policy);
      const wakeText = payload.wake_mac_enabled
        ? ` Ori will ask macOS to wake this Mac ${payload.wake_lead_minutes} minutes before the run.`
        : '';
      this.elements.schedulePreview.textContent = payload.schedule_enabled
        ? `This existing task will run ${summary}. ${sleepText}.${wakeText}`.trim()
        : `This schedule is paused. Ori will keep "${summary}" saved, but it will not run again until re-enabled.`;
    } catch (_error) {
      this.elements.schedulePreview.textContent =
        'Complete the schedule fields to preview the run cadence.';
    }
  }

  buildScheduleUpdatePayload({ validate = true } = {}) {
    const type = this.elements.scheduleTypeInput?.value || 'daily';
    const enabled = Boolean(this.elements.scheduleEnabledInput?.checked);
    const scheduleName = String(this.elements.scheduleNameInput?.value || '').trim();
    const schedule = { type };
    const wakeMacEnabled = Boolean(this.elements.scheduleWakeMacInput?.checked);
    const sleepPolicy = String(
      this.elements.scheduleSleepPolicyInput?.value || 'run_once_on_wake'
    ).trim();
    const wakeFallbackPolicy = String(
      this.elements.scheduleWakeFallbackInput?.value || 'run_on_next_wake'
    ).trim();
    const rawWakeLead = Number.parseInt(this.elements.scheduleWakeLeadInput?.value || '5', 10);
    const wakeLeadMinutes =
      Number.isFinite(rawWakeLead) && rawWakeLead > 0 ? Math.min(rawWakeLead, 120) : 5;

    const assignee = String(this.task?.to || '').trim();
    if (enabled && validate && (!assignee || assignee === 'unassigned')) {
      throw new Error('Assign this task to an agent before enabling a schedule.');
    }

    const requireTime = () => {
      const value = String(this.elements.scheduleTimeInput?.value || '').trim();
      if (validate && !value) throw new Error('Choose a time for this schedule.');
      return value || '09:00';
    };

    switch (type) {
      case 'daily':
        schedule.time = requireTime();
        break;
      case 'weekdays':
        schedule.type = 'cron';
        schedule.cron_expr = this.buildWeekdayCron(requireTime());
        break;
      case 'weekly':
        schedule.time = requireTime();
        schedule.day_of_week = Number.parseInt(this.elements.scheduleDayInput?.value || '1', 10);
        if (
          validate &&
          (Number.isNaN(schedule.day_of_week) ||
            schedule.day_of_week < 0 ||
            schedule.day_of_week > 6)
        ) {
          throw new Error('Choose a valid weekday.');
        }
        break;
      case 'interval': {
        const rawValue = Number.parseInt(
          this.elements.scheduleIntervalValueInput?.value || '1',
          10
        );
        if (validate && (!Number.isFinite(rawValue) || rawValue < 1)) {
          throw new Error('Interval must be at least 1.');
        }
        const value = Number.isFinite(rawValue) && rawValue > 0 ? rawValue : 1;
        const unit = this.elements.scheduleIntervalUnitInput?.value || 'hours';
        let minutes = value;
        if (unit === 'hours') minutes = value * 60;
        if (unit === 'days') minutes = value * 1440;
        schedule.interval_minutes = minutes;
        break;
      }
      case 'once': {
        const runAt = String(this.elements.scheduleOnceInput?.value || '').trim();
        if (validate && !runAt) {
          throw new Error('Choose when this task should run.');
        }
        schedule.run_at = runAt;
        break;
      }
      case 'cron': {
        const cronExpr = String(this.elements.scheduleCronInput?.value || '')
          .replace(/\s+/g, ' ')
          .trim();
        if (validate && cronExpr.split(' ').filter(Boolean).length !== 5) {
          throw new Error('Cron schedules must use 5 fields: minute hour day month weekday.');
        }
        schedule.cron_expr = cronExpr || '0 9 * * *';
        break;
      }
      default:
        throw new Error('Choose a valid repeat option.');
    }

    return {
      schedule,
      schedule_enabled: enabled,
      schedule_name: scheduleName,
      sleep_policy: sleepPolicy || 'run_once_on_wake',
      wake_mac_enabled: enabled && wakeMacEnabled,
      wake_lead_minutes: wakeLeadMinutes,
      wake_fallback_policy: wakeFallbackPolicy || 'run_on_next_wake'
    };
  }

  async saveSchedule() {
    if (this.scheduleSubmitting) return;

    let payload;
    try {
      payload = this.buildScheduleUpdatePayload({ validate: true });
      this.setScheduleError('');
    } catch (error) {
      this.setScheduleError(error?.message || 'Check the schedule fields and try again.');
      return;
    }

    this.scheduleSubmitting = true;
    const submitBtn = this.elements.scheduleSubmitBtn;
    const submitText = submitBtn?.querySelector('span');
    const originalText = submitText?.textContent || 'Save Schedule';
    if (submitBtn) submitBtn.disabled = true;
    if (submitText) submitText.textContent = 'Saving...';

    try {
      // deferRender lets the modal close first; the page-wide re-render —
      // which includes markdown / trace / schedule sub-renders that can take
      // 100–300ms on a task with history — happens in the next frame so the
      // "Saving..." spinner doesn't stay pinned to the screen waiting on it.
      await this.updateTaskFields(payload, { deferRender: true });
      if (this.elements.scheduleModal && typeof bootstrap !== 'undefined') {
        bootstrap.Modal.getInstance(this.elements.scheduleModal)?.hide();
      }
      this.notify('success', payload.schedule_enabled ? 'Schedule saved' : 'Schedule paused');
    } catch (error) {
      console.error('Failed to save schedule:', error);
      this.setScheduleError(error?.message || 'Failed to save schedule.');
    } finally {
      this.scheduleSubmitting = false;
      if (submitBtn) submitBtn.disabled = false;
      if (submitText) submitText.textContent = originalText;
    }
  }

  async removeSchedule() {
    if (this.scheduleSubmitting || !this.task?.schedule) return;
    if (!window.confirm('Remove this recurring schedule from the task?')) return;

    this.scheduleSubmitting = true;
    const removeBtn = this.elements.scheduleRemoveBtn;
    if (removeBtn) removeBtn.disabled = true;

    try {
      await this.updateTaskFields(
        {
          schedule: null,
          schedule_enabled: false,
          schedule_name: ''
        },
        { deferRender: true }
      );
      if (this.elements.scheduleModal && typeof bootstrap !== 'undefined') {
        bootstrap.Modal.getInstance(this.elements.scheduleModal)?.hide();
      }
      this.notify('success', 'Schedule removed');
    } catch (error) {
      console.error('Failed to remove schedule:', error);
      this.setScheduleError(error?.message || 'Failed to remove schedule.');
    } finally {
      this.scheduleSubmitting = false;
      if (removeBtn) removeBtn.disabled = false;
    }
  }

  setScheduleError(message = '') {
    if (!this.elements.scheduleError) return;
    const normalized = String(message || '').trim();
    this.elements.scheduleError.textContent = normalized;
    this.elements.scheduleError.hidden = !normalized;
  }

  inferScheduleFormType(schedule) {
    const type = String(schedule?.type || '')
      .trim()
      .toLowerCase();
    if (type === 'cron' && this.parseWeekdayCron(schedule?.cron_expr || '')) return 'weekdays';
    if (['daily', 'weekly', 'interval', 'once', 'cron'].includes(type)) return type;
    return 'daily';
  }

  parseWeekdayCron(cronExpr) {
    const match = String(cronExpr || '')
      .trim()
      .match(/^(\d{1,2})\s+(\d{1,2})\s+\*\s+\*\s+(?:1-5|mon-fri)$/i);
    if (!match) return null;

    const minute = Number.parseInt(match[1], 10);
    const hour = Number.parseInt(match[2], 10);
    if (
      !Number.isInteger(hour) ||
      !Number.isInteger(minute) ||
      hour < 0 ||
      hour > 23 ||
      minute < 0 ||
      minute > 59
    ) {
      return null;
    }

    return {
      hour,
      minute,
      time: `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
    };
  }

  buildWeekdayCron(timeValue) {
    const [hourPart, minutePart] = String(timeValue || '09:00').split(':');
    const hour = Number.parseInt(hourPart, 10);
    const minute = Number.parseInt(minutePart, 10);
    return `${Number.isInteger(minute) ? minute : 0} ${Number.isInteger(hour) ? hour : 9} * * 1-5`;
  }

  getScheduleIntervalMinutes(schedule) {
    if (!schedule) return 0;
    const direct = Number(schedule.interval_minutes);
    if (Number.isFinite(direct) && direct > 0) return Math.round(direct);

    const interval = schedule.interval;
    if (typeof interval === 'number' && Number.isFinite(interval) && interval > 0) {
      return interval > 1000000
        ? Math.max(1, Math.round(interval / 60000000000))
        : Math.round(interval);
    }
    if (typeof interval === 'string') {
      const numeric = Number.parseFloat(interval);
      if (Number.isFinite(numeric) && numeric > 0) {
        return numeric > 1000000
          ? Math.max(1, Math.round(numeric / 60000000000))
          : Math.round(numeric);
      }
    }
    return 0;
  }

  populateScheduleIntervalFields(minutes) {
    const normalized = Number.isFinite(minutes) && minutes > 0 ? minutes : 60;
    let value = normalized;
    let unit = 'minutes';

    if (normalized >= 1440 && normalized % 1440 === 0) {
      value = normalized / 1440;
      unit = 'days';
    } else if (normalized >= 60 && normalized % 60 === 0) {
      value = normalized / 60;
      unit = 'hours';
    }

    if (this.elements.scheduleIntervalValueInput) {
      this.elements.scheduleIntervalValueInput.value = String(value);
    }
    if (this.elements.scheduleIntervalUnitInput) {
      this.elements.scheduleIntervalUnitInput.value = unit;
    }
  }

  formatLocalDatetimeInput(value) {
    if (!value) return '';

    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';

    const pad = number => String(number).padStart(2, '0');
    return (
      [date.getFullYear(), pad(date.getMonth() + 1), pad(date.getDate())].join('-') +
      `T${pad(date.getHours())}:${pad(date.getMinutes())}`
    );
  }

  formatTimeOfDay(value) {
    const match = String(value || '').match(/^(\d{1,2}):(\d{2})/);
    if (!match) return String(value || '9:00 AM');

    const date = new Date();
    date.setHours(Number.parseInt(match[1], 10), Number.parseInt(match[2], 10), 0, 0);
    return date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  }

  formatDayOfWeek(value) {
    const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
    const index = Number.parseInt(value, 10);
    return days[index] || 'Monday';
  }

  describeSchedule(schedule) {
    const type = String(schedule?.type || '')
      .trim()
      .toLowerCase();
    switch (type) {
      case 'daily':
        return `daily at ${this.formatTimeOfDay(schedule?.time || schedule?.time_of_day || '09:00')}`;
      case 'weekly':
        return `weekly on ${this.formatDayOfWeek(schedule?.day_of_week)} at ${this.formatTimeOfDay(schedule?.time || schedule?.time_of_day || '09:00')}`;
      case 'interval':
        return `every ${this.formatIntervalMinutes(this.getScheduleIntervalMinutes(schedule) || Number(schedule?.interval_minutes) || 60)}`;
      case 'once':
        return `once at ${formatDateTime(schedule?.run_at || schedule?.execute_at)}`;
      case 'cron': {
        const weekday = this.parseWeekdayCron(schedule?.cron_expr || '');
        if (weekday) return `weekdays at ${this.formatTimeOfDay(weekday.time)}`;
        return `on cron "${schedule?.cron_expr || '0 9 * * *'}"`;
      }
      default:
        return 'on a saved schedule';
    }
  }

  describeSleepPolicy(policy) {
    const normalized = String(policy || '')
      .trim()
      .toLowerCase();
    switch (normalized) {
      case 'skip':
        return 'If Ori was asleep, missed runs will be skipped';
      case 'run_once_on_wake':
      default:
        return 'If Ori was asleep, one missed run will be queued when Ori wakes';
    }
  }

  formatIntervalMinutes(minutes) {
    const normalized = Math.max(1, Number.parseInt(minutes, 10) || 1);
    if (normalized >= 1440 && normalized % 1440 === 0) {
      const days = normalized / 1440;
      return `${days} day${days === 1 ? '' : 's'}`;
    }
    if (normalized >= 60 && normalized % 60 === 0) {
      const hours = normalized / 60;
      return `${hours} hour${hours === 1 ? '' : 's'}`;
    }
    return `${normalized} minute${normalized === 1 ? '' : 's'}`;
  }

  renderSchedule() {
    if (!this.elements.schedule || !this.elements.scheduleCard) return;

    const hasSchedule = Boolean(this.task?.schedule);
    const scheduleEnabled = hasSchedule && Boolean(this.task?.schedule_enabled);
    const history = Array.isArray(this.task?.execution_history) ? this.task.execution_history : [];
    const executionCount = Number(this.task?.execution_count) || 0;
    const failureCount = Number(this.task?.failure_count) || 0;
    // The card is now "Automation & output", not just Schedule, so an output
    // contract, configured storage destination, or a declared output shape is
    // enough to surface it even when the task has never run on a cadence.
    const storageOwner = this.getTaskResultStorageTask();
    const hasContract =
      Array.isArray(storageOwner?.output_contract?.columns) &&
      storageOwner.output_contract.columns.length > 0;
    const hasStorage = Boolean(storageOwner?.result_storage?.enabled);
    const editingColumns = Array.isArray(this.resultContractDraft);
    const hasStructuredOutput = this.hasStructuredOutputShape();

    if (
      !hasSchedule &&
      history.length === 0 &&
      executionCount === 0 &&
      !hasContract &&
      !hasStorage &&
      !editingColumns &&
      !hasStructuredOutput
    ) {
      this.elements.scheduleCard.hidden = true;
      this.elements.schedule.innerHTML = '';
      this.renderAutomationSections();
      return;
    }

    this.elements.scheduleCard.hidden = false;

    // Surface the "Open output folder" action in the card header whenever the
    // task writes to the workspace's default output folder. Store-node and
    // explicit-path destinations are skipped because openWorkspaceOutputDir
    // only reveals the default folder; for those the buried per-destination
    // control in Advanced > storage remains the right entry point.
    if (this.elements.scheduleOpenFolderBtn) {
      const storage = storageOwner?.result_storage || null;
      const usesDefaultOutputDir =
        Boolean(storage?.enabled) &&
        !String(storage.store_node_id || '').trim() &&
        String(storage.storage_target || '').trim() !== 'workspace_folder' &&
        !String(storage.file_path || '').trim();
      this.elements.scheduleOpenFolderBtn.hidden = !usesDefaultOutputDir;
      const labelSpan = this.elements.scheduleOpenFolderBtn.querySelector('span');
      if (labelSpan) labelSpan.textContent = this.fileManagerActionLabel();
    }

    const stats = [];
    stats.push({ label: 'Total Runs', value: String(executionCount) });
    stats.push({ label: 'Failures', value: String(failureCount) });
    if (this.task?.next_run) {
      stats.push({ label: 'Next Run', value: formatDateTime(this.task.next_run) });
    }
    if (this.task?.last_run) {
      stats.push({ label: 'Last Run', value: formatDateTime(this.task.last_run) });
    }
    if (this.task?.wake_mac_enabled) {
      stats.push({ label: 'Mac Wake', value: `${this.task?.wake_lead_minutes || 5} min before` });
    }

    // Only show the run stats once there's schedule or run activity. A task
    // that only surfaces this card because it declares an output shape (or a
    // storage destination) hasn't run on a cadence, so a "Total Runs: 0 /
    // Failures: 0" block would just be noise.
    const showStats = hasSchedule || executionCount > 0 || failureCount > 0 || history.length > 0;
    const statsHtml = showStats
      ? `
      <div class="workspace-task-schedule-stats">
        ${stats
          .map(
            s => `
          <div class="workspace-task-schedule-stat">
            <div class="workspace-task-schedule-stat-label">${this.escapeHtml(s.label)}</div>
            <div class="workspace-task-schedule-stat-value">${this.escapeHtml(s.value)}</div>
          </div>
        `
          )
          .join('')}
      </div>
    `
      : '';

    const bannerHtml = hasSchedule
      ? `
      <div class="workspace-task-schedule-banner" data-state="${scheduleEnabled ? 'enabled' : 'paused'}">
        <div class="workspace-task-schedule-banner-icon">
          <i class="bi ${scheduleEnabled ? 'bi-calendar-check' : 'bi-pause-circle'}" aria-hidden="true"></i>
        </div>
        <div>
          <div class="workspace-task-schedule-banner-title">${this.escapeHtml(scheduleEnabled ? 'Scheduled' : 'Schedule paused')}</div>
          <div class="workspace-task-schedule-banner-copy">
            ${this.escapeHtml(this.describeSchedule(this.task.schedule))}
            ${scheduleEnabled && this.task?.next_run ? ` · Next run ${this.escapeHtml(formatDateTime(this.task.next_run))}` : ''}
            ${this.task?.wake_mac_enabled ? ` · ${this.escapeHtml('Mac wake on')}` : ''}
          </div>
        </div>
      </div>
    `
      : '';

    // Recent runs: a compact history list collapsed by default so it doesn't
    // add density to the Overview. The full friendly history also lives in the
    // Activity > Runs tab; this is a quick in-context peek.
    let historyHtml = '';
    if (history.length > 0) {
      const recentRuns = history.slice(-10).reverse();
      historyHtml = `
        <details class="workspace-task-advanced workspace-task-schedule-history-disclosure">
          <summary class="workspace-task-advanced-summary">Recent runs</summary>
          <div class="workspace-task-schedule-history">
            ${recentRuns
              .map((run, idx) => {
                const runStatus = String(run?.status || 'completed')
                  .trim()
                  .toLowerCase();
                const statusClass = getStatusClass(runStatus);
                // TaskExecution carries executed_at; completed_at/started_at are
                // legacy fallbacks for any older history shape that may still be
                // sitting in the workspace store.
                const ts = run?.executed_at || run?.completed_at || run?.started_at;
                const durationMs = Number(run?.duration) || 0;
                const durationLabel =
                  durationMs > 0
                    ? durationMs >= 1000
                      ? `${(durationMs / 1000).toFixed(1)}s`
                      : `${durationMs}ms`
                    : '';
                const summary = String(run?.summary || run?.error || '').trim();
                // Result is the full body (capped server-side at 16 KiB). Older
                // history rows recorded before that field existed only have
                // summary; treat that as a "no full result available" case.
                const fullResult = String(run?.result || '').trim();
                const hasExpandable = fullResult && fullResult !== summary;
                const panelId = `workspace-task-schedule-run-panel-${idx}`;
                return `
                <div class="workspace-task-schedule-run">
                  <div class="workspace-task-schedule-run-row">
                    <span class="workspace-task-schedule-run-meta">${this.escapeHtml(formatDateTime(ts))}${durationLabel ? ` <span class="workspace-task-schedule-run-duration">· ${this.escapeHtml(durationLabel)}</span>` : ''}${summary ? `<div class="workspace-task-schedule-run-summary" title="${this.escapeHtml(summary)}">${this.escapeHtml(summary)}</div>` : ''}</span>
                    <div class="workspace-task-schedule-run-trail">
                      <span class="workspace-task-schedule-run-status" data-state="${this.escapeHtml(statusClass)}">${this.escapeHtml(getDisplayStatus(runStatus))}</span>
                      ${
                        hasExpandable
                          ? `<button type="button" class="workspace-task-schedule-run-toggle" data-task-run-toggle aria-expanded="false" aria-controls="${panelId}" title="Show full result">
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M7,10L12,15L17,10H7Z"/></svg>
                        <span>Result</span>
                      </button>`
                          : ''
                      }
                    </div>
                  </div>
                  ${hasExpandable ? `<div id="${panelId}" class="workspace-task-schedule-run-panel" hidden><pre class="workspace-task-schedule-run-result">${this.escapeHtml(fullResult)}</pre></div>` : ''}
                </div>
              `;
              })
              .join('')}
          </div>
        </details>
      `;
    }

    this.elements.schedule.innerHTML = bannerHtml + statsHtml + historyHtml;
    this.renderAutomationSections();

    // Wire expand/collapse for run rows that captured a full result. The
    // chevron flips and the <pre> below toggles between hidden and visible.
    this.elements.schedule.querySelectorAll('[data-task-run-toggle]').forEach(btn => {
      btn.addEventListener('click', event => {
        event.stopPropagation();
        const panelId = btn.getAttribute('aria-controls');
        const panel = panelId ? document.getElementById(panelId) : null;
        if (!panel) return;
        const next = panel.hidden;
        panel.hidden = !next;
        btn.setAttribute('aria-expanded', next ? 'true' : 'false');
        btn.classList.toggle('is-open', next);
        btn.title = next ? 'Hide full result' : 'Show full result';
      });
    });
  }

  // renderRunsCard owns the visibility + active-tab state of the merged
  // Runs/Trace card. It delegates the inner HTML to renderExecutionBreakdown
  // (Runs tab) and renderExecutionTrace (Trace tab); those methods only
  // populate their inner panes — they no longer toggle card visibility on
  // their own.
  renderRunsCard() {
    if (!this.elements.runsCard) return;

    const breakdownSteps = this.getExecutionBreakdownSteps(this.task, this.getSubtasks());
    const traceEntries = this.buildExecutionTraceEntries();
    const hasBreakdown = breakdownSteps.length > 0;
    const hasAnyTrace = traceEntries.length > 0;
    const hasTraceSurface = hasAnyTrace || this.hasExecutionActivity();

    if (!hasBreakdown && !hasTraceSurface) {
      this.elements.runsCard.hidden = true;
      if (this.elements.toolSummary) {
        this.elements.toolSummary.hidden = true;
        this.elements.toolSummary.innerHTML = '';
      }
      if (this.elements.trace) this.elements.trace.innerHTML = '';
      if (this.elements.executionTrace) this.elements.executionTrace.innerHTML = '';
      if (this.elements.executionTraceControls) this.elements.executionTraceControls.hidden = true;
      return;
    }

    this.elements.runsCard.hidden = false;
    if (typeof this.renderToolSummary === 'function') {
      this.renderToolSummary();
    }

    if (this.elements.runsTabRuns) this.elements.runsTabRuns.hidden = !hasBreakdown;
    if (this.elements.runsTabTrace) this.elements.runsTabTrace.hidden = !hasTraceSurface;

    let active = this._runsTab === 'trace' ? 'trace' : 'runs';
    if (active === 'runs' && !hasBreakdown && hasTraceSurface) active = 'trace';
    if (active === 'trace' && !hasTraceSurface && hasBreakdown) active = 'runs';
    this._runsTab = active;

    if (this.elements.runsTabRuns) {
      this.elements.runsTabRuns.classList.toggle('is-active', active === 'runs');
      this.elements.runsTabRuns.setAttribute('aria-selected', active === 'runs' ? 'true' : 'false');
    }
    if (this.elements.runsTabTrace) {
      this.elements.runsTabTrace.classList.toggle('is-active', active === 'trace');
      this.elements.runsTabTrace.setAttribute(
        'aria-selected',
        active === 'trace' ? 'true' : 'false'
      );
    }

    const setCount = (el, count) => {
      if (!el) return;
      if (!count) {
        el.hidden = true;
        el.textContent = '';
        return;
      }
      el.hidden = false;
      el.textContent = String(count);
    };
    setCount(this.elements.runsTabRunsCount, breakdownSteps.length);
    setCount(this.elements.runsTabTraceCount, traceEntries.length);

    this.renderExecutionBreakdown();
    this.renderExecutionTrace();

    if (this.elements.trace) this.elements.trace.hidden = active !== 'runs';
    if (this.elements.executionTrace) this.elements.executionTrace.hidden = active !== 'trace';
    if (this.elements.executionTraceControls) {
      // Filter chips only make sense while looking at the trace pane and
      // only when we have at least 2 distinct buckets — bucketing logic in
      // renderTraceFilterChips already handles the latter; here we just
      // gate by which tab is visible.
      const wantControls = active === 'trace' && hasAnyTrace;
      // Don't force-show: the trace renderer may still hide it when there's
      // only one bucket. Only force-hide when on the runs tab.
      if (!wantControls) this.elements.executionTraceControls.hidden = true;
    }
  }

  renderWorkspaceRunsCard() {
    if (!this.elements.workspaceRunsCard || !this.elements.workspaceRuns) return;

    const runs = Array.isArray(this.workspaceRuns) ? this.workspaceRuns : [];
    if (!runs.length) {
      this.elements.workspaceRunsCard.hidden = true;
      this.elements.workspaceRuns.innerHTML = '';
      return;
    }

    this.elements.workspaceRunsCard.hidden = false;
    this.elements.workspaceRuns.innerHTML = runs
      .map(run => {
        const status = String(run?.status || '').trim();
        const validationStatus = String(run?.report?.validation_status || '')
          .trim()
          .toLowerCase();
        const summary = String(run?.report?.summary || '').trim();
        // Read like a history entry: when it ran is the clickable title. The
        // profile snapshot, executor kind/worker ref, validation status, and
        // raw run id are developer details — they live on the run page and the
        // Developer tab, not in this friendly list.
        const when = run?.started_at || run?.created_at || run?.finished_at;
        const title = when ? formatDateTime(when) : 'Run';
        const needsReview = validationStatus === 'needs_review';

        return `
        <article class="workspace-task-workspace-run">
          <div class="workspace-task-workspace-run-head">
            <div class="workspace-task-workspace-run-title">
              <strong><a href="${this.escapeHtml(this.getRunHref(run?.id || ''))}" class="workspace-task-workspace-run-link">${this.escapeHtml(title)}</a></strong>
              ${needsReview ? '<div class="workspace-task-workspace-run-meta">Needs review before saving</div>' : ''}
            </div>
            <span class="workspace-task-workspace-run-status" data-state="${this.escapeHtml(status)}">${this.escapeHtml(this.formatWorkspaceRunStatus(status) || 'Recorded')}</span>
          </div>
          ${summary ? `<div class="workspace-task-workspace-run-summary">${this.escapeHtml(summary)}</div>` : ''}
        </article>
      `;
      })
      .join('');
  }

  setRunsTab(name) {
    const next = name === 'trace' ? 'trace' : 'runs';
    if (next === this._runsTab) return;
    this._runsTab = next;
    if (this._renderCache) delete this._renderCache.runs;
    this.renderRunsCard();
  }

  renderContext() {
    if (!this.elements.context || !this.elements.contextCard) return;

    const context =
      this.task?.context && typeof this.task.context === 'object' ? { ...this.task.context } : {};
    delete context.human_loop;

    const contextText = Object.keys(context).length > 0 ? normalizeResultText(context) : '';

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

    // The respond UI is an inline card at the top of the flow (no slide-out).
    // Toggle it with the blocked state; renderAssistCard fills its content.
    if (!blocked) {
      if (this.elements.assistCard) this.elements.assistCard.hidden = true;
      if (this.elements.blockedContextCard) this.elements.blockedContextCard.hidden = true;
      return;
    }

    if (this.elements.assistCard) this.elements.assistCard.hidden = false;
    this.renderBlockedContext();
    this.renderAssistCard();
  }

  renderBlockedContext() {
    if (!this.elements.blockedContextCard) return;

    const response = String(this.currentBlockedTask?.response || '').trim();
    const responsePreview = summarizeText(response, 260);

    this.elements.blockedContextCard.hidden = false;
    if (this.elements.blockedReason) {
      this.elements.blockedReason.textContent =
        this.currentBlockedTask?.reason || 'This task is waiting for your input.';
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
      this.elements.blockedRequest.classList.toggle(
        'd-none',
        !this.taskAssistResponseExpanded || !response
      );
    }
    if (this.elements.blockedRequestToggle) {
      const hasLongResponse = response.length > 0 && response !== responsePreview;
      this.elements.blockedRequestToggle.classList.toggle('d-none', !hasLongResponse);
      this.elements.blockedRequestToggle.textContent = this.taskAssistResponseExpanded
        ? 'Hide full request'
        : 'View full request';
      this.elements.blockedRequestToggle.setAttribute(
        'aria-expanded',
        this.taskAssistResponseExpanded ? 'true' : 'false'
      );
    }
  }

  renderAssistCard() {
    if (!this.elements.assistCard) return;

    const workflowStep = this.currentBlockedTask?.workflowStep || null;

    if (this.elements.assistKnown) {
      this.elements.assistKnown.textContent =
        summarizeText(
          this.currentBlockedTask?.response ||
            this.currentBlockedTask?.reason ||
            'The task is paused waiting on your input.',
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
      const showQuestion =
        Boolean(this.currentBlockedTask?.question) && workflowStep?.stepType !== 'ask_form';
      this.elements.assistQuestionWrap.classList.toggle('d-none', !showQuestion);
    }
    if (this.elements.assistQuestion) {
      this.elements.assistQuestion.textContent = this.currentBlockedTask?.question || '';
    }
    if (this.elements.assistContinueBtn) {
      const primaryLabel = this.getAssistPrimaryActionLabel(workflowStep);
      this.elements.assistContinueBtn.textContent = primaryLabel;
      this.elements.assistContinueBtn.setAttribute('aria-label', primaryLabel);
      this.elements.assistContinueBtn.hidden = !this.isAssistActionSuggested(
        'continue_with_instruction'
      );
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
      const hasMoreActions =
        this.isAssistActionSuggested('switch_agent_retry') ||
        this.isAssistActionSuggested('retry') ||
        this.isAssistActionSuggested('mark_failed');
      this.elements.assistMoreActions.hidden = !hasMoreActions;
    }

    this.populateAssistAgents(this.currentBlockedTask?.currentAgent || '');
    this.renderWorkflowStepUI(workflowStep);
    this.updateAssistSwitchButtonState();
  }

  getAssistNeedsSummary(workflowStep) {
    if (this.currentBlockedTask?.reasonCode === 'assigned_agent_missing') {
      return 'Pick a runnable agent for this task before you retry execution.';
    }
    if (
      workflowStep?.stepType === 'ask_form' &&
      Array.isArray(workflowStep.fields) &&
      workflowStep.fields.length > 0
    ) {
      return `Answer ${workflowStep.fields.length} question${workflowStep.fields.length === 1 ? '' : 's'} so the agent can continue.`;
    }
    if (
      workflowStep?.stepType === 'ask_choice' &&
      Array.isArray(workflowStep.choices) &&
      workflowStep.choices.length > 0
    ) {
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
      return this.currentBlockedTask?.selectedChoiceId
        ? 'Continue With Selected Path'
        : 'Send Choice Or Guidance';
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
    const currentUnavailable =
      Boolean(currentNormalized) && !this.isRunnableAgentName(currentNormalized);
    const options = [
      currentUnavailable
        ? '<option value="" selected>Select an available agent</option>'
        : '<option value="">Keep current assignment</option>'
    ];

    if (currentUnavailable) {
      options.push(
        `<option value="${this.escapeHtml(currentNormalized)}" disabled>${this.escapeHtml(`${currentNormalized} (Current assignment unavailable)`)}</option>`
      );
    }

    this.getAssignableAgentNames(currentAgent).forEach(agentName => {
      const normalized = String(agentName || '').trim();
      if (!normalized || normalized.toLowerCase() === currentNormalized.toLowerCase()) return;
      options.push(
        `<option value="${this.escapeHtml(normalized)}">${this.escapeHtml(normalized)}</option>`
      );
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
        ${workflowStep.choices
          .map(
            (choice, index) => `
          <button
            type="button"
            class="workspace-task-assist-option${selectedChoiceId === choice.id ? ' is-selected' : ''}"
            data-assist-choice-id="${this.escapeHtml(choice.id)}"
            aria-pressed="${selectedChoiceId === choice.id ? 'true' : 'false'}">
            <span class="workspace-task-assist-option-card">
              <span class="workspace-task-assist-option-key">${this.escapeHtml(choice.number || String.fromCharCode(65 + (index % 26)))}</span>
              <span class="workspace-task-assist-option-copy">
                <span class="workspace-task-assist-option-label">${this.escapeHtml(choice.label)}</span>
                ${choice.recommended ? '<span class="workspace-task-assist-option-badge">Recommended</span>' : ''}
                ${choice.description ? `<span class="workspace-task-assist-option-description">${this.escapeHtml(choice.description)}</span>` : ''}
              </span>
            </span>
          </button>
        `
          )
          .join('')}
      </div>
    `;

    this.elements.assistFormFields.querySelectorAll('[data-assist-choice-id]').forEach(button => {
      button.addEventListener('click', () => {
        const choiceId = String(button.getAttribute('data-assist-choice-id') || '').trim();
        const choice = workflowStep.choices.find(item => item.id === choiceId);
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

    const currentFieldIndex = fields.findIndex(field => field.id === this.assistActiveFieldId);
    const firstUnansweredIndex = fields.findIndex(
      field => !String(selectedValues[field.id] || '').trim()
    );
    const activeIndex =
      currentFieldIndex >= 0
        ? currentFieldIndex
        : firstUnansweredIndex >= 0
          ? firstUnansweredIndex
          : 0;
    const activeField = fields[activeIndex];
    const answeredCount = fields.filter(field =>
      String(selectedValues[field.id] || '').trim()
    ).length;
    const progressPercent =
      fields.length > 0 ? Math.round((answeredCount / fields.length) * 100) : 0;
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
            ${fields
              .map((field, index) => {
                const fieldValue = String(selectedValues[field.id] || '').trim();
                const isActive = index === activeIndex;
                const isAnswered = Boolean(fieldValue);
                const meta =
                  field.type === 'select' && Array.isArray(field.options)
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
              })
              .join('')}
          </div>

          <div class="workspace-task-assist-deck-panel">
            <div class="workspace-task-assist-deck-progress-row">
              <div>
                <div class="workspace-task-assist-deck-kicker">${reviewMode ? 'Final Review' : `Question ${activeIndex + 1} of ${fields.length}`}</div>
                <div class="workspace-task-assist-deck-progress-copy" data-assist-progress-count>${
                  reviewMode
                    ? answeredCount === fields.length
                      ? 'All answers captured'
                      : `${fields.length - answeredCount} answers still missing`
                    : `${answeredCount} answered so far`
                }</div>
              </div>
              <div class="workspace-task-assist-deck-progress-bar" aria-hidden="true">
                <span data-assist-progress-fill style="width: ${progressPercent}%"></span>
              </div>
            </div>

            ${
              reviewMode
                ? this.renderAssistReviewMarkup(fields)
                : `<div class="workspace-task-assist-deck-stage">
                  ${this.renderAssistFieldMarkup(activeField, activeIndex, String(selectedValues[activeField.id] || '').trim(), { active: true })}
                </div>`
            }

            <div class="workspace-task-assist-deck-nav">
              <button type="button" class="modern-btn modern-btn-secondary" data-assist-field-nav="${reviewMode ? 'review-back' : 'prev'}" ${reviewMode ? '' : activeIndex === 0 ? 'disabled' : ''}>${reviewMode ? 'Back To Questions' : 'Previous'}</button>
              ${
                reviewMode
                  ? '<div class="workspace-task-assist-deck-nav-note">Use Send Answers below or edit any item in this review.</div>'
                  : `<button type="button" class="modern-btn modern-btn-secondary" data-assist-field-nav="next">${activeIndex === fields.length - 1 ? 'Review Answers' : 'Next Question'}</button>`
              }
            </div>
          </div>
        </div>
      `;
    }

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-tab]').forEach(button => {
      button.addEventListener('click', () => {
        this.assistReviewMode = false;
        this.assistActiveFieldId = String(
          button.getAttribute('data-assist-field-tab') || ''
        ).trim();
        this.renderFormWorkflow(workflowStep);
        this.focusActiveAssistField();
      });
    });

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-nav]').forEach(button => {
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

    this.elements.assistFormFields
      .querySelectorAll('[data-assist-review-edit-id]')
      .forEach(button => {
        button.addEventListener('click', () => {
          this.assistReviewMode = false;
          this.assistActiveFieldId = String(
            button.getAttribute('data-assist-review-edit-id') || ''
          ).trim();
          this.renderFormWorkflow(workflowStep);
          this.focusActiveAssistField();
        });
      });

    this.elements.assistFormFields
      .querySelectorAll('[data-assist-field-id]')
      .forEach(fieldElement => {
        const syncValue = () => {
          const fieldId = String(fieldElement.getAttribute('data-assist-field-id') || '').trim();
          if (!fieldId) return;
          if (
            fieldElement instanceof HTMLInputElement &&
            fieldElement.type === 'radio' &&
            !fieldElement.checked
          ) {
            return;
          }
          if (fieldElement instanceof HTMLInputElement && fieldElement.type === 'radio') {
            const optionValue = String(
              fieldElement.getAttribute('data-assist-option-value') || fieldElement.value || ''
            ).trim();
            const isCustomOption =
              String(fieldElement.getAttribute('data-assist-custom-option') || '').trim() ===
              'true';
            this.setAssistFormFieldOptionValue(fieldId, optionValue);
            if (isCustomOption) {
              const existingCustomValue = String(
                this.currentBlockedTask?.selectedFieldCustomValues?.[fieldId] || ''
              ).trim();
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

    this.elements.assistFormFields
      .querySelectorAll('[data-assist-custom-field-id]')
      .forEach(fieldElement => {
        const syncCustomValue = () => {
          const fieldId = String(
            fieldElement.getAttribute('data-assist-custom-field-id') || ''
          ).trim();
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
          ${
            field.description && this.getAssistFieldPrompt(field) !== field.description
              ? `<div class="workspace-task-assist-field-hint">${this.escapeHtml(field.description)}</div>`
              : ''
          }
          ${field.evidence ? `<div class="workspace-task-assist-field-evidence">${this.escapeHtml(field.evidence)}</div>` : ''}
        </div>
      </div>
    `;

    if (field.type === 'select' && Array.isArray(field.options) && field.options.length > 0) {
      const selectedOptionValue = String(
        this.currentBlockedTask?.selectedFieldOptionValues?.[field.id] || ''
      ).trim();
      const customValue = String(
        this.currentBlockedTask?.selectedFieldCustomValues?.[field.id] || ''
      ).trim();
      const otherOption = field.options.find(option => isAssistOtherOption(option));
      const otherOptionValue = String(otherOption?.value || otherOption?.label || '').trim();
      const showCustomInput = Boolean(otherOptionValue) && selectedOptionValue === otherOptionValue;

      return `
        <article class="workspace-task-assist-field${active ? ' is-active' : ''}">
          ${questionIntro}
          <div class="workspace-task-assist-option-group" role="radiogroup" aria-label="${this.escapeHtml(field.label)}">
            ${field.options
              .map(
                option => `
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
            `
              )
              .join('')}
          </div>
          ${
            showCustomInput
              ? `
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
          `
              : ''
          }
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
    const answeredCount = fields.filter(field =>
      Boolean(this.getAssistFieldAnswerValue(field))
    ).length;

    return `
      <div class="workspace-task-assist-review">
        <div class="workspace-task-assist-review-banner">
          <div class="workspace-task-assist-review-title">Review what will be sent back to the agent.</div>
          <div class="workspace-task-assist-review-copy">${
            answeredCount === fields.length
              ? 'Everything requested has an answer. You can send this now or edit any item below.'
              : `${fields.length - answeredCount} item${fields.length - answeredCount === 1 ? '' : 's'} still need attention before you continue.`
          }</div>
        </div>
        <div class="workspace-task-assist-review-list">
          ${fields
            .map((field, index) => {
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
            })
            .join('')}
        </div>
      </div>
    `;
  }

  getAssistFieldAnswerValue(field) {
    if (!field || !this.currentBlockedTask) return '';

    const selectedValue = String(
      this.currentBlockedTask.selectedFieldValues?.[field.id] || ''
    ).trim();
    if (!selectedValue) return '';

    if (field.type === 'select' && Array.isArray(field.options) && field.options.length > 0) {
      const option = field.options.find(item => String(item?.value || '').trim() === selectedValue);
      if (option) {
        return String(option.label || option.value || '').trim();
      }
    }

    return selectedValue;
  }

  syncAssistFormProgress(workflowStep) {
    if (!workflowStep || workflowStep.stepType !== 'ask_form' || !this.elements.assistFormFields)
      return;

    const fields = Array.isArray(workflowStep.fields) ? workflowStep.fields : [];
    if (fields.length <= 1) return;

    const selectedValues = this.currentBlockedTask?.selectedFieldValues || {};
    const answeredCount = fields.filter(field =>
      String(selectedValues[field.id] || '').trim()
    ).length;
    const progressPercent =
      fields.length > 0 ? Math.round((answeredCount / fields.length) * 100) : 0;

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-tab]').forEach(button => {
      const fieldId = String(button.getAttribute('data-assist-field-tab') || '').trim();
      const isAnswered = Boolean(String(selectedValues[fieldId] || '').trim());
      button.classList.toggle('is-answered', isAnswered);
      const state = button.querySelector('.workspace-task-assist-deck-tab-state');
      if (state) {
        state.textContent = isAnswered ? 'Answered' : 'Open';
      }
    });

    const progressCopy = this.elements.assistFormFields.querySelector(
      '[data-assist-progress-count]'
    );
    if (progressCopy) {
      progressCopy.textContent = this.assistReviewMode
        ? answeredCount === fields.length
          ? 'All answers captured'
          : `${fields.length - answeredCount} answers still missing`
        : `${answeredCount} answered so far`;
    }

    const progressFill = this.elements.assistFormFields.querySelector(
      '[data-assist-progress-fill]'
    );
    if (progressFill) {
      progressFill.style.width = `${progressPercent}%`;
    }
  }

  focusActiveAssistField() {
    if (!this.elements.assistFormFields) return;

    const stage =
      this.elements.assistFormFields.querySelector('.workspace-task-assist-deck-stage') ||
      this.elements.assistFormFields;
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
    if (
      !this.currentBlockedTask.selectedFieldValues ||
      typeof this.currentBlockedTask.selectedFieldValues !== 'object'
    ) {
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
    if (
      !this.currentBlockedTask.selectedFieldOptionValues ||
      typeof this.currentBlockedTask.selectedFieldOptionValues !== 'object'
    ) {
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
    if (
      !this.currentBlockedTask.selectedFieldCustomValues ||
      typeof this.currentBlockedTask.selectedFieldCustomValues !== 'object'
    ) {
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
    if (
      !workflowStep ||
      workflowStep.stepType !== 'ask_form' ||
      !Array.isArray(workflowStep.fields)
    ) {
      return [];
    }

    const selectedValues = this.currentBlockedTask?.selectedFieldValues || {};
    return workflowStep.fields
      .map(field => {
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
    ].forEach(button => {
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
    const status = String(this.task?.status || '')
      .trim()
      .toLowerCase();
    const isRerun = status === 'completed' || status === 'failed';
    const label = isRerun ? 'Re-run' : 'Run';

    if (isRerun && !confirm(`${label} this task? Previous results will be replaced.`)) return;

    // Inline assign-and-run for unassigned tasks. Without this the user
    // would see the Run button do nothing meaningful (or a generic
    // server error) when the task has no agent — they'd have to leave
    // the hero, scroll to the snapshot card, change agent, then come
    // back. The picker reuses the same dialog the canvas uses.
    const currentAgent = String(this.task?.to || '').trim();
    if (!currentAgent || currentAgent === 'unassigned') {
      const agents = this.getAssignableAgentOptions();
      if (agents.length === 0) {
        this.notify('error', 'No agents in this workspace yet — add one before running tasks.');
        return;
      }
      const picked = await showCanvasAgentPicker({
        title: 'Assign an agent',
        message: `This task is unassigned. Pick an agent to run "${this.getTaskDisplayLabel()}".`,
        agents
      });
      if (!picked) return;
      try {
        await this.updateTaskFields({ to: picked.name });
      } catch (error) {
        this.notify('error', error?.message || 'Failed to assign agent');
        return;
      }
    }

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

  /**
   * Render or clear the inline disabled-reason hint for the workflow
   * "Run all" button. Anchored after the card header (rather than
   * alongside the buttons) so it never sits inside the header's flex
   * row and disrupt the action-button layout.
   */
  renderWorkflowRunAllReason(reason) {
    const card = this.elements.workflowCard;
    if (!card) return;
    const id = 'workspace-task-workflow-run-all-reason';
    let el = document.getElementById(id);
    if (!reason) {
      if (el) el.remove();
      return;
    }
    if (!el) {
      el = document.createElement('div');
      el.id = id;
      el.className = 'workspace-task-workflow-run-all-reason';
      const header = card.querySelector('.workspace-task-page-card-header');
      if (header) {
        header.insertAdjacentElement('afterend', el);
      } else {
        card.prepend(el);
      }
    }
    el.textContent = reason;
  }

  /**
   * Build the agent list for the inline assign-and-run picker, in the
   * shape expected by showCanvasAgentPicker ([{ name, instanceNumber }]).
   * Filters out the "unassigned" placeholder. Mirrors the existing
   * snapshot-card agent dropdown so the user sees the same set in
   * either place.
   */
  getAssignableAgentOptions() {
    const names = this.getAssignableAgentNames('');
    return names.filter(name => name && name !== 'unassigned').map(name => ({ name }));
  }

  async completeTask() {
    // Confirm when overriding a running task. The server validates the
    // transition (since the harden-completion commit) and will reject
    // bad cases — but a silent client-side override on an in-flight
    // execution still races the executor in confusing ways. Make the
    // user opt in explicitly. The other allowed source statuses
    // (pending / assigned / waiting_for_choice) don't risk losing
    // execution work, so they skip the prompt.
    const status = String(this.task?.status || '')
      .trim()
      .toLowerCase();
    if (status === 'in_progress') {
      const proceed = window.confirm(
        'This task is currently running. Marking it complete now will override the live execution and may discard any work the agent is mid-way through. Continue?'
      );
      if (!proceed) return;
    }
    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.taskId)}/complete`,
        { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(this.parseResponseError(text, 'Failed to complete task'));
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

    if (
      action === 'switch_agent_retry' &&
      selectedAgent.toLowerCase() ===
        String(this.currentBlockedTask.currentAgent || '')
          .trim()
          .toLowerCase()
    ) {
      this.notify('warning', 'Choose a different agent before switching.');
      return;
    }

    if (
      action === 'continue_with_instruction' &&
      workflowStep?.stepType === 'ask_choice' &&
      !selectedChoiceId &&
      !message
    ) {
      this.notify('warning', 'Choose a next step or add guidance before continuing.');
      return;
    }

    if (action === 'continue_with_instruction' && workflowStep?.stepType === 'ask_form') {
      const requiredFields = Array.isArray(workflowStep.fields)
        ? workflowStep.fields.filter(field => field?.required !== false)
        : [];
      const missingRequired = requiredFields.filter(
        field => !fieldValues.some(item => item.id === field.id)
      );
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
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.currentBlockedTask.taskId)}/assist`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to update task');
      }

      this.notify('success', 'Task updated');
      // No explicit close: the inline respond card hides itself on the next
      // render (renderBlockedState) once loadData shows the task is unblocked.
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

// Mix in the sibling-module method bundles. They live next door purely to
// keep this file navigable; behaviorally they're indistinguishable from
// declaring them inline because Object.assign places them on the prototype
// before any instance is constructed.
Object.assign(
  WorkspaceTaskPage.prototype,
  taskExecutionViewsMethods,
  taskSkillDraftMethods,
  taskResultActionsMethods
);
