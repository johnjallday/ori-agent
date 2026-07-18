#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { loadManifest, validateRepository } from './manifest.mjs';

const STATIC_RELEVANT_PATHS = [
  'README.md',
  'docs/readme-screenshots.json',
  'playwright.readme.config.ts',
  'tests/readme-capture.spec.ts',
  'tests/fixtures/readme-scenes.ts',
  'scripts/readme-refresh.sh',
  'scripts/readme/audit.mjs',
  'scripts/readme/capture-driver.mjs',
  'scripts/readme/encode.mjs',
  'scripts/readme/manifest.mjs',
  'scripts/readme/propose.mjs',
  'scripts/readme/report.mjs',
  'internal/web/templates/',
  'internal/web/static/',
];

const RECOMMENDED_ACTION = 'Ask README Steward to refresh the README.';

class AuditError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

function git(root, args, { allowStatus = [] } = {}) {
  const result = spawnSync('git', args, { cwd: root, encoding: 'utf8' });
  if (result.error) throw new AuditError('audit.git_unavailable', `Could not run Git ${args.join(' ')}: ${result.error.message}`);
  if (result.status !== 0 && !allowStatus.includes(result.status)) {
    throw new AuditError('audit.git_failure', `Git ${args.join(' ')} failed: ${(result.stderr || result.stdout || '').trim()}`);
  }
  return { status: result.status, output: result.stdout || '' };
}

function lines(value) {
  return value.split('\n').map((item) => item.trim()).filter(Boolean);
}

function unique(values) {
  return [...new Set(values)].sort();
}

function relevantPaths(manifest) {
  return unique([
    ...STATIC_RELEVANT_PATHS,
    ...manifest.screenshots.flatMap((scene) => scene.owner_paths),
  ]);
}

function pathMatches(candidate, rule) {
  return rule.endsWith('/') ? candidate.startsWith(rule) : candidate === rule;
}

function classifySurface(changedPath, manifest) {
  const owners = manifest.screenshots.filter((scene) => scene.owner_paths.some((owner) => pathMatches(changedPath, owner)));
  if (owners.length > 0) return owners.map((scene) => scene.id);
  if (changedPath.startsWith('internal/web/templates/') || changedPath.startsWith('internal/web/static/')) return ['shared-ui-navigation-layout'];
  if (changedPath === 'README.md' || changedPath === 'docs/readme-screenshots.json') return ['readme-contract'];
  if (changedPath.startsWith('tests/') || changedPath.startsWith('playwright.')) return ['capture-fixtures'];
  return ['capture-tooling'];
}

function acceptanceSelfChanges(manifest) {
  return new Set([
    'README.md',
    'docs/readme-screenshots.json',
    ...manifest.screenshots.map((scene) => scene.output_path),
  ]);
}

function parseCommitLines(value) {
  return lines(value).map((line) => {
    const [sha, subject = ''] = line.split('\t', 2);
    return { sha, subject };
  });
}

function errorReport({ root, code, message, manifest = null }) {
  return {
    audit_version: 1,
    status: 'error',
    acceptance_state: manifest?.acceptance_state ?? null,
    audited_range: null,
    affected_paths: [],
    affected_surfaces: [],
    commits: [],
    working_tree_paths: [],
    validation_errors: [],
    error: { code, message },
    recommended_action: RECOMMENDED_ACTION,
    root: path.resolve(root),
  };
}

export async function auditReadme(root = process.cwd()) {
  const resolvedRoot = path.resolve(root);
  let manifest;
  try {
    ({ manifest } = await loadManifest(resolvedRoot));
  } catch (error) {
    return errorReport({ root: resolvedRoot, code: 'audit.manifest_unreadable', message: error.message });
  }

  if (manifest.acceptance_state !== 'accepted') {
    return errorReport({
      root: resolvedRoot,
      manifest,
      code: 'audit.bootstrap',
      message: 'No accepted README capture exists yet; complete the documented bootstrap refresh before scheduling monthly audits.',
    });
  }

  try {
    if (git(resolvedRoot, ['rev-parse', '--is-shallow-repository']).output.trim() === 'true') {
      throw new AuditError('audit.shallow_history', 'Git history is shallow; fetch the accepted capture commit before auditing README drift.');
    }
    const captureCommit = manifest.last_accepted_capture_commit;
    if (git(resolvedRoot, ['cat-file', '-e', `${captureCommit}^{commit}`], { allowStatus: [1, 128] }).status !== 0) {
      throw new AuditError('audit.missing_capture_commit', `Accepted capture commit ${captureCommit} is unavailable in this repository.`);
    }
    if (git(resolvedRoot, ['merge-base', '--is-ancestor', captureCommit, 'HEAD'], { allowStatus: [1] }).status !== 0) {
      throw new AuditError('audit.capture_commit_not_ancestor', `Accepted capture commit ${captureCommit} is not an ancestor of HEAD.`);
    }

    const validation = await validateRepository(resolvedRoot);
    const trackedRelevantPaths = relevantPaths(manifest);
    const committedPaths = lines(git(resolvedRoot, ['diff', '--name-only', `${captureCommit}..HEAD`, '--']).output);
    const unstagedPaths = lines(git(resolvedRoot, ['diff', '--name-only', '--']).output);
    const stagedPaths = lines(git(resolvedRoot, ['diff', '--cached', '--name-only', '--']).output);
    const workingTreePaths = unique([...unstagedPaths, ...stagedPaths]);
    const selfChanges = validation.errors.length === 0 ? acceptanceSelfChanges(manifest) : new Set();
    const changedPaths = unique([...committedPaths, ...workingTreePaths])
      .filter((changedPath) => trackedRelevantPaths.some((rule) => pathMatches(changedPath, rule)))
      .filter((changedPath) => !selfChanges.has(changedPath));
    const affectedSurfaces = unique(changedPaths.flatMap((changedPath) => classifySurface(changedPath, manifest)));
    const commits = changedPaths.length === 0
      ? []
      : parseCommitLines(git(resolvedRoot, ['log', '--format=%H%x09%s', `${captureCommit}..HEAD`, '--', ...changedPaths]).output);
    const hasDrift = changedPaths.length > 0 || validation.errors.length > 0;

    return {
      audit_version: 1,
      status: hasDrift ? 'drift' : 'clean',
      acceptance_state: manifest.acceptance_state,
      audited_range: { from: captureCommit, to: git(resolvedRoot, ['rev-parse', 'HEAD']).output.trim() },
      affected_paths: changedPaths,
      affected_surfaces: affectedSurfaces,
      commits,
      working_tree_paths: workingTreePaths.filter((changedPath) => changedPaths.includes(changedPath)),
      validation_errors: validation.errors,
      error: null,
      recommended_action: hasDrift ? RECOMMENDED_ACTION : null,
      root: resolvedRoot,
    };
  } catch (error) {
    const code = error instanceof AuditError ? error.code : 'audit.unexpected_failure';
    return errorReport({ root: resolvedRoot, manifest, code, message: error.message });
  }
}

function parseArgs(argv) {
  if (argv.length === 0) return { root: process.cwd() };
  if (argv.length === 2 && argv[0] === '--root') return { root: argv[1] };
  throw new Error('Usage: scripts/readme/audit.mjs [--root REPOSITORY_ROOT]');
}

async function main() {
  const { root } = parseArgs(process.argv.slice(2));
  const report = await auditReadme(root);
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  if (report.status === 'error') process.exitCode = 2;
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    process.stderr.write(`README audit failed: ${error.message}\n`);
    process.exitCode = 2;
  });
}
