import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

const script = resolve('scripts/e2e-fresh.sh');

function fixture(t) {
  const root = mkdtempSync(join(realpathSync(tmpdir()), 'ori-e2e-contract-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const repo = join(root, 'repo');
  const temp = join(root, 'temp');
  const bin = join(root, 'commands');
  for (const dir of [temp, bin, join(repo, 'bin'), join(repo, 'scripts')])
    mkdirSync(dir, { recursive: true });
  const executable = (path, body) => writeFileSync(path, `#!/bin/bash\n${body}\n`, { mode: 0o700 });
  executable(join(bin, 'git'), 'printf "%s\\n" "$E2E_TEST_REPO"');
  executable(join(bin, 'go'), 'exit 0'); // Never build or launch Ori in this shell contract.
  executable(join(bin, 'lsof'), 'exit 1'); // The fake server binds no socket.
  executable(
    join(repo, 'bin/ori-agent'),
    'trap "exit 0" TERM INT; printf "%s:%s:%s\\n" "$$" "$HOME" "$PWD" >> "$HOME/runs"; while :; do sleep 0.1; done'
  );
  executable(
    join(repo, 'scripts/e2e.sh'),
    'for arg in "$@"; do case "$arg" in E2E_TEST_TARGET=*) target="${arg#*=}";; esac; done; for i in {1..100}; do [[ -f "$target/runs" ]] && break; sleep 0.01; done; sleep 0.15; echo "  1 passed"'
  );
  const owned = mkdtempSync(join(temp, 'ori-e2e.'));
  writeFileSync(join(owned, '.ori-demo-owner'), `worktree=${repo}\n`, { mode: 0o600 });
  const run = (...args) =>
    spawnSync(
      'bash',
      [script, '--tail', '0', '--sandbox-env', 'E2E_TEST_TARGET', ...args, 'tests/fake.spec.ts'],
      {
        cwd: repo,
        encoding: 'utf8',
        timeout: 60_000,
        env: {
          ...process.env,
          PATH: `${bin}:${process.env.PATH}`,
          TMPDIR: temp,
          E2E_TEST_REPO: repo
        }
      }
    );
  return { root, repo, temp, owned, run };
}

test('explicit reuse starts and stops a new server in the same owned HOME', t => {
  const { owned, run } = fixture(t);
  for (let i = 0; i < 2; i++) {
    const result = run('--sandbox', owned);
    assert.equal(result.status, 0, result.stdout + result.stderr);
  }
  const runs = readFileSync(join(owned, 'runs'), 'utf8').trim().split('\n');
  assert.equal(runs.length, 2);
  const pids = runs.map(row => {
    const [pid, home, cwd] = row.split(':');
    assert.equal(home, owned);
    assert.equal(cwd, owned);
    assert.throws(() => process.kill(Number(pid), 0), { code: 'ESRCH' });
    return pid;
  });
  assert.notEqual(pids[0], pids[1]);
});

test('default fresh sandboxes gain a canonical worktree ownership marker', t => {
  const { repo, temp, run } = fixture(t);
  const result = run();
  assert.equal(result.status, 0, result.stdout + result.stderr);
  const sandbox = result.stdout.match(/^   sandbox (.+)$/m)?.[1];
  assert.ok(sandbox?.startsWith(`${temp}/ori-e2e.`));
  assert.equal(readFileSync(join(sandbox, '.ori-demo-owner'), 'utf8'), `worktree=${repo}\n`);
});

test('reuse refuses foreign, unmarked, linked-marker and non-temporary roots', t => {
  const { root, repo, temp, owned, run } = fixture(t);
  for (const marker of ['', 'worktree=/another/worktree\n']) {
    writeFileSync(join(owned, '.ori-demo-owner'), marker);
    const result = run('--sandbox', owned);
    assert.equal(result.status, 2, result.stdout + result.stderr);
  }
  const foreign = join(root, 'outside');
  mkdirSync(foreign);
  writeFileSync(join(foreign, '.ori-demo-owner'), `worktree=${repo}\n`);
  assert.equal(run('--sandbox', foreign).status, 2);
  rmSync(join(owned, '.ori-demo-owner'));
  symlinkSync(join(foreign, '.ori-demo-owner'), join(owned, '.ori-demo-owner'));
  assert.equal(run('--sandbox', owned).status, 2);
  const unmarked = mkdtempSync(join(temp, 'ori-e2e.'));
  assert.equal(run('--sandbox', unmarked).status, 2);
  assert.equal(run('--sandbox', owned, 'second.spec.ts').status, 2);
});
