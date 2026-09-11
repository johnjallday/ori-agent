/*
 * Workspace role roster — the one component that draws a blueprint's roles.
 *
 * A role is a slot, not an agent. It is empty until someone fills it, by
 * creating a new agent for it or by assigning one they already have. An empty
 * role is an invitation, not a failure: "Missing" never uses the danger palette
 * and never sets role="alert" (FR6).
 *
 * Both surfaces render from here — the Create Workspace Team step, where
 * nothing is persisted yet, and the workspace's own roster, where every action
 * persists immediately. They pass the same projection shape and differ only in
 * their callbacks, so the two can never describe different teams (FR20/FR33).
 *
 * This module does no fetching and no persistence. It maps a roster projection
 * to rows, draws them, and calls back. The host owns "which roles are filled" —
 * adding a second copy of that here is exactly the bug the draft module exists
 * to prevent.
 *
 * Loaded as a classic deferred script so it is installed before the wizard and
 * workspace controllers run; `window.WorkspaceRoleRoster` is also the surface
 * the unit tests evaluate in a node:vm sandbox.
 */
(function () {
  'use strict';

  var STATE_EMPTY = 'empty';
  var STATE_FILLED = 'filled';

  var SOURCE_CREATED = 'created';
  var SOURCE_ASSIGNED = 'assigned';

  var SCOPE_HOME = 'home';
  var SCOPE_PROJECT = 'project';

  // Written once, shared between surfaces. A tag that reads differently in the
  // wizard and on the workspace is the same defect as a count that disagrees.
  var COPY = {
    missing: 'Missing',
    optional: 'Optional',
    primary: 'PRIMARY',
    specialist: 'SPECIALIST',
    create: 'Create',
    assign: 'Assign…',
    clear: 'Clear',
    needsClear: 'Needs clear',
    scopeProject: 'this workspace only',
    scopeHome: 'group scope only',
    sourceCreated: 'New agent',
    sourceAssigned: 'Your saved agent',
    sourceGroup: 'Existing group holder',
    openEditor: 'Open in Agents',
    emptyRoster: 'This blueprint declares no roles.',
    alsoHere: 'Also in this workspace',
    giveRole: 'Give a role…'
  };

  // Display order (FR40). The primary slot leads whatever its state, because it
  // is the one that decides whether the workspace can do anything at all. Then
  // the roles still waiting on a required decision, then the settled ones, and
  // last the optional roles nobody has to fill — quieter, and below Missing.
  var BUCKET_PRIMARY = 0;
  var BUCKET_REQUIRED_EMPTY = 1;
  var BUCKET_FILLED = 2;
  var BUCKET_OPTIONAL_EMPTY = 3;

  function text(value) {
    return String(value == null ? '' : value).trim();
  }

  function sortBucket(role) {
    if (role.primary) return BUCKET_PRIMARY;
    if (role.state === STATE_FILLED) return BUCKET_FILLED;
    return role.required ? BUCKET_REQUIRED_EMPTY : BUCKET_OPTIONAL_EMPTY;
  }

  function scopeLine(scope) {
    return scope === SCOPE_HOME ? COPY.scopeHome : COPY.scopeProject;
  }

  function sourceLabel(source) {
    if (source === 'group') return COPY.sourceGroup;
    return source === SOURCE_ASSIGNED ? COPY.sourceAssigned : COPY.sourceCreated;
  }

  // The state tag carries the state in WORDS (FR67). Colour is decoration on
  // top of it, never the only signal.
  function stateTag(role) {
    if (role.state === STATE_FILLED) {
      return { key: 'filled', label: text(role.agent && role.agent.name) };
    }
    return role.required
      ? { key: 'missing', label: COPY.missing }
      : { key: 'optional', label: COPY.optional };
  }

  // rowsFrom is the whole projection→view mapping, kept pure so the tests can
  // assert the states and the order without a DOM.
  function rowsFrom(roster) {
    var roles = (roster && Array.isArray(roster.roles) ? roster.roles : []).map(
      function (role, index) {
        var normalized = {
          roleId: text(role && role.role_id),
          label: text(role && role.label),
          description: text(role && role.description),
          scope: text(role && role.scope) === SCOPE_HOME ? SCOPE_HOME : SCOPE_PROJECT,
          required: Boolean(role && role.required),
          primary: Boolean(role && role.primary),
          state: text(role && role.state) === STATE_FILLED ? STATE_FILLED : STATE_EMPTY,
          agent: (role && role.agent) || null,
          source: text(role && role.source),
          readOnly: Boolean(role && role.read_only),
          readOnlyReason: text(role && role.read_only_reason),
          needsClear: Boolean(role && role.needs_clear),
          // What the blueprint proposes for this role, so the Create form can
          // show the instructions the agent would actually get rather than an
          // empty box that hides them.
          proposed: (role && role.proposed) || null,
          declarationIndex: index
        };
        // A role can only be filled by an agent; a projection claiming otherwise
        // is treated as empty rather than drawn as a row with no one in it.
        if (normalized.state === STATE_FILLED && !normalized.agent) {
          normalized.state = STATE_EMPTY;
        }
        normalized.designation = normalized.primary
          ? normalized.scope === SCOPE_HOME
            ? 'GROUP COORDINATOR'
            : 'PROJECT LEAD'
          : COPY.specialist;
        normalized.scopeLine = scopeLine(normalized.scope);
        normalized.tag = normalized.needsClear
          ? { key: 'stale', label: COPY.needsClear }
          : stateTag(normalized);
        normalized.sourceLabel =
          normalized.state === STATE_FILLED ? sourceLabel(normalized.source) : '';
        normalized.quiet = normalized.state === STATE_EMPTY && !normalized.required;
        normalized.bucket = sortBucket(normalized);
        return normalized;
      }
    );

    roles.sort(function (left, right) {
      if (left.bucket !== right.bucket) return left.bucket - right.bucket;
      return left.declarationIndex - right.declarationIndex;
    });
    return roles;
  }

  // headerCount is the roster's one-line answer to "how staffed is this?"
  // (FR37). It counts what the projection says, never a separate tally.
  function headerCount(roster) {
    var roles = roster && Array.isArray(roster.roles) ? roster.roles : [];
    var filled = roles.filter(function (role) {
      return text(role && role.state) === STATE_FILLED;
    }).length;
    return filled + ' of ' + roles.length + ' role' + (roles.length === 1 ? '' : 's') + ' filled';
  }

  function element(tag, className, textContent) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (textContent != null) node.textContent = textContent;
    return node;
  }

  function avatarMarkup(agent) {
    if (!agent) return '';
    if (typeof window === 'undefined' || !window.AgentAvatar || !window.AgentAvatar.markup)
      return '';
    var appearance = agent.appearance || null;
    var characterId = String(
      (appearance && appearance.character && appearance.character.catalog_id) || ''
    ).trim();
    return window.AgentAvatar.markup(
      {
        name: agent.name,
        role: agent.role,
        type: agent.type,
        appearance: appearance,
        // Character portraits require their catalog asset as well as the saved
        // appearance choice. Keep this synchronous, like the other workspace
        // roster: an unloaded or withdrawn entry truthfully falls back.
        character:
          characterId && window.CharacterCatalog ? window.CharacterCatalog.get(characterId) : null
      },
      { size: 32, className: 'ws-role-row__avatar' }
    );
  }

  // Every action names the role it acts on, so a screen reader hears "Assign an
  // agent to Mix Engineer" rather than the fourth "Assign" on the page (FR70).
  function actionButton(label, accessibleName, className) {
    var button = element('button', className, label);
    button.type = 'button';
    button.setAttribute('aria-label', accessibleName);
    return button;
  }

  // NOTE: this component deliberately owns no form. Create hands the role back
  // to its host, which opens the app's canonical Create Agent modal prefilled
  // for that role. Two reasons: the app already has ONE agent-creation form
  // (agent-create-form.js, mounted into #addAgentModal), and a second, poorer
  // one here would be exactly the "never add a second creation form" problem —
  // this roster's three fields could not offer type, system prompt,
  // temperature, or web tools.
  //
  // The Create Workspace wizard reaches that modal without nesting: it
  // SUSPENDS itself, shows the agent modal, and restores on close. That swap is
  // the established pattern in this step, which is why "never open a dialog"
  // does not apply — nothing is ever stacked on the full-screen surface.
  function renderRow(row, options) {
    var item = element('li', 'ws-role-row');
    item.dataset.roleId = row.roleId;
    item.dataset.state = row.state;
    if (row.quiet) item.classList.add('is-quiet');
    if (row.readOnly) item.classList.add('is-read-only');
    // Focus returns here after an action resolves (FR24/FR69), so the row has
    // to be able to take focus without becoming a tab stop of its own.
    item.tabIndex = -1;

    var head = element('div', 'ws-role-row__head');
    var identity = element('div', 'ws-role-row__identity');
    identity.append(element('span', 'ws-role-row__label', row.label));
    identity.append(element('span', 'ws-role-row__designation', row.designation));
    head.append(identity);

    var tag = element('span', 'ws-role-tag ws-role-tag--' + row.tag.key);
    if (row.state === STATE_FILLED) {
      var markup = avatarMarkup(row.agent);
      if (markup) {
        var avatar = element('span', 'ws-role-tag__avatar');
        avatar.innerHTML = markup;
        tag.append(avatar);
      }
      tag.append(element('span', 'ws-role-tag__name', row.tag.label));
      tag.append(element('span', 'ws-role-tag__source', row.sourceLabel));
    } else {
      tag.textContent = row.tag.label;
    }
    head.append(tag);
    item.append(head);

    var meta = element('p', 'ws-role-row__meta', row.scopeLine);
    item.append(meta);
    if (row.description) {
      item.append(element('p', 'ws-role-row__description', row.description));
    }

    if (row.readOnly) {
      var readOnly = element('p', 'ws-role-row__read-only', row.readOnlyReason);
      if (options.groupWorkspaceHref) {
        var link = element('a', 'ws-role-row__link', 'Open the group workspace');
        link.href = options.groupWorkspaceHref;
        readOnly.append(document.createTextNode(' '), link);
      }
      item.append(readOnly);
      return item;
    }

    var actions = element('div', 'ws-role-row__actions');
    if (row.state === STATE_FILLED) {
      if (typeof options.agentHref === 'function') {
        var editor = element('a', 'ws-role-row__link btn btn-sm btn-link', COPY.openEditor);
        editor.href = options.agentHref(row.agent.name);
        // /agents is the editor of record; the roster links to it rather than
        // growing a second place to edit an agent (FR35).
        editor.setAttribute('aria-label', 'Open ' + row.agent.name + ' in Agents');
        actions.append(editor);
      }
      var clear = actionButton(
        COPY.clear,
        'Clear ' + row.label,
        'btn btn-sm btn-outline-secondary'
      );
      clear.addEventListener('click', function () {
        options.onClear(row.roleId, row);
      });
      actions.append(clear);
    } else if (row.needsClear) {
      var clearStale = actionButton(
        COPY.clear,
        'Clear stale assignment from ' + row.label,
        'btn btn-sm btn-outline-secondary'
      );
      clearStale.addEventListener('click', function () {
        options.onClear(row.roleId, row);
      });
      actions.append(clearStale);
    } else {
      var create = actionButton(
        COPY.create,
        'Create an agent for ' + row.label,
        'btn btn-sm btn-primary'
      );
      create.addEventListener('click', function () {
        options.onRequestCreate(row.roleId, row);
      });
      var assign = actionButton(
        COPY.assign,
        'Assign an agent to ' + row.label,
        'btn btn-sm btn-outline-secondary'
      );
      assign.addEventListener('click', function () {
        options.onAssign(row.roleId, row);
      });
      actions.append(create, assign);
    }
    item.append(actions);
    return item;
  }

  function requiredProgress(rows) {
    var required = (Array.isArray(rows) ? rows : []).filter(function (row) {
      return row.required;
    });
    var filled = required.filter(function (row) {
      return row.state === STATE_FILLED;
    }).length;
    if (!required.length) return 'Optional roles · none required';
    return (
      filled +
      ' of ' +
      required.length +
      ' required role' +
      (required.length === 1 ? '' : 's') +
      ' filled'
    );
  }

  function sectionsFrom(rows, options) {
    var list = Array.isArray(rows) ? rows : [];
    var home = list.filter(function (row) {
      return row.scope === SCOPE_HOME;
    });
    var project = list.filter(function (row) {
      return row.scope === SCOPE_PROJECT;
    });
    if (!home.length || !project.length) {
      return [{ scope: home.length ? SCOPE_HOME : SCOPE_PROJECT, title: '', rows: list }];
    }
    return [
      {
        scope: SCOPE_HOME,
        title: text(options && options.groupTitle) || 'Group coordination',
        rows: home
      },
      {
        scope: SCOPE_PROJECT,
        title: text(options && options.projectTitle) || 'Project team',
        rows: project
      }
    ];
  }

  function noop() {}

  function withDefaults(options) {
    var given = options || {};
    return {
      title: text(given.title) || 'Roles',
      groupTitle: text(given.groupTitle),
      projectTitle: text(given.projectTitle),
      groupWorkspaceHref: given.groupWorkspaceHref,
      agentHref: typeof given.agentHref === 'function' ? given.agentHref : null,
      // Create hands the role back; the host opens the canonical Create Agent
      // modal for it. This component never collects the values itself.
      onRequestCreate: given.onRequestCreate || noop,
      onAssign: given.onAssign || noop,
      onClear: given.onClear || noop,
      // Optional: when absent, unbound agents are listed without an action,
      // which is the right answer for the wizard where none exist yet.
      onAssignExisting: given.onAssignExisting,
      showHeader: given.showHeader !== false
    };
  }

  // Agents attached to the workspace that hold no declared role. A workspace
  // created before this feature has all of its agents here, and so does one
  // where somebody was added outside the roster — listing them is what keeps
  // the roster an honest picture of who is in the workspace (FR63, FR38).
  //
  // Each can be given a role in place, so the answer to "why is this agent
  // here?" comes with the way to resolve it.
  function renderUnassigned(container, roster, options) {
    var agents = roster && Array.isArray(roster.unassigned) ? roster.unassigned : [];
    if (!agents.length) return;

    var section = element('section', 'ws-role-roster__also');
    section.append(element('h4', 'ws-role-roster__also-title', COPY.alsoHere));

    var list = element('ul', 'ws-role-roster__also-list');
    list.setAttribute('aria-label', COPY.alsoHere);
    agents.forEach(function (agent) {
      var item = element('li', 'ws-role-roster__also-row');
      var markup = avatarMarkup(agent);
      if (markup) {
        var avatar = element('span', 'ws-role-tag__avatar');
        avatar.innerHTML = markup;
        item.append(avatar);
      }
      item.append(element('span', 'ws-role-roster__also-name', agent.name));
      if (typeof options.onAssignExisting === 'function') {
        var assign = actionButton(
          COPY.giveRole,
          'Give ' + agent.name + ' a role',
          'btn btn-sm btn-outline-secondary'
        );
        assign.addEventListener('click', function () {
          options.onAssignExisting(agent);
        });
        item.append(assign);
      }
      list.append(item);
    });
    section.append(list);
    container.append(section);
  }

  // render replaces the container's contents with the roster. It returns the
  // rows it drew so a caller can move focus to one, which is how focus lands
  // back on the acted-on role after a fill or clear.
  function render(container, roster, options) {
    if (!container) return [];
    var opts = withDefaults(options);
    var rows = rowsFrom(roster);
    container.replaceChildren();

    var sections = sectionsFrom(rows, opts);
    if (opts.showHeader && sections.length === 1) {
      var header = element('p', 'ws-role-roster__count', headerCount(roster));
      container.append(header);
    }
    if (!rows.length) {
      container.append(element('p', 'ws-role-roster__empty', COPY.emptyRoster));
      return [];
    }
    sections.forEach(function (scopeSection) {
      var list = element('ul', 'ws-role-roster__list');
      list.setAttribute('aria-label', scopeSection.title || opts.title);
      scopeSection.rows.forEach(function (row) {
        list.append(renderRow(row, opts));
      });
      if (sections.length === 1) {
        container.append(list);
        return;
      }
      var section = element('section', 'ws-role-roster__scope');
      section.dataset.scope = scopeSection.scope;
      section.append(element('h4', 'ws-role-roster__scope-title', scopeSection.title));
      section.append(
        element('p', 'ws-role-roster__scope-progress', requiredProgress(scopeSection.rows))
      );
      section.append(list);
      container.append(section);
    });
    renderUnassigned(container, roster, opts);
    return rows;
  }

  // focusRole puts focus back on one role's row. Callers use it after an action
  // resolves so the user is returned to where they were working (FR24).
  function focusRole(container, roleId) {
    if (!container || !roleId) return false;
    var row = container.querySelector('[data-role-id="' + CSS.escape(String(roleId)) + '"]');
    if (!row) return false;
    row.focus();
    return true;
  }

  var api = {
    COPY: COPY,
    STATE_EMPTY: STATE_EMPTY,
    STATE_FILLED: STATE_FILLED,
    SOURCE_CREATED: SOURCE_CREATED,
    SOURCE_ASSIGNED: SOURCE_ASSIGNED,
    SCOPE_HOME: SCOPE_HOME,
    SCOPE_PROJECT: SCOPE_PROJECT,
    BUCKETS: {
      PRIMARY: BUCKET_PRIMARY,
      REQUIRED_EMPTY: BUCKET_REQUIRED_EMPTY,
      FILLED: BUCKET_FILLED,
      OPTIONAL_EMPTY: BUCKET_OPTIONAL_EMPTY
    },
    rowsFrom: rowsFrom,
    headerCount: headerCount,
    requiredProgress: requiredProgress,
    sectionsFrom: sectionsFrom,
    render: render,
    focusRole: focusRole
  };

  if (typeof window !== 'undefined') {
    window.WorkspaceRoleRoster = api;
  }
})();
