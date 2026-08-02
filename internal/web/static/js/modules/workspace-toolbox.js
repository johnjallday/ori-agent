// workspace-toolbox.js — the Workshop: named, versioned Toolboxes for one
// workspace agent instance.
//
// This replaces the old Loadout chip editor, which toggled individual workspace
// bindings one request at a time. The difference that matters is not visual: a
// Toolbox is a SAVED, VERSIONED recipe, so a selection can be named, compared,
// reused, and reasoned about, and a capability added to the workspace later
// does not silently join it.
//
// The module is deliberately a read-then-write client of server-owned state:
//
//   GET  /toolboxes                       the saved recipes
//   GET  /toolbox-workshop                what may go in one, grouped by source
//   POST /toolboxes                       create (empty | current | duplicate)
//   POST /toolboxes/{id}/versions         save an edit as a new version
//   PUT  /toolboxes/{id}                  rename / recolor (no new version)
//   POST /toolboxes/{id}/status           archive / reactivate
//   GET  /toolboxes/{id}/compare          exact version diff
//
// It never installs, connects, enables, trusts, or classifies a capability.
// Adding something the workspace has not approved records a REQUIREMENT and
// links out to the existing capability catalog, which is where those decisions
// actually belong (PRD FR-45, FR-46).
(function () {
  'use strict';

  const HOST_ID = 'workspace-toolbox-panel';

  const state = {
    workspaceId: '',
    agentInstanceId: '',
    toolboxes: [],
    workshop: null,
    workspaceVersion: 0,
    // draft holds the in-progress edit: { toolboxId, version, skills[], bindings[] }.
    // Null means "showing the saved state", which is why an unsaved edit can
    // never be mistaken for what the agent is actually using.
    draft: null,
    compare: null,
    loading: false,
    error: '',
    notice: '',
    busy: '',
    expanded: new Set()
  };

  function wsId() {
    if (state.workspaceId) return state.workspaceId;
    // The URL is authoritative and available immediately; window.currentWorkspaceId
    // is set by a module script that runs after deferred ones, so relying on it
    // alone leaves this module idle on exactly the load that matters — the first.
    const match =
      typeof window !== 'undefined' &&
      /^\/workspaces\/([^/?#]+)/.exec((window.location && window.location.pathname) || '');
    if (match) {
      state.workspaceId = decodeURIComponent(match[1]);
      return state.workspaceId;
    }
    return (typeof window !== 'undefined' && window.currentWorkspaceId) || '';
  }

  function el(tag, opts = {}, children = []) {
    const node = document.createElement(tag);
    if (opts.className) node.className = opts.className;
    if (opts.text !== undefined) node.textContent = opts.text;
    if (opts.attrs) {
      for (const [key, value] of Object.entries(opts.attrs)) {
        if (value !== undefined && value !== null) node.setAttribute(key, String(value));
      }
    }
    if (opts.disabled) node.disabled = true;
    for (const child of children) {
      if (child) node.appendChild(child);
    }
    return node;
  }

  function api(path, options) {
    const id = wsId();
    if (!id) return Promise.reject(new Error('Workspace is unavailable.'));
    return fetch('/api/workspaces/' + encodeURIComponent(id) + path, {
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      ...options
    });
  }

  async function readJSON(response) {
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error((payload && payload.message) || 'That did not work.');
    }
    return payload;
  }

  // ---------------------------------------------------------------- loading

  async function load({ agentInstanceId } = {}) {
    if (agentInstanceId !== undefined) state.agentInstanceId = String(agentInstanceId || '');
    if (!wsId()) return;

    state.loading = true;
    state.error = '';
    render();
    try {
      const listResponse = await api('/toolboxes');
      const listPayload = await readJSON(listResponse);
      state.toolboxes = Array.isArray(listPayload.toolboxes) ? listPayload.toolboxes : [];
      state.workspaceVersion = Number(listPayload.workspace_version || 0);

      let query = '';
      if (state.agentInstanceId) {
        query = '?agent_instance_id=' + encodeURIComponent(state.agentInstanceId);
      }
      if (state.draft && state.draft.toolboxId) {
        query += (query ? '&' : '?') + 'toolbox_id=' + encodeURIComponent(state.draft.toolboxId);
      }
      const workshopPayload = await readJSON(await api('/toolbox-workshop' + query));
      state.workshop = workshopPayload.workshop || null;
      state.workspaceVersion = Number(workshopPayload.workspace_version || state.workspaceVersion);
    } catch (error) {
      state.error = error?.message || 'The Workshop could not be loaded.';
      state.workshop = null;
    } finally {
      state.loading = false;
      render();
    }
  }

  // ------------------------------------------------------------------ draft

  // The draft is seeded from the SERVER's inventory rather than from whatever
  // the previous render happened to show, so an edit always starts from what is
  // actually saved.
  function beginEdit(toolboxId) {
    const workshop = state.workshop || {};
    const skills = [];
    const bindings = [];
    for (const item of allSelectableItems()) {
      if (!item.selected) continue;
      if (item.kind === 'skill' && item.source !== 'core') {
        skills.push({
          capability_id: item.capability_id,
          display_name: item.display_name,
          source: item.source,
          binding_id: item.binding_id || '',
          owner_capability_id: item.owner_capability_id || '',
          required: item.required !== false
        });
      } else if (item.kind === 'mcp' && item.source !== 'core') {
        bindings.push({
          binding_id: item.binding_id,
          allowed_tools: Array.isArray(item.selected_tools) ? item.selected_tools.slice() : [],
          owner_capability_id: item.owner_capability_id || '',
          required: item.required !== false
        });
      }
    }
    // Requirements are unmet entries the saved recipe still names. Carrying them
    // into the draft is what preserves the recipe through a repair (FR-14).
    for (const requirement of workshop.requirements || []) {
      if (requirement.kind === 'skill') {
        skills.push({
          capability_id: requirement.capability_id,
          display_name: requirement.display_name,
          source: requirement.source,
          binding_id: requirement.binding_id || '',
          required: requirement.required !== false
        });
      } else {
        bindings.push({
          binding_id: requirement.binding_id,
          allowed_tools: Array.isArray(requirement.selected_tools)
            ? requirement.selected_tools.slice()
            : [],
          required: requirement.required !== false
        });
      }
    }

    state.draft = {
      toolboxId: toolboxId || workshop.toolbox_id || '',
      version: Number(workshop.toolbox_version || 0),
      skills,
      bindings
    };
    state.notice = '';
    render();
  }

  function cancelEdit() {
    state.draft = null;
    state.notice = '';
    render();
  }

  function allSelectableItems() {
    const workshop = state.workshop || {};
    return []
      .concat(workshop.core || [])
      .concat(workshop.agent_learned || [])
      .concat(workshop.workspace_provided || []);
  }

  function draftHasSkill(capabilityId, source, bindingId) {
    if (!state.draft) return false;
    return state.draft.skills.some(
      entry =>
        entry.capability_id === capabilityId &&
        entry.source === source &&
        String(entry.binding_id || '') === String(bindingId || '')
    );
  }

  function draftBinding(bindingId) {
    if (!state.draft) return null;
    return state.draft.bindings.find(entry => entry.binding_id === bindingId) || null;
  }

  function toggleSkill(item) {
    if (!state.draft) return;
    const bindingId = item.binding_id || '';
    const index = state.draft.skills.findIndex(
      entry =>
        entry.capability_id === item.capability_id &&
        entry.source === item.source &&
        String(entry.binding_id || '') === String(bindingId)
    );
    if (index >= 0) {
      state.draft.skills.splice(index, 1);
    } else {
      state.draft.skills.push({
        capability_id: item.capability_id,
        display_name: item.display_name,
        source: item.source,
        binding_id: bindingId,
        owner_capability_id: item.owner_capability_id || '',
        required: true
      });
    }
    state.notice = '';
    render();
  }

  function toggleBinding(item) {
    if (!state.draft) return;
    const index = state.draft.bindings.findIndex(entry => entry.binding_id === item.binding_id);
    if (index >= 0) {
      state.draft.bindings.splice(index, 1);
    } else {
      // A binding that permits everything gets an empty explicit selection, not
      // a wildcard: FR-13 requires a concrete subset, and the operation
      // checkboxes below are how the user fills it in.
      state.draft.bindings.push({
        binding_id: item.binding_id,
        allowed_tools: Array.isArray(item.exposed_tools) ? item.exposed_tools.slice() : [],
        owner_capability_id: item.owner_capability_id || '',
        required: true
      });
    }
    state.notice = '';
    render();
  }

  function toggleOperation(bindingId, tool) {
    const entry = draftBinding(bindingId);
    if (!entry) return;
    const index = entry.allowed_tools.indexOf(tool);
    if (index >= 0) entry.allowed_tools.splice(index, 1);
    else entry.allowed_tools.push(tool);
    state.notice = '';
    render();
  }

  // FR-46: the ONLY way an unapproved capability enters a Toolbox. It becomes a
  // required placeholder and the draft stays inert — nothing is installed,
  // connected, enabled, trusted, or granted by this click.
  function addRequirement(item) {
    if (!state.draft) return;
    if (item.kind === 'skill') {
      if (draftHasSkill(item.capability_id, 'workspace_provided', '')) return;
      state.draft.skills.push({
        capability_id: item.capability_id,
        display_name: item.display_name,
        source: 'workspace_provided',
        binding_id: '',
        required: true
      });
    } else {
      if (draftBinding(item.capability_id)) return;
      state.draft.bindings.push({
        binding_id: item.capability_id,
        allowed_tools: [],
        required: true
      });
    }
    state.notice =
      'Added as a requirement. This toolbox stays "Missing capability" until you set it up.';
    render();
  }

  // ----------------------------------------------------------------- writes

  async function saveDraft() {
    if (!state.draft) return;
    // A workspace-provided skill with no binding is an unmet requirement, and
    // the server rejects it as invalid content. Requirements are tracked in the
    // draft for display but are not sent as content until they resolve.
    const skills = state.draft.skills.filter(
      entry => entry.source !== 'workspace_provided' || entry.binding_id
    );
    const bindings = state.draft.bindings.filter(entry => entry.binding_id);

    state.busy = 'save';
    state.error = '';
    render();
    try {
      if (state.draft.toolboxId) {
        await readJSON(
          await api('/toolboxes/' + encodeURIComponent(state.draft.toolboxId) + '/versions', {
            method: 'POST',
            body: JSON.stringify({
              skills,
              mcp_bindings: bindings,
              expected_version: state.draft.version || undefined,
              expected_workspace_version: state.workspaceVersion || undefined
            })
          })
        );
        state.notice = 'Saved as a new version.';
      } else {
        const name = promptForName('Name this toolbox');
        if (!name) return;
        await readJSON(
          await api('/toolboxes', {
            method: 'POST',
            body: JSON.stringify({
              name,
              from: 'explicit',
              skills,
              mcp_bindings: bindings,
              expected_workspace_version: state.workspaceVersion || undefined
            })
          })
        );
        state.notice = 'Toolbox created.';
      }
      state.draft = null;
      await load();
    } catch (error) {
      state.error = error?.message || 'The toolbox could not be saved.';
    } finally {
      state.busy = '';
      render();
    }
  }

  async function createToolbox(from, options = {}) {
    const name = promptForName(
      from === 'duplicate' ? 'Name for the duplicate' : 'Name this toolbox'
    );
    if (!name) return;

    state.busy = 'create';
    state.error = '';
    render();
    try {
      await readJSON(
        await api('/toolboxes', {
          method: 'POST',
          body: JSON.stringify({
            name,
            from,
            agent_instance_id: state.agentInstanceId || undefined,
            source_toolbox_id: options.sourceToolboxId,
            source_version: options.sourceVersion,
            expected_workspace_version: state.workspaceVersion || undefined
          })
        })
      );
      state.notice = 'Toolbox created.';
      await load();
    } catch (error) {
      state.error = error?.message || 'The toolbox could not be created.';
    } finally {
      state.busy = '';
      render();
    }
  }

  async function renameToolbox(toolboxId, currentName) {
    const name = promptForName('Rename toolbox', currentName);
    if (!name || name === currentName) return;

    state.busy = 'rename:' + toolboxId;
    state.error = '';
    render();
    try {
      await readJSON(
        await api('/toolboxes/' + encodeURIComponent(toolboxId), {
          method: 'PUT',
          body: JSON.stringify({
            name,
            expected_workspace_version: state.workspaceVersion || undefined
          })
        })
      );
      state.notice = 'Renamed.';
      await load();
    } catch (error) {
      state.error = error?.message || 'The toolbox could not be renamed.';
    } finally {
      state.busy = '';
      render();
    }
  }

  async function setStatus(toolboxId, status) {
    state.busy = 'status:' + toolboxId;
    state.error = '';
    render();
    try {
      await readJSON(
        await api('/toolboxes/' + encodeURIComponent(toolboxId) + '/status', {
          method: 'POST',
          body: JSON.stringify({
            status,
            expected_workspace_version: state.workspaceVersion || undefined
          })
        })
      );
      state.notice = status === 'archived' ? 'Toolbox archived.' : 'Toolbox restored.';
      await load();
    } catch (error) {
      state.error = error?.message || 'The toolbox status could not be changed.';
    } finally {
      state.busy = '';
      render();
    }
  }

  async function deleteToolbox(toolboxId) {
    state.busy = 'delete:' + toolboxId;
    state.error = '';
    render();
    try {
      await readJSON(
        await api('/toolboxes/' + encodeURIComponent(toolboxId), { method: 'DELETE' })
      );
      state.notice = 'Toolbox deleted.';
      await load();
    } catch (error) {
      state.error = error?.message || 'The toolbox could not be deleted.';
    } finally {
      state.busy = '';
      render();
    }
  }

  async function compareVersions(toolboxId, from, to) {
    state.busy = 'compare:' + toolboxId;
    state.error = '';
    render();
    try {
      const payload = await readJSON(
        await api(
          '/toolboxes/' +
            encodeURIComponent(toolboxId) +
            '/compare?from=' +
            encodeURIComponent(from) +
            '&to=' +
            encodeURIComponent(to)
        )
      );
      state.compare = payload;
    } catch (error) {
      state.error = error?.message || 'The versions could not be compared.';
      state.compare = null;
    } finally {
      state.busy = '';
      render();
    }
  }

  function promptForName(message, initial) {
    if (typeof window === 'undefined' || typeof window.prompt !== 'function') return '';
    const answer = window.prompt(message, initial || '');
    return answer === null ? '' : String(answer).trim();
  }

  // --------------------------------------------------------------- rendering

  function header() {
    const workshop = state.workshop || {};
    const capacity = workshop.capacity || {};
    const current = state.toolboxes.find(item => item.id === workshop.toolbox_id);

    const title = el('div', { className: 'ws-toolbox-title' }, [
      el('h3', { text: current ? current.name : 'No toolbox yet' }),
      workshop.toolbox_version
        ? el('span', { className: 'ws-toolbox-version', text: 'v' + workshop.toolbox_version })
        : null,
      current && current.status === 'archived'
        ? el('span', { className: 'ws-toolbox-chip is-archived', text: 'Archived' })
        : null
    ]);

    // Separate readouts rather than one opaque number (FR-71). Focus itself
    // arrives with the preview work; what is knowable here is capacity and the
    // exposed operation count.
    const facts = el('ul', { className: 'ws-toolbox-facts' }, [
      el('li', {
        text:
          capacity.capacity > 0
            ? capacity.used + ' / ' + capacity.capacity + ' skill spaces'
            : (capacity.used || 0) + ' active skills'
      }),
      el('li', { text: countOperations() + ' exposed operations' }),
      capacity.expert_mode
        ? el('li', { className: 'ws-toolbox-fact-note', text: 'Expert mode: no skill-space limit' })
        : null,
      capacity.full && !capacity.expert_mode
        ? el('li', {
            className: 'ws-toolbox-fact-warn',
            text: capacity.grandfathered
              ? 'Toolbox full — kept from before the limit. You can remove or swap, but not add.'
              : 'Toolbox full — remove a skill to add another.'
          })
        : null
    ]);

    return el('header', { className: 'ws-toolbox-header' }, [title, facts]);
  }

  function countOperations() {
    const workshop = state.workshop || {};
    let total = 0;
    for (const item of workshop.workspace_provided || []) {
      if (item.kind !== 'mcp' || !item.selected) continue;
      total += (item.selected_tools || []).length;
    }
    return total;
  }

  function picker() {
    const rows = state.toolboxes.map(toolbox => {
      const busyPrefixes = ['rename:', 'status:', 'delete:', 'compare:'];
      const busy = busyPrefixes.some(prefix => state.busy === prefix + toolbox.id);
      const meta =
        'v' +
        toolbox.version +
        ' · ' +
        toolbox.skill_count +
        ' skills · ' +
        (toolbox.operation_count < 0
          ? 'operations not pinned yet'
          : toolbox.operation_count + ' operations');

      const actions = el('div', { className: 'ws-toolbox-row-actions' }, [
        el('button', {
          className: 'modern-btn modern-btn-secondary modern-btn-sm',
          text: 'Duplicate',
          attrs: { type: 'button', 'data-toolbox-duplicate': toolbox.id },
          disabled: busy
        }),
        el('button', {
          className: 'modern-btn modern-btn-secondary modern-btn-sm',
          text: 'Rename',
          attrs: {
            type: 'button',
            'data-toolbox-rename': toolbox.id,
            'data-toolbox-name': toolbox.name
          },
          disabled: busy
        }),
        toolbox.version > 1
          ? el('button', {
              className: 'modern-btn modern-btn-secondary modern-btn-sm',
              text: 'Compare',
              attrs: {
                type: 'button',
                'data-toolbox-compare': toolbox.id,
                'data-toolbox-version': toolbox.version
              },
              disabled: busy
            })
          : null,
        el('button', {
          className: 'modern-btn modern-btn-secondary modern-btn-sm',
          text: toolbox.status === 'archived' ? 'Restore' : 'Archive',
          attrs: {
            type: 'button',
            'data-toolbox-status': toolbox.id,
            'data-toolbox-next-status': toolbox.status === 'archived' ? 'active' : 'archived'
          },
          disabled: busy
        }),
        // Delete is offered only when nothing references the toolbox. The
        // server refuses regardless and explains why; hiding the button when we
        // already know keeps the refusal from being the first feedback (FR-21).
        (toolbox.assigned_instance_ids || []).length === 0
          ? el('button', {
              className: 'modern-btn modern-btn-danger modern-btn-sm',
              text: 'Delete',
              attrs: { type: 'button', 'data-toolbox-delete': toolbox.id },
              disabled: busy
            })
          : null
      ]);

      return el(
        'li',
        {
          className:
            'ws-toolbox-row' +
            (toolbox.id === (state.workshop || {}).toolbox_id ? ' is-current' : '') +
            (toolbox.status === 'archived' ? ' is-archived' : ''),
          attrs: { 'data-toolbox-id': toolbox.id }
        },
        [
          el('div', { className: 'ws-toolbox-row-main' }, [
            el('span', { className: 'ws-toolbox-row-name', text: toolbox.name }),
            el('span', { className: 'ws-toolbox-row-meta', text: meta }),
            toolbox.description
              ? el('span', { className: 'ws-toolbox-row-desc', text: toolbox.description })
              : null
          ]),
          actions
        ]
      );
    });

    const createRow = el('div', { className: 'ws-toolbox-create' }, [
      el('button', {
        className: 'modern-btn modern-btn-primary modern-btn-sm',
        text: 'Save current as new toolbox',
        attrs: { type: 'button', 'data-toolbox-create': 'current' },
        disabled: !((state.workshop || {}).toolbox_id && state.busy === '')
      }),
      el('button', {
        className: 'modern-btn modern-btn-secondary modern-btn-sm',
        text: 'Start an empty toolbox',
        attrs: { type: 'button', 'data-toolbox-create': 'empty' },
        disabled: state.busy !== ''
      })
    ]);

    return el('section', { className: 'ws-toolbox-picker' }, [
      el('h4', { text: 'Saved toolboxes' }),
      rows.length
        ? el('ul', { className: 'ws-toolbox-list' }, rows)
        : el('p', {
            className: 'ws-toolbox-empty',
            text: 'No saved toolboxes yet. Save the current selection to start one.'
          }),
      createRow
    ]);
  }

  function skillCard(item) {
    const editing = !!state.draft;
    const selected = editing
      ? draftHasSkill(item.capability_id, item.source, item.binding_id)
      : !!item.selected;
    const expandKey = 'skill:' + item.source + ':' + item.capability_id;
    const isExpanded = state.expanded.has(expandKey);

    const children = [
      el('div', { className: 'ws-toolbox-card-head' }, [
        item.locked
          ? el('span', { className: 'ws-toolbox-lock', text: 'Always on' })
          : el('button', {
              className:
                'ws-toolbox-select' +
                (selected ? ' is-on' : ' is-off') +
                (editing ? '' : ' is-readonly'),
              text: selected ? 'Selected' : 'Add',
              attrs: {
                type: 'button',
                role: 'switch',
                'aria-checked': selected ? 'true' : 'false',
                'aria-label': (selected ? 'Remove ' : 'Add ') + item.display_name,
                'data-toolbox-toggle-skill': item.capability_id,
                'data-toolbox-source': item.source,
                'data-toolbox-binding': item.binding_id || ''
              },
              disabled: !editing || !item.available
            }),
        el('span', { className: 'ws-toolbox-card-name', text: item.display_name }),
        item.consumes_skill_space
          ? el('span', { className: 'ws-toolbox-chip', text: '1 skill space' })
          : el('span', { className: 'ws-toolbox-chip is-free', text: 'No skill space' })
      ])
    ];

    // FR-49: plain language about what it adds, with the real prompt behind an
    // expander so the summary can be checked rather than trusted.
    if (item.summary) {
      children.push(el('p', { className: 'ws-toolbox-card-summary', text: item.summary }));
    }
    if (!item.available && item.unavailable_reason) {
      children.push(el('p', { className: 'ws-toolbox-card-warn', text: item.unavailable_reason }));
    }
    if (item.prompt || item.config) {
      children.push(
        el('button', {
          className: 'ws-toolbox-expand',
          text: isExpanded ? 'Hide details' : 'Show what it instructs',
          attrs: {
            type: 'button',
            'aria-expanded': isExpanded ? 'true' : 'false',
            'data-toolbox-expand': expandKey
          }
        })
      );
      if (isExpanded) {
        const detail = [];
        if (item.prompt) {
          detail.push(el('pre', { className: 'ws-toolbox-prompt', text: item.prompt }));
        }
        if (item.skill_allowed_tools && item.skill_allowed_tools.length) {
          detail.push(
            el('p', {
              className: 'ws-toolbox-card-policy',
              text: 'Tool policy — allowed: ' + item.skill_allowed_tools.join(', ')
            })
          );
        }
        if (item.skill_disallowed_tools && item.skill_disallowed_tools.length) {
          detail.push(
            el('p', {
              className: 'ws-toolbox-card-policy',
              text: 'Tool policy — blocked: ' + item.skill_disallowed_tools.join(', ')
            })
          );
        }
        if (item.config) {
          detail.push(
            el('pre', {
              className: 'ws-toolbox-config',
              text: JSON.stringify(item.config, null, 2)
            })
          );
        }
        children.push(el('div', { className: 'ws-toolbox-card-detail' }, detail));
      }
    }

    return el(
      'article',
      {
        className:
          'ws-toolbox-card is-skill' +
          (selected ? ' is-selected' : '') +
          (item.locked ? ' is-locked' : '') +
          (item.available ? '' : ' is-unavailable'),
        attrs: { 'data-toolbox-item': item.capability_id }
      },
      children
    );
  }

  function mcpCard(item) {
    const editing = !!state.draft;
    const draftEntry = editing ? draftBinding(item.binding_id) : null;
    const selected = editing ? !!draftEntry : !!item.selected;
    const selectedTools = draftEntry
      ? draftEntry.allowed_tools
      : Array.isArray(item.selected_tools)
        ? item.selected_tools
        : [];

    const children = [
      el('div', { className: 'ws-toolbox-card-head' }, [
        item.locked
          ? el('span', { className: 'ws-toolbox-lock', text: 'Always on' })
          : el('button', {
              className:
                'ws-toolbox-select' +
                (selected ? ' is-on' : ' is-off') +
                (editing ? '' : ' is-readonly'),
              text: selected ? 'Selected' : 'Add',
              attrs: {
                type: 'button',
                role: 'switch',
                'aria-checked': selected ? 'true' : 'false',
                'aria-label': (selected ? 'Remove ' : 'Add ') + item.display_name,
                'data-toolbox-toggle-mcp': item.binding_id
              },
              disabled: !editing || !item.available
            }),
        el('span', { className: 'ws-toolbox-card-name', text: item.display_name }),
        el('span', {
          className: 'ws-toolbox-chip ' + (item.connected ? 'is-connected' : 'is-disconnected'),
          text: item.connected ? 'Connected' : 'Not connected'
        })
      ])
    ];

    // FR-50: connection, scope, exposed operations, risk class, and anything
    // still unclassified — all of it text, none of it inferred.
    if (item.server_name) {
      children.push(
        el('p', { className: 'ws-toolbox-card-summary', text: 'Server: ' + item.server_name })
      );
    }
    if (item.scope && Object.keys(item.scope).length) {
      children.push(
        el('p', {
          className: 'ws-toolbox-card-scope',
          text: 'Scope: ' + summarizeScope(item.scope)
        })
      );
    }
    if (item.default_side_effect) {
      children.push(
        el('p', {
          className: 'ws-toolbox-card-risk',
          text: 'Default effect: ' + item.default_side_effect
        })
      );
    }
    if (item.unclassified_tools && item.unclassified_tools.length) {
      children.push(
        el('p', {
          className: 'ws-toolbox-card-warn',
          text:
            'Unclassified operations (blocked until classified): ' +
            item.unclassified_tools.join(', ')
        })
      );
    }
    if (!item.available && item.unavailable_reason) {
      children.push(el('p', { className: 'ws-toolbox-card-warn', text: item.unavailable_reason }));
    }

    if (item.exposes_all_tools) {
      children.push(
        el('p', {
          className: 'ws-toolbox-card-warn',
          text: 'This connection allows every operation. Ori needs an explicit list before it can be saved in a toolbox.'
        })
      );
    } else if (item.exposed_tools && item.exposed_tools.length) {
      const operations = item.exposed_tools.map(tool => {
        const on = selectedTools.includes(tool);
        const risk = (item.tool_risks || {})[tool] || item.default_side_effect || 'unclassified';
        return el('li', {}, [
          el('button', {
            className: 'ws-toolbox-op' + (on ? ' is-on' : ' is-off'),
            text: tool,
            attrs: {
              type: 'button',
              role: 'switch',
              'aria-checked': on ? 'true' : 'false',
              'aria-label': (on ? 'Remove operation ' : 'Add operation ') + tool,
              'data-toolbox-toggle-op': tool,
              'data-toolbox-binding': item.binding_id
            },
            disabled: !editing || !selected
          }),
          el('span', { className: 'ws-toolbox-op-risk', text: risk })
        ]);
      });
      children.push(
        el(
          'ul',
          { className: 'ws-toolbox-ops', attrs: { 'aria-label': 'Exposed operations' } },
          operations
        )
      );
    }

    return el(
      'article',
      {
        className:
          'ws-toolbox-card is-mcp' +
          (selected ? ' is-selected' : '') +
          (item.locked ? ' is-locked' : '') +
          (item.available ? '' : ' is-unavailable'),
        attrs: { 'data-toolbox-item': item.binding_id }
      },
      children
    );
  }

  function summarizeScope(scope) {
    const parts = [];
    for (const [key, value] of Object.entries(scope || {})) {
      parts.push(key + ': ' + (Array.isArray(value) ? value.join(', ') : String(value)));
    }
    return parts.join(' · ');
  }

  function librarySection(items) {
    if (!items || !items.length) return null;
    return el('section', { className: 'ws-toolbox-section is-library' }, [
      el('h4', { text: 'Elsewhere in Ori' }),
      el('p', {
        className: 'ws-toolbox-section-note',
        text: 'Ori knows about these, but this workspace has not set them up. Adding one records a requirement — it does not install or connect anything.'
      }),
      el(
        'div',
        { className: 'ws-toolbox-cards' },
        items.map(item =>
          el('article', { className: 'ws-toolbox-card is-library' }, [
            el('div', { className: 'ws-toolbox-card-head' }, [
              el('span', { className: 'ws-toolbox-card-name', text: item.display_name }),
              el('span', { className: 'ws-toolbox-chip is-unavailable', text: 'Not set up here' })
            ]),
            item.summary
              ? el('p', { className: 'ws-toolbox-card-summary', text: item.summary })
              : null,
            el('p', { className: 'ws-toolbox-card-warn', text: item.unavailable_reason || '' }),
            el('div', { className: 'ws-toolbox-card-actions' }, [
              el('button', {
                className: 'modern-btn modern-btn-secondary modern-btn-sm',
                text: 'Add requirement',
                attrs: {
                  type: 'button',
                  'data-toolbox-add-requirement': item.capability_id,
                  'data-toolbox-kind': item.kind
                },
                disabled: !state.draft
              }),
              el('a', {
                className: 'ws-toolbox-setup-link',
                text: 'Open capability setup',
                attrs: { href: '#workspace-capabilities-list' }
              })
            ])
          ])
        )
      )
    ]);
  }

  function section(title, items, note) {
    if (!items || !items.length) return null;
    return el('section', { className: 'ws-toolbox-section' }, [
      el('h4', { text: title }),
      note ? el('p', { className: 'ws-toolbox-section-note', text: note }) : null,
      el(
        'div',
        { className: 'ws-toolbox-cards' },
        items.map(item => (item.kind === 'mcp' ? mcpCard(item) : skillCard(item)))
      )
    ]);
  }

  function requirementsSection(items) {
    if (!items || !items.length) return null;
    return el('section', { className: 'ws-toolbox-section is-requirements' }, [
      el('h4', { text: 'Missing capability' }),
      el('p', {
        className: 'ws-toolbox-section-note',
        text: 'This toolbox names these, but they are not available here. The recipe is kept as saved.'
      }),
      el(
        'div',
        { className: 'ws-toolbox-cards' },
        items.map(item =>
          el('article', { className: 'ws-toolbox-card is-requirement' }, [
            el('div', { className: 'ws-toolbox-card-head' }, [
              el('span', { className: 'ws-toolbox-card-name', text: item.display_name }),
              el('span', {
                className: 'ws-toolbox-chip is-unavailable',
                text: item.required ? 'Required' : 'Optional'
              })
            ]),
            el('p', { className: 'ws-toolbox-card-warn', text: item.unavailable_reason || '' })
          ])
        )
      )
    ]);
  }

  function editorActions() {
    if (state.draft) {
      return el('div', { className: 'ws-toolbox-actions' }, [
        el('button', {
          className: 'modern-btn modern-btn-primary modern-btn-sm',
          text: state.busy === 'save' ? 'Saving…' : 'Save as new version',
          attrs: { type: 'button', 'data-toolbox-save': 'true' },
          disabled: state.busy === 'save'
        }),
        el('button', {
          className: 'modern-btn modern-btn-secondary modern-btn-sm',
          text: 'Cancel',
          attrs: { type: 'button', 'data-toolbox-cancel': 'true' },
          disabled: state.busy === 'save'
        })
      ]);
    }
    return el('div', { className: 'ws-toolbox-actions' }, [
      el('button', {
        className: 'modern-btn modern-btn-secondary modern-btn-sm',
        text: 'Edit this toolbox',
        attrs: { type: 'button', 'data-toolbox-edit': 'true' },
        disabled: !(state.workshop || {}).toolbox_id
      })
    ]);
  }

  function comparePanel() {
    if (!state.compare) return null;
    const diff = state.compare.diff || {};
    const line = (label, entries, describe) => {
      if (!entries || !entries.length) return null;
      return el('li', {
        className: 'ws-toolbox-diff-line',
        text: label + ': ' + entries.map(describe).join(', ')
      });
    };

    return el('section', { className: 'ws-toolbox-compare' }, [
      el('h4', {
        text:
          'Comparing v' +
          (state.compare.from || {}).version +
          ' → v' +
          (state.compare.to || {}).version
      }),
      state.compare.identical
        ? el('p', { className: 'ws-toolbox-section-note', text: 'These versions are identical.' })
        : el('ul', { className: 'ws-toolbox-diff' }, [
            line(
              'Skills added',
              diff.skills_added,
              entry => entry.display_name || entry.capability_id
            ),
            line(
              'Skills removed',
              diff.skills_removed,
              entry => entry.display_name || entry.capability_id
            ),
            line(
              'Skills changed',
              diff.skills_changed,
              entry =>
                (entry.display_name || entry.capability_id) + ' (' + entry.fields.join(', ') + ')'
            ),
            line('Connections added', diff.bindings_added, entry => entry.binding_id),
            line('Connections removed', diff.bindings_removed, entry => entry.binding_id),
            line(
              'Operations changed',
              diff.bindings_changed,
              entry =>
                entry.binding_id +
                (entry.added_tools?.length ? ' +' + entry.added_tools.join('/') : '') +
                (entry.removed_tools?.length ? ' −' + entry.removed_tools.join('/') : '')
            ),
            el('li', {
              className: 'ws-toolbox-diff-line',
              text: 'Skill spaces: ' + diff.skill_spaces_before + ' → ' + diff.skill_spaces_after
            }),
            state.compare.expands_operations
              ? el('li', {
                  className: 'ws-toolbox-diff-line is-warn',
                  text: 'This version exposes operations the earlier one did not.'
                })
              : null
          ]),
      el('button', {
        className: 'modern-btn modern-btn-secondary modern-btn-sm',
        text: 'Close comparison',
        attrs: { type: 'button', 'data-toolbox-close-compare': 'true' }
      })
    ]);
  }

  function render() {
    const host = hostNode();
    if (!host) return;
    host.innerHTML = '';

    if (state.loading && !state.workshop) {
      host.appendChild(el('p', { className: 'ws-toolbox-empty', text: 'Loading the Workshop…' }));
      return;
    }
    if (state.error) {
      const alert = el('p', { className: 'ws-toolbox-error', text: state.error });
      alert.setAttribute('role', 'alert');
      host.appendChild(alert);
    }
    if (state.notice) {
      const notice = el('p', { className: 'ws-toolbox-notice', text: state.notice });
      notice.setAttribute('role', 'status');
      host.appendChild(notice);
    }
    if (!state.workshop) {
      host.appendChild(
        el('p', {
          className: 'ws-toolbox-empty',
          text: 'Pick an agent to see its toolbox.'
        })
      );
      return;
    }

    const workshop = state.workshop;
    const coreOnly =
      !(workshop.agent_learned || []).some(item => item.selected) &&
      !(workshop.workspace_provided || []).some(item => item.selected) &&
      !(workshop.requirements || []).length;

    host.appendChild(header());
    host.appendChild(picker());
    host.appendChild(editorActions());
    if (state.compare) host.appendChild(comparePanel());

    if (workshop.collisions && workshop.collisions.length) {
      const collision = el('p', {
        className: 'ws-toolbox-error',
        text:
          'These skills come from two places and need one picked: ' +
          workshop.collisions.map(entry => entry.capability_id).join(', ')
      });
      collision.setAttribute('role', 'alert');
      host.appendChild(collision);
    }

    const core = section(
      'Core',
      workshop.core,
      'Always available in this workspace. These use no skill spaces.'
    );
    if (core) host.appendChild(core);

    if (coreOnly && !state.draft) {
      host.appendChild(
        el('p', {
          className: 'ws-toolbox-empty',
          text: 'Core only — this agent has nothing else switched on yet.'
        })
      );
    }

    const requirements = requirementsSection(workshop.requirements);
    if (requirements) host.appendChild(requirements);

    const learned = section(
      'From this agent',
      workshop.agent_learned,
      'Skills this agent has learned. Knowing one does not make it active.'
    );
    if (learned) host.appendChild(learned);

    const provided = section(
      'From this workspace',
      workshop.workspace_provided,
      'Approved in this workspace and ready to use.'
    );
    if (provided) host.appendChild(provided);

    const library = librarySection(workshop.global_library);
    if (library) host.appendChild(library);
  }

  function hostNode() {
    if (typeof document === 'undefined' || typeof document.getElementById !== 'function') {
      return null;
    }
    return document.getElementById(HOST_ID);
  }

  // ----------------------------------------------------------------- events

  function findItem(predicate) {
    const workshop = state.workshop || {};
    const groups = [
      workshop.core,
      workshop.agent_learned,
      workshop.workspace_provided,
      workshop.global_library
    ];
    for (const group of groups) {
      const found = (group || []).find(predicate);
      if (found) return found;
    }
    return null;
  }

  function bindHost(host) {
    if (!host || host.dataset?.toolboxBound === 'true') return;
    if (host.dataset) host.dataset.toolboxBound = 'true';
    host.addEventListener('click', event => {
      const target = event.target && event.target.closest ? event.target : null;
      if (!target) return;

      const handlers = [
        ['[data-toolbox-edit]', () => beginEdit()],
        ['[data-toolbox-cancel]', () => cancelEdit()],
        ['[data-toolbox-save]', () => void saveDraft()],
        [
          '[data-toolbox-create]',
          node => void createToolbox(node.getAttribute('data-toolbox-create'))
        ],
        [
          '[data-toolbox-duplicate]',
          node =>
            void createToolbox('duplicate', {
              sourceToolboxId: node.getAttribute('data-toolbox-duplicate')
            })
        ],
        [
          '[data-toolbox-rename]',
          node =>
            void renameToolbox(
              node.getAttribute('data-toolbox-rename'),
              node.getAttribute('data-toolbox-name')
            )
        ],
        [
          '[data-toolbox-status]',
          node =>
            void setStatus(
              node.getAttribute('data-toolbox-status'),
              node.getAttribute('data-toolbox-next-status')
            )
        ],
        [
          '[data-toolbox-delete]',
          node => void deleteToolbox(node.getAttribute('data-toolbox-delete'))
        ],
        [
          '[data-toolbox-compare]',
          node =>
            void compareVersions(
              node.getAttribute('data-toolbox-compare'),
              Number(node.getAttribute('data-toolbox-version')) - 1,
              Number(node.getAttribute('data-toolbox-version'))
            )
        ],
        [
          '[data-toolbox-close-compare]',
          () => {
            state.compare = null;
            render();
          }
        ],
        [
          '[data-toolbox-expand]',
          node => {
            const key = node.getAttribute('data-toolbox-expand');
            if (state.expanded.has(key)) state.expanded.delete(key);
            else state.expanded.add(key);
            render();
          }
        ],
        [
          '[data-toolbox-toggle-skill]',
          node => {
            const capabilityId = node.getAttribute('data-toolbox-toggle-skill');
            const source = node.getAttribute('data-toolbox-source');
            const bindingId = node.getAttribute('data-toolbox-binding') || '';
            const item = findItem(
              entry =>
                entry.kind === 'skill' &&
                entry.capability_id === capabilityId &&
                entry.source === source &&
                String(entry.binding_id || '') === bindingId
            );
            if (item) toggleSkill(item);
          }
        ],
        [
          '[data-toolbox-toggle-mcp]',
          node => {
            const bindingId = node.getAttribute('data-toolbox-toggle-mcp');
            const item = findItem(entry => entry.kind === 'mcp' && entry.binding_id === bindingId);
            if (item) toggleBinding(item);
          }
        ],
        [
          '[data-toolbox-toggle-op]',
          node =>
            toggleOperation(
              node.getAttribute('data-toolbox-binding'),
              node.getAttribute('data-toolbox-toggle-op')
            )
        ],
        [
          '[data-toolbox-add-requirement]',
          node => {
            const capabilityId = node.getAttribute('data-toolbox-add-requirement');
            const kind = node.getAttribute('data-toolbox-kind');
            const item = findItem(
              entry => entry.capability_id === capabilityId && entry.kind === kind
            );
            if (item) addRequirement(item);
          }
        ]
      ];

      for (const [selector, handler] of handlers) {
        const node = target.closest(selector);
        if (node) {
          event.preventDefault();
          handler(node);
          return;
        }
      }
    });
  }

  async function init(options = {}) {
    if (!wsId()) return;
    const host = hostNode();
    if (host) bindHost(host);
    await load(options);
  }

  window.WorkspaceToolbox = {
    init,
    load,
    render,
    bindHost,
    beginEdit,
    cancelEdit,
    saveDraft,
    setAgentInstance: id => {
      state.agentInstanceId = String(id || '');
      state.draft = null;
      state.compare = null;
    },
    state: () => ({ ...state, expanded: Array.from(state.expanded) }),
    // Test hooks.
    _reset: () => {
      state.workspaceId = '';
      state.agentInstanceId = '';
      state.toolboxes = [];
      state.workshop = null;
      state.workspaceVersion = 0;
      state.draft = null;
      state.compare = null;
      state.loading = false;
      state.error = '';
      state.notice = '';
      state.busy = '';
      state.expanded = new Set();
    },
    _setWorkspace: id => {
      state.workspaceId = String(id || '');
    },
    _setData: (toolboxes, workshop) => {
      state.toolboxes = Array.isArray(toolboxes) ? toolboxes : [];
      state.workshop = workshop || null;
    },
    _draft: () => (state.draft ? JSON.parse(JSON.stringify(state.draft)) : null)
  };
})();
