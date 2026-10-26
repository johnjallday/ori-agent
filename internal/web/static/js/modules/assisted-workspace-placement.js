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

export function placementPanelHTML(view = {}) {
  const draft = view.draft || {};
  const review = view.review;
  const busy = Boolean(view.busy);
  const disabled = busy ? ' disabled' : '';
  const art =
    globalThis.OriWorkspaceBuildingArt?.svgForVariant?.('depot', { context: 'map' }) || '';
  const team = review?.team || view.team || [];
  const teamText = team
    .map(member => `${member.name} · ${member.action === 'create' ? 'new agent' : 'saved agent'}`)
    .join(', ');
  const folder = review?.folder || view.folder || {};
  const workspace = review?.workspace || draft;
  return `
    <section class="assisted-placement" aria-label="Review proposed workspace">
      <header class="assisted-placement__header">
        <div class="assisted-placement__art" aria-hidden="true">${art}</div>
        <div><p class="assisted-placement__eyebrow">${view.prototype ? 'Prototype · ' : ''}Draft workspace</p>
          <h2>Make room for ${escape(draft.name || 'your folder')}</h2>
          <p>Suggested by your assistant. Nothing is created until you confirm.</p></div>
      </header>
      <p class="assisted-placement__location">Standalone workspace · Position does not join a district.</p>
      <div class="assisted-placement__fields">
        <label>Workspace name<input name="name" data-placement-field="name" maxlength="200" value="${escape(draft.name)}"${disabled}></label>
        <label>Goal<textarea name="mission" data-placement-field="mission" rows="2" maxlength="2000"${disabled}>${escape(draft.mission)}</textarea></label>
      </div>
      <div class="assisted-placement__slots">
        <section aria-label="File Curator role" class="assisted-placement__slot">
          <span class="assisted-placement__number" aria-hidden="true">01</span>
          <div><h3>File Curator</h3><p>${escape(teamText || 'Choose an agent')}</p></div>
          <button type="button" data-placement-action="agent"${disabled}>Choose agent</button>
        </section>
        <section aria-label="Linked folder" class="assisted-placement__slot">
          <span class="assisted-placement__number" aria-hidden="true">02</span>
          <div><h3>Linked folder</h3><p>${escape(folder.name || 'Choose a folder')}</p><small>${escape(folder.path)}</small></div>
          <button type="button" data-placement-action="folder"${disabled}>Choose folder</button>
        </section>
        <section aria-label="First action" class="assisted-placement__slot">
          <span class="assisted-placement__number" aria-hidden="true">03</span>
          <div><h3>Prepare a filing review</h3><p>A metadata-only scan. You approve any filing separately.</p></div>
        </section>
      </div>
      <label class="assisted-placement__monitoring"><input type="checkbox" data-placement-field="monitoring"${view.monitoring ? ' checked' : ''}${disabled}> Also watch for new files and run a daily catch-up</label>
      <p class="assisted-placement__hint">${view.monitoring ? 'Monitoring is selected. Review the exact schedule below before confirming.' : 'Monitoring is off. No background scans will be enabled.'}</p>
      <div class="assisted-placement__navigation">
        <button type="button" data-placement-action="place"${disabled}>${view.candidate ? 'Adjust position' : 'Place on map'}</button>
        <button type="button" data-placement-action="suggested"${disabled}>Use suggested setup</button>
      </div>
      <p class="assisted-placement__hint">${view.candidate ? `Requested position: ${escape(view.candidate.x)}, ${escape(view.candidate.y)}. Saved only after creation.` : 'Without manual placement, Ori uses its automatic map layout. Your setup choices stay the same.'}</p>
      <section class="assisted-placement__review" aria-label="Before you confirm">
        <h3>Before you confirm</h3>
        ${
          review
            ? `<dl>
          <dt>Workspace</dt><dd>${escape(workspace.name)} · standalone</dd>
          <dt>Goal</dt><dd>${escape(workspace.mission)}</dd>
          <dt>Team</dt><dd>${escape(teamText)}</dd>
          <dt>Source</dt><dd>${escape(folder.path || folder.name)} — linked, not moved</dd>
          <dt>Access</dt><dd>Top-level names, types, sizes and dates only. No file contents.</dd>
          <dt>Destination</dt><dd>Create or reuse Filed/ inside this folder.</dd>
          <dt>Next</dt><dd>Prepare the first filing review. No model call. Nothing is moved or deleted.</dd>
          <dt>Monitoring</dt><dd>${review.monitoring ? `On · watch for new files; daily catch-up at ${escape(review.terms?.daily_scan_local_time || 'the reviewed time')}.` : 'Off · manual scans only.'}</dd>
        </dl>`
            : '<p>Your current choices need a fresh server review before you can confirm.</p>'
        }
      </section>
      <p class="assisted-placement__status" role="status" aria-live="polite">${escape(view.message || '')}</p>
      <footer class="assisted-placement__footer">
        <button type="button" data-placement-action="cancel"${disabled}>Cancel draft</button>
        <button type="button" class="assisted-placement__primary" data-placement-action="confirm"${!view.canConfirm || !review || busy ? ' disabled' : ''}>${busy ? 'Working…' : 'Create workspace & scan'}</button>
      </footer>
    </section>`;
}

// All values are rendered as text. Only host-owned building SVG is markup.
// The integration owner rechecks version/review on every intention, including
// a final click; disabled controls are feedback, never a security boundary.
export function mountPlacementPanel(host, onAction) {
  let current = {};
  const click = event => {
    const control = event.target.closest?.('[data-placement-action]');
    if (!control || !host.contains(control) || control.disabled) return;
    onAction({ action: control.dataset.placementAction });
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
  host.addEventListener('click', click);
  host.addEventListener('change', change);
  return {
    update(view) {
      current = view;
      const focused = host.contains(host.ownerDocument.activeElement)
        ? host.ownerDocument.activeElement
        : null;
      const field = focused?.dataset?.placementField;
      const action = focused?.dataset?.placementAction;
      host.innerHTML = placementPanelHTML(current);
      // Retain the same labelled control, not an arbitrary first button.
      if (field || action)
        host
          .querySelector(`[data-placement-${field ? 'field' : 'action'}="${field || action}"]`)
          ?.focus();
    },
    destroy() {
      host.removeEventListener('click', click);
      host.removeEventListener('change', change);
      host.replaceChildren();
    }
  };
}
