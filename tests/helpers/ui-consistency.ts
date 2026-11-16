import {
  expect,
  type APIRequestContext,
  type Locator,
  type Page,
  type TestInfo
} from '@playwright/test';
import { readFileSync, realpathSync, mkdirSync, writeFileSync, existsSync } from 'node:fs';
import { join, relative, resolve, isAbsolute } from 'node:path';
import { execFileSync } from 'node:child_process';
import { installLocalCdn } from './offline-cdn';
import { viewportHeight } from '../fixtures/ui-consistency-scenes';

/** Fail closed before writes: a localhost URL alone is not evidence of isolation. */
export async function assertSandbox(request: APIRequestContext) {
  const url = new URL(process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:8765');
  expect(['localhost', '127.0.0.1', '[::1]']).toContain(url.hostname);
  const configured = process.env.ORI_UI_SANDBOX;
  if (!configured) throw new Error('ORI_UI_SANDBOX must name this worktree’s demo sandbox');
  const sandbox = realpathSync(configured);
  expect(readFileSync(join(sandbox, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync(process.cwd())}`
  );
  const response = await request.get('/api/settings/workspace-root');
  expect(response.ok()).toBeTruthy();
  const settings = await response.json();
  expect(settings.confirmed, 'confirm the disposable workspace root before testing').toBe(true);
  expect(settings.effective_workspace_root).toBeTruthy();
  const root = realpathSync(settings.effective_workspace_root);
  const child = relative(sandbox, root);
  expect(
    child && !child.startsWith('..') && !isAbsolute(child),
    'server must use sandbox root'
  ).toBeTruthy();
  return sandbox;
}

export async function createWorkspace(request: APIRequestContext) {
  await assertSandbox(request);
  const response = await request.post('/api/workspaces', {
    data: {
      name: `UI consistency ${crypto.randomUUID()}`,
      blank: true,
      create_template_agents: true
    }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  const workspace = body.folder ?? body.workspace ?? body;
  expect(workspace.id).toBeTruthy();
  expect(workspace.folder_slug).toBeTruthy();
  return workspace as { id: string; folder_slug: string; name: string };
}

/** Retire only this test's exact disposable workspace, including its local
 * greeting session. The orchestration DELETE only unlinks those sessions. */
export async function removeOwnedWorkspace(
  request: APIRequestContext,
  expected: { id: string; name: string; folder_slug: string }
) {
  const sandbox = await assertSandbox(request);
  expect(expected.name).toMatch(
    /^UI consistency [0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
  );
  const response = await request.get(`/api/workspaces/${encodeURIComponent(expected.id)}`);
  expect(response.ok()).toBeTruthy();
  const workspace = await response.json();
  expect(workspace.id).toBe(expected.id);
  expect(workspace.name).toBe(expected.name);
  expect(workspace.folder_slug).toBe(expected.folder_slug);
  expect(workspace.kind).toBe('workspace');
  const sidecar = realpathSync(workspace.sidecar_path);
  const settings = await (await request.get('/api/settings/workspace-root')).json();
  const root = realpathSync(settings.effective_workspace_root);
  expect(relative(root, sidecar)).toBe(expected.folder_slug);
  expect(relative(sandbox, sidecar).startsWith('..')).toBe(false);
  for (const key of ['tasks', 'scheduled_tasks', 'messages', 'attachments', 'folders']) {
    expect(workspace[key] || [], `owned blank workspace unexpectedly gained ${key}`).toEqual([]);
  }
  for (const key of ['workflows', 'assistant_program']) {
    expect(workspace[key] === null || typeof workspace[key] === 'object').toBe(true);
    expect(Object.keys(workspace[key] || {}), `owned workspace unexpectedly gained ${key}`).toEqual(
      []
    );
  }
  // Explicit permanent removal stays inside the verified sidecar. Do not move
  // these fixtures into the user's native Trash, or delete their home_directory.
  const deleted = await request.delete(
    `/api/workspaces/${encodeURIComponent(expected.id)}?confirm=true&delete_sessions=true`
  );
  expect(deleted.status()).toBe(204);
  expect((await request.get(`/api/workspaces/${encodeURIComponent(expected.id)}`)).status()).toBe(
    404
  );
  expect(existsSync(sidecar), 'the owned sidecar must not survive metadata deletion').toBe(false);
}

export async function preparePage(page: Page, theme: 'light' | 'dark', width = 1440) {
  await installLocalCdn(page);
  if (process.env.ORI_UI_FONTS === 'real') {
    // Keep Bootstrap deterministic, but require real Google FontFace loads.
    await page.unroute('https://fonts.googleapis.com/**');
    await page.unroute('https://fonts.gstatic.com/**');
  }
  await page.setViewportSize({ width, height: viewportHeight });
  await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
  await page.emulateMedia({ reducedMotion: 'reduce' });
}

/** Read scenes must never turn an unexpected write into a fake success. */
export async function guardReadTour(page: Page, allowedPosts: string[] = []) {
  const unexpected: string[] = [];
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    // Existing activation only registers workspace directory watchers.
    if (
      !['GET', 'HEAD'].includes(request.method()) &&
      path !== '/api/orchestration/workspace/activate' &&
      !(request.method() === 'POST' && allowedPosts.includes(path))
    ) {
      unexpected.push(`${request.method()} ${path}`);
      await route.abort('blockedbyclient');
      return;
    }
    await route.fallback();
  });
  return unexpected;
}

export async function readFixture(page: Page, pattern: string | RegExp, json: unknown) {
  await page.route(pattern, route => {
    if (route.request().method() !== 'GET') return route.fallback();
    return route.fulfill({ json });
  });
}

export async function capture(
  page: Page,
  testInfo: TestInfo,
  name: string,
  facts: object = {},
  options: { fullPage?: boolean } = {}
) {
  await page.evaluate(() => document.fonts.ready);
  const fonts = await page.evaluate(() =>
    Array.from(document.fonts)
      .filter(face => face.status === 'loaded')
      .map(face => face.family)
  );
  if (process.env.ORI_UI_FONTS === 'real') {
    expect(fonts).toContain('Manrope');
    expect(fonts).toContain('Fraunces');
  }
  const modalVisible =
    (await page.locator('.modal.show:visible, dialog[open], [role="dialog"]:visible').count()) > 0;
  const fullPage = options.fullPage ?? !modalVisible;
  const metadata = {
    captureMode: fullPage ? 'full-page' : 'viewport',
    revision: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    dirty: !!execFileSync('git', ['status', '--porcelain'], { encoding: 'utf8' }).trim(),
    servedBuild: process.env.ORI_UI_BUILD || 'working-tree (rebuild after asset changes)',
    route: new URL(page.url()).pathname,
    viewport: page.viewportSize(),
    engine: testInfo.project.name,
    theme: await page.locator('html').getAttribute('data-bs-theme'),
    fontMode: process.env.ORI_UI_FONTS === 'real' ? 'real' : 'offline-fallback',
    loadedFaces: fonts,
    computedFonts: await page.evaluate(() => ({
      body: getComputedStyle(document.body).fontFamily,
      heading: getComputedStyle(document.querySelector('h1, h2, h3') || document.body).fontFamily
    })),
    ...facts
  };
  if (process.env.ORI_UI_FONTS === 'real') {
    expect(metadata.computedFonts.body).toContain('Manrope');
    const displayHeading = page.locator('.ui-page-header h1');
    if (await displayHeading.count()) {
      expect(
        await displayHeading.first().evaluate(node => getComputedStyle(node).fontFamily)
      ).toContain('Fraunces');
    }
  }
  await testInfo.attach(`${name}-metadata`, {
    body: JSON.stringify(metadata, null, 2),
    contentType: 'application/json'
  });
  if (!process.env.ORI_UI_EVIDENCE) return;
  const phase = process.env.ORI_UI_EVIDENCE;
  if (!['before', 'after'].includes(phase))
    throw new Error('ORI_UI_EVIDENCE must be before or after');
  const group =
    testInfo.file.includes('ui-consistency-navigation') ||
    testInfo.file.includes('ui-consistency-home') ||
    testInfo.file.includes('ui-consistency-agent')
      ? 'group-4'
      : testInfo.file.includes('ui-consistency-utilities') ||
          testInfo.file.endsWith('default-model-utility.spec.ts')
        ? 'group-3'
        : testInfo.file.endsWith('ui-consistency-defects.spec.ts')
          ? 'group-1'
          : 'group-2';
  const directory = resolve('tasks/evidence-ui-consistency', group, phase);
  mkdirSync(directory, { recursive: true });
  const stem = `${name}-${metadata.engine}-${metadata.theme}-${metadata.viewport?.width}-${metadata.fontMode}`;
  // Full-page captures at a scrolled position can paint the fixed navbar in
  // the middle of the image. Normalize only photography, then restore the tour.
  const scrollTop = await page.evaluate(() => window.scrollY);
  if (fullPage) await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
  try {
    await page.screenshot({ path: join(directory, `${stem}.png`), fullPage });
  } finally {
    if (fullPage)
      await page.evaluate(top => window.scrollTo({ top, behavior: 'instant' }), scrollTop);
  }
  writeFileSync(join(directory, `${stem}.json`), JSON.stringify(metadata, null, 2));
}

export async function reachable(control: Locator, minimum = 0) {
  await expect(control).toBeVisible();
  // Check before scrolling: scrollIntoView can horizontally pan a clipped
  // ancestor (even overflow:hidden), making an offscreen toolbar look valid.
  const initial = await control.boundingBox();
  const viewportWidth = await control.page().evaluate(() => window.innerWidth);
  expect(initial!.x).toBeGreaterThanOrEqual(-1);
  expect(initial!.x + initial!.width).toBeLessThanOrEqual(viewportWidth + 1);
  await control.scrollIntoViewIfNeeded();
  const measurement = await control.evaluate(node => {
    const box = node.getBoundingClientRect();
    const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
    return {
      x: box.x,
      right: box.right,
      width: box.width,
      height: box.height,
      viewport: window.innerWidth,
      hit: hit === node || node.contains(hit),
      topElement: hit?.outerHTML.slice(0, 180)
    };
  });
  expect(measurement.x, JSON.stringify(measurement)).toBeGreaterThanOrEqual(-1);
  expect(measurement.right, JSON.stringify(measurement)).toBeLessThanOrEqual(
    measurement.viewport + 1
  );
  expect(measurement.width).toBeGreaterThanOrEqual(minimum);
  expect(measurement.height).toBeGreaterThanOrEqual(minimum);
  expect(
    measurement.hit,
    `control must receive pointer: ${JSON.stringify(measurement)}`
  ).toBeTruthy();
  return measurement;
}

/** Composite solid/alpha backgrounds; refuse gradients rather than claim a bogus ratio. */
export async function contrast(locator: Locator, pseudo?: string) {
  return locator.evaluate((node, pseudoElement) => {
    const rgba = (value: string) => {
      const parts = value.match(/[\d.]+/g)?.map(Number);
      if (!parts || parts.length < 3) throw new Error(`Unsupported color ${value}`);
      return [parts[0], parts[1], parts[2], parts[3] ?? 1];
    };
    const over = (fg: number[], bg: number[]) => {
      const alpha = fg[3] + bg[3] * (1 - fg[3]);
      return [0, 1, 2]
        .map(i => (fg[i] * fg[3] + bg[i] * bg[3] * (1 - fg[3])) / (alpha || 1))
        .concat(alpha);
    };
    // Opacity composites an entire group, including opaque child backgrounds.
    // Do not report uncomposited colors while a tab/dialog is still fading.
    for (let parent: Element | null = node; parent; parent = parent.parentElement) {
      if (Number(getComputedStyle(parent).opacity) < 0.999) {
        throw new Error(
          `Wait for fading content or measure group-opacity pixels: ${parent.id || parent.className}`
        );
      }
    }
    let background = [0, 0, 0, 0];
    for (
      let parent: Element | null = node;
      parent && background[3] < 0.999;
      parent = parent.parentElement
    ) {
      const style = getComputedStyle(parent);
      if (style.backgroundImage !== 'none')
        throw new Error(`Measure gradient pixels separately: ${parent.className}`);
      background = over(background, rgba(style.backgroundColor));
    }
    if (background[3] < 0.999) throw new Error('No opaque backdrop found');
    const style = getComputedStyle(node, pseudoElement);
    const ink = rgba(style.color);
    ink[3] *= Number(style.opacity);
    const foreground = over(ink, background);
    const luminance = (color: number[]) =>
      color
        .slice(0, 3)
        .map(value => {
          const v = value / 255;
          return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
        })
        .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
    const a = luminance(foreground),
      b = luminance(background);
    return { foreground, background, ratio: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) };
  }, pseudo);
}
