import { test, expect, type Locator, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { seedLearningWorkspace, learningStatus } from './helpers/learning-missions';

test.describe('learning production layout and keyboard matrix', () => {
  test.describe.configure({ mode: 'serial', timeout: 90000 });
  test.skip(
    process.env.ORI_LEARNING_DEMO_GROUP !== '6',
    'Run the isolated Group-6 helper with AXE_CORE_FILE.'
  );
  const base = process.env.PLAYWRIGHT_BASE_URL || '';
  const evidence = process.env.ORI_LEARNING_DEMO_EVIDENCE || '';
  let workspaceID: string, ticketHref: string;
  const hostileInput =
    'Fictional <img src=x onerror=alert(1)> is literal supplied text. ' +
    'LongWord'.repeat(100) +
    ' Please draft a short welcome.';
  const axe = async (page: Page, selector: string) => {
    await page.addScriptTag({ content: readFileSync(process.env.AXE_CORE_FILE || '', 'utf8') });
    const violations = await page.evaluate(async target => {
      const result = await (
        window as unknown as {
          axe: {
            run: (
              target: string,
              options: object
            ) => Promise<{ violations: { id: string; nodes: unknown[] }[] }>;
          };
        }
      ).axe.run(target, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } });
      return result.violations;
    }, selector);
    expect(violations).toEqual([]);
  };
  const tabTo = async (page: Page, target: Locator, backwards = false) => {
    await expect(target).toBeVisible();
    for (let n = 0; n < 90; n++) {
      if (await target.evaluate(node => node === document.activeElement)) return;
      // Safari/WebKit's default macOS policy uses Option+Tab for all controls.
      const option = page.context().browser()?.browserType().name() === 'webkit' ? 'Alt+' : '';
      await page.keyboard.press(option + (backwards ? 'Shift+Tab' : 'Tab'));
    }
    throw new Error('Keyboard could not reach the labelled control');
  };
  const fits = async (target: Locator, width: number) => {
    const bounds = await target.boundingBox();
    expect(bounds).toBeTruthy();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1);
    expect(await target.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
  };
  test.beforeAll(async ({ request }) => {
    workspaceID = await seedLearningWorkspace(
      request,
      base,
      'Layout <img src=x onerror=alert(1)> ' + 'Long-name-'.repeat(7) + ' ユーザー'
    );
  });

  test('keyboard alone delegates, reviews exact literal input/result and reloads one recorded rejection', async ({
    page,
    request
  }) => {
    await page.setViewportSize({ width: 1365, height: 1100 });
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto('/');
    await tabTo(page, page.locator('#cockpitQuestsToggle'));
    await page.keyboard.press('Enter');
    const board = page.locator('#learningMissions');
    await expect(board).toBeVisible();
    await axe(page, '#learningMissions');
    await tabTo(page, board.getByRole('button', { name: 'Choose a workspace', exact: true }));
    await page.keyboard.press('Enter');
    const workspace = board.getByLabel('Workspace', { exact: true });
    await tabTo(page, workspace);
    await page.keyboard.press('Home');
    await expect(workspace).toHaveValue(workspaceID);
    await tabTo(page, board.getByRole('button', { name: 'Request a practice', exact: true }));
    await page.keyboard.press('Enter');
    await expect(
      board.getByRole('button', { name: 'Start learning — no execution', exact: true })
    ).toBeVisible();
    await axe(page, '#learningMissions');
    await tabTo(
      page,
      board.getByRole('button', { name: 'Start learning — no execution', exact: true })
    );
    await page.keyboard.press('Enter');
    await tabTo(page, board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }));
    await page.keyboard.press('Enter');
    await tabTo(
      page,
      board.getByLabel('Text and drafting requirements (maximum 4 KiB)', { exact: true })
    );
    await page.keyboard.type(hostileInput);
    await tabTo(page, board.getByLabel('Assigned agent', { exact: true }));
    await page.keyboard.press('End');
    await tabTo(
      page,
      board.getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
    );
    await page.keyboard.press('Enter');
    const link = board.getByRole('link', { name: 'Open Ticket to review execution', exact: true });
    await expect(link).toBeVisible();
    ticketHref = (await link.getAttribute('href')) || '';
    await tabTo(page, link);
    await page.keyboard.press('Enter');
    const review = page.locator('#learningTicketReview');
    await expect(review.getByRole('region', { name: 'Reviewed input', exact: true })).toHaveText(
      hostileInput
    );
    expect(await review.locator('img').count()).toBe(0);
    await axe(page, '#learningTicketReview');
    await tabTo(
      page,
      review.getByRole('button', {
        name: 'Start this Ticket with this input and model',
        exact: true
      })
    );
    await page.keyboard.press('Enter');
    await expect
      .poll(async () => (await learningStatus(request, base)).instances[0].progress.result, {
        timeout: 15000
      })
      .toBeTruthy();
    await tabTo(
      page,
      review.getByRole('button', { name: 'Refresh learning and execution status', exact: true })
    );
    await page.keyboard.press('Enter');
    await tabTo(
      page,
      review.getByRole('button', { name: 'Inspect the exact result for learning', exact: true })
    );
    await page.keyboard.press('Enter');
    await expect
      .poll(() => page.evaluate(() => document.activeElement?.outerHTML))
      .toContain('Inspect this exact result');
    await axe(page, '#learningTicketReview');
    await tabTo(page, review.getByLabel('Your decision', { exact: true }));
    // Native Chromium/macOS select type-ahead is keyboard-operable; End and
    // popup Arrow keys are not reliably synthesized by headless macOS.
    await page.keyboard.press('r');
    await expect(review.getByLabel('Your decision', { exact: true })).toHaveValue('reject');
    await tabTo(page, review.getByLabel('Reason', { exact: true }));
    await page.keyboard.press('i');
    await expect(review.getByLabel('Reason', { exact: true })).toHaveValue('incorrect');
    await tabTo(
      page,
      review.getByRole('button', { name: 'Record this result-specific decision', exact: true })
    );
    await page.keyboard.press('Enter');
    await expect(review).toContainText(
      'Result-specific practice recorded. It is not a quality rating or proof of mastery.'
    );
    await page.reload();
    await expect(review).toContainText(
      'Result-specific practice recorded. It is not a quality rating or proof of mastery.'
    );
    expect((await learningStatus(request, base)).competencies.items[0].attempts).toBe(1);
    expect(errors).toEqual([]);
  });

  for (const theme of ['light', 'dark'])
    for (const width of [1365, 390]) {
      test(`practice, evidence, preferences and safe errors fit ${width}px ${theme}`, async ({
        page,
        request,
        context,
        browserName
      }) => {
        const errors: string[] = [];
        page.on('pageerror', error => errors.push(error.message));
        await page.setViewportSize({ width, height: 1100 });
        await page.emulateMedia({
          colorScheme: theme as 'light' | 'dark',
          reducedMotion: 'reduce'
        });
        await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
        await page.goto('/');
        await tabTo(page, page.locator('#cockpitQuestsToggle'));
        await page.keyboard.press('Enter');
        const board = page.locator('#learningMissions');
        await expect(board).toBeVisible();
        const snapshot = await learningStatus(request, base);
        await fits(board, width);
        await tabTo(page, board.getByRole('button', { name: 'Recorded practice', exact: true }));
        await page.keyboard.press('Enter');
        await expect(board).toContainText('Recorded practice · not a skill score');
        await axe(page, '#learningMissions');
        await tabTo(
          page,
          board.getByRole('button', { name: 'Inspect delegate review 1', exact: true })
        );
        await page.keyboard.press('Enter');
        // This explicit evidence view contains canonical source text, not a new nonce.
        await expect(board).toContainText(hostileInput);
        expect(await board.locator('img').count()).toBe(0);
        await fits(board, width);
        await axe(page, '#learningMissions');
        await board
          .getByRole('heading', { name: 'Saved decision · exact reviewed result', exact: true })
          .evaluate(node => node.scrollIntoView({ block: 'start' }));
        await page.screenshot({ path: join(evidence, `evidence-${width}-${theme}.png`) });
        const recordedInput = board.getByRole('region', {
          name: 'Recorded execution input',
          exact: true
        });
        await tabTo(page, recordedInput);
        await recordedInput.evaluate(node => node.scrollIntoView({ block: 'start' }));
        await page.screenshot({ path: join(evidence, `literal-input-${width}-${theme}.png`) });
        await tabTo(
          page,
          board.getByRole('button', { name: 'Learning preferences', exact: true }),
          true
        );
        await page.keyboard.press('Enter');
        await expect(
          board.getByRole('heading', { name: 'Your learning preferences', exact: true })
        ).toBeFocused();
        await fits(board, width);
        await axe(page, '#learningMissions');
        await board
          .getByRole('heading', { name: 'Your learning preferences', exact: true })
          .evaluate(node => node.scrollIntoView({ block: 'start' }));
        await page.screenshot({ path: join(evidence, `preferences-${width}-${theme}.png`) });
        await board
          .locator('.learning-scopes')
          .evaluate(node => node.scrollIntoView({ block: 'start' }));
        await fits(board.locator('.learning-scopes'), width);
        await page.screenshot({ path: join(evidence, `scope-name-${width}-${theme}.png`) });
        await board.getByLabel('Coaching mode', { exact: true }).selectOption('off');
        await tabTo(
          page,
          board.getByRole('button', { name: 'Save learning preferences', exact: true })
        );
        await page.keyboard.press('Enter');
        await expect(board).toContainText('Coaching is off');
        await axe(page, '#learningMissions');
        await page.screenshot({ path: join(evidence, `off-${width}-${theme}.png`) });
        await tabTo(page, board.getByRole('button', { name: 'Learning preferences', exact: true }));
        await page.keyboard.press('Enter');
        await board.getByLabel('Coaching mode', { exact: true }).selectOption('on_request');
        await tabTo(
          page,
          board.getByRole('button', { name: 'Save learning preferences', exact: true })
        );
        await page.keyboard.press('Enter');
        await expect(board).not.toHaveAttribute('aria-busy', 'true');
        const afterPreferences = await learningStatus(request, base);
        expect(afterPreferences.competencies).toEqual(snapshot.competencies);
        expect(afterPreferences.instances).toEqual(snapshot.instances);
        await page.keyboard.press('Escape');
        await expect(page.locator('#cockpitQuestsToggle')).toBeFocused();
        await expect(board).not.toBeVisible();
        await page.keyboard.press('Enter');
        const session = browserName === 'chromium' ? await context.newCDPSession(page) : null;
        if (session) {
          await session.send('Network.enable');
          await session.send('Network.emulateNetworkConditions', {
            offline: false,
            latency: 1000,
            downloadThroughput: -1,
            uploadThroughput: -1
          });
          await tabTo(page, board.getByRole('button', { name: 'Refresh learning', exact: true }));
          await page.keyboard.press('Enter');
          await expect(board.getByRole('status')).toHaveText('Saving…');
          await session.send('Network.emulateNetworkConditions', {
            offline: false,
            latency: 0,
            downloadThroughput: -1,
            uploadThroughput: -1
          });
          await expect(board).not.toHaveAttribute('aria-busy', 'true');
        }
        await context.setOffline(true);
        await tabTo(page, board.getByRole('button', { name: 'Refresh learning', exact: true }));
        await page.keyboard.press('Enter');
        await expect(board.getByRole('status')).toHaveText(
          /connection and refresh saved learning/i
        );
        await context.setOffline(false);
        await page.keyboard.press('Enter');
        await expect(board).not.toHaveAttribute('aria-busy', 'true');
        expect((await learningStatus(request, base)).owner.revision).toBe(
          afterPreferences.owner.revision
        );
        await page.goto(ticketHref);
        const review = page.locator('#learningTicketReview');
        await expect(review).toContainText(
          'Result-specific practice recorded. It is not a quality rating or proof of mastery.'
        );
        await fits(review, width);
        expect(
          await page
            .locator('#workspace-task-subtitle')
            .evaluate(node => node.scrollWidth <= node.clientWidth + 1)
        ).toBe(true);
        await axe(page, '#learningTicketReview');
        await review.scrollIntoViewIfNeeded();
        await page.screenshot({ path: join(evidence, `completed-ticket-${width}-${theme}.png`) });
        await session?.detach();
        expect(
          await (await request.get(process.env.ORI_LEARNING_DEMO_PROVIDER + '/counts')).json()
        ).toEqual({ calls: 1 });
        expect(errors).toEqual([]);
      });
    }

  test('a narrow dark blocked contract remains stoppable and an empty workspace set stays quiet', async ({
    page,
    request
  }) => {
    await page.setViewportSize({ width: 390, height: 1100 });
    await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
    await page.goto('/');
    await tabTo(page, page.locator('#cockpitQuestsToggle'));
    await page.keyboard.press('Enter');
    const board = page.locator('#learningMissions');
    await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
    await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
    await board.getByRole('button', { name: 'Start learning — no execution', exact: true }).click();
    await expect(
      board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true })
    ).toBeVisible();
    const removed = await request.delete(
      `${base}/api/workspaces/${workspaceID}?confirm=true&delete_sessions=true`
    );
    expect(removed.ok()).toBe(true);
    await board.getByRole('button', { name: 'Refresh learning', exact: true }).click();
    await expect(board).toContainText('The original workspace or evidence is unavailable');
    await fits(board, 390);
    await axe(page, '#learningMissions');
    await page.screenshot({ path: join(evidence, 'blocked-390-dark.png') });
    await tabTo(
      page,
      board.getByRole('button', { name: 'Stop mission — keep actual work', exact: true })
    );
    await page.keyboard.press('Enter');
    await expect(
      board.getByRole('button', { name: 'Choose a workspace', exact: true })
    ).toBeVisible();
    await tabTo(page, board.getByRole('button', { name: 'Choose a workspace', exact: true }));
    await page.keyboard.press('Enter');
    await expect(board).toContainText('No workspace is available. Learning can wait');
    await axe(page, '#learningMissions');
    await page.screenshot({ path: join(evidence, 'empty-390-dark.png') });
    expect((await learningStatus(request, base)).competencies.items[0].attempts).toBe(1);
  });
});
