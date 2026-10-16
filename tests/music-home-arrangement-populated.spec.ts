import { test, expect, type APIRequestContext, type Locator } from '@playwright/test';
import { createServer, type ServerResponse } from 'node:http';
import { createHash } from 'node:crypto';
import { cp, mkdir, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Populated Music Production Home → Organize library → exact collection review →
// one independent project activation, on a real isolated Ori host.
//
// Real: the disposable server, the exact staged Music Project Management and
// REAPER candidate commits (local development copies, NOT release-verified),
// the folder scan, the step-by-step portfolio setup (Home creator, then the
// library's initialize, offer-root grant and scan reviews in the browser), the
// bound Home Manager with its installed, trusted music-project-management skill,
// the planning tools, canonical history, collection review/commit, the activation
// queue and the per-project activation creator.
//
// Scripted: ONLY the model. A loopback OpenAI-compatible fixture returns fixed
// tool calls (search + select, then propose). This proves what the host shares,
// discloses, saves, reviews and applies for that model output. It is NOT evidence
// of live-model judgment.
//
//   ./scripts/reaper-demo.sh test --suite arrangement --music-source DIR \
//     --reaper-source DIR --install-order music-first --keep
const sandbox = process.env.ORI_MUSIC_REAPER_SANDBOX || '';
const modelPort = Number(process.env.ORI_ARRANGEMENT_MODEL_PORT || 0);
const musicPath = process.env.ORI_MUSIC_PLUGIN_PATH || '';
const reaperPath = process.env.ORI_REAPER_PLUGIN_PATH || '';
// With an older installed package (the harness's --music-upgrade-source run),
// planning is expected to be refused and the Home is then upgraded; that run
// keeps its own evidence beside the candidate run's.
const oldGuidance = process.env.ORI_ARRANGEMENT_OLD_GUIDANCE === '1';
const upgradeRun = oldGuidance || Boolean(process.env.ORI_MUSIC_UPGRADE_PATH);
const evidence =
  process.env.ORI_ARRANGEMENT_EVIDENCE_DIR ||
  join(
    process.cwd(),
    'tasks',
    'evidence-agentic-workspace-arrangement',
    'music-populated-browser',
    ...(upgradeRun ? ['upgrade-from-old-home'] : [])
  );

const MODEL = 'music-arrangement-fixture';
const GOAL = 'Keep experiments catalog-only; separate the client songs';
const SONGS = [
  'Client Alpha',
  'Client Beta',
  'Experiment 1',
  'Experiment 2',
  'Experiment 3',
  'Experiment 4'
];
const SENTINEL = 'PRIVATE_SONG_CONTENT_SENTINEL';
const TOOLS = [
  'arrangement_detail',
  'arrangement_overview',
  'arrangement_propose',
  'arrangement_search',
  'arrangement_select'
];

type Phase = 'setup' | 'planning' | 'review' | 'apply' | 'activation' | 'reload';

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()} → ${response.status()} ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

const sha = (bytes: Buffer) => createHash('sha256').update(bytes).digest('hex');

function findKey(value: any, key: string): any {
  if (!value || typeof value !== 'object') return undefined;
  if (Object.hasOwn(value, key)) return value[key];
  for (const child of Object.values(value)) {
    const found = findKey(child, key);
    if (found !== undefined) return found;
  }
  return undefined;
}

test('populated Home: Organize library → reviewed collection + one queued activation', async ({
  page,
  request
}) => {
  test.skip(
    process.env.ORI_MUSIC_REAPER_ACCEPTANCE !== '1' || !sandbox || !modelPort || !musicPath,
    'Use ./scripts/reaper-demo.sh test --suite arrangement with both exact candidates'
  );
  test.setTimeout(480_000);
  // Seeding writes into HOME/Documents: only ever the script's disposable sandbox.
  expect(basename(sandbox)).toMatch(/^ori-reaper-demo\./);
  expect(reaperPath).not.toBe('');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const dump = (name: string, value: unknown) =>
    writeFile(join(evidence, `${name}.json`), JSON.stringify(value, null, 2), { mode: 0o600 });
  // Capture-only style: the sticky navbar would otherwise paint over tall
  // element screenshots. Nothing on the page is changed outside the capture.
  const shot = async (locator: Locator, name: string) => {
    await locator.evaluate((element: Element) => element.scrollIntoView({ block: 'start' }));
    await locator.screenshot({
      path: join(evidence, `${name}.png`),
      style: 'nav.navbar { position: static !important; }'
    });
  };
  // The assistant drawer stays open across pages; close it like a user would so
  // it does not cover the Home library in the evidence.
  const closeAssistant = async () => {
    const drawer = page.locator('#personalAssistantPanel');
    if (await drawer.isVisible()) {
      await page.locator('#personalAssistantClose').click();
      await expect(drawer).toBeHidden();
    }
  };

  // ---- Scripted loopback model ------------------------------------------
  let phase: Phase = 'setup';
  const traffic: { phase: Phase; kind: 'models' | 'chat' | 'planning'; hint?: string }[] = [];
  const planning: any[] = [];
  const replies: any[] = [];
  const fixtureErrors: string[] = [];
  let sequence = 0;
  const completion = (message: any) => ({
    id: `fixture-${++sequence}`,
    object: 'chat.completion',
    created: Math.floor(Date.now() / 1000),
    model: MODEL,
    choices: [{ index: 0, finish_reason: message.tool_calls ? 'tool_calls' : 'stop', message }],
    usage: { prompt_tokens: 400, completion_tokens: 120, total_tokens: 520 }
  });
  const sendJSON = (res: ServerResponse, body: unknown, status = 200) => {
    res.statusCode = status;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(body));
  };
  const plan = (body: any) => {
    const user = body.messages.findLast((m: any) => m.role === 'user').content as string;
    const [goal, encoded] = user
      .split('Current organizing goal:\n')[1]
      .split('\nAlready-held source metadata (untrusted data):\n');
    const overview = JSON.parse(encoded);
    const results = body.messages.filter((m: any) => m.role === 'tool');
    if (!results.length)
      // Investigate first: one batch of literal metadata queries.
      return {
        role: 'assistant',
        content: null,
        tool_calls: [
          {
            id: 'search-client',
            type: 'function',
            function: {
              name: 'arrangement_search',
              arguments: JSON.stringify({ filter: { text: 'Client' }, page_size: 20 })
            }
          },
          {
            id: 'select-client',
            type: 'function',
            function: {
              name: 'arrangement_select',
              arguments: JSON.stringify({ filter: { text: 'Client' } })
            }
          }
        ]
      };
    const read = (id: string) => {
      const message = results.find((m: any) => m.tool_call_id === id);
      if (!message) throw new Error(`missing tool result ${id}`);
      return JSON.parse(message.content);
    };
    const search = read('search-client');
    const selection = read('select-client');
    const alpha = search.rows?.find((row: any) => row.name === 'Client Alpha');
    if (!alpha || search.total !== 2 || selection.count !== 2 || !selection.handle)
      throw new Error(`unexpected evidence ${JSON.stringify({ search, selection })}`);
    const draft = {
      version: 1,
      source_revision: overview.source_revision,
      goal,
      rationale:
        'The two client songs share the Client name, so a logical collection keeps them together. The experiment songs are left as they are. Only Client Alpha is selected for a later, separate activation review.',
      unknowns: ['Whether Client Beta also needs active work is not known from saved metadata.'],
      unchanged: { ids: [] },
      needs_review: [],
      operations: [
        { kind: 'create_collection', name: 'Client work', members: { handle: selection.handle } },
        { kind: 'select_activation', members: { ids: [alpha.id] } }
      ]
    };
    return {
      role: 'assistant',
      content: null,
      tool_calls: [
        {
          id: 'propose-client',
          type: 'function',
          function: { name: 'arrangement_propose', arguments: JSON.stringify(draft) }
        }
      ]
    };
  };
  const server = createServer(async (req, res) => {
    if (req.method === 'GET' && req.url?.endsWith('/models')) {
      traffic.push({ phase, kind: 'models' });
      return sendJSON(res, { object: 'list', data: [{ id: MODEL, object: 'model' }] });
    }
    if (req.method !== 'POST' || !req.url?.endsWith('/chat/completions')) {
      res.statusCode = 404;
      return res.end();
    }
    let raw = '';
    for await (const chunk of req) raw += chunk;
    let body: any;
    try {
      body = JSON.parse(raw);
    } catch (_) {
      fixtureErrors.push('unparseable request');
      return sendJSON(res, { error: { message: 'invalid JSON' } }, 400);
    }
    const isPlanning = (body.tools || []).some(
      (tool: any) => tool?.function?.name === 'arrangement_propose'
    );
    const system = (body.messages || []).find((m: any) => m.role === 'system')?.content;
    traffic.push({
      phase,
      kind: isPlanning ? 'planning' : 'chat',
      hint: isPlanning ? undefined : String(system || '').slice(0, 160)
    });
    if (!isPlanning) {
      if (body.stream) {
        res.setHeader('Content-Type', 'text/event-stream');
        const chunk = (delta: any, finish: string | null) =>
          `data: ${JSON.stringify({ id: `fixture-${++sequence}`, object: 'chat.completion.chunk', model: MODEL, choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`;
        res.write(chunk({ role: 'assistant', content: 'Fixture model ready.' }, null));
        res.write(chunk({}, 'stop'));
        return res.end('data: [DONE]\n\n');
      }
      return sendJSON(res, completion({ role: 'assistant', content: 'Fixture model ready.' }));
    }
    planning.push(body);
    if (body.stream) fixtureErrors.push('planning request asked for streaming');
    let message: any;
    try {
      message = plan(body);
    } catch (error) {
      fixtureErrors.push(String(error));
      message = { role: 'assistant', content: 'The scripted fixture could not continue.' };
    }
    replies.push(message);
    return sendJSON(res, completion(message));
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(modelPort, '127.0.0.1', () => resolve());
  });
  const calls = (which: Phase) => ({
    chat: traffic.filter(t => t.phase === which && t.kind === 'chat').length,
    planning: traffic.filter(t => t.phase === which && t.kind === 'planning').length,
    model_list: traffic.filter(t => t.phase === which && t.kind === 'models').length
  });
  const inference = (which: Phase) => calls(which).chat + calls(which).planning;

  try {
    // ---- a. Seed the song folders and record their exact bytes ----------
    const documents = join(sandbox, 'Documents');
    const files: string[] = [];
    for (const name of SONGS) {
      const folder = join(documents, name);
      await mkdir(folder, { recursive: true, mode: 0o750 });
      const file = join(folder, 'Song.rpp');
      await writeFile(
        file,
        `<REAPER_PROJECT 0.1 "7.0" 1700000000\n  TEMPO 120 4 4\n  <NOTES 0 2\n    |${SENTINEL} ${name}\n  >\n>\n`,
        { mode: 0o600 }
      );
      files.push(file);
    }
    const hashes = async () =>
      Object.fromEntries(await Promise.all(files.map(async f => [f, sha(await readFile(f))])));
    const listings = async () =>
      Object.fromEntries(
        await Promise.all(
          SONGS.map(async name => [name, (await readdir(join(documents, name))).sort()])
        )
      );
    const sourceHashes = await hashes();
    const sourceListings = await listings();

    // ---- b. Populate the Home through the legitimate portfolio path ----
    await ok(await request.post('/api/onboarding/skip'));
    await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
    // c (part 1). The system model is the loopback fixture; staffing defaults to it.
    await ok(
      await request.post('/api/settings/system-model', {
        data: { provider: 'lmstudio', model: MODEL }
      })
    );
    const plugins = (await ok(await request.get('/api/plugins'))).plugins || [];
    const music = plugins.find((row: any) => row.name === 'music-project-management');
    const reaper = plugins.find((row: any) => row.name === 'reaper-plugin');
    expect(music).toMatchObject({ enabled: true, source: musicPath });
    expect(reaper).toMatchObject({ enabled: true, source: reaperPath });
    const initial = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'populated-arrangement-hire',
          if_version: initial.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my songs organized.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    const hired = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: 'populated-arrangement-hq',
          if_version: hired.state_version,
          name: 'My HQ'
        }
      })
    );
    const scanned = await ok(
      await request.post('/api/personal-assistant/folder-digest/scan', {
        data: { chip: 'documents' }
      })
    );
    await dump('01-portfolio-offer', scanned.offer);
    expect(scanned.offer?.portfolio?.projects).toBe(SONGS.length);
    const workspacesAtStart = (await ok(await request.get('/api/workspaces'))).folders.length;

    // The step-by-step portfolio path (Adjust…): the Home creator, then the
    // library's own initialize, offer-root grant and scan reviews. The one-card
    // Set up runs the same steps server-side, but on a computer where the Home
    // profile step detects a DAW its receipt carries a `profile` row that the
    // folder-digest validator rejects, so that card ends "Setup paused".
    const nativeDialogs: string[] = [];
    page.on('dialog', async dialog => {
      nativeDialogs.push(dialog.message());
      await dialog.dismiss(); // None is expected: the provider is already installed.
    });
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.goto('/');
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() =>
      (window as any).PersonalAssistantPanel?.open?.(
        document.getElementById('personalAssistantLauncher')
      )
    );
    const card = page.locator('#personalAssistantFolderOffer');
    await expect(card).toBeVisible();
    await expect(card).toContainText(`${SONGS.length} music projects`);
    await shot(card, '00-portfolio-offer');
    await card.getByRole('button', { name: 'Adjust…', exact: true }).click();
    const creator = page.locator('#addFolderModal');
    await expect(creator).toBeVisible({ timeout: 90_000 });
    await creator.getByRole('button', { name: 'Continue →' }).click();
    await creator.getByRole('button', { name: 'Continue →' }).click();
    await expect(creator.locator('.ws-role-row[data-role-id="portfolio_manager"]')).toContainText(
      'Music Portfolio Manager'
    );
    await creator.getByRole('button', { name: 'Review →' }).click();
    await creator.locator('#createFolderBtn').click();
    await page.waitForURL(/\/assistant\?folder_offer_id=/, { timeout: 60_000 });
    expect(nativeDialogs).toEqual([]);
    const libraryURL = new URL(page.url()).pathname;
    const slug = decodeURIComponent(libraryURL.split('/')[2] || '');
    const folders = (await ok(await request.get('/api/workspaces'))).folders;
    const home = folders.find((row: any) => row.folder_slug === slug);
    expect(home, JSON.stringify(folders)).toMatchObject({ kind: 'group' });
    const homeID = home.id as string;
    const base = `/api/workspaces/${homeID}/assistant-program/library`;
    expect((await ok(await request.get(`${base}/roots`))).initialized).toBe(false);
    await closeAssistant();
    const setupCard = page.locator('#projectSetupNext');
    await expect(setupCard.locator('#projectSetupNextAction')).toHaveText(
      'Start library and review Documents'
    );
    await setupCard.locator('#projectSetupNextAction').click();
    const grant = page.getByRole('dialog', { name: 'Connect this discovery folder?' });
    await expect(grant).toContainText(documents);
    expect((await ok(await request.get(`${base}/roots`))).total_roots).toBe(0);
    await shot(grant, '00b-library-root-grant');
    await grant.getByRole('button', { name: 'Connect folder' }).click();
    const scanReview = page.getByRole('dialog', { name: 'Scan this discovery folder once?' });
    await expect(scanReview).toContainText(documents);
    await shot(scanReview, '00c-library-scan-review');
    await scanReview.getByRole('button', { name: 'Scan metadata' }).click();
    await expect(page.locator('#projectLibraryCount')).toHaveText(
      `${SONGS.length} of ${SONGS.length} projects`,
      { timeout: 60_000 }
    );
    const roots = await ok(await request.get(`${base}/roots`));
    expect(roots.total_roots).toBeGreaterThanOrEqual(1);
    const catalog = await ok(await request.get(`${base}/projects`));
    expect(catalog.total).toBe(SONGS.length);
    expect(catalog.rows.map((row: any) => row.name).sort()).toEqual([...SONGS].sort());
    expect(catalog.rows.every((row: any) => row.connection === 'catalog_only')).toBe(true);
    const entry = (name: string) => catalog.rows.find((row: any) => row.name === name);
    const alphaID = entry('Client Alpha').id as string;
    await dump('02-populated-library', {
      home: { id: homeID, slug, name: home.name },
      roots: roots.roots?.map((root: any) => ({ id: root.id, status: root.status })),
      total_roots: roots.total_roots,
      projects: catalog.rows.map((row: any) => ({
        id: row.id,
        name: row.name,
        connection: row.connection
      }))
    });
    // A completed scan starts the Home's bounded Manager review turn (an
    // existing feature, not Organize library). Let it settle so its model call
    // is counted in setup, never in a later phase.
    await expect
      .poll(
        async () =>
          String((await ok(await request.get(`${base}/summary`))).proposal_run?.status || ''),
        { timeout: 90_000 }
      )
      .toMatch(/^(finished|skipped)$/);
    const scanReviewRun = (await ok(await request.get(`${base}/summary`))).proposal_run;
    const setupCalls = calls('setup');
    expect(setupCalls.planning).toBe(0);

    // ---- c. The actually bound Manager, its model and trusted skill -----
    const program = await ok(await request.get(`/api/workspaces/${homeID}/assistant-program`));
    const roster = (findKey(program, 'roster') || []) as any[];
    const manager = roster.find(binding => binding.role_id === 'portfolio_manager');
    expect(manager, JSON.stringify(program)).toBeTruthy();
    const untrusted = await request.get(`${base}/arrangements/context`);
    const untrustedBody = await untrusted.json();
    const bindingsBefore = await ok(await request.get(`/api/workspaces/${homeID}/skill-bindings`));
    await dump('03-context-before-trust', {
      status: untrusted.status(),
      body: untrustedBody,
      bindings: bindingsBefore
    });
    const existing = (findKey(bindingsBefore, 'bindings') || bindingsBefore || []) as any[];
    const bound = Array.isArray(existing)
      ? existing.find(b => b.skill_name === 'music-project-management')
      : undefined;
    if (!bound) {
      // Planning never creates or trusts a binding itself (the context above is
      // refused without one). This is the owner's explicit workspace trust choice.
      expect(untrusted.status()).toBe(409);
      expect(untrustedBody.arrangement_error?.code).toBe('unavailable');
      await ok(
        await request.post(`/api/workspaces/${homeID}/skill-bindings`, {
          data: { skill_name: 'music-project-management', enabled: true, trusted: true, config: {} }
        })
      );
    }
    const context = await ok(await request.get(`${base}/arrangements/context`));
    const bindingsAfter = await ok(await request.get(`/api/workspaces/${homeID}/skill-bindings`));
    await dump('04-context-after-trust', { context, bindings: bindingsAfter, scanReviewRun });
    expect(
      (bindingsAfter.bindings || []).filter(
        (b: any) => b.skill_name === 'music-project-management' && b.enabled && b.trusted
      )
    ).toHaveLength(1);
    expect(context).toMatchObject({
      home_id: homeID,
      agent_name: manager.agent_name,
      provider: 'lmstudio',
      model: MODEL
    });
    expect(context.context_pin).toMatch(/^[a-f0-9]{64}$/);
    const manifest = JSON.parse(await readFile(join(musicPath, '.ori-plugin/plugin.json'), 'utf8'));
    const expectedRole = manifest.assistant_program_homes[0].roles.find(
      (role: any) => role.id === 'portfolio_manager'
    ).system_prompt as string;
    const skillBytes = await readFile(
      join(musicPath, 'skills/music-project-management/SKILL.md'),
      'utf8'
    );
    const expectedSkill = skillBytes.replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n/, '').trim();
    expect(expectedRole.length).toBeGreaterThan(0);
    expect(expectedSkill.length).toBeGreaterThan(0);

    // ---- d. Home page → Organize library with explicit consent ---------
    await page.goto(libraryURL);
    const shelf = page.locator('#projectLibraryPanel');
    await expect(shelf.locator('#projectLibraryCount')).toHaveText(
      `${SONGS.length} of ${SONGS.length} projects`,
      { timeout: 30_000 }
    );
    await closeAssistant();
    await shot(shelf, '10-populated-library');
    const workspacesBeforePlanning = (await ok(await request.get('/api/workspaces'))).folders
      .length;
    const panel = page.locator('#projectLibraryArrangementPlanning');
    await panel.getByText('Organize library', { exact: true }).click();
    await expect(panel.getByText('Context checked.', { exact: false })).toBeVisible();
    await panel.getByRole('textbox', { name: 'Organizing goal' }).fill(GOAL);
    await expect(panel.getByText('Planning recipient:', { exact: false })).toContainText(
      `${manager.agent_name} · lmstudio / ${MODEL}`
    );
    const send = panel.getByRole('button', { name: 'Send organizing goal' });
    await expect(send).toHaveAttribute('aria-disabled', 'true');
    await panel
      .getByRole('checkbox', { name: 'Share this bounded metadata for one planning turn' })
      .check();
    const upgrade = panel.getByRole('checkbox', {
      name: /Saving a proposal or clarification upgrades/
    });
    const upgradeShown = await upgrade.isVisible();
    if (upgradeShown) await upgrade.check();
    await expect(send).toHaveAttribute('aria-disabled', 'false');
    expect(inference('setup')).toBe(setupCalls.chat); // Opening/consent called no model.
    await shot(panel, '11-planning-disclosure');
    phase = 'planning';
    const plannedResponse = page.waitForResponse(r => r.url().endsWith('/arrangements/plan'));
    await send.click();
    const planned = await plannedResponse;
    if (oldGuidance) {
      // The older package's full guidance leaves no room for an investigate-
      // then-propose turn inside the host's fixed budget, which is never raised
      // or truncated for it: the turn is refused, nothing is saved or created.
      const refusal = await planned.json();
      await dump('12-plan-refusal', {
        status: planned.status(),
        body: refusal,
        planning: planning.length
      });
      expect(planned.status(), JSON.stringify(refusal)).toBe(409);
      expect(refusal.arrangement_error?.code).toBe('limit_reached');
      expect((await ok(await request.get(`${base}/arrangements`))).arrangements).toHaveLength(0);
      expect((await ok(await request.get('/api/workspaces'))).folders.length).toBe(
        workspacesBeforePlanning
      );
      // A refusal decided before any save is reported as certain, not as an
      // unconfirmed save that would demand a history reopen.
      await expect(panel).toContainText('Refused before anything was saved or applied');
      await expect(panel).not.toContainText('could not be confirmed');
      await shot(panel, '12-old-guidance-refusal');
      expect(await hashes()).toEqual(sourceHashes);
      await dump('99-summary', {
        kind: 'old-guidance-refusal-before-upgrade',
        boundary:
          'Only the model is scripted. The older package is installed; planning is refused before the Home upgrade.',
        candidates: { music: { path: musicPath, revision: process.env.ORI_MUSIC_PLUGIN_REVISION } },
        home_id: homeID,
        library_path: libraryURL,
        planning_calls_before_refusal: planning.length,
        source_hashes: sourceHashes,
        workspaces: { before_planning: workspacesBeforePlanning }
      });
      return;
    }
    expect(
      planned.ok(),
      `planning ${planned.status()} ${await planned.text()} errors=${fixtureErrors.join('; ')}`
    ).toBeTruthy();
    await dump('12-plan-response', await planned.json());
    await expect(
      panel.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    expect(fixtureErrors).toEqual([]);
    expect(planning).toHaveLength(2);
    expect(calls('planning')).toMatchObject({ planning: 2, chat: 0 });
    for (const body of planning) {
      const text = JSON.stringify(body);
      const system = body.messages
        .filter((m: any) => m.role === 'system')
        .map((m: any) => m.content)
        .join('\n');
      expect(body.model).toBe(MODEL);
      expect(system).toContain(expectedSkill);
      expect(system).toContain(expectedRole);
      expect(body.tools).toHaveLength(5);
      expect(body.tools.map((tool: any) => tool.function.name).sort()).toEqual(TOOLS);
      // No path in any spelling (the sandbox basename is unique), no file bytes.
      for (const leak of [SENTINEL, 'TEMPO 120', basename(sandbox), '/var/folders', '/private/'])
        expect(text).not.toContain(leak);
    }
    expect(replies.map(r => r.tool_calls?.map((c: any) => c.function.name))).toEqual([
      ['arrangement_search', 'arrangement_select'],
      ['arrangement_propose']
    ]);
    const saved = await ok(await request.get(`${base}/arrangements`));
    expect(saved.arrangements).toHaveLength(1);
    expect(saved.arrangements[0]).toMatchObject({ kind: 'proposal' });
    expect((await ok(await request.get(`${base}/collections`))).total).toBe(0);
    expect((await ok(await request.get('/api/workspaces'))).folders.length).toBe(
      workspacesBeforePlanning
    );
    const rows = page.locator('#projectLibraryArrangementRows');
    const startReview = rows.getByRole('button', {
      name: 'Review collection metadata',
      exact: true
    });
    await expect(startReview).toBeVisible();

    // ---- e. Review exact effects, confirm, verify the receipt ----------
    phase = 'review';
    // Reading the saved history is a canonical read, never a model call.
    await rows.getByRole('button', { name: 'Open saved arrangement' }).click();
    await expect(
      rows.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    await expect(rows).toContainText('Client work');
    await shot(page.locator('#projectLibraryArrangements'), '12-proposal-history');
    const reviewResponse = page.waitForResponse(
      r => r.url().endsWith('/collection-review') && r.request().method() === 'POST'
    );
    await startReview.click();
    const firstPage = await ok(await reviewResponse);
    await dump('13-review-page-0', firstPage);
    expect(firstPage.effects).toHaveLength(2);
    expect(firstPage.effects[0]).toMatchObject({
      kind: 'create_collection',
      after_name: 'Client work',
      before_members: 0,
      after_members: 2
    });
    expect(firstPage.effects[1]).toMatchObject({
      kind: 'select_activation',
      collection_id: '',
      before_members: 0,
      after_members: 1
    });
    expect(firstPage.unchanged).toBe(0);
    expect(firstPage.members.map((m: any) => [m.name, m.before, m.after]).sort()).toEqual([
      ['Client Alpha', false, true],
      ['Client Beta', false, true]
    ]);
    const review = rows.getByRole('region', { name: 'Review exact collection metadata' });
    await expect(
      review.getByRole('heading', { name: 'Review exact collection metadata' })
    ).toBeVisible();
    await expect(review).toContainText('New collection “Client work” · 2 members');
    await expect(review).toContainText('Select 1 project for separate activation reviews');
    await expect(review).not.toContainText(alphaID);
    await expect(review).toContainText('Queue preparation creates no workspace or project link');
    await expect(review.locator('li', { hasText: /^Add: Client Alpha/ })).toHaveCount(1);
    await expect(review.locator('li', { hasText: /^Add: Client Beta/ })).toHaveCount(1);
    await expect(review.locator('li', { hasText: /^Add: Experiment/ })).toHaveCount(0);
    await shot(review, '13-review-page');
    const activationPageResponse = page.waitForResponse(
      r => r.url().includes('/collection-review/') && r.url().includes('operation=1')
    );
    await review.getByRole('combobox', { name: 'Collection effect' }).selectOption('1');
    const activationPage = await ok(await activationPageResponse);
    await dump('13-review-page-1', activationPage);
    expect(activationPage.members.map((m: any) => [m.id, m.name, m.after])).toEqual([
      [alphaID, 'Client Alpha', true]
    ]);
    await expect(review.locator('li', { hasText: /^Add: Client Alpha/ })).toHaveCount(1);
    await expect(review.locator('li', { hasText: /^Add: Client Beta/ })).toHaveCount(0);
    await shot(review, '13b-review-activation-selection');
    expect(inference('review')).toBe(0);

    phase = 'apply';
    await review
      .getByRole('checkbox', {
        name: /I confirm the listed metadata changes and exact activation-review selection/
      })
      .check();
    const commitResponse = page.waitForResponse(r => r.url().endsWith('/collection-commit'));
    await review
      .getByRole('button', { name: 'Save metadata and prepare activation reviews', exact: true })
      .click();
    const receipt = await ok(await commitResponse);
    await dump('14-collection-receipt', receipt);
    expect(receipt.arrangement.state).toBe('completed');
    const effects = receipt.collection_receipt.effects;
    expect(effects.map((e: any) => e.kind)).toEqual(['create_collection', 'select_activation']);
    expect(effects[1].selected_count).toBe(1);
    const receiptCard = rows.getByRole('region', {
      name: 'Saved metadata and activation-review selection'
    });
    await expect(receiptCard).toBeVisible();
    await expect(receiptCard).toContainText(
      '1 project(s) selected for independent activation review; queue preparation did not create or activate them.'
    );
    await shot(receiptCard, '14-receipt');
    const collections = await ok(await request.get(`${base}/collections`));
    expect(collections.total).toBe(1);
    expect(collections.collections[0]).toMatchObject({ name: 'Client work', members: 2 });
    const members = await ok(
      await request.get(
        `${base}/collections/${encodeURIComponent(collections.collections[0].id)}?context_pin=${collections.context_pin}&offset=0`
      )
    );
    expect(members.members.map((m: any) => m.name).sort()).toEqual(['Client Alpha', 'Client Beta']);
    const queued = await ok(await request.get(`${base}/queue`));
    expect(queued.queue).toMatchObject({ status: 'active', ids: [alphaID], index: 0 });
    expect(queued.queue.id).toBe(effects[1].queue_id);
    const afterApply = await ok(await request.get(`${base}/projects`));
    expect(afterApply.rows.every((row: any) => row.connection === 'catalog_only')).toBe(true);
    expect((await ok(await request.get('/api/workspaces'))).folders.length).toBe(
      workspacesBeforePlanning
    );
    const browser = page.locator('#projectLibraryCollectionBrowser');
    await expect(browser.getByRole('heading', { name: 'Client work' })).toBeVisible();
    await expect(browser).toContainText('2 saved members');
    await shot(browser, '14b-collection-browser');
    expect(inference('apply')).toBe(0);

    // ---- f. One independent activation through the saved queue -------
    phase = 'activation';
    const beforeActivation = (await ok(await request.get('/api/workspaces'))).folders;
    const queueStatus = page.locator('#projectLibraryQueueStatus');
    await expect(queueStatus).toContainText('0 of 1 handled');
    await page.locator('#projectLibraryQueueResume').click();
    const song = page.getByRole('dialog', { name: 'Song 1 of 1 · Client Alpha' });
    await expect(song).toBeVisible();
    await shot(song, '15-queue-song');
    await song.getByRole('button', { name: 'Review this song' }).click();
    const setupForm = page.getByRole('dialog', { name: 'Set up Client Alpha' });
    await expect(
      setupForm.getByRole('combobox', { name: 'Authoritative project file' })
    ).toHaveValue('Song.rpp');
    await expect(setupForm.getByRole('textbox', { name: 'Project workspace name' })).toHaveValue(
      'Client Alpha'
    );
    await shot(setupForm, '15b-activation-file-choice');
    const activationReviewResponse = page.waitForResponse(r =>
      r.url().endsWith(`/projects/${encodeURIComponent(alphaID)}/activation/review`)
    );
    await setupForm.getByRole('button', { name: 'Review this project' }).click();
    const activationReview = await ok(await activationReviewResponse);
    await dump('15-activation-review', activationReview);
    expect(activationReview).toMatchObject({
      project_file: 'Song.rpp',
      workspace_name: 'Client Alpha'
    });
    const confirmation = page.getByRole('dialog', { name: 'Connect this one project?' });
    await expect(confirmation).toContainText('Queue item 1 of 1: Client Alpha');
    await expect(confirmation).toContainText('Authoritative file: Song.rpp');
    await expect(confirmation).toContainText('Installed project roles: REAPER Assistant');
    await expect(confirmation).toContainText('Starts File-only');
    await expect(confirmation).toContainText(
      'Project-role staffing and live access require separate reviews'
    );
    await expect(confirmation).toContainText(
      'Other queued songs remain untouched until separately confirmed.'
    );
    await shot(confirmation, '15c-activation-review');
    expect((await ok(await request.get('/api/workspaces'))).folders).toHaveLength(
      beforeActivation.length
    );
    const activationCommit = page.waitForResponse(
      r =>
        r.url().endsWith(`/projects/${encodeURIComponent(alphaID)}/activation/commit`) &&
        r.request().method() === 'POST'
    );
    await confirmation.getByRole('button', { name: 'Connect project' }).click();
    const activated = await ok(await activationCommit);
    await dump('16-activation-commit', activated);
    await expect(shelf.locator('#projectLibraryStatus')).toContainText(
      'Review queue complete. Only separately confirmed songs were connected.'
    );
    const afterActivation = (await ok(await request.get('/api/workspaces'))).folders;
    expect(afterActivation).toHaveLength(beforeActivation.length + 1);
    const created = afterActivation.filter(
      (row: any) => !beforeActivation.some((old: any) => old.id === row.id)
    );
    expect(created.map((row: any) => row.id)).toEqual([activated.workspace_id]);
    const child = await ok(await request.get(`/api/workspaces/${activated.workspace_id}`));
    await dump('16-activated-workspace', child);
    // Linked to exactly the Client Alpha folder and its Song.rpp; no agent was
    // staffed (project roles need their own review).
    expect(child).toMatchObject({ kind: 'workspace', parent_id: homeID, agent_count: 0 });
    expect(child.assistant_program?.link?.station_workspace_id).toBe(homeID);
    expect(child.directory_references).toHaveLength(1);
    expect(await realpath(child.directory_references[0].path)).toBe(
      await realpath(join(documents, 'Client Alpha'))
    );
    expect(child.shared_data?.project_entry).toMatchObject({
      kind: 'directory_reference',
      directory_reference_id: child.directory_references[0].id,
      relative_path: 'Song.rpp'
    });
    for (const other of SONGS.filter(name => name !== 'Client Alpha'))
      expect(JSON.stringify(child)).not.toContain(`/Documents/${other}`);
    const finalCatalog = await ok(await request.get(`${base}/projects`));
    const connections = Object.fromEntries(
      finalCatalog.rows.map((row: any) => [row.name, row.connection])
    );
    expect(connections).toEqual({
      'Client Alpha': 'connected',
      'Client Beta': 'catalog_only',
      'Experiment 1': 'catalog_only',
      'Experiment 2': 'catalog_only',
      'Experiment 3': 'catalog_only',
      'Experiment 4': 'catalog_only'
    });
    const finished = await ok(await request.get(`${base}/queue`));
    expect(finished.queue).toBeUndefined();
    expect(finished.recent[0]).toMatchObject({
      status: 'complete',
      selected_count: 1,
      connected_count: 1,
      skipped_count: 0
    });
    await expect(
      shelf.locator('#projectLibraryRows tr').filter({ hasText: 'Client Alpha' })
    ).toContainText('connected');
    await shot(shelf, '17-final-library');
    expect(inference('activation')).toBe(0);

    // ---- g. Reload: everything renders from canonical state -----------
    phase = 'reload';
    await page.reload();
    await expect(shelf.locator('#projectLibraryCount')).toHaveText(
      `${SONGS.length} of ${SONGS.length} projects`,
      { timeout: 30_000 }
    );
    await closeAssistant();
    const history = page.locator('#projectLibraryArrangementRows');
    await expect(history.getByRole('button', { name: 'Open saved arrangement' })).toHaveCount(1);
    await history.getByRole('button', { name: 'Open saved arrangement' }).click();
    await expect(
      history.getByRole('heading', { name: 'Saved metadata and activation-review selection' })
    ).toBeVisible();
    await expect(history).not.toContainText('Historical context has changed');
    const reloadedBrowser = page.locator('#projectLibraryCollectionBrowser');
    await expect(reloadedBrowser).toContainText('2 saved members');
    await reloadedBrowser.getByRole('button', { name: 'View members: Client work' }).click();
    const memberNames = reloadedBrowser.locator('li strong');
    await expect(memberNames).toHaveCount(2); // Exactly these members; order is not a contract.
    expect((await memberNames.allInnerTexts()).sort()).toEqual(['Client Alpha', 'Client Beta']);
    await expect(page.locator('#projectLibraryQueueHistory')).toContainText(
      '1 connected · 0 skipped'
    );
    await shot(page.locator('#projectLibraryArrangements'), '18-reload-receipt');
    await shot(reloadedBrowser, '18b-reload-collection-members');
    expect(inference('reload')).toBe(0);

    // ---- Source files and model accounting ----------------------------
    expect(await hashes()).toEqual(sourceHashes);
    const finalListings = await listings();
    const summary = {
      kind: 'exact-candidate-real-host-scripted-loopback-model',
      boundary:
        'Only the model is scripted; this is not live-model judgment. Candidates are local development copies, not release-verified.',
      candidates: {
        music: { path: musicPath, revision: process.env.ORI_MUSIC_PLUGIN_REVISION },
        reaper: { path: reaperPath, revision: process.env.ORI_REAPER_PLUGIN_REVISION }
      },
      home_id: homeID,
      library_path: libraryURL,
      manager: manager.agent_name,
      skill_binding_created_by_test: !bound,
      upgrade_consent_shown: upgradeShown,
      scan_review_run: scanReviewRun,
      native_dialogs: nativeDialogs,
      model_calls: Object.fromEntries(
        (['setup', 'planning', 'review', 'apply', 'activation', 'reload'] as Phase[]).map(p => [
          p,
          calls(p)
        ])
      ),
      setup_chat_hints: traffic.filter(t => t.kind === 'chat').map(t => `${t.phase}: ${t.hint}`),
      source_hashes: sourceHashes,
      source_listings_before: sourceListings,
      source_listings_after: finalListings,
      workspaces: {
        at_start: workspacesAtStart,
        before_planning: workspacesBeforePlanning,
        after_activation: afterActivation.length,
        activated: activated.workspace_id
      }
    };
    await dump('99-summary', summary);
    await dump('99-planning-provider-trace', {
      skill_sha256: sha(Buffer.from(skillBytes)),
      requests: planning,
      replies
    });
  } finally {
    await dump('99-model-traffic', traffic).catch(() => {});
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
});

// Run by the harness after it restarts the same disposable server: the receipt,
// collection membership and queue history come back from canonical storage,
// with no planning call, no new workspace and no change to the song files.
test('restart reopens the saved arrangement, collection and queue from canonical state', async ({
  page,
  request
}) => {
  test.skip(
    process.env.ORI_ARRANGEMENT_RESTART_CHECK !== '1' || !sandbox || !modelPort,
    'Run by ./scripts/reaper-demo.sh test --suite arrangement after its restart'
  );
  expect(basename(sandbox)).toMatch(/^ori-reaper-demo\./);
  const summary = JSON.parse(await readFile(join(evidence, '99-summary.json'), 'utf8'));
  // The model fixture only answers and counts: nothing here may plan.
  let planningCalls = 0;
  const server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET' && req.url?.endsWith('/models'))
      return res.end(JSON.stringify({ object: 'list', data: [{ id: MODEL, object: 'model' }] }));
    let raw = '';
    for await (const chunk of req) raw += chunk;
    const body = raw ? JSON.parse(raw) : {};
    if ((body.tools || []).some((tool: any) => tool?.function?.name === 'arrangement_propose'))
      planningCalls++;
    return res.end(
      JSON.stringify({
        id: 'restart-fixture',
        object: 'chat.completion',
        model: MODEL,
        choices: [
          {
            index: 0,
            finish_reason: 'stop',
            message: { role: 'assistant', content: 'Fixture model ready.' }
          }
        ]
      })
    );
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(modelPort, '127.0.0.1', () => resolve());
  });
  try {
    await page.goto(summary.library_path);
    const shelf = page.locator('#projectLibraryPanel');
    await expect(shelf.locator('#projectLibraryCount')).toHaveText(
      `${SONGS.length} of ${SONGS.length} projects`,
      { timeout: 30_000 }
    );
    const drawer = page.locator('#personalAssistantPanel');
    if (await drawer.isVisible()) {
      await page.locator('#personalAssistantClose').click();
      await expect(drawer).toBeHidden();
    }
    const history = page.locator('#projectLibraryArrangementRows');
    await history.getByRole('button', { name: 'Open saved arrangement' }).click();
    await expect(
      history.getByRole('heading', { name: 'Saved metadata and activation-review selection' })
    ).toBeVisible();
    const browser = page.locator('#projectLibraryCollectionBrowser');
    await expect(browser).toContainText('2 saved members');
    await browser.getByRole('button', { name: 'View members: Client work' }).click();
    const memberNames = browser.locator('li strong');
    await expect(memberNames).toHaveCount(2);
    expect((await memberNames.allInnerTexts()).sort()).toEqual(['Client Alpha', 'Client Beta']);
    await expect(page.locator('#projectLibraryQueueHistory')).toContainText(
      '1 connected · 0 skipped'
    );
    // The activation it queued moved the catalog on: a note, never a warning.
    await expect(history).toContainText('The catalog has changed since this was applied');
    await expect(history).not.toContainText('Historical context has changed');
    await page.locator('#projectLibraryArrangements').screenshot({
      path: join(evidence, '19-restart-receipt.png'),
      style: 'nav.navbar { position: static !important; }'
    });
    const folders = (await ok(await request.get('/api/workspaces'))).folders;
    expect(folders).toHaveLength(summary.workspaces.after_activation);
    for (const [file, digest] of Object.entries(summary.source_hashes))
      expect(sha(await readFile(file))).toBe(digest);
    expect(planningCalls).toBe(0);
  } finally {
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
});

// Run by the harness with --music-upgrade-source, after the main test populated
// the Home on the installed (older) Music commit and saw planning refused: the
// owner upgrades the Home to the newer commit through the existing reviewed Home
// upgrade. The library, roots and projects stay; the next planning turn fits and
// carries the NEW skill and role guidance, never the old.
test('upgrade keeps the library and plans with the new guidance', async ({ page, request }) => {
  const upgradePath = process.env.ORI_MUSIC_UPGRADE_PATH || '';
  test.skip(
    process.env.ORI_ARRANGEMENT_UPGRADE_CHECK !== '1' ||
      !sandbox ||
      !modelPort ||
      !musicPath ||
      !upgradePath,
    'Run by ./scripts/reaper-demo.sh test --suite arrangement --music-upgrade-source DIR'
  );
  test.setTimeout(240_000);
  expect(basename(sandbox)).toMatch(/^ori-reaper-demo\./);
  expect(upgradePath.startsWith(sandbox)).toBe(true);
  const summary = JSON.parse(await readFile(join(evidence, '99-summary.json'), 'utf8'));
  const homeID = summary.home_id as string;
  const base = `/api/workspaces/${homeID}/assistant-program/library`;
  const manifestOf = async (root: string) =>
    JSON.parse(await readFile(join(root, '.ori-plugin/plugin.json'), 'utf8'));
  const guidanceOf = async (root: string) => {
    const manifest = await manifestOf(root);
    const skill = await readFile(join(root, 'skills/music-project-management/SKILL.md'), 'utf8');
    return {
      version: manifest.version as string,
      role: manifest.assistant_program_homes[0].roles.find(
        (role: any) => role.id === 'portfolio_manager'
      ).system_prompt as string,
      skill: skill.replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n/, '').trim()
    };
  };
  const before = await guidanceOf(musicPath);
  const after = await guidanceOf(upgradePath);
  expect(after.version).not.toBe(before.version);
  expect(after.skill).not.toBe(before.skill);
  const installedVersion = async () => {
    const body = await ok(await request.get('/api/plugins'));
    const plugins = Array.isArray(body) ? body : body.plugins || [];
    return plugins.find((plugin: any) => plugin.name === 'music-project-management')?.version;
  };
  expect(await installedVersion()).toBe(before.version);

  // The model fixture answers planning with a short clarification only.
  const planning: any[] = [];
  const server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET' && req.url?.endsWith('/models'))
      return res.end(JSON.stringify({ object: 'list', data: [{ id: MODEL, object: 'model' }] }));
    let raw = '';
    for await (const chunk of req) raw += chunk;
    const body = raw ? JSON.parse(raw) : {};
    const isPlanning = (body.tools || []).some(
      (tool: any) => tool?.function?.name === 'arrangement_propose'
    );
    if (isPlanning) planning.push(body);
    return res.end(
      JSON.stringify({
        id: 'upgrade-fixture',
        object: 'chat.completion',
        model: MODEL,
        choices: [
          {
            index: 0,
            finish_reason: 'stop',
            message: {
              role: 'assistant',
              content: isPlanning
                ? 'Which other songs belong with the client work?'
                : 'Fixture model ready.'
            }
          }
        ],
        usage: { prompt_tokens: 300, completion_tokens: 20, total_tokens: 320 }
      })
    );
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(modelPort, '127.0.0.1', () => resolve());
  });
  try {
    expect(summary.kind).toBe('old-guidance-refusal-before-upgrade');
    const workspaces = (await ok(await request.get('/api/workspaces'))).folders;
    const rootsBefore = await ok(await request.get(`${base}/roots`));
    const arrangementsBefore = (await ok(await request.get(`${base}/arrangements`))).arrangements;
    expect(arrangementsBefore).toHaveLength(0);

    // A developer replaces the installed export with the newer commit in place.
    for (const entry of await readdir(musicPath))
      await rm(join(musicPath, entry), { recursive: true, force: true });
    await cp(upgradePath, musicPath, { recursive: true });

    // The Plugins page hands off to the Home's own review of the new release.
    await page.goto('/plugins');
    const update = page.locator(
      '[data-plugin-action="update"][data-plugin-name="music-project-management"]'
    );
    await expect(update).toBeVisible({ timeout: 30_000 });
    await update.click();
    const toast = page.locator('.toast', { hasText: 'A Home uses this plugin' });
    await expect(toast).toBeVisible();
    await toast.getByRole('button', { name: 'Open Home' }).click();
    const dialog = page.locator('dialog.assistant-program-action-dialog');
    await expect(dialog.getByRole('heading', { level: 2 })).toHaveText(
      `Upgrade music-project-management to ${after.version}?`,
      { timeout: 30_000 }
    );
    await expect(dialog).toContainText(`move from ${before.version} to ${after.version}`);
    await expect(dialog).toContainText('Approved project library folders stay approved.');
    await dialog.screenshot({ path: join(evidence, '20-upgrade-review.png') });
    expect(await installedVersion()).toBe(before.version);
    await dialog.getByRole('button', { name: 'Upgrade Home' }).click();
    await expect(dialog).toBeHidden({ timeout: 60_000 });
    await expect(page.locator('#assistantProgramUpgrade')).toHaveAttribute(
      'data-state',
      'succeeded'
    );
    expect(await installedVersion()).toBe(after.version);

    // The library survives as it was: no workspace, root or record was added,
    // lost or rebuilt by the upgrade.
    expect((await ok(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspaces.length
    );
    const rootsAfter = await ok(await request.get(`${base}/roots`));
    expect(JSON.stringify(rootsAfter.roots ?? rootsAfter)).toBe(
      JSON.stringify(rootsBefore.roots ?? rootsBefore)
    );
    expect((await ok(await request.get(`${base}/arrangements`))).arrangements).toHaveLength(0);

    await page.goto(summary.library_path);
    const shelf = page.locator('#projectLibraryPanel');
    await expect(shelf.locator('#projectLibraryCount')).toHaveText(
      `${SONGS.length} of ${SONGS.length} projects`,
      { timeout: 30_000 }
    );
    const drawer = page.locator('#personalAssistantPanel');
    if (await drawer.isVisible()) {
      await page.locator('#personalAssistantClose').click();
      await expect(drawer).toBeHidden();
    }
    // Planning after the upgrade: either the owner must trust the changed
    // skill again (an explicit, recorded state), or it is already trusted.
    // Either way the next turn carries only the new guidance.
    const context = await request.get(`${base}/arrangements/context`);
    const contextBody = await context.json();
    const bindings = await ok(await request.get(`/api/workspaces/${homeID}/skill-bindings`));
    await writeFile(
      join(evidence, '21-context-after-upgrade.json'),
      JSON.stringify({ status: context.status(), body: contextBody, bindings }, null, 2),
      { mode: 0o600 }
    );
    if (!context.ok()) {
      expect(context.status()).toBe(409);
      await ok(
        await request.post(`/api/workspaces/${homeID}/skill-bindings`, {
          data: { skill_name: 'music-project-management', enabled: true, trusted: true, config: {} }
        })
      );
      await ok(await request.get(`${base}/arrangements/context`));
    }
    await page.reload();
    await expect(shelf.locator('#projectLibraryCount')).toHaveText(
      `${SONGS.length} of ${SONGS.length} projects`,
      { timeout: 30_000 }
    );
    if (await drawer.isVisible()) {
      await page.locator('#personalAssistantClose').click();
      await expect(drawer).toBeHidden();
    }
    const panel = page.locator('#projectLibraryArrangementPlanning');
    await panel.getByText('Organize library', { exact: true }).click();
    await expect(panel.getByText('Context checked.', { exact: false })).toBeVisible();
    await panel
      .getByRole('textbox', { name: 'Organizing goal' })
      .fill('Find the rest of the client songs');
    await panel
      .getByRole('checkbox', { name: 'Share this bounded metadata for one planning turn' })
      .check();
    // Nothing was saved before the upgrade, so the library is still version 1:
    // the owner separately accepts its storage upgrade.
    await panel
      .getByRole('checkbox', { name: /Saving a proposal or clarification upgrades/ })
      .check();
    const send = panel.getByRole('button', { name: 'Send organizing goal' });
    await expect(send).toBeEnabled();
    const planned = page.waitForResponse(r => r.url().endsWith('/arrangements/plan'));
    await send.click();
    const response = await planned;
    expect(response.ok(), `planning ${response.status()} ${await response.text()}`).toBeTruthy();
    expect(planning.length).toBeGreaterThan(0);
    // The scripted reply is a question: saved as clarification, never a proposal.
    const saved = (await ok(await request.get(`${base}/arrangements`))).arrangements;
    expect(saved).toHaveLength(1);
    expect(saved[0]).toMatchObject({ kind: 'clarification' });
    for (const body of planning) {
      const system = body.messages
        .filter((m: any) => m.role === 'system')
        .map((m: any) => m.content)
        .join('\n');
      expect(system).toContain(after.skill);
      expect(system).not.toContain(before.skill);
      expect(system).toContain(after.role);
      // The newer role prompt may extend the older one verbatim; only a prompt
      // the new one does not contain could be a stale leftover.
      if (!after.role.includes(before.role)) expect(system).not.toContain(before.role);
    }
    await panel.screenshot({
      path: join(evidence, '22-planning-after-upgrade.png'),
      style: 'nav.navbar { position: static !important; }'
    });
    expect((await ok(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspaces.length
    );
  } finally {
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
});
