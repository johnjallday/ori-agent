/**
 * Workspace Hub Smart Input
 * Handles AI-powered input classification and routing for task/chat creation.
 *
 * @module workspace-hub-smart-input
 */
(function() {
  'use strict';

  const SMART_INPUT_CLASSIFY_ENDPOINT = '/api/smart-input/classify';
  const SMART_INPUT_OVERRIDE_ENDPOINT = '/api/smart-input/override';
  const SMART_INPUT_PROGRESS_STEPS = ['analyze', 'decide', 'execute'];

  let smartInputProgressModal = null;

  /**
   * Get or create the progress modal instance
   * @returns {Object|null} Bootstrap modal instance
   */
  function getProgressModal() {
    const elements = window.WorkspaceHubState.getElements();
    if (!elements.smartInputProgressModal || !window.bootstrap) return null;
    if (!smartInputProgressModal) {
      smartInputProgressModal = new bootstrap.Modal(elements.smartInputProgressModal);
    }
    return smartInputProgressModal;
  }

  /**
   * Set smart input status message
   * @param {string} message - Status message
   * @param {Object} options - Options
   * @param {boolean} options.busy - Whether status is busy state
   */
  function setStatus(message, { busy = false } = {}) {
    const elements = window.WorkspaceHubState.getElements();
    if (!elements.smartInputStatus) return;
    elements.smartInputStatus.textContent = message || '';
    elements.smartInputStatus.classList.toggle('is-busy', busy);
  }

  /**
   * Set smart input enabled state
   * @param {boolean} enabled - Whether input is enabled
   */
  function setEnabled(enabled) {
    const elements = window.WorkspaceHubState.getElements();
    if (elements.smartInputField) elements.smartInputField.disabled = !enabled;
    if (elements.smartInputSubmit) elements.smartInputSubmit.disabled = !enabled;
    if (elements.smartInputCard) {
      elements.smartInputCard.classList.toggle('is-disabled', !enabled);
    }
  }

  /**
   * Set smart input busy state
   * @param {boolean} isBusy - Whether input is busy
   * @param {string} message - Optional status message
   */
  function setBusy(isBusy, message) {
    const elements = window.WorkspaceHubState.getElements();
    if (elements.smartInputField) elements.smartInputField.disabled = isBusy;
    if (elements.smartInputSubmit) elements.smartInputSubmit.disabled = isBusy;
    if (elements.smartInputCard) {
      elements.smartInputCard.dataset.state = isBusy ? 'deciding' : 'idle';
    }
    if (message !== undefined) {
      setStatus(message, { busy: isBusy });
    } else if (isBusy) {
      setStatus('Deciding...', { busy: true });
    } else {
      setStatus('', { busy: false });
    }
  }

  /**
   * Clear smart input field
   */
  function clearField() {
    const elements = window.WorkspaceHubState.getElements();
    if (elements.smartInputField) {
      elements.smartInputField.value = '';
    }
  }

  /**
   * Update progress modal step indicators
   * @param {string} step - Current step ('analyze', 'decide', 'execute')
   * @param {Object} options - Options
   * @param {string} options.headline - Progress headline
   * @param {string} options.message - Progress message
   */
  function updateProgress(step, { headline, message } = {}) {
    const elements = window.WorkspaceHubState.getElements();
    if (elements.smartInputProgressHeadline && headline) {
      elements.smartInputProgressHeadline.textContent = headline;
    }
    if (elements.smartInputProgressMessage && message) {
      elements.smartInputProgressMessage.textContent = message;
    }
    if (!elements.smartInputProgressSteps) return;

    const stepIndex = SMART_INPUT_PROGRESS_STEPS.indexOf(step);
    const items = Array.from(elements.smartInputProgressSteps.querySelectorAll('li'));
    items.forEach((item) => {
      const itemStep = item.dataset.step;
      const itemIndex = SMART_INPUT_PROGRESS_STEPS.indexOf(itemStep);
      item.classList.remove('is-active', 'is-complete');
      if (itemIndex === -1 || stepIndex === -1) return;
      if (itemIndex < stepIndex) item.classList.add('is-complete');
      if (itemIndex === stepIndex) item.classList.add('is-active');
    });
  }

  /**
   * Show progress modal
   * @param {string} step - Current step
   * @param {Object} options - Options for headline and message
   */
  function showProgress(step, { headline, message } = {}) {
    const modal = getProgressModal();
    if (!modal) return;
    updateProgress(step, { headline, message });
    modal.show();
  }

  /**
   * Hide progress modal
   */
  function hideProgress() {
    const modal = getProgressModal();
    if (!modal) return;
    modal.hide();
  }

  /**
   * Cancel smart input operation
   */
  function cancel() {
    const state = window.WorkspaceHubState.getState();
    state.smartInputCancelled = true;
    hideProgress();
    setBusy(false);
    resetPrompt();
    setStatus('Cancelled', { busy: false });

    setTimeout(() => {
      const elements = window.WorkspaceHubState.getElements();
      if (elements.smartInputStatus && elements.smartInputStatus.textContent === 'Cancelled') {
        setStatus('');
      }
    }, 2000);

    if (window.Toast) {
      window.Toast.info('Operation cancelled');
    }
  }

  /**
   * Set default decision indicator in prompt
   * @param {string} decision - 'task' or 'chat' or null
   */
  function setDefaultDecision(decision) {
    const elements = window.WorkspaceHubState.getElements();
    const isTask = decision === 'task';
    const isChat = decision === 'chat';

    if (elements.smartInputPromptTask) {
      elements.smartInputPromptTask.classList.toggle('is-default', isTask);
    }
    if (elements.smartInputPromptChat) {
      elements.smartInputPromptChat.classList.toggle('is-default', isChat);
    }
    if (elements.smartInputPromptHint) {
      if (isTask) {
        elements.smartInputPromptHint.textContent = 'Suggested: Create Task';
      } else if (isChat) {
        elements.smartInputPromptHint.textContent = 'Suggested: Start Assistant';
      } else {
        elements.smartInputPromptHint.textContent = '';
      }
    }
  }

  /**
   * Hide the prompt UI
   */
  function hidePrompt() {
    const elements = window.WorkspaceHubState.getElements();
    if (elements.smartInputPrompt) {
      elements.smartInputPrompt.hidden = true;
    }
  }

  /**
   * Reset the prompt UI state
   */
  function resetPrompt() {
    const state = window.WorkspaceHubState.getState();
    state.smartInput = null;
    hidePrompt();
    setDefaultDecision(null);
  }

  /**
   * Show the prompt UI with classification result
   * @param {Object} payload - Classification payload
   */
  function showPrompt(payload) {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!elements.smartInputPrompt) return;
    state.smartInput = {
      input: payload.input,
      predictedDecision: payload.decision,
      confidence: payload.confidence || 0,
      method: payload.method || 'fallback'
    };
    setDefaultDecision(payload.decision);
    elements.smartInputPrompt.hidden = false;
    setStatus(payload.message || 'Choose where to route this.', { busy: false });
  }

  /**
   * Classify input using AI
   * @param {string} input - Input text to classify
   * @returns {Promise<Object>} Classification result
   */
  async function classifyInput(input) {
    const state = window.WorkspaceHubState.getState();

    const response = await fetch(SMART_INPUT_CLASSIFY_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: state.selectedId,
        input
      })
    });

    if (!response.ok) {
      const errText = await response.text();
      throw new Error(errText || 'Failed to classify input');
    }

    return response.json();
  }

  /**
   * Log an override when user selects different than AI suggested
   * @param {Object} payload - Override data
   */
  async function logOverride(payload) {
    try {
      await fetch(SMART_INPUT_OVERRIDE_ENDPOINT, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: payload.workspaceId,
          input: payload.input,
          predicted_decision: payload.predictedDecision,
          selected_decision: payload.selectedDecision,
          confidence: payload.confidence,
          method: payload.method
        })
      });
    } catch (error) {
      console.warn('Failed to log smart input override:', error);
    }
  }

  /**
   * Extract task from API response
   * @param {Object} payload - API response
   * @returns {Object|null} Task object
   */
  function extractTaskFromResponse(payload) {
    if (!payload) return null;
    if (payload.task) return payload.task;
    if (payload.id) return payload;
    return null;
  }

  function summarizeScheduleForConfirmation(scheduleData) {
    if (!scheduleData || scheduleData.schedule_enabled !== true || !scheduleData.schedule) return '';
    const schedule = scheduleData.schedule;
    return String(
      scheduleData.schedule_name ||
      schedule.description ||
      schedule.expression ||
      schedule.cron ||
      schedule.run_at ||
      schedule.type ||
      'Scheduled'
    ).trim();
  }

  function summarizeResultStorageForConfirmation(resultStorageData) {
    const storage = resultStorageData?.result_storage;
    if (!storage || storage.enabled !== true) return '';
    if (storage.file_path) return `Store result at ${storage.file_path}`;
    if (storage.store_node_id) return `Store result in node ${storage.store_node_id}`;
    return `Store result as ${storage.format || 'text'}`;
  }

  async function confirmTaskCreation(options = {}) {
    const kind = String(options.kind || 'task').trim();
    const title = kind === 'workflow' ? 'Create this workflow?' : 'Create this task?';
    const confirmLabel = kind === 'workflow' ? 'Create Workflow' : 'Create Task';
    const metaItems = ['Assistant', kind === 'workflow' ? `${options.stepCount || 0} steps` : 'Task'];
    if (options.assignee) {
      metaItems.push(String(options.assignee));
    }
    if (options.scheduleSummary) {
      metaItems.push(options.scheduleSummary);
    }

    const details = (Array.isArray(options.details) ? options.details : [])
      .map((item) => String(item || '').trim())
      .filter(Boolean);

    if (window.WorkspaceHubModals && typeof window.WorkspaceHubModals.showExecutionConfirm === 'function') {
      return window.WorkspaceHubModals.showExecutionConfirm({
        eyebrow: 'Assistant Task',
        title,
        message: kind === 'workflow'
          ? 'Assistant wants to create a workflow in this workspace.'
          : 'Assistant wants to create this task in the workspace.',
        confirmLabel,
        cancelLabel: 'Cancel',
        metaItems,
        details
      });
    }

    return window.confirm([title, ...details].join('\n\n'));
  }

  /**
   * Create task from smart input (with auto-parsing)
   * @param {string} input - Input text
   * @returns {Promise<Object>} Creation result
   */
  async function createTaskFromSmartInput(input) {
    const state = window.WorkspaceHubState.getState();

    const fallbackCreate = async (fallbackError) => {
      const confirmed = await confirmTaskCreation({
        kind: 'task',
        details: [
          input,
          fallbackError ? 'Assistant could not auto-parse this request, so it will create a basic task instead.' : ''
        ]
      });
      if (!confirmed) {
        return { kind: 'task', cancelled: true, fallback: false };
      }

      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: state.selectedId,
          description: input,
          details: '',
          priority: 3
        })
      });

      if (!response.ok) {
        const errText = await response.text();
        throw new Error(errText || 'Failed to create task');
      }

      await window.WorkspaceHubTasks.loadTasks(state.selectedId);
      return { kind: 'task', fallback: true, error: fallbackError };
    };

    const parseResponse = await fetch('/api/orchestration/tasks/auto-parse', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: input,
        workspace_id: state.selectedId
      })
    });

    if (!parseResponse.ok) {
      const errText = await parseResponse.text();
      return fallbackCreate(errText || 'Auto-parse unavailable');
    }

    const parsed = await parseResponse.json();

    let scheduleData = { schedule_enabled: false };
    if (parsed.schedule_enabled && parsed.schedule) {
      const schedule = { ...parsed.schedule };
      if (schedule.once_at && !schedule.run_at) {
        schedule.run_at = schedule.once_at;
        delete schedule.once_at;
      }
      scheduleData = {
        schedule: schedule,
        schedule_enabled: true,
        schedule_name: parsed.schedule_name || ''
      };
    }

    let resultStorageData = {};
    if (parsed.result_storage && parsed.result_storage.enabled) {
      resultStorageData = {
        result_storage: {
          enabled: true,
          format: parsed.result_storage.format || 'text',
          store_node_id: parsed.result_storage.store_node_id || undefined,
          file_path: parsed.result_storage.file_path || undefined
        }
      };
    }

    const workflowSteps = Array.isArray(parsed.tasks) ? parsed.tasks.filter(Boolean) : [];
    if (workflowSteps.length > 0) {
      // Create workflow with multiple steps
      const parentTitle = parsed.title || workflowSteps[0]?.title || 'New Workflow';
      const parentDetails = parsed.details || '';
      const parentPriority = parsed.priority || 3;
      const workflowScheduleSummary = summarizeScheduleForConfirmation(scheduleData);
      const workflowStorageSummary = summarizeResultStorageForConfirmation(resultStorageData);
      const workflowDetails = [];

      if (parentTitle) workflowDetails.push(parentTitle);
      if (parentDetails) workflowDetails.push(parentDetails);
      workflowSteps.forEach((step, index) => {
        const stepTitle = String(step?.title || step?.description || `Task ${index + 1}`).trim();
        const stepBits = [`Step ${index + 1}: ${stepTitle}`];
        if (step?.agent_name) {
          stepBits.push(`Assign to ${step.agent_name}`);
        }
        if (step?.details) {
          stepBits.push(String(step.details).trim());
        }
        workflowDetails.push(stepBits.join(' | '));
      });
      if (workflowScheduleSummary) {
        workflowDetails.push(`Schedule: ${workflowScheduleSummary}`);
      }
      if (workflowStorageSummary) {
        workflowDetails.push(workflowStorageSummary);
      }

      const confirmed = await confirmTaskCreation({
        kind: 'workflow',
        stepCount: workflowSteps.length,
        scheduleSummary: workflowScheduleSummary,
        details: workflowDetails
      });
      if (!confirmed) {
        return { kind: 'workflow', cancelled: true, fallback: false };
      }

      const parentResponse = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: state.selectedId,
          description: parentTitle,
          details: parentDetails,
          priority: parentPriority
        })
      });

      if (!parentResponse.ok) {
        const errText = await parentResponse.text();
        return fallbackCreate(errText || 'Failed to create parent task');
      }

      const parentPayload = await parentResponse.json();
      const parentTask = extractTaskFromResponse(parentPayload);
      const parentTaskId = parentTask?.id;

      if (!parentTaskId) {
        throw new Error('Parent task created but ID is missing');
      }

      const stepIdToTaskId = new Map();

      for (let i = 0; i < workflowSteps.length; i++) {
        const step = workflowSteps[i] || {};
        const stepId = step.id || `step-${i + 1}`;
        const stepTitle = step.title || step.description || parsed.title || `Task ${i + 1}`;
        const stepDetails = step.details || '';
        const stepPriority = Number.isInteger(step.priority) ? step.priority : (parsed.priority || 3);

        let to = '';
        let assignedNodeId = '';
        if (step.agent_name) {
          assignedNodeId = `${step.agent_name}-node-1`;
          to = step.agent_name;
        }

        let dependsOn = Array.isArray(step.depends_on) ? step.depends_on : [];
        if (dependsOn.length === 0 && i > 0) {
          const fallbackId = workflowSteps[i - 1]?.id || `step-${i}`;
          dependsOn = [fallbackId];
        }
        const inputTaskIds = dependsOn.map((id) => stepIdToTaskId.get(id)).filter(Boolean);

        const stepScheduleData = i === 0 ? scheduleData : { schedule_enabled: false };
        const stepResultStorageData = i === workflowSteps.length - 1 ? resultStorageData : {};

        const createResponse = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            studio_id: state.selectedId,
            description: stepTitle,
            details: stepDetails,
            priority: stepPriority,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            input_task_ids: inputTaskIds,
            parent_task_id: parentTaskId,
            subtask_index: i + 1,
            ...stepScheduleData,
            ...stepResultStorageData
          })
        });

        if (!createResponse.ok) {
          const errText = await createResponse.text();
          throw new Error(errText || 'Failed to create task');
        }

        const createdPayload = await createResponse.json();
        const createdTask = extractTaskFromResponse(createdPayload);
        if (createdTask?.id) {
          stepIdToTaskId.set(stepId, createdTask.id);
        }
      }

      await window.WorkspaceHubTasks.loadTasks(state.selectedId);
      return { kind: 'workflow', fallback: false };
    }

    // Create single task
    let to = '';
    let assignedNodeId = '';
    if (parsed.agent_name) {
      assignedNodeId = `${parsed.agent_name}-node-1`;
      to = parsed.agent_name;
    }

    const singleTaskScheduleSummary = summarizeScheduleForConfirmation(scheduleData);
    const singleTaskStorageSummary = summarizeResultStorageForConfirmation(resultStorageData);
    const confirmed = await confirmTaskCreation({
      kind: 'task',
      assignee: parsed.agent_name || '',
      scheduleSummary: singleTaskScheduleSummary,
      details: [
        parsed.title || input,
        parsed.details || '',
        singleTaskStorageSummary
      ]
    });
    if (!confirmed) {
      return { kind: 'task', cancelled: true, fallback: false };
    }

    const response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        studio_id: state.selectedId,
        description: parsed.title || input,
        details: parsed.details || '',
        priority: parsed.priority || 3,
        to: to || undefined,
        assigned_node_id: assignedNodeId || undefined,
        ...scheduleData,
        ...resultStorageData
      })
    });

    if (!response.ok) {
      const errText = await response.text();
      throw new Error(errText || 'Failed to create task');
    }

    await window.WorkspaceHubTasks.loadTasks(state.selectedId);
    return { kind: 'task', fallback: false };
  }

  /**
   * Create chat from smart input
   * @param {string} input - Input text
   */
  async function createChatFromSmartInput(input) {
    const state = window.WorkspaceHubState.getState();

    if (!window.sessionManager) {
      throw new Error('Chat manager not available');
    }

    if (typeof window.sessionManager.createAssistantSession === 'function') {
      await window.sessionManager.createAssistantSession(
        state.selectedId,
        input ? input.slice(0, 50) : 'Assistant'
      );
    } else if (window.workspaceDetail && typeof window.workspaceDetail.createSessionWithMessage === 'function') {
      await window.workspaceDetail.createSessionWithMessage(input);
      return;
    } else {
      throw new Error('Assistant session creation is unavailable');
    }

    if (window.sendMessageToChat) {
      setTimeout(() => window.sendMessageToChat(input), 100);
    }

    await window.WorkspaceHubSessions.loadSessions(state.selectedId);
  }

  /**
   * Handle smart input decision (task or chat)
   * @param {string} decision - 'task' or 'chat'
   * @param {Object} classification - Optional classification data
   */
  async function handleDecision(decision, classification = null) {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (state.smartInputCancelled) return;

    const meta = classification || state.smartInput || {};
    const input = meta.input || (elements.smartInputField ? elements.smartInputField.value.trim() : '');
    if (!input) return;

    const predictedDecision = meta.decision || meta.predictedDecision || decision;
    const confidence = meta.confidence || 0;
    const method = meta.method || 'fallback';

    hidePrompt();
    setBusy(true, decision === 'task' ? 'Creating task...' : 'Starting Assistant...');
    showProgress('execute', {
      headline: decision === 'task' ? 'Creating task' : 'Starting Assistant',
      message: decision === 'task' ? 'Building tasks in your workspace.' : 'Opening a new session.'
    });

    try {
      if (decision === 'task') {
        const createResult = await createTaskFromSmartInput(input);
        if (createResult?.cancelled) {
          setBusy(false, 'Task creation cancelled.');
          setStatus('Task creation cancelled.', { busy: false });
          if (window.Toast) {
            window.Toast.info('Task creation cancelled');
          }
          state.smartInput = null;
          return;
        }
        if (createResult?.fallback && window.Toast) {
          window.Toast.warning('Auto-parse unavailable. Created a basic task instead.');
        }
        const createdLabel = createResult?.kind === 'workflow' ? 'Workflow created.' : 'Task created.';
        setBusy(false, createdLabel);
      } else {
        await createChatFromSmartInput(input);
        setBusy(false, 'Assistant started.');
      }

      clearField();
    } catch (error) {
      console.error('Smart input routing failed:', error);
      setBusy(false, 'Something went wrong. Try again.');
      if (window.Toast) {
        window.Toast.error(decision === 'task' ? 'Failed to create task' : 'Failed to start chat');
      }
    } finally {
      hideProgress();
    }

    if (predictedDecision && predictedDecision !== decision) {
      void logOverride({
        workspaceId: state.selectedId,
        input,
        predictedDecision,
        selectedDecision: decision,
        confidence,
        method
      });
    }

    state.smartInput = null;
  }

  /**
   * Create quick task (bypasses AI classification)
   * @param {string} description - Task description
   */
  async function createQuickTask(description) {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!state.selectedId) return;

    const confirmed = await confirmTaskCreation({
      kind: 'task',
      details: [description]
    });
    if (!confirmed) {
      setStatus('Task creation cancelled.', { busy: false });
      if (window.Toast) {
        window.Toast.info('Task creation cancelled');
      }
      return;
    }

    setBusy(true, 'Creating task...');

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: state.selectedId,
          description: description,
          name: description.length > 100 ? description.slice(0, 97) + '...' : description,
          status: 'pending'
        })
      });

      if (!response.ok) {
        throw new Error('Failed to create task');
      }

      if (elements.smartInputField) {
        elements.smartInputField.value = '';
      }
      setBusy(false);
      setStatus('');

      if (window.Toast) {
        window.Toast.success('Task created');
      }

      await window.WorkspaceHubTasks.loadTasks(state.selectedId);
    } catch (error) {
      console.error('Failed to create quick task:', error);
      setBusy(false);
      setStatus('Failed to create task', { busy: false });
      if (window.Toast) {
        window.Toast.error('Failed to create task');
      }
    }
  }

  /**
   * Create quick chat (bypasses AI classification)
   * @param {string} initialMessage - Optional initial message
   */
  async function createQuickChat(initialMessage) {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!state.selectedId) return;

    setBusy(true, 'Starting Assistant...');

    try {
      if (window.sessionManager && typeof window.sessionManager.createAssistantSession === 'function') {
        const session = await window.sessionManager.createAssistantSession(
          state.selectedId,
          initialMessage ? initialMessage.slice(0, 50) : 'Assistant'
        );

        if (elements.smartInputField) {
          elements.smartInputField.value = '';
        }
        setBusy(false);
        setStatus('');

        if (session && session.id) {
          window.location.href = `/chat/${session.id}`;
        }
        return;
      }

      // Fallback: create session via API directly
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          folder_id: state.selectedId,
          title: initialMessage ? initialMessage.slice(0, 50) : 'Assistant'
        })
      });

      if (!response.ok) {
        throw new Error('Failed to create chat');
      }

      const data = await response.json();

      if (elements.smartInputField) {
        elements.smartInputField.value = '';
      }
      setBusy(false);
      setStatus('');

      if (data.session && data.session.id) {
        window.location.href = `/chat/${data.session.id}`;
      } else if (data.id) {
        window.location.href = `/chat/${data.id}`;
      }
    } catch (error) {
      console.error('Failed to create quick chat:', error);
      setBusy(false);
      setStatus('Failed to start chat', { busy: false });
      if (window.Toast) {
        window.Toast.error('Failed to start chat');
      }
    }
  }

  /**
   * Submit smart input for classification and routing
   */
  async function submit() {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!elements.smartInputField) return;

    const input = elements.smartInputField.value.trim();
    if (!input) {
      setStatus('Type something to get started.', { busy: false });
      return;
    }

    if (!state.selectedId) {
      setStatus('Select a workspace first.', { busy: false });
      return;
    }

    // Handle slash commands
    const lowerInput = input.toLowerCase();

    if (window.WorkspaceInputRouter &&
        typeof window.WorkspaceInputRouter.isAskCommand === 'function' &&
        window.WorkspaceInputRouter.isAskCommand(input)) {
      setBusy(true, 'Routing with Assistant...');
      showProgress('analyze', {
        headline: 'Assistant routing',
        message: 'Analyzing your request.'
      });

      try {
        await window.WorkspaceInputRouter.dispatchToAskOri(input, { workspaceId: state.selectedId });
        clearField();
        resetPrompt();
        setBusy(false);
        setStatus('');
      } catch (error) {
        console.error('Assistant routing failed:', error);
        setBusy(false);
        setStatus(error.message || 'Failed to route with Assistant', { busy: false });
        if (window.Toast) {
          window.Toast.error(error.message || 'Failed to route with Assistant');
        }
      } finally {
        hideProgress();
      }
      return;
    }

    if (lowerInput.startsWith('/note ') || lowerInput === '/note') {
      const noteContent = input.slice(6).trim();
      if (!noteContent) {
        setStatus('Usage: /note <your note content>', { busy: false });
        return;
      }
      await window.WorkspaceHubNotes.createQuickNote(noteContent);
      return;
    }

    if (lowerInput.startsWith('/task ') || lowerInput === '/task') {
      const taskContent = input.slice(6).trim();
      if (!taskContent) {
        setStatus('Usage: /task <task description>', { busy: false });
        return;
      }
      await createQuickTask(taskContent);
      return;
    }

    if (lowerInput.startsWith('/chat ') || lowerInput === '/chat') {
      const chatMessage = input.slice(6).trim();
      await createQuickChat(chatMessage);
      return;
    }

    if (lowerInput === '@file' || lowerInput.startsWith('@file ')) {
      window.WorkspaceHubFiles.openAddFileModal();
      return;
    }

    // Reset cancelled flag for new operation
    state.smartInputCancelled = false;

    resetPrompt();
    setBusy(true, 'Deciding...');
    showProgress('analyze', {
      headline: 'Analyzing input',
      message: 'Reviewing your request.'
    });

    let classification;
    try {
      classification = await classifyInput(input);
    } catch (error) {
      console.error('Smart input classification failed:', error);
      if (window.WorkspaceInputRouter &&
          typeof window.WorkspaceInputRouter.canUseAskOri === 'function' &&
          window.WorkspaceInputRouter.canUseAskOri()) {
        try {
          updateProgress('decide', {
            headline: 'Escalating to Assistant',
            message: 'Smart classification unavailable, using full routing.'
          });
          await window.WorkspaceInputRouter.dispatchToAskOri(`/ask ${input}`, { workspaceId: state.selectedId });
          clearField();
          resetPrompt();
          setBusy(false);
          setStatus('');
          hideProgress();
          return;
        } catch (askOriError) {
          console.error('Assistant fallback failed:', askOriError);
        }
      }
      setBusy(false);
      hideProgress();
      showPrompt({
        input,
        decision: 'task',
        confidence: 0,
        method: 'fallback',
        message: 'Could not auto-classify. Choose where to route this.'
      });
      return;
    }

    updateProgress('decide', {
      headline: 'Routing',
      message: 'Choosing the best path.'
    });
    setBusy(false);

    const decision = classification.decision || 'task';
    const payload = {
      input,
      decision,
      confidence: classification.confidence || 0,
      method: classification.method || 'fallback',
      message: classification.message
    };

    if (classification.needs_confirmation) {
      hideProgress();
      showPrompt(payload);
      return;
    }

    await handleDecision(decision, payload);
  }

  /**
   * Bind smart input event handlers
   */
  function bindEvents() {
    const elements = window.WorkspaceHubState.getElements();

    if (elements.smartInputSubmit) {
      elements.smartInputSubmit.addEventListener('click', submit);
    }

    if (elements.smartInputAttachBtn) {
      elements.smartInputAttachBtn.addEventListener('click', () => {
        const state = window.WorkspaceHubState.getState();
        if (state.selectedId) {
          window.WorkspaceHubFiles.openAddFileModal();
        }
      });
    }

    if (elements.smartInputField) {
      elements.smartInputField.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          submit();
        }
      });

      elements.smartInputField.addEventListener('input', () => {
        if (elements.smartInputPrompt && !elements.smartInputPrompt.hidden) {
          resetPrompt();
        }
        setStatus('', { busy: false });
      });
    }

    if (elements.smartInputPromptTask) {
      elements.smartInputPromptTask.addEventListener('click', () => handleDecision('task'));
    }

    if (elements.smartInputPromptChat) {
      elements.smartInputPromptChat.addEventListener('click', () => handleDecision('chat'));
    }

    if (elements.smartInputPromptCancel) {
      elements.smartInputPromptCancel.addEventListener('click', () => {
        resetPrompt();
        setStatus('', { busy: false });
      });
    }

    if (elements.smartInputCancelBtn) {
      elements.smartInputCancelBtn.addEventListener('click', cancel);
    }
  }

  // Expose smart input manager globally
  window.WorkspaceHubSmartInput = {
    setStatus,
    setEnabled,
    setBusy,
    clearField,
    updateProgress,
    showProgress,
    hideProgress,
    cancel,
    setDefaultDecision,
    hidePrompt,
    resetPrompt,
    showPrompt,
    classifyInput,
    logOverride,
    createTaskFromSmartInput,
    createChatFromSmartInput,
    handleDecision,
    createQuickTask,
    createQuickChat,
    submit,
    bindEvents
  };
})();
