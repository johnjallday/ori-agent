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
// The lists behind a pair card and a bundle card that hold more files than they show.
const FAMILY = 'family';
const BUNDLE = 'bundle';

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
  versions:
    'The names match apart from a copy or version ending. Ori has not compared what is inside.',
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

// How many files each folder card will file, by destination. The console reads this
// before and after one saved change so it can say what the change did (R66).
export function folderCounts(picture) {
  const counts = new Map();
  for (const folder of picture?.folders || [])
    counts.set(folder.destination || folder.category || '', {
      label: folderLabel(folder),
      count: folder.count || 0
    });
  return counts;
}

// R66: what a screen reader hears after a change, such as "2 files moved to
// Documents". Said from what the picture now shows against what it showed, never
// from the request, so it cannot claim a move that did not happen. Folders that
// gained files are named; when none gained and some lost, those are named instead.
export function changeAnnouncement(before, after) {
  if (!before || !after) return '';
  const gained = [];
  const lost = [];
  for (const [key, next] of after) {
    const was = before.get(key)?.count || 0;
    if (next.count > was) gained.push(filesWord(next.count - was) + ' moved to ' + next.label);
    else if (next.count < was)
      lost.push(filesWord(was - next.count) + ' no longer set to be filed in ' + next.label);
  }
  for (const [key, was] of before)
    if (!after.has(key) && was.count > 0)
      lost.push(filesWord(was.count) + ' no longer set to be filed in ' + was.label);
  return (gained.length ? gained : lost).join('. ') + (gained.length || lost.length ? '.' : '');
}

// R69: a long file name is shortened in the middle, so the start and the extension
// both show, and the whole name is in the page: it is the text a screen reader reads,
// it can be copied, and it shows in full on hover and on focus (see the stylesheet).
// A name that fits is plain text.
const SHORT_NAME_MAX = 28;
export function setName(h, node, name) {
  const text = String(name ?? '');
  const letters = Array.from(text);
  if (letters.length <= SHORT_NAME_MAX) {
    node.textContent = text;
    return node;
  }
  const dot = text.lastIndexOf('.');
  const extension = dot > 0 && text.length - dot <= 8 ? text.length - dot : 0;
  const endLength = Math.min(Math.max(extension + 5, 9), 14);
  // The two halves are separate boxes, which a browser reads with a space between
  // them. A button says its whole name outright, so a screen reader hears the file's
  // own name and nothing is lost to the cut.
  if (String(node.tagName).toUpperCase() === 'BUTTON') node.setAttribute('aria-label', text);
  const wrap = h('span', 'fj-mid');
  wrap.title = text;
  wrap.appendChild(
    h('span', 'fj-mid-start', letters.slice(0, letters.length - endLength).join(''))
  );
  wrap.appendChild(h('span', 'fj-mid-end', letters.slice(letters.length - endLength).join('')));
  node.appendChild(wrap);
  return node;
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

// R35, R37, R39, R40: the one main button. It states what pressing it will do, and
// is disabled until pressing it is safe to offer. Pure: the console passes what it
// knows and draws what this returns; nothing here decides what may move.
//
//   plan                 the server's plan check for what the person is looking at
//   planCurrent          that plan names the saved choices on screen
//   saveState            '' | 'saving' | 'failed' (retry) | 'conflict' (changed
//                        elsewhere: reload) | 'unavailable' (could not be read)
//   trashAcknowledged    the person ticked "Yes, move M files to Trash" for this M
//   editsAvailable       nothing else is in flight
export const CHANGED_TAG = 'changed since you looked';
export const OVER_LIMIT_NOTE =
  'Ori files up to 1,000 files at a time. Untick a folder to continue.';

export function trashTickLabel(count) {
  return 'Yes, move ' + filesWord(count) + ' to Trash';
}

export function fileButtonModel({
  plan,
  planCurrent,
  saveState = '',
  trashAcknowledged = false,
  editsAvailable = true
} = {}) {
  // A plan from another revision is not a forecast for the choices on screen.
  if (!planCurrent || saveState) plan = null;
  const moves = plan?.move_count || 0;
  const trash = plan?.trash_count || 0;
  // Until a matching plan is available, the button says File and waits rather
  // than claiming there is nothing to file.
  let label = plan ? 'No changes selected' : 'File';
  if (moves > 0 && trash > 0) label = 'File ' + filesWord(moves) + ' · ' + trash + ' to Trash';
  else if (moves > 0) label = 'File ' + filesWord(moves);
  else if (trash > 0) label = 'Move ' + filesWord(trash) + ' to Trash';
  let note = '';
  let disabled = false;
  if (plan?.over_limit) {
    label = OVER_LIMIT_NOTE;
    disabled = true;
  } else if (saveState === 'saving') {
    note = 'Saving…';
    disabled = true;
  } else if (saveState === 'failed') {
    note = 'Not saved. Retry';
    disabled = true;
  } else if (saveState === 'conflict') {
    // Changed somewhere else: retrying would overwrite it, so the way on is to reload.
    note = 'Not saved. Reload';
    disabled = true;
  } else if (saveState === 'unavailable') {
    note = 'Choices unavailable. Reload';
    disabled = true;
  } else if (!plan || !planCurrent) {
    note = 'Checking…';
    disabled = true;
  } else if (moves + trash === 0) {
    disabled = true;
  } else if (trash > 0 && !trashAcknowledged) {
    disabled = true;
  } else if (!editsAvailable) {
    disabled = true;
  }
  return { label, note, disabled, moves, trash, needsTrashTick: trash > 0 && !plan?.over_limit };
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
  if (more.section === FAMILY) return LOOSE_LABELS.versions;
  if (more.section === BUNDLE) {
    for (const row of picture?.loose || []) {
      const bundle = (row.bundles || []).find(item => item.bundle_id === more.key);
      if (bundle) return bundle.name;
    }
    return LOOSE_LABELS.audio;
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

// These are whole-section counts, not samples. A placed sibling may be drawn
// both in its destination and on its pair card, but is no longer a question.
function decisionSummary(h, picture, plan, folderName, sections) {
  const { move_count: moves, trash_count: trash } = plan;
  const undecided = picture.loose.reduce((total, row) => total + row.loose_count, 0);
  const summary = h('div', 'fj-decision-summary');
  const counts = h('p', 'fj-decision-counts');
  const add = (count, label, id, target) => {
    if (counts.children.length) {
      const separator = h('span', '', '·');
      separator.setAttribute('aria-hidden', 'true');
      counts.appendChild(separator);
    }
    const actionable = count > 0 && target;
    const node = h(actionable ? 'button' : 'span', actionable ? 'fj-link' : '', count + label);
    if (actionable) {
      node.type = 'button';
      node.id = id;
      node.addEventListener('click', () => {
        target.scrollIntoView?.({ block: 'center' });
        target.focus({ preventScroll: true });
      });
    }
    counts.appendChild(node);
  };
  add(moves, ' ready to file', 'fileJanitorJumpFiled', sections.filed);
  add(
    undecided,
    undecided === 1 ? ' needs a decision' : ' need a decision',
    'fileJanitorJumpLoose',
    sections.loose
  );
  if (trash > 0) add(trash, ' marked for Trash', 'fileJanitorJumpTrash', sections.trash);
  summary.appendChild(counts);
  if (undecided > 0)
    summary.appendChild(h('p', 'fj-sub', 'Unanswered files stay in ' + folderName + '.'));
  return summary;
}

function sectionTitle(h, text) {
  const title = h('h4', 'fj-box-title', text);
  title.setAttribute('tabindex', '-1');
  return title;
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
  // Focusable by script only: when the control a person was on goes away (a row
  // that became a note), focus lands on the picture, never on the page top.
  root.setAttribute('tabindex', '-1');

  const lead = h('p', 'fj-lead', leadSentences(folderName, picture.content_read).join(' '));
  lead.id = 'fileJanitorLead';
  root.appendChild(lead);

  const frame = h('section', 'fj-frame');
  frame.setAttribute('aria-labelledby', 'fileJanitorFrameTitle');
  const title = h('h3', 'fj-frame-title', frameTitle(folderName));
  title.id = 'fileJanitorFrameTitle';
  frame.appendChild(title);

  const filed = h('div', 'fj-box fj-filed');
  const filedTitle = sectionTitle(h, 'Filed');
  filedTitle.id = 'fileJanitorFiledTitle';
  filed.appendChild(filedTitle);
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
  // A row whose files were all placed is not loose any more, but it stays drawn so
  // the choice can be changed back (R20); so does a row Ori was told to stop asking
  // about (R22).
  const rows = picture.loose.some(row => row.count > 0) || (ctx.keptRows || []).length > 0;
  const looseTitle = sectionTitle(
    h,
    'Still loose in ' + folderName + (loose > 0 ? ' · ' + loose : '')
  );
  looseTitle.id = 'fileJanitorLooseTitle';
  if (loose > 0 || rows) frame.appendChild(looseSection(h, picture, looseTitle, ctx));
  const trashTitle = sectionTitle(h, 'Trash · ' + picture.trash.count);
  trashTitle.id = 'fileJanitorTrashTitle';
  if (picture.trash.count > 0) frame.appendChild(trashBox(h, picture, trashTitle, ctx));
  if (ctx.plan && !ctx.plan.over_limit && Number.isSafeInteger(ctx.plan.move_count))
    root.appendChild(
      decisionSummary(h, picture, ctx.plan, folderName, {
        filed: filedTitle,
        loose: looseTitle,
        trash: trashTitle
      })
    );
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

// One candidate can appear in its destination, its pair or bundle, and a paged
// list simultaneously. The rendering location is part of the control identity.
function fileRow(h, className, file, location, fallback) {
  const row = h('li', className);
  row.id = 'fileJanitorFileRow-' + location + '--' + file.id;
  row.setAttribute('data-fj-file-row', fallback);
  row.setAttribute('tabindex', '-1');
  return row;
}

function fileChoice(h, row, file, ctx) {
  const select = h('select', 'fj-row-menu fj-file-choice');
  select.id = row.id + '-choice';
  select.setAttribute('aria-label', 'Filing choice for ' + file.name);
  select.disabled = !ctx.editsAvailable;
  const prompt = h('option', '', 'Filing choice');
  prompt.value = '';
  prompt.selected = true;
  prompt.disabled = true;
  select.appendChild(prompt);
  for (const option of fileMenuOptions(file, ctx.destinations)) {
    const node = h('option', '', option.label);
    node.value = option.value;
    select.appendChild(node);
  }
  select.addEventListener('change', () => {
    const choice = parseFileChoice(select.value);
    if (choice) ctx.onFileChoice?.(file, choice);
  });
  return select;
}

export function parseFileChoice(value) {
  if (value === 'leave' || value === 'stop' || value === 'trash') return { kind: value };
  if (String(value).startsWith('move:')) return { kind: 'move', category: String(value).slice(5) };
  return null;
}

// A file the plan says will get a new name (a file of that name is already in the
// destination) is shown under the new name, tagged "renamed" (R7). The plan check
// is a read of disk, so the names arrive after the picture; until then, and for a
// file the plan does not cover, the name is the file's own. Any file listed here
// can be clicked to show the choices beside it, in that rendering only.
function nameList(h, files, className, ctx, { location, fallback, planned: usePlan = false }) {
  const list = h('ul', className);
  for (const file of files) {
    const planned = usePlan ? ctx?.plan?.items?.[file.id] : undefined;
    const renamed = Boolean(planned?.renamed && planned.final_name);
    const shown = renamed ? planned.final_name : file.name;
    const selectable = Boolean(ctx?.onSelectFile);
    const item = fileRow(h, 'fj-file', file, location, fallback);
    if (!selectable) setName(h, item, shown);
    if (selectable) {
      const control = setName(h, h('button', 'fj-file-name'), shown);
      control.type = 'button';
      control.id = item.id + '-name';
      const expanded = ctx.selectedFile === item.id;
      control.setAttribute('aria-expanded', String(expanded));
      control.addEventListener('click', () => ctx.onSelectFile(item.id, !expanded));
      item.appendChild(control);
      if (expanded) {
        const choices = h('div', 'fj-inline-choice');
        choices.appendChild(fileChoice(h, item, file, ctx));
        const cancel = h('button', 'fj-btn', 'Cancel');
        cancel.type = 'button';
        cancel.id = item.id + '-cancel';
        cancel.addEventListener('click', () => ctx.onSelectFile(item.id, false));
        choices.appendChild(cancel);
        item.appendChild(choices);
      }
    }
    appendInspection(h, item, file, ctx);
    if (renamed) item.appendChild(h('span', 'fj-tag', 'renamed'));
    if (file.your_choice) item.appendChild(h('span', 'fj-tag', 'your choice'));
    // R36: a press that found the plan had changed moved nothing; the files whose
    // plan differs from what the person looked at say so, until the next change.
    if (ctx?.changedIds?.has(file.id))
      item.appendChild(h('span', 'fj-tag fj-tag-changed', CHANGED_TAG));
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
  card.appendChild(h('p', 'fj-folder-destination', folder.destination));
  if (unticked) card.appendChild(h('p', 'fj-not-filed', 'Not filed'));
  if (folder.count > 0) {
    card.appendChild(
      nameList(h, folder.files, 'fj-folder-files', ctx, {
        location: FILED + '-' + folder.category,
        fallback: 'fileJanitorFiledTitle',
        planned: true
      })
    );
    if (folder.count > folder.files.length)
      card.appendChild(
        moreButton(h, ctx, FILED, folder.category, folder.files.length, folder.count)
      );
  }
  if (folder.unfiled_count > 0) {
    if (folder.count > 0) card.appendChild(h('p', 'fj-not-filed', 'Not filed'));
    card.appendChild(
      nameList(h, folder.unfiled_files, 'fj-folder-files', ctx, {
        location: UNFILED + '-' + folder.category,
        fallback: 'fileJanitorFiledTitle'
      })
    );
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

// What the one menu on a question row can do (R19), in this order: leave the files
// where they are, leave them and stop asking, then one entry per destination folder.
// A row menu never offers Trash. When the row's files are in several folders none of
// the choices describes the row, so a disabled line says so and "Leave here" stays
// selectable.
export function rowMenuValue(row) {
  const placed = row?.placed_count || 0;
  if (placed === 0) return 'leave';
  if (placed === row.count && row.placed_category) return 'move:' + row.placed_category;
  return 'mixed';
}

export function rowMenuOptions(destinations, value = 'leave') {
  const options = [
    { value: 'leave', label: 'Leave here' },
    { value: 'stop', label: 'Leave here, stop asking' }
  ];
  for (const destination of destinations || [])
    options.push({ value: 'move:' + destination.id, label: 'Move to ' + destination.label });
  // Some placed, some still loose or in Trash, or placed in different folders: no
  // single choice describes the row, and the note below it says exactly what.
  if (value === 'mixed') options.push({ value: 'mixed', label: 'Mixed choices', disabled: true });
  return options;
}

export function parseChoice(value) {
  if (value === 'leave' || value === 'stop') return { kind: value };
  if (String(value).startsWith('move:')) return { kind: 'move', category: String(value).slice(5) };
  return null;
}

// R20: the row stays visible with a short note, so the choice can be changed back.
export function rowNote(row, ctx) {
  const placed = row?.placed_count || 0;
  const trash = row?.trash_count || 0;
  const parts = [];
  const folder = (ctx?.destinations || []).find(item => item.id === row?.placed_category);
  const where = folder
    ? (ctx?.filedRoot || 'Filed') + '/' + (folder.folder_name || folder.label)
    : '';
  if (placed > 0) {
    if (placed === row.count && where) parts.push('All in ' + where);
    else if (where) parts.push(placed + ' in ' + where);
    else parts.push(placed + ' placed in other folders');
  }
  if (trash > 0) parts.push(trash + ' in Trash');
  return parts.join(' · ');
}

// R23: what can be done with one file, by where it is now. Moving to a folder is
// always offered except to the folder it is already filed in; "Leave here" only
// means something for a file that would otherwise move or be trashed.
export function fileMenuOptions(file, destinations) {
  const options = [];
  for (const destination of destinations || []) {
    if (file?.placement === FILED && file.category === destination.id) continue;
    options.push({ value: 'move:' + destination.id, label: 'Move to ' + destination.label });
  }
  if (file?.placement === FILED || file?.placement === TRASH)
    options.push({ value: 'leave', label: 'Leave here' });
  options.push({ value: 'stop', label: 'Leave here, stop asking' });
  if (file?.placement !== TRASH) options.push({ value: 'trash', label: 'Move to Trash' });
  return options;
}

// ------------------------------------------------------- pairs and bundles

// R3 of the pair card (R27): sizes and dates only. Ori does not compare what is
// inside files, so no sentence here may say two files are "duplicates" or
// "identical" (tests render every state and look for those words).
export const PAIR_BANNED_WORDS = ['duplicate', 'identical'];

export function formatSize(bytes) {
  const size = Number(bytes);
  if (!Number.isFinite(size) || size < 0) return '';
  if (size < 1024) return size + ' B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = size / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return value.toFixed(value >= 10 ? 0 : 1).replace(/\.0$/, '') + ' ' + units[unit];
}

// The calendar day a file was last changed, as the person would read it.
export function dayOf(iso) {
  const time = Date.parse(iso || '');
  if (!Number.isFinite(time)) return '';
  const date = new Date(time);
  return date.getFullYear() + '-' + (date.getMonth() + 1) + '-' + date.getDate();
}

export function formatDay(iso) {
  const time = Date.parse(iso || '');
  if (!Number.isFinite(time)) return '';
  return new Date(time).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  });
}

// One hint line under a pair card, from size and date alone (R27). A file whose date
// is not known is never claimed to share a day with another.
export function pairHint(files) {
  const list = (files || []).filter(Boolean);
  if (list.length < 2) return '';
  if (list.some(file => file.size !== list[0].size)) return 'Different size';
  const days = list.map(file => dayOf(file.modified_at));
  const sameDay = days[0] !== '' && days.every(day => day === days[0]);
  return sameDay ? 'Same size, same day' : 'Same size, different day';
}

// R28: a per-file Trash mark is offered next to a file only when its name ends in a number
// in brackets or in "copy" (not "v2" or "version 2": the higher version may be the
// one to keep), the card has exactly one file with no ending, and the two files are
// the same size in bytes. A file that is not loose has no such button: it already
// went somewhere. Pure, so the whole table is tested.
export function trashCopyAllowed(file, files) {
  if (!file || file.placement !== LOOSE) return false;
  if (file.ending !== 'number' && file.ending !== 'copy') return false;
  const originals = (files || []).filter(item => item.ending === 'none');
  if (originals.length !== 1) return false;
  return Number.isFinite(file.size) && file.size === originals[0].size;
}

// R33: when the files of a bundle no longer share one destination, the card says so
// in plain words ("Split: 4 stay here, 1 in Filed/Audio"). Empty while they do.
export function bundleNote(bundle, ctx) {
  const {
    count,
    loose_count: loose = 0,
    placed_count: placed = 0,
    trash_count: trash = 0
  } = bundle || {};
  const folder = (ctx?.destinations || []).find(item => item.id === bundle?.placed_category);
  const where = folder
    ? (ctx?.filedRoot || 'Filed') + '/' + (folder.folder_name || folder.label)
    : '';
  if (placed === count && where) return 'All in ' + where;
  if (loose === count || count === 0) return '';
  const parts = [];
  if (loose > 0) parts.push(loose + (loose === 1 ? ' stays here' : ' stay here'));
  if (placed > 0) parts.push(placed + (where ? ' in ' + where : ' placed in other folders'));
  if (trash > 0) parts.push(trash + ' in Trash');
  return 'Split: ' + parts.join(', ');
}

export function bundleMenuValue(bundle) {
  if ((bundle?.loose_count || 0) === bundle?.count) return 'leave';
  if (bundle?.placed_count === bundle?.count && bundle?.placed_category)
    return 'move:' + bundle.placed_category;
  return 'mixed';
}

export const KEPT_NOTE = 'Ori will not ask about these again.';

// The menu of a row or a bundle: the same choices, one value in, one choice out.
function choiceMenu(h, { id, label, value, destinations, disabled, onChoice }) {
  const select = h('select', 'fj-row-menu');
  select.id = id;
  select.setAttribute('aria-label', label);
  select.disabled = disabled;
  for (const option of rowMenuOptions(destinations, value)) {
    const node = h('option', '', option.label);
    node.value = option.value;
    node.selected = option.value === value;
    if (option.disabled) node.disabled = true;
    select.appendChild(node);
  }
  select.addEventListener('change', () => {
    const choice = parseChoice(select.value);
    if (choice) onChoice(choice);
  });
  return select;
}

function rowMenu(h, row, ctx) {
  return choiceMenu(h, {
    id: 'fileJanitorRowMenu-' + row.key,
    label: looseLabel(row) + ': what to do with these files',
    value: rowMenuValue(row),
    destinations: ctx.destinations,
    disabled: !ctx.editsAvailable,
    onChoice: choice => ctx.onRowChoice?.(row, choice)
  });
}

// The native inspection control is independent of selecting the filename or
// changing its planned destination. It is absent without a local Finder.
function appendInspection(h, line, file, ctx) {
  if (!ctx?.onInspect) return;
  if (file.viewer) {
    const open = h('button', 'fj-link fj-reveal', 'Open');
    open.type = 'button';
    if (line.id) open.id = line.id + '-open';
    open.title = 'Open in ' + file.viewer;
    open.setAttribute('aria-label', 'Open ' + file.name + ' in ' + file.viewer);
    open.addEventListener('click', () => ctx.onInspect(file, 'open'));
    line.appendChild(open);
  }
  const reveal = h('button', 'fj-link fj-reveal', 'Show in Finder');
  reveal.type = 'button';
  if (line.id) reveal.id = line.id + '-reveal';
  reveal.title = 'Select this file in Finder. Press Space there for Quick Look.';
  reveal.setAttribute('aria-label', 'Show ' + file.name + ' in Finder');
  reveal.addEventListener('click', () => ctx.onInspect(file, 'reveal'));
  line.appendChild(reveal);
}

function pairLine(h, file, files, ctx, location, fallback) {
  const line = fileRow(h, 'fj-pair-line', file, location, fallback);
  line.appendChild(h('span', 'fj-pair-name', file.name));
  const rounded = formatSize(file.size);
  const hidesDifference = files.some(
    other => other.size !== file.size && formatSize(other.size) === rounded
  );
  const size = hidesDifference ? file.size.toLocaleString() + ' bytes' : rounded;
  const facts = [size, formatDay(file.modified_at)].filter(Boolean);
  if (facts.length) line.appendChild(h('span', 'fj-pair-facts', facts.join(' · ')));
  appendInspection(h, line, file, ctx);
  if (file.placement === TRASH) {
    line.appendChild(h('span', 'fj-pair-state', 'Marked for Trash'));
    const back = h('button', 'fj-link', 'Undo mark');
    back.type = 'button';
    back.id = line.id + '-undo';
    back.disabled = !ctx.editsAvailable;
    back.setAttribute('aria-label', 'Undo Trash mark for ' + file.name);
    back.addEventListener('click', () => ctx.onPutBack?.(file));
    line.appendChild(back);
  } else if (file.placement === FILED) {
    const folder = (ctx.destinations || []).find(item => item.id === file.category);
    const where = folder
      ? (ctx.filedRoot || 'Filed') + '/' + (folder.folder_name || folder.label)
      : 'a folder';
    line.appendChild(h('span', 'fj-pair-state', 'in ' + where));
  } else if (trashCopyAllowed(file, files)) {
    const trash = h('button', 'fj-link', 'Mark for Trash');
    trash.type = 'button';
    trash.id = line.id + '-mark';
    trash.disabled = !ctx.editsAvailable;
    trash.setAttribute('aria-label', 'Mark for Trash: ' + file.name);
    trash.addEventListener('click', () => ctx.onTrashCopy?.(file));
    line.appendChild(trash);
  }
  line.appendChild(fileChoice(h, line, file, ctx));
  return line;
}

// R26, R27: possible copies as one card per family: the file with no ending first,
// then each with an ending, and one hint line from size and date.
function pairCard(h, pair, ctx) {
  const card = h('article', 'fj-pair');
  card.setAttribute('data-fj-family', pair.family_id);
  const original = pair.files.find(file => file.ending === 'none') || pair.files[0];
  card.setAttribute('aria-label', 'Possible copies of ' + original.name);
  const list = h('ul', 'fj-pair-files');
  for (const file of pair.files)
    list.appendChild(
      pairLine(
        h,
        file,
        pair.more ? [] : pair.files,
        ctx,
        pair.family_id,
        'fileJanitorRowMenu-versions'
      )
    );
  card.appendChild(list);
  const hint = pairHint(pair.files);
  if (hint) card.appendChild(h('p', 'fj-pair-hint', hint));
  if (pair.more > 0)
    card.appendChild(moreButton(h, ctx, FAMILY, pair.family_id, pair.files.length, pair.count));
  return card;
}

// R31 to R33: music that shares a name apart from the extension is one bundle. One
// menu choice applies to every file in it and sends them all to one folder.
function bundleCard(h, bundle, ctx) {
  const card = h('article', 'fj-bundle');
  card.setAttribute('data-fj-bundle', bundle.bundle_id);
  card.appendChild(
    h(
      'h6',
      'fj-bundle-title',
      bundle.name + ' · ' + filesWord(bundle.count) + ' · moves or stays together'
    )
  );
  card.appendChild(
    choiceMenu(h, {
      id: 'fileJanitorBundleMenu-' + bundle.bundle_id,
      label: bundle.name + ': what to do with these ' + bundle.count + ' files',
      value: bundleMenuValue(bundle),
      destinations: ctx.destinations,
      disabled: !ctx.editsAvailable,
      onChoice: choice => ctx.onBundleChoice?.(bundle, choice)
    })
  );
  const note = bundleNote(bundle, ctx);
  if (note) card.appendChild(h('p', 'fj-row-note fj-bundle-note', note));
  card.appendChild(
    nameList(h, bundle.files, 'fj-loose-files', ctx, {
      location: bundle.bundle_id,
      fallback: 'fileJanitorBundleMenu-' + bundle.bundle_id
    })
  );
  if (bundle.more > 0)
    card.appendChild(
      moreButton(h, ctx, BUNDLE, bundle.bundle_id, bundle.files.length, bundle.count)
    );
  return card;
}

function looseSection(h, picture, title, ctx) {
  const section = h('div', 'fj-box fj-loose');
  section.appendChild(title);
  for (const row of picture.loose) {
    if (!row.count) continue;
    const box = h('div', 'fj-loose-row');
    box.setAttribute('data-fj-group', row.group_id);
    // The heading counts what is listed under it: the files still loose. A placed
    // row keeps its heading and says where its files went in the note below.
    box.appendChild(
      h(
        'h5',
        'fj-loose-label',
        looseLabel(row) + ' · ' + (row.loose_count > 0 ? row.loose_count : row.count)
      )
    );
    box.appendChild(rowMenu(h, row, ctx));
    const note = rowNote(row, ctx);
    if (note) box.appendChild(h('p', 'fj-row-note', note));
    const why = h('details', 'fj-why');
    why.appendChild(h('summary', 'fj-why-toggle', 'Why?'));
    why.appendChild(h('p', 'fj-why-text', LOOSE_WHY[row.key] || LOOSE_WHY.unmatched));
    box.appendChild(why);
    for (const pair of row.pairs || []) box.appendChild(pairCard(h, pair, ctx));
    for (const bundle of row.bundles || []) box.appendChild(bundleCard(h, bundle, ctx));
    if (row.singles?.length)
      box.appendChild(
        nameList(h, row.singles, 'fj-loose-files', ctx, {
          location: row.group_id,
          fallback: 'fileJanitorRowMenu-' + row.key
        })
      );
    if (row.more > 0)
      box.appendChild(moreButton(h, ctx, LOOSE, row.group_id, row.singles.length, row.loose_count));
    section.appendChild(box);
  }
  // R22: a row the person asked Ori to stop asking about is no longer waiting, so
  // the picture does not hold its files. It stays as a note with a way back.
  for (const kept of ctx.keptRows || []) {
    const box = h('div', 'fj-loose-row fj-kept-row');
    box.setAttribute('data-fj-kept', kept.key);
    box.appendChild(
      h('h5', 'fj-loose-label', (LOOSE_LABELS[kept.key] || kept.key) + ' · ' + kept.count)
    );
    box.appendChild(h('p', 'fj-row-note', KEPT_NOTE));
    const again = h('button', 'fj-link', 'Review again');
    again.type = 'button';
    again.id = 'fileJanitorReviewAgain-' + kept.key;
    again.addEventListener('click', () => ctx.onReviewAgain?.(kept));
    box.appendChild(again);
    section.appendChild(box);
  }
  if (picture.left_out.count > 0) {
    const box = h('div', 'fj-loose-row');
    box.appendChild(h('h5', 'fj-loose-label', LEFT_OUT_LABEL + ' · ' + picture.left_out.count));
    box.appendChild(
      nameList(h, picture.left_out.files, 'fj-loose-files', ctx, {
        location: LEFT_OUT,
        fallback: title.id
      })
    );
    if (picture.left_out.count > picture.left_out.files.length)
      box.appendChild(
        moreButton(h, ctx, LEFT_OUT, '', picture.left_out.files.length, picture.left_out.count)
      );
    section.appendChild(box);
  }
  return section;
}

function trashBox(h, picture, title, ctx) {
  const box = h('div', 'fj-box fj-trash');
  box.appendChild(title);
  box.appendChild(
    nameList(h, picture.trash.files, 'fj-loose-files', ctx, {
      location: TRASH,
      fallback: title.id
    })
  );
  if (picture.trash.count > picture.trash.files.length)
    box.appendChild(moreButton(h, ctx, TRASH, '', picture.trash.files.length, picture.trash.count));
  return box;
}

function couldNotFile(h, picture, ctx) {
  const box = h('div', 'fj-box fj-could-not');
  box.appendChild(h('h4', 'fj-box-title', 'Could not be filed · ' + picture.could_not_file.count));
  const list = h('ul', 'fj-loose-files');
  for (const file of picture.could_not_file.files) {
    const item = setName(h, h('li', 'fj-file'), file.name);
    if (file.reason) item.appendChild(h('span', 'fj-file-reason', file.reason));
    // R25: a file an earlier attempt left behind can be tried again. It comes back
    // unticked, so trying again never files anything by itself.
    if (file.can_retry) {
      const retry = h('button', 'fj-link', 'Try again');
      retry.type = 'button';
      retry.id = 'fileJanitorRetry-' + file.id;
      retry.setAttribute('aria-label', 'Try ' + file.name + ' again');
      retry.addEventListener('click', () => ctx.onRetry?.(file));
      item.appendChild(retry);
    }
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
    const location = 'page-' + more.section + '-' + more.key;
    const fallback = panel.id;
    if (more.section === FAMILY) {
      const list = h('ul', 'fj-pair-files');
      const complete = more.files.length === more.total ? more.files : [];
      for (const file of more.files)
        list.appendChild(pairLine(h, file, complete, ctx, location, fallback));
      panel.appendChild(list);
    } else {
      const choices = more.section === COULD_NOT_FILE ? undefined : ctx;
      panel.appendChild(
        nameList(h, more.files, 'fj-loose-files', choices, {
          location,
          fallback,
          planned: more.section === FILED
        })
      );
    }
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
    folderLabel,
    folderCounts,
    changeAnnouncement,
    rowMenuValue,
    rowMenuOptions,
    rowNote,
    fileMenuOptions,
    fileButtonModel,
    trashTickLabel,
    OVER_LIMIT_NOTE
  };
  document.dispatchEvent(new CustomEvent('ori:filing-picture-ready'));
}
