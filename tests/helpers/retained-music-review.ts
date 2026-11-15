import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { relative, sep } from 'node:path';

/** Real retained-owner fixture, not the current Add folder / hire-first entry. */
export async function seedRetainedMusicReview(
  request: APIRequestContext,
  path: string,
  library = false
) {
  const local = relative(realpathSync(tmpdir()), realpathSync(path));
  expect(local.split(sep)[0], 'retained fixtures require a disposable Ori sandbox').toMatch(
    /^(?:ori-e2e\.|ori-demo\.narrated-)/
  );
  const post = async (url: string, data: object) => {
    const response = await request.post(url, { data });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  const picked = await post('/api/folder-picker/select-path', { path });
  const draft_id = randomUUID();
  const selected = await post('/api/home-assistant/folder-context/select', {
    draft_id,
    revision: '',
    mode: 'selection',
    selection_token: picked.selection_token
  });
  const candidate =
    selected.hire_candidate || selected.observation.projects.find((item: any) => item.root)?.id;
  expect(candidate, 'the real observation must offer the reviewed root').toBeTruthy();
  const reviewed = await post('/api/home-assistant/folder-context/review', {
    draft_id,
    revision: '',
    selection_id: selected.observation.id,
    candidate_id: candidate,
    ...(library ? { structure: { group_type: 'library' } } : {})
  });
  expect(reviewed.conversation.id).toBeTruthy();
  return reviewed;
}

export async function resumeRetainedMusicReview(page: Page, conversationID: string, tree = false) {
  await page.goto(`/?panel=today${tree ? '&view=tree' : ''}`);
  await page.waitForFunction(
    () =>
      (window as any).PersonalAssistantConversation?._state.hydrated &&
      !(window as any).PersonalAssistantConversation.isLoading()
  );
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  expect(
    await page.evaluate(
      id => (window as any).PersonalAssistantConversation.resume(id),
      conversationID
    )
  ).toBe(true);
  await expect(page.locator('[data-source-setup-summary]:visible')).toHaveCount(1);
}

/** Open the retained source's actual shell, never the hire-first card. */
export async function openRetainedMusicDialog(page: Page) {
  const dialog = page.locator('#addFolderModal[data-source-setup-owner="music-folder"]');
  if (!(await dialog.isVisible()))
    await page
      .locator('[data-source-setup-summary]:visible')
      .getByRole('button', { name: /^(Review setup|View setup status)$/ })
      .click();
  await expect(dialog).toBeVisible();
  return dialog;
}

/** Reveal editable requirements through the source owner's own control. */
export async function retainedMusicOptions(page: Page) {
  const dialog = await openRetainedMusicDialog(page);
  const edit = dialog.getByRole('button', { name: 'Review choices & requirements', exact: true });
  if (await edit.isVisible()) await edit.click();
  const card = dialog.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  return card;
}

/** Stage an explicit required-role choice. This creates no profile or Home. */
export async function chooseRetainedHomeTeam(
  page: Page,
  {
    provider,
    model,
    name = 'Music Portfolio Manager'
  }: { provider: string; model: string; name?: string }
) {
  const offer = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  if (!offer.home_staffing || offer.home_staffing.state === 'ready' || offer.setup) return;
  const card = await retainedMusicOptions(page);
  const team = card.locator('#personalAssistantFolderHomeStaffing');
  // A stale staged choice is deliberately not reusable. Clear it through the
  // source control before explicitly choosing the requested fixture model.
  if (
    ['stale', 'unavailable'].includes(offer.home_staffing.state) &&
    offer.home_staffing.choices?.length
  ) {
    const clear = team.getByRole('button', { name: 'Clear pending choices', exact: true });
    await clear.click();
    await expect(clear).toHaveCount(0);
  }
  for (const role of offer.home_staffing.roles) {
    if (!role.required || role.held_name) continue;
    const row = team.locator(`[data-role-id="${role.role_id}"]`);
    if ((await row.getAttribute('data-state')) === 'filled') continue;
    await row
      .getByRole('button', { name: `Create an agent for ${role.label}`, exact: true })
      .click();
    const form = team.locator('.folder-home-staffing__editor');
    await expect(form).toBeVisible();
    await form.locator('[data-agent-create-field="name"]').fill(name);
    // Provider is derived from the selected option, not a separate control.
    const models = form.locator('[data-agent-create-field="model"]');
    await expect(models).toBeVisible();
    const index = await models
      .locator('option')
      .evaluateAll(
        (options, wanted) =>
          options.findIndex(
            option =>
              (option as HTMLOptionElement).value === wanted.model &&
              option.getAttribute('data-provider') === wanted.provider
          ),
        { provider, model }
      );
    expect(index, `fixture model ${provider}/${model} must be offered`).toBeGreaterThanOrEqual(0);
    await models.selectOption({ index });
    await expect(models.locator('option:checked')).toHaveAttribute('data-provider', provider);
    await form.getByRole('button', { name: 'Use for this role', exact: true }).click();
    await expect(form).toBeHidden();
    await expect(row).toHaveAttribute('data-state', 'filled');
  }
  await expect
    .poll(() =>
      page.evaluate(() => (window as any).PersonalAssistantFolder.current()?.home_staffing?.state)
    )
    .toBe('ready');
}

export async function returnToRetainedMusicReview(page: Page) {
  const dialog = await openRetainedMusicDialog(page);
  const back = dialog.getByRole('button', { name: 'Return to setup review', exact: true });
  if (await back.isVisible()) {
    await expect(back).toBeEnabled();
    await back.click();
  }
  const review = dialog.locator('[data-source-creation-review]:visible');
  await expect(review).toBeVisible();
  return review;
}

/** The manager's separate, explicit project consent after the Home exists. */
export async function openRetainedSongFromHome(
  page: Page,
  home: { folder_slug: string },
  song: string,
  brief?: string,
  keyboard = false
): Promise<Locator> {
  await page.goto(`/workspaces/${home.folder_slug}?mode=map&panel=assistant`);
  const offer = page
    .locator('#personalAssistantHomeOffer .pa-home-offer')
    .filter({ hasText: song });
  await expect(offer).toBeVisible();
  if (brief !== undefined) {
    await offer.locator('textarea').fill(brief);
    await offer.locator('textarea').press('Tab');
  }
  await page.waitForFunction(
    () => (window as any).OriWorkspaceMap?.getLayoutState?.().status === 'ready'
  );
  const open = offer.getByRole('button', { name: `Open ${song} & hire`, exact: true });
  if (keyboard) {
    await open.focus();
    await expect(open).toBeFocused();
    await open.press('Enter');
  } else await open.click();
  const automatic = page
    .locator('[data-ws-map-placement-controls]')
    .getByRole('button', { name: 'Use automatic placement', exact: true });
  if (keyboard) {
    await automatic.focus();
    await expect(automatic).toBeFocused();
    await automatic.press('Enter');
  } else await automatic.click();
  await expect(offer).toHaveAttribute('data-state', 'opened', { timeout: 120_000 });
  return offer;
}
