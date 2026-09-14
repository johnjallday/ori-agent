// Group Templates inside the shared Workspace / Group creator.
//
// sessions.js stays the one modal controller. This module owns only the
// Group-template chooser and the managed Home review/commit protocol against
// /api/workspaces/group-templates. General keeps its reviewed Group Manager
// roster; a managed template creates or reuses exactly one program Home and
// nothing else. All state lives on the creator context, so a new dialog
// generation can never be touched by a stale response.
(function () {
  'use strict';

  const LIST_URL = '/api/workspaces/group-templates';
  const REVIEW_URL = '/api/workspaces/group-templates/review';
  const COMMIT_URL = '/api/workspaces/group-templates/commit';
  const GENERAL_ID = 'general';
  const USABLE = ['creatable', 'reusable'];

  function isManaged(entry) {
    return entry?.kind === 'managed_home';
  }

  function isReusable(entry) {
    return isManaged(entry) && entry?.availability?.state === 'reusable';
  }

  function isSelectable(entry) {
    return Boolean(entry) && (!isManaged(entry) || USABLE.includes(entry?.availability?.state));
  }

  // Mirrors the server's declaration rule for Home display names.
  function nameProblem(value) {
    const name = String(value || '').trim();
    if (!name) return 'Group name is required';
    if (new TextEncoder().encode(name).length > 120) return 'Group name must be 120 bytes or fewer';
    if (/[/\\]/.test(name) || name.toLowerCase().includes('://')) {
      return 'Group name cannot contain a path or protocol';
    }
    for (const character of name) {
      const code = character.codePointAt(0);
      if (code < 32 || code === 127) return 'Group name cannot contain control characters';
    }
    return '';
  }

  function roleLabels(entry, required) {
    return (Array.isArray(entry?.home_roles) ? entry.home_roles : [])
      .filter(role => Boolean(role?.required) === required)
      .map(role => String(role?.label || '').trim())
      .filter(Boolean);
  }

  function providerLabel(entry) {
    const provider = entry?.provider;
    if (!provider) return '';
    if (provider.kind === 'plugin') {
      return `Plugin: ${provider.plugin_id}${provider.plugin_version ? ` ${provider.plugin_version}` : ''}`;
    }
    return provider.template_name ? `Your template: ${provider.template_name}` : 'Your template';
  }

  const AVAILABILITY_COPY = {
    creatable: 'Ready to create',
    reusable: 'Group exists',
    unavailable: 'Unavailable',
    existing_only_missing: 'Needs an existing group',
    conflict: 'Source conflict',
    ambiguous: 'Needs attention',
    incompatible: 'Needs migration'
  };

  function availabilityLabel(entry) {
    return AVAILABILITY_COPY[entry?.availability?.state] || 'Unavailable';
  }

  function unavailableNote(entry) {
    switch (entry?.availability?.state) {
      case 'unavailable':
        return entry?.home?.state === 'exists'
          ? `Its source is not available right now. The existing group “${entry.home.name}” stays readable.`
          : 'Its source plugin or template is not available right now.';
      case 'existing_only_missing':
        return 'This template can only use a group that already exists.';
      case 'conflict':
        return 'Two trusted sources define this group differently. Nothing can be created until they agree.';
      case 'ambiguous':
        return 'More than one group claims this program. Open the groups to resolve it.';
      case 'incompatible':
        return 'An existing group for this program needs its guided migration first.';
      default:
        return '';
    }
  }

  // The canonical request fields. Nothing else is ever sent: ownership, source,
  // parent, team, and project fields are server-derived or out of scope.
  function reviewRequest(entry, name) {
    return {
      group_template_id: String(entry?.id || ''),
      revision: String(entry?.revision || ''),
      name: String(name || '').trim()
    };
  }

  function commitRequest(entry, name, token, idempotencyKey) {
    return {
      ...reviewRequest(entry, name),
      group_review_token: String(token || ''),
      idempotency_key: String(idempotencyKey || '')
    };
  }

  function reviewKey(entry, name) {
    const request = reviewRequest(entry, name);
    return JSON.stringify([request.group_template_id, request.revision, request.name]);
  }

  function stateFor(context) {
    if (!context) return null;
    if (!context.groupTemplates) {
      context.groupTemplates = {
        status: 'idle',
        items: [],
        catalogUnavailable: false,
        selectedId: GENERAL_ID,
        names: {},
        review: null,
        pending: null,
        error: '',
        loadToken: 0
      };
    }
    return context.groupTemplates;
  }

  function selectedEntry(context) {
    const state = context?.groupTemplates;
    if (!state) return null;
    return state.items.find(item => item.id === state.selectedId) || null;
  }

  function selectedManaged(context) {
    const entry = selectedEntry(context);
    return isManaged(entry) ? entry : null;
  }

  // Only an ordinary Group creator offers templates. Guided setup owns its fixed
  // Home, and a selected-members Group cannot adopt workspaces into a program.
  function chooserMode(manager) {
    const context = manager?.workspaceCreatorContext;
    if (!context || manager.importModeEnabled || context.kind !== 'group') return 'hidden';
    if (context.mode === 'selected-members') return 'general-only';
    if (context.mode !== 'ordinary') return 'hidden';
    return 'full';
  }

  function managedActive(manager) {
    return (
      chooserMode(manager) === 'full' && Boolean(selectedManaged(manager?.workspaceCreatorContext))
    );
  }

  function currentName() {
    return String(document.getElementById('folderNameInput')?.value || '').trim();
  }

  async function load(manager, { force = false } = {}) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    if (!state || (!force && state.status !== 'idle')) return;
    const token = ++state.loadToken;
    state.status = 'loading';
    render(manager);
    try {
      const response = await fetch(LIST_URL, { headers: { Accept: 'application/json' } });
      const body = await response.json().catch(() => ({}));
      if (manager.workspaceCreatorContext !== context || state.loadToken !== token) return;
      if (!response.ok) throw new Error(body?.error || 'Group templates could not be loaded.');
      state.items = Array.isArray(body.group_templates) ? body.group_templates : [];
      state.catalogUnavailable = Boolean(
        body.catalog_unavailable || body.dependency_state_unavailable
      );
      if (!state.items.some(item => item.id === GENERAL_ID)) {
        state.items.unshift({ id: GENERAL_ID, kind: 'ordinary_group', name: 'General' });
      }
      const selected = state.items.find(item => item.id === state.selectedId);
      if (!isSelectable(selected)) state.selectedId = GENERAL_ID;
      state.status = 'ready';
    } catch (error) {
      if (manager.workspaceCreatorContext !== context || state.loadToken !== token) return;
      state.status = 'error';
      state.items = [{ id: GENERAL_ID, kind: 'ordinary_group', name: 'General' }];
      state.selectedId = GENERAL_ID;
      state.error = error?.message || 'Group templates could not be loaded.';
    }
    manager.refreshWizardChrome?.();
  }

  function select(manager, id) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    const next = state?.items.find(item => item.id === id);
    if (!state || !isSelectable(next) || state.pending) return false;
    if (chooserMode(manager) === 'general-only' && isManaged(next)) return false;
    const previous = selectedEntry(context);
    if (previous?.id === next.id) return false;
    const input = document.getElementById('folderNameInput');
    // Each template keeps its own typed name so switching never carries one
    // template's name into another's consent.
    state.names[previous?.id || GENERAL_ID] = String(input?.value || '');
    state.selectedId = next.id;
    state.review = null;
    state.error = '';
    if (input) {
      if (isReusable(next)) input.value = String(next.home?.name || '');
      else if (state.names[next.id] !== undefined) input.value = state.names[next.id];
      else if (isManaged(next)) input.value = String(next.proposed_group_name || '');
      else input.value = '';
    }
    manager.clearWorkspaceNameError?.();
    if (isManaged(next)) manager.resetTemplateAgentReview?.();
    manager.wizardStep = 2;
    manager.refreshWizardChrome?.();
    return true;
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function render(manager) {
    const container = document.getElementById('workspaceGroupTemplateChoice');
    if (!container) return;
    const mode = chooserMode(manager);
    container.hidden = mode === 'hidden';
    if (mode === 'hidden') return;
    const context = manager.workspaceCreatorContext;
    const state = stateFor(context);
    if (state.status === 'idle') void load(manager);

    const list = document.getElementById('workspaceGroupTemplateOptions');
    const status = document.getElementById('workspaceGroupTemplateStatus');
    if (!list) return;
    // Re-rendering replaces the radios; keep keyboard focus on the same option.
    const active = document.activeElement;
    const focusedId = active?.name === 'workspace-group-template' ? String(active.value) : '';
    list.replaceChildren();
    let refocus = null;
    const items = state.items.length
      ? state.items
      : [{ id: GENERAL_ID, kind: 'ordinary_group', name: 'General' }];
    for (const entry of items) {
      const managed = isManaged(entry);
      const disabled =
        !isSelectable(entry) || (mode === 'general-only' && managed) || Boolean(state.pending);
      const label = el('label', 'workspace-group-template-option');
      label.dataset.groupTemplateId = entry.id;
      label.classList.toggle('is-disabled', disabled);
      const radio = el('input');
      radio.type = 'radio';
      radio.name = 'workspace-group-template';
      radio.value = entry.id;
      radio.checked = entry.id === state.selectedId;
      radio.disabled = disabled;
      if (focusedId && entry.id === focusedId) refocus = radio;
      const body = el('span', 'workspace-group-template-body');
      const head = el('span', 'workspace-group-template-head');
      head.append(el('strong', '', String(entry.name || 'Group template')));
      if (managed) {
        head.append(
          el(
            'span',
            `workspace-group-template-badge is-${entry.availability?.state || 'unavailable'}`,
            availabilityLabel(entry)
          )
        );
      }
      body.append(head);
      if (managed) {
        const provider = providerLabel(entry);
        if (provider) body.append(el('small', 'workspace-group-template-provider', provider));
        body.append(el('small', '', String(entry.description || '')));
        const required = roleLabels(entry, true);
        const optional = roleLabels(entry, false);
        const creates = isReusable(entry)
          ? `Reuses the existing group “${entry.home?.name || ''}” unchanged.`
          : 'Creates one group only — no project, team, schedule, or tool access.';
        body.append(el('small', 'workspace-group-template-creates', creates));
        if (required.length) {
          body.append(el('small', '', `Set up after: ${required.join(', ')} (required)`));
        }
        if (optional.length) body.append(el('small', '', `Optional: ${optional.join(', ')}`));
        const projectRoles = Array.isArray(entry.project_roles_note)
          ? entry.project_roles_note
          : [];
        if (projectRoles.length) {
          body.append(el('small', '', `Stays project-local: ${projectRoles.join(', ')}`));
        }
        const note =
          mode === 'general-only'
            ? 'Group templates cannot adopt selected workspaces. Use General to group them.'
            : unavailableNote(entry);
        if (note) body.append(el('small', 'workspace-group-template-note', note));
      } else {
        body.append(
          el('small', '', 'A home for related workspaces with a reviewed Group Manager.')
        );
      }
      label.append(radio, body);
      list.append(label);
    }
    refocus?.focus?.();
    if (status) {
      const message =
        state.status === 'loading'
          ? 'Checking group templates…'
          : state.status === 'error'
            ? `${state.error} General is still available.`
            : state.catalogUnavailable
              ? 'Some group templates could not be checked. General is still available.'
              : '';
      status.textContent = message;
      status.hidden = !message;
    }
  }

  // Applied after sessions.js has synced the ordinary Group presentation.
  function syncPresentation(manager) {
    render(manager);
    const active = managedActive(manager);
    const entry = active ? selectedManaged(manager.workspaceCreatorContext) : null;
    const input = document.getElementById('folderNameInput');
    const description = document.getElementById('workspaceDetailsDescriptionCard');
    const parentCard = document.getElementById('workspaceCreatorDestinationCard');
    const notice = document.getElementById('workspaceGroupDetailsNotice');
    const step2Description = document.getElementById('wizardStep2Description');
    const step4Title = document.getElementById('wizardStep4Title');
    const step4Description = document.getElementById('wizardStep4Description');
    if (input) {
      input.readOnly = Boolean(entry && isReusable(entry));
      input.setAttribute('aria-readonly', String(input.readOnly));
    }
    if (!active) return;
    if (description) description.hidden = true;
    if (parentCard) parentCard.hidden = true;
    if (notice) {
      notice.hidden = false;
      notice.replaceChildren(
        el(
          'strong',
          '',
          isReusable(entry) ? 'This group already exists.' : 'Only the group is created.'
        ),
        el(
          'span',
          '',
          isReusable(entry)
            ? 'It will be reused unchanged: no rename, no new agents, and no project.'
            : 'Its coordinator is set up separately afterward. Projects, teams, and tool access are never created here.'
        )
      );
    }
    if (step2Description) {
      step2Description.textContent = isReusable(entry)
        ? 'Review the existing group this template uses.'
        : 'Name the group this template creates. You can rename it later.';
    }
    if (step4Title) {
      step4Title.textContent = isReusable(entry) ? 'Reuse this group' : 'Create this group only';
    }
    if (step4Description) {
      step4Description.textContent = 'Nothing is created or changed until you confirm below.';
    }
  }

  function identityProblem(manager) {
    const entry = selectedManaged(manager?.workspaceCreatorContext);
    if (!entry) return null;
    return nameProblem(currentName());
  }

  function escape(manager, value) {
    return manager?.escapeHtml ? manager.escapeHtml(String(value ?? '')) : String(value ?? '');
  }

  function renderReceipt(manager) {
    const context = manager.workspaceCreatorContext;
    const state = stateFor(context);
    const entry = selectedManaged(context);
    if (!entry) return '';
    const name = currentName();
    const review =
      state.review && state.review.key === reviewKey(entry, name) ? state.review : null;
    const required = roleLabels(entry, true);
    const optional = roleLabels(entry, false);
    const projectRoles = Array.isArray(entry.project_roles_note) ? entry.project_roles_note : [];
    let status = 'Preparing a review… nothing has been created.';
    if (review?.status === 'ready') status = review.data?.summary || '';
    if (review?.status === 'error') status = review.error;
    if (state.pending?.uncertain) {
      status =
        'The confirmed request did not return. Retry the same confirmed change; Ori will not create a second group.';
    }
    const reuse = review?.status === 'ready' ? Boolean(review.data?.reuse) : isReusable(entry);
    const groupName = reuse ? review?.data?.home_name || entry.home?.name || name : name;
    const provider = providerLabel(entry);
    return `
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">${reuse ? 'Existing group' : 'Group'}</span>
          <strong class="workspace-review-identity-name">${escape(manager, groupName || 'Untitled group')}</strong>
          <span class="workspace-review-card-meta">${escape(manager, `Template: ${entry.name}`)}${provider ? ` · ${escape(manager, provider)}` : ''}</span>
        </div>
        <div class="workspace-review-card-actions">
          <button type="button" class="workspace-wizard-inline-action" data-wizard-edit-step="2">Edit</button>
        </div>
      </div>
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">What will happen</span>
          <strong>${reuse ? 'The existing group is reused unchanged' : 'One group is created, initially unstaffed'}</strong>
          <span class="workspace-review-card-note ${review?.status === 'error' ? 'is-error' : ''}" data-group-template-review-status>${escape(manager, status)}</span>
        </div>
      </div>
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">Group roles</span>
          ${required.length ? `<span class="workspace-review-card-meta">${escape(manager, `Required, set up after: ${required.join(', ')}`)}</span>` : ''}
          ${optional.length ? `<span class="workspace-review-card-meta">${escape(manager, `Optional: ${optional.join(', ')}`)}</span>` : ''}
          ${projectRoles.length ? `<span class="workspace-review-card-note">${escape(manager, `Stays project-local: ${projectRoles.join(', ')}. Each project keeps its own team.`)}</span>` : ''}
        </div>
      </div>`;
  }

  async function ensureReview(manager) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    const entry = selectedManaged(context);
    if (!state || !entry || state.pending) return;
    const name = currentName();
    const key = reviewKey(entry, name);
    if (state.review?.key === key && state.review.status !== 'error') return;
    const review = { key, status: 'loading', data: null, error: '' };
    state.review = review;
    manager.refreshWorkspaceReview?.();
    try {
      const response = await fetch(REVIEW_URL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(reviewRequest(entry, name))
      });
      const body = await response.json().catch(() => ({}));
      if (manager.workspaceCreatorContext !== context || state.review !== review) return;
      if (!response.ok || !body.group_template_review?.review_token) {
        throw new Error(
          body?.group_template?.summary ||
            body?.group_requirement?.summary ||
            (typeof body?.error === 'string' ? body.error : '') ||
            'This group could not be reviewed.'
        );
      }
      review.status = 'ready';
      review.data = body.group_template_review;
    } catch (error) {
      if (manager.workspaceCreatorContext !== context || state.review !== review) return;
      review.status = 'error';
      review.error = `${error?.message || 'This group could not be reviewed.'} Nothing was created.`;
    }
    manager.refreshWizardChrome?.();
  }

  function canSubmit(manager) {
    const state = manager?.workspaceCreatorContext?.groupTemplates;
    const entry = selectedManaged(manager?.workspaceCreatorContext);
    if (!state || !entry) return true;
    if (state.pending) return true;
    return state.review?.status === 'ready' && state.review.key === reviewKey(entry, currentName());
  }

  function ctaLabel(manager) {
    const state = manager?.workspaceCreatorContext?.groupTemplates;
    const entry = selectedManaged(manager?.workspaceCreatorContext);
    if (!entry) return '';
    if (state?.pending?.uncertain) return 'Retry confirmed change';
    const reuse = state?.review?.status === 'ready' ? state.review.data?.reuse : isReusable(entry);
    const name = reuse ? state?.review?.data?.home_name || entry.home?.name : currentName();
    return reuse ? `Use existing group “${name}”` : `Create group “${name || 'Untitled'}” only`;
  }

  function showFollowUp(manager, result, folder, requestedName) {
    const name = String(result.home_name || 'Group');
    const nameNote =
      String(requestedName || '').trim() && String(requestedName).trim() !== name
        ? ' The name you entered was not applied.'
        : '';
    const message = result.created_by_this_operation
      ? `${name} is ready. No project or team was created. Set up its coordinator next.`
      : `${name} already existed and was reused unchanged.${nameNote}`;
    const slug = String(folder?.folder_slug || '').trim();
    const options = {
      title: result.created_by_this_operation ? 'Group created' : 'Existing group reused',
      duration: 9000
    };
    if (slug) {
      options.action = {
        label: 'Open group',
        onClick: () => {
          window.location.href = `/workspaces/${encodeURIComponent(slug)}`;
        }
      };
    }
    if (window.Toast?.success) window.Toast.success(message, options);
    else manager.showToast?.(message, 'success');
  }

  async function submit(manager) {
    const context = manager?.workspaceCreatorContext;
    const state = stateFor(context);
    const entry = selectedManaged(context);
    if (!state || !entry || manager.isCreatingFolder) return false;
    const name = currentName();
    const problem = nameProblem(name);
    if (problem) {
      manager.goToWizardStep?.(2);
      manager.setWorkspaceNameError?.(problem);
      return false;
    }
    if (!state.pending) {
      if (!canSubmit(manager)) {
        await ensureReview(manager);
        return false;
      }
      state.pending = {
        request: commitRequest(entry, name, state.review.data.review_token, crypto.randomUUID()),
        uncertain: false
      };
    }
    const pending = state.pending;
    const createBtn = document.getElementById('createFolderBtn');
    manager.isCreatingFolder = true;
    context.submitting = true;
    if (createBtn) {
      createBtn.disabled = true;
      createBtn.textContent = 'Confirming…';
    }
    try {
      let response;
      let body;
      try {
        response = await fetch(COMMIT_URL, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
          body: JSON.stringify(pending.request)
        });
        body = await response.json().catch(() => ({}));
      } catch (networkError) {
        pending.uncertain = true;
        throw networkError;
      }
      if (manager.workspaceCreatorContext !== context) return false;
      if (response.status === 400 || response.status === 409) {
        // A definite refusal consumed nothing: return to a fresh review.
        state.pending = null;
        state.review = {
          key: reviewKey(entry, name),
          status: 'error',
          data: null,
          error: `${body?.group_template?.summary || body?.group_requirement?.summary || body?.error || 'This review changed.'} Review it again.`
        };
        state.status = 'idle';
        void load(manager, { force: true });
        return false;
      }
      if (!response.ok || !body?.group_template?.home_workspace_id) {
        pending.uncertain = true;
        throw new Error(
          body?.group_template?.summary ||
            body?.error ||
            'The confirmed group could not be verified.'
        );
      }
      const result = body.group_template;
      // Only a Home this operation created receives the launcher's post-create
      // adoption (Map settle/focus without coordinate writes). A reused Home is
      // never re-placed, re-parented, or treated as newly created. Managed
      // templates are unavailable to selected-member launchers, so no member
      // move can ride this callback.
      if (
        result.created_by_this_operation &&
        typeof context.onCreated === 'function' &&
        !context.selection
      ) {
        try {
          await context.onCreated({
            folder: { id: result.home_workspace_id, name: result.home_name, kind: 'group' },
            groupId: result.home_workspace_id,
            placed: [],
            failed: [],
            uncertain: []
          });
        } catch (callbackError) {
          console.warn(
            'Group was created but its launcher follow-up could not complete:',
            callbackError
          );
        }
      }
      await manager.refreshWorkspaceSurfacesAfterOrdinaryGroupCreate?.();
      const folder = (manager.folders || []).find(
        item => String(item?.id) === String(result.home_workspace_id)
      );
      state.pending = null;
      const modalElement = document.getElementById('addFolderModal');
      if (modalElement) bootstrap.Modal.getInstance(modalElement)?.hide();
      manager.resetAddWorkspaceModalForm?.();
      showFollowUp(manager, result, folder, pending.request.name);
      return true;
    } catch (error) {
      if (manager.workspaceCreatorContext === context) {
        manager.showWorkspaceCreateError?.(
          `${error?.message || 'The confirmed request did not return.'} Retry the same confirmed change; Ori will not create a second group.`
        );
      }
      return false;
    } finally {
      manager.isCreatingFolder = false;
      if (context) context.submitting = false;
      if (manager.workspaceCreatorContext === context) manager.refreshWizardChrome?.();
    }
  }

  window.GroupTemplateCreator = {
    GENERAL_ID,
    isManaged,
    isReusable,
    isSelectable,
    nameProblem,
    providerLabel,
    availabilityLabel,
    reviewRequest,
    commitRequest,
    reviewKey,
    stateFor,
    selectedManaged,
    chooserMode,
    managedActive,
    load,
    select,
    render,
    syncPresentation,
    identityProblem,
    renderReceipt,
    ensureReview,
    canSubmit,
    ctaLabel,
    submit
  };
})();
