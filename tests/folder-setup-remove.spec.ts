import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { openAttachMenu } from './helpers/assistant-menus';

// A confirmed folder setup that stopped stays in Needs you until the user
// removes it. These fixtures drive the real card with route mocks for the
// digest read and the abandon call; the server side is covered by Go tests.
const OFFER_ID = 'stopped-album';

const stoppedOffer = () => ({
  id: OFFER_ID,
  status: 'awaiting_outcome',
  verdict: 'project',
  folder: 'Album-4',
  subject: { name: 'Album-4', kind: 'project', is_root: true },
  capability: { recognized: 'music project', workspace: 'Song workspace' },
  decision: 'yes',
  choice: 'project',
  setup: {
    plan_digest: 'fixture-digest',
    status: 'stopped',
    stop_reason: 'interrupted',
    lines: [
      { kind: 'workspace', name: 'Creates a Song workspace named Album-4', state: 'done' },
      { kind: 'folder', name: 'Linked where it is', state: 'done' },
      { kind: 'agents', name: 'Hires an assistant', state: 'done' },
      { kind: 'task', name: 'Queues a first read-only task', state: 'failed' }
    ]
  }
});

const chips = [{ id: 'desktop', label: 'Desktop' }];

// Today and Needs you render only for a hired assistant with an HQ, so the demo
// sandbox hires one for real (once); the folder digest is the part that is mocked.
async function hireAssistant(request: APIRequestContext) {
  await request.post('/api/onboarding/skip');
  const read = async () =>
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  let relationship = await read();
  if (relationship.state === 'needs_hire') {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'remove-spec-hire',
        if_version: relationship.state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.ok(), await hire.text()).toBeTruthy();
    relationship = await read();
  }
  if (!relationship.hq_workspace_id) {
    const hq = await request.post('/api/personal-assistant/hq', {
      data: { request_id: 'remove-spec-hq', if_version: relationship.state_version, name: 'My HQ' }
    });
    expect(hq.ok(), await hq.text()).toBeTruthy();
  }
}

async function mountStopped(page: Page) {
  const state = { removed: false, abandons: [] as any[] };
  await page.route('**/api/personal-assistant/folder-digest', route =>
    route.fulfill({
      json: {
        folder_digest: {
          chips,
          picker_available: false,
          ...(state.removed ? {} : { offer: stoppedOffer() })
        }
      }
    })
  );
  return state;
}

// The sandbox hires once; parallel workers would race that setup.
test.describe.configure({ mode: 'serial' });

test.beforeEach(async ({ request }) => {
  await hireAssistant(request);
});

test('fixture Remove asks once, Keep and Esc back out, and a removed setup stays gone after a reload', async ({
  page
}) => {
  const state = await mountStopped(page);
  await page.route(`**/api/personal-assistant/folder-digest/offers/${OFFER_ID}/abandon`, route => {
    state.abandons.push(route.request().postDataJSON());
    state.removed = true;
    return route.fulfill({ json: { offer: { ...stoppedOffer(), status: 'abandoned' } } });
  });
  await page.goto('/?panel=today');
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  // The headline says what the setup needs, not only that it needs you.
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
    'Album-4 needs you to finish setup'
  );
  // A run saved without visual data draws no diagram and no placeholder text.
  await expect(card.locator('.folder-visual')).toHaveCount(0);
  await expect(card).not.toContainText('Setup preview');
  await expect(card).not.toContainText('Not reviewed');
  await expect(card).not.toContainText('No destination proposed');
  // It leads with the ask: the sentence and the way forward are in view, and
  // what it found and what it needed wait, collapsed, under the details.
  await expect(card.locator('#personalAssistantFolderOfferQuestion')).toContainText(
    'Setup was interrupted before it finished.'
  );
  const details = card.locator('#personalAssistantFolderDetails');
  await expect(details).not.toHaveAttribute('open', '');
  await expect(details.locator('#personalAssistantFolderRequirements')).toHaveCount(1);
  await expect(details.locator('#personalAssistantFolderFindings')).toHaveCount(1);
  await expect(details.locator('#personalAssistantFolderProgress')).toHaveCount(1);
  await expect(card.getByRole('region', { name: 'Setup requirements', exact: true })).toBeHidden();
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/no-diagram-pre-visual.png` });
  // Remove sits beside the headline; the row below keeps only the way forward.
  const head = card.locator('#personalAssistantFolderOfferHeadActions');
  await expect(head.getByRole('button')).toHaveText(['Remove']);
  await expect(
    card
      .locator('#personalAssistantFolderOfferActions')
      .getByRole('button', { name: 'Remove', exact: true })
  ).toHaveCount(0);
  await expect(card.getByRole('button', { name: 'Try again', exact: true })).toBeVisible();
  const headBox = await head.boundingBox();
  const rowBox = await card.locator('#personalAssistantFolderOfferActions').boundingBox();
  expect(headBox!.y + headBox!.height).toBeLessThanOrEqual(rowBox!.y);

  // First click opens the confirm, which says what stays. Nothing is sent yet.
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(head).toBeHidden();
  await expect(card.locator('#personalAssistantFolderOfferQuestion')).toContainText(
    'Remove this setup from Needs you?'
  );
  await expect(card.locator('#personalAssistantFolderOfferQuestion')).toContainText(
    'Kept: the workspace, the folder link, the agent.'
  );
  await expect(card.locator('#personalAssistantFolderOfferQuestion')).toContainText(
    'Show the folder again to start over.'
  );
  const keep = card.getByRole('button', { name: 'Keep', exact: true });
  await expect(keep).toBeFocused();
  await expect(card.getByRole('button', { name: 'Try again', exact: true })).toHaveCount(0);
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/remove-confirm.png` });

  // Keep restores the row and puts focus back on Remove.
  await keep.click();
  await expect(card.getByRole('button', { name: 'Try again', exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Remove', exact: true })).toBeFocused();

  // Esc leaves the confirm too, without closing the drawer.
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(keep).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(card.getByRole('button', { name: 'Try again', exact: true })).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  expect(state.abandons).toHaveLength(0);

  // Confirm: one request with a fresh id, the card leaves, and focus goes where
  // the user starts over: Add folder is in the composer's closed "+" menu, so
  // the composer itself.
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(card).toBeHidden();
  expect(state.abandons).toHaveLength(1);
  expect(String(state.abandons[0].request_id)).toMatch(/\S{8,}/);
  await expect(page.locator('#personalAssistantInput')).toBeFocused();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'Album-4 setup removed. Show the folder again to start over.'
  );
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/remove-done.png` });

  // After a reload the server no longer returns it: the chooser, not a card.
  await page.reload();
  await openAttachMenu(page);
  await expect(page.locator('#personalAssistantFolderChip')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderOffer')).toBeHidden();
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/remove-reloaded.png` });
  expect(state.abandons).toHaveLength(1);
});

test('fixture a refused Remove keeps the confirm and says why', async ({ page }) => {
  await mountStopped(page);
  let reason = 'setup_running';
  await page.route(`**/api/personal-assistant/folder-digest/offers/${OFFER_ID}/abandon`, route =>
    route.fulfill({
      status: 409,
      json: { error: 'server words', reason }
    })
  );
  await page.goto('/?panel=today');
  const card = page.locator('#personalAssistantFolderOffer');
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  const error = card.locator('#personalAssistantFolderOfferError');
  await expect(error).toHaveText('Setup is still running. Wait for it to stop.');
  await expect(card.getByRole('button', { name: 'Keep', exact: true })).toBeVisible();
  await expect(card).toBeVisible();

  reason = 'offer_decided';
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(error).toHaveText('This setup already moved on. Reload to see it.');
  await expect(card.getByRole('button', { name: 'Keep', exact: true })).toBeVisible();

  // Keep clears the message and the card is whole again.
  await card.getByRole('button', { name: 'Keep', exact: true }).click();
  await expect(error).toBeHidden();
  await expect(card.getByRole('button', { name: 'Try again', exact: true })).toBeVisible();
});

test('fixture a running setup has no Remove', async ({ page }) => {
  const running = stoppedOffer();
  running.setup.status = 'running';
  running.setup.stop_reason = '';
  running.setup.lines[3].state = 'working';
  await page.route('**/api/personal-assistant/folder-digest', route =>
    route.fulfill({
      json: { folder_digest: { chips, picker_available: false, offer: running } }
    })
  );
  await page.goto('/?panel=today');
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await expect(card.getByRole('button', { name: 'Remove', exact: true })).toHaveCount(0);
});

test('fixture a stopped run that saved its visual data still draws the diagram', async ({
  page
}) => {
  const withVisual = stoppedOffer() as any;
  withVisual.setup.visual = {
    entity: { kind: 'workspace', name: 'Album-4', operation: 'create' }
  };
  await page.route('**/api/personal-assistant/folder-digest', route =>
    route.fulfill({
      json: { folder_digest: { chips, picker_available: false, offer: withVisual } }
    })
  );
  await page.goto('/?panel=today');
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  // The diagram is evidence, so a stopped card keeps it under the details.
  await expect(card.locator('.folder-visual')).toBeHidden();
  await card.getByText('Full setup details', { exact: true }).click();
  await expect(card.locator('#personalAssistantFolderDetails .folder-visual')).toBeVisible();
  await expect(card.locator('.folder-visual')).toContainText('Album-4');
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/diagram-with-visual.png` });
});
