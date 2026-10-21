import { test, expect, type Page, type Locator } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import { installLocalCdn } from './helpers/offline-cdn';
import {
  prepareFolderCreationRuntime,
  abandonOpenBuild,
  currentBuild,
  resources,
  inbox,
  attachFirst
} from './helpers/folder-first-runtime';

// Actual Create Workspace modal, real services, one scripted model operation.
// Browser reflow/axe evidence is not native-picker or screen-reader certification.
// ./scripts/e2e.sh --port 8937 --env ORI_TEST_BUILD_CREATION=1 tests/workspace-folder-review.a11y.spec.ts -- --workers=1

test.describe.configure({ mode: 'serial' });
test.skip(process.env.ORI_TEST_BUILD_CREATION !== '1', 'Requires an isolated scripted-model demo');
let model: Awaited<ReturnType<typeof startJanitorBuildModel>>;
test.beforeAll(async ({ request }) => {
  model = await startJanitorBuildModel();
  await prepareFolderCreationRuntime(request);
});
test.afterAll(async () => {
  await model?.();
});
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
});

function barrier() {
  let release!: () => void;
  const promise = new Promise<void>(resolve => {
    release = resolve;
  });
  return { promise, release };
}

async function tabTo(page: Page, target: Locator) {
  await expect(target).toBeVisible();
  for (let n = 0; n < 100; n++) {
    if (await target.evaluate(node => node === document.activeElement)) return;
    await page.keyboard.press('Tab');
  }
  await expect(target, 'reachable by Tab inside the active dialog').toBeFocused();
}

async function typeIn(page: Page, target: Locator, value: string) {
  await tabTo(page, target);
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type(value);
}

async function checkReviewLayout(page: Page) {
  const layout = await page.locator('#addFolderModal').evaluate(modal => {
    const body = modal.querySelector('.workspace-build-folder-review__body') as HTMLElement;
    const footer = modal.querySelector('.workspace-build-folder-review__footer') as HTMLElement;
    const primary = [...modal.querySelectorAll<HTMLElement>('.modern-btn-primary')].find(
      node => node.getClientRects().length
    )!;
    const active = document.activeElement as HTMLElement;
    const bounds = (node: HTMLElement) => {
      const b = node.getBoundingClientRect();
      return { top: b.top, bottom: b.bottom, left: b.left, right: b.right };
    };
    const scrolls = [...modal.querySelectorAll<HTMLElement>('*')]
      .filter(
        node =>
          node.tagName !== 'TEXTAREA' &&
          node.clientHeight > 0 &&
          node.scrollHeight > node.clientHeight + 1 &&
          /auto|scroll/.test(getComputedStyle(node).overflowY)
      )
      .map(node => node.className);
    const controls = [
      ...modal.querySelectorAll<HTMLElement>('button, a, input, textarea, select, summary')
    ]
      .filter(
        node => node.getClientRects().length && getComputedStyle(node).visibility !== 'hidden'
      )
      .map(node => ({
        label: node.getAttribute('aria-label') || node.id || node.textContent,
        ...bounds(node)
      }));
    return {
      width: innerWidth,
      height: innerHeight,
      body: bounds(body),
      footer: bounds(footer),
      primary: bounds(primary),
      active: bounds(active),
      activeInside: body.contains(active),
      overflow:
        body.scrollWidth > body.clientWidth + 1 ||
        document.documentElement.scrollWidth > innerWidth + 1,
      scrolls,
      controls
    };
  });
  expect(layout.overflow).toBe(false);
  expect(layout.scrolls.length).toBeLessThanOrEqual(1);
  expect(layout.scrolls.every(name => name === 'workspace-build-folder-review__body')).toBe(true);
  expect(layout.primary.top).toBeGreaterThanOrEqual(layout.footer.top);
  expect(layout.primary.bottom).toBeLessThanOrEqual(layout.height);
  for (const control of layout.controls) {
    expect(control.left, String(control.label)).toBeGreaterThanOrEqual(0);
    expect(control.right, String(control.label)).toBeLessThanOrEqual(layout.width);
  }
  if (layout.activeInside) {
    expect(
      layout.active.top,
      'focused control is not above the scroll area'
    ).toBeGreaterThanOrEqual(layout.body.top);
    expect(layout.active.bottom, 'footer does not cover the focused control').toBeLessThanOrEqual(
      layout.body.bottom
    );
  }
  await expect(page.locator('#addFolderModal .modern-btn-primary:visible')).toHaveCount(1);
}

async function auditModal(page: Page) {
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  const scan = await page.evaluate(async () => {
    const result = await (window as any).axe.run('#addFolderModal', {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
    });
    return {
      violations: result.violations,
      contrast: result.passes.find((r: any) => r.id === 'color-contrast')?.nodes.length || 0
    };
  });
  expect(scan.violations).toEqual([]);
  expect(scan.contrast).toBeGreaterThan(0);
}

for (const theme of ['light', 'dark']) {
  for (const size of [
    { width: 1280, height: 900 },
    { width: 390, height: 844 },
    { width: 640, height: 450 }
  ]) {
    test(`keyboard folder creation, reduced motion and reflow ${theme} ${size.width}x${size.height}`, async ({
      page,
      request
    }, testInfo) => {
      test.setTimeout(90_000);
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.setViewportSize(size);
      if (size.width === 640) {
        const client = await page.context().newCDPSession(page);
        await client.send('Emulation.setDeviceMetricsOverride', {
          ...size,
          deviceScaleFactor: 2,
          mobile: false,
          screenWidth: 1280,
          screenHeight: 900
        });
      }
      await abandonOpenBuild(request);
      await request.post('/api/onboarding/skip');
      const root = inbox(
        'Downloads for a synthetic project with a deliberately long folder identity'
      );
      const before = await resources(request);
      const calls = model.calls();
      const held = model.hold();
      const preparing = barrier(),
        releasePreparation = barrier();
      await page.route('**/api/workspaces/build-sessions/*/janitor-prepare', async route => {
        preparing.release();
        await releasePreparation.promise;
        await route.continue().catch(() => {}); // cleanup may close a failing case's page
      });
      try {
        await page.goto('/');
        await tabTo(page, page.locator('#cockpitCreateWorkspaceBtn'));
        await page.keyboard.press('Enter');
        await expect(page.locator('#workspaceBuildComposerInput')).toBeEnabled();
        await tabTo(page, page.getByRole('button', { name: 'Add folder', exact: true }));
        await page.keyboard.press('Enter');
        await expect(page.locator('#workspaceBuildPane')).toContainText(
          'shared with your configured model'
        );
        await typeIn(page, page.getByPlaceholder('~/Projects/my-app'), root);
        await tabTo(page, page.getByRole('button', { name: 'Use this path', exact: true }));
        await page.keyboard.press('Enter');
        await held.started;
        const primary = page.locator('#workspaceBuildPlanReviewSetup');
        await expect(primary).toBeDisabled();
        await expect(page.locator('#workspaceBuildFolderStatus')).toContainText('Suggesting');
        await expect(page.locator('#workspaceBuildFolderStatus')).toHaveAttribute(
          'aria-live',
          'polite'
        );
        const name = (
          `Keyboard ${theme} ${size.width} ${Date.now()} ` + 'Long name '.repeat(6)
        ).slice(0, 80);
        const nameField = page.locator('#workspaceBuildPlanName');
        await typeIn(page, nameField, name);
        await page.screenshot({
          path: testInfo.outputPath(`folder-loading-${theme}-${size.width}.png`)
        });
        held.release();
        await expect(primary).toHaveText('Create & scan folder');
        await expect(primary).toBeEnabled();
        await expect(nameField).toHaveValue(name);
        await expect(nameField).toBeFocused();
        await tabTo(page, page.locator('#workspaceBuildFolderDetails summary'));
        await page.keyboard.press('Enter');
        const description = 'A deliberately long but editable description. '.repeat(8);
        await typeIn(page, page.locator('#workspaceBuildPlanDescription'), description);
        await typeIn(
          page,
          page.locator('#workspaceBuildPlanGoal'),
          'Keep this keyboard-edited goal and review metadata without moving source files.'
        );
        await expect(primary).toBeEnabled();
        const permission = page.locator('#workspaceBuildCreationReview');
        await tabTo(page, permission.locator('summary'));
        await page.keyboard.press('Enter');
        await expect(permission).toContainText(root);
        const monitoring = permission.getByRole('checkbox');
        await expect(monitoring).not.toBeChecked();
        await tabTo(page, monitoring);
        await page.keyboard.press('Space');
        await expect(primary).toBeEnabled();
        await expect(permission).toContainText('Daily scan at');
        await expect(monitoring).toBeFocused();
        await checkReviewLayout(page);
        await page.keyboard.press('Space');
        await expect(primary).toBeEnabled();
        await expect(monitoring).not.toBeChecked();
        await expect(monitoring).toBeFocused();
        await checkReviewLayout(page);
        await auditModal(page);
        await page.screenshot({
          path: testInfo.outputPath(`folder-permissions-${theme}-${size.width}.png`)
        });
        await tabTo(page, nameField);
        await checkReviewLayout(page);
        await page.screenshot({
          path: testInfo.outputPath(`folder-review-${theme}-${size.width}.png`)
        });
        expect(await resources(request)).toEqual(before);
        expect(existsSync(join(root, 'Filed'))).toBe(false);
        const build = await currentBuild(request);
        expect(build.draft.name).toBe(name);
        expect(build.draft.description).toBe(description.trim());
        await tabTo(page, primary);
        await page.keyboard.press('Enter');
        await preparing.promise;
        await expect(primary).toBeDisabled();
        await expect(primary).toHaveText('Preparing workspace…');
        await expect(page.locator('#workspaceBuildFolderProblem')).toHaveAttribute(
          'role',
          'status'
        );
        await page.screenshot({
          path: testInfo.outputPath(`folder-creating-${theme}-${size.width}.png`)
        });
        releasePreparation.release();
        await expect(page).toHaveURL(/panel=file-janitor/, { timeout: 20_000 });
        await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
        const final = (
          await (await request.get(`/api/workspaces/build-sessions/${build.id}`)).json()
        ).session;
        expect(final.draft.name).toBe(name);
        const workspaces = (await (await request.get('/api/workspaces')).json()).folders;
        expect(workspaces.filter((w: { name: string }) => w.name === name)).toHaveLength(1);
        expect(model.calls()).toBe(calls + 1);
        expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
      } finally {
        held.release();
        releasePreparation.release();
      }
    });
  }
}

test('picker cancellation and Escape return to the unchanged folder review', async ({
  page,
  request
}) => {
  const calls = model.calls();
  const root = inbox('Downloads');
  await attachFirst(page, request, root);
  const primary = page.locator('#workspaceBuildPlanReviewSetup');
  await expect(primary).toHaveText('Create & scan folder');
  await expect(primary).toBeEnabled();
  const saved = await currentBuild(request);
  const before = await resources(request);
  // Injectable picker boundary only: this does not exercise macOS's native UI.
  await page.route('**/api/workspaces/build-sessions/folder-choices', route =>
    route.fulfill({
      json: { available: true, chips: [], picker_available: true }
    })
  );
  await page.route('**/api/workspaces/build-sessions/*/folder', async route => {
    expect(route.request().postDataJSON().mode).toBe('picker');
    const current = await (await request.get(`/api/workspaces/build-sessions/${saved.id}`)).json();
    await route.fulfill({ json: { ...current, cancelled: true } });
  });
  const change = page.getByRole('button', { name: 'Change folder', exact: true });
  await tabTo(page, change);
  await page.keyboard.press('Enter');
  await tabTo(page, page.getByRole('button', { name: 'Pick another folder…', exact: true }));
  await page.keyboard.press('Enter');
  await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
  await expect(change).toBeFocused();
  await expect(primary).toBeEnabled();
  await page.keyboard.press('Enter'); // open Change folder again
  await expect(page.locator('#workspaceBuildFolderChooser')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
  await expect(change).toBeFocused();
  expect((await currentBuild(request)).folder.observation.id).toBe(saved.folder.observation.id);
  expect(model.calls()).toBe(calls + 1);
  expect(await resources(request)).toEqual(before);
});

test('same-named generic folders show the exact local path and replacement returns to review', async ({
  page,
  request
}, testInfo) => {
  model.scenario({ mode: 'ambiguous' });
  try {
    const roots = [inbox('Same folder name'), inbox('Same folder name')];
    const before = await resources(request),
      calls = model.calls();
    await attachFirst(page, request, roots[0]);
    await expect(page.locator('#workspaceBuildFolderQuestion')).toContainText('Organize files');
    const details = page.locator('#workspaceBuildFolderPathDetails');
    await details.locator('summary').click();
    await expect(details).toContainText(roots[0]);
    await expect(page.locator('[id=workspaceBuildFolderPath]')).toHaveCount(1);
    await expect(page.locator('[id=workspaceBuildFolderReviewPath]')).toHaveCount(1);
    await page.getByRole('button', { name: 'Change folder', exact: true }).click();
    await page.getByPlaceholder('~/Projects/my-app').fill(roots[1]);
    await page.getByRole('button', { name: 'Use this path', exact: true }).click();
    await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
    await expect(details).toContainText(roots[1]);
    await expect(details).not.toContainText(roots[0]);
    await expect.poll(() => model.calls()).toBe(calls + 2);
    await page.screenshot({ path: testInfo.outputPath('folder-same-name-details.png') });
    expect(await resources(request)).toEqual(before);
  } finally {
    model.scenario();
  }
});

for (const stage of ['attachment', 'permissions']) {
  test(`close before the ${stage} response, then Resume without replay`, async ({
    page,
    request
  }) => {
    const ready = barrier(),
      release = barrier(),
      returned = barrier();
    const routePath = stage === 'attachment' ? 'folder' : 'janitor-review';
    let intercepted = false;
    await page.route(`**/api/workspaces/build-sessions/*/${routePath}`, async route => {
      if (intercepted) {
        await route.continue();
        return;
      }
      intercepted = true;
      const response = await route.fetch(); // real read/observation completes; hold its delivery
      ready.release();
      await release.promise;
      await route.fulfill({ response }).catch(() => {});
      returned.release();
    });
    const before = await resources(request),
      calls = model.calls();
    try {
      await attachFirst(page, request, inbox('Downloads'));
      await ready.promise;
      const saved = await currentBuild(request);
      await page.getByRole('button', { name: 'Close create workspace', exact: true }).click();
      await expect(page.locator('#addFolderModal')).toBeHidden();
      release.release();
      await returned.promise;
      await page.locator('#cockpitCreateWorkspaceBtn').click();
      await page.getByRole('button', { name: 'Resume', exact: true }).click();
      await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
      if (stage === 'permissions') {
        await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeEnabled();
        await expect(page.locator('#workspaceBuildPlanName')).toHaveValue(saved.draft.name);
      } else {
        await expect(
          page.getByRole('button', { name: 'Retry suggestion', exact: true })
        ).toBeVisible();
      }
      const resumed = await currentBuild(request);
      expect(resumed.id).toBe(saved.id);
      expect(resumed.folder.observation.id).toBe(saved.folder.observation.id);
      expect(resumed.create_now || false).toBe(false);
      expect(model.calls()).toBe(calls + (stage === 'permissions' ? 1 : 0));
      expect(await resources(request)).toEqual(before);
    } finally {
      release.release();
    }
  });
}

for (const width of [390, 640]) {
  test(`permission failure and save conflict keep narrow controls reachable ${width}`, async ({
    page,
    request
  }, testInfo) => {
    test.setTimeout(60_000);
    await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width, height: width === 390 ? 844 : 450 });
    let reads = 0;
    await page.route('**/api/workspaces/build-sessions/*/janitor-review', async route => {
      if (++reads === 1)
        await route.fulfill({
          status: 503,
          json: {
            error: 'Permissions are temporarily unavailable. Refresh permissions to try again.'
          }
        });
      else await route.continue();
    });
    const before = await resources(request),
      calls = model.calls();
    await attachFirst(page, request, inbox('Downloads'));
    const primary = page.locator('#workspaceBuildPlanReviewSetup');
    await expect(primary).toHaveText('Refresh permissions');
    await expect(primary).toBeEnabled();
    await tabTo(page, primary);
    await checkReviewLayout(page);
    await page.screenshot({ path: testInfo.outputPath(`folder-error-dark-${width}.png`) });
    expect(reads).toBe(1);
    await page.keyboard.press('Enter');
    await expect(primary).toHaveText('Create & scan folder');
    await expect(primary).toBeEnabled();
    const build = await currentBuild(request);
    const other = await request.patch(`/api/workspaces/build-sessions/${build.id}/draft`, {
      data: {
        version: build.version,
        draft: { ...build.draft, name: 'Saved in another tab' },
        team_state: build.team_state,
        step: 4,
        edited_fields: ['name']
      }
    });
    expect(other.ok(), await other.text()).toBeTruthy();
    const local = `My kept local name ${width}`;
    const name = page.locator('#workspaceBuildPlanName');
    await typeIn(page, name, local);
    const save = page.getByRole('button', { name: 'Save my draft', exact: true });
    await expect(save).toBeVisible();
    await expect(primary).toBeHidden();
    await expect(save).toBeEnabled();
    await expect(name).toHaveValue(local);
    await expect(name).toBeFocused();
    await checkReviewLayout(page);
    await auditModal(page);
    await page.screenshot({ path: testInfo.outputPath(`folder-conflict-dark-${width}.png`) });
    await page.keyboard.press('Escape');
    await expect(page.locator('#addFolderModal')).toBeVisible();
    expect((await currentBuild(request)).draft.name).toBe('Saved in another tab');
    await expect(name).toHaveValue(local);
    await tabTo(page, save);
    await page.keyboard.press('Enter');
    await expect(primary).toBeEnabled();
    expect((await currentBuild(request)).draft.name).toBe(local);
    expect(
      await page
        .locator('#addFolderModal')
        .evaluate(modal => modal.contains(document.activeElement))
    ).toBe(true);
    expect(model.calls()).toBe(calls + 1);
    expect(await resources(request)).toEqual(before);
  });
}
