// Presentation adapter only. GroupTemplateCreator retains the name/role
// setters, frozen commit, staffing and canonical Home protocol. Stored data is
// an immutable lookup reference: it is never restored as executable consent.
import { mountPlacementPanel, creationTeamReviewRows } from './assisted-workspace-placement.js';

const REFERENCE_KEY = 'ori:managed-home-operation:v1';
const OBSERVE_URL = '/api/workspaces/group-templates/observe';

export function managedHomeReviewKey(manager, creator) {
  const context = manager.workspaceCreatorContext;
  const state = context?.groupTemplates;
  const entry = creator.selectedManaged(context);
  return JSON.stringify([
    context?.generation,
    creator.reviewRequest(entry, document.getElementById('folderNameInput')?.value),
    state?.review?.data?.review_token,
    creator.staffingPlan(manager)
  ]);
}

function stateFor(manager) {
  const context = manager.workspaceCreatorContext;
  const state = context?.groupTemplates;
  if (!state) return null;
  if (state.sharedReview?.generation !== context.generation) {
    state.sharedReview?.panel?.destroy();
    state.sharedReview = { generation: context.generation, automatic: false, dirty: false };
  }
  return state.sharedReview;
}

// As before the shared review: a launcher's landing (e.g. its folder offer)
// applies only when nothing needs the group page's notice, such as a role
// that failed to staff. Otherwise Open lands on the group itself.
export function managedHomeLauncherLanding(ui) {
  return ui?.landing && ui.notice?.tone === 'success' ? ui.landing : '';
}

export function forgetManagedHomeReference(storage, reference) {
  const raw = storage.getItem(REFERENCE_KEY);
  if (!raw) return;
  const current = JSON.parse(raw);
  // Closing an older result in another tab cannot erase a newer operation's
  // lookup reference. This cache is never the domain acceptance authority.
  if (
    current?.request?.idempotency_key === reference?.request?.idempotency_key &&
    current?.request?.group_review_token === reference?.request?.group_review_token
  ) {
    storage.removeItem(REFERENCE_KEY);
  }
}

function forgetReference(ui) {
  forgetManagedHomeReference(window.localStorage, ui.reference);
  ui.reference = null;
}

function savedReference() {
  const raw = window.localStorage.getItem(REFERENCE_KEY);
  if (!raw) return null;
  const value = JSON.parse(raw);
  const request = value?.request;
  if (
    !request ||
    ['group_template_id', 'revision', 'name', 'group_review_token', 'idempotency_key'].some(
      key => typeof request[key] !== 'string' || !request[key] || request[key].length > 256
    )
  ) {
    throw new Error(
      'The saved Home reference is unreadable. Do not submit a replacement creation.'
    );
  }
  // Only the original strict lookup fields are ever sent to the read owner.
  return {
    ...value,
    request: Object.fromEntries(
      ['group_template_id', 'revision', 'name', 'group_review_token', 'idempotency_key'].map(
        key => [key, request[key]]
      )
    )
  };
}

async function canonicalHome(result) {
  if (result?.state !== 'home_ready' || !result.home_workspace_id) {
    throw new Error(
      'The original Home outcome is not complete. Reading status has resumed nothing.'
    );
  }
  const response = await fetch(`/api/workspaces/${encodeURIComponent(result.home_workspace_id)}`, {
    headers: { Accept: 'application/json' }
  });
  const home = await response.json().catch(() => null);
  if (
    !response.ok ||
    home?.id !== result.home_workspace_id ||
    home?.kind !== 'group' ||
    !home.assistant_program?.state ||
    ['trashed', 'missing'].includes(home.status)
  ) {
    throw new Error(
      'The original Home resource could not be verified. Nothing was recreated or repositioned.'
    );
  }
  return home;
}

export async function readManagedHomeStatus(manager, creator) {
  const context = manager.workspaceCreatorContext;
  const ui = stateFor(manager);
  if (!ui?.reference || ui.busy) return false;
  ui.busy = true;
  try {
    const response = await fetch(OBSERVE_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(ui.reference.request)
    });
    const result = (await response.json().catch(() => ({})))?.group_template;
    if (response.status !== 200)
      throw new Error(
        result?.summary ||
          'The original Home operation could not be observed. Do not confirm again.'
      );
    const home = await canonicalHome(result);
    if (manager.workspaceCreatorContext !== context) return false;
    ui.result = home;
    ui.layoutSubjectNew = result.layout_subject_new === true;
    ui.message =
      'The original Home is ready. Its actual agents are shown. Reading status did not resume staffing, setup, tasks or positioning. Open the group to review any unfinished roles.';
    if (ui.reference.candidate && ui.layoutSubjectNew) {
      const layoutResponse = await fetch('/api/workspace-map/layout', {
        headers: { Accept: 'application/json' }
      });
      if (!layoutResponse.ok)
        throw new Error(
          'The Home is ready, but its position could not be observed. Nothing was moved. Read status again.'
        );
      const layout = (await layoutResponse.json()).layout;
      if (manager.workspaceCreatorContext !== context) return false;
      const position = layout?.positions?.[home.id];
      const candidate = ui.reference.candidate;
      ui.positionFailed = !position;
      ui.message += position
        ? position.x === candidate.x && position.y === candidate.y
          ? ' Its original requested Group position is saved.'
          : ' It now has a different saved position, which this read left unchanged. Use the normal Map move control if needed.'
        : ' Its requested position is not saved; automatic layout remains. Retry position only to use that exact point.';
    }
    return true;
  } catch (error) {
    if (manager.workspaceCreatorContext === context) ui.message = error.message;
    return false;
  } finally {
    ui.busy = false;
    if (manager.workspaceCreatorContext === context) void syncManagedHomeReview(manager, creator);
  }
}

export async function confirmManagedHomeReview(manager, creator, reviewedKey, placement = null) {
  const context = manager.workspaceCreatorContext;
  const state = context?.groupTemplates;
  const ui = stateFor(manager);
  if (
    !ui ||
    ui.busy ||
    ui.accepted ||
    ui.dirty ||
    !creator.canSubmit(manager) ||
    reviewedKey !== managedHomeReviewKey(manager, creator)
  )
    return false;
  ui.busy = true;
  void syncManagedHomeReview(manager, creator);
  try {
    return await creator.submit(manager, {
      onAccepted({ request }) {
        const reference = {
          request,
          reviewDigest: state.review?.data?.review_digest,
          candidate: placement?.candidate || null
        };
        // If this cannot be retained, the existing owner sends nothing.
        try {
          const prior = savedReference();
          if (
            prior &&
            (prior.request.idempotency_key !== request.idempotency_key ||
              prior.request.group_review_token !== request.group_review_token)
          ) {
            ui.reference = prior;
            ui.accepted = true;
            throw new Error(
              'Another confirmed Home operation is retained. Read its original status before confirming anything else.'
            );
          }
          window.localStorage.setItem(REFERENCE_KEY, JSON.stringify(reference));
        } catch (error) {
          ui.message =
            error?.message || 'The confirmed reference could not be retained. Nothing was sent.';
          throw error;
        }
        ui.reference = reference;
        ui.accepted = true;
        void syncManagedHomeReview(manager, creator);
      },
      onRefused() {
        forgetReference(ui);
        ui.accepted = false;
      },
      async onResult({ result, notice, landing }) {
        const home = await canonicalHome(result);
        if (manager.workspaceCreatorContext !== context) return;
        ui.result = home;
        ui.notice = notice;
        // A launcher's own landing (for example its folder offer) is kept for
        // Open; the creator never navigates by itself.
        ui.landing = typeof landing === 'string' ? landing : '';
        ui.layoutSubjectNew = result.layout_subject_new === true;
        ui.message = notice.message;
        if (placement) await manager.finishManagedHomePlacement?.(context, placement, ui);
      }
    });
  } finally {
    ui.busy = false;
    if (ui.accepted && !ui.result) {
      ui.message =
        'Confirmation delivery is unknown or incomplete. Read the original saved status; do not confirm again. Closing is not undo.';
    }
    if (placement && manager.workspaceCreatorContext === context) {
      manager.resumeManagedHomeReview?.(context, placement, intent =>
        act(manager, creator, context, intent)
      );
    }
    if (manager.workspaceCreatorContext === context) void syncManagedHomeReview(manager, creator);
  }
}

async function act(manager, creator, context, intent) {
  if (manager.workspaceCreatorContext !== context) return;
  const ui = stateFor(manager);
  if (!ui || ui.busy) return;
  const { action, field, value } = intent;
  if (action === 'read-status') return readManagedHomeStatus(manager, creator);
  if (action === 'retry-position' && ui.result && ui.layoutSubjectNew && ui.reference?.candidate) {
    if (
      !(await readManagedHomeStatus(manager, creator)) ||
      manager.workspaceCreatorContext !== context ||
      !ui.positionFailed
    )
      return;
    ui.busy = true;
    try {
      const saved = await window.OriWorkspaceMap?.retryPlacement?.(
        ui.result.id,
        ui.reference.candidate,
        { kind: 'group' }
      );
      ui.positionFailed = saved !== true;
      ui.message = saved
        ? 'The original Home Group position was saved. No creation, staffing, membership, setup or task was repeated.'
        : 'The original point is unavailable or its save failed. The Home is unchanged; no creation or staffing was repeated.';
    } finally {
      ui.busy = false;
      if (manager.workspaceCreatorContext === context) void syncManagedHomeReview(manager, creator);
    }
    return;
  }
  if (action === 'open' && ui.result) {
    forgetReference(ui);
    const landing = managedHomeLauncherLanding(ui);
    if (landing) {
      window.location.href = landing;
      return true;
    }
    return creator.openSharedResult(manager, ui.result, ui.notice);
  }
  if (action === 'back' || action === 'cancel') {
    if (ui.result) forgetReference(ui);
    if (ui.accepted || action === 'cancel') {
      bootstrap.Modal.getInstance(document.getElementById('addFolderModal'))?.hide();
    } else manager.goToWizardStep(3);
    return;
  }
  if (ui.accepted) return;
  if (action === 'refresh-review') {
    ui.busy = true;
    ui.message = '';
    context.groupTemplates.review = null;
    try {
      await creator.load(manager, { force: true });
      if (manager.workspaceCreatorContext !== context) return;
      await creator.ensureReview(manager);
    } finally {
      ui.busy = false;
      if (manager.workspaceCreatorContext === context) void syncManagedHomeReview(manager, creator);
    }
    return;
  }
  if (action === 'dirty') ui.dirty = true;
  if (action === 'edit' && field === 'name') {
    const input = document.getElementById('folderNameInput');
    if (input?.readOnly) return;
    if (input) input.value = value;
    ui.dirty = false;
    manager.updateWorkspaceNameHint?.();
    await creator.ensureReview(manager);
  }
  if (['team', 'details', 'blueprint'].includes(action)) {
    manager.goToWizardStep(action === 'team' ? 3 : action === 'blueprint' ? 1 : 2);
    return;
  }
  if (action === 'suggested') {
    manager.abandonWorkspaceMapPlacement?.();
    ui.automatic = true;
  }
  if (action === 'place' && !ui.dirty && creator.canSubmit(manager)) {
    return manager.beginManagedHomeMapPlacement?.(context, ui.reviewedKey);
  }
  if (action === 'confirm' && ui.automatic) {
    return confirmManagedHomeReview(manager, creator, ui.reviewedKey);
  }
  void syncManagedHomeReview(manager, creator);
}

export async function restoreManagedHomeReview(manager, creator) {
  const context = manager.workspaceCreatorContext;
  const state = context?.groupTemplates;
  if (
    !state ||
    state.pending ||
    state.sharedReview?.accepted ||
    manager.isCreatingFolder ||
    context.submitting
  )
    return false;
  let reference;
  let unreadable = '';
  try {
    reference = savedReference();
    if (!reference) return false;
  } catch (error) {
    unreadable = error.message;
  }
  // This is a read-only retained-operation view, not an available blueprint or
  // an executable selection. Recovery must not depend on an enabled source.
  const id = reference?.request.group_template_id || 'unreadable-home-reference';
  if (!state.items.some(item => item.id === id)) {
    state.items.push({
      id,
      kind: 'managed_home',
      name: 'Confirmed Home operation',
      availability: { state: 'unavailable' }
    });
  }
  state.selectedId = id;
  const ui = stateFor(manager);
  ui.restored = true;
  ui.accepted = true;
  ui.reference = reference;
  ui.message =
    unreadable ||
    'Read the original confirmed Home. Its source need not be enabled; reading status never resumes staffing or setup.';
  if (reference)
    state.pending = { request: reference.request, observationOnly: true, uncertain: true };
  await syncManagedHomeReview(manager, creator);
  return true;
}

export async function syncManagedHomeReview(manager, creator) {
  const context = manager.workspaceCreatorContext;
  const state = context?.groupTemplates;
  const entry = creator.selectedManaged(context);
  const host = document.getElementById('workspaceCreationReviewPanel');
  if (!state || !entry || !host || !creator.managedActive(manager)) return;
  const ui = stateFor(manager);
  // This adapter replaces only presentation; never leave a second owner
  // listening to the same shared host after General -> managed selection.
  context.ordinaryReview?.panel?.destroy();
  if (context.ordinaryReview) context.ordinaryReview.panel = null;
  host.hidden = false;
  const summary = document.getElementById('workspaceReviewSummary');
  if (summary) summary.hidden = true;
  if (!ui.restored) {
    ui.restored = true;
    try {
      const reference = savedReference();
      if (reference) {
        ui.reference = reference;
        ui.accepted = true;
        // Restored bytes can only be observed, never submitted/staffed.
        state.pending = { request: reference.request, observationOnly: true, uncertain: true };
        ui.message =
          'Read the original confirmed Home before starting another. Reading status never resumes staffing or setup.';
      }
    } catch (error) {
      ui.accepted = true;
      ui.message = error.message;
    }
  }
  if (ui.accepted && manager.wizardStep !== manager.wizardStepCount) {
    // Restore only the original read-only result, not a new editable draft.
    // A retained operation must not strand the editor behind a stage guard.
    manager.wizardStep = manager.wizardStepCount;
    manager.refreshWizardChrome();
  }
  if (!ui.panel)
    ui.panel = mountPlacementPanel(host, intent => {
      void act(manager, creator, context, intent).catch(error => {
        if (manager.workspaceCreatorContext === context) {
          ui.message = error.message;
          void syncManagedHomeReview(manager, creator);
        }
      });
    });
  const reuse = Boolean(state.review?.data?.reuse || creator.isReusable(entry));
  const name = reuse
    ? state.review?.data?.home_name || entry.home?.name
    : document.getElementById('folderNameInput')?.value || '';
  const rows = window.WorkspaceRoleRoster?.rowsFrom(creator.teamRoster(manager)) || [];
  const staffing = creator.staffingPlan(manager);
  const sourceProblem =
    !creator.isSelectable(entry) || state.catalogUnavailable
      ? state.error ||
        'This Home source is unavailable. Your edits are kept. Refresh its source or explicitly choose another blueprint; nothing was created.'
      : '';
  const canPlace =
    !sourceProblem &&
    !reuse &&
    Boolean(manager.beginManagedHomeMapPlacement && manager.ordinaryCreationMapAvailable());
  if (!canPlace) ui.automatic = true;
  if (!ui.accepted) ui.reviewedKey = managedHomeReviewKey(manager, creator);
  ui.panel.update({
    ownerKind: 'ordinary',
    subjectKind: 'group',
    name,
    goal: entry.description || 'A separately owned program Home.',
    canEditName: !reuse,
    canEditGoal: false,
    nameMaxLength: 120,
    title: reuse ? `Prepare existing ${name}` : `Make room for ${name || entry.name}`,
    destination: reuse
      ? `Existing Home: ${name}. Name, members and position are preserved.`
      : `New Home Group: ${name || entry.name}. No project workspace is created.`,
    rows: creationTeamReviewRows({}, rows),
    facts: [
      { label: 'Blueprint', value: creator.templateMeta(entry) },
      {
        label: 'Now',
        value: `${reuse ? 'Reuse the existing Home' : 'Create one Home Group'}${staffing.length ? `, then staff ${staffing.map(item => item.label).join(', ')}` : '; no roles are staffed'}. No project, scan, schedule, task, tool access or application opening.`
      },
      {
        label: 'Team',
        value:
          'Roles are staffed one at a time by the existing Home role owner. Partial staffing stays partial; existing holders are never replaced.'
      },
      {
        label: 'Later',
        value:
          'Projects, connections, Home profile and runtime setup keep their separate reviews. Required roles left empty still need setup.'
      }
    ],
    canRefreshReview: Boolean(sourceProblem || state.review?.status === 'error'),
    reviewed: creator.canSubmit(manager),
    canConfirm: creator.canSubmit(manager) && !creator.identityProblem(manager),
    canPlace,
    automatic: ui.automatic,
    dirty: ui.dirty,
    busy: ui.busy || manager.isCreatingFolder,
    accepted: ui.accepted,
    result: ui.result,
    positionFailed: ui.positionFailed,
    resultTitle: ui.result ? 'Home ready' : 'Check confirmed Home',
    message:
      ui.message ||
      sourceProblem ||
      state.review?.error ||
      'Nothing is created or staffed until your final confirmation.',
    confirmLabel: creator.ctaLabel(manager),
    mapHint: reuse
      ? 'A reused Home is never repositioned.'
      : 'Use automatic placement to finish here without a mounted Map.'
  });
}
