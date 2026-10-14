import { mountArrangementConsent } from './workspace-arrangement-consent.js';
import { collectCurrentWorkspaceContext } from './personal-assistant-workspace-context.js';

// No model request is made by attachment, opening the disclosure or refreshing
// runtime labels. The existing Ask renderer owns the canonical reply/transcript.
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
  const details = document.createElement('details');
  details.id = 'personalAssistantArrangement';
  details.className = 'personal-assistant-folder-context';
  const title = document.createElement('summary');
  title.textContent = 'Suggest arrangement';
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
  details.append(title, hint, refreshButton, historyButton, status, body);
  const scroll = document.getElementById('personalAssistantScroll');
  const chips = document.getElementById('personalAssistantChips');
  if (!scroll || !chips) return;
  scroll.append(details);
  const openButton = document.createElement('button');
  openButton.type = 'button';
  openButton.className = 'personal-assistant-panel__chip';
  openButton.textContent = 'Suggest arrangement';
  openButton.setAttribute('aria-controls', details.id);
  openButton.setAttribute('aria-expanded', 'false');
  chips.append(openButton);
  openButton.addEventListener('click', () => {
    window.PersonalAssistantFolderContext?.showChat?.();
    details.open = !details.open;
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
      if (restoreFocus) openButton.focus({ preventScroll: true });
    }
  });
  const refresh = () => {
    details.hidden = !window.PersonalAssistantFolderContext?.hasFolder?.();
    openButton.hidden = details.hidden;
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
  details.addEventListener('toggle', () => {
    openButton.setAttribute('aria-expanded', String(details.open));
    if (details.open) {
      void loadModel();
      details.scrollIntoView({ block: 'start' });
    } else consent.clearChoices();
  });
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
