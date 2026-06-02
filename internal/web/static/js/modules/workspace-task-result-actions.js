// Selection-driven result actions for WorkspaceTaskPage.
//
// These methods are mixed onto WorkspaceTaskPage.prototype via Object.assign
// in workspace-task.js (same pattern as workspace-task-execution-views.js).
// They let a reader highlight any portion of a task result and act on just
// that span — rather than treating the whole result as one block.
//
// Pass 1 wires the floating popover and the three actions that reuse existing
// flows: Copy, Save as note, and Research (the existing research modal). The
// spawn/ask dialog (highlight -> subtask | follow-up) and the per-item hover
// buttons are layered on in later passes.

import { summarizeText } from './workspace-task.js';

export const taskResultActionsMethods = {
  // Attach the selection listeners once. this.elements.output (#workspace-task-
  // output) is a stable container whose innerHTML is replaced on each render,
  // so a single delegated listener survives re-renders.
  initResultSelectionActions() {
    if (this._resultSelectionActionsBound) return;
    const container = this.elements.output;
    if (!container) return;
    this._resultSelectionActionsBound = true;

    this.boundResultSelectionEvent = (event) => this.handleResultSelectionEvent(event);
    this.boundResultSelectionDismiss = (event) => {
      if (this.resultSelectionPopover && this.resultSelectionPopover.contains(event.target)) return;
      this.closeResultActionPopover();
    };
    this.boundResultSelectionReposition = () => this.closeResultActionPopover();

    container.addEventListener('mouseup', this.boundResultSelectionEvent);
    container.addEventListener('keyup', this.boundResultSelectionEvent);
  },

  handleResultSelectionEvent(_event) {
    // Defer a tick so window.getSelection() reflects the finished selection.
    window.setTimeout(() => {
      const info = this.getResultSelectionInfo();
      if (!info) {
        this.closeResultActionPopover();
        return;
      }
      this.showResultActionPopover(info);
    }, 0);
  },

  // Returns { text, title, rect } when there's a non-trivial selection wholly
  // inside the result body, otherwise null.
  getResultSelectionInfo() {
    const selection = window.getSelection();
    if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;

    const text = String(selection.toString() || '').replace(/ /g, ' ').trim();
    if (text.length < 3) return null;

    const container = this.elements.output;
    if (!container) return null;

    const range = selection.getRangeAt(0);
    const anchor = range.commonAncestorContainer;
    const anchorEl = anchor && anchor.nodeType === Node.ELEMENT_NODE ? anchor : anchor?.parentElement;
    if (!anchorEl || !container.contains(anchorEl)) return null;

    const rect = range.getBoundingClientRect();
    if (!rect || (rect.width === 0 && rect.height === 0)) return null;

    return { text, title: this.deriveSelectionTitle(text), rect };
  },

  deriveSelectionTitle(text) {
    const firstLine = String(text || '')
      .split('\n')
      .map((line) => line.trim())
      .find(Boolean) || String(text || '');
    return summarizeText(firstLine, 80);
  },

  // Discoverable per-item triggers: results are often lists (or bold-led
  // paragraphs) with no Markdown headings, so the heading-based section
  // enhancer never fires. Here we attach a hover-revealed button to each list
  // item / bold-led paragraph that opens the same popover, anchored to the
  // item — so a reader doesn't have to manually select to act on one item.
  enhanceResultItems() {
    // renderOutput rebuilds the result DOM on each call, which would orphan a
    // popover anchored to a now-removed trigger; close it before re-enhancing.
    this.closeResultActionPopover();
    const prose = this.elements.output?.querySelector?.('.workspace-task-page-prose');
    if (!prose) return;

    const candidates = [];
    prose.querySelectorAll('li').forEach((li) => candidates.push(li));
    prose.querySelectorAll(':scope > p').forEach((p) => {
      if (p.firstElementChild && p.firstElementChild.tagName === 'STRONG') candidates.push(p);
    });

    candidates.forEach((item) => {
      if (item.dataset.resultItemEnhanced === 'true') return;
      const text = String(item.textContent || '').replace(/\s+/g, ' ').trim();
      if (text.length < 8) return; // skip trivial markers / empty wrappers

      item.dataset.resultItemEnhanced = 'true';
      item.classList.add('workspace-task-result-item');

      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'workspace-task-result-item-action';
      button.title = 'Actions for this item';
      button.setAttribute('aria-label', 'Actions for this item');
      button.innerHTML = '<i class="bi bi-three-dots" aria-hidden="true"></i>';

      // Stop the button's mouse events from reaching the result container's
      // selection listener — otherwise its mouseup would see a collapsed
      // selection and immediately close the popover we just opened.
      ['mousedown', 'mouseup'].forEach((type) => {
        button.addEventListener(type, (event) => event.stopPropagation());
      });
      button.addEventListener('click', (event) => {
        event.preventDefault();
        event.stopPropagation();
        const rect = button.getBoundingClientRect();
        this.showResultActionPopover({ text, title: this.deriveSelectionTitle(text), rect });
      });

      item.appendChild(button);
    });
  },

  showResultActionPopover({ text, title, rect }) {
    this.closeResultActionPopover();

    const menu = document.createElement('div');
    menu.className = 'workspace-task-selection-popover';
    menu.setAttribute('role', 'toolbar');
    menu.setAttribute('aria-label', 'Actions for the selected result text');
    menu.innerHTML = `
      <button type="button" class="workspace-task-selection-action" data-selection-action="spawn" title="Turn the selection into a subtask or follow-up task">
        <i class="bi bi-diagram-2" aria-hidden="true"></i><span>Task</span>
      </button>
      <button type="button" class="workspace-task-selection-action" data-selection-action="research" title="Research or verify this">
        <i class="bi bi-search" aria-hidden="true"></i><span>Research</span>
      </button>
      <button type="button" class="workspace-task-selection-action" data-selection-action="ask" title="Ask the agent about this">
        <i class="bi bi-chat-dots" aria-hidden="true"></i><span>Ask</span>
      </button>
      <button type="button" class="workspace-task-selection-action" data-selection-action="note" title="Save the selection as a note">
        <i class="bi bi-journal-plus" aria-hidden="true"></i><span>Note</span>
      </button>
      <button type="button" class="workspace-task-selection-action" data-selection-action="copy" title="Copy the selection">
        <i class="bi bi-clipboard" aria-hidden="true"></i><span>Copy</span>
      </button>
    `;

    document.body.appendChild(menu);
    this.resultSelectionPopover = menu;
    this.positionResultActionPopover(menu, rect);

    menu.querySelectorAll('[data-selection-action]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const action = btn.getAttribute('data-selection-action') || '';
        this.closeResultActionPopover();
        void this.handleResultSelectionAction(action, { text, title });
      });
    });

    // Defer wiring the dismiss listeners so the click/mousedown that produced
    // the selection doesn't immediately close the popover.
    window.setTimeout(() => {
      document.addEventListener('mousedown', this.boundResultSelectionDismiss, true);
      window.addEventListener('scroll', this.boundResultSelectionReposition, true);
      window.addEventListener('resize', this.boundResultSelectionReposition, true);
    }, 0);
  },

  positionResultActionPopover(menu, rect) {
    const pad = 10;
    const gap = 8;
    const menuRect = menu.getBoundingClientRect();

    let top = rect.top - menuRect.height - gap;
    if (top < pad) top = rect.bottom + gap; // flip below when there's no room above

    let left = rect.left + rect.width / 2 - menuRect.width / 2;
    left = Math.min(Math.max(left, pad), Math.max(pad, window.innerWidth - menuRect.width - pad));
    top = Math.min(Math.max(top, pad), Math.max(pad, window.innerHeight - menuRect.height - pad));

    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
  },

  closeResultActionPopover() {
    if (this.resultSelectionPopover) {
      this.resultSelectionPopover.remove();
      this.resultSelectionPopover = null;
    }
    document.removeEventListener('mousedown', this.boundResultSelectionDismiss, true);
    window.removeEventListener('scroll', this.boundResultSelectionReposition, true);
    window.removeEventListener('resize', this.boundResultSelectionReposition, true);
  },

  async handleResultSelectionAction(action, { text, title } = {}) {
    const selection = String(text || '').trim();
    if (!selection) return;

    switch (action) {
      case 'copy':
        await this.copyToClipboard(selection, 'Selection copied');
        return;
      case 'note':
        await this.saveResultSelectionAsNote(selection, title);
        return;
      case 'research':
        this.researchResultSelection(selection, title);
        return;
      case 'spawn':
        this.openSelectionTaskDialog({ text: selection, title, mode: 'spawn' });
        return;
      case 'ask':
        this.openSelectionTaskDialog({ text: selection, title, mode: 'ask' });
        return;
      default:
        return;
    }
  },

  // Opens the existing research modal seeded with the highlighted text. The
  // modal/submit path accepts a null section (sectionId becomes ''), so no
  // DOM section element is required.
  researchResultSelection(text, title) {
    if (typeof this.buildResultResearchDraft !== 'function' || typeof this.openResultResearchModal !== 'function') {
      this.notify('error', 'Research is not available on this page.');
      return;
    }
    const sectionTitle = title || this.deriveSelectionTitle(text);
    const draft = this.buildResultResearchDraft(null, sectionTitle, text);
    this.openResultResearchModal(draft);
  },

  async saveResultSelectionAsNote(text, title) {
    if (!this.workspaceId) {
      this.notify('error', 'Workspace is not loaded yet — try again in a moment.');
      return;
    }
    const name = (title || this.deriveSelectionTitle(text) || 'Result excerpt').trim();
    const sourceLabel = typeof this.getTaskDisplayLabel === 'function' ? this.getTaskDisplayLabel() : '';
    const sourceId = String(this.task?.id || this.taskId || '').trim();
    const footerParts = [sourceLabel ? `From task: ${sourceLabel}` : '', sourceId ? `(${sourceId})` : '']
      .filter(Boolean)
      .join(' ');
    const content = footerParts ? `${text}\n\n---\n${footerParts}` : text;

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, content })
      });
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(this.parseResponseError(errorText, 'Failed to save selection as note.'));
      }
      this.notify('success', 'Selection saved as a note');
    } catch (error) {
      console.error('Failed to save selection as note:', error);
      this.notify('error', error?.message || 'Failed to save selection as note');
    }
  },

  // The next subtask_index for a child created under this task: one past the
  // highest existing index so it sorts to the end of the step list.
  getNextSubtaskIndex() {
    const subtasks = typeof this.getSubtasks === 'function' ? this.getSubtasks() : [];
    const maxIndex = (Array.isArray(subtasks) ? subtasks : []).reduce((max, item) => {
      const index = Number(item?.subtask_index);
      return Number.isFinite(index) && index > max ? index : max;
    }, 0);
    return maxIndex + 1;
  },

  buildSelectionDialogAgentOptions(selectedAgent = '') {
    const normalizedSelected = String(selectedAgent || '').trim();
    const names = typeof this.getAssignableAgentNames === 'function'
      ? this.getAssignableAgentNames(normalizedSelected)
      : [];
    const options = ['<option value="">Unassigned (manual task)</option>'];
    names.forEach((agentName) => {
      const name = String(agentName || '').trim();
      if (!name) return;
      const selected = name.toLowerCase() === normalizedSelected.toLowerCase() ? ' selected' : '';
      options.push(`<option value="${this.escapeHtml(name)}"${selected}>${this.escapeHtml(name)}</option>`);
    });
    return options.join('');
  },

  // A self-contained dialog (no Bootstrap dependency) for turning a highlighted
  // excerpt into a child subtask or a standalone follow-up. "ask" mode reuses
  // the same form, pre-framed as a question with quick presets.
  openSelectionTaskDialog({ text, title, mode = 'spawn' } = {}) {
    const selection = String(text || '').trim();
    if (!selection) return;
    if (!this.task?.id) {
      this.notify('error', 'Source task is not loaded yet — try again in a moment.');
      return;
    }
    this.closeSelectionTaskDialog();

    const isAsk = mode === 'ask';
    const currentAgent = String(this.task?.to || '').trim();
    const defaultAgent = currentAgent && currentAgent.toLowerCase() !== 'unassigned' ? currentAgent : '';
    // A question is usually standalone; an extracted action usually belongs to
    // this task. Default accordingly, but the choice is always shown.
    const defaultRelationship = isAsk ? 'followup' : 'subtask';
    const excerpt = summarizeText(selection, 280);

    const backdrop = document.createElement('div');
    backdrop.className = 'workspace-task-selection-dialog-backdrop';
    backdrop.innerHTML = `
      <div class="workspace-task-selection-dialog" role="dialog" aria-modal="true" aria-label="${isAsk ? 'Ask about the selection' : 'Create a task from the selection'}">
        <div class="workspace-task-selection-dialog-head">
          <h3>${isAsk ? 'Ask about this' : 'Create a task from this'}</h3>
          <button type="button" class="workspace-task-selection-dialog-close" data-dialog-action="cancel" aria-label="Close">
            <i class="bi bi-x-lg" aria-hidden="true"></i>
          </button>
        </div>
        <blockquote class="workspace-task-selection-dialog-excerpt">${this.escapeHtml(excerpt)}</blockquote>

        <div class="workspace-task-selection-dialog-field">
          <span class="workspace-task-selection-dialog-label">Add as</span>
          <div class="workspace-task-selection-dialog-segmented" role="radiogroup" aria-label="Task relationship">
            <label class="workspace-task-selection-dialog-segment">
              <input type="radio" name="selection-task-relationship" value="subtask"${defaultRelationship === 'subtask' ? ' checked' : ''} />
              <span>Subtask of this task</span>
            </label>
            <label class="workspace-task-selection-dialog-segment">
              <input type="radio" name="selection-task-relationship" value="followup"${defaultRelationship === 'followup' ? ' checked' : ''} />
              <span>Standalone follow-up</span>
            </label>
          </div>
        </div>

        ${isAsk ? `
        <div class="workspace-task-selection-dialog-presets" role="group" aria-label="Question presets">
          <button type="button" class="workspace-task-selection-dialog-preset" data-preset="Explain this in more detail.">Explain</button>
          <button type="button" class="workspace-task-selection-dialog-preset" data-preset="Verify this and cite sources.">Verify</button>
          <button type="button" class="workspace-task-selection-dialog-preset" data-preset="Summarize this concisely.">Summarize</button>
        </div>` : ''}

        <label class="workspace-task-selection-dialog-field">
          <span class="workspace-task-selection-dialog-label">${isAsk ? 'What do you want to ask?' : 'What should the task do?'}</span>
          <textarea class="form-control" data-dialog-field="description" rows="3" placeholder="${isAsk ? 'e.g. Explain this in more detail.' : 'Describe the task'}"></textarea>
        </label>

        <label class="workspace-task-selection-dialog-field">
          <span class="workspace-task-selection-dialog-label">Assign to</span>
          <select class="form-select" data-dialog-field="agent">${this.buildSelectionDialogAgentOptions(defaultAgent)}</select>
        </label>

        <label class="workspace-task-selection-dialog-check">
          <input type="checkbox" data-dialog-field="run-now"${isAsk ? ' checked' : ''} />
          <span>Run it now</span>
        </label>

        <div class="workspace-task-selection-dialog-error" data-dialog-field="error" hidden></div>

        <div class="workspace-task-selection-dialog-actions">
          <button type="button" class="modern-btn modern-btn-secondary" data-dialog-action="cancel">Cancel</button>
          <button type="button" class="modern-btn modern-btn-primary" data-dialog-action="submit">
            <span>${isAsk ? 'Ask' : 'Create task'}</span>
          </button>
        </div>
      </div>
    `;

    document.body.appendChild(backdrop);
    this.selectionTaskDialog = backdrop;

    const descriptionInput = backdrop.querySelector('[data-dialog-field="description"]');
    if (descriptionInput && !isAsk) descriptionInput.value = selection;

    backdrop.querySelectorAll('[data-preset]').forEach((btn) => {
      btn.addEventListener('click', () => {
        if (descriptionInput) {
          descriptionInput.value = btn.getAttribute('data-preset') || '';
          descriptionInput.focus();
        }
      });
    });

    backdrop.querySelectorAll('[data-dialog-action="cancel"]').forEach((btn) => {
      btn.addEventListener('click', () => this.closeSelectionTaskDialog());
    });
    backdrop.querySelector('[data-dialog-action="submit"]')?.addEventListener('click', () => {
      void this.submitSelectionTaskDialog(selection, mode);
    });

    backdrop.addEventListener('mousedown', (event) => {
      if (event.target === backdrop) this.closeSelectionTaskDialog();
    });
    this.boundSelectionDialogKeydown = (event) => {
      if (event.key === 'Escape') this.closeSelectionTaskDialog();
    };
    document.addEventListener('keydown', this.boundSelectionDialogKeydown, true);

    window.requestAnimationFrame(() => descriptionInput?.focus());
  },

  closeSelectionTaskDialog() {
    if (this._selectionTaskSubmitting) return;
    if (this.selectionTaskDialog) {
      this.selectionTaskDialog.remove();
      this.selectionTaskDialog = null;
    }
    if (this.boundSelectionDialogKeydown) {
      document.removeEventListener('keydown', this.boundSelectionDialogKeydown, true);
      this.boundSelectionDialogKeydown = null;
    }
  },

  setSelectionDialogError(message) {
    const errorEl = this.selectionTaskDialog?.querySelector('[data-dialog-field="error"]');
    if (!errorEl) return;
    if (message) {
      errorEl.textContent = message;
      errorEl.hidden = false;
    } else {
      errorEl.textContent = '';
      errorEl.hidden = true;
    }
  },

  async submitSelectionTaskDialog(selection, mode) {
    if (this._selectionTaskSubmitting) return;
    const dialog = this.selectionTaskDialog;
    if (!dialog) return;

    const description = String(dialog.querySelector('[data-dialog-field="description"]')?.value || '').trim();
    if (!description) {
      this.setSelectionDialogError(mode === 'ask' ? 'Type a question first.' : 'Describe what the task should do.');
      dialog.querySelector('[data-dialog-field="description"]')?.focus();
      return;
    }
    if (!this.task?.id) {
      this.setSelectionDialogError('Source task is not loaded yet — try again in a moment.');
      return;
    }
    this.setSelectionDialogError('');

    const relationship = String(dialog.querySelector('input[name="selection-task-relationship"]:checked')?.value || 'subtask');
    const isSubtask = relationship === 'subtask';
    const agent = String(dialog.querySelector('[data-dialog-field="agent"]')?.value || '').trim();
    const runNow = Boolean(dialog.querySelector('[data-dialog-field="run-now"]')?.checked);

    const sourceLabel = typeof this.getTaskDisplayLabel === 'function' ? this.getTaskDisplayLabel() : '';
    const sourceId = String(this.task.id);
    const details = [
      `Created from a highlighted excerpt of ${sourceLabel ? `"${sourceLabel}"` : 'a task'} (${sourceId}):`,
      '',
      `"${selection}"`
    ].join('\n');

    const payload = {
      workspace_id: this.workspaceId,
      description,
      details,
      status: 'pending',
      to: agent || undefined,
      input_task_ids: [sourceId]
    };
    if (isSubtask) {
      payload.parent_task_id = sourceId;
      payload.subtask_index = this.getNextSubtaskIndex();
    }

    const submitBtn = dialog.querySelector('[data-dialog-action="submit"]');
    const submitText = submitBtn?.querySelector('span');
    const originalText = submitText?.textContent || 'Create task';
    this._selectionTaskSubmitting = true;
    if (submitBtn) submitBtn.disabled = true;
    if (submitText) submitText.textContent = runNow ? 'Creating & running…' : 'Creating…';

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(this.parseResponseError(text, 'Failed to create task.'));
      }
      const created = await response.json().catch(() => ({}));
      const newTaskId = String(created?.task?.id || created?.id || '').trim();

      if (runNow && newTaskId) {
        const executeResponse = await fetch('/api/orchestration/tasks/execute', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ task_id: newTaskId })
        });
        if (!executeResponse.ok) {
          const text = await executeResponse.text();
          throw new Error(this.parseResponseError(text, 'Task was created but could not be started.'));
        }
      }

      this._selectionTaskSubmitting = false;
      this.closeSelectionTaskDialog();
      this.notify('success', isSubtask ? 'Subtask created' : 'Follow-up task created');

      // A subtask belongs to this task's graph, so refresh in place to surface
      // it in the step list. A standalone follow-up is its own task, so send
      // the user there to run/edit it (matching the existing follow-up flow).
      if (isSubtask) {
        if (typeof this.refreshAfterStepChange === 'function') {
          await this.refreshAfterStepChange();
        } else {
          await this.loadData();
        }
      } else if (newTaskId) {
        window.location.href = this.getTaskHref(newTaskId);
      }
    } catch (error) {
      console.error('Failed to create task from selection:', error);
      this._selectionTaskSubmitting = false;
      this.setSelectionDialogError(error?.message || 'Failed to create task.');
    } finally {
      this._selectionTaskSubmitting = false;
      if (submitBtn) submitBtn.disabled = false;
      if (submitText) submitText.textContent = originalText;
    }
  },
};
