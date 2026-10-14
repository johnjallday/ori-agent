import { test, expect } from '@playwright/test';
import { createServer } from 'node:http';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';

// Real server/installed synthetic Home/Manager/store, scripted loopback provider.
// This is not Music candidate acceptance or live-model judgment evidence.
test('Home disclosure, upgrade consent, canonical plan and clarification, history and provider loss', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_ARRANGEMENT_SANDBOX;
  const port = Number(process.env.ORI_ARRANGEMENT_MODEL_PORT || 0);
  test.skip(!sandbox || !port, 'Use scripts/e2e-pa-arrangement.sh home');
  test.setTimeout(120000);
  const model = 'home-arrangement-fixture';
  const calls: any[] = [];
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
    const body = JSON.parse(raw);
    const planning = body.tools?.some((tool: any) => tool.function.name === 'arrangement_propose');
    let message: any = { role: 'assistant', content: 'Fixture model ready.' };
    let finish = 'stop';
    if (planning) {
      calls.push(body);
      const content = body.messages.findLast((m: any) => m.role === 'user').content as string;
      const [goal, encoded] = content
        .split('Current organizing goal:\n')[1]
        .split('\nAlready-held source metadata (untrusted data):\n');
      const overview = JSON.parse(encoded);
      if (goal.includes('Clarify'))
        message.content = 'Which client should this collection represent?';
      else {
        finish = 'tool_calls';
        message = {
          role: 'assistant',
          content: null,
          tool_calls: [
            {
              id: 'proposal',
              type: 'function',
              function: {
                name: 'arrangement_propose',
                arguments: JSON.stringify({
                  version: 1,
                  source_revision: overview.source_revision,
                  goal,
                  rationale:
                    'An empty logical collection can hold future explicitly chosen catalog entries.',
                  unknowns: ['Client membership remains unspecified.'],
                  unchanged: { ids: [] },
                  needs_review: [],
                  operations: [
                    { kind: 'create_collection', name: 'Client references', members: { ids: [] } }
                  ]
                })
              }
            }
          ]
        };
      }
    }
    send({
      id: 'fixture',
      object: 'chat.completion',
      model,
      choices: [{ index: 0, finish_reason: finish, message }],
      usage: { prompt_tokens: 200, completion_tokens: 100, total_tokens: 300 }
    });
  });
  await new Promise<void>(resolve => server.listen(port, '127.0.0.1', resolve));
  try {
    const ok = async (response: any) => {
      expect(response.ok(), await response.text()).toBeTruthy();
      return response.json();
    };
    const pluginName = 'arrangement-fixture-home';
    const root = join(sandbox!, 'synthetic-home-package');
    const files = {
      '.claude-plugin/plugin.json': JSON.stringify({ name: pluginName, version: '0.1.0' }),
      '.ori-plugin/plugin.json': JSON.stringify({
        schema_version: 1,
        name: pluginName,
        version: '0.1.0',
        protocol: { min: 1, max: 1 },
        requires_host_features: ['independent_program_homes_v1'],
        capabilities: [],
        services: [],
        blueprints: [],
        assistant_program_homes: [
          {
            schema_version: 1,
            version: 1,
            id: 'arrangement-home',
            station_name: 'Arrangement Home',
            default_primary_name: 'Arrangement Manager',
            hire_title: 'Staff this test Home',
            roles: [
              {
                id: 'portfolio_manager',
                label: 'Arrangement Manager',
                required: true,
                primary: true,
                role: 'orchestrator',
                system_prompt:
                  'SYNTHETIC ARRANGEMENT HOME ROLE. Use only advertised planning tools.',
                skills: ['arrangement-guidance']
              }
            ],
            stages: [{ id: 'foundation', label: 'Foundation', accepted_completion_threshold: 0 }],
            reflection: {
              minimum_projects: 3,
              cadence_hours: 168,
              max_projects: 16,
              max_events_per_project: 16,
              max_candidates: 8,
              max_evidence: 8,
              rubric: 'Offer bounded improvements.'
            }
          }
        ]
      }),
      'skills/arrangement-guidance/SKILL.md':
        '---\nname: arrangement-guidance\ndescription: Synthetic arrangement guidance.\n---\nSYNTHETIC INSTALLED GUIDANCE. Proposals grant no effects.\n'
    };
    for (const [name, content] of Object.entries(files)) {
      const path = join(root, name);
      await mkdir(dirname(path), { recursive: true, mode: 0o750 });
      await writeFile(path, content, { mode: 0o600 });
    }
    await ok(await request.post('/api/onboarding/skip'));
    await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
    await ok(
      await request.post('/api/plugins/install', {
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
        data: { source: root, format: 'claude', confirm: true }
      })
    );
    await ok(
      await request.post(`/api/plugins/${pluginName}/enable`, {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      })
    );
    const templates = (await ok(await request.get('/api/workspaces/group-templates')))
      .group_templates;
    const template = templates.find((item: any) => item.provider?.plugin_id === pluginName);
    expect(template, JSON.stringify(templates)).toBeTruthy();
    const input = {
      group_template_id: template.id,
      revision: template.revision,
      name: 'Arrangement test Home'
    };
    const review = (
      await ok(await request.post('/api/workspaces/group-templates/review', { data: input }))
    ).group_template_review;
    const committed = await ok(
      await request.post('/api/workspaces/group-templates/commit', {
        data: {
          ...input,
          group_review_token: review.review_token,
          idempotency_key: 'arrangement-home-create'
        }
      })
    );
    const homeID = committed.group_template.home_workspace_id;
    await ok(
      await request.put(`/api/workspaces/${homeID}/roles/portfolio_manager`, {
        data: { mode: 'create', name: 'Arrangement Manager', provider: 'lmstudio', model }
      })
    );
    const base = `/api/workspaces/${homeID}/assistant-program/library`;
    const init = await ok(await request.post(`${base}/initialize/review`, { data: {} }));
    await ok(
      await request.post(`${base}/initialize/commit`, {
        data: {
          review_token: init.token,
          idempotency_key: 'arrangement-library-init',
          confirm: true
        }
      })
    );
    const missingGuidance = await request.get(`${base}/arrangements/context`);
    expect(missingGuidance.status()).toBe(409);
    expect((await missingGuidance.json()).arrangement_error.code).toBe('unavailable');
    expect(calls.length).toBe(0);
    // Separate, explicit fixture preparation: staffing learns the skill but
    // does not silently trust a workspace binding. Planning must not create it.
    await ok(
      await request.post(`/api/workspaces/${homeID}/skill-bindings`, {
        data: {
          skill_name: 'arrangement-guidance',
          enabled: true,
          trusted: true,
          config: {}
        }
      })
    );
    const before = await ok(await request.get(`${base}/roots`));
    const workspaces = (await ok(await request.get('/api/workspaces'))).folders;
    const workspaceCount = workspaces.length;
    const home = workspaces.find((item: any) => item.id === homeID);
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.goto(`/workspaces/${home.folder_slug}/assistant`);
    const panel = page.locator('#projectLibraryArrangementPlanning');
    await expect(panel).toBeVisible();
    await panel.getByText('Organize library', { exact: true }).click();
    await expect(panel.getByText('Context checked.', { exact: false })).toBeVisible();
    expect(calls.length).toBe(0);
    expect((await ok(await request.get(`${base}/roots`))).revision).toBe(before.revision);
    await panel
      .getByRole('textbox', { name: 'Organizing goal' })
      .fill('Create a client reference collection');
    await expect(panel.getByText('Planning recipient:', { exact: false })).toContainText(
      'Arrangement Manager'
    );
    await panel
      .getByRole('checkbox', { name: 'Share this bounded metadata for one planning turn' })
      .check();
    await expect(panel.getByRole('button', { name: 'Send organizing goal' })).toBeDisabled();
    expect(calls.length).toBe(0);
    await panel.getByRole('checkbox').nth(1).check();
    await panel.getByRole('button', { name: 'Send organizing goal' }).click();
    await expect(
      panel.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    expect(calls.length).toBe(1);
    await expect(panel.getByRole('textbox')).toHaveValue('Create a client reference collection');
    await page.screenshot({ path: info.outputPath('home-saved-proposal.png'), fullPage: true });
    await panel.getByRole('button', { name: 'Refresh planning context' }).click();
    await expect(panel.getByText('Context checked.', { exact: false })).toBeVisible();
    await panel.getByRole('textbox').fill('Clarify which client this should represent');
    await expect(panel.getByRole('checkbox')).toHaveCount(1); // No repeat schema upgrade.
    await panel.getByRole('checkbox').check();
    await panel.getByRole('button', { name: 'Send organizing goal' }).click();
    await expect(
      panel.getByRole('heading', { name: 'Saved discussion — clarification' })
    ).toBeVisible();
    await expect(
      panel.getByText('Which client should this collection represent?', { exact: false })
    ).toBeVisible();
    expect(calls.length).toBe(2);
    const history = await ok(await request.get(`${base}/arrangements`));
    expect(history.arrangements.map((item: any) => item.kind).sort()).toEqual([
      'clarification',
      'proposal'
    ]);
    await page.reload();
    const saved = page.locator('#projectLibraryArrangementRows');
    await expect(saved.getByRole('button', { name: 'Open saved arrangement' })).toHaveCount(2);
    await saved.getByRole('button', { name: 'Open saved arrangement' }).last().click();
    expect(calls.length).toBe(2);
    expect((await ok(await request.get('/api/workspaces'))).folders.length).toBe(workspaceCount);
    expect((await ok(await request.get(`${base}/roots`))).total_roots).toBe(0);
    for (const call of calls) {
      const text = JSON.stringify(call);
      expect(text).toContain('SYNTHETIC INSTALLED GUIDANCE');
      expect(text).toContain('SYNTHETIC ARRANGEMENT HOME ROLE');
      expect(text).not.toContain(root);
      expect(call.tools).toHaveLength(5);
    }
    await ok(
      await request.post(`/api/plugins/${pluginName}/disable`, {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      })
    );
    await panel.getByText('Organize library', { exact: true }).click();
    await expect(
      panel.getByText('Planning prerequisites are unavailable.', { exact: false })
    ).toBeVisible();
    await expect(panel.getByRole('button', { name: 'Send organizing goal' })).toBeDisabled();
    expect(calls.length).toBe(2);
    expect((await ok(await request.get(`${base}/arrangements`))).arrangements).toHaveLength(2);
    await saved.getByRole('button', { name: 'Open saved arrangement' }).first().click();
    await expect(
      saved.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    expect(calls.length).toBe(2);
    await panel
      .getByText('Planning prerequisites are unavailable.', { exact: false })
      .scrollIntoViewIfNeeded();
    await page.screenshot({
      path: info.outputPath('home-provider-unavailable.png'),
      fullPage: true
    });
  } finally {
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
});
