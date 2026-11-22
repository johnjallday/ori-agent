import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { drawer, FIXTURE_MODEL, seedHiredAssistant, seedWorkspace } from './helpers/operate-ori';

// Only seed synthetic owner records in the runner's isolated wt demo store.
// The conversation, Today reads, links and transitions all use the real host.
test('wt demo: the Assistant reads the same Needs you records as Home', async ({
  page,
  request
}) => {
  const configured = process.env.ORI_ASSISTANT_TODAY_SANDBOX;
  test.skip(!configured || !process.env.ORI_WORKSPACE_PROVIDER_FIXTURE, 'Use --assistant-today');
  const sandbox = await realpath(configured!);
  expect(basename(sandbox)).toMatch(/^ori-demo\./);
  const marker = (await readFile(join(sandbox, '.ori-demo-owner'), 'utf8')).trim();
  expect(marker).toBe(`worktree=${await realpath(process.cwd())}`);
  const database = await realpath(join(sandbox, 'sessions.db'));
  expect(database).toBe(join(sandbox, 'sessions.db'));
  const dark = process.env.ORI_ASSISTANT_TODAY_VIEW === 'compact-dark';
  if (dark) {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
  }
  const evidence = join(
    process.cwd(),
    'tasks/evidence-assistant-today-actions',
    dark ? 'compact-dark' : 'desktop-light'
  );
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const shot = (name: string) => page.screenshot({ path: join(evidence, name) });
  const sql = (query: string, ...args: string[]) => {
    execFileSync('python3', [
      '-c',
      'import sqlite3,sys; c=sqlite3.connect(sys.argv[1], uri=True); assert c.execute(sys.argv[2],sys.argv[3:]).rowcount == 1; c.commit()',
      `file:${database}?mode=rw`,
      query,
      ...args
    ]);
  };
  await seedHiredAssistant(request, 'today-actions');
  const title = 'Reply to Mina';
  async function capture(label: string) {
    const response = await request.post('/api/personal-hq/followups', {
      data: { title: label, category: 'waiting_on' }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).followup;
  }
  const hqItem = await capture(title);
  const mailItem = await capture(title);
  const email = await seedWorkspace(request, 'Email Ops', ['Postmaster'], FIXTURE_MODEL);
  expect(email.folder_slug).toMatch(/^[a-z0-9-]+$/);
  const manifest = await realpath(
    join(sandbox, 'Ori Workspaces', email.folder_slug, 'workspace.json')
  );
  expect(manifest.startsWith(sandbox + '/')).toBeTruthy();
  const definition = JSON.parse(await readFile(manifest, 'utf8'));
  expect(definition.id).toBe(email.id);
  definition.template_provenance = { template_id: 'email-ops', builtin: true };
  await writeFile(manifest, JSON.stringify(definition));
  const config = await request.put('/api/personal-hq/brief/config', {
    data: {
      timezone: 'America/New_York',
      schedule_days: ['mon'],
      schedule_time: '08:00',
      schedule_enabled: false,
      scope: 'selected',
      selected_workspace_ids: [email.id]
    }
  });
  expect(config.ok(), await config.text()).toBeTruthy();
  sql('UPDATE personal_hq_followup SET workspace_id=? WHERE id=?', email.id, mailItem.id);
  const homeRead = async () =>
    (await (await request.get('/api/personal-assistant/today')).json()).today;
  await expect
    .poll(
      async () =>
        (await homeRead()).needs_you.items.filter((item: any) => item.title === title).length
    )
    .toBe(2);

  if (!dark) await page.addInitScript(() => localStorage.setItem('ori-theme', 'light'));
  await page.goto('/?panel=today');
  await expect(page.locator('html')).toHaveAttribute('data-bs-theme', dark ? 'dark' : 'light');
  const assistant = drawer(page);
  await assistant.open();
  const home = page.locator('#personalAssistantNeedsYou');
  await home.locator('summary', { hasText: 'Also needs you' }).click();
  await expect(home.getByText(title, { exact: true })).toHaveCount(2);
  await shot('02-home-needs-you.png');
  const answer = await assistant.send('What needs me today?');
  const list = page.getByRole('region', { name: 'Needs you · Today' });
  await expect(list.getByRole('link', { name: title, exact: true })).toHaveCount(2);
  expect(
    answer.today.rows.filter((row: any) => row.title === title).map((row: any) => row.owner)
  ).toEqual(['My HQ', 'Email Ops']);
  await shot('02-chat-needs-you.png');
  const owning = list
    .getByRole('link', { name: title })
    .and(list.locator(`a[href$="follow_up=${mailItem.id}"]`));
  await owning.click();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${email.folder_slug}\\?follow_up=`));
  await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Postmaster');
  await assistant.open();
  await expect(list).toHaveCount(0);
  let personalAsks = 0;
  page.on('request', event => {
    if (new URL(event.url()).pathname === '/api/home-assistant/ask') personalAsks++;
  });
  const commander = page.waitForRequest(
    event => new URL(event.url()).pathname === '/api/chat' && event.method() === 'POST'
  );
  await page.locator('#personalAssistantInput').fill('What needs me today?');
  await page.locator('#personalAssistantSend').click();
  expect((await commander).postDataJSON()).toMatchObject({ agent_name: 'Postmaster' });
  expect(personalAsks).toBe(0);
  await expect(list).toHaveCount(0);

  // A failed value scan in one owner only, not a stubbed Today response.
  sql("UPDATE personal_hq_followup SET due_at=x'ff' WHERE id=?", mailItem.id);
  await page.goto('/');
  await assistant.open();
  const partial = await assistant.send('What needs me today?');
  expect(partial.today.health).toBe('partial');
  expect(partial.response).not.toMatch(/nothing needs you/i);
  expect(
    partial.today.rows.filter((row: any) => row.title === title).map((row: any) => row.owner)
  ).toEqual(['My HQ']);
  await expect(list.getByRole('link', { name: title, exact: true })).toHaveAttribute(
    'href',
    new RegExp(`follow_up=${hqItem.id}$`)
  );
  await shot('02-partial-needs-you.png');
  sql('UPDATE personal_hq_followup SET due_at=NULL WHERE id=?', mailItem.id);

  const stored = async (id: string) => {
    const response = await request.get(`/api/personal-hq/followups/item?id=${id}`);
    expect(response.ok()).toBeTruthy();
    return (await response.json()).followup;
  };
  const before = await Promise.all([stored(hqItem.id), stored(mailItem.id)]);
  await assistant.send('What needs me today?');
  const latest = list.last();
  await expect(
    latest.getByRole('button', { name: `Select ${title} in My HQ for snooze` })
  ).toHaveCount(1);
  await expect(latest.getByRole('button', { name: /Email Ops/ })).toHaveCount(0);
  const pending = await assistant.send('Snooze until Monday');
  expect(pending.snooze.status).toBe('choose');
  const review = page.getByRole('region', { name: 'Follow-up snooze' }).last();
  const choose = review.getByRole('button', { name: `Choose My HQ: ${title}` });
  await expect(review.getByRole('link', { name: `Open ${title} in Email Ops` })).toBeVisible();
  await expect(review.getByRole('button', { name: /Choose Email Ops/ })).toHaveCount(0);
  await shot('03-snooze-choices.png');
  await choose.press('Enter');
  await expect(review.getByRole('heading', { name: 'Review snooze' })).toBeVisible();
  await expect(review.locator('time')).toHaveText(
    /Suggested: Monday, \d+ \w+ \d{4} at 09:00 E[DS]T \(UTC-0[45]:00\) · America\/New_York/
  );
  await shot('03-review-snooze.png');
  await review.getByRole('button', { name: 'Cancel' }).press('Enter');
  await expect(review.getByRole('status')).toHaveText('Snooze cancelled. Nothing changed.');
  expect(await Promise.all([stored(hqItem.id), stored(mailItem.id)])).toEqual(before);
  await shot('03-cancelled-snooze.png');

  await assistant.send('What needs me today?');
  await list
    .last()
    .getByRole('button', { name: `Select ${title} in My HQ for snooze` })
    .press('Enter');
  const checked = await assistant.send('Snooze until Monday');
  expect(checked.snooze.status).toBe('review');
  await expect(review.locator('time')).toHaveAttribute('datetime', checked.snooze.until);
  // Choose a future Sunday just before the local week rolls over. The
  // server's adjustment preview, not the browser's timezone, owns the instant.
  const monday = checked.snooze.until.slice(0, 10);
  const localSunday = new Date(Date.parse(`${monday}T00:00:00Z`) + 6 * 24 * 60 * 60 * 1000)
    .toISOString()
    .slice(0, 10);
  const field = review.getByLabel('Change the date or time');
  const newTime = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/today/action'
  );
  await field.fill(`${localSunday} 23:59`);
  await field.press('Enter');
  const checkedAgain = await (await newTime).json();
  expect(checkedAgain.status).toBe('review');
  expect(checkedAgain.review).not.toBe(checked.snooze.review);
  expect(checkedAgain.until).not.toBe(checked.snooze.until);
  await expect(review.locator('time')).toHaveAttribute('datetime', checkedAgain.until);
  expect((await stored(hqItem.id)).status).toBe('active');
  const commit = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/today/action'
  );
  await review.getByRole('button', { name: 'Confirm snooze' }).press('Enter');
  expect((await (await commit).json()).status).toBe('saved');
  await expect(review.getByRole('status')).toHaveText('Snooze saved.');
  await expect(review.locator('time')).toHaveAttribute('datetime', checkedAgain.until);
  await expect
    .poll(async () => (await homeRead()).needs_you.items.some((item: any) => item.id === hqItem.id))
    .toBe(false);
  const after = await stored(hqItem.id);
  expect(after).toMatchObject({
    id: hqItem.id,
    title,
    workspace_id: hqItem.workspace_id,
    status: 'snoozed'
  });
  expect(Date.parse(after.snoozed_until)).toBe(Date.parse(checkedAgain.until));
  expect(await stored(mailItem.id)).toEqual(before[1]);
  await shot('04-snooze-saved.png');
  await review.getByRole('link', { name: `Open ${title} in My HQ` }).click();
  await page.reload();
  const owner = page.getByRole('article', { name: `Selected follow-up: ${title}` });
  await expect(owner).toContainText(/Snoozed until/);
  await shot('04-owning-follow-up.png');

  const otherItem = await capture('Finish the notes');
  // The manual capture endpoint may use Email Ops once it exists. This
  // synthetic second-tab conflict deliberately exercises the HQ owner.
  sql(
    'UPDATE personal_hq_followup SET workspace_id=? WHERE id=?',
    hqItem.workspace_id,
    otherItem.id
  );
  await page.goto('/');
  await assistant.open();
  await assistant.send('What needs me today?');
  await list
    .last()
    .getByRole('button', { name: 'Select Finish the notes in My HQ for snooze' })
    .press('Enter');
  const stale = await assistant.send('Snooze Finish the notes until Monday');
  expect(stale.snooze.status).toBe('review');
  const secondTab = await page.context().newPage();
  await secondTab.goto(`/workspaces/my-hq?follow_up=${otherItem.id}`);
  const staleRow = secondTab.getByRole('article', { name: 'Selected follow-up: Finish the notes' });
  await staleRow.getByRole('button', { name: /Done|Complete/ }).click();
  await expect.poll(async () => (await stored(otherItem.id)).status).toBe('completed');
  await review.getByRole('button', { name: 'Confirm snooze' }).press('Enter');
  await expect(review.getByRole('status')).toContainText('changed after review');
  expect((await stored(otherItem.id)).status).toBe('completed');
  await shot('04-stale-snooze-refused.png');
  await secondTab.close();
  await page.reload();
  await assistant.open();
  await expect(page.locator('#homeAssistantConversation')).toContainText('Finish the notes');
  await expect(list).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Confirm snooze' })).toHaveCount(0);
});
