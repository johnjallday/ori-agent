import { test, expect, type Locator } from '@playwright/test';
import {
  assertSandbox,
  capture,
  preparePage,
  reachable,
  guardReadTour,
  readFixture,
  contrast
} from './helpers/ui-consistency';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { themes, skillScene, pluginScene } from './fixtures/ui-consistency-scenes';

async function buttonStyle(button: Locator) {
  return button.evaluate(node => {
    const s = getComputedStyle(node);
    return {
      height: node.getBoundingClientRect().height,
      fontSize: s.fontSize,
      radius: s.borderRadius,
      color: s.color,
      background: s.backgroundColor,
      border: s.borderColor,
      shadow: s.boxShadow,
      transform: s.transform
    };
  });
}

test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
});

for (const theme of themes) {
  test(`Bootstrap and modern secondary buttons share metrics and states: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme);
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    const writes = await guardReadTour(page);
    await page.goto('/models');
    await expect(page.locator('#modelsContent')).toBeVisible();
    const bootstrap = page.locator('#refreshModelsBtn');
    const base = await buttonStyle(bootstrap);
    expect(base.height).toBe(40);
    expect(base.fontSize).toBe('14px');
    expect(base.radius).toBe('8px');
    await bootstrap.hover();
    await expect
      .poll(async () => (await buttonStyle(bootstrap)).background)
      .not.toBe(base.background);
    // Poll for the settled state, not an intermediate transition color.
    await page.waitForTimeout(180);
    const hovered = await buttonStyle(bootstrap);
    await page.goto('/skills');
    await page.locator('#skillsCreateBtn').click();
    const cancel = page
      .locator('#skillsEditorModal')
      .getByRole('button', { name: 'Cancel', exact: true });
    await expect(cancel).toBeVisible();
    expect(await buttonStyle(cancel)).toEqual(base);
    await cancel.hover();
    await expect.poll(() => buttonStyle(cancel)).toEqual(hovered);
    // Enter keyboard modality, then focus the comparison target. WebKit on
    // macOS may omit buttons from plain Tab depending on OS full-keyboard
    // access; this checks the shared focus treatment, not that OS preference.
    await page.keyboard.press('Tab');
    await cancel.focus();
    await expect(cancel).toBeFocused();
    const ring = await cancel.evaluate(node => ({
      width: getComputedStyle(node).outlineWidth,
      color: getComputedStyle(node).outlineColor,
      token: getComputedStyle(document.documentElement).getPropertyValue('--focus-color').trim()
    }));
    expect(ring.width).toBe('2px');
    await capture(page, info, 'button-parity', { base, hovered, ring });
    expect(writes).toEqual([]);
  });

  test(`workspace creation feedback stays inside the shared modal: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 390);
    // project_templates.go builds this synthetic roster read-only; unlike
    // template-agent-create it persists no agent/workspace and calls no model.
    const planPath = '/api/workspaces/template-agent-plan';
    const writes = await guardReadTour(page, [planPath]);
    await page.route(`**${planPath}`, async route => {
      expect(route.request().method()).toBe('POST');
      expect(route.request().postDataJSON()).toEqual({ blank: true });
      await route.fallback();
    });
    await page.goto('/');
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'ready');
    // Exercise the mounted wizard through Bootstrap, without creating a
    // workspace, starting an assistant, or replacing its markup.
    await page.evaluate(() => {
      const win = window as any;
      win.bootstrap.Modal.getOrCreateInstance(document.getElementById('addFolderModal')).show();
    });
    const modal = page.locator('#addFolderModal');
    await expect(modal).toBeVisible();
    await page.evaluate(() =>
      (window as any).Toast.error('Fictional creation feedback; no workspace created.', {
        duration: 60000
      })
    );
    const feedbackBounds = await modal.locator('.toast-container--modal').boundingBox();
    expect(feedbackBounds!.x).toBeGreaterThanOrEqual(0);
    expect(feedbackBounds!.x + feedbackBounds!.width).toBeLessThanOrEqual(390);
    await reachable(modal.locator('.toast-close'), 44);
    const cancel = modal.locator('.modal-footer [data-bs-dismiss="modal"]');
    await reachable(cancel, 44);
    await capture(page, info, 'workspace-feedback-sentinel', {
      fixture: 'synthetic feedback only; no creation or execution'
    });
    await cancel.click();
    await expect(modal).toBeHidden();
    expect(writes).toEqual([]);
  });

  test(`assistant and vault shared-dialog sentinels: ${theme}`, async ({ page }, info) => {
    await preparePage(page, theme);
    // This endpoint only resolves context/relationship reads; no ask/send/run.
    const writes = await guardReadTour(page, ['/api/home-assistant/context']);
    await readFixture(page, /\/api\/personal-assistant$/, {
      personal_assistant: {
        state: 'active',
        state_version: 1,
        assistant_id: 'layout-assistant',
        display_name: 'Fictional layout assistant',
        next_action: 'ask',
        availability: { model: { status: 'not_configured', available: false } }
      }
    });
    await readFixture(page, /\/api\/vault\/vaults$/, {
      count: 1,
      vaults: [
        { id: 'layout-vault', name: 'Fictional vault', password_protected: true, record_count: 1 }
      ]
    });
    await readFixture(page, /\/api\/vault\/status(?:\?.*)?$/, {
      vault_id: 'layout-vault',
      vault_name: 'Fictional vault',
      available: true,
      locked: false,
      writable: true,
      password_protected: true,
      requires_passphrase: false,
      record_count: 1,
      secret_store: { backend: 'vault_password', available: true, writable: true, locked: false }
    });
    await readFixture(page, /\/api\/vault\/folders(?:\?.*)?$/, { folders: [] });
    await readFixture(page, /\/api\/vault\/records(?:\?.*)?$/, {
      records: [
        {
          id: 'layout-note',
          vault_id: 'layout-vault',
          type: 'personal_note',
          label: 'Fictional layout note',
          folder_path: '',
          workspace_id: '',
          tags: [],
          payload: { note: 'Fictional content, not a secret.' }
        }
      ]
    });
    await page.goto('/skills');
    const launcher = page.locator('#personalAssistantLauncher');
    await launcher.click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await reachable(page.locator('#personalAssistantClose'));
    await capture(page, info, 'assistant-sentinel', {
      reads: 'fictional hired/unconfigured identity; no provider call'
    });
    // The assistant is nonmodal. Open the real page action with the keyboard
    // and ensure it cannot cover the modal's actions.
    await page.locator('#skillsCreateBtn').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#skillsEditorModal')).toBeVisible();
    await reachable(page.locator('#skillsSaveBtn'), 40);
    await capture(page, info, 'assistant-modal-layer');
    await page
      .locator('#skillsEditorModal')
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    if (await page.locator('#personalAssistantPanel').isVisible())
      await page.locator('#personalAssistantClose').click();
    await page.locator('#vaultLauncherButton').click();
    const vault = page.locator('#vaultModal');
    await expect(vault).toBeVisible();
    await expect(page.locator('#vaultModalRecordsSummary')).toContainText('1 of 1 item');
    await expect(vault).toContainText('Fictional layout note');
    const close = vault.locator('.vault-modal-close');
    await reachable(close);
    await capture(page, info, 'vault-dialog-sentinel', {
      reads: 'fictional unlocked record; no vault write or unlock claim'
    });
    await close.click();
    await expect(vault).toBeHidden();
    expect(writes).toEqual([]);
  });

  test(`Plugins real fixture preview, install disabled and update cancellation: ${theme}`, async ({
    page,
    request
  }, info) => {
    const sandbox = await assertSandbox(request);
    const name = `ui-consistency-${crypto.randomUUID()}`;
    const source = mkdtempSync(join(sandbox, 'ui-plugin-candidate-'));
    cpSync(resolve('examples/plugins/workspace-surface-demo'), source, { recursive: true });
    const manifests = ['.claude-plugin', '.codex-plugin', '.ori-plugin'].map(dir =>
      join(source, dir, 'plugin.json')
    );
    const setVersion = (version: string) => {
      for (const path of manifests) {
        const manifest = JSON.parse(readFileSync(path, 'utf8'));
        manifest.name = name;
        manifest.version = version;
        writeFileSync(path, JSON.stringify(manifest, null, 2), { mode: 0o600 });
      }
    };
    setVersion('0.1.0');
    const blueprintPath = join(source, 'blueprints/demo-workspace/template.json');
    const blueprint = JSON.parse(readFileSync(blueprintPath, 'utf8'));
    expect(blueprint.runtime_requirements.requirements[0].adapter).toBe(
      'plugin:workspace-surface-demo:demo-runtime'
    );
    blueprint.runtime_requirements.requirements[0].adapter = `plugin:${name}:demo-runtime`;
    writeFileSync(blueprintPath, JSON.stringify(blueprint, null, 2), { mode: 0o600 });
    const installed = async () => {
      const response = await request.get('/api/plugins');
      expect(response.ok()).toBeTruthy();
      return (await response.json()).plugins.find(
        (plugin: { name: string }) => plugin.name === name
      );
    };
    expect(await installed()).toBeUndefined();
    await preparePage(page, theme, 390);
    const updatePath = `/api/plugins/${name}/update`;
    const writes = await guardReadTour(page, ['/api/plugins/install', updatePath]);
    let allowConfirmation = false;
    let confirmations = 0;
    await page.route('**/api/plugins/**', async route => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      if (req.method() === 'POST' && path === '/api/plugins/install') {
        const body = req.postDataJSON();
        expect(body.source).toBe(source);
        expect(body).toEqual({ source, format: 'claude', confirm: body.confirm === true });
        if (body.confirm) {
          expect(allowConfirmation, 'no installation before the reviewed confirmation').toBe(true);
          confirmations += 1;
          expect(confirmations).toBe(1);
        } else expect(body.confirm).toBe(false);
      } else if (req.method() === 'POST' && path === updatePath) {
        expect(req.postDataJSON()).toEqual({ confirm: false });
      }
      await route.fallback();
    });
    try {
      await page.goto('/plugins');
      await page.locator('#pluginInstallSection > summary').click();
      await page.locator('#pluginSource').fill(source);
      await page.locator('#pluginFormat').selectOption('claude');
      await page.locator('#pluginPreviewBtn').click();
      await expect(page.locator('#pluginTrust')).toBeVisible();
      await expect(page.locator('#pluginTrust')).toContainText('Surface Demo');
      await expect(page.locator('#pluginTrustTitle')).toBeFocused();
      await reachable(page.locator('#pluginTrustConfirm'), 44);
      await capture(page, info, 'plugins-trust', {
        fixture: 'repository surface example, renamed disposable candidate',
        name
      });
      await page
        .locator('#pluginTrust')
        .getByRole('button', { name: 'Cancel', exact: true })
        .click();
      await expect(page.locator('#pluginTrust')).toBeHidden();
      await expect(page.locator('#pluginPreviewBtn')).toBeFocused();
      expect(await installed()).toBeUndefined();
      expect(confirmations).toBe(0);
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.locator('#pluginPreviewBtn').click();
      await expect(page.locator('#pluginTrust')).toBeVisible();
      await capture(page, info, 'plugins-trust', {
        fixture: 'real local preview; no execution',
        name
      });
      allowConfirmation = true;
      await page.locator('#pluginTrustConfirm').click();
      const update = page.locator(`[data-plugin-action="update"][data-plugin-name="${name}"]`);
      await expect(update).toBeVisible();
      expect((await installed()).enabled).toBe(false);
      expect(confirmations).toBe(1);
      // Change only the disposable candidate. Added skill requires a fresh
      // trust review; cancellation must leave the installed 0.1.0 untouched.
      setVersion('0.2.0');
      const skillDir = join(source, 'skills', 'ui-layout-verification');
      mkdirSync(skillDir, { recursive: true, mode: 0o750 });
      writeFileSync(
        join(skillDir, 'SKILL.md'),
        '---\nname: ui-layout-verification\ndescription: Harmless layout fixture; never execute.\n---\nDescribe this fictional layout.\n',
        { mode: 0o600 }
      );
      await update.click();
      const modal = page.locator('#pluginUpdateModal');
      await expect(modal).toBeVisible();
      await expect(page.locator('#pluginUpdateTrustBody')).toContainText('ui-layout-verification');
      expect(
        (
          await contrast(
            modal.locator('.plugin-trust-section.is-warning .plugin-trust-section-title').first()
          )
        ).ratio
      ).toBeGreaterThanOrEqual(4.5);
      await reachable(page.locator('#pluginUpdateConfirm'), 40);
      await capture(page, info, 'plugins-update-review', {
        name,
        fixture: 'real local changed-component preview'
      });
      await page.setViewportSize({ width: 390, height: 900 });
      const layers = await page.evaluate(() => ({
        toast: Number(getComputedStyle(document.getElementById('toastContainer')!).zIndex),
        modal: Number(getComputedStyle(document.getElementById('pluginUpdateModal')!).zIndex)
      }));
      expect(layers.toast).toBeLessThan(layers.modal);
      // A synthetic component message, not a simulated successful API write or
      // a claim that this real update failed. New modal feedback stays visible
      // in its layout while the earlier install notice cannot cover actions.
      await page.evaluate(() =>
        (
          window as unknown as {
            Toast: { error: (message: string, options: { duration: number }) => void };
          }
        ).Toast.error('Fictional feedback to verify dialog action reachability.', {
          duration: 30000
        })
      );
      await expect(modal.locator('.toast-container--modal')).toContainText('Fictional feedback');
      await reachable(modal.locator('.toast-close'), 44);
      const notification = await modal.locator('.toast').evaluate(node => ({
        bottom: node.getBoundingClientRect().bottom,
        messageBottom: node.querySelector('.toast-message')!.getBoundingClientRect().bottom
      }));
      expect(notification.messageBottom).toBeLessThanOrEqual(notification.bottom);
      const bounds = await modal.locator('.modal-content').evaluate(node => {
        const rect = node.getBoundingClientRect();
        return { top: rect.top, bottom: rect.bottom, viewport: innerHeight };
      });
      expect(bounds.top, JSON.stringify(bounds)).toBeGreaterThanOrEqual(0);
      expect(bounds.bottom, JSON.stringify(bounds)).toBeLessThanOrEqual(bounds.viewport);
      await reachable(page.locator('#pluginUpdateConfirm'), 44);
      await capture(page, info, 'plugins-update-review', {
        name,
        fixture: 'no update confirmed; synthetic modal error notification',
        layers,
        notification,
        bounds
      });
      await modal.locator('.toast-close').focus();
      await page.keyboard.press('Enter');
      await expect(modal.locator('.toast')).toHaveCount(0);
      await expect(modal).toBeFocused();
      await expect(modal.locator('.toast-container--modal')).toBeHidden();
      await modal.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(modal).toBeHidden();
      await expect(update).toBeFocused();
      const record = await installed();
      expect(record.version).toBe('0.1.0');
      expect(record.enabled).toBe(false);
      expect(writes).toEqual([]);
    } finally {
      await assertSandbox(request);
      if (await installed()) {
        const removal = await request.delete(`/api/plugins/${name}`);
        expect(removal.ok(), await removal.text()).toBeTruthy();
      }
      rmSync(source, { recursive: true, force: true });
    }
  });

  for (const width of [320, 390, 1440]) {
    test(`static sections and editor surface ${theme} ${width}px`, async ({ page }, info) => {
      await preparePage(page, theme, width);
      await page.emulateMedia({ reducedMotion: 'no-preference' });
      const writes = await guardReadTour(page);
      await page.goto('/skills');
      await expect(page.locator('#skillsList')).not.toContainText('Loading skills');
      const card = page.locator('section[aria-labelledby="skillsAvailableTitle"]');
      const style = () =>
        card.evaluate(node => {
          const s = getComputedStyle(node);
          return {
            transform: s.transform,
            shadow: s.boxShadow,
            background: s.backgroundColor,
            cursor: s.cursor
          };
        });
      const before = await style();
      expect(before.transform).toBe('none');
      expect(before.shadow).toBe('none');
      await card.hover({ position: { x: 8, y: 8 } });
      await expect.poll(style).toEqual(before);
      await page.locator('#skillsCreateBtn').click();
      const modal = page.locator('#skillsEditorModal');
      await expect(modal).toBeVisible();
      const name = page.locator('#skillNameInput');
      await expect(name).toBeVisible();
      await expect(name).toHaveCSS(
        'border-color',
        theme === 'dark' ? 'rgb(115, 121, 133)' : 'rgb(118, 125, 135)'
      );
      await expect(page.locator('#skillsEditorError')).toBeHidden();
      const geometry = await reachable(
        modal.getByRole('button', { name: 'Save Skill', exact: true })
      );
      await reachable(modal.getByRole('button', { name: 'Cancel', exact: true }));
      const surface = await modal.locator('.modal-content').evaluate(node => {
        const s = getComputedStyle(node);
        return {
          background: s.backgroundColor,
          filter: s.backdropFilter,
          height: node.getBoundingClientRect().height,
          viewport: innerHeight
        };
      });
      expect(surface.background).toMatch(/^rgb\(/); // Opaque, not faded glass over text.
      expect(surface.filter).toBe('none');
      expect(surface.height).toBeLessThanOrEqual(surface.viewport);
      await capture(page, info, 'skills-editor-surface', { geometry, surface });
      await page.locator('#skillsSaveBtn').click();
      await expect(name).toBeFocused();
      expect(await name.evaluate(node => (node as HTMLInputElement).validationMessage)).not.toBe(
        ''
      );
      await expect(name).toHaveCSS(
        'border-color',
        theme === 'dark' ? 'rgb(255, 174, 174)' : 'rgb(162, 29, 43)'
      );
      await capture(page, info, 'skills-editor-invalid');
      await modal.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(modal).toBeHidden();
      await expect(page.locator('#skillsCreateBtn')).toBeFocused();
      expect(writes).toEqual([]);
    });

    test(`Skills populated filters, details, import and error ${theme} ${width}px`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      await page.emulateMedia({ reducedMotion: 'no-preference' });
      const writes = await guardReadTour(page);
      await readFixture(page, /\/api\/skills(?:\?.*)?$/, skillScene);
      await readFixture(page, /\/api\/skills\/fictional-long-skill[^/]*(?:\?.*)?$/, {
        prompt: 'Fictional layout prompt. Never execute.'
      });
      await readFixture(page, '**/api/skills/import/candidates', {
        should_show: false,
        candidates: [
          {
            name: 'fictional-import-candidate-with-a-long-name',
            description: 'Fictional preview only.'
          }
        ]
      });
      await page.goto('/skills');
      await expect(page.locator('#skillsCount')).toHaveText('2');
      for (const id of [
        'skillsCreateBtn',
        'skillsDiscoverBtn',
        'skillsAgentSelect',
        'skillsSearch',
        'skillsRefreshBtn'
      ]) {
        await reachable(page.locator(`#${id}`), width < 769 ? 44 : 32);
      }
      await page.locator('[data-source="repo"]').click();
      await expect(page.locator('[data-source="repo"]')).toHaveAttribute('aria-pressed', 'true');
      expect((await contrast(page.locator('[data-source="repo"]'))).ratio).toBeGreaterThanOrEqual(
        4.5
      );
      await expect(page.locator('#skillsList [data-action="run"]')).toBeDisabled();
      for (const badge of await page.locator('#skillsList .badge').all()) {
        expect((await contrast(badge)).ratio).toBeGreaterThanOrEqual(4.5);
      }
      const card = page.locator('#skillsList .ui-section');
      await card.hover({ position: { x: 8, y: 8 } });
      await expect(card).toHaveCSS('transform', 'none');
      await expect(card).toHaveCSS('box-shadow', 'none');
      await capture(page, info, 'skills-populated', { reads: 'fictional skills' });
      const details = page.locator('#skillsList [data-action="details"]');
      await details.click();
      await expect(page.locator('#skillsDetailsTitle')).toHaveText(skillScene.skills[0].name);
      await expect(page.locator('#skillsDetailsPrompt')).toContainText('Fictional layout prompt');
      await reachable(
        page
          .locator('#skillsDetailsModal .modal-footer')
          .getByRole('button', { name: 'Close', exact: true }),
        width < 769 ? 44 : 32
      );
      await capture(page, info, 'skills-details');
      await page
        .locator('#skillsDetailsModal .modal-footer')
        .getByRole('button', { name: 'Close', exact: true })
        .click();
      await expect(details).toBeFocused();
      await page.locator('#skillsSearch').fill('no-matching-fictional-skill');
      await expect(page.locator('#skillsCount')).toHaveText('0');
      await expect(page.locator('#skillsList')).toContainText('No skills');
      await capture(page, info, 'skills-empty');
      await page.locator('#skillsActionsMenuBtn').click();
      await page.locator('#skillsImportMenuItem').click();
      const importButton = page.locator('[data-import-action="import"]');
      await expect(importButton).toBeDisabled();
      await page.locator('[data-import-name]').check();
      await expect(importButton).toBeEnabled();
      await reachable(importButton, width < 769 ? 44 : 32);
      await reachable(page.locator('[data-import-action="dismiss"]'), width < 769 ? 44 : 32);
      await capture(page, info, 'skills-import-preview', {
        reads: 'fictional candidates; no import submitted'
      });
      let releaseInstalled!: () => void;
      const installedReady = new Promise<void>(resolve => {
        releaseInstalled = resolve;
      });
      await page.route('**/api/skills/marketplace/installed', async route => {
        if (route.request().method() !== 'GET') return route.fallback();
        await installedReady;
        await route.fulfill({ json: { skills: [] } });
      });
      await page.locator('#skillsDiscoverBtn').click();
      const marketplace = page.locator('#skillsMarketplaceModal');
      await expect(marketplace).toBeVisible();
      await expect(page.locator('#skillsMarketplaceSearchBtn')).toBeDisabled();
      await expect(page.locator('#skillsMarketplaceStatus')).toContainText('Loading');
      await capture(page, info, 'skills-marketplace-loading', {
        reads: 'delayed GET fixture, no external search/install'
      });
      releaseInstalled();
      await expect(page.locator('#skillsMarketplaceSearchBtn')).toBeEnabled();
      for (const [tab, pane] of [
        ['Install', 'Install'],
        ['Manage', 'Manage'],
        ['Search', 'Search']
      ]) {
        await page.locator(`#skillsMarketplace${tab}Tab`).click();
        await expect(page.locator(`#skillsMarketplace${tab}Tab`)).toHaveAttribute(
          'aria-selected',
          'true'
        );
        await expect(page.locator(`#skillsMarketplace${pane}Pane`)).toBeVisible();
        await reachable(page.locator(`#skillsMarketplace${tab}Tab`), width < 769 ? 44 : 32);
      }
      await capture(page, info, 'skills-marketplace-tabs');
      await expect(page.locator('#skillsMarketplaceSearchBtn')).toBeEnabled();
      await readFixture(page, '**/api/skills/marketplace/installed', {
        skills: [
          {
            name: 'fictional-long-installed-skill-for-layout-verification',
            package: 'fictional/example/long-installed-skill-for-layout-verification',
            location: '/fictional/skills/long-installed-skill-for-layout-verification'
          }
        ]
      });
      await page.locator('#skillsMarketplaceManageTab').click();
      const installedCard = page.locator('#skillsMarketplaceInstalledList .ui-section');
      await expect(installedCard).toContainText('fictional-long-installed-skill');
      const remove = installedCard.locator('[data-market-remove]');
      await expect(remove).toBeEnabled();
      const pane = page.locator('#skillsMarketplaceManagePane');
      await expect(pane).toHaveCSS('opacity', '1');
      await reachable(remove, width < 769 ? 44 : 32);
      expect((await contrast(remove)).ratio).toBeGreaterThanOrEqual(4.5);
      await installedCard.hover();
      await expect(installedCard).toHaveCSS('transform', 'none');
      await capture(page, info, 'skills-marketplace-installed', {
        reads: 'fictional installed GET only; no search, install, update or removal',
        dangerContrast: await contrast(remove)
      });
      await marketplace.getByRole('button', { name: 'Close', exact: true }).last().click();
      await expect(page.locator('#skillsDiscoverBtn')).toBeFocused();
      await page.route(/\/api\/skills(?:\?.*)?$/, route =>
        route.request().method() === 'GET'
          ? route.fulfill({ status: 503, json: { error: 'Fictional read failure' } })
          : route.fallback()
      );
      await page.locator('#skillsRefreshBtn').click();
      await expect(page.locator('#skillsList')).toContainText('Failed to load skills');
      await capture(page, info, 'skills-error');
      expect(writes).toEqual([]);
    });

    test(`Plugins installed-first, empty and error ${theme} ${width}px`, async ({ page }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      await readFixture(page, /\/api\/plugins$/, pluginScene);
      await readFixture(page, '**/api/plugins/updates', { checking: false, updates: [] });
      await readFixture(page, '**/api/plugins/workspace-list', { pending: [] });
      await page.goto('/plugins');
      await expect(page.locator('#pluginList')).toContainText(pluginScene.plugins[0].name);
      await expect(page.locator('#pluginUpdateNotice')).toHaveAttribute('data-state', 'empty');
      expect(
        (await contrast(page.locator('#pluginUpdateNoticeMarker'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      await expect(page.locator('#pluginInstallSection')).not.toHaveAttribute('open');
      const list = await page.locator('#pluginList').boundingBox();
      const install = await page.locator('#pluginInstallSection').boundingBox();
      expect(list!.y).toBeLessThan(install!.y);
      for (const control of await page.locator('#pluginList button').all()) {
        await reachable(control, width < 769 ? 44 : 32);
        expect((await contrast(control)).ratio).toBeGreaterThanOrEqual(4.5);
      }
      await capture(page, info, 'plugins-populated', {
        reads: 'fictional installed records; no enabled runtime'
      });
      await page.locator('#pluginInstallSection > summary').click();
      for (const id of ['pluginSource', 'pluginFormat', 'pluginPreviewBtn']) {
        await reachable(page.locator(`#${id}`), width < 769 ? 44 : 32);
      }
      await capture(page, info, 'plugins-install-fields');
      await readFixture(page, /\/api\/plugins$/, { plugins: [] });
      await page.getByRole('button', { name: 'Refresh', exact: true }).first().click();
      await expect(page.locator('#pluginList')).toHaveText('No plugins installed yet.');
      await capture(page, info, 'plugins-empty');
      await page.route(/\/api\/plugins$/, route =>
        route.request().method() === 'GET'
          ? route.fulfill({ status: 503, json: { error: 'Fictional read failure' } })
          : route.fallback()
      );
      await page.getByRole('button', { name: 'Refresh', exact: true }).first().click();
      await expect(page.locator('#pluginList')).toContainText('Failed to load plugins');
      await capture(page, info, 'plugins-error');
      expect(writes).toEqual([]);
    });
  }
}
