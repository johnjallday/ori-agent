import { test, expect, type Page, type TestInfo, type APIRequestContext } from '@playwright/test';
import { readFileSync, realpathSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomUUID } from 'node:crypto';
import {
  assertSandbox,
  preparePage,
  guardReadTour,
  readFixture,
  capture as captureSurface,
  reachable,
  contrast
} from './helpers/ui-consistency';
import {
  modelsLibraryScene,
  modelCategoriesScene,
  themes,
  longModelName
} from './fixtures/ui-consistency-scenes';

const modelCategoryCleanups = new Map<string, (request: APIRequestContext) => Promise<void>>();
test.afterEach(async ({ request }, info) => {
  try {
    await modelCategoryCleanups.get(info.testId)?.(request);
  } finally {
    modelCategoryCleanups.delete(info.testId);
  }
});

async function capture(page: Page, info: TestInfo, name: string) {
  await captureSurface(
    page,
    info,
    name,
    {
      fixture: 'fictional provider/category reads; no analysis, configuration or assignment writes'
    },
    { fullPage: false }
  );
}

async function modelsReads(page: Page, state = () => 'populated', view = 'provider') {
  const unexpected: string[] = [];
  await readFixture(page, '**/api/providers', modelsLibraryScene);
  await page.route('**/api/model-categories', async route => {
    if (route.request().method() !== 'GET') return route.fallback();
    if (state() === 'error')
      return route.fulfill({ status: 503, json: { error: 'Fictional categories unavailable' } });
    return route.fulfill({ json: { ...modelCategoriesScene, view_preference: view } });
  });
  await page.route('**/api/models/**', async route => {
    const request = route.request();
    if (request.method() !== 'GET') return route.fallback();
    const url = new URL(request.url());
    if (url.pathname !== '/api/models/auto-categorize/availability' || url.search) {
      unexpected.push(url.pathname + url.search);
      return route.abort('blockedbyclient');
    }
    return route.fulfill({ json: { available: false } });
  });
  return unexpected;
}

test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
});

for (const theme of themes) {
  for (const width of [320, 390, 768, 1440]) {
    test(`Models native disclosures, metadata and category dialogs: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      const unexpected = await modelsReads(page);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto('/models');
      await expect(page.locator('#modelsContent')).toBeVisible();
      await expect(page.locator('.ui-page-header h1')).toHaveText('Available Models');
      await expect(page.locator('.model-item')).toHaveCount(2);
      await expect(page.locator('.model-select-checkbox:visible')).toHaveCount(0);
      // WebKit's classic scrollbar can make the CSS media viewport narrower
      // than the requested screenshot width at the exact Bootstrap breakpoint.
      const gridViewport = await page.evaluate(() => ({
        columns: matchMedia('(min-width: 768px)').matches,
        innerWidth,
        clientWidth: document.documentElement.clientWidth
      }));
      await info.attach('filter-grid-viewport', {
        body: JSON.stringify(gridViewport),
        contentType: 'application/json'
      });
      const tops = await page
        .locator(
          'label[for="providerFilter"], label[for="categoryFilter"], label[for="searchFilter"], #modelsViewLabel'
        )
        .evaluateAll(labels => labels.map(label => label.getBoundingClientRect().top));
      if (gridViewport.columns) {
        expect(Math.max(...tops) - Math.min(...tops)).toBeLessThanOrEqual(2);
      } else {
        for (let index = 1; index < tops.length; index++) {
          expect(tops[index] - tops[index - 1]).toBeGreaterThanOrEqual(44);
        }
      }
      const header = page.locator('button.provider-header').first();
      await header.focus();
      await header.press('Space');
      await expect(header).toHaveAttribute('aria-expanded', 'false');
      await expect(page.locator('.model-info').first()).toBeHidden();
      await header.press('Enter');
      await expect(header).toHaveAttribute('aria-expanded', 'true');
      await expect(page.locator('.model-info').first()).toBeVisible();
      for (const selector of [
        '#manageCategoriesBtn',
        '#refreshModelsBtn',
        '#providerFilter',
        '#categoryFilter',
        '#searchFilter',
        'label[for="viewProvider"]',
        'label[for="viewCategory"]'
      ]) {
        await reachable(page.locator(selector), width < 768 ? 44 : 32);
      }
      expect(
        (await contrast(page.locator('#searchFilter'), '::placeholder')).ratio
      ).toBeGreaterThanOrEqual(4.5);
      for (const selector of [
        '.api-key-status',
        '.provider-badge',
        '.model-tag',
        'label[for="viewCategory"]'
      ]) {
        for (const label of await page.locator(selector).all())
          expect((await contrast(label)).ratio).toBeGreaterThanOrEqual(4.5);
      }
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'models-library');
      await page.locator('#showLegacyFilter').check();
      await expect(page.locator('.model-item')).toHaveCount(3);
      await page.locator('#showLegacyFilter').uncheck();
      await page.locator('#searchFilter').fill('fictional-missing-filter');
      await expect(page.locator('#noResults')).toBeVisible();
      await page.locator('#searchFilter').fill('');
      await page.locator('#providerFilter').selectOption('fictional-local');
      await expect(page.locator('.model-item')).toHaveCount(1);
      await page.locator('#providerFilter').selectOption('all');
      const model = page.locator('.model-info').first();
      await reachable(model, width < 768 ? 44 : 32);
      await capture(page, info, 'models-row');
      await model.focus();
      await model.press('Enter');
      const assignment = page.locator('#assignmentModal');
      await expect(assignment).toBeVisible();
      await expect(assignment).toHaveCSS('opacity', '1');
      await expect(page.locator('#assignmentModelName')).toHaveText(longModelName);
      const firstChoice = assignment.locator('.assignment-category-item').first();
      const checkbox = firstChoice.getByRole('checkbox');
      await checkbox.check();
      await expect(firstChoice).toHaveClass(/selected/);
      await checkbox.focus();
      await checkbox.press('Space');
      await expect(checkbox).not.toBeChecked();
      await expect(firstChoice).not.toHaveClass(/selected/);
      await expect(assignment.getByRole('checkbox').last()).toBeChecked();
      for (const choice of await assignment.locator('.assignment-category-item').all())
        await reachable(choice, 44);
      for (const badge of await assignment.locator('.category-badge').all())
        expect((await contrast(badge)).ratio).toBeGreaterThanOrEqual(4.5);
      const glyph = await checkbox.boundingBox();
      expect(glyph!.width).toBeLessThanOrEqual(20);
      expect(glyph!.height).toBeLessThanOrEqual(20);
      await expect(
        assignment.getByRole('checkbox', { name: 'Fictional hidden category', exact: true })
      ).toHaveCount(0);
      await capture(page, info, 'models-assignment');
      await assignment.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(model).toBeFocused();
      await page.locator('#manageCategoriesBtn').click();
      const manager = page.locator('#categoryModal');
      await expect(manager).toBeVisible();
      await expect(
        manager.getByRole('button', { name: 'Show Fictional hidden category', exact: true })
      ).toBeVisible();
      await expect(
        page.locator('#categoryFilter option').filter({ hasText: 'Fictional hidden category' })
      ).toHaveCount(0);
      await expect(
        manager
          .locator('.category-item')
          .first()
          .getByRole('button', { name: /^Delete/ })
      ).toHaveCount(0);
      await capture(page, info, 'models-categories');
      await page.locator('#addCategoryBtn').click();
      const editor = page.locator('#categoryEditorModal');
      const name = page.locator('#categoryName');
      await expect(name).toBeFocused();
      await expect(name).not.toHaveClass(/is-invalid/);
      await page.locator('#saveCategoryBtn').click();
      await expect(name).toHaveAttribute('aria-invalid', 'true');
      await expect(name).toBeFocused();
      await expect(page.locator('#categoryNameError')).toHaveText('Name is required');
      for (const choice of await editor.locator('.color-option, .icon-option').all())
        await reachable(choice, 44);
      await reachable(editor.locator('label[for="customColor"]'), 44);
      await reachable(
        editor.getByRole('button', { name: 'Cancel', exact: true }),
        width < 768 ? 44 : 40
      );
      const bounds = await editor.locator('.modal-content').boundingBox();
      expect(bounds!.y).toBeGreaterThanOrEqual(0);
      expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(900);
      expect((await contrast(page.locator('#categoryPreview'))).ratio).toBeGreaterThanOrEqual(4.5);
      await capture(page, info, 'models-category-invalid');
      const color = editor.getByRole('button', { name: 'Choose color #ffff00', exact: true });
      await color.focus();
      await color.press('Enter');
      await expect(color).toHaveAttribute('aria-pressed', 'true');
      await expect(
        editor.getByRole('button', { name: 'Choose color #ffffff', exact: true })
      ).toHaveAttribute('aria-pressed', 'false');
      await name.fill('<Fictional preview>');
      await expect(name).not.toHaveClass(/is-invalid/);
      await expect(page.locator('#categoryNameError')).toBeEmpty();
      await expect(page.locator('#categoryPreview')).toContainText('<Fictional preview>');
      expect(
        await page
          .locator('#categoryPreview')
          .evaluate(node => (node as HTMLElement).style.getPropertyValue('--category-color'))
      ).toBe('#ffff00');
      const icon = editor.getByRole('button', { name: 'Choose icon code-slash', exact: true });
      await icon.focus();
      await icon.press('Space');
      await expect(icon).toHaveAttribute('aria-pressed', 'true');
      await capture(page, info, 'models-category-editor');
      await editor.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(page.locator('#manageCategoriesBtn')).toBeFocused();
      await page.locator('#manageCategoriesBtn').click();
      await page.locator('#addCategoryBtn').click();
      await expect(name).toBeFocused();
      await expect(name).toHaveValue('');
      expect(await name.evaluate(input => input.matches(':user-invalid'))).toBe(false);
      await expect(name).not.toHaveClass(/is-invalid/);
      await expect(name).not.toHaveAttribute('aria-invalid', 'true');
      await editor.getByRole('button', { name: 'Cancel', exact: true }).click();
      await readFixture(page, '**/api/providers', {
        providers: modelsLibraryScene.providers.map(provider => ({ ...provider, available: true }))
      });
      await page.locator('#refreshModelsBtn').click();
      await expect(page.locator('.api-key-status.configured')).toHaveText('API Key Configured');
      expect(
        (await contrast(page.locator('.api-key-status.configured'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      await reachable(page.locator('.api-key-status.configured'));
      await capture(page, info, 'models-configured');
      expect(writes).toEqual([]);
      expect(unexpected).toEqual([]);
      expect(errors).toEqual([]);
    });
  }

  test(`Models category view, empty providers and failed read recovery: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page);
    let state = 'populated';
    const unexpected = await modelsReads(page, () => state, 'category');
    await page.goto('/models');
    const header = page.locator('button.category-header').first();
    await expect(header).toBeVisible();
    await expect(page.locator('#viewCategory')).toBeChecked();
    await header.focus();
    await header.press('Space');
    await expect(header).toHaveAttribute('aria-expanded', 'false');
    await header.press('Enter');
    await expect(header).toHaveAttribute('aria-expanded', 'true');
    await reachable(header, 44);
    await capture(page, info, 'models-category-view');
    state = 'error';
    await page.locator('#refreshModelsBtn').click();
    await expect(page.locator('#errorState')).toContainText('Failed to fetch categories');
    await expect(page.locator('#manageCategoriesBtn')).toBeDisabled();
    await expect(page.locator('#modelsContent')).toBeHidden();
    await capture(page, info, 'models-error');
    state = 'populated';
    await page.locator('#refreshModelsBtn').click();
    await expect(header).toBeVisible();
    await expect(page.locator('#manageCategoriesBtn')).toBeEnabled();
    await readFixture(page, '**/api/model-categories', {
      ...modelCategoriesScene,
      view_preference: 'provider'
    });
    await readFixture(page, '**/api/providers', { providers: [modelsLibraryScene.providers[0]] });
    await page.locator('#refreshModelsBtn').click();
    await expect(page.locator('.api-key-status.missing')).toHaveText('API Key Required');
    await expect(page.locator('.model-item')).toHaveCount(1);
    await reachable(page.locator('.api-key-status.missing'));
    await capture(page, info, 'models-unconfigured');
    await readFixture(page, '**/api/providers', { providers: [] });
    await page.locator('#refreshModelsBtn').click();
    await expect(page.locator('#noResults')).toBeVisible();
    await expect(page.locator('.model-item')).toHaveCount(0);
    await capture(page, info, 'models-empty');
    expect(writes).toEqual([]);
    expect(unexpected).toEqual([]);
  });

  test(`Models real local category lifecycle and view preference: ${theme}`, async ({
    page,
    request
  }, info) => {
    expect(info.config.workers, 'category settings are shared across browser engines').toBe(1);
    const sandbox = await assertSandbox(request);
    const storePath = realpathSync(join(sandbox, 'model_categories.json'));
    expect(dirname(storePath)).toBe(sandbox);
    const disk = () => JSON.parse(readFileSync(storePath, 'utf8'));
    const readCategories = async (client = request) => {
      const response = await client.get('/api/model-categories');
      expect(response.ok()).toBeTruthy();
      return response.json();
    };
    const original = await readCategories();
    expect(disk().categories).toEqual(original.categories);
    expect(['provider', 'category']).toContain(original.view_preference);
    const name = `UI models ${randomUUID().slice(0, 18)}`;
    const editedName = `${name} edited`;
    const ownedNames = [name, editedName];
    expect(original.categories.some((category: any) => ownedNames.includes(category.name))).toBe(
      false
    );
    const payload = {
      name,
      color: original.predefined_colors[0],
      icon: original.predefined_icons[0]
    };
    const nextView = original.view_preference === 'category' ? 'provider' : 'category';
    let ownedID = '';
    await preparePage(page, theme, 390);
    const unexpected = await guardReadTour(page);
    await readFixture(page, '**/api/providers', modelsLibraryScene);
    await readFixture(page, '**/api/models/auto-categorize/availability', { available: false });
    const allowed: string[] = [];
    await page.route(/\/api\/model-categories(?:\/[^?]*)?$/, async route => {
      const req = route.request();
      if (req.method() === 'GET') return route.fallback();
      const path = new URL(req.url()).pathname;
      try {
        if (path === '/api/model-categories' && req.method() === 'POST') {
          expect(req.postDataJSON()).toEqual(payload);
        } else if (path === '/api/model-categories/view-preference' && req.method() === 'PUT') {
          expect(req.postDataJSON()).toEqual({ preference: nextView });
        } else if (
          ownedID &&
          path === `/api/model-categories/${ownedID}` &&
          req.method() === 'PUT'
        ) {
          expect(req.postDataJSON()).toEqual({ ...payload, name: editedName });
        } else if (
          ownedID &&
          path === `/api/model-categories/${ownedID}` &&
          req.method() === 'DELETE'
        ) {
          expect(req.postData()).toBeNull();
        } else {
          return route.fallback();
        }
      } catch (error) {
        await route.abort('blockedbyclient');
        throw error;
      }
      allowed.push(`${req.method()} ${path}`);
      return route.continue();
    });
    // Cleanup gets the hook's separate timeout budget, even if a UI assertion times out.
    modelCategoryCleanups.set(info.testId, async cleanupRequest => {
      if (!page.isClosed()) await page.close();
      for (const category of (await readCategories(cleanupRequest)).categories.filter(
        (category: any) => ownedNames.includes(category.name)
      )) {
        expect(original.categories.some((before: any) => before.id === category.id)).toBe(false);
        expect(
          (
            await cleanupRequest.delete(`/api/model-categories/${encodeURIComponent(category.id)}`)
          ).ok()
        ).toBeTruthy();
      }
      expect(
        (
          await cleanupRequest.put('/api/model-categories/view-preference', {
            data: { preference: original.view_preference }
          })
        ).ok()
      ).toBeTruthy();
      const restored = await readCategories(cleanupRequest);
      expect(restored.categories).toEqual(original.categories);
      expect(restored.model_assignments).toEqual(original.model_assignments);
      expect(restored.view_preference).toBe(original.view_preference);
      expect(disk().categories).toEqual(original.categories);
      expect(disk().view_preference).toBe(original.view_preference);
    });
    await page.goto('/models');
    await expect(page.locator('#modelsContent')).toBeVisible();
    const preferenceSaved = page.waitForResponse(
      response =>
        response.url().endsWith('/api/model-categories/view-preference') &&
        response.request().method() === 'PUT'
    );
    await page
      .locator(`label[for="${nextView === 'category' ? 'viewCategory' : 'viewProvider'}"]`)
      .click();
    expect((await preferenceSaved).ok()).toBeTruthy();
    expect((await readCategories()).view_preference).toBe(nextView);
    expect(disk().view_preference).toBe(nextView);
    await page.reload();
    await expect(
      page.locator(nextView === 'category' ? '#viewCategory' : '#viewProvider')
    ).toBeChecked();
    await page.locator('#manageCategoriesBtn').click();
    await page.locator('#addCategoryBtn').click();
    const input = page.locator('#categoryName');
    await expect(input).toBeFocused();
    const beforeInvalid = allowed.length;
    await page.locator('#saveCategoryBtn').click();
    await expect(page.locator('#categoryNameError')).toHaveText('Name is required');
    expect(allowed).toHaveLength(beforeInvalid);
    await input.fill(name);
    const createdResponse = page.waitForResponse(
      response =>
        response.url().endsWith('/api/model-categories') && response.request().method() === 'POST'
    );
    await input.press('Enter');
    const created = await createdResponse;
    expect(created.status()).toBe(201);
    // The controller intentionally reloads instead of consuming the POST body.
    // Read authoritative persistence; waiting for Chromium's unread fetch body can stall.
    const createdCategories = (await readCategories()).categories.filter(
      (category: any) => category.name === name
    );
    expect(createdCategories).toHaveLength(1);
    ownedID = createdCategories[0].id;
    expect(ownedID).toMatch(/^[a-zA-Z0-9_-]+$/);
    const manager = page.locator('#categoryModal');
    await expect(manager).toBeVisible();
    const persisted = () => disk().categories.find((category: any) => category.id === ownedID);
    expect(persisted()).toMatchObject({ ...payload, id: ownedID });
    expect(Boolean(persisted().is_default)).toBe(false);
    expect(
      (await readCategories()).categories.find((category: any) => category.id === ownedID)
    ).toMatchObject(payload);
    await manager.getByRole('button', { name: `Edit ${name}`, exact: true }).click();
    await expect(input).toBeFocused();
    await input.fill(`${name} unsaved`);
    await page
      .locator('#categoryEditorModal')
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    expect(persisted().name).toBe(name);
    expect(
      (await readCategories()).categories.find((category: any) => category.id === ownedID).name
    ).toBe(name);
    await page.locator('#manageCategoriesBtn').click();
    await manager.getByRole('button', { name: `Edit ${name}`, exact: true }).click();
    await expect(input).toBeFocused();
    await expect(input).toHaveValue(name);
    await input.fill(editedName);
    const updated = page.waitForResponse(
      response =>
        response.url().endsWith(`/api/model-categories/${ownedID}`) &&
        response.request().method() === 'PUT'
    );
    await page.locator('#saveCategoryBtn').click();
    expect((await updated).ok()).toBeTruthy();
    await expect(manager).toBeVisible();
    expect(persisted().name).toBe(editedName);
    await captureSurface(
      page,
      info,
      'models-category-persisted',
      {
        journey:
          'real isolated category create/edit and saved view preference; synthetic providers; no AI calls'
      },
      { fullPage: false }
    );
    page.once('dialog', dialog => dialog.dismiss());
    await manager.getByRole('button', { name: `Delete ${editedName}`, exact: true }).click();
    await expect(
      manager.getByRole('button', { name: `Delete ${editedName}`, exact: true })
    ).toBeFocused();
    expect(
      (await readCategories()).categories.some((category: any) => category.id === ownedID)
    ).toBe(true);
    expect(persisted().name).toBe(editedName);
    page.once('dialog', dialog => dialog.accept());
    const removed = page.waitForResponse(
      response =>
        response.url().endsWith(`/api/model-categories/${ownedID}`) &&
        response.request().method() === 'DELETE'
    );
    await manager.getByRole('button', { name: `Delete ${editedName}`, exact: true }).click();
    expect((await removed).ok()).toBeTruthy();
    await expect(page.locator('#addCategoryBtn')).toBeFocused();
    expect(
      (await readCategories()).categories.some((category: any) => category.id === ownedID)
    ).toBe(false);
    expect(persisted()).toBeUndefined();
    expect(allowed).toEqual([
      'PUT /api/model-categories/view-preference',
      'POST /api/model-categories',
      `PUT /api/model-categories/${ownedID}`,
      `DELETE /api/model-categories/${ownedID}`
    ]);
    expect(unexpected).toEqual([]);
  });
}
