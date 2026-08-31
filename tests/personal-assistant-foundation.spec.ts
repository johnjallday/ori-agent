import { test, expect } from '@playwright/test';

test.describe('Personal Assistant Foundation first value', () => {
  test('fresh no-model install hires, replaces preview, and creates one deterministic result', async ({
    page
  }) => {
    let relationshipVersion = 1;
    let previewVersion = 0;
    let previewId = '';
    let currentPreview: any = null;
    let applyCalls = 0;
    const externalWrites: string[] = [];

    page.on('request', request => {
      if (
        request.method() !== 'GET' &&
        /calendar|gmail|email|connection|oauth/i.test(new URL(request.url()).pathname)
      ) {
        externalWrites.push(`${request.method()} ${request.url()}`);
      }
    });

    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: true,
          current_step: 0,
          completed: false,
          skipped: false,
          steps_completed: [],
          user_name: '',
          assistant_name: 'Ori',
          timezone: 'UTC',
          personal_assistant_eligible: true
        })
      })
    );
    await page.route('**/api/settings/workspace-root', async route => {
      const confirmed = route.request().method() === 'POST';
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: confirmed || undefined,
          workspace_root: confirmed ? '/tmp/paf-workspaces' : '',
          effective_workspace_root: '/tmp/paf-workspaces',
          default_workspace_root: '/tmp/paf-workspaces',
          source: confirmed ? 'settings' : 'unconfirmed',
          confirmed
        })
      });
    });
    await page.route('**/api/onboarding/names', async route => {
      const body = route.request().postDataJSON();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ user_name: body.user_name || '', assistant_name: 'Ori' })
      });
    });
    await page.route('**/api/onboarding/timezone', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"timezone":"UTC"}' })
    );
    await page.route('**/api/onboarding/step', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"success":true}' })
    );
    await page.route('**/api/onboarding/complete', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"success":true}' })
    );
    await page.route('**/api/providers', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"providers":[]}' })
    );
    await page.route('**/api/project-templates', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"templates":[]}' })
    );
    await page.route('**/api/personal-assistant', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            state: 'not_hired',
            state_version: relationshipVersion,
            availability: {
              rollout: { status: 'available', available: true },
              model: { status: 'not_configured', available: false },
              calendar: { status: 'not_configured', available: false },
              email: { status: 'not_configured', available: false }
            }
          }
        })
      })
    );
    await page.route('**/api/personal-assistant/hire', async route => {
      relationshipVersion += 1;
      await route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            status: 'active',
            assistant_id: 'assistant-1',
            display_name: 'Atlas',
            hq_workspace_id: 'hq-1',
            hq_entry_agent_instance_id: 'entry-1',
            state_version: relationshipVersion,
            first_assignment_status: 'not_started',
            daily_brief: { timezone: 'UTC', schedule_days: ['mon'], schedule_time: '08:00' }
          }
        })
      });
    });
    await page.route('**/api/personal-assistant/first-assignment', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          first_assignment: {
            state_version: relationshipVersion,
            status: currentPreview ? 'previewed' : '',
            preview: currentPreview
          }
        })
      })
    );
    await page.route('**/api/personal-assistant/first-assignment/preview', async route => {
      const body = route.request().postDataJSON();
      previewVersion += 1;
      relationshipVersion += 1;
      previewId = `preview-${previewVersion}`;
      const items = body.rows.map((row: any, index: number) => ({
        id: `${previewId}-item-${index}`,
        input_type: row.type,
        record_type:
          row.type === 'priority' || (row.type === 'fixed_commitment' && row.action)
            ? 'ticket'
            : 'follow_up',
        category:
          row.type === 'priority'
            ? 'today_priority'
            : row.type === 'fixed_commitment'
              ? row.action
                ? 'fixed_commitment_action'
                : 'needs_decision'
              : row.type,
        state: row.type === 'priority' || row.action ? 'ready' : 'active',
        title: row.action || row.title,
        detail:
          row.type === 'fixed_commitment' && row.action ? `Fixed commitment: ${row.title}` : '',
        counterparty: row.counterparty || '',
        due: row.due || '',
        awaiting_execution_intent: row.type === 'priority' || Boolean(row.action)
      }));
      currentPreview = {
        preview_id: previewId,
        assignment_version: previewVersion,
        payload_hash: `hash-${previewVersion}`,
        items,
        count: items.length
      };
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          first_assignment: {
            preview: currentPreview,
            state_version: relationshipVersion,
            first_assignment_status: 'previewed'
          }
        })
      });
    });
    await page.route('**/api/personal-assistant/first-assignment/apply', async route => {
      applyCalls += 1;
      const body = route.request().postDataJSON();
      expect(body.preview_id).toBe(previewId);
      expect(body.preview_version).toBe(previewVersion);
      await new Promise(resolve => setTimeout(resolve, 350));
      relationshipVersion += 2;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          first_assignment: {
            preview_id: previewId,
            assignment_version: previewVersion + 4,
            state_version: relationshipVersion,
            status: 'completed',
            applied_count: currentPreview.items.length,
            total_count: currentPreview.items.length,
            outcome: 'complete',
            retryable: false,
            created_canonical_refs: [
              { kind: 'ticket', workspace_id: 'hq-1', id: 'ticket-1' },
              { kind: 'follow_up', workspace_id: 'hq-1', id: 'followup-1' }
            ],
            brief: {
              request_id: 'brief-request-1',
              revision_id: 'brief-revision-1',
              status: 'succeeded',
              route: '/api/personal-hq/brief/current',
              top_items: [
                {
                  title: 'Review the launch',
                  ref: { workspace_id: 'hq-1', entity_type: 'task', entity_id: 'ticket-1' }
                },
                {
                  title: 'Send Maya the draft',
                  ref: { workspace_id: 'hq-1', entity_type: 'follow_up', entity_id: 'followup-1' }
                }
              ],
              next_scheduled_check_in: '2026-10-21T08:00:00Z'
            }
          }
        })
      });
    });

    await page.goto('/');
    await expect(page.locator('#onboardingModal')).toBeVisible();
    await page.locator('#onboardingUserName').fill('Jordan');
    await page.locator('#welcomeNextBtn').click();
    await page.locator('#continueWithoutModelBtn').click();

    await expect(page.locator('#onboardingPersonalAssistantHire')).toBeVisible();
    await page.locator('#pafAssistantName').fill('Atlas');
    await page.locator('#pafHireConfirm').check();
    await page.locator('#pafHireBtn').click();

    await expect(page.locator('#onboardingPersonalAssistantAssignment')).toBeVisible();
    await page.locator('#pafPriorityRows [data-field="title"]').first().fill('Review the launch');
    await page
      .locator('#pafCommitmentRows [data-paf-assignment-row="i_owe"] [data-field="title"]')
      .fill('Send Maya the draft');
    await page
      .locator('#pafCommitmentRows [data-paf-assignment-row="i_owe"] [data-field="counterparty"]')
      .fill('Maya');
    await page
      .locator('#pafCommitmentRows [data-paf-assignment-row="waiting_on"] [data-field="title"]')
      .fill('Design approval');
    await page.locator('#pafFixedRows [data-field="title"]').fill('Call at 15:00');
    await page.locator('#pafFixedRows [data-field="action"]').fill('Prepare call notes');
    await page.locator('#pafPreviewAssignmentBtn').click();

    await expect(page.locator('#pafAssignmentPreview')).toBeVisible();
    const originalPreviewId = previewId;
    await page
      .locator('#pafAssignmentPreviewRows [data-field="title"]')
      .first()
      .fill('Review launch plan');
    await page.locator('#pafReplacePreviewBtn').click();
    await expect.poll(() => previewId).not.toBe(originalPreviewId);

    await page.locator('#pafAssignmentConfirm').check();
    await page.locator('#pafApplyAssignmentBtn').dblclick();
    await expect(page.locator('#pafAssignmentStatus')).toContainText('Generating');
    await expect(page.locator('#pafAssignmentResult')).toBeVisible();
    await expect(page.locator('#pafAssignmentResultSummary')).toContainText('saved');
    await expect(page.locator('#pafAssignmentResultItems')).toContainText('Review the launch');

    expect(applyCalls).toBe(1);
    expect(externalWrites).toEqual([]);
    expect(currentPreview.items.filter((item: any) => item.record_type === 'ticket')).toHaveLength(
      2
    );
    expect(
      currentPreview.items.filter((item: any) => item.record_type === 'follow_up')
    ).toHaveLength(2);
  });
});
