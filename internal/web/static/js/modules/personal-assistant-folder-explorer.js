import {
  folderSelectionFocus,
  folderSelectAllFocus,
  folderTreeView
} from './personal-assistant-folder-tree.js';

const element = (tag, text = '', className = '') => {
  const node = document.createElement(tag);
  node.textContent = text;
  node.className = className;
  return node;
};

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

// explorerCountsLine is the Tree pane's one line: what was recorded, and that
// nothing was read. The privacy details sit in its info tooltip.
export function explorerCountsLine(observation, tree) {
  if (!observation) return '';
  const folders = tree
    ? tree.entries.filter(entry => entry.kind === 'folder').length
    : (observation.projects || []).filter(project => !project.root).length;
  const more = tree?.omitted ? '+' : '';
  const files = Number.isInteger(observation.files) ? observation.files : 0;
  return `${folders}${more} ${folders === 1 && !more ? 'folder' : 'folders'} · ${plural(files, 'file', 'files')} · metadata only, nothing read`;
}

// explorerPartialLine says, only for a scan that stopped early, why.
export function explorerPartialLine(observation) {
  const coverage = observation?.coverage;
  if (!coverage?.partial) return '';
  const reasons = { entries: 'entry limit reached', time: 'time limit reached' };
  return `Partial scan · ${reasons[coverage.partial_reason] || 'some entries were not recorded'}`;
}

// focusStatusLine is the Focus status, shown only while topics are checked.
export function focusStatusLine(count) {
  return count > 0 ? `${plural(count, 'topic', 'topics')} for the next message` : '';
}

// Uses the same panel, attachment, conversation and composer. This controller
// owns only ephemeral layout/disclosures, never permissions or durable scope.
export function initFolderExplorer({ current, setFocus, notify, layoutChanged }) {
  const get = id => document.getElementById(`personalAssistant${id}`);
  const panel = get('Panel'),
    pane = get('FolderExplorer'),
    header = get('FolderExplorerHeader'),
    list = get('FolderTree');
  if (!panel || !pane || !header || !list) return null;
  const toolbar = get('ExplorerToolbar'),
    launch = get('ExploreAttachedFolder');
  const treeTab = get('ExplorerTreeTab'),
    chatTab = get('ExplorerChatTab'),
    selectAll = get('FolderSelectAll'),
    focusToggle = get('FolderFocusToggle');
  const expanded = new Set();
  let open = false,
    tab = 'tree',
    renderedKey = '',
    observationId = '',
    // Focus mode: the checks, Select all and the status line are shown.
    focusing = false;

  function layout() {
    panel.classList.toggle('personal-assistant-panel--exploring', open);
    panel.dataset.explorerTab = tab;
    pane.hidden = !open;
    toolbar.hidden = !open;
    launch.setAttribute('aria-expanded', String(open));
    treeTab.setAttribute('aria-pressed', String(tab === 'tree'));
    chatTab.setAttribute('aria-pressed', String(tab === 'chat'));
    window.PersonalAssistantPanel?.syncViewport?.();
    layoutChanged?.();
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
            'These topics cannot be distinguished in this snapshot. Clear the checks and choose distinguishable entries, or pick the folder again. Your draft is unchanged.';
          get('ExplorerNotice').textContent = message;
          notify(message);
        } else {
          // The status line says what the next message is about.
          get('ExplorerNotice').textContent = '';
          notify('');
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
    selectAll.disabled = !all || state?.pending || loading;
    const selectedCount = state?.focusIDs.length || 0;
    // Checks in use are always shown; otherwise Focus is the user's choice.
    if (observation?.id !== observationId) focusing = false;
    if (selectedCount) focusing = true;
    pane.classList.toggle('personal-assistant-explorer__pane--focus', focusing);
    focusToggle.hidden = launch.hidden;
    focusToggle.disabled = Boolean(state?.pending || loading);
    focusToggle.setAttribute('aria-pressed', String(focusing));
    get('FolderSelection').hidden = !focusing || launch.hidden;
    selectAll.checked = Boolean(all && selectedCount === all.count);
    selectAll.indeterminate = selectedCount > 0 && !selectAll.checked;
    selectAll.title = all?.wholeFolder
      ? 'Some recorded names are indistinguishable. Select all uses whole-folder discussion.'
      : 'Select every recorded entry, including entries in collapsed folders, for the next message.';
    const status = focusStatusLine(selectedCount);
    get('FolderSelectionStatus').textContent = status;
    get('FolderSelectionLine').hidden = !status;
    get('FolderFocusClear').disabled = Boolean(state?.pending || loading);
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
    const focus = folderSelectionFocus(observation, state.focusIDs);
    const labels = focus?.topics.map(topic => topic.names.join(' › ')) || [];
    // The topics themselves are the status line's title, for a pointer.
    get('FolderSelectionStatus').title = labels.join('; ');
    get('FolderExplorerName').textContent = observation.folder;
    get('FolderExplorerName').title = observation.folder;
    get('FolderExplorerCounts').textContent = explorerCountsLine(observation, tree);
    const partial = explorerPartialLine(observation);
    get('FolderExplorerPartial').textContent = partial;
    get('FolderExplorerPartial').hidden = !partial;
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
  selectAll.addEventListener('change', () => {
    if (!selectAll.isConnected || selectAll.disabled) return;
    const state = current();
    if (state?.pending || window.PersonalAssistantConversation?.isLoading?.()) return;
    const all = folderSelectAllFocus(state?.observation);
    const selecting = selectAll.checked;
    if (!all || !setFocus(selecting ? all.ids : [], binding(state))) {
      refresh();
      return;
    }
    // The status line says it; only an indistinguishable selection needs more.
    const message =
      selecting && all.wholeFolder
        ? 'Some recorded names are indistinguishable, so the next message discusses the whole folder.'
        : '';
    get('ExplorerNotice').textContent = message;
    notify(message);
  });
  get('FolderFocusClear').addEventListener('click', () => {
    const state = current();
    if (state?.observation && setFocus([], binding(state))) {
      get('ExplorerNotice').textContent = '';
      notify('');
      get('Input')?.focus();
    }
  });
  // Leaving Focus mode with topics checked returns to whole-folder discussion.
  focusToggle.addEventListener('click', () => {
    const state = current();
    if (!state?.observation || state.pending || window.PersonalAssistantConversation?.isLoading?.())
      return;
    if (focusing && state.focusIDs.length && !setFocus([], binding(state))) return;
    focusing = !focusing;
    get('ExplorerNotice').textContent = '';
    refresh();
    if (focusing) (list.querySelector('input:not(:disabled)') || selectAll).focus();
  });
  function revealFocusedControl() {
    if (!open || !pane.getClientRects().length) return;
    const compact = pane.clientHeight < header.offsetHeight + 60;
    pane.classList.toggle('personal-assistant-explorer__pane--compact', compact);
    const active = document.activeElement;
    if (!pane.contains(active)) return;
    const viewport = pane.getBoundingClientRect();
    const target = active.getBoundingClientRect();
    const scale = viewport.height / pane.offsetHeight || 1;
    const top =
      !compact && !header.contains(active) ? header.getBoundingClientRect().bottom : viewport.top;
    if (target.top < top) pane.scrollTop += (target.top - top - 2) / scale;
    else if (target.bottom > viewport.bottom)
      pane.scrollTop += (target.bottom - viewport.bottom + 2) / scale;
  }
  pane.addEventListener('focusin', () => requestAnimationFrame(revealFocusedControl));
  if (typeof ResizeObserver !== 'undefined') {
    const resize = new ResizeObserver(() => requestAnimationFrame(revealFocusedControl));
    resize.observe(pane);
    resize.observe(header);
  }
  // Shared footer review still opens the actual reviewed setup in Chat.
  get('ReviewFolderSetup')?.addEventListener('click', showChat, true);
  document.addEventListener('personal-assistant:sent', showChat);
  return { explore, collapse, refresh, showChat };
}
