/**
 * hq-note-inventory-view.js — the note inventory's collections, slots and
 * paper inspector, built as DOM from a plain model.
 *
 * The controller (hq-note-inventory.js) decides what is known and selected;
 * these builders only draw it. Every name and note text goes in as text,
 * never as markup, and every control carries:
 *   - data-note-inventory-action / -value: what a click does;
 *   - data-note-inventory-control: a stable name so focus survives a repaint.
 */

import { inventoryPicture } from './hq-note-inventory-art.js';
import { itemArt, kindLabel } from './hq-note-inventory-items.js';
import { previewParagraphs } from './personal-assistant-second-brain.js';

function el(doc, tag, className = '', text = '') {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text) node.textContent = text;
  return node;
}

function control(node, name, action = '', value = '') {
  node.setAttribute('data-note-inventory-control', name);
  if (action) node.setAttribute('data-note-inventory-action', action);
  if (value !== '') node.setAttribute('data-note-inventory-value', value);
  return node;
}

function button(doc, className, text, name, action, value = '') {
  const node = el(doc, 'button', className, text);
  node.type = 'button';
  return control(node, name, action, value);
}

const pad = n => String(n).padStart(2, '0');
// An exact count reads like the slot numbers ("03"); a lower bound stays "200+".
const padCount = text => (/^\d+$/.test(text) ? pad(text) : text);

// ---- collections ---------------------------------------------------------------

// The collection tabs: each says its count only when it is known, and a lower
// bound with "+" when the server stopped listing.
export function tabsNode(doc, { tabs, active }) {
  const nav = el(doc, 'div', 'hq-note-inventory__tabs');
  nav.setAttribute('role', 'group');
  nav.setAttribute('aria-label', 'Collections');
  for (const tab of tabs) {
    const node = button(
      doc,
      'hq-note-inventory__tab' + (tab.key === active ? ' is-active' : ''),
      '',
      'tab:' + tab.key,
      'tab',
      tab.key
    );
    node.setAttribute('aria-pressed', tab.key === active ? 'true' : 'false');
    node.append(el(doc, 'span', 'hq-note-inventory__tab-label', tab.label));
    if (tab.count) {
      const count = el(doc, 'span', 'hq-note-inventory__tab-count', tab.count);
      node.append(count);
      node.setAttribute(
        'aria-label',
        tab.label + ', ' + (tab.exact ? tab.count : 'at least ' + tab.count.replace('+', ''))
      );
    }
    nav.append(node);
  }
  return nav;
}

// ---- slots -----------------------------------------------------------------------

function slotNode(doc, item, { index, selected }) {
  const li = el(doc, 'li', 'hq-note-inventory__cell');
  const node = button(
    doc,
    'hq-note-inventory__slot' + (selected ? ' is-selected' : '') + ' is-' + item.kind,
    '',
    'item:' + item.id,
    'select',
    item.id
  );
  node.setAttribute('aria-pressed', selected ? 'true' : 'false');
  node.setAttribute('aria-label', [item.title, item.detail].filter(Boolean).join(', '));
  node.setAttribute('title', item.name);
  const number = el(doc, 'span', 'hq-note-inventory__slot-index', pad(index));
  number.setAttribute('aria-hidden', 'true');
  const art = el(doc, 'span', 'hq-note-inventory__slot-art');
  art.setAttribute('aria-hidden', 'true');
  art.innerHTML = inventoryPicture(itemArt(item));
  const mark = el(doc, 'span', 'hq-note-inventory__slot-mark');
  mark.setAttribute('aria-hidden', 'true');
  node.append(
    number,
    art,
    el(doc, 'span', 'hq-note-inventory__slot-title', item.title),
    el(doc, 'span', 'hq-note-inventory__slot-detail', item.detail || kindLabel(item.kind)),
    mark
  );
  li.append(node);
  return li;
}

function gridNode(doc, items, { selectedId, start = 1, label }) {
  const list = el(doc, 'ul', 'hq-note-inventory__grid');
  if (label) list.setAttribute('aria-label', label);
  items.forEach((item, i) =>
    list.append(slotNode(doc, item, { index: start + i, selected: item.id === selectedId }))
  );
  return list;
}

function noticeNode(doc, text, className = '') {
  return el(doc, 'p', 'hq-note-inventory__notice' + (className ? ' ' + className : ''), text);
}

// A section that is loading, could not be listed, or is empty says so in
// words; an empty Inbox is "Inbox empty", not "no notes".
function sectionStatusNodes(doc, group) {
  if (group.status === 'loading' || group.status === 'idle') {
    const busy = noticeNode(doc, 'Listing ' + group.label + '…', 'is-loading');
    busy.setAttribute('role', 'status');
    return [busy];
  }
  if (group.status === 'error') {
    const row = el(doc, 'div', 'hq-note-inventory__section-problem');
    row.append(noticeNode(doc, group.error || 'Ori could not list ' + group.label + '.'));
    row.append(
      button(
        doc,
        'hq-note-inventory__link-button',
        'Try again',
        'retry:' + group.key,
        'retry-section',
        group.key
      )
    );
    return [row];
  }
  if (!group.items.length) return [noticeNode(doc, group.empty, 'is-empty')];
  return [];
}

// The slots pane: in All notes, one short group per section; in a section,
// a page of its slots with Show more.
export function slotsNode(doc, model) {
  const pane = el(doc, 'section', 'hq-note-inventory__slots');
  pane.setAttribute('aria-label', model.heading);
  const head = el(doc, 'p', 'hq-note-inventory__eyebrow-row');
  head.append(el(doc, 'span', '', model.heading));
  if (model.countText) head.append(el(doc, 'span', '', ' / ' + padCount(model.countText)));
  pane.append(head);
  if (model.scope) pane.append(noticeNode(doc, model.scope, 'is-scope'));
  if (model.crumbs) pane.append(crumbsNode(doc, model.crumbs));

  for (const group of model.groups) {
    const block = el(doc, 'div', 'hq-note-inventory__group');
    block.setAttribute('data-note-inventory-group', group.key);
    if (model.groups.length > 1) {
      const title = el(doc, 'h3', 'hq-note-inventory__group-title');
      title.append(el(doc, 'span', '', group.label));
      if (group.countText)
        title.append(el(doc, 'span', 'hq-note-inventory__group-count', group.countText));
      block.append(title);
    }
    block.append(...sectionStatusNodes(doc, group));
    if (group.items.length) {
      block.append(
        gridNode(doc, group.items, {
          selectedId: model.selectedId,
          start: group.start || 1,
          label: group.label
        })
      );
    }
    if (group.seeAll) {
      block.append(
        button(
          doc,
          'hq-note-inventory__link-button',
          group.seeAll,
          'see:' + group.key,
          'tab',
          group.key
        )
      );
    }
    if (group.more) {
      block.append(
        button(doc, 'hq-note-inventory__more', group.more, 'more:' + group.key, 'more', group.key)
      );
    }
    if (group.partial) block.append(noticeNode(doc, group.partial, 'is-partial'));
    pane.append(block);
  }
  return pane;
}

// Where in a connected folder the slots are: its root and each folder opened.
function crumbsNode(doc, crumbs) {
  const nav = el(doc, 'nav', 'hq-note-inventory__crumbs');
  nav.setAttribute('aria-label', 'Folder');
  crumbs.forEach((crumb, i) => {
    if (i) {
      const sep = el(doc, 'span', 'hq-note-inventory__crumb-sep', '/');
      sep.setAttribute('aria-hidden', 'true');
      nav.append(sep);
    }
    if (i === crumbs.length - 1) {
      const here = el(doc, 'span', 'hq-note-inventory__crumb is-current', crumb.label);
      here.setAttribute('aria-current', 'location');
      nav.append(here);
    } else {
      nav.append(
        button(doc, 'hq-note-inventory__crumb', crumb.label, 'crumb:' + i, 'crumb', String(i))
      );
    }
  });
  return nav;
}

// ---- inspector -------------------------------------------------------------------

function handoffNodes(doc, model) {
  const row = el(doc, 'div', 'hq-note-inventory__handoffs');
  if (model.external) {
    const link = el(doc, 'a', 'hq-note-inventory__link-button', 'Open in Obsidian');
    link.href = model.external;
    control(link, 'obsidian');
    row.append(link);
  } else if (model.openFolder) {
    row.append(
      button(doc, 'hq-note-inventory__link-button', 'Open folder', 'open-folder', 'open-folder')
    );
  }
  if (model.folderNotice) row.append(noticeNode(doc, model.folderNotice));
  return row.childNodes.length ? [row] : [];
}

// The paper: the note's own words as plain paragraphs, never as markup.
function paperNode(doc, model) {
  const paper = el(doc, 'div', 'hq-note-inventory__paper');
  paper.append(el(doc, 'p', 'hq-note-inventory__paper-eyebrow', model.paperEyebrow));
  const state = model.state;
  if (state?.status === 'ready') {
    const title = String(state.preview?.title || '').trim();
    if (title && title !== model.item.title) {
      paper.append(el(doc, 'p', 'hq-note-inventory__paper-title', title));
    }
    let paragraphs = previewParagraphs(state.preview?.body);
    // The note's own first heading is already the title above; it is not
    // repeated as a raw "# …" line. Everything else stays exactly as written.
    const heading = paragraphs[0]?.match(/^#[ \t]+([^\n]+)$/);
    if (heading && [title, model.item.title].includes(heading[1].trim())) {
      paragraphs = paragraphs.slice(1);
    }
    if (paragraphs.length) {
      paragraphs.forEach(text => paper.append(el(doc, 'p', 'hq-note-inventory__paper-text', text)));
    } else {
      paper.append(el(doc, 'p', 'hq-note-inventory__paper-text is-quiet', 'This note is empty.'));
    }
    if (state.preview?.truncated) {
      paper.append(
        el(doc, 'p', 'hq-note-inventory__paper-more', 'Showing the first part of this note.')
      );
    }
    return paper;
  }
  const line = el(doc, 'p', 'hq-note-inventory__paper-text is-quiet', model.message);
  if (state?.status === 'loading' || model.checking) line.setAttribute('role', 'status');
  else if (['missing', 'error', 'yours'].includes(state?.status))
    line.setAttribute('role', 'alert');
  paper.append(line);
  return paper;
}

export function inspectorNode(doc, model) {
  const pane = el(doc, 'section', 'hq-note-inventory__inspector');
  pane.setAttribute('aria-label', 'Selected note');
  if (model.back) {
    pane.append(button(doc, 'hq-note-inventory__back', '← Back to ' + model.back, 'back', 'back'));
  }
  if (!model.item) {
    pane.append(el(doc, 'p', 'hq-note-inventory__eyebrow-row', 'Selected note'));
    pane.append(noticeNode(doc, model.empty, 'is-empty'));
    return pane;
  }
  const eyebrow = el(doc, 'p', 'hq-note-inventory__eyebrow-row');
  eyebrow.append(el(doc, 'span', '', 'Selected note'));
  if (model.index) eyebrow.append(el(doc, 'span', '', ' / ' + pad(model.index)));
  pane.append(eyebrow);
  const heading = el(doc, 'h3', 'hq-note-inventory__inspector-title', model.item.title);
  heading.tabIndex = -1;
  control(heading, 'inspector-title');
  pane.append(heading);
  if (!model.item.folder) pane.append(paperNode(doc, model));
  if (model.retry) {
    pane.append(
      button(doc, 'hq-note-inventory__link-button', 'Try again', 'retry-preview', 'retry-preview')
    );
  }
  pane.append(...handoffNodes(doc, model));
  const place = el(doc, 'p', 'hq-note-inventory__place');
  place.textContent = model.place.join(' / ');
  place.setAttribute('aria-label', 'In ' + model.place.join(', '));
  pane.append(place);
  return pane;
}

// ---- Quick note --------------------------------------------------------------------

// The Quick note form, in the inspector's place. It says where the note goes
// before anything is sent; it has no id, so it never collides with the HQ
// Overview's own capture field.
export function captureNode(doc, model) {
  const pane = el(doc, 'section', 'hq-note-inventory__inspector is-capture');
  pane.setAttribute('aria-label', 'Quick note');
  if (model.back) {
    pane.append(
      button(doc, 'hq-note-inventory__back', '← Back to ' + model.back, 'back', 'cancel-capture')
    );
  }
  pane.append(el(doc, 'p', 'hq-note-inventory__eyebrow-row', 'Quick note'));
  const form = el(doc, 'form', 'hq-note-inventory__capture');
  form.setAttribute('data-note-inventory-capture', '');
  form.noValidate = true;
  const label = el(doc, 'label', 'hq-note-inventory__capture-label');
  label.append(el(doc, 'span', 'hq-note-inventory__capture-where', model.label));
  const field = el(doc, 'textarea', 'hq-note-inventory__capture-field');
  control(field, 'draft');
  field.value = model.text;
  field.rows = 5;
  field.maxLength = 4000;
  field.placeholder = 'A thought to keep…';
  field.readOnly = model.busy || model.stranded;
  label.append(field);
  form.append(label);
  const row = el(doc, 'div', 'hq-note-inventory__capture-actions');
  if (model.stranded) {
    row.append(
      button(doc, 'hq-note-inventory__quick', 'Discard draft', 'discard-draft', 'discard-draft')
    );
  } else {
    const save = el(
      doc,
      'button',
      'hq-note-inventory__quick',
      model.busy ? 'Saving…' : 'Save to Inbox'
    );
    save.type = 'submit';
    control(save, 'save');
    save.disabled = model.busy;
    row.append(save);
  }
  const cancel = button(
    doc,
    'hq-note-inventory__link-button',
    'Cancel',
    'cancel-capture',
    'cancel-capture'
  );
  cancel.disabled = model.busy;
  row.append(cancel);
  form.append(row);
  if (model.stranded) {
    form.append(
      noticeNode(
        doc,
        'Copy this draft if you want to keep it. Ori won’t send it to another folder.'
      )
    );
  }
  if (model.problem) {
    const problem = noticeNode(doc, model.problem, 'is-problem');
    problem.setAttribute('role', 'alert');
    form.append(problem);
  }
  pane.append(form);
  return pane;
}
