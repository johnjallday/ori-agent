import { expect, test } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { drawer, seedHiredAssistant, seedWorkspace, taskCount } from './helpers/operate-ori';

test('wt demo: ticket and task fill the same composer; a question does not', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_OPERATETICKET_SANDBOX;
  test.skip(!sandbox || !process.env.ORI_WORKSPACE_PROVIDER_FIXTURE, 'Use --operate-ticket');
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence = join(process.cwd(), 'tasks/evidence-assistant-today-actions');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await seedHiredAssistant(request, 'operate-ticket');
  const album = await seedWorkspace(request, 'Album Demo', ['Producer', 'Mixer']);
  const form = page.locator('#taskModal');
  const assistant = drawer(page);
  const runs = async () => {
    const response = await request.get(`/api/workspaces/${album.id}/runs`);
    expect(response.ok()).toBeTruthy();
    return (await response.json()).runs ?? [];
  };
  for (const noun of ['ticket', 'task']) {
    await page.goto('/');
    await assistant.open();
    await assistant.setWatch(true);
    const reply = await assistant.send(
      `Prepare a ${noun} for Mixer in the Album Demo workspace to mix the chorus`
    );
    expect(reply.confirmation).toBeUndefined();
    expect(reply.operation).toMatchObject({ kind: 'task', status: 'prepared' });
    await expect(form).toBeVisible();
    await expect(page.locator('#taskModalTitle')).toHaveText('Review ticket for Mixer');
    await expect(page.locator('#taskModalDescription')).toHaveValue('Mix the chorus');
    await expect(page.locator('#taskModalAssignment option:checked')).toHaveText('Mixer');
    expect(await taskCount(request, album.id)).toBe(0);
    expect(await runs()).toHaveLength(0);
    await page.screenshot({ path: join(evidence, `01-${noun}-composer.png`) });
    await page.locator('#taskModalClose').click();
    await expect(form).toBeHidden();
  }
  await page.goto('/');
  await assistant.open();
  const question = await assistant.send('Should I create a ticket in the Album Demo workspace?');
  expect(question.confirmation).toBeUndefined();
  expect(question.operation).toBeUndefined();
  await expect(form).toBeHidden();
  expect(await taskCount(request, album.id)).toBe(0);
  expect(await runs()).toHaveLength(0);
  await page.screenshot({ path: join(evidence, '01-ticket-question.png') });
});
