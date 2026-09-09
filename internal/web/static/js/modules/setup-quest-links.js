// Host-owned routes only. Neither a plugin declaration nor a template may
// supply a URL, action handler, user ID, or permission scope.
export const ASSISTANT_SETUP_ROOT = '/api/personal-assistant/setup-journey';
const idPattern = /^[a-z0-9][a-z0-9_-]{0,63}$/;

export function setupQuestAPIRoot(pluginID, questID) {
  if (
    typeof pluginID !== 'string' ||
    typeof questID !== 'string' ||
    pluginID.trim() !== pluginID ||
    questID.trim() !== questID ||
    !idPattern.test(pluginID) ||
    !idPattern.test(questID)
  ) {
    throw new Error('This setup quest is unavailable. Refresh its plugin or template.');
  }
  return `/api/setup-quests/${encodeURIComponent(pluginID)}/${encodeURIComponent(questID)}`;
}

export function setupJourneyAPIRoot(projection) {
  const declaration = projection?.journey;
  return declaration?.plugin_id
    ? setupQuestAPIRoot(declaration.plugin_id, declaration.id)
    : ASSISTANT_SETUP_ROOT;
}

export function setupQuestURL(quest) {
  setupQuestAPIRoot(quest?.plugin_id, quest?.id);
  const query = new URLSearchParams({ setup: 'quest', plugin: quest.plugin_id, quest: quest.id });
  return `/?${query}`;
}

// Match only canonical template ownership and an optional exact reference.
// Local copies cannot select another plugin's quest by naming its ID alone.
export function setupQuestForTemplate(template, quests) {
  if (!template) return null;
  const owner = template.plugin_owner;
  const templateID =
    owner?.plugin_id && owner?.blueprint_id
      ? `plugin:${owner.plugin_id}:${owner.blueprint_id}`
      : template.id;
  if (typeof templateID !== 'string' || !templateID.startsWith('plugin:')) return null;
  const identity = templateID.split(':');
  if (identity.length !== 3) return null;
  const matches = (Array.isArray(quests) ? quests : []).filter(
    quest =>
      quest?.template_id === templateID &&
      quest.plugin_id === identity[1] &&
      (!template.setup_quest || quest.id === template.setup_quest)
  );
  return matches.length === 1 ? matches[0] : null;
}

export async function loadSetupQuests() {
  const response = await fetch('/api/setup-quests', { headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error('Guided setup is unavailable. Refresh to check again.');
  const data = await response.json();
  if (!Array.isArray(data?.quests)) throw new Error('The setup catalog is unavailable.');
  return data.quests.map(quest => ({ ...quest, launch_url: setupQuestURL(quest) }));
}
