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
  const evidence = join(process.cwd(), 'tasks/evidence-assistant-today-actions');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const shot = (name: string) => page.screenshot({ path: join(evidence, name) });
  const sql = (query: string, ...args: string[]) => {
    execFileSync('python3', [
      '-c',
      'import sqlite3,sys; c=sqlite3.connect(sys.argv[1], uri=True); assert c.execute(sys.argv[2],sys.argv[3:]).rowcount == 1; c.commit()',
      `file:${join(sandbox, 'sessions.db')}?mode=rw`,
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

  await page.goto('/?panel=today');
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
  await page.locator('#personalAssistantInput').fill('What needs me today?');
  await page.locator('#personalAssistantSend').click();
  await expect(
    page.locator('#homeAssistantConversation [data-message-author="Postmaster"]')
  ).not.toHaveCount(0);
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
});
