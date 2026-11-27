import { expect, type APIRequestContext } from '@playwright/test';

// Seed a disposable workspace through the real manual Filing setup. Callers
// supply only a synthetic source root; this never attaches a destination.
export async function filingWorkspace(
  request: APIRequestContext,
  label: string,
  root: string,
  name = `${label} ${Date.now()} ${Math.random().toString(36).slice(2, 6)}`
) {
  const created = await request.post('/api/workspaces', {
    data: { name }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const body = await created.json();
  const folder = body.folder || body.workspace;
  const id = folder.id as string;
  const install = await request.post(`/api/workspaces/${id}/capabilities/file-janitor/install`, {
    data: { source: 'in-place' }
  });
  expect(install.ok(), await install.text()).toBeTruthy();
  const link = await request.post(`/api/workspaces/${id}/directories`, {
    data: { name: `${label} folder`, path: root }
  });
  expect(link.ok(), await link.text()).toBeTruthy();
  const ref = (await link.json()).directory.id;
  const base = `/api/workspaces/${id}/file-janitor`;
  const review = await request.post(`${base}/setup-review`, {
    data: { reference_id: ref, monitoring: false }
  });
  expect(review.ok(), await review.text()).toBeTruthy();
  const revision = (await review.json()).review.review_revision;
  const prepare = await request.post(`${base}/prepare-review`, {
    data: { reference_id: ref, monitoring: false, review_revision: revision }
  });
  expect(prepare.ok(), await prepare.text()).toBeTruthy();
  return { id, slug: folder.folder_slug as string, base, root, name: folder.name as string };
}
