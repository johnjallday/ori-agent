import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./templates-page.js', import.meta.url), 'utf8');

function harness(template) {
  const nodes = new Map();
  const requests = [];
  const notices = [];
  let confirmResult = false;

  const makeNode = id => {
    const classes = new Set();
    const value = {
      id,
      hidden: false,
      disabled: false,
      checked: false,
      value: '',
      textContent: '',
      innerHTML: '',
      style: {},
      children: [],
      options: [],
      appendChild(child) {
        this.children.push(child);
        return child;
      },
      querySelectorAll() {
        return [];
      },
      removeAttribute(name) {
        delete this[name];
      },
      classList: {
        add(...names) {
          names.forEach(name => classes.add(name));
        },
        remove(...names) {
          names.forEach(name => classes.delete(name));
        },
        contains(name) {
          return classes.has(name);
        }
      }
    };
    if (id === 'tplGroupPolicy') {
      value.options = ['none', 'recommended', 'required'].map(option => ({
        value: option,
        disabled: false
      }));
    }
    if (id === 'tplGroupMissing') {
      value.options = ['offer_create', 'existing_only'].map(option => ({
        value: option,
        disabled: false
      }));
    }
    return value;
  };
  const node = id => {
    if (!nodes.has(id)) nodes.set(id, makeNode(id));
    return nodes.get(id);
  };

  const sandbox = {
    console,
    document: {
      readyState: 'loading',
      addEventListener() {},
      getElementById: node,
      querySelectorAll: () => [],
      createElement: tag => makeNode(tag)
    },
    window: {
      confirm: () => confirmResult,
      notifyToast: (message, kind) => notices.push({ message, kind })
    },
    fetch: async (url, options = {}) => {
      requests.push({ url, options });
      return {
        ok: true,
        status: 200,
        json: async () => ({
          label: 'Preview — no workspace or Home created',
          policy: 'recommended',
          available_compositions: ['grouped', 'standalone'],
          grouped: {
            project_roles: ['Project Lead'],
            unavailable_home_roles: [],
            capabilities: ['project-files'],
            runtime_modes: ['File-only'],
            pre_workspace_setup: true,
            assistant_program: true
          },
          standalone: {
            project_roles: ['Project Lead'],
            unavailable_home_roles: ['Coordinator'],
            capabilities: ['project-files'],
            runtime_modes: ['File-only'],
            pre_workspace_setup: false,
            assistant_program: false
          }
        })
      };
    }
  };
  vm.createContext(sandbox);
  vm.runInContext(
    source +
      `\nglobalThis.subject = {
        tplState, tplGroup, tplFiles, tplTemplateVariant, tplSourceSectionsReadOnly,
        tplGroupRender, tplGroupMarkDirty, tplGroupPreview, tplGroupRequirementFromControls,
        tplApplyReadOnly, tplGuardDirty, tplSaveOverview,
        setRefresh(fn) { tplRefresh = fn; }
      };`,
    sandbox
  );
  sandbox.subject.tplState.templates = [template];
  sandbox.subject.tplState.selectedId = template.id;
  sandbox.subject.setRefresh(async () => {});
  return {
    subject: sandbox.subject,
    node,
    requests,
    notices,
    setConfirm(value) {
      confirmResult = value;
    }
  };
}

function groupedPluginTemplate() {
  return {
    id: 'plugin:neutral-plugin:neutral-project',
    name: 'Neutral Project',
    revision: 'a'.repeat(64),
    plugin_owner: {
      plugin_id: 'neutral-plugin',
      plugin_version: '1.0.0',
      blueprint_id: 'neutral-project',
      blueprint_version: 2
    },
    assistant_program: { id: 'project-guide', station_name: 'Project Guide Home' },
    standalone_composition: { schema_version: 1, project_roles: [] },
    group_requirement: {
      schema_version: 1,
      policy: 'required',
      assistant_program_id: 'project-guide',
      missing_home: 'offer_create',
      default_home_name: 'Project Guide Home'
    }
  };
}

test('plugin originals disclose the effective requirement and offer bounded customization', () => {
  const { subject, node, requests } = harness(groupedPluginTemplate());
  subject.tplGroupRender();
  subject.tplApplyReadOnly();

  assert.equal(node('tplGroupPolicy').value, 'required');
  assert.equal(node('tplGroupPolicy').disabled, true);
  assert.equal(node('tplGroupMissing').disabled, true);
  assert.equal(node('tplGroupCustomizeBtn').hidden, false);
  assert.equal(node('tplDuplicateBtn').disabled, false);
  assert.equal(node('tplDuplicateBtn').textContent, 'Customize');
  assert.match(
    node('tplGroupComposition').textContent,
    /exact source-owned Assistant Program Home/
  );
  assert.deepEqual(requests, []);
});

test('a source-linked variant edits only metadata and placement while source sections stay locked', () => {
  const sourceTemplate = groupedPluginTemplate();
  const variant = {
    ...sourceTemplate,
    id: 'my-neutral-project',
    plugin_owner: undefined,
    variant_revision: 'b'.repeat(64),
    variant_source_state: 'ready',
    template_variant: {
      source: {
        plugin_id: 'neutral-plugin',
        plugin_version: '1.0.0',
        blueprint_id: 'neutral-project',
        blueprint_version: 2
      }
    }
  };
  const { subject, node } = harness(variant);
  subject.tplGroupRender();
  subject.tplApplyReadOnly();

  assert.equal(subject.tplTemplateVariant(variant), true);
  assert.equal(subject.tplSourceSectionsReadOnly(variant), true);
  assert.equal(node('tplEditName').disabled, false);
  assert.equal(node('tplEditIcon').disabled, false);
  assert.equal(node('tplGroupPolicy').disabled, false);
  assert.equal(node('tplEditBehavior').disabled, true);
  assert.equal(node('tplToolsSaveBtn').disabled, true);
  assert.equal(node('tplSaveBtn').disabled, false);
  assert.match(node('tplGroupSource').textContent, /Pinned source: neutral-plugin 1.0.0/);
  assert.match(node('tplReadOnlyText').textContent, /placement policy are user-owned/);
});

test('preview sends the current variant revision and renders both inert consequences', async () => {
  const variant = {
    ...groupedPluginTemplate(),
    id: 'my-neutral-project',
    plugin_owner: undefined,
    variant_revision: 'c'.repeat(64),
    variant_source_state: 'ready',
    template_variant: {
      source: {
        plugin_id: 'neutral-plugin',
        plugin_version: '1.0.0',
        blueprint_id: 'neutral-project',
        blueprint_version: 2
      }
    }
  };
  const { subject, node, requests } = harness(variant);
  subject.tplGroupRender();
  node('tplGroupPolicy').value = 'recommended';
  node('tplGroupMissing').value = 'existing_only';
  await subject.tplGroupPreview();

  assert.equal(requests.length, 1);
  assert.equal(
    requests[0].url,
    '/api/project-templates/my-neutral-project/group-requirement/preview'
  );
  const body = JSON.parse(requests[0].options.body);
  assert.equal(body.if_revision, variant.variant_revision);
  assert.deepEqual(body.group_requirement, {
    schema_version: 1,
    policy: 'recommended',
    assistant_program_id: 'project-guide',
    missing_home: 'existing_only'
  });
  assert.equal(node('tplGroupPreview').hidden, false);
  assert.equal(node('tplGroupPreview').children.length, 5);
});

test('legacy absence survives unrelated saves and dirty navigation is explicit', async () => {
  const template = {
    id: 'ordinary',
    name: 'Ordinary',
    revision: 'd'.repeat(64),
    agents: [{ name: 'Lead' }]
  };
  const { subject, node, requests, setConfirm } = harness(template);
  node('tplEditName').value = 'Ordinary';
  node('tplEditDescription').value = 'Metadata only';
  node('tplEditIcon').value = '';
  node('tplEditBehavior').value = 'general';
  subject.tplGroupRender();
  await subject.tplSaveOverview();
  const metadataBody = JSON.parse(requests[0].options.body);
  assert.equal('group_requirement' in metadataBody, false);
  assert.equal(metadataBody.if_revision, template.revision);

  node('tplGroupPolicy').value = 'none';
  subject.tplGroupMarkDirty();
  await subject.tplSaveOverview();
  const policyBody = JSON.parse(requests[1].options.body);
  assert.deepEqual(policyBody.group_requirement, { schema_version: 1, policy: 'none' });

  subject.tplGroupMarkDirty();
  assert.equal(subject.tplGroup.dirty, true);
  assert.equal(node('tplGroupDirty').hidden, false);
  let proceeded = false;
  subject.tplGuardDirty(() => {
    proceeded = true;
  });
  assert.equal(proceeded, false);
  assert.equal(subject.tplGroup.dirty, true);
  setConfirm(true);
  subject.tplGuardDirty(() => {
    proceeded = true;
  });
  assert.equal(proceeded, true);
  assert.equal(subject.tplGroup.dirty, false);
});
