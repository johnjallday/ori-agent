// Protect your folders: the host-rendered choice between Time Machine and a
// restic repository, before any Backup & Recovery workspace exists. Opening it
// reads only this server's fixed provider options: no drive, folder or OS
// query, no backup read, and nothing is created until an option is confirmed.

export const PROTECT_ROUTE_PARAM = 'protect';
export const PRESELECT_KEY = 'ori.backupRecovery.preselect';
const BLUEPRINT = 'backup-and-recovery';

export const RESTIC_NEEDS = [
  'An external drive with free space',
  'restic 0.19.1, installed by you',
  'An encrypted repository you prepare on that drive',
  'A Vault secret that holds its password',
  'About 30 minutes'
];

// The two cards, Time Machine first only on a Mac that can read it.
export function chooserCards(options) {
  const timeMachine = options?.time_machine || {};
  const cards = [];
  if (timeMachine.platform_supported) {
    cards.push({
      id: 'time_machine',
      title: 'Time Machine (built into this Mac)',
      does: "Ori will check Time Machine's status only after you approve one read in the new workspace. Ori never changes Time Machine settings or starts a backup.",
      needs: [
        'Time Machine turned on with a backup disk',
        'Full Disk Access for the app that runs Ori, to see completed backups'
      ],
      action: 'Use Time Machine'
    });
  }
  cards.push({
    id: 'restic',
    title: 'A restic repository on an external drive',
    does: 'restic runs your backups. Ori reads the repository’s metadata after you approve one read; it never runs a backup or restore.',
    needs: RESTIC_NEEDS,
    action: 'Use restic'
  });
  return cards;
}

export async function fetchProtectionOptions(fetchImpl = fetch) {
  try {
    const response = await fetchImpl('/api/personal-assistant/protection-options', {
      headers: { Accept: 'application/json' }
    });
    if (!response?.ok) return null;
    const body = await response.json();
    return body?.protection_options || null;
  } catch (_) {
    return null;
  }
}

// Confirming hands the choice to the console as a one-time preselect, then
// opens the existing Backup & Recovery create flow unchanged.
export function confirmChoice(id, win = window) {
  try {
    if (id === 'time_machine') win.sessionStorage?.setItem(PRESELECT_KEY, 'time_machine');
    else win.sessionStorage?.removeItem(PRESELECT_KEY);
  } catch (_) {
    // A blocked store only loses the preselect; the console still asks.
  }
  const manager = win.sessionManager;
  if (manager?.showAddWorkspaceModal && win.document?.getElementById?.('addFolderModal')) {
    manager.showAddWorkspaceModal({ entryPoint: 'backup_and_recovery_cta', blueprint: BLUEPRINT });
    return 'opened';
  }
  win.location.href = `/?create=1&blueprint=${BLUEPRINT}`;
  return 'navigated';
}

function el(doc, tag, className, text) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

let dialog = null;
let opener = null;

export function closeChooser(doc = document) {
  if (!dialog) return;
  if (dialog.open && typeof dialog.close === 'function') dialog.close();
  dialog.remove();
  dialog = null;
  opener?.focus?.();
  opener = null;
  void doc;
}

export async function openChooser(win = window) {
  const doc = win.document;
  if (!doc?.body) return false;
  closeChooser(doc);
  opener = doc.activeElement;
  const options = (await fetchProtectionOptions(win.fetch?.bind(win))) || {};
  dialog = el(doc, 'dialog', 'br-dialog br-chooser');
  dialog.id = 'protectFoldersChooser';
  dialog.setAttribute('aria-labelledby', 'protectFoldersTitle');
  dialog.setAttribute('aria-modal', 'true');
  dialog.addEventListener('cancel', event => {
    event.preventDefault();
    closeChooser(doc);
  });
  const shell = el(doc, 'div', 'br-shell');
  const head = el(doc, 'header', 'br-head');
  const title = el(doc, 'h2', 'br-title', 'Protect your folders');
  title.id = 'protectFoldersTitle';
  head.appendChild(title);
  shell.appendChild(head);
  // The console's body wrapper gives the content its padding.
  const body = el(doc, 'div', 'br-body');
  shell.appendChild(body);
  body.appendChild(
    el(
      doc,
      'p',
      '',
      'Choose how these folders are backed up. Ori observes the backup you choose; it never backs anything up itself. Nothing is created until you choose.'
    )
  );
  const cards = el(doc, 'div', 'br-chooser-cards');
  for (const card of chooserCards(options)) {
    const section = el(doc, 'section', 'br-chooser-card');
    section.dataset.provider = card.id;
    section.appendChild(el(doc, 'h3', 'br-heading', card.title));
    section.appendChild(el(doc, 'h4', '', 'What Ori will do'));
    section.appendChild(el(doc, 'p', '', card.does));
    section.appendChild(el(doc, 'h4', '', "What you'll need"));
    const needs = el(doc, 'ul', 'br-needs');
    for (const need of card.needs) needs.appendChild(el(doc, 'li', '', need));
    section.appendChild(needs);
    const choose = el(doc, 'button', 'br-button', card.action);
    choose.type = 'button';
    choose.dataset.protectChoice = card.id;
    choose.addEventListener('click', () => {
      closeChooser(doc);
      confirmChoice(card.id, win);
    });
    section.appendChild(choose);
    cards.appendChild(section);
  }
  body.appendChild(cards);
  const cancel = el(doc, 'button', 'br-button br-button-secondary', 'Cancel');
  cancel.type = 'button';
  cancel.id = 'protectFoldersCancel';
  cancel.addEventListener('click', () => closeChooser(doc));
  body.appendChild(cancel);
  dialog.appendChild(shell);
  doc.body.appendChild(dialog);
  if (typeof dialog.showModal === 'function') dialog.showModal();
  else dialog.setAttribute('open', '');
  dialog.querySelector?.('[data-protect-choice]')?.focus?.();
  return true;
}

// A Today line or readiness card links to /?protect=folders. Open the chooser
// once and scrub the parameter so a refresh does not reopen it.
export function openChooserFromLocation(win = window) {
  try {
    const url = new URL(win.location.href);
    if (url.searchParams.get(PROTECT_ROUTE_PARAM) !== 'folders') return false;
    url.searchParams.delete(PROTECT_ROUTE_PARAM);
    win.history?.replaceState?.(null, '', url.pathname + url.search + url.hash);
    void openChooser(win);
    return true;
  } catch (_) {
    return false;
  }
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  window.OriProtectFolders = {
    open: () => openChooser(window),
    close: () => closeChooser(document)
  };
  if (document.readyState === 'loading')
    document.addEventListener('DOMContentLoaded', () => openChooserFromLocation(window));
  else openChooserFromLocation(window);
}
