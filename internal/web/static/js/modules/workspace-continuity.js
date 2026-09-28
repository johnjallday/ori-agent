// Workspace continuity — Import Folder review, confirmed import and the
// workspace's "ready to move" status.
//
// Everything that decides what the user is told is a pure function of a server
// response, so it can be tested without the page. Text is always rendered with
// textContent by callers: folder names and assistant names come from a copied
// folder and are untrusted.
(function () {
  'use strict';

  const DOMAIN_LABELS = {
    workspace: 'Workspaces',
    agents: 'Agents',
    notes: 'Notes',
    assistant: 'Assistant and working agreement',
    followups: 'Follow-ups',
    brief_config: 'Daily Brief settings',
    brief_history: 'Daily Brief history',
    sessions: 'Conversations',
    uploads: 'Uploaded files',
    tool_history: 'Tool history',
    knowledge: 'Remembered items',
    setup: 'Setup progress'
  };

  const REASON_TEXT = {
    not_adopted: 'kept with the workspace; your current assistant is unchanged',
    not_in_copy: 'not in this copy',
    adapter_unavailable: 'not in this copy',
    uploads_store_unavailable: 'uploads cannot be restored in this build',
    some_files_unavailable: 'some linked or missing files need to be reconnected here',
    needs_review: 'some items need your review',
    inactive_until_adopted: 'kept, but not used unless this HQ becomes yours',
    count_unavailable: 'restored'
  };

  function count(value) {
    return Number.isSafeInteger(value) && value >= 0 ? value : 0;
  }

  function plural(n, one, many) {
    return `${n} ${n === 1 ? one : many || `${one}s`}`;
  }

  function truncate(value, max) {
    const text = typeof value === 'string' ? value : '';
    return text.length > max ? `${text.slice(0, max - 1)}…` : text;
  }

  // describeReview turns an /api/workspaces/import/check "continuity" object
  // into what the Import Folder modal shows. Legacy folders keep the existing
  // import path (mode 'legacy').
  function describeReview(review) {
    const status = review?.status || 'unavailable';
    if (status === 'legacy') return { mode: 'legacy', title: '', lines: [], actions: [] };
    if (status !== 'review_required') {
      return {
        mode: 'blocked',
        title: 'This folder cannot be imported safely',
        lines: [
          'Ori could not verify this folder’s saved copy of its work (it may be incomplete, changed while copying, or damaged). Nothing was imported. Copy the folder again from the other computer after it shows “Ready to move”, then try again.'
        ],
        actions: []
      };
    }
    if (review.already_imported && review.recommended_action === 'open') {
      return {
        mode: 'already',
        title: 'Already imported',
        lines: [
          'This exact folder was imported before. Nothing will change; open the workspace to continue.'
        ],
        actions: [{ action: 'open', label: 'Open workspace', primary: true }],
        // Workspace pages are addressed by folder slug.
        workspaceId:
          review.already_imported.workspace_slug ||
          review.already_imported.workspace_id ||
          review.workspace_id ||
          ''
      };
    }
    if (review.conflict === 'workspace_exists') {
      return {
        mode: 'blocked',
        title: 'This workspace is already in Ori',
        lines: [
          'A workspace in this folder already exists here. Importing it again would replace newer work, so nothing will be changed. Open the existing workspace instead.'
        ],
        actions: []
      };
    }
    if (review.import_supported !== true) {
      return {
        mode: 'blocked',
        title: 'Restoring saved work is not available here',
        lines: [
          'This folder has saved history, but this Ori cannot restore it. Nothing was imported.'
        ],
        actions: []
      };
    }
    const history = review.history || {};
    const saved = review.saved_work || {};
    const lines = [];
    const workParts = [
      plural(count(saved.tasks), 'task'),
      plural(count(history.sessions), 'conversation'),
      plural(count(history.messages), 'message'),
      plural(count(history.follow_ups), 'follow-up'),
      plural(count(history.brief_revisions), 'Daily Brief', 'Daily Briefs'),
      plural(count(history.notes), 'note'),
      plural(count(history.uploads), 'uploaded file')
    ];
    const members = count(review.members);
    lines.push(
      `Saved work${members > 1 ? ` across ${plural(members, 'workspace')}` : ''}: ${workParts.join(', ')}.`
    );
    if (review.checkpoint_at) {
      const when = new Date(review.checkpoint_at);
      if (!Number.isNaN(when.getTime())) {
        lines.push(`Saved on the other computer ${when.toLocaleString()}.`);
      }
    }
    const candidates = Array.isArray(review.assistant_candidates)
      ? review.assistant_candidates
      : [];
    const assistantName = truncate(candidates[0]?.display_name, 80);
    const actions = [];
    const offered = Array.isArray(review.actions) ? review.actions : [];
    const retry = Boolean(review.already_imported);
    let title = retry ? 'Finish importing this workspace' : 'Import this workspace';
    if (offered.includes('continue')) {
      title = retry ? `Finish restoring ${assistantName}` : `Continue with ${assistantName}?`;
      lines.push(
        `Restores Personal HQ, ${assistantName}’s working agreement, commitments, conversations and Daily Brief history from this copy.`
      );
      actions.push({
        action: 'continue',
        label: retry ? 'Retry import' : 'Import and continue',
        primary: true
      });
    }
    if (offered.includes('workspace_only')) {
      if (review.adoption_blocked === 'existing_assistant' && assistantName) {
        title = 'Your current assistant will stay unchanged';
        lines.push(
          `This folder contains another Personal HQ (${assistantName}). Its workspace and history can be imported without making it your personal assistant.`
        );
      } else if (review.adoption_blocked === 'multiple_assistants') {
        lines.push(
          'This folder contains more than one Personal HQ, so none will become your personal assistant. Their workspaces and history can be imported.'
        );
      }
      actions.push({
        action: 'workspace_only',
        label: retry
          ? 'Retry import'
          : offered.includes('continue')
            ? 'Import workspace only'
            : 'Import workspace',
        primary: !offered.includes('continue')
      });
    }
    lines.push(
      'Background routines stay off until you turn them on here. Accounts, API keys and permissions are not copied; set them up on this computer.'
    );
    lines.push(
      'This folder contains private conversations, follow-ups, briefs and uploaded files. Treat copies of it as personal data.'
    );
    return { mode: 'review', title, lines, actions };
  }

  // describeReport summarizes a finished (or interrupted) import in the three
  // groups the user needs: restored work, what this copy did not include, and
  // what to set up here. Missing history is never presented as empty.
  function describeReport(report) {
    const restored = [];
    const missing = [];
    const setup = [];
    const members = Array.isArray(report?.members) ? report.members : [];
    const totals = {};
    for (const member of members) {
      for (const outcome of Array.isArray(member.components) ? member.components : []) {
        const label = DOMAIN_LABELS[outcome.domain] || outcome.domain;
        if (outcome.status === 'restored') {
          totals[label] = (totals[label] || 0) + count(outcome.count);
          if (outcome.reason && outcome.reason !== 'count_unavailable') {
            setup.push(`${label}: ${REASON_TEXT[outcome.reason] || 'needs review'}`);
          }
        } else if (outcome.status === 'unavailable' || outcome.status === 'unsupported') {
          const reason = outcome.reason || '';
          if (reason === 'not_adopted') {
            setup.push(`${label}: ${REASON_TEXT.not_adopted}`);
          } else {
            missing.push(`${label}: ${REASON_TEXT[reason] || 'not in this copy'}`);
          }
        } else if (outcome.status !== 'restoring') {
          missing.push(`${label}: could not be restored (${outcome.status})`);
        }
      }
    }
    for (const [label, n] of Object.entries(totals)) {
      if (n > 0) restored.push(`${label}: ${n}`);
    }
    if (report?.adopted && report.assistant_name) {
      setup.unshift(
        `${truncate(report.assistant_name, 80)} is your personal assistant again. It starts paused; resume it when you are ready.`
      );
    }
    setup.push('Background routines are off for imported workspaces until you turn them on.');
    const complete = report?.status === 'complete';
    return {
      complete,
      title: complete
        ? missing.length
          ? 'Workspace restored — some information was not in this folder'
          : 'Workspace restored'
        : 'The import stopped before it finished',
      restored,
      missing: [...new Set(missing)],
      setup: [...new Set(setup)],
      retryable: !complete
    };
  }

  // describeStatus explains a workspace's portable checkpoint for its settings
  // view: can this folder be copied to another computer right now?
  function describeStatus(status) {
    const state = status?.state || 'unavailable';
    const when = status?.checkpoint_at ? new Date(status.checkpoint_at) : null;
    const asOf = when && !Number.isNaN(when.getTime()) ? when.toLocaleString() : '';
    const children = Array.isArray(status?.children) ? status.children : [];
    let label;
    let detail;
    switch (state) {
      case 'ready':
        label = asOf ? `Ready to move as of ${asOf}` : 'Ready to move';
        detail =
          status.tree_ready || children.length === 0
            ? 'Copy this whole folder to move this workspace with its history.'
            : 'A workspace inside this one is still being prepared. Wait before copying.';
        break;
      case 'preparing':
        label = 'Preparing';
        detail = 'Ori is saving this workspace’s latest work into its folder.';
        break;
      case 'update_needed':
        // Automatic: the latest save is folded in once edits pause.
        label = 'Saving latest work';
        detail = asOf
          ? `The last complete copy is from ${asOf}; newer work will be added shortly. Wait for “Ready to move” before copying.`
          : 'Ori will prepare this folder shortly. Wait for “Ready to move” before copying.';
        break;
      default:
        label = 'Unavailable';
        detail = unavailableReason(status?.reason);
    }
    return {
      state,
      label,
      detail,
      imported: Boolean(status?.imported),
      backgroundAllowed: Boolean(status?.background_allowed),
      canActivate: status?.attachment === 'imported_inactive',
      canDeactivate: status?.attachment === 'imported_active',
      version: count(status?.attachment_version)
    };
  }

  function unavailableReason(reason) {
    const code = String(reason || '');
    if (code === 'not_attached') return 'This workspace is still being imported.';
    if (code.endsWith('_limit')) return 'This folder is larger than a portable copy can hold.';
    if (code.endsWith('_unsafe'))
      return 'The folder contains a link or a file name that cannot be copied faithfully. Replace links with real files.';
    if (code.startsWith('assistant_'))
      return 'The assistant’s setup is unfinished. Finish or repair it, then Ori will prepare this folder.';
    if (code.startsWith('agents_'))
      return 'An agent in this workspace is missing its saved profile. Open the workspace so Ori can save it.';
    if (code.startsWith('knowledge_'))
      return 'The assistant’s remembered items in this folder could not be read.';
    return 'Ori could not prepare this folder yet. It will retry automatically.';
  }

  async function confirmImport(fetchImpl, path, review, action) {
    const response = await fetchImpl('/api/workspaces/import/continuity', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        path,
        tree_digest: review?.tree_digest || '',
        destination_digest: review?.destination_digest || '',
        action
      })
    });
    const result = await response.json().catch(() => ({}));
    return { ok: response.ok && result.success !== false, status: response.status, result };
  }

  const api = { describeReview, describeReport, describeStatus, confirmImport, DOMAIN_LABELS };
  if (typeof window !== 'undefined') window.WorkspaceContinuity = api;
  if (typeof globalThis !== 'undefined') globalThis.WorkspaceContinuity = api;
})();
