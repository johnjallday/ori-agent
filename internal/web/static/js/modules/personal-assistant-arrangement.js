import { mountArrangementConsent } from './workspace-arrangement-consent.js';
import { collectCurrentWorkspaceContext } from './personal-assistant-workspace-context.js';

// No model request is made by attachment, opening the disclosure or refreshing
// runtime labels. The existing Ask renderer owns the canonical reply/transcript.
// libraryHomeLinks validates the host's Home list for the handoff: names and
// Home routes only. Anything else is dropped rather than turned into a link.
export function libraryHomeLinks(payload) {
  const homes = Array.isArray(payload?.homes) ? payload.homes.slice(0, 32) : [];
  return homes
    .filter(
      home =>
        home &&
        typeof home.name === 'string' &&
        home.name.trim() &&
        home.name.length <= 200 &&
        typeof home.route === 'string' &&
        /^\/workspaces\/[^/?#\s]+$/u.test(home.route)
    )
    .map(home => ({ name: home.name.trim(), href: home.route }));
}

export function arrangementAttachmentContext({
  assistant,
  view,
  folder,
  conversation,
  context,
  model,
  goal
}) {
  return {
    kind: 'attachment',
    goal,
    agentName: view?.name || '',
    providerName: model?.provider || '',
    modelName: model?.model || '',
    available:
      view?.available === true &&
      model?.configured === true &&
      Boolean(folder?.selection_id) &&
      Boolean(conversation),
    historical: folder?.historical === true,
    focusIDs: folder?.focus_ids || [],
    contextKey: JSON.stringify([
      assistant?.assistant_id,
      assistant?.hq_workspace_id,
      assistant?.hq_agent_instance_id,
      assistant?.state_version,
      assistant?.state,
      conversation,
      folder,
      context,
      model?.provider,
      model?.model
    ])
  };
}

function init() {
  const form = document.getElementById('personalAssistantForm');
  const input = document.getElementById('personalAssistantInput');
  if (!form || !input) return;
  const openButton = document.getElementById('personalAssistantArrangeFolder');
  const returnFocus = document.getElementById('personalAssistantAttachMenu');
  if (!openButton) return;
  const details = document.createElement('details');
  details.hidden = true;
  details.id = 'personalAssistantArrangement';
  details.className = 'personal-assistant-folder-context';
  const title = document.createElement('summary');
  title.textContent = 'Arrangement planning';
  const hint = document.createElement('p');
  hint.className = 'small';
  hint.textContent =
    'Write an organizing goal in the conversation box. The separate Send below requests a saved plan; ordinary Send remains discussion. No Apply action is available.';
  const refreshButton = document.createElement('button');
  refreshButton.type = 'button';
  refreshButton.className = 'btn btn-sm btn-outline-secondary';
  refreshButton.textContent = 'Refresh planning model';
  const historyButton = document.createElement('button');
  historyButton.type = 'button';
  historyButton.className = 'btn btn-sm btn-link';
  historyButton.textContent = 'Reopen saved conversation history';
  const status = document.createElement('p');
  status.setAttribute('role', 'status');
  const body = document.createElement('div');
  // A Home's saved catalog is organized inside that Home, by its own Manager.
  // This is navigation only: the assistant shares nothing and plans nothing.
  const homes = document.createElement('section');
  homes.className = 'small';
  homes.setAttribute('aria-label', 'Organize inside a Home instead');
  homes.hidden = true;
  details.append(title, hint, refreshButton, historyButton, status, body, homes);
  const scroll = document.getElementById('personalAssistantScroll');
  if (!scroll) return;
  scroll.append(details);
  const close = (focus = false) => {
    details.open = false;
    details.hidden = true;
    openButton.setAttribute('aria-expanded', 'false');
    consent.clearChoices();
    if (focus) returnFocus?.focus({ preventScroll: true });
  };
  openButton.addEventListener('click', () => {
    if (details.open) {
      close(true);
      return;
    }
    window.PersonalAssistantFolderContext?.showChat?.();
    details.hidden = false;
    details.open = true;
    title.focus({ preventScroll: true });
  });
  let model = null;
  let generation = 0;
  const refs = () => {
    const context = collectCurrentWorkspaceContext();
    return {
      context,
      conversation: window.PersonalAssistantConversation?.request(context),
      folder: window.PersonalAssistantFolderContext?.request()
    };
  };
  const current = () => {
    const state = window.PersonalAssistantPanel?._state;
    return arrangementAttachmentContext({
      assistant: state?.personalAssistant,
      view: state?.view,
      ...refs(),
      model,
      goal: input.value.trim()
    });
  };
  const consent = mountArrangementConsent(body, {
    kind: 'attachment',
    enabled: true,
    getContext: current,
    onSend: async request => {
      const captured = refs();
      const key = current().contextKey;
      const isCurrent = () => current().contextKey === key;
      if (
        window.PersonalAssistantConversation?.isLoading() ||
        window.PersonalAssistantFolderContext?.isPending() ||
        window.OriAskRouting?.getState?.().busy ||
        !window.OriAskRouting?.submitArrangement
      )
        throw new Error('Not ready');
      // These optional server-enforced pins prevent sharing with a model changed
      // since the disclosure. Never forward drafts, research or other approvals.
      const saved = await window.OriAskRouting.submitArrangement(
        {
          ...request,
          arrangement: {
            ...request.arrangement,
            expected_provider: model.provider,
            expected_model: model.model
          }
        },
        captured,
        isCurrent
      );
      const restoreFocus = details.contains(document.activeElement);
      details.open = false;
      const row = document.querySelector(
        `[data-message-id="${CSS.escape(saved.conversation.assistant_message_id)}"]`
      );
      row?.scrollIntoView({ block: 'start' });
      if (restoreFocus) returnFocus?.focus({ preventScroll: true });
    }
  });
  let sourceKey = '';
  const refresh = () => {
    const context = refs();
    const next = JSON.stringify([context.conversation, context.folder]);
    if (next !== sourceKey) {
      sourceKey = next;
      generation++;
      homesGeneration++;
      model = null;
      refreshButton.disabled = false;
      homes.hidden = true;
      homes.replaceChildren();
      close(details.contains(document.activeElement));
    }
    const attached = Boolean(window.PersonalAssistantFolderContext?.hasFolder?.());
    openButton.hidden = !attached;
    details.hidden = !attached || !details.open;
    consent.refresh();
  };
  const loadModel = async () => {
    const run = ++generation;
    model = null;
    consent.refresh();
    refreshButton.disabled = true;
    status.textContent = 'Checking the configured planning model…';
    try {
      const response = await fetch('/api/settings/system-model', {
        cache: 'no-store',
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) throw new Error('Unavailable');
      const result = await response.json();
      if (run !== generation) return;
      model = result;
      status.textContent = result.configured
        ? 'Model checked. Review the metadata disclosure before Send.'
        : 'Configure a tool-capable API model, or keep using ordinary discussion and manual setup.';
    } catch (_) {
      if (run === generation)
        status.textContent = 'The planning model could not be checked. Nothing was shared.';
    } finally {
      if (run === generation) {
        refreshButton.disabled = false;
        refresh();
      }
    }
  };
  let homesGeneration = 0;
  const loadHomes = async () => {
    const run = ++homesGeneration;
    homes.hidden = true;
    homes.replaceChildren();
    try {
      const response = await fetch('/api/assistant-program/library-homes', {
        cache: 'no-store',
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) return;
      const links = libraryHomeLinks(await response.json());
      if (run !== homesGeneration || !links.length) return;
      const heading = document.createElement('h4');
      heading.className = 'h6';
      heading.textContent = 'Organize inside a Home instead';
      const note = document.createElement('p');
      note.textContent =
        "A Home's saved catalog is organized there, by that Home's own Manager, with its own disclosure. Opening a Home shares nothing from this conversation, plans nothing and changes nothing.";
      const list = document.createElement('ul');
      for (const link of links) {
        const item = document.createElement('li');
        const anchor = document.createElement('a');
        anchor.href = `${link.href}#projectLibraryArrangementPlanning`;
        anchor.textContent = `Open ${link.name} → Organize library`;
        item.append(anchor);
        list.append(item);
      }
      homes.append(heading, note, list);
      homes.hidden = false;
    } catch (_) {
      // The handoff is optional; planning here is unaffected.
    }
  };
  details.addEventListener('toggle', () => {
    openButton.setAttribute('aria-expanded', String(details.open));
    if (details.open) {
      void loadModel();
      void loadHomes();
      details.scrollIntoView({ block: 'start' });
    } else close(details.contains(document.activeElement));
  });
  details.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    close(true);
  });
  document.addEventListener('personal-assistant:closed', () => close());
  refreshButton.addEventListener('click', () => void loadModel());
  historyButton.addEventListener('click', async () => {
    if (window.OriAskRouting?.getState?.().busy) return;
    if (await window.PersonalAssistantConversation?.reopenHistory?.()) {
      consent.historyReopened();
      refresh();
    } else
      status.textContent =
        'Saved history could not be opened. Keep your goal and try again before resending.';
  });
  input.addEventListener('input', refresh);
  document.addEventListener('personal-assistant:sent', () => consent.clearChoices());
  const relationship = () => {
    const pa = window.PersonalAssistantPanel?._state?.personalAssistant;
    return JSON.stringify([
      pa?.assistant_id,
      pa?.hq_workspace_id,
      pa?.hq_agent_instance_id,
      pa?.state_version,
      pa?.state
    ]);
  };
  let relationshipKey = relationship();
  document.addEventListener('personal-assistant:status', () => {
    const next = relationship();
    if (next !== relationshipKey) {
      relationshipKey = next;
      generation++;
      model = null;
      refreshButton.disabled = false;
    }
    refresh();
  });
  window.PersonalAssistantArrangement = { contextChanged: refresh };
  refresh();
}
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
