import { openSetupWorkspaceCreator } from './setup-workspace-creator.js';
import {
  MODEL_SETTINGS_URL,
  SETTINGS_TAB_NOTE,
  accountActionLabel,
  accountLinkReviewPresentation,
  accountSettingsURL,
  accountStepReceiptRows,
  accountStepWorkspaceRoute,
  advanceCreatorToTeam,
  summaryModelNote,
  teamReviewCreatorOptions
} from './setup-journey-account-steps.js';
import { groupBuildState, isGroupBuilderOpen, openGroupBuilder } from './group-builder.js';
import {
  fetchGroupTemplateStatus,
  groupTemplateProviderLabel,
  groupTemplateTeamFact
} from './group-template-status.js';

import {
  ASSISTANT_SETUP_ROOT,
  hostSetupQuestAPIRoot,
  setupJourneyAPIRoot,
  setupQuestAPIRoot,
  setupQuestOpenDetail,
  userTemplateSetupQuestAPIRoot
} from './setup-quest-links.js';

export const PLUGINS_PAGE_URL = '/plugins';

// integrationHandoffNavigation turns an install quest's summary offer into the
// one browser request it makes. The target is the server-resolved handoff on
// the summary step; its key is validated through the host route helper and the
// event detail is built by the shared helper, never from display text.
export function integrationHandoffNavigation(step, actionID) {
  if (actionID === 'open_plugins') return { kind: 'location', url: PLUGINS_PAGE_URL };
  if (actionID !== 'continue_integration_setup') return null;
  const handoff = step?.handoff;
  if (handoff?.source !== 'plugin') return null;
  try {
    setupQuestAPIRoot(handoff.plugin_id, handoff.id);
  } catch (_) {
    return null;
  }
  return {
    kind: 'open_quest',
    detail: setupQuestOpenDetail({ source: 'plugin', plugin_id: handoff.plugin_id, id: handoff.id })
  };
}

// setupJourneyActionLabel names the continue offer after the quest it opens.
export function setupJourneyActionLabel(step, action) {
  const title = String(step?.handoff?.title || '').trim();
  if (action?.id === 'continue_integration_setup' && title) return `Continue: ${title}`;
  return accountActionLabel(step, action);
}

// setupQuestSelectionFromParams turns a `?setup=quest` deep link into the
// selection openSpecialistSetupJourney understands. The parameters are only a
// request: every ID is validated again when its API root is built, and the
// server checks it against the catalog.
export function setupQuestSelectionFromParams(params) {
  const source = params.get('source');
  if (source === 'host') return { source: 'host', quest_id: params.get('quest') || '' };
  if (source === 'user_template') {
    return {
      source: 'user_template',
      template_id: params.get('template') || '',
      attachment_id: params.get('attachment') || ''
    };
  }
  return { plugin_id: params.get('plugin') || '', quest_id: params.get('quest') || '' };
}

const state = {
  journey: null,
  selectedStepID: '',
  launchStage: '',
  draft: null,
  projectDrafts: {},
  review: null,
  reviewInput: null,
  pendingCommit: null,
  returnFocus: null,
  busy: false,
  commitLocked: false,
  modal: null,
  groupTemplate: null,
  // The in-quest Home-provider recovery: a disclosure awaiting confirmation,
  // or the last refused/failed attempt. Keyed to its run and plugin.
  providerRecovery: null
};

function byID(id) {
  return document.getElementById(id);
}

function ui() {
  const root = byID('specialistSetupJourneyModal');
  if (!root) return null;
  return {
    root,
    title: byID('specialistSetupJourneyTitle'),
    description: byID('specialistSetupJourneyDescription'),
    steps: byID('specialistSetupJourneySteps'),
    busy: byID('specialistSetupJourneyBusy'),
    error: byID('specialistSetupJourneyError'),
    content: byID('specialistSetupJourneyContent'),
    stepState: byID('specialistSetupJourneyStepState'),
    stepTitle: byID('specialistSetupJourneyStepTitle'),
    stepDescription: byID('specialistSetupJourneyStepDescription'),
    receipt: byID('specialistSetupJourneyReceipt'),
    draft: byID('specialistSetupJourneyDraft'),
    actions: byID('specialistSetupJourneyActions'),
    review: byID('specialistSetupJourneyReview'),
    live: byID('specialistSetupJourneyLiveStatus'),
    close: byID('specialistSetupJourneyClose'),
    later: byID('specialistSetupJourneyLater')
  };
}

export function setupJourneyCurrentStep(journey, selectedID = '') {
  const steps = Array.isArray(journey?.steps) ? journey.steps : [];
  return (
    steps.find(step => step?.id === selectedID) ||
    steps.find(step => step?.id === journey?.current_step_id) ||
    steps.find(step => step?.status !== 'complete') ||
    // A ready run has no current step; its summary is what to show.
    steps[steps.length - 1] ||
    null
  );
}

export function setupJourneyReceiptRows(journey, step) {
  const rows = [];
  if (step?.integration) {
    rows.push(['Integration', step.integration.plugin_id]);
    if (!step.integration.installed_version) {
      // Before install, enablement and verification are not states yet; the
      // version is the one that will be installed.
      rows.push(['Installed', 'No']);
      rows.push(['Version to install', step.integration.expected_version]);
    } else {
      rows.push(['Installed', 'Yes']);
      rows.push(['Version', step.integration.installed_version]);
      rows.push(['Enabled', step.integration.enabled ? 'Yes' : 'Not yet']);
      rows.push([
        'Verification',
        step.integration.development_copy
          ? 'Local development copy — not release-verified'
          : step.integration.verified
            ? 'Verified release'
            : 'Not verified for guided setup'
      ]);
      if (step.integration.replacement_required) {
        rows.push(['Next step', 'Review the verified replacement before continuing']);
      }
    }
  }
  if (step?.workspace_setup) {
    rows.push(['Files connected', step.workspace_setup.files_connected ? 'Yes' : 'No']);
    rows.push(['Operating mode', step.workspace_setup.mode_label || 'Not selected']);
    rows.push([
      'Live control configured',
      step.workspace_setup.live_control_configured ? 'Yes' : 'No'
    ]);
    rows.push(['Live control tested', step.workspace_setup.live_control_tested ? 'Yes' : 'No']);
  }
  if (step?.kind === 'project_connect' && journey?.receipts?.project_workspace_id) {
    rows.push(['Project', 'Connected']);
  }
  // An install quest's summary restates what its install step verified.
  if (
    step?.kind === 'summary' &&
    journey?.journey?.source === 'host' &&
    !journey?.journey?.workspace_launch &&
    journey?.receipts?.integration_plugin_id
  ) {
    rows.push([
      'Installed',
      [journey.receipts.integration_plugin_id, journey.receipts.integration_version]
        .filter(Boolean)
        .join(' ')
    ]);
  }
  const providerRow = homeProviderReceiptRow(step?.home_provider);
  if (providerRow) rows.push(providerRow);
  rows.push(...accountStepReceiptRows(step));
  if (journey?.lifecycle === 'ready') rows.push(['Setup', 'Ready']);
  return rows.filter(row => row[1]);
}

// homeProviderReceiptRow states the plugin that provides a split blueprint's
// Home: its ID, its version once installed, and where its lifecycle stands.
export function homeProviderReceiptRow(provider) {
  const pluginID = String(provider?.plugin_id || '').trim();
  if (!pluginID) return null;
  const version = provider.installed ? String(provider.version || '').trim() : '';
  const lifecycle = !provider.installed
    ? 'Not installed'
    : provider.enabled
      ? 'Installed · Enabled'
      : 'Installed · Switched off';
  return ['Home provider', `${[pluginID, version].filter(Boolean).join(' ')} · ${lifecycle}`];
}

// homeProviderOffer picks the one recovery the group screen offers for a Home
// provider that is not ready, in the Create Workspace card's labels. Only a
// provider on the host's reviewed list is repaired from here; any other is
// left to the Plugins page. Enable needs no disclosure: its components were
// disclosed at install, and enabling registers exactly those.
export function homeProviderOffer(provider) {
  const actions = Array.isArray(provider?.actions) ? provider.actions : [];
  const name = provider?.reviewed ? String(provider.display_name || '').trim() : '';
  if (!name) return null;
  if (actions.includes('install_plugin')) {
    return {
      action: 'install_plugin',
      label: `Install ${name}…`,
      confirm: 'Install',
      disclose: true
    };
  }
  if (actions.includes('enable_plugin')) {
    return { action: 'enable_plugin', label: `Enable ${name}`, confirm: '', disclose: false };
  }
  if (actions.includes('review_plugin_update')) {
    return {
      action: 'review_plugin_update',
      label: 'Review update',
      confirm: 'Update',
      disclose: true
    };
  }
  return null;
}

// homeProviderDisclosureIntro introduces the trust report, in the card's words.
export function homeProviderDisclosureIntro(provider, action, preview) {
  const name = String(provider?.display_name || provider?.plugin_id || '').trim();
  return action === 'review_plugin_update' && preview?.changed === false
    ? `${name} asks for nothing new. Updating changes only its version.`
    : `${name} will be able to do the following on this computer:`;
}

// homeProviderSuccessToast is the confirmation shown once a Home-provider
// recovery fully completes; a partial outcome keeps its in-step message instead.
export function homeProviderSuccessToast(name, action) {
  const plugin = String(name || '').trim() || 'The plugin';
  return (
    {
      install_plugin: `${plugin} installed and enabled.`,
      enable_plugin: `${plugin} enabled.`,
      review_plugin_update: `${plugin} updated.`
    }[action] || ''
  );
}

const HOME_PROVIDER_WORKING = {
  install_plugin: 'Installing',
  enable_plugin: 'Enabling',
  review_plugin_update: 'Updating'
};

// homeProviderDisclosureRows are the facts shown above the trust report before
// the user confirms: what will be installed, the reviewed floor, and its source.
export function homeProviderDisclosureRows(provider, preview) {
  return [
    ['Release', String(preview?.release || '').trim()],
    ['Minimum reviewed version', String(provider?.minimum_version || '').trim()],
    ['Installed from', String(preview?.source || '').trim()]
  ].filter(row => row[1]);
}

// homeProviderRecoveryFailure turns a refused or failed recovery call into the
// copy the group screen shows. The endpoint's own outcome wins; a transport
// failure falls back to the lifecycle client's message.
export function homeProviderRecoveryFailure(result) {
  const outcome = result?.data?.outcome || {};
  const step = Array.isArray(outcome.steps) ? outcome.steps.find(item => item?.message) : null;
  return {
    message:
      String(outcome.summary || '').trim() ||
      String(result?.error || '').trim() ||
      'That did not work. Nothing was changed.',
    detail: [String(step?.message || '').trim(), String(outcome.detail || '').trim()]
      .filter(Boolean)
      .join(' '),
    // Only a reachable server answered about the current plugin state; a
    // transport failure leaves nothing new to re-read.
    reread: Number(result?.status) > 0
  };
}

export const RELEASE_UNCHECKED_NOTE =
  'The latest release could not be checked. Ori will install the minimum reviewed version.';

// integrationReleaseCheckNote explains an install or replacement offer that
// targets the minimum reviewed version because the latest release could not be
// checked. It is informational only; the step's actions are unchanged.
export function integrationReleaseCheckNote(step) {
  if (step?.integration?.release_checked !== false) return '';
  const offersRelease = (step.actions || []).some(
    action => action?.id === 'review_install' || action?.id === 'review_update'
  );
  return offersRelease ? RELEASE_UNCHECKED_NOTE : '';
}

export function newJourneyIdempotencyKey() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return `journey-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

// setupJourneyCloseDismisses decides whether closing the modal records the
// journey's persisted dismissal. Plugin and user-template journeys keep their
// existing close-as-dismiss contract. A host quest only hides: leaving it
// unfinished is what lets Home's resume card offer it again, and the card's
// "Not now" is its one explicit dismissal (#455 FR 44-46).
export function setupJourneyCloseDismisses(journey) {
  return journey?.journey?.source !== 'host';
}

export function setupJourneyControlDisabled(busy, lockClose, isCloseControl) {
  return Boolean(busy && (!isCloseControl || lockClose));
}

function setBusy(busy, message = '', { lockClose = false } = {}) {
  state.busy = busy;
  state.commitLocked = busy && lockClose;
  const elements = ui();
  if (!elements) return;
  elements.busy.hidden = !busy;
  elements.live.textContent = message;
  elements.root.setAttribute('aria-busy', busy ? 'true' : 'false');
  elements.root.querySelectorAll('button, input, select').forEach(control => {
    const isCloseControl = control === elements.close || control === elements.later;
    if (!control.closest('#specialistSetupJourneyBusy')) {
      control.disabled =
        control.dataset.unavailable === 'true' ||
        setupJourneyControlDisabled(busy, lockClose, isCloseControl);
    }
  });
}

function hideJourneyPresentation() {
  const elements = ui();
  if (state.modal) {
    // Bootstrap ignores hide() during its opening transition. Honor a quick
    // Escape/Back even then, and remove the queued hide after normal closure.
    // Bootstrap finishes its internal transition bookkeeping after it emits
    // shown.bs.modal, so queue the hide one turn later instead of issuing a
    // second request while the instance still considers itself transitioning.
    const hideWhenShown = () => window.setTimeout(() => state.modal?.hide(), 0);
    elements.root.addEventListener('shown.bs.modal', hideWhenShown, { once: true });
    elements.root.addEventListener(
      'hidden.bs.modal',
      () => {
        elements.root.removeEventListener('shown.bs.modal', hideWhenShown);
      },
      { once: true }
    );
    state.modal.hide();
  } else if (elements) {
    elements.root.classList.remove('show');
    elements.root.style.display = 'none';
    elements.root.setAttribute('aria-hidden', 'true');
  }
  state.returnFocus?.focus?.();
}

function showError(message = '') {
  const element = ui()?.error;
  if (!element) return;
  element.textContent = message;
  element.hidden = !message;
  if (message) element.focus?.();
}

async function request(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    headers: {
      Accept: 'application/json',
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(options.headers || {})
    }
  });
  let payload = null;
  try {
    payload = await response.json();
  } catch (_) {
    payload = null;
  }
  if (!response.ok) {
    const failure = new Error(
      payload?.error?.guidance ||
        'Setup could not be updated. Check the current state and try again.'
    );
    failure.current = payload?.current || null;
    failure.reason = payload?.error?.reason_code || '';
    throw failure;
  }
  return payload;
}

function mutationBody(input = {}) {
  return {
    if_revision: Number(state.journey?.state_revision) || 0,
    idempotency_key: newJourneyIdempotencyKey(),
    ...input
  };
}

function runURL(suffix = '') {
  const id = encodeURIComponent(String(state.journey?.run_id || ''));
  return `${setupJourneyAPIRoot(state.journey)}/runs/${id}${suffix}`;
}

function makeText(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = text || '';
  return node;
}

// safeExternalLink returns a plain https URL suitable for an outbound link, or
// '' when the value has another scheme, credentials, a query or a fragment.
export function safeExternalLink(value) {
  try {
    const parsed = new URL(String(value || ''));
    if (
      parsed.protocol !== 'https:' ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password ||
      parsed.search ||
      parsed.hash
    )
      return '';
    return parsed.href;
  } catch (_) {
    return '';
  }
}

// appendRows renders label/value rows. A value may be { text, href } for a
// link that opens in a new tab; the href must already be a safe https URL.
function appendRows(container, rows, className = 'setup-journey__receipt-list') {
  if (!rows.length) return;
  const list = document.createElement('ul');
  list.className = className;
  rows.forEach(([label, value]) => {
    const item = document.createElement('li');
    const strong = makeText('strong', '', `${label}: `);
    const href = value && typeof value === 'object' ? safeExternalLink(value.href) : '';
    if (href) {
      const link = makeText('a', '', value.text || href);
      link.href = href;
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      item.append(strong, link);
    } else {
      item.append(
        strong,
        document.createTextNode(value && typeof value === 'object' ? value.text : String(value))
      );
    }
    list.appendChild(item);
  });
  container.appendChild(list);
}

export function projectDraftInput(draft) {
  const placement = draft.groupComposition ? { group_composition: draft.groupComposition } : {};
  if (draft.kind === 'existing') {
    return {
      mode_id: 'existing_project',
      selection_token: draft.selectionToken,
      entry_name: draft.entryName || undefined,
      workspace_name: draft.workspaceName.trim(),
      ...placement
    };
  }
  return {
    mode_id: 'new_project',
    workspace_name: draft.workspaceName.trim() || draft.projectName.trim(),
    project_name: draft.projectName.trim(),
    ...placement
  };
}

export function projectReviewPresentation(project) {
  const existing = project.mode_id === 'existing_project';
  return {
    title: `${existing ? 'Import' : 'Create'} “${project.workspace_name}”?`,
    confirm: existing ? 'Import Project' : 'Create Project',
    description: existing
      ? 'Add this project to Ori without moving or changing its existing files.'
      : 'Create the project files listed below and add the project to Ori.'
  };
}

export function projectFailureGuidance(phase, reason, existing = false) {
  if (phase === 'commit') {
    return 'Ori could not confirm the project change. Your entries are kept. Retrying uses your original confirmation; some files may already exist.';
  }
  const recovery =
    reason === 'input_invalid'
      ? existing
        ? 'Check the name and select the project folder again.'
        : 'Check the project name. Use a name such as “First Idea”, without slashes.'
      : 'Try the review again. If it still fails, check the integration in Plugins.';
  return `Could not prepare the project review. Your entries are kept. This review did not create or import a project. ${recovery}`;
}

function statusLabel(status) {
  return (
    {
      complete: 'Complete',
      current: 'Next step',
      active: 'Next step',
      blocked: 'Needs attention',
      pending: 'Not started'
    }[status] || 'Setup step'
  );
}

function render() {
  const elements = ui();
  if (!elements || !state.journey) return;
  const journey = state.journey;
  if (journey.precondition && !journey.declaration_incompatible) {
    renderPrecondition(journey);
    return;
  }
  if (
    journey.journey?.workspace_launch &&
    !journey.declaration_incompatible &&
    !state.managementView
  ) {
    renderWorkspaceLaunch(journey);
    return;
  }
  const step = setupJourneyCurrentStep(journey, state.selectedStepID);
  state.selectedStepID = step?.id || '';
  elements.title.textContent = journey.journey?.title || 'Setup';
  elements.description.textContent = journey.journey?.description || '';
  elements.steps.replaceChildren();
  const visibleSteps = state.managementView
    ? journey.steps.filter(item => ['assistant_program_staffing', 'summary'].includes(item.kind))
    : journey.steps || [];
  if (state.managementView) {
    const item = document.createElement('li');
    const back = makeText('button', 'setup-journey__step-button', 'Back to workspace');
    back.type = 'button';
    back.addEventListener('click', () => {
      state.managementView = false;
      state.review = null;
      state.draft = null;
      render();
    });
    item.appendChild(back);
    elements.steps.appendChild(item);
  }
  visibleSteps.forEach((candidate, index) => {
    const item = document.createElement('li');
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'setup-journey__step-button';
    button.dataset.status = candidate.status || 'pending';
    if (candidate.id === step?.id) button.setAttribute('aria-current', 'step');
    button.append(
      makeText(
        'span',
        'setup-journey__step-number',
        candidate.status === 'complete' ? '✓' : String(index + 1)
      ),
      makeText('span', 'setup-journey__step-label', candidate.title)
    );
    button.addEventListener('click', () => selectStep(candidate.id, button));
    item.appendChild(button);
    elements.steps.appendChild(item);
  });
  const stepIndex = (journey.steps || []).indexOf(step) + 1;
  const projectDraft = step?.kind === 'project_connect' && state.draft;
  elements.stepState.textContent = state.managementView
    ? `Team and extras · ${statusLabel(step?.status)}`
    : `Step ${stepIndex} of ${journey.steps.length} · ${
        projectDraft
          ? state.draft.kind === 'new'
            ? 'New project'
            : 'Import project'
          : statusLabel(step?.status)
      }`;
  elements.stepTitle.textContent = projectDraft
    ? state.draft.kind === 'new'
      ? 'Name your project'
      : 'Import your project'
    : step?.title || '';
  elements.stepDescription.textContent = projectDraft
    ? state.draft.kind === 'new'
      ? 'Ori will create a project folder and add it to your workspace.'
      : 'Choose how this project appears in Ori. Your existing files stay where they are.'
    : step?.description || '';
  elements.receipt.replaceChildren();
  if (step?.guidance && !projectDraft)
    elements.receipt.appendChild(makeText('p', '', step.guidance));
  const modelNote = summaryModelNote(step);
  if (modelNote) elements.receipt.appendChild(makeText('p', '', modelNote));
  appendRows(elements.receipt, setupJourneyReceiptRows(journey, step));
  const releaseNote = integrationReleaseCheckNote(step);
  if (releaseNote) {
    elements.receipt.appendChild(makeText('p', 'setup-journey__scope-note', releaseNote));
  }
  renderDraft(step);
  renderActions(step);
  renderStartOver(journey);
  renderReview();
  elements.live.textContent = journey.busy
    ? 'Ori is checking the previous change. Check its status before trying again.'
    : '';
}

export const START_OVER_EXPLANATION =
  'Your group, project and team stay. Only your setup progress is reset.';

// setupJourneyStartOverView offers Start over for a root whose saved
// declaration is incompatible (FR 38). User-template quests reset by removing
// and recreating the quest, so they have no restart route.
export function setupJourneyStartOverView(journey) {
  if (!journey?.declaration_incompatible) return null;
  if (journey.run_kind && journey.run_kind !== 'root') return null;
  if (journey.journey?.source === 'user_template') return null;
  let root = '';
  try {
    root = setupJourneyAPIRoot(journey);
  } catch (_) {
    return null;
  }
  return { label: 'Start over', explanation: START_OVER_EXPLANATION, url: `${root}/restart` };
}

function renderStartOver(journey) {
  const view = setupJourneyStartOverView(journey);
  if (!view || state.pendingCommit || journey.busy) return;
  const elements = ui();
  elements.receipt.appendChild(makeText('p', 'setup-journey__start-over-note', view.explanation));
  const button = makeText('button', 'setup-journey__action', view.label);
  button.type = 'button';
  button.dataset.effect = 'commit';
  button.dataset.action = 'start_over';
  button.dataset.primary = 'true';
  button.addEventListener('click', () => startOver(view.url, button));
  elements.actions.appendChild(button);
}

async function startOver(url, trigger) {
  if (state.busy) return;
  state.returnFocus = trigger;
  showError('');
  setBusy(true, 'Starting setup over…');
  try {
    const payload = await request(url, { method: 'POST' });
    state.journey = payload?.setup_journey || state.journey;
    state.selectedStepID = state.journey?.current_step_id || '';
    state.managementView = false;
    state.launchStage = '';
    state.draft = null;
    state.projectDrafts = {};
    state.pendingCommit = null;
    state.review = null;
    render();
    ui()?.stepTitle?.focus();
  } catch (error) {
    if (error.current) {
      state.journey = error.current;
      render();
    }
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

export const WORKSPACE_LAUNCH_DESCRIPTION = 'Create your group, then create a workspace.';

// workspaceLaunchStages is the two-screen launch view of a project_setup quest.
// Installing the integration is a precondition, not a screen, and live-control
// readiness belongs to the workspace's own Setup Wizard.
export function workspaceLaunchStages(journey) {
  const copy = journey.journey.workspace_launch;
  const project = journey.steps.find(step => step.kind === 'project_connect');
  const preparation = project?.preparation;
  const connected =
    project?.status === 'complete' && Boolean(journey.receipts?.project_workspace_id);
  const grouped = Boolean(preparation?.exists);
  const policy = preparation?.group_policy || 'required';
  const groupOptional = policy === 'recommended' || policy === 'none';
  const groupComplete = grouped || policy === 'none';
  return [
    { id: 'group', title: copy.group_title, complete: groupComplete, enabled: true },
    {
      id: 'workspace',
      title: 'Create New Workspace',
      complete: connected,
      enabled: groupComplete || groupOptional
    }
  ];
}

// setupJourneyPreconditionView describes the single blocked panel shown while a
// project_setup quest's integration is not ready. The install quest detail is
// built only from the server's install quest ID, validated as a host route.
export function setupJourneyPreconditionView(journey) {
  const precondition = journey?.precondition;
  if (!precondition) return null;
  let installDetail = null;
  try {
    hostSetupQuestAPIRoot(precondition.install_quest_id);
    installDetail = setupQuestOpenDetail({ source: 'host', id: precondition.install_quest_id });
  } catch (_) {
    installDetail = null;
  }
  return {
    guidance: String(precondition.guidance || ''),
    rows: setupJourneyReceiptRows({}, { integration: precondition.integration }),
    installDetail
  };
}

// renderPrecondition shows the one blocked panel of a project_setup quest whose
// integration is not ready (FR 28): guidance, the integration receipt, and the
// install quest. No launch screen is enabled.
function renderPrecondition(journey) {
  const elements = ui();
  const view = setupJourneyPreconditionView(journey);
  elements.title.textContent = journey.journey?.title || 'Setup';
  elements.description.textContent = journey.journey?.workspace_launch
    ? WORKSPACE_LAUNCH_DESCRIPTION
    : journey.journey?.description || '';
  elements.steps.replaceChildren();
  const labels = journey.journey?.workspace_launch
    ? workspaceLaunchStages(journey).map(stage => stage.title)
    : (journey.steps || []).map(step => step.title);
  labels.forEach((label, index) => {
    const button = makeText('button', 'setup-journey__step-button', '');
    button.type = 'button';
    button.dataset.status = 'blocked';
    button.disabled = true;
    button.dataset.unavailable = 'true';
    button.append(
      makeText('span', 'setup-journey__step-number', String(index + 1)),
      makeText('span', 'setup-journey__step-label', label)
    );
    const li = document.createElement('li');
    li.appendChild(button);
    elements.steps.appendChild(li);
  });
  elements.stepState.textContent = statusLabel('blocked');
  elements.stepTitle.textContent = 'The integration needs attention';
  elements.stepDescription.textContent = view.guidance;
  elements.receipt.replaceChildren();
  appendRows(elements.receipt, view.rows);
  elements.draft.replaceChildren();
  elements.review.replaceChildren();
  elements.review.hidden = true;
  elements.actions.replaceChildren();
  if (view.installDetail) {
    const open = makeText('button', 'setup-journey__action', 'Open install quest');
    open.type = 'button';
    open.dataset.effect = 'review';
    open.addEventListener('click', () => {
      // Opens the install quest in this same modal; this quest is unchanged.
      window.dispatchEvent(
        new CustomEvent('ori:open-specialist-setup', { detail: view.installDetail })
      );
    });
    elements.actions.appendChild(open);
  }
  elements.live.textContent = '';
}

function renderWorkspaceLaunch(journey) {
  const elements = ui();
  const copy = journey.journey.workspace_launch;
  const stages = workspaceLaunchStages(journey);
  const current = stages.find(stage => !stage.complete) || stages.at(-1);
  const stage = stages.find(item => item.id === state.launchStage && item.enabled) || current;
  state.launchStage = stage.id;
  const project = journey.steps.find(step => step.kind === 'project_connect');
  const preparation = project?.preparation;
  state.selectedStepID = project.id;
  elements.title.textContent = journey.journey.title;
  elements.description.textContent = WORKSPACE_LAUNCH_DESCRIPTION;
  elements.steps.replaceChildren();
  stages.forEach((item, index) => {
    const button = makeText('button', 'setup-journey__step-button', '');
    button.type = 'button';
    button.dataset.status = item.complete ? 'complete' : 'pending';
    button.disabled = !item.enabled;
    button.dataset.unavailable = item.enabled ? 'false' : 'true';
    if (item.id === stage.id) button.setAttribute('aria-current', 'step');
    button.append(
      makeText('span', 'setup-journey__step-number', item.complete ? '✓' : String(index + 1)),
      makeText('span', 'setup-journey__step-label', item.title)
    );
    button.addEventListener('click', () => {
      if (!item.enabled || state.busy) return;
      state.launchStage = item.id;
      state.review = null;
      state.reviewInput = null;
      showError('');
      render();
      elements.stepTitle.focus();
    });
    const li = document.createElement('li');
    li.appendChild(button);
    elements.steps.appendChild(li);
  });
  elements.stepState.textContent = `Step ${stages.indexOf(stage) + 1} of ${stages.length}`;
  elements.stepTitle.textContent = stage.title;
  elements.stepDescription.textContent = '';
  elements.receipt.replaceChildren();
  elements.draft.replaceChildren();
  elements.actions.replaceChildren();
  const button = (label, action, primary = false) => {
    const control = makeText('button', 'setup-journey__action', label);
    control.type = 'button';
    if (primary) control.dataset.effect = 'review';
    control.addEventListener('click', action);
    elements.actions.appendChild(control);
  };
  if (journey.busy || state.pendingCommit) {
    renderActions(project);
  } else if (stage.id === 'group') {
    appendRows(elements.receipt, groupTemplateRows(preparation));
    if (preparation?.exists) {
      elements.stepDescription.textContent = `Using your existing group: ${preparation.name}. No duplicate group will be created.`;
      button(
        'Continue',
        () => {
          state.launchStage = 'workspace';
          render();
          elements.stepTitle.focus();
        },
        true
      );
    } else if (groupBuildState(journey) === 'provider') {
      renderHomeProviderGroup(journey, project, button);
    } else if (groupBuildState(journey) !== 'create') {
      elements.stepDescription.textContent =
        'The existing setup group could not be verified. Check its status before building anything; no replacement group will be created.';
      button('Check Again', refreshJourney);
      if (journey.receipts?.project_workspace_id)
        button('Open Existing Workspace', async () => {
          const route = await workspaceRoute(journey.receipts.project_workspace_id);
          if (route) window.location.assign(route);
        });
    } else if (preparation?.group_policy === 'recommended') {
      elements.stepDescription.textContent = `This template recommends ${copy.group_name}, but you can continue without creating a Home. The workspace creator will require an explicit grouped or standalone choice.`;
      button('Build Recommended Group', launchGroupBuilder, true);
      button('Choose Placement in Workspace Creator', () => {
        state.launchStage = 'workspace';
        render();
        elements.stepTitle.focus();
      });
    } else if (preparation?.group_policy === 'none') {
      elements.stepDescription.textContent =
        'This template is explicitly standalone. No Assistant Program Home will be created.';
      button(
        'Continue',
        () => {
          state.launchStage = 'workspace';
          render();
          elements.stepTitle.focus();
        },
        true
      );
    } else {
      elements.stepDescription.textContent = `Create “${copy.group_name}”, the one group for these projects. Only the group is created now; its coordinator and each project's team are set up afterward.`;
      button('Build Group', launchGroupBuilder, true);
    }
  } else {
    if (preparation?.group_policy === 'none') {
      elements.stepDescription.textContent =
        'Create a standalone workspace. No Assistant Program Home or membership will be added.';
    } else if (preparation?.group_policy === 'recommended' && !preparation?.exists) {
      elements.stepDescription.textContent =
        'Choose grouped or standalone placement, then review the exact project changes in the workspace creator.';
    } else {
      elements.stepDescription.textContent = `Your workspace will belong to ${preparation?.name || 'your group'}. Choose a new or existing project and confirm its team in the workspace creator.`;
    }
    elements.receipt.appendChild(
      makeText(
        'p',
        '',
        'Live access remains a separate workspace approval and project-specific check.'
      )
    );
    if (journey.receipts?.project_workspace_id) {
      if (project.status !== 'complete')
        elements.stepDescription.textContent =
          'The previous project could not be verified. Open its workspace or group to review what needs attention; this will not create a replacement.';
      button(
        'Open Workspace',
        async () => {
          const route = await workspaceRoute(journey.receipts.project_workspace_id);
          if (route) window.location.assign(route);
        },
        true
      );
      button('Manage Team and Extras', () => {
        state.managementView = true;
        state.selectedStepID =
          journey.steps.find(item => item.kind === 'assistant_program_staffing')?.id || '';
        render();
        elements.stepTitle.focus();
      });
      if (
        journey.steps.some(item =>
          item.actions?.some(action => action.id === 'connect_another_project')
        )
      ) {
        button('Create Another Workspace', async () => {
          await createChildRun();
          if (
            elements.root.classList.contains('show') &&
            state.journey.run_kind === 'child' &&
            !state.journey.receipts?.project_workspace_id
          )
            await launchWorkspaceCreator();
        });
      }
      if (preparation?.group_id || journey.receipts.home_workspace_id) {
        button('Open Group', async () => {
          const route = await workspaceRoute(
            preparation?.group_id || journey.receipts.home_workspace_id
          );
          if (route) window.location.assign(route);
        });
      }
    } else button('Create New Workspace', launchWorkspaceCreator, true);
  }
  renderReview();
}

// renderHomeProviderGroup is the group screen while a split blueprint's Home
// provider is not ready. Its copy is the server's derivation — the Create
// Workspace card's own words — and its one recovery goes through the same
// reviewed endpoint the card uses.
function renderHomeProviderGroup(journey, step, button) {
  const elements = ui();
  const provider = step.home_provider;
  const offer = homeProviderOffer(provider);
  const recovery =
    state.providerRecovery?.runID === journey.run_id &&
    state.providerRecovery?.pluginID === provider.plugin_id
      ? state.providerRecovery
      : null;
  elements.stepDescription.textContent = [provider.summary, provider.detail]
    .filter(Boolean)
    .join(' ');
  appendRows(elements.receipt, setupJourneyReceiptRows(journey, step));
  if (recovery?.phase === 'review') {
    renderHomeProviderDisclosure(provider, recovery);
    button(recovery.confirmLabel, confirmHomeProviderRecovery, true);
    button('Cancel', () => {
      state.providerRecovery = null;
      render();
      elements.stepTitle.focus();
    });
    return;
  }
  if (recovery?.phase === 'failed') {
    const alert = makeText('p', 'setup-journey__provider-error', recovery.message);
    alert.setAttribute('role', 'alert');
    alert.tabIndex = -1;
    elements.draft.appendChild(alert);
    if (recovery.detail) {
      elements.draft.appendChild(makeText('p', 'setup-journey__scope-note', recovery.detail));
    }
  }
  if (offer) button(offer.label, () => startHomeProviderRecovery(provider, offer), true);
  button('Open Plugins', () => window.location.assign(PLUGINS_PAGE_URL));
  button('Check Again', () => {
    state.providerRecovery = null;
    void refreshJourney();
  });
}

// renderHomeProviderDisclosure shows, in the step itself, exactly what the
// reviewed release will register — the card's trust report — before Install.
function renderHomeProviderDisclosure(provider, recovery) {
  const elements = ui();
  const panel = makeText('div', 'setup-journey__provider-review', '');
  panel.setAttribute('role', 'group');
  panel.setAttribute('aria-label', `Review ${provider.display_name || provider.plugin_id}`);
  panel.tabIndex = -1;
  panel.appendChild(
    makeText(
      'p',
      'setup-journey__provider-review-intro',
      homeProviderDisclosureIntro(provider, recovery.action, recovery.preview)
    )
  );
  appendRows(panel, homeProviderDisclosureRows(provider, recovery.preview));
  const lifecycle = window.PluginLifecycle;
  if (lifecycle?.renderTrustReport)
    panel.appendChild(lifecycle.renderTrustReport(recovery.preview?.trust));
  elements.draft.appendChild(panel);
}

async function startHomeProviderRecovery(provider, offer) {
  const client = window.PluginRecoveryClient;
  if (!client || !window.PluginLifecycle) {
    window.location.assign(PLUGINS_PAGE_URL);
    return;
  }
  if (state.busy || !state.journey) return;
  const recovery = {
    runID: state.journey.run_id,
    pluginID: provider.plugin_id,
    displayName: provider.display_name || provider.plugin_id,
    templateID: provider.template_id,
    action: offer.action,
    generation: provider.generation,
    confirmLabel: offer.confirm,
    preview: {},
    phase: 'review'
  };
  showError('');
  if (!offer.disclose) {
    await applyHomeProviderRecovery(recovery);
    return;
  }
  setBusy(true, `Reading what ${provider.display_name || provider.plugin_id} will add…`);
  try {
    const result = await client.previewRecovery(
      provider.template_id,
      offer.action,
      provider.plugin_id,
      provider.generation
    );
    if (state.journey?.run_id !== recovery.runID) return;
    state.providerRecovery = result?.ok
      ? { ...recovery, preview: result.data || {} }
      : {
          runID: recovery.runID,
          pluginID: provider.plugin_id,
          phase: 'failed',
          ...homeProviderRecoveryFailure(result)
        };
    render();
  } finally {
    setBusy(false);
  }
  ui()?.draft?.firstElementChild?.focus?.();
}

function confirmHomeProviderRecovery() {
  const recovery = state.providerRecovery;
  if (recovery?.phase !== 'review') return;
  void applyHomeProviderRecovery(recovery);
}

// applyHomeProviderRecovery applies the reviewed action, then re-reads the
// step so the screen moves on without Check Again. A refusal or failure keeps
// the offer and says why; the endpoint reports what, if anything, changed.
async function applyHomeProviderRecovery(recovery) {
  const client = window.PluginRecoveryClient;
  if (!client || state.busy) return;
  setBusy(true, `${HOME_PROVIDER_WORKING[recovery.action] || 'Applying'} ${recovery.pluginID}…`, {
    lockClose: true
  });
  let result = null;
  try {
    result = await client.confirmRecovery(
      recovery.templateID,
      recovery.action,
      recovery.pluginID,
      recovery.generation,
      recovery.preview?.release
    );
  } finally {
    setBusy(false);
  }
  const outcome = result?.data?.outcome || {};
  const failure = homeProviderRecoveryFailure(result);
  const completed = Boolean(result?.ok && outcome.completed !== false);
  if (completed) {
    state.providerRecovery = null;
  } else {
    // An install that could not be switched on reports its partial outcome
    // here; the re-read then offers what is left to do.
    state.providerRecovery = {
      runID: recovery.runID,
      pluginID: recovery.pluginID,
      phase: 'failed',
      ...failure
    };
  }
  if (result?.ok || failure.reread) await refreshJourney();
  else render();
  const live = ui()?.live;
  if (live && result?.ok && outcome.summary) live.textContent = outcome.summary;
  const toast = completed ? homeProviderSuccessToast(recovery.displayName, recovery.action) : '';
  if (toast) globalThis.Toast?.success?.(toast);
}

// The group stage names the same Group Template and the same group/coordinator
// facts the creator and the group page show. Both reads are presentation only;
// an unreadable fact is omitted rather than guessed.
function groupTemplateRows(preparation) {
  const templateID = String(preparation?.group_template_id || '');
  const groupID = preparation?.exists ? String(preparation?.group_id || '') : '';
  const key = `${templateID}|${groupID}`;
  if (!templateID && !groupID) return [];
  if (state.groupTemplate?.key !== key) {
    const context = { key, entry: null, status: null };
    state.groupTemplate = context;
    const creator = window.GroupTemplateCreator;
    Promise.all([
      templateID && creator?.catalogEntry
        ? creator.catalogEntry(templateID).catch(() => null)
        : null,
      fetchGroupTemplateStatus(groupID)
    ]).then(([entry, status]) => {
      if (state.groupTemplate !== context) return;
      context.entry = entry;
      context.status = status?.kind === 'managed_home' ? status : null;
      if (context.entry || context.status) render();
    });
    return [];
  }
  const { entry, status } = state.groupTemplate;
  const rows = [];
  const templateName = entry?.name || status?.template?.name || '';
  const provider = entry
    ? window.GroupTemplateCreator.providerLabel(entry)
    : groupTemplateProviderLabel(status);
  if (templateName) {
    rows.push(['Group template', `${templateName}${provider ? ` · ${provider}` : ''}`]);
  }
  if (status) {
    rows.push(['Group', 'Created']);
    rows.push(['Coordinator', groupTemplateTeamFact(status).copy]);
  } else if (entry && !groupID) {
    for (const line of window.GroupTemplateCreator.roleSummary(entry)) {
      const [label, ...rest] = line.split(': ');
      rows.push([label, rest.join(': ')]);
    }
  }
  return rows;
}

async function launchGroupBuilder() {
  if (state.launchingGroup || isGroupBuilderOpen() || state.busy || state.journey?.busy) return;
  state.launchingGroup = true;
  const journey = state.journey;
  const open = () => {
    try {
      openGroupBuilder({
        journey,
        onJourneyChange: current => {
          if (state.journey?.run_id === journey.run_id) state.journey = current;
        },
        onClose: current => {
          state.launchingGroup = false;
          if (state.journey?.run_id !== journey.run_id) return;
          state.launchStage = groupBuildState(current) === 'existing' ? 'workspace' : 'group';
          render();
          state.modal?.show();
        }
      });
    } catch (error) {
      state.launchingGroup = false;
      showError(error.message);
      state.modal?.show();
    }
  };
  if (state.modal) {
    ui().root.addEventListener('hidden.bs.modal', open, { once: true });
    hideJourneyPresentation();
  } else {
    hideJourneyPresentation();
    open();
  }
}

async function launchWorkspaceCreator() {
  if (state.launchingWorkspace || state.commitLocked || state.journey?.busy) return;
  state.launchingWorkspace = true;
  const journeyToOpen = state.journey;
  const elements = ui();
  const open = async () => {
    try {
      await openSetupWorkspaceCreator(journeyToOpen, journey => {
        if (state.journey?.run_id === journeyToOpen.run_id) {
          state.journey = journey;
          state.launchStage = '';
        }
      });
    } catch (error) {
      showError(error.message);
      if (state.modal) state.modal.show();
    } finally {
      state.launchingWorkspace = false;
    }
  };
  if (state.modal) {
    elements.root.addEventListener('hidden.bs.modal', open, { once: true });
    hideJourneyPresentation();
  } else {
    hideJourneyPresentation();
    await open();
  }
}

function selectStep(stepID, trigger) {
  if (state.selectedStepID === stepID) return;
  // Switching the rail invalidates the active review without mutating server
  // state. Separate project-form entries remain in memory for Back/retry.
  state.selectedStepID = stepID;
  state.draft = null;
  state.review = null;
  state.reviewInput = null;
  state.returnFocus = trigger;
  showError('');
  render();
  ui()?.stepTitle?.focus();
}

function renderActions(step) {
  const container = ui().actions;
  container.replaceChildren();
  if (state.pendingCommit || state.journey?.busy) {
    const check = makeText(
      'button',
      'setup-journey__action',
      state.pendingCommit
        ? state.pendingCommit.projectChange
          ? 'Retry Project Change'
          : 'Retry Confirmed Change'
        : step?.kind === 'project_connect'
          ? 'Check Project Status'
          : 'Check Setup Status'
    );
    check.type = 'button';
    check.dataset.effect = state.pendingCommit ? 'commit' : 'navigation';
    check.addEventListener('click', () =>
      state.pendingCommit ? commitReview() : refreshJourney()
    );
    container.appendChild(check);
    return;
  }
  if (step?.kind === 'project_connect' && state.draft) return;
  (step?.actions || []).forEach(action => {
    const button = makeText(
      'button',
      'setup-journey__action',
      setupJourneyActionLabel(step, action)
    );
    button.type = 'button';
    button.dataset.effect = action.effect || '';
    button.dataset.action = action.id || '';
    // Continuing into the plugin's quest is the install summary's next step.
    if (action.id === 'continue_integration_setup') button.dataset.primary = 'true';
    button.addEventListener('click', () => handleAction(action, button));
    container.appendChild(button);
  });
}

async function handleAction(action, trigger) {
  if (state.busy || !action?.id) return;
  state.returnFocus = trigger;
  showError('');
  if (action.effect === 'review') {
    if (action.id === 'review_existing_project') return beginExistingProject();
    if (action.id === 'review_new_project') return beginNewProject();
    if (
      ['review_home_staffing', 'review_project_staffing', 'review_optional_home_staffing'].includes(
        action.id
      )
    )
      return beginStaffing(action.id);
    return reviewAction(action.id, {});
  }
  if (action.effect === 'navigation') return navigateAction(action.id);
  if (action.id === 'connect_another_project') return createChildRun();
}

function currentProjectPlacement() {
  const preparation = setupJourneyCurrentStep(state.journey, state.selectedStepID)?.preparation;
  const available = Array.isArray(preparation?.available_compositions)
    ? preparation.available_compositions.filter(
        value => value === 'grouped' || value === 'standalone'
      )
    : [];
  return {
    policy: String(preparation?.group_policy || ''),
    available,
    initial: available.length === 1 ? available[0] : ''
  };
}

async function beginExistingProject(pickAgain = false) {
  if (!pickAgain && state.projectDrafts.existing) {
    state.draft = state.projectDrafts.existing;
    render();
    ui()?.draft?.querySelector('input')?.focus();
    return;
  }
  setBusy(true, 'Waiting for the trusted folder picker…');
  try {
    const picked = await request('/api/folder-picker/select-path', {
      method: 'POST',
      body: JSON.stringify({ title: 'Choose the project folder' })
    });
    if (!picked?.selected) {
      state.returnFocus?.focus?.();
      return;
    }
    if (!picked.selection_token)
      throw new Error('The trusted folder selection expired. Choose the folder again.');
    const pieces = String(picked.path || '')
      .split(/[\\/]/)
      .filter(Boolean);
    const placement = currentProjectPlacement();
    state.draft = {
      kind: 'existing',
      selectionToken: picked.selection_token,
      selectedFolder: picked.path || '',
      workspaceName: state.projectDrafts.existing?.workspaceName || pieces.at(-1) || 'Project',
      entryName: '',
      candidates: [],
      groupPolicy: placement.policy,
      groupCompositions: placement.available,
      groupComposition: state.projectDrafts.existing?.groupComposition || placement.initial
    };
    state.projectDrafts.existing = state.draft;
    state.review = null;
    render();
    ui()?.draft?.querySelector('input')?.focus();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

function beginNewProject() {
  const placement = currentProjectPlacement();
  state.draft = state.projectDrafts.new || {
    kind: 'new',
    workspaceName: '',
    projectName: '',
    candidates: [],
    optionsOpen: false,
    groupPolicy: placement.policy,
    groupCompositions: placement.available,
    groupComposition: placement.initial
  };
  state.projectDrafts.new = state.draft;
  state.review = null;
  render();
  ui()?.draft?.querySelector('input')?.focus();
}

function beginStaffing(actionID) {
  const optional = actionID === 'review_optional_home_staffing';
  const wantedScope = actionID === 'review_project_staffing' ? 'project' : 'home';
  const staffing = setupJourneyCurrentStep(state.journey, state.selectedStepID)?.staffing;
  const target = (staffing?.scopes || []).find(item => item.scope === wantedScope);
  if (!target) {
    showError('Staffing details are unavailable. Refresh setup and try again.');
    return;
  }
  state.draft = {
    kind: 'staffing',
    actionID,
    scope: wantedScope,
    workspaceLabel: target.workspace_label,
    roles: (target.roles || [])
      .filter(role => (optional ? !role.required : role.required) && !role.configured)
      .map(role => ({
        roleID: role.role_id,
        label: role.label,
        name:
          wantedScope === 'project'
            ? `${role.label} · ${target.workspace_label}`.slice(0, 80)
            : role.label.slice(0, 80),
        provider: '',
        model: '',
        toolGrants: role.tool_grants || []
      }))
  };
  state.review = null;
  render();
  ui()?.draft?.querySelector('input')?.focus();
}

function renderDraft(step) {
  const container = ui().draft;
  container.replaceChildren();
  if (!state.draft || state.pendingCommit || state.journey?.busy) return;
  if (state.draft.kind === 'staffing') {
    renderStaffingDraft(container, step);
    return;
  }
  if (step?.kind !== 'project_connect') return;
  const form = document.createElement('form');
  form.className = 'setup-journey__form';
  if (state.draft.kind === 'existing') {
    form.appendChild(makeText('p', 'setup-journey__scope-note', state.draft.selectedFolder));
    const choose = makeText('button', 'setup-journey__action', 'Choose a Different Folder');
    choose.type = 'button';
    choose.addEventListener('click', () => beginExistingProject(true));
    form.appendChild(choose);
    form.appendChild(
      field(
        'Name in Ori',
        'Your existing folder and files will not be renamed.',
        'text',
        state.draft.workspaceName,
        value => {
          state.draft.workspaceName = value;
        }
      )
    );
    const scope = makeText('p', '', `Selected folder: ${state.draft.selectedFolder}`);
    form.appendChild(scope);
    if (state.draft.candidates.length > 1) {
      const label = document.createElement('label');
      label.className = 'setup-journey__field';
      label.appendChild(makeText('span', '', 'Project file to import'));
      const select = document.createElement('select');
      select.required = true;
      select.appendChild(new Option('Choose one file', ''));
      state.draft.candidates.forEach(name => select.appendChild(new Option(name, name)));
      select.value = state.draft.entryName;
      select.addEventListener('change', () => {
        state.draft.entryName = select.value;
      });
      label.appendChild(select);
      form.appendChild(label);
    }
  } else {
    form.appendChild(
      field(
        'Project name',
        'Used for your project in Ori and its new files.',
        'text',
        state.draft.projectName,
        value => {
          state.draft.projectName = value;
        }
      )
    );
    const options = document.createElement('details');
    options.className = 'setup-journey__options';
    options.open = Boolean(state.draft.optionsOpen);
    options.appendChild(makeText('summary', '', 'More options'));
    options.appendChild(
      field(
        'Different display name in Ori',
        'Optional. Leave blank to use the project name.',
        'text',
        state.draft.workspaceName,
        value => {
          state.draft.workspaceName = value;
        },
        false
      )
    );
    options.addEventListener('toggle', () => {
      if (state.draft?.kind === 'new') state.draft.optionsOpen = options.open;
    });
    form.appendChild(options);
  }
  const compositions = Array.isArray(state.draft.groupCompositions)
    ? state.draft.groupCompositions
    : [];
  if (compositions.length > 1) {
    const label = document.createElement('label');
    label.className = 'setup-journey__field';
    label.appendChild(makeText('span', '', 'Project placement'));
    const select = document.createElement('select');
    select.name = 'project-placement';
    select.required = true;
    select.appendChild(new Option('Choose grouped or standalone', ''));
    select.appendChild(new Option('Use the Assistant Program Home', 'grouped'));
    select.appendChild(new Option('Create a standalone project', 'standalone'));
    select.value = state.draft.groupComposition || '';
    select.addEventListener('change', () => {
      state.draft.groupComposition = select.value;
      state.review = null;
      state.reviewInput = null;
    });
    label.appendChild(select);
    label.appendChild(
      makeText(
        'small',
        '',
        'Grouped placement uses the exact canonical Home. Standalone keeps project roles local and creates no Home membership.'
      )
    );
    form.appendChild(label);
  } else if (compositions[0] === 'grouped') {
    form.appendChild(
      makeText(
        'p',
        'setup-journey__scope-note',
        'This template requires its canonical group destination.'
      )
    );
  } else if (compositions[0] === 'standalone') {
    form.appendChild(
      makeText(
        'p',
        'setup-journey__scope-note',
        'This template creates a standalone project with no Home membership.'
      )
    );
  }
  form.appendChild(
    makeText(
      'p',
      'setup-journey__scope-note',
      'Nothing is created or imported until you confirm the next screen.'
    )
  );
  const controls = document.createElement('div');
  controls.className = 'setup-journey__review-controls';
  const back = makeText('button', 'setup-journey__action', 'Back');
  back.type = 'button';
  back.addEventListener('click', () => {
    const actionID = state.draft.kind === 'new' ? 'review_new_project' : 'review_existing_project';
    state.draft = null;
    state.review = null;
    state.reviewInput = null;
    showError('');
    render();
    ui()?.actions?.querySelector(`[data-action="${actionID}"]`)?.focus();
  });
  const submit = makeText(
    'button',
    'setup-journey__action',
    state.draft.kind === 'new' ? 'Review Project' : 'Review Import'
  );
  submit.type = 'submit';
  submit.dataset.effect = 'review';
  controls.append(back, submit);
  form.appendChild(controls);
  form.addEventListener('submit', event => {
    event.preventDefault();
    if (!form.reportValidity()) return;
    const input = projectDraftInput(state.draft);
    reviewAction(
      state.draft.kind === 'existing' ? 'review_existing_project' : 'review_new_project',
      input
    );
  });
  container.appendChild(form);
}

function renderStaffingDraft(container, step) {
  if (step?.kind !== 'assistant_program_staffing' || !state.draft.roles.length) return;
  const form = document.createElement('form');
  form.className = 'setup-journey__form';
  form.appendChild(
    makeText(
      'p',
      'setup-journey__scope-note',
      `These profiles belong only to ${state.draft.workspaceLabel}. Names must be unique in Your Agents.`
    )
  );
  state.draft.roles.forEach(role => {
    const group = document.createElement('fieldset');
    group.className = 'setup-journey__staffing-role';
    group.appendChild(makeText('legend', '', role.label));
    group.appendChild(
      field('Profile name', 'The independently saved profile name.', 'text', role.name, value => {
        role.name = value;
      })
    );
    group.appendChild(
      field(
        'Provider',
        'Leave blank with Model to use the current system default.',
        'text',
        role.provider,
        value => {
          role.provider = value;
        },
        false
      )
    );
    group.appendChild(
      field(
        'Model',
        'Leave blank with Provider to use the current system default.',
        'text',
        role.model,
        value => {
          role.model = value;
        },
        false
      )
    );
    group.appendChild(
      makeText(
        'small',
        '',
        role.toolGrants.length ? `Tool grants: ${role.toolGrants.join(', ')}` : 'Tool grants: none'
      )
    );
    form.appendChild(group);
  });
  const submit = makeText('button', 'setup-journey__action', 'Review scoped staffing');
  submit.type = 'submit';
  submit.dataset.effect = 'review';
  form.appendChild(submit);
  form.addEventListener('submit', event => {
    event.preventDefault();
    if (!form.reportValidity()) return;
    reviewAction(state.draft.actionID, {
      roles: state.draft.roles.map(role => ({
        role_id: role.roleID,
        name: role.name.trim(),
        provider: role.provider.trim() || undefined,
        model: role.model.trim() || undefined
      }))
    });
  });
  container.appendChild(form);
}

let fieldSequence = 0;

function field(labelText, help, type, value, onInput, required = true) {
  const label = document.createElement('label');
  label.className = 'setup-journey__field';
  label.appendChild(makeText('span', '', labelText));
  const input = document.createElement('input');
  input.type = type;
  input.name = labelText.toLowerCase().replaceAll(' ', '-');
  input.autocomplete = 'off';
  input.required = required;
  input.maxLength = 128;
  input.value = value || '';
  input.addEventListener('input', () => onInput(input.value));
  input.setAttribute('aria-label', labelText);
  const description = makeText('small', '', help);
  description.id = `setup-journey-field-help-${++fieldSequence}`;
  input.setAttribute('aria-describedby', description.id);
  label.append(input, description);
  return label;
}

async function reviewAction(actionID, input) {
  setBusy(true, 'Preparing an exact review…');
  showError('');
  try {
    const payload = await request(runURL(`/actions/${encodeURIComponent(actionID)}`), {
      method: 'POST',
      body: JSON.stringify(mutationBody({ input }))
    });
    if (!payload?.review) throw new Error('The review is unavailable. Nothing was changed.');
    const project = payload.review.project_connection;
    if (
      project &&
      !project.entry_name &&
      Array.isArray(project.entry_candidates) &&
      project.entry_candidates.length > 1
    ) {
      state.draft.candidates = project.entry_candidates.slice();
      state.review = null;
      state.reviewInput = null;
      render();
      ui()?.draft?.querySelector('select')?.focus();
      return;
    }
    state.review = payload.review;
    state.reviewInput = input;
    render();
    ui()?.review?.querySelector('h4')?.focus();
  } catch (error) {
    if (error.current) state.journey = error.current;
    const projectDraft = state.draft?.kind === 'new' || state.draft?.kind === 'existing';
    showError(
      projectDraft
        ? projectFailureGuidance('review', error.reason, state.draft.kind === 'existing')
        : error.message
    );
    render();
  } finally {
    setBusy(false);
  }
}

// integrationReviewPresentation names an integration review by its outcome:
// install, enable, or replace. stepTitle is the integration step's own title,
// which the registry supplies. Other reviews return null.
export function integrationReviewPresentation(review, stepTitle = '') {
  const integration = review?.integration;
  if (!integration) return null;
  if (integration.replacement_required === true || review.commit_action === 'update') {
    return {
      title: 'Replace installed integration?',
      description:
        'Replace the current installation with Ori’s reviewed version, even if the version number is unchanged. Its enabled state is preserved. Existing workspaces and project files are not deleted; runtime access may need review again.',
      confirm: 'Replace with reviewed version'
    };
  }
  if (review.commit_action === 'install') {
    const version = integration.expected_version ? ` ${integration.expected_version}` : '';
    const source = integration.source_label ? ` from ${integration.source_label}` : '';
    const title = String(stepTitle || '').trim();
    return {
      title: title ? `${title}?` : 'Install this plugin?',
      description: `Ori downloads the reviewed${version} release${source}, checks its fingerprint, and installs it. Nothing runs until you enable it.`,
      confirm: 'Install'
    };
  }
  if (review.commit_action === 'enable') {
    return {
      title: 'Enable this plugin?',
      description:
        'Enabling lets Ori use the installed plugin’s blueprints, tools and guided setup. You can disable it again on the Plugins page.',
      confirm: 'Enable'
    };
  }
  return null;
}

function renderReview() {
  const container = ui().review;
  container.replaceChildren();
  container.hidden = !state.review;
  ui().content.hidden = Boolean(state.review);
  if (!state.review) return;
  const project = state.review.project_connection;
  const group = state.review.group;
  const accountLink = accountLinkReviewPresentation(state.review);
  const reviewedStep = setupJourneyCurrentStep(state.journey, state.selectedStepID);
  const integrationPresentation = integrationReviewPresentation(
    state.review,
    reviewedStep?.kind === 'integration_install' ? reviewedStep.title : ''
  );
  const presentation = project
    ? projectReviewPresentation(project)
    : group
      ? {
          title: `Create “${group.name}”?`,
          confirm: 'Create Group',
          description:
            'Create your group. No project, agent, schedule, or access permission will be added.'
        }
      : accountLink;
  const heading = makeText(
    'h4',
    '',
    presentation?.title || integrationPresentation?.title || 'Review before making changes'
  );
  heading.tabIndex = -1;
  container.appendChild(heading);
  if (group) {
    container.appendChild(makeText('p', '', presentation.description));
    container.appendChild(
      makeText(
        'p',
        'setup-journey__scope-note',
        'Your workspaces will go inside this group. The group coordinates projects without inheriting their access.'
      )
    );
  } else if (accountLink) {
    container.appendChild(makeText('p', '', accountLink.description));
    appendRows(
      container,
      [
        ...accountLink.rows,
        ['Consent expires', new Date(state.review.expires_at).toLocaleString()]
      ],
      'setup-journey__review-list'
    );
  } else if (presentation) {
    container.appendChild(makeText('p', '', presentation.description));
    appendRows(
      container,
      [
        ['Project in Ori', project.workspace_name],
        [
          'Placement',
          project.group_composition === 'standalone'
            ? 'Standalone — no Home membership'
            : project.group_composition === 'grouped'
              ? `Grouped in ${project.parent_workspace_name}`
              : project.parent_workspace_name
        ],
        ...(project.group_composition === 'standalone'
          ? []
          : [
              ['Home', project.parent_workspace_name],
              [
                'Home setup',
                project.home_will_be_created ? 'Create this Home' : 'Use your existing Home'
              ]
            ]),
        ['Project file', project.entry_name],
        ...(project.selected_folder ? [['Existing folder', project.selected_folder]] : []),
        ['Team', 'Add your team in a later step'],
        ['Project app', 'Will not open automatically']
      ],
      'setup-journey__review-list'
    );
    if (project.created_files?.length) {
      container.appendChild(makeText('h5', '', 'Files to create'));
      const files = document.createElement('ul');
      files.className = 'setup-journey__file-list';
      project.created_files.forEach(name => files.appendChild(makeText('li', '', name)));
      container.appendChild(files);
    }
    if (project.defaults_statement)
      container.appendChild(makeText('p', 'setup-journey__scope-note', project.defaults_statement));
  } else {
    if (integrationPresentation) {
      container.appendChild(makeText('p', '', integrationPresentation.description));
    }
    const integrationRows = integrationReviewRows(state.review);
    appendRows(
      container,
      [
        ...(integrationRows?.summary || []),
        ...reviewRows({ ...state.review, integration: undefined })
      ],
      'setup-journey__review-list'
    );
    if (integrationRows?.details.length) {
      // The full trust disclosure stays available but collapsed, so the
      // decision and its buttons are not pushed below a wall of data.
      const technical = document.createElement('details');
      technical.className = 'setup-journey__options setup-journey__technical';
      technical.appendChild(makeText('summary', '', 'Technical details'));
      appendRows(technical, integrationRows.details, 'setup-journey__review-list');
      container.appendChild(technical);
    }
  }
  const controls = document.createElement('div');
  controls.className = 'setup-journey__review-controls';
  const cancel = makeText('button', 'btn btn-outline-secondary', 'Back');
  cancel.type = 'button';
  cancel.addEventListener('click', () => {
    state.review = null;
    state.reviewInput = null;
    render();
    showError('');
    const target =
      ui()?.draft?.querySelector('input, select') || ui()?.actions?.querySelector('button');
    target?.focus();
  });
  const confirm = makeText(
    'button',
    'setup-journey__action setup-journey__review-confirm',
    presentation?.confirm || integrationPresentation?.confirm || 'Confirm this change'
  );
  confirm.type = 'button';
  confirm.addEventListener('click', commitReview);
  controls.append(cancel, confirm);
  container.appendChild(controls);
}

// integrationReviewRows splits an integration review into the few facts a
// person decides on and the technical trust disclosure, which the review panel
// keeps collapsed. Returns null for reviews without an integration.
export function integrationReviewRows(review) {
  const integration = review?.integration;
  if (!integration) return null;
  // The full repository URL links out so the source can be checked first.
  const sourceURL = safeExternalLink(integration.source_url);
  const summary = [
    ['Publisher', integration.publisher],
    [
      'Source',
      sourceURL ? { text: integration.source_url, href: sourceURL } : integration.source_label
    ],
    ...(integration.installed_version
      ? [['Installed version', integration.installed_version]]
      : []),
    ['Release version', integration.expected_version],
    ['Minimum reviewed version', integration.minimum_version],
    [
      'Enabled after this action',
      review.commit_action === 'enable' ? 'Yes' : integration.enabled ? 'Already enabled' : 'No'
    ]
  ];
  const details = [
    ['Integration', integration.plugin_id],
    ['Platform', (integration.supported_platforms || []).join(', ')],
    ['Required host features', (integration.required_host_features || []).join(', ')]
  ];
  Object.entries(integration.trust || {}).forEach(([key, value]) => {
    if (value === null || value === '' || (Array.isArray(value) && !value.length)) return;
    details.push([
      humanize(key),
      Array.isArray(value)
        ? value.map(item => (typeof item === 'object' ? JSON.stringify(item) : item)).join('; ')
        : String(value)
    ]);
  });
  const present = row => row[1] !== undefined && row[1] !== null && row[1] !== '';
  return { summary: summary.filter(present), details: details.filter(present) };
}

export function reviewRows(review) {
  const rows = [];
  const integrationRows = integrationReviewRows(review);
  if (integrationRows) rows.push(...integrationRows.summary, ...integrationRows.details);
  const project = review.project_connection;
  if (project) {
    rows.push(['Workspace', project.workspace_name]);
    if (project.group_composition === 'standalone') {
      rows.push(['Placement', 'Standalone — no Home membership']);
    } else {
      rows.push(
        ['Parent', project.parent_workspace_name],
        [
          'Home',
          project.home_will_be_created ? 'Will be created if still missing' : 'Will be reused'
        ]
      );
    }
    rows.push(['Project file', project.entry_name]);
    if (project.selected_folder) rows.push(['Authorized folder', project.selected_folder]);
    if (project.project_name) rows.push(['Project name', project.project_name]);
    if (project.created_files?.length)
      rows.push(['Files the blueprint creates', project.created_files.join(', ')]);
    if (project.defaults_statement) rows.push(['Blueprint defaults', project.defaults_statement]);
  }
  const staffing = review.staffing;
  if (staffing?.scopes?.length) {
    const target = staffing.scopes[0];
    rows.push(
      ['Staffing scope', humanize(target.scope)],
      ['Workspace', target.workspace_label],
      ['Workspace ID', target.workspace_id],
      ['Current binding revision', target.binding_revision],
      ['Runtime ready', target.runtime_ready ? 'Yes' : 'No'],
      ['Models ready', target.models_ready ? 'Yes' : 'No — deterministic features still work'],
      ['Authority boundary', target.authority_boundary],
      [
        'Operating mode',
        target.selected_mode_id ? humanize(target.selected_mode_id) : 'Not required'
      ]
    );
    (target.roles || []).forEach(role => {
      rows.push(
        [`${role.label} responsibility`, role.responsibility],
        [`${role.label} profile`, role.profile_name],
        [
          `${role.label} chat/execution`,
          role.chat_available ? 'Available' : 'Unavailable — no compatible model is configured'
        ],
        [`${role.label} model`, role.model || 'No configured model'],
        [`${role.label} provider`, role.provider || 'No configured provider'],
        [
          `${role.label} tool grants`,
          role.tool_grants?.length ? role.tool_grants.join(', ') : 'None'
        ]
      );
    });
  }
  const setup = review.workspace_setup;
  if (setup) {
    rows.push(
      ['Operating mode', setup.mode_label],
      ['What it means', setup.mode_description],
      ['Files connected', setup.files_connected ? 'Yes' : 'No'],
      ['Live control configured', setup.live_control_configured ? 'Yes' : 'No'],
      ['Live control tested', setup.live_control_tested ? 'Yes' : 'No']
    );
  }
  rows.push(['Consent expires', new Date(review.expires_at).toLocaleString()]);
  return rows.filter(row => row[1] !== undefined && row[1] !== '');
}

function humanize(value) {
  return String(value)
    .replaceAll('_', ' ')
    .replace(/\b\w/g, letter => letter.toUpperCase());
}

async function commitReview() {
  if ((!state.review && !state.pendingCommit) || state.busy) return;
  const projectChange = Boolean(
    state.review?.project_connection || state.pendingCommit?.projectChange
  );
  // A lost response must replay the same consent and idempotency envelope, not
  // start another mutation. Keep it in memory only, never browser storage.
  state.pendingCommit ||= {
    url: runURL(`/actions/${encodeURIComponent(state.review.commit_action)}`),
    body: mutationBody({ review_token: state.review.token, input: state.reviewInput || {} }),
    projectChange
  };
  setBusy(true, 'Applying the reviewed change…', { lockClose: true });
  showError('');
  try {
    const payload = await request(state.pendingCommit.url, {
      method: 'POST',
      body: JSON.stringify(state.pendingCommit.body)
    });
    state.journey = payload?.setup_journey || state.journey;
    state.review = null;
    state.reviewInput = null;
    state.pendingCommit = null;
    state.draft = null;
    state.projectDrafts = {};
    state.launchStage = '';
    state.selectedStepID = state.journey?.current_step_id || '';
    render();
    ui()?.stepTitle?.focus();
  } catch (error) {
    if (error.current) state.journey = error.current;
    state.review = null;
    state.reviewInput = null;
    if (error.current && !error.current.busy) state.pendingCommit = null;
    showError(
      projectChange
        ? state.pendingCommit
          ? projectFailureGuidance('commit')
          : 'The project change was not confirmed. Your entries are kept. Review the project again before another attempt.'
        : error.message
    );
    render();
  } finally {
    setBusy(false);
  }
}

async function workspaceRoute(workspaceID, suffix = '') {
  if (!workspaceID) return '';
  try {
    const workspace = await request(`/api/workspaces/${encodeURIComponent(workspaceID)}`);
    const slug = String(workspace?.folder_slug || '').trim();
    if (!slug) throw new Error('That workspace route is unavailable.');
    return `/workspaces/${encodeURIComponent(slug)}${suffix}`;
  } catch (_) {
    showError('That workspace route is unavailable. Refresh setup and try again.');
    return '';
  }
}

async function navigateAction(actionID) {
  const receipts = state.journey?.receipts || {};
  const homeWorkspaceID =
    receipts.home_workspace_id ||
    state.journey?.steps.find(step => step.kind === 'project_connect')?.preparation?.group_id;
  switch (actionID) {
    case 'manage_integration':
      window.location.assign(PLUGINS_PAGE_URL);
      return;
    case 'open_plugins':
    case 'continue_integration_setup': {
      const summary = (state.journey?.steps || []).find(step => step.kind === 'summary');
      const navigation = integrationHandoffNavigation(summary, actionID);
      if (!navigation) {
        showError('That setup is unavailable right now. Refresh setup and try again.');
        return;
      }
      if (navigation.kind === 'location') {
        window.location.assign(navigation.url);
        return;
      }
      // Opens the plugin's quest in this same modal. The install quest stays
      // ready; it is neither dismissed nor changed.
      window.dispatchEvent(
        new CustomEvent('ori:open-specialist-setup', { detail: navigation.detail })
      );
      return;
    }
    case 'open_project':
      if (!receipts.project_workspace_id) return;
      setBusy(true, 'Sending an operating-system open request…');
      try {
        await request(
          `/api/workspaces/${encodeURIComponent(receipts.project_workspace_id)}/project/open`,
          { method: 'POST' }
        );
        ui().live.textContent =
          'The operating system accepted the open request. Live control is not implied.';
      } catch (error) {
        showError(error.message);
      } finally {
        setBusy(false);
      }
      return;
    case 'open_workspace_setup':
    case 'open_live_setup': {
      const route = await workspaceRoute(receipts.project_workspace_id, '?panel=settings');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_home': {
      const route = await workspaceRoute(homeWorkspaceID, '/assistant');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_home_staffing': {
      const route = await workspaceRoute(homeWorkspaceID, '/assistant');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_project_staffing': {
      const route = await workspaceRoute(receipts.project_workspace_id, '/assistant');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_sample_library_setup': {
      const route = await workspaceRoute(homeWorkspaceID, '/assistant#sampleLibraryPanel');
      if (route) window.location.assign(route);
      return;
    }
    case 'refresh_workspace_setup':
    case 'review_setup':
      return refreshJourney();
    case 'review_team':
      return launchTeamReview();
    case 'open_account_settings': {
      // Google sign-in lives on the Settings card. The quest stays open here
      // and never starts OAuth itself; Check again re-reads afterwards.
      window.open(accountSettingsURL(state.journey), '_blank', 'noopener');
      ui().live.textContent = SETTINGS_TAB_NOTE;
      return;
    }
    case 'recheck_connection':
      return refreshJourney();
    case 'start_inbox_triage': {
      // Opens the workspace's tasks; it never starts, assigns, or runs one.
      const route =
        accountStepWorkspaceRoute(state.journey, '?panel=tasks') ||
        (await workspaceRoute(receipts.project_workspace_id, '?panel=tasks'));
      if (route) window.location.assign(route);
      return;
    }
    case 'open_model_settings':
      window.location.assign(MODEL_SETTINGS_URL);
      return;
    case 'open_workspace': {
      const route =
        accountStepWorkspaceRoute(state.journey) ||
        (await workspaceRoute(receipts.project_workspace_id));
      if (route) window.location.assign(route);
      return;
    }
    default:
      return;
  }
}

// launchTeamReview hands the user to the shared Workspace creator on the
// Email Ops blueprint's Team step. The quest does not create anything: when
// the creator closes — after a create or a cancel — the quest re-reads its
// canonical state and reopens where the server says the user is.
async function launchTeamReview() {
  const manager = window.sessionManager;
  const creatorModal = document.getElementById('addFolderModal');
  if (state.launchingTeamReview || state.commitLocked || state.journey?.busy) return;
  if (!manager?.showAddWorkspaceModal || !creatorModal) {
    showError('The workspace creator is unavailable. Refresh the page and try again.');
    return;
  }
  state.launchingTeamReview = true;
  const questID = String(state.journey?.journey?.id || '');
  const elements = ui();
  let current = true;
  const onCreatorHidden = () => {
    // The creator hides itself while a teammate's setup form or a Map
    // placement is open; only a real close returns to the quest.
    if (
      creatorModal.dataset.suspendedForAgentSetup ||
      creatorModal.dataset.suspendedForMapPlacement
    ) {
      return;
    }
    creatorModal.removeEventListener('hidden.bs.modal', onCreatorHidden);
    current = false;
    state.launchingTeamReview = false;
    void openSpecialistSetupJourney({ source: 'host', quest_id: questID });
  };
  const open = async () => {
    creatorModal.addEventListener('hidden.bs.modal', onCreatorHidden);
    try {
      manager.showAddWorkspaceModal(teamReviewCreatorOptions());
      await advanceCreatorToTeam({
        manager,
        getSelectedTemplate: () => window.ProjectTemplateCard?.getSelectedTemplate?.(),
        nameInput: document.getElementById('folderNameInput'),
        isCurrent: () => current
      });
    } catch (error) {
      creatorModal.removeEventListener('hidden.bs.modal', onCreatorHidden);
      state.launchingTeamReview = false;
      showError(error.message);
      if (state.modal) state.modal.show();
    }
  };
  if (state.modal) {
    elements.root.addEventListener('hidden.bs.modal', open, { once: true });
    hideJourneyPresentation();
  } else {
    hideJourneyPresentation();
    await open();
  }
}

async function createChildRun() {
  setBusy(true, 'Preparing another independent project setup…');
  try {
    const payload = await request(`${setupJourneyAPIRoot(state.journey)}/children`, {
      method: 'POST',
      body: JSON.stringify(mutationBody())
    });
    state.journey = payload?.setup_journey || state.journey;
    state.selectedStepID = state.journey?.current_step_id || '';
    state.managementView = false;
    state.launchStage = '';
    state.draft = null;
    state.projectDrafts = {};
    state.pendingCommit = null;
    state.review = null;
    render();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

async function refreshJourney() {
  if (!state.journey?.run_id) return;
  setBusy(true, 'Refreshing canonical setup status…');
  try {
    const payload = await request(runURL());
    state.journey = payload?.setup_journey || state.journey;
    // Check Again also re-reads the group's template and coordinator facts.
    state.groupTemplate = null;
    if (!state.journey.busy) {
      state.pendingCommit = null;
      state.selectedStepID = state.journey.current_step_id || '';
      if (state.journey.receipts?.project_workspace_id) state.draft = null;
      showError('');
    }
    render();
    ui()?.stepTitle?.focus();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

async function dismissJourney() {
  if (!state.journey || state.commitLocked) return;
  if (
    state.busy ||
    state.pendingCommit ||
    state.journey?.busy ||
    (state.journey.lifecycle_state || state.journey.lifecycle) === 'ready' ||
    !setupJourneyCloseDismisses(state.journey)
  ) {
    hideJourneyPresentation();
    return;
  }
  setBusy(true, 'Saving your place…');
  try {
    const payload = await request(runURL('/dismiss'), {
      method: 'POST',
      body: JSON.stringify(mutationBody())
    });
    state.journey = payload?.setup_journey || state.journey;
    hideJourneyPresentation();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

export async function openSpecialistSetupJourney(requested = null) {
  const intent = String(requested?.detail?.intent || requested?.intent || 'review');
  const requestedRunID = String(requested?.detail?.run_id || requested?.run_id || '');
  const selection = requested?.detail || requested || {};
  const elements = ui();
  if (!elements || state.busy || state.launchingGroup || isGroupBuilderOpen()) return false;
  state.returnFocus = document.activeElement;
  setBusy(true, 'Loading current setup…');
  showError('');
  try {
    const previousRunID = state.journey?.run_id;
    const root =
      selection.source === 'host'
        ? hostSetupQuestAPIRoot(selection.quest_id)
        : selection.source === 'user_template' ||
            selection.template_id != null ||
            selection.attachment_id != null
          ? userTemplateSetupQuestAPIRoot(selection.template_id, selection.attachment_id)
          : selection.plugin_id != null || selection.quest_id != null
            ? setupQuestAPIRoot(selection.plugin_id, selection.quest_id)
            : ASSISTANT_SETUP_ROOT;
    const endpoint = requestedRunID ? `${root}/runs/${encodeURIComponent(requestedRunID)}` : root;
    let payload = await request(endpoint);
    state.journey = payload?.setup_journey;
    state.groupTemplate = null;
    state.providerRecovery = null;
    if (!state.journey) return false;
    // An unresolved owner operation blocks presentation writes, not access
    // to its status. Show the authorized read without claiming another action.
    if (
      !state.journey.busy &&
      (state.journey.lifecycle_state || state.journey.lifecycle) !== 'ready'
    ) {
      payload = await request(`${endpoint}/open`, {
        method: 'POST',
        body: JSON.stringify(mutationBody())
      });
      state.journey = payload?.setup_journey || state.journey;
    }
    if (
      intent === 'connect_another' &&
      (state.journey?.lifecycle_state || state.journey?.lifecycle) === 'ready'
    ) {
      payload = await request(`${setupJourneyAPIRoot(state.journey)}/children`, {
        method: 'POST',
        body: JSON.stringify(mutationBody())
      });
      state.journey = payload?.setup_journey || state.journey;
    }
    state.selectedStepID = state.journey.current_step_id || '';
    state.launchStage = '';
    state.managementView = false;
    if (
      previousRunID !== state.journey.run_id ||
      (!state.journey.busy && state.journey.receipts?.project_workspace_id)
    ) {
      state.draft = null;
      state.projectDrafts = {};
      state.pendingCommit = null;
    }
    state.review = null;
    state.reviewInput = null;
    render();
    if (globalThis.bootstrap?.Modal) {
      state.modal ||= globalThis.bootstrap.Modal.getOrCreateInstance(elements.root);
      state.modal.show();
      elements.root.addEventListener('shown.bs.modal', () => elements.stepTitle?.focus(), {
        once: true
      });
      elements.root.addEventListener('hidden.bs.modal', () => state.returnFocus?.focus?.(), {
        once: true
      });
    } else {
      elements.root.classList.add('show');
      elements.root.style.display = 'block';
      elements.root.removeAttribute('aria-hidden');
      elements.stepTitle?.focus();
    }
    return true;
  } catch (error) {
    showError(error.message);
    // A rejected deep link has no open modal to display its alert in.
    if (!elements.root.classList.contains('show')) {
      if (globalThis.Toast?.error) globalThis.Toast.error(error.message);
      else window.alert(error.message);
    }
    return false;
  } finally {
    setBusy(false);
  }
}

function initialize() {
  const elements = ui();
  if (!elements) return;
  elements.close?.addEventListener('click', dismissJourney);
  elements.later?.addEventListener('click', dismissJourney);
  window.addEventListener(
    'keydown',
    event => {
      if (event.key !== 'Escape' || !elements.root.classList.contains('show')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      if (state.commitLocked) {
        elements.live.textContent = 'Finish the reviewed change before closing setup.';
        return;
      }
      void dismissJourney();
    },
    true
  );
  window.addEventListener('ori:open-specialist-setup', openSpecialistSetupJourney);
  const params = new URLSearchParams(window.location.search);
  if (params.get('setup') === 'specialist') openSpecialistSetupJourney();
  if (params.get('setup') === 'quest') {
    openSpecialistSetupJourney(setupQuestSelectionFromParams(params));
  }
}

if (document.readyState === 'loading')
  document.addEventListener('DOMContentLoaded', initialize, { once: true });
else initialize();
