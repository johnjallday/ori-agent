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

export const OPERATION_ACTION = Object.freeze({
  OPEN_WORKSPACE: 'open_workspace',
  SELECT_AGENT: 'select_agent'
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
 * @param {() => {workspaceId: string, command: object|null}} env.workspacePage
 *        - the workspace page's identity and its command-view controller, when mounted
 * @param {(key: string) => Element|null} env.agentControl - the mounted control for an agent key
 * @param {() => boolean} env.hasUnsavedWork - whether an editor on this page holds unsaved input
 * @param {(ms: number) => Promise<void>} env.wait
 */
export function createOperationTargets(env) {
  async function openWorkspace(command, { allowNavigate }) {
    const url = operationWorkspaceURL(command.workspace_slug);
    if (!url) return unavailable('This workspace has no page address.');
    if (comparablePath(env.pathname()) === comparablePath(url)) {
      return { outcome: OUTCOME.ACKNOWLEDGED, observed: { path: env.pathname() } };
    }
    if (!allowNavigate) return { outcome: OUTCOME.YIELD, reason: 'moved_elsewhere' };
    if (env.hasUnsavedWork()) return { outcome: OUTCOME.YIELD, reason: 'unsaved_work' };
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

  return {
    /**
     * Carry out one command. `allowNavigate` is true only for a request the
     * user just made or explicitly resumed; a page load never navigates again
     * by itself.
     */
    async execute(command, options = {}) {
      const action = String((command && command.action) || '');
      try {
        if (action === OPERATION_ACTION.OPEN_WORKSPACE) {
          return await openWorkspace(command, { allowNavigate: Boolean(options.allowNavigate) });
        }
        if (action === OPERATION_ACTION.SELECT_AGENT) return await selectAgent(command);
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
      command: window.workspaceCommand || null
    }),
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
