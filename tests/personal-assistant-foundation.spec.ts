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

  test('assistant-first Home keeps Ori Help isolated and handoff confirmable', async ({ page }) => {
    let eligible = true;
    let assistantName = 'Atlas';
    let relationshipState = 'active';
    let stateVersion = 7;
    let rememberWrites = 0;
    let routeCalls = 0;
    let askCalls = 0;
    const briefConfig = {
      timezone: 'UTC',
      schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
      schedule_time: '08:00',
      schedule_enabled: true,
      scope: 'all',
      selected_workspace_ids: [],
      include_future_workspaces: true,
      notify_on_ready: false,
      config_revision: 3,
      next_generation_at: '2026-09-01T08:00:00Z'
    };

    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 4,
          completed: true,
          skipped: false,
          steps_completed: ['step-done'],
          personal_assistant_eligible: eligible
        })
      })
    );
    await page.route('**/api/personal-assistant', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: eligible
            ? {
                state: relationshipState,
                state_version: stateVersion,
                assistant_id: 'assistant-stable',
                display_name: assistantName,
                hq_workspace_id: 'hq-1',
                mandate: 'Keep launch work visible.',
                focus_areas: ['plan_my_day'],
                daily_brief: briefConfig,
                appearance: { mode: 'generated', generated: { color: '#446688' } },
                availability: { model: { status: 'available', available: true } }
              }
            : { state: 'ineligible', availability: {} }
        })
      })
    );
    await page.route('**/api/personal-assistant/capabilities', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          capabilities: {
            state: relationshipState,
            cards: [
              {
                key: 'email',
                label: 'Email',
                status: 'not_configured',
                can_read: 'Attention signals from linked email.',
                can_propose: 'Follow-ups and draft replies.',
                requires_confirmation:
                  'No external email write is mapped in Personal Assistant v1.',
                mapped_write: false,
                action_label: 'Set up email',
                action_route: '/settings#google-account'
              },
              {
                key: 'projects',
                label: 'Projects and workspaces',
                status: 'available',
                can_read: 'Bounded task and result summaries.',
                can_propose: 'Internal tasks and follow-ups.',
                requires_confirmation: 'Existing work gates apply.',
                mapped_write: true,
                action_label: 'Open workspace Map',
                action_route: '/'
              }
            ]
          }
        })
      })
    );
    await page.route('**/api/personal-assistant/working-agreement', async route => {
      const body = route.request().postDataJSON();
      briefConfig.timezone = body.timezone;
      briefConfig.schedule_days = body.schedule_days;
      briefConfig.schedule_time = body.schedule_time;
      briefConfig.schedule_enabled = body.schedule_enabled;
      briefConfig.scope = body.scope;
      briefConfig.selected_workspace_ids = body.selected_workspace_ids;
      briefConfig.include_future_workspaces = body.include_future_workspaces;
      briefConfig.notify_on_ready = body.notify_on_ready;
      briefConfig.config_revision += 1;
      stateVersion += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            state: relationshipState,
            state_version: stateVersion,
            assistant_id: 'assistant-stable',
            display_name: assistantName,
            hq_workspace_id: 'hq-1',
            mandate: body.mandate,
            focus_areas: body.focus_areas,
            daily_brief: briefConfig,
            availability: { model: { status: 'available', available: true } }
          }
        })
      });
    });
    await page.route('**/api/personal-assistant/rename', async route => {
      assistantName = String(route.request().postDataJSON()?.name || assistantName);
      stateVersion += 3;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            state: relationshipState,
            state_version: stateVersion,
            assistant_id: 'assistant-stable',
            display_name: assistantName,
            hq_workspace_id: 'hq-1',
            mandate: 'Keep launch work visible.',
            focus_areas: ['plan_my_day'],
            daily_brief: briefConfig,
            availability: { model: { status: 'available', available: true } }
          }
        })
      });
    });
    await page.route(/\/api\/personal-assistant\/(pause|resume)$/, async route => {
      relationshipState = route.request().url().endsWith('/pause') ? 'paused' : 'active';
      stateVersion += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            state: relationshipState,
            state_version: stateVersion,
            assistant_id: 'assistant-stable',
            display_name: assistantName,
            hq_workspace_id: 'hq-1',
            mandate: 'Keep launch work visible.',
            focus_areas: ['plan_my_day'],
            daily_brief: briefConfig,
            availability: { model: { status: 'available', available: true } }
          }
        })
      });
    });
    await page.route('**/api/personal-assistant/today', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          today: {
            state: relationshipState,
            relationship_state: relationshipState,
            display_name: assistantName,
            hq_workspace_id: 'hq-1',
            hq_workspace_slug: 'personal-hq',
            model: { status: 'available', available: true },
            brief: {
              health: { status: 'available' },
              revision_id: 'brief-1',
              opening_summary: 'Two confirmed items need attention.',
              items: []
            },
            decisions: { health: { status: 'healthy_empty' }, items: [] },
            priorities: {
              health: { status: 'available' },
              items: [
                {
                  id: 'ticket-1',
                  kind: 'ticket',
                  title: 'Review launch plan',
                  route: '/workspaces/personal-hq?ticket=ticket-1'
                }
              ]
            },
            follow_ups: {
              health: { status: 'available' },
              items: [
                {
                  id: 'follow-1',
                  kind: 'follow_up',
                  title: 'Hear back from Maya',
                  route: '/workspaces/personal-hq?follow_up=follow-1'
                }
              ]
            },
            results: { health: { status: 'healthy_empty' }, items: [] },
            next_check_in: '2026-09-01T08:00:00Z',
            links: {
              personal_hq: '/workspaces/personal-hq',
              working_agreement: '/?personal-assistant=working-agreement',
              memory: '/workspaces/personal-hq#memory',
              advanced: '/agents'
            }
          }
        })
      })
    );
    await page.route('**/api/ori-guide', async route => {
      const question = String(route.request().postDataJSON()?.question || '');
      const work = /^create|^send|^draft/i.test(question);
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          work
            ? {
                status: 'answered',
                topic_key: 'workspace-manager',
                answer: 'That is personal work. Review the handoff before sending it.',
                actions: [{ type: 'handoff', label: 'Send this as work', handoff_text: question }]
              }
            : {
                status: 'answered',
                topic_key: 'settings',
                location: 'Home',
                answer: 'Settings contains global configuration.',
                actions: [
                  {
                    type: 'navigate',
                    label: 'Open Settings',
                    href: '/settings',
                    nav_key: 'settings'
                  }
                ]
              }
        )
      });
    });
    await page.route('**/api/home-assistant/route', route => {
      routeCalls += 1;
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'app_introspection',
          intent_label: 'current work',
          routing_policy: 'assistant_only',
          context_mode: 'direct',
          handoff_policy: 'assistant',
          matched_agent: assistantName,
          requires_creation: false,
          workspace_recommended: false,
          route_mode: 'home_inline',
          target_surface: 'current',
          assistant_name: assistantName,
          personal_assistant_state: relationshipState
        })
      });
    });
    await page.route('**/api/home-assistant/ask', route => {
      askCalls += 1;
      const body = route.request().postDataJSON() || {};
      if (body.confirmed_action?.type === 'remember') {
        rememberWrites += 1;
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            response: 'Saved that explicit memory.',
            intent: 'app_introspection',
            identity: {
              display_name: assistantName,
              role: 'Personal Assistant',
              state: relationshipState
            },
            actions: [
              {
                id: 'open-saved-memory',
                type: 'navigate',
                label: 'Review saved memory',
                href: '/workspaces/personal-hq#memory'
              }
            ]
          })
        });
        return;
      }
      if (
        String(body.prompt || '')
          .toLowerCase()
          .startsWith('remember that ')
      ) {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            response: 'Review this memory before saving it.',
            intent: 'app_introspection',
            identity: {
              display_name: assistantName,
              role: 'Personal Assistant',
              state: relationshipState
            },
            requires_confirmation: true,
            confirmation: {
              action_id: 'remember-explicit',
              action_type: 'remember',
              summary: 'Remember “Friday launches need a Thursday review” in Personal HQ Memory?',
              arguments: {
                state_version: stateVersion,
                destination: 'personal_hq',
                text: 'Friday launches need a Thursday review'
              }
            }
          })
        });
        return;
      }
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response: 'Your launch review is ready.',
          intent: 'app_introspection',
          identity: {
            display_name: assistantName,
            role: 'Personal Assistant',
            state: relationshipState
          },
          actions: []
        })
      });
    });

    await page.goto('/');
    await expect(page.locator('#personalAssistantToday')).toBeVisible();
    await expect(page.locator('#personalAssistantTodayEyebrow')).toHaveText('Today from Atlas');
    await expect(page.locator('#personalAssistantTodayPriorities')).toContainText(
      'Review launch plan'
    );
    await expect(page.locator('#personalAssistantTodayHQ')).toHaveAttribute(
      'href',
      '/workspaces/personal-hq'
    );
    expect(
      await page.evaluate(() => {
        const today = document.getElementById('personalAssistantToday');
        const map = document.getElementById('homeCockpit');
        return Boolean(
          today && map && today.compareDocumentPosition(map) & Node.DOCUMENT_POSITION_FOLLOWING
        );
      })
    ).toBe(true);

    const todayBox = await page.locator('#personalAssistantToday').boundingBox();
    const mapBox = await page.locator('#homeCockpit').boundingBox();
    expect(todayBox && mapBox && todayBox.y + todayBox.height <= mapBox.y + 1).toBe(true);

    await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
    await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Atlas');
    await expect(page.locator('#personalAssistantLauncherAvatar .agent-avatar')).toHaveCount(1);
    await page.locator('#oriGuideMapTrigger').click();
    await expect(page.locator('#oriGuidePanel')).toBeVisible();
    await expect(page.locator('#oriGuideRole')).toHaveText('App Guide');
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();

    await page.locator('#oriGuideInput').fill('Where are global settings?');
    await page.locator('#oriGuideSend').click();
    await expect(page.locator('#oriGuideReply')).toContainText('Settings contains');

    const handoffText = 'Create a launch review for Friday';
    await page.locator('#oriGuideInput').fill(handoffText);
    await page.locator('#oriGuideSend').click();
    await expect(page.getByRole('button', { name: 'Send to Atlas' })).toBeVisible();
    expect(routeCalls).toBe(0);
    await page.getByRole('button', { name: 'Send to Atlas' }).click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(page.locator('#oriGuidePanel')).toBeHidden();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(handoffText);
    expect(routeCalls).toBe(0);

    // Cancel once: closing the panel keeps this only as an assistant draft and
    // never routes it. Ori's Help composer/transcript stay in Ori's own panel.
    await page.locator('#personalAssistantClose').click();
    expect(routeCalls).toBe(0);
    await page.locator('#personalAssistantLauncher').click();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(handoffText);
    await expect(page.locator('#personalAssistantPanel #oriGuideActivity')).toHaveCount(0);
    await expect(page.locator('#oriGuidePanel #homeAssistantConversation')).toHaveCount(0);

    await page.locator('#personalAssistantSend').click();
    await expect.poll(() => routeCalls).toBe(1);
    await expect.poll(() => askCalls).toBe(1);
    await page.locator('#personalAssistantClose').click();

    // Working agreement edits reuse canonical schedule values, rename the same
    // stable identity, and survive reload.
    await page.locator('#personalAssistantTodayAgreement').click();
    await expect(page.locator('#personalAssistantContinuity')).toBeVisible();
    await expect(page.locator('#personalAssistantCapabilities')).toContainText(
      'No external email write is mapped in Personal Assistant v1'
    );
    await page.locator('#personalAssistantContinuityName').fill('Nova');
    await page
      .locator('#personalAssistantContinuityMandate')
      .fill('Keep launch decisions visible.');
    await page.locator('#personalAssistantContinuityTimezone').fill('America/New_York');
    await page.locator('#personalAssistantContinuityTime').fill('09:15');
    await page.locator('#personalAssistantContinuityScope').selectOption('personal_hq');
    await page.locator('#personalAssistantContinuityFuture').uncheck();
    await page.locator('#personalAssistantContinuitySave').click();
    await expect(page.locator('#personalAssistantContinuityStatus')).toContainText(
      'Working agreement saved'
    );
    await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Nova');
    expect(briefConfig.schedule_enabled).toBe(true);
    expect(briefConfig.schedule_time).toBe('09:15');
    expect(briefConfig.scope).toBe('selected');
    expect(briefConfig.selected_workspace_ids).toEqual(['hq-1']);
    expect(briefConfig.include_future_workspaces).toBe(false);

    await page.reload();
    await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Nova');
    await page.locator('#personalAssistantTodayAgreement').click();
    await expect(page.locator('#personalAssistantContinuityName')).toHaveValue('Nova');
    await page.locator('#personalAssistantContinuityPause').click();
    await expect(page.locator('#personalAssistantContinuityStatus')).toContainText(
      'Your schedule, history, memory, connections, and permissions were preserved'
    );
    expect(relationshipState).toBe('paused');
    expect(briefConfig.schedule_enabled).toBe(true);
    await page.locator('#personalAssistantContinuityPause').click();
    await expect(page.locator('#personalAssistantContinuityStatus')).toContainText(
      'Resumed with the preserved Daily Brief rhythm'
    );
    expect(relationshipState).toBe('active');
    await page.locator('#personalAssistantContinuityClose').click();

    // Explicit memory is confirmation-gated. Cancel once, then confirm exactly
    // once; ordinary Help never receives or executes this action.
    await page.locator('#personalAssistantLauncher').click();
    await page
      .locator('#personalAssistantInput')
      .fill('remember that Friday launches need a Thursday review');
    await page.locator('#personalAssistantSend').click();
    await expect(page.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    expect(rememberWrites).toBe(0);
    await page
      .locator('#personalAssistantInput')
      .fill('remember that Friday launches need a Thursday review');
    await page.locator('#personalAssistantSend').click();
    await page.getByRole('button', { name: 'Confirm', exact: true }).click();
    await expect.poll(() => rememberWrites).toBe(1);
    await expect(page.getByRole('button', { name: 'Review saved memory' })).toBeVisible();

    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)
    ).toBe(true);

    // The same server build preserves the legacy unified surface when the
    // authoritative status says this installation is ineligible.
    eligible = false;
    await page.reload();
    await expect(page.locator('#personalAssistantToday')).toBeHidden();
    await expect(page.locator('#personalAssistantLauncher')).toBeHidden();
    await expect(page.locator('#oriGuideLauncher .ori-guide__launcher-name')).toHaveText('Ask Ori');
    await expect(page.locator('#homeCockpit')).toBeVisible();
  });
});
