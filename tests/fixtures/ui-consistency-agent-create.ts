import type { Page } from '@playwright/test';
import { guardReadTour, readFixture } from '../helpers/ui-consistency';
import { collectionAgents, collectionReads } from './ui-consistency-agents';

export async function guardAgentCreationReads(page: Page) {
  const unexpected = await guardReadTour(page);
  await page.route('**/api/mcp/**', route => {
    const url = new URL(route.request().url());
    if (route.request().method() === 'GET' && (url.search || url.pathname.endsWith('/details'))) {
      unexpected.push(`GET ${url.pathname}${url.search}`);
      return route.abort('blockedbyclient');
    }
    return route.fallback();
  });
  return unexpected;
}

/** Presentation-only reads. Hire/start, auto-configuration, uploads, provider
 * connection and agent writes are never replaced by successful mutations. */
export async function agentCreateReads(page: Page, options: { unhired?: boolean } = {}) {
  await collectionReads(page, options);
  await readFixture(page, /\/api\/agents$/, { agents: collectionAgents });
  await readFixture(page, /\/api\/plugins$/, { plugins: [] });
  await readFixture(page, '**/api/mcp/servers', { servers: [] });
  await readFixture(page, '**/api/agents/auto-config/availability', {
    available: false,
    system_model_configured: false
  });
  await readFixture(page, '**/api/settings/system-model', { configured: false });
  await readFixture(page, '**/api/settings/available-models?provider=layout-fixture', {
    available: true,
    models: ['fictional-layout-model-with-a-very-long-readable-name']
  });
}
