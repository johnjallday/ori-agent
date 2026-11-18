import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { randomUUID, createHash } from 'node:crypto';
import {
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  unlinkSync,
  writeFileSync
} from 'node:fs';
import { join, resolve } from 'node:path';
import { assertSandbox, guardReadTour } from './ui-consistency';

export type OwnedAgent = {
  id: string;
  name: string;
  marker: string;
  root: string;
  file: string;
  manifest: string;
  expected: any;
  hash: string;
};
const projection = (agent: any) =>
  Object.fromEntries(
    [
      'name',
      'role',
      'model',
      'provider',
      'temperature',
      'max_output_tokens',
      'system_prompt',
      'metadata',
      'appearance',
      'version'
    ].map(key => [key, agent[key]])
  );
const hash = (file: string) => createHash('sha256').update(readFileSync(file)).digest('hex');

function verifyFiles(owned: OwnedAgent) {
  expect(owned.id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  expect(owned.name).toBe(`UI-detail-${owned.id}`);
  expect(owned.manifest).toBe(resolve('.tmp/ui-consistency/owned-agents', `${owned.id}.json`));
  expect(lstatSync(owned.manifest).isSymbolicLink()).toBe(false);
  const folder = join(owned.root, 'Agents', owned.name);
  expect(lstatSync(folder).isSymbolicLink()).toBe(false);
  expect(realpathSync(folder)).toBe(folder);
  expect(owned.file).toBe(join(folder, 'agent_settings.json'));
  expect(lstatSync(owned.file).isSymbolicLink()).toBe(false);
  expect(readFileSync(owned.file, 'utf8')).toContain(owned.marker);
}

export async function ownedAgentDetail(request: APIRequestContext, owned: OwnedAgent) {
  const response = await request.get(`/api/agents/${owned.name}/detail`);
  expect(response.ok(), 'owned definition must remain readable').toBe(true);
  const agent = await response.json();
  expect(agent.name).toBe(owned.name);
  expect(agent.metadata.description).toContain(owned.marker);
  expect(agent.system_prompt).toContain(owned.marker);
  expect(agent.workspace_count || 0).toBe(0);
  return agent;
}

/** Adopt only after this test has verified its own completed mutation. */
export async function recordOwnedAgent(request: APIRequestContext, owned: OwnedAgent) {
  const settings = await (await request.get('/api/settings/workspace-root')).json();
  expect(realpathSync(settings.effective_workspace_root)).toBe(owned.root);
  verifyFiles(owned);
  owned.expected = projection(await ownedAgentDetail(request, owned));
  owned.hash = hash(owned.file);
  writeFileSync(owned.manifest, JSON.stringify(owned, null, 2), { mode: 0o600 });
}

export async function createOwnedAgent(request: APIRequestContext): Promise<OwnedAgent> {
  await assertSandbox(request);
  const settings = await (await request.get('/api/settings/workspace-root')).json();
  const root = realpathSync(settings.effective_workspace_root);
  const id = randomUUID(),
    name = `UI-detail-${id}`,
    marker = `Owned UI definition ${id}`;
  const dir = resolve('.tmp/ui-consistency/owned-agents');
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  expect(realpathSync(dir)).toBe(dir);
  const owned: OwnedAgent = {
    id,
    name,
    marker,
    root,
    file: join(root, 'Agents', name, 'agent_settings.json'),
    manifest: join(dir, `${id}.json`),
    expected: null,
    hash: ''
  };
  expect(existsSync(join(root, 'Agents', name))).toBe(false);
  expect((await request.get(`/api/agents/${name}/detail`)).status()).toBe(404);
  // A definition only: no client invocation, account, provider key, workspace
  // assignment, hire relationship, schedule, session or execution is created.
  writeFileSync(owned.manifest, JSON.stringify(owned, null, 2), { mode: 0o600 });
  const response = await request.post('/api/agents', {
    data: {
      name,
      role: 'researcher',
      llm_provider: 'claude',
      model: 'claude-sonnet-4-5-20250929',
      temperature: 0.6,
      description: marker,
      system_prompt: `${marker}: original prompt`,
      tags: ['ui-consistency', id],
      allow_web_search: false,
      appearance: { mode: 'character', character: { catalog_id: 'research-archivist' } }
    }
  });
  expect(response.ok(), await response.text()).toBe(true);
  await recordOwnedAgent(request, owned);
  return owned;
}

export async function guardOwnedAgentEdits(
  page: Page,
  owned: OwnedAgent,
  options: { appearanceOnly?: boolean } = {}
) {
  const unexpected = await guardReadTour(page);
  await page.route('**/api/**', route => {
    const request = route.request();
    const url = new URL(request.url());
    if (
      request.method() === 'GET' &&
      url.pathname.startsWith('/api/mcp/') &&
      (url.search || url.pathname.endsWith('/details'))
    ) {
      unexpected.push(`Unreviewed MCP read: ${url.pathname}`);
      return route.abort('blockedbyclient');
    }
    const ownedPath =
      url.pathname === `/api/agents/${owned.name}` ||
      (options.appearanceOnly &&
        url.pathname === '/api/agents' &&
        url.searchParams.get('name') === owned.name &&
        [...url.searchParams.keys()].every(key => key === 'name'));
    if (request.method() !== 'PATCH' || !ownedPath) return route.fallback();
    const body = request.postDataJSON();
    if (options.appearanceOnly) {
      if (
        body &&
        body.appearance &&
        body.expected_version !== undefined &&
        Object.keys(body).every(key => ['appearance', 'expected_version'].includes(key))
      ) {
        return route.continue(); // The real server enforces its opaque version.
      }
      unexpected.push('Rejected non-appearance PATCH during appearance-only journey');
      return route.abort('blockedbyclient');
    }
    const allowed = new Set([
      'name',
      'description',
      'tags',
      'favorite',
      'system_prompt',
      'expected_version'
    ]);
    if (
      !body ||
      Object.keys(body).some(key => !allowed.has(key)) ||
      body.expected_version !== owned.expected.version ||
      (body.name && body.name !== owned.name) ||
      (body.description && !body.description.includes(owned.marker)) ||
      (body.system_prompt && !body.system_prompt.includes(owned.marker))
    ) {
      unexpected.push('Rejected non-owned or out-of-scope definition PATCH');
      return route.abort('blockedbyclient');
    }
    return route.continue(); // Real server response, never a fake save.
  });
  return unexpected;
}

export async function removeOwnedAgent(request: APIRequestContext, owned: OwnedAgent) {
  await assertSandbox(request);
  const settings = await (await request.get('/api/settings/workspace-root')).json();
  expect(realpathSync(settings.effective_workspace_root)).toBe(owned.root);
  verifyFiles(owned);
  const manifest = JSON.parse(readFileSync(owned.manifest, 'utf8'));
  expect(manifest).toEqual(owned);
  verifyFiles(owned);
  expect(
    hash(owned.file),
    'unrecognized definition drift must be preserved for investigation'
  ).toBe(owned.hash);
  expect(projection(await ownedAgentDetail(request, owned))).toEqual(owned.expected);
  // Canonical deletion includes the agent's session purge; no direct fs removal.
  expect((await request.delete(`/api/agents?name=${owned.name}`)).status()).toBe(200);
  expect((await request.get(`/api/agents/${owned.name}/detail`)).status()).toBe(404);
  expect(existsSync(join(owned.root, 'Agents', owned.name))).toBe(false);
  unlinkSync(owned.manifest);
}
