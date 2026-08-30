// Create Workspace — team draft state (PRD create-workspace-team-step, FR67)
//
// One authoritative client-side representation of the workspace team the user is
// assembling, plus a pure derivation of everything the wizard renders from it.
// Blueprint chips (step 1), the Team roster (step 3), and the Review receipt
// (step 4) all read the same derived view, so they cannot disagree.
//
// Two layers, deliberately:
//
//   draft   — minimal, mutable, serializable. The ONLY source of truth. Rendered
//             DOM controls are never consulted; they write into the draft.
//   derive  — a pure function of the draft. Recomputed on read, never stored, so
//             a stale render can't outlive a state change.
//
// Nothing here performs I/O. Staging a customization, choosing a primary, or
// adding a saved agent only mutates local state; the draft becomes a request
// exactly once, via toCreatePayload(), when the user submits Create (FR67, FR68).
(function () {
  'use strict';

  const PLAN_IDLE = 'idle';
  const PLAN_LOADING = 'loading';
  const PLAN_READY = 'ready';
  const PLAN_ERROR = 'error';

  // Lifecycle copy is fixed here rather than at each call site so the wizard
  // cannot claim an agent is already attached before the workspace exists
  // (FR37). Present/future tense is the point.
  const LIFECYCLE_LABELS = {
    reuse: 'Saved agent · will be attached',
    create: 'New reusable agent · will be created and attached',
    'customized-copy': 'Customized copy · will be created and attached',
    'assistant-create': 'Shared assistant role · will be created and linked',
    'assistant-link': 'Existing shared assistant role · will be linked'
  };

  // Fields a staged customization may carry, mapped to their request keys. Key
  // PRESENCE is significant: the server's templateAgentOverride uses pointer
  // fields, so an absent key keeps the blueprint's value while a present-but-
  // empty key clears it (the agent then inherits the system/app default).
  // Never test these with truthiness — always with hasOwnProperty.
  const OVERRIDE_FIELDS = {
    name: 'name',
    model: 'model',
    provider: 'provider',
    systemPrompt: 'system_prompt',
    role: 'role',
    type: 'type'
  };

  function has(object, key) {
    return Boolean(object) && Object.prototype.hasOwnProperty.call(object, key);
  }

  function text(value) {
    return String(value == null ? '' : value).trim();
  }

  // Case-insensitive identity for agent names. Matches the server, which lowers
  // names when checking duplicates (template_agents.go validateTemplateAgent-
  // OverrideNames) and when canonicalizing saved-agent selections.
  function agentKey(name) {
    return text(name).toLocaleLowerCase();
  }

  function normalizePlanAgent(agent) {
    return {
      name: text(agent && agent.name),
      action: text(agent && agent.action).toLowerCase() === 'reuse' ? 'reuse' : 'create',
      entryPoint: Boolean(agent && agent.entry_point),
      role: text(agent && agent.role),
      type: text(agent && agent.type),
      model: text(agent && agent.model),
      provider: text(agent && agent.provider),
      systemPrompt: text(agent && agent.system_prompt),
      modelSource: text(agent && agent.model_source),
      tools: (agent && agent.tools) || null,
      warning: text(agent && agent.warning)
    };
  }

  function normalizeAssistantProgram(program) {
    if (!program || typeof program !== 'object') return null;
    const roles = (Array.isArray(program.roles) ? program.roles : [])
      .map(role => ({
        id: text(role && role.id),
        label: text(role && role.label),
        description: text(role && role.description),
        primary: Boolean(role && role.primary),
        agentName: text(role && role.agent_name)
      }))
      .filter(role => role.id && role.label);
    const stages = (Array.isArray(program.stages) ? program.stages : [])
      .map(stage => ({
        id: text(stage && stage.id),
        label: text(stage && stage.label),
        description: text(stage && stage.description)
      }))
      .filter(stage => stage.id && stage.label);
    const id = text(program.id);
    if (!id || !roles.length || !roles.some(role => role.primary) || !stages.length) return null;
    return {
      id,
      stationName: text(program.station_name),
      stationDescription: text(program.station_description),
      defaultPrimaryName: text(program.default_primary_name),
      hireTitle: text(program.hire_title),
      hireDescription: text(program.hire_description),
      existingHired: Boolean(program.existing_hired),
      existingProvider: text(program.existing_provider),
      existingModel: text(program.existing_model),
      roles,
      stages
    };
  }

  // Accepts the raw /api/workspaces/template-agent-plan response and flattens it
  // to the camelCase shape the rest of this module uses. Tolerates a missing or
  // malformed agents array rather than throwing mid-render.
  function normalizePlan(data) {
    const rawAgents = Array.isArray(data && data.agents) ? data.agents : [];
    const agents = rawAgents.map(normalizePlanAgent).filter(agent => agent.name !== '');
    return {
      hasAgents: Boolean(data && data.has_agents) && agents.length > 0,
      templateId: text(data && data.template_id),
      templateName: text(data && data.template_name),
      declaredPrimary: text(data && data.entry_agent_name),
      systemProvider: text(data && data.system_provider),
      systemModel: text(data && data.system_model),
      systemModelConfigured: Boolean(data && data.system_model_configured),
      assistantProgram: normalizeAssistantProgram(data && data.assistant_program),
      agents,
      warnings: (Array.isArray(data && data.warnings) ? data.warnings : [])
        .map(warning => text(warning))
        .filter(Boolean)
    };
  }

  function emptyAssistantHire() {
    return { programKey: '', name: '', provider: '', model: '' };
  }

  function createDraft() {
    return {
      plan: { status: PLAN_IDLE, blueprintKey: '', data: null, error: '' },
      includeBlueprintTeam: true,
      overrides: new Map(),
      assistantHire: emptyAssistantHire(),
      savedSelections: [],
      savedRoster: { status: PLAN_IDLE, agents: [], error: '' },
      explicitPrimary: ''
    };
  }

  // Full reset for modal close/cancel: no staged agent survives a discarded
  // wizard (FR13). Mutates in place so callers holding the reference stay valid.
  function resetDraft(draft) {
    if (!draft) return createDraft();
    const fresh = createDraft();
    Object.keys(fresh).forEach(key => {
      draft[key] = fresh[key];
    });
    return draft;
  }

  // Blueprint-derived state is discarded whenever the blueprint identity
  // changes (FR21) but survives a retry of the same blueprint. Manually selected
  // saved agents are deliberately retained (FR22); an explicit primary survives
  // only while it still names one of them.
  function discardBlueprintDerivedState(draft) {
    draft.overrides = new Map();
    draft.assistantHire = emptyAssistantHire();
    draft.includeBlueprintTeam = true;
    if (draft.explicitPrimary && !isSelected(draft, draft.explicitPrimary)) {
      draft.explicitPrimary = '';
    }
  }

  function applyBlueprintKey(draft, blueprintKey) {
    const nextKey = text(blueprintKey);
    if (nextKey !== text(draft.plan && draft.plan.blueprintKey)) {
      discardBlueprintDerivedState(draft);
    }
    return nextKey;
  }

  function setPlanLoading(draft, blueprintKey) {
    const key = applyBlueprintKey(draft, blueprintKey);
    draft.plan = { status: PLAN_LOADING, blueprintKey: key, data: null, error: '' };
    return draft;
  }

  function setPlanReady(draft, blueprintKey, data) {
    const key = applyBlueprintKey(draft, blueprintKey);
    const normalized = normalizePlan(data);
    draft.plan = { status: PLAN_READY, blueprintKey: key, data: normalized, error: '' };
    const program = normalized.assistantProgram;
    if (program) {
      const programKey = `${key}:${program.id}`;
      if (!draft.assistantHire || draft.assistantHire.programKey !== programKey) {
        const existingPrimary = program.roles.find(role => role.primary)?.agentName;
        draft.assistantHire = {
          programKey,
          name: program.existingHired
            ? existingPrimary || program.defaultPrimaryName
            : program.defaultPrimaryName,
          provider: program.existingHired ? program.existingProvider : '',
          model: program.existingHired ? program.existingModel : ''
        };
      }
    } else {
      draft.assistantHire = emptyAssistantHire();
    }
    return draft;
  }

  function setAssistantHire(draft, fields) {
    const program = draft && draft.plan && draft.plan.data?.assistantProgram;
    if (!draft || !program || program.existingHired || !fields) return draft;
    const next = { ...(draft.assistantHire || emptyAssistantHire()) };
    for (const field of ['name', 'provider', 'model']) {
      if (has(fields, field)) next[field] = text(fields[field]);
    }
    draft.assistantHire = next;
    return draft;
  }

  function setPlanError(draft, blueprintKey, message) {
    const key = applyBlueprintKey(draft, blueprintKey);
    draft.plan = {
      status: PLAN_ERROR,
      blueprintKey: key,
      data: null,
      error: text(message) || 'Could not load blueprint agents.'
    };
    return draft;
  }

  // Used when there is no blueprint agent plan to load at all (import mode, or a
  // selection with neither template nor blank marker). Distinct from an error:
  // there is nothing to retry.
  function clearPlan(draft) {
    applyBlueprintKey(draft, '');
    draft.plan = { status: PLAN_IDLE, blueprintKey: '', data: null, error: '' };
    return draft;
  }

  function setIncludeBlueprintTeam(draft, included) {
    draft.includeBlueprintTeam = Boolean(included);
    // The blueprint's entry agent can no longer own the primary slot once its
    // team is excluded; derive() recomputes the fallback (FR49, FR50).
    return draft;
  }

  function planAgents(draft) {
    const data = draft && draft.plan && draft.plan.data;
    return data && data.hasAgents ? data.agents : [];
  }

  function planAgentAt(draft, index) {
    const agents = planAgents(draft);
    return Number.isInteger(index) && agents[index] ? agents[index] : null;
  }

  function getOverride(draft, index) {
    const override = draft && draft.overrides ? draft.overrides.get(index) : null;
    return override ? { ...override } : null;
  }

  // Upserts a staged customization for one blueprint roster entry. Customizing a
  // NEW blueprint agent edits its staged create definition in place rather than
  // adding a second entry (FR46); customizing a REUSED agent stages one copy
  // (FR40) — the rename is what makes it a copy and leaves the shared saved
  // agent untouched (FR41).
  //
  // Only keys actually present in `fields` are staged, preserving the server's
  // absent/empty/set distinction. A rename that differs from the blueprint name
  // by letter case alone is dropped on a reused row: it would not produce a
  // meaningfully different agent, and staging it would make the row claim
  // "customized copy" while the server still reused the original.
  function stageOverride(draft, index, fields) {
    if (!draft || !Number.isInteger(index) || !fields) return draft;
    const planAgent = planAgentAt(draft, index);
    if (!planAgent) return draft;

    const next = { ...(draft.overrides.get(index) || {}) };
    Object.keys(OVERRIDE_FIELDS).forEach(field => {
      if (!has(fields, field)) return;
      next[field] = text(fields[field]);
    });

    if (has(next, 'name')) {
      const staged = next.name;
      const original = planAgent.name;
      const sameName =
        staged === original ||
        (planAgent.action === 'reuse' && agentKey(staged) === agentKey(original));
      if (sameName) delete next.name;
    }

    // An override that stages nothing is not an override; dropping it keeps the
    // request clean and isModifiedFromBlueprint honest.
    if (Object.keys(next).length === 0) draft.overrides.delete(index);
    else draft.overrides.set(index, next);
    return draft;
  }

  function clearOverride(draft, index) {
    if (draft && draft.overrides) draft.overrides.delete(index);
    return draft;
  }

  function isAttachableSavedAgent(agent) {
    return (
      Boolean(agent && text(agent.name)) && text(agent.source || 'user').toLowerCase() !== 'cli'
    );
  }

  function findSavedAgent(draft, name) {
    const key = agentKey(name);
    if (!key) return null;
    const agents = draft && draft.savedRoster ? draft.savedRoster.agents : [];
    return agents.find(agent => agentKey(agent && agent.name) === key) || null;
  }

  function isSelected(draft, name) {
    const key = agentKey(name);
    return (draft.savedSelections || []).some(selected => agentKey(selected) === key);
  }

  // The renderer input for one agent's visual identity, projected from the saved
  // record the dashboard list already returns. Every Team-step surface reads
  // this rather than deriving a face of its own, so the wizard shows the same
  // identity as the Agents page and the workspace (shared-renderer contract).
  //
  // `agent` is null for a blueprint agent that does not exist yet: the resolver
  // then falls back to art seeded on the name, which is exactly what that agent
  // will look like once it is created. The lookup is by name for BOTH sources,
  // because an unrenamed blueprint row sharing a name with a saved agent is
  // ordinary reuse (FR41) — that agent's real face is the honest one to show.
  function identityFrom(name, agent) {
    const appearance = (agent && agent.appearance) || null;
    const character = (appearance && appearance.character) || {};
    return {
      name: text(name),
      source: text(agent && agent.source).toLocaleLowerCase() === 'cli' ? 'cli' : 'user',
      role: text(agent && agent.role),
      // The canonical object travels whole to the shared renderer, which is the
      // only thing that decides what shows. Inferring a source from populated
      // fields here is exactly the drift this feature removes (FR-81/FR-82).
      appearance: appearance,
      characterId: text(character.catalog_id)
    };
  }

  // Identity for a name as it stands in the draft right now. Renaming a
  // blueprint agent re-seeds its fallback art, which is correct: the created
  // agent will carry the new name.
  function identityForName(draft, name) {
    return identityFrom(name, findSavedAgent(draft, name));
  }

  // True when the blueprint already contributes this name under its ORIGINAL
  // identity. Used to disable the picker's Add action (FR62) and, in derive(),
  // to explain which source owns a retained selection after a blueprint change
  // (FR23).
  function isBlueprintOwned(draft, name) {
    const key = agentKey(name);
    return planAgents(draft).some(agent => agentKey(agent.name) === key);
  }

  function setSavedRosterLoading(draft) {
    draft.savedRoster = { status: PLAN_LOADING, agents: draft.savedRoster.agents || [], error: '' };
    return draft;
  }

  function setSavedRosterReady(draft, agents) {
    const list = (Array.isArray(agents) ? agents : []).filter(agent => text(agent && agent.name));
    draft.savedRoster = { status: PLAN_READY, agents: list, error: '' };
    return draft;
  }

  function setSavedRosterError(draft, message) {
    draft.savedRoster = {
      status: PLAN_ERROR,
      agents: [],
      error: text(message) || 'Your saved agents could not be loaded.'
    };
    return draft;
  }

  // Returns true when the selection changed, so callers can announce it (FR103)
  // without re-announcing a no-op click.
  function addSavedAgent(draft, name) {
    const agent = findSavedAgent(draft, name);
    if (!agent || !isAttachableSavedAgent(agent)) return false;
    const canonical = text(agent.name);
    if (isSelected(draft, canonical) || isBlueprintOwned(draft, canonical)) return false;
    draft.savedSelections = [...draft.savedSelections, canonical];
    return true;
  }

  function removeSavedAgent(draft, name) {
    const key = agentKey(name);
    const before = draft.savedSelections.length;
    draft.savedSelections = draft.savedSelections.filter(selected => agentKey(selected) !== key);
    // Removing the chosen primary hands the slot back to derive()'s fallback
    // rather than leaving a dangling reference (FR53).
    if (agentKey(draft.explicitPrimary) === key) draft.explicitPrimary = '';
    return draft.savedSelections.length !== before;
  }

  // Only a selected saved agent may be chosen explicitly: the server requires
  // entry_agent_name to also appear in existing_agent_names
  // (workspace_handler.go validateCreateWorkspaceAgentComposition), and a
  // blueprint primary is instead derived from roster order.
  function setExplicitPrimary(draft, name) {
    const canonical = text(name);
    if (canonical && !isSelected(draft, canonical)) return false;
    if (agentKey(draft.explicitPrimary) === agentKey(canonical)) return false;
    draft.explicitPrimary = canonical;
    return true;
  }

  function modelLabel(model, provider) {
    const trimmedModel = text(model);
    if (!trimmedModel) return 'App default';
    const trimmedProvider = text(provider);
    return trimmedProvider ? `${trimmedProvider} / ${trimmedModel}` : trimmedModel;
  }

  function modelSourceLabel(source) {
    switch (text(source)) {
      case 'system':
        return 'System model';
      case 'template':
        return 'Template model';
      case 'existing':
        return 'Saved agent model';
      default:
        return 'Default model';
    }
  }

  // Applies any staged override to one blueprint plan entry, yielding the agent
  // definition this request would actually produce.
  function resolveBlueprintEntry(draft, planAgent, index) {
    const override = draft.overrides.get(index) || {};
    const name = has(override, 'name') ? override.name : planAgent.name;
    const model = has(override, 'model') ? override.model : planAgent.model;
    const provider = has(override, 'provider') ? override.provider : planAgent.provider;
    const renamed = agentKey(name) !== agentKey(planAgent.name);
    const customized = Object.keys(override).length > 0;
    // A reused definition only becomes a separate copy once it is renamed; an
    // unrenamed reuse row stays a plain attachment of the shared agent (FR41).
    const lifecycle =
      planAgent.action === 'reuse' ? (renamed ? 'customized-copy' : 'reuse') : 'create';
    return {
      key: agentKey(name),
      name,
      source: 'blueprint',
      identity: identityForName(draft, name),
      lifecycle,
      lifecycleLabel: LIFECYCLE_LABELS[lifecycle],
      modelLabel: modelLabel(model, provider),
      modelSourceLabel: has(override, 'model')
        ? text(override.model)
          ? 'Custom model'
          : 'Default model'
        : modelSourceLabel(planAgent.modelSource),
      inheritsModel: text(model) === '',
      role: planAgent.role,
      type: planAgent.type,
      templateAgentIndex: index,
      declaredPrimary: planAgent.entryPoint,
      originalName: planAgent.name,
      isCustomized: customized,
      // Customization is expressible only for blueprint rows: it rides the
      // existing template_agent_overrides field. A manually added saved agent is
      // attached by name and has no override channel (see Non-Goal 3).
      customizable: true,
      removable: false,
      warning: planAgent.warning
    };
  }

  function resolveSavedEntry(draft, name) {
    const agent = findSavedAgent(draft, name) || { name };
    const canonical = text(agent.name) || text(name);
    return {
      key: agentKey(canonical),
      name: canonical,
      source: 'saved',
      identity: identityFrom(canonical, agent),
      lifecycle: 'reuse',
      lifecycleLabel: LIFECYCLE_LABELS.reuse,
      modelLabel: text(agent.model) || 'Uses saved agent model',
      modelSourceLabel: 'Saved agent model',
      inheritsModel: false,
      role: text(agent.role),
      type: text(agent.type),
      templateAgentIndex: null,
      declaredPrimary: false,
      originalName: canonical,
      isCustomized: false,
      customizable: false,
      removable: true,
      workspaceCount: Number(agent.workspace_count) || 0,
      warning: ''
    };
  }

  function resolveAssistantEntries(draft, program) {
    const hire = draft.assistantHire || emptyAssistantHire();
    return program.roles.map(role => {
      const name = program.existingHired
        ? role.agentName || (role.primary ? text(hire.name) : role.label)
        : role.primary
          ? text(hire.name)
          : role.label;
      const lifecycle = program.existingHired ? 'assistant-link' : 'assistant-create';
      return {
        key: agentKey(name),
        name,
        source: 'assistant-program',
        identity: identityFrom(name, null),
        lifecycle,
        lifecycleLabel: LIFECYCLE_LABELS[lifecycle],
        modelLabel: hire.model
          ? modelLabel(hire.model, hire.provider)
          : hire.provider
            ? `${hire.provider} default`
            : 'Ori default',
        modelSourceLabel: hire.model
          ? 'Selected model'
          : hire.provider
            ? 'Provider default'
            : 'Ori default',
        inheritsModel: !text(hire.model),
        role: role.label,
        description: role.description,
        type: '',
        templateAgentIndex: null,
        assistantRoleId: role.id,
        declaredPrimary: role.primary,
        originalName: role.label,
        isCustomized:
          !program.existingHired &&
          role.primary &&
          agentKey(name) !== agentKey(program.defaultPrimaryName),
        customizable: false,
        removable: false,
        warning: ''
      };
    });
  }

  function assistantNameProblem(name) {
    const normalized = text(name);
    if (!normalized) return 'Choose a name for the primary assistant.';
    if (normalized.length > 100) return 'Assistant name must be 100 characters or fewer.';
    if (!/^[A-Za-z0-9 _-]+$/.test(normalized)) {
      return 'Assistant name may use letters, numbers, spaces, underscores, and hyphens.';
    }
    return '';
  }

  function resolvePrimaryName(draft, blueprintEntries, savedEntries) {
    const all = [...blueprintEntries, ...savedEntries];
    const explicit = agentKey(draft.explicitPrimary);
    if (explicit && all.some(entry => entry.key === explicit)) return draft.explicitPrimary;
    const declared = blueprintEntries.find(entry => entry.declaredPrimary);
    if (declared) return declared.name;
    if (savedEntries.length > 0) return savedEntries[0].name;
    return '';
  }

  function serializeOverrides(draft) {
    return Array.from(draft.overrides.entries())
      .sort(([left], [right]) => left - right)
      .map(([index, override]) => {
        const payload = { index };
        Object.keys(OVERRIDE_FIELDS).forEach(field => {
          if (has(override, field)) payload[OVERRIDE_FIELDS[field]] = override[field];
        });
        return payload;
      })
      .filter(override => Object.keys(override).length > 1);
  }

  // Pure projection of the draft. Everything the wizard renders — Blueprint
  // chips, the Team roster, the Review receipt, and the create request — comes
  // from here, so no two surfaces can describe different teams.
  function derive(draft) {
    const source = draft || createDraft();
    const plan = source.plan || { status: PLAN_IDLE, data: null, error: '' };
    const assistantProgram = plan.data?.assistantProgram || null;
    const includeTeam = assistantProgram ? true : Boolean(source.includeBlueprintTeam);
    const allPlanAgents = planAgents(source);
    const activePlanAgents = includeTeam ? allPlanAgents : [];

    const blueprintEntries = assistantProgram
      ? resolveAssistantEntries(source, assistantProgram)
      : activePlanAgents.map((agent, index) => resolveBlueprintEntry(source, agent, index));

    // A retained saved selection that the (possibly changed) blueprint already
    // contributes stays selected but yields its roster slot to the blueprint
    // entry, and we report which source won so the UI can say so (FR23).
    const originalKeys = new Set(activePlanAgents.map(agent => agentKey(agent.name)));
    const shadowedSelections = [];
    const savedEntries = [];
    if (!assistantProgram) {
      source.savedSelections.forEach(name => {
        const key = agentKey(name);
        if (originalKeys.has(key)) {
          shadowedSelections.push({ name, ownedBy: 'blueprint' });
          return;
        }
        if (savedEntries.some(entry => entry.key === key)) return;
        savedEntries.push(resolveSavedEntry(source, name));
      });
    }

    const primaryName = resolvePrimaryName(source, blueprintEntries, savedEntries);
    const primaryKey = agentKey(primaryName);
    const members = [...blueprintEntries, ...savedEntries];
    // Promote by POSITION, not by key. A staged rename can make two members share
    // one key, and excluding "everything matching the primary key" would silently
    // drop the colliding member — hiding the very duplicate FR45 must report.
    const primaryIndex = members.findIndex(entry => entry.key === primaryKey);
    const primary = primaryIndex >= 0 ? members[primaryIndex] : null;
    const ordered = primary
      ? [primary, ...members.filter((entry, index) => index !== primaryIndex)]
      : members.slice();
    const roster = ordered.map((entry, index) => ({
      ...entry,
      designation: primary && index === 0 ? 'primary' : 'specialist',
      // Only a selected saved agent can be promoted; see setExplicitPrimary.
      canMakePrimary: entry.source === 'saved' && !(primary && index === 0)
    }));
    const specialists = roster.filter(entry => entry.designation === 'specialist');

    // Resulting-name collisions are a blocker, distinct from the shadowing case
    // above: here a staged rename would produce two definitions with one name
    // (FR45), which the server would reject after the user left the wizard.
    const nameCounts = new Map();
    roster.forEach(entry => {
      nameCounts.set(entry.key, (nameCounts.get(entry.key) || 0) + 1);
    });
    // Report the customized member first: it owns the name the user just typed,
    // so that is the field focus should land on (FR104).
    const collisions = roster
      .filter(entry => nameCounts.get(entry.key) > 1)
      .sort((left, right) => Number(right.isCustomized) - Number(left.isCustomized));

    const issues = [];
    if (plan.status === PLAN_LOADING) {
      // Never rendered as a confirmed empty team or as a warning (FR92).
      issues.push({
        id: 'plan-loading',
        severity: 'loading',
        message: 'Checking this blueprint’s agents…',
        recovery: [],
        anchor: 'team-roster'
      });
    }
    if (plan.status === PLAN_ERROR && includeTeam) {
      // Blocking: the resulting roster cannot be reviewed, so no trustworthy
      // request can be built (FR94). Excluding the team removes the blocker.
      issues.push({
        id: 'plan-error',
        severity: 'blocking',
        message: plan.error || 'Could not load blueprint agents.',
        recovery: assistantProgram
          ? ['retry-plan', 'edit-blueprint']
          : ['retry-plan', 'edit-blueprint', 'exclude-blueprint-team'],
        anchor: 'team-roster'
      });
    }
    if (assistantProgram) {
      const nameProblem = assistantNameProblem(source.assistantHire?.name);
      if (nameProblem) {
        issues.push({
          id: 'assistant-name',
          severity: 'blocking',
          message: nameProblem,
          recovery: [],
          anchor: 'assistantProgramCreateName'
        });
      }
    }
    if (collisions.length > 0) {
      issues.push({
        id: 'duplicate-names',
        severity: 'blocking',
        message: `More than one agent would be named “${collisions[0].name}”. Give the customized copy a different name.`,
        recovery: ['edit-customization'],
        anchor:
          collisions[0].source === 'assistant-program'
            ? 'assistantProgramCreateName'
            : collisions[0].templateAgentIndex === null
              ? 'team-roster'
              : `team-agent-name-${collisions[0].templateAgentIndex}`,
        templateAgentIndex: collisions[0].templateAgentIndex
      });
    }
    if (text(source.explicitPrimary) && !primary) {
      issues.push({
        id: 'invalid-primary',
        severity: 'blocking',
        message: `${source.explicitPrimary} is no longer part of this team, so it cannot be the primary agent.`,
        recovery: ['choose-primary'],
        anchor: 'team-roster'
      });
    }
    if (!assistantProgram && source.savedRoster.status === PLAN_ERROR) {
      // Advisory: an already-valid team can still be created (FR66, FR96).
      issues.push({
        id: 'saved-roster-error',
        severity: 'advisory',
        message: source.savedRoster.error,
        recovery: ['retry-saved-roster'],
        anchor: 'saved-agent-picker'
      });
    }
    if (!assistantProgram && plan.status !== PLAN_LOADING && roster.length === 0) {
      // Intentionally allowed (FR55) — prominent, but never blocking.
      issues.push({
        id: 'empty-team',
        severity: 'advisory',
        message:
          'No agent will be attached to this workspace. Starter and setup tasks may remain unassigned until you add one.',
        recovery: ['add-saved-agent', 'include-blueprint-team'],
        anchor: 'team-roster'
      });
    }
    shadowedSelections.forEach(shadowed => {
      issues.push({
        id: 'shadowed-selection',
        severity: 'advisory',
        message: `${shadowed.name} is already included by this blueprint, so it is attached once — the blueprint owns that roster entry.`,
        recovery: [],
        anchor: 'team-roster'
      });
    });

    const payload = {};
    if (assistantProgram) {
      payload.assistant_hire = {
        name: text(source.assistantHire?.name),
        provider: text(source.assistantHire?.provider),
        model: text(source.assistantHire?.model)
      };
    } else {
      if (allPlanAgents.length > 0) payload.create_template_agents = includeTeam;
      if (includeTeam) {
        const overrides = serializeOverrides(source);
        if (overrides.length > 0) payload.template_agent_overrides = overrides;
      }
      // Sent only when non-empty: the server treats a nil existing_agent_names as
      // a legacy request and keeps its original entry-agent behavior.
      if (savedEntries.length > 0) {
        payload.existing_agent_names = savedEntries.map(entry => entry.name);
        const savedPrimary = savedEntries.find(entry => entry.key === primaryKey);
        if (savedPrimary) payload.entry_agent_name = savedPrimary.name;
      }
    }

    const planForSummary = plan.data;
    const summaryEntries = assistantProgram ? blueprintEntries : allPlanAgents;
    return {
      planStatus: plan.status,
      planError: plan.error || '',
      includeBlueprintTeam: includeTeam,
      // Describes what the BLUEPRINT provides, independent of whether its team
      // is currently included, because step 1 previews the blueprint itself.
      blueprintSummary: {
        status: plan.status,
        hasAgents: Boolean(assistantProgram || (planForSummary && planForSummary.hasAgents)),
        count: summaryEntries.length,
        names: summaryEntries.map(agent => agent.name),
        declaredPrimary: assistantProgram
          ? (summaryEntries.find(agent => agent.declaredPrimary) || {}).name || ''
          : (planForSummary && planForSummary.declaredPrimary) ||
            (allPlanAgents.find(agent => agent.entryPoint) || {}).name ||
            '',
        isEmpty: plan.status === PLAN_READY && summaryEntries.length === 0,
        templateName: (planForSummary && planForSummary.templateName) || '',
        warnings: (planForSummary && planForSummary.warnings) || []
      },
      assistantProgram,
      assistantHire: assistantProgram
        ? { ...(source.assistantHire || emptyAssistantHire()) }
        : null,
      isAssistantProgram: Boolean(assistantProgram),
      roster,
      primaryName: primary ? primary.name : '',
      primaryIsAutomatic: Boolean(primary) && !text(source.explicitPrimary),
      specialists,
      shadowedSelections,
      issues,
      blockingIssues: issues.filter(issue => issue.severity === 'blocking'),
      advisoryIssues: issues.filter(issue => issue.severity === 'advisory'),
      isLoading: plan.status === PLAN_LOADING,
      canContinueFromTeam: issues.every(issue => issue.severity !== 'blocking'),
      // Inherited/default model use is informational, never a warning or a
      // blocker (FR97) — surfacing it as an issue would flag the Blank happy
      // path, which is exactly the confusion FR92 guards against.
      inheritedModelNote: assistantProgram
        ? text(source.assistantHire?.provider)
          ? `Assistant roles use the selected provider’s default model unless a model is chosen.`
          : 'Assistant roles use Ori’s default provider and model.'
        : planForSummary
          ? planForSummary.systemModelConfigured
            ? `Agents without their own model use ${planForSummary.systemProvider} / ${planForSummary.systemModel}.`
            : 'Agents without their own model use the app default because no system model is configured.'
          : '',
      isModifiedFromBlueprint: assistantProgram
        ? !assistantProgram.existingHired &&
          (agentKey(source.assistantHire?.name) !== agentKey(assistantProgram.defaultPrimaryName) ||
            text(source.assistantHire?.provider) !== '' ||
            text(source.assistantHire?.model) !== '')
        : source.overrides.size > 0 ||
          source.savedSelections.length > 0 ||
          !includeTeam ||
          text(source.explicitPrimary) !== '',
      payload
    };
  }

  function toCreatePayload(draft) {
    return derive(draft).payload;
  }

  window.CreateWorkspaceTeamDraft = {
    PLAN_IDLE,
    PLAN_LOADING,
    PLAN_READY,
    PLAN_ERROR,
    LIFECYCLE_LABELS,
    createDraft,
    resetDraft,
    setPlanLoading,
    setPlanReady,
    setPlanError,
    clearPlan,
    normalizePlan,
    setIncludeBlueprintTeam,
    setAssistantHire,
    stageOverride,
    clearOverride,
    getOverride,
    setSavedRosterLoading,
    setSavedRosterReady,
    setSavedRosterError,
    addSavedAgent,
    removeSavedAgent,
    setExplicitPrimary,
    isSelected,
    isBlueprintOwned,
    isAttachableSavedAgent,
    findSavedAgent,
    identityFrom,
    agentKey,
    derive,
    toCreatePayload
  };
})();
