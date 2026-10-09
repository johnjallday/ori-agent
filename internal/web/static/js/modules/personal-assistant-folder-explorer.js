import {
  folderFocusView,
  folderSelectAllFocus,
  folderTreeView
} from './personal-assistant-folder-tree.js';

const element = (tag, text = '', className = '') => {
  const node = document.createElement(tag);
  node.textContent = text;
  node.className = className;
  return node;
};

// Uses the same panel, attachment, conversation and composer. This controller
// owns only ephemeral layout/disclosures, never permissions or durable scope.
export function initFolderExplorer({ current, setFocus, notify }) {
  const get = id => document.getElementById(`personalAssistant${id}`);
  const panel = get('Panel'),
    pane = get('FolderExplorer'),
    list = get('FolderTree');
  if (!panel || !pane || !list) return null;
  const toolbar = get('ExplorerToolbar'),
    launch = get('ExploreAttachedFolder');
  const treeTab = get('ExplorerTreeTab'),
    chatTab = get('ExplorerChatTab'),
    selectAll = get('FolderSelectAll');
  const expanded = new Set();
  let open = false,
    tab = 'tree',
    renderedKey = '',
    observationId = '';

  function layout() {
    panel.classList.toggle('personal-assistant-panel--exploring', open);
    panel.dataset.explorerTab = tab;
    pane.hidden = !open;
    toolbar.hidden = !open;
    launch.setAttribute('aria-expanded', String(open));
    const state = current();
    get('FolderFocus').hidden = !state?.observation?.tree || (!open && !state.focusIDs.length);
    treeTab.setAttribute('aria-pressed', String(tab === 'tree'));
    chatTab.setAttribute('aria-pressed', String(tab === 'chat'));
    window.PersonalAssistantPanel?.syncViewport?.();
  }
  function showChat() {
    tab = 'chat';
    layout();
  }
  function collapse({ focus = true } = {}) {
    open = false;
    layout();
    if (focus) get('Input')?.focus();
  }
  function explore({ automatic = false } = {}) {
    const state = current();
    if (!state?.observation || state.pending || window.PersonalAssistantConversation?.isLoading?.())
      return false;
    open = true;
    tab = 'tree';
    refresh();
    if (automatic && panel.classList.contains('personal-assistant-panel--explorer-narrow'))
      treeTab.focus();
    else (list.querySelector('button, input') || get('ExplorerBack')).focus();
    return true;
  }
  function binding(state) {
    return {
      observationId: state.observation.id,
      generation: state.generation,
      conversationId: state.conversationId
    };
  }
  function renderEntries(tree, state) {
    const captured = binding(state);
    function branch(entry) {
      const item = element('li'),
        row = element('div', '', 'personal-assistant-folder-tree__row');
      let children, toggle;
      if (entry.kind === 'folder') {
        toggle = element(
          'button',
          expanded.has(entry.id) ? '⌄' : '›',
          'personal-assistant-folder-tree__toggle'
        );
        toggle.type = 'button';
        toggle.dataset.treeToggle = entry.id;
        toggle.setAttribute('aria-label', `Expand ${entry.label}`);
        toggle.setAttribute('aria-expanded', String(expanded.has(entry.id)));
        toggle.addEventListener('click', () => {
          if (
            !toggle.isConnected ||
            current().generation !== captured.generation ||
            current().pending ||
            window.PersonalAssistantConversation?.isLoading?.()
          )
            return;
          const isOpen = !expanded.has(entry.id);
          if (isOpen) expanded.add(entry.id);
          else expanded.delete(entry.id);
          children.hidden = !isOpen;
          toggle.textContent = isOpen ? '⌄' : '›';
          toggle.setAttribute('aria-expanded', String(isOpen));
        });
        row.append(toggle);
      } else row.append(element('span', '·', 'personal-assistant-folder-tree__file'));
      const label = element('label', '', 'personal-assistant-folder-tree__label');
      const check = document.createElement('input');
      check.type = 'checkbox';
      check.dataset.treeFocus = entry.id;
      check.checked = state.focusIDs.includes(entry.id);
      check.disabled = entry.ambiguous || state.pending;
      if (toggle) toggle.disabled = state.pending;
      check.setAttribute(
        'aria-label',
        `Discuss ${entry.kind}: ${entry.label}${entry.ambiguous ? ' (indistinguishable name)' : ''}`
      );
      check.addEventListener('change', () => {
        if (!check.isConnected) return;
        const ids = current().focusIDs.filter(id => id !== entry.id);
        if (check.checked) ids.push(entry.id);
        if (!setFocus(ids, captured)) {
          check.checked = current().focusIDs.includes(entry.id);
          const message =
            'Choose up to 8 distinguishable topics; long labels may require fewer. Your draft is unchanged.';
          get('ExplorerNotice').textContent = message;
          notify(message);
        } else {
          get('ExplorerNotice').textContent = '';
          notify('Discussion focus updated for the next message. No message sent.');
        }
      });
      const name = element(
        'span',
        entry.name + (entry.ambiguous ? ' (indistinguishable name)' : '')
      );
      name.title = entry.label;
      label.append(check, name);
      row.append(label);
      item.append(row);
      if (toggle) {
        children = element('ul', '', 'personal-assistant-folder-tree__children');
        children.hidden = !expanded.has(entry.id);
        for (const child of entry.children) children.append(branch(child));
        if (!entry.children.length)
          children.append(
            element(
              'li',
              'No child entries recorded in this bounded snapshot.',
              'personal-assistant-folder-tree__empty'
            )
          );
        item.append(children);
      }
      return item;
    }
    for (const entry of tree.roots) list.append(branch(entry));
  }
  function refresh() {
    const state = current();
    const observation = state?.observation;
    const loading = window.PersonalAssistantConversation?.isLoading?.() === true;
    launch.hidden = !folderTreeView(observation);
    launch.disabled = Boolean(state?.pending || loading);
    const all = folderSelectAllFocus(observation);
    selectAll.hidden = launch.hidden;
    selectAll.disabled = !all || state?.pending || loading;
    selectAll.textContent = all?.wholeFolder ? 'Select all · Whole folder' : 'Select all';
    selectAll.title = all?.wholeFolder
      ? 'Uses whole-folder discussion because all entries cannot fit as distinguishable individual topics. No partial selection.'
      : 'Select every recorded entry, including entries in collapsed folders, for the next message.';
    get('FolderFocus').hidden = !observation?.tree || (!open && !state.focusIDs.length);
    if (!observation || loading) {
      collapse({ focus: false });
      renderedKey = '';
    }
    if (!observation) {
      list.replaceChildren();
      observationId = '';
      expanded.clear();
      return;
    }
    const tree = folderTreeView(observation);
    const focus = folderFocusView(observation, state.focusIDs);
    const labels = focus?.topics.map(topic => topic.names.join(' › ')) || [];
    const focusText = get('FolderFocusText');
    focusText.textContent =
      labels.length === 1
        ? `Next message · ${labels[0]}`
        : labels.length
          ? `Next message · ${labels.length} topics`
          : 'Next message · Whole folder';
    focusText.setAttribute(
      'aria-label',
      `Next message discussion focus: ${labels.length ? labels.join('; ') : 'Whole folder'}`
    );
    focusText.title = labels.join('; ') || 'Whole folder';
    get('FolderFocusClear').hidden = !labels.length;
    get('FolderFocusClear').disabled = state.pending || loading;
    get('FolderExplorerName').textContent = observation.folder;
    get('FolderExplorerName').title = observation.folder;
    get('FolderExplorerCoverageSummary').textContent = tree
      ? `${observation.coverage?.partial ? 'Partial' : 'Bounded'} snapshot · ${tree.entries.length} entries${tree.omitted ? ` · ${tree.omitted} omitted` : ''}`
      : 'Saved folder summaries only';
    get('FolderExplorerStatus').textContent = state.preview
      ? 'Local snapshot · not sent'
      : `Saved snapshot${state.authority ? ' · historical' : ''}`;
    get('FolderExplorerCoverage').textContent = tree
      ? `${tree.entries.length} recorded entries${tree.omitted ? ` · ${tree.omitted} more observed entries omitted` : ''}. Scanned ${new Date(observation.scanned_at).toLocaleString()}. Limits: ${observation.coverage.max_depth} directory levels, ${observation.coverage.max_entries.toLocaleString()} entries, ${observation.coverage.budget_seconds} seconds; ${observation.coverage.skipped_links} links skipped. Bounded look, not a complete tree. Hidden/tooling folders, links, deeper levels and inaccessible entries may be absent. Expanding reads nothing.`
      : 'This saved snapshot contains folder summaries, not a file tree. Pick again for fresh metadata; nothing is rescanned automatically.';
    if (observationId !== observation.id) {
      expanded.clear();
      get('ExplorerNotice').textContent = '';
      observationId = observation.id;
      // A replacement/new thread retires the old view; only the explicit
      // successful-attachment callback is allowed to open it automatically.
      collapse({ focus: false });
    }
    const key = `${observation.id}:${state.conversationId}:${state.generation}`;
    if (key !== renderedKey) {
      const active = document.activeElement;
      const focusID = active?.dataset?.treeFocus,
        toggleID = active?.dataset?.treeToggle;
      const scrollTop = pane.scrollTop;
      renderedKey = key;
      list.replaceChildren();
      if (tree) renderEntries(tree, state);
      if (!tree || !tree.entries.length)
        list.append(
          element(
            'li',
            tree
              ? 'No visible entries recorded. This does not prove the folder is empty.'
              : 'No individual entries recorded in this snapshot.',
            'personal-assistant-folder-tree__empty'
          )
        );
      pane.scrollTop = scrollTop;
      if (focusID || toggleID) {
        Array.from(list.querySelectorAll('input, button'))
          .find(node =>
            focusID ? node.dataset.treeFocus === focusID : node.dataset.treeToggle === toggleID
          )
          ?.focus();
      }
    }
    for (const toggle of list.querySelectorAll('button'))
      toggle.disabled = state.pending || loading;
    for (const check of list.querySelectorAll('input')) {
      check.checked = state.focusIDs.includes(check.dataset.treeFocus);
      check.disabled =
        !tree || tree.byId.get(check.dataset.treeFocus)?.ambiguous || state.pending || loading;
    }
    layout();
  }
  launch.addEventListener('click', () => explore());
  get('ExplorerBack').addEventListener('click', () => collapse());
  treeTab.addEventListener('click', () => {
    tab = 'tree';
    layout();
  });
  chatTab.addEventListener('click', showChat);
  selectAll.addEventListener('click', () => {
    if (!selectAll.isConnected || selectAll.disabled) return;
    const state = current();
    if (state?.pending || window.PersonalAssistantConversation?.isLoading?.()) return;
    const all = folderSelectAllFocus(state?.observation);
    if (!all || !setFocus(all.ids, binding(state))) return;
    const message = all.wholeFolder
      ? 'Whole-folder focus selected; individual checks cleared. Nothing read or sent.'
      : `All ${all.count} recorded entries selected for the next message. Nothing read or sent.`;
    get('ExplorerNotice').textContent = message;
    notify(message);
  });
  get('FolderFocusClear').addEventListener('click', () => {
    const state = current();
    if (state?.observation && setFocus([], binding(state))) {
      get('ExplorerNotice').textContent = '';
      notify('Whole-folder focus selected. No message sent.');
      get('Input')?.focus();
    }
  });
  if (typeof ResizeObserver !== 'undefined') {
    new ResizeObserver(() =>
      requestAnimationFrame(() => {
        const active = document.activeElement;
        if (!open || !pane.contains(active) || !pane.getClientRects().length) return;
        const viewport = pane.getBoundingClientRect();
        const target = active.getBoundingClientRect();
        const scale = viewport.height / pane.offsetHeight || 1;
        if (target.top < viewport.top) pane.scrollTop += (target.top - viewport.top - 2) / scale;
        else if (target.bottom > viewport.bottom)
          pane.scrollTop += (target.bottom - viewport.bottom + 2) / scale;
      })
    ).observe(pane);
  }
  // Shared footer review still opens the actual reviewed setup in Chat.
  get('ReviewFolderSetup')?.addEventListener('click', showChat, true);
  document.addEventListener('personal-assistant:sent', showChat);
  return { explore, collapse, refresh, showChat };
}
