import { expect, type Locator, type Page } from '@playwright/test';

/**
 * Home has one view switcher: the Desk rail, with two destinations, Files and
 * Map. (Files is the workspace tree; the address is still `?view=tree`.) On a
 * narrow screen the rail is not drawn and the Destination picker takes its
 * place with the same two options.
 *
 * Specs go through these helpers instead of naming the controls, so the next
 * change to the switcher is a change here and not in twenty files.
 */
export type HomeDestination = 'files' | 'map';

const VALUE: Record<HomeDestination, 'tree' | 'map'> = { files: 'tree', map: 'map' };

/** The rail's link for a destination (hidden on a narrow screen). */
export function railLink(page: Page, destination: HomeDestination): Locator {
  return page.locator(`.desk-rail [data-desk-destination="${VALUE[destination]}"]`);
}

/** The narrow screen's Destination picker. */
export function destinationPicker(page: Page): Locator {
  return page.locator('[data-map-picker]');
}

/**
 * The destination controls that are on screen right now: the rail's two links
 * on a wide screen, the one picker on a narrow one. For target-size checks.
 */
export function destinationControls(page: Page): Locator {
  return page.locator('.desk-rail [data-desk-destination]:visible, [data-map-picker]:visible');
}

/** Go to a destination with whichever control is on screen. */
export async function goToDestination(page: Page, destination: HomeDestination): Promise<void> {
  const link = railLink(page, destination);
  if (await link.isVisible()) {
    await link.click();
  } else {
    await destinationPicker(page).selectOption(VALUE[destination]);
  }
}

export const goToFiles = (page: Page) => goToDestination(page, 'files');
export const goToMap = (page: Page) => goToDestination(page, 'map');

/** Assert which destination Home says it is on, in the rail and the picker. */
export async function expectDestination(page: Page, destination: HomeDestination): Promise<void> {
  await expect(destinationPicker(page)).toHaveValue(VALUE[destination]);
  await expect(page.locator('.desk-rail [aria-current="page"]')).toHaveAttribute(
    'data-desk-destination',
    VALUE[destination]
  );
}
