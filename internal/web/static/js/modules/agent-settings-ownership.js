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

  function requestURL(agentName, ctx) {
    const base = `/api/agents/${encodeURIComponent(agentName)}/settings-ownership`;
    if (ctx.kind !== 'workspace') return base;
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

  function formatValue(field) {
    if (!field || field.empty) {
      return field?.key === 'max_output_tokens' ? 'Not set (provider default)' : 'Not set';
    }
    if (field.key === 'system_prompt') {
      const value = String(field.value ?? '');
      return value.length > 160 ? `${value.slice(0, 160)}…` : value;
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

  // ---- Mount ----------------------------------------------------------------

  function el(doc, tag, className, content) {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    if (content !== undefined) node.textContent = content;
    return node;
  }

  function mount({ root, agentName, fetchImpl, win }) {
    if (!root || !agentName) return null;
    const w = win || window;
    const doc = root.ownerDocument || document;
    const select = root.querySelector('[data-ownership-context]');
    const status = root.querySelector('[data-ownership-status]');
    const body = root.querySelector('[data-ownership-body]');
    const doFetch = fetchImpl || ((url, init) => w.fetch(url, init));
    let name = agentName;
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

    function renderProjection(projection) {
      const view = viewModel(projection);
      const nodes = [el(doc, 'h3', 'settings-ownership-heading', view.heading)];
      if (view.message) nodes.push(el(doc, 'p', 'settings-ownership-message', view.message));
      if (view.source) {
        const source = el(doc, 'p', 'settings-ownership-source');
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
          const value = el(doc, 'dd', field.empty ? 'is-empty' : '', field.value);
          row.append(value);
          // Always three cells per row: the grid pairs labels and values by
          // position, so a missing comparison cell would shift every later row.
          const cmp = el(doc, 'dd', 'settings-ownership-comparison', field.comparison);
          cmp.dataset.comparison = field.comparison ? field.comparisonCode : 'none';
          row.append(cmp);
          list.append(row);
        }
        nodes.push(list);
      }
      if (view.excluded) nodes.push(el(doc, 'p', 'settings-ownership-excluded', view.excluded));
      body.replaceChildren(...nodes);
    }

    function renderFailure(message) {
      const retry = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Retry');
      retry.type = 'button';
      retry.addEventListener('click', () => void load());
      body.replaceChildren(el(doc, 'p', 'settings-ownership-message', message), retry);
    }

    async function load() {
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
        renderProjection(projection);
        setStatus('', 'ready');
      } catch (error) {
        if (mine !== sequence || error?.name === 'AbortError') return;
        setStatus('', 'error');
        renderFailure(error?.message || 'Saved settings could not be loaded.');
      } finally {
        if (mine === sequence) root.removeAttribute('aria-busy');
      }
    }

    function chooseContext(next, { push = true } = {}) {
      if (contextKey(next) === contextKey(ctx)) return;
      ctx = next;
      if (push) {
        const url = `${w.location.pathname}${writeContext(w.location.search, ctx)}${w.location.hash || ''}`;
        w.history.pushState({ ownershipContext: contextKey(ctx) }, '', url);
      }
      void load();
    }

    select.addEventListener('change', () => chooseContext(contextFromOption(select.value)));
    w.addEventListener('popstate', () =>
      chooseContext(parseContext(w.location.search), { push: false })
    );
    void load();

    return {
      context: () => ({ ...ctx }),
      reload: () => load(),
      // A confirmed rename keeps the context and re-reads under the new name.
      setAgentName(next) {
        if (next && next !== name) {
          name = next;
          void load();
        }
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
    formatValue
  };
})();
