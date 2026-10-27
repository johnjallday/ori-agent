// Inert presentation shared by creation owners. Every value is a projection;
// only the existing controller may turn an intention into a material edit or
// confirmed operation. No requests, retained draft or executable plan lives here.
export const escapeReviewText = value =>
  String(value ?? '').replace(
    /[&<>"']/g,
    char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]
  );

export function creationReviewHeaderHTML({
  title,
  titleId,
  note,
  destination,
  prototype,
  art = ''
} = {}) {
  // art is an internal building variant, never caller-supplied SVG/HTML.
  const svg = globalThis.OriWorkspaceBuildingArt?.svgForVariant?.(art, { context: 'map' }) || '';
  return `<header class="assisted-placement__header">
    ${art ? `<div class="assisted-placement__art" aria-hidden="true">${svg}</div>` : ''}
    <div><p class="assisted-placement__eyebrow">${prototype ? 'Prototype · ' : ''}1 · Review setup</p>
      <h2${titleId ? ` id="${escapeReviewText(titleId)}" tabindex="-1"` : ''}>${escapeReviewText(title)}</h2><p>${escapeReviewText(note)}</p></div>
    </header><p class="assisted-placement__location">${escapeReviewText(destination)}</p>`;
}

export function creationReviewSlotsHTML(rows = [], { locked = false, selectedCard } = {}) {
  return `<div class="assisted-placement__slots">${rows
    .slice(0, 64)
    .map(
      (
        row,
        index
      ) => `<section aria-label="${escapeReviewText(row.ariaLabel || row.label)}" class="assisted-placement__slot"${row.slot ? ` data-placement-slot="${escapeReviewText(row.slot)}"` : ''}>
        <span class="assisted-placement__number" aria-hidden="true">${String(index + 1).padStart(2, '0')}</span>
        <div><h3>${escapeReviewText(row.label)}</h3><p>${escapeReviewText(row.value)}</p>${row.detail ? `<small>${escapeReviewText(row.detail)}</small>` : ''}</div>
        ${
          row.action
            ? `<div class="assisted-placement__slot-actions"><button type="button" data-placement-action="${escapeReviewText(row.action)}"${locked ? ' disabled' : ''}>${escapeReviewText(row.actionLabel)}</button>
          ${row.slot && selectedCard?.kind === row.slot ? `<button type="button" data-placement-action="card-use" data-placement-slot-use="${escapeReviewText(row.slot)}"${locked ? ' disabled' : ''}>Use selected ${escapeReviewText(row.slot)}</button>` : ''}</div>`
            : ''
        }
      </section>`
    )
    .join('')}</div>`;
}

export function creationReviewNavigationHTML(view = {}) {
  const locked = Boolean(view.busy || view.conflict || view.closed || view.accepted);
  return `<div class="assisted-placement__navigation">
    <button type="button" class="${view.automatic ? '' : 'assisted-placement__primary'}" data-placement-action="place"${!view.canPlace || !view.reviewed || view.dirty || locked ? ' disabled' : ''}>Place &amp; create on map</button>
    <button type="button" data-placement-action="suggested" aria-pressed="${Boolean(view.automatic)}"${locked ? ' disabled' : ''}>Use automatic placement</button>
    </div><p class="assisted-placement__hint">${escapeReviewText(view.automatic ? 'Automatic placement selected. Confirm below to create this reviewed setup.' : view.mapHint)}</p>`;
}

export function workspaceCreationReviewHTML(view = {}) {
  if (view.onMap && !view.accepted && !view.result) return '';
  const locked = Boolean(view.busy || view.accepted || view.conflict || view.closed);
  const disabled = locked ? ' disabled' : '';
  if (view.accepted || view.result) {
    return `<section class="assisted-placement" aria-label="Workspace creation result">
      <h2 id="workspaceCreationReviewTitle" tabindex="-1">${view.result ? 'Workspace created' : 'Check confirmed creation'}</h2>
      <p>${escapeReviewText(view.result?.name || '')}</p>
      ${view.result ? `<p>${view.result.parent_id ? `Group: ${escapeReviewText(view.result.parent_id)}` : 'Standalone workspace'}</p><p>${view.result.agent_instances?.length ? `Assigned: ${view.result.agent_instances.map(member => escapeReviewText(member.name)).join(', ')}` : 'No agents attached.'}</p>` : ''}
      <p role="status" aria-live="polite" tabindex="-1" data-placement-focus="status">${escapeReviewText(view.message)}</p>
      ${view.accepted && !view.result ? '<p>Work may already exist. Closing is not undo. Reading status never continues creation.</p>' : ''}
      <div class="assisted-placement__footer">
        ${view.accepted && !view.result ? `<button type="button" data-placement-action="read-status"${view.busy ? ' disabled' : ''}>Read saved status</button>` : ''}
        ${view.canAbandonReview && !view.result ? `<button type="button" data-placement-action="abandon-review"${view.busy ? ' disabled' : ''}>Release unaccepted review</button>` : ''}
        ${view.positionFailed && view.result ? `<button type="button" data-placement-action="retry-position"${view.busy ? ' disabled' : ''}>Retry position only</button>` : ''}
        ${view.result ? '<button type="button" data-placement-action="open">Open workspace</button>' : ''}
        <button type="button" data-placement-action="back"${view.busy ? ' disabled' : ''}>Close</button>
      </div></section>`;
  }
  return `<section class="assisted-placement" aria-label="Review proposed workspace">
    ${creationReviewHeaderHTML({ title: `Make room for ${view.name || 'your workspace'}`, titleId: 'workspaceCreationReviewTitle', note: view.note || 'Nothing is created until you confirm.', destination: view.destination, art: view.art })}
    <div class="assisted-placement__fields">
      <div><label for="workspaceCreationReviewName">Workspace name</label><input id="workspaceCreationReviewName" data-placement-field="name" maxlength="200" value="${escapeReviewText(view.name)}"${disabled}></div>
      <div><label for="workspaceCreationReviewGoal">Goal</label><textarea id="workspaceCreationReviewGoal" data-placement-field="mission" rows="2" maxlength="2000"${disabled}>${escapeReviewText(view.goal)}</textarea></div>
    </div>
    ${creationReviewSlotsHTML(view.rows, { locked })}
    <button type="button" data-placement-action="details"${disabled}>Edit details &amp; advanced settings</button>
    <section class="assisted-placement__review" aria-label="Before you confirm"><h3>Before you confirm</h3>
      <dl>${(view.facts || [])
        .slice(0, 64)
        .map(
          fact => `<dt>${escapeReviewText(fact.label)}</dt><dd>${escapeReviewText(fact.value)}</dd>`
        )
        .join('')}</dl>
    </section>
    <p class="assisted-placement__status" role="status" aria-live="polite" tabindex="-1" data-placement-focus="status">${escapeReviewText(view.message || '')}</p>
    ${creationReviewNavigationHTML(view)}
    <footer class="assisted-placement__footer">
      <button type="button" data-placement-action="back"${view.busy ? ' disabled' : ''}>Back</button>
      <button type="button" data-placement-action="cancel"${disabled}>Cancel draft</button>
      ${view.automatic ? `<button type="button" class="assisted-placement__primary" data-placement-action="confirm"${!view.canConfirm || !view.reviewed || view.dirty || locked ? ' disabled' : ''}>${escapeReviewText(view.busy ? 'Working…' : view.confirmLabel || 'Create workspace')}</button>` : ''}
    </footer></section>`;
}
