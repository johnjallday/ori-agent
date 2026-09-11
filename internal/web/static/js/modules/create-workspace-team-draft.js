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

  function normalizeRecommendedSetup(setup) {
    if (!setup || typeof setup !== 'object') return null;
    return {
      role: text(setup.role),
      type: text(setup.type),
      model: text(setup.model),
      provider: text(setup.provider),
      reasoningEffort: text(setup.reasoning_effort),
      systemPrompt: text(setup.system_prompt),
      appearance: setup.appearance || null,
      modelSource: text(setup.model_source),
      tools: setup.tools || null
    };
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
      reasoningEffort: text(agent && agent.reasoning_effort),
      systemPrompt: text(agent && agent.system_prompt),
      appearance: (agent && agent.appearance) || null,
      modelSource: text(agent && agent.model_source),
      tools: (agent && agent.tools) || null,
      warning: text(agent && agent.warning),
      recommended: normalizeRecommendedSetup(agent && agent.recommended_setup)
    };
  }

  function stableValue(value) {
    if (Array.isArray(value)) return value.map(stableValue);
    if (!value || typeof value !== 'object') return value;
    return Object.keys(value)
      .sort()
      .reduce((result, key) => {
        result[key] = stableValue(value[key]);
        return result;
      }, {});
  }

  // Acknowledgement belongs to the definition the person actually reviewed,
  // not merely to an array slot. This lets a same-blueprint refresh retain a
  // review only while every visible part of that entry is unchanged.
  function planAgentIdentity(agent) {
    return JSON.stringify(stableValue(agent || null));
  }

  function normalizeAssistantProgram(program) {
    if (!program || typeof program !== 'object') return null;
    // EVERY declared role reaches the Team step, optional ones included (FR9,
    // FR10). Filtering out `required !== false` here is what made optional roles
    // invisible in the wizard — the user could neither see them nor fill them,
    // and the only way to get one was a separate setup action afterwards.
    const roles = (Array.isArray(program.roles) ? program.roles : [])
      .map(role => ({
        id: text(role && role.id),
        label: text(role && role.label),
        description: text(role && role.description),
        primary: Boolean(role && role.primary),
        scope: text(role && role.scope),
        required: role?.required === true,
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
    const namedPrimary =
      roles.find(role => role.primary && role.scope === 'home') || roles.find(role => role.primary);
    return {
      id,
      stationName: text(program.station_name),
      stationWorkspaceSlug: text(program.station_workspace_slug),
      stationDescription: text(program.station_description),
      defaultPrimaryName: text(program.default_primary_name),
      hireTitle: text(program.hire_title),
      hireDescription: text(program.hire_description),
      existingHired: Boolean(program.existing_hired),
      existingProvider: text(program.existing_provider),
      existingModel: text(program.existing_model),
      roles,
      homeAlreadyStaffed: Boolean(namedPrimary?.scope === 'home' && namedPrimary.agentName),
      namedPrimaryID: namedPrimary?.id || '',
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
      revision: text(data && data.revision),
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

  // ---- Role vacancies -------------------------------------------------------
  //
  // A blueprint declares roles; a role is a slot. Which slots the user has
  // filled lives HERE, in the draft, and reaches every surface through
  // derive(). There is deliberately no second copy: the Team roster, the Review
  // receipt, and the create request all read the same projection, which is the
  // whole reason this module exists.

  const FILL_CREATE = 'create';
  const FILL_ASSIGN = 'assign';

  // roleIdFromName mirrors projecttemplates.AgentRoleID exactly. An ordinary
  // blueprint's roster is a list of agents, not slots, so its role identity is
  // derived from the name on both sides — the wizard sends a role_id the server
  // must re-derive to the same string, or the fill binds nothing.
  function roleIdFromName(name) {
    let slug = '';
    let lastHyphen = true;
    for (const char of text(name).toLocaleLowerCase()) {
      if ((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
        slug += char;
        lastHyphen = false;
      } else if (!lastHyphen) {
        slug += '-';
        lastHyphen = true;
      }
    }
    slug = slug.replace(/^-+|-+$/g, '');
    if (slug.length > 80) slug = slug.slice(0, 80).replace(/-+$/, '');
    return slug || 'role';
  }

  // declaredRoles is the one place either blueprint kind becomes a list of
  // slots, so the Team step renders one roster whichever kind the user picked
  // (FR20).
  //
  // An ordinary blueprint declares no requiredness, so it follows the roster
  // contract templates already have: the first entry is the workspace's entry
  // agent and the rest are specialists. First role primary and required, the
  // rest optional — calling four specialists "Missing" would read as four
  // failures for a team the user never asked for.
  function declaredRoles(source) {
    const plan = source.plan || {};
    const program = plan.data?.assistantProgram || null;
    if (program) {
      return program.roles.map(role => ({
        roleId: role.id,
        label: role.label,
        description: role.description,
        scope: role.scope === 'home' ? 'home' : 'project',
        required: role.required,
        primary: Boolean(role.primary),
        // A home role an existing station already staffs is shown with its
        // holder and cannot be changed from here (D2).
        heldElsewhere: role.scope === 'home' ? role.agentName : ''
      }));
    }
    const seen = new Map();
    return planAgents(source).map((agent, index) => {
      const base = roleIdFromName(agent.name);
      let roleId = base;
      for (;;) {
        const count = seen.get(roleId) || 0;
        seen.set(roleId, count + 1);
        if (count === 0) break;
        roleId = base + '-' + (count + 1);
      }
      // The blueprint's own proposal for this role. It mirrors the server's
      // ProposedSetup so the Team step and the workspace roster seed the
      // Create form from the same shape.
      const recommended = agent.recommended || agent;
      return {
        roleId,
        label: agent.name,
        description: agent.systemPrompt ? firstSentence(agent.systemPrompt) : '',
        scope: 'project',
        required: index === 0,
        primary: index === 0,
        heldElsewhere: '',
        templateAgentIndex: index,
        proposed: {
          type: text(recommended.type),
          model: text(recommended.model),
          provider: text(recommended.provider),
          system_prompt: text(recommended.systemPrompt),
          ...(recommended.tools ? { tools: recommended.tools } : {})
        }
      };
    });
  }

  function firstSentence(value) {
    const flat = text(value).replace(/\s+/g, ' ');
    const end = flat.search(/[.!?]/);
    if (end > 0 && end + 1 <= 160) return flat.slice(0, end + 1);
    return flat.length <= 160 ? flat : flat.slice(0, 160).replace(/\s\S*$/, '') + '…';
  }

  function normalizeFill(fill) {
    if (!fill || typeof fill !== 'object') return null;
    const mode = text(fill.mode) === FILL_ASSIGN ? FILL_ASSIGN : FILL_CREATE;
    const name = text(fill.name);
    if (!name) return null;
    // An assigned agent keeps its own definition entirely, so a fill that binds
    // one carries nothing but its name.
    if (mode === FILL_ASSIGN) return { mode, name };
    return {
      mode,
      name,
      provider: text(fill.provider),
      model: text(fill.model),
      // Carried so an edit made in the Create form reaches the created agent.
      // Empty means "use what the blueprint proposes".
      type: text(fill.type),
      systemPrompt: text(fill.systemPrompt)
    };
  }

  // setRoleFill records that a role will be filled — by creating an agent for
  // it, or by assigning one the user already has. It never performs I/O; the
  // fill becomes a request only at submit. Every precondition is rechecked at
  // action time so a stale suggestion cannot move an agent, replace a role, or
  // bind a declaration that changed after it was rendered.
  function setRoleFill(draft, roleId, fill) {
    const id = text(roleId);
    const normalized = normalizeFill(fill);
    if (!draft || !id || !normalized) return false;
    const role = declaredRoles(draft).find(item => item.roleId === id);
    if (!role || role.scope === 'home' || role.heldElsewhere || draft.roleFills?.has(id))
      return false;
    if (!draft.roleFills) draft.roleFills = new Map();

    const wanted = agentKey(normalized.name);
    for (const other of draft.roleFills.values()) {
      if (agentKey(other.name) === wanted) return false;
    }
    if (
      declaredRoles(draft).some(
        item => item.heldElsewhere && agentKey(item.heldElsewhere) === wanted
      )
    ) {
      return false;
    }
    if (normalized.mode === FILL_CREATE && findSavedAgent(draft, normalized.name)) return false;
    if (normalized.mode === FILL_ASSIGN) {
      const saved = findSavedAgent(draft, normalized.name);
      if (!saved || !isAttachableSavedAgent(saved)) return false;
      normalized.name = text(saved.name);
      // A role-specific action deliberately converts an extra teammate into a
      // role holder. Keeping both would attach the same definition twice.
      draft.savedSelections = (draft.savedSelections || []).filter(
        name => agentKey(name) !== wanted
      );
    }
    draft.roleFills.set(id, normalized);
    draft.agentless = false;
    clearResolvedReadinessConflict(draft);
    return true;
  }

  function setAgentless(draft, enabled) {
    if (!draft) return false;
    const blank = draft.plan?.status === PLAN_READY && draft.plan.data?.templateId === 'blank';
    if (enabled && !blank) return false;
    draft.agentless = Boolean(enabled);
    if (draft.agentless) {
      draft.roleFills.clear();
      draft.savedSelections = [];
      draft.explicitPrimary = '';
      draft.overrides.clear();
      draft.acknowledgements.clear();
    }
    clearResolvedReadinessConflict(draft);
    return true;
  }

  function clearResolvedReadinessConflict(draft) {
    if (draft?.staleConflict?.kind !== 'team-readiness') return;
    const ready = declaredRoles(draft)
      .filter(role => role.required)
      .every(role => role.heldElsewhere || draft.roleFills?.has(role.roleId));
    if (ready || draft.agentless) draft.staleConflict = null;
  }

  function clearRoleFill(draft, roleId) {
    const id = text(roleId);
    if (!draft || !id || !draft.roleFills) return false;
    return draft.roleFills.delete(id);
  }

  function getRoleFill(draft, roleId) {
    const fill = draft && draft.roleFills && draft.roleFills.get(text(roleId));
    return fill ? { ...fill } : null;
  }

  // roleFilledBy reports which role an agent name currently fills, so every
  // picker appearance shares one eligibility decision. Inherited group holders
  // count too: they cannot also be attached as a project extra.
  function roleFilledBy(source, name) {
    const key = agentKey(name);
    if (!key) return '';
    for (const [roleId, fill] of source.roleFills || []) {
      if (agentKey(fill.name) === key) return roleId;
    }
    const inherited = declaredRoles(source).find(
      role => role.heldElsewhere && agentKey(role.heldElsewhere) === key
    );
    return inherited ? inherited.roleId : '';
  }

  function createDraft() {
    return {
      plan: { status: PLAN_IDLE, blueprintKey: '', data: null, error: '' },
      includeBlueprintTeam: true,
      overrides: new Map(),
      overrideIdentities: new Map(),
      acknowledgements: new Map(),
      reconciledPlanChanges: new Set(),
      staleConflict: null,
      creationFailures: new Map(),
      assistantHire: emptyAssistantHire(),
      // Which declared roles the user has chosen to fill. Empty is the correct
      // starting state and a valid end state (FR7, FR11).
      roleFills: new Map(),
      savedSelections: [],
      savedRoster: { status: PLAN_IDLE, agents: [], error: '' },
      explicitPrimary: '',
      agentless: false
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
    draft.overrideIdentities = new Map();
    draft.acknowledgements = new Map();
    draft.reconciledPlanChanges = new Set();
    draft.staleConflict = null;
    draft.creationFailures = new Map();
    draft.assistantHire = emptyAssistantHire();
    // Role ids belong to the blueprint that declared them, so a different
    // blueprint's fills would bind to slots that no longer exist.
    draft.roleFills = new Map();
    draft.includeBlueprintTeam = true;
    draft.agentless = false;
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
    const changedIndexes = new Set();
    for (const [index, acknowledgement] of draft.acknowledgements || []) {
      const nextAgent = normalized.agents[index];
      if (!nextAgent || acknowledgement.identity !== planAgentIdentity(nextAgent)) {
        if (nextAgent) changedIndexes.add(index);
        draft.acknowledgements.delete(index);
      }
    }
    for (const [index, identity] of draft.overrideIdentities || []) {
      const nextAgent = normalized.agents[index];
      if (!nextAgent || identity !== planAgentIdentity(nextAgent)) {
        if (nextAgent) changedIndexes.add(index);
        draft.overrides.delete(index);
        draft.overrideIdentities.delete(index);
      }
    }
    draft.reconciledPlanChanges = changedIndexes;
    draft.plan = { status: PLAN_READY, blueprintKey: key, data: normalized, error: '' };
    const currentRoles = new Map(declaredRoles(draft).map(role => [role.roleId, role]));
    for (const roleID of draft.roleFills.keys()) {
      const role = currentRoles.get(roleID);
      if (!role || role.heldElsewhere) draft.roleFills.delete(roleID);
    }
    const program = normalized.assistantProgram;
    if (program) {
      const programKey = `${key}:${program.id}`;
      if (!draft.assistantHire || draft.assistantHire.programKey !== programKey) {
        const existingPrimary = program.roles.find(
          role => role.id === program.namedPrimaryID
        )?.agentName;
        // The primary role starts EMPTY like every other one (FR11). The name
        // is prefilled only when a station already holds this role — there the
        // agent exists and the wizard is reporting it, not proposing it.
        //
        // Prefilling defaultPrimaryName for a fresh station is what made "pick
        // a blueprint" mean "and here are four agents you did not ask for":
        // the wizard filled the slot before the user could look at it.
        const alreadyStaffed = program.existingHired || program.homeAlreadyStaffed;
        draft.assistantHire = {
          programKey,
          name: alreadyStaffed ? existingPrimary || program.defaultPrimaryName : '',
          provider: alreadyStaffed ? program.existingProvider : '',
          model: alreadyStaffed ? program.existingModel : ''
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
      if (field === 'name' && program.homeAlreadyStaffed) continue;
      if (has(fields, field)) next[field] = text(fields[field]);
    }
    draft.assistantHire = next;
    return draft;
  }

  function markPlanConflict(draft, message, details = {}) {
    if (!draft) return false;
    const changedIndexes = new Set(draft.reconciledPlanChanges || []);
    const index = Number(details.index);
    if (Number.isInteger(index) && planAgentAt(draft, index)) changedIndexes.add(index);
    const occupiedNames = new Map();
    if (
      Number.isInteger(index) &&
      text(details.expected_action) === 'create' &&
      text(details.actual_action) === 'reuse'
    ) {
      occupiedNames.set(index, text(details.name));
    }
    draft.staleConflict = {
      message: text(message) || 'Blueprint changed—review again before creating the workspace.',
      kind: details.kind === 'team-readiness' ? 'team-readiness' : 'plan',
      changedIndexes,
      occupiedNames
    };
    return true;
  }

  function confirmFreshPlan(draft) {
    if (!draft || !draft.staleConflict) return false;
    if (draft.staleConflict.kind === 'team-readiness') {
      const ready = declaredRoles(draft)
        .filter(role => role.required)
        .every(role => role.heldElsewhere || draft.roleFills?.has(role.roleId));
      if (!ready && !draft.agentless) return false;
      draft.staleConflict = null;
      return true;
    }
    const pending = planAgents(draft).some(
      (agent, index) => agent.action === 'create' && !isSetupAcknowledged(draft, index, agent)
    );
    const changed = (draft.staleConflict.changedIndexes?.size || 0) > 0;
    if (draft.includeBlueprintTeam && (pending || changed)) return false;
    draft.staleConflict = null;
    return true;
  }

  function reviewChangedEntry(draft, index) {
    if (!draft?.staleConflict?.changedIndexes?.has(index)) return false;
    draft.staleConflict.changedIndexes.delete(index);
    draft.staleConflict.occupiedNames?.delete(index);
    return true;
  }

  function markCreationFailure(draft, index, name, message) {
    if (!draft || !Number.isInteger(index) || !planAgentAt(draft, index)) return false;
    if (!draft.creationFailures) draft.creationFailures = new Map();
    draft.creationFailures.set(index, {
      name: text(name),
      message: text(message) || `Agent ${text(name)} could not be created.`
    });
    return true;
  }

  function clearCreationFailure(draft, index) {
    return Boolean(draft?.creationFailures?.delete(index));
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
    if (!draft || !included) return false;
    draft.includeBlueprintTeam = true;
    return true;
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
    if (Object.keys(next).length === 0) {
      draft.overrides.delete(index);
      draft.overrideIdentities?.delete(index);
    } else {
      draft.overrides.set(index, next);
      if (!draft.overrideIdentities) draft.overrideIdentities = new Map();
      draft.overrideIdentities.set(index, planAgentIdentity(planAgent));
    }
    return draft;
  }

  function clearOverride(draft, index) {
    if (draft && draft.overrides) draft.overrides.delete(index);
    if (draft && draft.overrideIdentities) draft.overrideIdentities.delete(index);
    return draft;
  }

  function acknowledgeSetup(draft, index, provenance) {
    const planAgent = planAgentAt(draft, index);
    if (!draft || !planAgent) return false;
    if (!draft.acknowledgements) draft.acknowledgements = new Map();
    draft.acknowledgements.set(index, {
      identity: planAgentIdentity(planAgent),
      provenance: provenance === 'batch' ? 'batch' : 'individual'
    });
    reviewChangedEntry(draft, index);
    clearCreationFailure(draft, index);
    return true;
  }

  function isSetupAcknowledged(draft, index, planAgent) {
    const acknowledgement = draft && draft.acknowledgements?.get(index);
    return Boolean(acknowledgement && acknowledgement.identity === planAgentIdentity(planAgent));
  }

  function acceptRecommended(draft, index) {
    return acknowledgeSetup(draft, index, 'individual');
  }

  function resetToRecommended(draft, index) {
    const planAgent = planAgentAt(draft, index);
    if (!draft || !planAgent) return false;
    draft.overrides.delete(index);
    draft.overrideIdentities?.delete(index);
    return acknowledgeSetup(draft, index, 'individual');
  }

  function acceptAllRecommended(draft) {
    if (!draft || !draft.includeBlueprintTeam) return 0;
    let accepted = 0;
    planAgents(draft).forEach((planAgent, index) => {
      if (planAgent.action !== 'create' || isSetupAcknowledged(draft, index, planAgent)) return;
      if (draft.overrides?.has(index)) return;
      if (acknowledgeSetup(draft, index, 'batch')) accepted += 1;
    });
    return accepted;
  }

  function undoBatchRecommended(draft) {
    if (!draft || !draft.acknowledgements) return 0;
    let undone = 0;
    for (const [index, acknowledgement] of draft.acknowledgements) {
      if (acknowledgement.provenance !== 'batch' || draft.overrides?.has(index)) continue;
      draft.acknowledgements.delete(index);
      undone += 1;
    }
    return undone;
  }

  // Saves one editor as an authoritative replacement, rather than merging it
  // into an older edit. Values equal to the current plan are intentionally
  // omitted so the request carries only real changes, while acknowledgement is
  // retained even when the recommendation is accepted unchanged.
  function saveSetup(draft, index, fields) {
    const planAgent = planAgentAt(draft, index);
    if (!draft || !planAgent || !fields) return false;

    const next = {};
    Object.keys(OVERRIDE_FIELDS).forEach(field => {
      if (!has(fields, field)) return;
      const value = text(fields[field]);
      const original = text(planAgent[field]);
      const sameName =
        field === 'name' && planAgent.action === 'reuse'
          ? agentKey(value) === agentKey(original)
          : value === original;
      if (!sameName) next[field] = value;
    });
    if (Object.keys(next).length > 0) {
      draft.overrides.set(index, next);
      if (!draft.overrideIdentities) draft.overrideIdentities = new Map();
      draft.overrideIdentities.set(index, planAgentIdentity(planAgent));
    } else {
      draft.overrides.delete(index);
      draft.overrideIdentities?.delete(index);
    }
    return acknowledgeSetup(draft, index, 'individual');
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

  // True when the blueprint proposes this name under its ORIGINAL identity.
  // This is declaration metadata, not membership: a proposed Downloads Curator
  // does not fill its role merely because a saved agent has the same name.
  // Retained only for the legacy whole-roster reconciliation path.
  function isBlueprintOwned(draft, name) {
    const key = agentKey(name);
    return planAgents(draft).some(agent => agentKey(agent.name) === key);
  }

  function isActuallyIncluded(draft, name) {
    return Boolean(isSelected(draft, name) || roleFilledBy(draft, name));
  }

  function matchText(value) {
    return text(value)
      .toLocaleLowerCase()
      .replace(/[^a-z0-9]+/g, ' ')
      .trim()
      .replace(/\s+/g, ' ');
  }

  function recommendationMatch(agent, role) {
    const label = matchText(role && role.label);
    const name = matchText(agent && agent.name);
    const savedRole = matchText(agent && agent.role);
    if (!label || !name) return null;
    if (name === label) return { rank: 0, kind: 'exact-name', reason: "Matches this role's name" };
    if (savedRole && savedRole === label) {
      return { rank: 1, kind: 'exact-role', reason: "Matches this saved agent's role label" };
    }
    if (name.includes(label) || label.includes(name)) {
      return { rank: 2, kind: 'name-text', reason: "Matches text in this role's name" };
    }
    if (savedRole && (savedRole.includes(label) || label.includes(savedRole))) {
      return {
        rank: 3,
        kind: 'role-text',
        reason: "Matches text in this saved agent's role label"
      };
    }
    return null;
  }

  // Pure recommendation projection. It owns no mutable roster and makes no
  // request: suggestions are a role/name view over the current declarations,
  // fills, explicit extras, and attachable saved definitions.
  function recommendSavedAgents(source, roster) {
    const roles = (roster?.roles || [])
      .map((role, index) => ({ ...role, declarationIndex: index }))
      .filter(role => role.state !== 'filled' && !role.read_only)
      .sort((left, right) => {
        const bucket = role => (role.primary ? 0 : role.required ? 1 : 2);
        return bucket(left) - bucket(right) || left.declarationIndex - right.declarationIndex;
      });
    const agents = ((source.savedRoster && source.savedRoster.agents) || []).filter(
      agent => isAttachableSavedAgent(agent) && !isActuallyIncluded(source, agent.name)
    );
    const out = [];
    for (const role of roles) {
      const matches = agents
        .map(agent => ({ agent, match: recommendationMatch(agent, role) }))
        .filter(item => item.match)
        .sort(
          (left, right) =>
            left.match.rank - right.match.rank ||
            text(left.agent.name).localeCompare(text(right.agent.name), undefined, {
              sensitivity: 'base'
            })
        );
      for (const item of matches) {
        out.push({
          roleId: role.role_id,
          roleLabel: role.label,
          roleRequired: Boolean(role.required),
          rolePrimary: Boolean(role.primary),
          agent: item.agent,
          matchKind: item.match.kind,
          reason: item.match.reason
        });
      }
    }
    return out;
  }

  function setSavedRosterLoading(draft) {
    draft.savedRoster = { status: PLAN_LOADING, agents: draft.savedRoster.agents || [], error: '' };
    return draft;
  }

  function setSavedRosterReady(draft, agents) {
    const list = (Array.isArray(agents) ? agents : []).filter(agent => text(agent && agent.name));
    draft.savedRoster = { status: PLAN_READY, agents: list, error: '' };
    for (const [roleID, fill] of draft.roleFills || []) {
      if (fill.mode !== FILL_ASSIGN) continue;
      const saved = findSavedAgent(draft, fill.name);
      if (!saved || !isAttachableSavedAgent(saved)) draft.roleFills.delete(roleID);
    }
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
    // A blueprint name is only a proposal. Actual role fills and explicit extras
    // are the membership states that make Add unavailable.
    if (isSelected(draft, canonical) || roleFilledBy(draft, canonical)) return false;
    draft.savedSelections = [...draft.savedSelections, canonical];
    draft.agentless = false;
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
    if (declaredRoles(draft).length > 0) return false;
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
    const renamed = agentKey(name) !== agentKey(planAgent.name);
    const recommended = planAgent.recommended || planAgent;
    const definition = planAgent.action === 'reuse' && renamed ? recommended : planAgent;
    const model = has(override, 'model') ? override.model : definition.model;
    const provider = has(override, 'provider') ? override.provider : definition.provider;
    const customized = Object.keys(override).length > 0;
    const acknowledged = isSetupAcknowledged(draft, index, planAgent);
    const planChanged = Boolean(draft.staleConflict?.changedIndexes?.has(index));
    const creationFailure = draft.creationFailures?.get(index) || null;
    const staleOccupiedName = draft.staleConflict?.occupiedNames?.get(index) || '';
    // A reused definition only becomes a separate copy once it is renamed; an
    // unrenamed reuse row stays a plain attachment of the shared agent (FR41).
    const lifecycle =
      planAgent.action === 'reuse' ? (renamed ? 'customized-copy' : 'reuse') : 'create';
    return {
      key: agentKey(name),
      name,
      source: 'blueprint',
      identity: identityFrom(
        name,
        planAgent.action === 'reuse' && !renamed
          ? findSavedAgent(draft, name) || {
              role: planAgent.role,
              appearance: planAgent.appearance
            }
          : {
              role: definition.role,
              appearance: definition.appearance
            }
      ),
      lifecycle,
      lifecycleLabel: LIFECYCLE_LABELS[lifecycle],
      setupState: creationFailure
        ? 'missing'
        : planChanged
          ? 'changed'
          : planAgent.action === 'create' && !acknowledged
            ? 'needsSetup'
            : 'ready',
      statusLabel: creationFailure
        ? 'Missing · Creation failed'
        : planChanged
          ? 'Changed · Review setup'
          : planAgent.action === 'reuse'
            ? renamed
              ? 'Customized copy · Will be created with workspace'
              : 'Saved · Ready to attach'
            : !acknowledged
              ? 'New · Needs setup'
              : customized
                ? 'Customized · Will be created with workspace'
                : 'Ready · Will be created with workspace',
      actionLabel: creationFailure
        ? 'Retry'
        : planChanged
          ? 'Review setup'
          : planAgent.action === 'reuse' && !renamed
            ? 'Customize as new agent'
            : planAgent.action === 'create' && !acknowledged
              ? 'Set up agent'
              : 'Edit setup',
      setupAcknowledged: acknowledged,
      planChanged,
      creationFailure,
      staleOccupiedName,
      sourceLabel: planAgent.action === 'reuse' && !renamed ? 'Your Agents' : 'Blueprint',
      readinessLabel: creationFailure
        ? 'Missing'
        : planChanged
          ? 'Changed'
          : planAgent.action === 'create' && !acknowledged
            ? 'Needs setup'
            : customized
              ? 'Customized'
              : 'Ready',
      futureActionLabel:
        planAgent.action === 'reuse' && !renamed
          ? 'Will attach saved definition'
          : 'Will create with workspace',
      model,
      provider,
      modelLabel: modelLabel(model, provider),
      modelSourceLabel: has(override, 'model')
        ? text(override.model)
          ? 'Custom model'
          : 'Default model'
        : modelSourceLabel(definition.modelSource),
      inheritsModel: text(model) === '',
      role: definition.role,
      type: has(override, 'type') ? override.type : definition.type,
      reasoningEffort: definition.reasoningEffort,
      systemPrompt: has(override, 'systemPrompt') ? override.systemPrompt : definition.systemPrompt,
      appearance: definition.appearance,
      tools: definition.tools,
      recommended,
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

  function resolveAssistantEntries(source, program) {
    const hire = source.assistantHire || emptyAssistantHire();
    const roles = program.existingHired
      ? program.roles.filter(role => role.agentName)
      : program.roles;
    return roles.map(role => {
      const namedPrimary = role.id === program.namedPrimaryID;
      const existing = program.existingHired || (role.scope === 'home' && Boolean(role.agentName));
      // An unfilled role is described by its LABEL, primary included. The
      // primary used to be described by the hire name, which is now empty until
      // the user fills the slot (FR11) — without the fallback the roster would
      // show a blank row where a role should be.
      const fill = source.roleFills && source.roleFills.get(role.id);
      const name = existing
        ? role.agentName || text(hire.name) || role.label
        : text(fill && fill.name) || (namedPrimary ? text(hire.name) : '') || role.label;
      const lifecycle = existing ? 'assistant-link' : 'assistant-create';
      return {
        key: agentKey(name),
        name,
        source: 'assistant-program',
        identity: identityFrom(name, null),
        lifecycle,
        lifecycleLabel:
          role.scope === 'home'
            ? `${existing ? 'Existing' : 'New'} group coordination role · group scope only`
            : role.scope === 'project'
              ? 'New project role · this workspace only'
              : LIFECYCLE_LABELS[lifecycle],
        assistantScope: role.scope,
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
        declaredPrimary: role.primary && role.scope !== 'home',
        originalName: role.label,
        isCustomized:
          !program.existingHired &&
          namedPrimary &&
          agentKey(name) !== agentKey(program.defaultPrimaryName),
        customizable: false,
        removable: false,
        warning: ''
      };
    });
  }

  // An EMPTY name is not a problem: leaving the primary role unfilled is a
  // supported outcome, so it must never block creation (FR7, FR19). Only a name
  // the user actually typed can be malformed.
  function assistantNameProblem(name) {
    const normalized = text(name);
    if (!normalized) return '';
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

  // buildRoleRoster projects declared roles plus the user's fills into the same
  // wire shape the workspace's own roster endpoint returns, so the shared
  // component renders the wizard and the workspace from one contract (FR33).
  //
  // Nothing is persisted yet here — a "filled" row means "will be filled".
  function buildRoleRoster(source, savedAgentsByKey) {
    const roles = declaredRoles(source).map(role => {
      const item = {
        role_id: role.roleId,
        label: role.label,
        description: role.description,
        scope: role.scope,
        required: role.required,
        primary: role.primary,
        state: 'empty'
      };
      // Only an empty role has anything left to propose. Assistant-program
      // roles carry none: that declaration's prompts stay server-side.
      if (role.proposed) item.proposed = role.proposed;
      // Every group-scoped role is read-only here: an existing holder is
      // reported, while a vacancy remains an actionable blocker owned by the
      // group workspace rather than an impossible project-side field (D2).
      if (role.scope === 'home') {
        item.read_only = true;
        item.read_only_reason = GROUP_ROLE_READ_ONLY;
        if (role.heldElsewhere) {
          item.state = 'filled';
          item.source = SOURCE_GROUP_WIRE;
          item.agent = { name: role.heldElsewhere };
        }
        return item;
      }
      const fill = source.roleFills && source.roleFills.get(role.roleId);
      if (fill) {
        const saved = savedAgentsByKey.get(agentKey(fill.name));
        item.state = 'filled';
        item.source = fill.mode === FILL_ASSIGN ? SOURCE_ASSIGNED_WIRE : SOURCE_CREATED_WIRE;
        item.agent = {
          name: fill.name,
          role: saved ? text(saved.role) : '',
          type: saved ? text(saved.type) : '',
          appearance: (saved && saved.appearance) || null
        };
        // A filled role has nothing left to propose.
        delete item.proposed;
      }
      return item;
    });
    const filled = roles.filter(role => role.state === 'filled');
    return {
      roles,
      filled_count: filled.length,
      total_count: roles.length,
      // Counted separately because only these two produce a request: a role
      // held by the group station is neither created nor attached here.
      created_count: roles.filter(
        role => role.state === 'filled' && !role.read_only && role.source === SOURCE_CREATED_WIRE
      ).length,
      assigned_count: roles.filter(
        role => role.state === 'filled' && !role.read_only && role.source === SOURCE_ASSIGNED_WIRE
      ).length,
      empty_count: roles.length - filled.length
    };
  }

  const SOURCE_CREATED_WIRE = 'created';
  const SOURCE_ASSIGNED_WIRE = 'assigned';
  const SOURCE_GROUP_WIRE = 'group';
  const GROUP_ROLE_READ_ONLY = 'This role belongs to the group workspace. Fill or clear it there.';

  // roleStaffingSummary states the request in FUTURE tense and counts every
  // outcome, so the Team summary and the Review receipt can be the same
  // sentence and cannot drift apart (FR17, FR18, FR30).
  function roleStaffingSummary(roster) {
    if (!roster || roster.total_count === 0) return '';
    const required = roster.roles.filter(role => role.required);
    const requiredFilled = required.filter(role => role.state === 'filled').length;
    const parts = [
      `${requiredFilled} of ${required.length} required role${required.length === 1 ? '' : 's'} filled`
    ];
    if (roster.created_count > 0) {
      parts.push(
        `${roster.created_count} new agent${roster.created_count === 1 ? '' : 's'} will be created`
      );
    }
    const savedCount = roster.assigned_count + (roster.unassigned?.length || 0);
    if (savedCount > 0) {
      parts.push(`${savedCount} saved agent${savedCount === 1 ? '' : 's'} will be attached`);
    }
    if (roster.empty_count > 0) {
      parts.push(
        `${roster.empty_count} role${roster.empty_count === 1 ? '' : 's'} will stay empty`
      );
    }
    return parts.join(' · ') + '.';
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

    const savedAgentsByKey = new Map(
      ((source.savedRoster && source.savedRoster.agents) || []).map(agent => [
        agentKey(agent && agent.name),
        agent
      ])
    );
    const roleRoster = includeTeam
      ? buildRoleRoster(source, savedAgentsByKey)
      : {
          roles: [],
          filled_count: 0,
          total_count: 0,
          created_count: 0,
          assigned_count: 0,
          empty_count: 0
        };
    const isBlank = plan.data?.templateId === 'blank';
    const agentless = Boolean(source.agentless && isBlank);

    // A retained saved selection that the (possibly changed) blueprint already
    // contributes stays selected but yields its roster slot to the blueprint
    // entry, and we report which source won so the UI can say so (FR23).
    const originalKeys = new Set(
      roleRoster.roles
        .filter(role => role.state === 'filled' && !role.read_only && role.agent)
        .map(role => agentKey(role.agent.name))
    );
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
    // Saved teammates that do not fill a declared role are still real members
    // of the resulting workspace. Put them in the shared roster projection so
    // Team and Review never make an explicit Add action disappear merely
    // because this blueprint also declares role slots.
    roleRoster.unassigned = savedEntries.map(entry => ({
      name: entry.name,
      role: entry.role || '',
      type: entry.type || '',
      appearance: entry.identity?.appearance || null
    }));
    const roleSummary = agentless
      ? 'No agents will be created or attached.'
      : roleStaffingSummary(roleRoster);
    const recommendations = agentless ? [] : recommendSavedAgents(source, roleRoster);

    const rosterPrimaryName = resolvePrimaryName(source, blueprintEntries, savedEntries);
    const primaryKey = agentKey(rosterPrimaryName);
    // Once a plan declares roles, only an actual local holder or explicitly
    // added teammate can be the workspace entry agent. A blueprint proposal is
    // a slot description, not membership, so it must never drive primary copy.
    const filledLocalPrimary = roleRoster.roles.find(
      role => role.state === 'filled' && !role.read_only && role.primary && role.agent
    );
    const firstFilledLocalRole = roleRoster.roles.find(
      role => role.state === 'filled' && !role.read_only && role.agent
    );
    const actualPrimaryName = agentless
      ? ''
      : filledLocalPrimary?.agent?.name ||
        firstFilledLocalRole?.agent?.name ||
        savedEntries[0]?.name ||
        '';
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
    const collisionCandidates =
      roleRoster.total_count > 0
        ? [
            ...roleRoster.roles
              .filter(role => role.state === 'filled' && role.agent)
              .map(role => ({
                name: role.agent.name,
                key: agentKey(role.agent.name),
                source: 'role',
                templateAgentIndex: null,
                isCustomized: role.source === SOURCE_CREATED_WIRE
              })),
            ...savedEntries
          ]
        : roster;
    const nameCounts = new Map();
    collisionCandidates.forEach(entry => {
      nameCounts.set(entry.key, (nameCounts.get(entry.key) || 0) + 1);
    });
    // Report the customized member first: it owns the name the user just typed,
    // so that is the field focus should land on (FR104).
    const collisions = collisionCandidates
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
    if (includeTeam && plan.status === PLAN_READY && !text(plan.data?.revision)) {
      issues.push({
        id: 'plan-revision-missing',
        severity: 'blocking',
        message: 'This blueprint agent plan is missing its review revision. Retry before creating.',
        recovery: ['retry-plan'],
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
        recovery: ['retry-plan', 'edit-blueprint'],
        anchor: 'team-roster'
      });
    }
    const pendingSetups = roster.filter(
      entry => entry.source === 'blueprint' && entry.setupState === 'needsSetup'
    );
    const changedEntry = roster.find(
      entry => entry.source === 'blueprint' && entry.setupState === 'changed'
    );
    const failedEntry = roster.find(
      entry => entry.source === 'blueprint' && entry.setupState === 'missing'
    );
    if (includeTeam && source.staleConflict) {
      issues.push({
        id: 'template-agent-plan-changed',
        severity: 'blocking',
        message: source.staleConflict.message,
        recovery: ['confirm-fresh-plan', 'retry-plan'],
        anchor: changedEntry
          ? `team-agent-setup-${changedEntry.templateAgentIndex}`
          : 'workspaceTeamIssues',
        templateAgentIndex: changedEntry?.templateAgentIndex ?? null
      });
    }
    if (failedEntry) {
      issues.push({
        id: 'template-agent-creation-failed',
        severity: 'blocking',
        message: failedEntry.creationFailure.message,
        recovery: ['retry-creation'],
        anchor: `team-agent-retry-${failedEntry.templateAgentIndex}`,
        templateAgentIndex: failedEntry.templateAgentIndex
      });
    }
    // "Set up these proposed agents first" belongs to the flow that created the
    // whole roster on submit. Under the vacancy model nothing is created unless
    // the user fills a role, and filling one configures it on its own row — so
    // demanding setup for agents that may never exist would block Review on
    // work the request will not do, and leaving roles empty must never block
    // (FR19).
    if (pendingSetups.length > 0 && roleRoster.total_count === 0) {
      issues.push({
        id: 'template-agent-setup-required',
        severity: 'blocking',
        message:
          pendingSetups.length === 1
            ? `Set up ${pendingSetups[0].name} before reviewing this workspace.`
            : `Set up ${pendingSetups.length} proposed agents before reviewing this workspace.`,
        recovery: ['review-template-agent-setup'],
        anchor: `team-agent-setup-${pendingSetups[0].templateAgentIndex}`,
        templateAgentIndex: pendingSetups[0].templateAgentIndex,
        count: pendingSetups.length
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
    const missingRequired = roleRoster.roles.filter(
      role => role.required && role.state !== 'filled'
    );
    if (!agentless && missingRequired.length > 0) {
      issues.push({
        id: 'required-roles-missing',
        severity: 'blocking',
        message:
          missingRequired.length === 1
            ? `Fill the required ${missingRequired[0].label} role before reviewing this workspace.`
            : `Fill all ${missingRequired.length} required roles before reviewing this workspace.`,
        recovery: ['fill-required-role'],
        anchor: `workspace-role-${missingRequired[0].role_id}`,
        roleId: missingRequired[0].role_id
      });
    }
    if (agentless) {
      issues.push({
        id: 'agentless-workspace',
        severity: 'advisory',
        message:
          'This Blank workspace will be created without agents. Chat and agent work require adding one later.',
        recovery: [],
        anchor: 'workspaceBlankAgentlessToggle'
      });
    } else if (
      plan.status !== PLAN_LOADING &&
      roleRoster.total_count === 0 &&
      savedEntries.length === 0
    ) {
      issues.push({
        id: 'empty-team',
        severity: 'advisory',
        message: 'This blueprint declares no agent roles. You can still add a saved teammate.',
        recovery: ['add-saved-agent'],
        anchor: 'saved-agent-picker'
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
    // Every non-import wizard create carries an explicit versioned intent and
    // an explicit staffing array. API callers that omit team_intent still use
    // the legacy behavior; the wizard never relies on omission as permission.
    if (includeTeam && plan.status === PLAN_READY && text(plan.data?.revision)) {
      payload.team_intent = {
        version: 1,
        mode: agentless ? 'agentless' : 'staffed',
        plan_revision: plan.data.revision
      };
      payload.role_staffing = (agentless ? [] : roleRoster.roles)
        .filter(role => role.state === 'filled' && !role.read_only)
        .map(role => {
          const fill = source.roleFills.get(role.role_id);
          const item = { role_id: role.role_id, mode: fill.mode, name: fill.name };
          if (fill.mode === FILL_CREATE) {
            if (fill.provider) item.provider = fill.provider;
            if (fill.model) item.model = fill.model;
            // Sent only when the user actually edited them; absent means the
            // server applies what the blueprint declared.
            if (fill.type) item.type = fill.type;
            if (fill.systemPrompt) item.system_prompt = fill.systemPrompt;
          }
          return item;
        });
    }
    if (assistantProgram) {
      // assistant_hire drives the post-create hire, which staffs every required
      // role at once. It is sent ONLY when the vacancy model is not in play, so
      // a wizard submission can never silently staff a role the user left
      // empty.
      if (!assistantProgram.existingHired && !payload.role_staffing)
        payload.assistant_hire = {
          name: text(source.assistantHire?.name),
          provider: text(source.assistantHire?.provider),
          model: text(source.assistantHire?.model)
        };
    } else {
      if (agentless) payload.create_template_agents = false;
      else if (!payload.team_intent && allPlanAgents.length > 0)
        payload.create_template_agents = includeTeam;
      if (includeTeam && !payload.team_intent) {
        const overrides = serializeOverrides(source);
        if (overrides.length > 0) payload.template_agent_overrides = overrides;
        // The reviewed-roster contract describes creating the WHOLE blueprint
        // team. Under the vacancy model the request creates only the roles the
        // user filled, so the two cannot both describe the same submission.
        if (
          !payload.role_staffing &&
          allPlanAgents.length > 0 &&
          text(plan.data?.revision) &&
          pendingSetups.length === 0 &&
          !source.staleConflict
        ) {
          payload.template_agent_review = {
            version: 1,
            plan_revision: plan.data.revision,
            expectations: blueprintEntries
              .slice()
              .sort((left, right) => left.templateAgentIndex - right.templateAgentIndex)
              .map(entry => ({
                index: entry.templateAgentIndex,
                name: entry.name,
                action: entry.lifecycle === 'reuse' ? 'reuse' : 'create'
              }))
          };
        }
      }
      // Sent only when non-empty: the server treats a nil existing_agent_names as
      // a legacy request and keeps its original entry-agent behavior.
      if (!agentless && savedEntries.length > 0) {
        payload.existing_agent_names = savedEntries.map(entry => entry.name);
        const savedPrimary = savedEntries.find(entry => entry.key === primaryKey);
        // A declared primary role owns entry-agent selection. Explicit saved
        // primaries remain meaningful only for a blueprint with no role slots.
        if (savedPrimary && roleRoster.total_count === 0) {
          payload.entry_agent_name = savedPrimary.name;
        }
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
      isBlank,
      agentless,
      assistantHire: assistantProgram
        ? { ...(source.assistantHire || emptyAssistantHire()) }
        : null,
      isAssistantProgram: Boolean(assistantProgram),
      // The vacancy projection both the Team step and the Review receipt
      // render, and the one the create request is built from.
      roleRoster,
      roleSummary,
      recommendations,
      roster,
      primaryName: roleRoster.total_count > 0 ? actualPrimaryName : primary ? primary.name : '',
      primaryIsAutomatic:
        roleRoster.total_count > 0
          ? Boolean(actualPrimaryName) && !text(source.explicitPrimary)
          : Boolean(primary) && !text(source.explicitPrimary),
      specialists,
      shadowedSelections,
      batchSetup: {
        pendingCount: pendingSetups.length,
        canAcceptAll: pendingSetups.length >= 2,
        acceptedCount: Array.from(source.acknowledgements || []).filter(
          ([index, acknowledgement]) =>
            acknowledgement.provenance === 'batch' &&
            activePlanAgents[index] &&
            isSetupAcknowledged(source, index, activePlanAgents[index])
        ).length
      },
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
    markPlanConflict,
    confirmFreshPlan,
    reviewChangedEntry,
    markCreationFailure,
    clearCreationFailure,
    setPlanError,
    clearPlan,
    normalizePlan,
    setIncludeBlueprintTeam,
    setAssistantHire,
    setRoleFill,
    clearRoleFill,
    setAgentless,
    getRoleFill,
    roleFilledBy,
    declaredRoles,
    roleIdFromName,
    FILL_CREATE,
    FILL_ASSIGN,
    stageOverride,
    clearOverride,
    getOverride,
    acceptRecommended,
    acceptAllRecommended,
    undoBatchRecommended,
    resetToRecommended,
    saveSetup,
    acknowledgeSetup,
    setSavedRosterLoading,
    setSavedRosterReady,
    setSavedRosterError,
    addSavedAgent,
    removeSavedAgent,
    setExplicitPrimary,
    isSelected,
    isBlueprintOwned,
    isActuallyIncluded,
    recommendSavedAgents,
    recommendationMatch,
    isAttachableSavedAgent,
    findSavedAgent,
    identityFrom,
    agentKey,
    derive,
    toCreatePayload
  };
})();
