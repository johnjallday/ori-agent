import { expect, test, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

// Real server process, real recovery host, real receipt on disk. This spec is a
// journey in phases, because the server process must be stopped, emptied around
// and relaunched between them, which Playwright cannot do. Drive it with
// scripts/reset-cancel-demo.sh against a disposable demo sandbox:
//
//   ORI_RESET_CANCEL_PHASE=recovery   server in recovery, receipt blocked rev 3:
//                                     offer, keyboard, refused feedback, real cancel
//   ORI_RESET_CANCEL_PHASE=reopened   the SAME sandbox relaunched normally: the
//                                     canceled receipt reads as ready, not completed
//   ORI_RESET_CANCEL_PHASE=managed    server started with -managed-restart: cancel,
//                                     and the same tab sees Ori reopen by itself
//   ORI_RESET_CANCEL_PHASE=ineligible a sandbox whose vault ALSO changed: no Cancel
//
// Without a phase every test is skipped, and a skipped test is not a pass.
// ORI_RESET_CANCEL_SANDBOX must name the demo sandbox (it needs .ori-demo-owner).

const phase = process.env.ORI_RESET_CANCEL_PHASE ?? '';
const sandbox = process.env.ORI_RESET_CANCEL_SANDBOX ?? '';
const evidence = 'tasks/evidence-reset-workspace-change-recovery';

function guarded() {
  expect(sandbox, 'requires ORI_RESET_CANCEL_SANDBOX (a disposable demo sandbox)').toBeTruthy();
  const root = fs.realpathSync(sandbox);
  expect(fs.existsSync(path.join(root, '.ori-demo-owner'))).toBeTruthy();
  const state = JSON.parse(fs.readFileSync(path.join(root, '.reset-cancel-demo.json'), 'utf8'));
  const receipt = () =>
    JSON.parse(fs.readFileSync(path.join(root, '.ori-reset', 'operation.json'), 'utf8')).operation;
  return { root, operation: state.operation_id as string, receipt };
}

const title = (page: Page) => page.locator('#reset-state-title');

async function focusCancel(page: Page) {
  const button = page.getByRole('button', { name: 'Cancel reset' });
  let presses = 0;
  while (!(await button.evaluate(el => el === document.activeElement)) && presses < 25) {
    await page.keyboard.press('Tab');
    presses += 1;
  }
  await expect(button).toBeFocused();
  return presses;
}

async function realCancel(page: Page) {
  await focusCancel(page);
  await page.keyboard.press('Enter');
  await expect(title(page)).toHaveText(/Reset canceled|Reopening Ori/);
}

test.describe('recovery page: cancel an unapplied Start Fresh', () => {
  test('offers cancel, explains it, refuses without claiming success, then cancels from the keyboard', async ({
    page
  }) => {
    test.skip(phase !== 'recovery', 'phase=recovery only');
    const { root, operation, receipt } = guarded();
    expect(receipt()).toMatchObject({ state: 'blocked', revision: 3 });
    fs.mkdirSync(evidence, { recursive: true });

    await page.goto('/reset/status');
    await expect(page).toHaveURL(new RegExp(`operation=${operation}`));
    await expect(title(page)).toHaveText('Reset needs attention');
    const section = page.locator('#reset-cancel');
    await expect(section).toBeVisible();
    await expect(section).toContainText('is now empty');
    await expect(section).toContainText('No part of this reset was applied');
    await expect(section).toContainText('does not restore anything that was removed outside Ori');
    await expect(section).toContainText('Canceled is not completed');
    await expect(page.locator('#reset-restart-instructions')).toBeVisible();
    expect((await page.content()).toLowerCase()).not.toContain('deleted');
    await page.screenshot({ path: `${evidence}/03-recovery-eligible.png`, fullPage: true });

    // Keyboard reachability and a visible focus ring.
    const presses = await focusCancel(page);
    expect(presses).toBeGreaterThan(0);
    const ring = await page
      .getByRole('button', { name: 'Cancel reset' })
      .evaluate(el => getComputedStyle(el).outlineStyle);
    expect(ring).not.toBe('none');
    await page.screenshot({ path: `${evidence}/04-cancel-focused.png` });

    // A refusal shows feedback, keeps the page actionable and claims nothing.
    await page.route('**/api/reset/operations/*/cancel', route =>
      route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({
          canceled: false,
          code: 'not_cancelable',
          message:
            'Ori can no longer show that this reset changed nothing, so it cannot be canceled here.'
        })
      })
    );
    await page.keyboard.press('Enter');
    const feedback = page.locator('#reset-cancel-feedback');
    await expect(feedback).toBeVisible();
    await expect(feedback).toContainText('can no longer show that this reset changed nothing');
    await expect(feedback).toHaveAttribute('role', 'alert');
    await expect(page.getByRole('button', { name: 'Cancel reset' })).toBeEnabled();
    await expect(title(page)).toHaveText('Reset needs attention');
    expect(receipt()).toMatchObject({ state: 'blocked', revision: 3 });
    await page.screenshot({ path: `${evidence}/05-cancel-refused.png` });
    await page.unroute('**/api/reset/operations/*/cancel');

    // The real cancel, by keyboard on a fresh load.
    await page.reload();
    await realCancel(page);
    await expect(title(page)).toHaveText('Reset canceled');
    await expect(page.locator('#reset-state-message')).toContainText(
      'Nothing was reset, removed or restored'
    );
    await expect(title(page)).not.toContainText(/complet/i);
    await expect(section).toBeHidden();
    await expect(page.locator('#reset-restart-instructions')).toBeVisible();
    await expect(page.locator('#reset-results')).toBeHidden();
    await expect(page.locator('#reset-enter')).toBeHidden();
    await expect(title(page)).toBeFocused();
    expect(receipt()).toMatchObject({
      state: 'canceled',
      revision: 4,
      cancellation: { from_revision: 3, prior_blocker: 'recovery_scope_changed' }
    });
    expect(
      receipt().results.every((r: { outcome: string }) => r.outcome === 'pending')
    ).toBeTruthy();
    expect(fs.existsSync(path.join(root, 'workspace-staging'))).toBeTruthy();
    await page.screenshot({ path: `${evidence}/06-canceled-manual-steps.png`, fullPage: true });

    await page.reload();
    await expect(title(page)).toHaveText('Reset canceled');
    await expect(section).toBeHidden();
    await page.screenshot({ path: `${evidence}/07-canceled-reloaded.png` });
  });

  test('a canceled receipt opens the relaunched app as ready, never as a completed reset', async ({
    page,
    request
  }) => {
    test.skip(phase !== 'reopened', 'phase=reopened only');
    const { operation, receipt } = guarded();
    const before = JSON.stringify(receipt());
    const api = await request.get(`/api/reset/operations/${operation}`);
    expect(api.headers()['x-ori-reset-ready']).toBe('1');
    const payload = await api.json();
    expect(payload.success).toBe(false);
    expect(payload.operation.state).toBe('canceled');
    expect(payload.requires_restart).toBe(false);

    await page.goto('/');
    await page.evaluate(() => localStorage.setItem('ori-theme', 'canceled-sentinel'));
    await page.goto(`/reset/status?operation=${operation}`);
    await expect(title(page)).toHaveText('Reset canceled');
    await expect(page.locator('#reset-state-message')).toContainText('Ori is ready');
    const enter = page.locator('#reset-enter');
    await expect(enter).toBeVisible();
    await expect(enter).toHaveText('Back to Ori');
    await expect(page.locator('#reset-restart-instructions')).toBeHidden();
    await expect(page.locator('#reset-results')).toBeHidden();
    await expect(page.getByText('Continue setup')).toHaveCount(0);
    // None of a completed reset's browser cleanup ran.
    expect(await page.evaluate(() => localStorage.getItem('ori-theme'))).toBe('canceled-sentinel');
    expect(await page.evaluate(() => localStorage.getItem('ori.reset.generation'))).toBeNull();
    await page.screenshot({ path: `${evidence}/08-reopened-ready.png`, fullPage: true });
    await enter.click();
    await expect(page).toHaveURL(/\/$/);
    expect(JSON.stringify(receipt())).toBe(before);
  });

  test('under a managed launch the same tab sees Ori reopen by itself after a durable cancel', async ({
    page
  }) => {
    test.skip(phase !== 'managed', 'phase=managed only');
    test.setTimeout(150_000);
    const { operation, receipt } = guarded();
    expect(receipt()).toMatchObject({ state: 'blocked', revision: 3 });
    await page.goto(`/reset/status?operation=${operation}`);
    await expect(page.locator('#reset-cancel')).toBeVisible();
    await realCancel(page);
    await expect(title(page)).toHaveText('Reopening Ori');
    await expect(page.locator('#reset-state-message')).toContainText('automatically');
    expect(receipt()).toMatchObject({ state: 'canceled', revision: 4 });
    fs.mkdirSync(evidence, { recursive: true });
    await page.screenshot({ path: `${evidence}/09-managed-reopening.png` });
    // The supervisor starts one replacement that finds this operation canceled.
    await expect(page.locator('#reset-enter')).toBeVisible({ timeout: 120_000 });
    await expect(title(page)).toHaveText('Reset canceled');
    await expect(page.locator('#reset-state-message')).toContainText('Ori is ready');
    await expect(page.locator('#reset-enter')).toHaveText('Back to Ori');
    await page.screenshot({ path: `${evidence}/10-managed-ready.png`, fullPage: true });
    await page.locator('#reset-enter').click();
    await expect(page).toHaveURL(/\/$/);
    expect(receipt()).toMatchObject({ state: 'canceled', revision: 4 });
  });

  test('when more than the workspace changed there is no Cancel, and a forged request is refused', async ({
    page,
    request
  }) => {
    test.skip(phase !== 'ineligible', 'phase=ineligible only');
    const { operation, receipt } = guarded();
    expect(receipt()).toMatchObject({ state: 'blocked', revision: 3 });
    await page.goto(`/reset/status?operation=${operation}`);
    await expect(title(page)).toHaveText('Reset needs attention');
    await expect(page.locator('#reset-cancel')).toBeHidden();
    await expect(page.getByRole('button', { name: 'Cancel reset' })).toBeHidden();
    await expect(page.locator('#reset-restart-instructions')).toBeVisible();
    await expect(page.locator('#reset-blockers')).toBeVisible();
    fs.mkdirSync(evidence, { recursive: true });
    await page.screenshot({ path: `${evidence}/11-not-cancelable.png`, fullPage: true });

    const api = await (await request.get(`/api/reset/operations/${operation}`)).json();
    expect(api.cancel).toBeUndefined();
    // The server decides, not the button: forged requests change nothing.
    const forged = (headers: Record<string, string>) =>
      request.post(`/api/reset/operations/${operation}/cancel`, {
        headers: { 'X-Requested-With': 'XMLHttpRequest', ...headers },
        data: { revision: 3 }
      });
    expect((await forged({ Origin: 'https://evil.example' })).status()).toBe(403);
    expect((await forged({})).status()).toBe(403);
    const base = new URL(page.url()).origin;
    expect((await forged({ Origin: base, 'X-Ori-Reset-Token': 'guess' })).status()).toBe(403);
    expect(receipt()).toMatchObject({ state: 'blocked', revision: 3 });
  });
});
