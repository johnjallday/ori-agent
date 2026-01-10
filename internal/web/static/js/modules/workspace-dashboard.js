// Workspace Dashboard - Core Module
// Handles workspace loading, state management, rendering, and utilities
// Additional functionality in: workspace-dashboard-modals.js, workspace-dashboard-agents.js

(function() {
    'use strict';

    const workspaceId = document.body.dataset.workspaceId;

    // Initialize shared state object
    window.wsDashboard = {
        workspaceId: workspaceId,
        currentWorkspace: null,
        workspaceLayout: null,
        storeNodes: [],
        workspaceConnections: [],
        workspaceAttachments: [],
        workspaceAttachmentsById: new Map(),
        tasksById: new Map()
    };

    const state = window.wsDashboard;

    // Load workspace data on page load
    async function loadWorkspaceData() {
        try {
            const response = await fetch(`/api/orchestration/workspace?id=${workspaceId}`);
            if (!response.ok) throw new Error('Failed to load workspace');

            const workspace = await response.json();
            state.currentWorkspace = workspace;
            state.workspaceLayout = workspace.layout;
            state.storeNodes = workspace.store_nodes || workspace.layout?.store_nodes || [];
            state.workspaceConnections = workspace.layout?.workflow_connections || [];
            state.workspaceAttachments = workspace.attachments || [];
            state.workspaceAttachmentsById = new Map((state.workspaceAttachments || []).map(a => [a.id, a]));

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
                    <div style="flex: 1; min-width: 0;">
                        <div class="note-title" style="font-weight: 500; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                            ${escapeHtml(note.name || 'Untitled Note')}
                        </div>
                        <div style="font-size: 0.75rem; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; max-height: 2.4em;">
                            ${escapeHtml((note.content || '').substring(0, 100))}${(note.content || '').length > 100 ? '...' : ''}
                        </div>
                    </div>
                    <div class="d-flex gap-1 ms-2">
                        <button class="modern-btn modern-btn-secondary btn-sm" onclick="openEditNoteModal('${escapeHtml(note.id)}')" title="Edit note">
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                                <path d="M14.06,9L15,9.94L5.92,19H5V18.08L14.06,9M17.66,3C17.41,3 17.15,3.1 16.96,3.29L15.13,5.12L18.88,8.87L20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18.17,3.09 17.92,3 17.66,3M14.06,6.19L3,17.25V21H6.75L17.81,9.94L14.06,6.19Z"/>
                            </svg>
                        </button>
                        <button class="modern-btn modern-btn-danger btn-sm" onclick="deleteNote('${escapeHtml(note.id)}')" title="Delete note">
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
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

        attachmentsList.innerHTML = attachments.map(att => {
            const typeIcon = getAttachmentTypeIcon(att.type);
            return `
                <div class="attachment-item p-2 mb-2" style="border: 1px solid var(--border-color); border-radius: 6px;">
                    <div class="d-flex justify-content-between align-items-start">
                        <div class="d-flex align-items-start gap-2" style="flex: 1; min-width: 0;">
                            <div style="color: var(--text-muted);">${typeIcon}</div>
                            <div style="flex: 1; min-width: 0;">
                                <div class="attachment-title" style="font-weight: 500; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                                    ${escapeHtml(att.title || 'Untitled')}
                                </div>
                                <div style="font-size: 0.75rem; color: var(--text-muted);">
                                    ${att.type ? `<span class="badge rounded-pill" style="background: rgba(52, 152, 219, 0.15); font-size: 0.65rem;">${escapeHtml(att.type)}</span>` : ''}
                                </div>
                            </div>
                        </div>
                        <div class="d-flex gap-1 ms-2">
                            <button class="modern-btn modern-btn-secondary btn-sm" onclick="openEditAttachmentModal('${escapeHtml(att.id)}')" title="Edit attachment">
                                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                                    <path d="M14.06,9L15,9.94L5.92,19H5V18.08L14.06,9M17.66,3C17.41,3 17.15,3.1 16.96,3.29L15.13,5.12L18.88,8.87L20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18.17,3.09 17.92,3 17.66,3M14.06,6.19L3,17.25V21H6.75L17.81,9.94L14.06,6.19Z"/>
                                </svg>
                            </button>
                            <button class="modern-btn modern-btn-danger btn-sm" onclick="deleteAttachment('${escapeHtml(att.id)}')" title="Delete attachment">
                                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                                    <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                                </svg>
                            </button>
                        </div>
                    </div>
                </div>
            `;
        }).join('');
    }

    function getAttachmentTypeIcon(type) {
        switch (type) {
            case 'doc':
                return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/></svg>';
            case 'link':
                return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M10.59,13.41C11,13.8 11,14.44 10.59,14.83C10.2,15.22 9.56,15.22 9.17,14.83C7.22,12.88 7.22,9.71 9.17,7.76V7.76L12.71,4.22C14.66,2.27 17.83,2.27 19.78,4.22C21.73,6.17 21.73,9.34 19.78,11.29L18.29,12.78C18.3,11.96 18.17,11.14 17.89,10.36L18.36,9.88C19.54,8.71 19.54,6.81 18.36,5.64C17.19,4.46 15.29,4.46 14.12,5.64L10.59,9.17C9.41,10.34 9.41,12.24 10.59,13.41M13.41,9.17C13.8,8.78 14.44,8.78 14.83,9.17C16.78,11.12 16.78,14.29 14.83,16.24V16.24L11.29,19.78C9.34,21.73 6.17,21.73 4.22,19.78C2.27,17.83 2.27,14.66 4.22,12.71L5.71,11.22C5.7,12.04 5.83,12.86 6.11,13.65L5.64,14.12C4.46,15.29 4.46,17.19 5.64,18.36C6.81,19.54 8.71,19.54 9.88,18.36L13.41,14.83C14.59,13.66 14.59,11.76 13.41,10.59C13,10.2 13,9.56 13.41,9.17Z"/></svg>';
            case 'file':
                return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/></svg>';
            default:
                return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M16.5,6V17.5A4.5,4.5 0 0,1 12,22A4.5,4.5 0 0,1 7.5,17.5V5A3.5,3.5 0 0,1 11,1.5A3.5,3.5 0 0,1 14.5,5V15.5A2.5,2.5 0 0,1 12,18A2.5,2.5 0 0,1 9.5,15.5V6H11V15.5A1,1 0 0,0 12,16.5A1,1 0 0,0 13,15.5V5A2,2 0 0,0 11,3A2,2 0 0,0 9,5V17.5A3,3 0 0,0 12,20.5A3,3 0 0,0 15,17.5V6H16.5Z"/></svg>';
        }
    }

    // Load tasks
    async function loadTasks() {
        try {
            const response = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
            if (!response.ok) throw new Error('Failed to load tasks');

            const data = await response.json();
            const tasks = data.tasks || [];
            const stats = data.stats || {};
            state.tasksById = new Map(tasks.map(t => [t.id, t]));

            // Update stats
            document.getElementById('tasks-completed').textContent = stats.completed || 0;
            document.getElementById('tasks-in-progress').textContent = stats.in_progress || 0;
            document.getElementById('tasks-failed').textContent = stats.failed || 0;
            const scheduledCount = stats.scheduled || tasks.filter(t => t.schedule_enabled).length;
            document.getElementById('tasks-scheduled').textContent = scheduledCount;

            // Render tasks list
            const tasksList = document.getElementById('tasks-list');
            if (tasks.length === 0) {
                tasksList.innerHTML = '<p style="color: var(--text-muted); text-align: center; padding: 2rem;">No tasks yet</p>';
            } else {
                tasksList.innerHTML = tasks.map(task => renderTaskItem(task)).join('');
            }

        } catch (error) {
            console.error('Error loading tasks:', error);
            document.getElementById('tasks-list').innerHTML = '<p style="color: var(--danger-color); text-align: center; padding: 2rem;">Failed to load tasks</p>';
        }
    }

    function renderTaskItem(task) {
        const assignedTo = getFormattedAgentAssignment(task);
        const assignedNodeId = task.assigned_node_id || null;
        const taskId = `task-${task.id}`;
        const store = assignedNodeId ? state.storeNodes.find(s => s.agent_node_id === assignedNodeId) : null;
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
                ${renderTaskCollapsibles(task, taskId, store, attachments)}
            </div>
        `;
    }

    function renderTaskCollapsibles(task, taskId, store, attachments) {
        let html = '';

        // Result section
        if (task.status === 'completed' && task.result) {
            html += `
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
            `;
        }

        // Error section
        if (task.error) {
            html += `
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
            `;
        }

        // Schedule section (show if task has a schedule)
        if (task.schedule) {
            const schedule = task.schedule;
            const scheduleType = schedule.type || 'interval';
            html += `
                <div class="task-collapsible">
                    <div class="task-collapsible-header" onclick="toggleTaskCollapsible('${taskId}-schedule')">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                            <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
                        </svg>
                        <span>Schedule</span>
                        <svg class="chevron" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                            <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                        </svg>
                        <button class="modern-btn modern-btn-secondary btn-sm" onclick="event.stopPropagation(); openEditTaskModal('${escapeHtml(task.id)}')" title="Edit schedule">Edit</button>
                    </div>
                    <div class="task-collapsible-content" id="${taskId}-schedule">
                        <div class="task-detail-row">
                            <span class="task-detail-label">Type:</span>
                            <span class="task-detail-value">${escapeHtml(scheduleType)}</span>
                        </div>
                        ${scheduleType === 'daily' ? `
                        <div class="task-detail-row">
                            <span class="task-detail-label">Time:</span>
                            <span class="task-detail-value">${escapeHtml(schedule.time || '09:00')}</span>
                        </div>
                        ` : ''}
                        ${scheduleType === 'weekly' ? `
                        <div class="task-detail-row">
                            <span class="task-detail-label">Day:</span>
                            <span class="task-detail-value">${['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'][schedule.day_of_week || 0]}</span>
                        </div>
                        <div class="task-detail-row">
                            <span class="task-detail-label">Time:</span>
                            <span class="task-detail-value">${escapeHtml(schedule.time || '09:00')}</span>
                        </div>
                        ` : ''}
                        ${scheduleType === 'interval' ? `
                        <div class="task-detail-row">
                            <span class="task-detail-label">Every:</span>
                            <span class="task-detail-value">${schedule.interval_minutes || 60} minutes</span>
                        </div>
                        ` : ''}
                        ${scheduleType === 'once' && schedule.run_at ? `
                        <div class="task-detail-row">
                            <span class="task-detail-label">Run at:</span>
                            <span class="task-detail-value">${new Date(schedule.run_at).toLocaleString()}</span>
                        </div>
                        ` : ''}
                        <div class="task-detail-row">
                            <span class="task-detail-label">Status:</span>
                            <span class="task-detail-value status-badge ${task.schedule_enabled ? 'enabled' : 'disabled'}">
                                ${task.schedule_enabled ? 'Enabled' : 'Disabled'}
                            </span>
                        </div>
                        ${task.next_run ? `
                        <div class="task-detail-row">
                            <span class="task-detail-label">Next Run:</span>
                            <span class="task-detail-value">${new Date(task.next_run).toLocaleString()}</span>
                        </div>
                        ` : ''}
                    </div>
                </div>
            `;
        }

        // Storage section
        if (store) {
            html += `
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
            `;
        }

        // Attachments section
        if (attachments.length > 0) {
            html += `
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
            `;
        }

        return html;
    }

    // Refresh tasks
    function refreshTasks() {
        loadTasks();
    }

    async function copyTaskResult(taskId) {
        const task = state.tasksById.get(taskId);
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

    function saveTaskAsWorkflow(taskId) {
        if (!taskId) return;
        window.location.href = `/studios/${workspaceId}/canvas?save_workflow_task=${encodeURIComponent(taskId)}`;
    }

    // Execute task
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

            await loadTasks();
            pollTaskCompletion(taskId);
        } catch (err) {
            console.error(err);
            alert(`Failed to execute task: ${err.message || err}`);
        }
    }

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
                        await loadTasks();
                        return;
                    }

                    setTimeout(poll, intervalMs);
                }
            } catch (err) {
                console.error('Error polling task status:', err);
                setTimeout(poll, intervalMs);
            }
        };

        setTimeout(poll, intervalMs);
    }

    // Delete functions
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

    async function deleteAttachment(attachmentId) {
        if (!confirm('Are you sure you want to delete this attachment?')) return;

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
        if (!Array.isArray(state.workspaceConnections) || state.workspaceConnections.length === 0) return [];
        if (!state.workspaceAttachmentsById || state.workspaceAttachmentsById.size === 0) return [];

        const attachmentIds = new Set();
        for (const conn of state.workspaceConnections) {
            if (!conn) continue;
            if (conn.to === taskId && state.workspaceAttachmentsById.has(conn.from)) {
                attachmentIds.add(conn.from);
            }
            if (conn.from === taskId && state.workspaceAttachmentsById.has(conn.to)) {
                attachmentIds.add(conn.to);
            }
        }

        return Array.from(attachmentIds)
            .map(id => state.workspaceAttachmentsById.get(id))
            .filter(Boolean);
    }

    // Get formatted agent assignment with instance number
    function getFormattedAgentAssignment(task) {
        if (!task.to || task.to === 'unassigned') {
            return 'Unassigned';
        }

        if (task.assigned_node_id) {
            const match = task.assigned_node_id.match(/^(.+)-node-(\d+)$/);
            if (match) {
                const agentName = match[1];
                const instanceNumber = match[2];
                return `${agentName} #${instanceNumber}`;
            }
        }

        if (state.workspaceLayout && state.workspaceLayout.agent_positions) {
            const agentPositionKeys = Object.keys(state.workspaceLayout.agent_positions);
            const matchingKeys = agentPositionKeys.filter(key => key.startsWith(task.to + '-node-'));

            if (matchingKeys.length > 0) {
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

        return task.to;
    }

    // Toggle task collapsible section
    function toggleTaskCollapsible(contentId) {
        const content = document.getElementById(contentId);
        const header = content?.previousElementSibling;
        if (content && header) {
            content.classList.toggle('expanded');
            header.classList.toggle('expanded');
        }
    }

    // Export core functions to global state and window
    Object.assign(state, {
        loadWorkspaceData,
        loadTasks,
        escapeHtml,
        getTaskAttachments,
        getFormattedAgentAssignment
    });

    // Export global functions for onclick handlers
    window.openEditTaskModal = function(taskId) { state.openEditTaskModal(taskId); };
    window.openCreateTaskModal = function() { state.openCreateTaskModal(); };
    window.submitCreateTask = function() { state.submitCreateTask(); };
    window.submitEditTask = function() { state.submitEditTask(); };
    window.openCreateNoteModal = function() { state.openCreateNoteModal(); };
    window.submitCreateNote = function() { state.submitCreateNote(); };
    window.openEditNoteModal = function(noteId) { state.openEditNoteModal(noteId); };
    window.submitEditNote = function() { state.submitEditNote(); };
    window.openEditDescriptionModal = function() { state.openEditDescriptionModal(); };
    window.submitEditDescription = function() { state.submitEditDescription(); };
    window.openEditStorageModal = function(taskId) { state.openEditStorageModal(taskId); };
    window.submitEditStorage = function() { state.submitEditStorage(); };
    window.openEditAttachmentModal = function(attachmentId) { state.openEditAttachmentModal(attachmentId); };
    window.submitEditAttachment = function() { state.submitEditAttachment(); };
    window.openCreateAttachmentModal = function() { state.openCreateAttachmentModal(); };
    window.submitCreateAttachment = function() { state.submitCreateAttachment(); };
    window.openManageAgentsModal = function() { state.openManageAgentsModal(); };
    window.deleteTask = deleteTask;
    window.deleteNote = deleteNote;
    window.deleteAttachment = deleteAttachment;
    window.executeTask = executeTask;
    window.copyTaskResult = copyTaskResult;
    window.saveTaskAsWorkflow = saveTaskAsWorkflow;
    window.viewCanvas = viewCanvas;
    window.refreshTasks = refreshTasks;
    window.toggleTaskCollapsible = toggleTaskCollapsible;
    window.toggleEditTaskScheduleFields = function() { state.toggleEditTaskScheduleFields(); };
    window.updateEditTaskScheduleTypeFields = function() { state.updateEditTaskScheduleTypeFields(); };
    window.toggleCreateTaskScheduleFields = function() { state.toggleCreateTaskScheduleFields(); };
    window.updateCreateTaskScheduleTypeFields = function() { state.updateCreateTaskScheduleTypeFields(); };

    // Load data when page loads
    document.addEventListener('DOMContentLoaded', () => {
        loadWorkspaceData();
    });

})();
