// Shared, inert presentation for folder setup. Classic-script/module seam like
// folder-plan-lines.js: no requests, storage, timers or setup authority here.
(function () {
  'use strict';

  const text = value => (typeof value === 'string' ? value : '');
  const list = value => (Array.isArray(value) ? value : []);
  const operations = { create: 'Proposed · new', reuse: 'Proposed · reuse' };
  const kinds = { workspace: 'Workspace', group: 'Workspace group', home: 'Home' };

  function visualView({ observation, offer, plan = offer?.plan } = {}) {
    const visual = offer?.setup?.visual || plan?.visual;
    const proposed = visual?.entity;
    const entity =
      proposed && kinds[proposed.kind] && operations[proposed.operation]
        ? { kind: proposed.kind, name: text(proposed.name), status: operations[proposed.operation] }
        : null;
    const destination = offer?.setup?.destination || plan?.destination || offer?.destination;
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
      sourceNote: observed
        ? 'Observed names only · contents not read'
        : 'Source folder · kept in place',
      summary: entity
        ? `${source} → ${entity.status.toLowerCase()} ${kinds[entity.kind].toLowerCase()} ${entity.name}. ${parent}.${agent ? ` ${agent.status}: ${agent.name || agent.role || 'agent not yet known'}.` : ''} Nothing here proves creation or assignment.`
        : `${source}. Destination and agent are not yet reviewed. No setup has started.`,
      footer: entity
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

  function briefError(value) {
    return Array.from(String(value ?? '')).length > 300
      ? 'Keep the brief to 300 characters or fewer. Your draft is kept.'
      : '';
  }

  globalThis.OriFolderSetupVisual = Object.freeze({
    visualView,
    renderDiagram,
    reviewConsequences,
    renderConsequences,
    briefError
  });
})();
