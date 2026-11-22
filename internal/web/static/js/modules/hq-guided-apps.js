// Only names from the explicit review reach the scoped add. Do not send this
// inventory to inference, chat or storage. Receipts use salted fingerprints
// of *saved selections* to reconcile against Profile, not a stored inventory.
import { currentHQAttempt } from './hq-guided-attempt.js';

const key = 'ori.hq.toolSave.v1';
const norm = name => name.trim().toLowerCase();
export function reviewNames(names) {
  if (!Array.isArray(names) || names.some(name => typeof name !== 'string' || !name.trim()))
    throw Error('App names could not be read. Try again.');
  const unique = new Map();
  for (const name of names) if (!unique.has(norm(name))) unique.set(norm(name), name.trim());
  return [...unique.values()];
}
async function fingerprint(hint, name) {
  const bytes = await crypto.subtle.digest(
    'SHA-256',
    new TextEncoder().encode(hint.attempt + ':' + norm(name))
  );
  return Array.from(new Uint8Array(bytes), b => b.toString(16).padStart(2, '0')).join('');
}
export async function saveReviewedTools(hint, selected, post) {
  if (!hint || !currentHQAttempt(hint.hq, 'apps', hint.attempt))
    throw Error('Reopen the app review to continue.');
  let hashes = [];
  if (selected.length) {
    const saved = await post('/api/onboarding/preferred-tools', { names: selected });
    const persisted = reviewNames(saved?.preferred_tools).map(norm);
    if (!selected.every(name => persisted.includes(norm(name))))
      throw Error('The selected tools could not be verified. Check Profile.');
    hashes = await Promise.all(selected.map(name => fingerprint(hint, name)));
  }
  if (currentHQAttempt(hint.hq, 'apps', hint.attempt))
    sessionStorage.setItem(key, JSON.stringify({ attempt: hint.attempt, hashes }));
}
export async function toolsReceipt(hint, read) {
  const saved = JSON.parse(sessionStorage.getItem(key) || 'null');
  const profile = { href: '/profile#personalization', label: 'Review preferred tools in Profile' };
  if (!saved || saved.attempt !== hint.attempt || !Array.isArray(saved.hashes))
    return { text: 'No tool save was confirmed here.', ...profile };
  if (!saved.hashes.length)
    return { text: 'No tools saved. Your preferences were not changed.', ...profile };
  const stored = await read('/api/onboarding/user-profile');
  if (!stored?.success) throw Error('Profile unavailable');
  const names = reviewNames(stored.profile?.preferred_tools || []);
  const ids = await Promise.all(names.map(name => fingerprint(hint, name)));
  const present = names.filter((_, i) => saved.hashes.includes(ids[i]));
  return {
    text: present.length
      ? `Selected tools now in Profile: ${present.join(', ')}.`
      : 'The selected tools are no longer in Profile.',
    ...profile
  };
}
