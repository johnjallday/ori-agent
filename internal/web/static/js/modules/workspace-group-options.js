/**
 * Workspace Group Options — shared parent-group <select> helpers
 *
 * Builds the "parent group" <select> used by both the workspace-hub create
 * modal (workspace-create.js → populateWorkspaceParentSelect) and the sessions
 * create-folder modal (sessions.js → resetAddWorkspaceModalForm). Centralizing
 * the walk/render/disable logic keeps the two modals in sync.
 *
 * Exposes window.WorkspaceGroupOptions:
 *   - collectWorkspaceGroupOptions(nodes)       → [{ id, name, depth }]
 *   - renderWorkspaceParentOptions(groups)      → <option> markup string
 *   - workspaceParentSelectState(n)             → { disabled, help } for the owner to apply
 *
 * @module workspace-group-options
 */
(function () {
  'use strict';

  // Walk a workspace tree and collect groups (kind === 'group'), tracking
  // nesting depth so options can be indented.
  function collectWorkspaceGroupOptions(nodes) {
    const groups = [];

    (function walk(items, depth) {
      (items || []).forEach(node => {
        if (!node || !node.id) return;
        if (String(node.kind || '').trim() === 'group') {
          groups.push({ id: node.id, name: node.name || node.id, depth });
        }
        if (Array.isArray(node.children) && node.children.length > 0) {
          walk(node.children, depth + 1);
        }
      });
    })(nodes, 0);

    return groups;
  }

  function renderWorkspaceParentOptions(groups) {
    const options = ['<option value="">No group</option>'];

    (groups || []).forEach(group => {
      const indent = group.depth > 0 ? `${'--'.repeat(group.depth)} ` : '';
      options.push(
        `<option value="${escapeHtml(group.id)}">${escapeHtml(indent + group.name)}</option>`
      );
    });

    return options.join('');
  }

  // Returns values instead of writing them: sessions.js owns the control and
  // combines this with the blueprint's placement, so nothing here can override
  // that decision. `help` is the caption to show instead of the owner's own, or
  // '' when there are groups to choose from.
  function workspaceParentSelectState(groupCount) {
    const hasGroups = groupCount > 0;
    // A native <select> exposes its disabled state to assistive tech
    // automatically, so `disabled` is enough (no redundant aria-disabled).
    return {
      disabled: !hasGroups,
      help: hasGroups
        ? ''
        : 'No groups yet. Select workspaces in the launcher and click Group to create one.'
    };
  }

  window.WorkspaceGroupOptions = {
    collectWorkspaceGroupOptions,
    renderWorkspaceParentOptions,
    workspaceParentSelectState
  };
})();
