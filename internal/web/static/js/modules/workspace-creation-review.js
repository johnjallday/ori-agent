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

// Consume the existing roster component's normalized rows. This projection
// neither invents role fills nor changes the team's payload or saved profiles.
export function creationTeamReviewRows(view = {}, roleRows = []) {
  if (view.agentless)
    return [
      {
        label: 'Team',
        value: 'No agents will be created or attached.',
        action: 'team',
        actionLabel: 'Edit team'
      }
    ];
  const scoped = view.isAssistantProgram
    ? roleRows.filter(row => row.scope === 'project')
    : roleRows;
  if (scoped.length) {
    return [
      ...scoped.map(row => ({
        label: row.label,
        value: [
          ...new Set(
            [
              row.state === 'empty' ? 'No agent assigned' : '',
              row.tag?.label,
              row.sourceLabel,
              row.required ? 'Required' : 'Optional'
            ].filter(Boolean)
          )
        ].join(' · '),
        detail: [
          row.state === 'filled'
            ? [row.agent?.model, row.agent?.provider].filter(Boolean).join(' · ')
            : row.proposed?.model
              ? `Suggested model: ${row.proposed.model}`
              : '',
          row.scopeLine,
          row.readOnlyReason
        ]
          .filter(Boolean)
          .join(' · '),
        ...(row.readOnly ? {} : { action: 'team', actionLabel: 'Edit team' })
      })),
      ...(view.roleRoster?.unassigned || []).map(agent => ({
        label: 'Also in this workspace',
        value: `${agent.name} · saved agent`,
        action: 'team',
        actionLabel: 'Edit team'
      }))
    ];
  }
  const roster = Array.isArray(view.roster) ? view.roster : [];
  return [
    {
      label: 'Team',
      value: roster.length
        ? roster
            .map(member =>
              [
                member.name,
                member.lifecycle === 'reuse' ? 'saved agent' : 'new agent',
                member.modelLabel
              ]
                .filter(Boolean)
                .join(' · ')
            )
            .join('; ')
        : view.planStatus === 'ready'
          ? 'No agents will be created or attached.'
          : 'Team choices are not yet reviewed.',
      action: 'team',
      actionLabel: 'Edit team'
    }
  ];
}

export function creationReviewSlotsHTML(rows = [], { locked = false, selectedCard } = {}) {
  return `<div class="assisted-placement__slots">${rows
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

// A recognized plan's fixed rows are presentation only. The setup controller
// retains its brief/name setters, readiness, source hold and exact commit.
export function setupCreationReviewMaterial(session = {}, brief = '') {
  const plan = session.plan || {};
  const hire = plan.hire;
  const rows = [];
  if (hire)
    rows.push({
      label: hire.role_label || 'Plan-owned team',
      value: `${hire.agent_name || hire.name || 'Agent named by the plan'} · ${hire.mode === 'bind' ? 'Saved assignment' : hire.mode === 'create' ? 'New agent' : 'Set by this plan'}`,
      detail: [
        hire.model,
        plan.visual?.model?.provider,
        'Fixed by the canonical setup plan; saved definitions are not edited.'
      ]
        .filter(Boolean)
        .join(' · ')
    });
  if (session.folder?.observation)
    rows.push({
      label: 'Selected folder',
      value: session.folder.observation.folder || 'Held selection',
      detail: [
        session.folder_path,
        'Linked in place, not moved. The displayed path is not selection authority.'
      ]
        .filter(Boolean)
        .join(' · ')
    });
  return {
    rows,
    goal: brief,
    destination: plan.home?.name
      ? `${plan.home.status === 'existing' ? 'Existing' : 'New'} Home: ${plan.home.name}. Geometry never changes this destination.`
      : 'Standalone workspace · Position does not join a district.',
    facts: (Array.isArray(plan.lines) ? plan.lines : []).map(line => ({
      label:
        {
          workspace: 'Workspace',
          folder: 'Linked folder',
          agents: 'Team',
          mode: 'Working mode',
          task: 'First task',
          home: 'Home',
          placement: 'Placement'
        }[line.kind] || 'Setup step',
      value: [line.name, line.detail].filter(Boolean).join(' — ')
    })),
    later: plan.first_task_deferred
      ? 'The first task is prepared, not started. Run it from the workspace when you choose.'
      : 'This setup may run its disclosed first task. Review the exact host-owned steps above before confirming.'
  };
}

export function creationReviewNavigationHTML(view = {}) {
  const locked = Boolean(view.busy || view.conflict || view.closed || view.accepted);
  return `<div class="assisted-placement__navigation">
    <button type="button" class="${view.automatic ? '' : 'assisted-placement__primary'}" data-placement-action="place"${!view.canPlace || !view.reviewed || view.dirty || locked ? ' disabled' : ''}>${escapeReviewText(view.placeLabel || 'Place & create on map')}</button>
    <button type="button" data-placement-action="suggested" aria-pressed="${Boolean(view.automatic)}"${locked ? ' disabled' : ''}>Use automatic placement</button>
    ${view.canRefreshReview ? `<button type="button" data-placement-action="refresh-review"${locked || view.dirty ? ' disabled' : ''}>Refresh review</button>` : ''}
    </div><p class="assisted-placement__hint">${escapeReviewText(view.automatic ? 'Automatic placement selected. Confirm below to create this reviewed setup.' : view.mapHint)}</p>`;
}

const reviewMaxLength = value =>
  Number.isSafeInteger(value) && value >= 0 ? ` maxlength="${value}"` : '';

export function workspaceCreationReviewHTML(view = {}) {
  if (view.onMap && !view.accepted && !view.result) return '';
  const locked = Boolean(view.busy || view.accepted || view.conflict || view.closed);
  const disabled = locked ? ' disabled' : '';
  const prefix = view.idPrefix || 'workspaceCreationReview';
  const group = (view.result?.kind || view.subjectKind) === 'group';
  if (view.accepted || view.result) {
    return `<section class="assisted-placement" aria-label="${group ? 'Group' : 'Workspace'} creation result">
      <h2 id="${escapeReviewText(prefix)}Title" tabindex="-1">${escapeReviewText(view.resultTitle || (view.result ? (group ? 'Group created' : 'Workspace created') : 'Check confirmed creation'))}</h2>
      <p>${escapeReviewText(view.result?.name || '')}</p>
      ${view.result ? `<p>${group ? 'Group · Existing member workspaces are unchanged.' : view.result.parent_id ? `Group: ${escapeReviewText(view.resultParentName || view.result.parent_id)}` : 'Standalone workspace'}</p><p>${view.result.agent_instances?.length ? `Assigned: ${view.result.agent_instances.map(member => escapeReviewText(member.name)).join(', ')}` : 'No agents attached.'}</p>` : ''}
      <p role="status" aria-live="polite" tabindex="-1" data-placement-focus="status">${escapeReviewText(view.message)}</p>
      ${view.result && view.nextSetup ? `<p><strong>Next setup:</strong> ${escapeReviewText(view.nextSetup)}. Open the workspace to continue; setup readiness has not been verified here.</p>` : ''}
      ${view.accepted && !view.result ? '<p>Work may already exist. Closing is not undo. Reading status never continues creation.</p>' : ''}
      <div class="assisted-placement__footer">
        ${view.accepted && !view.result ? `<button type="button" data-placement-action="read-status"${view.busy ? ' disabled' : ''}>Read saved status</button>` : ''}
        ${view.canAbandonReview && !view.result ? `<button type="button" data-placement-action="abandon-review"${view.busy ? ' disabled' : ''}>Release unaccepted review</button>` : ''}
        ${view.positionFailed && view.result ? `<button type="button" data-placement-action="retry-position"${view.busy ? ' disabled' : ''}>Retry position only</button>` : ''}
        ${view.result ? `<button type="button" data-placement-action="open"${view.busy ? ' disabled' : ''}>${escapeReviewText(view.openLabel || (group ? 'Open group' : 'Open workspace'))}</button>` : ''}
        ${view.canShowOnMap ? `<button type="button" data-placement-action="show-map"${view.busy ? ' disabled' : ''}>Show on map</button>` : ''}
        ${view.canMoveGroup ? `<button type="button" data-placement-action="move-group"${view.busy ? ' disabled' : ''}>Move group</button>` : ''}
        <button type="button" data-placement-action="back"${view.busy ? ' disabled' : ''}>Close</button>
      </div></section>`;
  }
  return `<section class="assisted-placement" aria-label="Review proposed ${group ? 'group' : 'workspace'}">
    ${creationReviewHeaderHTML({ title: view.title || `Make room for ${view.name || 'your workspace'}`, titleId: `${prefix}Title`, note: view.note || 'Nothing is created until you confirm.', destination: view.destination, art: view.art })}
    <div class="assisted-placement__fields">
      <div><label for="${escapeReviewText(prefix)}Name">${group ? 'Group name' : 'Workspace name'}</label><input id="${escapeReviewText(prefix)}Name" data-placement-field="name"${reviewMaxLength(view.nameMaxLength)} value="${escapeReviewText(view.name)}"${view.canEditName === false ? ' readonly' : ''}${disabled}></div>
      <div><label for="${escapeReviewText(prefix)}Goal">${group ? 'Purpose' : 'Goal'}</label><textarea id="${escapeReviewText(prefix)}Goal" data-placement-field="mission" rows="2"${reviewMaxLength(view.goalMaxLength)}${view.canEditGoal === false ? ' readonly' : ''}${disabled}>${escapeReviewText(view.goal)}</textarea></div>
    </div>
    ${creationReviewSlotsHTML(view.rows, { locked })}
    <button type="button" data-placement-action="details"${disabled}>${escapeReviewText(view.detailsLabel || 'Edit details & advanced settings')}</button>
    ${view.canChangeBlueprint === false ? '' : `<button type="button" data-placement-action="blueprint"${disabled}>${group ? 'Change group blueprint' : 'Change blueprint'}</button>`}
    <section class="assisted-placement__review" aria-label="Before you confirm"><h3>Before you confirm</h3>
      <dl>${(view.facts || [])
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
