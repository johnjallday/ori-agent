// "Give a task…" on an Agents roster card (agents-hire-with-a-brief FR18):
// where giving this agent a task takes the user. An agent in one workspace
// goes there with the task form open and the agent chosen; an agent in several
// first picks the workspace; an agent in none is offered the Hire card for its
// role, since work happens in a workspace.
//
// A classic script with no imports or exports; agents-roster.js reads
// window.OriAgentGiveTask. The workspace page reads the same query parameter
// (workspace-detail.js checkAutoOpenNewTaskFor).
(function () {
  'use strict';

  // The query parameter a workspace page opens its task form for.
  const NEW_TASK_FOR_PARAM = 'newTaskFor';
  const SLUG = /^[a-z0-9][a-z0-9-]*$/;
  const NO_WORKSPACE_NOTE = 'This agent has no workspace yet';

  // newTaskHref is a workspace's page with its task form opening for agent.
  function newTaskHref(slug, agentName) {
    const workspace = String(slug || '').trim();
    const agent = String(agentName || '').trim();
    if (!SLUG.test(workspace) || !agent) return '';
    return `/workspaces/${encodeURIComponent(workspace)}?${NEW_TASK_FOR_PARAM}=${encodeURIComponent(agent)}`;
  }

  // giveTaskTarget decides what Give a task… does for an agent and its
  // workspace refs ({name, slug}).
  function giveTaskTarget(agentName, workspaces) {
    const places = (Array.isArray(workspaces) ? workspaces : [])
      .map(ws => ({
        name: String(ws?.name || '').trim(),
        slug: String(ws?.slug || '').trim(),
        href: newTaskHref(ws?.slug, agentName)
      }))
      .filter(ws => ws.name && ws.href);
    if (places.length === 1) return { kind: 'workspace', href: places[0].href };
    if (places.length > 1) return { kind: 'choose', workspaces: places };
    return { kind: 'hire', note: NO_WORKSPACE_NOTE };
  }

  globalThis.OriAgentGiveTask = {
    NEW_TASK_FOR_PARAM,
    NO_WORKSPACE_NOTE,
    newTaskHref,
    giveTaskTarget
  };
})();
