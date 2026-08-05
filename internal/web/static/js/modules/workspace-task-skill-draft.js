// Skill-draft modal methods for WorkspaceTaskPage.
//
// This module hosts the UI flow that turns a completed task into a reusable
// agent skill: gating, default-name/description generation, the AI prompt
// generation request, and the final skill submission. It exists purely to
// keep workspace-task.js navigable. Methods are mixed onto
// WorkspaceTaskPage.prototype via Object.assign at the bottom of
// workspace-task.js — the methods continue to use `this` to access the page
// instance's state and to call sibling methods (notify, copyToClipboard,
// getTaskDisplayLabel, getCurrentResultText, etc.) which remain on the
// class.
//
// Module-level helpers + skill-context length constants are imported from
// workspace-task.js. ES modules' live bindings handle the cyclic import
// because every reference happens inside method bodies at call time.

import {
  buildTaskSkillNameSlug,
  extractGeneratedSkillPrompt,
  formatDateTime,
  normalizeResultText,
  stripSkillUnsafeMarkup,
  summarizeText,
  trimTaskSkillText,
  TASK_SKILL_DETAILS_CONTEXT_MAX_CHARS,
  TASK_SKILL_GENERATION_CONTEXT_MAX_CHARS,
  TASK_SKILL_RESULT_CONTEXT_MAX_CHARS
} from './workspace-task.js';

export const taskSkillDraftMethods = {
  canCreateSkillFromTask(task = this.task) {
    if (!task || typeof task !== 'object') return false;
    if (
      String(task.status || '')
        .trim()
        .toLowerCase() !== 'completed'
    )
      return false;
    if (!String(task.to || '').trim()) return false;
    if (String(task.error || '').trim()) return false;
    return Boolean(normalizeResultText(task.result).trim());
  },

  async copyCurrentResult() {
    const outputText = this.getCurrentOutputText();
    if (!outputText) {
      this.notify('warning', 'No result is available to copy.');
      return;
    }

    await this.copyToClipboard(outputText, 'Result copied');
  },

  setSkillDraftError(message) {
    if (!this.elements.skillError) return;

    const text = String(message || '').trim();
    this.elements.skillError.hidden = !text;
    this.elements.skillError.textContent = text;
  },

  updateSkillDraftButtons() {
    const busy = this.skillDraftGenerating || this.skillDraftSubmitting;
    if (this.elements.skillGenerateBtn) {
      const label = this.elements.skillGenerateBtn.querySelector('span');
      if (label)
        label.textContent = this.skillDraftGenerating ? 'Generating...' : 'Regenerate With AI';
      this.elements.skillGenerateBtn.disabled = busy;
    }
    if (this.elements.skillSubmitBtn) {
      const label = this.elements.skillSubmitBtn.querySelector('span');
      if (label) label.textContent = this.skillDraftSubmitting ? 'Creating...' : 'Create Skill';
      this.elements.skillSubmitBtn.disabled = busy;
    }

    const resultText = this.getCurrentResultText();
    this.updateResultActionButtons(resultText, Boolean(resultText));
  },

  getTaskSkillAgentName() {
    return String(this.task?.to || '').trim();
  },

  buildTaskSkillDefaultName() {
    return buildTaskSkillNameSlug(this.getTaskDisplayLabel());
  },

  buildTaskSkillSavedDescription() {
    const title = stripSkillUnsafeMarkup(this.getTaskDisplayLabel()) || 'completed task';
    const details = stripSkillUnsafeMarkup(this.task?.details || '');
    const result = stripSkillUnsafeMarkup(this.getCurrentResultText());
    const parts = [
      `Repeatable workflow derived from a completed Ori task: ${trimTaskSkillText(title, 140)}.`
    ];

    if (details) {
      parts.push(`Use when: ${trimTaskSkillText(details, 360)}.`);
    } else if (result) {
      parts.push(`Produces results like: ${trimTaskSkillText(result, 360)}.`);
    }

    return trimTaskSkillText(parts.join(' ').replace(/\s+/g, ' ').trim(), 900);
  },

  buildTaskSkillGenerationDescription(name, savedDescription) {
    const taskTitle = this.getTaskDisplayLabel();
    const taskDetails = String(this.task?.details || '').trim();
    const resultText = this.getCurrentResultText();
    const assistMessage = String(this.task?.context?.user_assist_message || '').trim();
    const completedAt = formatDateTime(this.task?.completed_at);
    const agentName = this.getTaskSkillAgentName();
    const workspaceName = String(this.workspace?.name || '').trim();

    const lines = [
      'Create a reusable Ori Agent skill from this successful task.',
      'The skill should let the assigned agent repeat the workflow with less user instruction next time.',
      'Prefer public pages, browser-readable sources, local context, and fallback source strategies before asking users for API keys.',
      'Generalize the workflow; do not include task IDs, run-specific timestamps, localhost URLs, or one-off troubleshooting notes unless they are broadly useful.',
      `Proposed skill name: ${name}`,
      `Saved skill description: ${savedDescription}`
    ];

    if (workspaceName) lines.push(`Workspace: ${workspaceName}`);
    if (agentName) lines.push(`Assigned agent: ${agentName}`);
    if (completedAt !== '—') lines.push(`Completed: ${completedAt}`);
    if (taskTitle) lines.push(`Original task title: ${taskTitle}`);
    if (taskDetails) {
      lines.push(
        '',
        'Original task details:',
        trimTaskSkillText(taskDetails, TASK_SKILL_DETAILS_CONTEXT_MAX_CHARS)
      );
    }
    if (assistMessage) {
      lines.push(
        '',
        'User clarification collected during the task:',
        trimTaskSkillText(assistMessage, 900)
      );
    }
    if (resultText) {
      lines.push(
        '',
        'Successful result excerpt:',
        trimTaskSkillText(resultText, TASK_SKILL_RESULT_CONTEXT_MAX_CHARS)
      );
    }

    lines.push(
      '',
      'Write the skill prompt as operational guidance for future runs. Include source selection, verification expectations, fallback behavior, and the expected final response format when inferable from the result.'
    );

    return trimTaskSkillText(lines.join('\n'), TASK_SKILL_GENERATION_CONTEXT_MAX_CHARS);
  },

  populateSkillDraftModal() {
    const agentName = this.getTaskSkillAgentName();
    const skillName = this.buildTaskSkillDefaultName();
    const description = this.buildTaskSkillSavedDescription();
    const taskLabel = summarizeText(this.getTaskDisplayLabel(), 90);

    this.setSkillDraftError('');
    if (this.elements.skillMeta) {
      this.elements.skillMeta.textContent = `Drafts a reusable skill for "${agentName}" from "${taskLabel}".`;
    }
    if (this.elements.skillAgentInput) {
      this.elements.skillAgentInput.value = agentName;
    }
    if (this.elements.skillNameInput) {
      this.elements.skillNameInput.value = skillName;
    }
    if (this.elements.skillDescriptionInput) {
      this.elements.skillDescriptionInput.value = description;
    }
    if (this.elements.skillPromptInput) {
      this.elements.skillPromptInput.value = '';
    }
    this.updateSkillDraftButtons();
  },

  openSkillDraftModal() {
    if (!this.canCreateSkillFromTask()) {
      this.notify(
        'warning',
        'Only completed tasks with a successful result and assigned agent can become skills.'
      );
      return;
    }
    if (!this.elements.skillModal || typeof bootstrap === 'undefined') {
      this.notify('error', 'Skill editor is not available.');
      return;
    }

    this.populateSkillDraftModal();
    const modal =
      typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.skillModal)
        : bootstrap.Modal.getInstance(this.elements.skillModal) ||
          new bootstrap.Modal(this.elements.skillModal);
    modal.show();
    setTimeout(() => {
      this.elements.skillNameInput?.focus();
      this.elements.skillNameInput?.select();
    }, 120);
    this.generateSkillPromptFromTask(false);
  },

  async generateSkillPromptFromTask(force = false) {
    if (this.skillDraftGenerating || this.skillDraftSubmitting) return;

    const agentName = String(
      this.elements.skillAgentInput?.value || this.getTaskSkillAgentName()
    ).trim();
    const rawName = String(
      this.elements.skillNameInput?.value || this.buildTaskSkillDefaultName()
    ).trim();
    const skillName = buildTaskSkillNameSlug(rawName);
    const savedDescription = trimTaskSkillText(
      stripSkillUnsafeMarkup(
        this.elements.skillDescriptionInput?.value || this.buildTaskSkillSavedDescription()
      ),
      1024
    );

    if (!agentName || !skillName || !savedDescription) {
      this.setSkillDraftError('Skill name, description, and agent are required.');
      return;
    }

    if (this.elements.skillNameInput) this.elements.skillNameInput.value = skillName;
    if (this.elements.skillDescriptionInput)
      this.elements.skillDescriptionInput.value = savedDescription;

    const currentPrompt = String(this.elements.skillPromptInput?.value || '').trim();
    if (force && currentPrompt && currentPrompt !== 'Generating prompt...') {
      const confirmed = window.confirm('Replace the current prompt with a newly generated one?');
      if (!confirmed) return;
    }

    if (this.skillDraftAbortController) {
      this.skillDraftAbortController.abort();
    }

    const controller = new AbortController();
    this.skillDraftAbortController = controller;
    const requestId = ++this.skillDraftRequestId;
    this.skillDraftGenerating = true;
    this.setSkillDraftError('');
    if (this.elements.skillPromptInput) {
      this.elements.skillPromptInput.value = 'Generating prompt...';
    }
    this.updateSkillDraftButtons();

    try {
      const response = await fetch('/api/skills/generate-prompt', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          agent: agentName,
          name: skillName,
          description: this.buildTaskSkillGenerationDescription(skillName, savedDescription)
        }),
        signal: controller.signal
      });
      const data = await response.json().catch(() => ({}));
      if (requestId !== this.skillDraftRequestId) return;

      const details = typeof data?.details === 'string' ? data.details.trim() : '';
      const baseError =
        typeof data?.error === 'string' ? data.error : 'Failed to generate skill prompt.';
      if (!response.ok) throw new Error(`${baseError}${details ? ` ${details}` : ''}`);

      const generated = extractGeneratedSkillPrompt(data?.prompt || '');
      if (!generated) throw new Error('Assistant returned an empty skill prompt.');

      if (this.elements.skillPromptInput) {
        this.elements.skillPromptInput.value = generated;
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      console.error('Failed to generate task skill prompt:', error);
      if (requestId !== this.skillDraftRequestId) return;

      if (this.elements.skillPromptInput) {
        this.elements.skillPromptInput.value =
          currentPrompt === 'Generating prompt...' ? '' : currentPrompt;
      }
      this.setSkillDraftError(error?.message || 'Failed to generate skill prompt.');
    } finally {
      if (requestId === this.skillDraftRequestId) {
        this.skillDraftGenerating = false;
        this.skillDraftAbortController = null;
        this.updateSkillDraftButtons();
      }
    }
  },

  async submitTaskSkillDraft() {
    if (this.skillDraftSubmitting) return;

    const agentName = String(
      this.elements.skillAgentInput?.value || this.getTaskSkillAgentName()
    ).trim();
    const rawSkillName = String(this.elements.skillNameInput?.value || '').trim();
    const skillName = rawSkillName ? buildTaskSkillNameSlug(rawSkillName) : '';
    const description = trimTaskSkillText(
      stripSkillUnsafeMarkup(this.elements.skillDescriptionInput?.value || ''),
      1024
    );
    const prompt = String(this.elements.skillPromptInput?.value || '')
      .replace(/\r\n/g, '\n')
      .trim();

    if (this.elements.skillNameInput) this.elements.skillNameInput.value = skillName;
    if (this.elements.skillDescriptionInput)
      this.elements.skillDescriptionInput.value = description;

    if (!agentName) {
      this.setSkillDraftError('Agent is required.');
      return;
    }
    if (!skillName) {
      this.setSkillDraftError('Skill name is required.');
      return;
    }
    if (!description) {
      this.setSkillDraftError('Description is required.');
      return;
    }
    if (!prompt || prompt === 'Generating prompt...') {
      this.setSkillDraftError('Wait for the AI draft to finish or enter a skill prompt manually.');
      return;
    }

    this.skillDraftSubmitting = true;
    this.setSkillDraftError('');
    this.updateSkillDraftButtons();

    try {
      const response = await fetch('/api/skills', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          agent: agentName,
          name: skillName,
          description,
          prompt
        })
      });
      const data = await response.json().catch(() => ({}));
      const details = typeof data?.details === 'string' ? data.details.trim() : '';
      const baseError = typeof data?.error === 'string' ? data.error : 'Failed to create skill.';
      if (!response.ok) throw new Error(`${baseError}${details ? ` ${details}` : ''}`);

      if (this.elements.skillModal && typeof bootstrap !== 'undefined') {
        const modal = bootstrap.Modal.getInstance(this.elements.skillModal);
        modal?.hide();
      }
      this.notify('success', `Skill "${skillName}" created for ${agentName}`);
    } catch (error) {
      console.error('Failed to create skill from task:', error);
      this.setSkillDraftError(error?.message || 'Failed to create skill.');
    } finally {
      this.skillDraftSubmitting = false;
      this.updateSkillDraftButtons();
    }
  }
};
