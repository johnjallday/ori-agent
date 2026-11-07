import { test, expect, type APIResponse } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { installLocalCdn } from './helpers/offline-cdn';
import { attachFirst, prepareFolderCreationRuntime } from './helpers/folder-first-runtime';
import { startRosterStaffingModel } from './helpers/roster-staffing-model';

// The model alone is scripted. All folder observations, review owners, source
// guards, profile bindings and setup effects go through the current real server.
test('recognized build and collection review preserve and reuse the exact saved Home manager', async ({
  page,
  request
}, testInfo) => {
  const sandbox = process.env.ORI_MUSIC_HOME_SANDBOX || '';
  const reviewed = process.env.ORI_MUSIC_PROVIDER_MODE === 'reviewed';
  const recovery = process.env.ORI_STAFFING_FOLDER_RECOVERY === '1';
  test.skip(
    process.env.ORI_STAFFING_DEMO !== '1' ||
      process.env.ORI_MUSIC_HOME_ACCEPTANCE !== '1' ||
      !['local', 'reviewed'].includes(process.env.ORI_MUSIC_PROVIDER_MODE || '') ||
      !sandbox,
    'requires isolated music-home-demo --suite roster-folder or roster-folder-reviewed'
  );
  test.setTimeout(reviewed ? 360_000 : 240_000);
  page.setDefaultTimeout(20_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const model = await startRosterStaffingModel();
  model.candidatePurpose('music-project-management');
  const json = async (response: APIResponse) => {
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  const names = async () =>
    ((await json(await request.get('/api/agents'))).agents as { name: string }[])
      .map(agent => agent.name)
      .sort();
  const manager = recovery
    ? `Aster ${Date.now()} North Star Portfolio Coordination and Handoffs`
    : `Aster ${Date.now()}`;
  let rolePUTs = 0;
  page.on('request', outgoing => {
    if (outgoing.method() === 'PUT' && /\/roles\//.test(new URL(outgoing.url()).pathname))
      rolePUTs++;
  });
  try {
    await request.post('/api/onboarding/skip');
    await prepareFolderCreationRuntime(request);
    const documents = join(sandbox, 'Documents');
    for (let i = 0; i < 5; i++) {
      const folder = join(documents, `Roster album ${i}`);
      await mkdir(folder, { recursive: true, mode: 0o750 });
      await writeFile(join(folder, 'Song.rpp'), `synthetic metadata fixture ${i}`, { mode: 0o600 });
    }
    let reviewedSource = '';
    let bootstrapOfferID = '';
    if (reviewed) {
      const installed = (await json(await request.get('/api/plugins'))).plugins;
      expect(
        installed.some((entry: { name: string }) => entry.name === 'music-project-management')
      ).toBe(false);
      const bootstrap = await json(
        await request.post('/api/personal-assistant/folder-digest/scan', {
          data: { chip: 'documents' }
        })
      );
      bootstrapOfferID = bootstrap.offer.id;
      await json(
        await request.post(
          `/api/personal-assistant/folder-digest/offers/${bootstrapOfferID}/decide`,
          { data: { request_id: randomUUID(), decision: 'yes', choice: 'project' } }
        )
      );
      const endpoint = `/api/personal-assistant/folder-digest/offers/${bootstrapOfferID}/home-provider`;
      const preview = (await json(await request.post(endpoint, { data: {} }))).home_provider;
      expect(preview.development_copy).not.toBe(true);
      expect(preview.source).toMatch(
        /^https:\/\/github\.com\/johnjallday\/music-project-management#sha=[0-9a-f]{40}$/
      );
      const confirmed = (
        await json(
          await request.post(endpoint, {
            data: { confirm: true, reviewed_version: preview.version }
          })
        )
      ).home_provider;
      expect(confirmed.ready).toBe(true);
      expect(confirmed.development_copy).not.toBe(true);
      reviewedSource = preview.source;
      const plugins = (await json(await request.get('/api/plugins'))).plugins;
      expect(
        plugins.find((entry: { name: string }) => entry.name === 'music-project-management').source
      ).toBe(reviewedSource);
    }
    const catalog = (await json(await request.get('/api/workspaces/group-templates')))
      .group_templates;
    const template = catalog.find(
      (entry: { name: string }) => entry.name === 'Music Production Home'
    );
    expect(template).toBeTruthy();
    // Seed a real capability-bearing saved manager through canonical creation,
    // then retain it through supported removal. No profile files are hand-edited.
    const exact = {
      group_template_id: template.id,
      revision: template.revision,
      name: 'Music fixture Home',
      role_staffing: [{ role_id: 'portfolio_manager', mode: 'create', name: manager }]
    };
    const review = (
      await json(await request.post('/api/workspaces/group-templates/review', { data: exact }))
    ).group_template_review;
    const created = (
      await json(
        await request.post('/api/workspaces/group-templates/commit', {
          data: { ...exact, group_review_token: review.review_token, idempotency_key: randomUUID() }
        })
      )
    ).group_template;
    expect(created.home_staffing.roles[0].outcome).toBe('created');
    if (bootstrapOfferID) {
      await json(
        await request.post(
          `/api/personal-assistant/folder-digest/offers/${bootstrapOfferID}/resolve`,
          { data: { home_id: created.home_workspace_id, request_id: randomUUID() } }
        )
      );
    }
    const oldHome = await json(await request.get(`/api/workspaces/${created.home_workspace_id}`));
    const removal = await json(
      await request.post(`/api/workspaces/${oldHome.id}/assistant-program/remove-home/review`, {
        data: { state_revision: oldHome.assistant_program.state.state_revision }
      })
    );
    await json(
      await request.post(`/api/workspaces/${oldHome.id}/assistant-program/remove-home/commit`, {
        data: { token: removal.token }
      })
    );
    const standalone = (
      await json(
        await request.post('/api/workspaces', {
          data: {
            name: `Standalone assignment ${Date.now()}`,
            create_template_agents: false,
            existing_agent_names: [manager],
            entry_agent_name: manager
          }
        })
      )
    ).folder;
    await json(
      await request.patch(
        `/api/workspaces/${standalone.id}/agents/${encodeURIComponent(manager)}/instance-settings`,
        { data: { custom_instructions: 'PRIVATE_OTHER_ASSIGNMENT_CANARY' } }
      )
    );
    const before = await names();
    let version = (
      await json(await request.get(`/api/agents/${encodeURIComponent(manager)}/detail`))
    ).version;
    await installLocalCdn(page);
    // A recognized build's first explicit manual action establishes its existing
    // offer owner. It does not require a ranking call or accept setup.
    await attachFirst(page, request, join(documents, 'Roster album 0'));
    const buildTeam = page
      .locator('#workspaceBuildHomeStaffing:visible, #workspaceBuildPlanReviewHomeStaffing:visible')
      .first();
    await expect(buildTeam).toBeVisible({ timeout: 60_000 });
    await buildTeam
      .getByRole('button', { name: 'Assign an agent to Music Portfolio Manager', exact: true })
      .click();
    await buildTeam
      .getByRole('button', { name: `Assign ${manager} to Music Portfolio Manager`, exact: true })
      .click();
    await expect(buildTeam.locator('[data-role-id="portfolio_manager"]')).toContainText(manager);
    const build = await page.evaluate(() => (window as any).sessionManager.workspaceBuild.session);
    expect(build.plan.home_staffing.state).toBe('ready');
    expect(build.folder.recognition.offer_id).toBeTruthy();
    expect(build.setup).toBeFalsy();
    expect(model.calls()).toBe(0);
    expect(await names()).toEqual(before);
    await buildTeam.scrollIntoViewIfNeeded();
    await page.screenshot({
      path: testInfo.outputPath('03-build-manual-team.png'),
      fullPage: true
    });
    await json(
      await request.post(`/api/workspaces/build-sessions/${build.id}/abandon`, {
        data: { version: build.version }
      })
    );

    const scanned = await json(
      await request.post('/api/personal-assistant/folder-digest/scan', {
        data: { chip: 'documents' }
      })
    );
    expect(scanned.offer.portfolio.projects).toBe(5);
    expect(scanned.offer.home_staffing.state).toBe('needs_choice');
    expect(scanned.offer.plan.digest || '').toBe('');
    const offerID = scanned.offer.id;
    await page.goto('/?panel=today');
    const today = page.locator('#personalAssistantTodayStripToggle');
    const summary = page.locator('[data-source-setup-summary]');
    await expect
      .poll(
        async () => {
          if ((await today.getAttribute('aria-expanded')) === 'false') await today.click();
          return summary.isVisible();
        },
        { timeout: 20_000 }
      )
      .toBe(true);
    if (recovery) {
      await page.getByRole('button', { name: 'Switch to dark mode', exact: true }).click();
      await page.setViewportSize({ width: 390, height: 844 });
    }
    await summary.getByRole('button', { name: 'Review setup', exact: true }).click();
    const dialog = page.locator('#addFolderModal[data-source-setup-owner]');
    await expect(dialog).toBeVisible();
    const team = dialog.locator('#personalAssistantFolderHomeStaffing');
    await expect(team).toBeVisible();
    await expect(team.locator('[data-role-id="portfolio_manager"]')).toContainText('Missing');
    if (recovery) {
      model.mode('unavailable');
      const rank = team.getByRole('button', { name: 'Recommend saved agents', exact: true });
      await rank.focus();
      await page.keyboard.press('Enter');
      await expect(team).toContainText('unavailable');
      const assign = team.getByRole('button', {
        name: 'Assign an agent to Music Portfolio Manager',
        exact: true
      });
      await assign.focus();
      await page.keyboard.press('Enter');
      const search = team.getByRole('searchbox', {
        name: 'Find a saved agent for Music Portfolio Manager',
        exact: true
      });
      await search.fill('No such synthetic profile');
      await expect(team).toContainText(
        'No saved agents match this search. You can go back and create a new agent.'
      );
      await page.screenshot({
        path: testInfo.outputPath('04-empty-manual-search.png'),
        fullPage: true
      });
      await search.fill(manager);
      const pick = team.getByRole('button', {
        name: `Assign ${manager} to Music Portfolio Manager`,
        exact: true
      });
      await pick.focus();
      await page.keyboard.press('Enter');
      const row = team.locator('[data-role-id="portfolio_manager"]');
      await expect(row).toContainText(manager);
      await expect(row).toBeFocused();
      await expect(team.locator('[aria-live="polite"]')).toContainText('Review updated');
      expect(model.calls()).toBe(1);
      expect(await names()).toEqual(before);
      await row.scrollIntoViewIfNeeded();
      expect((await row.boundingBox())!.width).toBeLessThanOrEqual(390);
      await page.screenshot({
        path: testInfo.outputPath('04-unavailable-manual-dark-narrow.png'),
        fullPage: true
      });
      await row.getByRole('button', { name: 'Clear Music Portfolio Manager', exact: true }).click();
      model.mode('recommended');
      const release = model.holdNextRecommendation();
      await team
        .getByRole('button', {
          name: /^(Recommend saved agents|Refresh recommendation)$/,
          exact: true
        })
        .click();
      await expect.poll(() => model.calls()).toBe(2);
      await team.getByRole('button', { name: 'Clear pending choices', exact: true }).click();
      await expect(team.locator('[data-role-id="portfolio_manager"]')).toHaveAttribute(
        'data-state',
        'empty'
      );
      release();
      // Observe after delivery: the original clear, not the late advice, owns
      // the revision. No profile, accepted run or selected choice was created.
      await expect
        .poll(async () => {
          const snapshot = (
            await json(
              await request.get(`/api/personal-assistant/folder-digest?offer_id=${offerID}`)
            )
          ).folder_digest.offer;
          return {
            choices: snapshot.home_staffing.choices?.length || 0,
            advice: Boolean(snapshot.home_staffing.advice),
            setup: Boolean(snapshot.setup)
          };
        })
        .toEqual({ choices: 0, advice: false, setup: false });
    }
    await team
      .getByRole('button', {
        name: /^(Recommend saved agents|Refresh recommendation)$/,
        exact: true
      })
      .click();
    const use = team.getByRole('button', {
      name: `Use ${manager} for Music Portfolio Manager`,
      exact: true
    });
    await expect(use).toBeEnabled();
    expect(await names()).toEqual(before);
    await use.scrollIntoViewIfNeeded();
    await page.screenshot({
      path: testInfo.outputPath('03-folder-recommendation.png'),
      fullPage: true
    });
    await use.click();
    await expect(team.locator('[data-role-id="portfolio_manager"]')).toHaveAttribute(
      'data-state',
      'filled'
    );
    await expect(team.locator('[data-role-id="portfolio_manager"]')).toContainText(manager);
    if (recovery) {
      // A deliberate shared-profile edit in the disposable fixture must stale
      // the original selected witness. GET/reopen cannot renew or re-rank it.
      await json(
        await request.patch(`/api/agents/${encodeURIComponent(manager)}`, {
          data: {
            expected_version: version,
            confirm_shared_edit: true,
            description:
              'Coordinates revised music portfolio priorities and reviewed project handoffs.'
          }
        })
      );
      version = (await json(await request.get(`/api/agents/${encodeURIComponent(manager)}/detail`)))
        .version;
      await page.goto('/?panel=today');
      await expect
        .poll(
          async () => {
            if ((await today.getAttribute('aria-expanded')) === 'false') await today.click();
            return summary.isVisible();
          },
          { timeout: 20_000 }
        )
        .toBe(true);
      await summary.getByRole('button', { name: 'Review setup', exact: true }).click();
      await expect(team).toContainText('Clear');
      const stale = await json(
        await request.get(`/api/personal-assistant/folder-digest?offer_id=${offerID}`)
      );
      expect(stale.folder_digest.offer.home_staffing.state).toBe('stale');
      expect(stale.folder_digest.offer.home_staffing.choices[0].name).toBe(manager);
      expect(stale.folder_digest.offer.plan.digest || '').toBe('');
      expect(model.calls()).toBe(3);
      await team.locator('[data-role-id="portfolio_manager"]').scrollIntoViewIfNeeded();
      await page.screenshot({
        path: testInfo.outputPath('04-stale-original-choice.png'),
        fullPage: true
      });
      await team
        .locator('[data-role-id="portfolio_manager"]')
        .getByRole('button', { name: 'Clear Music Portfolio Manager', exact: true })
        .click();
      await team
        .getByRole('button', { name: 'Assign an agent to Music Portfolio Manager', exact: true })
        .click();
      await team
        .getByRole('button', { name: `Assign ${manager} to Music Portfolio Manager`, exact: true })
        .click();
      expect(model.calls()).toBe(3);
    }
    await dialog.getByRole('button', { name: 'Return to setup review', exact: true }).click();
    const final = dialog.locator('[data-source-creation-review]:visible');
    await expect(final).toContainText(`${manager} · Reuse saved agent`);
    await final.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await expect(final.locator('[data-placement-action="confirm"]')).toBeEnabled();
    await page.screenshot({
      path: testInfo.outputPath('03-folder-exact-team.png'),
      fullPage: true
    });
    const accepted = page.waitForResponse(
      response =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname ===
          `/api/personal-assistant/folder-digest/offers/${offerID}/setup`
    );
    await final.locator('[data-placement-action="confirm"]').click();
    await json(await accepted);
    let homeID = '';
    await expect
      .poll(
        async () => {
          const folders = (await json(await request.get('/api/workspaces'))).folders;
          const home = folders.find((entry: { kind: string }) => entry.kind === 'group');
          homeID = home?.id || '';
          if (!homeID) return '';
          const detail = await json(await request.get(`/api/workspaces/${homeID}`));
          return (
            detail.agent_instances?.find(
              (entry: { role_id: string }) => entry.role_id === 'portfolio_manager'
            )?.name || ''
          );
        },
        { timeout: 60_000 }
      )
      .toBe(manager);
    expect(homeID).not.toBe(oldHome.id);
    const readOffer = async () =>
      (await json(await request.get(`/api/personal-assistant/folder-digest?offer_id=${offerID}`)))
        .folder_digest.offer;
    await expect.poll(async () => (await readOffer()).status, { timeout: 90_000 }).toBe('resolved');
    const completed = await readOffer();
    expect(completed.home_result).toMatchObject({ state: 'ready', home_id: homeID });
    expect(completed.outcome.receipt).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ name: manager, detail: 'reused saved agent', assigned: true })
      ])
    );
    await expect(final.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible();
    await page.screenshot({
      path: testInfo.outputPath('03-folder-reuse-result.png'),
      fullPage: true
    });
    expect(await names()).toEqual(before);
    expect(
      (await json(await request.get(`/api/agents/${encodeURIComponent(manager)}/detail`))).version
    ).toBe(version);
    const other = await json(await request.get(`/api/workspaces/${standalone.id}`));
    expect(other.parent_id || '').toBe('');
    expect(
      other.agent_instances.find((entry: { name: string }) => entry.name === manager)
        .custom_instructions
    ).toBe('PRIVATE_OTHER_ASSIGNMENT_CANARY');
    const workspaces = (await json(await request.get('/api/workspaces'))).folders;
    expect(
      workspaces.filter((entry: { parent_id?: string }) => entry.parent_id === homeID)
    ).toHaveLength(0);
    const releaseInputs = [];
    if (reviewed) {
      for (const entry of (await json(await request.get('/api/plugins'))).plugins) {
        if (!['music-project-management', 'reaper-plugin'].includes(entry.name)) continue;
        expect(entry.source).toMatch(
          /^https:\/\/github\.com\/johnjallday\/(music-project-management|reaper-plugin)#sha=[0-9a-f]{40}$/
        );
        releaseInputs.push({ name: entry.name, version: entry.version, source: entry.source });
      }
      expect(releaseInputs).toHaveLength(2);
    }
    expect(model.calls()).toBe(recovery ? 3 : 1);
    expect(rolePUTs).toBe(0);
    await writeFile(
      testInfo.outputPath('03-folder-staffing-evidence.json'),
      JSON.stringify(
        {
          providerBoundary: 'scripted',
          recovery: recovery
            ? {
                unavailableManual: true,
                staleOriginalPreserved: true,
                clearBeatsLateAdvice: true,
                deliberateSourceEdit: true,
                keyboardFocus: true,
                emptySearchManualFallback: true,
                longName: true,
                viewport: '390x844',
                theme: 'dark'
              }
            : undefined,
          pluginInput: reviewed
            ? 'genuine reviewed published release'
            : 'exact local candidate, not reviewed-release acceptance',
          reviewedSource: reviewedSource || undefined,
          releaseInputs: reviewed ? releaseInputs : undefined,
          buildManual: { canonicalOffer: true, rankingCalls: 0, setupAccepted: false },
          collection: {
            homeID,
            manager,
            action: 'assign',
            profileCount: before.length,
            profileUnchanged: true,
            resolved: true,
            reusedReceipt: true,
            standaloneAssignmentUnchanged: true,
            projectsOpened: 0
          },
          recommendationCalls: model.calls(),
          rolePUTs
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
  } finally {
    await model.stop();
  }
});
