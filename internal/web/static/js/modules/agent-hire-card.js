// The Hire card (agents-hire-with-a-brief): which role, what to call them,
// what they should do, and the workspace that brief becomes the goal of. The
// Agents roster's Create Agent modal and the /agents/create page draw the same
// card and send the same one request, POST /api/agents/hire.
//
// A classic script with no imports or exports, like folder-plan-lines.js,
// whose line renderer it uses; both surfaces read window.OriAgentHire.
(function () {
  'use strict';

  // A project hire reports back to the personal assistant, so it is offered
  // only once there is one (internal/agenthttp/agents_hire.go).
  const ASSISTANT_STATES_FOR_HIRE = ['needs_hq', 'provisioning_hq', 'active', 'paused'];

  const BRIEF_MAX = 300;
  const WORKSPACE_NAME_MAX = 80;
  const AGENT_NAME_MAX = 100;
  const DOMAIN_MAX = 80;

  const BRIEF_HINT = 'This becomes the workspace goal. You can change it later.';
  const FIRST_LOOK_LINE = 'First look: read the brief and suggest the first task';
  const OPEN_ROLES_LINE = 'Other roles stay open to fill later';

  // assistantAllowsHire reads a personal_assistant payload (or its state).
  function assistantAllowsHire(assistant) {
    const state = typeof assistant === 'string' ? assistant : assistant?.state;
    return ASSISTANT_STATES_FOR_HIRE.includes(String(state || ''));
  }

  // The workspace name a brief gives, exactly as the server derives it
  // (personalassistant.HireWorkspaceName): the first five words in title case,
  // small words lower case and dropped from the end.
  const NAME_WORDS = 5;
  const SMALL_WORDS = new Set([
    'a',
    'about',
    'an',
    'and',
    'as',
    'at',
    'by',
    'for',
    'from',
    'in',
    'into',
    'of',
    'on',
    'or',
    'the',
    'to',
    'with'
  ]);

  function hireWorkspaceName(brief) {
    const words = [];
    for (const field of String(brief || '').split(/\s+/)) {
      const word = field.replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, '');
      if (!word) continue;
      words.push(word);
      if (words.length === NAME_WORDS) break;
    }
    while (words.length && SMALL_WORDS.has(words[words.length - 1].toLowerCase())) words.pop();
    const titled = words.map((word, i) => {
      if (i > 0 && SMALL_WORDS.has(word.toLowerCase())) return word.toLowerCase();
      const first = Array.from(word)[0];
      return first.toUpperCase() + word.slice(first.length);
    });
    let name = titled.join(' ');
    while (Array.from(name).length > WORKSPACE_NAME_MAX) {
      const cut = name.lastIndexOf(' ');
      if (cut <= 0) return Array.from(name).slice(0, WORKSPACE_NAME_MAX).join('');
      name = name.slice(0, cut);
    }
    return name;
  }

  function oneLine(text) {
    return String(text ?? '')
      .split(/\s+/)
      .filter(Boolean)
      .join(' ');
  }

  // roleFacts is what the catalog's hire block says about one role.
  function roleFacts(catalog, slug) {
    const facts = catalog?.hire?.roles?.[slug] || {};
    return {
      defaultBrief: oneLine(facts.default_brief),
      blueprint: String(facts.blueprint || 'blank'),
      blueprintLabel: oneLine(facts.blueprint_label) || 'Blank',
      openRoles: Number(facts.open_roles) > 0 ? Number(facts.open_roles) : 0
    };
  }

  // defaultBriefFor is the brief the card starts from for a role: the
  // Specialist names its domain once it has one.
  function defaultBriefFor(catalog, entry, domain) {
    const template = oneLine(catalog?.hire?.domain_brief);
    const named = oneLine(domain);
    if (entry?.supports_domain && named && template.includes('{domain}')) {
      return Array.from(template.replace('{domain}', named)).slice(0, BRIEF_MAX).join('');
    }
    return roleFacts(catalog, entry?.slug).defaultBrief;
  }

  // suggestAgentName is the role's name, or the first "Name N" no agent has.
  function suggestAgentName(displayName, takenNames = []) {
    const base = oneLine(displayName) || 'Agent';
    const taken = new Set((takenNames || []).map(name => String(name).trim().toLowerCase()));
    if (!taken.has(base.toLowerCase())) return base;
    for (let n = 2; n < 100; n++) {
      const candidate = `${base} ${n}`;
      if (!taken.has(candidate.toLowerCase())) return candidate;
    }
    return base;
  }

  // prefillState is the card's starting state from a prefill: a role, and any
  // of name, domain, brief and workspace name already written (the assistant's
  // proposal, say). A brief or workspace name that was given counts as edited,
  // so a role change keeps it. requestId, when given, is the hire's request
  // id (a proposal's id); otherwise the card mints one.
  function prefillState(prefill = {}, requestId = '') {
    const brief = oneLine(prefill.brief);
    const workspaceName = oneLine(prefill.workspaceName);
    return {
      role: '',
      name: oneLine(prefill.name),
      domain: oneLine(prefill.domain),
      brief,
      workspaceName,
      edited: { name: false, brief: Boolean(brief), workspaceName: Boolean(workspaceName) },
      requestId: oneLine(requestId) || newRequestId(),
      phase: 'idle',
      failedStep: '',
      error: '',
      note: oneLine(prefill.note)
    };
  }

  // suggestedNameFor is the name a role gets while the user has not typed one:
  // the prefilled name for the prefilled role (it comes back with that role),
  // else the role's own name.
  function suggestedNameFor(entry, prefill, takenNames) {
    const given = oneLine(prefill?.name);
    if (given && entry?.slug === oneLine(prefill?.role)) return given;
    return suggestAgentName(entry?.display_name, takenNames);
  }

  // planLines is what Hire and start does, line by line, in the folder
  // card's line kinds.
  function planLines({ name, roleLabel, blueprintLabel, openRoles, workspaceName, brief }) {
    const who = oneLine(name);
    const lines = [
      {
        kind: 'agents',
        name: who ? `Hire ${who} (${roleLabel})` : `Hire a ${roleLabel}`
      },
      {
        kind: 'workspace',
        name: `A ${blueprintLabel} workspace named ${oneLine(workspaceName) || '…'}`,
        detail: brief ? `Goal: ${oneLine(brief)}` : ''
      }
    ];
    if (openRoles > 0) lines.push({ kind: 'agents', name: OPEN_ROLES_LINE });
    lines.push({ kind: 'task', name: FIRST_LOOK_LINE });
    return lines;
  }

  // Which line a failed step stops at (agents_hire.go's hireStep* names).
  const STEP_LINE = { agent: 0, workspace: 1, record: -1, look: -1 };

  // progressLines marks the plan's lines for a hire in flight ('running'),
  // finished ('done') or stopped at a step ('failed'). A refusal before any
  // step names none, and the lines stay unmarked.
  function progressLines(lines, phase, failedStep) {
    if (phase === 'done') return lines.map(line => ({ ...line, state: 'done' }));
    if (phase === 'running') {
      return lines.map((line, i) => ({ ...line, state: i === 0 ? 'working' : 'waiting' }));
    }
    if (phase === 'failed' && failedStep && failedStep in STEP_LINE) {
      let at = STEP_LINE[failedStep];
      if (at < 0) at = lines.length - 1;
      return lines.map((line, i) => ({
        ...line,
        state: i < at ? 'done' : i === at ? 'failed' : 'waiting'
      }));
    }
    return lines.map(line => ({ ...line }));
  }

  // hireCardView is everything the card shows for its current state.
  function hireCardView(catalog, state) {
    const entries = Array.isArray(catalog?.entries) ? catalog.entries : [];
    const entry = entries.find(candidate => candidate.slug === state.role) || null;
    const facts = roleFacts(catalog, entry?.slug);
    const brief = oneLine(state.brief);
    const name = oneLine(state.name);
    const workspaceName = oneLine(state.workspaceName);
    const lines = entry
      ? progressLines(
          planLines({
            name,
            roleLabel: entry.display_name,
            blueprintLabel: facts.blueprintLabel,
            openRoles: facts.openRoles,
            workspaceName,
            brief
          }),
          state.phase,
          state.failedStep
        )
      : [];
    const problems = [];
    if (!entry) problems.push('Choose a role.');
    if (!name) problems.push('Give them a name.');
    else if (name.length > AGENT_NAME_MAX || !/^[A-Za-z0-9_\- ]+$/.test(name)) {
      problems.push('Use letters, numbers, spaces, underscores and hyphens in the name.');
    }
    if (Array.from(brief).length > BRIEF_MAX)
      problems.push(`Keep what it should do within ${BRIEF_MAX} characters.`);
    if (Array.from(workspaceName).length > WORKSPACE_NAME_MAX) {
      problems.push(`Keep the workspace name within ${WORKSPACE_NAME_MAX} characters.`);
    }
    const running = state.phase === 'running';
    return {
      // The card's current words, for a host that heads the card with them.
      fields: { role: state.role, name, domain: oneLine(state.domain), brief, workspaceName },
      roles: entries.map(candidate => ({
        slug: candidate.slug,
        label: candidate.display_name,
        tagline: candidate.tagline,
        emblem: candidate.emblem,
        accent: candidate.accent_color,
        selected: candidate.slug === state.role
      })),
      showDomain: Boolean(entry?.supports_domain),
      lines,
      problems,
      canSubmit: problems.length === 0 && !running && state.phase !== 'done',
      busy: running,
      buttonLabel: state.phase === 'failed' ? 'Try again' : 'Hire and start',
      error: state.phase === 'failed' ? oneLine(state.error) : '',
      note: oneLine(state.note)
    };
  }

  // hireRequestBody is the one request Hire and start sends.
  function hireRequestBody(state, appearance) {
    const body = {
      request_id: state.requestId,
      role: state.role,
      name: oneLine(state.name),
      brief: oneLine(state.brief),
      workspace_name: oneLine(state.workspaceName)
    };
    if (state.domain) body.domain = oneLine(state.domain);
    if (appearance) body.appearance = appearance;
    return body;
  }

  // hiredMessage is what the user is told once the hire is done.
  function hiredMessage(result) {
    const name = oneLine(result?.agent_name) || 'Your agent';
    if (result?.look_state === 'failed') {
      const reason = oneLine(result.look_failure);
      return `Hired ${name}, but the first look did not start${reason ? `: ${reason}` : '.'} Your assistant can retry it.`;
    }
    return `Hired. ${name} is taking a first look; your assistant will report back.`;
  }

  function newRequestId() {
    if (globalThis.crypto?.randomUUID) return `hire-${globalThis.crypto.randomUUID()}`;
    return `hire-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
  }

  function escapeHtml(text) {
    return String(text ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // bindTablist wires an ARIA tablist: click, Left/Right/Home/End. onSelect
  // receives the chosen tab; the caller shows its panel.
  function bindTablist(tabs, onSelect) {
    tabs.forEach((tab, i) => {
      tab.addEventListener('click', () => onSelect(tab));
      tab.addEventListener('keydown', event => {
        const last = tabs.length - 1;
        const next = {
          ArrowRight: i === last ? 0 : i + 1,
          ArrowDown: i === last ? 0 : i + 1,
          ArrowLeft: i === 0 ? last : i - 1,
          ArrowUp: i === 0 ? last : i - 1,
          Home: 0,
          End: last
        }[event.key];
        if (next === undefined) return;
        event.preventDefault();
        onSelect(tabs[next]);
        tabs[next].focus();
      });
    });
  }

  // selectTab marks one tab of a list selected and shows only its panel.
  function selectTab(tabs, chosen) {
    tabs.forEach(tab => {
      const on = tab === chosen;
      tab.classList.toggle('is-active', on);
      tab.setAttribute('aria-selected', on ? 'true' : 'false');
      tab.tabIndex = on ? 0 : -1;
      const panel = tab.ownerDocument.getElementById(tab.getAttribute('aria-controls'));
      if (panel) panel.hidden = !on;
    });
  }

  // mountHireCard draws the card into host and runs its one request.
  // options: { catalog, prefill: { role, name, domain, brief, workspaceName,
  //   note }, requestId, appearance (false leaves the face out), takenNames(),
  //   idPrefix, onChange(view), onHired(result), fetchImpl, doc }
  // The caller owns the Hire and start button and calls submit().
  function mountHireCard(host, options = {}) {
    const doc = options.doc || document;
    const fetchImpl = options.fetchImpl || ((...args) => fetch(...args));
    const id = suffix => `${options.idPrefix || 'hire'}${suffix}`;
    const catalog = options.catalog || { entries: [] };
    const taken = () => (typeof options.takenNames === 'function' ? options.takenNames() : []);
    const prefill = options.prefill || {};
    const state = prefillState(prefill, options.requestId);
    let appearanceEditor = null;

    host.replaceChildren();
    const card = doc.createElement('div');
    card.className = 'agent-hire-card';
    card.dataset.hireCard = '';
    card.innerHTML = `
      <p class="agent-hire-card__note" data-hire-note hidden></p>
      <fieldset class="agent-hire-card__roles mb-3">
        <legend class="form-label">Role</legend>
        <div class="catalog-grid agent-hire-card__grid" role="radiogroup" aria-label="Role" data-hire-roles></div>
      </fieldset>
      <div class="mb-3" data-hire-domain hidden>
        <label class="form-label" for="${id('Domain')}">Domain</label>
        <input id="${id('Domain')}" class="modern-input w-100" type="text" maxlength="${DOMAIN_MAX}"
               autocomplete="off" placeholder="e.g. mixing, tax law, React">
      </div>
      <div class="mb-3">
        <label class="form-label" for="${id('Name')}">Name</label>
        <input id="${id('Name')}" class="modern-input w-100" type="text" maxlength="${AGENT_NAME_MAX}"
               autocomplete="off" spellcheck="false" required>
      </div>
      <div class="mb-3" id="${id('AppearanceHost')}"></div>
      <div class="mb-3">
        <label class="form-label" for="${id('Brief')}">What should it do?</label>
        <textarea id="${id('Brief')}" class="modern-input w-100" rows="3" maxlength="${BRIEF_MAX}"
                  aria-describedby="${id('BriefHint')}"></textarea>
        <small class="form-helper" id="${id('BriefHint')}">${escapeHtml(BRIEF_HINT)}</small>
      </div>
      <div class="mb-3">
        <label class="form-label" for="${id('WorkspaceName')}">Workspace name</label>
        <input id="${id('WorkspaceName')}" class="modern-input w-100" type="text" maxlength="${WORKSPACE_NAME_MAX}"
               autocomplete="off">
      </div>
      <section class="agent-hire-card__plan" aria-labelledby="${id('PlanHeading')}">
        <p class="agent-hire-card__plan-heading" id="${id('PlanHeading')}">Hire and start will</p>
        <ol class="pa-folder__plan" data-hire-lines aria-live="polite"></ol>
      </section>
      <div class="agent-hire-card__error" role="alert" tabindex="-1" data-hire-error hidden></div>`;
    host.append(card);

    const els = {
      note: card.querySelector('[data-hire-note]'),
      roles: card.querySelector('[data-hire-roles]'),
      domainField: card.querySelector('[data-hire-domain]'),
      domain: card.querySelector(`#${id('Domain')}`),
      name: card.querySelector(`#${id('Name')}`),
      brief: card.querySelector(`#${id('Brief')}`),
      workspaceName: card.querySelector(`#${id('WorkspaceName')}`),
      lines: card.querySelector('[data-hire-lines]'),
      error: card.querySelector('[data-hire-error]')
    };

    function entryFor(slug) {
      return (catalog.entries || []).find(entry => entry.slug === slug) || null;
    }

    // Fields the user has not typed in follow the role and the brief.
    function refreshDefaults() {
      const entry = entryFor(state.role);
      if (!entry) return;
      if (!state.edited.name) state.name = suggestedNameFor(entry, prefill, taken());
      if (!state.edited.brief) state.brief = defaultBriefFor(catalog, entry, state.domain);
      if (!state.edited.workspaceName) state.workspaceName = hireWorkspaceName(state.brief);
      els.name.value = state.name;
      els.brief.value = state.brief;
      els.workspaceName.value = state.workspaceName;
      if (els.domain.value !== state.domain) els.domain.value = state.domain;
    }

    function render() {
      const view = hireCardView(catalog, state);
      els.note.hidden = !view.note;
      els.note.textContent = view.note;
      els.roles.innerHTML = view.roles
        .map(
          role =>
            `<button type="button" class="catalog-card${role.selected ? ' is-selected' : ''}" role="radio"` +
            ` aria-checked="${role.selected}" tabindex="${role.selected || (!state.role && role === view.roles[0]) ? 0 : -1}"` +
            ` data-slug="${escapeHtml(role.slug)}" style="--catalog-accent: ${escapeHtml(role.accent)};">` +
            `<span class="catalog-card-emblem"><i class="bi bi-${escapeHtml(role.emblem)}" aria-hidden="true"></i></span>` +
            `<span class="catalog-card-name">${escapeHtml(role.label)}</span>` +
            `<span class="catalog-card-tagline">${escapeHtml(role.tagline)}</span></button>`
        )
        .join('');
      els.domainField.hidden = !view.showDomain;
      const planLines = globalThis.OriFolderPlanLines;
      if (planLines?.renderSetupLines) {
        planLines.renderSetupLines(els.lines, view.lines, doc, { kindIcons: true });
      }
      els.error.hidden = !view.error;
      els.error.textContent = view.error;
      [els.domain, els.name, els.brief, els.workspaceName].forEach(input => {
        input.disabled = view.busy;
      });
      if (typeof options.onChange === 'function') options.onChange(view);
      return view;
    }

    function chooseRole(slug, { focus = false } = {}) {
      const entry = entryFor(slug);
      if (!entry || state.phase === 'running') return;
      state.role = slug;
      if (!entry.supports_domain) state.domain = '';
      refreshDefaults();
      appearanceEditor?.setAgentRole?.(slug);
      appearanceEditor?.setAgentName?.(state.name);
      render();
      if (focus) els.roles.querySelector(`[data-slug="${CSS.escape(slug)}"]`)?.focus();
    }

    els.roles.addEventListener('click', event => {
      const card = event.target.closest('[data-slug]');
      if (card) chooseRole(card.dataset.slug);
    });
    // A radiogroup moves with the arrow keys.
    els.roles.addEventListener('keydown', event => {
      const slugs = (catalog.entries || []).map(entry => entry.slug);
      const at = Math.max(0, slugs.indexOf(state.role));
      const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key];
      if (!step || !slugs.length) return;
      event.preventDefault();
      chooseRole(slugs[(at + step + slugs.length) % slugs.length], { focus: true });
    });
    els.domain.addEventListener('input', () => {
      state.domain = els.domain.value;
      refreshDefaults();
      render();
    });
    els.name.addEventListener('input', () => {
      state.name = els.name.value;
      state.edited.name = true;
      appearanceEditor?.setAgentName?.(state.name);
      render();
    });
    els.brief.addEventListener('input', () => {
      state.brief = els.brief.value;
      state.edited.brief = true;
      if (!state.edited.workspaceName) {
        state.workspaceName = hireWorkspaceName(state.brief);
        els.workspaceName.value = state.workspaceName;
      }
      render();
    });
    els.workspaceName.addEventListener('input', () => {
      state.workspaceName = els.workspaceName.value;
      state.edited.workspaceName = true;
      render();
    });

    // The same Appearance editor as the Advanced form, with a face suggested
    // for the role. No upload: the hire is one request. A host that keeps the
    // card short (the conversation) leaves the face out.
    const appearanceHost = card.querySelector(`#${id('AppearanceHost')}`);
    if (options.appearance === false) {
      appearanceHost?.remove();
    } else if (globalThis.AgentAppearanceEditor && appearanceHost) {
      appearanceEditor = globalThis.AgentAppearanceEditor.create({
        host: appearanceHost,
        idPrefix: id('Appearance'),
        mode: 'create',
        agent: { name: '', source: 'user', role: '' },
        takenCharacterIds: options.takenCharacterIds || (() => []),
        suggestCharacter: true,
        allowedModes: ['generated', 'character']
      });
    }

    // A prefill that asks the user to choose the role (the assistant's chip
    // on a model without tools) starts with none selected and the brief as
    // given; otherwise the prefilled role, else the first.
    const prefillRole = entryFor(prefill.role)
      ? prefill.role
      : prefill.requireRole
        ? ''
        : catalog.entries?.[0]?.slug;
    if (prefillRole) chooseRole(prefillRole);
    else {
      els.name.value = state.name;
      els.brief.value = state.brief;
      els.workspaceName.value = state.workspaceName;
      render();
    }

    async function submit() {
      const view = hireCardView(catalog, state);
      if (!view.canSubmit) {
        if (view.busy || state.phase === 'done') return null;
        els.error.hidden = false;
        els.error.textContent = view.problems[0] || 'Check the card and try again.';
        els.error.focus?.();
        return null;
      }
      state.phase = 'running';
      state.failedStep = '';
      state.error = '';
      render();
      let response;
      let payload = {};
      try {
        response = await fetchImpl('/api/agents/hire', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
          body: JSON.stringify(hireRequestBody(state, appearanceEditor?.createRequest?.() || null))
        });
        payload = (await response.json().catch(() => ({}))) || {};
      } catch (_) {
        response = null;
      }
      if (response && response.ok) {
        state.phase = 'done';
        render();
        if (typeof options.onHired === 'function') options.onHired(payload);
        return payload;
      }
      // Try again keeps the request id: a hire that did finish answers with
      // itself instead of hiring twice.
      state.phase = 'failed';
      state.failedStep = String(payload.step || '');
      state.error = response
        ? payload.message || payload.error || `The hire did not finish (${response.status}).`
        : 'Could not reach Ori. Check the connection and try again.';
      render();
      els.error.focus?.();
      return null;
    }

    return {
      submit,
      view: () => hireCardView(catalog, state),
      chooseRole,
      focus() {
        (els.roles.querySelector('[aria-checked="true"]') || els.name).focus();
      },
      destroy() {
        appearanceEditor?.destroy?.();
        appearanceEditor = null;
        host.replaceChildren();
      }
    };
  }

  // loadHireCatalog reads the roles and the hire block once per open.
  async function loadHireCatalog(fetchImpl = (...args) => fetch(...args)) {
    const response = await fetchImpl('/api/agents/catalog', {
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) throw new Error(`catalog ${response.status}`);
    const data = await response.json();
    return { entries: Array.isArray(data.entries) ? data.entries : [], hire: data.hire || {} };
  }

  globalThis.OriAgentHire = {
    ASSISTANT_STATES_FOR_HIRE,
    assistantAllowsHire,
    hireWorkspaceName,
    defaultBriefFor,
    suggestAgentName,
    prefillState,
    suggestedNameFor,
    planLines,
    progressLines,
    hireCardView,
    hireRequestBody,
    hiredMessage,
    bindTablist,
    selectTab,
    mountHireCard,
    loadHireCatalog
  };
})();
