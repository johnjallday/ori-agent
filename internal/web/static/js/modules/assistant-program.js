import { workspaceRootURL } from './workspace-routes.js';

function text(value) {
  return String(value == null ? '' : value);
}

function setText(id, value) {
  const element = document.getElementById(id);
  if (element) element.textContent = text(value);
  return element;
}

function selectOption(label, value) {
  const option = document.createElement('option');
  option.value = value;
  option.textContent = label;
  return option;
}

async function responseJSON(response) {
  try {
    return await response.json();
  } catch (_) {
    return {};
  }
}

export class AssistantProgramPage {
  constructor({ workspaceId, workspaceSlug, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = text(workspaceId).trim();
    this.workspaceSlug = text(workspaceSlug).trim();
    // Keep browser fetch as a plain invocation. Calling a stored Window.fetch as
    // an instance method gives it the wrong receiver and fails before any API
    // request is made.
    this.fetchImpl = (...args) => fetchImpl(...args);
    this.program = null;
    this.learningDocument = null;
    this.providers = [];
  }

  workspaceURL(suffix = '') {
    return workspaceRootURL(this.workspaceSlug) + suffix;
  }

  apiURL(suffix = '') {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/assistant-program${suffix}`;
  }

  homeName(program = this.program) {
    return text(program?.declaration?.station_name).trim() || 'Team Home';
  }

  async init() {
    const workspaceURL = this.workspaceURL();
    for (const id of [
      'assistantProgramWorkspaceLink',
      'assistantProgramBack',
      'assistantProgramHireCancel'
    ]) {
      const element = document.getElementById(id);
      if (element) element.href = workspaceURL;
    }
    document.getElementById('assistantProgramHireForm')?.addEventListener('submit', event => {
      event.preventDefault();
      void this.hire();
    });
    document
      .getElementById('assistantProgramHireOpen')
      ?.addEventListener('click', () => this.openHire());
    document
      .getElementById('assistantProgramActivate')
      ?.addEventListener('click', () => void this.activate());
    document
      .getElementById('assistantProgramPromotionAck')
      ?.addEventListener('click', () => void this.acknowledgePromotion());
    document
      .getElementById('assistantProgramRemoveHome')
      ?.addEventListener('click', event => void this.openHomeRemovalReview(event.currentTarget));
    document
      .getElementById('assistantProgramReflect')
      ?.addEventListener('click', () => void this.reflect());
    document
      .getElementById('assistantProgramSuggest')
      ?.addEventListener('click', () => void this.generateSuggestions());
    document
      .getElementById('assistantProgramLearning')
      ?.addEventListener('click', event => void this.handleLearningAction(event));
    document
      .getElementById('assistantProgramSuggestions')
      ?.addEventListener('click', event => void this.handleSuggestionAction(event));
    document
      .getElementById('assistantProgramHireProvider')
      ?.addEventListener('change', () => this.renderHireModels());
    await this.loadProviderCatalog();
    await this.load();
  }

  async request(path = '', options = {}) {
    if (typeof this.fetchImpl !== 'function') throw new Error('Network access is unavailable');
    const response = await this.fetchImpl(this.apiURL(path), {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const payload = await responseJSON(response);
    if (!response.ok) {
      throw new Error(text(payload.error || `Request failed (${response.status})`));
    }
    return payload;
  }

  async loadProviderCatalog() {
    if (typeof this.fetchImpl !== 'function') return;
    try {
      const response = await this.fetchImpl('/api/providers', {
        headers: { Accept: 'application/json' }
      });
      const payload = await responseJSON(response);
      this.providers =
        response.ok && Array.isArray(payload.providers)
          ? payload.providers.filter(provider => provider.available)
          : [];
    } catch (_) {
      this.providers = [];
    }
    const select = document.getElementById('assistantProgramHireProvider');
    if (select) {
      select.replaceChildren(selectOption('Use Ori default', ''));
      for (const provider of this.providers) {
        select.append(selectOption(provider.display_name || provider.name, provider.name));
      }
    }
    this.renderHireModels();
  }

  renderHireModels() {
    const providerName = document.getElementById('assistantProgramHireProvider')?.value || '';
    const select = document.getElementById('assistantProgramHireModel');
    if (!select) return;
    const provider = this.providers.find(item => item.name === providerName);
    select.replaceChildren(
      selectOption(providerName ? 'Use provider default' : 'Use Ori default', '')
    );
    const seenModels = new Set();
    for (const model of provider?.models || []) {
      if (!model.value || seenModels.has(model.value)) continue;
      seenModels.add(model.value);
      select.append(selectOption(model.label || model.value, model.value));
    }
    select.disabled = !providerName;
  }

  async load() {
    try {
      const program = await this.request();
      if (!program.available) {
        this.showUnavailable(program.activation_needed);
        return;
      }
      this.program = program;
      this.render();
      if (!program.hired && Number(program.declaration?.schema_version || 1) < 2) this.openHire();
    } catch (error) {
      this.showError(error.message || 'This assistant home is unavailable.');
    }
  }

  showUnavailable(activationNeeded) {
    this.finishLoading();
    setText(
      'assistantProgramErrorText',
      activationNeeded
        ? 'This older project can join the assistant program. Activation is explicit and keeps the existing project intact.'
        : 'This workspace does not declare an assistant program.'
    );
    const activate = document.getElementById('assistantProgramActivate');
    if (activate) activate.hidden = !activationNeeded;
    const error = document.getElementById('assistantProgramError');
    if (error) error.hidden = false;
  }

  showError(message) {
    this.finishLoading();
    setText('assistantProgramErrorText', message);
    const error = document.getElementById('assistantProgramError');
    if (error) error.hidden = false;
  }

  finishLoading() {
    const page = document.getElementById('assistantProgramPage');
    const loading = document.getElementById('assistantProgramLoading');
    if (page) page.setAttribute('aria-busy', 'false');
    if (loading) loading.hidden = true;
  }

  render() {
    const program = this.program || {};
    const declaration = program.declaration || {};
    this.finishLoading();
    const error = document.getElementById('assistantProgramError');
    const content = document.getElementById('assistantProgramContent');
    if (error) error.hidden = true;
    if (content) content.hidden = false;

    const homeName = this.homeName(program);
    setText('assistantProgramBreadcrumbName', homeName);
    setText('assistantProgramTitle', homeName);
    setText(
      'assistantProgramDescription',
      declaration.station_description || 'A shared assistant for linked projects.'
    );
    setText(
      'assistantProgramStage',
      program.hired
        ? `Stage ${program.level || 1} — ${program.stage_label || 'Active'}`
        : 'Not hired'
    );
    setText(
      'assistantProgramProjectCount',
      `${(program.projects || []).length} linked ${(program.projects || []).length === 1 ? 'project' : 'projects'}`
    );
    setText('assistantProgramLevel', program.hired ? program.level || 1 : '—');

    const meter = document.getElementById('assistantProgramMeterValue');
    if (meter) {
      const stages = Array.isArray(declaration.stages) ? declaration.stages : [];
      const current = Math.max(
        0,
        stages.findIndex(stage => stage.id === program.stage_id)
      );
      const fraction = program.hired && stages.length > 1 ? (current + 1) / stages.length : 0;
      meter.style.strokeDashoffset = String(358.14 * (1 - fraction));
      const progress = meter.closest?.('.assistant-program-meter');
      if (progress) {
        progress.setAttribute('role', 'progressbar');
        progress.setAttribute('aria-valuemin', '1');
        progress.setAttribute('aria-valuemax', String(Math.max(1, stages.length)));
        progress.setAttribute('aria-valuenow', String(program.hired ? current + 1 : 1));
        progress.setAttribute(
          'aria-valuetext',
          program.hired
            ? `${program.stage_label || 'Active'}, level ${program.level || 1}`
            : 'Assistant not hired'
        );
      }
    }

    const disabled = document.getElementById('assistantProgramDisabled');
    if (disabled) disabled.hidden = program.plugin_available !== false;
    setText(
      'assistantProgramDisabledText',
      declaration.disabled_message ||
        'The contribution is unavailable. Existing assistant data remains readable.'
    );

    const scopedProgram = Number(declaration.schema_version || 1) >= 2;
    const rosterScope = program.roster_scope || (program.is_station ? 'home' : 'project');
    const visibleRoles = scopedProgram
      ? (declaration.roles || []).filter(role => role.scope === rosterScope)
      : declaration.roles || [];
    this.renderRoster(
      visibleRoles,
      program.roster || [],
      rosterScope,
      scopedProgram,
      program.role_profiles || []
    );
    this.renderStages(declaration.stages || []);
    this.renderProjects(program.projects || [], program.portfolio || []);

    const hireOpen = document.getElementById('assistantProgramHireOpen');
    if (hireOpen)
      hireOpen.hidden =
        scopedProgram || Boolean(program.hired) || program.plugin_available === false;
    const removeHome = document.getElementById('assistantProgramRemoveHome');
    if (removeHome) removeHome.hidden = !program.is_station || program.plugin_available === false;
    const promotionAck = document.getElementById('assistantProgramPromotionAck');
    if (promotionAck)
      promotionAck.hidden = !program.promotion_pending || program.plugin_available === false;
    const reflect = document.getElementById('assistantProgramReflect');
    if (reflect) reflect.disabled = !program.hired || program.plugin_available === false;
    const suggest = document.getElementById('assistantProgramSuggest');
    if (suggest)
      suggest.disabled =
        !program.hired ||
        program.plugin_available === false ||
        program.level < 2 ||
        !program.project_id;
    if (program.hired) void this.loadLearnings();
  }

  renderRoster(roles, roster, rosterScope = '', scopedProgram = false, profiles = []) {
    const root = document.getElementById('assistantProgramRoster');
    if (!root) return;
    root.replaceChildren();
    for (const role of roles) {
      const binding = roster.find(item => item.role_id === role.id);
      const profile = profiles.find(item => item.role_id === role.id);
      const card = document.createElement('article');
      card.className = 'assistant-program-role';
      card.dataset.primary = role.primary ? 'true' : 'false';
      const roleID = document.createElement('span');
      roleID.className = 'assistant-program-role-id';
      const scopeLabel = scopedProgram
        ? `${rosterScope === 'home' ? 'Home' : 'This project'} / `
        : '';
      roleID.textContent = role.primary
        ? `${scopeLabel}Primary / coordinator`
        : `${scopeLabel}Specialist / ${role.id}`;
      const title = document.createElement('h3');
      title.textContent = role.label || role.id;
      const description = document.createElement('p');
      description.textContent = role.description || '';
      card.append(roleID, title, description);
      const profileName = profile?.profile_name || binding?.agent_name;
      if (profileName) {
        const agentName = document.createElement('a');
        agentName.className = 'assistant-program-role-agent';
        agentName.href = `/agents/${encodeURIComponent(profileName)}`;
        agentName.textContent = `Open ${profileName} →`;
        card.append(agentName);
        if (scopedProgram) {
          const readiness = document.createElement('span');
          readiness.className = 'assistant-program-role-readiness';
          readiness.textContent = profile?.chat_available
            ? `${profile.provider || 'Default provider'} · ${profile.model || 'Provider default'}`
            : 'Chat/execution unavailable — no compatible model is configured';
          card.append(readiness);
        }
      } else if (scopedProgram) {
        const unfinished = document.createElement('span');
        unfinished.className = 'assistant-program-role-readiness';
        unfinished.textContent = `Not staffed in ${rosterScope === 'home' ? 'Home' : 'this project'}`;
        card.append(unfinished);
      }
      root.append(card);
    }
  }

  renderStages(stages) {
    const root = document.getElementById('assistantProgramStages');
    if (!root) return;
    root.replaceChildren();
    const currentIndex = stages.findIndex(stage => stage.id === this.program.stage_id);
    stages.forEach((stage, index) => {
      const item = document.createElement('div');
      item.className = 'assistant-program-stage';
      if (index === currentIndex) item.classList.add('is-current');
      if (index < currentIndex) item.classList.add('is-complete');
      const marker = document.createElement('span');
      marker.className = 'assistant-program-stage-marker';
      marker.textContent = String(index + 1).padStart(2, '0');
      const copy = document.createElement('div');
      const title = document.createElement('strong');
      title.textContent = stage.label || stage.id;
      const description = document.createElement('span');
      description.textContent = stage.description || '';
      copy.append(title, description);
      item.append(marker, copy);
      root.append(item);
    });
    const acceptedTasks = Number(this.program.accepted_tasks) || 0;
    setText(
      'assistantProgramRemaining',
      this.program.next_threshold > 0
        ? `${acceptedTasks} accepted completions · ${this.program.remaining} until ${stages[currentIndex + 1]?.label || 'the next stage'}`
        : this.program.hired
          ? `${acceptedTasks} accepted completions · highest stage reached`
          : 'Progress begins after the roster is hired.'
    );
  }

  renderProjects(projects, portfolio = []) {
    const root = document.getElementById('assistantProgramProjects');
    if (!root) return;
    root.replaceChildren();
    if (!projects.length) {
      const empty = document.createElement('p');
      empty.className = 'assistant-program-project-empty';
      empty.textContent = 'No linked projects yet.';
      root.append(empty);
      return;
    }
    const portfolioByProject = new Map(portfolio.map(item => [item.project_workspace_id, item]));
    for (const project of projects) {
      const card = document.createElement('article');
      card.className = 'assistant-program-project-card';
      const link = document.createElement('a');
      link.className = 'assistant-program-project';
      if (project.id === this.program.project_id) link.classList.add('is-current');
      link.href = project.folder_slug ? workspaceRootURL(project.folder_slug) : '#';
      link.textContent = project.name || project.id;
      card.append(link);
      const record = portfolioByProject.get(project.id);
      if (record) {
        const details = document.createElement('p');
        details.className = 'assistant-program-project-meta';
        details.textContent = `${text(record.fields?.status || 'planning').replaceAll('_', ' ')} · Priority ${Number(record.fields?.priority || 0)} · ${Number(record.open_ticket_count || 0)} open Tickets`;
        card.append(details);
        if (this.program.is_station) {
          const actions = document.createElement('div');
          actions.className = 'assistant-program-project-actions';
          const edit = document.createElement('button');
          edit.type = 'button';
          edit.className = 'modern-btn modern-btn-secondary';
          edit.textContent = 'Edit portfolio details';
          edit.addEventListener('click', () => this.openPortfolioEditor(record, edit));
          const handoff = document.createElement('button');
          handoff.type = 'button';
          handoff.className = 'modern-btn modern-btn-secondary';
          handoff.textContent = 'Send to project';
          handoff.addEventListener('click', () => this.openHandoffEditor(record, handoff));
          const disconnect = document.createElement('button');
          disconnect.type = 'button';
          disconnect.className = 'modern-btn modern-btn-secondary';
          disconnect.textContent = 'Review disconnect';
          disconnect.addEventListener('click', () =>
            this.openDisconnectReview(project, disconnect)
          );
          actions.append(edit, handoff, disconnect);
          card.append(actions);
        }
      }
      root.append(card);
    }
  }

  async openHomeRemovalReview(trigger) {
    const { dialog, close } = this.newActionDialog('Review Home removal', trigger);
    const heading = document.createElement('h3');
    heading.textContent = `Remove ${this.homeName()}?`;
    const progress = document.createElement('p');
    progress.setAttribute('role', 'status');
    progress.setAttribute('aria-live', 'polite');
    progress.textContent = 'Loading the current impact…';
    const controls = document.createElement('div');
    controls.className = 'assistant-program-action-buttons';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'modern-btn modern-btn-secondary';
    cancel.textContent = 'Cancel';
    cancel.addEventListener('click', close);
    controls.append(cancel);
    dialog.append(heading, progress, controls);
    try {
      const review = await this.request('/remove-home/review', {
        method: 'POST',
        body: JSON.stringify({ state_revision: Number(this.program?.state_revision || 0) })
      });
      progress.textContent = `${Number(review.linked_project_count || 0)} linked projects will be retained.`;
      const impact = document.createElement('ul');
      impact.className = 'assistant-program-impact-list';
      (review.impact || []).forEach(line => {
        const item = document.createElement('li');
        item.textContent = line;
        impact.append(item);
      });
      const confirm = document.createElement('button');
      confirm.type = 'button';
      confirm.className = 'modern-btn modern-btn-danger';
      confirm.textContent = 'Remove Home and retain projects';
      confirm.addEventListener('click', async () => {
        confirm.disabled = true;
        cancel.disabled = true;
        progress.textContent = 'Removing Home…';
        try {
          await this.request('/remove-home/commit', {
            method: 'POST',
            body: JSON.stringify({ token: review.token })
          });
          globalThis.location.assign(this.workspaceURL());
        } catch (error) {
          progress.textContent = error.message || 'The Home could not be removed.';
          confirm.disabled = false;
          cancel.disabled = false;
        }
      });
      dialog.insertBefore(impact, controls);
      controls.append(confirm);
      confirm.focus();
    } catch (error) {
      progress.textContent = error.message || 'The removal impact could not be reviewed.';
      cancel.focus();
    }
  }

  async openDisconnectReview(project, trigger) {
    const { dialog, close } = this.newActionDialog(
      `Review disconnect for ${project.name || 'project'}`,
      trigger
    );
    const heading = document.createElement('h3');
    heading.textContent = `Disconnect ${project.name || 'project'}?`;
    const progress = document.createElement('p');
    progress.setAttribute('role', 'status');
    progress.setAttribute('aria-live', 'polite');
    progress.textContent = 'Loading the current impact…';
    const controls = document.createElement('div');
    controls.className = 'assistant-program-action-buttons';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'modern-btn modern-btn-secondary';
    cancel.textContent = 'Cancel';
    cancel.addEventListener('click', close);
    controls.append(cancel);
    dialog.append(heading, progress, controls);
    try {
      const review = await this.request('/disconnect/review', {
        method: 'POST',
        body: JSON.stringify({
          project_workspace_id: project.id,
          state_revision: Number(this.program?.state_revision || 0)
        })
      });
      progress.textContent = '';
      const impact = document.createElement('ul');
      impact.className = 'assistant-program-impact-list';
      (review.impact || []).forEach(line => {
        const item = document.createElement('li');
        item.textContent = line;
        impact.append(item);
      });
      const confirm = document.createElement('button');
      confirm.type = 'button';
      confirm.className = 'modern-btn modern-btn-danger';
      confirm.textContent = 'Disconnect and preserve project';
      confirm.addEventListener('click', async () => {
        confirm.disabled = true;
        cancel.disabled = true;
        progress.textContent = 'Disconnecting…';
        try {
          await this.request('/disconnect/commit', {
            method: 'POST',
            body: JSON.stringify({
              token: review.token,
              idempotency_key: globalThis.crypto?.randomUUID?.() || `disconnect-${Date.now()}`
            })
          });
          close();
          await this.load();
        } catch (error) {
          progress.textContent = error.message || 'The project could not be disconnected.';
          confirm.disabled = false;
          cancel.disabled = false;
        }
      });
      dialog.insertBefore(impact, controls);
      controls.append(confirm);
      confirm.focus();
    } catch (error) {
      progress.textContent = error.message || 'The disconnect impact could not be reviewed.';
      cancel.focus();
    }
  }

  newActionDialog(title, trigger) {
    const dialog = document.createElement('dialog');
    dialog.className = 'assistant-program-action-dialog';
    dialog.setAttribute('aria-label', title);
    const close = () => {
      if (dialog.open && typeof dialog.close === 'function') dialog.close();
      dialog.remove();
      trigger?.focus?.();
    };
    dialog.addEventListener('cancel', event => {
      event.preventDefault();
      close();
    });
    document.body.append(dialog);
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    return { dialog, close };
  }

  actionField(label, value = '', type = 'text') {
    const wrapper = document.createElement('label');
    wrapper.className = 'assistant-program-action-field';
    const caption = document.createElement('span');
    caption.textContent = label;
    const input = document.createElement('input');
    input.type = type;
    input.value = text(value);
    input.maxLength = type === 'number' || type === 'date' ? 32 : 240;
    wrapper.append(caption, input);
    return { wrapper, input };
  }

  renderActionReview(dialog, title, rows, onConfirm, close) {
    dialog.replaceChildren();
    const heading = document.createElement('h2');
    heading.textContent = title;
    const note = document.createElement('p');
    note.textContent = 'Review these exact effects. Nothing else is authorized.';
    const list = document.createElement('dl');
    list.className = 'assistant-program-action-review';
    for (const [label, value] of rows) {
      const term = document.createElement('dt');
      term.textContent = label;
      const detail = document.createElement('dd');
      detail.textContent = text(value);
      list.append(term, detail);
    }
    const error = document.createElement('p');
    error.className = 'assistant-program-action-error';
    error.setAttribute('role', 'alert');
    const controls = document.createElement('div');
    controls.className = 'assistant-program-project-actions';
    const back = document.createElement('button');
    back.type = 'button';
    back.className = 'modern-btn modern-btn-secondary';
    back.textContent = 'Cancel';
    back.addEventListener('click', close);
    const confirm = document.createElement('button');
    confirm.type = 'button';
    confirm.className = 'modern-btn modern-btn-primary';
    confirm.textContent = 'Confirm reviewed change';
    confirm.addEventListener('click', async () => {
      confirm.disabled = true;
      try {
        await onConfirm();
        close();
      } catch (requestError) {
        error.textContent = requestError.message || 'The reviewed change was not applied.';
        confirm.disabled = false;
      }
    });
    controls.append(back, confirm);
    dialog.append(heading, note, list, error, controls);
    confirm.focus();
  }

  announceAction(message) {
    setText('assistantProgramActionLive', message);
  }

  openPortfolioEditor(record, trigger) {
    const { dialog, close } = this.newActionDialog(
      `Edit portfolio details for ${record.project_name}`,
      trigger
    );
    const form = document.createElement('form');
    form.className = 'assistant-program-action-form';
    const heading = document.createElement('h2');
    heading.textContent = `Portfolio details · ${record.project_name}`;
    const statusLabel = document.createElement('label');
    statusLabel.className = 'assistant-program-action-field';
    const statusCaption = document.createElement('span');
    statusCaption.textContent = 'Status';
    const status = document.createElement('select');
    for (const value of ['planning', 'active', 'on_hold', 'complete', 'archived'])
      status.append(selectOption(value.replaceAll('_', ' '), value));
    status.value = record.fields?.status || 'planning';
    statusLabel.append(statusCaption, status);
    const priority = this.actionField('Priority (0–5)', record.fields?.priority || 0, 'number');
    priority.input.min = '0';
    priority.input.max = '5';
    const sessionDate = this.actionField('Session date', record.fields?.session_date || '', 'date');
    const releaseDate = this.actionField('Release date', record.fields?.release_date || '', 'date');
    const blockers = document.createElement('label');
    blockers.className = 'assistant-program-action-field';
    blockers.append(document.createElement('span'), document.createElement('textarea'));
    blockers.firstChild.textContent = 'Blockers (one per line)';
    blockers.lastChild.value = (record.fields?.blockers || []).join('\n');
    blockers.lastChild.maxLength = 4096;
    const deliverables = document.createElement('label');
    deliverables.className = 'assistant-program-action-field';
    deliverables.append(document.createElement('span'), document.createElement('textarea'));
    deliverables.firstChild.textContent = 'Deliverables (one per line)';
    deliverables.lastChild.value = (record.fields?.deliverables || []).join('\n');
    deliverables.lastChild.maxLength = 4096;
    const archiveLabel = document.createElement('label');
    archiveLabel.className = 'assistant-program-action-field';
    const archiveCaption = document.createElement('span');
    archiveCaption.textContent = 'Archive review';
    const archive = document.createElement('select');
    for (const value of ['not_ready', 'ready', 'reviewed'])
      archive.append(selectOption(value.replaceAll('_', ' '), value));
    archive.value = record.fields?.archive_review_state || 'not_ready';
    archiveLabel.append(archiveCaption, archive);
    const error = document.createElement('p');
    error.className = 'assistant-program-action-error';
    error.setAttribute('role', 'alert');
    const controls = document.createElement('div');
    controls.className = 'assistant-program-project-actions';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'modern-btn modern-btn-secondary';
    cancel.textContent = 'Cancel';
    cancel.addEventListener('click', close);
    const reviewButton = document.createElement('button');
    reviewButton.type = 'submit';
    reviewButton.className = 'modern-btn modern-btn-primary';
    reviewButton.textContent = 'Review portfolio change';
    controls.append(cancel, reviewButton);
    form.append(
      heading,
      statusLabel,
      priority.wrapper,
      sessionDate.wrapper,
      releaseDate.wrapper,
      blockers,
      deliverables,
      archiveLabel,
      error,
      controls
    );
    form.addEventListener('submit', async event => {
      event.preventDefault();
      reviewButton.disabled = true;
      const lines = element =>
        element.value
          .split(/\r?\n/)
          .map(value => value.trim())
          .filter(Boolean);
      const fields = {
        status: status.value,
        priority: Number(priority.input.value || 0),
        milestones: record.fields?.milestones || [],
        session_date: sessionDate.input.value || undefined,
        release_date: releaseDate.input.value || undefined,
        blockers: lines(blockers.lastChild),
        deliverables: lines(deliverables.lastChild),
        archive_review_state: archive.value
      };
      try {
        const review = await this.request('/portfolio/review', {
          method: 'POST',
          body: JSON.stringify({
            link_id: record.link_id,
            if_revision: Number(record.state_revision || 0),
            fields
          })
        });
        const reviewed = review.project;
        this.renderActionReview(
          dialog,
          `Confirm portfolio change · ${reviewed.project_name}`,
          [
            ['Exact project link', reviewed.link_id],
            ['Status', reviewed.fields.status],
            ['Priority', reviewed.fields.priority],
            ['Session date', reviewed.fields.session_date || 'Not set'],
            ['Release date', reviewed.fields.release_date || 'Not set'],
            ['Blockers', reviewed.fields.blockers?.join('; ') || 'None'],
            ['Deliverables', reviewed.fields.deliverables?.join('; ') || 'None'],
            ['Archive review', reviewed.fields.archive_review_state],
            ['Archive guidance', reviewed.archive_guidance?.join(' ') || 'None']
          ],
          async () => {
            const receipt = await this.request('/portfolio/commit', {
              method: 'POST',
              body: JSON.stringify({
                review_token: review.token,
                idempotency_key: globalThis.crypto?.randomUUID?.() || `portfolio-${Date.now()}`,
                fields
              })
            });
            this.announceAction(
              `Portfolio details saved at revision ${receipt.state_revision}. No project files were changed.`
            );
            await this.load();
          },
          close
        );
      } catch (requestError) {
        error.textContent = requestError.message || 'Portfolio review is unavailable.';
        reviewButton.disabled = false;
      }
    });
    dialog.append(form);
    status.focus();
  }

  openHandoffEditor(record, trigger) {
    const { dialog, close } = this.newActionDialog(`Send work to ${record.project_name}`, trigger);
    const form = document.createElement('form');
    form.className = 'assistant-program-action-form';
    const heading = document.createElement('h2');
    heading.textContent = `Send to project · ${record.project_name}`;
    const title = this.actionField('Ticket title');
    title.input.required = true;
    const description = document.createElement('label');
    description.className = 'assistant-program-action-field';
    description.append(document.createElement('span'), document.createElement('textarea'));
    description.firstChild.textContent = 'Description';
    description.lastChild.maxLength = 8000;
    const stateLabel = document.createElement('label');
    stateLabel.className = 'assistant-program-action-field';
    const stateCaption = document.createElement('span');
    stateCaption.textContent = 'Child Ticket state';
    const ticketState = document.createElement('select');
    ticketState.append(selectOption('Backlog', 'backlog'), selectOption('Ready', 'ready'));
    stateLabel.append(stateCaption, ticketState);
    const error = document.createElement('p');
    error.className = 'assistant-program-action-error';
    error.setAttribute('role', 'alert');
    const controls = document.createElement('div');
    controls.className = 'assistant-program-project-actions';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'modern-btn modern-btn-secondary';
    cancel.textContent = 'Cancel';
    cancel.addEventListener('click', close);
    const reviewButton = document.createElement('button');
    reviewButton.type = 'submit';
    reviewButton.className = 'modern-btn modern-btn-primary';
    reviewButton.textContent = 'Review child Ticket';
    controls.append(cancel, reviewButton);
    form.append(heading, title.wrapper, description, stateLabel, error, controls);
    form.addEventListener('submit', async event => {
      event.preventDefault();
      reviewButton.disabled = true;
      const input = {
        link_id: record.link_id,
        title: title.input.value.trim(),
        description: description.lastChild.value.trim() || undefined,
        state: ticketState.value
      };
      try {
        const review = await this.request('/handoffs/review', {
          method: 'POST',
          body: JSON.stringify(input)
        });
        const handoff = review.handoff;
        this.renderActionReview(
          dialog,
          `Confirm child Ticket · ${handoff.project_name}`,
          [
            ['Exact project link', handoff.link_id],
            ['Ticket title', handoff.title],
            ['Description', handoff.description || 'None'],
            ['State', handoff.state],
            ['Assignment', handoff.assignment],
            ['Suggested assignee', handoff.suggested_assignee || 'Not available'],
            ['Authority boundary', handoff.authority_boundary]
          ],
          async () => {
            const receipt = await this.request('/handoffs/commit', {
              method: 'POST',
              body: JSON.stringify({
                review_token: review.token,
                idempotency_key: globalThis.crypto?.randomUUID?.() || `handoff-${Date.now()}`,
                title: input.title,
                description: input.description,
                state: input.state
              })
            });
            this.announceAction(
              `Created child-owned Ticket #${receipt.ticket_number || receipt.ticket_id}. No project tools were granted to Home.`
            );
            await this.load();
          },
          close
        );
      } catch (requestError) {
        error.textContent = requestError.message || 'Handoff review is unavailable.';
        reviewButton.disabled = false;
      }
    });
    dialog.append(form);
    title.input.focus();
  }

  openHire() {
    const declaration = this.program?.declaration || {};
    setText('assistantProgramHireTitle', declaration.hire_title || 'Hire your assistant');
    setText(
      'assistantProgramHireDescription',
      declaration.hire_description ||
        'Choose a name for the primary assistant and review the bounded roster.'
    );
    const name = document.getElementById('assistantProgramHireName');
    if (name && !name.value) name.value = declaration.default_primary_name || '';
    const cancel = document.getElementById('assistantProgramHireCancel');
    if (cancel) cancel.href = this.workspaceURL();
    const rolesRoot = document.getElementById('assistantProgramHireRoles');
    if (rolesRoot) {
      rolesRoot.replaceChildren();
      for (const role of declaration.roles || []) {
        const row = document.createElement('div');
        row.className = 'assistant-program-hire-role';
        const title = document.createElement('strong');
        title.textContent = role.label || role.id;
        const description = document.createElement('span');
        description.textContent = role.description || '';
        row.append(title, description);
        rolesRoot.append(row);
      }
    }
    setText('assistantProgramHireError', '');
    const dialog = document.getElementById('assistantProgramHireDialog');
    if (!dialog) return;
    if (typeof dialog.showModal === 'function' && !dialog.open) dialog.showModal();
    else dialog.setAttribute('open', '');
    name?.focus();
  }

  async hire() {
    const submit = document.getElementById('assistantProgramHireSubmit');
    const request = {
      name: text(document.getElementById('assistantProgramHireName')?.value).trim(),
      provider: text(document.getElementById('assistantProgramHireProvider')?.value).trim(),
      model: text(document.getElementById('assistantProgramHireModel')?.value).trim(),
      version: Number(this.program?.state_revision || 0)
    };
    if (!request.name) {
      setText('assistantProgramHireError', 'Choose a name for the primary assistant.');
      return;
    }
    if (submit) submit.disabled = true;
    setText('assistantProgramHireError', '');
    try {
      this.program = await this.request('/hire', { method: 'POST', body: JSON.stringify(request) });
      const dialog = document.getElementById('assistantProgramHireDialog');
      if (dialog?.open && typeof dialog.close === 'function') dialog.close();
      this.render();
    } catch (error) {
      setText('assistantProgramHireError', error.message || 'The assistant could not be hired.');
    } finally {
      if (submit) submit.disabled = false;
    }
  }

  async activate() {
    const button = document.getElementById('assistantProgramActivate');
    if (button) button.disabled = true;
    try {
      this.program = await this.request('/activate', { method: 'POST' });
      this.render();
      if (!this.program.hired && Number(this.program.declaration?.schema_version || 1) < 2)
        this.openHire();
    } catch (error) {
      setText('assistantProgramErrorText', error.message || 'Activation failed.');
    } finally {
      if (button) button.disabled = false;
    }
  }

  async learningRequest(path = '', options = {}) {
    if (typeof this.fetchImpl !== 'function') throw new Error('Network access is unavailable');
    const response = await this.fetchImpl(`${this.apiURL('/learnings')}${path}`, {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const payload = await responseJSON(response);
    if (!response.ok) throw new Error(text(payload.error || `Request failed (${response.status})`));
    return payload;
  }

  async loadLearnings() {
    try {
      this.learningDocument = await this.learningRequest();
      this.renderLearnings();
      this.renderSuggestions();
    } catch (error) {
      const root = document.getElementById('assistantProgramLearning');
      if (root) root.textContent = error.message || 'Learnings are unavailable.';
    }
  }

  renderLearnings() {
    const root = document.getElementById('assistantProgramLearning');
    if (!root) return;
    root.replaceChildren();
    const documentState = this.learningDocument || {};
    const candidates = (documentState.candidates || []).filter(
      candidate => !candidate.rejected_at && !candidate.approved_learning_id
    );
    const learnings = (documentState.learnings || []).filter(
      learning => !learning.deleted_at && (learning.revisions || []).length
    );
    if (!candidates.length && !learnings.length) {
      root.className = 'assistant-program-learning-empty';
      root.textContent = 'No pending candidates or approved learnings yet.';
    } else {
      root.className = 'assistant-program-learning-list';
    }
    for (const candidate of candidates) {
      root.append(this.learningCard(candidate, 'candidate'));
    }
    for (const learning of learnings) {
      root.append(this.learningCard(learning, 'learning'));
    }
    const runs = documentState.runs || [];
    if (runs.length) {
      const diagnostic = document.createElement('p');
      diagnostic.className = 'assistant-program-learning-diagnostic';
      const last = runs[runs.length - 1];
      diagnostic.textContent = `Last reflection: ${last.status}${last.summary ? ` — ${last.summary}` : ''}`;
      root.append(diagnostic);
    }
  }

  renderSuggestions() {
    const root = document.getElementById('assistantProgramSuggestions');
    if (!root) return;
    root.replaceChildren();
    const suggestions = (this.learningDocument?.suggestions || []).filter(
      item => item.project_id === this.program?.project_id && !item.dismissed_at
    );
    if (!suggestions.length) {
      root.className = 'assistant-program-learning-empty';
      root.textContent =
        this.program?.level >= 2
          ? 'No suggestions yet. Find suggestions from your approved learnings.'
          : 'Suggestions unlock at Collaborator stage.';
      return;
    }
    root.className = 'assistant-program-learning-list';
    for (const suggestion of suggestions) {
      const card = document.createElement('article');
      card.className = 'assistant-program-learning-card';
      const meta = document.createElement('span');
      meta.className = 'assistant-program-role-id';
      meta.textContent = suggestion.accepted_at
        ? 'Accepted · in Backlog'
        : 'Recommendation · review first';
      const copy = document.createElement('p');
      copy.textContent = suggestion.text;
      const rationale = document.createElement('p');
      rationale.className = 'assistant-program-learning-copy';
      rationale.textContent = suggestion.rationale;
      const targetProject = (this.program?.projects || []).find(
        item => item.id === suggestion.project_id
      );
      const capabilities = this.program?.declaration?.suggestion_required_capabilities || [];
      const impact = document.createElement('p');
      impact.className = 'assistant-program-learning-impact';
      impact.textContent = `Target: ${targetProject?.name || suggestion.project_id}. Required capabilities: ${capabilities.length ? capabilities.join(', ') : 'none declared'}. Adding this to Backlog does not change the project; execution still requires ordinary confirmation and readiness checks.`;
      const actions = document.createElement('div');
      actions.className = 'assistant-program-learning-actions';
      if (suggestion.accepted_at && suggestion.task_id) {
        const project = (this.program?.projects || []).find(
          item => item.id === suggestion.project_id
        );
        const link = document.createElement('a');
        link.className = 'modern-btn modern-btn-secondary modern-btn-sm';
        link.href = project?.folder_slug
          ? `${workspaceRootURL(project.folder_slug)}/task/${encodeURIComponent(suggestion.task_id)}`
          : '#';
        link.textContent = 'Review task';
        actions.append(link);
      } else if (this.program?.plugin_available !== false) {
        actions.append(
          this.learningButton('Add to Backlog', 'accept-suggestion', suggestion.id),
          this.learningButton('Dismiss', 'dismiss-suggestion', suggestion.id)
        );
      }
      card.append(meta, copy, rationale, impact, actions);
      root.append(card);
    }
  }

  learningCard(record, kind) {
    const candidate = kind === 'candidate';
    const revision = candidate ? record : record.revisions[record.revisions.length - 1];
    const card = document.createElement('article');
    card.className = 'assistant-program-learning-card';
    const meta = document.createElement('span');
    meta.className = 'assistant-program-role-id';
    meta.textContent = candidate
      ? `Pending · ${revision.confidence}`
      : `Approved · ${revision.confidence}`;
    const copy = document.createElement('p');
    copy.textContent = revision.text || '';
    const evidence = document.createElement('details');
    const summary = document.createElement('summary');
    summary.textContent = `${(revision.evidence || []).length} evidence references`;
    evidence.append(summary);
    const evidenceList = document.createElement('ul');
    for (const item of revision.evidence || []) {
      const row = document.createElement('li');
      if (item.project_slug && item.route) {
        const link = document.createElement('a');
        link.href = item.route;
        link.textContent = item.summary;
        row.append(link);
      } else {
        row.textContent = item.summary;
      }
      evidenceList.append(row);
    }
    evidence.append(evidenceList);
    const actions = document.createElement('div');
    actions.className = 'assistant-program-learning-actions';
    if (this.program?.plugin_available !== false) {
      if (candidate) {
        actions.append(
          this.learningButton('Approve', 'approve-candidate', record.id),
          this.learningButton('Edit', 'edit-candidate', record.id),
          this.learningButton('Reject', 'reject-candidate', record.id),
          this.learningButton('Delete', 'delete-candidate', record.id)
        );
      } else {
        actions.append(
          this.learningButton('Edit', 'edit-learning', record.id, record.version),
          this.learningButton('Delete', 'delete-learning', record.id, record.version)
        );
      }
    }
    card.append(meta, copy, evidence, actions);
    return card;
  }

  learningButton(label, action, id, version = '') {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'modern-btn modern-btn-secondary modern-btn-sm';
    button.textContent = label;
    button.dataset.learningAction = action;
    button.dataset.recordId = id;
    if (version !== '') button.dataset.recordVersion = String(version);
    return button;
  }

  async handleLearningAction(event) {
    const button = event.target.closest?.('[data-learning-action]');
    if (!button || button.disabled) return;
    const action = button.dataset.learningAction;
    const id = button.dataset.recordId;
    button.disabled = true;
    try {
      if (action === 'approve-candidate' || action === 'reject-candidate') {
        const verb = action.startsWith('approve') ? 'approve' : 'reject';
        await this.request(`/candidates/${encodeURIComponent(id)}/${verb}`, {
          method: 'POST',
          body: JSON.stringify({ version: Number(this.learningDocument?.version || 0) })
        });
      } else if (action === 'edit-candidate') {
        const candidate = (this.learningDocument?.candidates || []).find(item => item.id === id);
        const nextText = globalThis.prompt?.('Edit pending learning', candidate?.text || '');
        if (nextText == null) return;
        await this.request(`/candidates/${encodeURIComponent(id)}`, {
          method: 'PATCH',
          body: JSON.stringify({
            version: Number(this.learningDocument?.version || 0),
            text: nextText,
            type: candidate.type,
            confidence: candidate.confidence
          })
        });
      } else if (action === 'delete-candidate') {
        if (
          globalThis.confirm &&
          !globalThis.confirm(
            'Delete this pending learning? Rejected evidence will not be proposed again.'
          )
        )
          return;
        await this.request(`/candidates/${encodeURIComponent(id)}`, {
          method: 'DELETE',
          body: JSON.stringify({ version: Number(this.learningDocument?.version || 0) })
        });
      } else if (action === 'edit-learning') {
        const learning = (this.learningDocument?.learnings || []).find(item => item.id === id);
        const revision = learning?.revisions?.[learning.revisions.length - 1];
        const nextText = globalThis.prompt?.('Edit approved learning', revision?.text || '');
        if (nextText == null) return;
        await this.learningRequest(`/${encodeURIComponent(id)}`, {
          method: 'PATCH',
          body: JSON.stringify({
            version: Number(button.dataset.recordVersion),
            text: nextText,
            type: revision.type,
            confidence: revision.confidence
          })
        });
      } else if (action === 'delete-learning') {
        if (
          globalThis.confirm &&
          !globalThis.confirm('Delete this approved learning? The audit tombstone will remain.')
        )
          return;
        await this.learningRequest(`/${encodeURIComponent(id)}`, {
          method: 'DELETE',
          body: JSON.stringify({ version: Number(button.dataset.recordVersion) })
        });
      }
      await this.loadLearnings();
    } catch (error) {
      button.disabled = false;
      button.textContent = error.message || 'Try again';
    }
  }

  async generateSuggestions() {
    const button = document.getElementById('assistantProgramSuggest');
    if (button) button.disabled = true;
    try {
      this.learningDocument = await this.request('/suggestions/generate', {
        method: 'POST',
        body: JSON.stringify({
          version: Number(this.learningDocument?.version || 0),
          project_id: this.program?.project_id || ''
        })
      });
      this.renderSuggestions();
    } catch (error) {
      const root = document.getElementById('assistantProgramSuggestions');
      if (root) root.textContent = error.message || 'Suggestions could not be generated.';
    } finally {
      if (button)
        button.disabled =
          this.program?.plugin_available === false ||
          this.program?.level < 2 ||
          !this.program?.project_id;
    }
  }

  async handleSuggestionAction(event) {
    const button = event.target.closest?.('[data-learning-action]');
    if (!button || button.disabled) return;
    const action = button.dataset.learningAction;
    if (action !== 'accept-suggestion' && action !== 'dismiss-suggestion') return;
    button.disabled = true;
    const verb = action.startsWith('accept') ? 'accept' : 'dismiss';
    try {
      await this.request(`/suggestions/${encodeURIComponent(button.dataset.recordId)}/${verb}`, {
        method: 'POST',
        body: JSON.stringify({ version: Number(this.learningDocument?.version || 0) })
      });
      await this.loadLearnings();
    } catch (error) {
      button.disabled = false;
      button.textContent = error.message || 'Try again';
    }
  }

  async reflect() {
    const button = document.getElementById('assistantProgramReflect');
    if (button) {
      button.disabled = true;
      button.textContent = 'Reflecting…';
    }
    try {
      await this.request('/reflection', { method: 'POST' });
      await this.loadLearnings();
    } catch (error) {
      const root = document.getElementById('assistantProgramLearning');
      if (root) root.textContent = error.message || 'Reflection could not run.';
    } finally {
      if (button) {
        button.disabled = this.program?.plugin_available === false;
        button.textContent = 'Reflect';
      }
    }
  }

  async acknowledgePromotion() {
    const button = document.getElementById('assistantProgramPromotionAck');
    if (button) button.disabled = true;
    try {
      await this.request('/promotion/ack', { method: 'POST' });
      await this.load();
    } catch (error) {
      if (button) {
        button.disabled = false;
        button.textContent = error.message || 'Try acknowledgement again';
      }
    }
  }
}
