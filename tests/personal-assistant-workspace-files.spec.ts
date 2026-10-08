import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Production browser, Route/Ask, Ollama adapter, brokered file readers, the
// real folder store and the real filesystem under `wt demo`. The loopback
// provider is a deterministic stand-in for a tool-using model: it walks
// listing -> folder -> file as the readers allow and quotes only what a reader
// returned. It also audits its own input, so this run can show that a file in a
// folder that was only attached never reached the model. Not vendor-model
// evidence; nothing here opens REAPER, decodes audio or edits a file.
test('wt demo: linked files and attachments are read with coverage; an attached folder stays metadata', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_FILES_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use python3 scripts/assistant-workspace-demo.py --files');
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });

  // Markers the provider must never be sent.
  const unlinked = 'UNLINKED_FOLDER_BODY_MUST_NOT_REACH_THE_MODEL';
  const hidden = 'HIDDEN_FILE_BODY_MUST_NOT_REACH_THE_MODEL';
  const foreign = 'OTHER_WORKSPACE_BODY_MUST_NOT_REACH_THE_MODEL';
  await writeFile(join(provider!, 'sentinels.json'), JSON.stringify([unlinked, hidden, foreign]), {
    mode: 0o600
  });

  const assets = join(sandbox!, 'Music', 'Album-1 assets');
  const otherAssets = join(sandbox!, 'Music', 'Other assets');
  const attachedOnly = join(sandbox!, 'Documents', 'Album-5 unlinked');
  for (const dir of [join(assets, 'lyrics'), join(otherAssets, 'lyrics'), attachedOnly])
    await mkdir(dir, { recursive: true, mode: 0o750 });
  const bridge = 'The bridge is in D minor.\nA second line the fixture does not quote.';
  const longLog = 'Session log line with a takt of ninety-six.\n'.repeat(2600); // ~114,000 characters
  await writeFile(join(assets, 'lyrics', 'bridge.txt'), bridge, { mode: 0o600 });
  await writeFile(join(assets, 'long-log.txt'), longLog, { mode: 0o600 });
  await writeFile(join(assets, '.env'), hidden, { mode: 0o600 });
  await writeFile(join(otherAssets, 'lyrics', 'bridge.txt'), foreign, { mode: 0o600 });
  await writeFile(join(attachedOnly, 'notes.txt'), unlinked, { mode: 0o600 });

  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'workspace-files-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'workspace-files-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  const workspaces = async () => (await (await request.get('/api/workspaces')).json()).folders;
  const create = async (name: string) => {
    const response = await request.post('/api/workspaces', { data: { name } });
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await workspaces()).find((row: any) => row.name === name);
  };
  const link = async (workspaceId: string, name: string, path: string) => {
    const response = await request.post(`/api/workspaces/${workspaceId}/directories`, {
      data: { name, path }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).directory.id as string;
  };
  const project = await create('Album-1 files fixture');
  const other = await create('Other files fixture');
  const directoryId = await link(project.id, 'Album-1 assets', assets);
  await link(other.id, 'Album-1 assets', otherAssets);
  const artwork = 'Artwork is due on the 12th.';
  const uploaded = await request.post(`/api/workspaces/${project.id}/files`, {
    multipart: {
      file: { name: 'artwork.md', mimeType: 'text/markdown', buffer: Buffer.from(artwork) }
    }
  });
  expect(uploaded.ok(), await uploaded.text()).toBeTruthy();
  const before = (await workspaces()).length;

  const label = page.locator('#personalAssistantWorkspaceContext');
  const answers = page.locator('#homeAssistantConversation [data-message-role="assistant"]');
  const openDrawer = async () => {
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
  };
  const send = async (prompt: string) => {
    await page.locator('#personalAssistantInput').fill(prompt);
    const response = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const body = await (await response).json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    return body;
  };
  const shot = (name: string) =>
    page.locator('#personalAssistantPanel').screenshot({ path: join(evidence, name) });
  const noPaths = (value: unknown) => {
    const text = JSON.stringify(value);
    for (const location of [sandbox!, assets, '/Music/', '/Documents/'])
      expect(text, `an absolute location leaked: ${location}`).not.toContain(location);
  };

  await page.goto(`/workspaces/${project.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toHaveText('Context · Project: Album-1 files fixture');

  // 1. A greeting opens no file and claims no source.
  const greeting = await send('Hello there');
  expect(greeting.conversation.stored).toBe(true);
  expect(greeting.workspace_context.sources).toBeUndefined();

  // 2. A linked file and an attachment are read and attributed to the workspace
  // source they came from. Names and places are shown; absolute paths are not.
  const linkedFolders = async () =>
    (await (await request.get(`/api/workspaces/${project.id}/directories`)).json()).directories
      .map((directory: any) => directory.id)
      .sort();
  const linkedAtStart = await linkedFolders();
  expect(linkedAtStart).toContain(directoryId);
  const summary = await send(
    'Summarize the lyrics file in Album-1 assets and the artwork attachment'
  );
  expect(summary.conversation.stored).toBe(true);
  expect(summary.response).toContain(
    'From Linked folder “Album-1 assets” · lyrics/bridge.txt: “The bridge is in D minor.” [S1]'
  );
  expect(summary.response).toContain(`From Workspace attachment: “${artwork}” [S2]`);
  expect(summary.workspace_context.sources).toHaveLength(2);
  expect(summary.workspace_context.sources[0]).toMatchObject({
    key: 'S1',
    kind: 'file',
    label: 'bridge.txt',
    detail: 'Linked folder “Album-1 assets” · lyrics/bridge.txt',
    workspace: 'Album-1 files fixture',
    coverage: 'full',
    cited: true
  });
  expect(summary.workspace_context.sources[1]).toMatchObject({
    key: 'S2',
    kind: 'attachment',
    label: 'artwork.md',
    detail: 'Workspace attachment',
    coverage: 'full'
  });
  expect(summary.workspace_context.sources[0].href).toBeUndefined();
  noPaths(summary);
  const sources = answers.last().locator('.personal-assistant-message__sources');
  await sources.locator('summary').focus();
  await page.keyboard.press('Enter');
  await expect(sources).toHaveAttribute('open', '');
  await expect(sources).toContainText(
    'File: bridge.txt · Linked folder “Album-1 assets” · lyrics/bridge.txt · Album-1 files fixture · read in full'
  );
  await expect(sources).toContainText('Attachment: artwork.md · Workspace attachment');
  await expect(sources.locator('a')).toHaveCount(0);
  await sources.scrollIntoViewIfNeeded();
  await shot('files-sources.png');

  // 3. A long file is read in parts within the turn's budget. The reply and the
  // source both say that only part of it was read.
  const partial = await send('Read the long log in Album-1 assets');
  const log = partial.workspace_context.sources[0];
  expect(partial.workspace_context.sources).toHaveLength(1);
  expect(log).toMatchObject({ kind: 'file', label: 'long-log.txt', coverage: 'partial' });
  expect(log.start ?? 0).toBe(0);
  expect(log.end).toBeGreaterThan(40000);
  expect(log.end).toBeLessThan(log.total);
  expect(log.total).toBe(longLog.length);
  expect(partial.response).toContain(
    `only characters 1–${log.end} of ${log.total} were read, not the whole file`
  );
  await answers.last().locator('.personal-assistant-message__sources summary').click();
  await expect(answers.last().locator('.personal-assistant-message__sources')).toContainText(
    `part read (characters 1–${log.end} of ${log.total})`
  );
  await answers.last().scrollIntoViewIfNeeded();
  await shot('files-partial-read.png');

  // 4. A folder that is only attached stays metadata, even on a workspace whose
  // own files are readable: the answer comes from the workspace's linked file.
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await expect(page.locator('#personalAssistantFolderPreview')).toBeVisible();
  const attached = await send(
    'Summarize the lyrics file in Album-1 assets and the artwork attachment'
  );
  expect(attached.conversation.stored).toBe(true);
  expect(attached.folder_context.observation.folder).toBe('Documents');
  expect(attached.workspace_context.sources.map((source: any) => source.detail)).toEqual([
    'Linked folder “Album-1 assets” · lyrics/bridge.txt',
    'Workspace attachment'
  ]);
  expect(attached.response).not.toContain(unlinked);
  // Attaching and asking linked nothing: the workspace has the same folders.
  expect(await linkedFolders()).toEqual(linkedAtStart);

  // 5. Where there is no workspace file to read, asking for the attached
  // folder's contents is refused by Ori itself, with the real ways forward.
  await page.goto('/settings');
  await openDrawer();
  await expect(label).toHaveText('Context · App-wide');
  const refused = await send('Summarize these documents');
  expect(refused.response).toContain('File contents have not been read.');
  expect(refused.response).toContain('Picking it is not permission to read its files.');
  expect(refused.response).toContain('link the folder to a workspace through a reviewed setup');
  expect(refused.workspace_context?.sources).toBeUndefined();
  await answers.last().scrollIntoViewIfNeeded();
  await shot('files-unlinked-refusal.png');

  // 6. Access removed during the conversation stops the next read. Earlier
  // replies keep what they read then; the user's files are untouched.
  await page.goto(`/workspaces/${project.folder_slug}/canvas`);
  await openDrawer();
  const removed = await request.delete(`/api/workspaces/${project.id}/directories/${directoryId}`);
  expect(removed.ok(), await removed.text()).toBeTruthy();
  const afterRemoval = await send('Read the long log in Album-1 assets');
  expect(afterRemoval.response).toContain('I could not read a file for that');
  expect(afterRemoval.response).toContain('I am not describing a file I did not read');
  expect(afterRemoval.workspace_context.sources).toBeUndefined();
  await answers.last().scrollIntoViewIfNeeded();
  await shot('files-revoked-access.png');
  await page.reload();
  await openDrawer();
  await expect(
    page.locator('#homeAssistantConversation .personal-assistant-message__sources')
  ).toHaveCount(3);
  expect(await readFile(join(assets, 'lyrics', 'bridge.txt'), 'utf8')).toBe(bridge);
  expect(await readFile(join(attachedOnly, 'notes.txt'), 'utf8')).toBe(unlinked);
  expect((await workspaces()).length).toBe(before);

  // The provider's own record of its input: it was consulted, and none of the
  // watched bodies ever reached it.
  const audit = JSON.parse(await readFile(join(provider!, 'provider-audit.json'), 'utf8'));
  expect(audit.requests).toBeGreaterThan(8);
  expect(audit.hits).toEqual([]);
  const saved = await (
    await request.get(`/api/home-assistant/conversations/${summary.conversation.id}`)
  ).json();
  noPaths(saved);
  expect(JSON.stringify(saved)).not.toContain(unlinked);

  await writeFile(
    join(evidence, 'files-demo.json'),
    JSON.stringify(
      {
        evidence:
          'wt demo real host, folder store and filesystem + production Ollama adapter + loopback deterministic tool-using provider; no vendor model, audio decoding or live application',
        linkedFileAndAttachment: summary.workspace_context.sources.map((source: any) => ({
          kind: source.kind,
          label: source.label,
          where: source.detail,
          coverage: source.coverage
        })),
        partialRead: {
          start: log.start ?? 0,
          end: log.end,
          total: log.total,
          coverage: log.coverage
        },
        attachedFolder: 'metadata only; answer attributed to the workspace linked file',
        noWorkspaceFile: 'refused by the host before any model call',
        accessRemoved: 'next read found nothing readable; earlier sources kept as history',
        providerAudit: {
          requests: audit.requests,
          watchedBodiesThatReachedTheProvider: audit.hits.length,
          watched: [
            'file in the attached-only folder',
            'hidden file in the linked folder',
            'same-named file in another workspace'
          ]
        },
        absolutePathsExposed: false,
        filesChanged: false
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
