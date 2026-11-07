import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { cpus, platform, release, arch, loadavg } from 'node:os';
import { createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import {
  browserAuthoritySnapshot,
  libraryJSON,
  seedProjectBrowser,
  sourceHashes
} from './helpers/project-library-browser-fixture';

// Real host/SQLite/folder APIs throughout; no intercepted responses. One fresh
// sandbox per spec, never alongside another fixture export into the same HOME.
const sandbox = process.env.ORI_LIBRARY_BROWSER_SANDBOX || '';
const evidence = 'tasks/evidence-project-library-browser/05-delivery';
test.skip(!sandbox, 'requires e2e-fresh --sandbox-env ORI_LIBRARY_BROWSER_SANDBOX');
test.setTimeout(180_000);
test.use({ actionTimeout: 15_000, reducedMotion: 'reduce' });

function timings(samples: number[]) {
  const sorted = [...samples].sort((a, b) => a - b);
  return {
    samples: samples.length,
    p50_ms: sorted[Math.ceil(sorted.length * 0.5) - 1],
    p95_ms: sorted[Math.ceil(sorted.length * 0.95) - 1],
    max_ms: sorted.at(-1)
  };
}

test('real 105-entry warm queries, feedback, themed keyboard layout and inert browsing', async ({
  page,
  request,
  browserName
}) => {
  const fixture = await seedProjectBrowser(request, sandbox);
  const before = await browserAuthoritySnapshot(request, fixture.api);
  const hashes = sourceHashes(fixture.source_root);
  mkdirSync(evidence, { recursive: true });
  const loadAverageBeforeMeasurement = loadavg();
  const samples: Record<string, ReturnType<typeof timings>> = {};
  for (const [name, path] of [
    ['list', `${fixture.api}/projects`],
    ['detail', `${fixture.api}/projects/browser-000`]
  ]) {
    for (let i = 0; i < 5; i++) await libraryJSON(await request.get(path));
    const elapsed = [];
    for (let i = 0; i < 100; i++) {
      const start = performance.now();
      await libraryJSON(await request.get(path));
      elapsed.push(performance.now() - start);
    }
    samples[name] = timings(elapsed);
  }

  const requests: string[] = [];
  const mutations: string[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (path.includes('/assistant-program/library')) requests.push(path);
    if (!['GET', 'HEAD'].includes(req.method()) && path.startsWith('/api/'))
      mutations.push(`${req.method()} ${path}`);
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(fixture.route);
  const rows = page.locator('#projectLibraryRows');
  const pane = page.locator('#projectLibraryPane');
  const title = page.locator('#projectLibraryPaneTitle');
  await expect(rows.locator('tr')).toHaveCount(25);
  await expect(page.locator('#projectLibraryBrowser').getByRole('columnheader')).toHaveCount(4);
  const feedback = [];
  for (let i = 0; i < 20; i++) {
    const name = `Project 00${i % 2}`;
    // Timestamp the actual DOM click handler and next painted frame in the
    // browser, excluding Playwright transport. Requests still hit the real host.
    const result = await page
      .getByRole('button', { name: `Inspect ${name}`, exact: true })
      .evaluate(button => {
        const start = performance.now();
        (button as HTMLButtonElement).click();
        const busy = document.querySelector('#projectLibraryTabPanel')?.getAttribute('aria-busy');
        const heading = document.querySelector('#projectLibraryPaneTitle')?.textContent;
        return new Promise<{
          ms: number;
          busy: string | null | undefined;
          heading: string | null | undefined;
        }>(resolve =>
          requestAnimationFrame(() => resolve({ ms: performance.now() - start, busy, heading }))
        );
      });
    expect(result.busy === 'true' || result.heading === name).toBe(true);
    feedback.push(result.ms);
    await expect(title).toHaveText(name);
  }
  samples.selection_feedback = timings(feedback);
  expect(requests.filter(path => /\/(files|preview|sessions|activation)$/.test(path))).toEqual([]);

  // Supported theme switch; keyboard activation and tab/folder navigation use
  // the real controls. ARIA snapshots are semantic evidence, not a screen reader.
  for (const theme of ['light', 'dark']) {
    if ((await page.locator('html').getAttribute('data-bs-theme')) !== theme)
      await page.locator('#darkModeToggle').click();
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
    await page.getByRole('tab', { name: 'Overview', exact: true }).click();
    await page.getByRole('button', { name: 'Inspect Project 000', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(title).toHaveText('Project 000');
    await page.getByRole('tab', { name: 'Overview', exact: true }).focus();
    await page.keyboard.press('ArrowRight');
    await expect(page.getByRole('tab', { name: 'Files', exact: true })).toBeFocused();
    const folder = pane.getByRole('button', {
      name: 'Expand folder Nested space 日本語',
      exact: true
    });
    await folder.focus();
    await page.keyboard.press('Enter');
    await expect(
      pane.getByRole('button', { name: 'Collapse folder Nested space 日本語', exact: true })
    ).toHaveAttribute('aria-expanded', 'true');
    await pane.getByRole('button', { name: 'read me.txt', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(pane.locator('pre')).toContainText('Only the original source.');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true
    );
    await page
      .locator('#projectLibraryBrowser')
      .screenshot({ path: `${evidence}/desktop-${theme}.png` });
    writeFileSync(`${evidence}/semantics-${theme}.txt`, await pane.ariaSnapshot(), { mode: 0o600 });

    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole('button', { name: 'Back to projects', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(
      page.getByRole('button', { name: 'Inspect Project 000', exact: true })
    ).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(title).toBeFocused();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true
    );
    await page
      .locator('#projectLibraryBrowser')
      .screenshot({ path: `${evidence}/narrow-${theme}.png` });

    // 1440x1000 at 200% has 720x500 CSS-pixel layout space. This checks
    // equivalent reflow, not browser-native zoom or assistive technology.
    await page.setViewportSize({ width: 720, height: 500 });
    await page.getByRole('button', { name: 'Back to projects', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(
      page.getByRole('button', { name: 'Inspect Project 000', exact: true })
    ).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(title).toBeFocused();
    await expect(title).toHaveText('Project 000');
    await expect(pane.getByRole('button', { name: 'page.html', exact: true })).toBeVisible();
    const focusVisible = await title.evaluate(element => {
      const box = element.getBoundingClientRect();
      const navbar = document.querySelector('nav.navbar')?.getBoundingClientRect();
      const back = document.querySelector('#projectLibraryBack')!.getBoundingClientRect();
      return back.top >= (navbar?.bottom || 0) && box.bottom <= innerHeight;
    });
    expect(
      focusVisible,
      'Back and focused title must be below the fixed navbar at short-height reflow'
    ).toBe(true);
    await expect(page.locator('#projectLibraryResults')).toBeHidden();
    await expect(pane).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true
    );
    // Use the visible viewport: an element screenshot taller than the viewport
    // scrolls its crop under the fixed navbar and misrepresents this focus state.
    await page.screenshot({ path: `${evidence}/reflow-200-equivalent-${theme}.png` });
    await page.setViewportSize({ width: 1440, height: 1000 });
  }
  expect(await browserAuthoritySnapshot(request, fixture.api)).toEqual(before);
  expect(sourceHashes(fixture.source_root)).toEqual(hashes);
  expect(mutations).toEqual([]);
  writeFileSync(
    `${evidence}/acceptance.json`,
    JSON.stringify(
      {
        build: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
        workingTree: 'current working-tree build; tracked changes identified by diff digest',
        trackedDiffSHA256: createHash('sha256')
          .update(execFileSync('git', ['diff', 'HEAD']))
          .digest('hex'),
        loadAverageBeforeMeasurement,
        platform: `${platform()} ${release()} ${arch()}`,
        cpu: cpus()[0]?.model,
        browser: browserName,
        entries: before.entries,
        samples,
        measurement:
          'Warm sequential loopback HTTP including body parse; 5 warmups then 100 requests per endpoint. Feedback: DOM click to next animation frame, 20 real-host selections.',
        libraryGETs: requests.length,
        sourceListingGETs: requests.filter(path => path.endsWith('/files')).length,
        previewGETs: requests.filter(path => path.endsWith('/preview')).length,
        apiMutations: mutations,
        unchanged: {
          sourceHashes: Object.keys(hashes).length,
          workspaces: before.workspaces.length,
          globalAgents: before.agentNames.length,
          workspaceAgentInstances: before.workspaces.reduce(
            (sum, workspace) => sum + workspace.agents.length,
            0
          ),
          libraryRevision: true,
          discoveryGrantsAndChildReferences: true
        },
        notRun: ['manual screen-reader pass', 'browser-native 200% zoom', 'non-Chromium browsers'],
        provider: 'absent browser-fixture; historical read-only library'
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
  expect(samples.list.p95_ms).toBeLessThanOrEqual(250);
  expect(samples.detail.p95_ms).toBeLessThanOrEqual(250);
  expect(samples.selection_feedback.max_ms).toBeLessThanOrEqual(100);
});
