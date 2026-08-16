/**
 * Workspace Skills Manager
 *
 * Owns workspace-scoped skill binding management: loading the available
 * skill catalog, the binding modal (add/edit/delete), agent-access matrix,
 * and rendering the skill binding cards in the workspace detail page.
 *
 * It used to own planning-skill config too — mode, clarification depth, tasks
 * dir, branch requirements. Those read as policy while being config a model
 * might or might not honor, and they are compiled now and live in the
 * workspace's Planning Settings (FR-181, FR-182).
 *
 * Extracted from workspace-detail.js. Instantiated by WorkspaceDetailPage,
 * which provides workspace data, workspaceId, escapeHtml, normalizeAgentName,
 * loadWorkspace, renderWorkspaceConfigSummary, and DOM element refs through
 * the host.
 *
 * @module workspace-detail-skills
 */

export class WorkspaceSkillsManager {
  constructor(host) {
    this.host = host;
    this.availableSkills = [];
    this.availableSkillsPromise = null;
    this.activeWorkspaceSkillBindingId = '';
    this.activeWorkspaceSkillMode = 'create';
  }

  bindEvents() {
    const elements = this.host.elements;
    elements.skillsList?.addEventListener('click', event =>
      this.handleWorkspaceSkillListClick(event)
    );
    elements.skillsForm?.addEventListener('submit', event => {
      event.preventDefault();
      this.submitWorkspaceSkillModal();
    });
    elements.skillNameSelect?.addEventListener('change', () =>
      this.handleWorkspaceSkillSelectionChange()
    );
    elements.skillAgentOptions?.addEventListener('change', () =>
      this.updateWorkspaceSkillAgentAccessSummary()
    );
    elements.skillsModal?.addEventListener('hidden.bs.modal', () =>
      this.resetWorkspaceSkillModal()
    );
    elements.skillsModal?.addEventListener('shown.bs.modal', () => {
      this.handleWorkspaceSkillSelectionChange();
    });
  }

  getWorkspaceSkillBindings(options = {}) {
    if (!this.host.workspace || !Array.isArray(this.host.workspace.skill_bindings)) {
      return [];
    }

    const includeDisabled = options.includeDisabled === true;
    return this.host.workspace.skill_bindings
      .map(binding => {
        const skillName = String(binding?.skill_name || binding?.skillName || '').trim();
        const config =
          binding?.config && typeof binding.config === 'object' ? { ...binding.config } : {};
        return {
          id: String(binding?.id || '').trim(),
          skillName,
          enabled: binding?.enabled !== false,
          trusted: binding?.trusted === true,
          config
        };
      })
      .filter(binding => binding.id && binding.skillName)
      .filter(binding => includeDisabled || binding.enabled);
  }

  getWorkspaceSkillBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '')
      .trim()
      .toLowerCase();
    if (!normalizedBindingId) return null;
    return (
      this.getWorkspaceSkillBindings({ includeDisabled: true }).find(
        binding =>
          String(binding?.id || '')
            .trim()
            .toLowerCase() === normalizedBindingId
      ) || null
    );
  }

  getAvailableWorkspaceSkill(skillName) {
    const normalizedSkillName = String(skillName || '')
      .trim()
      .toLowerCase();
    if (!normalizedSkillName || !Array.isArray(this.availableSkills)) {
      return null;
    }
    return (
      this.availableSkills.find(
        skill =>
          String(skill?.name || '')
            .trim()
            .toLowerCase() === normalizedSkillName
      ) || null
    );
  }

  getWorkspaceSkillAgentAccessEntry(agentInstanceId) {
    const normalizedAgentInstanceId = String(agentInstanceId || '').trim();
    if (
      !normalizedAgentInstanceId ||
      !this.host.workspace ||
      !Array.isArray(this.host.workspace.agent_skill_access)
    ) {
      return null;
    }

    return (
      this.host.workspace.agent_skill_access.find(
        entry => String(entry?.agent_instance_id || '').trim() === normalizedAgentInstanceId
      ) || null
    );
  }

  getWorkspaceSkillAgentNamesForBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '')
      .trim()
      .toLowerCase();
    if (
      !normalizedBindingId ||
      !this.host.workspace ||
      !Array.isArray(this.host.workspace.agent_instances)
    ) {
      return [];
    }

    const accessEntries = Array.isArray(this.host.workspace.agent_skill_access)
      ? this.host.workspace.agent_skill_access
      : [];

    const names = [];
    const seen = new Set();
    this.host.workspace.agent_instances.forEach(instance => {
      const instanceId = String(instance?.id || '').trim();
      const agentName = String(instance?.name || '').trim();
      if (!instanceId || !agentName) return;

      const entry = accessEntries.find(
        item => String(item?.agent_instance_id || '').trim() === instanceId
      );
      let allowed = true;
      if (entry) {
        const enabledIDs = Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids
              .map(value =>
                String(value || '')
                  .trim()
                  .toLowerCase()
              )
              .filter(Boolean)
          : [];
        allowed = enabledIDs.includes(normalizedBindingId);
      }
      if (!allowed) return;

      const key = this.host.normalizeAgentName(agentName);
      if (!key || seen.has(key)) return;
      seen.add(key);
      names.push(agentName);
    });
    return names;
  }

  getWorkspaceSkillAgentAccessSelections(bindingId) {
    if (!this.host.workspace || !Array.isArray(this.host.workspace.agent_instances)) {
      return [];
    }

    const normalizedBindingId = String(bindingId || '')
      .trim()
      .toLowerCase();
    return this.host.workspace.agent_instances
      .map(instance => {
        const instanceId = String(instance?.id || '').trim();
        const instanceName = String(instance?.name || '').trim();
        if (!instanceId || !instanceName) return null;

        const entry = this.getWorkspaceSkillAgentAccessEntry(instanceId);
        const enabledBindingIds = Array.isArray(entry?.enabled_binding_ids)
          ? entry.enabled_binding_ids
              .map(value =>
                String(value || '')
                  .trim()
                  .toLowerCase()
              )
              .filter(Boolean)
          : [];

        const instanceNumber = Number(instance?.instance_number || 0);
        const nodeID = String(instance?.node_id || '').trim();
        const label = instanceNumber > 1 ? `${instanceName} #${instanceNumber}` : instanceName;
        const meta = nodeID || 'Workspace agent instance';
        const checked = entry ? enabledBindingIds.includes(normalizedBindingId) : true;

        return {
          id: instanceId,
          label,
          meta,
          checked
        };
      })
      .filter(Boolean);
  }

  /**
   * Skill bindings that are effective for an agent in this workspace — the
   * workspace skill bindings the agent's instances are allowed to use (after
   * agent_skill_access rules). Mirrors the MCP manager's
   * getEffectiveWorkspaceMCPBindingsForAgent so the agent info card stays
   * workspace-scoped rather than showing the agent's global skill catalog.
   */
  getEffectiveWorkspaceSkillBindingsForAgent(agentName) {
    const bindings = this.getWorkspaceSkillBindings();
    if (bindings.length === 0) {
      return [];
    }

    const instanceIds = this.host.getAgentInstanceIdsForName(agentName);
    if (instanceIds.length === 0) {
      return bindings;
    }

    const accessEntries = Array.isArray(this.host.workspace?.agent_skill_access)
      ? this.host.workspace.agent_skill_access
      : [];

    const allowedByInstance = instanceIds.map(instanceID => {
      const entry = accessEntries.find(
        item => String(item?.agent_instance_id || '').trim() === instanceID
      );
      if (!entry) {
        return bindings;
      }
      if (!Array.isArray(entry.enabled_binding_ids) || entry.enabled_binding_ids.length === 0) {
        return [];
      }

      const allowedIDs = new Set(
        entry.enabled_binding_ids
          .map(value =>
            String(value || '')
              .trim()
              .toLowerCase()
          )
          .filter(Boolean)
      );
      return bindings.filter(binding =>
        allowedIDs.has(
          String(binding.id || '')
            .trim()
            .toLowerCase()
        )
      );
    });

    const merged = [];
    const seen = new Set();
    allowedByInstance.flat().forEach(binding => {
      const key =
        String(binding?.id || '')
          .trim()
          .toLowerCase() ||
        String(binding?.skillName || '')
          .trim()
          .toLowerCase();
      if (!key || seen.has(key)) return;
      seen.add(key);
      merged.push(binding);
    });
    return merged;
  }

  getEffectiveWorkspaceSkillNamesForAgent(agentName) {
    const names = [];
    const seen = new Set();
    this.getEffectiveWorkspaceSkillBindingsForAgent(agentName).forEach(binding => {
      const name = String(binding?.skillName || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      names.push(name);
    });
    return names;
  }

  async loadAvailableSkills(force = false) {
    if (!force && Array.isArray(this.availableSkills) && this.availableSkills.length > 0) {
      return this.availableSkills;
    }
    if (!force && this.availableSkillsPromise) {
      return this.availableSkillsPromise;
    }

    this.availableSkillsPromise = (async () => {
      const response = await fetch('/api/skills?agent=default');
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to load skills');
      }

      const data = await response.json();
      const seen = new Set();
      const skillsList = (Array.isArray(data?.skills) ? data.skills : [])
        .map(skill => ({
          name: String(skill?.name || '').trim(),
          description: String(skill?.description || '').trim(),
          enabled: skill?.enabled !== false
        }))
        .filter(skill => skill.name)
        .filter(skill => {
          const key = skill.name.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .sort((left, right) => left.name.localeCompare(right.name));

      this.availableSkills = skillsList;
      return skillsList;
    })();

    try {
      return await this.availableSkillsPromise;
    } finally {
      this.availableSkillsPromise = null;
    }
  }

  getWorkspaceSkillModalInstance() {
    if (!this.host.elements.skillsModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return null;
    }

    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.host.elements.skillsModal)
      : bootstrap.Modal.getInstance(this.host.elements.skillsModal) ||
          new bootstrap.Modal(this.host.elements.skillsModal);
  }

  generateWorkspaceSkillBindingId() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') {
      return window.crypto.randomUUID();
    }
    return `skill-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  }

  setWorkspaceSkillNameHelp(message, isError = false) {
    if (!this.host.elements.skillNameHelp) return;
    this.host.elements.skillNameHelp.textContent = message;
    this.host.elements.skillNameHelp.classList.toggle('is-error', !!isError);
  }

  populateWorkspaceSkillOptions(selectedSkillName = '') {
    if (!this.host.elements.skillNameSelect) return;

    const normalizedSelected = String(selectedSkillName || '').trim();
    const available = Array.isArray(this.availableSkills) ? [...this.availableSkills] : [];
    const selectedExists = normalizedSelected
      ? available.some(
          skill =>
            String(skill?.name || '')
              .trim()
              .toLowerCase() === normalizedSelected.toLowerCase()
        )
      : false;

    if (normalizedSelected && !selectedExists) {
      available.unshift({ name: normalizedSelected, unavailable: true });
    }

    const options = ['<option value="">Select a skill</option>'];
    available.forEach(skill => {
      const name = String(skill?.name || '').trim();
      if (!name) return;

      const unavailable = skill?.unavailable === true;
      const selected =
        normalizedSelected && name.toLowerCase() === normalizedSelected.toLowerCase()
          ? ' selected'
          : '';
      const label = unavailable ? `${name} (not currently available)` : name;
      options.push(
        `<option value="${this.host.escapeHtml(name)}"${selected}>${this.host.escapeHtml(label)}</option>`
      );
    });

    this.host.elements.skillNameSelect.innerHTML = options.join('');

    if (normalizedSelected) {
      this.host.elements.skillNameSelect.value = normalizedSelected;
    }

    if (available.length === 0) {
      this.setWorkspaceSkillNameHelp(
        'No skills are available yet. Create or install skills first.',
        true
      );
      return;
    }

    if (normalizedSelected && !selectedExists) {
      this.setWorkspaceSkillNameHelp(
        `${normalizedSelected} is not currently available, but you can still update or remove this workspace binding.`,
        true
      );
      return;
    }

    this.setWorkspaceSkillNameHelp('Choose a skill to bind to this workspace.');
  }

  renderWorkspaceSkillAgentOptions(bindingId) {
    if (!this.host.elements.skillAgentOptions) return;

    const accessOptions = this.getWorkspaceSkillAgentAccessSelections(bindingId);
    if (accessOptions.length === 0) {
      this.host.elements.skillAgentOptions.innerHTML = `
        <div class="workspace-detail-mcp-agent-empty">
          Add one or more agents to this workspace before assigning skill access.
        </div>
      `;
      this.updateWorkspaceSkillAgentAccessSummary();
      return;
    }

    this.host.elements.skillAgentOptions.innerHTML = accessOptions
      .map(
        option => `
      <label class="workspace-detail-mcp-agent-option">
        <input type="checkbox" class="form-check-input workspace-detail-skill-agent-checkbox" value="${this.host.escapeHtml(option.id)}"${option.checked ? ' checked' : ''}>
        <span class="workspace-detail-mcp-agent-option-copy">
          <span class="workspace-detail-mcp-agent-option-title">${this.host.escapeHtml(option.label)}</span>
          <span class="workspace-detail-mcp-agent-option-meta">${this.host.escapeHtml(option.meta)}</span>
        </span>
      </label>
    `
      )
      .join('');
    this.updateWorkspaceSkillAgentAccessSummary();
  }

  updateWorkspaceSkillAgentAccessSummary() {
    if (!this.host.elements.skillAgentAccessSummary || !this.host.elements.skillAgentOptions)
      return;

    const checkboxes = Array.from(
      this.host.elements.skillAgentOptions.querySelectorAll(
        '.workspace-detail-skill-agent-checkbox'
      )
    );
    if (checkboxes.length === 0) {
      this.host.elements.skillAgentAccessSummary.textContent = 'No agents';
      return;
    }

    const selectedCount = checkboxes.filter(checkbox => checkbox.checked).length;
    this.host.elements.skillAgentAccessSummary.textContent = `${selectedCount} of ${checkboxes.length} selected`;
  }

  // Selecting a skill no longer reveals planning-policy fields.
  //
  // The binding modal used to grow a "planning settings" section for any skill
  // flagged as a planning profile — mode, tasks dir, require branch. Those read
  // as policy while being config a model might read, and they are compiled now
  // and live in Workspace Settings (FR-182).
  handleWorkspaceSkillSelectionChange() {
    if (this.host.elements.skillPlanningFields) {
      this.host.elements.skillPlanningFields.classList.add('d-none');
    }
  }

  resetWorkspaceSkillModal() {
    this.activeWorkspaceSkillBindingId = '';
    this.activeWorkspaceSkillMode = 'create';

    if (this.host.elements.skillsForm) {
      this.host.elements.skillsForm.reset();
    }
    if (this.host.elements.skillNameSelect) {
      this.host.elements.skillNameSelect.innerHTML = '<option value="">Select a skill</option>';
    }
    if (this.host.elements.skillAgentOptions) {
      this.host.elements.skillAgentOptions.innerHTML =
        '<div class="workspace-detail-mcp-agent-empty">No agent instances in this workspace yet.</div>';
    }
    if (this.host.elements.skillsModalTitle) {
      this.host.elements.skillsModalTitle.textContent = 'Add Workspace Skill';
    }
    if (this.host.elements.skillsModalSubtitle) {
      this.host.elements.skillsModalSubtitle.textContent =
        'Bind a skill to this workspace, then decide which agent instances can use it here.';
    }
    if (this.host.elements.skillEnabledInput) {
      this.host.elements.skillEnabledInput.checked = true;
    }
    if (this.host.elements.skillTrustedInput) {
      this.host.elements.skillTrustedInput.checked = false;
    }
    if (this.host.elements.skillPlanningFields) {
      this.host.elements.skillPlanningFields.classList.add('d-none');
    }
    if (this.host.elements.skillSubmitBtn) {
      this.host.elements.skillSubmitBtn.disabled = false;
      this.host.elements.skillSubmitBtn.textContent = 'Add Binding';
    }
    this.setWorkspaceSkillNameHelp('Choose a skill to bind to this workspace.');
    this.updateWorkspaceSkillAgentAccessSummary();
  }

  handleWorkspaceSkillListClick(event) {
    const button = event.target.closest('[data-workspace-skill-action]');
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();

    const action = String(button.dataset.workspaceSkillAction || '').trim();
    const bindingId = String(button.dataset.bindingId || '').trim();
    if (!bindingId) return;

    if (action === 'edit') {
      this.openWorkspaceSkillModal(bindingId);
      return;
    }

    if (action === 'delete') {
      this.deleteWorkspaceSkillBinding(bindingId);
    }
  }

  async openWorkspaceSkillModal(bindingId = '') {
    const existingBinding = bindingId ? this.getWorkspaceSkillBinding(bindingId) : null;
    if (bindingId && !existingBinding) {
      if (window.Toast) {
        window.Toast.info('That workspace skill binding is no longer available.');
      }
      return;
    }

    try {
      await this.loadAvailableSkills();
    } catch (error) {
      console.error('Failed to load skills:', error);
      if (!existingBinding) {
        if (window.Toast) window.Toast.error(error.message || 'Failed to load skills');
        return;
      }
    }

    this.activeWorkspaceSkillMode = existingBinding ? 'edit' : 'create';
    this.activeWorkspaceSkillBindingId =
      existingBinding?.id || this.generateWorkspaceSkillBindingId();
    this.populateWorkspaceSkillOptions(existingBinding?.skillName || '');

    if (this.host.elements.skillsModalTitle) {
      this.host.elements.skillsModalTitle.textContent = existingBinding
        ? 'Edit Workspace Skill'
        : 'Add Workspace Skill';
    }
    if (this.host.elements.skillsModalSubtitle) {
      this.host.elements.skillsModalSubtitle.textContent = existingBinding
        ? 'Update this workspace skill binding or change which agent instances can use it.'
        : 'Bind a skill to this workspace and decide which agent instances should be able to use it.';
    }
    if (this.host.elements.skillEnabledInput) {
      this.host.elements.skillEnabledInput.checked = existingBinding
        ? existingBinding.enabled !== false
        : true;
    }
    if (this.host.elements.skillTrustedInput) {
      this.host.elements.skillTrustedInput.checked = existingBinding
        ? existingBinding.trusted === true
        : false;
    }
    if (this.host.elements.skillSubmitBtn) {
      this.host.elements.skillSubmitBtn.textContent = existingBinding
        ? 'Save Changes'
        : 'Add Binding';
      this.host.elements.skillSubmitBtn.disabled = false;
    }

    this.handleWorkspaceSkillSelectionChange();
    this.renderWorkspaceSkillAgentOptions(this.activeWorkspaceSkillBindingId);
    this.getWorkspaceSkillModalInstance()?.show();
  }

  getWorkspaceSkillSelectedAgentInstanceIDs() {
    if (!this.host.elements.skillAgentOptions) return [];
    return Array.from(
      this.host.elements.skillAgentOptions.querySelectorAll(
        '.workspace-detail-skill-agent-checkbox:checked'
      )
    )
      .map(checkbox => String(checkbox.value || '').trim())
      .filter(Boolean);
  }

  setWorkspaceSkillModalSubmitting(isSubmitting) {
    if (!this.host.elements.skillSubmitBtn) return;
    this.host.elements.skillSubmitBtn.disabled = isSubmitting;
    this.host.elements.skillSubmitBtn.textContent = isSubmitting
      ? this.activeWorkspaceSkillMode === 'edit'
        ? 'Saving...'
        : 'Adding...'
      : this.activeWorkspaceSkillMode === 'edit'
        ? 'Save Changes'
        : 'Add Binding';
  }

  async saveWorkspaceSkillBinding(payload) {
    const isEditing = this.activeWorkspaceSkillMode === 'edit';
    const endpoint = isEditing
      ? `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/skill-bindings/${encodeURIComponent(this.activeWorkspaceSkillBindingId)}`
      : `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/skill-bindings`;

    const response = await fetch(endpoint, {
      method: isEditing ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to save workspace skill binding');
    }

    return response.json();
  }

  async persistWorkspaceSkillAgentAccess(bindingId, selectedAgentInstanceIds) {
    if (
      !this.host.workspace ||
      !Array.isArray(this.host.workspace.agent_instances) ||
      this.host.workspace.agent_instances.length === 0
    ) {
      return;
    }

    const selectedSet = new Set(
      selectedAgentInstanceIds.map(value => String(value || '').trim()).filter(Boolean)
    );
    const effectiveBindingIds = this.getWorkspaceSkillBindings()
      .map(binding => String(binding?.id || '').trim())
      .filter(Boolean);
    const defaultBindingIds = Array.from(new Set(effectiveBindingIds)).sort();
    const normalizeIDs = ids =>
      Array.from(new Set(ids.map(value => String(value || '').trim()).filter(Boolean))).sort();
    const arraysEqual = (left, right) =>
      left.length === right.length && left.every((value, index) => value === right[index]);

    const requests = this.host.workspace.agent_instances.map(async instance => {
      const instanceId = String(instance?.id || '').trim();
      if (!instanceId) return;

      const entry = this.getWorkspaceSkillAgentAccessEntry(instanceId);
      const currentIds = entry
        ? Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids.map(value => String(value || '').trim()).filter(Boolean)
          : []
        : [...defaultBindingIds];
      const allowedSet = new Set(currentIds);

      if (selectedSet.has(instanceId)) {
        allowedSet.add(bindingId);
      } else {
        allowedSet.delete(bindingId);
      }

      const enabledBindingIDs = normalizeIDs(Array.from(allowedSet));
      const currentNormalized = normalizeIDs(currentIds);

      if (!entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        return;
      }

      if (entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/agent-skill-access/${encodeURIComponent(instanceId)}`,
          { method: 'DELETE' }
        );

        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || `Failed to clear skill access rule for ${instanceId}`);
        }
        return;
      }

      if (arraysEqual(enabledBindingIDs, currentNormalized)) {
        return;
      }

      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/agent-skill-access/${encodeURIComponent(instanceId)}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled_binding_ids: enabledBindingIDs })
        }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Failed to update skill access for ${instanceId}`);
      }
    });

    await Promise.all(requests);
  }

  async submitWorkspaceSkillModal() {
    const skillName = String(this.host.elements.skillNameSelect?.value || '').trim();

    if (!skillName) {
      this.setWorkspaceSkillNameHelp('Choose a skill before saving this workspace binding.', true);
      if (window.Toast) window.Toast.error('Choose a skill');
      return;
    }

    this.setWorkspaceSkillNameHelp('Choose a skill to bind to this workspace.');
    this.setWorkspaceSkillModalSubmitting(true);

    try {
      const enabled = this.host.elements.skillEnabledInput?.checked !== false;
      const trusted = this.host.elements.skillTrustedInput?.checked === true;
      const selectedAgentInstanceIds = this.getWorkspaceSkillSelectedAgentInstanceIDs();
      const payload = {
        skill_name: skillName,
        enabled,
        trusted,
        // Bindings carry no config from this form any more. Planning policy is
        // compiled and lives in Workspace Settings; a skill binding is a
        // binding (FR-182).
        config: {}
      };

      if (this.activeWorkspaceSkillMode !== 'edit') {
        payload.id = this.activeWorkspaceSkillBindingId;
      }

      await this.saveWorkspaceSkillBinding(payload);
      await this.host.loadWorkspace();
      await this.persistWorkspaceSkillAgentAccess(
        this.activeWorkspaceSkillBindingId,
        selectedAgentInstanceIds
      );
      await this.host.loadWorkspace();

      this.getWorkspaceSkillModalInstance()?.hide();
      if (window.Toast) {
        window.Toast.success(
          this.activeWorkspaceSkillMode === 'edit'
            ? 'Workspace skill updated'
            : 'Workspace skill added'
        );
      }
    } catch (error) {
      console.error('Failed to save workspace skill binding:', error);
      if (window.Toast)
        window.Toast.error(error.message || 'Failed to save workspace skill binding');
    } finally {
      this.setWorkspaceSkillModalSubmitting(false);
    }
  }

  async deleteWorkspaceSkillBinding(bindingId) {
    const binding = this.getWorkspaceSkillBinding(bindingId);
    if (!binding) {
      if (window.Toast) {
        window.Toast.info('That skill binding was not found.');
      }
      return;
    }

    const label = binding.skillName || binding.id;
    if (!window.confirm(`Remove workspace skill binding "${label}"?`)) {
      return;
    }

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/skill-bindings/${encodeURIComponent(bindingId)}`,
        { method: 'DELETE' }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to remove workspace skill binding');
      }

      await this.host.loadWorkspace();
      if (window.Toast) window.Toast.success('Workspace skill removed');
    } catch (error) {
      console.error('Failed to delete workspace skill binding:', error);
      if (window.Toast)
        window.Toast.error(error.message || 'Failed to remove workspace skill binding');
    }
  }

  renderWorkspaceSkillBindings() {
    if (!this.host.elements.skillsList) return;

    const bindings = this.getWorkspaceSkillBindings({ includeDisabled: true });
    if (bindings.length === 0) {
      this.host.elements.skillsList.innerHTML = `
        <div class="workspace-detail-empty">
          No workspace skill bindings yet.
          <div class="workspace-detail-mcp-empty-note">Add a skill with the <strong>+</strong> button to make it available to agents in this workspace.</div>
        </div>
      `;
      this.host.renderWorkspaceConfigSummary();
      return;
    }

    this.host.elements.skillsList.innerHTML = bindings
      .map(binding => {
        const skillName = String(binding?.skillName || '').trim() || 'unknown';
        const isDisabled = binding?.enabled === false;
        const isTrusted = binding?.trusted === true;
        const agentNames = this.getWorkspaceSkillAgentNamesForBinding(binding.id);
        const accessSummary = isDisabled
          ? 'Disabled for this workspace'
          : agentNames.length > 0
            ? `${agentNames.length} agent${agentNames.length === 1 ? '' : 's'} can use this`
            : Array.isArray(this.host.workspace?.agent_instances) &&
                this.host.workspace.agent_instances.length > 0
              ? 'No agent instances currently have access'
              : 'No agent instances in this workspace';
        const accessLabel = isDisabled
          ? 'Agents: unavailable while disabled'
          : agentNames.length > 0
            ? `Agents: ${agentNames.join(', ')}`
            : 'Agents: none';

        const chips = [
          `<span class="workspace-detail-mcp-chip status${isDisabled ? ' is-disabled' : ''}">${isDisabled ? 'Disabled' : 'Enabled'}</span>`,
          isTrusted ? `<span class="workspace-detail-mcp-chip source">Trusted</span>` : '',
          `<span class="workspace-detail-mcp-chip access">${this.host.escapeHtml(accessLabel)}</span>`
        ]
          .filter(Boolean)
          .join('');

        return `
        <div class="workspace-detail-mcp-card" data-skill-binding-id="${this.host.escapeHtml(binding.id)}">
          <div class="workspace-detail-mcp-card-top">
            <div class="workspace-detail-mcp-card-top-main">
              <div class="workspace-detail-mcp-server">
                <span>${this.host.escapeHtml(skillName)}</span>
                <code>${this.host.escapeHtml(binding.id)}</code>
              </div>
              <div class="workspace-detail-mcp-meta">${this.host.escapeHtml(accessSummary)}</div>
            </div>
            <div class="workspace-detail-mcp-card-actions">
              <button type="button" class="workspace-detail-mcp-card-btn" data-workspace-skill-action="edit" data-binding-id="${this.host.escapeHtml(binding.id)}" title="Edit binding" aria-label="Edit skill binding ${this.host.escapeHtml(skillName)}">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
                </svg>
              </button>
              <button type="button" class="workspace-detail-mcp-card-btn is-danger" data-workspace-skill-action="delete" data-binding-id="${this.host.escapeHtml(binding.id)}" title="Remove binding" aria-label="Remove skill binding ${this.host.escapeHtml(skillName)}">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
                </svg>
              </button>
            </div>
          </div>
          <div class="workspace-detail-mcp-chip-row">${chips}</div>
        </div>
      `;
      })
      .join('');
    this.host.renderWorkspaceConfigSummary();
  }
}
