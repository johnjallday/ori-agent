import { expect, type APIRequestContext } from '@playwright/test';
import { resolve, sep } from 'node:path';
import { verifyLearningSandbox } from '../../scripts/learning-missions-demo.mjs';

export async function learningPost(
  request: APIRequestContext,
  base: string,
  path: string,
  data: object,
  expected = 200
) {
  const response = await request.post(base + path, { data, headers: { Origin: base } });
  expect(response.status(), `${path}: ${await response.text()}`).toBe(expected);
  return response.json();
}

export async function learningStatus(request: APIRequestContext, base: string) {
  const response = await request.get(base + '/api/learning-missions');
  expect(response.status()).toBe(200);
  expect(response.headers()['cache-control']).toBe('no-store');
  return response.json();
}

export async function seedLearningWorkspace(request: APIRequestContext, base: string) {
  const sandbox = verifyLearningSandbox(base, process.env.ORI_LEARNING_DEMO_SANDBOX, process.cwd());
  // Verify the running server, not just an environment variable, belongs to
  // the disposable HOME before making even an onboarding/preferences write.
  const root = await (await request.get(base + '/api/settings/workspace-root')).json();
  expect(resolve(root.default_workspace_root).startsWith(sandbox + sep)).toBe(true);
  const initial = await learningStatus(request, base);
  expect(initial.owner.revision).toBe(0);
  expect(initial.instances).toEqual([]);
  await learningPost(request, base, '/api/onboarding/complete', {});
  await learningPost(request, base, '/api/settings/workspace-root', { workspace_root: '' });
  await learningPost(request, base, '/api/agents', {
    name: 'Learning writer',
    llm_provider: 'openai',
    model: 'gpt-4o-mini'
  });
  const workspace = await learningPost(request, base, '/api/orchestration/workspace', {
    name: 'Learning workshop',
    participating_agents: ['Learning writer']
  });
  return workspace.workspace_id as string;
}
