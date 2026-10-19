import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';

// Folder protection M2: with INVENTED stored restic evidence covering one of
// two adopted folders, Today reads "1 of your 2 project folders…", the covered
// workspace's fact names restic and the owning workspace, and the other says
// it is not in any backup Ori observes.
//
// Stored backup evidence is encrypted under the installation secret store, so
// it cannot be fabricated through the product API (by design). Like
// tests/backup-recovery-summary.spec.ts this runs only with an operator-seeded
// sandbox described by ORI_FOLDER_PROTECTION_COVERED_CONTROL, a JSON file:
//   { "kind": "invented stored metadata only", "covered_workspace_id": "…",
//     "uncovered_workspace_id": "…", "backup_workspace_name": "…" }
// The seed runs while Ori is stopped, with the server's own secret store, from
// the sandbox directory. Go tests cover the same behavior without a browser.
test('invented restic evidence covers one adopted folder of two', async ({ page }) => {
  test.skip(
    !process.env.ORI_FOLDER_PROTECTION_COVERED_CONTROL,
    'Needs an operator-seeded covered fixture (see the header).'
  );
  const control = JSON.parse(
    readFileSync(process.env.ORI_FOLDER_PROTECTION_COVERED_CONTROL!, 'utf8')
  );
  expect(control.kind).toBe('invented stored metadata only');

  const today = (await (await page.request.get('/api/personal-assistant/today')).json()).today;
  const lines = (today.backups?.items || []).filter(item => item.kind === 'backup_coverage');
  expect(lines).toHaveLength(1);
  expect(lines[0].title).toBe(
    '1 of your 2 project folders is not covered by a backup Ori observes'
  );
  expect(lines[0].route).toMatch(/\?panel=backup-recovery&tab=overview&section=coverage$/);

  const covered = (
    await (
      await page.request.get(`/api/workspaces/${control.covered_workspace_id}/protection`)
    ).json()
  ).protection;
  expect(covered.state).toBe('covered');
  expect(covered.sentence).toMatch(
    new RegExp(
      `^Protected by restic in ${control.backup_workspace_name}, last completed backup .+ ago$`
    )
  );
  const uncovered = (
    await (
      await page.request.get(`/api/workspaces/${control.uncovered_workspace_id}/protection`)
    ).json()
  ).protection;
  expect(uncovered.sentence).toBe('Not in any backup Ori observes');
  for (const payload of [today, covered, uncovered])
    expect(JSON.stringify(payload)).not.toMatch(/"\/(Users|Volumes|private)\//);
});
