// The folder/build review's Home team, using the same role rows, create form
// and appearance editor as managed Homes. The caller owns requests and final
// setup review; this component never creates an agent or executes setup.
(function () {
  'use strict';
  const mounted = new WeakMap();
  let sequence = 0;
  const text = value => String(value || '').trim();

  function roster(view) {
    const advice = window.CreateWorkspaceTeamDraft?.normalizeStaffingAdvice(view?.advice);
    const choices = Array.isArray(view?.choices) ? view.choices : [];
    const roles = (view?.roles || []).map(role => {
      const fill = choices.find(choice => choice.role_id === role.role_id);
      const held = text(role.held_name);
      const suggested = advice?.roles.find(item => item.roleId === role.role_id);
      return {
        ...role,
        scope: 'home',
        description: [
          role.description,
          role.skills?.length ? `Required skills: ${role.skills.join(', ')}` : ''
        ]
          .filter(Boolean)
          .join(' · '),
        state: fill || held ? 'filled' : 'empty',
        agent: fill
          ? {
              name: fill.name,
              model: fill.model,
              provider: fill.provider,
              appearance: fill.appearance
            }
          : held
            ? { name: held }
            : null,
        source: held ? 'group' : fill?.mode === 'assign' ? 'assigned' : 'created',
        read_only: Boolean(held && !fill),
        read_only_reason: held ? 'This Home role is already held. Setup will not replace it.' : '',
        needs_clear: Boolean(
          fill &&
          ((view.state === 'stale' && !Object.keys(view.choice_problems || {}).length) ||
            view.state === 'unavailable' ||
            view.choice_problems?.[role.role_id])
        ),
        staffing:
          !fill && !held && advice
            ? {
                ...(suggested || { roleId: role.role_id, scope: 'home', choices: [] }),
                state: ['stale', 'unavailable'].includes(view.state)
                  ? view.state
                  : ['recommended', 'needs_choice'].includes(advice.state)
                    ? suggested?.state || 'needs_choice'
                    : advice.state,
                complete: advice.complete,
                assignmentsComplete: advice.assignmentsComplete
              }
            : null
      };
    });
    const filled = roles.filter(role => role.state === 'filled').length;
    return {
      roles,
      total_count: roles.length,
      filled_count: filled,
      empty_count: roles.length - filled
    };
  }

  function mount(host, options) {
    if (!host) return null;
    if (mounted.has(host)) {
      const prior = mounted.get(host);
      prior.update(options);
      return prior;
    }
    let opts = options || {};
    let view = opts.view;
    let owner = text(opts.owner);
    let destroyed = false;
    let editor = null;
    let editing = 0;
    let mutation = false;
    let ranking = false;
    let generation = 0;
    let pendingRank = null;
    let rowsSignature = '';
    const prefix = `folderHomeStaffing${++sequence}`;
    const node = (tag, className, value) => {
      const el = document.createElement(tag);
      el.className = className || '';
      if (value) el.textContent = value;
      return el;
    };
    const button = (label, action, primary = false) => {
      const el = node(
        'button',
        `modern-btn modern-btn-${primary ? 'primary' : 'secondary'}`,
        label
      );
      el.type = 'button';
      el.addEventListener('click', action);
      return el;
    };
    const root = node('section', 'folder-home-staffing');
    root.setAttribute('aria-label', 'Home team');
    const heading = node('h3', 'ws-role-roster__scope-title', 'Home team');
    const note = node(
      'p',
      'pa-folder__note',
      'Choose each required role. Saved profiles and their other assignments stay unchanged. Nothing is staffed until you confirm setup.'
    );
    const actions = node('div', 'folder-home-staffing__actions');
    const recommend = button('Recommend saved agents', () => void recommendAgents());
    const clear = button('Clear pending choices', () => void act('clear_staffing'));
    actions.append(recommend, clear);
    const problem = node('p', 'pa-folder__note');
    const rows = node('div');
    const formHost = node('div', 'folder-home-staffing__editor');
    formHost.hidden = true;
    const status = node('p', 'pa-folder__note');
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    const settings = node('p', 'pa-folder__note');
    const settingsLink = node('a', '', 'Review saved agents');
    settingsLink.href = '/agents';
    settings.append(
      settingsLink,
      node(
        'span',
        '',
        ' to check configuration, skill membership or whether a workspace-only profile can be made reusable. Changes here never grant access; after editing a profile, clear its original choice and choose again.'
      )
    );
    root.append(heading, note, actions, problem, rows, formHost, settings, status);
    host.replaceChildren(root);

    function closeEditor(focus = true) {
      editing++;
      const role = editor?.role;
      editor?.destroy?.();
      editor = null;
      formHost.replaceChildren();
      formHost.hidden = true;
      if (focus && role) window.WorkspaceRoleRoster?.focusRole(rows, role);
    }
    function draw() {
      host.hidden = !view;
      if (!view) {
        closeEditor(false);
        return;
      }
      problem.textContent = text(view.problem);
      problem.hidden = !problem.textContent;
      recommend.textContent = ranking
        ? 'Checking saved agents…'
        : view.advice
          ? 'Refresh recommendation'
          : 'Recommend saved agents';
      recommend.disabled =
        mutation ||
        ranking ||
        Boolean(opts.disabled) ||
        ['stale', 'unavailable'].includes(view.state);
      clear.hidden = !(view.choices?.length || view.advice || ranking);
      clear.disabled = mutation || Boolean(opts.disabled);
      const projection = roster(view);
      const signature = JSON.stringify([projection, mutation, Boolean(opts.disabled)]);
      if (signature === rowsSignature) return;
      rowsSignature = signature;
      window.WorkspaceRoleRoster?.render(rows, projection, {
        title: 'Home roles',
        agentHref: null,
        onAssign: role => void openEditor(role, 'assign'),
        onRequestCreate: role => void openEditor(role, 'create'),
        onClear: role => void act('clear_staffing', { role_id: role }),
        onRecommend: (role, handle) => {
          const candidate = view.advice?.roles
            ?.find(item => item.role_id === role)
            ?.choices?.find(item => item.handle === handle);
          if (candidate)
            void act('choose_staffing', {
              choice: {
                role_id: role,
                mode: 'assign',
                name: candidate.name,
                recommendation: handle
              }
            });
        }
      });
      if (mutation || opts.disabled)
        rows.querySelectorAll('button').forEach(el => {
          el.disabled = true;
        });
    }
    function update(next = {}) {
      if (destroyed) return;
      const nextOwner = text(next.owner);
      if (owner !== nextOwner) {
        generation++;
        pendingRank?.abort();
        closeEditor(false);
        owner = nextOwner;
        mutation = ranking = false;
        view = null;
        status.textContent = '';
      }
      opts = next;
      // An older response cannot undo a newer clear/choice. Context changes at
      // the same revision are still shown, without altering an open form.
      if (!view || !next.view || Number(next.view.revision || 0) >= Number(view.revision || 0))
        view = next.view;
      draw();
    }
    async function act(action, fields = {}) {
      if (destroyed || mutation || opts.disabled || !view) return null;
      const isRank = action === 'recommend_staffing';
      if (isRank && ranking) return null;
      const token = ++generation;
      const originalOwner = owner;
      pendingRank?.abort();
      const controller = new AbortController();
      pendingRank = isRank ? controller : null;
      ranking = isRank;
      mutation = !isRank;
      status.textContent = isRank
        ? 'Checking declared profile evidence. No agent will be assigned.'
        : 'Saving this review choice…';
      draw();
      let succeeded = false;
      try {
        const input =
          action === 'prepare_staffing'
            ? { action }
            : {
                action,
                context_digest: view.context_digest || '',
                staffing_revision: Number(view.revision || 0),
                ...fields
              };
        const next = await opts.onAction(input, { signal: controller.signal });
        if (destroyed || owner !== originalOwner || token !== generation) return null;
        if (next) update({ ...opts, view: next });
        if (action === 'choose_staffing') closeEditor(false);
        succeeded = true;
        status.textContent = isRank
          ? 'Recommendations are advice. Choose a role explicitly before setup.'
          : 'Review updated. Setup still needs your confirmation.';
        return view;
      } catch (error) {
        if (
          !destroyed &&
          owner === originalOwner &&
          token === generation &&
          error?.name !== 'AbortError'
        ) {
          status.textContent =
            error?.message ||
            'This choice could not be saved. Refresh the review or choose another agent; your draft is kept.';
        }
        return null;
      } finally {
        if (!destroyed && token === generation) {
          mutation = ranking = false;
          pendingRank = null;
          draw();
          if (succeeded && !isRank) {
            const role = fields.choice?.role_id || fields.role_id;
            if (role) window.WorkspaceRoleRoster?.focusRole(rows, role);
            else if (action === 'clear_staffing') recommend.focus();
          }
        }
      }
    }
    async function prepared() {
      if (!view || opts.disabled || mutation) return false;
      if (view.state === 'needs_review' || !view.context_digest)
        return Boolean(await act('prepare_staffing'));
      return true;
    }
    async function recommendAgents() {
      if (await prepared()) await act('recommend_staffing');
    }
    async function metadata(url) {
      const response = await fetch(url, { headers: { Accept: 'application/json' } });
      if (!response.ok)
        throw new Error(
          'Saved agents or models could not be loaded. Try again; no profiles were changed.'
        );
      return response.json();
    }
    async function openEditor(roleId, mode) {
      if (!(await prepared())) return;
      const role = view.roles?.find(item => item.role_id === roleId);
      if (!role || role.held_name || view.choices?.some(choice => choice.role_id === roleId))
        return;
      closeEditor(false);
      const token = editing;
      const originalOwner = owner;
      const context = view.context_digest;
      formHost.hidden = false;
      formHost.append(
        node(
          'h4',
          'ws-role-roster__scope-title',
          `${mode === 'assign' ? 'Assign a saved agent' : 'Create a new agent'} · ${role.label || roleId}`
        )
      );
      const body = node('div');
      const cancel = button('Back to Home roles', () => closeEditor());
      formHost.append(body, cancel);
      body.textContent = 'Loading…';
      editor = { role: roleId };
      const active = () => !destroyed && token === editing && owner === originalOwner;
      const choose = choice => {
        if (!active()) return;
        if (context !== view.context_digest) {
          status.textContent =
            'The setup changed. Return to Home roles and review this role again.';
          return;
        }
        void act('choose_staffing', { choice: { role_id: roleId, ...choice } });
      };
      try {
        if (mode === 'assign') {
          const data = await metadata('/api/agents/dashboard/list?sort_by=name&order=asc');
          if (!active()) return;
          const agents = Array.isArray(data.agents) ? data.agents : [];
          const search = node('input', 'form-control');
          search.type = 'search';
          search.setAttribute('aria-label', `Find a saved agent for ${role.label || roleId}`);
          search.placeholder = 'Search saved agents';
          const list = node('div', 'workspace-existing-agent-list');
          list.setAttribute('role', 'list');
          body.replaceChildren(search, list);
          const paint = () => {
            list.replaceChildren();
            const matches = agents.filter(agent =>
              text(agent.name).toLowerCase().includes(search.value.toLowerCase())
            );
            matches.forEach(agent => {
              const card = node('article', 'workspace-existing-agent-card');
              card.setAttribute('role', 'listitem');
              const avatar = node(
                'span',
                'workspace-agent-avatar',
                text(agent.name).slice(0, 1).toUpperCase()
              );
              avatar.setAttribute('aria-hidden', 'true');
              if (window.AgentAvatar)
                avatar.innerHTML = window.AgentAvatar.markup(
                  {
                    name: agent.name,
                    source: agent.source || 'user',
                    role: agent.role || '',
                    appearance: agent.appearance || null
                  },
                  { size: 40 }
                );
              const copy = node('div', 'workspace-existing-agent-card-copy');
              copy.append(
                node('strong', '', agent.name),
                node('span', '', agent.model || 'Uses saved agent model'),
                node(
                  'small',
                  '',
                  'Profile eligibility is checked when you choose. Other assignments are not verified here.'
                )
              );
              const pick = button('Assign', () => choose({ mode: 'assign', name: agent.name }));
              pick.setAttribute('aria-label', `Assign ${agent.name} to ${role.label || roleId}`);
              pick.disabled = agent.source === 'cli' || Boolean(agent.paused);
              card.append(avatar, copy, pick);
              list.append(card);
            });
            if (!matches.length)
              list.append(
                node(
                  'p',
                  'pa-folder__note',
                  'No saved agents match this search. You can go back and create a new agent.'
                )
              );
          };
          search.addEventListener('input', paint);
          paint();
          search.focus();
        } else {
          const data = await metadata('/api/providers');
          if (!active()) return;
          if (!window.AgentCreateForm || !window.AgentAppearanceEditor)
            throw new Error(
              'The shared agent form is unavailable. Return to Home roles and try again.'
            );
          const fields = node('div');
          const face = node('div');
          body.replaceChildren(fields, face);
          const form = window.AgentCreateForm.mount(fields, {
            idPrefix: `${prefix}Form`,
            profile: 'template',
            providers: data.providers || [],
            omitFields: ['systemPrompt'],
            values: { name: '' }
          });
          const appearance = window.AgentAppearanceEditor.create({
            host: face,
            idPrefix: `${prefix}Face`,
            mode: 'create',
            agent: { name: '', source: 'user' },
            allowedModes: ['generated', 'character'],
            suggestCharacter: true
          });
          const name = form.get('name');
          const onName = () => appearance?.setAgentName(name.value);
          name?.addEventListener('input', onName);
          editor.destroy = () => {
            name?.removeEventListener('input', onName);
            appearance?.destroy();
          };
          body.append(
            button(
              'Use for this role',
              () => {
                const result = form.extract();
                if (!result.valid) {
                  form.focus('name');
                  return;
                }
                const values = result.values;
                choose({
                  mode: 'create',
                  name: values.name,
                  provider: values.provider || '',
                  model: values.model || '',
                  reasoning_effort: values.reasoningEffort || '',
                  appearance: appearance?.appearance() || null
                });
              },
              true
            )
          );
          form.focus('name');
        }
      } catch (error) {
        if (active())
          body.textContent =
            error?.message || 'The editor could not be loaded. Go back and try again.';
      }
    }
    const api = {
      update,
      destroy() {
        destroyed = true;
        generation++;
        pendingRank?.abort();
        closeEditor(false);
        mounted.delete(host);
        host.replaceChildren();
      },
      act
    };
    mounted.set(host, api);
    draw();
    return api;
  }
  window.FolderHomeStaffing = { roster, mount };
})();
