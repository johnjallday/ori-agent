#!/usr/bin/env node
// Disposable old/new Home package fixture for the in-place plugin update
// (tests/plugin-in-place-updates.spec.ts and its wt demo).
//
// It writes two Ori-owned packages into a sandbox under the system temp
// folder, installs them into the isolated server it is pointed at, and seeds
// Homes, a linked project and staffed agents through ordinary routes. A newer
// release is made by rewriting the installed Home package directory in place,
// which is what ordinary Update and the Home upgrade both read for a local
// install. Nothing here talks to a network release, a real plugin store, or
// the user's own Ori.
//
//   node tests/fixtures/in-place-upgrade.mjs seed    --base-url URL --sandbox DIR
//   node tests/fixtures/in-place-upgrade.mjs release --sandbox DIR [--version V]

import { mkdir, readdir, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

export const HOME_PLUGIN = 'in-place-studio-home';
export const SONG_PLUGIN = 'in-place-studio-songs';
export const SONG_TEAM = 'studio-song-team';
export const OLD_VERSION = '0.1.0';
export const NEW_VERSION = '0.2.0';
export const EDITED_PROMPT = 'My own label guidance. Keep it through upgrades.';

// The package declares two Home programs, so one upgrade moves two Homes (an
// owner has at most one Home per program). Only the studio takes songs.
export const HOME_PROGRAMS = [
  { id: 'studio-home', station: 'Studio Home', role: 'portfolio_manager', label: 'Studio Manager' },
  { id: 'label-home', station: 'Label Home', role: 'label_manager', label: 'Label Manager' }
];

export function rolePrompt(program, version) {
  return version === OLD_VERSION
    ? `${program.label}: coordinate this Home's portfolio. (in-place fixture ${version})`
    : `${program.label}: coordinate this Home's portfolio and its reviewed library. (in-place fixture ${version})`;
}

function homeDeclaration(program, version) {
  return {
    schema_version: 1,
    version: 1,
    id: program.id,
    station_name: program.station,
    default_primary_name: program.label,
    hire_title: `Staff this ${program.station}`,
    roles: [
      {
        id: program.role,
        label: program.label,
        required: true,
        primary: true,
        role: 'orchestrator',
        system_prompt: rolePrompt(program, version),
        skills: [HOME_PLUGIN]
      }
    ],
    stages: [{ id: 'foundation', label: 'Foundation', accepted_completion_threshold: 0 }],
    reflection: {
      minimum_projects: 3,
      cadence_hours: 168,
      max_projects: 16,
      max_events_per_project: 16,
      max_candidates: 8,
      max_evidence: 8,
      rubric: 'Offer bounded improvements.'
    },
    ...(program.id === HOME_PROGRAMS[0].id
      ? {
          allowed_project_attachments: [
            {
              provider_plugin_id: SONG_PLUGIN,
              blueprint_id: 'song',
              project_team_id: SONG_TEAM,
              project_team_schema_version: 1,
              min_project_team_version: 1,
              max_project_team_version: 1
            }
          ]
        }
      : {})
  };
}

export function homePackageFiles(version = OLD_VERSION) {
  return {
    '.claude-plugin/plugin.json': JSON.stringify({ name: HOME_PLUGIN, version }),
    '.ori-plugin/plugin.json': JSON.stringify(
      {
        schema_version: 1,
        name: HOME_PLUGIN,
        version,
        protocol: { min: 1, max: 1 },
        requires_host_features: ['independent_program_homes_v1'],
        capabilities: [],
        services: [],
        blueprints: [],
        assistant_program_homes: HOME_PROGRAMS.map(program => homeDeclaration(program, version))
      },
      null,
      2
    ),
    [`skills/${HOME_PLUGIN}/SKILL.md`]: `---\nname: ${HOME_PLUGIN}\ndescription: Studio guidance ${version}.\n---\nStudio guidance for ${version}.\n`
  };
}

export function songPackageFiles() {
  return {
    '.claude-plugin/plugin.json': JSON.stringify({ name: SONG_PLUGIN, version: '1.0.0' }),
    '.ori-plugin/plugin.json': JSON.stringify(
      {
        schema_version: 1,
        name: SONG_PLUGIN,
        version: '1.0.0',
        protocol: { min: 1, max: 1 },
        requires_host_features: ['template_group_requirements_v1', 'independent_program_homes_v1'],
        capabilities: [],
        services: [],
        blueprints: [
          {
            id: 'song',
            version: 1,
            manifest: 'blueprints/song/template.json',
            skeleton: 'blueprints/song/project',
            capabilities: []
          }
        ]
      },
      null,
      2
    ),
    'blueprints/song/template.json': JSON.stringify(
      {
        name: 'Studio Song',
        description: 'A song project that belongs to a Studio Home (in-place update fixture).',
        behavior_profile: 'general',
        assistant_project: {
          schema_version: 1,
          version: 1,
          id: SONG_TEAM,
          home: {
            provider_plugin_id: HOME_PLUGIN,
            program_id: HOME_PROGRAMS[0].id,
            home_schema_version: 1,
            min_home_version: 1,
            max_home_version: 1
          },
          roles: [
            {
              id: 'song_lead',
              label: 'Song Lead',
              description: 'Coordinates this song only.',
              required: true,
              primary: true,
              role: 'orchestrator',
              system_prompt: 'Coordinate only this song.'
            }
          ]
        },
        group_requirement: {
          schema_version: 2,
          policy: 'required',
          assistant_project_id: SONG_TEAM,
          missing_home: 'existing_only'
        }
      },
      null,
      2
    ),
    'blueprints/song/project/README.md': '# Studio song\n\nIn-place update fixture project.\n'
  };
}

// assertSandbox refuses any directory outside the system temp folder, so the
// fixture can never write into a real home or workspace tree.
export async function assertSandbox(dir) {
  if (!dir) throw new Error('a sandbox directory is required');
  await mkdir(dir, { recursive: true, mode: 0o750 });
  const [root, real] = await Promise.all([realpath(tmpdir()), realpath(resolve(dir))]);
  if (real !== root && !real.startsWith(root + sep)) {
    throw new Error(`refusing a sandbox outside ${root}: ${real}`);
  }
  return real;
}

async function writeTree(root, files) {
  for (const [name, content] of Object.entries(files)) {
    const path = join(root, name);
    await mkdir(dirname(path), { recursive: true, mode: 0o750 });
    await writeFile(path, content, { mode: 0o600 });
  }
}

export function homePackageDir(sandbox) {
  return join(sandbox, 'in-place-upgrade', HOME_PLUGIN);
}

// writeHomeRelease replaces the installed Home package export with another
// release, as a developer replacing a local export would.
export async function writeHomeRelease(sandbox, version = NEW_VERSION) {
  const root = homePackageDir(await assertSandbox(sandbox));
  await mkdir(root, { recursive: true, mode: 0o750 });
  for (const entry of await readdir(root)) await rm(join(root, entry), { recursive: true, force: true });
  await writeTree(root, homePackageFiles(version));
  return root;
}

function client(baseURL) {
  const url = new URL(baseURL);
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)) {
    throw new Error(`refusing a non-local server: ${baseURL}`);
  }
  if (url.port === '8765' || url.port === '') {
    throw new Error('refusing the default Ori port; point the fixture at an isolated demo server');
  }
  return async (method, path, body) => {
    const response = await fetch(new URL(path, url), {
      method,
      headers: {
        Accept: 'application/json',
        'X-Requested-With': 'XMLHttpRequest',
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' })
      },
      body: body === undefined ? undefined : JSON.stringify(body)
    });
    const text = await response.text();
    if (!response.ok) throw new Error(`${method} ${path} → ${response.status}: ${text}`);
    return text ? JSON.parse(text) : {};
  };
}

async function createHome(call, program, name, key) {
  const templates = (await call('GET', '/api/workspaces/group-templates')).group_templates || [];
  const template = templates.find(
    item => item?.provider?.plugin_id === HOME_PLUGIN && item.name === program.station
  );
  if (!template) throw new Error(`no group template for ${program.station}: ${JSON.stringify(templates)}`);
  const input = { group_template_id: template.id, revision: template.revision, name };
  const review = (await call('POST', '/api/workspaces/group-templates/review', input))
    .group_template_review;
  const committed = await call('POST', '/api/workspaces/group-templates/commit', {
    ...input,
    group_review_token: review.review_token,
    idempotency_key: key
  });
  const id = committed.group_template.home_workspace_id;
  return { id, ...(await call('GET', `/api/workspaces/${encodeURIComponent(id)}`)) };
}

async function linkSong(call, name, key) {
  const data = { name, template_id: `plugin:${SONG_PLUGIN}:song`, project_name: name };
  const reviewed = (
    await call('POST', '/api/workspaces', {
      ...data,
      group_composition: 'grouped',
      group_requirement_review: true
    })
  ).group_requirement_review;
  if (reviewed?.state !== 'ready_grouped') {
    throw new Error(`song ${name} is not ready to join its Home: ${JSON.stringify(reviewed)}`);
  }
  return call('POST', '/api/workspaces', {
    ...data,
    group_composition: 'grouped',
    group_review_token: reviewed.review_token,
    idempotency_key: key
  });
}

// seed installs the old release and creates one Home per program: North
// Studio with a linked song and a manager whose prompt is still the role's,
// and South Label whose manager's prompt the owner edited.
export async function seed({ baseURL, sandbox, homes = ['North Studio', 'South Label'], song = 'First Song' }) {
  const real = await assertSandbox(sandbox);
  const call = client(baseURL);
  const fixtures = join(real, 'in-place-upgrade');
  const songRoot = join(fixtures, SONG_PLUGIN);
  await rm(fixtures, { recursive: true, force: true });
  await writeTree(songRoot, songPackageFiles());
  const homeRoot = await writeHomeRelease(real, OLD_VERSION);

  await call('POST', '/api/onboarding/skip').catch(() => {});
  await call('POST', '/api/settings/workspace-root', { workspace_root: '' }).catch(() => {});
  for (const root of [homeRoot, songRoot]) {
    const installed = await call('POST', '/api/plugins/install', {
      source: root,
      format: 'claude',
      confirm: true
    });
    await call('POST', `/api/plugins/${encodeURIComponent(installed.plugin.name)}/enable`);
  }

  const seeded = [];
  for (const [index, name] of homes.slice(0, HOME_PROGRAMS.length).entries()) {
    const program = HOME_PROGRAMS[index];
    const home = await createHome(call, program, name, `in-place-home-${index}`);
    const manager = `${name.split(' ')[0]} Manager`;
    // No provider or model: an explicit one must be available to chat, and
    // this fixture never chats. The agent keeps the role's prompt.
    await call('PUT', `/api/workspaces/${encodeURIComponent(home.id)}/roles/${program.role}`, {
      mode: 'create',
      name: manager
    });
    if (index === 0 && song) await linkSong(call, song, 'in-place-song-0');
    if (index > 0) {
      await call('PATCH', `/api/agents/${encodeURIComponent(manager)}`, {
        system_prompt: EDITED_PROMPT,
        confirm_shared_edit: true
      });
    }
    seeded.push({ id: home.id, name, slug: home.folder_slug, manager });
  }
  return { sandbox: real, homePackage: homeRoot, songPackage: songRoot, homes: seeded, song };
}

function argument(args, name) {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
}

async function main(argv) {
  const [command, ...args] = argv;
  const sandbox = argument(args, '--sandbox');
  if (command === 'seed') {
    const result = await seed({ baseURL: argument(args, '--base-url'), sandbox });
    console.log(JSON.stringify(result, null, 2));
  } else if (command === 'release') {
    const version = argument(args, '--version') || NEW_VERSION;
    const root = await writeHomeRelease(sandbox, version);
    console.log(JSON.stringify({ home_package: root, version }, null, 2));
  } else {
    throw new Error('usage: in-place-upgrade.mjs seed --base-url URL --sandbox DIR | release --sandbox DIR [--version V]');
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] || '').href) {
  main(process.argv.slice(2)).catch(error => {
    console.error(error.message);
    process.exit(1);
  });
}
