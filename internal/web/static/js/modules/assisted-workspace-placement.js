import {
  creationReviewHeaderHTML,
  creationReviewSlotsHTML,
  creationReviewNavigationHTML,
  workspaceCreationReviewHTML
} from './workspace-creation-review.js';

// Presentation only. The sessions controller supplies its canonical draft/review
// and handles intentions. This module never fetches, creates, grants or scans.
const escape = value =>
  String(value ?? '').replace(
    /[&<>"']/g,
    char =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      })[char]
  );

// Ephemeral card references are local to one editor generation. Drag data is
// only an opaque key, never a profile, path, folder token or executable plan.
// Resolving a key selects an existing setter; server review remains authority.
export function createPlacementCards(makeKey) {
  let version;
  let cards = new Map();
  return {
    update(nextVersion, choices) {
      if (version !== nextVersion) cards.clear();
      version = nextVersion;
      const previous = new Map([...cards.values()].map(card => [`${card.kind}:${card.id}`, card]));
      cards = new Map();
      for (const choice of choices.slice(0, 64)) {
        if (!['agent', 'folder'].includes(choice.kind) || !choice.id) continue;
        const key = previous.get(`${choice.kind}:${choice.id}`)?.key || makeKey();
        cards.set(key, { ...choice, key });
      }
      return [...cards.values()].map(card => ({
        key: card.key,
        kind: card.kind,
        label: card.label,
        detail: card.detail
      }));
    },
    resolve(key, slot, currentVersion) {
      const card = typeof key === 'string' && cards.get(key);
      if (version !== currentVersion || !card || card.kind !== slot) return null;
      return { kind: card.kind, id: card.id };
    }
  };
}

export function placementPanelHTML(view = {}) {
  if (view.ownerKind === 'ordinary') return workspaceCreationReviewHTML(view);
  const draft = view.draft || {};
  if (view.result)
    return `<section class="assisted-placement" aria-label="Workspace preparation result">
    <h2>Workspace prepared</h2>
    <p>${escape(view.resultName || draft.name)}</p>
    ${Array.isArray(view.members) && view.members.length ? `<p>Assigned: ${view.members.map(escape).join(', ')}</p>` : ''}
    <p>${view.result.outcome === 'batch' ? `${escape(view.result.eligible_count)} ${Number(view.result.eligible_count) === 1 ? 'file' : 'files'} in the first filing review.` : 'The first scan found no eligible files.'}</p>
    <p>Source files were not moved. Filing still needs your separate approval.</p>
    <p role="status" aria-live="polite" tabindex="-1" data-placement-focus="status">${escape(view.message)}</p>
    ${view.sourceReceiptPending ? '<p>The assistant receipt is not yet saved. Read status again to reconcile this same setup; do not create another.</p>' : ''}
    <div class="assisted-placement__footer">
      ${view.sourceReceiptPending ? `<button type="button" data-placement-action="read-status"${view.busy ? ' disabled' : ''}>Read saved status</button>` : ''}
      ${view.positionFailed ? `<button type="button" data-placement-action="retry-position"${view.busy ? ' disabled' : ''}>Retry position only</button>` : ''}
      <button type="button" data-placement-action="open"${view.busy ? ' disabled' : ''}>Open filing review</button>
      <button type="button" data-placement-action="back"${view.busy ? ' disabled' : ''}>Close</button>
    </div></section>`;
  const saved = view.review?.outcome;
  if (view.accepted)
    return `<section class="assisted-placement" aria-label="Confirmed workspace setup">
      <h2>Check confirmed setup</h2>
      <p>Work may already exist. Closing is not undo, and this draft cannot create a second workspace.</p>
      <p role="status" aria-live="polite" tabindex="-1" data-placement-focus="status">${escape(view.message)}</p>
      <p>Reading status never continues setup. An explicit continuation uses only the original confirmed choices.</p>
      ${
        view.recoveryRoute && saved
          ? `<section class="assisted-placement__review" aria-label="Saved setup">
        <dl><dt>Existing workspace</dt><dd>${escape(saved.name)}</dd>
        <dt>Goal</dt><dd class="assisted-placement__goal">${escape(saved.goal || 'No goal recorded')}</dd>
        <dt>Assigned team</dt><dd>${Array.isArray(saved.members) ? saved.members.map(escape).join(', ') : ''}</dd>
        <dt>Source</dt><dd>${escape(saved.folder_path || saved.folder_name)} — linked, not moved</dd>
        <dt>Access</dt><dd>Top-level metadata only; create or reuse Filed/. No file contents, moves or deletions.</dd>
        <dt>Next</dt><dd>Continue the original filing-review preparation, not a new workspace.</dd>
        <dt>Confirmed monitoring</dt><dd>${view.review.monitoring ? `On · watch for new files; daily catch-up at ${escape(saved.terms?.daily_scan_local_time || 'the confirmed time')} (${escape(saved.terms?.timezone || 'local time')}).` : 'Off · manual scans only.'}</dd></dl>
      </section>`
          : ''
      }
      <div class="assisted-placement__footer">
        <button type="button" data-placement-action="read-status"${view.busy ? ' disabled' : ''}>Read saved status</button>
        ${view.recoveryRoute ? `<button type="button" data-placement-action="open-settings"${view.busy ? ' disabled' : ''}>Open existing filing review</button>` : ''}
        <button type="button" data-placement-action="confirm"${view.busy || view.reconciliationRequired || !view.review ? ' disabled' : ''}>Continue accepted setup</button>
        <button type="button" data-placement-action="back"${view.busy ? ' disabled' : ''}>${view.onMap ? 'Back to review' : 'Close'}</button>
      </div></section>`;
  const review = view.review;
  const busy = Boolean(view.busy);
  const locked = busy || view.accepted || view.conflict || view.closed;
  const disabled = locked ? ' disabled' : '';
  const team = review?.team || view.team || [];
  const teamText = team
    .map(member => `${member.name} · ${member.action === 'create' ? 'new agent' : 'saved agent'}`)
    .join(', ');
  const folder = review?.folder || view.folder || {};
  const workspace = review?.workspace || draft;
  // The reviewed map gesture is confirmation. No second panel competes with
  // that step; accepted/progress/results above remain available afterwards.
  if (view.onMap) return '';
  return `
    <section class="assisted-placement" aria-label="Review proposed workspace">
      ${creationReviewHeaderHTML({
        title: `Make room for ${draft.name || 'your folder'}`,
        note: 'Suggested by your assistant. Nothing is created until you confirm.',
        destination: 'Standalone workspace · Position does not join a district.',
        prototype: view.prototype,
        art: 'depot'
      })}
      <div class="assisted-placement__fields">
        <div><label for="assistedPlacementName">Workspace name</label><input id="assistedPlacementName" name="name" data-placement-field="name" maxlength="200" value="${escape(draft.name)}"${disabled}></div>
        <div><label for="assistedPlacementGoal">Goal</label><textarea id="assistedPlacementGoal" name="mission" data-placement-field="mission" rows="2" maxlength="2000"${disabled}>${escape(draft.mission)}</textarea></div>
      </div>
      ${view.cardsAvailable ? `<button type="button" data-placement-action="cards" aria-expanded="${Boolean(view.cardsOpen)}"${disabled}>${view.cardsOpen ? 'Hide setup cards' : 'Browse setup cards'}</button>` : ''}
      ${
        view.cardsOpen
          ? `<div class="assisted-placement__cards">
        <p class="assisted-placement__hint">Select a card, then use its slot button, or drag it to the slot. These are saved agents and folders already selected in this draft. The Choose controls show the full roster, new-agent form and folder picker.</p>
        <ul aria-label="Available setup cards">${(view.cards || []).map(card => `<li><button type="button" data-placement-action="card-select" data-placement-card="${escape(card.key)}" draggable="${!locked}" aria-pressed="${view.selectedCard?.key === card.key}"${disabled}><strong>${escape(card.label)}</strong><small>${escape(card.detail)}</small></button></li>`).join('')}</ul>
      </div>`
          : ''
      }
      ${creationReviewSlotsHTML(
        [
          {
            label: 'File Curator',
            ariaLabel: 'File Curator role',
            value: teamText || 'Choose an agent',
            slot: 'agent',
            action: 'agent',
            actionLabel: 'Choose agent'
          },
          {
            label: 'Linked folder',
            value: folder.name || 'Choose a folder',
            detail: folder.path,
            slot: 'folder',
            action: 'folder',
            actionLabel: 'Choose folder'
          },
          {
            label: 'Prepare a filing review',
            ariaLabel: 'First action',
            value: 'A metadata-only scan. You approve any filing separately.'
          }
        ],
        { locked, selectedCard: view.selectedCard }
      )}
      <label class="assisted-placement__monitoring"><input type="checkbox" data-placement-field="monitoring"${view.monitoring ? ' checked' : ''}${disabled}> Also watch for new files and run a daily catch-up</label>
      <p class="assisted-placement__hint">${view.monitoring ? 'Monitoring is selected. Review the exact schedule below before confirming.' : 'Monitoring is off. No background scans will be enabled.'}</p>
      <section class="assisted-placement__review" aria-label="Before you confirm">
        <h3>Before you confirm</h3>
        ${
          review
            ? `<dl>
          <dt>Workspace</dt><dd>${escape(workspace.name)} · standalone</dd>
          <dt>Goal</dt><dd class="assisted-placement__goal">${escape(workspace.mission)}</dd>
          <dt>Team</dt><dd>${escape(teamText)}</dd>
          <dt>Source</dt><dd>${escape(folder.path || folder.name)} — linked, not moved</dd>
          <dt>Access</dt><dd>Top-level names, types, sizes and dates only. No file contents.</dd>
          <dt>Destination</dt><dd>Create or reuse Filed/ inside this folder.</dd>
          <dt>Next</dt><dd>Prepare the first filing review. No model call. Nothing is moved or deleted.</dd>
          <dt>Monitoring</dt><dd>${review.monitoring ? `On · watch for new files; daily catch-up at ${escape(review.terms?.daily_scan_local_time || 'the reviewed time')} (${escape(review.terms?.timezone || 'local time')}).` : 'Off · manual scans only.'}</dd>
        </dl>`
            : '<p>Your current choices need a fresh server review before you can confirm.</p>'
        }
      </section>
      <p class="assisted-placement__status" role="status" aria-live="polite" tabindex="-1" data-placement-focus="status">${escape(view.message || '')}</p>
      ${view.conflict ? `<button type="button" data-placement-action="reload-draft"${busy ? ' disabled' : ''}>Discard local edits &amp; load saved draft</button>` : ''}
      ${view.canRefresh ? '<button type="button" data-placement-action="review">Refresh permission review</button>' : ''}
      ${creationReviewNavigationHTML({ ...view, reviewed: Boolean(review), mapHint: view.candidate ? 'Your previous position is kept. On the map, click or press Enter to create this reviewed setup and scan.' : 'On the map, click a valid position or press Enter to create this reviewed setup and scan. Moving the preview does nothing. Escape returns here.' })}
      <footer class="assisted-placement__footer">
        <button type="button" data-placement-action="back"${busy ? ' disabled' : ''}>Back</button>
        <button type="button" data-placement-action="cancel"${disabled}>Cancel draft</button>
        ${view.automatic ? `<button type="button" class="assisted-placement__primary" data-placement-action="confirm"${!view.canConfirm || !review || locked ? ' disabled' : ''}>${busy ? 'Working…' : 'Create workspace &amp; scan'}</button>` : ''}
      </footer>
    </section>`;
}

// All values are rendered as text. Only host-owned building SVG is markup.
// The integration owner rechecks version/review on every intention, including
// a final click; disabled controls are feedback, never a security boundary.
export function mountPlacementPanel(host, onAction) {
  let current = {};
  let dragging = false;
  let pointing = false;
  let redraw = false;
  let redrawTimer;
  let controller;
  const click = event => {
    const control = event.target.closest?.('[data-placement-action]');
    if (!control || !host.contains(control) || control.disabled) return;
    const action = { action: control.dataset.placementAction };
    if (action.action.startsWith('card-')) {
      action.key = control.dataset.placementCard || current.selectedCard?.key;
      action.slot = control.dataset.placementSlotUse;
    }
    onAction(action);
  };
  const change = event => {
    const control = event.target;
    const field = control.dataset?.placementField;
    if (!field || control.disabled) return;
    onAction({
      action: 'edit',
      field,
      value: field === 'monitoring' ? control.checked : control.value
    });
  };
  const input = event => {
    if (!event.target.dataset?.placementField || event.target.disabled) return;
    // Block the old review immediately, without replacing an actively typed
    // field or losing its unsaved value before its change event.
    for (const action of ['confirm', 'place']) {
      const control = host.querySelector(`[data-placement-action="${action}"]`);
      if (control) control.disabled = true;
    }
    onAction({ action: 'dirty', field: event.target.dataset.placementField });
  };
  const pointerdown = event => {
    const card = event.target.closest?.('[data-placement-card]');
    if (card && !card.disabled) pointing = true;
  };
  const pointerup = () => {
    pointing = false;
    if (!dragging && redraw) {
      clearTimeout(redrawTimer);
      redrawTimer = setTimeout(() => {
        if (redraw) controller.update(current);
      }, 0);
    }
  };
  const cardType = 'application/x-ori-placement-card';
  const rejected =
    'Use a current setup card and its matching slot. Files, paths and cards from another draft cannot be used.';
  const clearTargets = () =>
    host
      .querySelectorAll('[data-placement-slot]')
      .forEach(slot => slot.classList.remove('is-drop-target'));
  const dragstart = event => {
    const card = event.target.closest?.('[data-placement-card]');
    if (!card || !host.contains(card) || card.disabled || current.busy || current.accepted) {
      event.preventDefault();
      return;
    }
    event.dataTransfer.setData(cardType, card.dataset.placementCard);
    event.dataTransfer.effectAllowed = 'copy';
    dragging = true;
    pointing = false;
  };
  const dragover = event => {
    event.preventDefault(); // An OS file must never navigate this sheet.
    const slot = event.target.closest?.('[data-placement-slot]');
    const allowed =
      slot &&
      !current.busy &&
      !current.accepted &&
      Array.from(event.dataTransfer?.types || []).includes(cardType);
    clearTargets();
    if (allowed) slot.classList.add('is-drop-target');
    if (event.dataTransfer) event.dataTransfer.dropEffect = allowed ? 'copy' : 'none';
    if (!allowed) {
      const status = host.querySelector('[role="status"]');
      if (status && status.textContent !== rejected) status.textContent = rejected;
    }
  };
  const dragend = () => {
    clearTargets();
    if (!dragging) return;
    dragging = false;
    pointing = false;
    if (redraw) controller.update(current);
  };
  const dragleave = event => {
    if (!host.contains(event.relatedTarget)) clearTargets();
  };
  const drop = event => {
    event.preventDefault();
    clearTargets();
    const slot = event.target.closest?.('[data-placement-slot]')?.dataset.placementSlot;
    const key = event.dataTransfer?.getData(cardType);
    dragend();
    if (!slot || !key || event.dataTransfer?.files?.length || current.busy || current.accepted)
      onAction({ action: 'card-reject' });
    else onAction({ action: 'card-use', slot, key });
  };
  host.addEventListener('pointerdown', pointerdown);
  host.ownerDocument.addEventListener?.('pointerup', pointerup);
  host.ownerDocument.addEventListener?.('pointercancel', pointerup);
  host.addEventListener('dragstart', dragstart);
  host.addEventListener('dragover', dragover);
  host.addEventListener('drop', drop);
  host.addEventListener('dragend', dragend);
  host.addEventListener('dragleave', dragleave);
  host.addEventListener('click', click);
  host.addEventListener('input', input);
  host.addEventListener('change', change);
  controller = {
    update(view) {
      current = view;
      // Permission reads can settle during a drag. Keep the source DOM stable;
      // the latest version is still checked before applying the drop.
      if (dragging || pointing) {
        redraw = true;
        return;
      }
      redraw = false;
      const focused = host.contains(host.ownerDocument.activeElement)
        ? host.ownerDocument.activeElement
        : null;
      const field = focused?.dataset?.placementField;
      const action = focused?.dataset?.placementAction;
      const card = focused?.dataset?.placementCard;
      const marker = focused?.dataset?.placementFocus;
      host.innerHTML = placementPanelHTML(current);
      // Retain the same labelled control. When acceptance or a conflict
      // removes/disables it, keep focus with this editor's readable status,
      // never on the document body or another editor's arbitrary first button.
      let target;
      if (card || field || action || marker)
        target = host.querySelector(
          `[data-placement-${card ? 'card' : field ? 'field' : action ? 'action' : 'focus'}="${card || field || action || marker}"]`
        );
      if (focused && (!target || target.disabled))
        target = host.querySelector('[data-placement-focus="status"]');
      target?.focus();
    },
    destroy() {
      clearTimeout(redrawTimer);
      host.removeEventListener('pointerdown', pointerdown);
      host.ownerDocument.removeEventListener?.('pointerup', pointerup);
      host.ownerDocument.removeEventListener?.('pointercancel', pointerup);
      host.removeEventListener('dragstart', dragstart);
      host.removeEventListener('dragover', dragover);
      host.removeEventListener('drop', drop);
      host.removeEventListener('dragend', dragend);
      host.removeEventListener('dragleave', dragleave);
      host.removeEventListener('click', click);
      host.removeEventListener('input', input);
      host.removeEventListener('change', change);
      host.replaceChildren();
    }
  };
  return controller;
}
