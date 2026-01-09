    const workspaceId = document.body.dataset.workspaceId;
    let currentWorkspace = null; // Cache workspace payload for exporting workflows
    let workspaceLayout = null; // Store layout data for agent instance lookup
    let scheduledTasks = []; // Store scheduled tasks for lookup
    let schedulerNodes = []; // Store scheduler node wrappers for editing
    let storeNodes = []; // Store nodes for lookup
    let workspaceConnections = []; // Store workflow connections for attachment lookup
    let workspaceAttachments = []; // Store attachments for attachment lookup
    let workspaceAttachmentsById = new Map(); // id -> attachment
    let tasksById = new Map(); // id -> task for edit modal

    // Load workspace data on page load
    async function loadWorkspaceData() {
      try {
        const response = await fetch(`/api/orchestration/workspace?id=${workspaceId}`);
        if (!response.ok) throw new Error('Failed to load workspace');

        const workspace = await response.json();
        currentWorkspace = workspace;

        // Store layout for agent instance lookup
        workspaceLayout = workspace.layout;

        // Load scheduler nodes (ensures canvas_node_id exists) for schedule lookup/editing
        schedulerNodes = await loadSchedulerNodes(workspaceId);
        scheduledTasks = normalizeScheduledTasks(schedulerNodes, workspace.scheduled_tasks || []);
        storeNodes = workspace.store_nodes || workspace.layout?.store_nodes || [];
        workspaceConnections = workspace.layout?.workflow_connections || [];
        workspaceAttachments = workspace.attachments || [];
        workspaceAttachmentsById = new Map((workspaceAttachments || []).map(a => [a.id, a]));

        // Update workspace details
        document.getElementById('workspace-name').textContent = workspace.name || 'Unnamed Workspace';
        document.getElementById('workspace-created').textContent = `Created: ${new Date(workspace.created_at).toLocaleString()}`;
        document.getElementById('workspace-updated').textContent = workspace.updated_at ? `Updated: ${new Date(workspace.updated_at).toLocaleString()}` : 'Updated: --';
        document.getElementById('workspace-description').textContent = workspace.description || 'No description';

        // Update status badge
        const statusBadge = document.getElementById('workspace-status-badge');
        const statusClass = workspace.status || 'active';
        statusBadge.innerHTML = `<span class="workspace-status ${statusClass}">${statusClass}</span>`;

        // Load tasks
        await loadTasks();

        // Render sessions list (from session store)
        renderSessions(workspace.sessions || []);

        // Render notes list (from session store)
        renderNotes(workspace.notes || []);

        // Render attachments list (from workspace)
        renderAttachments(workspace.attachments || []);

      } catch (error) {
        console.error('Error loading workspace:', error);
        alert('Failed to load workspace data');
      }
    }

    // Render sessions list
    function renderSessions(sessions) {
      const sessionsList = document.getElementById('sessions-list');
      if (!sessions || sessions.length === 0) {
        sessionsList.innerHTML = '<p style="color: var(--text-muted); font-size: 0.875rem; text-align: center; padding: 1rem;">No sessions in this workspace</p>';
        return;
      }

      sessionsList.innerHTML = sessions.map(session => `
        <a href="/chat?session=${encodeURIComponent(session.id)}" class="d-block text-decoration-none mb-2">
          <div class="session-item p-2" style="border: 1px solid var(--border-color); border-radius: 6px; transition: all 0.2s ease; cursor: pointer;">
            <div class="d-flex justify-content-between align-items-start">
              <div style="flex: 1; min-width: 0;">
                <div class="session-title" style="font-weight: 500; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                  ${escapeHtml(session.title || 'Untitled Session')}
                </div>
                <div style="font-size: 0.75rem; color: var(--text-muted);">
                  ${session.agent_name ? `<span class="me-2">${escapeHtml(session.agent_name)}</span>` : ''}
                  <span>${session.message_count || 0} messages</span>
                </div>
              </div>
              <div style="font-size: 0.7rem; color: var(--text-muted); white-space: nowrap;">
                ${session.updated_at ? new Date(session.updated_at).toLocaleDateString() : ''}
              </div>
            </div>
          </div>
        </a>
      `).join('');
    }

    // Render notes list
    function renderNotes(notes) {
      const notesList = document.getElementById('notes-list');
      if (!notes || notes.length === 0) {
        notesList.innerHTML = '<p style="color: var(--text-muted); font-size: 0.875rem; text-align: center; padding: 1rem;">No notes in this workspace</p>';
        return;
      }

      notesList.innerHTML = notes.map(note => `
        <div class="note-item p-2 mb-2" style="border: 1px solid var(--border-color); border-radius: 6px; border-left: 3px solid var(--primary-color);">
          <div class="d-flex justify-content-between align-items-start">
            <div style="flex: 1; min-width: 0; cursor: pointer;" onclick="openEditNoteModal('${escapeHtml(note.id)}')">
              <div style="font-weight: 500; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                ${escapeHtml(note.name || 'Untitled Note')}
              </div>
              ${note.preview ? `<div style="font-size: 0.75rem; color: var(--text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">${escapeHtml(note.preview)}</div>` : ''}
            </div>
            <div class="d-flex align-items-center gap-1">
              <span style="font-size: 0.7rem; color: var(--text-muted); white-space: nowrap;">
                ${note.updated_at ? new Date(note.updated_at).toLocaleDateString() : ''}
              </span>
              <button class="btn btn-sm p-1" onclick="openEditNoteModal('${escapeHtml(note.id)}')" title="Edit note" style="color: var(--text-muted); background: transparent; border: none;">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M14.06,9L15,9.94L5.92,19H5V18.08L14.06,9M17.66,3C17.41,3 17.15,3.1 16.96,3.29L15.13,5.12L18.88,8.87L20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18.17,3.09 17.92,3 17.66,3M14.06,6.19L3,17.25V21H6.75L17.81,9.94L14.06,6.19Z"/>
                </svg>
              </button>
              <button class="btn btn-sm p-1" onclick="deleteNote('${escapeHtml(note.id)}')" title="Delete note" style="color: var(--danger-color); background: transparent; border: none;">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                </svg>
              </button>
            </div>
          </div>
        </div>
      `).join('');
    }

    // Render attachments list
    function renderAttachments(attachments) {
      const attachmentsList = document.getElementById('attachments-list');
      if (!attachments || attachments.length === 0) {
        attachmentsList.innerHTML = '<p style="color: var(--text-muted); font-size: 0.875rem; text-align: center; padding: 1rem;">No attachments in this workspace</p>';
        return;
      }

      const typeColors = {
        'note': 'var(--warning-color)',
        'link': 'var(--info-color)',
        'file': 'var(--success-color)',
        'image': 'var(--primary-color)'
      };

      const typeIcons = {
        'note': '<path d="M19,3H14.82C14.4,1.84 13.3,1 12,1C10.7,1 9.6,1.84 9.18,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3M12,3A1,1 0 0,1 13,4A1,1 0 0,1 12,5A1,1 0 0,1 11,4A1,1 0 0,1 12,3Z"/>',
        'link': '<path d="M10.59,13.41C11,13.8 11,14.44 10.59,14.83C10.2,15.22 9.56,15.22 9.17,14.83C7.22,12.88 7.22,9.71 9.17,7.76V7.76L12.71,4.22C14.66,2.27 17.83,2.27 19.78,4.22C21.73,6.17 21.73,9.34 19.78,11.29L18.29,12.78C18.3,11.96 18.17,11.14 17.89,10.36L18.36,9.88C19.54,8.71 19.54,6.81 18.36,5.64C17.19,4.46 15.29,4.46 14.12,5.64L10.59,9.17C9.41,10.34 9.41,12.24 10.59,13.41M13.41,9.17C13.8,8.78 14.44,8.78 14.83,9.17C16.78,11.12 16.78,14.29 14.83,16.24V16.24L11.29,19.78C9.34,21.73 6.17,21.73 4.22,19.78C2.27,17.83 2.27,14.66 4.22,12.71L5.71,11.22C5.7,12.04 5.83,12.86 6.11,13.65L5.64,14.12C4.46,15.29 4.46,17.19 5.64,18.36C6.81,19.54 8.71,19.54 9.88,18.36L13.41,14.83C14.59,13.66 14.59,11.76 13.41,10.59C13,10.2 13,9.56 13.41,9.17Z"/>',
        'file': '<path d="M13,9H18.5L13,3.5V9M6,2H14L20,8V20A2,2 0 0,1 18,22H6C4.89,22 4,21.1 4,20V4C4,2.89 4.89,2 6,2M15,18V16H6V18H15M18,14V12H6V14H18Z"/>',
        'image': '<path d="M8.5,13.5L11,16.5L14.5,12L19,18H5M21,19V5C21,3.89 20.1,3 19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19Z"/>'
      };

      attachmentsList.innerHTML = attachments.map(attachment => {
        const typeColor = attachment.color || typeColors[attachment.type] || 'var(--text-muted)';
        const typeIcon = typeIcons[attachment.type] || typeIcons['file'];

        return `
          <div class="attachment-item p-2 mb-2" style="border: 1px solid var(--border-color); border-radius: 6px; border-left: 3px solid ${typeColor};">
            <div class="d-flex align-items-center gap-2">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="${typeColor}">
                ${typeIcon}
              </svg>
              <div style="flex: 1; min-width: 0; cursor: pointer;" onclick="openEditAttachmentModal('${escapeHtml(attachment.id)}')">
                <div style="font-weight: 500; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                  ${escapeHtml(attachment.title || 'Untitled')}
                </div>
                ${attachment.body ? `<div style="font-size: 0.75rem; color: var(--text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">${escapeHtml(attachment.body.substring(0, 100))}</div>` : ''}
              </div>
              <span class="badge" style="background: ${typeColor}; font-size: 0.65rem;">
                ${escapeHtml(attachment.type || 'doc')}
              </span>
              <button class="btn btn-sm p-1" onclick="openEditAttachmentModal('${escapeHtml(attachment.id)}')" title="Edit attachment" style="color: var(--text-muted); background: transparent; border: none;">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M14.06,9L15,9.94L5.92,19H5V18.08L14.06,9M17.66,3C17.41,3 17.15,3.1 16.96,3.29L15.13,5.12L18.88,8.87L20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18.17,3.09 17.92,3 17.66,3M14.06,6.19L3,17.25V21H6.75L17.81,9.94L14.06,6.19Z"/>
                </svg>
              </button>
              <button class="btn btn-sm p-1" onclick="deleteAttachment('${escapeHtml(attachment.id)}')" title="Delete attachment" style="color: var(--danger-color); background: transparent; border: none;">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                </svg>
              </button>
            </div>
          </div>
        `;
      }).join('');
    }

    // Load tasks
    async function loadTasks() {
      try {
        const response = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
        if (!response.ok) throw new Error('Failed to load tasks');

        const data = await response.json();
        const tasks = data.tasks || [];
        const stats = data.stats || {};
        tasksById = new Map(tasks.map(t => [t.id, t]));

        // Update stats using API stats if available, otherwise calculate
        document.getElementById('tasks-completed').textContent = stats.completed || 0;
        document.getElementById('tasks-in-progress').textContent = stats.in_progress || 0;
        document.getElementById('tasks-failed').textContent = stats.failed || 0;
        // Calculate scheduled count from tasks with schedule_enabled
        const scheduledCount = stats.scheduled || tasks.filter(t => t.schedule_enabled).length;
        document.getElementById('tasks-scheduled').textContent = scheduledCount;

        // Render tasks list
        const tasksList = document.getElementById('tasks-list');
        if (tasks.length === 0) {
          tasksList.innerHTML = '<p style="color: var(--text-muted); text-align: center; padding: 2rem;">No tasks yet</p>';
        } else {
          tasksList.innerHTML = tasks.map(task => {
            const assignedTo = getFormattedAgentAssignment(task);
            const assignedNodeId = task.assigned_node_id || null;
            const taskId = `task-${task.id}`;

            // Find scheduler for this task
            const scheduler = scheduledTasks.find(s => s.target_task_id === task.id);

            // Find store node for the assigned agent
            const store = assignedNodeId ? storeNodes.find(s => s.agent_node_id === assignedNodeId) : null;

            // Find attachments connected to this task (via workflow_connections)
            const attachments = getTaskAttachments(task.id);

            return `
              <div class="task-item">
                <div class="task-header">
                  <div class="task-title">${escapeHtml(task.name || task.id)}</div>
                  <div class="d-flex align-items-center gap-2">
                    <button class="modern-btn modern-btn-secondary btn-sm" onclick="saveTaskAsWorkflow('${escapeHtml(task.id)}')" title="Save this task as a workflow">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                        <path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/>
                      </svg>
                      Save
                    </button>
                    <button class="modern-btn modern-btn-secondary btn-sm" onclick="openEditTaskModal('${escapeHtml(task.id)}')" title="Edit task">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                        <path d="M14.06,9L15,9.94L5.92,19H5V18.08L14.06,9M17.66,3C17.41,3 17.15,3.1 16.96,3.29L15.13,5.12L18.88,8.87L20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18.17,3.09 17.92,3 17.66,3M14.06,6.19L3,17.25V21H6.75L17.81,9.94L14.06,6.19Z"/>
                      </svg>
                      Edit
                    </button>
                    ${task.to && task.status !== 'in_progress' ? `
                    <button class="modern-btn modern-btn-primary btn-sm" onclick="executeTask('${escapeHtml(task.id)}')" title="${task.status === 'completed' || task.status === 'failed' ? 'Re-execute task' : 'Execute task now'}">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                        <path d="${task.status === 'completed' || task.status === 'failed' ? 'M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z' : 'M8,5.14V19.14L19,12.14L8,5.14Z'}"/>
                      </svg>
                      ${task.status === 'completed' || task.status === 'failed' ? 'Re-run' : 'Execute'}
                    </button>
                    ` : ''}
                    <button class="modern-btn modern-btn-danger btn-sm" onclick="deleteTask('${escapeHtml(task.id)}')" title="Delete task">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                      </svg>
                    </button>
                    <span class="task-status ${task.status || 'pending'}">${task.status || 'pending'}</span>
                  </div>
                </div>
                ${task.description ? `<div class="task-description">${escapeHtml(task.description)}</div>` : ''}
                <div class="task-meta">
                  <span>
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" style="vertical-align: text-bottom;">
                      <path d="M12,4A4,4 0 0,1 16,8A4,4 0 0,1 12,12A4,4 0 0,1 8,8A4,4 0 0,1 12,4M12,14C16.42,14 20,15.79 20,18V20H4V18C4,15.79 7.58,14 12,14Z"/>
                    </svg>
                    ${escapeHtml(assignedTo)}
                  </span>
                  <span>
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" style="vertical-align: text-bottom;">
                      <path d="M19,19H5V8H19M16,1V3H8V1H6V3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3H18V1M17,12H12V17H17V12Z"/>
                    </svg>
                    ${task.created_at ? new Date(task.created_at).toLocaleString() : '--'}
                  </span>
                  ${task.schedule && task.schedule_enabled ? `
                  <span class="task-schedule-indicator" style="color: var(--accent-primary);">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" style="vertical-align: text-bottom;">
                      <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
                    </svg>
                    Next: ${task.next_run ? new Date(task.next_run).toLocaleString() : 'Not set'}
                  </span>
                  ` : ''}
                </div>

                ${task.status === 'completed' && task.result ? `
                <div class="task-collapsible">
                  <div class="task-collapsible-header" onclick="toggleTaskCollapsible('${taskId}-result')">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M9,7H7V9H9V7M9,11H7V13H9V11M7,15H9V17H7V15M11,7H17V9H11V7M11,11H17V13H11V11M11,15H17V17H11V15M3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3H5A2,2 0 0,0 3,5Z"/>
                    </svg>
                    <span>Result</span>
                    <svg class="chevron" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                    </svg>
                  </div>
                  <div class="task-collapsible-content task-result" id="${taskId}-result">
                    <div class="d-flex justify-content-end mb-2">
                      <button class="modern-btn modern-btn-secondary btn-sm" onclick="event.stopPropagation(); copyTaskResult('${escapeHtml(task.id)}')">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                          <path d="M19,21H8V7H19M19,5H8A2,2 0 0,0 6,7V21A2,2 0 0,0 8,23H19A2,2 0 0,0 21,21V7A2,2 0 0,0 19,5M16,1H4A2,2 0 0,0 2,3V17H4V3H16V1Z"/>
                        </svg>
                        Copy
                      </button>
                    </div>
                    <pre class="task-result-pre">${escapeHtml(task.result)}</pre>
                  </div>
                </div>
                ` : ''}

                ${task.error ? `
                <div class="task-collapsible">
                  <div class="task-collapsible-header" onclick="toggleTaskCollapsible('${taskId}-error')">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M11,15H13V17H11V15M11,7H13V13H11V7M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
                    </svg>
                    <span>Error</span>
                    <svg class="chevron" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                    </svg>
                  </div>
                  <div class="task-collapsible-content task-error" id="${taskId}-error">
                    <pre class="task-result-pre task-error-pre">${escapeHtml(task.error)}</pre>
                  </div>
                </div>
                ` : ''}

                ${scheduler ? `
                <div class="task-collapsible">
                  <div class="task-collapsible-header" onclick="toggleTaskCollapsible('${taskId}-schedule')">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
                    </svg>
                    <span>Schedule</span>
                    <svg class="chevron" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                    </svg>
                    <button class="modern-btn modern-btn-secondary btn-sm" onclick="event.stopPropagation(); openEditScheduleModal('${escapeHtml(task.id)}')" title="Edit schedule">Edit</button>
                  </div>
                  <div class="task-collapsible-content" id="${taskId}-schedule">
                    <div class="task-detail-row">
                      <span class="task-detail-label">Type:</span>
                      <span class="task-detail-value">${escapeHtml(scheduler.schedule_type || 'N/A')}</span>
                    </div>
                    ${scheduler.schedule_type === 'cron' ? `
                    <div class="task-detail-row">
                      <span class="task-detail-label">Cron:</span>
                      <code class="task-detail-value">${escapeHtml(scheduler.cron_expression || 'N/A')}</code>
                    </div>
                    ` : ''}
                    ${scheduler.schedule_type === 'interval' ? `
                    <div class="task-detail-row">
                      <span class="task-detail-label">Every:</span>
                      <span class="task-detail-value">${scheduler.interval_minutes || 0} minutes</span>
                    </div>
                    ` : ''}
                    ${scheduler.schedule_type === 'once' && scheduler.run_at ? `
                    <div class="task-detail-row">
                      <span class="task-detail-label">Run at:</span>
                      <span class="task-detail-value">${new Date(scheduler.run_at).toLocaleString()}</span>
                    </div>
                    ` : ''}
                    <div class="task-detail-row">
                      <span class="task-detail-label">Status:</span>
                      <span class="task-detail-value status-badge ${scheduler.enabled !== false ? 'enabled' : 'disabled'}">
                        ${scheduler.enabled !== false ? 'Enabled' : 'Disabled'}
                      </span>
                    </div>
                  </div>
                </div>
                ` : ''}

                ${store ? `
                <div class="task-collapsible">
                  <div class="task-collapsible-header" onclick="toggleTaskCollapsible('${taskId}-store')">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M6,2H18A2,2 0 0,1 20,4V20A2,2 0 0,1 18,22H6A2,2 0 0,1 4,20V4A2,2 0 0,1 6,2M6,4V8H18V4H6M6,10V14H18V10H6M6,16V20H18V16H6Z"/>
                    </svg>
                    <span>Storage</span>
                    <svg class="chevron" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                    </svg>
                    <button class="modern-btn modern-btn-secondary btn-sm" onclick="event.stopPropagation(); openEditStorageModal('${escapeHtml(task.id)}')" title="Edit storage">Edit</button>
                  </div>
                  <div class="task-collapsible-content" id="${taskId}-store">
                    <div class="task-detail-row">
                      <span class="task-detail-label">Name:</span>
                      <span class="task-detail-value">${escapeHtml(store.name || 'Unnamed')}</span>
                    </div>
                    <div class="task-detail-row">
                      <span class="task-detail-label">Directory:</span>
                      <code class="task-detail-value" style="font-size: 0.75rem;">${escapeHtml(store.base_dir || 'N/A')}</code>
                    </div>
                    <div class="task-detail-row">
                      <span class="task-detail-label">Auto-store:</span>
                      <span class="task-detail-value status-badge ${store.auto_store !== false ? 'enabled' : 'disabled'}">
                        ${store.auto_store !== false ? 'Enabled' : 'Disabled'}
                      </span>
                    </div>
                  </div>
                </div>
                ` : ''}

                ${attachments.length > 0 ? `
                <div class="task-collapsible">
                  <div class="task-collapsible-header" onclick="toggleTaskCollapsible('${taskId}-attachments')">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M16.5,6V17.5A4.5,4.5 0 0,1 12,22A4.5,4.5 0 0,1 7.5,17.5V5A3.5,3.5 0 0,1 11,1.5A3.5,3.5 0 0,1 14.5,5V15.5A2.5,2.5 0 0,1 12,18A2.5,2.5 0 0,1 9.5,15.5V6H11V15.5A1,1 0 0,0 12,16.5A1,1 0 0,0 13,15.5V5A2,2 0 0,0 11,3A2,2 0 0,0 9,5V17.5A3,3 0 0,0 12,20.5A3,3 0 0,0 15,17.5V6H16.5Z"/>
                    </svg>
                    <span>Attachments</span>
                    <svg class="chevron" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                    </svg>
                  </div>
                  <div class="task-collapsible-content" id="${taskId}-attachments">
                    ${attachments.map(att => `
                      <div class="task-detail-row" style="align-items: flex-start;">
                        <span class="task-detail-label">${escapeHtml(att.title || 'Attachment')}:</span>
                        <span class="task-detail-value">
                          <div style="margin-bottom: 6px;">
                            <button class="modern-btn modern-btn-secondary btn-sm" onclick="openEditAttachmentModal('${escapeHtml(att.id)}')" title="Edit attachment">Edit</button>
                          </div>
                          ${att.type ? `<span class="badge rounded-pill" style="background: rgba(52, 152, 219, 0.15); color: var(--text-primary);">${escapeHtml(att.type)}</span>` : ''}
                          ${att.link_url ? `<div><a href="${escapeHtml(att.link_url)}" target="_blank" rel="noopener noreferrer">${escapeHtml(att.link_url)}</a></div>` : ''}
                          ${att.file_meta?.url ? `<div><code style="font-size: 0.75rem;">${escapeHtml(att.file_meta.url)}</code></div>` : ''}
                          ${att.body ? `<div style="margin-top: 6px; color: var(--text-secondary); white-space: pre-wrap;">${escapeHtml(att.body)}</div>` : ''}
                        </span>
                      </div>
                    `).join('')}
                  </div>
                </div>
                ` : ''}
              </div>
            `;
          }).join('');
        }

      } catch (error) {
        console.error('Error loading tasks:', error);
        document.getElementById('tasks-list').innerHTML = '<p style="color: var(--danger-color); text-align: center; padding: 2rem;">Failed to load tasks</p>';
      }
    }

    // Refresh tasks
    function refreshTasks() {
      loadTasks();
    }

    async function copyTaskResult(taskId) {
      const task = tasksById.get(taskId);
      const result = task?.result || '';
      if (!result) {
        alert('No task result to copy');
        return;
      }

      try {
        await navigator.clipboard.writeText(result);
      } catch (err) {
        console.warn('Clipboard API failed; falling back to legacy copy', err);
        const el = document.createElement('textarea');
        el.value = result;
        el.setAttribute('readonly', '');
        el.style.position = 'absolute';
        el.style.left = '-9999px';
        document.body.appendChild(el);
        el.select();
        document.execCommand('copy');
        document.body.removeChild(el);
      }
    }

    // Jump to canvas and open "Save as Workflow" for a specific task
    function saveTaskAsWorkflow(taskId) {
      if (!taskId) return;
      window.location.href = `/studios/${workspaceId}/canvas?save_workflow_task=${encodeURIComponent(taskId)}`;
    }

    // ========== TASK EDIT MODAL ==========

    function deriveToFromAssignedNodeID(assignedNodeId) {
      if (!assignedNodeId) return 'unassigned';

      // Extract agent name from node ID (e.g., "agent-name-node-1" -> "agent-name")
      if (assignedNodeId.includes('-node-')) {
        return assignedNodeId.replace(/-node-\d+$/, '');
      }

      return assignedNodeId;
    }

    async function buildAssignmentSelectOptions(selectEl, task) {
      if (!selectEl) return;

      const currentAssignedNode = task?.assigned_node_id || '';

      const options = [];
      options.push({ label: 'Unassigned', value: 'unassigned' });

      // Fetch all available agents (not workspace-specific)
      try {
        const response = await fetch('/api/agents/dashboard/list');
        if (response.ok) {
          const data = await response.json();
          const agents = data.agents || [];

          agents.forEach(agent => {
            // Use agent name as node ID (agent-name-node-1 format)
            const nodeId = `${agent.name}-node-1`;
            options.push({
              label: agent.name,
              value: `node:${nodeId}`
            });
          });
        }
      } catch (err) {
        console.error('Failed to load agents:', err);
      }

      selectEl.innerHTML = options
        .map(o => `<option value="${escapeHtml(o.value)}">${escapeHtml(o.label)}</option>`)
        .join('');

      if (currentAssignedNode) {
        selectEl.value = `node:${currentAssignedNode}`;
      } else {
        selectEl.value = 'unassigned';
      }
    }

    // Toggle edit task schedule fields visibility
    function toggleEditTaskScheduleFields() {
      const enabled = document.getElementById('edit-task-schedule-enabled').checked;
      const fields = document.getElementById('edit-task-schedule-fields');
      if (fields) {
        fields.style.display = enabled ? 'block' : 'none';
      }
      if (enabled) {
        updateEditTaskScheduleTypeFields();
      }
    }

    // Update edit task schedule type-specific fields
    function updateEditTaskScheduleTypeFields() {
      const scheduleType = document.getElementById('edit-task-schedule-type').value;

      const timeField = document.getElementById('edit-task-schedule-time-field');
      const dayField = document.getElementById('edit-task-schedule-day-field');
      const intervalField = document.getElementById('edit-task-schedule-interval-field');
      const onceField = document.getElementById('edit-task-schedule-once-field');

      // Hide all
      if (timeField) timeField.style.display = 'none';
      if (dayField) dayField.style.display = 'none';
      if (intervalField) intervalField.style.display = 'none';
      if (onceField) onceField.style.display = 'none';

      // Show relevant fields
      switch (scheduleType) {
        case 'daily':
          if (timeField) timeField.style.display = 'block';
          break;
        case 'weekly':
          if (timeField) timeField.style.display = 'block';
          if (dayField) dayField.style.display = 'block';
          break;
        case 'interval':
          if (intervalField) intervalField.style.display = 'block';
          break;
        case 'once':
          if (onceField) onceField.style.display = 'block';
          break;
      }
    }

    // Get schedule data from edit task modal
    function getEditTaskScheduleData() {
      const enabled = document.getElementById('edit-task-schedule-enabled')?.checked;
      if (!enabled) {
        return { schedule_enabled: false };
      }

      const scheduleType = document.getElementById('edit-task-schedule-type').value;
      const scheduleName = document.getElementById('edit-task-schedule-name').value.trim();

      const schedule = { type: scheduleType };

      switch (scheduleType) {
        case 'daily':
          schedule.time = document.getElementById('edit-task-schedule-time').value || '09:00';
          break;
        case 'weekly':
          schedule.time = document.getElementById('edit-task-schedule-time').value || '09:00';
          schedule.day_of_week = document.getElementById('edit-task-schedule-day').value || '1';
          break;
        case 'interval':
          const intervalValue = parseInt(document.getElementById('edit-task-schedule-interval-value').value) || 1;
          const intervalUnit = document.getElementById('edit-task-schedule-interval-unit').value || 'hours';
          let intervalMinutes = intervalValue;
          if (intervalUnit === 'hours') {
            intervalMinutes = intervalValue * 60;
          } else if (intervalUnit === 'days') {
            intervalMinutes = intervalValue * 1440;
          }
          schedule.interval_minutes = intervalMinutes;
          break;
        case 'once':
          const datetime = document.getElementById('edit-task-schedule-datetime').value;
          if (datetime) {
            schedule.run_at = new Date(datetime).toISOString();
          }
          break;
      }

      return {
        schedule,
        schedule_enabled: true,
        schedule_name: scheduleName || undefined
      };
    }

    // Populate edit task schedule fields from task data
    function populateEditTaskScheduleFields(task) {
      const enabledCheckbox = document.getElementById('edit-task-schedule-enabled');
      const scheduleFields = document.getElementById('edit-task-schedule-fields');
      const scheduleName = document.getElementById('edit-task-schedule-name');
      const scheduleType = document.getElementById('edit-task-schedule-type');
      const scheduleTime = document.getElementById('edit-task-schedule-time');
      const scheduleDay = document.getElementById('edit-task-schedule-day');
      const scheduleIntervalValue = document.getElementById('edit-task-schedule-interval-value');
      const scheduleIntervalUnit = document.getElementById('edit-task-schedule-interval-unit');
      const scheduleDatetime = document.getElementById('edit-task-schedule-datetime');
      const scheduleStats = document.getElementById('edit-task-schedule-stats');

      if (task.schedule_enabled && task.schedule) {
        if (enabledCheckbox) enabledCheckbox.checked = true;
        if (scheduleFields) scheduleFields.style.display = 'block';
        if (scheduleName) scheduleName.value = task.schedule_name || '';
        if (scheduleType) scheduleType.value = task.schedule.type || 'interval';

        // Populate type-specific fields
        const schedule = task.schedule;
        if (schedule.time && scheduleTime) scheduleTime.value = schedule.time;
        if (schedule.day_of_week != null && scheduleDay) scheduleDay.value = schedule.day_of_week;

        if (schedule.interval_minutes) {
          const minutes = schedule.interval_minutes;
          if (minutes >= 1440 && minutes % 1440 === 0) {
            if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 1440;
            if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'days';
          } else if (minutes >= 60 && minutes % 60 === 0) {
            if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 60;
            if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
          } else {
            if (scheduleIntervalValue) scheduleIntervalValue.value = minutes;
            if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'minutes';
          }
        }

        if (schedule.run_at && scheduleDatetime) {
          const date = new Date(schedule.run_at);
          scheduleDatetime.value = date.toISOString().slice(0, 16);
        }

        // Show execution stats if available
        if (scheduleStats) {
          scheduleStats.style.display = 'block';
          document.getElementById('edit-task-execution-count').textContent = task.execution_count || 0;

          const nextRunRow = document.getElementById('edit-task-next-run-row');
          const lastRunRow = document.getElementById('edit-task-last-run-row');

          if (task.next_run) {
            nextRunRow.style.display = 'block';
            document.getElementById('edit-task-next-run').textContent = new Date(task.next_run).toLocaleString();
          } else {
            nextRunRow.style.display = 'none';
          }

          if (task.last_run) {
            lastRunRow.style.display = 'block';
            document.getElementById('edit-task-last-run').textContent = new Date(task.last_run).toLocaleString();
          } else {
            lastRunRow.style.display = 'none';
          }
        }

        updateEditTaskScheduleTypeFields();
      } else {
        // Reset schedule fields
        if (enabledCheckbox) enabledCheckbox.checked = false;
        if (scheduleFields) scheduleFields.style.display = 'none';
        if (scheduleName) scheduleName.value = '';
        if (scheduleType) scheduleType.value = 'interval';
        if (scheduleTime) scheduleTime.value = '09:00';
        if (scheduleDay) scheduleDay.value = '1';
        if (scheduleIntervalValue) scheduleIntervalValue.value = '1';
        if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
        if (scheduleDatetime) scheduleDatetime.value = '';
        if (scheduleStats) scheduleStats.style.display = 'none';
        updateEditTaskScheduleTypeFields();
      }
    }

    async function openEditTaskModal(taskId) {
      const task = tasksById.get(taskId);
      if (!task) {
        alert('Task not found');
        return;
      }

      document.getElementById('edit-task-id').textContent = task.id;
      document.getElementById('edit-task-id-hidden').value = task.id;
      document.getElementById('edit-task-from').value = task.from || 'user';
      document.getElementById('edit-task-description').value = task.description || '';

      await buildAssignmentSelectOptions(document.getElementById('edit-task-assignment'), task);

      // Populate schedule fields
      populateEditTaskScheduleFields(task);

      const modal = new bootstrap.Modal(document.getElementById('editTaskModal'));
      modal.show();
    }

    async function submitEditTask() {
      const taskId = document.getElementById('edit-task-id-hidden').value;
      const description = document.getElementById('edit-task-description').value;
      const from = document.getElementById('edit-task-from').value;
      const assignment = document.getElementById('edit-task-assignment').value;

      let to = 'unassigned';
      let assignedNodeId = '';
      if (assignment && assignment.startsWith('node:')) {
        assignedNodeId = assignment.slice('node:'.length);
        to = deriveToFromAssignedNodeID(assignedNodeId);
      }

      const saveBtn = document.getElementById('edit-task-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Saving...';

      // Get schedule data
      const scheduleData = getEditTaskScheduleData();

      try {
        const resp = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}/tasks/${encodeURIComponent(taskId)}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            description: description,
            to: to,
            from: from,
            assigned_node_id: assignedNodeId,
            ...scheduleData
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to update task');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('editTaskModal'));
        modal.hide();

        await loadWorkspaceData();
        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to update task: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== TASK CREATE MODAL ==========

    async function openCreateTaskModal() {
      // Clear form
      document.getElementById('create-task-description').value = '';
      document.getElementById('create-task-details').value = '';
      document.getElementById('create-task-priority').value = '3';

      // Populate agent assignment dropdown with all available agents
      const selectEl = document.getElementById('create-task-assignment');
      const options = [];
      options.push({ label: '-- No agent (manual task) --', value: '' });

      try {
        const response = await fetch('/api/agents/dashboard/list');
        if (response.ok) {
          const data = await response.json();
          const agents = data.agents || [];

          agents.forEach(agent => {
            const nodeId = `${agent.name}-node-1`;
            options.push({
              label: agent.name,
              value: `node:${nodeId}`
            });
          });
        }
      } catch (err) {
        console.error('Failed to load agents:', err);
      }

      selectEl.innerHTML = options
        .map(o => `<option value="${escapeHtml(o.value)}">${escapeHtml(o.label)}</option>`)
        .join('');

      const modal = new bootstrap.Modal(document.getElementById('createTaskModal'));
      modal.show();
    }

    // Toggle schedule fields visibility
    function toggleCreateTaskScheduleFields() {
      const enabled = document.getElementById('create-task-schedule-enabled').checked;
      const fields = document.getElementById('create-task-schedule-fields');
      if (fields) {
        fields.style.display = enabled ? 'block' : 'none';
      }
      if (enabled) {
        updateCreateTaskScheduleTypeFields();
      }
    }

    // Update schedule type-specific fields
    function updateCreateTaskScheduleTypeFields() {
      const scheduleType = document.getElementById('create-task-schedule-type').value;

      const timeField = document.getElementById('create-task-schedule-time-field');
      const dayField = document.getElementById('create-task-schedule-day-field');
      const intervalField = document.getElementById('create-task-schedule-interval-field');
      const onceField = document.getElementById('create-task-schedule-once-field');

      // Hide all
      if (timeField) timeField.style.display = 'none';
      if (dayField) dayField.style.display = 'none';
      if (intervalField) intervalField.style.display = 'none';
      if (onceField) onceField.style.display = 'none';

      // Show relevant fields
      switch (scheduleType) {
        case 'daily':
          if (timeField) timeField.style.display = 'block';
          break;
        case 'weekly':
          if (timeField) timeField.style.display = 'block';
          if (dayField) dayField.style.display = 'block';
          break;
        case 'interval':
          if (intervalField) intervalField.style.display = 'block';
          break;
        case 'once':
          if (onceField) onceField.style.display = 'block';
          break;
      }
    }

    // Get schedule data from create task modal
    function getCreateTaskScheduleData() {
      const enabled = document.getElementById('create-task-schedule-enabled')?.checked;
      if (!enabled) {
        return { schedule_enabled: false };
      }

      const scheduleType = document.getElementById('create-task-schedule-type').value;
      const scheduleName = document.getElementById('create-task-schedule-name').value.trim();

      const schedule = { type: scheduleType };

      switch (scheduleType) {
        case 'daily':
          schedule.time = document.getElementById('create-task-schedule-time').value || '09:00';
          break;
        case 'weekly':
          schedule.time = document.getElementById('create-task-schedule-time').value || '09:00';
          schedule.day_of_week = document.getElementById('create-task-schedule-day').value || '1';
          break;
        case 'interval':
          const intervalValue = parseInt(document.getElementById('create-task-schedule-interval-value').value) || 1;
          const intervalUnit = document.getElementById('create-task-schedule-interval-unit').value || 'hours';
          let intervalMinutes = intervalValue;
          if (intervalUnit === 'hours') {
            intervalMinutes = intervalValue * 60;
          } else if (intervalUnit === 'days') {
            intervalMinutes = intervalValue * 1440;
          }
          schedule.interval_minutes = intervalMinutes;
          break;
        case 'once':
          const datetime = document.getElementById('create-task-schedule-datetime').value;
          if (datetime) {
            schedule.run_at = new Date(datetime).toISOString();
          }
          break;
      }

      return {
        schedule,
        schedule_enabled: true,
        schedule_name: scheduleName || undefined
      };
    }

    // Reset create task modal form
    function resetCreateTaskModal() {
      document.getElementById('create-task-description').value = '';
      document.getElementById('create-task-details').value = '';
      document.getElementById('create-task-priority').value = '3';
      document.getElementById('create-task-assignment').value = '';
      document.getElementById('create-task-schedule-enabled').checked = false;
      document.getElementById('create-task-schedule-fields').style.display = 'none';
      document.getElementById('create-task-schedule-name').value = '';
      document.getElementById('create-task-schedule-type').value = 'interval';
      updateCreateTaskScheduleTypeFields();
    }

    async function submitCreateTask() {
      const description = document.getElementById('create-task-description').value.trim();
      const details = document.getElementById('create-task-details').value.trim();
      const priority = parseInt(document.getElementById('create-task-priority').value) || 3;
      const assignment = document.getElementById('create-task-assignment').value;

      if (!description) {
        alert('Task title is required');
        return;
      }

      let to = '';
      let assignedNodeId = '';
      if (assignment && assignment.startsWith('node:')) {
        assignedNodeId = assignment.slice('node:'.length);
        to = deriveToFromAssignedNodeID(assignedNodeId);
      }

      const saveBtn = document.getElementById('create-task-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Creating...';

      // Get schedule data
      const scheduleData = getCreateTaskScheduleData();

      try {
        const resp = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            studio_id: workspaceId,
            description: description,
            details: details,
            priority: priority,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            ...scheduleData
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to create task');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('createTaskModal'));
        modal.hide();
        resetCreateTaskModal();

        await loadWorkspaceData();
        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to create task: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== NOTE CREATE MODAL ==========

    function openCreateNoteModal() {
      // Clear form
      document.getElementById('create-note-name').value = '';
      document.getElementById('create-note-content').value = '';

      const modal = new bootstrap.Modal(document.getElementById('createNoteModal'));
      modal.show();
    }

    async function submitCreateNote() {
      const name = document.getElementById('create-note-name').value.trim();
      const content = document.getElementById('create-note-content').value.trim();

      const saveBtn = document.getElementById('create-note-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Creating...';

      try {
        const resp = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: name || 'Untitled Note',
            content: content
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to create note');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('createNoteModal'));
        modal.hide();

        // Reload workspace data to refresh notes list
        await loadWorkspaceData();
      } catch (err) {
        console.error(err);
        alert(`Failed to create note: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== EXECUTE TASK FUNCTION ==========

    async function executeTask(taskId) {
      if (!confirm('Execute this task now?')) return;

      try {
        const resp = await fetch('/api/orchestration/tasks/execute', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ task_id: taskId })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to execute task');
        }

        // Refresh immediately to show in_progress status
        await loadTasks();

        // Poll for task completion
        pollTaskCompletion(taskId);
      } catch (err) {
        console.error(err);
        alert(`Failed to execute task: ${err.message || err}`);
      }
    }

    // Poll task status until completed or failed
    async function pollTaskCompletion(taskId, maxAttempts = 36, intervalMs = 5000) {
      let attempts = 0;

      const poll = async () => {
        attempts++;
        if (attempts > maxAttempts) {
          console.log('Task polling timed out');
          return;
        }

        try {
          const resp = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`);
          if (resp.ok) {
            const task = await resp.json();
            const status = task.status;

            if (status === 'completed' || status === 'failed' || status === 'cancelled' || status === 'timeout') {
              // Task finished, refresh the list
              await loadTasks();
              return;
            }

            // Still running, continue polling
            setTimeout(poll, intervalMs);
          }
        } catch (err) {
          console.error('Error polling task status:', err);
          // Continue polling on error
          setTimeout(poll, intervalMs);
        }
      };

      // Start polling after a short delay
      setTimeout(poll, intervalMs);
    }

    // ========== DELETE FUNCTIONS ==========

    async function deleteTask(taskId) {
      if (!confirm('Are you sure you want to delete this task?')) return;

      try {
        const resp = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`, {
          method: 'DELETE'
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to delete task');
        }

        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to delete task: ${err.message || err}`);
      }
    }

    async function deleteNote(noteId) {
      if (!confirm('Are you sure you want to delete this note?')) return;

      try {
        const resp = await fetch(`/api/notes/${encodeURIComponent(noteId)}`, {
          method: 'DELETE'
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to delete note');
        }

        await loadWorkspaceData();
      } catch (err) {
        console.error(err);
        alert(`Failed to delete note: ${err.message || err}`);
      }
    }

    // ========== NOTE EDIT MODAL ==========

    async function openEditNoteModal(noteId) {
      try {
        // Fetch full note content
        const resp = await fetch(`/api/notes/${encodeURIComponent(noteId)}`);
        if (!resp.ok) throw new Error('Failed to load note');

        const note = await resp.json();

        document.getElementById('edit-note-id').value = note.id;
        document.getElementById('edit-note-name').value = note.name || '';
        document.getElementById('edit-note-content').value = note.content || '';

        const modal = new bootstrap.Modal(document.getElementById('editNoteModal'));
        modal.show();
      } catch (err) {
        console.error(err);
        alert(`Failed to load note: ${err.message || err}`);
      }
    }

    async function submitEditNote() {
      const noteId = document.getElementById('edit-note-id').value;
      const name = document.getElementById('edit-note-name').value.trim();
      const content = document.getElementById('edit-note-content').value;

      const saveBtn = document.getElementById('edit-note-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Saving...';

      try {
        const resp = await fetch(`/api/notes/${encodeURIComponent(noteId)}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: name || 'Untitled Note',
            content: content
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to update note');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('editNoteModal'));
        modal.hide();

        await loadWorkspaceData();
      } catch (err) {
        console.error(err);
        alert(`Failed to update note: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== DESCRIPTION EDIT MODAL ==========

    function openEditDescriptionModal() {
      // Get current description from the display element
      const currentDescription = document.getElementById('workspace-description').textContent;
      document.getElementById('edit-description-content').value = currentDescription === 'No description' ? '' : currentDescription;

      const modal = new bootstrap.Modal(document.getElementById('editDescriptionModal'));
      modal.show();
    }

    async function submitEditDescription() {
      const description = document.getElementById('edit-description-content').value.trim();

      const saveBtn = document.getElementById('edit-description-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Saving...';

      try {
        const resp = await fetch(`/api/orchestration/studios?id=${encodeURIComponent(workspaceId)}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            description: description
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to update description');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('editDescriptionModal'));
        modal.hide();

        // Update the display immediately
        document.getElementById('workspace-description').textContent = description || 'No description';
      } catch (err) {
        console.error(err);
        alert(`Failed to update description: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== SCHEDULE EDIT MODAL ==========

    function updateEditScheduleInputs() {
      const type = document.getElementById('edit-schedule-type').value;
      document.getElementById('edit-schedule-interval-fields').style.display = (type === 'interval') ? '' : 'none';
      document.getElementById('edit-schedule-once-fields').style.display = (type === 'once') ? '' : 'none';
      document.getElementById('edit-schedule-daily-fields').style.display = (type === 'daily') ? '' : 'none';
      document.getElementById('edit-schedule-weekly-fields').style.display = (type === 'weekly') ? '' : 'none';
      document.getElementById('edit-schedule-cron-fields').style.display = (type === 'cron') ? '' : 'none';
    }

    function toggleEditScheduleEndDate() {
      const hasEnd = document.getElementById('edit-schedule-has-end-date').checked;
      document.getElementById('edit-schedule-end-date').style.display = hasEnd ? '' : 'none';
    }

    function isoToLocalDatetimeValue(iso) {
      if (!iso) return '';
      try {
        const d = new Date(iso);
        const pad = (n) => String(n).padStart(2, '0');
        return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
      } catch {
        return '';
      }
    }

    function nsToMinutes(ns) {
      if (typeof ns !== 'number') return null;
      return Math.round(ns / 60000000000);
    }

    function buildEditScheduleConfig() {
      const scheduleType = document.getElementById('edit-schedule-type').value || 'interval';
      const config = { type: scheduleType };

      switch (scheduleType) {
        case 'once': {
          const executeAtStr = document.getElementById('edit-schedule-execute-at').value;
          if (!executeAtStr) throw new Error('Please select a date and time');
          const executeAt = new Date(executeAtStr);
          config.execute_at = executeAt.toISOString();
          break;
        }
        case 'interval': {
          let minutes = parseInt(document.getElementById('edit-schedule-interval-minutes').value, 10);
          if (isNaN(minutes) || minutes <= 0) minutes = 60;
          const runOnce = document.getElementById('edit-schedule-interval-run-once').checked;
          if (runOnce) {
            config.type = 'relative_delay';
            config.delay_duration = minutes * 60 * 1000000000;
            config.trigger_once = true;
          } else {
            config.interval = minutes * 60 * 1000000000;
          }
          break;
        }
        case 'daily':
          config.time_of_day = document.getElementById('edit-schedule-daily-time').value;
          break;
        case 'weekly':
          config.time_of_day = document.getElementById('edit-schedule-weekly-time').value;
          config.day_of_week = parseInt(document.getElementById('edit-schedule-weekly-day').value, 10);
          break;
        case 'cron': {
          const expr = document.getElementById('edit-schedule-cron-expr').value.trim();
          if (!expr) throw new Error('Cron expression is required');
          config.cron_expr = expr;
          break;
        }
      }

      if (document.getElementById('edit-schedule-has-end-date').checked) {
        const endDateStr = document.getElementById('edit-schedule-end-date').value;
        if (endDateStr) {
          config.end_date = new Date(endDateStr).toISOString();
        }
      }

      return config;
    }

    function openEditScheduleModal(taskId) {
      const sched = scheduledTasks.find(s => s.target_task_id === taskId);
      if (!sched || !sched.canvas_node_id) {
        alert('No scheduler found for this task');
        return;
      }

      const nodeWrapper = (schedulerNodes || []).find(n => (n.node_id === sched.canvas_node_id) || (n.scheduled_task && n.scheduled_task.canvas_node_id === sched.canvas_node_id));
      const scheduledTask = nodeWrapper?.scheduled_task || null;
      const schedule = scheduledTask?.schedule || {};

      document.getElementById('edit-schedule-node-id').value = sched.canvas_node_id;
      document.getElementById('edit-schedule-task-id').value = taskId;
      document.getElementById('edit-schedule-enabled').checked = scheduledTask ? (scheduledTask.enabled !== false) : (sched.enabled !== false);

      let type = schedule.type || sched.schedule_type || 'interval';
      let runOnce = false;
      let intervalMinutes = null;

      if (type === 'relative_delay' && schedule.trigger_once) {
        runOnce = true;
        intervalMinutes = nsToMinutes(schedule.delay_duration);
        type = 'interval';
      } else if (type === 'interval') {
        intervalMinutes = nsToMinutes(schedule.interval);
      }

      document.getElementById('edit-schedule-type').value = type;
      updateEditScheduleInputs();

      document.getElementById('edit-schedule-interval-minutes').value = intervalMinutes || sched.interval_minutes || 60;
      document.getElementById('edit-schedule-interval-run-once').checked = runOnce;
      document.getElementById('edit-schedule-execute-at').value = isoToLocalDatetimeValue(schedule.execute_at);
      document.getElementById('edit-schedule-daily-time').value = schedule.time_of_day || '09:00';
      document.getElementById('edit-schedule-weekly-day').value = (schedule.day_of_week ?? 1);
      document.getElementById('edit-schedule-weekly-time').value = schedule.time_of_day || '09:00';
      document.getElementById('edit-schedule-cron-expr').value = schedule.cron_expr || sched.cron_expression || '';

      const hasEnd = !!schedule.end_date;
      document.getElementById('edit-schedule-has-end-date').checked = hasEnd;
      revealEditScheduleEndDate(hasEnd);
      document.getElementById('edit-schedule-end-date').value = isoToLocalDatetimeValue(schedule.end_date);

      const modal = new bootstrap.Modal(document.getElementById('editScheduleModal'));
      modal.show();
    }

    function revealEditScheduleEndDate(hasEnd) {
      document.getElementById('edit-schedule-end-date').style.display = hasEnd ? '' : 'none';
    }

    async function submitEditSchedule() {
      const nodeId = document.getElementById('edit-schedule-node-id').value;
      const enabled = document.getElementById('edit-schedule-enabled').checked;

      const saveBtn = document.getElementById('edit-schedule-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Saving...';

      try {
        const schedule = buildEditScheduleConfig();
        const resp = await fetch(`/api/orchestration/workspaces/${encodeURIComponent(workspaceId)}/scheduler-nodes/${encodeURIComponent(nodeId)}?studio_id=${encodeURIComponent(workspaceId)}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            studio_id: workspaceId,
            schedule: schedule,
            enabled: enabled
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to update schedule');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('editScheduleModal'));
        modal.hide();

        await loadWorkspaceData();
        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to update schedule: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== STORAGE EDIT MODAL ==========

    function openEditStorageModal(taskId) {
      const task = tasksById.get(taskId);
      if (!task || !task.assigned_node_id) {
        alert('Task is not assigned to an agent instance');
        return;
      }
      const store = (storeNodes || []).find(s => s.agent_node_id === task.assigned_node_id);
      if (!store) {
        alert('No store node found for this task/agent');
        return;
      }

      document.getElementById('edit-storage-node-id').value = store.canvas_node_id || store.id;
      document.getElementById('edit-storage-name').value = store.name || '';
      document.getElementById('edit-storage-base-dir').value = store.base_dir || '';
      document.getElementById('edit-storage-format').value = store.format || 'json';
      document.getElementById('edit-storage-write-mode').value = store.write_mode || 'overwrite';
      document.getElementById('edit-storage-auto-create-dir').checked = store.auto_create_dir !== false;
      document.getElementById('edit-storage-auto-store').checked = store.auto_store !== false;

      const modal = new bootstrap.Modal(document.getElementById('editStorageModal'));
      modal.show();
    }

    async function submitEditStorage() {
      const nodeId = document.getElementById('edit-storage-node-id').value;
      const name = document.getElementById('edit-storage-name').value;
      const baseDir = document.getElementById('edit-storage-base-dir').value;
      const format = document.getElementById('edit-storage-format').value;
      const writeMode = document.getElementById('edit-storage-write-mode').value;
      const autoCreateDir = document.getElementById('edit-storage-auto-create-dir').checked;
      const autoStore = document.getElementById('edit-storage-auto-store').checked;

      const saveBtn = document.getElementById('edit-storage-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Saving...';

      try {
        const resp = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}/store-nodes/${encodeURIComponent(nodeId)}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: name,
            base_dir: baseDir,
            format: format,
            write_mode: writeMode,
            auto_create_dir: autoCreateDir,
            auto_store: autoStore
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to update storage');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('editStorageModal'));
        modal.hide();

        await loadWorkspaceData();
        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to update storage: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== ATTACHMENT EDIT MODAL ==========

    function openEditAttachmentModal(attachmentId) {
      const att = workspaceAttachmentsById.get(attachmentId);
      if (!att) {
        alert('Attachment not found');
        return;
      }

      document.getElementById('edit-attachment-id').value = att.id;
      document.getElementById('edit-attachment-title').value = att.title || '';
      document.getElementById('edit-attachment-type').value = att.type || 'other';
      document.getElementById('edit-attachment-link-url').value = att.link_url || '';
      document.getElementById('edit-attachment-body').value = att.body || '';
      document.getElementById('edit-attachment-file-url').value = att.file_meta?.url || '';

      const modal = new bootstrap.Modal(document.getElementById('editAttachmentModal'));
      modal.show();
    }

    async function submitEditAttachment() {
      const attachmentId = document.getElementById('edit-attachment-id').value;
      const title = document.getElementById('edit-attachment-title').value;
      const type = document.getElementById('edit-attachment-type').value;
      const linkURL = document.getElementById('edit-attachment-link-url').value;
      const body = document.getElementById('edit-attachment-body').value;

      const existing = workspaceAttachmentsById.get(attachmentId);
      const fileMeta = existing?.file_meta || null;

      const saveBtn = document.getElementById('edit-attachment-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Saving...';

      try {
        const resp = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}/attachments/${encodeURIComponent(attachmentId)}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            title: title,
            type: type,
            link_url: linkURL,
            body: body,
            file_meta: fileMeta
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to update attachment');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('editAttachmentModal'));
        modal.hide();

        await loadWorkspaceData();
        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to update attachment: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== ATTACHMENT CREATE MODAL ==========

    function openCreateAttachmentModal() {
      // Clear form fields
      document.getElementById('create-attachment-title').value = '';
      document.getElementById('create-attachment-type').value = 'doc';
      document.getElementById('create-attachment-body').value = '';
      document.getElementById('create-attachment-link').value = '';
      document.getElementById('create-attachment-color').value = '#3498db';

      const modal = new bootstrap.Modal(document.getElementById('createAttachmentModal'));
      modal.show();
    }

    async function submitCreateAttachment() {
      const title = document.getElementById('create-attachment-title').value.trim();
      const type = document.getElementById('create-attachment-type').value;
      const body = document.getElementById('create-attachment-body').value;
      const linkUrl = document.getElementById('create-attachment-link').value;
      const color = document.getElementById('create-attachment-color').value;

      if (!title) {
        alert('Title is required');
        return;
      }

      const saveBtn = document.getElementById('create-attachment-save-btn');
      const oldHtml = saveBtn.innerHTML;
      saveBtn.disabled = true;
      saveBtn.innerHTML = 'Creating...';

      try {
        const resp = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}/attachments`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            title: title,
            type: type,
            body: body,
            link_url: linkUrl,
            color: color
          })
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to create attachment');
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('createAttachmentModal'));
        modal.hide();

        await loadWorkspaceData();
        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to create attachment: ${err.message || err}`);
      } finally {
        saveBtn.disabled = false;
        saveBtn.innerHTML = oldHtml;
      }
    }

    // ========== ATTACHMENT DELETE ==========

    async function deleteAttachment(attachmentId) {
      if (!confirm('Are you sure you want to delete this attachment?')) {
        return;
      }

      try {
        const resp = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}/attachments/${encodeURIComponent(attachmentId)}`, {
          method: 'DELETE'
        });

        if (!resp.ok) {
          const text = await resp.text();
          throw new Error(text || 'Failed to delete attachment');
        }

        await loadWorkspaceData();
        await loadTasks();
      } catch (err) {
        console.error(err);
        alert(`Failed to delete attachment: ${err.message || err}`);
      }
    }

    // View canvas
    function viewCanvas() {
      // Navigate to workspace-specific canvas page
      window.location.href = `/studios/${workspaceId}/canvas`;
    }

    // HTML escape function
    function escapeHtml(text) {
      if (!text) return '';
      const div = document.createElement('div');
      div.textContent = text;
      return div.innerHTML;
    }

    // Get attachments connected to a task via layout workflow_connections
    function getTaskAttachments(taskId) {
      if (!taskId) return [];
      if (!Array.isArray(workspaceConnections) || workspaceConnections.length === 0) return [];
      if (!workspaceAttachmentsById || workspaceAttachmentsById.size === 0) return [];

      const attachmentIds = new Set();
      for (const conn of workspaceConnections) {
        if (!conn) continue;
        if (conn.to === taskId && workspaceAttachmentsById.has(conn.from)) {
          attachmentIds.add(conn.from);
        }
        if (conn.from === taskId && workspaceAttachmentsById.has(conn.to)) {
          attachmentIds.add(conn.to);
        }
      }

      return Array.from(attachmentIds)
        .map(id => workspaceAttachmentsById.get(id))
        .filter(Boolean);
    }

    // Get formatted agent assignment with instance number (e.g., "default #1")
    function getFormattedAgentAssignment(task) {
      if (!task.to || task.to === 'unassigned') {
        return 'Unassigned';
      }

      // Check if task has assigned_node_id (e.g., "default-node-2")
      if (task.assigned_node_id) {
        // Parse the nodeId format: {agentName}-node-{instanceNumber}
        const match = task.assigned_node_id.match(/^(.+)-node-(\d+)$/);
        if (match) {
          const agentName = match[1];
          const instanceNumber = match[2];
          return `${agentName} #${instanceNumber}`;
        }
      }

      // If no assigned_node_id or couldn't parse, check if there are agent positions
      if (workspaceLayout && workspaceLayout.agent_positions) {
        // Find all agent positions that start with the agent name
        const agentPositionKeys = Object.keys(workspaceLayout.agent_positions);
        const matchingKeys = agentPositionKeys.filter(key => key.startsWith(task.to + '-node-'));

        if (matchingKeys.length > 0) {
          // Sort by instance number and use the first one
          matchingKeys.sort((a, b) => {
            const aMatch = a.match(/-node-(\d+)$/);
            const bMatch = b.match(/-node-(\d+)$/);
            const aNum = aMatch ? parseInt(aMatch[1]) : 0;
            const bNum = bMatch ? parseInt(bMatch[1]) : 0;
            return aNum - bNum;
          });

          const firstKey = matchingKeys[0];
          const match = firstKey.match(/^(.+)-node-(\d+)$/);
          if (match) {
            return `${match[1]} #${match[2]}`;
          }
        }
      }

      // Fallback to task.to
      return task.to;
    }

    // Normalize scheduled tasks from API to the shape expected by the dashboard UI
    function normalizeScheduledTasks(nodeWrappers, fallbackScheduledTasks) {
      const rows = [];

      if (Array.isArray(nodeWrappers) && nodeWrappers.length > 0) {
        nodeWrappers.forEach(item => {
          const task = item && item.scheduled_task ? item.scheduled_task : null;
          if (!task) return;
          const schedule = task.schedule || {};
          const scheduleType = schedule.type || task.schedule_type || 'cron';
          const cron = schedule.cron_expr || task.cron_expression || '';
          const intervalMinutes = parseIntervalToMinutes(schedule.interval || schedule.delay_duration);
          const runAt = schedule.execute_at || null;
          rows.push({
            id: task.id,
            canvas_node_id: task.canvas_node_id || item.node_id,
            target_task_id: task.target_task_id || null,
            schedule_type: scheduleType,
            cron_expression: cron,
            interval_minutes: intervalMinutes,
            run_at: runAt,
            enabled: task.enabled !== false
          });
        });
        return rows;
      }

      if (Array.isArray(fallbackScheduledTasks)) {
        fallbackScheduledTasks.forEach(task => {
          const schedule = task.schedule || {};
          const scheduleType = schedule.type || task.schedule_type || 'cron';
          const cron = schedule.cron_expr || task.cron_expression || '';
          const intervalMinutes = parseIntervalToMinutes(schedule.interval || schedule.delay_duration);
          const runAt = schedule.execute_at || null;
          rows.push({
            id: task.id,
            canvas_node_id: task.canvas_node_id || null,
            target_task_id: task.target_task_id || null,
            schedule_type: scheduleType,
            cron_expression: cron,
            interval_minutes: intervalMinutes,
            run_at: runAt,
            enabled: task.enabled !== false
          });
        });
      }

      return rows;
    }

    // Convert Go time.Duration (ns or string) into minutes for display
    function parseIntervalToMinutes(value) {
      if (value === undefined || value === null) return null;

      if (typeof value === 'number') {
        return Math.round(value / 60000000000); // ns -> minutes
      }

      if (typeof value === 'string') {
        // Supports formats like "10m0s", "1h30m", "45s"
        let minutes = 0;
        const hoursMatch = value.match(/([\d.]+)h/);
        const minsMatch = value.match(/([\d.]+)m/);
        const secsMatch = value.match(/([\d.]+)s/);

        if (hoursMatch) minutes += parseFloat(hoursMatch[1]) * 60;
        if (minsMatch) minutes += parseFloat(minsMatch[1]);
        if (!hoursMatch && !minsMatch && secsMatch) {
          minutes += parseFloat(secsMatch[1]) / 60;
        }

        if (minutes > 0) {
          return Math.round(minutes);
        }
      }

      return null;
    }

    async function loadSchedulerNodes(studioId) {
      try {
        const resp = await fetch(`/api/orchestration/workspaces/${encodeURIComponent(studioId)}/scheduler-nodes?studio_id=${encodeURIComponent(studioId)}`);
        if (!resp.ok) {
          return [];
        }
        const data = await resp.json();
        return data.scheduler_nodes || [];
      } catch {
        return [];
      }
    }

    // Toggle task collapsible section
    function toggleTaskCollapsible(contentId) {
      const content = document.getElementById(contentId);
      const header = content?.previousElementSibling;
      if (content && header) {
        const isExpanded = content.classList.contains('expanded');
        content.classList.toggle('expanded');
        header.classList.toggle('expanded');
      }
    }

    // Load data when page loads
    document.addEventListener('DOMContentLoaded', () => {
      loadWorkspaceData();
    });

    // ========== AGENT MANAGEMENT FUNCTIONS ==========

    let workspaceAvailableProviders = [];

    // Load providers for workspace dashboard
    async function loadWorkspaceProviders() {
      try {
        const response = await fetch('/api/providers');
        const data = await response.json();
        workspaceAvailableProviders = data.providers || [];
        return workspaceAvailableProviders;
      } catch (error) {
        console.error('Failed to load providers:', error);
        return [];
      }
    }

    // Populate model select for workspace dashboard
    function populateWorkspaceModelSelect(modelSelect, selectedType = 'tool-calling') {
      if (!modelSelect || workspaceAvailableProviders.length === 0) {
        console.warn('Cannot populate models: modelSelect or providers missing');
        return;
      }

      modelSelect.innerHTML = '';

      workspaceAvailableProviders.forEach(provider => {
        const providerGroup = document.createElement('optgroup');
        providerGroup.label = provider.display_name;

        provider.models.forEach(model => {
          const option = document.createElement('option');
          option.value = model.value;
          option.textContent = model.label;
          option.setAttribute('data-type', model.type);
          option.setAttribute('data-provider', model.provider);

          if (model.type !== selectedType) {
            option.style.display = 'none';
            option.disabled = true;
          }

          providerGroup.appendChild(option);
        });

        modelSelect.appendChild(providerGroup);
      });

      // Select first available option
      for (let i = 0; i < modelSelect.options.length; i++) {
        if (!modelSelect.options[i].disabled) {
          modelSelect.selectedIndex = i;
          break;
        }
      }
    }

    async function openManageAgentsModal() {
      const modal = new bootstrap.Modal(document.getElementById('manageAgentsModal'));
      modal.show();

      // Load and populate available models
      try {
        await loadWorkspaceProviders();
        const modelSelect = document.getElementById('new-agent-model');
        const typeSelect = document.getElementById('new-agent-type');
        if (modelSelect && typeSelect) {
          populateWorkspaceModelSelect(modelSelect, typeSelect.value);
        }
      } catch (error) {
        console.error('Error loading providers:', error);
      }
    }

    // Update model dropdown when agent type changes
    document.getElementById('new-agent-type').addEventListener('change', function(e) {
      const modelSelect = document.getElementById('new-agent-model');
      if (modelSelect && workspaceAvailableProviders.length > 0) {
        populateWorkspaceModelSelect(modelSelect, e.target.value);
      }
    });

    // Update temperature value display when slider changes
    document.getElementById('new-agent-temperature').addEventListener('input', function(e) {
      document.getElementById('new-agent-temperature-value').textContent = e.target.value;
    });

    // Auto-config state
    let wsDashLLMAvailable = false;
    let wsDashSystemModelConfigured = false;
    let wsDashAutoConfigApplied = false;

    // Check LLM availability
    async function checkWsDashLLMAvailability() {
      try {
        const response = await fetch('/api/agents/auto-config/availability');
        const data = await response.json();
        wsDashLLMAvailable = data.available;
        wsDashSystemModelConfigured = data.system_model_configured || false;
        return data;
      } catch (error) {
        console.error('Failed to check LLM availability:', error);
        wsDashLLMAvailable = false;
        wsDashSystemModelConfigured = false;
        return { available: false, system_model_configured: false };
      }
    }

    // Handle config mode toggle
    function handleWsDashConfigModeChange(mode) {
      const autoConfigSection = document.getElementById('wsDashAutoConfigSection');
      const llmWarning = document.getElementById('wsDashLlmNotAvailableWarning');
      const llmWarningMessage = document.getElementById('wsDashLlmWarningMessage');
      const configModeHelp = document.getElementById('wsDashConfigModeHelp');
      const autoSelectedIndicator = document.getElementById('wsDashAutoSelectedIndicator');

      if (mode === 'auto') {
        if (configModeHelp) configModeHelp.classList.remove('d-none');
        if (wsDashLLMAvailable) {
          if (autoConfigSection) autoConfigSection.classList.remove('d-none');
          if (llmWarning) llmWarning.classList.add('d-none');
        } else {
          if (autoConfigSection) autoConfigSection.classList.add('d-none');
          if (llmWarning) llmWarning.classList.remove('d-none');
          // Update warning message based on what's missing
          if (llmWarningMessage) {
            if (!wsDashSystemModelConfigured) {
              llmWarningMessage.textContent = 'Auto-config requires a System Model to be configured.';
            } else {
              llmWarningMessage.textContent = 'Auto-config requires an LLM provider. Please set up an API key or install Ollama.';
            }
          }
        }
      } else {
        if (autoConfigSection) autoConfigSection.classList.add('d-none');
        if (llmWarning) llmWarning.classList.add('d-none');
        if (configModeHelp) configModeHelp.classList.add('d-none');
        if (autoSelectedIndicator) autoSelectedIndicator.classList.add('d-none');
        wsDashAutoConfigApplied = false;
      }
    }

    function isWsDashAutoConfigFallback(config) {
      return Boolean(config && typeof config.reasoning === 'string' && config.reasoning.startsWith('Auto-config failed'));
    }

    // Generate auto-config
    async function generateWsDashAutoConfig() {
      const description = document.getElementById('wsDashAutoConfigDescription').value.trim();
      const generateBtn = document.getElementById('wsDashGenerateAutoConfigBtn');
      const autoConfigStatus = document.getElementById('wsDashAutoConfigStatus');

      if (!description) {
        alert('Please enter a description of what you want your agent to do.');
        return;
      }

      generateBtn.disabled = true;
      generateBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Generating...';
      if (autoConfigStatus) {
        autoConfigStatus.textContent = 'Analyzing...';
        autoConfigStatus.classList.remove('d-none', 'bg-success', 'bg-danger');
        autoConfigStatus.classList.add('bg-secondary');
      }

      try {
        const response = await fetch('/api/agents/auto-config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ description })
        });

        if (!response.ok) {
          const error = await response.text();
          throw new Error(error || 'Failed to generate configuration');
        }

        const config = await response.json();

        // Apply config
        const typeSelect = document.getElementById('new-agent-type');
        if (typeSelect && config.agent_type) {
          typeSelect.value = config.agent_type;
          typeSelect.dispatchEvent(new Event('change'));
        }

        setTimeout(() => {
          const modelSelect = document.getElementById('new-agent-model');
          if (modelSelect && config.model) {
            for (let i = 0; i < modelSelect.options.length; i++) {
              if (modelSelect.options[i].value === config.model) {
                modelSelect.selectedIndex = i;
                break;
              }
            }
          }
        }, 100);

        const tempSlider = document.getElementById('new-agent-temperature');
        const tempValue = document.getElementById('new-agent-temperature-value');
        if (tempSlider && config.temperature !== undefined) {
          tempSlider.value = config.temperature;
          if (tempValue) tempValue.textContent = config.temperature.toFixed(1);
        }

        const promptTextarea = document.getElementById('new-agent-prompt');
        if (promptTextarea && config.system_prompt) {
          promptTextarea.value = config.system_prompt;
        }

        const fallback = isWsDashAutoConfigFallback(config);
        if (autoConfigStatus) {
          autoConfigStatus.textContent = fallback ? 'Applied (defaults)' : 'Applied!';
          autoConfigStatus.classList.remove('bg-secondary', 'bg-success', 'bg-danger', 'bg-warning');
          autoConfigStatus.classList.add(fallback ? 'bg-warning' : 'bg-success');
        }

        const indicator = document.getElementById('wsDashAutoSelectedIndicator');
        if (indicator) indicator.classList.remove('d-none');
        wsDashAutoConfigApplied = true;

        if (config.reasoning) console.log('Auto-config reasoning:', config.reasoning);
        if (fallback) console.warn('Auto-config failed, using defaults.');

      } catch (error) {
        console.error('Auto-config error:', error);
        if (autoConfigStatus) {
          autoConfigStatus.textContent = 'Failed';
          autoConfigStatus.classList.remove('bg-secondary');
          autoConfigStatus.classList.add('bg-danger');
        }
        alert('Failed to generate configuration: ' + error.message);
      } finally {
        generateBtn.disabled = false;
        generateBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75Z"/></svg>Generate Config';
      }
    }

    // Config mode toggle listeners
    const wsDashConfigModeManual = document.getElementById('wsDashConfigModeManual');
    const wsDashConfigModeAuto = document.getElementById('wsDashConfigModeAuto');

    if (wsDashConfigModeManual) {
      wsDashConfigModeManual.addEventListener('change', function() {
        if (this.checked) handleWsDashConfigModeChange('manual');
      });
    }

    if (wsDashConfigModeAuto) {
      wsDashConfigModeAuto.addEventListener('change', async function() {
        if (this.checked) {
          await checkWsDashLLMAvailability();
          handleWsDashConfigModeChange('auto');
        }
      });
    }

    const wsDashGenerateBtn = document.getElementById('wsDashGenerateAutoConfigBtn');
    if (wsDashGenerateBtn) {
      wsDashGenerateBtn.addEventListener('click', generateWsDashAutoConfig);
    }

    document.getElementById('createAgentForm').addEventListener('submit', async function(e) {
      e.preventDefault();

      const name = document.getElementById('new-agent-name').value.trim();
      const type = document.getElementById('new-agent-type').value;
      const model = document.getElementById('new-agent-model').value.trim();
      const temperature = document.getElementById('new-agent-temperature').value;
      const systemPrompt = document.getElementById('new-agent-prompt').value.trim();

      if (!name) {
        alert('Please enter an agent name');
        return;
      }

      try {
        const requestBody = { name, type };

        if (model) requestBody.model = model;
        if (temperature) requestBody.temperature = parseFloat(temperature);
        if (systemPrompt) requestBody.system_prompt = systemPrompt;

        const response = await fetch('/api/agents', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(requestBody)
        });

        if (!response.ok) {
          const error = await response.text();
          throw new Error(error || 'Failed to create agent');
        }

        const result = await response.json();

        // Clear form
        document.getElementById('createAgentForm').reset();

        // Show success message
        alert('Agent created successfully: ' + name);
      } catch (error) {
        console.error('Error creating agent:', error);
        alert('Failed to create agent: ' + error.message);
      }
    });

