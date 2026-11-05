// Where an agent's saved settings come from, in one explicit context: its
// library profile or one workspace it belongs to. Read only: this module never
// writes, never retargets an editor's Save, and renders every value as text.
// The server owns the facts (GET /api/agents/{name}/settings-ownership); the
// browser only labels them.
(function () {
  'use strict';

  const MAX_ID = 200;
  const text = value => String(value ?? '').trim();

  // ---- URL context ---------------------------------------------------------

  function parseContext(search) {
    const params = new URLSearchParams(search || '');
    const studioId = text(params.get('studio_id'));
    if (params.get('ctx') === 'workspace' && studioId && studioId.length <= MAX_ID) {
      const instanceId = text(params.get('instance_id'));
      return {
        kind: 'workspace',
        studioId,
        instanceId: instanceId.length <= MAX_ID ? instanceId : ''
      };
    }
    return { kind: 'library', studioId: '', instanceId: '' };
  }

  function contextKey(ctx) {
    return ctx.kind === 'workspace'
      ? `workspace:${ctx.studioId}:${ctx.instanceId || ''}`
      : 'library';
  }

  // writeContext returns the query string for ctx, keeping every other
  // parameter (the roster's ?tab= deep link, for one) as it was.
  function writeContext(search, ctx) {
    const params = new URLSearchParams(search || '');
    params.delete('ctx');
    params.delete('studio_id');
    params.delete('instance_id');
    if (ctx.kind === 'workspace' && ctx.studioId) {
      params.set('ctx', 'workspace');
      params.set('studio_id', ctx.studioId);
      if (ctx.instanceId) params.set('instance_id', ctx.instanceId);
    }
    const query = params.toString();
    return query ? `?${query}` : '';
  }

  function requestURL(agentName, ctx, { impact = false } = {}) {
    const base = `/api/agents/${encodeURIComponent(agentName)}/settings-ownership`;
    if (ctx.kind !== 'workspace') return impact ? `${base}?include=impact` : base;
    const params = new URLSearchParams({ studio_id: ctx.studioId });
    if (ctx.instanceId) params.set('instance_id', ctx.instanceId);
    return `${base}?${params.toString()}`;
  }

  // ---- Labels ---------------------------------------------------------------

  const SOURCE_LABELS = {
    library: 'Library profile',
    system: 'Built-in assistant (protected)',
    workspace_copy: "This workspace's own saved copy",
    library_fallback: 'Library profile, read directly (this workspace keeps no copy)',
    unknown: 'Could not be verified'
  };

  const COMPARISON_LABELS = {
    same: 'Same as library',
    different: 'Differs from library',
    not_compared: '',
    unknown: 'Comparison unknown'
  };

  const TRACKING_LABELS = {
    in_step:
      'Ori keeps this copy in step with the library profile: model and prompt edits made there are copied here.',
    pending:
      "Ori keeps this copy in step, but it does not have the library profile's latest model or prompt settings yet. Ori updates it the next time those settings are saved in Ori.",
    customized: 'This copy was changed in this workspace, so library edits no longer reach it.',
    untracked:
      'This is an independent copy: library edits do not reach it, even where the values match.',
    unknown: 'Whether Ori keeps this copy in step with the library could not be checked.',
    not_applicable: ''
  };

  const PROMPT_PREVIEW_LIMIT = 160;

  function formatValue(field) {
    if (!field || field.empty) {
      return field?.key === 'max_output_tokens' ? 'Not set (provider default)' : 'Not set';
    }
    if (field.key === 'system_prompt') {
      const value = String(field.value ?? '');
      return value.length > PROMPT_PREVIEW_LIMIT
        ? `${value.slice(0, PROMPT_PREVIEW_LIMIT)}… (full text under Saved prompt)`
        : value;
    }
    return String(field.value);
  }

  // viewModel turns a projection into the strings the section shows. It never
  // invents a value: a missing or degraded definition shows no fields.
  function viewModel(projection) {
    const ctx = projection?.context || {};
    const def = projection?.definition || {};
    const workspaceName = text(ctx.workspace?.name);
    const heading =
      ctx.kind === 'workspace'
        ? workspaceName
          ? `In workspace “${workspaceName}”`
          : 'In an unavailable workspace'
        : 'Library profile';
    const okContext = ctx.state === 'ok';
    const showFields = okContext && def.state === 'ok' && Array.isArray(def.fields);
    return {
      heading,
      contextState: ctx.state || 'unavailable',
      message:
        ['not_found', 'not_member'].includes(ctx.state) && text(ctx.message)
          ? `${text(ctx.message)} Choose another place to view from the list above.`
          : text(ctx.message),
      contextsNote: text(projection?.contexts?.note),
      definitionState: def.state || 'unavailable',
      source: okContext ? SOURCE_LABELS[def.source] || SOURCE_LABELS.unknown : '',
      note: okContext ? text(def.note) : '',
      disabled: Boolean(def.disabled),
      tracking: okContext ? TRACKING_LABELS[projection?.tracking?.state] || '' : '',
      fields: showFields
        ? def.fields.map(field => ({
            key: field.key,
            label: text(field.label) || field.key,
            value: formatValue(field),
            empty: Boolean(field.empty),
            comparison: COMPARISON_LABELS[field.comparison] ?? '',
            comparisonCode: text(field.comparison)
          }))
        : [],
      excluded: text(projection?.excluded)
    };
  }

  // contextOptions lists the selector's choices. A workspace-owned agent has
  // no Library option (none exists); an unknown workspace from the URL stays
  // selected as "unavailable" instead of silently switching elsewhere.
  function contextOptions(projection, ctx) {
    const options = [];
    if (projection?.library?.state && projection.library.state !== 'absent') {
      options.push({ value: 'library', label: 'Library profile' });
    }
    const seen = new Set();
    for (const ws of projection?.contexts?.workspaces || []) {
      const id = text(ws.id);
      if (!id || seen.has(id)) continue;
      seen.add(id);
      options.push({ value: `workspace:${id}`, label: `Workspace: ${text(ws.name) || id}` });
    }
    if (ctx.kind === 'workspace' && !seen.has(ctx.studioId)) {
      options.push({ value: `workspace:${ctx.studioId}`, label: 'Unavailable workspace' });
    }
    if (ctx.kind === 'library' && !options.some(o => o.value === 'library')) {
      options.unshift({ value: 'library', label: 'Choose a workspace' });
    }
    return options;
  }

  function contextFromOption(value) {
    if (value.startsWith('workspace:')) {
      return { kind: 'workspace', studioId: value.slice('workspace:'.length), instanceId: '' };
    }
    return { kind: 'library', studioId: '', instanceId: '' };
  }

  function instanceLabel(item) {
    const number = Number.isInteger(item.instance_number) && item.instance_number > 0;
    const parts = [number ? `Instance ${item.instance_number}` : 'Instance'];
    if (text(item.role)) parts.push(text(item.role));
    if (item.entry_point) parts.push('workspace entry');
    return parts.join(' · ');
  }

  // instanceChoice describes the instance picker. Several instances are never
  // resolved for the reader: nothing is selected until they choose one.
  function instanceChoice(projection) {
    const inst = projection?.instances || {};
    const items = Array.isArray(inst.items) ? inst.items.filter(item => text(item.id)) : [];
    const choice = {
      show: false,
      options: [],
      selected: text(inst.selected_id),
      message: ''
    };
    if (projection?.context?.state !== 'ok') return choice;
    if (inst.state === 'unidentified') {
      choice.message =
        'This workspace lists the agent without a stable instance ID, so its workspace guidance cannot be shown separately.';
      return choice;
    }
    if (inst.state === 'not_found') {
      choice.message =
        'That instance is no longer in this workspace. Choose one of the current instances.';
    } else if (inst.state === 'ambiguous') {
      choice.message =
        'This agent has several instances here, each with its own workspace guidance. Choose one to see it.';
    }
    if (items.length < 2 && inst.state !== 'not_found') return choice;
    choice.show = true;
    choice.options = [
      { value: '', label: 'Choose an instance' },
      ...items.map(item => ({ value: text(item.id), label: instanceLabel(item) }))
    ];
    return choice;
  }

  // promptView describes the saved-prompt block. Base and workspace guidance
  // are labelled separately; run-time layers are explicitly excluded.
  function promptView(projection) {
    const prompt = projection?.prompt || {};
    const view = {
      show: false,
      baseSource: '',
      base: '',
      baseEmpty: true,
      layers: [],
      preview: '',
      needsInstance: false,
      variables: Boolean(prompt.has_variables),
      note: text(prompt.note)
    };
    if (projection?.context?.state !== 'ok' || !['ok', 'needs_instance'].includes(prompt.state)) {
      return view;
    }
    view.show = true;
    view.baseSource = SOURCE_LABELS[prompt.base_source] || SOURCE_LABELS.unknown;
    view.base = String(prompt.base ?? '');
    view.baseEmpty = text(view.base) === '';
    view.layers = (Array.isArray(prompt.layers) ? prompt.layers : []).map(layer => ({
      label: text(layer.label) || text(layer.key),
      value: String(layer.value ?? '')
    }));
    view.needsInstance = prompt.state === 'needs_instance';
    view.preview = view.needsInstance ? '' : String(prompt.preview ?? '');
    return view;
  }

  // conversationRequest names the existing conversation-model reader for a
  // context, or null when no supported conversation context applies. The
  // library card itself is never used as a conversation reader.
  function conversationRequest(projection, { systemModelLinked = false } = {}) {
    const ctx = projection?.context || {};
    if (ctx.state !== 'ok' || projection?.definition?.state !== 'ok') return null;
    if (ctx.kind === 'workspace' && text(ctx.workspace?.id)) {
      return {
        label: 'Workspace chat model',
        context: { kind: 'workspace_entry', workspaceId: ctx.workspace.id, name: projection.agent }
      };
    }
    if (ctx.kind === 'library' && systemModelLinked) {
      return { label: 'Conversation model (app Default model)', context: {} };
    }
    return null;
  }

  // conversationView labels a selection from the reader. Unknown stays
  // unknown; a selection for another recipient is never shown as this agent's.
  function conversationView(request, selection, savedModel, describe) {
    const recipient = text(selection?.recipient);
    const forThisAgent =
      request.context.kind !== 'workspace_entry' ||
      !recipient ||
      recipient.toLowerCase() === text(request.context.name).toLowerCase();
    const value = forThisAgent ? describe(selection) : 'unknown';
    let note = '';
    if (forThisAgent && selection?.state === 'configured' && text(savedModel)) {
      if (text(selection.model) && text(selection.model) !== text(savedModel)) {
        note = 'This differs from the saved model above: a new conversation here uses this one.';
      }
    }
    return { label: request.label, value, note };
  }

  // ---- Toolboxes ------------------------------------------------------------

  const AVAILABILITY_LABELS = {
    available: 'Available',
    switched_off: 'Switched off in this workspace',
    missing: 'Not found',
    disabled: 'Turned off for this agent, so it does not run',
    unknown: 'Availability unknown'
  };

  const SKILL_SOURCE_LABELS = {
    agent_learned: "Agent's skill",
    workspace_provided: 'Workspace skill',
    core: 'Provided by Ori'
  };

  function skillRows(skills) {
    return (Array.isArray(skills) ? skills : []).map(skill => ({
      name: text(skill.name) || text(skill.id),
      source: SKILL_SOURCE_LABELS[skill.source] || '',
      availability: AVAILABILITY_LABELS[skill.availability] || AVAILABILITY_LABELS.unknown,
      availabilityCode: text(skill.availability) || 'unknown',
      required: Boolean(skill.required)
    }));
  }

  // toolboxView describes the Default Toolbox (direct chat outside
  // workspaces) and the selected instance's workspace Toolbox as two separate
  // sections. Selected never means granted, connected or ready.
  function toolboxView(projection) {
    const ok = projection?.context?.state === 'ok';
    const direct = projection?.direct_toolbox || {};
    const ws = projection?.workspace_toolbox || {};
    const directSummary =
      {
        selected: `Selected skills${direct.version ? ` (version ${direct.version})` : ''}`,
        empty: 'Explicitly empty',
        legacy: 'Not chosen yet',
        unavailable: 'Unknown',
        not_applicable: ''
      }[direct.state] ?? '';
    let pin = '';
    if (ws.state === 'pinned' || ws.state === 'empty') {
      pin = `“${text(ws.toolbox_name) || text(ws.toolbox_id)}”, pinned to version ${ws.pinned_version}`;
      if (ws.current_version && ws.current_version !== ws.pinned_version) {
        pin += ` (the Toolbox is now at version ${ws.current_version}; this instance stays on ${ws.pinned_version})`;
      }
    } else if (ws.state === 'broken') {
      pin = `Assignment to “${text(ws.toolbox_id) || 'a Toolbox'}” version ${ws.pinned_version || '?'} does not resolve`;
    }
    const native = projection?.permissions?.workspace_native_tools;
    return {
      show: ok,
      direct: {
        show: ok && direct.state !== undefined,
        state: text(direct.state),
        summary: directSummary,
        note: text(direct.note),
        skills: skillRows(direct.skills),
        inventoryUnknown: direct.inventory === 'unavailable',
        // This slice adds no Default Toolbox editor; never point at an
        // unrelated skill switch as if it edited this selection.
        editor:
          direct.state && direct.state !== 'not_applicable'
            ? 'There is no editor for the Default Toolbox on this page.'
            : ''
      },
      workspace: {
        show: ok && projection?.context?.kind === 'workspace',
        state: text(ws.state),
        pin,
        archived: Boolean(ws.archived),
        note: text(ws.note),
        skills: skillRows(ws.skills),
        connections: (Array.isArray(ws.connections) ? ws.connections : []).map(conn => ({
          name: text(conn.name) || text(conn.binding_id),
          state: AVAILABILITY_LABELS[conn.state] || AVAILABILITY_LABELS.unknown,
          stateCode: text(conn.state) || 'unknown',
          required: Boolean(conn.required),
          tools: conn.all_tools
            ? 'Every operation the connection allows (not pinned to a list)'
            : Array.isArray(conn.tools) && conn.tools.length
              ? `Operations: ${conn.tools.join(', ')}`
              : 'No operations selected',
          noLongerAllowed:
            Array.isArray(conn.no_longer_allowed) && conn.no_longer_allowed.length
              ? `No longer allowed by the connection, so not available: ${conn.no_longer_allowed.join(', ')}`
              : '',
          repo: text(conn.repo_scope) ? `Limited to repository ${text(conn.repo_scope)}` : ''
        })),
        core: Array.isArray(ws.core) ? ws.core.map(text).filter(Boolean) : [],
        inventoryUnknown: ws.inventory === 'unavailable'
      },
      permissions: {
        native:
          native === 'allowed'
            ? 'Trusted native tools: allowed in this workspace (each agent must also opt in).'
            : native === 'not_allowed'
              ? 'Trusted native tools: not allowed in this workspace.'
              : '',
        notes: Array.isArray(projection?.permissions?.notes)
          ? projection.permissions.notes.map(text).filter(Boolean)
          : []
      }
    };
  }

  // ---- Where to change it -----------------------------------------------------

  // Only these destinations exist. Each is built here from validated pieces
  // (canonical slug or workspace ID, encoded names); nothing the server sends is
  // used as a URL. A family with no editor says so instead of linking.
  const SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
  const ID_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$/;

  function workspacePathKey(ws) {
    const slug = text(ws?.folder_slug);
    if (SLUG_RE.test(slug)) return slug;
    const id = text(ws?.id);
    return ID_RE.test(id) ? id : '';
  }

  const FAMILY_LABELS = {
    configuration: 'Model and generation settings',
    profile: 'Role and description',
    prompt: 'Base prompt',
    guidance: "This instance's workspace guidance",
    toolbox: 'Workspace Toolbox'
  };

  // ownerLinks lists, per setting family, where the existing editor for the
  // selected context lives and what saving there affects.
  function ownerLinks(projection, { systemModelLinked = false } = {}) {
    const ctx = projection?.context || {};
    const def = projection?.definition || {};
    if (ctx.state !== 'ok' || def.state !== 'ok') return [];
    const name = text(projection.agent);
    const out = [];
    if (ctx.kind === 'library' || def.source === 'library_fallback') {
      const viaWorkspace = ctx.kind === 'workspace';
      const reach = viaWorkspace
        ? ' This workspace has no copy of its own yet, so it reads these from the library profile, and so does any other workspace without a copy. That lasts until Ori takes a copy for it (for example when Ori next starts).'
        : '';
      out.push({
        family: 'configuration',
        label: 'Configuration on this page',
        href: '#agentConfigurationSection',
        scope: `Saves to the library profile.${reach}`
      });
      out.push({
        family: 'profile',
        label: 'Edit Profile button at the top of this page',
        href: '#agentHeader',
        scope: `Saves to the library profile. Workspace copies keep their own role and description.${reach}`
      });
      out.push({
        family: 'prompt',
        label: 'System prompt on this page',
        href: '#promptSection',
        scope: `Saves to the library profile.${reach}`
      });
      if (systemModelLinked && ctx.kind === 'library') {
        out.push({
          family: 'configuration',
          label: 'App Default model in Settings',
          href: '/settings#system-model',
          scope:
            "Conversations with this agent use the app's Default model. Changing it there affects every conversation that uses the Default model, not only this agent."
        });
      }
      return out;
    }
    // A workspace's own copy: the workspace's editors, per agent name.
    const key = workspacePathKey(ctx.workspace);
    const tracked = ['in_step', 'pending'].includes(projection?.tracking?.state);
    const copyScope =
      `Changes this workspace's copy for every instance named ${name} here; the library profile and other workspaces are unchanged.` +
      (tracked
        ? ' After this change Ori treats the copy as customized, so later library model and prompt edits stop reaching it.'
        : '');
    out.push(
      key
        ? {
            family: 'configuration',
            label: 'Model editor in the workspace (model and provider only)',
            href: `/workspaces/${encodeURIComponent(key)}?${new URLSearchParams({ editAgentModel: name })}`,
            scope: `${copyScope} Reasoning level, temperature and output limit have no editor for a workspace copy. Opening the workspace page also activates the workspace and its lead conversation, as it always does.`
          }
        : {
            family: 'configuration',
            message: 'The workspace page could not be addressed, so no editor link is offered.'
          }
    );
    out.push({
      family: 'profile',
      message: "No editor in Ori changes a workspace copy's role or description."
    });
    out.push(
      key
        ? {
            family: 'prompt',
            label: 'System prompt on the workspace agent page',
            href: `/workspaces/${encodeURIComponent(key)}/agents/${encodeURIComponent(name)}#wad-prompt`,
            scope: copyScope
          }
        : { family: 'prompt', message: 'The workspace page could not be addressed.' }
    );
    return out;
  }

  // workspaceOwnerLinks covers the instance-level owners in a workspace context.
  function workspaceOwnerLinks(projection) {
    const ctx = projection?.context || {};
    if (ctx.kind !== 'workspace' || ctx.state !== 'ok') return [];
    const name = text(projection.agent);
    const key = workspacePathKey(ctx.workspace);
    const out = [
      {
        family: 'guidance',
        message: `No page in Ori edits an instance's workspace guidance yet. The API that changes it updates every instance named ${name} in this workspace, not only one.`
      }
    ];
    if (key) {
      out.push({
        family: 'toolbox',
        label: 'Open the agent in its workspace',
        href: `/workspaces/${encodeURIComponent(key)}?${new URLSearchParams({ agent: name.toLowerCase() })}`,
        scope:
          'Then choose Loadout to see or change its Toolbox there. Opening Loadout can give an instance that has no Toolbox assignment one; looking here never does. Opening the workspace page also activates the workspace and its lead conversation, as it always does.'
      });
    }
    return out;
  }

  const IMPACT_GROUPS = [
    [
      'reads_library',
      'No copy yet, reads the library profile directly (until Ori takes a copy for it)'
    ],
    ['kept_in_step', 'Kept in step: model and prompt changes are copied here'],
    ['pending', 'Kept in step, waiting for the next model or prompt save'],
    ['customized', 'Customized here: keeps its own settings'],
    ['independent', "Independent copy: library changes don't reach it"],
    ['unknown', 'Could not be checked']
  ];

  // impactView groups workspaces by how a library edit would reach them. It is
  // advisory current evidence; the save itself still checks and confirms.
  function impactView(projection) {
    const impact = projection?.impact || {};
    if (projection?.context?.kind !== 'library') return { show: false, lazy: false, groups: [] };
    if (impact.state === 'not_loaded') return { show: false, lazy: true, groups: [] };
    if (!['ok', 'partial'].includes(impact.state)) return { show: false, lazy: false, groups: [] };
    const copies = Array.isArray(impact.copies) ? impact.copies : [];
    const groups = IMPACT_GROUPS.map(([effect, label]) => ({
      effect,
      label,
      names: copies.filter(c => c.effect === effect).map(c => text(c.name) || text(c.workspace_id))
    })).filter(group => group.names.length);
    return {
      show: true,
      empty: copies.length === 0,
      partial: impact.state === 'partial',
      groups,
      notCarried: text(impact.not_carried)
    };
  }

  // ---- Mount ----------------------------------------------------------------

  function el(doc, tag, className, content) {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    if (content !== undefined) node.textContent = content;
    return node;
  }

  function defaultModelApi(w) {
    return () =>
      import('/js/modules/personal-assistant-model.js').catch(() => w.PersonalAssistantModel);
  }

  function mount({ root, agentName, fetchImpl, win, modelApi, systemModelLinked, onRendered }) {
    if (!root || !agentName) return null;
    const w = win || window;
    const doc = root.ownerDocument || document;
    const select = root.querySelector('[data-ownership-context]');
    const status = root.querySelector('[data-ownership-status]');
    const body = root.querySelector('[data-ownership-body]');
    const unsaved = root.querySelector('[data-ownership-unsaved]');
    const doFetch = fetchImpl || ((url, init) => w.fetch(url, init));
    const loadModelApi = modelApi || defaultModelApi(w);
    const linked = typeof systemModelLinked === 'function' ? systemModelLinked : () => false;
    let name = agentName;
    let version = null;
    let ctx = parseContext(w.location.search);
    let sequence = 0;
    let controller = null;
    root.hidden = false;

    function setStatus(message, kind) {
      status.textContent = message;
      status.dataset.state = kind || '';
    }

    function renderOptions(projection) {
      const options = contextOptions(projection, ctx);
      select.replaceChildren(
        ...options.map(option => {
          const node = el(doc, 'option', '', option.label);
          node.value = option.value;
          return node;
        })
      );
      select.value = ctx.kind === 'workspace' ? `workspace:${ctx.studioId}` : 'library';
      select.disabled = options.length < 2;
    }

    function renderInstancePicker(projection) {
      const choice = instanceChoice(projection);
      const nodes = [];
      if (choice.message) nodes.push(el(doc, 'p', 'settings-ownership-message', choice.message));
      if (!choice.show) return nodes;
      const wrap = el(doc, 'div', 'settings-ownership-picker');
      const label = el(doc, 'label', '', 'Instance');
      label.setAttribute('for', 'settingsOwnershipInstance');
      const picker = el(doc, 'select', '');
      picker.id = 'settingsOwnershipInstance';
      picker.dataset.ownershipInstance = '';
      picker.replaceChildren(
        ...choice.options.map(option => {
          const node = el(doc, 'option', '', option.label);
          node.value = option.value;
          return node;
        })
      );
      picker.value = choice.selected;
      picker.addEventListener('change', () =>
        chooseContext({ ...ctx, instanceId: text(picker.value) }, { focus: 'instance' })
      );
      wrap.append(label, picker);
      nodes.push(wrap);
      return nodes;
    }

    function renderPrompt(projection) {
      const view = promptView(projection);
      if (!view.show) return [];
      const block = el(doc, 'div', 'settings-ownership-prompt');
      block.dataset.ownershipPrompt = '';
      block.append(el(doc, 'h4', 'settings-ownership-subheading', 'Saved prompt'));
      const source = el(doc, 'p', 'settings-ownership-source');
      source.append(
        el(doc, 'strong', '', 'Base prompt from: '),
        doc.createTextNode(view.baseSource)
      );
      block.append(source);
      const base = el(doc, 'details', 'settings-ownership-text');
      base.append(
        el(doc, 'summary', '', view.baseEmpty ? 'Base prompt: not set' : 'Base prompt'),
        el(doc, 'pre', view.baseEmpty ? 'is-empty' : '', view.baseEmpty ? 'Not set' : view.base)
      );
      block.append(base);
      if (view.variables) {
        block.append(
          el(
            doc,
            'p',
            'settings-ownership-note',
            'The base prompt has {{…}} placeholders that Ori fills in when the agent runs.'
          )
        );
      }
      if (view.layers.length) {
        const list = el(doc, 'dl', 'settings-ownership-layers');
        for (const layer of view.layers) {
          const row = el(doc, 'div', 'settings-ownership-layer');
          row.append(el(doc, 'dt', '', layer.label), el(doc, 'dd', '', layer.value));
          list.append(row);
        }
        block.append(
          el(doc, 'h5', 'settings-ownership-minor', 'Workspace guidance for this instance')
        );
        block.append(list);
      }
      if (view.needsInstance) {
        block.append(
          el(
            doc,
            'p',
            'settings-ownership-message',
            'Choose an instance to see its workspace guidance layered on this base.'
          )
        );
      } else if (projection?.context?.kind === 'workspace') {
        const preview = el(doc, 'details', 'settings-ownership-text');
        preview.append(
          el(doc, 'summary', '', 'Saved text together'),
          el(doc, 'pre', '', view.preview || 'Not set')
        );
        block.append(preview);
      }
      if (view.note) block.append(el(doc, 'p', 'settings-ownership-excluded', view.note));
      return [block];
    }

    function renderOwners(projection, mine) {
      const links = [
        ...ownerLinks(projection, { systemModelLinked: linked() }),
        ...workspaceOwnerLinks(projection)
      ];
      const impact = impactView(projection);
      if (!links.length && !impact.show && !impact.lazy) return [];
      const block = el(doc, 'div', 'settings-ownership-toolbox');
      block.dataset.ownershipOwners = '';
      block.append(el(doc, 'h4', 'settings-ownership-subheading', 'Where to change it'));
      const list = el(doc, 'ul', 'settings-ownership-list');
      for (const link of links) {
        const item = el(doc, 'li', '');
        item.dataset.ownerFamily = link.family;
        item.append(
          el(doc, 'span', 'settings-ownership-item-name', `${FAMILY_LABELS[link.family]}: `)
        );
        if (link.href) {
          const anchor = el(doc, 'a', 'settings-ownership-link', link.label);
          anchor.href = link.href;
          item.append(anchor);
          if (link.scope) item.append(el(doc, 'div', 'settings-ownership-item-detail', link.scope));
        } else {
          item.append(el(doc, 'span', 'settings-ownership-item-meta', link.message));
        }
        list.append(item);
      }
      if (links.length) block.append(list);
      if (links.some(link => link.href && !link.href.startsWith('#'))) {
        block.append(
          el(
            doc,
            'p',
            'settings-ownership-note',
            "Those editors keep their own save checks and confirmations. Use your browser's Back button to return to this view."
          )
        );
      }
      if (impact.show || impact.lazy) block.append(renderImpact(projection, impact, mine));
      return [block];
    }

    function fillImpact(target, impact) {
      const nodes = [];
      if (impact.empty) {
        nodes.push(el(doc, 'p', 'settings-ownership-note', 'This agent is not in any workspace.'));
      }
      const groups = el(doc, 'ul', 'settings-ownership-list');
      for (const group of impact.groups) {
        const item = el(doc, 'li', '');
        item.dataset.impact = group.effect;
        item.append(el(doc, 'span', 'settings-ownership-item-name', `${group.label}: `));
        item.append(el(doc, 'span', '', group.names.join(', ')));
        groups.append(item);
      }
      if (impact.groups.length) nodes.push(groups);
      nodes.push(
        el(
          doc,
          'p',
          'settings-ownership-note',
          'Only model, provider, reasoning level, temperature, output limit and base prompt are copied to workspaces Ori keeps in step; role and description are not.'
        )
      );
      if (impact.notCarried) nodes.push(el(doc, 'p', 'settings-ownership-note', impact.notCarried));
      nodes.push(
        el(
          doc,
          'p',
          'settings-ownership-excluded',
          impact.partial
            ? 'Some workspaces could not be checked. This is a summary of current records, not a promise: saving checks again.'
            : 'This is a summary of current records, not a promise: saving checks again.'
        )
      );
      target.replaceChildren(...nodes);
    }

    // The impact reads every workspace's copy, so it loads only when opened.
    function renderImpact(projection, impact, mine) {
      const details = el(doc, 'details', 'settings-ownership-text settings-ownership-impact');
      details.dataset.ownershipImpact = '';
      const target = el(doc, 'div', '');
      details.append(el(doc, 'summary', '', 'What a library change reaches'), target);
      if (impact.show) {
        fillImpact(target, impact);
        return details;
      }
      let loaded = false;
      const loadImpact = async () => {
        if (loaded) return;
        loaded = true;
        target.replaceChildren(
          el(doc, 'p', 'settings-ownership-note', "Checking each workspace's copy…")
        );
        try {
          const response = await doFetch(requestURL(name, ctx, { impact: true }), {
            headers: { Accept: 'application/json' }
          });
          if (!response.ok) throw new Error('impact unavailable');
          const full = await response.json();
          if (mine !== sequence) return;
          const view = impactView(full);
          if (!view.show) throw new Error('impact unavailable');
          fillImpact(target, view);
        } catch (_) {
          if (mine !== sequence) return;
          loaded = false;
          const retry = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Try again');
          retry.type = 'button';
          retry.addEventListener('click', () => void loadImpact());
          target.replaceChildren(
            el(
              doc,
              'p',
              'settings-ownership-message',
              'What a library change reaches could not be checked.'
            ),
            retry
          );
        }
      };
      details.addEventListener('toggle', () => {
        if (details.open) void loadImpact();
      });
      return details;
    }

    function renderSkillList(rows) {
      const list = el(doc, 'ul', 'settings-ownership-list');
      for (const row of rows) {
        const item = el(doc, 'li', '');
        item.dataset.availability = row.availabilityCode;
        item.append(el(doc, 'span', 'settings-ownership-item-name', row.name));
        const meta = [row.source, row.required ? 'required' : '', row.availability].filter(Boolean);
        item.append(el(doc, 'span', 'settings-ownership-item-meta', ` — ${meta.join(' · ')}`));
        list.append(item);
      }
      return list;
    }

    function collapsible(summary, content) {
      const details = el(doc, 'details', 'settings-ownership-text');
      details.append(el(doc, 'summary', '', summary), content);
      return details;
    }

    function renderToolboxes(projection) {
      const view = toolboxView(projection);
      if (!view.show) return [];
      const nodes = [];
      if (view.direct.show && (view.direct.state !== 'not_applicable' || view.direct.note)) {
        const block = el(doc, 'div', 'settings-ownership-toolbox');
        block.dataset.ownershipToolbox = 'direct';
        block.append(el(doc, 'h4', 'settings-ownership-subheading', 'Direct chat Default Toolbox'));
        if (view.direct.summary)
          block.append(el(doc, 'p', 'settings-ownership-source', view.direct.summary));
        if (view.direct.note)
          block.append(el(doc, 'p', 'settings-ownership-note', view.direct.note));
        if (view.direct.skills.length) {
          block.append(
            collapsible(
              `${view.direct.skills.length} selected skill${view.direct.skills.length === 1 ? '' : 's'}`,
              renderSkillList(view.direct.skills)
            )
          );
        }
        if (view.direct.inventoryUnknown) {
          block.append(
            el(
              doc,
              'p',
              'settings-ownership-note',
              "The agent's skills could not be read, so availability is unknown."
            )
          );
        }
        if (view.direct.editor)
          block.append(el(doc, 'p', 'settings-ownership-excluded', view.direct.editor));
        nodes.push(block);
      }
      if (view.workspace.show) {
        const block = el(doc, 'div', 'settings-ownership-toolbox');
        block.dataset.ownershipToolbox = 'workspace';
        block.append(
          el(doc, 'h4', 'settings-ownership-subheading', 'Workspace Toolbox for this instance')
        );
        if (view.workspace.pin) {
          const pin = el(doc, 'p', 'settings-ownership-source');
          pin.append(el(doc, 'strong', '', 'Toolbox: '), doc.createTextNode(view.workspace.pin));
          block.append(pin);
        }
        if (view.workspace.archived) {
          block.append(el(doc, 'p', 'settings-ownership-note', 'This Toolbox is archived.'));
        }
        if (view.workspace.note)
          block.append(el(doc, 'p', 'settings-ownership-note', view.workspace.note));
        if (view.workspace.skills.length) {
          block.append(
            collapsible(
              `${view.workspace.skills.length} selected skill${view.workspace.skills.length === 1 ? '' : 's'}`,
              renderSkillList(view.workspace.skills)
            )
          );
        }
        if (view.workspace.connections.length) {
          const list = el(doc, 'ul', 'settings-ownership-list');
          for (const conn of view.workspace.connections) {
            const item = el(doc, 'li', '');
            item.dataset.availability = conn.stateCode;
            item.append(el(doc, 'span', 'settings-ownership-item-name', conn.name));
            item.append(
              el(
                doc,
                'span',
                'settings-ownership-item-meta',
                ` — ${[conn.required ? 'required' : '', conn.state].filter(Boolean).join(' · ')}`
              )
            );
            for (const line of [conn.tools, conn.noLongerAllowed, conn.repo].filter(Boolean)) {
              item.append(el(doc, 'div', 'settings-ownership-item-detail', line));
            }
            list.append(item);
          }
          block.append(
            collapsible(
              `${view.workspace.connections.length} selected connection${view.workspace.connections.length === 1 ? '' : 's'}`,
              list
            )
          );
        }
        if (view.workspace.inventoryUnknown) {
          block.append(
            el(
              doc,
              'p',
              'settings-ownership-note',
              "The agent's skills could not be read, so their availability is unknown."
            )
          );
        }
        if (view.workspace.core.length) {
          block.append(
            el(
              doc,
              'p',
              'settings-ownership-note',
              `Always provided here: ${view.workspace.core.join('; ')}.`
            )
          );
        }
        nodes.push(block);
      }
      if (view.permissions.notes.length || view.permissions.native) {
        const block = el(doc, 'div', 'settings-ownership-toolbox');
        block.dataset.ownershipPermissions = '';
        block.append(el(doc, 'h4', 'settings-ownership-subheading', 'Who controls access'));
        if (view.permissions.native)
          block.append(el(doc, 'p', 'settings-ownership-note', view.permissions.native));
        const list = el(doc, 'ul', 'settings-ownership-list');
        for (const note of view.permissions.notes) list.append(el(doc, 'li', '', note));
        block.append(list);
        nodes.push(block);
      }
      return nodes;
    }

    async function renderConversation(projection, mine, slot) {
      const request = conversationRequest(projection, { systemModelLinked: linked() });
      if (!request) return;
      const saved = (projection.definition.fields || []).find(f => f.key === 'model');
      const row = el(doc, 'p', 'settings-ownership-conversation');
      row.dataset.ownershipConversation = '';
      row.append(el(doc, 'strong', '', `${request.label}: `), doc.createTextNode('checking…'));
      slot.replaceChildren(row);
      let api = null;
      let selection = null;
      try {
        api = await loadModelApi();
        selection = await api.readModelSelection(request.context);
      } catch (_) {
        /* Unknown is not a fallback model. */
      }
      if (mine !== sequence) return;
      const describe = api?.modelDescription || (() => 'unknown');
      const view = conversationView(request, selection, saved?.value, describe);
      row.replaceChildren(el(doc, 'strong', '', `${view.label}: `), doc.createTextNode(view.value));
      const href = api?.modelSettingsHref?.(selection) || '';
      if (href) {
        const link = el(doc, 'a', 'settings-ownership-link', 'Where to change it');
        link.href = href;
        row.append(doc.createTextNode(' '), link);
      }
      if (view.note) slot.append(el(doc, 'p', 'settings-ownership-note', view.note));
    }

    function renderProjection(projection, mine) {
      const view = viewModel(projection);
      const nodes = [el(doc, 'h3', 'settings-ownership-heading', view.heading)];
      if (view.message) nodes.push(el(doc, 'p', 'settings-ownership-message', view.message));
      if (view.contextsNote) nodes.push(el(doc, 'p', 'settings-ownership-note', view.contextsNote));
      nodes.push(...renderInstancePicker(projection));
      if (view.source) {
        const source = el(doc, 'p', 'settings-ownership-source');
        source.dataset.ownershipSource = 'definition';
        source.append(el(doc, 'strong', '', 'Saved in: '), doc.createTextNode(view.source));
        nodes.push(source);
      }
      if (view.note) nodes.push(el(doc, 'p', 'settings-ownership-note', view.note));
      if (view.disabled)
        nodes.push(el(doc, 'p', 'settings-ownership-note', 'This agent is paused.'));
      if (view.tracking) nodes.push(el(doc, 'p', 'settings-ownership-tracking', view.tracking));
      if (view.fields.length) {
        const list = el(doc, 'dl', 'settings-ownership-fields');
        for (const field of view.fields) {
          const row = el(doc, 'div', 'settings-ownership-row');
          row.dataset.field = field.key;
          row.append(el(doc, 'dt', '', field.label));
          row.append(el(doc, 'dd', field.empty ? 'is-empty' : '', field.value));
          // Always three cells per row: the grid pairs labels and values by
          // position, so a missing comparison cell would shift every later row.
          const cmp = el(doc, 'dd', 'settings-ownership-comparison', field.comparison);
          cmp.dataset.comparison = field.comparison ? field.comparisonCode : 'none';
          row.append(cmp);
          list.append(row);
        }
        nodes.push(list);
      }
      const conversation = el(doc, 'div', 'settings-ownership-conversation-slot');
      nodes.push(conversation);
      nodes.push(...renderPrompt(projection));
      nodes.push(...renderToolboxes(projection));
      nodes.push(...renderOwners(projection, mine));
      if (view.excluded) nodes.push(el(doc, 'p', 'settings-ownership-excluded', view.excluded));
      body.replaceChildren(...nodes);
      void renderConversation(projection, mine, conversation);
    }

    function renderFailure(message) {
      const retry = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Retry');
      retry.type = 'button';
      retry.addEventListener('click', () => void load());
      body.replaceChildren(el(doc, 'p', 'settings-ownership-message', message), retry);
    }

    async function load({ focus = '' } = {}) {
      const mine = ++sequence;
      const key = contextKey(ctx);
      controller?.abort?.();
      controller = typeof AbortController === 'function' ? new AbortController() : null;
      // Clear the previous context's values at once: a slow answer must never
      // leave one workspace's settings under another's heading.
      body.replaceChildren();
      root.dataset.context = key;
      root.setAttribute('aria-busy', 'true');
      setStatus('Loading saved settings…', 'loading');
      try {
        const response = await doFetch(requestURL(name, ctx), {
          headers: { Accept: 'application/json' },
          signal: controller?.signal
        });
        if (mine !== sequence) return;
        if (!response.ok) {
          throw new Error(
            response.status === 404
              ? 'This agent could not be found.'
              : 'Saved settings could not be loaded.'
          );
        }
        const projection = await response.json();
        if (mine !== sequence || contextKey(ctx) !== key) return;
        renderOptions(projection);
        renderProjection(projection, mine);
        if (typeof onRendered === 'function') onRendered();
        setStatus('', 'ready');
        if (focus === 'instance') body.querySelector?.('[data-ownership-instance]')?.focus?.();
      } catch (error) {
        if (mine !== sequence || error?.name === 'AbortError') return;
        setStatus('', 'error');
        // A failed first read has no choices yet; still name what was asked for.
        if (!select.children?.length) {
          const option = el(
            doc,
            'option',
            '',
            ctx.kind === 'workspace' ? 'Selected workspace' : 'Library profile'
          );
          option.value = ctx.kind === 'workspace' ? `workspace:${ctx.studioId}` : 'library';
          select.replaceChildren(option);
          select.value = option.value;
        }
        renderFailure(error?.message || 'Saved settings could not be loaded.');
      } finally {
        if (mine === sequence) root.removeAttribute('aria-busy');
      }
    }

    function chooseContext(next, { push = true, focus = '' } = {}) {
      if (contextKey(next) === contextKey(ctx)) return;
      ctx = next;
      if (push) {
        const url = `${w.location.pathname}${writeContext(w.location.search, ctx)}${w.location.hash || ''}`;
        w.history.pushState({ ownershipContext: contextKey(ctx) }, '', url);
      }
      void load({ focus });
    }

    select.addEventListener('change', () => chooseContext(contextFromOption(select.value)));
    w.addEventListener('popstate', () =>
      chooseContext(parseContext(w.location.search), { push: false })
    );
    void load();

    return {
      context: () => ({ ...ctx }),
      reload: () => load(),
      // The page tells the view about each confirmed definition. It re-reads
      // only when the confirmed name or version moved; a draft never counts.
      syncAgent({ name: nextName, version: nextVersion } = {}) {
        const renamed = Boolean(nextName) && nextName !== name;
        const moved = version !== null && text(nextVersion) !== version;
        if (nextName) name = nextName;
        version = text(nextVersion);
        if (renamed || moved) void load();
      },
      // Unsaved edits are not part of any saved record, so they are not shown
      // here; say so while they exist.
      setUnsaved(dirty) {
        if (!unsaved) return;
        unsaved.hidden = !dirty;
        unsaved.textContent = dirty
          ? 'You have unsaved changes on this page. They are not shown here until they are saved.'
          : '';
      }
    };
  }

  window.AgentSettingsOwnership = {
    mount,
    parseContext,
    writeContext,
    requestURL,
    contextKey,
    viewModel,
    contextOptions,
    contextFromOption,
    formatValue,
    instanceChoice,
    promptView,
    conversationRequest,
    conversationView,
    toolboxView,
    ownerLinks,
    workspaceOwnerLinks,
    impactView
  };
})();
