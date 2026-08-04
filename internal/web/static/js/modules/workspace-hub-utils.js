/**
 * Workspace Hub Utilities
 * Shared helper functions for the workspace hub module.
 *
 * @module workspace-hub-utils
 */
(function () {
  'use strict';

  /**
   * Format a date value for display
   * @param {string|Date} value - Date to format
   * @returns {string} Formatted date string or '--' if invalid
   */
  function formatDate(value) {
    if (!value) return '--';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '--';
    return date.toLocaleString();
  }

  /**
   * Format a file size in bytes to human-readable format
   * @param {number} bytes - File size in bytes
   * @returns {string} Formatted size string
   */
  function formatFileSize(bytes) {
    if (!bytes) return '';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  }

  /**
   * Get an appropriate icon SVG for a file based on type/mime
   * @param {string} type - File type
   * @param {string} mime - MIME type
   * @returns {string} SVG icon HTML
   */
  function getFileIcon(type, mime) {
    if (type === 'image' || (mime && mime.startsWith('image/'))) {
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8.5,13.5L11,16.5L14.5,12L19,18H5M21,19V5C21,3.89 20.1,3 19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19Z"/></svg>';
    }
    if (type === 'doc' || (mime && (mime.includes('text') || mime.includes('document')))) {
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M14,17H7V15H14M17,13H7V11H17M17,9H7V7H17M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3Z"/></svg>';
    }
    return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M13,9V3.5L18.5,9M6,2C4.89,2 4,2.89 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2H6Z"/></svg>';
  }

  /**
   * Flatten a hierarchical workspace tree into a flat array
   * @param {Array} workspaces - Hierarchical workspace array
   * @param {number} depth - Current depth (internal)
   * @param {Array} path - Current path (internal)
   * @returns {Array} Flattened array with depth and path info
   */
  function flattenWorkspaces(workspaces, depth = 0, path = []) {
    const rows = [];
    workspaces.forEach(workspace => {
      const currentPath = [...path, workspace.name || 'Untitled'];
      rows.push({
        ...workspace,
        depth,
        path: currentPath.join(' / ')
      });

      if (workspace.children && workspace.children.length > 0) {
        rows.push(...flattenWorkspaces(workspace.children, depth + 1, currentPath));
      }
    });
    return rows;
  }

  /**
   * Compute task statistics from a list of tasks
   * @param {Array} tasks - Array of task objects
   * @returns {Object} Stats object with completed, in_progress, failed, scheduled counts
   */
  function computeTaskStats(tasks) {
    const stats = {
      completed: 0,
      in_progress: 0,
      pending: 0,
      blocked: 0,
      failed: 0,
      scheduled: 0,
      needs_attention: 0
    };

    (tasks || []).forEach(task => {
      const status = String(task.status || 'pending')
        .trim()
        .toLowerCase();
      const hasNeedsReview = (
        Array.isArray(task.execution_history) ? task.execution_history : []
      ).some(entry => {
        const validation = entry?.validation_result || entry?.validation || null;
        return (
          String(validation?.validation_status || '')
            .trim()
            .toLowerCase() === 'needs_review'
        );
      });
      if (status === 'completed') stats.completed += 1;
      if (status === 'in_progress') stats.in_progress += 1;
      if (status === 'pending' || status === 'assigned') stats.pending += 1;
      if (status === 'blocked' || status === 'waiting_for_choice') stats.blocked += 1;
      if (status === 'failed') stats.failed += 1;
      if (task.schedule_enabled) stats.scheduled += 1;
      // Needs attention combines blocked-ish + failed so users have one
      // pill to scan when they open the hub looking for "what needs me".
      if (
        status === 'blocked' ||
        status === 'waiting_for_choice' ||
        status === 'failed' ||
        hasNeedsReview
      ) {
        stats.needs_attention += 1;
      }
    });

    return stats;
  }

  /**
   * Build a task hierarchy from a flat task list
   * @param {Array} tasks - Flat array of tasks
   * @returns {Object} Hierarchy object with taskById, subtasksByParent, rootTasks
   */
  function buildTaskHierarchy(tasks) {
    const taskById = new Map();
    const subtasksByParent = new Map();
    const rootTasks = [];

    (tasks || []).forEach(task => {
      if (task && task.id) {
        taskById.set(task.id, task);
      }
    });

    (tasks || []).forEach(task => {
      if (!task || !task.id) return;
      const parentId = task.parent_task_id;
      if (parentId && taskById.has(parentId)) {
        if (!subtasksByParent.has(parentId)) {
          subtasksByParent.set(parentId, []);
        }
        subtasksByParent.get(parentId).push(task);
      } else {
        rootTasks.push(task);
      }
    });

    // Sort subtasks by index, then by created_at
    subtasksByParent.forEach(list => {
      list.sort((a, b) => {
        const aIndex =
          Number.isFinite(a.subtask_index) && a.subtask_index > 0
            ? a.subtask_index
            : Number.MAX_SAFE_INTEGER;
        const bIndex =
          Number.isFinite(b.subtask_index) && b.subtask_index > 0
            ? b.subtask_index
            : Number.MAX_SAFE_INTEGER;
        if (aIndex !== bIndex) return aIndex - bIndex;
        const aTime = a.created_at ? new Date(a.created_at).getTime() : 0;
        const bTime = b.created_at ? new Date(b.created_at).getTime() : 0;
        if (aTime !== bTime) return aTime - bTime;
        return String(a.id || '').localeCompare(String(b.id || ''));
      });
    });

    return { taskById, subtasksByParent, rootTasks };
  }

  /**
   * Get display status for a parent task based on subtask statuses
   * @param {Object} task - Parent task
   * @param {Array} subtasks - Array of subtasks
   * @returns {string} Computed status
   */
  function getDisplayStatus(task, subtasks) {
    if (!subtasks || subtasks.length === 0) return task.status || 'pending';

    const statuses = subtasks.map(subtask => subtask.status || 'pending');
    if (statuses.some(status => status === 'in_progress')) return 'in_progress';
    if (statuses.some(status => status === 'failed')) return 'failed';
    if (statuses.some(status => status === 'waiting_for_choice')) return 'waiting_for_choice';
    if (statuses.some(status => status === 'timeout')) return 'timeout';
    if (statuses.some(status => status === 'cancelled')) return 'cancelled';
    if (statuses.every(status => status === 'completed')) return 'completed';
    if (statuses.some(status => status === 'assigned')) return 'assigned';
    return task.status || 'pending';
  }

  /**
   * Get display result for a task (including checking subtasks)
   * @param {Object} task - Task object
   * @param {Array} subtasks - Optional array of subtasks
   * @returns {Object|null} Result object with label and text, or null
   */
  function getDisplayResult(task, subtasks) {
    if (task.error) return { label: 'Error', text: task.error };
    if (task.result) return { label: 'Result', text: task.result };
    if (subtasks && subtasks.length > 0) {
      const lastSubtask = subtasks[subtasks.length - 1];
      if (lastSubtask.error) return { label: 'Error', text: lastSubtask.error };
      if (lastSubtask.result) return { label: 'Result', text: lastSubtask.result };
    }
    return null;
  }

  /**
   * Get kanban column id from a task (UI-only)
   * @param {Object} task - Task object
   * @param {string} fallback - Fallback column id
   * @returns {string} Column id
   */
  function getTaskKanbanColumnId(task, fallback = 'backlog') {
    if (!task) return fallback;
    const ctx = task.context;
    if (!ctx || typeof ctx !== 'object') return fallback;
    const value = ctx.kanban_column_id;
    if (typeof value !== 'string') return fallback;
    const trimmed = value.trim();
    return trimmed || fallback;
  }

  /**
   * Group tasks by kanban column id
   * @param {Array} tasks - Tasks list
   * @param {Array} columns - Board columns
   * @param {string} fallback - Fallback column id
   * @returns {Map<string, Array>} Map of columnId -> tasks
   */
  function groupTasksByKanbanColumn(tasks, columns, fallback = 'backlog') {
    const groups = new Map();
    const known = new Set();
    (columns || []).forEach(col => {
      if (col && col.id) {
        groups.set(col.id, []);
        known.add(col.id);
      }
    });

    (tasks || []).forEach(task => {
      let columnId = getTaskKanbanColumnId(task, fallback);
      if (known.size > 0 && !known.has(columnId)) {
        columnId = fallback;
      }
      if (!groups.has(columnId)) {
        groups.set(columnId, []);
      }
      groups.get(columnId).push(task);
    });

    return groups;
  }

  /**
   * Collect all descendant workspace ids from a workspace tree
   * @param {Array} workspaces - Workspace tree
   * @param {string} rootId - Root workspace id
   * @param {Object} options - Options
   * @param {boolean} options.includeRoot - Whether to include root id
   * @returns {Array<string>} Descendant ids (optionally includes root)
   */
  function collectWorkspaceDescendantIds(workspaces, rootId, { includeRoot = false } = {}) {
    const ids = [];
    if (!rootId) return ids;

    function walk(nodes, inSubtree) {
      (nodes || []).forEach(node => {
        if (!node || !node.id) return;

        const isRoot = node.id === rootId;
        const nextInSubtree = inSubtree || isRoot;

        if (nextInSubtree) {
          if (isRoot) {
            if (includeRoot) ids.push(node.id);
          } else {
            ids.push(node.id);
          }
        }

        if (node.children && node.children.length > 0) {
          walk(node.children, nextInSubtree);
        }
      });
    }

    walk(workspaces, false);
    return ids;
  }

  // Expose utilities globally
  window.WorkspaceHubUtils = {
    formatDate,
    formatFileSize,
    getFileIcon,
    flattenWorkspaces,
    computeTaskStats,
    buildTaskHierarchy,
    getDisplayStatus,
    getDisplayResult,
    getTaskKanbanColumnId,
    groupTasksByKanbanColumn,
    collectWorkspaceDescendantIds
  };
})();
