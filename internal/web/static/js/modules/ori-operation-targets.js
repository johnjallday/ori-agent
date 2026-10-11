/**
 * ori-operation-targets.js
 *
 * The closed mapping from Ori's named operation commands to the page
 * controllers that already own each view. A command names an action and
 * carries host-resolved references; it never carries a URL, a selector, a
 * script or a coordinate, and nothing here accepts one.
 *
 * Each adapter answers what it OBSERVED, never what it hoped for:
 *   acknowledged - the owning controller reports the state and its control is
 *                  mounted (with the evidence the server checks);
 *   unavailable  - the page cannot take the command; the manual route remains;
 *   navigating   - a full-page navigation to a URL this module built is needed;
 *   yield        - the user is somewhere else or has unsaved work, so nothing
 *                  is moved until they ask to resume.
 *
 * The environment is injected so the mapping is testable without a DOM.
 */

import { workspaceRootURL } from './workspace-routes.js';
import { serializeWorkspaceURLState } from './workspace-url-state.js';

export const OPERATION_ACTION = Object.freeze({
  OPEN_WORKSPACE: 'open_workspace',
  SELECT_AGENT: 'select_agent',
  OPEN_TASK_DRAFT: 'open_task_draft'
});

export const OUTCOME = Object.freeze({
  ACKNOWLEDGED: 'acknowledged',
  UNAVAILABLE: 'unavailable',
  NAVIGATING: 'navigating',
  YIELD: 'yield'
});

// How long an adapter waits for a controller or its control to mount after a
// navigation. Bounded: a page that never mounts is reported, not waited on.
const MOUNT_ATTEMPTS = 40;
const MOUNT_INTERVAL_MS = 150;

const unavailable = reason => ({ outcome: OUTCOME.UNAVAILABLE, reason });

/** Whether an action is one this browser can carry out. Anything else is refused. */
export function isSupportedAction(action) {
  return Object.values(OPERATION_ACTION).includes(String(action || ''));
}

/**
 * The only URL an operation may navigate to: this app's page for a workspace,
 * built from a slug by the existing route helper. Returns '' for anything that
 * is not a plain folder slug.
 */
export function operationWorkspaceURL(slug) {
  const value = String(slug || '').trim();
  if (!value || value.length > 200 || /[\\/?#\s]|\.\./.test(value)) return '';
  try {
    return workspaceRootURL(value);
  } catch (_) {
    return '';
  }
}

/**
 * This app's page for one task: the workspace's page with its own Tasks panel
 * open on that task, built by the page's existing URL helpers from a slug and
 * a task ID. Returns '' for anything that is not both.
 */
export function operationTaskURL(slug, taskId) {
  const value = String(slug || '').trim();
  const id = String(taskId || '').trim();
  if (!operationWorkspaceURL(value) || !/^[A-Za-z0-9_-]{1,64}$/.test(id)) return '';
  try {
    return workspaceRootURL(value, {
      search: serializeWorkspaceURLState({ panel: 'tasks', task: id })
    });
  } catch (_) {
    return '';
  }
}

/** A path with any trailing slash removed and its escapes decoded, for comparison. */
export function comparablePath(pathname) {
  const trimmed = String(pathname || '').replace(/\/+$/, '');
  try {
    return decodeURIComponent(trimmed);
  } catch (_) {
    return trimmed;
  }
}

async function waitFor(env, probe) {
  for (let attempt = 0; attempt < MOUNT_ATTEMPTS; attempt += 1) {
    const value = probe();
    if (value) return value;
    await env.wait(MOUNT_INTERVAL_MS);
  }
  return null;
}

/**
 * @param {object} env
 * @param {() => string} env.pathname - the current page's path
 * @param {() => {workspaceId: string, command: object|null, composer: object|null}} env.workspacePage
 *        - the workspace page's identity, its command-view controller and its
 *          task form controller, when mounted
 * @param {(key: string) => Element|null} env.agentControl - the mounted control for an agent key
 * @param {() => {open: boolean, title: string, brief: string, assignment: string}} env.taskForm
 *        - what the task form holds right now
 * @param {() => boolean} env.hasUnsavedWork - whether an editor on this page holds unsaved input
 * @param {(ms: number) => Promise<void>} env.wait
 */
export function createOperationTargets(env) {
  async function openWorkspace(command, { allowNavigate, replace }) {
    const url = operationWorkspaceURL(command.workspace_slug);
    if (!url) return unavailable('This workspace has no page address.');
    if (comparablePath(env.pathname()) === comparablePath(url)) {
      return { outcome: OUTCOME.ACKNOWLEDGED, observed: { path: env.pathname() } };
    }
    if (!allowNavigate) return { outcome: OUTCOME.YIELD, reason: 'moved_elsewhere' };
    // Leaving would discard what the user typed, unless they chose to.
    if (!replace && env.hasUnsavedWork()) return { outcome: OUTCOME.YIELD, reason: 'unsaved_work' };
    return { outcome: OUTCOME.NAVIGATING, url };
  }

  async function selectAgent(command) {
    const wanted = String(command.agent_name || '').trim();
    if (!wanted) return unavailable('No agent was named.');
    // The controller mounts after the page's own data loads; wait for it, but
    // only on the workspace this command names.
    const page = await waitFor(env, () => {
      const current = env.workspacePage();
      return current && current.command ? current : null;
    });
    if (!page) return unavailable('This page has no agent roster.');
    if (String(page.workspaceId || '') !== String(command.workspace_id || '')) {
      return { outcome: OUTCOME.YIELD, reason: 'moved_elsewhere' };
    }
    const controller = page.command;
    const key = controller.normalizeAgentKey(wanted);
    const agent = await waitFor(env, () => {
      const groups = controller.agentGroups();
      return (
        ((groups && groups.agents) || []).find(
          item => controller.normalizeAgentKey(item.key || item.name) === key
        ) || null
      );
    });
    if (!agent) return unavailable(`${wanted} is not in this workspace's roster.`);
    // The page's own selection owner does the selecting, URL state included.
    controller.selectAgent(encodeURIComponent(agent.name), { focus: false });
    const element = await waitFor(env, () =>
      controller.selectedAgentKey === key ? env.agentControl(key) : null
    );
    if (!element) {
      return unavailable(`${agent.name}'s control is not shown in this view.`);
    }
    // The page re-renders its roster, replacing this node with an identical
    // one. `locate` finds the live control again from the same key.
    return {
      outcome: OUTCOME.ACKNOWLEDGED,
      observed: { agent_key: agent.name },
      element,
      locate: () => env.agentControl(key)
    };
  }

  /**
   * Show a prepared draft in the page's own task form. The form's controller
   * does the opening; this only hands it the draft and then checks that the
   * form really holds it. Nothing is typed, auto-parsed or saved.
   */
  async function openTaskDraft(command, { snapshot, replace, onSaved }) {
    const draft = snapshot && snapshot.draft;
    if (!draft || Number(draft.revision) !== Number(command.draft_revision)) {
      return unavailable('This draft is no longer the current one.');
    }
    const node = String(draft.agent_node_id || '').trim();
    const agent = String(draft.agent_name || '').trim();
    if (!node || !agent) return unavailable('This draft has no agent to assign.');
    const page = await waitFor(env, () => {
      const current = env.workspacePage();
      return current && current.composer ? current : null;
    });
    if (!page) return unavailable('This page has no task form.');
    if (String(page.workspaceId || '') !== String(command.workspace_id || '')) {
      return { outcome: OUTCOME.YIELD, reason: 'moved_elsewhere' };
    }
    const composer = page.composer;
    // The user's own edits win over a proposal that arrives late.
    if (!replace && composer.isModalOpen() && composer.hasUnsavedChanges()) {
      return { outcome: OUTCOME.YIELD, reason: 'unsaved_work' };
    }
    const assignment = `node:${node}`;
    await composer.openForCreate(page.workspaceId, draft.title, onSaved || null, {
      forceManualMode: true,
      prefillTitle: draft.title,
      prefillDetails: draft.brief || '',
      draftAssignmentValue: assignment,
      draftAssignmentLabel: agent,
      modalTitle: `Review task for ${agent}`,
      // The form's own save names this operation, so the server binds the one
      // task it creates. Nothing here saves or starts anything.
      assistantOperation: { id: String(snapshot.id || ''), revision: Number(draft.revision) }
    });
    const form = env.taskForm();
    if (!form || !form.open) return unavailable('The task form did not open.');
    if (form.assignment !== assignment) {
      // Leaving the form open would let a save hand the task to someone the
      // user was not shown. Close it and say so instead.
      composer.close({ force: true });
      return unavailable(`${agent} cannot be assigned in the task form.`);
    }
    if (form.title !== String(draft.title || '') || form.brief !== String(draft.brief || '')) {
      composer.close({ force: true });
      return unavailable('The task form did not take the draft.');
    }
    return { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: Number(draft.revision) } };
  }

  return {
    /**
     * Carry out one command. `allowNavigate` is true only for a request the
     * user just made or explicitly resumed; a page load never navigates again
     * by itself. `replace` is the user choosing Ori's step over unsaved work.
     */
    async execute(command, options = {}) {
      const action = String((command && command.action) || '');
      try {
        if (action === OPERATION_ACTION.OPEN_WORKSPACE) {
          return await openWorkspace(command, {
            allowNavigate: Boolean(options.allowNavigate),
            replace: Boolean(options.replace)
          });
        }
        if (action === OPERATION_ACTION.SELECT_AGENT) return await selectAgent(command);
        if (action === OPERATION_ACTION.OPEN_TASK_DRAFT) {
          return await openTaskDraft(command, {
            snapshot: options.snapshot || null,
            replace: Boolean(options.replace),
            onSaved: options.onSaved
          });
        }
      } catch (_) {
        return unavailable('This page could not carry out the step.');
      }
      return unavailable('This step is not supported here.');
    }
  };
}

/** The live environment: the real location, page controllers and DOM. */
export function browserOperationEnvironment() {
  return {
    pathname: () => window.location.pathname,
    workspacePage: () => ({
      workspaceId: String(window.currentWorkspaceId || ''),
      command: window.workspaceCommand || null,
      // The task form exists on workspace pages only.
      composer:
        window.currentWorkspaceId && document.getElementById('taskModal')
          ? window.taskModalController || null
          : null
    }),
    taskForm: () => {
      const value = id => String(document.getElementById(id)?.value ?? '');
      const composer = window.taskModalController;
      return {
        open: Boolean(composer && composer.isModalOpen()),
        title: value('taskModalDescription'),
        brief: value('taskModalDetails'),
        assignment: value('taskModalAssignment')
      };
    },
    agentControl: key => {
      const escaped =
        window.CSS && typeof window.CSS.escape === 'function'
          ? window.CSS.escape(key)
          : String(key).replace(/["\\]/g, '\\$&');
      return document.querySelector(
        `[data-cmd-select-agent][data-agent-key="${escaped}"], [data-cmd-map-select-agent][data-agent-key="${escaped}"]`
      );
    },
    hasUnsavedWork: () => {
      const composer = window.taskModalController;
      try {
        return Boolean(
          composer &&
          typeof composer.isModalOpen === 'function' &&
          composer.isModalOpen() &&
          typeof composer.hasUnsavedChanges === 'function' &&
          composer.hasUnsavedChanges()
        );
      } catch (_) {
        return false;
      }
    },
    wait: ms => new Promise(resolve => window.setTimeout(resolve, ms))
  };
}
