import { createHash } from 'node:crypto';
import type { Page, Route } from '@playwright/test';

/**
 * UI-route fixture ONLY. This adapts an existing virtual legacy create handler
 * to the reviewed transport; it proves no domain creation, plugin compatibility
 * or server idempotency. Real acceptance must not import this helper.
 */
export async function routeReviewedCreation(page: Page, create: (route: Route) => Promise<void>) {
  await page.route('**/api/workspaces', create);
  type Operation = {
    id: string;
    digest: string;
    material: Record<string, unknown>;
    state: string;
    result?: Record<string, any>;
    status?: number;
  };
  const operations = new Map<string, Operation>();
  const view = (operation: Operation) => ({
    id: operation.id,
    review_digest: operation.digest,
    state: operation.state
  });
  await page.route('**/api/workspaces/create-operations**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const input = request.method() === 'POST' ? request.postDataJSON() : {};
    if (path === '/api/workspaces/create-operations' && request.method() === 'POST') {
      const operation: Operation = {
        id: input.id,
        digest: createHash('sha256').update(JSON.stringify(input.request)).digest('hex'),
        material: structuredClone(input.request),
        state: 'reviewed'
      };
      operations.set(operation.id, operation);
      await route.fulfill({ json: { operation: view(operation) } });
      return;
    }
    const match = path.match(/\/create-operations\/([^/]+)(?:\/(confirm|abandon))?$/);
    const operation = match && operations.get(decodeURIComponent(match[1]));
    if (!operation) {
      await route.fulfill({ status: 404, json: { error: 'Unknown fixture operation' } });
      return;
    }
    if (request.method() === 'GET') {
      await route.fulfill({
        json: {
          operation: view(operation),
          ...(operation.state === 'refused'
            ? { refusal: operation.result }
            : { result: operation.result })
        }
      });
      return;
    }
    if (input.review_digest !== operation.digest) {
      await route.fulfill({ status: 409, json: { error: 'Fixture review changed' } });
      return;
    }
    if (match?.[2] === 'abandon' && operation.state === 'reviewed') {
      operation.state = 'abandoned';
      await route.fulfill({ json: { operation: view(operation) } });
      return;
    }
    if (match?.[2] !== 'confirm') {
      await route.fulfill({ status: 409, json: { error: 'Invalid fixture action' } });
      return;
    }
    if (operation.result) {
      await route.fulfill({ status: operation.status, json: operation.result });
      return;
    }
    // Invoke the test's existing virtual handler with the frozen ordinary body.
    // No request is forwarded to a real creation endpoint.
    const virtualRequest = new Proxy(request, {
      get(target, key) {
        if (key === 'url') return () => new URL('/api/workspaces', request.url()).href;
        if (key === 'postDataJSON') return () => structuredClone(operation.material);
        if (key === 'postData') return () => JSON.stringify(operation.material);
        const value = Reflect.get(target, key);
        return typeof value === 'function' ? value.bind(target) : value;
      }
    });
    const virtualRoute = new Proxy(route, {
      get(target, key) {
        if (key === 'request') return () => virtualRequest;
        if (key === 'fulfill')
          return async (options: Parameters<Route['fulfill']>[0]) => {
            const body = options?.json ?? JSON.parse(String(options?.body || '{}'));
            const status = options?.status || 200;
            if (status >= 200 && status < 300 && body.folder?.id) {
              body.success = true;
              body.folder = {
                name: operation.material.name,
                ...body.folder,
                shared_data: {
                  ...body.folder.shared_data,
                  workspace_creation_operation_id: operation.id,
                  workspace_creation_operation_digest: operation.digest
                }
              };
              operation.state = 'complete';
              const folder = structuredClone(body.folder);
              await page.route(`**/api/workspaces/${encodeURIComponent(folder.id)}`, resource =>
                resource.fulfill({ json: folder })
              );
            } else {
              operation.state = status >= 400 ? 'refused' : 'reconciliation_required';
            }
            body.creation_operation = view(operation);
            operation.result = body;
            operation.status = status;
            await route.fulfill({ status, json: body });
          };
        const value = Reflect.get(target, key);
        return typeof value === 'function' ? value.bind(target) : value;
      }
    });
    await create(virtualRoute);
  });
}

export { confirmReviewedCreation } from './confirm-reviewed-creation';
