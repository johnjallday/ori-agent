#!/usr/bin/env node
// check-user-facing-task-copy.mjs — the word "task" stays out of what people read
// (PRD 760 FR-3). Work is a ticket, and one attempt at it is a run.
//
// It reads visible template text, user-facing attributes and the display strings
// in browser scripts, rather than searching for the word. So `task_id`, `/tasks/`,
// `.task-card`, `type === 'task'`, console output and comments all pass, while
// "Create task" fails wherever it can reach a person.
//
// A file that may still say "task" is named in scripts/task-copy-allowlist.json
// with its reason. An entry may list `texts`, the start of each allowed line, so
// the rest of that file stays checked. The list only shrinks: an entry or text
// that no longer matches fails, and so does going over ALLOWLIST_CEILING.

import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

// Lowered as surfaces convert. Raising it is a decision a reviewer should see.
export const ALLOWLIST_CEILING = 6;

const REPO = join(fileURLToPath(new URL('.', import.meta.url)), '..');
const TEMPLATE_ROOT = 'internal/web/templates';
const SCRIPT_ROOT = 'internal/web/static/js';
const ALLOWLIST_PATH = 'scripts/task-copy-allowlist.json';
const SKIP_DIRS = new Set(['vendor', 'node_modules']);

// The word on its own, or as "subtask": not part of an identifier, a path, a
// class, a key or a query string.
const TASK_WORD = /(^|[^\w\-./:#$@=?&])((?:sub)?tasks?)(?![\w\-/=&]|[:.][\w\u0000])/gi;

const USER_FACING_ATTRIBUTES =
  /\s(?:title|placeholder|aria-label|aria-description|alt|label|data-bs-title|data-tooltip|data-title)\s*=\s*("[^"]*"|'[^']*')/gi;

// A literal in one of these calls never reaches a person.
const CODE_CONTEXT =
  /(?:console\.\w+|logger\.\w+|fetch|import|require|querySelector(?:All)?|getElementById|getElementsByClassName|closest|matches|classList\.\w+|addEventListener|removeEventListener|dispatchEvent|CustomEvent|localStorage\.\w+|sessionStorage\.\w+|startsWith|endsWith|includes|URLSearchParams|searchParams\.\w+|createElement|getAttribute|hasAttribute|removeAttribute|new RegExp|encodeURIComponent)\s*\(\s*$/;
const ASSIGNED_TO_CODE =
  /(?:\.className|\.id|\.href|\.src|\.name|\.type|\.dataset\.\w+|\[['"][\w-]+['"]\])\s*\+?=\s*$/;
const COMPARED = /(?:===|!==|==|!=)\s*$/;
const NON_COPY_ATTRIBUTE =
  /\.setAttribute\(\s*['"](?!title|placeholder|aria-label|aria-description|alt|label)[\w-]+['"]\s*,\s*$/;

function lineOf(source, index) {
  let line = 1;
  for (let i = 0; i < index; i += 1) if (source.charCodeAt(i) === 10) line += 1;
  return line;
}

function hasTaskWord(text) {
  TASK_WORD.lastIndex = 0;
  return TASK_WORD.test(text);
}

// isCopy says whether plain text reads as words for a person: a lone lowercase
// `task` is a value; `Task`, `Tasks` and anything in a sentence are copy.
function isCopy(text) {
  const trimmed = text.trim();
  if (!hasTaskWord(trimmed)) return false;
  if (/^(?:(?:sub)?tasks?)$/.test(trimmed)) return false;
  if (/^[\w.\-/:#?=&]+$/.test(trimmed) && !/^(?:Sub)?[Tt]asks?$/.test(trimmed)) return false;
  return true;
}

function clip(text) {
  const flat = text.replaceAll(HOLE, ' ').replace(/\s+/g, ' ').trim();
  return flat.length > 90 ? `${flat.slice(0, 87)}…` : flat;
}

// visibleParts takes HTML and returns what a person can read, with its offset.
function visibleParts(html) {
  const parts = [];
  const tag = /<!--[\s\S]*?-->|<(script|style)\b[^>]*>[\s\S]*?<\/\1\s*>|<[a-zA-Z/!][^<>]*>/g;
  let cursor = 0;
  let match;
  while ((match = tag.exec(html))) {
    if (match.index > cursor) {
      const text = html.slice(cursor, match.index);
      parts.push({ text, index: cursor + (text.length - text.trimStart().length) });
    }
    if (!match[1] && !match[0].startsWith('<!--')) {
      USER_FACING_ATTRIBUTES.lastIndex = 0;
      let attribute;
      while ((attribute = USER_FACING_ATTRIBUTES.exec(match[0]))) {
        parts.push({ text: attribute[1].slice(1, -1), index: match.index + attribute.index });
      }
    }
    cursor = tag.lastIndex;
  }
  if (cursor < html.length) parts.push({ text: html.slice(cursor), index: cursor });
  return parts;
}

const stripTemplateActions = text =>
  text.replace(/\{\{[\s\S]*?\}\}/g, match => ' '.repeat(match.length));

// scanTemplate returns the copy in one Go template, inline scripts included.
export function scanTemplate(source) {
  const hits = [];
  const blanked = stripTemplateActions(source);
  for (const part of visibleParts(blanked)) {
    if (isCopy(part.text)) hits.push({ line: lineOf(source, part.index), text: clip(part.text) });
  }
  const script = /<script\b(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script\s*>/gi;
  let match;
  while ((match = script.exec(source))) {
    const offset = match.index + match[0].indexOf(match[1]);
    for (const hit of scanScript(stripTemplateActions(match[1]))) {
      hits.push({ line: lineOf(source, offset) + hit.line - 1, text: hit.text });
    }
  }
  return hits;
}

// What a template literal's ${…} reads as while it is checked.
const HOLE = '\u0000';

const REGEX_MAY_FOLLOW = /(?:^|[=(,:;!&|?{}[\n+\-*%<>~^]|\breturn|\btypeof|\bcase|\bin|\bof)\s*$/;

// stringLiterals walks a script and yields each string and template chunk with
// the code just before it. Comments and regular expressions are skipped.
export function stringLiterals(source) {
  const found = [];
  let code = '';
  let i = 0;
  const push = (text, index) => found.push({ text, index, before: code.slice(-90) });

  const readQuoted = quote => {
    const start = i;
    i += 1;
    let text = '';
    while (i < source.length && source[i] !== quote && source[i] !== '\n') {
      if (source[i] === '\\') {
        i += 1;
        text += source[i] === 'n' ? '\n' : source[i];
      } else {
        text += source[i];
      }
      i += 1;
    }
    i += 1;
    push(text, start);
    // A short name stays readable, so the next literal can see `setAttribute('x',`.
    code += /^[\w-]+$/.test(text) ? `'${text}'` : "''";
  };

  // A template literal is one chunk, with its holes blanked; code inside a hole
  // is walked too, because it often holds more copy.
  const readTemplate = () => {
    const start = i;
    const before = code.slice(-90);
    i += 1;
    let text = '';
    while (i < source.length && source[i] !== '`') {
      if (source[i] === '\\') {
        text += source[i + 1] || '';
        i += 2;
      } else if (source[i] === '$' && source[i + 1] === '{') {
        i += 2;
        text += HOLE;
        walk(true);
      } else {
        text += source[i];
        i += 1;
      }
    }
    i += 1;
    found.push({ text, index: start, before });
    code += '``';
  };

  function walk(insideHole) {
    let depth = 0;
    while (i < source.length) {
      const c = source[i];
      const next = source[i + 1];
      if (c === '/' && next === '/') {
        while (i < source.length && source[i] !== '\n') i += 1;
      } else if (c === '/' && next === '*') {
        const end = source.indexOf('*/', i + 2);
        i = end < 0 ? source.length : end + 2;
      } else if (c === '/' && REGEX_MAY_FOLLOW.test(code.slice(-12))) {
        i += 1;
        let inClass = false;
        while (i < source.length && source[i] !== '\n' && (inClass || source[i] !== '/')) {
          if (source[i] === '\\') i += 1;
          else if (source[i] === '[') inClass = true;
          else if (source[i] === ']') inClass = false;
          i += 1;
        }
        i += 1;
        code += 'r';
      } else if (c === '"' || c === "'") {
        readQuoted(c);
      } else if (c === '`') {
        readTemplate();
      } else {
        if (insideHole) {
          if (c === '{') depth += 1;
          if (c === '}') {
            if (depth === 0) {
              i += 1;
              return;
            }
            depth -= 1;
          }
        }
        code += c;
        i += 1;
      }
    }
  }

  walk(false);
  return found;
}

function inCodeContext(before) {
  return (
    CODE_CONTEXT.test(before) ||
    ASSIGNED_TO_CODE.test(before) ||
    COMPARED.test(before) ||
    NON_COPY_ATTRIBUTE.test(before)
  );
}

// scanScript returns the display strings in one browser script.
export function scanScript(source) {
  const hits = [];
  for (const literal of stringLiterals(source)) {
    if (inCodeContext(literal.before)) continue;
    const parts = /<[a-zA-Z/!]/.test(literal.text) ? visibleParts(literal.text) : [literal];
    for (const part of parts) {
      if (isCopy(part.text)) {
        hits.push({ line: lineOf(source, literal.index), text: clip(part.text) });
      }
    }
  }
  return hits;
}

function walkFiles(root, keep, out = []) {
  if (!existsSync(root)) return out;
  for (const name of readdirSync(root)) {
    const path = join(root, name);
    if (statSync(path).isDirectory()) {
      if (!SKIP_DIRS.has(name)) walkFiles(path, keep, out);
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
  for (const path of walkFiles(join(repo, TEMPLATE_ROOT), name => name.endsWith('.tmpl'))) {
    record(path, scanTemplate(readFileSync(path, 'utf8')));
  }
  const isBrowserScript = name => name.endsWith('.js') && !name.endsWith('.test.js');
  for (const path of walkFiles(join(repo, SCRIPT_ROOT), isBrowserScript)) {
    record(path, scanScript(readFileSync(path, 'utf8')));
  }
  return hits;
}

// evaluate compares the hits with the allowlist and returns every problem.
export function evaluate(hits, allowlist, ceiling = ALLOWLIST_CEILING) {
  const problems = [];
  const entries = Array.isArray(allowlist?.entries) ? allowlist.entries : [];
  if (entries.length > ceiling) {
    problems.push(
      `The allowlist has ${entries.length} entries and may have at most ${ceiling}. Change the copy instead of adding one.`
    );
  }
  const allowed = new Map();
  for (const entry of entries) {
    if (!entry?.file || !String(entry.reason || '').trim()) {
      problems.push(`Allowlist entry needs a file and a reason: ${JSON.stringify(entry)}`);
      continue;
    }
    const texts = Array.isArray(entry.texts) ? entry.texts.map(String) : null;
    allowed.set(entry.file, texts);
    const found = hits.get(entry.file) || [];
    if (!found.length) {
      problems.push(`${entry.file} no longer says "task". Remove it from the allowlist.`);
      continue;
    }
    for (const text of texts || []) {
      if (!found.some(hit => hit.text.startsWith(text))) {
        problems.push(`${entry.file} no longer says "${text}". Remove it from the allowlist.`);
      }
    }
  }
  for (const [file, found] of [...hits].sort(([a], [b]) => a.localeCompare(b))) {
    const texts = allowed.get(file);
    if (texts === null) continue;
    for (const hit of found) {
      if (texts && texts.some(text => hit.text.startsWith(text))) continue;
      problems.push(`${file}:${hit.line}: ${hit.text}`);
    }
  }
  return problems;
}

function main() {
  const allowlist = JSON.parse(readFileSync(join(REPO, ALLOWLIST_PATH), 'utf8'));
  const problems = evaluate(scanRepository(), allowlist);
  if (problems.length) {
    console.error(`User-facing copy says "task" (${problems.length}). It is a ticket or a run:`);
    for (const problem of problems) console.error(`  ${problem}`);
    process.exit(1);
  }
  console.log('User-facing copy check passed.');
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main();
