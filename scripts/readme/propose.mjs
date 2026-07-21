#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { loadManifest, validateReadmeContent } from './manifest.mjs';

const RUN_PARENT = path.join('test-results', 'readme-refresh');

function fail(message) {
  throw new Error(message);
}

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!key?.startsWith('--') || value === undefined) fail('Expected --key value arguments.');
    values[key.slice(2)] = value;
  }
  return values;
}

function assertRunID(value) {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(value || '') || value.includes('..')) {
    fail(`Unsafe run ID: ${JSON.stringify(value)}`);
  }
  return value;
}

function runDirectory(root, runID) {
  const parent = path.resolve(root, RUN_PARENT);
  const directory = path.resolve(parent, assertRunID(runID));
  if (path.dirname(directory) !== parent || !existsSync(directory)) {
    fail(`No staged README run exists for ${runID}.`);
  }
  return directory;
}

function hash(text) {
  return createHash('sha256').update(text).digest('hex');
}

function imageTag(scene) {
  return `<img src="${scene.output_path}" alt="${scene.alt_text}" width="${scene.display_width}" />`;
}

function centeredImage(scene) {
  return `<p align="center">\n  ${imageTag(scene)}\n</p>\n<p align="center"><em>${scene.caption}</em></p>`;
}

function replaceExactlyOnce(source, expression, replacement, label) {
  const matches = source.match(expression) || [];
  if (matches.length !== 1) fail(`Expected exactly one ${label} block in README.md, found ${matches.length}.`);
  return source.replace(expression, replacement);
}

export function renderProposedREADME(current, manifest) {
  const scenes = new Map(manifest.screenshots.map((scene) => [scene.id, scene]));
  const hero = scenes.get('hero');
  const actionCenter = scenes.get('action-center');
  const workspaceMap = scenes.get('workspace-map');
  const workspace = scenes.get('workspace');
  if (!hero || !actionCenter || !workspaceMap || !workspace) fail('Manifest does not contain the required four screenshot scenes.');

  let proposed = current;
  proposed = replaceExactlyOnce(
    proposed,
    /<p align="center">\s*<img src="docs\/images\/hero\.(?:png|webp)"[^>]*\/>\s*<\/p>(?:\s*<p align="center"><em>[\s\S]*?<\/em><\/p>)?/,
    centeredImage(hero),
    'hero screenshot',
  );
  proposed = replaceExactlyOnce(
    proposed,
    /<p align="center">\s*<img src="docs\/images\/action-center\.(?:png|webp)"[^>]*\/>\s*<\/p>(?:\s*<p align="center"><em>[\s\S]*?<\/em><\/p>)?/,
    centeredImage(actionCenter),
    'Action Center screenshot',
  );
  proposed = replaceExactlyOnce(
    proposed,
    /\| (?:Onboarding|Workspace Map) \| (?:Workspace|Workspace Command) \|\n\| :---: \| :---: \|\n\| <img src="docs\/images\/(?:onboarding|workspace-map)\.(?:png|webp)"[^>]*\/> \| <img src="docs\/images\/workspace\.(?:png|webp)"[^>]*\/> \|(?:\n\| <sub>[^<]*<\/sub> \| <sub>[^<]*<\/sub> \|)?/,
    `| Workspace Map | Workspace Command |\n| :---: | :---: |\n| ${imageTag(workspaceMap)} | ${imageTag(workspace)} |\n| <sub>${workspaceMap.caption}</sub> | <sub>${workspace.caption}</sub> |`,
    'paired screenshot table',
  );
  return proposed;
}

export function auditCandidate({ root, manifest, current, proposed }) {
  const required = [];
  if (current !== proposed) {
    required.push({
      kind: 'screenshot_portfolio',
      reason: 'Replace retired PNG product imagery with the four manifest-governed WebP scenes, including Workspace Map instead of Onboarding.',
    });
  }
  const expectedSections = ['How it works', 'What you can do', 'Screenshots', 'Quick start', 'Providers', 'Documentation'];
  const missingSections = expectedSections.filter((section) => !new RegExp(`^#{2,3} .*${section}`, 'im').test(proposed));
  const evidencePaths = [
    'docs/INSTALLATION_MACOS.md',
    'docs/INSTALLATION_LINUX.md',
    'docs/INSTALLATION_WINDOWS.md',
    'docs/api/API_REFERENCE.md',
    'internal/llm/README.md',
    'docs/README.md',
    ...manifest.screenshots.flatMap((scene) => scene.owner_paths),
  ];
  const missingEvidence = evidencePaths.filter((relativePath) => !existsSync(path.join(root, relativePath)));
  const contentChecks = [
    {
      id: 'introduction',
      evidence_paths: ['internal/workspace/mission.go', 'internal/workspace/opportunities.go'],
      required_text: ['local-first', 'AI headquarters', 'Personal HQ', 'Action Center'],
    },
    {
      id: 'how_it_works',
      evidence_paths: ['internal/workspace/mission.go', 'internal/workspace/scheduler.go', 'internal/workspace/opportunities.go'],
      required_text: ['Compose a workspace', 'Set a mission + autonomy policy', 'It runs on a schedule', 'Triage in the Action Center'],
    },
    {
      id: 'feature_list',
      evidence_paths: ['internal/workspace/mission.go', 'internal/skills/manager.go', 'internal/mcp/registry.go'],
      required_text: ['Missions & Action Center', 'Workspaces', 'Multiple agents', 'MCP servers & Skills', 'Workspace memory'],
    },
    {
      id: 'screenshots',
      evidence_paths: manifest.screenshots.flatMap((scene) => scene.owner_paths),
      required_text: manifest.screenshots.flatMap((scene) => [scene.output_path, scene.caption, scene.alt_text]),
    },
    {
      id: 'quick_start',
      evidence_paths: ['docs/INSTALLATION_MACOS.md', 'docs/INSTALLATION_LINUX.md', 'docs/INSTALLATION_WINDOWS.md', 'scripts/build-server.sh'],
      required_text: ['Download the DMG', 'Build from source', 'scripts/build-server.sh'],
    },
    {
      id: 'providers',
      evidence_paths: ['internal/llm/README.md'],
      required_text: ['OpenAI', 'Anthropic Claude', 'Google Gemini', 'Ollama', 'LM Studio', 'MLX LM'],
    },
    {
      id: 'documentation_links',
      evidence_paths: ['docs/README.md', 'docs/api/API_REFERENCE.md'],
      required_text: ['docs/INSTALLATION_MACOS.md', 'docs/INSTALLATION_LINUX.md', 'docs/INSTALLATION_WINDOWS.md', 'docs/api/API_REFERENCE.md', 'internal/llm/README.md', 'docs/README.md'],
    },
  ].map((check) => ({
    ...check,
    missing_text: check.required_text.filter((value) => !proposed.includes(value)),
    missing_evidence_paths: check.evidence_paths.filter((relativePath) => !existsSync(path.join(root, relativePath))),
  }));
  const failedContentChecks = contentChecks.filter((check) => check.missing_text.length > 0 || check.missing_evidence_paths.length > 0);
  return {
    audit_version: 1,
    required_changes: required,
    optional_suggestions: [],
    checks: {
      expected_sections: expectedSections,
      missing_sections: missingSections,
      missing_evidence_paths: missingEvidence,
      content: contentChecks,
    },
    status: missingSections.length === 0 && missingEvidence.length === 0 && failedContentChecks.length === 0 ? 'ready' : 'blocked',
  };
}

export async function createProposal({ root, runID }) {
  const runDir = runDirectory(root, runID);
  const { manifest } = await loadManifest(root);
  const current = readFileSync(path.join(root, 'README.md'), 'utf8');
  const proposed = renderProposedREADME(current, manifest);
  const audit = auditCandidate({ root, manifest, current, proposed });
  const referenceErrors = await validateReadmeContent(root, manifest, proposed, {
    virtualExistingPaths: manifest.screenshots.map((scene) => scene.output_path),
  });
  if (referenceErrors.length > 0) {
    audit.status = 'blocked';
    audit.checks.reference_errors = referenceErrors;
  }
  const output = path.join(runDir, 'README.proposed.md');
  const diffOutput = path.join(runDir, 'README.proposed.diff');
  const auditOutput = path.join(runDir, 'README.refresh-audit.json');
  mkdirSync(runDir, { recursive: true });
  writeFileSync(output, proposed);
  const diffResult = spawnSync('git', ['diff', '--no-index', '--', path.join(root, 'README.md'), output], {
    cwd: root,
    encoding: 'utf8',
  });
  if (diffResult.error || (diffResult.status !== 0 && diffResult.status !== 1)) {
    fail(`Could not create staged README diff: ${diffResult.error?.message || diffResult.stderr || 'git diff failed'}`);
  }
  writeFileSync(diffOutput, diffResult.stdout || '');
  audit.proposed_readme_sha256 = hash(proposed);
  writeFileSync(auditOutput, `${JSON.stringify(audit, null, 2)}\n`);
  return {
    run_dir: runDir,
    comparison_report: path.join(runDir, 'comparison', 'README_CAPTURE_REPORT.md'),
    proposed_readme: output,
    diff: diffOutput,
    audit: auditOutput,
    status: audit.status,
    exact_effects_after_approval: {
      update: ['README.md', 'docs/readme-screenshots.json', ...manifest.screenshots.map((scene) => scene.output_path)],
      conditionally_remove_after_repository_reference_check: [
        'docs/images/hero.png',
        'docs/images/action-center.png',
        'docs/images/onboarding.png',
        'docs/images/workspace.png',
      ],
    },
    checkpoint: 'Checkpoint 1: Apply staged refresh?',
  };
}

async function main() {
  const values = parseArgs(process.argv.slice(2));
  const result = await createProposal({ root: process.cwd(), runID: values['run-id'] });
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
  if (result.status !== 'ready') process.exitCode = 2;
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    process.stderr.write(`README proposal error: ${error.message}\n`);
    process.exitCode = 2;
  });
}
