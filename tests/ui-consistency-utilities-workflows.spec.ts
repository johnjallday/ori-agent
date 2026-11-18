import { test, expect, type Page, type APIRequestContext, type TestInfo } from '@playwright/test';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomUUID } from 'node:crypto';
import {
  assertSandbox,
  preparePage,
  guardReadTour,
  readFixture,
  reachable,
  contrast,
  capture as captureSurface
} from './helpers/ui-consistency';
import { workflowScene, themes } from './fixtures/ui-consistency-scenes';

const cleanups = new Map<string, (request: APIRequestContext) => Promise<void>>();
test.afterEach(async ({ request }, info) => {
  try {
    await cleanups.get(info.testId)?.(request);
  } finally {
    cleanups.delete(info.testId);
  }
});
test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
});
async function capture(page: Page, info: TestInfo, name: string, facts = {}) {
  await captureSurface(
    page,
    info,
    name,
    { fixture: 'fictional library/workspace reads; no instantiation or execution', ...facts },
    { fullPage: false }
  );
}
async function workflowReads(page: Page, state = () => 'populated') {
  await readFixture(page, '**/api/workspaces', { workspaces: [] });
  await page.route('**/api/orchestration/templates', route => {
    if (route.request().method() !== 'GET') return route.fallback();
    if (state() === 'error')
      return route.fulfill({ status: 503, body: 'Fictional library unavailable' });
    return route.fulfill({ json: state() === 'empty' ? { templates: [] } : workflowScene });
  });
}

for (const theme of themes) {
  for (const width of [320, 390, 768, 1440]) {
    test(`Workflows library, editor, draft focus and launch cancellation: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await workflowReads(page);
      await page.goto('/workflows');
      const editor = page.locator('#behaviorStudioEditor');
      const name = editor.locator('[data-field="name"]');
      await expect(name).toHaveValue('Fictional editable orchestration');
      await expect(page.locator('h1')).toHaveText('Workflows');
      const editorBox = await editor.boundingBox();
      expect(editorBox!.y).toBeLessThan(500);
      expect((await name.boundingBox())!.y + (await name.boundingBox())!.height).toBeLessThan(900);
      await reachable(page.locator('#behaviorStudioNewBtn'), width < 768 ? 44 : 0);
      const save = editor.locator('[data-preview-action="save"]');
      await reachable(save, width < 768 ? 44 : 0);
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'workflows-editor');
      expect((await contrast(name)).ratio).toBeGreaterThanOrEqual(4.5);
      expect((await contrast(name, '::placeholder')).ratio).toBeGreaterThanOrEqual(4.5);
      expect((await contrast(page.locator('.behavior-page__copy'))).ratio).toBeGreaterThanOrEqual(
        4.5
      );
      const library = page.locator('#behaviorStudioLibrary');
      if (width < 992) await library.locator('summary').click();
      await page.locator('#behaviorStudioSearch').fill('no-fictional-match-xyz');
      await expect(page.locator('#behaviorStudioList')).toContainText(
        'No orchestration skills match'
      );
      await page.locator('#behaviorStudioSearch').fill('');
      await page.locator('#behaviorStudioSourceFilter').selectOption('builtin');
      const selection = page.locator('[data-template-select]').first();
      await reachable(selection, width < 768 ? 44 : 0);
      await capture(page, info, 'workflows-library');
      await selection.focus();
      await page.keyboard.press('Enter');
      await expect(name).toHaveValue(workflowScene.templates[0].name);
      await expect(name).toBeDisabled();
      await expect(save).toBeDisabled();
      if (width < 992) {
        await expect(library).not.toHaveAttribute('open', '');
        await expect(editor.locator('[data-editor-heading]')).toBeFocused();
      } else await expect(page.locator('[data-template-select]')).toBeFocused();
      const graph = page.getByRole('region', { name: 'Step dependency graph', exact: true });
      await reachable(graph);
      expect(await graph.evaluate(node => node.scrollWidth)).toBeGreaterThanOrEqual(720);
      await capture(page, info, 'workflows-preview');
      const launch = editor.locator('[data-preview-action="launch"]');
      await launch.click();
      const modal = page.locator('#behaviorLaunchModal');
      await expect(modal).toHaveAttribute('aria-modal', 'true');
      await expect(modal.locator('[data-launch-field="workspaceId"]')).toBeFocused();
      await reachable(
        modal.getByRole('button', { name: 'Cancel', exact: true }),
        width < 768 ? 44 : 0
      );
      await capture(page, info, 'workflows-launch');
      await page.keyboard.press('Escape');
      await expect(modal).toBeHidden();
      await expect(launch).toBeFocused();
      await editor.locator('[data-editor-more]').click();
      await editor.locator('[data-preview-action="duplicate"]').click();
      await expect(name).toBeFocused();
      await expect(name).toBeEnabled();
      const stepName = editor.locator('[data-step-field="name"]').first();
      await stepName.fill('');
      await stepName.pressSequentially('Draft name stays focused');
      await expect(stepName).toBeFocused();
      await expect(stepName).toHaveValue('Draft name stays focused');
      const defaultValue = editor.locator('[data-param-field="default_value"]').first();
      await defaultValue.fill('fictional changed input');
      const context = editor.locator('[data-step-field="context_text"]').first();
      await context.fill('{fictional-invalid');
      await save.click();
      await expect(context).toBeFocused();
      await expect(context).toHaveAttribute('aria-invalid', 'true');
      await expect(editor.locator('#behaviorEditorFeedback')).toContainText('Step 1 context');
      await capture(page, info, 'workflows-invalid');
      await context.fill('{"fictional":"draft"}');
      await expect(context).not.toHaveAttribute('aria-invalid', 'true');
      await stepName.fill('Second edit retains JSON and default');
      await expect(defaultValue).toHaveValue('fictional changed input');
      await expect(context).toHaveValue('{"fictional":"draft"}');
      page.once('dialog', dialog => dialog.dismiss());
      await page.locator('#behaviorStudioNewBtn').click();
      await expect(stepName).toHaveValue('Second edit retains JSON and default');
      await expect(name).toHaveValue(`${workflowScene.templates[0].name} Copy`);
      page.once('dialog', dialog => dialog.accept());
      await page.locator('#behaviorStudioNewBtn').click();
      await expect(name).toBeFocused();
      await expect(name).toHaveValue('');
      await expect(name).not.toHaveAttribute('aria-invalid', 'true');
      await save.click();
      await expect(name).toBeFocused();
      await expect(name).toHaveAttribute('aria-invalid', 'true');
      await expect(editor.locator('#behaviorEditorFeedback')).toHaveText('Skill name is required.');
      expect(writes).toEqual([]);
      expect(errors).toEqual([]);
    });
  }

  test(`Workflows failed read retry and empty library: ${theme}`, async ({ page }, info) => {
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page);
    let state = 'error';
    await workflowReads(page, () => state);
    await page.goto('/workflows');
    await expect(
      page.getByRole('heading', { name: 'Could not load orchestration skills' })
    ).toBeVisible();
    await expect(page.locator('#behaviorStudioCount')).toHaveText('—');
    await capture(page, info, 'workflows-error');
    state = 'empty';
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(page.locator('[data-field="name"]')).toHaveValue('');
    await expect(page.locator('#behaviorStudioCount')).toHaveText('0');
    await expect(page.locator('[data-field="name"]')).not.toHaveAttribute('aria-invalid', 'true');
    await capture(page, info, 'workflows-empty');
    expect(writes).toEqual([]);
  });

  test(`Workflows real local save, validation and discard: ${theme}`, async ({
    page,
    request
  }, info) => {
    expect(info.config.workers).toBe(1);
    const sandbox = await assertSandbox(request);
    const directory = realpathSync(join(sandbox, 'workflow_templates'));
    expect(dirname(directory)).toBe(sandbox);
    const id = `ui-workflow-${randomUUID()}`;
    const name = `UI workflow ${id.slice(-12)}`;
    const editedName = `${name} edited`;
    const path = join(directory, `${id}.json`);
    expect(existsSync(path)).toBe(false);
    const readLibrary = async (client = request) => {
      const response = await client.get('/api/orchestration/templates');
      expect(response.ok()).toBeTruthy();
      return (await response.json()).templates;
    };
    const original = await readLibrary();
    expect(original.some((item: any) => item.id === id)).toBe(false);
    cleanups.set(info.testId, async client => {
      if (!page.isClosed()) await page.close();
      const owned = (await readLibrary(client)).find((item: any) => item.id === id);
      if (owned) {
        expect([name, editedName]).toContain(owned.name);
        expect(owned.source).toBe('custom');
        expect(realpathSync(path)).toBe(path);
        expect((await client.delete(`/api/orchestration/templates?id=${id}`)).ok()).toBeTruthy();
      }
      expect(existsSync(path)).toBe(false);
      const ordered = (items: any[]) => items.sort((a, b) => a.id.localeCompare(b.id));
      expect(ordered(await readLibrary(client))).toEqual(ordered(original));
    });
    await preparePage(page, theme, 390);
    const unexpected = await guardReadTour(page);
    await readFixture(page, '**/api/workspaces', { workspaces: [] });
    const payload = {
      id,
      name,
      description: '',
      category: '',
      source: 'custom',
      required_roles: ['general'],
      parameters: [
        {
          name: 'param_1',
          type: 'string',
          description: '',
          required: false,
          default_value: 'fictional input'
        }
      ],
      steps: [
        {
          id: 'step-1',
          name: 'Step 1',
          role: 'general',
          agent_name: '',
          description: 'Fictional saved notes; never execute.',
          details: '',
          depends_on: [],
          priority: 3,
          timeout: 600000000000,
          context: { fictional: 'persisted' },
          output_schema: null
        }
      ],
      orchestration_mode: 'graph',
      result_combination_mode: 'structured_outputs',
      combination_instruction: '',
      output_schema: null
    };
    const allowed: string[] = [];
    let edit = false;
    await page.route('**/api/orchestration/templates', async route => {
      const req = route.request();
      if (req.method() !== 'POST') return route.fallback();
      try {
        expect(req.postDataJSON()).toEqual({ ...payload, name: edit ? editedName : name });
      } catch (error) {
        await route.abort('blockedbyclient');
        throw error;
      }
      allowed.push('POST');
      return route.continue();
    });
    await page.goto('/workflows');
    await expect(page.locator('[data-field="name"]')).toBeVisible();
    await page.locator('#behaviorStudioNewBtn').click();
    const editor = page.locator('#behaviorStudioEditor');
    const nameInput = editor.locator('[data-field="name"]');
    await expect(nameInput).toBeFocused();
    await editor.locator('[data-preview-action="save"]').click();
    await expect(nameInput).toHaveAttribute('aria-invalid', 'true');
    expect(allowed).toEqual([]);
    await nameInput.fill(name);
    await editor.locator('[data-field="id"]').fill(id);
    await editor.locator('[data-param-field="default_value"]').fill('fictional input');
    await editor.locator('[data-step-field="description"]').fill(payload.steps[0].description);
    const context = editor.locator('[data-step-field="context_text"]');
    await context.fill('{invalid');
    await editor.locator('[data-preview-action="save"]').click();
    await expect(context).toHaveAttribute('aria-invalid', 'true');
    expect(allowed).toEqual([]);
    await context.fill('{"fictional":"persisted"}');
    const persist = async () => {
      const response = page.waitForResponse(
        res =>
          res.url().endsWith('/api/orchestration/templates') && res.request().method() === 'POST'
      );
      await editor.locator('[data-preview-action="save"]').click();
      expect((await response).status()).toBe(201);
      await expect(editor.locator('[data-preview-action="save"]')).toBeFocused();
      expect(realpathSync(path)).toBe(path);
    };
    await persist();
    const disk = () => JSON.parse(readFileSync(path, 'utf8'));
    expect(disk()).toMatchObject({
      id,
      name,
      parameters: payload.parameters,
      steps: [{ context: { fictional: 'persisted' } }]
    });
    expect((await readLibrary()).find((item: any) => item.id === id)).toEqual(disk());
    await page.reload();
    await expect(nameInput).toHaveValue(name);
    await expect(context).toHaveValue('{\n  "fictional": "persisted"\n}');
    await nameInput.fill(`${name} unsaved`);
    page.once('dialog', dialog => dialog.dismiss());
    await page.locator('#behaviorStudioNewBtn').click();
    await expect(nameInput).toHaveValue(`${name} unsaved`);
    expect(disk().name).toBe(name);
    page.once('dialog', dialog => dialog.accept());
    await page.locator('#behaviorStudioNewBtn').click();
    await expect(nameInput).toHaveValue('');
    page.once('dialog', dialog => dialog.accept());
    await page.goto(`/workflows?template=${id}`);
    await expect(nameInput).toHaveValue(name);
    await nameInput.fill(editedName);
    edit = true;
    await persist();
    expect(disk().name).toBe(editedName);
    await capture(page, info, 'workflows-persisted', {
      journey: 'genuine owned template create/save/reload/edit; no instantiation'
    });
    await editor.locator('[data-editor-more]').click();
    const remove = editor.locator('[data-preview-action="delete"]');
    page.once('dialog', dialog => dialog.dismiss());
    await remove.click();
    await expect(remove).toBeFocused();
    expect(disk().name).toBe(editedName);
    expect(allowed).toEqual(['POST', 'POST']);
    expect(unexpected).toEqual([]);
  });
}
