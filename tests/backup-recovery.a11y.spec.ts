import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve, dirname, basename } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

/** Real product console, not intercepted product APIs. One reviewed ordinary
 * workspace/Backup Advisor, no provider/picker/password/model call or source
 * grant. Optional operator-seeded configured state is explicitly a stored
 * fixture, not provider evidence. Run only on an isolated demo server.
 * Chromium is the repository's configured desktop browser project; this file
 * does not claim Firefox/WebKit or an automated screen-reader user verdict. */
let workspace: { id: string; folder_slug: string };
const run = Date.now().toString(36);
const storedFixture = Boolean(process.env.ORI_BACKUP_A11Y_STORED_CONTROL);

test.describe.serial('Backup & Recovery console accessibility', () => {
  test.beforeAll(async ({ request }) => {
    if (storedFixture) {
      const root = resolve(import.meta.dirname, '..');
      const file = resolve(process.env.ORI_BACKUP_A11Y_STORED_CONTROL!);
      expect(dirname(file)).toBe(resolve(root, '.tmp/backup-recovery-delivery'));
      expect(basename(file)).toMatch(/^backup-stored-demo-control-[0-9]+\.json$/);
      const control = JSON.parse(readFileSync(file, 'utf8'));
      expect(control.kind).toBe('invented stored metadata only');
      expect(control.workspace_id).toMatch(/^[a-f0-9-]{36}$/);
      expect(control.slug).toMatch(/^[a-z0-9][a-z0-9-]{0,79}$/);
      workspace = { id: control.workspace_id, folder_slug: control.slug };
      return;
    }
    expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
    const reviewed = await request.post('/api/workspaces/template-agent-plan', {
      data: { template_id: 'backup-and-recovery' }
    });
    expect(reviewed.ok(), await reviewed.text()).toBeTruthy();
    const plan = await reviewed.json();
    expect(plan.agents).toHaveLength(1);
    const created = await request.post('/api/workspaces', {
      data: {
        name: 'Backup accessibility ' + run,
        template_id: 'backup-and-recovery',
        team_intent: { version: 1, mode: 'staffed', plan_revision: plan.revision },
        role_staffing: [
          { role_id: 'backup-advisor', mode: 'create', name: 'Backup Accessibility Advisor ' + run }
        ]
      }
    });
    expect(created.status(), await created.text()).toBe(201);
    const result = await created.json();
    workspace = result.folder || result.workspace;
    expect(workspace.id).toBeTruthy();
    expect(workspace.folder_slug).toBeTruthy();
  });

  for (const theme of ['light', 'dark'] as const) {
    for (const narrow of [false, true]) {
      test(`${theme} ${narrow ? 'narrow' : 'desktop'}: named dialog, keyboard ownership, readable controls and safe close`, async ({
        page,
        request
      }, info) => {
        const mutations: string[] = [];
        page.on('request', request => {
          if (
            request.method() !== 'GET' &&
            new URL(request.url()).pathname.includes('/backup-recovery')
          )
            mutations.push(request.url());
        });
        await installLocalCdn(page);
        await page.setViewportSize(
          narrow ? { width: 390, height: 844 } : { width: 1280, height: 800 }
        );
        await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
        await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), theme);
        await page.goto(
          '/workspaces/' + encodeURIComponent(workspace.folder_slug) + '?mode=details'
        );
        // The real template's first-run wizard owns the first modal. Dismiss
        // it through its keyboard control before testing this console's focus.
        const wizard = page.locator('#setupWizardDialog');
        if (!(await wizard.isVisible())) await page.locator('#setupWizardBannerAction').click();
        await expect(wizard).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(wizard).not.toBeVisible();
        const trigger = page
          .locator('#backupRecoveryMount')
          .getByRole('button', { name: 'Open backup evidence', exact: true });
        await expect(trigger).toBeVisible();
        await trigger.focus();
        await page.keyboard.press('Enter');
        const dialog = page.getByRole('dialog', { name: 'Keep a way back.', exact: true });
        await expect(dialog).toBeVisible();
        await expect(dialog).toHaveAttribute('aria-busy', 'false');
        await expect(dialog).toHaveAttribute('aria-modal', 'true');
        const close = dialog.getByRole('button', { name: 'Close', exact: true });
        await expect(close).toBeFocused();
        await expect(close).toBeVisible();
        const closeIsUncovered = () =>
          close.evaluate(element => {
            const box = element.getBoundingClientRect();
            const top = document.elementFromPoint(
              box.left + box.width / 2,
              box.top + box.height / 2
            );
            return top === element || element.contains(top);
          });
        expect(
          await closeIsUncovered(),
          'application navigation covered the console Close control'
        ).toBeTruthy();
        // Inert furniture is omitted by hit testing but can still paint over
        // this non-top-layer dialog. Check the real application layer too.
        expect(
          await dialog.evaluate(element => {
            const navbar = document.querySelector('nav.navbar');
            return (
              navbar !== null &&
              Number(getComputedStyle(element).zIndex) > Number(getComputedStyle(navbar).zIndex)
            );
          }),
          'inert application navigation still paints above the console'
        ).toBeTruthy();
        expect(
          await dialog.evaluate(element => element.scrollWidth <= element.clientWidth + 1)
        ).toBeTruthy();

        const tabs = dialog.getByRole('tablist', { name: 'Backup & Recovery' });
        const overview = tabs.getByRole('tab', { name: 'Overview', exact: true });
        await overview.focus();
        await page.keyboard.press('ArrowRight');
        await expect(tabs.getByRole('tab', { name: 'History', exact: true })).toBeFocused();
        await expect(tabs.getByRole('tab', { name: 'History', exact: true })).toHaveAttribute(
          'aria-selected',
          'true'
        );
        await page.keyboard.press('End');
        await expect(tabs.getByRole('tab', { name: 'Settings', exact: true })).toBeFocused();
        await expect(dialog.getByRole('tabpanel')).toHaveAttribute(
          'aria-labelledby',
          'brTab-settings'
        );
        await expect(
          dialog.getByLabel('Selected Vault ID — not a password', { exact: true })
        ).toBeVisible();
        await expect(
          dialog.getByLabel('Selected workspace secret record ID', { exact: true })
        ).toBeVisible();
        await expect(
          dialog.getByRole('button', { name: 'Check metadata now', exact: true })
        ).toHaveCount(0);
        expect(await contrast(page, '#backupRecoveryTitle')).toBeGreaterThanOrEqual(4.5);
        expect(
          await dialog.evaluate(element =>
            getComputedStyle(element)
              .transitionDuration.split(',')
              .every(
                value =>
                  (value.trim().endsWith('ms') ? parseFloat(value) : parseFloat(value) * 1000) <=
                  0.011
              )
          )
        ).toBeTruthy();
        for (let i = 0; i < 18; i++) {
          const beforeFocus = await page.evaluate(() => ({
            tag: document.activeElement?.tagName,
            id: document.activeElement?.id
          }));
          await page.keyboard.press(i % 3 === 0 ? 'Shift+Tab' : 'Tab');
          const focus = await dialog.evaluate(element => ({
            inside: element.contains(document.activeElement),
            tag: document.activeElement?.tagName,
            id: document.activeElement?.id,
            open: element.hasAttribute('open')
          }));
          expect(
            focus.inside,
            'focus escaped at Tab step ' + i + ': ' + JSON.stringify({ beforeFocus, focus })
          ).toBeTruthy();
        }
        await tabs.getByRole('tab', { name: 'Recovery', exact: true }).click();
        await expect(dialog).toContainText('Ori does not restore or inspect recovered contents');
        await expect(dialog).toContainText('not Ori verification');
        expect(
          await dialog.evaluate(element => element.scrollWidth <= element.clientWidth + 1)
        ).toBeTruthy();
        if (storedFixture) {
          await expect(dialog.getByLabel('Test date (UTC)', { exact: true })).toBeVisible();
          for (const name of [
            'Your reported result',
            'Snapshot association',
            'Optional note — predefined choices only'
          ])
            await expect(dialog.getByRole('combobox', { name, exact: true })).toBeVisible();
          await expect(dialog.locator('#brReportConsent')).not.toBeChecked();
          await expect(
            dialog.getByRole('button', { name: 'Record user-reported result', exact: true })
          ).toBeDisabled();
          expect(await contrast(page, '.br-reports .br-note')).toBeGreaterThanOrEqual(4.5);
        }
        expect(
          await closeIsUncovered(),
          'scrolling or changing tabs hid the console Close control'
        ).toBeTruthy();
        await page.screenshot({
          path: info.outputPath(
            `actual-${storedFixture ? 'stored' : 'unconfigured'}-${theme}-${narrow ? 'narrow' : 'desktop'}.png`
          ),
          fullPage: false
        });
        expect(
          await closeIsUncovered(),
          'console Close became covered during layout settlement'
        ).toBeTruthy();
        await page.keyboard.press('Escape');
        await expect(dialog).not.toBeVisible();
        await expect(trigger).toBeFocused();
        expect(mutations).toEqual([]);
        const status = await request.get('/api/workspaces/' + workspace.id + '/backup-recovery');
        expect(status.ok(), await status.text()).toBeTruthy();
        expect((await status.json()).status).toMatchObject({
          connection: storedFixture ? 'configured' : 'not_configured',
          monitoring: 'manual_only'
        });
      });
    }
  }
});

async function contrast(page: Page, selector: string) {
  return page
    .locator(selector)
    .first()
    .evaluate(element => {
      // Let the browser convert CSS Color 4/interpolated values to sRGB;
      // parsing only rgb()/srgb strings can mistake oklab colors for black.
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = 1;
      const context = canvas.getContext('2d', { colorSpace: 'srgb' })!;
      const channels = (value: string) => {
        context.clearRect(0, 0, 1, 1);
        context.fillStyle = value;
        context.fillRect(0, 0, 1, 1);
        return Array.from(context.getImageData(0, 0, 1, 1).data);
      };
      const composite = (color: number[], background: number[]) =>
        color
          .slice(0, 3)
          .map(
            (value, index) => (value * color[3]) / 255 + background[index] * (1 - color[3] / 255)
          );
      const luminance = (values: number[]) =>
        values
          .map(value => {
            const fraction = value / 255;
            return fraction <= 0.04045 ? fraction / 12.92 : ((fraction + 0.055) / 1.055) ** 2.4;
          })
          .reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index], 0);
      let ancestor: Element | null = element;
      const layers: number[][] = [];
      while (ancestor) {
        const color = channels(getComputedStyle(ancestor).backgroundColor);
        layers.push(color);
        if (color[3] === 255) break;
        ancestor = ancestor.parentElement;
      }
      const background = layers
        .reverse()
        .reduce((base, color) => composite(color, base), [255, 255, 255]);
      const values = [
        luminance(composite(channels(getComputedStyle(element).color), background)),
        luminance(background)
      ].sort((left, right) => right - left);
      return (values[0] + 0.05) / (values[1] + 0.05);
    });
}
