// Shared, inert presentation for folder setup. Classic-script/module seam like
// folder-plan-lines.js: no requests, storage, timers or setup authority here.
(function () {
  'use strict';

  const text = value => (typeof value === 'string' ? value : '');
  const list = value => (Array.isArray(value) ? value : []);
  const operations = { create: 'Proposed · new', reuse: 'Proposed · reuse' };
  const kinds = { workspace: 'Workspace', group: 'Workspace group', home: 'Home' };

  function resultView(offer) {
    const outcome = offer.outcome;
    const rows = list(outcome?.receipt);
    const kind = { project: 'workspace', group: 'group', home: 'home' }[outcome?.kind];
    const resource = rows.find(row => row.kind === (kind === 'home' ? 'home' : 'workspace'));
    const source = text(offer.subject?.name) || text(offer.folder) || 'Selected folder';
    const entity =
      kind && text(resource?.name)
        ? {
            kind,
            name: text(resource.name),
            status: { create: 'Created', reuse: 'Reused' }[resource.operation] || 'Ready'
          }
        : null;
    const member = rows.find(
      row => row.kind === 'agent' && row.assigned === true && text(row.name)
    );
    const agent = member
      ? {
          name: text(member.name),
          role: '',
          model: text(member.model),
          status: 'Confirmed workspace member'
        }
      : null;
    const parent = outcome?.parent;
    const placement =
      parent?.status === 'standalone' || (!parent && resource?.placement === 'standalone')
        ? 'Separate workspace · no parent Home'
        : text(parent?.name)
          ? `In ${parent.kind === 'group' ? 'group' : 'Home'} · ${parent.name}`
          : kind === 'home'
            ? 'Home library'
            : kind === 'group'
              ? 'Workspace group'
              : 'Parent not reported';
    return {
      source,
      entity,
      agent,
      parent: placement,
      entries: [],
      sourceNote: rows.some(row => row.kind === 'folder')
        ? 'Linked where it is · kept in place'
        : 'Source folder · kept in place',
      summary: `${source}. ${entity ? `${entity.status} ${kinds[kind]} ${entity.name}.` : 'See the saved receipt.'} ${placement}.${agent ? ` Confirmed member: ${agent.name}.` : ' Agent membership not reported.'}`,
      footer: !entity
        ? 'See the saved receipt for the confirmed result.'
        : kind === 'group'
          ? 'Member workspaces are listed in the receipt. No automatic task.'
          : kind === 'home'
            ? 'A library listing does not mean every project has a workspace.'
            : 'Workspace ready. First-look status is separate.'
    };
  }

  function visualView({ observation, offer, plan = offer?.plan } = {}) {
    if (offer?.status === 'resolved') return resultView(offer);
    const visual = offer?.setup?.visual || plan?.visual;
    const proposed = visual?.entity;
    const entity =
      proposed && kinds[proposed.kind] && operations[proposed.operation]
        ? { kind: proposed.kind, name: text(proposed.name), status: operations[proposed.operation] }
        : null;
    const setup = offer?.setup;
    const done = kind =>
      list(setup?.lines).some(line => line.kind === kind && line.state === 'done');
    if (entity && done(entity.kind))
      entity.status = proposed.operation === 'reuse' ? 'Reused' : 'Created';
    const destination = setup?.destination || plan?.destination || offer?.destination;
    let parent = 'Destination not yet reviewed';
    if (
      destination?.status === 'standalone' ||
      (!destination && visual?.placement === 'standalone')
    )
      parent = 'Separate workspace · no parent Home';
    else if (['new', 'existing'].includes(destination?.status) && text(destination.name))
      parent = `${destination.status === 'new' ? 'Proposed new' : 'Existing'} ${destination.kind === 'group' ? 'group' : destination.kind === 'project' ? 'project' : 'Home'} · ${destination.name}`;
    const hire = offer?.setup?.hire || plan?.hire;
    const agent =
      hire && ['create', 'bind', 'pending'].includes(hire.mode)
        ? {
            name: hire.mode === 'pending' ? '' : text(hire.agent_name),
            role: text(hire.role_label),
            model: text(hire.model),
            status:
              hire.mode === 'bind'
                ? 'Proposed assignment'
                : hire.mode === 'pending'
                  ? 'Identity pending'
                  : 'Proposed new agent'
          }
        : null;
    if (agent && done('agents'))
      agent.status = 'Planned agent · staffing complete; identity awaits receipt';
    const source =
      text(offer?.subject?.name) ||
      text(observation?.folder) ||
      text(offer?.folder) ||
      'Selected folder';
    // Observations belong to the chosen root, never to a guessed child scope.
    const observed = observation && (!offer || source === observation.folder);
    const entries = observed
      ? list(observation.tree?.nodes)
          .filter(row => !row.parent_id)
          .slice(0, 3)
      : [];
    return {
      source,
      entity,
      parent,
      agent,
      entries: entries.map(row => ({
        name: text(row.name),
        kind: row.kind === 'folder' ? 'Folder' : 'File'
      })),
      sourceNote: done('folder')
        ? 'Folder link complete · kept in place'
        : observed && !setup && !plan
          ? 'Observed names only · contents not read'
          : 'Source folder · kept in place',
      summary: entity
        ? `${source} → ${entity.status.toLowerCase()} ${kinds[entity.kind].toLowerCase()} ${entity.name}. ${parent}.${agent ? ` ${agent.status}: ${agent.name || agent.role || 'agent not yet known'}.` : ''}${setup ? ' Status comes from the saved run.' : ' Nothing here proves creation or assignment.'}`
        : `${source}. Destination and agent are not yet reviewed.${setup ? ' Setup status comes from the saved run.' : ' No setup has started.'}`,
      footer: setup
        ? 'Setup is separate from the first look. Completed work is kept.'
        : entity
          ? 'Proposed setup · nothing created or assigned yet'
          : 'Review a supported setup to see its destination and agent.'
    };
  }

  function renderDiagram(container, input, { artwork = globalThis.OriWorkspaceBuildingArt } = {}) {
    if (!container) return;
    const view = visualView(input);
    const doc = container.ownerDocument;
    const node = (tag, label = '', className = '') => {
      const el = doc.createElement(tag);
      el.textContent = label;
      el.className = className;
      return el;
    };
    const figure = node('figure', '', 'folder-visual');
    figure.setAttribute('aria-label', 'Folder setup relationship');
    const relationship = node('div', '', 'folder-visual__relationship');
    const source = node('section', '', 'folder-visual__source');
    const folder = node('span', '', 'folder-visual__folder-art');
    folder.setAttribute('aria-hidden', 'true');
    source.append(
      folder,
      node('p', 'Your folder', 'folder-visual__label'),
      node('h4', view.source),
      node('p', view.sourceNote, 'folder-visual__note')
    );
    if (view.entries.length) {
      const entries = node('ul', '', 'folder-visual__entries');
      for (const entry of view.entries) {
        const row = node('li');
        row.append(node('span', entry.name), node('small', entry.kind));
        entries.append(row);
      }
      source.append(entries);
    }
    const connection = node('span', '→', 'folder-visual__connection');
    connection.setAttribute('aria-hidden', 'true');
    const destination = node('section', '', 'folder-visual__destination');
    destination.append(node('p', view.parent, 'folder-visual__label'));
    const workspace = node('div', '', 'folder-visual__workspace');
    if (view.entity && artwork?.svgForVariant) {
      const art = node('span', '', 'folder-visual__building');
      // Only the host's curated constant silhouette, never server/user HTML.
      art.innerHTML = artwork.svgForVariant('depot', { context: 'chat' });
      art.setAttribute('aria-hidden', 'true');
      workspace.append(art);
    }
    workspace.append(
      node('p', view.entity?.status || 'Not reviewed', 'folder-visual__badge'),
      node('h4', view.entity?.name || 'Setup preview'),
      node('p', kinds[view.entity?.kind] || 'No destination proposed', 'folder-visual__note')
    );
    if (view.agent) {
      const agent = node('div', '', 'folder-visual__agent');
      const initials = view.agent.name
        .split(/\s+/)
        .filter(Boolean)
        .slice(0, 2)
        .map(word => Array.from(word)[0])
        .join('')
        .toUpperCase();
      const avatar = node('span', initials || '?', 'folder-visual__initials');
      avatar.setAttribute('aria-hidden', 'true');
      const identity = node('div');
      identity.append(
        node('p', view.agent.status, 'folder-visual__label'),
        node('strong', view.agent.name || view.agent.role || 'Agent not yet known'),
        node(
          'p',
          [view.agent.role, view.agent.model ? `Model: ${view.agent.model}` : 'Model not yet known']
            .filter(Boolean)
            .join(' · '),
          'folder-visual__note'
        )
      );
      agent.append(avatar, identity);
      workspace.append(agent);
    }
    destination.append(workspace, node('p', view.footer, 'folder-visual__note'));
    relationship.append(source, connection, destination);
    figure.append(relationship, node('figcaption', view.summary, 'visually-hidden'));
    container.replaceChildren(figure);
  }

  // Configuration is not connectivity. Routes are compiled from the host's
  // applicable configuration owner; no URL or credential is accepted here.
  function modelRequirementView(model, { saved = false } = {}) {
    if (!model) return null;
    const configured = model.state === 'configured' && text(model.provider) && text(model.name);
    const missing = model.state === 'missing';
    const settings =
      model.settings === 'system'
        ? '/settings#system-model'
        : model.settings === 'agent' && text(model.agent_name)
          ? `/agents/${encodeURIComponent(text(model.agent_name))}`
          : '';
    return {
      state: configured ? 'configured' : missing ? 'missing' : 'not_checked',
      status: configured ? 'Configured' : missing ? 'Configuration needed' : 'Not checked',
      detail: configured
        ? `${text(model.provider)} · ${text(model.name)} — configuration only; connection not verified.`
        : missing
          ? 'This project agent needs a model configuration before setup.'
          : 'The project blueprint and agent must be available before their model requirement can be checked.',
      saved,
      settings: saved ? '' : settings
    };
  }

  // Requirements are display facts, not another plan or action dispatcher.
  function requirementsView({ offer, plan = offer?.plan, readiness } = {}) {
    const saved = Boolean(offer?.setup);
    const visual = saved ? offer.setup.visual : plan?.visual || offer?.readiness || readiness;
    const statuses = {
      install: 'Not installed · Install',
      update: 'Update needed',
      enable: 'Disabled · Enable · No download',
      ready: 'Ready · No download',
      not_checked: 'Not checked',
      unavailable: 'Check unavailable',
      unverified: 'Not verified'
    };
    const progressStatuses = {
      waiting: 'Waiting',
      working: 'In progress',
      done: 'Complete',
      failed: 'Stopped here'
    };
    const rows = [];
    for (const plugin of list(visual?.plugins)) {
      if (
        !['integration', 'provider'].includes(plugin.kind) ||
        rows.some(row => row.kind === plugin.kind)
      )
        continue;
      const operation = statuses[plugin.operation] ? plugin.operation : 'not_checked';
      const installed = text(plugin.installed_version);
      const version = text(plugin.version);
      const progress = saved
        ? list(offer.setup.lines).find(line => line.kind === plugin.kind)?.state
        : '';
      rows.push({
        kind: plugin.kind,
        name:
          text(plugin.name) ||
          (plugin.kind === 'provider' ? 'Home provider' : 'Project integration'),
        operation,
        progress: ['waiting', 'working', 'done', 'failed'].includes(progress) ? progress : '',
        status: saved ? progressStatuses[progress] || 'Progress not reported' : statuses[operation],
        purpose: text(plugin.purpose),
        source: text(plugin.source),
        version:
          installed && version && installed !== version
            ? `${saved ? 'Reviewed: ' : ''}${installed} → ${version}`
            : installed
              ? `${saved ? 'Installed at review' : 'Installed version'} ${installed}`
              : version
                ? `${saved ? 'Reviewed target' : 'Target version'} ${version}`
                : 'Version not reported'
      });
    }
    // Older saved plans may have lines but no structured display identity.
    for (const line of list(offer?.setup?.lines || plan?.lines)) {
      if (
        ['integration', 'provider'].includes(line.kind) &&
        !rows.some(row => row.kind === line.kind)
      )
        rows.push({
          kind: line.kind,
          name: line.kind === 'provider' ? 'Home provider' : 'Project integration',
          operation: 'not_checked',
          status: saved ? progressStatuses[line.state] || 'Progress not reported' : 'Not checked',
          progress: saved && progressStatuses[line.state] ? line.state : '',
          purpose: '',
          source: '',
          version: 'Version not reported'
        });
    }
    const complete =
      visual?.requirements_checked === true &&
      list(visual.plugins).length === rows.length &&
      rows.every(row => ['install', 'update', 'enable', 'ready'].includes(row.operation));
    const downloads = rows.filter(row => ['install', 'update'].includes(row.operation)).length;
    const count = `${downloads} ${downloads === 1 ? 'plugin download' : 'plugin downloads'}`;
    return {
      rows,
      model: modelRequirementView(visual?.model, { saved }),
      complete,
      downloads,
      saved,
      summary: saved
        ? 'Requirements recorded for this setup'
        : complete
          ? downloads
            ? `${count} for this setup`
            : 'Nothing to download'
          : downloads
            ? `${count} known · other requirements not fully checked`
            : 'Download requirements not fully checked'
    };
  }

  function stoppedRequirement(setup) {
    const failed = list(setup?.lines).find(
      line => ['integration', 'provider'].includes(line.kind) && line.state === 'failed'
    );
    if (!failed) return '';
    return text(list(setup.visual?.plugins).find(row => row.kind === failed.kind)?.name);
  }

  function requirementsActionLabel(plan, fallback = 'Set up') {
    const view = requirementsView({ plan });
    if (!view.complete) return fallback;
    const installs = view.rows.filter(row => row.operation === 'install').length;
    const updates = view.rows.filter(row => row.operation === 'update').length;
    const enables = view.rows.filter(row => row.operation === 'enable').length;
    const count = installs + updates || enables;
    if (!count) return fallback;
    const verb =
      installs && updates ? 'Install/update' : installs ? 'Install' : updates ? 'Update' : 'Enable';
    return `${verb} ${count} ${count === 1 ? 'plugin' : 'plugins'} & set up`;
  }

  function renderRequirements(container, input) {
    if (!container) return;
    const view = requirementsView(input);
    container.hidden = !view.rows.length && !view.model;
    const signature = JSON.stringify(view);
    if (container.dataset.signature === signature) return;
    container.dataset.signature = signature;
    container.replaceChildren();
    if (!view.rows.length && !view.model) return;
    const doc = container.ownerDocument;
    const node = (tag, label, className = '') => {
      const el = doc.createElement(tag);
      el.textContent = label;
      el.className = className;
      return el;
    };
    const section = node('section', '', 'folder-requirements');
    section.setAttribute('aria-label', 'Setup requirements');
    section.append(node('h4', 'What you need'));
    if (view.rows.length) section.append(node('p', view.summary, 'folder-requirements__summary'));
    const requirements = node('ul', '', 'folder-requirements__list');
    for (const requirement of view.rows) {
      const row = node('li', '', 'folder-requirements__row');
      row.dataset.operation = requirement.operation;
      row.dataset.requirementKind = requirement.kind;
      row.dataset.progress = requirement.progress || '';
      row.append(
        node('strong', requirement.name),
        node('p', requirement.status, 'folder-requirements__status')
      );
      if (requirement.purpose)
        row.append(
          node('p', requirement.purpose, 'folder-visual__note folder-requirements__purpose')
        );
      row.append(
        node('p', requirement.version, 'folder-visual__note folder-requirements__version')
      );
      const details = node('details', '', 'folder-requirements__source');
      details.append(
        node('summary', 'Source details'),
        node('p', requirement.source || 'Source not reported', 'folder-visual__note')
      );
      row.append(details, node('div', '', 'folder-requirements__actions'));
      requirements.append(row);
    }
    section.append(
      requirements,
      node(
        'p',
        'Ori plugins are separate from the desktop application, model runtime and model weights.',
        'folder-visual__note'
      )
    );
    if (view.model) {
      const model = node('section', '', 'folder-requirements__model');
      model.setAttribute('aria-label', 'Project agent model');
      model.append(
        node('strong', view.model.saved ? 'Model configuration at review' : 'Project agent model'),
        node('p', view.model.status, 'folder-requirements__status'),
        node('p', view.model.detail, 'folder-visual__note')
      );
      section.append(model);
    }
    container.append(section);
  }

  // Material effects stay outside the disclosure. The full original plan is
  // still rendered by folder-plan-lines; this is not another consent document.
  function reviewConsequences(plan) {
    if (!plan) return [];
    const effects = [];
    const plugins = list(plan.visual?.plugins);
    for (const line of list(plan.lines)) {
      if (['integration', 'provider'].includes(line.kind)) {
        const plugin = plugins.find(row => row.kind === line.kind);
        if (plugin?.operation === 'ready') continue;
        const verbs = { install: 'Install', update: 'Update', enable: 'Enable' };
        effects.push(
          plugin && verbs[plugin.operation]
            ? `${verbs[plugin.operation]} ${text(plugin.name)}${plugin.installed_version ? ` from ${plugin.installed_version}` : ''}${plugin.version ? ` to ${plugin.version}` : ' (version not yet known)'}.`
            : [text(line.name), text(line.detail)].filter(Boolean).join('. ')
        );
      } else if (
        [
          'workspace',
          'home',
          'group',
          'workspaces',
          'folder',
          'mode',
          'task',
          'profile',
          'library',
          'songs',
          'assistant'
        ].includes(line.kind)
      ) {
        effects.push([text(line.name), text(line.detail)].filter(Boolean).join('. '));
      } else if (line.kind === 'agents' && line.detail) {
        effects.push(text(line.detail));
      }
    }
    if (plan.hire && list(plan.lines).some(line => line.kind === 'task'))
      effects.push(
        'The first look shares permitted folder content with the configured model and uses model tokens. It does not change your source files.'
      );
    return effects.filter(Boolean);
  }

  function renderConsequences(container, plan) {
    if (!container) return;
    const effects = reviewConsequences(plan);
    container.hidden = !effects.length;
    const signature = JSON.stringify(effects);
    if (container.dataset.signature === signature) return;
    container.dataset.signature = signature;
    container.replaceChildren();
    if (!effects.length) return;
    const doc = container.ownerDocument;
    const heading = doc.createElement('h4');
    heading.textContent = 'Before you start';
    container.append(heading);
    const list = doc.createElement('ul');
    for (const effect of effects) {
      const row = doc.createElement('li');
      row.textContent = effect;
      list.append(row);
    }
    container.append(list);
  }

  // Reduce only applicable line kinds, preserving the order actually reported
  // by the runner. A task line is not evidence that a first look ran/finished.
  function setupMilestones(setup) {
    const labels = {
      integration: 'Integration',
      provider: 'Home plugin',
      home: 'Home',
      workspace: 'Workspace',
      group: 'Workspace group',
      workspaces: 'Member workspaces',
      member: 'Member workspace',
      folder: 'Folder access',
      mode: 'Access mode',
      agents: 'Workspace staffing',
      assistant: 'Shared-agent setup',
      profile: 'Home profile',
      library: 'Library',
      songs: 'Listed projects'
    };
    return list(setup?.lines)
      .filter(line => labels[line.kind])
      .map(line => ({
        kind: line.kind,
        label: line.kind === 'member' ? text(line.name) || labels.member : labels[line.kind],
        state: ['waiting', 'working', 'done', 'failed'].includes(line.state) ? line.state : '',
        detail: text(line.name)
      }));
  }

  function renderProgress(container, setup) {
    if (!container) return;
    container.hidden = !setup;
    const signature = JSON.stringify(setupMilestones(setup));
    if (container.dataset.signature === signature) return;
    container.dataset.signature = signature;
    container.replaceChildren();
    if (!setup) return;
    const doc = container.ownerDocument;
    const list = doc.createElement('ul');
    list.className = 'folder-setup-milestones';
    list.setAttribute('aria-label', 'Saved setup progress');
    const states = {
      waiting: 'Waiting',
      working: 'In progress',
      done: 'Complete',
      failed: 'Did not finish'
    };
    for (const stage of setupMilestones(setup)) {
      const row = doc.createElement('li');
      row.dataset.state = stage.state;
      row.textContent = `${stage.label} · ${states[stage.state] || 'Not reported'}`;
      list.append(row);
    }
    const note = doc.createElement('p');
    note.className = 'folder-visual__note';
    note.textContent =
      'Closing this panel does not cancel setup. Reopen it to see the saved result.';
    container.append(list, note);
  }

  function setupAcceptedLine(subject) {
    return `Setup accepted for ${text(subject) || 'this folder'}. The saved result below shows what actually finished.`;
  }

  function briefError(value) {
    return Array.from(String(value ?? '')).length > 300
      ? 'Keep the brief to 300 characters or fewer. Your draft is kept.'
      : '';
  }

  globalThis.OriFolderSetupVisual = Object.freeze({
    visualView,
    renderDiagram,
    requirementsView,
    modelRequirementView,
    stoppedRequirement,
    requirementsActionLabel,
    renderRequirements,
    reviewConsequences,
    renderConsequences,
    briefError,
    setupMilestones,
    renderProgress,
    setupAcceptedLine
  });
})();
