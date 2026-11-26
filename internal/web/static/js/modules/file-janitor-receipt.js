// file-janitor-receipt.js — what one press of File actually did, and what Undo all
// did about it.
//
// This module only draws. The console reads the receipt and the Undo all result
// from the server, owns every request and decides what may be pressed; here every
// number is taken as given (the server computes it from what happened, never from
// the plan), and every name reaches the page as text, never as markup.

const BANNED_WORDS = ['batch', 'candidate', 'proposal', 'exact', 'explicitly', 'draft'];
export { BANNED_WORDS };

export function filesPhrase(count) {
  return count === 1 ? '1 file' : count + ' files';
}

// R43: the title says how many files were actually filed. Files that went to the
// Trash are said beside it, never counted as filed.
export function receiptTitle(model) {
  const filed = model?.filed || 0;
  const trashed = model?.trashed || 0;
  // An answer that does not say what kind of change it was (an older reply) is
  // never called "filed".
  const completed = model?.completed || 0;
  const parts = [];
  if (filed) parts.push(filesPhrase(filed) + ' filed');
  if (trashed) parts.push(trashed.toLocaleString() + ' moved to Trash');
  if (completed) parts.push(completed + (completed === 1 ? ' change' : ' changes') + ' completed');
  return parts.length ? parts.join(' · ') : 'Nothing was filed';
}

// R43: where the files are, and how many stay loose.
export function receiptWhere(model, ctx) {
  const folder = ctx?.folderName || 'the folder';
  const filing = ctx?.filingRoot || 'Filed';
  const parts = [];
  const sent = model?.sent || 0;
  if (sent > 0) {
    // FR 30: "14 went to Documents. 29 are in Downloads/Filed."
    for (const send of model.sends || [])
      parts.push(
        (send.count === 1 ? '1 went to ' : send.count + ' went to ') + send.receiverName + '.'
      );
    const unnamed = sent - (model.sends || []).reduce((total, send) => total + send.count, 0);
    if (unnamed > 0) parts.push(unnamed + ' went to another workspace.');
    const here = (model.filed || 0) - sent;
    if (here > 0)
      parts.push((here === 1 ? '1 is in ' : here + ' are in ') + folder + '/' + filing + '.');
  } else if ((model?.filed || 0) > 0) parts.push('They are in ' + folder + '/' + filing + '.');
  if (typeof model?.looseNow === 'number') {
    const loose = model.looseNow;
    parts.push(
      loose === 0
        ? 'No files stay loose in ' + folder + '.'
        : loose === 1
          ? '1 file stays loose in ' + folder + '.'
          : filesPhrase(loose) + ' stay loose in ' + folder + '.'
    );
  }
  return parts.join(' ');
}

function directoryOf(destination) {
  const text = String(destination || '');
  const cut = text.lastIndexOf('/');
  return cut > 0 ? text.slice(0, cut) : '';
}

// The receipt as the server sent it, or built from the answer to the press when the
// receipt could not be read. Either way the numbers come from outcomes, not a plan.
export function receiptModelFromReceipt(receipt) {
  return {
    filingId: receipt.filing_id,
    state: receipt.state || 'done',
    filed: receipt.filed || 0,
    trashed: receipt.trashed || 0,
    completed: 0,
    sent: receipt.sent || 0,
    sends: (receipt.sends || []).map(item => ({
      workspaceId: item.receiver_workspace_id,
      receiverName: item.receiver_name || 'Another workspace',
      count: item.count || 0,
      link: typeof item.receiver_link === 'string' ? item.receiver_link : ''
    })),
    byDestination: (receipt.by_destination || []).map(item => ({
      destination: item.destination,
      count: item.count
    })),
    notMoved: (receipt.not_moved || []).map(item => ({ name: item.name, reason: item.reason })),
    notMovedTotal: receipt.not_moved_total || 0,
    notAttempted: receipt.not_attempted || 0,
    looseNow: typeof receipt.loose_now === 'number' ? receipt.loose_now : null,
    undoAvailable: Boolean(receipt.undo_available),
    historyTrimmed: Boolean(receipt.history_trimmed)
  };
}

export function receiptModelFromResult(result) {
  const outcomes = Array.isArray(result?.outcomes) ? result.outcomes : [];
  const byDestination = new Map();
  const notMoved = [];
  let filed = 0;
  let trashed = 0;
  let completed = 0;
  let sent = 0;
  for (const outcome of outcomes) {
    if (outcome.result === 'applied' && outcome.operation === 'trash') trashed += 1;
    else if (outcome.result === 'applied' && outcome.operation === 'send') {
      // Sent to another workspace; the receipt read names which.
      filed += 1;
      sent += 1;
    } else if (outcome.result === 'applied' && outcome.operation === 'move') {
      filed += 1;
      const folder = directoryOf(outcome.destination);
      if (folder) byDestination.set(folder, (byDestination.get(folder) || 0) + 1);
    } else if (outcome.result === 'applied') completed += 1;
    else if (outcome.result === 'failed' || outcome.result === 'stale')
      notMoved.push({
        name: outcome.name || 'A file',
        reason:
          outcome.message ||
          (outcome.result === 'stale'
            ? 'This file changed after you looked, so Ori left it alone.'
            : 'Ori could not move this file.')
      });
  }
  return {
    filingId: result?.filing_id || '',
    state: result?.interrupted ? 'interrupted' : 'done',
    replayed: Boolean(result?.replayed),
    filed,
    trashed,
    completed,
    sent,
    sends: [],
    byDestination: [...byDestination].map(([destination, count]) => ({ destination, count })),
    notMoved,
    notMovedTotal: notMoved.length,
    notAttempted: result?.not_attempted || 0,
    looseNow: null,
    // Every file filed or trashed is in the journal and can be put back until it is;
    // if the server disagrees, pressing Undo all says so and nothing moves.
    undoAvailable: filed + trashed > 0,
    historyTrimmed: false
  };
}

function builder(doc) {
  return (tag, className, text) => {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
}

function actionButton(h, id, label, className, onClick, disabled) {
  const control = h('button', className, label);
  control.type = 'button';
  control.id = id;
  control.disabled = Boolean(disabled);
  control.addEventListener('click', onClick);
  return control;
}

// R43 to R45: the receipt. The panel is a labelled group the console moves focus to,
// so a keyboard user lands on what happened.
export function renderReceipt(doc, model, ctx = {}) {
  const h = builder(doc);
  const panel = h('section', 'fj-results fj-receipt');
  panel.id = 'fileJanitorReceipt';
  panel.setAttribute('role', 'group');
  panel.setAttribute('tabindex', '-1');
  panel.setAttribute('aria-labelledby', 'fileJanitorResultsSummary');

  const title = h('h3', 'fj-results-summary', receiptTitle(model) + '.');
  title.id = 'fileJanitorResultsSummary';
  panel.appendChild(title);
  if (model.replayed)
    panel.appendChild(
      h('p', 'fj-results-replayed', 'Already completed earlier; nothing ran again.')
    );

  if (model.state === 'interrupted')
    panel.appendChild(
      h(
        'p',
        'fj-receipt-note',
        'Ori stopped before it finished' +
          (model.notAttempted ? ': ' + filesPhrase(model.notAttempted) + ' were not tried.' : '.')
      )
    );
  const where = receiptWhere(model, ctx);
  if (where) panel.appendChild(h('p', 'fj-receipt-where', where));

  // One row per receiving workspace, with Open <receiver>, above the Filed rows
  // (FR 31).
  if ((model.sends || []).length) {
    const sends = h('ul', 'fj-receipt-rows fj-receipt-sends');
    for (const send of model.sends) {
      const row = h('li', 'fj-receipt-row fj-receipt-send');
      row.appendChild(h('span', 'fj-receipt-count', String(send.count)));
      const arrow = h('span', 'fj-receipt-arrow', '→');
      arrow.setAttribute('aria-hidden', 'true');
      row.appendChild(arrow);
      const where = h('span', 'fj-receipt-to');
      where.appendChild(h('span', 'fj-receipt-to-name', send.receiverName));
      where.appendChild(h('span', 'fj-receipt-to-help', ' workspace, top level'));
      row.appendChild(where);
      if (send.link && send.link.startsWith('/workspaces/')) {
        const open = h('a', 'fj-btn fj-btn-secondary fj-receipt-open', 'Open ' + send.receiverName);
        open.href = send.link;
        row.appendChild(open);
      }
      sends.appendChild(row);
    }
    panel.appendChild(sends);
  }

  // One row per destination, then the Trash on its own row.
  const rows = h('ul', 'fj-receipt-rows');
  for (const row of model.byDestination || [])
    rows.appendChild(h('li', 'fj-receipt-row', row.count + ' → ' + row.destination));
  if ((model.trashed || 0) > 0)
    rows.appendChild(h('li', 'fj-receipt-row', model.trashed + ' → Trash'));
  if (rows.children.length) panel.appendChild(rows);

  // R43: every file that did not move, with its reason.
  const notMoved = model.notMoved || [];
  if (model.notMovedTotal > 0) {
    const section = h('section', 'fj-receipt-notmoved');
    const heading = h(
      'h4',
      'fj-receipt-notmoved-title',
      model.notMovedTotal === 1
        ? '1 file was not moved'
        : model.notMovedTotal + ' files were not moved'
    );
    section.appendChild(heading);
    const list = h('ul', 'fj-results-list');
    for (const item of notMoved) {
      const line = h('li', 'fj-results-item fj-results-failed');
      line.appendChild(h('span', 'fj-results-name', item.name));
      line.appendChild(h('span', 'fj-results-state', ' — ' + item.reason));
      list.appendChild(line);
    }
    section.appendChild(list);
    if (model.notMovedTotal > notMoved.length)
      section.appendChild(
        h('p', 'fj-sub', 'and ' + (model.notMovedTotal - notMoved.length) + ' more')
      );
    panel.appendChild(section);
  }
  if (model.historyTrimmed)
    panel.appendChild(
      h(
        'p',
        'fj-sub',
        'Ori no longer keeps the whole record of this filing, so this list may be short.'
      )
    );

  const actions = h('div', 'fj-results-actions');
  if (model.undoAvailable)
    actions.appendChild(
      actionButton(
        h,
        'fileJanitorUndoAll',
        ctx.busy ? 'Putting files back…' : 'Undo all',
        'fj-btn fj-btn-secondary',
        () => ctx.onUndoAll?.(model),
        ctx.busy
      )
    );
  actions.appendChild(
    actionButton(
      h,
      'fileJanitorReceiptDone',
      'Done',
      'fj-btn fj-btn-primary',
      () => ctx.onDone?.(),
      ctx.busy
    )
  );
  panel.appendChild(actions);
  return panel;
}

// Where a file is after Undo all could not put it back, in words (R49).
export function locationText(outcome) {
  switch (outcome?.location) {
    case 'filed':
      return outcome.destination
        ? 'It is still in ' + outcome.destination + '.'
        : 'It is still where it was filed.';
    case 'trash':
      return 'It is still in the Trash.';
    case 'folder':
      return 'A file with the original name is in the folder.';
    case 'sent':
      return 'It is still in the workspace it was sent to.';
    default:
      return 'Ori cannot find it.';
  }
}

export function undoneTitle(result) {
  const back = result?.back || 0;
  if (back === 0) return 'No files were put back';
  return back === 1 ? '1 file is back where it was' : back + ' files are back where they were';
}

// R49, R52: the Undone view. The one button runs "forget, then scan" in the console.
export function renderUndone(doc, result, ctx = {}) {
  const h = builder(doc);
  const panel = h('section', 'fj-results fj-undone');
  panel.id = 'fileJanitorUndone';
  panel.setAttribute('role', 'group');
  panel.setAttribute('tabindex', '-1');
  panel.setAttribute('aria-labelledby', 'fileJanitorUndoneTitle');
  const title = h('h3', 'fj-results-summary', undoneTitle(result) + '.');
  title.id = 'fileJanitorUndoneTitle';
  panel.appendChild(title);
  if (result.replayed)
    panel.appendChild(h('p', 'fj-results-replayed', 'This was already done; nothing ran again.'));
  if (result.interrupted)
    panel.appendChild(
      h('p', 'fj-receipt-note', 'Ori stopped before it finished putting files back.')
    );

  const notBack = (result.outcomes || []).filter(
    outcome => outcome.result === 'failed' || outcome.result === 'interrupted'
  );
  if (notBack.length) {
    const section = h('section', 'fj-receipt-notmoved');
    section.appendChild(
      h(
        'h4',
        'fj-receipt-notmoved-title',
        notBack.length === 1
          ? '1 file could not be put back'
          : notBack.length + ' files could not be put back'
      )
    );
    const list = h('ul', 'fj-results-list');
    for (const outcome of notBack) {
      const line = h('li', 'fj-results-item fj-results-failed');
      line.appendChild(h('span', 'fj-results-name', outcome.name));
      line.appendChild(
        h(
          'span',
          'fj-results-state',
          ' — ' + (outcome.message || 'Ori could not put it back.') + ' ' + locationText(outcome)
        )
      );
      list.appendChild(line);
    }
    section.appendChild(list);
    panel.appendChild(section);
  }

  const actions = h('div', 'fj-results-actions');
  if ((result.back || 0) > 0)
    actions.appendChild(
      actionButton(
        h,
        'fileJanitorScanAgain',
        ctx.busy ? 'Scanning…' : 'Scan again to review them',
        'fj-btn fj-btn-primary',
        () => ctx.onScanAgain?.(result),
        ctx.busy
      )
    );
  actions.appendChild(
    actionButton(
      h,
      'fileJanitorUndoneDone',
      'Done',
      'fj-btn fj-btn-secondary',
      () => ctx.onDone?.(),
      ctx.busy
    )
  );
  panel.appendChild(actions);
  return panel;
}

// R46: History's one line for the most recent filing, above the per-file list.
export function lastFilingLine(summary, when) {
  if (!summary) return '';
  const moved = (summary.filed || 0) + (summary.trashed || 0);
  const parts = ['Last filing', moved === 1 ? '1 file' : moved + ' files'];
  if (when) parts.push(when);
  if (summary.undone) parts.push('put back');
  return parts.join(' · ');
}

export function renderLastFiling(doc, summary, ctx = {}) {
  if (!summary) return null;
  const h = builder(doc);
  const line = h('p', 'fj-last-filing', lastFilingLine(summary, ctx.when));
  line.id = 'fileJanitorLastFiling';
  if (summary.undo_available) {
    line.appendChild(h('span', 'fj-last-filing-sep', ' · '));
    line.appendChild(
      actionButton(
        h,
        'fileJanitorLastFilingUndo',
        ctx.busy ? 'Putting files back…' : 'Undo all',
        'fj-link',
        () => ctx.onUndoAll?.(summary),
        ctx.busy
      )
    );
  }
  return line;
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  window.FileJanitorReceipt = {
    renderReceipt,
    renderUndone,
    renderLastFiling,
    receiptModelFromReceipt,
    receiptModelFromResult
  };
  document.dispatchEvent(new CustomEvent('ori:filing-receipt-ready'));
}
