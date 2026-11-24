import { test } from 'node:test';
import assert from 'node:assert/strict';

global.window = { workspaceDetail: null };
global.document = {
  createElement() {
    return {
      set textContent(_value) {},
      get innerHTML() {
        return '';
      }
    };
  }
};

const { WorkspaceDetailPage } = await import('./workspace-detail.js');

// The planning controls are folded into an "Existing plans" section, and Ori no
// longer starts new plans. Folding a control away must never change what a save
// writes: every planning value is loaded into its control and read back as it
// was, so a person who never opens the section keeps exactly the policy their
// existing plans were approved under.

const CONTROLS = [
  'settingsProfileInput',
  'settingsPresetInput',
  'settingsModeInput',
  'settingsConfirmationInput',
  'settingsPlanEnabledInput',
  'settingsRequireScanInput',
  'settingsSaveNotesInput',
  'settingsSyncTasksInput',
  'settingsAskHandoffInput',
  'settingsPlanningModeInput',
  'settingsClarificationModeInput',
  'settingsTasksDirInput',
  'settingsExecutionModeInput',
  'settingsWritePRDInput',
  'settingsWriteTaskListInput',
  'settingsRequireBranchInput',
  'settingsTaskMarkdownEnabledInput',
  'settingsTaskMarkdownPathInput',
  'settingsTaskMarkdownAgentViewsInput'
];

function formPage() {
  const page = Object.create(WorkspaceDetailPage.prototype);
  page.elements = { settingsTaskMarkdownStatus: { textContent: '' } };
  for (const name of CONTROLS) page.elements[name] = { value: '', checked: false };
  page.workspaceSettings = null;
  return page;
}

const roundTrip = stored => {
  const page = formPage();
  page.workspaceSettings = page.normalizeWorkspaceSettings(stored);
  page.populateWorkspaceSettingsForm(stored);
  return { page, saved: page.buildWorkspaceSettingsFromForm() };
};

const STORED = {
  // What software folder setup writes: the Planner preset with the exported
  // files switched off. A save that "tidied" these would start writing PRD and
  // task-list files into the user's project.
  softwareSetup: {
    profile: 'software_project',
    preset: 'planner',
    planning: { write_prd: false, write_task_list: false }
  },
  // A hand-tuned workspace that matches no preset.
  tuned: {
    version: 3,
    profile: 'software_project',
    preset: 'custom',
    workflow: {
      mode: 'plan_then_execute',
      require_repo_scan: true,
      save_outputs_as_notes: false,
      sync_plans_to_tasks: true,
      ask_before_specialist_handoff: true,
      confirmation_mode: 'always'
    },
    planning: {
      enabled: true,
      mode: 'investigation',
      write_prd: false,
      write_task_list: true,
      tasks_dir: 'plans/approved',
      clarification_mode: 'deep',
      default_execution_mode: 'auto',
      require_branch: false
    },
    task_markdown: { enabled: true, path: 'planning/tickets.md', generate_agent_views: false }
  },
  // A workspace with planning off and no plans at all.
  planningOff: {
    profile: 'general',
    preset: 'minimal',
    planning: { enabled: false }
  },
  // A fresh workspace with nothing saved yet.
  defaults: {}
};

for (const [name, stored] of Object.entries(STORED)) {
  test(`saving the form unchanged writes the same planning policy back (${name})`, () => {
    const { page, saved } = roundTrip(stored);
    const loaded = page.normalizeWorkspaceSettings(stored);
    assert.deepEqual(saved.planning, loaded.planning);
    assert.deepEqual(saved.workflow, loaded.workflow);
    assert.deepEqual(saved.task_markdown, loaded.task_markdown);
    assert.equal(saved.profile, loaded.profile);
  });
}

test('a policy that matches no preset is still saved as custom, not folded into one', () => {
  const { saved } = roundTrip(STORED.tuned);
  assert.equal(saved.preset, 'custom');
});

test('the form does not turn planning on, off or back to defaults when it was never touched', () => {
  for (const stored of [STORED.planningOff, STORED.softwareSetup]) {
    const before = formPage().normalizeWorkspaceSettings(stored).planning;
    const { saved } = roundTrip(stored);
    assert.equal(saved.planning.enabled, before.enabled);
    assert.equal(saved.planning.write_prd, before.write_prd);
    assert.equal(saved.planning.write_task_list, before.write_task_list);
  }
});
