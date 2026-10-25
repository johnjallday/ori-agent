import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { stageReviewedFolderPackages } from './reviewed-folder-packages';

// Shared request guard for collection and single-project recovery journeys.
export function watchFolderRequests(page: Page) {
  const seen = {
    errors: [] as string[],
    asks: [] as string[],
    posts: [] as { path: string; body: any }[]
  };
  page.on('pageerror', error => seen.errors.push(error.message));
  page.on('request', request => {
    if (request.method() === 'POST')
      seen.posts.push({ path: new URL(request.url()).pathname, body: request.postDataJSON() });
    if (/\/api\/home-assistant\/ask$/.test(new URL(request.url()).pathname))
      seen.asks.push(request.url());
  });
  return seen;
}

export function expectNoFolderExecution(seen: ReturnType<typeof watchFolderRequests>) {
  expect(
    seen.posts.filter(item =>
      /\/offers\/[^/]+\/(setup|decide)$|\/plugins\/.*(install|enable|update)|\/tasks.*execute/.test(
        item.path
      )
    )
  ).toEqual([]);
  expect(seen.asks).toEqual([]);
  expect(seen.errors).toEqual([]);
}

// Configuration/consent evidence only. The provider never contacts a paid or
// signed-in model, and its response is not evidence of musical analysis.
export async function collectionModel(port: number) {
  expect(Number.isSafeInteger(port) && port > 0 && port < 65536).toBeTruthy();
  const calls: { model: string; daily: boolean }[] = [];
  const server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      res.end(JSON.stringify({ data: [{ id: 'collection-fixture', object: 'model' }] }));
      return;
    }
    let raw = '';
    for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw || '{}');
    const daily = body.messages?.some((message: { content?: string }) =>
      String(message.content).startsWith('You write a concise daily brief')
    );
    calls.push({ model: body.model, daily: Boolean(daily) });
    const content = daily
      ? JSON.stringify({ opening_summary: 'Unrelated controlled Daily Brief fixture.' })
      : 'Controlled collection fixture only; no project analysis was performed.';
    res.end(
      JSON.stringify({
        id: 'collection-fixture',
        object: 'chat.completion',
        model: body.model,
        choices: [{ index: 0, message: { role: 'assistant', content }, finish_reason: 'stop' }],
        usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 }
      })
    );
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });
  return {
    calls,
    close: () => new Promise<void>(resolve => server.close(() => resolve()))
  };
}

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

// Existing published packages, installed only in the wrapper's disposable
// HOME. Keep the actual install registry and canonical Home receipt as evidence.
export async function existingCollectionHome(request: APIRequestContext, sandbox: string) {
  await stageReviewedFolderPackages(request);
  const installed = await json(await request.get('/api/plugins'));
  expect(installed.plugins).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ name: 'music-project-management', version: '0.4.0' }),
      expect.objectContaining({ name: 'reaper-plugin', version: '0.11.0' })
    ])
  );
  const templates = (await json(await request.get('/api/workspaces/group-templates')))
    .group_templates;
  const template = templates.find(
    (row: any) =>
      row.kind === 'managed_home' && row.provider?.plugin_id === 'music-project-management'
  );
  expect(template).toBeTruthy();
  const selection = {
    group_template_id: template.id,
    revision: template.revision,
    name: 'Music Home collection fixture'
  };
  const review = await json(
    await request.post('/api/workspaces/group-templates/review', { data: selection })
  );
  const commit = await json(
    await request.post('/api/workspaces/group-templates/commit', {
      data: {
        ...selection,
        group_review_token: review.group_template_review.review_token,
        idempotency_key: 'collection-existing-home'
      }
    })
  );
  const id = commit.group_template.home_workspace_id;
  const state = await json(await request.get(`/api/workspaces/${id}/assistant-program`));
  expect(state).toMatchObject({ is_station: true, home_provider_available: true });
  return {
    id,
    review,
    commit,
    state,
    registry: JSON.parse(readFileSync(join(sandbox, 'plugins', 'installed.json'), 'utf8'))
  };
}
