// Tests for file-janitor-picture.js, the picture of the folder after filing.
// Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/file-janitor-picture.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  changeAnnouncement,
  folderCounts,
  setName,
  BANNED_WORDS,
  LOOSE_LABELS,
  LOOSE_WHY,
  OVER_LIMIT_NOTE,
  PAIR_BANNED_WORDS,
  bundleMenuValue,
  bundleNote,
  earlierLine,
  formatDay,
  formatSize,
  pairHint,
  trashCopyAllowed,
  fileButtonModel,
  fileMenuOptions,
  folderAriaLabel,
  folderLabel,
  frameTitle,
  leadSentences,
  looseTotal,
  moreLabel,
  pictureAccounting,
  renderPicture,
  rowMenuOptions,
  rowMenuValue,
  rowNote,
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
  // Before the first folder there is only the lead line and the frame title (R56);
  // the one-file bar comes after the frame, above the File button.
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
  // A row has one menu and no tick: Ori never ticks a question group (R11).
  assert.equal(find(rows[0], node => node.tagName === 'INPUT').length, 0);
  assert.equal(find(rows[0], node => node.tagName === 'SELECT').length, 1);
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
  // The name is long enough to be shortened in the middle: both halves are text, and
  // the whole name, markup characters and all, is the wrapper's title.
  const hostile = '<img src=x onerror=alert(1)>.png';
  const wrapper = find(root, node => node.title === hostile)[0];
  assert.ok(wrapper, 'the whole name is available');
  assert.equal(
    wrapper.children.map(child => child._text).join(''),
    hostile,
    'the two halves are the name, as text'
  );
  assert.match(textOf(root), /<b>Downloads<\/b>, after filing/);
  const source = readFileSync(new URL('./file-janitor-picture.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /innerHTML|insertAdjacentHTML|outerHTML|document\.write/);
});

// R66: what a screen reader hears after a change, said from what the picture now
// shows against what it showed.
test('a change is announced from the picture, not from the request (R66)', () => {
  const folders = (...entries) =>
    folderCounts({
      folders: entries.map(([destination, count]) => ({ destination, count }))
    });
  const before = folders(['Filed/Documents', 26], ['Filed/Images', 10]);
  assert.equal(
    changeAnnouncement(before, folders(['Filed/Documents', 28], ['Filed/Images', 10])),
    '2 files moved to Documents.'
  );
  assert.equal(
    changeAnnouncement(before, folders(['Filed/Documents', 27], ['Filed/Images', 10])),
    '1 file moved to Documents.'
  );
  // A file sent from one folder to another is a gain for the one that gained it.
  assert.equal(
    changeAnnouncement(before, folders(['Filed/Documents', 25], ['Filed/Images', 11])),
    '1 file moved to Images.'
  );
  // Nothing gained and some lost: the folders that lost are named instead.
  assert.equal(
    changeAnnouncement(before, folders(['Filed/Documents', 26], ['Filed/Images', 0])),
    '10 files no longer set to be filed in Images.'
  );
  // A folder that is gone altogether counts as lost; a new one as gained.
  assert.equal(
    changeAnnouncement(before, folders(['Filed/Documents', 26])),
    '10 files no longer set to be filed in Images.'
  );
  assert.equal(
    changeAnnouncement(
      before,
      folders(['Filed/Documents', 26], ['Filed/Images', 10], ['Filed/Other', 3])
    ),
    '3 files moved to Other.'
  );
  // No change, or nothing to compare, says nothing.
  assert.equal(changeAnnouncement(before, before), '');
  assert.equal(changeAnnouncement(null, before), '');
  assert.equal(changeAnnouncement(before, null), '');
});

// The element builder the module is given: tag, class, text.
const el = (tag, className, text) => {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
};

test('a long file name is shortened in the middle and keeps its whole name and its extension (R69)', () => {
  const short = setName(el, el('li'), 'invoice.pdf');
  assert.equal(short.textContent, 'invoice.pdf');
  assert.equal(short.children.length, 0, 'a name that fits is plain text');
  const long = 'Quarterly report for the whole northern region final version 2026.pdf';
  const node = setName(el, el('li'), long);
  const wrap = node.children[0];
  assert.equal(wrap.className, 'fj-mid');
  assert.equal(wrap.title, long, 'the whole name on hover');
  const [start, end] = wrap.children.map(child => child._text);
  assert.equal(start + end, long, 'both halves together are the whole name');
  assert.ok(end.endsWith('.pdf'), 'the extension always shows');
  assert.ok(start.length > end.length);
  // A button says its whole name, so its accessible name is exactly the file's name.
  const button = setName(el, el('button'), long);
  assert.equal(button.getAttribute('aria-label'), long);
  assert.equal(setName(el, el('button'), 'short.pdf').getAttribute('aria-label'), undefined);
  assert.equal(setName(el, el('li'), long).getAttribute('aria-label'), undefined);
  // No extension, and a name made of astral characters, are cut on characters.
  const plain = setName(el, el('li'), 'x'.repeat(60)).children[0];
  assert.equal(plain.children.map(child => child._text).join(''), 'x'.repeat(60));
  const astral = '😀'.repeat(40);
  const emoji = setName(el, el('li'), astral).children[0];
  assert.equal(emoji.children.map(child => child._text).join(''), astral);
  // The cut never splits a character: no half of a surrogate pair is left alone.
  const loneSurrogate = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/;
  assert.ok(emoji.children.every(child => !loneSurrogate.test(child._text)));
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
  assert.equal(model(planOf(0, 0)).label, 'No changes selected');
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

// ------------------------------------------------- answers on the loose rows

const DESTINATIONS = [
  { id: 'documents', label: 'Documents', folder_name: 'Documents' },
  { id: 'images', label: 'Images', folder_name: 'Images' },
  { id: 'archives', label: 'Archives', folder_name: 'Archives' }
];
const answerCtx = (over = {}) => ctx({ destinations: DESTINATIONS, filedRoot: 'Filed', ...over });
const options = select => select.children.map(option => option.value);

test('a row menu offers Leave here, Leave here, stop asking, then one folder each, and never Trash (R19)', () => {
  const root = renderPicture(doc, pictureFixture(), answerCtx());
  const menu = find(
    root,
    node => node.tagName === 'SELECT' && node.id === 'fileJanitorRowMenu-sensitive'
  )[0];
  assert.deepEqual(options(menu), [
    'leave',
    'stop',
    'move:documents',
    'move:images',
    'move:archives'
  ]);
  assert.deepEqual(
    menu.children.map(option => textOf(option)),
    [
      'Leave here',
      'Leave here, stop asking',
      'Move to Documents',
      'Move to Images',
      'Move to Archives'
    ]
  );
  assert.ok(!menu.children.some(option => /trash/i.test(textOf(option))));
  assert.equal(menu.children.find(option => option.selected).value, 'leave');
  assert.match(menu.getAttribute('aria-label'), /^Sensitive-looking names: /);
});

test('choosing in a row menu reports the row and the choice, and only while edits are possible', () => {
  const calls = [];
  const root = renderPicture(
    doc,
    pictureFixture(),
    answerCtx({ onRowChoice: (row, choice) => calls.push([row.key, choice]) })
  );
  const menu = find(root, node => node.id === 'fileJanitorRowMenu-sensitive')[0];
  for (const value of ['move:documents', 'stop', 'leave']) {
    menu.value = value;
    menu.fire('change');
  }
  assert.deepEqual(calls, [
    ['sensitive', { kind: 'move', category: 'documents' }],
    ['sensitive', { kind: 'stop' }],
    ['sensitive', { kind: 'leave' }]
  ]);
  const busy = renderPicture(doc, pictureFixture(), answerCtx({ editsAvailable: false }));
  assert.ok(find(busy, node => node.tagName === 'SELECT').every(select => select.disabled));
});

test('a row whose files were all placed stays drawn with a note so the choice can be changed back (R20)', () => {
  const picture = pictureFixture();
  picture.loose[0] = {
    ...picture.loose[0],
    placed_count: 2,
    placed_category: 'documents',
    loose_count: 0,
    singles: []
  };
  const root = renderPicture(doc, picture, answerCtx());
  const row = find(root, node => node.getAttribute('data-fj-group') === 'g-sens')[0];
  assert.ok(row, 'the placed row is still drawn');
  assert.match(textOf(row), /^Sensitive-looking names/);
  // The heading keeps the row's count; the note says where the files went.
  assert.equal(
    textOf(find(row, node => node.className === 'fj-loose-label')[0]),
    'Sensitive-looking names · 2'
  );
  assert.equal(
    textOf(find(row, node => node.className === 'fj-row-note')[0]),
    'All in Filed/Documents'
  );
  assert.equal(
    find(row, node => node.tagName === 'SELECT')[0].children.find(o => o.selected).value,
    'move:documents'
  );
  // Still loose counts only what is listed loose: the audio row's 14.
  assert.ok(
    find(root, node => node.className === 'fj-box-title')
      .map(textOf)
      .includes('Still loose in Downloads · 14')
  );
});

test('when every row is placed the section is still there, with no count in its heading', () => {
  const picture = pictureFixture();
  picture.loose = picture.loose.map(row => ({
    ...row,
    placed_count: row.count,
    placed_category: 'images',
    loose_count: 0,
    singles: [],
    more: 0
  }));
  const root = renderPicture(doc, picture, answerCtx());
  const titles = find(root, node => node.className === 'fj-box-title').map(textOf);
  assert.ok(titles.includes('Still loose in Downloads'), titles.join(' | '));
  assert.equal(find(root, node => node.className === 'fj-loose-row').length, 2);
});

test('row notes say where the files went, in plain words', () => {
  const dest = answerCtx();
  assert.equal(
    rowNote({ count: 2, placed_count: 2, placed_category: 'images', trash_count: 0 }, dest),
    'All in Filed/Images'
  );
  assert.equal(
    rowNote({ count: 5, placed_count: 3, placed_category: 'images', trash_count: 0 }, dest),
    '3 in Filed/Images'
  );
  assert.equal(
    rowNote({ count: 5, placed_count: 3, placed_category: '', trash_count: 0 }, dest),
    '3 placed in other folders'
  );
  assert.equal(rowNote({ count: 5, placed_count: 0, trash_count: 1 }, dest), '1 in Trash');
  assert.equal(
    rowNote({ count: 5, placed_count: 3, placed_category: 'images', trash_count: 2 }, dest),
    '3 in Filed/Images · 2 in Trash'
  );
  assert.equal(rowNote({ count: 5, placed_count: 0, trash_count: 0 }, dest), '');
  assert.equal(rowMenuValue({ count: 5, placed_count: 3, placed_category: '' }), 'mixed');
  assert.ok(
    rowMenuOptions(DESTINATIONS, 'mixed').some(
      option => option.disabled && option.value === 'mixed'
    )
  );
});

test('the selected filename expands one inline editor, with its existing per-file choices', () => {
  const picked = [];
  const choices = [];
  const selectedFile = 'fileJanitorFileRow-filed-documents--id-doc-1';
  const picture = pictureFixture();
  picture.folders[0].files[1].category = 'documents';
  const root = renderPicture(
    doc,
    picture,
    answerCtx({
      selectedFile,
      onSelectFile: (...args) => picked.push(args),
      onFileChoice: (item, choice) => choices.push([item.name, choice])
    })
  );
  const buttons = find(root, node => node.className === 'fj-file-name');
  assert.ok(buttons.length >= 20, 'folder cards and loose rows alike');
  assert.equal(
    buttons.find(node => node.id === selectedFile + '-name').getAttribute('aria-expanded'),
    'true'
  );
  assert.equal(
    buttons.find(node => node.id.endsWith('--id-doc-0-name')).getAttribute('aria-expanded'),
    'false'
  );
  const [menu] = find(root, node => node.id?.endsWith('-choice'));
  assert.equal(menu.id, selectedFile + '-choice');
  assert.equal(menu.getAttribute('aria-label'), 'Filing choice for doc-1');
  assert.deepEqual(options(menu), ['', 'move:images', 'move:archives', 'leave', 'stop', 'trash']);
  menu.value = 'trash';
  menu.fire('change');
  assert.deepEqual(choices, [['doc-1', { kind: 'trash' }]]);
  find(root, node => node.id === selectedFile + '-cancel')[0].fire('click');
  const other = buttons.find(node => node.id.endsWith('--id-passport-0-name'));
  other.fire('click');
  assert.deepEqual(picked, [
    [selectedFile, false],
    [other.id.slice(0, -5), true]
  ]);
});

test('what the one-file menu offers follows where the file is now', () => {
  const labels = placement =>
    fileMenuOptions({ placement, category: 'documents' }, DESTINATIONS).map(option => option.value);
  assert.deepEqual(labels('filed'), ['move:images', 'move:archives', 'leave', 'stop', 'trash']);
  assert.deepEqual(labels('loose'), [
    'move:documents',
    'move:images',
    'move:archives',
    'stop',
    'trash'
  ]);
  assert.deepEqual(labels('left_out'), [
    'move:documents',
    'move:images',
    'move:archives',
    'stop',
    'trash'
  ]);
  assert.deepEqual(labels('trash'), [
    'move:documents',
    'move:images',
    'move:archives',
    'leave',
    'stop'
  ]);
});

test('a row Ori was told to stop asking about stays as a note with a way back (R22)', () => {
  const again = [];
  const root = renderPicture(
    doc,
    pictureFixture(),
    answerCtx({
      keptRows: [{ key: 'versions', count: 3, ids: ['a', 'b', 'c'] }],
      onReviewAgain: kept => again.push(kept.key)
    })
  );
  const row = find(root, node => node.getAttribute('data-fj-kept') === 'versions')[0];
  assert.match(textOf(row), /^Possible copies · 3/);
  assert.match(textOf(row), /Ori will not ask about these again\./);
  find(row, node => node.id === 'fileJanitorReviewAgain-versions')[0].fire('click');
  assert.deepEqual(again, ['versions']);
});

test('"Try again" is offered only for a file an earlier attempt can retry (R25)', () => {
  const picture = pictureFixture({
    could_not_file: {
      count: 2,
      files: [
        file('failed.pdf', {
          state: 'failed',
          reason: 'Ori could not move this file.',
          can_retry: true
        }),
        file('changed.pdf', {
          state: 'changed',
          reason: 'This file changed since you looked, so Ori left it alone.',
          can_retry: false
        })
      ]
    }
  });
  const retried = [];
  const root = renderPicture(doc, picture, answerCtx({ onRetry: item => retried.push(item.name) }));
  const retries = find(root, node => node.textContent === 'Try again');
  assert.equal(retries.length, 1);
  assert.equal(retries[0].getAttribute('aria-label'), 'Try failed.pdf again');
  retries[0].fire('click');
  assert.deepEqual(retried, ['failed.pdf']);
});

// ------------------------------------------------------ pairs and bundles

const noon = day => '2026-11-' + String(day).padStart(2, '0') + 'T12:00:00Z';
const copyFile = (name, ending, extra = {}) => ({
  id: 'id-' + name,
  name,
  size: 2048,
  modified_at: noon(10),
  placement: 'loose',
  ending,
  family_id: 'fam-' + '0'.repeat(24),
  ...extra
});
const pairPicture = (files, extra = {}) =>
  pictureFixture({
    loose: [
      {
        group_id: 'g-ver',
        key: 'versions',
        count: files.length,
        placed_count: 0,
        trash_count: 0,
        loose_count: files.length,
        singles: [],
        pairs: [
          { family_id: 'fam-' + '0'.repeat(24), count: files.length, files, more: 0, ...extra }
        ],
        bundles: [],
        more: 0
      }
    ]
  });

test('"Trash the copy" follows the rule table exactly (R28)', () => {
  const original = copyFile('Budget.pdf', 'none');
  const card = (...extra) => [original, ...extra];
  const copy = copyFile('Budget (2).pdf', 'number');
  assert.equal(trashCopyAllowed(copy, card(copy)), true, 'a bracketed number, same size');
  const wordCopy = copyFile('Budget copy 2.pdf', 'copy');
  assert.equal(trashCopyAllowed(wordCopy, card(wordCopy)), true, '"copy" and "copy 2"');
  // The higher version may be the one to keep: never offered.
  for (const ending of ['v', 'version'])
    assert.equal(
      trashCopyAllowed(copyFile('Budget v2.pdf', ending), card(copyFile('Budget v2.pdf', ending))),
      false,
      ending
    );
  const bigger = copyFile('Budget (2).pdf', 'number', { size: 4096 });
  assert.equal(trashCopyAllowed(bigger, card(bigger)), false, 'different size');
  assert.equal(trashCopyAllowed(copy, [copy]), false, 'no file without an ending in the card');
  assert.equal(
    trashCopyAllowed(copy, [original, copyFile('Budget.PDF', 'none', { id: 'x' }), copy]),
    false,
    'two files without an ending'
  );
  assert.equal(trashCopyAllowed(original, card(copy)), false, 'the original is never a copy');
  assert.equal(
    trashCopyAllowed({ ...copy, placement: 'trash' }, card(copy)),
    false,
    'already in Trash'
  );
  assert.equal(
    trashCopyAllowed({ ...copy, placement: 'filed' }, card(copy)),
    false,
    'already placed'
  );
  assert.equal(trashCopyAllowed(null, card(copy)), false);
});

test('the hint line comes from size and date only (R27)', () => {
  const a = copyFile('a.pdf', 'none');
  assert.equal(pairHint([a, copyFile('b.pdf', 'number')]), 'Same size, same day');
  assert.equal(
    pairHint([a, copyFile('b.pdf', 'number', { modified_at: noon(11) })]),
    'Same size, different day'
  );
  assert.equal(pairHint([a, copyFile('b.pdf', 'number', { size: 1 })]), 'Different size');
  // Three files: any different size means different size; all alike means same.
  assert.equal(
    pairHint([a, copyFile('b', 'number'), copyFile('c', 'copy', { size: 9 })]),
    'Different size'
  );
  assert.equal(
    pairHint([a, copyFile('b', 'number'), copyFile('c', 'copy')]),
    'Same size, same day'
  );
  // A date nobody knows is never claimed to be the same day.
  assert.equal(
    pairHint([a, copyFile('b.pdf', 'number', { modified_at: '' })]),
    'Same size, different day'
  );
  assert.equal(pairHint([a]), '');
  for (const hint of ['Same size, same day', 'Same size, different day', 'Different size'])
    for (const word of PAIR_BANNED_WORDS) assert.doesNotMatch(hint.toLowerCase(), new RegExp(word));
});

test('pair facts show exact bytes when different files round to the same size', () => {
  const sizes = [313300, 313320];
  const files = sizes.map((size, i) =>
    copyFile('form' + i + '.pdf', i ? 'number' : 'none', { size })
  );
  const tree = renderPicture(doc, pairPicture(files), answerCtx());
  assert.deepEqual(
    find(tree, n => n.className === 'fj-pair-facts').map(n => textOf(n).split(' · ')[0]),
    sizes.map(size => size.toLocaleString() + ' bytes')
  );
});

test('sizes and days read plainly', () => {
  assert.equal(formatSize(0), '0 B');
  assert.equal(formatSize(1023), '1023 B');
  assert.equal(formatSize(1024), '1 KB');
  assert.equal(formatSize(1536), '1.5 KB');
  assert.equal(formatSize(20 * 1024), '20 KB');
  assert.equal(formatSize(5 * 1024 * 1024), '5 MB');
  assert.equal(formatSize(-1), '');
  assert.equal(formatSize('x'), '');
  assert.equal(formatDay(noon(10)), 'Nov 10, 2026');
  assert.equal(formatDay(''), '');
  assert.equal(formatDay('not a date'), '');
});

test('a pair card lists the original first with name, size and date, and one hint line (R26, R27)', () => {
  const files = [
    copyFile('Budget.pdf', 'none'),
    copyFile('Budget (2).pdf', 'number'),
    copyFile('Budget-v2.pdf', 'v', { size: 4096, modified_at: noon(12) })
  ];
  const root = renderPicture(doc, pairPicture(files), answerCtx());
  const card = find(root, node => node.className === 'fj-pair')[0];
  assert.equal(card.getAttribute('aria-label'), 'Possible copies of Budget.pdf');
  const lines = find(card, node => node.className === 'fj-pair-line');
  assert.deepEqual(
    lines.map(line => textOf(find(line, node => node.className === 'fj-pair-name')[0])),
    ['Budget.pdf', 'Budget (2).pdf', 'Budget-v2.pdf']
  );
  assert.equal(
    textOf(find(lines[0], node => node.className === 'fj-pair-facts')[0]),
    '2 KB · Nov 10, 2026'
  );
  assert.equal(
    textOf(find(lines[2], node => node.className === 'fj-pair-facts')[0]),
    '4 KB · Nov 12, 2026'
  );
  assert.equal(textOf(find(card, node => node.className === 'fj-pair-hint')[0]), 'Different size');
  // Only the bracketed copy of the same size as the original has the button: not the
  // original, and not the higher version (it ends in v2 and has another size).
  const buttons = find(card, node => node.textContent === 'Mark for Trash');
  assert.deepEqual(
    buttons.map(button => button.getAttribute('aria-label')),
    ['Mark for Trash: Budget (2).pdf']
  );
});

test('"Trash the copy" appears on the one line that passes the rule, marks that file, and can be undone (R28, R29)', () => {
  const marked = [];
  const original = copyFile('Budget.pdf', 'none');
  const copy = copyFile('Budget (2).pdf', 'number');
  const root = renderPicture(
    doc,
    pairPicture([original, copy]),
    answerCtx({ onTrashCopy: file => marked.push(file.name) })
  );
  const trash = find(root, node => node.textContent === 'Mark for Trash');
  assert.equal(trash.length, 1);
  assert.equal(trash[0].getAttribute('aria-label'), 'Mark for Trash: Budget (2).pdf');
  trash[0].fire('click');
  assert.deepEqual(marked, ['Budget (2).pdf']);
  // After it is marked the line says so and offers the way back; nothing else changes.
  const back = [];
  const after = renderPicture(
    doc,
    pairPicture([original, { ...copy, placement: 'trash' }]),
    answerCtx({ onPutBack: file => back.push(file.name) })
  );
  const line = find(after, node => node.className === 'fj-pair-line')[1];
  assert.match(textOf(line), /Marked for Trash/);
  assert.equal(find(after, node => node.textContent === 'Mark for Trash').length, 0);
  find(line, node => node.textContent === 'Undo mark')[0].fire('click');
  assert.deepEqual(back, ['Budget (2).pdf']);
});

test('there is no Trash action on a row, a pair or a bundle as a whole (R30)', () => {
  const picture = pairPicture([
    copyFile('Budget.pdf', 'none'),
    copyFile('Budget (2).pdf', 'number')
  ]);
  picture.loose.push({
    group_id: 'g-audio',
    key: 'audio',
    count: 2,
    placed_count: 0,
    trash_count: 0,
    loose_count: 2,
    singles: [],
    pairs: [],
    bundles: [bundleFixture()],
    more: 0
  });
  const root = renderPicture(doc, picture, answerCtx());
  for (const select of find(root, node => node.tagName === 'SELECT')) {
    const individual = select.id.startsWith('fileJanitorFileRow-');
    assert.equal(
      select.children.some(option => /trash/i.test(textOf(option))),
      individual,
      select.id
    );
  }
  const trashLabels = find(
    root,
    node => /trash/i.test(textOf(node)) && node.tagName === 'BUTTON'
  ).map(textOf);
  assert.deepEqual(trashLabels, ['Mark for Trash'], 'only the one per-file button');
});

test('a pair card with more files than it shows opens its own list', () => {
  const opened = [];
  const files = [copyFile('Budget.pdf', 'none'), copyFile('Budget (2).pdf', 'number')];
  const root = renderPicture(
    doc,
    pairPicture(files, { count: 9, more: 7 }),
    answerCtx({ onOpenMore: (section, key) => opened.push([section, key]) })
  );
  const more = find(
    root,
    node => node.className === 'fj-more' && /and 7 more/.test(textOf(node))
  )[0];
  more.fire('click');
  assert.deepEqual(opened, [['family', 'fam-' + '0'.repeat(24)]]);
});

function bundleFixture(over = {}) {
  return {
    bundle_id: 'bun-' + '1'.repeat(24),
    name: 'Song 130',
    count: 5,
    more: 0,
    loose_count: 5,
    placed_count: 0,
    trash_count: 0,
    files: ['Song 130.wav', 'Song 130.als', 'Song 130.mp3', 'Song 130.aiff', 'Song 130.flac'].map(
      (name, i) => ({
        id: 'id-b' + i,
        name,
        size: 100,
        placement: 'loose',
        bundle_id: 'bun-' + '1'.repeat(24)
      })
    ),
    ...over
  };
}

const audioPicture = bundle =>
  pictureFixture({
    loose: [
      {
        group_id: 'g-audio',
        key: 'audio',
        count: bundle.count + 1,
        placed_count: bundle.placed_count,
        trash_count: bundle.trash_count,
        loose_count: bundle.loose_count + 1,
        singles: [file('solo.mp3', { placement: 'loose' })],
        pairs: [],
        bundles: [bundle],
        more: 0
      }
    ]
  });

test('a bundle card says what it is and moves or stays together (R31)', () => {
  const root = renderPicture(doc, audioPicture(bundleFixture()), answerCtx());
  const card = find(root, node => node.className === 'fj-bundle')[0];
  assert.equal(
    textOf(find(card, node => node.className === 'fj-bundle-title')[0]),
    'Song 130 · 5 files · moves or stays together'
  );
  assert.equal(find(card, node => node.className === 'fj-file').length, 5, 'the names are inside');
  // Each can still be selected by itself (R33).
  const selectable = renderPicture(
    doc,
    audioPicture(bundleFixture()),
    answerCtx({ onSelectFile: () => {} })
  );
  assert.equal(
    find(
      find(selectable, node => node.className === 'fj-bundle')[0],
      node => node.className === 'fj-file-name'
    ).length,
    5
  );
  // The single song with no partner is listed beside the bundle, in the same row.
  const row = find(root, node => node.getAttribute('data-fj-group') === 'g-audio')[0];
  const singles = find(row, node => node.className === 'fj-loose-files').filter(
    list => !card.all(n => n === list).length
  );
  assert.ok(singles.some(list => /solo\.mp3/.test(textOf(list))));
});

test('a bundle menu has the same choices as a row menu and one choice covers the whole bundle (R32)', () => {
  const chosen = [];
  const bundle = bundleFixture();
  const root = renderPicture(
    doc,
    audioPicture(bundle),
    answerCtx({ onBundleChoice: (item, choice) => chosen.push([item.name, choice]) })
  );
  const menu = find(root, node => node.id === 'fileJanitorBundleMenu-' + bundle.bundle_id)[0];
  assert.deepEqual(options(menu), [
    'leave',
    'stop',
    'move:documents',
    'move:images',
    'move:archives'
  ]);
  assert.equal(menu.children.find(option => option.selected).value, 'leave');
  assert.match(menu.getAttribute('aria-label'), /^Song 130: what to do with these 5 files$/);
  menu.value = 'move:archives';
  menu.fire('change');
  assert.deepEqual(chosen, [['Song 130', { kind: 'move', category: 'archives' }]]);
});

test('a bundle that is not all in one place says how it is split, in plain words (R33)', () => {
  const dest = answerCtx();
  const note = over => bundleNote(bundleFixture(over), dest);
  assert.equal(note({}), '', 'all loose: nothing to say');
  assert.equal(
    note({ loose_count: 0, placed_count: 5, placed_category: 'archives' }),
    'All in Filed/Archives'
  );
  assert.equal(
    note({ loose_count: 4, placed_count: 1, placed_category: 'archives' }),
    'Split: 4 stay here, 1 in Filed/Archives'
  );
  assert.equal(
    note({ loose_count: 1, placed_count: 4, placed_category: 'archives' }),
    'Split: 1 stays here, 4 in Filed/Archives'
  );
  assert.equal(
    note({ loose_count: 2, placed_count: 2, placed_category: '', trash_count: 1 }),
    'Split: 2 stay here, 2 placed in other folders, 1 in Trash'
  );
  assert.equal(bundleMenuValue(bundleFixture({ loose_count: 4, placed_count: 1 })), 'mixed');
  assert.equal(
    bundleMenuValue(bundleFixture({ loose_count: 0, placed_count: 5, placed_category: 'images' })),
    'move:images'
  );
  const drawn = renderPicture(
    doc,
    audioPicture(bundleFixture({ loose_count: 4, placed_count: 1, placed_category: 'archives' })),
    dest
  );
  assert.equal(
    textOf(find(drawn, node => node.className === 'fj-row-note fj-bundle-note')[0]),
    'Split: 4 stay here, 1 in Filed/Archives'
  );
});

test('no word on a pair or a bundle claims what Ori has not checked, or is a retired word', () => {
  const picture = audioPicture(
    bundleFixture({ loose_count: 4, placed_count: 1, placed_category: 'archives' })
  );
  picture.loose.push(
    pairPicture([
      copyFile('Budget.pdf', 'none'),
      copyFile('Budget (2).pdf', 'number', { placement: 'trash' })
    ]).loose[0]
  );
  const text = find(renderPicture(doc, picture, answerCtx()), () => true)
    .map(node => node._text)
    .join(' ')
    .toLowerCase();
  for (const word of [...BANNED_WORDS, ...PAIR_BANNED_WORDS])
    assert.doesNotMatch(text, new RegExp('\\b' + word + '\\b'), word);
});

test('the border tokens exist wherever the picture and the footer use them', () => {
  // A border that names an undefined custom property is not drawn at all: the Trash
  // tick and the one-file bar sit in the footer, outside the picture, and lost their
  // borders once. Both scopes must define the tokens.
  const css = readFileSync(new URL('../../css/file-janitor.css', import.meta.url), 'utf8');
  const definition = css.match(/([^{}]+)\{[^{}]*--fj-line-strong:[^{}]*\}/);
  assert.ok(definition, 'the tokens are defined in one rule');
  assert.match(definition[1], /\.fj-picture-host/);
  assert.match(definition[1], /\.fj-footer/);
});

test('no word on the answer controls is a retired word', () => {
  const picture = pictureFixture({
    could_not_file: {
      count: 1,
      files: [file('x.pdf', { reason: 'Ori could not move this file.', can_retry: true })]
    }
  });
  const root = renderPicture(
    doc,
    picture,
    answerCtx({
      keptRows: [{ key: 'versions', count: 1, ids: ['a'] }],
      selectedFile: file('doc-1', { category: 'documents' })
    })
  );
  const text = find(root, () => true)
    .map(node => node._text)
    .join(' ')
    .toLowerCase();
  for (const word of BANNED_WORDS)
    assert.doesNotMatch(text, new RegExp('\\b' + word + '\\b'), word);
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
