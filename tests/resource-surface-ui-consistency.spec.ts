import { test, expect } from '@playwright/test';
import { readFileSync, realpathSync, lstatSync } from 'node:fs';
import { join, dirname, relative } from 'node:path';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { preparePage, capture, contrast } from './helpers/ui-consistency';
import {
  resourceFileInventory,
  resourcePreservationSnapshot,
  journalResourceRead
} from './helpers/ui-consistency-resource';
import { boundedCanvasControl as bounded } from './helpers/ui-consistency-canvas';

const key = 'plugin:workspace-surface-demo:demo-tools:main';
let owned: any,
  beforeApi: Awaited<ReturnType<typeof api>>,
  beforeFiles: ReturnType<typeof resourcePreservationSnapshot>,
  source: Record<string, string>;
let violations: string[],
  sessions: Set<string>,
  revoked: Set<string>,
  states: Map<string, { count: number; revision: string }>;
let stateWrites: number,
  rechecks: number,
  definitionPath: string,
  beforeDefinition: any,
  beforeIndex: any,
  ready = false;
function verifyMetadata(current: any, previous: any) {
  const { version, updated_at, ...content } = current;
  const { version: oldVersion, updated_at: oldTime, ...oldContent } = previous;
  expect(content, 'the native recheck may not change any authority/content field').toEqual(
    oldContent
  );
  expect(Number.isInteger(version)).toBe(true);
  expect(version).toBeGreaterThanOrEqual(oldVersion);
  expect(version).toBeLessThanOrEqual(oldVersion + rechecks);
  expect(Number.isFinite(Date.parse(updated_at))).toBe(true);
  expect(Date.parse(updated_at)).toBeGreaterThanOrEqual(Date.parse(oldTime));
}
function readIndex() {
  return JSON.parse(
    execFileSync(
      'python3',
      [
        '-c',
        `
import sqlite3,json,sys,re,datetime
c=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro',uri=True)
c.execute('BEGIN')
schema=list(c.execute("SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name"))
rows=[]
for r in c.execute('SELECT id,name,folder_path,parent_id,updated_at FROM workspaces ORDER BY id'):
 t=re.sub(r' [A-Z]+$','',r[4]); base,offset=t.rsplit(' ',1)
 head,fraction=base.split('.',1) if '.' in base else (base,'0')
 stamp=head.replace(' ','T')+'.'+fraction[:6].ljust(6,'0')+offset[:3]+':'+offset[3:]
 rows.append(list(r[:4])+[datetime.datetime.fromisoformat(stamp).isoformat()])
print(json.dumps({'schema':schema,'rows':rows}))
`,
        join(owned.sidecars, 'index.db')
      ],
      { encoding: 'utf8', timeout: 5000 }
    )
  );
}
function verifiedFiles() {
  const files = resourcePreservationSnapshot(owned.sidecars);
  const definition = JSON.parse(readFileSync(definitionPath, 'utf8'));
  verifyMetadata(definition, beforeDefinition);
  const rel = relative(owned.sidecars, definitionPath);
  expect(files.raw[rel]).toBe(
    createHash('sha256').update(readFileSync(definitionPath)).digest('hex')
  );
  const index = readIndex();
  expect(index.schema).toEqual(beforeIndex.schema);
  expect(index.rows.map((row: any[]) => row.slice(0, 4))).toEqual(
    beforeIndex.rows.map((row: any[]) => row.slice(0, 4))
  );
  expect(index.rows).toHaveLength(1);
  expect(index.rows[0][0]).toBe(owned.workspace.id);
  expect(Date.parse(index.rows[0][4])).toBe(Date.parse(definition.updated_at));
  const comparable = { ...files.comparable, [rel]: beforeFiles.comparable[rel] };
  for (const name of ['index.db', 'index.db-wal', 'index.db-shm']) {
    expect(files.raw[name]).toMatch(/^[0-9a-f]{64}$/);
    comparable[name] = beforeFiles.comparable[name];
  }
  return { ...files, comparable, definition, index };
}
async function api(request: any) {
  const result: Record<string, any> = {};
  for (const path of [
    `/api/workspaces/${owned.workspace.id}`,
    `/api/workspaces/${owned.workspace.id}/surfaces`,
    '/api/agents'
  ]) {
    const response = await request.get(path);
    expect(response.ok()).toBe(true);
    const value = await response.json();
    if (path === '/api/agents') value.agents.sort((a: any, b: any) => a.name.localeCompare(b.name));
    result[path] = value;
  }
  const comparable = structuredClone(result);
  for (const surface of comparable[`/api/workspaces/${owned.workspace.id}/surfaces`].surfaces) {
    if (surface.status?.checked_at) {
      expect(Number.isFinite(Date.parse(surface.status.checked_at))).toBe(true);
      surface.status.checked_at = '[live status sample]';
    }
  }
  return { raw: result, comparable };
}
test.beforeEach(async ({ request }, info) => {
  ready = false;
  violations = [];
  sessions = new Set();
  revoked = new Set();
  states = new Map();
  stateWrites = 0;
  rechecks = 0;
  owned = JSON.parse(
    readFileSync('.tmp/ui-consistency/group-5-workspace-surface-retained.json', 'utf8')
  );
  expect(owned.stage).toBe('host-bridge-trust-journey-passed');
  expect(process.env.ORI_SURFACE_SANDBOX).toBe(owned.sandbox);
  expect(realpathSync(owned.sandbox)).toBe(owned.sandbox);
  expect(lstatSync(owned.sandbox).isSymbolicLink()).toBe(false);
  expect(relative(realpathSync('.tmp/ui-consistency'), owned.sandbox).startsWith('..')).toBe(false);
  expect(readFileSync(join(owned.sandbox, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync('.')}`
  );
  source = resourceFileInventory(owned.source);
  const prefix = relative(owned.sandbox, owned.source) + '/';
  const expected = Object.fromEntries(
    Object.entries(owned.files)
      .filter(([path]) => path.startsWith(prefix))
      .map(([path, hash]) => [path.slice(prefix.length), hash])
  );
  expect(source).toEqual(expected);
  const workspace = await (await request.get(`/api/workspaces/${owned.workspace.id}`)).json();
  expect(relative(owned.sandbox, realpathSync(workspace.sidecar_path)).startsWith('..')).toBe(
    false
  );
  owned.sidecars = dirname(workspace.sidecar_path);
  definitionPath = join(workspace.sidecar_path, 'workspace.json');
  beforeDefinition = JSON.parse(readFileSync(definitionPath, 'utf8'));
  beforeApi = await api(request);
  beforeFiles = resourcePreservationSnapshot(owned.sidecars);
  beforeIndex = readIndex();
  expect(beforeIndex.rows).toHaveLength(1);
  expect(beforeIndex.rows[0].slice(0, 4)).toEqual([
    owned.workspace.id,
    beforeDefinition.name,
    relative(owned.sidecars, dirname(definitionPath)),
    null
  ]);
  expect(Date.parse(beforeIndex.rows[0][4])).toBe(Date.parse(beforeDefinition.updated_at));
  for (const name of ['index.db', 'index.db-wal', 'index.db-shm']) {
    expect(beforeFiles.raw[name]).toMatch(/^[0-9a-f]{64}$/);
    beforeFiles.comparable[name] =
      '[verified complete index schema/rows, owned metadata timestamp only]';
  }
  journalResourceRead(info, 'before', {
    root: owned.sandbox,
    api: beforeApi,
    files: beforeFiles,
    definition: beforeDefinition,
    index: beforeIndex,
    source
  });
  ready = true;
});
test.afterEach(async ({ page, request }, info) => {
  await page.goto('about:blank');
  if (!ready) return;
  const afterApi = await api(request),
    files = verifiedFiles();
  const path = `/api/workspaces/${owned.workspace.id}`;
  verifyMetadata(afterApi.raw[path], beforeApi.raw[path]);
  afterApi.comparable[path] = {
    ...afterApi.comparable[path],
    version: beforeApi.raw[path].version,
    updated_at: beforeApi.raw[path].updated_at
  };
  journalResourceRead(info, 'after', {
    root: owned.sandbox,
    api: afterApi,
    files,
    source: resourceFileInventory(owned.source),
    sessions: sessions.size,
    revoked: revoked.size,
    stateWrites
  });
  expect(violations).toEqual([]);
  expect(afterApi.comparable).toEqual(beforeApi.comparable);
  expect(files.comparable).toEqual(beforeFiles.comparable);
  expect(resourceFileInventory(owned.source)).toEqual(source);
  expect(sessions.size).toBe(1);
  expect(revoked.size).toBe(1);
  expect(stateWrites).toBe(1);
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440])
    test(`real sandboxed surface host bounds focus and isolated frame ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      await page.route('**/api/**', async route => {
        const request = route.request(),
          path = new URL(request.url()).pathname,
          method = request.method();
        if (['GET', 'HEAD'].includes(method)) return route.fallback();
        const body = request.postData() ? request.postDataJSON() : null;
        if (method === 'POST' && path === '/api/orchestration/workspace/activate') {
          expect(new URL(request.url()).searchParams.get('id')).toBe(owned.workspace.id);
          expect(body).toBeNull();
          return route.fallback();
        }
        if (
          method === 'POST' &&
          [
            `/api/workspaces/${owned.workspace.id}/runtime-capabilities/recheck`,
            `/api/workspaces/${owned.workspace.id}/lead-conversation/open`
          ].includes(path)
        ) {
          expect(body === null || Object.keys(body).length === 0).toBe(true);
          const response = await route.fetch();
          expect(response.ok()).toBe(true);
          if (path.endsWith('/runtime-capabilities/recheck')) rechecks++;
          if (path.endsWith('/lead-conversation/open')) {
            const value = await response.json();
            expect(
              value.changed,
              'existing fixture welcome must remain an idempotent stored read'
            ).toBe(false);
          }
          await route.fulfill({ response });
          return;
        }
        if (
          method === 'POST' &&
          path ===
            `/api/workspaces/${owned.workspace.id}/surfaces/${encodeURIComponent(key)}/sessions`
        ) {
          expect(body).toBeNull();
          const response = await route.fetch();
          expect(response.ok()).toBe(true);
          const value = await response.json();
          sessions.add(value.session);
          return route.fulfill({ response });
        }
        if (method === 'POST' && path === '/api/workspace-surfaces/operations') {
          expect(sessions.has(body.session)).toBe(true);
          expect(body.operation_id).toBe('status.read');
          expect(body.input).toEqual({});
          expect(Object.keys(body).sort()).toEqual(['input', 'operation_id', 'session']);
          return route.fallback();
        }
        if (method === 'POST' && path === '/api/workspace-surfaces/state') {
          expect(sessions.has(body.session)).toBe(true);
          expect(body.key).toBe('opens');
          if (body.action === 'get') {
            expect(Object.keys(body).sort()).toEqual(['action', 'key', 'session']);
            const response = await route.fetch();
            expect(response.ok()).toBe(true);
            const value = await response.json();
            states.set(body.session, {
              count: value.found ? Number(value.value.count) : 0,
              revision: value.revision || '0'
            });
            return route.fulfill({ response });
          }
          expect(body.action).toBe('set');
          expect(Object.keys(body).sort()).toEqual([
            'action',
            'expected_revision',
            'key',
            'schema_version',
            'session',
            'value'
          ]);
          const prior = states.get(body.session)!;
          expect(prior).toBeTruthy();
          expect(body.schema_version).toBe(1);
          expect(body.expected_revision).toBe(prior.revision);
          expect(body.value).toEqual({ count: prior.count + 1 });
          const response = await route.fetch();
          expect(response.ok()).toBe(true);
          stateWrites++;
          return route.fulfill({ response });
        }
        if (method === 'DELETE' && path === '/api/workspace-surfaces/sessions') {
          expect(Object.keys(body)).toEqual(['session']);
          expect(sessions.has(body.session)).toBe(true);
          // This is revocation of our exact newly-created ephemeral session,
          // never fixture/data cleanup. Check all protected bytes beforehand.
          expect(resourceFileInventory(owned.source)).toEqual(source);
          expect(verifiedFiles().comparable).toEqual(beforeFiles.comparable);
          const response = await route.fetch();
          expect(response.ok()).toBe(true);
          revoked.add(body.session);
          return route.fulfill({ response });
        }
        violations.push(`${method} ${path}`);
        return route.abort('blockedbyclient');
      });
      await page.goto(`/workspaces/${owned.workspace.slug}?mode=details`);
      await page.waitForFunction(
        () => (window as any).workspaceDetail?.initialListsLoaded === true
      );
      const door = page.locator(`.ws-cmd-capability-access [data-cmd-hq-station="${key}"]`);
      await bounded(door);
      await page
        .locator('.ws-cmd-title h2')
        .first()
        .evaluate(node => {
          (node as HTMLElement).tabIndex = -1;
          (node as HTMLElement).focus();
        });
      await door.click();
      const modal = page.locator('.workspace-surface-modal'),
        frame = modal.locator('iframe');
      await expect(modal).toBeVisible();
      const child = page.frameLocator('.workspace-surface-modal iframe');
      await expect(child.getByText('Bridge ready')).toBeVisible();
      expect(
        await child.locator('html').evaluate(node => getComputedStyle(node).fontFamily)
      ).toContain('Avenir');
      await expect.poll(() => stateWrites).toBe(1);
      await expect(frame).toHaveAttribute('sandbox', 'allow-scripts');
      await expect(frame).toHaveAttribute('credentialless', '');
      await expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer');
      const close = modal.locator('.workspace-surface-close');
      await bounded(close);
      expect(
        (await contrast(modal.locator('.workspace-surface-eyebrow'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      expect((await contrast(close)).ratio).toBeGreaterThanOrEqual(4.5);
      const box = (await modal.locator('.workspace-surface-panel').boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.y).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(width + 1);
      expect(box.y + box.height).toBeLessThanOrEqual(901);
      await capture(
        page,
        info,
        'workspace-surface-host',
        {
          workspaceId: owned.workspace.id,
          fixture:
            'Actual repository example opaque sandboxed iframe and fixed status service; one narrowly checked opens UI-state increment and exact session revocation. Host styling only, not iframe/AT/native or live provider certification.'
        },
        { fullPage: false }
      );
      await close.press('Enter');
      await expect(modal).toHaveCount(0);
      await expect(door).toBeFocused();
      await expect.poll(() => revoked.size).toBe(1);
    });
