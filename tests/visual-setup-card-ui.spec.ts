import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';
import { mockHiredAssistant } from './helpers/hired-assistant';

// Presentation fixtures in the real assistant drawer. They do not claim to
// install plugins or hire agents; visual-setup-card.spec.ts proves that path.
const step = 'music-production-home';
const hires = [
  {
    step,
    name: 'Portfolio Manager',
    character: 'automation-specialist',
    model: 'claude-sonnet-4-6 (claude)',
    job: 'Coordinates portfolio status, priorities, milestones, blockers, deliverables, and explicit handoffs without inheriting project access.'
  },
  {
    step,
    name: 'REAPER Assistant',
    character: 'decision-strategist',
    model: 'claude-sonnet-4-6 (claude)',
    job: 'Handles everything REAPER-related for this project: session setup, tracks, routing, recording, editing, mixing, and export.',
    note: 'Joins each song you open.'
  }
];
const lines = [
  { step, kind: 'provider', name: 'Installs Music Project Management' },
  { step, kind: 'home_tools', name: 'Installs REAPER tools' },
  {
    step,
    kind: 'home',
    name: 'Creates your Music Production Home',
    outcome: 'Creates Music Production Home'
  },
  {
    step,
    kind: 'hire',
    agent_name: 'Portfolio Manager',
    name: 'Hires Portfolio Manager',
    outcome: 'Hires 2 agents'
  },
  {
    step,
    kind: 'assistant',
    agent_name: 'REAPER Assistant',
    name: 'Hires REAPER Assistant',
    outcome: 'Hires 2 agents'
  },
  { step, kind: 'songs', name: 'Lists 6 songs', outcome: 'Lists 6 songs' },
  {
    kind: 'folder',
    name: 'Nothing in Albums is moved, copied or read. Ori adds 2 plugins, Music Production Home, and 2 agents.'
  }
];
const plan = {
  digest: 'presentation-only',
  runnable: true,
  source: 'default',
  selected: [step],
  steps: [
    {
      handle: step,
      kind: 'home',
      name: 'Music Production Home',
      title: 'Create your Music Production Home and put Albums on its shelf (6 songs)',
      chosen: true
    }
  ],
  hires,
  lines,
  assurance: 'Nothing in Albums is moved, copied or read.',
  alternatives: [
    {
      step: 'group',
      title: 'Turn Albums into a group with one workspace per song',
      tradeoff: 'No shared Home library.'
    }
  ],
  installs: [
    {
      step,
      line: 'provider',
      name: 'Music Project Management',
      version: '0.5.0',
      publisher: 'Ori',
      source_label: 'johnjallday/music-project-management',
      source_url: 'https://github.com/johnjallday/music-project-management',
      why: 'Provides your Music Production Home.'
    },
    {
      step,
      line: 'home_tools',
      name: 'REAPER tools',
      version: '0.12.0',
      publisher: 'Ori',
      source_label: 'johnjallday/reaper-plugin',
      source_url: 'https://github.com/johnjallday/reaper-plugin',
      why: 'Lets the agent work with the REAPER songs here.',
      app_sentence: 'REAPER is on this Mac; its Ori tools are not yet.'
    }
  ]
};
const report = {
  workspace_id: 'visual-fixture',
  workspace_name: 'Albums',
  folder_name: 'Albums',
  agent_name: 'Albums Manager',
  state: 'reported',
  actions: ['set_up', 'keep'],
  plan
};
const evidence = 'tasks/evidence-visual-setup-card';

async function mount(page: Page, raw = report) {
  await installLocalCdn(page);
  await mockHiredAssistant(page);
  await page.goto('/?panel=today');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(async raw => {
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    );
    const modulePath = '/js/modules/folder-agent-card.js';
    const module = await import(modulePath);
    const card = module.renderFolderAgentReport(module.folderAgentReportView(raw), document, {
      compact: true
    });
    document.getElementById('personalAssistantScroll')!.replaceChildren(card);
  }, raw);
  const card = page.locator('.folder-agent-card--report:visible');
  await expect(card).toBeVisible();
  if (raw.state === 'reported')
    await expect(card.locator('.folder-agent-card__hire-face [data-aa-character]')).toHaveCount(2);
  return card;
}

for (const phase of ['running', 'done']) {
  test(`faces through the ${phase === 'running' ? 'hire lines' : 'receipt'}`, async ({
    page,
    request
  }) => {
    await request.post('/api/onboarding/skip');
    mkdirSync(`${evidence}/group-4`, { recursive: true, mode: 0o750 });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.route('**/api/agents/dashboard/list', route =>
      route.fulfill({
        json: hires.map(hire => ({
          name: hire.name,
          role: 'orchestrator',
          appearance: {
            mode: 'character',
            character: {
              catalog_id:
                hire.name === 'Portfolio Manager' ? 'decision-strategist' : 'automation-specialist'
            }
          }
        }))
      })
    );
    const running = phase === 'running';
    const liveLines = lines.map(line => ({
      ...line,
      state: running && line.kind === 'hire' ? 'working' : 'done',
      progress:
        line.kind === 'hire'
          ? `${running ? 'Hiring' : 'Hired'} Portfolio Manager`
          : line.kind === 'assistant'
            ? 'Already in your Home'
            : ''
    }));
    const card = await mount(page, {
      ...report,
      state: running ? 'setting_up' : 'set_up',
      actions: [],
      plan: { ...plan, hires: running ? hires : [] },
      setup: {
        status: phase,
        lines: liveLines,
        receipt: running ? undefined : { hired: hires.map(hire => hire.name) }
      }
    } as typeof report);
    if (running) {
      const action = card
        .locator('.folder-agent-card__action')
        .filter({ hasText: 'Hiring Portfolio Manager' });
      await expect(action.locator('[data-aa-character]')).toHaveAttribute(
        'data-aa-character',
        'decision-strategist'
      );
      const existing = card
        .locator('.folder-agent-card__action')
        .filter({ hasText: 'Already in your Home' });
      await expect(existing.locator('[data-aa-character]')).toHaveAttribute(
        'data-aa-character',
        'automation-specialist'
      );
      await action.scrollIntoViewIfNeeded();
    } else {
      const receipt = card.locator('.folder-agent-card__receipt-row[data-kind="hired"]');
      await expect(receipt.locator('.folder-agent-card__receipt-agent')).toHaveCount(2);
      await expect(receipt.locator('[data-aa-character]').first()).toHaveAttribute(
        'data-aa-character',
        'decision-strategist'
      );
      await expect(receipt.locator('[data-aa-character]').last()).toHaveAttribute(
        'data-aa-character',
        'automation-specialist'
      );
      await receipt.scrollIntoViewIfNeeded();
    }
    await page.screenshot({ path: `${evidence}/group-4/${phase}.png` });
  });
}

function contrast(foreground: string, background: string) {
  const luminance = (color: string) => {
    const components = (color.match(/[\d.]+/g) || [])
      .slice(0, 3)
      .map(Number)
      .map(value => {
        const c = value / 255;
        return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
      });
    return components[0] * 0.2126 + components[1] * 0.7152 + components[2] * 0.0722;
  };
  const a = luminance(foreground),
    b = luminance(background);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

for (const theme of ['light', 'dark']) {
  for (const width of [640, 560, 760]) {
    test(`team-first setup card: ${theme}, ${width}px ${width === 760 ? 'viewport' : 'drawer'}`, async ({
      page,
      request
    }) => {
      await request.post('/api/onboarding/skip');
      mkdirSync(`${evidence}/group-3`, { recursive: true, mode: 0o750 });
      await page.setViewportSize({ width: width === 760 ? 760 : 1440, height: 800 });
      await page.addInitScript(
        ({ theme, width }) => {
          localStorage.setItem('ori-theme', theme);
          localStorage.setItem('ori.assistant.drawer.width', String(width));
        },
        { theme, width }
      );
      const card = await mount(page);
      const drawerWidth = await page
        .locator('#personalAssistantPanel')
        .evaluate(node => node.getBoundingClientRect().width);
      expect(Math.abs(drawerWidth - width)).toBeLessThanOrEqual(1);
      const order = await card
        .locator('.folder-agent-card__run')
        .evaluate(node => Array.from(node.children).map(child => child.className));
      const index = (name: string) =>
        order.findIndex(value => value.split(' ').includes(`folder-agent-card__${name}`));
      expect(index('plan')).toBeLessThan(index('alternatives'));
      expect(index('alternatives')).toBeLessThan(index('hires'));
      expect(index('hires')).toBeLessThan(index('installs'));
      expect(index('installs')).toBeLessThan(index('line'));
      await expect(card.locator('.folder-agent-card__hire-model')).toHaveCount(1);
      await expect(card.locator('.folder-agent-card__hire-job').first()).toHaveText(hires[0].job);
      await expect(card.locator('.folder-agent-card__outcomes')).not.toContainText(
        'Hires 2 agents'
      );
      await expect(card.locator('.folder-agent-card__line--note')).toHaveText(plan.assurance);
      const primary = card.getByRole('button', { name: 'Install plugins & set up', exact: true });
      const colors = await primary.evaluate(node => ({
        color: getComputedStyle(node).color,
        background: getComputedStyle(node).backgroundColor
      }));
      expect(contrast(colors.color, colors.background)).toBeGreaterThanOrEqual(4.5);
      expect(colors.background).toBe(
        await page
          .locator('#personalAssistantSend')
          .evaluate(node => getComputedStyle(node).backgroundColor)
      );
      expect(await card.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
      for (const selector of [
        '.folder-agent-card__plan',
        '.folder-agent-card__hires',
        '.folder-agent-card__installs',
        '.folder-agent-card__actions'
      ]) {
        await card.locator(selector).last().scrollIntoViewIfNeeded();
        await page.screenshot({
          path: `${evidence}/group-3/${theme}-${width}-${selector.split('__')[1]}.png`
        });
      }
    });
  }
}
