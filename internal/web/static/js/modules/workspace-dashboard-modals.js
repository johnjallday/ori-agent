// Workspace Dashboard - Modal Functions
// Handles all modal-related operations for tasks, notes, attachments, schedules, and storage

(function() {
    'use strict';

    // Ensure shared state exists
    if (!window.wsDashboard) {
        console.error('workspace-dashboard-modals.js: wsDashboard not initialized');
        return;
    }

    const state = window.wsDashboard;

    // ========== TASK EDIT MODAL ==========

    function deriveToFromAssignedNodeID(assignedNodeId) {
        if (!assignedNodeId) return 'unassigned';
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
            .map(o => `<option value="${state.escapeHtml(o.value)}">${state.escapeHtml(o.label)}</option>`)
            .join('');

        if (currentAssignedNode) {
            selectEl.value = `node:${currentAssignedNode}`;
        } else {
            selectEl.value = 'unassigned';
        }
    }

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

    function updateEditTaskScheduleTypeFields() {
        const scheduleType = document.getElementById('edit-task-schedule-type').value;

        const timeField = document.getElementById('edit-task-schedule-time-field');
        const dayField = document.getElementById('edit-task-schedule-day-field');
        const intervalField = document.getElementById('edit-task-schedule-interval-field');
        const onceField = document.getElementById('edit-task-schedule-once-field');

        if (timeField) timeField.style.display = 'none';
        if (dayField) dayField.style.display = 'none';
        if (intervalField) intervalField.style.display = 'none';
        if (onceField) onceField.style.display = 'none';

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

    // ========== AUTO-SAVE FUNCTIONS ==========

    function toggleEditTaskAutoSaveFields() {
        const enabled = document.getElementById('edit-task-autosave-enabled').checked;
        const fields = document.getElementById('edit-task-autosave-fields');
        if (fields) {
            fields.style.display = enabled ? 'block' : 'none';
        }
        if (enabled) {
            updateEditTaskAutoSaveTargetFields();
        }
    }

    function updateEditTaskAutoSaveTargetFields() {
        const target = document.getElementById('edit-task-autosave-target').value;
        const pathField = document.getElementById('edit-task-autosave-path-field');

        if (pathField) {
            pathField.style.display = target === 'custom' ? 'block' : 'none';
        }
    }

    function getEditTaskAutoSaveData() {
        const enabled = document.getElementById('edit-task-autosave-enabled')?.checked;
        if (!enabled) {
            return { result_storage: null };
        }

        const target = document.getElementById('edit-task-autosave-target').value;
        const format = document.getElementById('edit-task-autosave-format').value;

        const resultStorage = {
            enabled: true,
            format: format
        };

        if (target === 'custom') {
            const filePath = document.getElementById('edit-task-autosave-path').value.trim();
            if (filePath) {
                resultStorage.file_path = filePath;
            }
        }

        return { result_storage: resultStorage };
    }

    function populateEditTaskAutoSaveFields(task) {
        const enabledCheckbox = document.getElementById('edit-task-autosave-enabled');
        const autoSaveFields = document.getElementById('edit-task-autosave-fields');
        const targetSelect = document.getElementById('edit-task-autosave-target');
        const pathInput = document.getElementById('edit-task-autosave-path');
        const formatSelect = document.getElementById('edit-task-autosave-format');

        if (task.result_storage?.enabled) {
            if (enabledCheckbox) enabledCheckbox.checked = true;
            if (autoSaveFields) autoSaveFields.style.display = 'block';

            if (task.result_storage.file_path) {
                if (targetSelect) targetSelect.value = 'custom';
                if (pathInput) pathInput.value = task.result_storage.file_path;
            } else {
                if (targetSelect) targetSelect.value = 'default';
            }

            if (formatSelect && task.result_storage.format) {
                formatSelect.value = task.result_storage.format;
            }

            updateEditTaskAutoSaveTargetFields();
        } else {
            if (enabledCheckbox) enabledCheckbox.checked = false;
            if (autoSaveFields) autoSaveFields.style.display = 'none';
            if (targetSelect) targetSelect.value = 'default';
            if (pathInput) pathInput.value = '';
            if (formatSelect) formatSelect.value = 'text';
            updateEditTaskAutoSaveTargetFields();
        }
    }

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
        const task = state.tasksById.get(taskId);
        if (!task) {
            alert('Task not found');
            return;
        }

        document.getElementById('edit-task-id').textContent = task.id;
        document.getElementById('edit-task-id-hidden').value = task.id;
        document.getElementById('edit-task-from').value = task.from || 'user';
        document.getElementById('edit-task-description').value = task.description || '';

        await buildAssignmentSelectOptions(document.getElementById('edit-task-assignment'), task);
        populateEditTaskScheduleFields(task);
        populateEditTaskAutoSaveFields(task);

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

        const scheduleData = getEditTaskScheduleData();
        const autoSaveData = getEditTaskAutoSaveData();

        try {
            const resp = await fetch(`/api/studios/${encodeURIComponent(state.workspaceId)}/tasks/${encodeURIComponent(taskId)}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    description: description,
                    to: to,
                    from: from,
                    assigned_node_id: assignedNodeId,
                    ...scheduleData,
                    ...autoSaveData
                })
            });

            if (!resp.ok) {
                const text = await resp.text();
                throw new Error(text || 'Failed to update task');
            }

            const modal = bootstrap.Modal.getInstance(document.getElementById('editTaskModal'));
            modal.hide();

            await state.loadWorkspaceData();
            await state.loadTasks();
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
        // Use shared TaskModalController
        if (window.taskModalController) {
            window.taskModalController.openForCreate(state.workspaceId, '', () => {
                // Refresh task list after creation
                state.loadTasks();
            });
        } else {
            console.error('TaskModalController not available');
            alert('Task creation modal not available');
        }
    }

    // ========== NOTE MODALS ==========

    function openCreateNoteModal() {
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
            const resp = await fetch(`/api/workspaces/${encodeURIComponent(state.workspaceId)}/notes`, {
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

            await state.loadWorkspaceData();
        } catch (err) {
            console.error(err);
            alert(`Failed to create note: ${err.message || err}`);
        } finally {
            saveBtn.disabled = false;
            saveBtn.innerHTML = oldHtml;
        }
    }

    async function openEditNoteModal(noteId) {
        try {
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

            await state.loadWorkspaceData();
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
            const resp = await fetch(`/api/orchestration/studios?id=${encodeURIComponent(state.workspaceId)}`, {
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

            document.getElementById('workspace-description').textContent = description || 'No description';
        } catch (err) {
            console.error(err);
            alert(`Failed to update description: ${err.message || err}`);
        } finally {
            saveBtn.disabled = false;
            saveBtn.innerHTML = oldHtml;
        }
    }

    // ========== STORAGE EDIT MODAL ==========

    function openEditStorageModal(taskId) {
        const task = state.tasksById.get(taskId);
        if (!task || !task.assigned_node_id) {
            alert('Task is not assigned to an agent instance');
            return;
        }
        const store = (state.storeNodes || []).find(s => s.agent_node_id === task.assigned_node_id);
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
            const resp = await fetch(`/api/studios/${encodeURIComponent(state.workspaceId)}/store-nodes/${encodeURIComponent(nodeId)}`, {
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

            await state.loadWorkspaceData();
            await state.loadTasks();
        } catch (err) {
            console.error(err);
            alert(`Failed to update storage: ${err.message || err}`);
        } finally {
            saveBtn.disabled = false;
            saveBtn.innerHTML = oldHtml;
        }
    }

    // ========== ATTACHMENT MODALS ==========

    function openEditAttachmentModal(attachmentId) {
        const att = state.workspaceAttachmentsById.get(attachmentId);
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

        const existing = state.workspaceAttachmentsById.get(attachmentId);
        const fileMeta = existing?.file_meta || null;

        const saveBtn = document.getElementById('edit-attachment-save-btn');
        const oldHtml = saveBtn.innerHTML;
        saveBtn.disabled = true;
        saveBtn.innerHTML = 'Saving...';

        try {
            const resp = await fetch(`/api/studios/${encodeURIComponent(state.workspaceId)}/attachments/${encodeURIComponent(attachmentId)}`, {
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

            await state.loadWorkspaceData();
            await state.loadTasks();
        } catch (err) {
            console.error(err);
            alert(`Failed to update attachment: ${err.message || err}`);
        } finally {
            saveBtn.disabled = false;
            saveBtn.innerHTML = oldHtml;
        }
    }

    function openCreateAttachmentModal() {
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
            const resp = await fetch(`/api/studios/${encodeURIComponent(state.workspaceId)}/attachments`, {
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

            await state.loadWorkspaceData();
            await state.loadTasks();
        } catch (err) {
            console.error(err);
            alert(`Failed to create attachment: ${err.message || err}`);
        } finally {
            saveBtn.disabled = false;
            saveBtn.innerHTML = oldHtml;
        }
    }

    // Export modal functions to global state
    Object.assign(state, {
        // Task modals
        openEditTaskModal,
        submitEditTask,
        openCreateTaskModal,
        toggleEditTaskScheduleFields,
        updateEditTaskScheduleTypeFields,
        toggleEditTaskAutoSaveFields,
        updateEditTaskAutoSaveTargetFields,

        // Note modals
        openCreateNoteModal,
        submitCreateNote,
        openEditNoteModal,
        submitEditNote,

        // Description modal
        openEditDescriptionModal,
        submitEditDescription,

        // Storage modal
        openEditStorageModal,
        submitEditStorage,

        // Attachment modals
        openEditAttachmentModal,
        submitEditAttachment,
        openCreateAttachmentModal,
        submitCreateAttachment
    });

})();
