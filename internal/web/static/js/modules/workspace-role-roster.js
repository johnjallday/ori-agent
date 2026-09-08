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
    cancel: 'Cancel',
    scopeProject: 'this workspace only',
    scopeHome: 'group scope only',
    sourceCreated: 'New agent',
    sourceAssigned: 'Your saved agent',
    defaultProvider: 'Use Ori default',
    defaultModel: 'Use provider default',
    openEditor: 'Open in Agents',
    emptyRoster: 'This blueprint declares no roles.'
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
          declarationIndex: index
        };
        // A role can only be filled by an agent; a projection claiming otherwise
        // is treated as empty rather than drawn as a row with no one in it.
        if (normalized.state === STATE_FILLED && !normalized.agent) {
          normalized.state = STATE_EMPTY;
        }
        normalized.designation = normalized.primary ? COPY.primary : COPY.specialist;
        normalized.scopeLine = scopeLine(normalized.scope);
        normalized.tag = stateTag(normalized);
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
    return window.AgentAvatar.markup(
      {
        name: agent.name,
        role: agent.role,
        type: agent.type,
        appearance: agent.appearance || null
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

  function optionsInto(select, entries, placeholder) {
    select.replaceChildren();
    var first = document.createElement('option');
    first.value = '';
    first.textContent = placeholder;
    select.append(first);
    entries.forEach(function (entry) {
      var option = document.createElement('option');
      option.value = entry.value;
      option.textContent = entry.label;
      select.append(option);
    });
  }

  function providerEntries(providers) {
    return (Array.isArray(providers) ? providers : [])
      .map(function (provider) {
        return {
          value: text(provider && provider.name),
          label: text(provider && provider.display_name)
        };
      })
      .filter(function (entry) {
        return entry.value && entry.value !== 'default';
      })
      .map(function (entry) {
        return { value: entry.value, label: entry.label || entry.value };
      });
  }

  function modelEntries(providers, providerName) {
    var provider = (Array.isArray(providers) ? providers : []).find(function (candidate) {
      return text(candidate && candidate.name) === providerName;
    });
    return (provider && Array.isArray(provider.models) ? provider.models : [])
      .map(function (model) {
        var value = text(model && (model.value || model.id));
        return { value: value, label: text(model && model.label) || value };
      })
      .filter(function (entry) {
        return entry.value;
      });
  }

  // The Create form expands IN PLACE. It must never open a dialog: the Create
  // Workspace modal is full-screen, and a dialog raised from it opens
  // underneath it.
  function createForm(row, options) {
    var form = element('form', 'ws-role-row__create');
    form.noValidate = true;

    var nameField = element('label', 'ws-role-row__field');
    nameField.append(element('span', 'ws-role-row__field-label', 'Name'));
    var name = document.createElement('input');
    name.type = 'text';
    name.className = 'form-control form-control-sm';
    // Prefilled from the role label, which is what the user just read (FR14).
    name.value = row.label;
    name.setAttribute('aria-label', 'Name the agent for ' + row.label);
    nameField.append(name);

    var providerField = element('label', 'ws-role-row__field');
    providerField.append(element('span', 'ws-role-row__field-label', 'Provider'));
    var provider = document.createElement('select');
    provider.className = 'form-select form-select-sm';
    provider.setAttribute('aria-label', 'Provider for ' + row.label);
    optionsInto(provider, providerEntries(options.providers), COPY.defaultProvider);
    providerField.append(provider);

    var modelField = element('label', 'ws-role-row__field');
    modelField.append(element('span', 'ws-role-row__field-label', 'Model'));
    var model = document.createElement('select');
    model.className = 'form-select form-select-sm';
    model.setAttribute('aria-label', 'Model for ' + row.label);
    optionsInto(model, [], COPY.defaultProvider);
    model.disabled = true;
    modelField.append(model);

    provider.addEventListener('change', function () {
      var selected = provider.value;
      optionsInto(
        model,
        modelEntries(options.providers, selected),
        selected ? COPY.defaultModel : COPY.defaultProvider
      );
      model.disabled = !selected;
    });

    var error = element('p', 'ws-role-row__error');
    error.hidden = true;

    var actions = element('div', 'ws-role-row__create-actions');
    var confirm = actionButton(
      COPY.create,
      'Create an agent for ' + row.label,
      'btn btn-sm btn-primary'
    );
    confirm.type = 'submit';
    var cancel = actionButton(
      COPY.cancel,
      'Cancel creating an agent for ' + row.label,
      'btn btn-sm btn-link'
    );
    actions.append(confirm, cancel);

    form.append(nameField, providerField, modelField, error, actions);

    form.addEventListener('submit', function (event) {
      event.preventDefault();
      var value = text(name.value);
      if (!value) {
        error.textContent = 'Give this agent a name.';
        error.hidden = false;
        name.focus();
        return;
      }
      error.hidden = true;
      options.onCreate(
        row.roleId,
        { name: value, provider: provider.value, model: model.value },
        row
      );
    });
    cancel.addEventListener('click', function () {
      options.onCancelCreate(row.roleId, row);
    });

    // The blocker a name collision raises belongs on the row that caused it,
    // with the recovery beside it (FR26). The host supplies the message.
    form.showBlocker = function (message, recovery) {
      error.replaceChildren(document.createTextNode(message));
      if (recovery && recovery.label && typeof recovery.onSelect === 'function') {
        var action = actionButton(
          recovery.label,
          recovery.label + ' for ' + row.label,
          'btn btn-sm btn-link'
        );
        action.addEventListener('click', recovery.onSelect);
        error.append(document.createTextNode(' '), action);
      }
      error.hidden = false;
      name.focus();
      name.select();
    };
    form.focusFirstField = function () {
      name.focus();
      name.select();
    };
    return form;
  }

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

    // While this row's Create form is open it IS the row's action set. Leaving
    // the buttons above it would put two controls called "Create an agent for
    // Mix Engineer" on the page, which is ambiguous to anyone navigating by
    // accessible name.
    var creatingHere = row.state === STATE_EMPTY && options.creatingRoleId === row.roleId;
    if (creatingHere) {
      item.append(createForm(row, options));
      item.rosterCreateForm = item.lastChild;
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

  function noop() {}

  function withDefaults(options) {
    var given = options || {};
    return {
      title: text(given.title) || 'Roles',
      providers: given.providers,
      creatingRoleId: text(given.creatingRoleId),
      groupWorkspaceHref: given.groupWorkspaceHref,
      agentHref: typeof given.agentHref === 'function' ? given.agentHref : null,
      onRequestCreate: given.onRequestCreate || noop,
      onCancelCreate: given.onCancelCreate || noop,
      onCreate: given.onCreate || noop,
      onAssign: given.onAssign || noop,
      onClear: given.onClear || noop,
      showHeader: given.showHeader !== false
    };
  }

  // render replaces the container's contents with the roster. It returns the
  // rows it drew so a caller can move focus to one, which is how focus lands
  // back on the acted-on role after a fill or clear.
  function render(container, roster, options) {
    if (!container) return [];
    var opts = withDefaults(options);
    var rows = rowsFrom(roster);
    container.replaceChildren();

    var list = element('ul', 'ws-role-roster__list');
    // A list with an accessible name, so the roster announces itself as one
    // thing rather than a run of unlabelled buttons (FR70).
    list.setAttribute('aria-label', opts.title);

    if (opts.showHeader) {
      var header = element('p', 'ws-role-roster__count', headerCount(roster));
      container.append(header);
    }
    if (!rows.length) {
      container.append(element('p', 'ws-role-roster__empty', COPY.emptyRoster));
      return [];
    }
    rows.forEach(function (row) {
      list.append(renderRow(row, opts));
    });
    container.append(list);

    if (opts.creatingRoleId) {
      var open = list.querySelector('[data-role-id="' + CSS.escape(opts.creatingRoleId) + '"]');
      if (open && open.rosterCreateForm && open.rosterCreateForm.focusFirstField) {
        open.rosterCreateForm.focusFirstField();
      }
    }
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
    render: render,
    focusRole: focusRole
  };

  if (typeof window !== 'undefined') {
    window.WorkspaceRoleRoster = api;
  }
})();
