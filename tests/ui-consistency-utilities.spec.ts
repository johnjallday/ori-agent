import { test, expect, type Page, type TestInfo } from '@playwright/test';
import {
  assertSandbox,
  capture as captureSurface,
  preparePage,
  reachable,
  guardReadTour,
  readFixture,
  contrast
} from './helpers/ui-consistency';
import { themes, templateScene, templateQuestScene } from './fixtures/ui-consistency-scenes';
import { existsSync, realpathSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve } from 'node:path';

const profileScene = {
  display_name: 'Alex Example',
  email: 'alex@example.test',
  timezone: 'UTC',
  locale: 'en-US',
  about: 'Fictional profile for the UI consistency tour.',
  preferences: { response_style: 'Concise, with sources', units: 'metric', language: 'English' }
};

// Long utility documents need readable viewport evidence as well as bounds
// assertions; full-page Settings photography is over 20,000px tall on phones.
async function capture(page: Page, info: TestInfo, name: string, facts: object) {
  await captureSurface(page, info, name, facts, { fullPage: false });
}

test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
});

for (const theme of themes) {
  for (const width of [320, 1440]) {
    test(`Settings sections, search, native links and measured navigation: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      await page.goto('/settings');
      const search = page.locator('#settingsSearch');
      await expect(page.locator('.ui-page-header h1')).toHaveText('Settings');
      await reachable(search, width < 992 ? 44 : 40);
      const active = page.locator('.settings-nav-item[data-section="api-keys"]');
      await expect(active).toHaveAttribute('aria-current', 'location');
      expect((await contrast(search)).ratio).toBeGreaterThanOrEqual(4.5);
      expect((await contrast(search, '::placeholder')).ratio).toBeGreaterThanOrEqual(4.5);
      expect((await contrast(active)).ratio).toBeGreaterThanOrEqual(4.5);
      const neutral = await page.locator('#openaiApiKeyInput').evaluate(node => ({
        invalid: node.matches(':user-invalid, .is-invalid'),
        radius: getComputedStyle(node).borderTopLeftRadius,
        fieldBackground: getComputedStyle(node.closest('.settings-field')!).backgroundColor
      }));
      expect(neutral).toEqual({
        invalid: false,
        radius: '8px',
        fieldBackground: 'rgba(0, 0, 0, 0)'
      });
      await capture(page, info, 'settings', { fixture: 'real isolated settings, no writes' });

      await search.fill('no-such-setting-ui-tour');
      await expect(page.locator('#settingsNoResults')).toBeVisible();
      await search.press('Escape');
      await expect(search).toBeFocused();
      await expect(page.locator('#settingsNoResults')).not.toBeVisible();
      await search.fill('Voice');
      const voiceLink = page.locator('.settings-nav-item[data-section="voice-language"]');
      await expect(voiceLink).toBeVisible();
      await voiceLink.focus();
      await voiceLink.press('Enter');
      await expect(page).toHaveURL(/#voice-language$/);
      const heading = page.locator('#voice-language h2');
      if (width < 992) {
        await expect(page.locator('#settingsNavDisclosure')).not.toHaveAttribute('open', '');
        await expect(heading).toBeFocused();
      }
      const geometry = await heading.evaluate(node => ({
        top: node.getBoundingClientRect().top,
        navbar: parseFloat(
          getComputedStyle(document.documentElement).getPropertyValue('--navbar-offset')
        ),
        height: innerHeight
      }));
      expect(geometry.top).toBeGreaterThanOrEqual(geometry.navbar);
      expect(geometry.top).toBeLessThan(geometry.height - 80);
      await reachable(page.locator('#speechProviderSelect'), width < 992 ? 44 : 40);
      // Cold deep links and Back must not append history entries.
      await page.goto('/settings#voice-language');
      await expect(
        page.locator('.settings-nav-item[data-section="voice-language"]')
      ).toHaveAttribute('aria-current', 'location');
      await page.reload();
      await expect(
        page.locator('.settings-nav-item[data-section="voice-language"]')
      ).toHaveAttribute('aria-current', 'location');
      if (width >= 992) expect(await page.evaluate(() => window.scrollY)).toBe(0);
      await page.evaluate(() => (window as any).SettingsNavigation.navigateTo('api-keys'));
      await expect(page).toHaveURL(/#api-keys$/);
      await page.goBack();
      await expect(page).toHaveURL(/#voice-language$/);
      const before = await page.evaluate(() => history.length);
      await page.waitForTimeout(200);
      expect(await page.evaluate(() => history.length)).toBe(before);
      // Search still exposes native full-page links, not JS-only navigation.
      await search.fill('Skills');
      const skillsLink = page.locator('.settings-nav-item[href="/skills"]');
      await expect(skillsLink).toBeVisible();
      await skillsLink.focus();
      await expect(skillsLink).toBeFocused();
      await skillsLink.press('Enter');
      await expect(page).toHaveURL(/\/skills$/);
      expect(writes).toEqual([]);
    });

    test(`Profile fields and explicit knowledge scopes: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      await readFixture(page, '**/api/user/profile', { profile: profileScene });
      await page.goto('/profile');
      await expect(page.locator('#userProfileForm')).toBeVisible();
      await expect(page.locator('#profileDisplayName')).toHaveValue(profileScene.display_name);
      await expect(
        page.locator('.profile-context-value').filter({ hasText: 'Excluded until you approve it' })
      ).toBeVisible();
      for (const selector of [
        '#profileDisplayName',
        '#profileEmail',
        '#profileTimezone',
        '#saveUserProfileBtn'
      ]) {
        await reachable(page.locator(selector), width < 992 ? 44 : 40);
      }
      for (const selector of [
        '.settings-help',
        '.profile-context-label',
        '.profile-context-value'
      ]) {
        expect((await contrast(page.locator(selector).first())).ratio).toBeGreaterThanOrEqual(4.5);
      }
      expect(
        await page
          .locator('#profileEmail')
          .evaluate(node => node.matches(':user-invalid, .is-invalid'))
      ).toBe(false);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        width + 1
      );
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'profile', {
        fixture: 'fictional global profile read; real isolated knowledge reads'
      });
      expect(writes).toEqual([]);
    });
  }

  for (const width of [320, 1440]) {
    test(`Vault empty setup, secondary actions and custom dialog focus: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      await readFixture(page, '**/api/vault/vaults', { count: 0, vaults: [] });
      await page.goto('/vault');
      await expect(page.locator('#private-vault')).toHaveAttribute('data-vault-state', 'empty');
      await expect(page.locator('.ui-page-header h1')).toHaveText('Vaults');
      await expect(page.locator('#vaultPageEmptySetup')).toBeVisible();
      await expect(page.locator('.vault-page-explorer-grid')).not.toBeVisible();
      await expect(page.locator('#vaultEntryGrid')).not.toBeVisible();
      await expect(page.locator('#vaultExplorerAddBtn')).not.toBeVisible();
      const create = page.locator('#vaultOpenCreateDialogBtn');
      await expect(create).toHaveClass(/modern-btn-primary/);
      await reachable(create, width < 992 ? 44 : 40);
      await capture(page, info, 'vault-empty', { fixture: 'empty vault list read; no writes' });
      await create.click();
      const overlay = page.locator('#vaultCreateOverlay');
      const dialog = overlay.getByRole('dialog');
      const name = page.locator('#vaultNewVaultName');
      const cancel = page.locator('#vaultCreateCancelBtn');
      await expect(name).toBeFocused();
      await expect(page.locator('.main-content-wrapper')).toHaveAttribute('inert', '');
      await name.press('Shift+Tab');
      await expect(cancel).toBeFocused();
      await cancel.press('Tab');
      await expect(name).toBeFocused();
      expect(await name.evaluate(node => node.matches(':user-invalid, .is-invalid'))).toBe(false);
      expect((await contrast(name, '::placeholder')).ratio).toBeGreaterThanOrEqual(4.5);
      await capture(page, info, 'vault-create', { fixture: 'neutral real dialog; no writes' });
      await page.locator('#vaultCreateVaultSpaceBtn').click();
      await expect(dialog.getByRole('alert')).toContainText(/name/i);
      await reachable(cancel, width < 992 ? 44 : 40);
      const box = await dialog.boundingBox();
      expect(box!.y).toBeGreaterThanOrEqual(0);
      expect(box!.y + box!.height).toBeLessThanOrEqual(901);
      await capture(page, info, 'vault-create-invalid', {
        fixture: 'client validation stays inside the real dialog; no writes'
      });
      await cancel.click();
      await expect(overlay).not.toBeVisible();
      await expect(create).toBeFocused();
      await expect(page.locator('.main-content-wrapper')).not.toHaveAttribute('inert', '');
      const more = page.locator('.vault-page-tools-disclosure > summary');
      await more.focus();
      await more.press('Enter');
      await expect(page.locator('.vault-page-tools-disclosure')).toHaveAttribute('open', '');
      const importButton = page.locator('#vaultOpenImportDialogBtn');
      await reachable(importButton, width < 992 ? 44 : 40);
      await importButton.click();
      await expect(page.locator('#vaultImportOverlay')).toBeVisible();
      await expect(page.locator('#vaultImportConfirmVaultPassword')).toBeVisible();
      await expect(page.locator('#vaultConfirmVaultPassword')).toHaveCount(1);
      await page.locator('#vaultImportCancelBtn').click();
      await expect(importButton).toBeFocused();
      expect(writes).toEqual([]);
    });
  }

  test(`Vault owns a real encrypted record through lock, unlock, edit and confirmed deletion: ${theme}`, async ({
    page,
    request
  }, info) => {
    const sandbox = await assertSandbox(request);
    const rootResponse = await request.get('/api/settings/vault-root');
    expect(rootResponse.ok()).toBeTruthy();
    const root = resolve((await rootResponse.json()).effective_vault_root);
    const within = (path: string) => {
      const child = relative(sandbox, path);
      expect(
        child && !child.startsWith('..') && !isAbsolute(child),
        'Vault storage must remain in the owned sandbox'
      ).toBeTruthy();
    };
    within(root);
    const existingRoot = realpathSync(existsSync(root) ? root : dirname(root));
    if (existingRoot !== sandbox) within(existingRoot);
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page);
    const vaultName = `UI fixture ${crypto.randomUUID()}`;
    const password = 'synthetic-vault-browser-test-only';
    let vaultID = '';
    let recordID = '';
    let safePackage = false;
    let note = 'Fictional encrypted content for UI verification only.';
    const label =
      'A fictional record with a long explanatory title for the narrow encrypted explorer';
    const allowed: string[] = [];
    await page.route('**/api/vault/**', async route => {
      const req = route.request();
      if (['GET', 'HEAD'].includes(req.method())) return route.fallback();
      const url = new URL(req.url());
      const key = `${req.method()} ${url.pathname}`;
      if (key === 'POST /api/vault/vaults') {
        expect(req.postDataJSON()).toEqual({
          name: vaultName,
          description: '',
          vault_password: password,
          storage: { mode: 'managed', directory: '' }
        });
      } else if (vaultID && key === 'POST /api/vault/lock') {
        expect(url.searchParams.get('vault_id')).toBe(vaultID);
        expect(req.postDataJSON()).toEqual({});
      } else if (vaultID && key === 'POST /api/vault/unlock') {
        expect(url.searchParams.get('vault_id')).toBe(vaultID);
        expect(req.postDataJSON()).toEqual({ vault_id: vaultID, vault_password: password });
      } else if (
        vaultID &&
        (key === 'POST /api/vault/records' ||
          (recordID && key === `PATCH /api/vault/records/${recordID}`))
      ) {
        if (req.method() === 'PATCH') expect(url.searchParams.get('vault_id')).toBe(vaultID);
        expect(req.postDataJSON()).toEqual({
          vault_id: vaultID,
          type: 'personal_note',
          workspace_id: '',
          label,
          tags: [],
          retention_policy: '',
          folder_path: '',
          payload: { note }
        });
      } else if (recordID && key === `DELETE /api/vault/records/${recordID}`) {
        expect(url.searchParams.get('vault_id')).toBe(vaultID);
        expect(req.postData()).toBeNull();
      } else return route.fallback(); // Unexpected writes fail in guardReadTour.
      allowed.push(key);
      await route.continue();
    });
    const waitFor = (method: string, path: string) =>
      page.waitForResponse(
        r => new URL(r.url()).pathname === path && r.request().method() === method
      );
    try {
      await page.goto('/vault');
      await page.locator('#vaultOpenCreateDialogBtn').click();
      await expect(page.locator('#vaultNewVaultName')).toBeFocused();
      await page.locator('#vaultNewVaultName').fill(vaultName);
      await page.locator('#vaultNewVaultPassword').fill(password);
      await page.locator('#vaultConfirmVaultPassword').fill(password);
      const [created] = await Promise.all([
        waitFor('POST', '/api/vault/vaults'),
        page.locator('#vaultCreateVaultSpaceBtn').click()
      ]);
      expect(created.ok()).toBeTruthy();
      const vault = (await created.json()).vault;
      vaultID = vault.id;
      expect(vaultID).toBeTruthy();
      within(realpathSync(vault.location_summary));
      safePackage = true;
      await expect(page.locator('#private-vault')).toHaveAttribute('data-vault-state', 'ready');
      const toggle = page.locator(
        `[data-action="toggle-vault-access"][data-vault-id="${vaultID}"]`
      );
      await reachable(toggle, 44);
      await toggle.click();
      await expect(page.locator('#private-vault')).toHaveAttribute('data-vault-state', 'locked');
      await expect(page.locator('#vaultExplorerAddBtn')).toBeDisabled();
      await toggle.click();
      await expect(page.locator('#vaultUnlockPassword')).toBeFocused();
      await capture(page, info, 'vault-unlock', {
        fixture: 'real owned password-protected vault; no native secret store'
      });
      await page.locator('#vaultUnlockCancelBtn').click();
      expect(
        (await (await request.get(`/api/vault/status?vault_id=${vaultID}`)).json()).locked
      ).toBe(true);
      await toggle.click();
      await page.locator('#vaultUnlockPassword').fill(password);
      await page.locator('#vaultUnlockBtn').click();
      await expect(page.locator('#private-vault')).toHaveAttribute('data-vault-state', 'ready');
      const status = await (await request.get(`/api/vault/status?vault_id=${vaultID}`)).json();
      expect(status.secret_store.backend).toBe('vault_password');
      await page.locator('#vaultExplorerAddBtn').click();
      await expect(page.locator('#vaultEntryLabel')).toBeFocused();
      await page.locator('#vaultEntryLabel').fill(label);
      await page.locator('#vaultEntryContent').fill(note);
      const [recordCreated] = await Promise.all([
        waitFor('POST', '/api/vault/records'),
        page.locator('#vaultSaveEntryBtn').click()
      ]);
      expect(recordCreated.ok()).toBeTruthy();
      recordID = (await recordCreated.json()).record.id;
      await expect(page.locator('#vaultEntryOverlay')).not.toBeVisible();
      const record = page.locator(`#vaultRecordsList [data-record-id="${recordID}"]`);
      await expect(record).toBeVisible();
      await reachable(record, 44);
      await capture(page, info, 'vault-record', {
        fixture: 'real persisted fictional record; private payload not revealed'
      });
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'vault-populated', {
        fixture: 'real owned vault and persisted fictional record; payload not revealed'
      });
      await record.click();
      await expect(page.locator('#vaultEntryLabel')).toBeFocused();
      await expect(page.locator('#vaultEntryContent')).toHaveValue(note);
      await page.locator('#vaultEntryContent').fill('Cancelled wording must not be persisted.');
      await page.locator('#vaultEditorCloseBtn').click();
      expect(
        (await (await request.get(`/api/vault/records/${recordID}?vault_id=${vaultID}`)).json())
          .payload
      ).toEqual({ note });
      await record.click();
      await expect(page.locator('#vaultEntryLabel')).toBeFocused();
      note = 'Updated fictional encrypted content.';
      await page.locator('#vaultEntryContent').fill(note);
      const [updated] = await Promise.all([
        waitFor('PATCH', `/api/vault/records/${recordID}`),
        page.locator('#vaultSaveEntryBtn').click()
      ]);
      expect(updated.ok()).toBeTruthy();
      expect(
        (await (await request.get(`/api/vault/records/${recordID}?vault_id=${vaultID}`)).json())
          .payload
      ).toEqual({ note });
      const remove = page.locator('#vaultPageExplorerPreview [data-action="delete-record"]');
      page.once('dialog', dialog => dialog.dismiss());
      await remove.click();
      expect(allowed.filter(key => key.startsWith('DELETE'))).toEqual([]);
      expect(
        (await request.get(`/api/vault/records/${recordID}?vault_id=${vaultID}`)).ok()
      ).toBeTruthy();
      page.once('dialog', dialog => dialog.accept());
      const [deleted] = await Promise.all([
        waitFor('DELETE', `/api/vault/records/${recordID}`),
        remove.click()
      ]);
      expect(deleted.ok()).toBeTruthy();
      await expect(record).toHaveCount(0);
      expect(
        (await (await request.get(`/api/vault/records?vault_id=${vaultID}`)).json()).records
      ).toEqual([]);
      expect(writes).toEqual([]);
    } finally {
      if (vaultID && safePackage) {
        expect(
          (await request.delete(`/api/vault/vaults/${vaultID}?delete_file=true`)).ok()
        ).toBeTruthy();
      }
    }
  });

  test(`Profile validates, persists and cancels forgetting; Settings persists a local preference: ${theme}`, async ({
    page,
    request
  }, info) => {
    expect(info.config.workers, 'singleton sandbox preferences require serial workers').toBe(1);
    const original = (await (await request.get('/api/user/profile')).json()).profile;
    const notesOriginal = (await (await request.get('/api/notes-open-behavior')).json()).behavior;
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page, ['/api/notes-open-behavior']);
    const name = `UI profile ${crypto.randomUUID().slice(0, 8)}`;
    const expected = {
      ...(original.updated_at ? { updated_at: original.updated_at } : {}),
      ...profileScene,
      display_name: name,
      role_category: 'designer',
      specializations: ['Accessibility', 'Frontend']
    };
    let saves = 0;
    await page.route('**/api/user/profile', async route => {
      if (route.request().method() !== 'PUT') return route.fallback();
      expect(route.request().postDataJSON()).toEqual(expected);
      saves++;
      // Only this exact reviewed PUT bypasses the read-tour guard. Real server.
      await route.continue();
    });
    try {
      await page.goto('/profile');
      await expect(page.locator('#userProfileForm')).toBeVisible();
      await page.locator('#profileEmail').fill('not-an-email');
      await page.locator('#saveUserProfileBtn').click();
      await expect(page.locator('#profileEmail')).toBeFocused();
      expect(saves).toBe(0);
      await page.locator('#profileDisplayName').fill(name);
      await page.locator('#profileEmail').fill(profileScene.email);
      await page.locator('#profileTimezone').selectOption('UTC');
      await page.locator('#profileLocale').fill('en-US');
      await page.locator('#profileAbout').fill(profileScene.about);
      await page.locator('#profileRoleCategory').fill('designer');
      await page.locator('#profileSpecializations').fill('Accessibility, Frontend');
      await page.locator('#profileResponseStyle').fill(profileScene.preferences.response_style);
      await page.locator('#profileUnits').fill('metric');
      await page.locator('#profileLanguage').fill('English');
      const [saved] = await Promise.all([
        page.waitForResponse(
          r => r.url().endsWith('/api/user/profile') && r.request().method() === 'PUT'
        ),
        page.locator('#saveUserProfileBtn').click()
      ]);
      expect(saved.ok(), await saved.text()).toBeTruthy();
      expect(saves).toBe(1);
      const persisted = (await (await request.get('/api/user/profile')).json()).profile;
      const { updated_at: _revision, ...values } = expected;
      expect(persisted).toMatchObject(values);
      await page.reload();
      await expect(page.locator('#profileDisplayName')).toHaveValue(name);
      await page.locator('#profileAbout').fill('An unsaved draft survives cancellation.');
      page.once('dialog', async dialog => {
        expect(dialog.type()).toBe('confirm');
        expect(dialog.message()).toContain('Forget your saved response style');
        await dialog.dismiss();
      });
      await page
        .locator('[data-profile-field="response_style"][data-profile-action="forget"]')
        .click();
      await expect(page.locator('#profileAbout')).toHaveValue(
        'An unsaved draft survives cancellation.'
      );
      expect((await (await request.get('/api/user/profile')).json()).profile.preferences).toEqual(
        profileScene.preferences
      );
      await capture(page, info, 'profile-saved-cancelled', {
        fixture:
          'real sandbox PUT, GET persistence, native forget cancellation; unrelated draft retained'
      });

      const next = notesOriginal === 'page' ? 'modal' : 'page';
      await page.route('**/api/notes-open-behavior', async route => {
        if (route.request().method() === 'POST')
          expect(route.request().postDataJSON()).toEqual({ behavior: next });
        await route.fallback();
      });
      await page.goto('/settings#notes');
      const radio = page.locator(`input[name="notesOpenBehavior"][value="${next}"]`);
      const label = page.locator('label.ui-density-option').filter({ has: radio });
      await reachable(label, 44);
      const [savedPreference] = await Promise.all([
        page.waitForResponse(
          r => r.url().endsWith('/api/notes-open-behavior') && r.request().method() === 'POST'
        ),
        label.click()
      ]);
      expect(savedPreference.ok()).toBeTruthy();
      expect((await (await request.get('/api/notes-open-behavior')).json()).behavior).toBe(next);
      await page.reload();
      await expect(radio).toBeChecked();
      expect(writes).toEqual([]);
    } finally {
      const current = (await (await request.get('/api/user/profile')).json()).profile;
      expect(
        (
          await request.put('/api/user/profile', {
            data: { ...original, updated_at: current.updated_at }
          })
        ).ok()
      ).toBeTruthy();
      expect(
        (await request.post('/api/notes-open-behavior', { data: { behavior: notesOriginal } })).ok()
      ).toBeTruthy();
    }
  });

  test(`Profile failed read has a reachable retry without inventing saved state: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 320);
    const writes = await guardReadTour(page);
    await page.route('**/api/user/profile', route =>
      route.request().method() === 'GET'
        ? route.fulfill({ status: 503, json: { error: 'UI tour: unavailable profile source' } })
        : route.fallback()
    );
    await page.goto('/profile');
    const retry = page.locator('.user-profile-retry');
    await reachable(retry, 44);
    await expect(page.locator('#userProfileForm')).not.toBeVisible();
    await capture(page, info, 'profile-error', {
      fixture: 'deliberate failed profile GET; no mutation'
    });
    expect(writes).toEqual([]);
  });
}

for (const theme of themes) {
  for (const width of [320, 1440]) {
    test(`Templates native selection, long filters, provenance and cancelled dialog: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      await readFixture(page, '**/api/project-templates', templateScene);
      await readFixture(page, '**/api/setup-quests', templateQuestScene);
      await page.goto('/templates');
      await expect(page.locator('.ui-page-header h1')).toHaveText('Templates');
      await expect(page.locator('#tplDetailEmpty')).toBeVisible();
      const rows = page.locator('#tplList button');
      await expect(rows).toHaveCount(2);
      const provenance = page.locator('.tpl-provenance summary');
      await provenance.focus();
      await provenance.press('Enter');
      await expect(page.locator('#tplRootPath')).toHaveText(templateScene.templates_root);
      expect((await contrast(page.locator('#tplRootPath'))).ratio).toBeGreaterThanOrEqual(4.5);
      await reachable(page.locator('#tplOpenLibraryBtn'), width < 992 ? 44 : 32);
      await provenance.press('Enter');

      const search = page.locator('#tplSearch');
      await search.fill('no matching template');
      await expect(rows).toHaveCount(0);
      await expect(page.locator('#tplList')).toContainText('No templates match');
      await search.fill('');
      const longChip = page
        .locator('.tag-filter-bar-chip')
        .filter({ hasText: templateScene.templates[0].tags[1] });
      for (const chip of await page.locator('.tag-filter-bar-chip').all()) {
        await reachable(chip, width < 992 ? 44 : 32);
      }
      const chipGeometry = await longChip.evaluate(node => ({
        width: node.clientWidth,
        content: node.scrollWidth
      }));
      expect(chipGeometry.content).toBeLessThanOrEqual(chipGeometry.width + 1);
      await longChip.focus();
      await longChip.press('Enter');
      await expect(longChip).toBeFocused();
      await expect(longChip).toHaveAttribute('aria-pressed', 'true');
      await expect(rows).toHaveCount(1);
      expect((await contrast(longChip)).ratio).toBeGreaterThanOrEqual(4.5);
      const clear = page.locator('.tag-filter-bar-clear');
      await clear.focus();
      await clear.press('Enter');
      await expect(page.locator('.tag-filter-bar-chip').first()).toBeFocused();
      await expect(rows).toHaveCount(2);
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'templates-library', {
        fixture: 'fictional library and quest reads; no setup or writes'
      });

      await rows.first().focus();
      await rows.first().press('Enter');
      const heading = page.locator('#tplDetailName');
      await expect(heading).toHaveText(templateScene.templates[0].name);
      await expect(page.locator('#tplQuestHeading')).toHaveText(templateQuestScene.quests[0].title);
      await expect(page.locator('#tplQuestOwnership')).toContainText(
        'plugin-owned declaration · read-only'
      );
      await expect(page.locator('#tplQuestOpen')).toHaveAttribute(
        'href',
        '/?setup=quest&plugin=fictional-layout-plugin&quest=fictional_setup'
      );
      await expect(page.locator('#tplQuestOpen')).toHaveJSProperty('tagName', 'A');
      await expect(page.locator('#tplSaveBtn')).toBeDisabled();
      await expect(rows.first()).toHaveAttribute('aria-pressed', 'true');
      if (width < 992) {
        await expect(heading).toBeFocused();
        const position = await heading.evaluate(node => ({
          top: node.getBoundingClientRect().top,
          navbar: parseFloat(
            getComputedStyle(document.documentElement).getPropertyValue('--navbar-offset')
          )
        }));
        expect(position.top).toBeGreaterThanOrEqual(position.navbar);
        expect(position.top).toBeLessThan(300);
      } else await expect(rows.first()).toBeFocused();
      for (const selector of ['#tplDetailId', '#tplQuestOwnership', '#tplDetailBuiltinBadge']) {
        expect((await contrast(page.locator(selector))).ratio).toBeGreaterThanOrEqual(4.5);
      }
      await heading.scrollIntoViewIfNeeded();
      await capture(page, info, 'templates-detail', {
        fixture: 'fictional plugin-supplied labels and native setup URL; no launch'
      });
      await page.locator('#tplTabAgents').click();
      await expect(page.locator('#tplAgentsReadOnlyNotice')).toBeVisible();
      const card = page.locator('.tpl-agent-card').first();
      await expect(card).toHaveAttribute('draggable', 'false');
      await expect(card).toHaveCSS('background-image', 'none');
      await expect(card).toHaveCSS('box-shadow', 'none');
      await expect(page.locator('.tpl-agent-display-name')).toHaveText(
        templateScene.templates[0].agents[0].name
      );
      if (width < 992) {
        await page.locator('#tplBackToLibrary').click();
        await expect(rows.first()).toBeFocused();
        await rows.first().press('Enter');
        await expect(heading).toBeFocused();
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        width + 1
      );

      const create = page.locator('#tplCreateBtn');
      await create.click();
      await expect(page.locator('#tplNameModalInput')).toBeFocused();
      await page.locator('#tplNameModalConfirm').click();
      await expect(page.locator('#tplNameModal .toast-message')).toContainText('name');
      const dialog = await page.locator('#tplNameModal .modal-content').boundingBox();
      expect(dialog!.x).toBeGreaterThanOrEqual(0);
      expect(dialog!.x + dialog!.width).toBeLessThanOrEqual(width);
      expect(dialog!.y + dialog!.height).toBeLessThanOrEqual(900);
      await capture(page, info, 'templates-create', {
        fixture: 'real client-side validation, no write'
      });
      await page.locator('#tplNameModal [data-bs-dismiss="modal"]').last().click();
      await expect(create).toBeFocused();
      await provenance.click();
      const settingsLink = page.locator('.tpl-provenance a');
      await settingsLink.focus();
      await settingsLink.press('Enter');
      await expect(page).toHaveURL(/\/settings#templates$/);
      expect(writes).toEqual([]);
    });
  }

  test(`Templates file draft survives cancelled selection and discards only after confirmation: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page);
    await readFixture(page, '**/api/project-templates', templateScene);
    await readFixture(page, '**/api/setup-quests', templateQuestScene);
    const local = templateScene.templates[1];
    const path =
      'a-long-folder-for-provenance/a-long-unbroken-file-name-that-must-remain-readable.md';
    await readFixture(page, `**/api/project-templates/${local.id}/setup-quest`, {
      eligibility: { eligible: false, missing: [] }
    });
    await readFixture(page, `**/api/project-templates/${local.id}/files`, {
      files: [{ path, type: 'file' }]
    });
    await readFixture(page, `**/api/project-templates/${local.id}/files/content?*`, {
      path,
      content: 'Fictional original file.'
    });
    await page.goto('/templates');
    const rows = page.locator('#tplList button');
    await rows.last().click();
    await page.locator('#tplTabFiles').click();
    await page.locator('#tplFileTree button').click();
    const editor = page.locator('#tplEditorTextarea');
    await expect(editor).toHaveValue('Fictional original file.');
    await editor.fill('Fictional unsaved draft.');
    await expect(page.locator('#tplEditorDirty')).toBeVisible();
    await page.locator('#tplBackToLibrary').click();
    await rows.first().click();
    const dialog = page.locator('#tplDirtyModal');
    await expect(dialog).toBeVisible();
    await expect(page.locator('#tplDetailName')).toHaveText(local.name);
    await expect(page.locator('#tplDirtyFile')).toHaveText(path);
    expect((await contrast(page.locator('#tplDirtyFile'))).ratio).toBeGreaterThanOrEqual(4.5);
    for (const button of await dialog.locator('.modal-footer button').all())
      await reachable(button, 44);
    await capture(page, info, 'templates-dirty', {
      fixture: 'fictional file GET; real unsaved editor; no writes'
    });
    await page.locator('#tplDirtyCancel').click();
    await expect(dialog).toBeHidden();
    await expect(rows.first()).toBeFocused();
    await expect(editor).toHaveValue('Fictional unsaved draft.');
    await rows.first().click();
    await expect(dialog).toBeVisible();
    await page.locator('#tplDirtyDiscard').click();
    await expect(dialog).toBeHidden();
    await expect(page.locator('#tplDetailName')).toHaveText(templateScene.templates[0].name);
    await expect(page.locator('#tplDetailName')).toBeFocused();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      391
    );
    expect(writes).toEqual([]);
  });

  test(`Templates empty library and failed read recover through Refresh: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 320);
    const writes = await guardReadTour(page);
    let unavailable = false;
    await page.route('**/api/project-templates', route => {
      if (route.request().method() !== 'GET') return route.fallback();
      return route.fulfill(
        unavailable
          ? { status: 503, json: { error: 'Fictional unavailable source' } }
          : { json: { templates: [] } }
      );
    });
    await readFixture(page, '**/api/setup-quests', { quests: [] });
    await page.goto('/templates');
    await expect(page.locator('#tplEmpty')).toBeVisible();
    await expect(page.locator('#tplDetailEmpty')).toBeHidden();
    await capture(page, info, 'templates-empty', { fixture: 'empty library read; no writes' });
    unavailable = true;
    await page.locator('#tplRefreshBtn').click();
    await expect(page.locator('#tplError')).toContainText('Could not load');
    await expect(page.locator('#tplEmpty')).toBeHidden();
    await capture(page, info, 'templates-error', { fixture: 'deliberate failed library GET' });
    unavailable = false;
    await page.locator('#tplRefreshBtn').click();
    await expect(page.locator('#tplError')).toBeHidden();
    await expect(page.locator('#tplEmpty')).toBeVisible();
    expect(writes).toEqual([]);
  });
}
