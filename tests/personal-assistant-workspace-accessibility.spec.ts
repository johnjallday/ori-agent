import { test, expect } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Keyboard use, screen-reader semantics and narrow layouts for the
// workspace-aware drawer: production browser, Route/Ask, readers and stores
// under `wt demo`. Everything is driven from the keyboard unless a step says
// otherwise. The loopback provider is the same deterministic stand-in the other
// demos use; this checks the drawer, not a vendor model. No assistive
// technology is run: roles, names, live regions, focus and layout are asserted.
test('wt demo: the workspace-aware drawer works from the keyboard, announces context and fits a narrow screen', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_ACCESSIBILITY_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(
    !sandbox || !provider,
    'Use python3 scripts/assistant-workspace-demo.py --accessibility'
  );
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });

  const assets = join(sandbox!, 'Music', 'Album-1 assets');
  await mkdir(join(assets, 'lyrics'), { recursive: true, mode: 0o750 });
  await writeFile(join(assets, 'lyrics', 'bridge.txt'), 'The bridge is in D minor.', {
    mode: 0o600
  });

  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'workspace-accessibility-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'workspace-accessibility-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  const create = async (name: string) => {
    const response = await request.post('/api/workspaces', { data: { name } });
    expect(response.ok(), await response.text()).toBeTruthy();
    const all = (await (await request.get('/api/workspaces')).json()).folders;
    return all.find((row: any) => row.name === name);
  };
  const project = await create('Album-1 accessibility fixture');
  const longName =
    'A second project with a deliberately long name that has to wrap in a narrow drawer';
  const second = await create(longName);
  const noteResponse = await request.post(`/api/workspaces/${project.id}/notes`, {
    data: {
      name: 'Release notes',
      content: `Master the single before Friday. ${'Background for a longer note. '.repeat(8)}`
    }
  });
  expect(noteResponse.ok(), await noteResponse.text()).toBeTruthy();
  const ticket = await request.post(`/api/workspaces/${project.id}/tickets`, {
    data: {
      title: 'Master the single',
      description: 'Send the stems to mastering.',
      state: 'backlog'
    }
  });
  expect(ticket.status(), await ticket.text()).toBe(201);
  const linked = await request.post(`/api/workspaces/${project.id}/directories`, {
    data: { name: 'Album-1 assets', path: assets }
  });
  expect(linked.ok(), await linked.text()).toBeTruthy();

  const launcher = page.locator('#personalAssistantLauncher');
  const panel = page.locator('#personalAssistantPanel');
  const label = page.locator('#personalAssistantWorkspaceContext');
  const input = page.locator('#personalAssistantInput');
  const sendButton = page.locator('#personalAssistantSend');
  const answers = page.locator('#homeAssistantConversation [data-message-role="assistant"]');
  const ready = () =>
    page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
  // Where focus is: 'drawer', or a description of the element that has it.
  const focusPlace = () =>
    page.evaluate(() => {
      const active = document.activeElement;
      if (document.getElementById('personalAssistantPanel')?.contains(active)) return 'drawer';
      return `${active?.tagName.toLowerCase() || 'nothing'}#${active?.id || ''}`;
    });
  // Type a question, Tab to Send and press Enter: no pointer.
  const sendFromKeyboard = async (prompt: string) => {
    await input.focus();
    await page.keyboard.press('ControlOrMeta+A');
    await page.keyboard.type(prompt);
    const response = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.keyboard.press('Tab');
    await expect(sendButton).toBeFocused();
    await page.keyboard.press('Enter');
    const body = await (await response).json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    expect(body.conversation.stored).toBe(true);
    return body;
  };
  // Walk backwards from the composer until the control has focus.
  const reachByShiftTab = async (target: ReturnType<typeof page.locator>, what: string) => {
    await input.focus();
    for (let step = 0; step < 60; step++) {
      await page.keyboard.press('Shift+Tab');
      if (await target.evaluate(node => node === document.activeElement)) return step + 1;
    }
    throw new Error(`${what} is not in the keyboard order`);
  };
  const shot = (name: string) => page.screenshot({ path: join(evidence, name) });

  await page.goto(`/workspaces/${project.folder_slug}/canvas`);
  await ready();
  await expect(launcher).toBeVisible();

  // 1. Open, identify and close the drawer from the keyboard; focus comes back.
  await launcher.focus();
  await page.keyboard.press('Enter');
  await expect(panel).toBeVisible();
  await expect(launcher).toHaveAttribute('aria-expanded', 'true');
  await expect(panel).toHaveAttribute('role', 'dialog');
  const titleId = await panel.getAttribute('aria-labelledby');
  expect(titleId).toBeTruthy();
  await expect(page.locator(`#${titleId}`)).not.toBeEmpty();
  await expect(input).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(panel).toBeHidden();
  await expect(launcher).toBeFocused();
  await expect(launcher).toHaveAttribute('aria-expanded', 'false');
  await page.keyboard.press('Enter');
  await expect(panel).toBeVisible();

  // 2. The context line is a polite, whole-line announcement naming the
  // workspace and its kind in words. The composer has an accessible name.
  for (const [name, value] of [
    ['role', 'status'],
    ['aria-live', 'polite'],
    ['aria-atomic', 'true']
  ] as const)
    await expect(label).toHaveAttribute(name, value);
  await expect(label).toHaveText('Context · Project: Album-1 accessibility fixture');
  await expect(
    page.getByRole('textbox', {
      name: 'Ask your personal assistant a question or describe work'
    })
  ).toBeVisible();

  // 3. Ask from the keyboard. Focus stays in the drawer when the reply lands,
  // and the reply says in text which workspace it answered about.
  const advice = await sendFromKeyboard(
    'Based on my release notes and open tasks, what should I do next?'
  );
  expect(advice.workspace_context.sources).toHaveLength(2);
  expect(await focusPlace(), 'focus after a keyboard Send').toBe('drawer');
  await expect(sendButton).toBeFocused();
  const reply = answers.last();
  await expect(reply.locator('.personal-assistant-message__context')).toContainText(
    'Album-1 accessibility fixture'
  );

  // 4. Sources: reachable by Tab order, opened with Enter, closed and reopened
  // with Space. Each link has a name that says what it is; focus is visible.
  const sources = reply.locator('.personal-assistant-message__sources');
  const summary = sources.locator('summary');
  await expect(summary).toHaveText('Sources used (2)');
  const stepsBack = await reachByShiftTab(summary, 'The Sources disclosure');
  const isOpen = () => sources.evaluate(node => (node as HTMLDetailsElement).open);
  await page.keyboard.press('Enter');
  expect(await isOpen()).toBe(true);
  await page.keyboard.press('Space');
  expect(await isOpen()).toBe(false);
  await page.keyboard.press('Space');
  expect(await isOpen()).toBe(true);
  await page.keyboard.press('Tab');
  const noteLink = sources.getByRole('link', { name: 'Note: Release notes', exact: true });
  await expect(noteLink).toBeFocused();
  const focusRing = await noteLink.evaluate(node => {
    const style = getComputedStyle(node);
    return (
      (style.outlineStyle !== 'none' && style.outlineWidth !== '0px') || style.boxShadow !== 'none'
    );
  });
  expect(focusRing, 'a focused source link shows a focus indicator').toBe(true);
  await page.keyboard.press('Tab');
  await expect(sources.getByRole('link', { name: /^Task: .*Master the single$/ })).toBeFocused();
  // Coverage is said in words, not by color or an icon alone.
  await expect(sources).toContainText('read in full');
  await sources.scrollIntoViewIfNeeded();
  await shot('accessibility-keyboard-sources.png');

  // 5. A file has no page in the app, so its source is text, not a dead link.
  const fileReply = await sendFromKeyboard('Summarize the lyrics file in Album-1 assets');
  expect(fileReply.workspace_context.sources).toHaveLength(1);
  const fileSources = answers.last().locator('.personal-assistant-message__sources');
  await reachByShiftTab(fileSources.locator('summary'), 'The file Sources disclosure');
  await page.keyboard.press('Enter');
  await expect(fileSources).toContainText('File: bridge.txt');
  await expect(fileSources.locator('a')).toHaveCount(0);

  // 6. A source that is later deleted leaves a link that still opens the app
  // rather than an error, and the saved answer keeps its source list.
  const noteHref = advice.workspace_context.sources[0].href as string;
  const noteId = advice.workspace_context.sources[0].id as string;
  expect((await request.delete(`/api/notes/${noteId}`)).ok()).toBeTruthy();
  const missing = await request.get(noteHref);
  expect(missing.status(), 'a deleted source link must not be a server error').toBeLessThan(500);

  // 7. Navigation: the drawer stays open, the unsent draft is kept, and the
  // context line changes to the new page (announced by its live region). A page
  // outside any workspace says so rather than keeping the last project.
  await input.fill('An unsent draft about the next step');
  await page.goto(`/workspaces/${second.folder_slug}/canvas`);
  await ready();
  await expect(panel).toBeVisible();
  await expect(label).toHaveText(`Context · Project: ${longName}`);
  await expect(input).toHaveValue('An unsent draft about the next step');
  await page.goto('/settings');
  await ready();
  await expect(panel).toBeVisible();
  await expect(label).toContainText('App-wide');
  await expect(label).not.toContainText(longName);
  await expect(input).toHaveValue('An unsent draft about the next step');

  // 8. Narrow and short screens. The drawer stays inside the screen, nothing in
  // it scrolls sideways, a long workspace name wraps inside it, and the
  // composer stays on screen while the conversation scrolls.
  const layout = async (width: number, height: number) => {
    await page.setViewportSize({ width, height });
    await page.goto(`/workspaces/${second.folder_slug}/canvas`);
    await ready();
    if (!(await panel.isVisible())) await launcher.click();
    await expect(label).toHaveText(`Context · Project: ${longName}`);
    await page.evaluate(
      (id: string) => (window as any).PersonalAssistantConversation.resume(id),
      advice.conversation.id
    );
    await expect(answers.first()).toBeVisible();
    const measured = await page.evaluate(() => {
      const box = (id: string) => document.getElementById(id)!.getBoundingClientRect();
      const drawer = document.getElementById('personalAssistantPanel')!;
      const scroll = document.getElementById('personalAssistantScroll')!;
      scroll.scrollTop = scroll.scrollHeight;
      const panelBox = box('personalAssistantPanel');
      const labelBox = box('personalAssistantWorkspaceContext');
      return {
        left: panelBox.left,
        right: panelBox.right,
        top: panelBox.top,
        bottom: panelBox.bottom,
        drawerSideways: drawer.scrollWidth - drawer.clientWidth,
        conversationSideways: scroll.scrollWidth - scroll.clientWidth,
        conversationScrolls: scroll.scrollHeight > scroll.clientHeight,
        labelInside: labelBox.left >= panelBox.left - 1 && labelBox.right <= panelBox.right + 1,
        labelLines: Math.round(
          labelBox.height /
            parseFloat(
              getComputedStyle(document.getElementById('personalAssistantWorkspaceContext')!)
                .lineHeight
            )
        )
      };
    });
    expect(measured.left, `${width}px: drawer starts on screen`).toBeGreaterThanOrEqual(-0.5);
    expect(measured.right, `${width}px: drawer ends on screen`).toBeLessThanOrEqual(width + 0.5);
    expect(measured.top).toBeGreaterThanOrEqual(-0.5);
    expect(measured.bottom, `${height}px: drawer fits the height`).toBeLessThanOrEqual(
      height + 0.5
    );
    expect(measured.drawerSideways, 'the drawer does not scroll sideways').toBeLessThanOrEqual(1);
    expect(measured.conversationSideways).toBeLessThanOrEqual(1);
    expect(measured.labelInside, 'the context line stays inside the drawer').toBe(true);
    for (const control of [input, sendButton, label]) await expect(control).toBeInViewport();
    return measured;
  };
  const narrow = await layout(360, 740);
  expect(narrow.labelLines, 'a long workspace name wraps rather than being cut').toBeGreaterThan(1);
  expect(narrow.conversationScrolls).toBe(true);
  await shot('accessibility-narrow-360.png');
  const short = await layout(760, 480);
  await shot('accessibility-short-480.png');

  await writeFile(
    join(evidence, 'accessibility-demo.json'),
    JSON.stringify(
      {
        boundary:
          'wt demo + production Ollama adapter + deterministic loopback provider. Roles, names, live regions, focus order and layout were asserted in Chromium; no screen reader was run and no vendor model was called',
        keyboardOnly: {
          openedAndClosedWithFocusReturn: true,
          sentWithTabAndEnter: true,
          focusStayedInDrawerAfterReply: true,
          sourcesReachedByShiftTabSteps: stepsBack,
          sourcesToggledWithEnterAndSpace: true,
          sourceLinksNamedAndFocusVisible: true
        },
        contextLine: {
          role: 'status',
          live: 'polite',
          atomic: true,
          appWideStatedOutsideWorkspace: true
        },
        fileSourceIsTextNotLink: true,
        deletedSourceLinkStatus: missing.status(),
        draftKeptAcrossNavigation: true,
        layouts: { '360x740': narrow, '760x480': short }
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
