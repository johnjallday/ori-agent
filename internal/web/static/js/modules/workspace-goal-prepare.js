// workspace-goal-prepare.js — the Goal **Prepare** step.
//
// Before a goal starts, this answers two questions the Goal surface could not
// answer before: what does this goal actually need, and which saved toolbox
// covers it?
//
//   GET  /goal/brief             the accepted brief + a fresh proposal
//   PUT  /goal/brief             accept an edited brief
//   GET  /goal/recommendations   ranked toolboxes, with explanations
//   PUT  /goal/toolbox-policy    pin a version, or use current-at-start
//
// The load-bearing rule is that a PROPOSAL controls nothing. Ori may draft a
// brief, but the draft is editable and only an ACCEPTED brief drives
// recommendations — otherwise a model's guess about what a goal needs would
// quietly decide which capabilities get recommended (PRD FR-94).
//
// And a recommendation is inert: it never selects, applies, installs, connects,
// widens a scope, enables expert mode, or raises autonomy (FR-99). Acting on
// one hands off to the Toolbox preview, which has its own review gate.
(function () {
  'use strict';

  const HOST_ID = 'workspace-goal-prepare';

  const state = {
    workspaceId: '',
    accepted: null,
    proposed: null,
    policy: null,
    recommendations: null,
    // draft is the brief being edited. Null means "showing what is accepted",
    // which is what keeps an unsaved edit from looking like a decision.
    draft: null,
    loading: false,
    busy: '',
    error: '',
    notice: ''
  };

  function wsId() {
    if (state.workspaceId) return state.workspaceId;
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
    if (opts.value !== undefined) node.value = opts.value;
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
    if (!response.ok) throw new Error((payload && payload.message) || 'That did not work.');
    return payload;
  }

  async function load() {
    if (!wsId()) return;
    state.loading = true;
    state.error = '';
    render();
    try {
      const brief = await readJSON(await api('/goal/brief'));
      state.accepted = brief.accepted || null;
      state.proposed = brief.proposed || null;
      state.policy = brief.policy || null;

      const ranked = await readJSON(await api('/goal/recommendations'));
      state.recommendations = ranked.recommendations || null;
      state.policy = ranked.policy || state.policy;
    } catch (error) {
      state.error = error?.message || 'Goal preparation could not be loaded.';
    } finally {
      state.loading = false;
      render();
    }
  }

  // Editing starts from what is ACCEPTED when there is one, and from the
  // proposal otherwise — so opening the editor never silently discards a
  // decision the user already made.
  function beginEdit() {
    const source = state.accepted || state.proposed || {};
    state.draft = {
      summary: source.summary || '',
      expected_output: source.expected_output || '',
      source_types: (source.source_types || []).join(', '),
      operations: (source.operations || []).join(', '),
      required_capabilities: (source.required_capabilities || []).join(', '),
      max_autonomy: source.max_autonomy || 'read'
    };
    state.notice = '';
    render();
  }

  function cancelEdit() {
    state.draft = null;
    render();
  }

  function setDraftField(field, value) {
    if (!state.draft) return;
    state.draft[field] = value;
  }

  function splitList(value) {
    return String(value || '')
      .split(',')
      .map(entry => entry.trim())
      .filter(Boolean);
  }

  async function acceptBrief() {
    if (!state.draft) return;
    state.busy = 'accept';
    state.error = '';
    render();
    try {
      await readJSON(
        await api('/goal/brief', {
          method: 'PUT',
          body: JSON.stringify({
            summary: state.draft.summary,
            expected_output: state.draft.expected_output,
            source_types: splitList(state.draft.source_types),
            operations: splitList(state.draft.operations),
            required_capabilities: splitList(state.draft.required_capabilities),
            max_autonomy: state.draft.max_autonomy
          })
        })
      );
      state.draft = null;
      state.notice = 'Goal brief accepted. Recommendations are based on it.';
      await load();
    } catch (error) {
      state.error = error?.message || 'The goal brief could not be saved.';
    } finally {
      state.busy = '';
      render();
    }
  }

  async function setPolicy(toolboxId, version, useCurrentAtStart) {
    const instanceId =
      (state.recommendations && state.recommendations.agent_instance_id) ||
      (state.policy && state.policy.entry_agent_instance_id) ||
      '';
    if (!instanceId) {
      state.error = 'Choose the agent that will carry out this goal first.';
      render();
      return;
    }

    state.busy = 'policy';
    state.error = '';
    render();
    try {
      const payload = await readJSON(
        await api('/goal/toolbox-policy', {
          method: 'PUT',
          body: JSON.stringify({
            entry_agent_instance_id: instanceId,
            toolbox_id: useCurrentAtStart ? undefined : toolboxId,
            toolbox_version: useCurrentAtStart ? undefined : version,
            use_current_at_start: !!useCurrentAtStart
          })
        })
      );
      state.notice = payload.message || 'Goal toolbox updated.';
      await load();
    } catch (error) {
      state.error = error?.message || 'The goal toolbox could not be set.';
    } finally {
      state.busy = '';
      render();
    }
  }

  // --------------------------------------------------------------- rendering

  function briefView() {
    const brief = state.accepted;
    if (!brief) {
      return el('section', { className: 'ws-goal-brief' }, [
        el('h4', { text: 'What this goal needs' }),
        el('p', {
          className: 'ws-goal-note',
          text: 'Ori has a suggestion. Review it — recommendations only start once you accept it.'
        }),
        el('button', {
          className: 'modern-btn modern-btn-primary modern-btn-sm',
          text: 'Review the suggestion',
          attrs: { type: 'button', 'data-goal-edit-brief': 'true' }
        })
      ]);
    }

    const line = (label, value) =>
      value && value.length
        ? el('li', { text: label + ': ' + (Array.isArray(value) ? value.join(', ') : value) })
        : null;

    return el('section', { className: 'ws-goal-brief' }, [
      el('h4', { text: 'What this goal needs' }),
      el('ul', { className: 'ws-goal-brief-facts' }, [
        line('Produces', brief.expected_output),
        line('Sources', brief.source_types),
        line('Needs to be able to', brief.operations),
        line('Must have', brief.required_capabilities),
        line('At most', brief.max_autonomy)
      ]),
      el('button', {
        className: 'modern-btn modern-btn-secondary modern-btn-sm',
        text: 'Edit',
        attrs: { type: 'button', 'data-goal-edit-brief': 'true' }
      })
    ]);
  }

  function briefEditor() {
    if (!state.draft) return null;
    const field = (label, key, placeholder) =>
      el('label', { className: 'ws-goal-field' }, [
        el('span', { text: label }),
        el('input', {
          className: 'modern-input',
          value: state.draft[key],
          attrs: { type: 'text', 'data-goal-field': key, placeholder: placeholder || '' }
        })
      ]);

    return el('section', { className: 'ws-goal-editor' }, [
      el('h4', { text: 'Review this goal brief' }),
      el('p', {
        className: 'ws-goal-note',
        text: 'Nothing is recommended from this until you accept it.'
      }),
      field('Produces', 'expected_output', 'a short summary of what changed'),
      field('Sources (comma separated)', 'source_types', 'public web, workspace files'),
      field('Needs to be able to (comma separated)', 'operations', 'search, read'),
      field('Must have (comma separated)', 'required_capabilities', 'summarize'),
      el('label', { className: 'ws-goal-field' }, [
        el('span', { text: 'At most' }),
        el(
          'select',
          { className: 'modern-input', attrs: { 'data-goal-field': 'max_autonomy' } },
          ['read', 'write', 'external'].map(value =>
            el('option', {
              text: value,
              value,
              attrs: value === state.draft.max_autonomy ? { selected: 'selected' } : {}
            })
          )
        )
      ]),
      el('div', { className: 'ws-goal-actions' }, [
        el('button', {
          className: 'modern-btn modern-btn-primary modern-btn-sm',
          text: state.busy === 'accept' ? 'Saving…' : 'Accept brief',
          attrs: { type: 'button', 'data-goal-accept-brief': 'true' },
          disabled: state.busy === 'accept'
        }),
        el('button', {
          className: 'modern-btn modern-btn-secondary modern-btn-sm',
          text: 'Cancel',
          attrs: { type: 'button', 'data-goal-cancel-brief': 'true' }
        })
      ])
    ]);
  }

  function policyView() {
    const policy = state.policy;
    const children = [el('h4', { text: 'Toolbox for this goal' })];

    if (policy && policy.needs_attention) {
      // Deliberately NOT a live region. "Needs attention" is a standing
      // condition, not a result — marking it live would re-announce the same
      // sentence on every re-render, which is the noise FR-164 exists to
      // prevent. It reads as ordinary text, which a screen reader reaches in
      // document order and a greyscale screenshot still shows.
      children.push(
        el('p', {
          className: 'ws-goal-error',
          text: 'Needs attention: ' + (policy.needs_attention_reason || 'this goal cannot start.')
        })
      );
    }

    if (!policy || (!policy.toolbox_id && !policy.use_current_at_start)) {
      children.push(
        el('p', { className: 'ws-goal-note', text: 'No toolbox chosen for this goal yet.' })
      );
    } else if (policy.use_current_at_start) {
      children.push(
        el('p', {
          className: 'ws-goal-note',
          text: 'Uses the current toolbox when this goal starts.'
        })
      );
    } else {
      // Pinning is the default precisely so an edit elsewhere cannot silently
      // change what a recurring goal does (FR-104). Saying which version it is
      // pinned to is how that promise stays visible.
      children.push(
        el('p', {
          className: 'ws-goal-note',
          text:
            'Pinned to version ' +
            policy.toolbox_version +
            '. Editing that toolbox will not change this goal.'
        })
      );
      children.push(
        el('button', {
          className: 'modern-btn modern-btn-secondary modern-btn-sm',
          text: 'Use the current toolbox when this goal starts',
          attrs: { type: 'button', 'data-goal-use-current': 'true' },
          disabled: state.busy === 'policy'
        })
      );
    }
    return el('section', { className: 'ws-goal-policy' }, children);
  }

  function recommendationCard(candidate) {
    const children = [
      el('div', { className: 'ws-goal-rec-head' }, [
        el('span', {
          className: 'ws-goal-rec-name',
          text: candidate.toolbox_name + ' v' + candidate.toolbox_version
        }),
        candidate.rank === 1 && candidate.fully_covers
          ? el('span', { className: 'ws-goal-chip is-best', text: 'Best match' })
          : null,
        candidate.is_current ? el('span', { className: 'ws-goal-chip', text: 'Current' }) : null,
        el('span', { className: 'ws-goal-chip', text: candidate.readiness })
      ]),
      el('p', { className: 'ws-goal-rec-why', text: candidate.explanation }),
      el('ul', { className: 'ws-goal-brief-facts' }, [
        el('li', {
          text: candidate.skill_spaces + ' skill spaces · ' + candidate.operations + ' tools'
        }),
        el('li', { text: 'Focus: ' + ((candidate.focus || {}).state || 'unknown') })
      ])
    ];

    // Permissions a candidate would introduce must be visible BEFORE it is
    // chosen, not after (FR-98).
    if ((candidate.introduces_permissions || []).length) {
      children.push(
        el('p', {
          className: 'ws-goal-warn',
          text: 'Introduces ' + candidate.introduces_permissions.join(', ') + '.'
        })
      );
    }
    if (candidate.exceeds_autonomy) {
      children.push(
        el('p', {
          className: 'ws-goal-warn',
          text: 'This goes beyond the autonomy you set for this goal.'
        })
      );
    }

    children.push(
      el('div', { className: 'ws-goal-actions' }, [
        // Choosing pins the version. It does NOT apply the toolbox to the
        // agent — that goes through the Toolbox preview and its own review
        // gate (FR-99, FR-100).
        el('button', {
          className: 'modern-btn modern-btn-primary modern-btn-sm',
          text: 'Use for this goal',
          attrs: {
            type: 'button',
            'data-goal-pin': candidate.toolbox_id,
            'data-goal-version': candidate.toolbox_version,
            'aria-label': 'Pin ' + candidate.toolbox_name + ' for this goal'
          },
          disabled: state.busy === 'policy'
        }),
        el('a', {
          className: 'ws-goal-inspect',
          text: 'Inspect in the Workshop',
          attrs: { href: '#workspace-toolbox-panel' }
        })
      ])
    );

    return el(
      'article',
      {
        className: 'ws-goal-rec' + (candidate.fully_covers ? ' is-covering' : ''),
        attrs: { 'data-goal-rec': candidate.toolbox_id }
      },
      children
    );
  }

  function recommendationsView() {
    const result = state.recommendations;
    if (!result) return null;
    const children = [el('h4', { text: 'Recommended toolboxes' })];

    if (result.message) {
      children.push(el('p', { className: 'ws-goal-note', text: result.message }));
    }
    for (const candidate of result.recommendations || []) {
      children.push(recommendationCard(candidate));
    }

    // FR-101: when nothing covers the brief, show the honest gap and an INERT
    // proposal rather than a false ready match.
    if (result.proposed_variant) {
      const variant = result.proposed_variant;
      children.push(
        el('section', { className: 'ws-goal-variant' }, [
          el('h5', { text: 'A toolbox that would cover this' }),
          el('p', { className: 'ws-goal-note', text: variant.explanation }),
          (variant.unavailable_requirements || []).length
            ? el('p', {
                className: 'ws-goal-warn',
                text:
                  'Not set up in this workspace yet: ' + variant.unavailable_requirements.join(', ')
              })
            : null,
          el('p', {
            className: 'ws-goal-note',
            text: 'Nothing has been created. Build it in the Workshop when you are ready.'
          })
        ])
      );
    }
    return el('section', { className: 'ws-goal-recs' }, children);
  }

  // One live region per host, reused across renders. Creating a fresh
  // aria-live node each time makes assistive tech announce whatever it holds
  // every render — including renders caused by nothing the user did. Keeping
  // the same node and only writing to it when the text actually changes means
  // a result is announced once, when it happens (FR-164).
  function syncLiveRegion(host) {
    let region = host.__goalLiveRegion;
    if (!region) {
      region = el('p', { className: 'ws-goal-live' });
      region.setAttribute('role', 'status');
      region.setAttribute('aria-live', 'polite');
      host.__goalLiveRegion = region;
    }
    const message = state.error || state.notice || '';
    // A failure interrupts; a success waits its turn.
    region.setAttribute('aria-live', state.error ? 'assertive' : 'polite');
    if (region.__lastMessage !== message) {
      region.__lastMessage = message;
      region.textContent = message;
    }
    return region;
  }

  function render() {
    const host = hostNode();
    if (!host) return;
    const liveRegion = syncLiveRegion(host);
    host.innerHTML = '';
    host.appendChild(liveRegion);

    if (state.loading && !state.accepted && !state.proposed) {
      host.appendChild(el('p', { className: 'ws-goal-note', text: 'Preparing…' }));
      return;
    }
    if (state.error) {
      host.appendChild(el('p', { className: 'ws-goal-error', text: state.error }));
    }
    if (state.notice) {
      host.appendChild(el('p', { className: 'ws-goal-notice', text: state.notice }));
    }

    const editor = briefEditor();
    host.appendChild(editor || briefView());
    host.appendChild(policyView());
    const recommendations = recommendationsView();
    if (recommendations) host.appendChild(recommendations);
  }

  function hostNode() {
    if (typeof document === 'undefined' || typeof document.getElementById !== 'function') {
      return null;
    }
    return document.getElementById(HOST_ID);
  }

  function bindHost(host) {
    if (!host || host.dataset?.goalPrepareBound === 'true') return;
    if (host.dataset) host.dataset.goalPrepareBound = 'true';

    host.addEventListener('input', event => {
      const target = event.target && event.target.closest ? event.target : null;
      const field = target && target.closest('[data-goal-field]');
      if (field) setDraftField(field.getAttribute('data-goal-field'), field.value);
    });
    host.addEventListener('change', event => {
      const target = event.target && event.target.closest ? event.target : null;
      const field = target && target.closest('[data-goal-field]');
      if (field) setDraftField(field.getAttribute('data-goal-field'), field.value);
    });

    host.addEventListener('click', event => {
      const target = event.target && event.target.closest ? event.target : null;
      if (!target) return;

      const handlers = [
        ['[data-goal-edit-brief]', () => beginEdit()],
        ['[data-goal-cancel-brief]', () => cancelEdit()],
        ['[data-goal-accept-brief]', () => void acceptBrief()],
        ['[data-goal-use-current]', () => void setPolicy('', 0, true)],
        [
          '[data-goal-pin]',
          node =>
            void setPolicy(
              node.getAttribute('data-goal-pin'),
              Number(node.getAttribute('data-goal-version')) || 0,
              false
            )
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

  async function init() {
    if (!wsId()) return;
    const host = hostNode();
    if (!host) return;
    bindHost(host);
    await load();
  }

  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    document.addEventListener('DOMContentLoaded', () => void init());
  }

  window.WorkspaceGoalPrepare = {
    init,
    load,
    render,
    bindHost,
    state: () => ({ ...state }),
    _reset: () => {
      state.workspaceId = '';
      state.accepted = null;
      state.proposed = null;
      state.policy = null;
      state.recommendations = null;
      state.draft = null;
      state.loading = false;
      state.busy = '';
      state.error = '';
      state.notice = '';
    },
    _setWorkspace: id => {
      state.workspaceId = String(id || '');
    },
    _draft: () => (state.draft ? { ...state.draft } : null)
  };
})();
