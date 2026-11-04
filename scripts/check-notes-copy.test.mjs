import { test } from 'node:test';
import assert from 'node:assert/strict';

import { evaluate, goStringLiterals, scanGo, scanScript } from './check-notes-copy.mjs';

const texts = hits => hits.map(hit => hit.text);

test('a browser string that says second brain fails, in any case or spacing', () => {
  assert.deepEqual(texts(scanScript("el.textContent = 'Set up your second brain';")), [
    'Set up your second brain'
  ]);
  assert.deepEqual(texts(scanScript('const t = `Second  Brain: ${name}`;')), ['Second Brain:']);
});

test('browser identifiers, paths, comments and console lines pass', () => {
  const clean = [
    "const secondBrain = fetch('/api/personal-assistant/second-brain');",
    '// the second brain card',
    '/* second brain */',
    "console.warn('second brain status failed');",
    "const key = 'second_brain_week';"
  ];
  for (const source of clean) assert.deepEqual(scanScript(source), [], source);
});

test('Go literals are read, comments and log lines skipped', () => {
  const source = [
    'package x',
    '// Your second brain is here.',
    '/* second brain */',
    'const title = "Connect your second brain"',
    'var raw = `This is your second brain`',
    'func f() { logger.Info("Filled in the second brain", nil) }',
    "var r = '\"'",
    'var ok = "Set up your notes"'
  ].join('\n');
  assert.deepEqual(scanGo(source), [
    { line: 4, text: 'Connect your second brain' },
    { line: 5, text: 'This is your second brain' }
  ]);
});

test('Go string escapes stay inside their literal', () => {
  const literals = goStringLiterals('x := "a \\"second\\" brain"; y := "b"');
  assert.deepEqual(
    literals.map(literal => literal.text),
    ['a "second" brain', 'b']
  );
});

test('the allowlist only shrinks: an entry that no longer matches fails', () => {
  const allowlist = [{ file: 'a.go', text: 'Second Brain', reason: 'folder name' }];
  assert.deepEqual(
    evaluate(new Map([['a.go', [{ line: 1, text: 'Second Brain' }]]]), allowlist),
    []
  );
  assert.deepEqual(evaluate(new Map(), allowlist), [
    'a.go no longer says "Second Brain". Remove it from the allowlist.'
  ]);
  assert.deepEqual(evaluate(new Map([['b.js', [{ line: 9, text: 'your second brain' }]]]), []), [
    'b.js:9: your second brain'
  ]);
});
