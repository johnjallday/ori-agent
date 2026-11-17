// file-janitor-picture.js — Filing's review drawn as a picture of the folder as
// it will look after filing.
//
// This module only draws what the server's picture read (GET .../picture) says
// and reports what the person did through callbacks. It owns no request, no
// saved state and no lifecycle: the console loads the picture, owns the saved
// choices and their conflict handling, and decides what may be edited. Nothing
// here decides what may move, and every name reaches the page as text, never as
// markup.

const FILED = 'filed';
const UNFILED = 'unfiled';
const LOOSE = 'loose';
const LEFT_OUT = 'left_out';
const TRASH = 'trash';
const COULD_NOT_FILE = 'could_not_file';

// The words on screen for each question group, keyed by the server's fixed rule
// word (R17). The "Why?" sentence says what Ori actually checked (R18).
export const LOOSE_LABELS = {
  sensitive: 'Sensitive-looking names',
  versions: 'Possible copies',
  audio: 'Music and audio',
  unresolved: 'Unknown type',
  unmatched: 'Other files'
};

export const LOOSE_WHY = {
  sensitive: 'A name has a word like passport or tax in it. Ori has not looked inside the files.',
  versions: 'The names match apart from a copy ending. Ori has not compared what is inside.',
  audio:
    'These are music files, or files that share a name with one. Ori does not know which belong together.',
  unresolved:
    'Ori could not tell what these are from their names, types and sizes. It has not looked inside.',
  unmatched: 'These do not fit a folder Ori offers. Ori has not looked inside them.'
};

const LEFT_OUT_LABEL = 'Left out by you';

// Words the picture must never use (R58). Tests render every state and fail if
// one appears.
export const BANNED_WORDS = ['batch', 'candidate', 'proposal', 'exact', 'explicitly', 'draft'];

export function filesWord(count) {
  return count === 1 ? '1 file' : count + ' files';
}

export function frameTitle(folderName) {
  return folderName + ', after filing';
}

// R2: sentences one and two are fixed. The third must be true for this scan, so
// it follows what the server says about reading inside files, and is left out
// rather than guessed when the server cannot say.
export function leadSentences(folderName, contentRead) {
  const sentences = [
    'This is how ' + folderName + ' will look.',
    'Nothing moves until you press File.'
  ];
  if (contentRead === 'none')
    sentences.push('Ori reads names, types, sizes and dates, never what is inside.');
  else if (contentRead === 'some')
    sentences.push('Ori read inside some files to sort them, as you allowed in Settings.');
  return sentences;
}

// The folder's own name: the last part of its destination ("Filed/Documents").
export function folderLabel(folder) {
  const parts = String(folder?.destination || folder?.category || '').split('/');
  return parts[parts.length - 1] || 'Other';
}

export function looseLabel(row) {
  return LOOSE_LABELS[row?.key] || LOOSE_LABELS.unmatched;
}

// R66: what a screen reader hears for a folder card.
export function folderAriaLabel(folder) {
  const filing = folder.count > 0;
  const shown = filing ? folder.count : folder.unfiled_count;
  return (
    folderLabel(folder) + ', ' + filesWord(shown) + ', ' + (filing ? 'will be filed' : 'not filed')
  );
}

export function moreLabel(shown, total) {
  return 'and ' + (total - shown) + ' more';
}

// Still loose: exactly the files its rows list: the question rows and the files
// taken out of a ticked folder. The files of a folder that is not ticked also stay
// loose (R13), but they are drawn in that folder's own dashed card with their own
// count, so this number always matches what is listed under its heading.
export function looseTotal(picture) {
  let total = picture?.left_out?.count || 0;
  for (const row of picture?.loose || []) total += row.loose_count || 0;
  return total;
}

// R8: what is drawn adds up to what is waiting: files in a ticked folder, files in
// a folder that is not ticked, files still loose, and files marked for Trash. The
// tests use this to prove it; the server computes the same sum.
export function pictureAccounting(picture) {
  let filed = 0;
  let notFiled = 0;
  for (const folder of picture?.folders || []) {
    filed += folder.count || 0;
    notFiled += folder.unfiled_count || 0;
  }
  const loose = looseTotal(picture);
  const trash = picture?.trash?.count || 0;
  return { filed, notFiled, loose, trash, total: filed + notFiled + loose + trash };
}

// R9: the quiet line under the frame for files handled before this review: files
// left here for good and files already filed. A scan's "not eligible" observations
// (a subfolder, a download still in progress) are not files that were handled, and
// All files has no list for them, so counting them would put "1 handled earlier"
// under nearly every folder and link to nothing.
export function earlierLine(counts) {
  const kept = counts?.kept || 0;
  const total = kept + (counts?.applied || 0);
  if (!total) return '';
  return total + (total === kept ? ' left here earlier' : ' handled earlier');
}

export function moreTitle(picture, more) {
  if (!more) return '';
  if (more.section === FILED || more.section === UNFILED) {
    const folder = (picture?.folders || []).find(item => item.category === more.key);
    return (
      (folder ? folderLabel(folder) : 'Folder') + (more.section === UNFILED ? ', not filed' : '')
    );
  }
  if (more.section === LOOSE) {
    const row = (picture?.loose || []).find(item => item.group_id === more.key);
    return row ? looseLabel(row) : LOOSE_LABELS.unmatched;
  }
  if (more.section === LEFT_OUT) return LEFT_OUT_LABEL;
  if (more.section === TRASH) return 'Trash';
  return 'Could not be filed';
}

function builder(doc) {
  return (tag, className, text) => {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
}

// renderPicture draws the first screenful. ctx supplies the folder's display
// name, whether edits are possible right now, the open "and N more" list, and
// the callbacks. Draws no control that the server did not describe.
export function renderPicture(doc, picture, ctx) {
  const h = builder(doc);
  const folderName = ctx.folderName || 'This folder';
  const root = h('section', 'fj-picture');
  root.id = 'fileJanitorPicture';
  root.setAttribute('aria-label', 'Your folder as it will look after filing');

  const lead = h('p', 'fj-lead', leadSentences(folderName, picture.content_read).join(' '));
  lead.id = 'fileJanitorLead';
  root.appendChild(lead);

  const frame = h('section', 'fj-frame');
  frame.setAttribute('aria-labelledby', 'fileJanitorFrameTitle');
  const title = h('h3', 'fj-frame-title', frameTitle(folderName));
  title.id = 'fileJanitorFrameTitle';
  frame.appendChild(title);

  const filed = h('div', 'fj-box fj-filed');
  filed.appendChild(h('h4', 'fj-box-title', 'Filed'));
  if (!picture.folders.length) {
    filed.appendChild(
      h(
        'p',
        'fj-box-empty',
        picture.loose.length
          ? 'Nothing here yet. Use the menus below to file something.'
          : 'Nothing is set to be filed.'
      )
    );
  } else {
    const cards = h('div', 'fj-folder-cards');
    for (const folder of picture.folders) cards.appendChild(folderCard(h, folder, ctx));
    filed.appendChild(cards);
  }
  frame.appendChild(filed);

  const loose = looseTotal(picture);
  if (loose > 0) frame.appendChild(looseSection(h, picture, folderName, loose, ctx));
  if (picture.trash.count > 0) frame.appendChild(trashBox(h, picture, ctx));
  if (picture.could_not_file.count > 0) frame.appendChild(couldNotFile(h, picture, ctx));
  if (ctx.more) frame.appendChild(morePanel(h, picture, ctx));
  root.appendChild(frame);

  const earlier = earlierLine(picture.counts);
  if (earlier) {
    const line = h('p', 'fj-earlier', earlier + ' · ');
    const show = h('button', 'fj-link', 'Show');
    show.type = 'button';
    show.id = 'fileJanitorShowEarlier';
    show.addEventListener('click', () => ctx.onShowEarlier?.());
    line.appendChild(show);
    root.appendChild(line);
  }
  return root;
}

function nameList(h, files, className) {
  const list = h('ul', className);
  for (const file of files) {
    const item = h('li', 'fj-file', file.name);
    if (file.your_choice) item.appendChild(h('span', 'fj-tag', 'your choice'));
    list.appendChild(item);
  }
  return list;
}

function moreButton(h, ctx, section, key, shown, total) {
  const open = ctx.more?.section === section && ctx.more?.key === key;
  const control = h('button', 'fj-more', moreLabel(shown, total));
  control.type = 'button';
  control.id = 'fileJanitorMore-' + section + '-' + key;
  control.setAttribute('aria-expanded', open ? 'true' : 'false');
  control.setAttribute('aria-controls', 'fileJanitorMorePanel');
  control.addEventListener('click', () => ctx.onOpenMore?.(section, key));
  return control;
}

function folderCard(h, folder, ctx) {
  const unticked =
    folder.group_state === 'unticked' || (folder.count === 0 && folder.unfiled_count > 0);
  const card = h('article', 'fj-folder-card' + (unticked ? ' is-unticked' : ''));
  card.setAttribute('data-fj-folder', folder.category);
  const head = h('div', 'fj-folder-head');
  const name = folderLabel(folder);
  if (folder.group_id) {
    const label = h('label', 'fj-folder-tick');
    const box = h('input');
    box.type = 'checkbox';
    box.id = 'fileJanitorFolder-' + folder.category;
    box.checked = folder.group_state === 'ticked' || folder.group_state === 'partial';
    box.indeterminate = folder.group_state === 'partial';
    box.disabled = !ctx.editsAvailable;
    box.setAttribute('aria-label', folderAriaLabel(folder));
    box.setAttribute('aria-checked', box.indeterminate ? 'mixed' : box.checked ? 'true' : 'false');
    box.addEventListener('change', () => ctx.onTickFolder?.(folder, box.checked));
    label.appendChild(box);
    label.appendChild(h('span', 'fj-folder-name', name));
    head.appendChild(label);
  } else {
    head.appendChild(h('span', 'fj-folder-name', name));
  }
  head.appendChild(
    h('span', 'fj-folder-count', String(folder.count > 0 ? folder.count : folder.unfiled_count))
  );
  card.appendChild(head);
  if (unticked) card.appendChild(h('p', 'fj-not-filed', 'Not filed'));
  if (folder.count > 0) {
    card.appendChild(nameList(h, folder.files, 'fj-folder-files'));
    if (folder.count > folder.files.length)
      card.appendChild(
        moreButton(h, ctx, FILED, folder.category, folder.files.length, folder.count)
      );
  }
  if (folder.unfiled_count > 0) {
    if (folder.count > 0) card.appendChild(h('p', 'fj-not-filed', 'Not filed'));
    card.appendChild(nameList(h, folder.unfiled_files, 'fj-folder-files'));
    if (folder.unfiled_count > folder.unfiled_files.length)
      card.appendChild(
        moreButton(
          h,
          ctx,
          UNFILED,
          folder.category,
          folder.unfiled_files.length,
          folder.unfiled_count
        )
      );
  }
  return card;
}

function looseSection(h, picture, folderName, total, ctx) {
  const section = h('div', 'fj-box fj-loose');
  section.appendChild(h('h4', 'fj-box-title', 'Still loose in ' + folderName + ' · ' + total));
  for (const row of picture.loose) {
    if (!row.loose_count) continue;
    const box = h('div', 'fj-loose-row');
    box.setAttribute('data-fj-group', row.group_id);
    box.appendChild(h('h5', 'fj-loose-label', looseLabel(row) + ' · ' + row.count));
    const why = h('details', 'fj-why');
    why.appendChild(h('summary', 'fj-why-toggle', 'Why?'));
    why.appendChild(h('p', 'fj-why-text', LOOSE_WHY[row.key] || LOOSE_WHY.unmatched));
    box.appendChild(why);
    box.appendChild(nameList(h, row.singles, 'fj-loose-files'));
    if (row.more > 0)
      box.appendChild(moreButton(h, ctx, LOOSE, row.group_id, row.singles.length, row.loose_count));
    section.appendChild(box);
  }
  if (picture.left_out.count > 0) {
    const box = h('div', 'fj-loose-row');
    box.appendChild(h('h5', 'fj-loose-label', LEFT_OUT_LABEL + ' · ' + picture.left_out.count));
    box.appendChild(nameList(h, picture.left_out.files, 'fj-loose-files'));
    if (picture.left_out.count > picture.left_out.files.length)
      box.appendChild(
        moreButton(h, ctx, LEFT_OUT, '', picture.left_out.files.length, picture.left_out.count)
      );
    section.appendChild(box);
  }
  return section;
}

function trashBox(h, picture, ctx) {
  const box = h('div', 'fj-box fj-trash');
  box.appendChild(h('h4', 'fj-box-title', 'Trash · ' + picture.trash.count));
  box.appendChild(nameList(h, picture.trash.files, 'fj-loose-files'));
  if (picture.trash.count > picture.trash.files.length)
    box.appendChild(moreButton(h, ctx, TRASH, '', picture.trash.files.length, picture.trash.count));
  return box;
}

function couldNotFile(h, picture, ctx) {
  const box = h('div', 'fj-box fj-could-not');
  box.appendChild(h('h4', 'fj-box-title', 'Could not be filed · ' + picture.could_not_file.count));
  const list = h('ul', 'fj-loose-files');
  for (const file of picture.could_not_file.files) {
    const item = h('li', 'fj-file', file.name);
    if (file.reason) item.appendChild(h('span', 'fj-file-reason', file.reason));
    list.appendChild(item);
  }
  box.appendChild(list);
  if (picture.could_not_file.count > picture.could_not_file.files.length)
    box.appendChild(
      moreButton(
        h,
        ctx,
        COULD_NOT_FILE,
        '',
        picture.could_not_file.files.length,
        picture.could_not_file.count
      )
    );
  return box;
}

// The full list behind an "and N more" control, 50 at a time (R4, R64).
function morePanel(h, picture, ctx) {
  const more = ctx.more;
  const panel = h('section', 'fj-more-panel');
  panel.id = 'fileJanitorMorePanel';
  panel.setAttribute('tabindex', '-1');
  panel.setAttribute('aria-label', 'All files in ' + moreTitle(picture, more));
  panel.appendChild(h('h4', 'fj-box-title', moreTitle(picture, more)));
  const close = h('button', 'fj-btn', 'Close list');
  close.type = 'button';
  close.id = 'fileJanitorMoreClose';
  close.addEventListener('click', () => ctx.onCloseMore?.());
  panel.appendChild(close);
  if (more.state === 'loaded') {
    const first = more.files.length ? more.offset + 1 : 0;
    panel.appendChild(
      h(
        'p',
        'fj-sub',
        'Showing ' + first + '–' + (more.offset + more.files.length) + ' of ' + more.total + '.'
      )
    );
    panel.appendChild(nameList(h, more.files, 'fj-loose-files'));
    const nav = h('nav', 'fj-actions');
    nav.setAttribute('aria-label', 'File list pages');
    for (const [label, offset, disabled] of [
      ['Previous files', Math.max(0, more.offset - more.limit), more.offset === 0],
      ['Next files', more.offset + more.limit, more.offset + more.files.length >= more.total]
    ]) {
      const control = h('button', 'fj-btn', label);
      control.type = 'button';
      control.disabled = disabled;
      control.addEventListener('click', () => ctx.onMorePage?.(offset));
      nav.appendChild(control);
    }
    panel.appendChild(nav);
  } else if (more.state === 'error') {
    const failed = h('p', 'fj-empty-copy', 'These files could not be loaded. Nothing was changed.');
    failed.setAttribute('role', 'alert');
    panel.appendChild(failed);
    const retry = h('button', 'fj-btn', 'Retry');
    retry.type = 'button';
    retry.addEventListener('click', () => ctx.onMorePage?.(more.offset));
    panel.appendChild(retry);
  } else {
    const loading = h('p', 'fj-empty-copy', 'Loading…');
    loading.setAttribute('role', 'status');
    panel.appendChild(loading);
  }
  return panel;
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  window.FileJanitorPicture = {
    renderPicture,
    leadSentences,
    frameTitle,
    looseTotal,
    pictureAccounting,
    earlierLine,
    folderLabel
  };
  document.dispatchEvent(new CustomEvent('ori:filing-picture-ready'));
}
