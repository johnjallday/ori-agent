// Skills import panel — the one-time offer to copy skills from ~/.agents/skills
// into the Workspace Directory's Skills folder. This module only builds the
// panel's HTML from its state; skills.js fetches, posts, and wires events.
(function (root) {
  'use strict';

  function escapeHTML(value) {
    return String(value ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // normalize keeps well-formed candidate rows only.
  function normalize(candidates) {
    return (Array.isArray(candidates) ? candidates : [])
      .filter(candidate => candidate && typeof candidate.name === 'string' && candidate.name.trim())
      .map(candidate => ({
        name: candidate.name.trim(),
        description: typeof candidate.description === 'string' ? candidate.description : '',
        sourceFolder: typeof candidate.source_folder === 'string' ? candidate.source_folder : '',
        alreadyPresent: candidate.already_present === true
      }));
  }

  function rowHTML(candidate, selected, busy) {
    const name = escapeHTML(candidate.name);
    const disabled = candidate.alreadyPresent || busy ? 'disabled' : '';
    const checked = !candidate.alreadyPresent && selected.has(candidate.name) ? 'checked' : '';
    const note = candidate.alreadyPresent
      ? '<small class="skills-import-note" style="color: var(--text-secondary);">Already in your Skills folder</small>'
      : '';
    return `
      <label class="skills-import-row d-flex gap-2 align-items-start py-1" style="${candidate.alreadyPresent ? 'opacity: 0.55;' : ''}">
        <input type="checkbox" class="form-check-input mt-1" data-import-name="${name}" ${checked} ${disabled}>
        <span style="min-width: 0;">
          <span style="font-weight: 600; color: var(--text-primary);">${name}</span>
          ${candidate.description ? `<span class="d-block" style="font-size: 12px; color: var(--text-secondary);">${escapeHTML(candidate.description)}</span>` : ''}
          ${note}
        </span>
      </label>`;
  }

  function resultHTML(result) {
    const imported = Array.isArray(result.imported) ? result.imported : [];
    const failed = Array.isArray(result.failed) ? result.failed : [];
    const lines = [];
    if (imported.length > 0) {
      lines.push(
        `<p class="mb-2" data-import-summary>Imported ${imported.length} skill${imported.length === 1 ? '' : 's'} into your Skills folder.</p>`
      );
    } else {
      lines.push('<p class="mb-2" data-import-summary>No skills were imported.</p>');
    }
    if (failed.length > 0) {
      lines.push('<p class="mb-1" style="font-weight: 600;">These could not be imported:</p>');
      lines.push(
        '<ul class="mb-2" data-import-failures>' +
          failed
            .map(
              failure =>
                `<li><strong>${escapeHTML(failure?.name)}</strong>: ${escapeHTML(failure?.reason)}</li>`
            )
            .join('') +
          '</ul>'
      );
    }
    lines.push(
      '<button type="button" class="modern-btn modern-btn-secondary btn-sm" data-import-action="done">Done</button>'
    );
    return lines.join('');
  }

  // render builds the whole panel. state: { candidates, selected (Set of
  // names), busy, result ({imported, failed} after an import), error }.
  function render(state) {
    const source = state && typeof state === 'object' ? state : {};
    const candidates = normalize(source.candidates);
    const selected = source.selected instanceof Set ? source.selected : new Set();
    const busy = source.busy === true;
    const header = `
      <div class="d-flex justify-content-between align-items-start gap-2 mb-2">
        <div>
          <h6 class="mb-1" style="color: var(--text-primary);">Import skills you already have</h6>
          <small style="color: var(--text-secondary);">Copies the skills you pick from ~/.agents/skills into your Workspace Directory's Skills folder. The originals are left untouched.</small>
        </div>
      </div>`;
    const error = source.error
      ? `<div class="alert alert-danger py-2 mb-2" style="font-size: 12px;">${escapeHTML(source.error)}</div>`
      : '';

    if (source.result) {
      return `${header}${error}${resultHTML(source.result)}`;
    }
    if (candidates.length === 0) {
      return `${header}${error}<p class="mb-2" style="color: var(--text-secondary);">No skills were found in ~/.agents/skills.</p>
        <button type="button" class="modern-btn modern-btn-secondary btn-sm" data-import-action="done">Close</button>`;
    }
    const anySelected = candidates.some(
      candidate => !candidate.alreadyPresent && selected.has(candidate.name)
    );
    return `${header}${error}
      <div class="skills-import-rows mb-2">${candidates.map(candidate => rowHTML(candidate, selected, busy)).join('')}</div>
      <div class="d-flex gap-2">
        <button type="button" class="modern-btn modern-btn-primary btn-sm" data-import-action="import" ${busy || !anySelected ? 'disabled' : ''}>${busy ? 'Importing...' : 'Import selected'}</button>
        <button type="button" class="modern-btn modern-btn-secondary btn-sm" data-import-action="dismiss" ${busy ? 'disabled' : ''}>Not now</button>
      </div>`;
  }

  root.SkillsImportPanel = { render, normalize, escapeHTML };
})(window);
