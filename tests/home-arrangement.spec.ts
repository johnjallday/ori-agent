import { test, expect } from '@playwright/test';
import { createServer } from 'node:http';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { dirname, join } from 'node:path';

// Real server/installed Home/Manager/store, scripted loopback provider.
// Default uses a synthetic package; music mode exports an exact clean candidate.
// Neither mode is live-model judgment or populated-catalog scale evidence.
test('Home disclosure, upgrade consent, canonical plan and clarification, history and provider loss', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_ARRANGEMENT_SANDBOX;
  const port = Number(process.env.ORI_ARRANGEMENT_MODEL_PORT || 0);
  test.skip(!sandbox || !port, 'Use scripts/e2e-pa-arrangement.sh home');
  test.setTimeout(120000);
  const musicSource = process.env.ORI_ARRANGEMENT_MUSIC_SOURCE;
  const model = 'home-arrangement-fixture';
  const calls: any[] = [];
  const attachJSON = async (name: string, value: unknown) => {
    const path = info.outputPath(`${name}.json`);
    await mkdir(dirname(path), { recursive: true, mode: 0o750 });
    await writeFile(path, JSON.stringify(value, null, 2), { mode: 0o600 });
    await info.attach(name, { path, contentType: 'application/json' });
  };
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
    const pluginName = musicSource ? 'music-project-management' : 'arrangement-fixture-home';
    const skillName = musicSource ? 'music-project-management' : 'arrangement-guidance';
    const root = join(sandbox!, musicSource ? 'candidate-home-package' : 'synthetic-home-package');
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
    if (musicSource) {
      const git = (...args: string[]) =>
        execFileSync('git', ['-C', musicSource, ...args], { encoding: 'utf8' }).trim();
      expect(git('status', '--porcelain', '--untracked-files=all')).toBe('');
      const revision = process.env.ORI_ARRANGEMENT_MUSIC_REVISION!;
      const tree = process.env.ORI_ARRANGEMENT_MUSIC_TREE!;
      expect(revision).toMatch(/^[a-f0-9]{40}$/);
      expect(git('rev-parse', 'HEAD')).toBe(revision);
      expect(git('rev-parse', `${revision}^{tree}`)).toBe(tree);
      const archive = execFileSync('git', ['-C', musicSource, 'archive', '--format=tar', revision]);
      const archivePath = join(sandbox!, 'music-candidate.tar');
      await writeFile(archivePath, archive, { mode: 0o600 });
      await mkdir(root, { recursive: true, mode: 0o750 });
      execFileSync('tar', ['-xf', archivePath, '-C', root]);
      const validation = execFileSync(
        'python3',
        [join(root, 'scripts/validate-package.py'), root],
        { encoding: 'utf8' }
      );
      await attachJSON('music-candidate-identity', {
        revision,
        tree,
        archive_sha256: createHash('sha256').update(archive).digest('hex'),
        validation
      });
    } else {
      for (const [name, content] of Object.entries(files)) {
        const path = join(root, name);
        await mkdir(dirname(path), { recursive: true, mode: 0o750 });
        await writeFile(path, content, { mode: 0o600 });
      }
    }
    const manifest = JSON.parse(await readFile(join(root, '.ori-plugin/plugin.json'), 'utf8'));
    expect(manifest.name).toBe(pluginName);
    const expectedRole = manifest.assistant_program_homes[0].roles.find(
      (role: any) => role.id === 'portfolio_manager'
    ).system_prompt;
    const skillBytes = await readFile(join(root, `skills/${skillName}/SKILL.md`), 'utf8');
    const expectedSkill = skillBytes.replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n/, '').trim();
    expect(expectedSkill.length).toBeGreaterThan(0);
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
          skill_name: skillName,
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
    const plannedResponse = page.waitForResponse(response =>
      response.url().endsWith('/arrangements/plan')
    );
    await panel.getByRole('button', { name: 'Send organizing goal' }).click();
    const planned = await plannedResponse;
    expect(
      planned.ok(),
      `Planning calls: ${calls.length}; response: ${await planned.text()}`
    ).toBeTruthy();
    await expect(
      panel.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    expect(calls.length).toBe(1);
    await expect(panel.getByRole('textbox')).toHaveValue('Create a client reference collection');
    await page.screenshot({ path: info.outputPath('home-saved-proposal.png'), fullPage: true });
    const reviewRows = page.locator('#projectLibraryArrangementRows');
    await reviewRows.getByRole('button', { name: 'Revise with assistant', exact: true }).click();
    await expect(panel.getByText('Context checked.', { exact: false })).toBeVisible();
    await panel.getByRole('textbox').fill('Clarify the current client collection before revising');
    await panel.getByRole('checkbox').check();
    await panel.getByRole('button', { name: 'Send organizing goal' }).click();
    await expect(
      panel.getByRole('heading', { name: 'Saved revision question — prior proposal retained' })
    ).toBeVisible();
    await expect(
      reviewRows.getByRole('button', { name: 'Review collection metadata', exact: true })
    ).toHaveCount(0);
    expect((await ok(await request.get(`${base}/arrangements`))).arrangements).toHaveLength(1);
    await panel.screenshot({ path: info.outputPath('home-revision-question.png') });
    await panel.getByRole('button', { name: 'Refresh planning context' }).click();
    await expect(panel.getByText('Context checked.', { exact: false })).toBeVisible();
    await panel.getByRole('textbox').fill('Keep the current client reference collection');
    await panel.getByRole('checkbox').check();
    await panel.getByRole('button', { name: 'Send organizing goal' }).click();
    await expect(
      panel.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    expect(calls.length).toBe(3);
    await expect(panel).toContainText('Model revision 2 → 3:');
    await panel.screenshot({ path: info.outputPath('home-conversational-revision.png') });
    expect(JSON.stringify(calls[2])).toContain('pending_question');
    expect((await ok(await request.get(`${base}/arrangements`))).arrangements).toHaveLength(1);
    await reviewRows
      .getByRole('button', { name: 'Review collection metadata', exact: true })
      .click();
    const metadataReview = reviewRows.getByRole('region', {
      name: 'Review exact collection metadata'
    });
    await expect(
      metadataReview.getByRole('heading', { name: 'Review exact collection metadata' })
    ).toBeVisible();
    await expect(metadataReview).toContainText('0–0 of 0 exact members');
    expect(calls.length).toBe(3);
    await metadataReview.getByText('Correct this proposal', { exact: true }).click();
    const candidatesResponse = page.waitForResponse(r => r.url().includes('/candidates?'));
    await metadataReview.getByRole('button', { name: 'Find saved members', exact: true }).click();
    const candidates = await ok(await candidatesResponse);
    expect(candidates.total).toBe(0);
    expect(candidates.members).toEqual([]);
    await expect(metadataReview).toContainText('0–0 of 0 eligible saved entries');
    expect(calls.length).toBe(3);
    await metadataReview
      .getByLabel('Proposed collection name', { exact: true })
      .fill('Client deliveries');
    await expect(metadataReview).toContainText('Name: Client references → Client deliveries');
    await metadataReview.screenshot({ path: info.outputPath('home-owner-correction.png') });
    const correctionRequest = page.waitForRequest(r => r.url().endsWith('/collection-correction'));
    const correctionResponse = page.waitForResponse(r =>
      r.url().endsWith('/collection-correction')
    );
    await metadataReview
      .getByRole('button', { name: 'Save correction — not apply', exact: true })
      .click();
    const oldToken = (await correctionRequest).postDataJSON().review_token;
    const revised = await (await correctionResponse).json();
    expect(revised.arrangement.revision).toBe(4);
    expect(revised.collection_correction.renamed).toBe(true);
    expect((await ok(await request.get(`${base}/collections`))).collections).toHaveLength(0);
    expect(
      (
        await request.post(`${base}/arrangements/${revised.arrangement.id}/collection-commit`, {
          data: { review_token: oldToken, idempotency_key: 'obsolete-correction', confirm: true }
        })
      ).status()
    ).toBe(409);
    await expect(reviewRows).toContainText('Correction saved as a new proposal revision');
    await reviewRows.getByRole('button', { name: 'Reopen saved history', exact: true }).click();
    await reviewRows
      .getByRole('button', { name: 'Review collection metadata', exact: true })
      .click();
    await expect(metadataReview).toContainText('Client deliveries');
    expect(calls.length).toBe(3);
    const confirm = metadataReview.getByRole('button', {
      name: 'Apply collection metadata',
      exact: true
    });
    await expect(confirm).toHaveAttribute('aria-disabled', 'true');
    await page.setViewportSize({ width: 390, height: 844 });
    await metadataReview.scrollIntoViewIfNeeded();
    expect(await metadataReview.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await metadataReview.screenshot({ path: info.outputPath('home-collection-review-narrow.png') });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await metadataReview.getByRole('checkbox').check();
    const committedMetadata = page.waitForResponse(r => r.url().endsWith('/collection-commit'));
    await confirm.click();
    const receipt = await (await committedMetadata).json();
    expect(receipt.collection_receipt.effects).toHaveLength(1);
    expect(receipt.arrangement.state).toBe('completed');
    await expect(
      metadataReview.getByRole('heading', { name: 'Saved collection metadata receipt' })
    ).toBeVisible();
    await expect(
      panel.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toHaveCount(0);
    expect(calls.length).toBe(3);
    await page.screenshot({ path: info.outputPath('home-collection-receipt.png'), fullPage: true });
    const collections = page.locator('#projectLibraryCollectionBrowser');
    await expect(collections.getByRole('heading', { name: 'Client deliveries' })).toBeVisible();
    await collections.getByRole('button', { name: 'View members: Client deliveries' }).click();
    await expect(
      collections.getByText('This collection is empty.', { exact: false })
    ).toBeVisible();
    const catalog = await ok(await request.get(`${base}/collections`));
    expect(catalog.collections).toHaveLength(1);
    expect(catalog.collections[0].id).toBe(receipt.collection_receipt.effects[0].collection_id);
    expect(catalog.collections[0].members).toBe(0);
    expect(calls.length).toBe(3);
    await collections.screenshot({ path: info.outputPath('home-collection-browser.png') });
    await panel.getByRole('button', { name: 'Start a new proposal', exact: true }).click();
    await panel.getByRole('button', { name: 'Refresh planning context' }).click();
    await expect(panel.getByText('Context checked.', { exact: false })).toBeVisible();
    await panel.getByRole('textbox').fill('Draft another collection for owner review');
    await panel.getByRole('checkbox').check();
    await panel.getByRole('button', { name: 'Send organizing goal' }).click();
    await expect(
      panel.getByRole('heading', { name: 'Saved arrangement — not applied' })
    ).toBeVisible();
    expect(calls.length).toBe(4);
    await reviewRows
      .getByRole('button', { name: 'Review collection metadata', exact: true })
      .click();
    await reviewRows.getByRole('button', { name: 'Decline proposal', exact: true }).click();
    await expect(
      reviewRows.getByRole('heading', { name: 'Declined arrangement — not applied' })
    ).toBeVisible();
    expect(calls.length).toBe(4);
    expect((await ok(await request.get(`${base}/collections`))).collections).toHaveLength(1);
    await reviewRows.screenshot({ path: info.outputPath('home-declined-proposal.png') });
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
    expect(calls.length).toBe(5);
    const history = await ok(await request.get(`${base}/arrangements`));
    expect(history.arrangements.map((item: any) => item.kind).sort()).toEqual([
      'clarification',
      'proposal',
      'proposal'
    ]);
    expect(history.arrangements.filter((item: any) => item.state === 'cancelled')).toHaveLength(1);
    await page.reload();
    const saved = page.locator('#projectLibraryArrangementRows');
    await expect(saved.getByRole('button', { name: 'Open saved arrangement' })).toHaveCount(3);
    await saved.getByRole('button', { name: 'Open saved arrangement' }).last().click();
    expect(calls.length).toBe(5);
    expect((await ok(await request.get('/api/workspaces'))).folders.length).toBe(workspaceCount);
    expect((await ok(await request.get(`${base}/roots`))).total_roots).toBe(0);
    for (const call of calls) {
      const text = JSON.stringify(call);
      const system = call.messages
        .filter((message: any) => message.role === 'system')
        .map((message: any) => message.content)
        .join('\n');
      expect(system).toContain(expectedSkill);
      expect(system).toContain(expectedRole);
      if (musicSource) {
        expect(text).not.toContain(musicSource);
        expect(text).not.toContain('SYNTHETIC INSTALLED GUIDANCE');
        expect(system.replace(/\s+/g, ' ')).toContain('Saving a proposal is not applying it');
      }
      expect(text).not.toContain(root);
      expect(call.tools).toHaveLength(5);
    }
    await attachJSON('planning-provider-trace', {
      kind: musicSource ? 'exact-music-candidate-scripted-provider' : 'synthetic-scripted-provider',
      skill_sha256: createHash('sha256').update(skillBytes).digest('hex'),
      calls
    });
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
    expect(calls.length).toBe(5);
    expect((await ok(await request.get(`${base}/arrangements`))).arrangements).toHaveLength(3);
    await collections.getByRole('button', { name: 'Refresh collections' }).click();
    await collections.getByRole('button', { name: 'View members: Client deliveries' }).click();
    await expect(
      collections.getByText('This collection is empty.', { exact: false })
    ).toBeVisible();
    expect(calls.length).toBe(5);
    await saved.getByRole('button', { name: 'Open saved arrangement' }).first().click();
    await expect(
      saved.getByRole('heading', { name: 'Saved collection metadata receipt' })
    ).toBeVisible();
    expect(calls.length).toBe(5);
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
