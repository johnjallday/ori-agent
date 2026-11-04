#!/usr/bin/env node
// check-notes-copy.mjs — people read "notes", never "second brain" (Issue 776 FR26).
//
// The folder Ori sets up is called Notes on every surface. "Second Brain" survives
// only as the default folder name and inside that folder's own README, which names
// the folder. Code identifiers (`secondBrain`, `second-brain`, `second_brain`) do not
// match, because the check looks for the two words with a space between them.
//
// It reads the string literals of the browser modules and of the Go packages that
// write the drawer's, the missions' and the notes' sentences. Comments and log lines
// pass. A literal that may still say it is listed in ALLOWLIST with its reason; an
// entry that no longer matches fails, so the list only shrinks.

import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { stringLiterals } from './check-user-facing-task-copy.mjs';

const REPO = join(fileURLToPath(new URL('.', import.meta.url)), '..');

export const JS_ROOTS = ['internal/web/static/js/modules'];
export const GO_ROOTS = [
  'internal/progression',
  'internal/personalassistant',
  'internal/secondbrain'
];

const PHRASE = /second\s+brain/i;

// Each entry names a file and the start of the one literal that may keep the words.
export const ALLOWLIST = [
  {
    file: 'internal/web/static/js/modules/personal-assistant-second-brain.js',
    text: 'Second Brain',
    reason: 'The default folder name; the folder keeps its name.'
  },
  {
    file: 'internal/secondbrain/creator.go',
    text: 'This is your second brain:',
    reason: "The folder's own root README names the folder."
  }
];

// A literal passed straight to one of these never reaches a person.
const JS_CODE_CONTEXT = /(?:console\.\w+|logger\.\w+)\s*\(\s*$/;
const GO_CODE_CONTEXT = /(?:logger|log|slog)\.\w+\(\s*$/;

function lineOf(source, index) {
  let line = 1;
  for (let i = 0; i < index; i += 1) if (source.charCodeAt(i) === 10) line += 1;
  return line;
}

function clip(text) {
  const flat = text.replaceAll('\u0000', ' ').replace(/\s+/g, ' ').trim();
  return flat.length > 90 ? `${flat.slice(0, 87)}…` : flat;
}

// goStringLiterals yields every interpreted ("…") and raw (`…`) string in a Go
// source, with the code just before it. Comments and rune literals are skipped.
export function goStringLiterals(source) {
  const found = [];
  let code = '';
  let i = 0;
  while (i < source.length) {
    const c = source[i];
    const next = source[i + 1];
    if (c === '/' && next === '/') {
      while (i < source.length && source[i] !== '\n') i += 1;
    } else if (c === '/' && next === '*') {
      const end = source.indexOf('*/', i + 2);
      i = end < 0 ? source.length : end + 2;
    } else if (c === "'") {
      i += 1;
      while (i < source.length && source[i] !== "'" && source[i] !== '\n') {
        if (source[i] === '\\') i += 1;
        i += 1;
      }
      i += 1;
      code += "''";
    } else if (c === '"') {
      const start = i;
      i += 1;
      let text = '';
      while (i < source.length && source[i] !== '"' && source[i] !== '\n') {
        if (source[i] === '\\') {
          i += 1;
          text += source[i] === 'n' ? '\n' : source[i];
        } else {
          text += source[i];
        }
        i += 1;
      }
      i += 1;
      found.push({ text, index: start, before: code.slice(-60) });
      code += '""';
    } else if (c === '`') {
      const start = i;
      const end = source.indexOf('`', i + 1);
      const stop = end < 0 ? source.length : end;
      found.push({ text: source.slice(i + 1, stop), index: start, before: code.slice(-60) });
      i = stop + 1;
      code += '``';
    } else {
      code += c;
      i += 1;
    }
  }
  return found;
}

function scan(literals, source, codeContext) {
  const hits = [];
  for (const literal of literals) {
    if (codeContext.test(literal.before)) continue;
    if (PHRASE.test(literal.text)) {
      hits.push({ line: lineOf(source, literal.index), text: clip(literal.text) });
    }
  }
  return hits;
}

export function scanScript(source) {
  return scan(stringLiterals(source), source, JS_CODE_CONTEXT);
}

export function scanGo(source) {
  return scan(goStringLiterals(source), source, GO_CODE_CONTEXT);
}

function walkFiles(root, keep, out = []) {
  if (!existsSync(root)) return out;
  for (const name of readdirSync(root)) {
    const path = join(root, name);
    if (statSync(path).isDirectory()) {
      if (name !== 'testdata' && name !== 'node_modules') walkFiles(path, keep, out);
    } else if (keep(name)) {
      out.push(path);
    }
  }
  return out;
}

export function scanRepository(repo = REPO) {
  const hits = new Map();
  const record = (path, found) => {
    if (found.length) hits.set(relative(repo, path), found);
  };
  const isModule = name => name.endsWith('.js') && !name.endsWith('.test.js');
  for (const root of JS_ROOTS) {
    for (const path of walkFiles(join(repo, root), isModule)) {
      record(path, scanScript(readFileSync(path, 'utf8')));
    }
  }
  const isGo = name => name.endsWith('.go') && !name.endsWith('_test.go');
  for (const root of GO_ROOTS) {
    for (const path of walkFiles(join(repo, root), isGo)) {
      record(path, scanGo(readFileSync(path, 'utf8')));
    }
  }
  return hits;
}

// evaluate compares the hits with the allowlist and returns every problem.
export function evaluate(hits, allowlist = ALLOWLIST) {
  const problems = [];
  for (const entry of allowlist) {
    const found = hits.get(entry.file) || [];
    if (!found.some(hit => hit.text.startsWith(entry.text))) {
      problems.push(`${entry.file} no longer says "${entry.text}". Remove it from the allowlist.`);
    }
  }
  for (const [file, found] of [...hits].sort(([a], [b]) => a.localeCompare(b))) {
    for (const hit of found) {
      if (allowlist.some(entry => entry.file === file && hit.text.startsWith(entry.text))) continue;
      problems.push(`${file}:${hit.line}: ${hit.text}`);
    }
  }
  return problems;
}

function main() {
  const problems = evaluate(scanRepository());
  if (problems.length) {
    console.error(`User-facing copy says "second brain" (${problems.length}). Say "notes":`);
    for (const problem of problems) console.error(`  ${problem}`);
    process.exit(1);
  }
  console.log('Notes copy check passed.');
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main();
