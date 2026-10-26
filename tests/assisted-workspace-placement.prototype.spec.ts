import { test, expect, type Page } from '@playwright/test';
import { mkdir } from 'node:fs/promises';

// Inert presentation fixture injected only by Playwright. No application route,
// query flag or mock setup API exists. Not proof of the real handoff/permissions.
async function prototype(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).OriWorkspaceMap));
  const effects: string[] = [];
  await page.route('**/api/**', route => {
    if (route.request().method() !== 'GET') {
      effects.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
      return route.abort('blockedbyclient');
    }
    return route.fallback();
  });
  await page.addStyleTag({ url: '/css/assisted-workspace-placement.css' });
  await page.addStyleTag({
    content: `
    #placement-prototype { position: fixed; inset: 0; z-index: 10010; background: var(--bg-primary); display: grid; grid-template-columns: minmax(0,1fr) minmax(340px,520px); }
    #placement-prototype-map { min-width:0; height:100vh; }
    #placement-prototype-map .ws-map-layout { grid-template-columns: minmax(0,1fr); height:100%; }
    #placement-prototype-map .ws-map-theatre { height:100%; padding:0; }
    #placement-prototype-map [data-ws-map-viewport] { height:100%; }
    #placement-prototype-panel { overflow:auto; padding:16px; }
    #placement-prototype-entry { position:absolute; left:24px; top:24px; z-index:10; padding:16px; background:var(--bg-secondary); border:1px solid var(--border-color); border-radius:12px; }
    #placement-prototype-entry button { padding:10px; margin:4px; }
    @media(max-width:700px) { #placement-prototype { display:block; overflow:auto; } #placement-prototype-map { display:none; } #placement-prototype-panel { padding:8px; } #placement-prototype-entry { position:static; } }
  `
  });
  await page.evaluate(async () => {
    const moduleURL = '/js/modules/assisted-workspace-placement.js';
    const { mountPlacementPanel } = await import(moduleURL);
    const host = document.createElement('main');
    host.id = 'placement-prototype';
    host.innerHTML = `<div id="placement-prototype-map" class="ws-map"></div><div id="placement-prototype-panel"></div><section id="placement-prototype-entry" aria-label="Prototype assistant proposal"><strong>Prototype · Your assistant suggests</strong><p>A File Janitor workspace for Music.</p><button data-entry="place">Review setup</button><button data-entry="suggested">Use automatic placement</button></section>`;
    document.body.append(host);
    const map = (window as any).OriWorkspaceMap;
    map.mount(document.getElementById('placement-prototype-map'), {
      workspaces: [{ id: 'prototype-existing', name: 'Studio', template_id: 'writing-project' }],
      hideChrome: true,
      selectOnly: true,
      noAutoSelect: true
    });
    const view: any = {
      prototype: true,
      draft: { name: 'Music', mission: 'Help me review and file my incoming music documents.' },
      team: [{ name: 'File Curator', action: 'create' }],
      folder: { name: 'Music', path: '/synthetic/Inbox/Music' },
      monitoring: false,
      message:
        'Prototype only. The earlier folder selection observed metadata; it did not grant access or run a filing scan.'
    };
    const panel = mountPlacementPanel(
      document.getElementById('placement-prototype-panel'),
      (intent: any) => {
        // Like the real owner, preserve an actively typed value until change.
        if (intent.action === 'dirty') return;
        if (intent.action === 'edit') {
          if (intent.field === 'monitoring') view.monitoring = intent.value;
          else view.draft[intent.field] = intent.value;
        } else if (intent.action === 'place') {
          place();
        } else if (intent.action === 'suggested') {
          map.endPlacement('prototype-only');
          view.candidate = null;
          view.onMap = false;
          view.automatic = true;
        } else if (intent.action === 'back') {
          map.endPlacement('prototype-only');
          view.onMap = false;
        } else if (intent.action === 'agent') {
          view.team = [{ name: 'Saved Curator', action: 'attach' }];
        } else if (intent.action === 'folder') {
          view.folder = { name: 'Music', path: '/synthetic/Another/Music' };
        } else if (intent.action === 'cancel') {
          map.endPlacement('prototype-only');
          view.canConfirm = false;
          view.review = null;
          view.message =
            'Draft cancelled. No workspace, agent, access, Filed folder, scan or monitoring was created.';
          panel.update(view);
          return;
        } else if (intent.action === 'confirm') {
          view.message =
            'Prototype: confirmation intentionally does nothing. No setup API is connected.';
        }
        render();
      }
    );
    function render() {
      // Fixture-owned projection only; real integration must use janitor-review.
      view.review = {
        workspace: view.draft,
        team: view.team,
        folder: view.folder,
        monitoring: view.monitoring,
        terms: { daily_scan_local_time: '09:00' }
      };
      view.canConfirm = true;
      view.canPlace = true;
      const focusedField = (document.activeElement as HTMLElement)?.dataset?.placementField;
      panel.update(view);
      if (focusedField)
        host.querySelector<HTMLInputElement>(`[data-placement-field="${focusedField}"]`)?.focus();
    }
    function place() {
      view.onMap = true;
      view.automatic = false;
      map.beginPlacement({
        token: 'prototype-only',
        mode: 'draft',
        candidate: view.candidate,
        preview: { name: view.draft.name, blueprintID: 'file-janitor', blueprintBuiltin: true },
        onSelect: (_token: string, candidate: any) => {
          view.candidate = candidate;
          render();
        },
        onBack: () => {
          view.onMap = false;
          view.message = 'Position selection closed. Your draft choices are kept.';
          render();
        }
      });
    }
    host.querySelectorAll('[data-entry]').forEach(button =>
      button.addEventListener('click', () => {
        document.getElementById('placement-prototype-entry')!.hidden = true;
        view.automatic = (button as HTMLElement).dataset.entry === 'suggested';
        render();
        host.querySelector<HTMLInputElement>('[data-placement-field="name"]')?.focus();
      })
    );
  });
  return effects;
}

for (const narrow of [false, true]) {
  test(`Prototype: ${narrow ? 'narrow keyboard suggested' : 'desktop pointer placement'} review and cancel are inert`, async ({
    page
  }) => {
    await page.setViewportSize(
      narrow ? { width: 390, height: 844 } : { width: 1440, height: 1100 }
    );
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const effects = await prototype(page);
    await page
      .getByRole('button', {
        name: narrow ? 'Use automatic placement' : 'Review setup',
        exact: true
      })
      .click();
    const panel = page.locator('#placement-prototype-panel');
    await expect(panel).toBeVisible();
    await panel.getByRole('textbox', { name: 'Workspace name', exact: true }).fill('Music inbox');
    await page.keyboard.press('Tab');
    await expect(panel.getByRole('region', { name: 'Before you confirm' })).toContainText(
      'Music inbox'
    );
    await panel.getByRole('button', { name: 'Choose agent' }).click();
    await expect(panel).toContainText('Saved Curator · saved agent');
    await expect(panel).toContainText('Off · manual scans only.');
    await mkdir('tasks/evidence-assisted-workspace-placement/group-1', { recursive: true });
    const prefix = `tasks/evidence-assisted-workspace-placement/group-1/prototype-${narrow ? 'narrow' : 'desktop'}`;
    await page.screenshot({ path: `${prefix}-review.png` });
    if (!narrow) {
      await panel.getByRole('button', { name: 'Next: place on map' }).click();
      await expect(panel.getByLabel('Workspace name')).toHaveCount(0);
      const canvas = page.locator('#placement-prototype-map [data-ws-map-viewport]');
      await canvas.click({ position: { x: 650, y: 350 } });
      await canvas.focus();
      await page.keyboard.press('ArrowRight');
      await page.keyboard.press('Enter');
      await expect(panel).toContainText('Requested position:');
      await expect(page.locator('[data-ws-map-placement-preview]')).toContainText('Draft');
    }
    await expect(panel.getByRole('button', { name: 'Create workspace & scan' })).toBeEnabled();
    await panel.getByRole('button', { name: 'Create workspace & scan' }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${prefix}-confirmation.png` });
    const cancel = panel.getByRole('button', { name: 'Cancel draft' });
    if (narrow) {
      await cancel.focus();
      await page.keyboard.press('Enter');
    } else await cancel.click();
    await expect(panel.getByRole('status')).toContainText('No workspace, agent, access');
    await expect(panel.getByRole('button', { name: 'Create workspace & scan' })).toBeDisabled();
    await page.screenshot({ path: `${prefix}-cancel.png` });
    expect(effects, 'prototype sends no mutating request').toEqual([]);
  });
}
