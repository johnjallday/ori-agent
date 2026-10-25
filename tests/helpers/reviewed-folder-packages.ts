import { expect, type APIRequestContext } from '@playwright/test';

// Disposable sandbox staging only. This is not canonical folder-card review
// evidence; that independent install/update/enable path has its own journey.
export async function stageReviewedFolderPackages(request: APIRequestContext) {
  for (const source of [
    'https://github.com/johnjallday/music-project-management#sha=af275d45936102cbea3a4dc6b32be619c293bc4c',
    'https://github.com/johnjallday/reaper-plugin#sha=1e0e06c3fcdb34c2022133bd08ea294d58457fe5'
  ]) {
    const preview = await request.post('/api/plugins/install', { data: { source } });
    expect(preview.ok(), await preview.text()).toBeTruthy();
    const install = await request.post('/api/plugins/install', { data: { source, confirm: true } });
    expect(install.ok(), await install.text()).toBeTruthy();
    const { plugin } = await install.json();
    const enable = await request.post(`/api/plugins/${plugin.name}/enable`, { data: {} });
    expect(enable.ok(), await enable.text()).toBeTruthy();
    console.log('Staged sandbox package:', plugin.name, plugin.version, source);
  }
}
