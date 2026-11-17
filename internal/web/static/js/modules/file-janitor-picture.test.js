// Tests for file-janitor-picture.js, the picture of the folder after filing.
// Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/file-janitor-picture.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  BANNED_WORDS,
  LOOSE_LABELS,
  LOOSE_WHY,
  OVER_LIMIT_NOTE,
  earlierLine,
  fileButtonModel,
  folderAriaLabel,
  folderLabel,
  frameTitle,
  leadSentences,
  looseTotal,
  moreLabel,
  pictureAccounting,
  renderPicture,
  trashTickLabel
} from './file-janitor-picture.js';

class El {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.className = '';
    this._text = '';
    this.disabled = false;
    this.checked = false;
    this.indeterminate = false;
  }
  get textContent() {
    return [this._text, ...this.children.map(child => child.textContent)].filter(Boolean).join(' ');
  }
  set textContent(value) {
    this._text = value;
  }
  appendChild(child) {
    this.children.push(child);
    child.parent = this;
    return child;
  }
  setAttribute(key, value) {
    this.attrs[key] = value;
  }
  getAttribute(key) {
    return this.attrs[key];
  }
  addEventListener(event, fn) {
    (this.listeners[event] ||= []).push(fn);
  }
  fire(event) {
    (this.listeners[event] || []).forEach(fn => fn());
  }
  all(predicate, out = []) {
    if (predicate(this)) out.push(this);
    this.children.forEach(child => child.all(predicate, out));
    return out;
  }
}
const doc = { createElement: tag => new El(tag) };

const file = (name, extra = {}) => ({
  id: 'id-' + name,
  name,
  size: 10,
  placement: 'filed',
  ...extra
});
const names = (prefix, n) => Array.from({ length: n }, (_, i) => file(prefix + i));

// What the server sends for a folder with three confident folders ticked on open
// and the question rows still loose.
function pictureFixture(over = {}) {
  return {
    batch_id: 'batch-1',
    revision: 'a'.repeat(64),
    draft_revision: 'b'.repeat(64),
    draft_state: 'default',
    content_read: 'none',
    counts: { candidates: 68, filing: 52, questions: 16, kept: 0, applied: 0, ineligible: 0 },
    waiting: 68,
    folders: [
      {
        category: 'documents',
        destination: 'Filed/Documents',
        group_id: 'g-docs',
        group_state: 'ticked',
        count: 28,
        files: names('doc-', 5),
        unfiled_count: 0,
        unfiled_files: []
      },
      {
        category: 'images',
        destination: 'Filed/Images',
        group_id: 'g-img',
        group_state: 'ticked',
        count: 19,
        files: names('img-', 5),
        unfiled_count: 0,
        unfiled_files: []
      },
      {
        category: 'installers',
        destination: 'Filed/Installers',
        group_id: 'g-inst',
        group_state: 'ticked',
        count: 5,
        files: names('app-', 5),
        unfiled_count: 0,
        unfiled_files: []
      }
    ],
    loose: [
      {
        group_id: 'g-sens',
        key: 'sensitive',
        count: 2,
        placed_count: 0,
        trash_count: 0,
        loose_count: 2,
        singles: names('passport-', 2),
        more: 0
      },
      {
        group_id: 'g-audio',
        key: 'audio',
        count: 14,
        placed_count: 0,
        trash_count: 0,
        loose_count: 14,
        singles: names('track-', 12),
        more: 2
      }
    ],
    left_out: { count: 0, files: [] },
    trash: { count: 0, files: [] },
    could_not_file: { count: 0, files: [] },
    ...over
  };
}

const ctx = (over = {}) => ({ folderName: 'Downloads', editsAvailable: true, more: null, ...over });
const textOf = node => node.textContent;
const find = (node, predicate) => node.all(predicate);

test('the lead line is three short sentences and the third is true for the scan', () => {
  assert.deepEqual(leadSentences('Downloads', 'none'), [
    'This is how Downloads will look.',
    'Nothing moves until you press File.',
    'Ori reads names, types, sizes and dates, never what is inside.'
  ]);
  assert.equal(
    leadSentences('Downloads', 'some')[2],
    'Ori read inside some files to sort them, as you allowed in Settings.'
  );
  // The server cannot say how an old file was sorted, so the sentence is left
  // out rather than guessed.
  assert.equal(leadSentences('Downloads', 'unknown').length, 2);
  assert.equal(frameTitle('Downloads'), 'Downloads, after filing');
});

test('opening draws the lead line, the frame title and ticked folders, and nothing else first', () => {
  const root = renderPicture(doc, pictureFixture(), ctx());
  const children = root.children.map(child => child.className);
  assert.deepEqual(children, ['fj-lead', 'fj-frame']);
  assert.equal(
    textOf(root.children[0]),
    'This is how Downloads will look. Nothing moves until you press File. Ori reads names, types, sizes and dates, never what is inside.'
  );
  assert.equal(textOf(root.children[1].children[0]), 'Downloads, after filing');
  const boxes = find(root, node => node.tagName === 'INPUT');
  assert.equal(boxes.length, 3, 'one tick box per folder, none for a question row');
  for (const box of boxes) {
    assert.equal(box.checked, true);
    assert.equal(box.indeterminate, false);
    assert.equal(box.disabled, false);
  }
  assert.equal(boxes[0].getAttribute('aria-label'), 'Documents, 28 files, will be filed');
});

test('folders keep the order the server sends: the category registry', () => {
  const root = renderPicture(doc, pictureFixture(), ctx());
  const order = find(root, node => node.getAttribute('data-fj-folder') !== undefined).map(card =>
    card.getAttribute('data-fj-folder')
  );
  assert.deepEqual(order, ['documents', 'images', 'installers']);
});

test('a folder card shows its count, five names and "and N more"', () => {
  const root = renderPicture(doc, pictureFixture(), ctx());
  const documents = find(root, node => node.getAttribute('data-fj-folder') === 'documents')[0];
  assert.equal(find(documents, node => node.tagName === 'LI').length, 5);
  assert.match(textOf(documents), /\b28\b/);
  const more = find(documents, node => node.className === 'fj-more')[0];
  assert.equal(textOf(more), 'and 23 more');
});

test('a folder with exactly its listed files has no more control', () => {
  const picture = pictureFixture();
  picture.folders[2].count = 5;
  const root = renderPicture(doc, picture, ctx());
  const installers = find(root, node => node.getAttribute('data-fj-folder') === 'installers')[0];
  assert.equal(find(installers, node => node.className === 'fj-more').length, 0);
});

test('an unticked folder keeps its card, drawn dashed with "Not filed"', () => {
  const picture = pictureFixture();
  picture.folders[1] = {
    ...picture.folders[1],
    group_state: 'unticked',
    count: 0,
    files: [],
    unfiled_count: 19,
    unfiled_files: names('img-', 5)
  };
  const root = renderPicture(doc, picture, ctx());
  const images = find(root, node => node.getAttribute('data-fj-folder') === 'images')[0];
  assert.match(images.className, /is-unticked/);
  assert.match(textOf(images), /Not filed/);
  const box = find(images, node => node.tagName === 'INPUT')[0];
  assert.equal(box.checked, false);
  assert.equal(box.getAttribute('aria-label'), 'Images, 19 files, not filed');
  assert.equal(find(images, node => node.className === 'fj-more')[0].textContent, 'and 14 more');
  // A ticked card is not drawn dashed.
  assert.doesNotMatch(
    find(root, node => node.getAttribute('data-fj-folder') === 'documents')[0].className,
    /is-unticked/
  );
});

test('Still loose counts exactly what its rows list, even when a folder is not ticked', () => {
  const picture = pictureFixture();
  picture.folders[1] = {
    ...picture.folders[1],
    group_state: 'unticked',
    count: 0,
    files: [],
    unfiled_count: 19,
    unfiled_files: names('img-', 5)
  };
  const root = renderPicture(doc, picture, ctx());
  const heading = find(root, node => node.className === 'fj-box-title').map(textOf);
  // 2 sensitive + 14 music, not the 19 images that sit in their own dashed card.
  assert.ok(heading.includes('Still loose in Downloads · 16'), heading.join(' | '));
  const listed = find(root, node => node.className === 'fj-loose-row').reduce(
    (sum, row) => sum + find(row, node => node.tagName === 'LI').length,
    0
  );
  assert.equal(listed, 14, 'twelve audio names + two sensitive names are what the rows draw');
  const card = find(root, node => node.getAttribute('data-fj-folder') === 'images')[0];
  assert.equal(textOf(find(card, node => node.className === 'fj-folder-count')[0]), '19');
  assert.deepEqual(pictureAccounting({ ...picture, waiting: 52 + 16 }), {
    filed: 33,
    notFiled: 19,
    loose: 16,
    trash: 0,
    total: 68
  });
});

test('a folder with some files left out is shown partly ticked', () => {
  const picture = pictureFixture();
  picture.folders[0].group_state = 'partial';
  const root = renderPicture(doc, picture, ctx());
  const box = find(root, node => node.tagName === 'INPUT')[0];
  assert.equal(box.checked, true);
  assert.equal(box.indeterminate, true);
  assert.equal(box.getAttribute('aria-checked'), 'mixed');
});

test('ticking and unticking report the folder and the new state', () => {
  const calls = [];
  const root = renderPicture(
    doc,
    pictureFixture(),
    ctx({ onTickFolder: (folder, ticked) => calls.push([folder.group_id, ticked]) })
  );
  const box = find(root, node => node.tagName === 'INPUT')[1];
  box.checked = false;
  box.fire('change');
  box.checked = true;
  box.fire('change');
  assert.deepEqual(calls, [
    ['g-img', false],
    ['g-img', true]
  ]);
});

test('no control can be used while edits are unavailable', () => {
  const root = renderPicture(doc, pictureFixture(), ctx({ editsAvailable: false }));
  assert.ok(find(root, node => node.tagName === 'INPUT').every(box => box.disabled));
});

test('a folder with no group of its own has no tick box', () => {
  const picture = pictureFixture();
  picture.folders[2].group_id = '';
  picture.folders[2].group_state = 'none';
  const root = renderPicture(doc, picture, ctx());
  assert.equal(find(root, node => node.tagName === 'INPUT').length, 2);
});

test('Still loose has one row per question group with the short label, a count and Why?', () => {
  const root = renderPicture(doc, pictureFixture(), ctx());
  const heading = find(root, node => node.className === 'fj-box-title').map(textOf);
  assert.ok(heading.includes('Still loose in Downloads · 16'));
  const rows = find(root, node => node.className === 'fj-loose-row');
  assert.equal(rows.length, 2);
  assert.match(textOf(rows[0]), /Sensitive-looking names · 2/);
  assert.match(textOf(rows[1]), /Music and audio · 14/);
  const why = find(rows[0], node => node.className === 'fj-why-text')[0];
  assert.equal(textOf(why), LOOSE_WHY.sensitive);
  assert.equal(find(rows[0], node => node.tagName === 'DETAILS').length, 1);
  // The rows are lists only: no menu and no tick for a question group.
  assert.equal(find(rows[0], node => ['INPUT', 'SELECT'].includes(node.tagName)).length, 0);
});

test('a loose row names at most twelve files and says how many more', () => {
  const root = renderPicture(doc, pictureFixture(), ctx());
  const audio = find(root, node => node.getAttribute('data-fj-group') === 'g-audio')[0];
  assert.equal(find(audio, node => node.tagName === 'LI').length, 12);
  assert.equal(find(audio, node => node.className === 'fj-more')[0].textContent, 'and 2 more');
});

test('every question group has a short label and a Why? sentence', () => {
  assert.deepEqual(Object.keys(LOOSE_LABELS).sort(), Object.keys(LOOSE_WHY).sort());
  assert.deepEqual(LOOSE_LABELS, {
    sensitive: 'Sensitive-looking names',
    versions: 'Possible copies',
    audio: 'Music and audio',
    unresolved: 'Unknown type',
    unmatched: 'Other files'
  });
  for (const why of Object.values(LOOSE_WHY)) assert.ok(why.split('. ').length <= 2, why);
});

test('"and N more" and the paged list report the section and key to open', () => {
  const opened = [];
  const root = renderPicture(
    doc,
    pictureFixture(),
    ctx({ onOpenMore: (section, key) => opened.push([section, key]) })
  );
  find(root, node => node.className === 'fj-more')[0].fire('click');
  find(root, node => node.className === 'fj-more')
    .at(-1)
    .fire('click');
  assert.deepEqual(opened, [
    ['filed', 'documents'],
    ['loose', 'g-audio']
  ]);
});

test('the open list shows its page, pages forward and back, and closes', () => {
  const calls = [];
  const more = {
    section: 'filed',
    key: 'documents',
    state: 'loaded',
    offset: 50,
    limit: 50,
    total: 206,
    files: names('doc-', 50)
  };
  const root = renderPicture(
    doc,
    pictureFixture(),
    ctx({
      more,
      onMorePage: offset => calls.push(['page', offset]),
      onCloseMore: () => calls.push(['close'])
    })
  );
  const panel = find(root, node => node.id === 'fileJanitorMorePanel')[0];
  assert.match(textOf(panel), /Showing 51–100 of 206/);
  assert.equal(find(panel, node => node.tagName === 'LI').length, 50);
  assert.equal(panel.getAttribute('tabindex'), '-1');
  const buttons = find(panel, node => node.tagName === 'BUTTON');
  const byLabel = label => buttons.find(button => textOf(button) === label);
  assert.equal(byLabel('Previous files').disabled, false);
  byLabel('Next files').fire('click');
  byLabel('Previous files').fire('click');
  byLabel('Close list').fire('click');
  assert.deepEqual(calls, [['page', 100], ['page', 0], ['close']]);
  // The first page cannot go back; the last cannot go forward.
  const first = renderPicture(doc, pictureFixture(), ctx({ more: { ...more, offset: 0 } }));
  assert.equal(
    find(first, node => textOf(node) === 'Previous files' && node.tagName === 'BUTTON')[0].disabled,
    true
  );
  const last = renderPicture(
    doc,
    pictureFixture(),
    ctx({ more: { ...more, offset: 200, files: names('x-', 6) } })
  );
  assert.equal(
    find(last, node => textOf(node) === 'Next files' && node.tagName === 'BUTTON')[0].disabled,
    true
  );
});

test('a list that could not load says so and offers a retry, never an empty list', () => {
  const more = {
    section: 'filed',
    key: 'documents',
    state: 'error',
    offset: 0,
    limit: 50,
    total: 0,
    files: []
  };
  const root = renderPicture(doc, pictureFixture(), ctx({ more }));
  const panel = find(root, node => node.id === 'fileJanitorMorePanel')[0];
  assert.match(textOf(panel), /could not be loaded/);
  assert.equal(find(panel, node => node.tagName === 'LI').length, 0);
  const loading = renderPicture(
    doc,
    pictureFixture(),
    ctx({ more: { ...more, state: 'loading' } })
  );
  assert.match(textOf(find(loading, node => node.id === 'fileJanitorMorePanel')[0]), /Loading/);
});

test('files left out, Trash and files that could not be filed are drawn only when present', () => {
  const plain = renderPicture(doc, pictureFixture(), ctx());
  assert.equal(find(plain, node => node.className === 'fj-box fj-trash').length, 0);
  assert.equal(find(plain, node => node.className === 'fj-box fj-could-not').length, 0);
  const picture = pictureFixture({
    left_out: { count: 1, files: [file('left.png', { placement: 'left_out' })] },
    trash: { count: 1, files: [file('old.pdf', { placement: 'trash' })] },
    could_not_file: {
      count: 1,
      files: [
        file('stuck.zip', { placement: 'could_not_file', reason: 'The move did not complete.' })
      ]
    }
  });
  const root = renderPicture(doc, picture, ctx());
  assert.match(textOf(root), /Left out by you · 1/);
  assert.match(textOf(find(root, node => node.className === 'fj-box fj-trash')[0]), /Trash · 1/);
  assert.match(
    textOf(find(root, node => node.className === 'fj-box fj-could-not')[0]),
    /Could not be filed · 1 stuck\.zip The move did not complete\./
  );
});

test('a file the person placed is tagged "your choice"', () => {
  const picture = pictureFixture();
  picture.folders[0].files = [file('passport-a.pdf', { your_choice: true }), ...names('doc-', 4)];
  const root = renderPicture(doc, picture, ctx());
  const tags = find(root, node => node.className === 'fj-tag');
  assert.deepEqual(tags.map(textOf), ['your choice']);
});

test('only questions: the Filed box points at the menus; nothing waiting says so', () => {
  const onlyQuestions = renderPicture(doc, pictureFixture({ folders: [] }), ctx());
  assert.match(textOf(onlyQuestions), /Nothing here yet\. Use the menus below to file something\./);
  const nothing = renderPicture(doc, pictureFixture({ folders: [], loose: [] }), ctx());
  assert.doesNotMatch(textOf(nothing), /Still loose/);
});

test('a hostile name is text, never markup, and never reaches innerHTML', () => {
  const picture = pictureFixture();
  picture.folders[0].files = [file('<img src=x onerror=alert(1)>.png'), ...names('doc-', 4)];
  const root = renderPicture(doc, picture, ctx({ folderName: '<b>Downloads</b>' }));
  assert.match(textOf(root), /<img src=x onerror=alert\(1\)>\.png/);
  assert.match(textOf(root), /<b>Downloads<\/b>, after filing/);
  const source = readFileSync(new URL('./file-janitor-picture.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /innerHTML|insertAdjacentHTML|outerHTML|document\.write/);
});

test('the accounting adds up to what is waiting, and the quiet line counts earlier files', () => {
  const picture = pictureFixture();
  assert.deepEqual(pictureAccounting(picture), {
    filed: 52,
    notFiled: 0,
    loose: 16,
    trash: 0,
    total: 68
  });
  assert.equal(pictureAccounting(picture).total, picture.waiting);
  assert.equal(looseTotal(picture), 16);
  picture.left_out = { count: 1, files: [file('a')] };
  picture.trash = { count: 2, files: [file('b'), file('c')] };
  picture.folders[1] = { ...picture.folders[1], count: 0, unfiled_count: 3, unfiled_files: [] };
  // The files of a folder that is not ticked are counted with that folder, not in
  // Still loose, so each file is counted once.
  assert.deepEqual(pictureAccounting(picture), {
    filed: 33,
    notFiled: 3,
    loose: 17,
    trash: 2,
    total: 55
  });

  assert.equal(earlierLine({ kept: 12 }), '12 left here earlier');
  assert.equal(earlierLine({ kept: 2, applied: 3 }), '5 handled earlier');
  assert.equal(earlierLine({}), '');
  // A subfolder or a half-finished download is not a file that was handled, and
  // there is no list to link to, so it never makes the quiet line appear.
  assert.equal(earlierLine({ ineligible: 4 }), '');
  const shown = [];
  const root = renderPicture(
    doc,
    pictureFixture({ counts: { kept: 12 } }),
    ctx({ onShowEarlier: () => shown.push(true) })
  );
  assert.match(
    textOf(find(root, node => node.className === 'fj-earlier')[0]),
    /12 left here earlier/
  );
  find(root, node => node.id === 'fileJanitorShowEarlier')[0].fire('click');
  assert.deepEqual(shown, [true]);
});

const planOf = (move_count, trash_count = 0, extra = {}) => ({
  move_count,
  trash_count,
  over_limit: false,
  ...extra
});

test('the one main button states what pressing it will do (R35)', () => {
  const model = plan => fileButtonModel({ plan, planCurrent: true, trashAcknowledged: true });
  assert.equal(model(planOf(52)).label, 'File 52 files');
  assert.equal(model(planOf(1)).label, 'File 1 file');
  assert.equal(model(planOf(52, 1)).label, 'File 52 files · 1 to Trash');
  assert.equal(model(planOf(52, 3)).label, 'File 52 files · 3 to Trash');
  assert.equal(model(planOf(0, 3)).label, 'Move 3 files to Trash');
  assert.equal(model(planOf(0, 1)).label, 'Move 1 file to Trash');
  assert.equal(model(planOf(0, 0)).label, 'Nothing to file');
  assert.equal(model(planOf(0, 0)).disabled, true, 'nothing to file is disabled');
  assert.equal(model(planOf(52)).disabled, false);
});

test('Trash needs its own tick before the button works, and the tick names the count (R37)', () => {
  const base = { planCurrent: true, plan: planOf(10, 2) };
  const without = fileButtonModel({ ...base, trashAcknowledged: false });
  assert.equal(without.disabled, true);
  assert.equal(without.needsTrashTick, true);
  assert.equal(fileButtonModel({ ...base, trashAcknowledged: true }).disabled, false);
  assert.equal(trashTickLabel(2), 'Yes, move 2 files to Trash');
  assert.equal(trashTickLabel(1), 'Yes, move 1 file to Trash');
  // No Trash, no tick to ask for.
  assert.equal(fileButtonModel({ plan: planOf(10), planCurrent: true }).needsTrashTick, false);
});

test('over 1,000 files the button is disabled and says what to do (R39)', () => {
  const model = fileButtonModel({
    plan: planOf(1200, 0, { over_limit: true }),
    planCurrent: true,
    trashAcknowledged: true
  });
  assert.equal(model.label, 'Ori files up to 1,000 files at a time. Untick a folder to continue.');
  assert.equal(model.label, OVER_LIMIT_NOTE);
  assert.equal(model.disabled, true);
});

test('while a choice is saving, unsaved, or the plan is being checked, the button waits and says why (R40)', () => {
  const plan = planOf(5);
  const saving = fileButtonModel({ plan, planCurrent: true, saveState: 'saving' });
  assert.deepEqual([saving.disabled, saving.note], [true, 'Saving…']);
  // The note names the way on that is actually offered: a failed save can be retried,
  // a change made elsewhere has to be reloaded (retrying would overwrite it).
  for (const [state, note] of [
    ['failed', 'Not saved. Retry'],
    ['conflict', 'Not saved. Reload'],
    ['unavailable', 'Choices unavailable. Reload']
  ]) {
    const model = fileButtonModel({ plan, planCurrent: true, saveState: state });
    assert.deepEqual([model.disabled, model.note], [true, note]);
  }
  const checking = fileButtonModel({ plan, planCurrent: false });
  assert.deepEqual([checking.disabled, checking.note], [true, 'Checking…']);
  assert.equal(fileButtonModel({ planCurrent: true }).disabled, true, 'no plan yet');
  assert.equal(fileButtonModel({ plan, planCurrent: true, editsAvailable: false }).disabled, true);
  const ready = fileButtonModel({ plan, planCurrent: true });
  assert.deepEqual([ready.disabled, ready.note], [false, '']);
});

test('no button label or note uses a retired word', () => {
  const texts = [];
  for (const [moves, trash] of [
    [52, 0],
    [52, 3],
    [0, 3],
    [0, 0]
  ])
    texts.push(fileButtonModel({ plan: planOf(moves, trash), planCurrent: true }).label);
  for (const saveState of ['saving', 'failed', 'conflict', 'unavailable'])
    texts.push(fileButtonModel({ plan: planOf(5), planCurrent: true, saveState }).note);
  texts.push(OVER_LIMIT_NOTE, trashTickLabel(3), 'Checking…');
  for (const text of texts)
    for (const word of BANNED_WORDS)
      assert.doesNotMatch(text.toLowerCase(), new RegExp('\\b' + word + '\\b'), text);
});

test('a file that will get a new name is shown under it, tagged "renamed" (R7)', () => {
  const picture = pictureFixture();
  picture.folders[0].files = [file('Invoice 100.pdf', { id: 'x1' }), ...names('doc-', 4)];
  const plan = {
    items: {
      x1: { renamed: true, final_name: 'Invoice 100 (2).pdf' },
      'id-doc-0': { renamed: false, final_name: 'doc-0' }
    }
  };
  const root = renderPicture(doc, picture, ctx({ plan }));
  const documents = find(root, node => node.getAttribute('data-fj-folder') === 'documents')[0];
  const items = find(documents, node => node.tagName === 'LI');
  assert.match(textOf(items[0]), /^Invoice 100 \(2\)\.pdf renamed$/);
  assert.equal(textOf(items[1]), 'doc-0', 'a file with no clash keeps its own name');
  // Until the plan check returns, every file shows its own name.
  const bare = renderPicture(doc, picture, ctx());
  const names0 = find(
    find(bare, node => node.getAttribute('data-fj-folder') === 'documents')[0],
    node => node.tagName === 'LI'
  ).map(textOf);
  assert.equal(names0[0], 'Invoice 100.pdf');
  assert.ok(!names0.join(' ').includes('renamed'));
});

test('small helpers read plainly', () => {
  assert.equal(folderLabel({ destination: 'Filed/Documents' }), 'Documents');
  assert.equal(folderLabel({ category: 'images' }), 'images');
  assert.equal(moreLabel(5, 28), 'and 23 more');
  assert.equal(
    folderAriaLabel({ destination: 'Filed/Audio', count: 1, unfiled_count: 0 }),
    'Audio, 1 file, will be filed'
  );
});

test('the picture never uses the retired words, in any state', () => {
  const states = [
    pictureFixture(),
    pictureFixture({ folders: [], content_read: 'some' }),
    pictureFixture({
      left_out: { count: 1, files: [file('a.png')] },
      trash: { count: 1, files: [file('b.png')] },
      could_not_file: {
        count: 1,
        files: [file('c.png', { reason: 'The move did not complete.' })]
      },
      counts: { kept: 3, applied: 2, ineligible: 1 }
    })
  ];
  const open = {
    section: 'loose',
    key: 'g-audio',
    state: 'loaded',
    offset: 0,
    limit: 50,
    total: 2,
    files: names('t-', 2)
  };
  for (const picture of states) {
    for (const withMore of [null, open]) {
      const text = textOf(renderPicture(doc, picture, ctx({ more: withMore }))).toLowerCase();
      for (const word of BANNED_WORDS)
        assert.doesNotMatch(text, new RegExp('\\b' + word + '\\b'), 'banned word: ' + word);
    }
  }
});
