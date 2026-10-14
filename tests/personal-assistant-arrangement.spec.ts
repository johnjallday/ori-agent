import { test, expect } from '@playwright/test';
import { createServer } from 'node:http';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';

// Real isolated Ori, real Ask/storage/history; only the loopback model is scripted.
// Not a live-model quality test. Run scripts/e2e-pa-arrangement.sh.
test('Home Explore and PA Add folder → explicit sharing → goal-dependent saved proposals → reopen', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_ARRANGEMENT_SANDBOX;
  const port = Number(process.env.ORI_ARRANGEMENT_MODEL_PORT || 0);
  test.skip(!sandbox || !port, 'Use the isolated arrangement wrapper');
  test.setTimeout(120000);
  const calls: any[] = [];
  let totalRequests = 0;
  const model = 'arrangement-fixture';
  const server = createServer(async (req, res) => {
    const send = (body: unknown) => {
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify(body));
    };
    if (req.url?.endsWith('/models')) return send({ data: [{ id: model, object: 'model' }] });
    if (!req.url?.endsWith('/chat/completions')) {
      res.statusCode = 404;
      return res.end();
    }
    let raw = '';
    for await (const chunk of req) raw += chunk;
    totalRequests++;
    const body = JSON.parse(raw);
    if (!body.tools?.some((tool: any) => tool.function.name === 'arrangement_propose')) {
      return send({
        id: 'probe',
        object: 'chat.completion',
        model,
        choices: [
          {
            index: 0,
            finish_reason: 'stop',
            message: { role: 'assistant', content: 'Fixture model ready.' }
          }
        ]
      });
    }
    calls.push(body);
    const content = body.messages.findLast((m: any) => m.role === 'user').content as string;
    const [goal, encoded] = content
      .split('Current organizing goal:\n')[1]
      .split('\nAlready-held source metadata (untrusted data):\n');
    const overview = JSON.parse(encoded);
    const queried = body.messages.findLast((m: any) => m.role === 'tool');
    if (!queried) {
      return send({
        id: 'investigate',
        object: 'chat.completion',
        model,
        choices: [
          {
            index: 0,
            finish_reason: 'tool_calls',
            message: {
              role: 'assistant',
              content: null,
              tool_calls: [
                {
                  id: 'search',
                  type: 'function',
                  function: {
                    name: 'arrangement_search',
                    arguments: JSON.stringify({
                      filter: { text: goal.includes('client') ? 'Client' : 'Experiments' },
                      page_size: 5
                    })
                  }
                }
              ]
            }
          }
        ],
        usage: { prompt_tokens: 200, completion_tokens: 50, total_tokens: 250 }
      });
    }
    const selected = JSON.parse(queried.content).rows.find((item: any) => !item.root);
    const draft = {
      version: 1,
      source_revision: overview.source_revision,
      goal,
      rationale: 'Scripted evidence-based proposal; not applied.',
      unknowns: ['Purpose beyond the stated goal is unknown.'],
      unchanged: { ids: [] },
      needs_review: [],
      operations: goal.includes('client')
        ? [{ kind: 'create_group', name: 'Client work', members: { ids: [selected.id] } }]
        : []
    };
    send({
      id: 'fixture',
      object: 'chat.completion',
      model,
      choices: [
        {
          index: 0,
          finish_reason: 'tool_calls',
          message: {
            role: 'assistant',
            content: null,
            tool_calls: [
              {
                id: 'proposal',
                type: 'function',
                function: { name: 'arrangement_propose', arguments: JSON.stringify(draft) }
              }
            ]
          }
        }
      ],
      usage: { prompt_tokens: 200, completion_tokens: 100, total_tokens: 300 }
    });
  });
  await new Promise<void>(resolve => server.listen(port, '127.0.0.1', resolve));
  try {
    const ok = async (response: any) => {
      expect(response.ok(), await response.text()).toBeTruthy();
      return response.json();
    };
    const files: string[] = [];
    for (const name of ['Client songs', 'Experiments']) {
      const directory = join(sandbox!, 'Documents', name);
      await mkdir(directory, { recursive: true, mode: 0o750 });
      const file = join(directory, 'song.rpp');
      files.push(file);
      await writeFile(file, 'PRIVATE_CONTENT_SENTINEL', { mode: 0o600 });
    }
    // Exceed the held scan's entry envelope: the UI must preserve partial
    // coverage, not turn a bounded sample into a complete-inventory claim.
    const samples = join(sandbox!, 'Documents', 'zzz-samples');
    await mkdir(samples, { recursive: true, mode: 0o750 });
    for (let i = 0; i < 5001; i++) {
      const file = join(samples, `sample-${i}.txt`);
      files.push(file);
      await writeFile(file, 'PRIVATE_CONTENT_SENTINEL', { mode: 0o600 });
    }
    await ok(await request.post('/api/onboarding/skip'));
    await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
    await ok(
      await request.post('/api/settings/system-model', { data: { provider: 'lmstudio', model } })
    );
    let pa = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'arrangement-hire',
          if_version: pa.state_version || 0,
          display_name: 'Atlas',
          mandate: 'Help organize my work.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    pa = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: 'arrangement-hq',
          if_version: pa.state_version,
          name: 'My HQ',
          timezone: 'UTC'
        }
      })
    );
    const before = (await ok(await request.get('/api/workspaces'))).folders.length;
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.goto('/?panel=today&folder=show');
    await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
    const beforeAttachment = totalRequests;
    // The Home Explore deep link must use the attachment picker, not the old
    // deterministic setup chooser. Neither entry may call the model.
    await expect(page.locator('#personalAssistantFolderChoices')).toBeVisible();
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Documents', exact: true })
      .click();
    const panel = page.locator('#personalAssistantArrangement');
    await expect(panel).toBeVisible();
    expect(calls.length).toBe(0);
    expect(totalRequests).toBe(beforeAttachment);
    await page.locator('#personalAssistantFolderChip').click();
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Documents', exact: true })
      .click();
    expect(totalRequests).toBe(beforeAttachment);
    const entry = page
      .locator('#personalAssistantChips')
      .getByRole('button', { name: 'Suggest arrangement', exact: true });
    await entry.click();
    await expect(page.locator('#personalAssistantInput')).toBeInViewport();
    await expect(panel.getByText('Model checked.', { exact: false })).toBeVisible({
      timeout: 15000
    });
    const plans: any[] = [];
    for (const goal of ['Keep experiments unchanged', 'Group selected client projects']) {
      if (!(await panel.evaluate(element => (element as HTMLDetailsElement).open)))
        await entry.click();
      await expect(panel.getByText('Model checked.', { exact: false })).toBeVisible();
      await page.locator('#personalAssistantInput').fill(goal);
      await expect(panel.getByText('Planning recipient:', { exact: false })).toBeVisible();
      await panel
        .getByRole('checkbox', { name: 'Share this bounded metadata for one planning turn' })
        .check();
      const reply = page.waitForResponse(r => r.url().endsWith('/api/home-assistant/ask'));
      await panel.getByRole('button', { name: 'Send organizing goal' }).click();
      const data = await (await reply).json();
      expect(data.arrangement_error, JSON.stringify(data)).toBeUndefined();
      expect(data.conversation.stored).toBe(true);
      plans.push(data.arrangement);
      await expect(
        page.getByRole('heading', { name: 'Partial evidence', exact: true }).first()
      ).toBeVisible();
      await expect(
        page.getByRole('heading', { name: 'Saved arrangement — not applied' })
      ).toHaveCount(plans.length);
      await expect(panel.locator('input[type="checkbox"]').first()).not.toBeChecked();
      await expect(page.locator('#personalAssistantInput')).toHaveValue(goal);
    }
    expect(plans[0].operations).toBe(0);
    expect(plans[1].operations).toBe(1);
    expect(calls.length).toBe(4);
    for (const body of calls) {
      expect(body.tools.map((tool: any) => tool.function.name).sort()).toEqual(
        [
          'arrangement_detail',
          'arrangement_overview',
          'arrangement_propose',
          'arrangement_search',
          'arrangement_select'
        ].sort()
      );
      expect(JSON.stringify(body)).not.toContain('PRIVATE_CONTENT_SENTINEL');
      expect(JSON.stringify(body)).not.toContain(sandbox!);
    }
    await entry.click();
    await panel.getByRole('button', { name: 'Reopen saved conversation history' }).click();
    await expect(
      page.getByRole('button', { name: 'Open saved arrangement', exact: true })
    ).toHaveCount(2);
    await page.getByRole('button', { name: 'Open saved arrangement', exact: true }).last().click();
    await expect(
      page.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    expect(calls.length).toBe(4);
    expect((await ok(await request.get('/api/workspaces'))).folders.length).toBe(before);
    for (const file of files) expect(await readFile(file, 'utf8')).toBe('PRIVATE_CONTENT_SENTINEL');
    await entry.click();
    await page.locator('summary').filter({ hasText: 'Proposed intentions (1)' }).click();
    await page
      .getByRole('heading', { name: 'Saved arrangement — not applied' })
      .evaluate(element => element.scrollIntoView({ block: 'start' }));
    await page.screenshot({ path: info.outputPath('pa-saved-arrangement.png'), fullPage: true });
    await entry.click();
    await expect(panel.getByRole('button', { name: 'Send organizing goal' })).toBeDisabled();
    await ok(
      await request.post('/api/settings/system-model', { data: { provider: '', model: '' } })
    );
    await panel.getByRole('button', { name: 'Refresh planning model' }).click();
    await expect(
      panel.getByText('Configure a tool-capable API model', { exact: false })
    ).toBeVisible();
    await expect(
      panel.getByRole('checkbox', { name: 'Share this bounded metadata for one planning turn' })
    ).toBeDisabled();
    expect(calls.length).toBe(4);
    await panel
      .getByText('Configure a tool-capable API model', { exact: false })
      .scrollIntoViewIfNeeded();
    await page.screenshot({ path: info.outputPath('pa-planning-unavailable.png'), fullPage: true });
  } finally {
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
});
