import { expect, type APIRequestContext } from '@playwright/test';

// Disposable sandbox staging only. This is not canonical folder-card review
// evidence; that independent install/update/enable path has its own journey.
// Keep these at the already-reviewed fallback commits in reviewedintegration/entries.go.
export async function stageReviewedFolderPackages(request: APIRequestContext) {
  for (const source of [
    'https://github.com/johnjallday/music-project-management#sha=c420507da24c1fc007d794f3e8974231dd2c5d4a',
    'https://github.com/johnjallday/reaper-plugin#sha=ea85c6692e49ddaaf83ae3f9e17ede17fe6385e3'
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
