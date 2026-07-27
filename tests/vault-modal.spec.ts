import { test, expect } from '@playwright/test';

test.describe('Vault Modal', () => {
  test('hydrates available vaults when opened', async ({ page }) => {
    let vaultListCalls = 0;

    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          completed: true,
          skipped: true
        })
      });
    });
    await page.route('**/api/vault/vaults', async route => {
      vaultListCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          count: 2,
          vaults: [
            {
              id: 'vault-personal',
              name: 'Personal',
              password_protected: true,
              record_count: 1
            },
            {
              id: 'vault-archive',
              name: 'Archive',
              password_protected: true,
              record_count: 0
            }
          ]
        })
      });
    });
    await page.route('**/api/vault/status?*', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          vault_id: 'vault-personal',
          vault_name: 'Personal',
          available: true,
          locked: false,
          writable: true,
          password_protected: true,
          requires_passphrase: false,
          record_count: 1,
          secret_store: {
            backend: 'vault_password',
            available: true,
            writable: true,
            locked: false
          }
        })
      });
    });
    await page.route('**/api/vault/folders?*', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ folders: [] })
      });
    });
    await page.route('**/api/vault/records?*', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          records: [
            {
              id: 'record-passport',
              vault_id: 'vault-personal',
              type: 'personal_note',
              label: 'Passport details',
              folder_path: '',
              workspace_id: '',
              tags: [],
              payload: { note: 'Private test content' },
              created_at: '2026-07-27T14:00:00Z',
              updated_at: '2026-07-27T14:00:00Z'
            }
          ]
        })
      });
    });

    await page.goto('/');
    await page.locator('#vaultLauncherButton').click();

    await expect.poll(() => vaultListCalls).toBe(1);
    await expect(page.locator('#vaultModal')).toHaveClass(/show/);
    await expect(
      page.locator('#vaultModalFolderVaultTabs [data-action="select-folder-vault-tab"]')
    ).toHaveCount(2);
    await expect(page.locator('#vaultModalRecordsSummary')).toHaveText('1 of 1 item in Personal');
  });
});
