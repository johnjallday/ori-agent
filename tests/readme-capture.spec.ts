import { expect, test, type Page, type Route } from '@playwright/test';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

import {
  assertReadmeSceneContract,
  README_CAPTURE_CLOCK,
  README_SCENES
} from './fixtures/readme-scenes';

const runDirectory = process.env.README_CAPTURE_RUN_DIR;
const rawDirectory = process.env.README_CAPTURE_RAW_DIR;
const sidecarDirectory = process.env.README_CAPTURE_SIDECAR_DIR;
const captureEnvironmentReady = Boolean(runDirectory && rawDirectory && sidecarDirectory);
const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const captureAssets = {
  bootstrapCss: path.join(repositoryRoot, 'node_modules/bootstrap/dist/css/bootstrap.min.css'),
  bootstrapJs: path.join(repositoryRoot, 'node_modules/bootstrap/dist/js/bootstrap.bundle.min.js'),
  bootstrapIconsCss: path.join(
    repositoryRoot,
    'node_modules/bootstrap-icons/font/bootstrap-icons.min.css'
  ),
  bootstrapIconsFont: path.join(
    repositoryRoot,
    'node_modules/bootstrap-icons/font/fonts/bootstrap-icons.woff2'
  )
};

const sceneStatuses: Array<{ id: string; status: 'passed' | 'failed'; detail?: string }> = [];
const privatePatterns = [
  /\/Users\//i,
  /\/home\//i,
  /\bsk-[a-z0-9]/i,
  /AKIA[0-9A-Z]{16}/,
  /BEGIN PRIVATE KEY/
];

test.skip(
  !captureEnvironmentReady,
  'Use scripts/readme-refresh.sh capture to supply an isolated staged run.'
);

function json(route: Route, payload: unknown, status = 200) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(payload) });
}

function workspace(id: string) {
  const summary =
    README_SCENES.workspaces.find(item => item.id === id) || README_SCENES.workspace_command;
  return {
    ...summary,
    id,
    description: 'Coordinate the launch review, owners, and next decisions.',
    entry_agent_name: 'Mira',
    agents: README_SCENES.workspace_command.agents.map(agent => agent.name),
    agent_instances: README_SCENES.workspace_command.agents.map((agent, index) => ({
      ...agent,
      id: `agent-${index + 1}`,
      entry_point: index === 0,
      source: 'workspace'
    })),
    attachments: [
      {
        id: README_SCENES.workspace_command.files[0].id,
        title: README_SCENES.workspace_command.files[0].name,
        type: 'other',
        file_meta: {
          name: README_SCENES.workspace_command.files[0].name,
          size: 4096,
          status: 'synced'
        }
      }
    ],
    directory_references: [],
    mcp_bindings: [],
    agent_mcp_access: [],
    shared_data: {},
    workspace_settings: { execution_mode: 'guided' }
  };
}

function taskPayload() {
  return {
    tasks: README_SCENES.workspace_command.tasks.map((item, index) => ({
      ...item,
      description: item.title,
      workspace_id: README_SCENES.workspace_command.workspace_id,
      assigned_to: index === 0 ? 'Mira' : 'Theo',
      created_at: '2026-07-17T12:00:00.000Z',
      updated_at: '2026-07-17T14:00:00.000Z',
      tags: ['launch']
    }))
  };
}

/**
 * The canonical Ticket projection of the same scene work
 * (tasks/prd-workspace-ticket-management.md).
 *
 * Derived from the scene's tasks rather than invented, so the TICKETS count on
 * the workspace header can never disagree with the tasks the screenshot shows.
 * A ticket IS a task evolved in place — one record, one number — so the
 * fixture models it that way too.
 */
function ticketPayload() {
  const owner = README_SCENES.workspaces.find(
    item => item.id === README_SCENES.workspace_command.workspace_id
  );
  const tickets = README_SCENES.workspace_command.tasks.map((item, index) => ({
    id: item.id,
    number: index + 1,
    title: item.title,
    state: item.status === 'in_progress' ? 'in_progress' : 'ready',
    priority: 3,
    source: 'manual',
    workspace_id: README_SCENES.workspace_command.workspace_id,
    owning_workspace_id: README_SCENES.workspace_command.workspace_id,
    owning_workspace_name: owner ? owner.name : 'Workspace',
    version: 1,
    created_at: '2026-07-17T12:00:00.000Z',
    updated_at: '2026-07-17T14:00:00.000Z',
    linked_note_ids: [],
    state_history: []
  }));
  return {
    tickets,
    count: tickets.length,
    total: tickets.length,
    truncated: false,
    partial_owners: [],
    include_descendants: false
  };
}

async function installFixtureRoutes(page: Page) {
  assertReadmeSceneContract();
  for (const [name, assetPath] of Object.entries(captureAssets)) {
    expect(existsSync(assetPath), `Missing required offline capture asset: ${name}`).toBe(true);
  }
  const unexpectedRequests: string[] = [];
  const consoleErrors: string[] = [];
  // Chrome's own automatic "Failed to load resource: the server responded
  // with a status of NNN" line fires for *any* non-2xx fetch response,
  // independent of whether the page's own JS handled it gracefully -- it is
  // categorically different from a real application bug (a thrown exception
  // or an explicit console.error(...) call from our own code), and several
  // fixture mocks in this file deliberately return a non-2xx status to match
  // a real handler's documented "not applicable"/"not ready" response
  // (e.g. Calendar Ops's capabilities probe on a non-Calendar-Ops
  // workspace). Filtering only this exact network-layer message keeps the
  // check strict for everything that actually indicates a bug.
  const resourceLoadStatusPattern =
    /^Failed to load resource: the server responded with a status of \d+/;
  page.on('console', message => {
    if (message.type() === 'error' && !resourceLoadStatusPattern.test(message.text())) {
      consoleErrors.push(message.text());
    }
  });
  await page.addInitScript(
    ({ clock }) => {
      const RealDate = Date;
      class FixedDate extends RealDate {
        constructor(...args: ConstructorParameters<typeof Date>) {
          super(args.length === 0 ? clock : args[0]);
        }
        static now() {
          return new RealDate(clock).valueOf();
        }
      }
      // @ts-expect-error deterministic capture clock
      window.Date = FixedDate;
    },
    { clock: README_CAPTURE_CLOCK }
  );
  await page.addInitScript(() => {
    const NativeEventSource = window.EventSource;
    class ReadmeCaptureEventSource extends EventTarget {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSED = 2;
      readyState = ReadmeCaptureEventSource.OPEN;
      url: string;
      withCredentials: boolean;

      constructor(url: string | URL, options?: EventSourceInit) {
        const resolved = new URL(String(url), window.location.origin);
        if (resolved.pathname !== '/api/orchestration/workflow/stream') {
          return new NativeEventSource(url, options) as unknown as ReadmeCaptureEventSource;
        }
        super();
        this.url = resolved.toString();
        this.withCredentials = options?.withCredentials || false;
        queueMicrotask(() => this.dispatchEvent(new Event('open')));
      }

      close() {
        this.readyState = ReadmeCaptureEventSource.CLOSED;
      }
    }
    // @ts-expect-error deterministic API-boundary fixture for an otherwise long-lived stream
    window.EventSource = ReadmeCaptureEventSource;
  });
  await page.context().route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const base = new URL(test.info().project.use.baseURL as string);
    if (url.origin !== base.origin) {
      // The production templates currently reference a few CDN assets. Serve
      // the pinned Bootstrap assets from node_modules so the real template can
      // initialize offline. All other third-party URLs receive a local stub;
      // the capture never contacts the network.
      if (
        url.hostname === 'cdn.jsdelivr.net' &&
        url.pathname.includes('/bootstrap@5.3.0/dist/css/bootstrap.min.css')
      ) {
        await route.fulfill({ path: captureAssets.bootstrapCss, contentType: 'text/css' });
        return;
      }
      if (
        url.hostname === 'cdn.jsdelivr.net' &&
        url.pathname.includes('/bootstrap@5.3.0/dist/js/bootstrap.bundle.min.js')
      ) {
        await route.fulfill({
          path: captureAssets.bootstrapJs,
          contentType: 'application/javascript'
        });
        return;
      }
      if (
        url.hostname === 'cdn.jsdelivr.net' &&
        url.pathname.includes('/bootstrap-icons@1.11.3/font/bootstrap-icons.css')
      ) {
        await route.fulfill({ path: captureAssets.bootstrapIconsCss, contentType: 'text/css' });
        return;
      }
      if (
        url.hostname === 'cdn.jsdelivr.net' &&
        url.pathname.includes('/bootstrap-icons@1.11.3/font/fonts/bootstrap-icons.woff2')
      ) {
        await route.fulfill({ path: captureAssets.bootstrapIconsFont, contentType: 'font/woff2' });
        return;
      }
      await route.fulfill({ status: 204, body: '' });
      return;
    }
    if (!url.pathname.startsWith('/api/')) {
      await route.continue();
      return;
    }
    const pathWithQuery = `${url.pathname}${url.search}`;
    if (url.pathname === '/api/personal-hq/status') {
      await json(route, {
        status: {
          valid: true,
          workspace_id: README_SCENES.personal_hq.id,
          hq_onboarding_state: 'complete'
        }
      });
      return;
    }
    if (url.pathname === '/api/onboarding/status') {
      await json(route, {
        needs_onboarding: false,
        user_name: 'Riley',
        assistant_name: 'Ori',
        timezone: 'UTC'
      });
      return;
    }
    if (url.pathname === '/api/settings/workspace-root') {
      await json(route, {
        workspace_root: '',
        effective_workspace_root: '/Ori Workspaces',
        default_workspace_root: '/Ori Workspaces',
        source: 'default',
        confirmed: true
      });
      return;
    }
    if (url.pathname === '/api/personal-hq/brief/config') {
      await json(route, { config: README_SCENES.daily_brief.config });
      return;
    }
    if (url.pathname === '/api/personal-hq/brief/current') {
      await json(route, README_SCENES.daily_brief.current);
      return;
    }
    if (url.pathname === '/api/personal-hq/brief/status') {
      await json(route, README_SCENES.daily_brief.status);
      return;
    }
    if (url.pathname === '/api/action-center/opportunities') {
      await json(route, README_SCENES.action_center);
      return;
    }
    if (url.pathname === '/api/personal-hq/email-ops') {
      await json(route, {
        status: {
          exists: false,
          open_followup_count: 0
        }
      });
      return;
    }
    // Calendar Ops portal: fired unconditionally by home-calendar-ops-portal.js
    // on every page load. None of these README fixtures have a Calendar Ops
    // workspace, so the real API's own "no workspace" shape is the accurate
    // mock (matches internal/calendarhttp.PortalSummary's has_workspace:false
    // branch) -- this keeps the Home scene's Calendar section hidden, same as
    // a genuinely unconfigured profile.
    if (url.pathname === '/api/calendar-ops/home-portal-summary') {
      await json(route, { has_workspace: false });
      return;
    }
    if (
      [
        '/api/updates/check',
        '/api/location/current',
        '/api/settings/system-paths',
        '/api/settings/speech',
        '/api/evolution/assistant',
        '/api/personal-hq/upgrade/preview',
        '/api/personal-hq/email/status',
        '/api/settings/email-oauth',
        '/api/personal-hq/mail/proposals',
        '/api/personal-hq/followups/home'
      ].includes(url.pathname)
    ) {
      await json(route, {});
      return;
    }
    if (url.pathname === '/api/workspaces/sync-status') {
      await json(route, { in_sync: true });
      return;
    }
    if (url.pathname === '/api/settings/system-model') {
      await json(route, { provider: '', model: '' });
      return;
    }
    if (url.pathname === '/api/workspaces/rescan') {
      await json(route, { started: false });
      return;
    }
    if (url.pathname === '/api/tags') {
      await json(route, { tags: [] });
      return;
    }
    if (url.pathname === '/api/workspaces' && url.searchParams.get('tree') === 'true') {
      await json(route, { folders: README_SCENES.workspaces });
      return;
    }
    if (url.pathname === '/api/workspaces') {
      await json(route, { folders: README_SCENES.workspaces });
      return;
    }
    // The coordinate Workspace Map reads the current user's layout on mount
    // (#292). Fixturing it keeps the capture deterministic: without a saved
    // arrangement the three fictional workspaces would fall back to automatic
    // placement, which is correct but not the picture the README is meant to
    // show. Only GET is answered -- a capture that wrote would not be a capture.
    if (url.pathname === '/api/workspace-map/layout') {
      if (route.request().method() !== 'GET') {
        await json(route, { success: false }, 405);
        return;
      }
      await json(route, {
        success: true,
        layout: {
          schema_version: 1,
          revision: 1,
          snap_to_grid: true,
          positions: {
            'ws-personal-hq': { x: 38, y: 38 },
            'ws-product-launch': { x: 418, y: 38 },
            'ws-research-lab': { x: 228, y: 342 }
          }
        }
      });
      return;
    }
    if (url.pathname === '/api/orchestration/scheduled-tasks/upcoming') {
      await json(route, { tasks: [] });
      return;
    }
    if (url.pathname === '/api/activity/recent') {
      await json(route, { activities: [] });
      return;
    }
    if (url.pathname === '/api/progression') {
      await json(route, {});
      return;
    }
    if (url.pathname === '/api/orchestration/workspace') {
      await json(
        route,
        workspace(url.searchParams.get('id') || README_SCENES.workspace_command.workspace_id)
      );
      return;
    }
    if (url.pathname === '/api/orchestration/tasks') {
      await json(route, taskPayload());
      return;
    }
    if (url.pathname === '/api/orchestration/backlog') {
      await json(route, { items: [], sync: null });
      return;
    }
    if (url.pathname === '/api/sessions') {
      await json(route, { sessions: [] });
      return;
    }
    if (url.pathname === '/api/agents/dashboard/list') {
      await json(route, {
        agents: README_SCENES.workspace_command.agents.map(agent => ({
          ...agent,
          type: 'cli',
          model: 'gpt-5',
          provider: 'openai'
        }))
      });
      return;
    }
    if (url.pathname === `/api/workspaces/${README_SCENES.workspace_command.workspace_id}/agents`) {
      await json(route, {
        agents: README_SCENES.workspace_command.agents.map(agent => ({
          ...agent,
          type: 'cli',
          model: 'gpt-5',
          provider: 'openai',
          source: 'workspace'
        }))
      });
      return;
    }
    if (
      url.pathname === `/api/workspaces/${README_SCENES.workspace_command.workspace_id}/files/tree`
    ) {
      await json(route, { files: [] });
      return;
    }
    if (/^\/api\/workspaces\/[^/]+\/notes$/.test(url.pathname)) {
      const id = url.pathname.split('/')[3];
      await json(route, {
        notes:
          id === README_SCENES.workspace_command.workspace_id
            ? README_SCENES.workspace_command.notes.map(note => ({
                ...note,
                content: 'Launch decision context is ready for review.'
              }))
            : []
      });
      return;
    }
    if (/^\/api\/workspaces\/[^/]+\/followups$/.test(url.pathname)) {
      await json(route, { followups: [] });
      return;
    }
    if (
      url.pathname === `/api/workspaces/${README_SCENES.workspace_command.workspace_id}/mission`
    ) {
      await json(route, {
        mission: README_SCENES.workspace_command.mission.title,
        cadence: null,
        autonomy_policy: 'watch',
        notification_policy: 'never',
        mission_enabled: false,
        mission_cadence_heartbeat: false,
        last_mission_run_at: null,
        next_mission_run_at: null,
        mission_execution_count: 0,
        mission_failure_count: 0,
        open_findings_count: 0,
        unclassified_mcp_ids: [],
        unclassified_skill_ids: [],
        bindings_ready: true
      });
      return;
    }
    if (
      url.pathname ===
      `/api/workspaces/${README_SCENES.workspace_command.workspace_id}/template-setup/start`
    ) {
      await json(route, { started: false });
      return;
    }
    // Blueprint Setup Wizard: setup-wizard.js and workspace-map.js both read a
    // workspace's setup state on load, for every workspace regardless of
    // blueprint. These fictional workspaces come from no blueprint, so the
    // accurate mock is the real handler's own answer for that case --
    // setupwizard.Status with applicable:false and state:not_applicable, which
    // keeps the dialog closed and the banner hidden exactly as in production.
    // The GitHub approval panel asks every workspace page whether it has
    // changes awaiting approval. An empty list is the answer for a workspace
    // with no GitHub binding, and the panel then hides itself entirely --
    // which is what keeps this scene identical to before the panel existed.
    if (/^\/api\/workspaces\/[^/]+\/github\/proposals$/.test(url.pathname)) {
      await json(route, { proposals: [] });
      return;
    }
    // Every workspace page reads its effective planning policy on load. These
    // scenes' workspaces are fictional and have no folder on disk, so the
    // accurate mock is the real handler's answer for zero capabilities:
    // general/guided with planning off, and the folder-backed controls
    // reporting themselves unavailable rather than claiming enforcement
    // nothing performs. Planning being off keeps the scene identical to
    // before the Plans workflow existed.
    if (/^\/api\/workspaces\/[^/]+\/planning-policy$/.test(url.pathname)) {
      await json(route, {
        workspace_id: README_SCENES.workspace_command.workspace_id,
        previewed_preset: '',
        capabilities: { has_folder: false, is_repository: false, current_branch: '' },
        policy: {
          version: 1,
          profile: 'general',
          preset: 'guided',
          planning_enabled: false,
          guidance: {
            style: 'feature',
            clarification_depth: 'standard',
            preferred_artifacts: ['prd', 'task_list', 'note'],
            detail_level: 'standard',
            tone: 'practical'
          },
          enforced: [
            {
              key: 'artifact_write',
              label: 'Write planning documents',
              description:
                'Approved plans render their documents into the workspace, checked to stay inside it.',
              enabled: true,
              available: false,
              reason: 'no_workspace_folder',
              detail: 'This workspace has no folder to write documents into.'
            },
            {
              key: 'destructive_confirmation',
              label: 'Confirm destructive actions',
              description:
                'An action that deletes or overwrites stops for your confirmation first.',
              enabled: true,
              available: true
            },
            {
              key: 'execution_mode',
              label: 'Execution mode',
              description:
                'Approved work waits for you to start each step. Approval is still required either way.',
              enabled: true,
              available: true
            },
            {
              key: 'handoff_confirmation',
              label: 'Confirm specialist handoffs',
              description: 'Handing work to a specialist agent stops for your confirmation first.',
              enabled: true,
              available: true
            },
            {
              key: 'note_creation',
              label: 'Save outputs as notes',
              description: 'Useful results are written to workspace notes.',
              enabled: true,
              available: true
            },
            {
              key: 'plan_approval',
              label: 'Explicit plan approval',
              description:
                'No tasks, files, or runs are created until you approve one exact plan version.',
              enabled: true,
              available: true
            },
            {
              key: 'repo_scan',
              label: 'Repository inspection before code work',
              description:
                'Code-oriented execution does not begin until the repository inspection step has completed.',
              enabled: false,
              available: false,
              reason: 'no_workspace_folder',
              detail: 'This workspace has no folder, so there is no repository to inspect.'
            },
            {
              key: 'safe_branch',
              label: 'Branch precondition',
              description:
                'Code execution is blocked on a disallowed branch, and reports the current branch and what to do about it.',
              enabled: true,
              available: false,
              reason: 'no_workspace_folder',
              detail: 'This workspace has no folder, so there is no repository to inspect.'
            },
            {
              key: 'task_materialization',
              label: 'Approval creates the tasks',
              description:
                'Approved plan items become workspace tasks, with the approval recorded on each one.',
              enabled: true,
              available: true
            }
          ]
        }
      });
      return;
    }
    if (/^\/api\/workspaces\/[^/]+\/setup-wizard$/.test(url.pathname)) {
      await json(route, {
        success: true,
        setup: {
          workspace_id: url.pathname.split('/')[3],
          applicable: false,
          state: 'not_applicable',
          dismissed: false,
          auto_open: false
        }
      });
      return;
    }
    // Calendar Ops: calendar-ops-setup.js and calendar-console.js are loaded
    // globally and both auto-check the current workspace on page load. This
    // fixture workspace isn't a Calendar Ops workspace, so the accurate mocks
    // are each real handler's own "not applicable"/"no binding" shape --
    // internal/calendarhttp.Setup's applicable:false and Capabilities'
    // resolveGateway connector_missing (409), matching production exactly and
    // keeping both modules dormant for this scene.
    if (
      url.pathname === '/api/calendar-ops/setup' &&
      url.searchParams.get('workspace_id') === README_SCENES.workspace_command.workspace_id
    ) {
      await json(route, { applicable: false });
      return;
    }
    if (
      url.pathname === '/api/calendar-ops/capabilities' &&
      url.searchParams.get('workspace_id') === README_SCENES.workspace_command.workspace_id
    ) {
      await json(
        route,
        {
          error: 'no calendar connector is configured for this workspace',
          code: 'connector_missing'
        },
        409
      );
      return;
    }
    if (url.pathname === `/api/workspaces/${README_SCENES.workspace_command.workspace_id}`) {
      await json(route, workspace(README_SCENES.workspace_command.workspace_id));
      return;
    }
    if (/^\/api\/workspaces\/[^/]+\/native-mcp$/.test(url.pathname)) {
      await json(route, { servers: [] });
      return;
    }
    // The File Janitor panel mounts on every workspace page and asks whether it
    // applies before it can know that it does not. The README fixture workspace
    // is not a Janitor workspace, so it answers no and the panel stays hidden —
    // which is what these screenshots should show.
    //
    // Both prefixes are matched: in-repo callers use the canonical one, and the
    // legacy alias is still served for this release.
    if (/^\/api\/workspaces\/[^/]+\/(file|downloads)-janitor$/.test(url.pathname)) {
      await json(route, { status: { applies: false } });
      return;
    }
    // The capability catalog is likewise asked for on every workspace page.
    if (/^\/api\/workspaces\/[^/]+\/capabilities$/.test(url.pathname)) {
      await json(route, { capabilities: [] });
      return;
    }
    // The Workshop panel (agent Toolbox tab) mounts for every agent tab that
    // renders, not just the one the user has open, so it loads on every
    // workspace page too. The fixture workspace has no saved Toolboxes.
    if (/^\/api\/workspaces\/[^/]+\/toolboxes$/.test(url.pathname)) {
      await json(route, { toolboxes: [], workspace_version: 0 });
      return;
    }
    if (/^\/api\/workspaces\/[^/]+\/toolbox-workshop$/.test(url.pathname)) {
      await json(route, {
        workshop: {
          workspace_id: README_SCENES.workspace_command.workspace_id,
          core: [],
          agent_learned: [],
          workspace_provided: [],
          global_library: [],
          capacity: { used: 0, capacity: 0, full: false }
        },
        workspace: README_SCENES.workspace_command.workspace_id,
        workspace_version: 0
      });
      return;
    }
    // The Workshop checks for an available Undo right after it loads (its own
    // repaint, separate from the initial render). Nothing has been switched in
    // this fixture, so the real handler's own "nothing to undo" shape applies.
    if (/^\/api\/workspaces\/[^/]+\/agent-toolboxes\/[^/]+\/undo$/.test(url.pathname)) {
      await json(route, {
        available: false,
        message: 'There is nothing to undo for this agent.',
        workspace: README_SCENES.workspace_command.workspace_id
      });
      return;
    }
    // Goal Prepare loads unconditionally wherever its host exists in the DOM
    // (the shared config surface, relocated on demand into a stat-modal) --
    // see workspace-goal-prepare.js. Nothing has been accepted or recommended
    // yet in this fixture workspace.
    if (/^\/api\/workspaces\/[^/]+\/goal\/brief$/.test(url.pathname)) {
      await json(route, {
        accepted: null,
        proposed: null,
        policy: null,
        goal: README_SCENES.workspace_command.mission.title,
        workspace: README_SCENES.workspace_command.workspace_id,
        workspace_version: 0
      });
      return;
    }
    if (/^\/api\/workspaces\/[^/]+\/goal\/recommendations$/.test(url.pathname)) {
      await json(route, {
        recommendations: {
          message: 'Choose the agent that will carry out this goal to see recommendations.'
        },
        workspace: README_SCENES.workspace_command.workspace_id
      });
      return;
    }
    // The workspace header's TICKETS tile reads its number from the canonical
    // Ticket list, so this scene needs the endpoint even though the capture
    // never opens the Tickets view itself.
    if (/^\/api\/workspaces\/[^/]+\/tickets$/.test(url.pathname)) {
      await json(route, ticketPayload());
      return;
    }
    if (url.pathname === '/api/skills') {
      await json(route, { skills: [] });
      return;
    }
    if (url.pathname === '/api/providers') {
      await json(route, { providers: [] });
      return;
    }
    if (url.pathname === '/api/agents') {
      await json(route, { agents: README_SCENES.workspace_command.agents });
      return;
    }
    if (url.pathname === '/api/plugins') {
      await json(route, { plugins: [] });
      return;
    }
    // The workspace page resolves agent identities through the shared character
    // catalog. An empty catalog is a real, handled state — every agent falls
    // back to its deterministic identity — and none of this scene's fixture
    // agents has a curated character, so the captured screenshot is unchanged.
    if (url.pathname === '/api/characters') {
      await json(route, { catalog_version: '1.0.0', guide: null, characters: [] });
      return;
    }
    if (url.pathname === '/api/orchestration/workspace/activate') {
      await json(route, { activated: true });
      return;
    }
    if (url.pathname === '/api/orchestration/workflow/stream') {
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: 'event: done\ndata: {}\n\n'
      });
      return;
    }
    unexpectedRequests.push(pathWithQuery);
    await json(route, { error: `Missing README fixture for ${pathWithQuery}` }, 503);
  });
  return { consoleErrors, unexpectedRequests };
}

async function captureScene(
  page: Page,
  id: string,
  route: string,
  selector: string,
  definingText: string,
  prepare?: (page: Page) => Promise<void>
) {
  const fixture = await installFixtureRoutes(page);
  try {
    await page.goto(route, { waitUntil: 'networkidle' });
    if (prepare) await prepare(page);
    await page.addStyleTag({
      content:
        '*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important;}'
    });
    await page.evaluate(async () => document.fonts.ready);
    await expect(page.locator(selector)).toBeVisible();
    await expect(page.locator('body')).toContainText(definingText);
    const readinessRoot = page.locator(
      id === 'hero'
        ? '#homeDailyBrief'
        : id === 'action-center'
          ? '#action-center-list'
          : id === 'workspace-map'
            ? '#cockpitMap'
            : '#workspaceCommandView'
    );
    await expect(
      readinessRoot.locator(
        '.home-daily-brief-placeholder:visible, .workspace-detail-loading:visible, .ws-cmd-loadout-prompt.is-loading:visible, .is-error:visible, [role="alert"]:visible'
      )
    ).toHaveCount(0);
    await expect(page.locator('.modal.show')).toHaveCount(0);
    const visibleText = await page.locator('body').innerText();
    for (const pattern of privatePatterns) expect(visibleText).not.toMatch(pattern);
    expect(fixture.unexpectedRequests, `Unexpected fixture requests for ${id}`).toEqual([]);
    expect(fixture.consoleErrors, `Console errors for ${id}`).toEqual([]);
    mkdirSync(rawDirectory, { recursive: true });
    mkdirSync(sidecarDirectory, { recursive: true });
    await page.screenshot({
      path: path.join(rawDirectory, `${id}.png`),
      animations: 'disabled',
      caret: 'hide'
    });
    writeFileSync(
      path.join(sidecarDirectory, `${id}.json`),
      `${JSON.stringify({ id, route: page.url(), visible_text: visibleText }, null, 2)}\n`
    );
    sceneStatuses.push({ id, status: 'passed' });
  } catch (error) {
    sceneStatuses.push({
      id,
      status: 'failed',
      detail: error instanceof Error ? error.message : String(error)
    });
    throw error;
  }
}

test.afterAll(() => {
  if (!captureEnvironmentReady) return;
  mkdirSync(sidecarDirectory, { recursive: true });
  writeFileSync(
    path.join(runDirectory, 'scene-statuses.json'),
    `${JSON.stringify(sceneStatuses, null, 2)}\n`
  );
});

test('captures the Home command bridge and populated Personal HQ Daily Brief', async ({ page }) => {
  await captureScene(page, 'hero', '/', '.home-command-bridge', 'Resolve launch readiness risks');
});

test('captures exactly three Action Center findings', async ({ page }) => {
  await captureScene(
    page,
    'action-center',
    '/action-center',
    '#action-center-list',
    'Choose the research direction'
  );
  await expect(page.locator('.action-center-row')).toHaveCount(3);
});

test('captures the Workspace Map with Personal HQ and fictional workspaces', async ({ page }) => {
  // The Workspace Map is Home's default view now, not a separate launcher page,
  // so this scene captures the canonical route rather than photographing a
  // redirect. It selects a site first: with nothing selected this is the same
  // picture as `hero`. Select a site to preserve the Map's selected state, then
  // dismiss its context modal so the canonical capture remains a full-width,
  // Map-only scene. Dismissal intentionally keeps selection and camera.
  await captureScene(
    page,
    'workspace-map',
    '/',
    '#cockpitMap',
    'Northstar Personal HQ',
    async scenePage => {
      const selectedTile = scenePage.locator('.ws-map-tile[data-ws-id]').first();
      await selectedTile.click();
      await expect(scenePage.locator('#cockpitContextModal')).toBeVisible();
      await scenePage.keyboard.press('Escape');
      await expect(scenePage.locator('#cockpitContextModal')).toBeHidden();
      // `toBeHidden` can observe Bootstrap's display state just before its
      // hidden event restores focus and removes the backdrop. Waiting for both
      // effects keeps the selected-map frame byte-stable on slower CI runners.
      await expect(selectedTile).toBeFocused();
      await expect(scenePage.locator('.modal-backdrop')).toHaveCount(0);
    }
  );
  await expect(page.locator('.ws-map-tile.is-hq')).toHaveCount(1);
  await expect(page.locator('.ws-map-tile')).toHaveCount(3);
});

test('captures Workspace Command with agents, task state, note/file context, and mission', async ({
  page
}) => {
  await captureScene(
    page,
    'workspace',
    '/workspaces/ws-product-launch',
    '#workspaceCommandView',
    'Launch decision brief'
  );
  await expect(page.locator('.ws-cmd-deck')).toBeVisible();
  await expect(page.locator('#workspace-command-mission-card')).toBeVisible();
  await expect(page.locator('[data-view="detailed"]')).toHaveCount(0);
});
