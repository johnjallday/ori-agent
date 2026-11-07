import test from 'node:test';
import assert from 'node:assert/strict';
import {
  LibraryFiles,
  boundedPreview,
  safeFileMarkdown,
  fileFailure,
  FILE_TEXT_LIMIT
} from './project-library-files.js';

const deferred = () => {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
};

test('Files loads one directory only on demand, never recursively prefetches', async () => {
  const calls = [];
  const state = new LibraryFiles({
    request: async path => {
      calls.push(path);
      return { path, entries: [{ name: 'Nested', path: 'Nested', kind: 'folder' }] };
    }
  });
  assert.deepEqual(calls, []);
  await state.load();
  assert.deepEqual(calls, ['']);
  await state.toggle('Nested');
  assert.deepEqual(calls, ['', 'Nested']);
  await state.toggle('Nested');
  assert.deepEqual(calls, ['', 'Nested']);
});

test('switching project aborts listing and clears all previews, late results are ignored', async () => {
  const waiting = deferred();
  const controller = new AbortController();
  let changes = 0;
  const state = new LibraryFiles({
    signal: controller.signal,
    request: () => waiting.promise,
    changed: () => changes++
  });
  const work = state.load();
  controller.abort();
  waiting.resolve({ entries: [{ name: 'private' }] });
  await work;
  assert.equal(state.folders.size, 0);
  assert.equal(state.preview, null);
  assert.equal(changes, 1);
});

test('late file A cannot replace B and link rows never request previews', async () => {
  const a = deferred();
  let calls = 0;
  const state = new LibraryFiles({
    preview: async entry => {
      calls++;
      return entry.name === 'A' ? a.promise : { kind: 'text', text: 'B' };
    }
  });
  const old = state.select({ kind: 'file', name: 'A', path: 'A' });
  await state.select({ kind: 'file', name: 'B', path: 'B' });
  a.resolve({ kind: 'text', text: 'A' });
  await old;
  await state.select({ kind: 'link', name: 'outside' });
  assert.equal(state.preview.text, 'B');
  assert.equal(calls, 2);
});

test('lost permission clears source names and preview bytes; partial listings remain explicit', async () => {
  const state = new LibraryFiles({
    request: async () => ({ entries: [], truncated: true, reason: 'entry_limit' }),
    preview: async () => {
      throw { code: 'denied' };
    }
  });
  await state.load();
  assert.equal(state.folders.get('').truncated, true);
  await state.select({ kind: 'file', name: 'private', path: 'private' });
  assert.equal(state.folders.size, 0);
  assert.equal(state.preview.status, 'error');
  assert.match(state.preview.error, /not a file-read grant/);
  assert.match(fileFailure({ code: 'oversized' }), /512 KiB/);
});

test('bounded previews check header, stream bytes and MIME; HTML remains literal text', async () => {
  const html = '<script>execute()</script>';
  const preview = await boundedPreview(
    new Response(html, { headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
  );
  assert.deepEqual(preview, { kind: 'text', text: html });
  await assert.rejects(
    boundedPreview(new Response(html, { headers: { 'Content-Type': 'text/html' } })),
    /Unsupported/
  );
  await assert.rejects(
    boundedPreview(
      new Response('x', {
        headers: { 'Content-Type': 'text/plain', 'Content-Length': String(FILE_TEXT_LIMIT + 1) }
      })
    ),
    /too large/
  );
  await assert.rejects(
    boundedPreview(
      new Response('x'.repeat(FILE_TEXT_LIMIT + 1), { headers: { 'Content-Type': 'text/plain' } })
    ),
    /too large/
  );
  const exact = await boundedPreview(
    new Response('x'.repeat(FILE_TEXT_LIMIT), { headers: { 'Content-Type': 'text/plain' } })
  );
  assert.equal(exact.text.length, FILE_TEXT_LIMIT);
});

test('Markdown without the sanitizer falls back to escaped text, never active marked output', t => {
  const prior = globalThis.window;
  t.after(() => {
    globalThis.window = prior;
  });
  globalThis.window = { marked: { parse: () => '<script>unsafe()</script>' } };
  assert.equal(safeFileMarkdown('[click](javascript:alert(1))'), null);
  let options;
  globalThis.window.DOMPurify = {
    sanitize: (_html, config) => {
      options = config;
      return 'safe';
    }
  };
  assert.equal(safeFileMarkdown('<img src="https://private">'), 'safe');
  assert.ok(options.FORBID_TAGS.includes('img'));
  assert.ok(options.FORBID_ATTR.includes('href'));
});
