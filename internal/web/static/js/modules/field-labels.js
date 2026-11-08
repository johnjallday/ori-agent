/*
 * field-labels.js — the host's labels for a library entry's stage and status,
 * the same words as the Home shelf's filter options in
 * templates/pages/workspace-assistant.tmpl. The project card, the Home roster
 * and the shelf use them; the vocabulary is host-owned and unchanged by PRD
 * #792 (open question 1).
 */

export const STAGE_LABELS = Object.freeze({
  idea: 'Idea',
  writing: 'Writing',
  recording: 'Recording',
  editing: 'Editing',
  mixing: 'Mixing',
  mastering: 'Mastering'
});

export const STATUS_LABELS = Object.freeze({
  planning: 'Planning',
  active: 'Active',
  on_hold: 'On hold',
  complete: 'Complete',
  archived: 'Archived'
});

/** "Mixing" for "mixing"; '' for an unset or unknown stage. */
export function stageLabel(stage) {
  return Object.hasOwn(STAGE_LABELS, stage) ? STAGE_LABELS[stage] : '';
}

/** "On hold" for "on_hold"; '' for an unset or unknown status. */
export function statusLabel(status) {
  return Object.hasOwn(STATUS_LABELS, status) ? STATUS_LABELS[status] : '';
}
