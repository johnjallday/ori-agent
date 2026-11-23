// Tab-local presentation memory, separate from the report's server-owned
// unread/answered state. Fail closed at capacity or without durable storage:
// dropping an old key would nudge again when that workspace is revisited.
const SLOT = 'ori.commander.presentedQuestions.v1';
export function firstQuestionPresentation(key, getStorage) {
  if (!key || key.length > 512) return false;
  try {
    const storage = getStorage();
    const keys = JSON.parse(storage.getItem(SLOT) ?? '[]');
    if (!Array.isArray(keys) || keys.length >= 128 || keys.includes(key)) return false;
    storage.setItem(SLOT, JSON.stringify([...keys, key]));
    return true;
  } catch (_) {
    return false;
  }
}
