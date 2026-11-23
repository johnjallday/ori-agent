import { currentHQAttempt } from './hq-guided-attempt.js';

// A chosen folder token is not an exploration. Look up only the workspace
// returned by the reviewed hire; never guess from a label or the latest agent.
export async function folderReceipt(hint, read) {
  const current = currentHQAttempt(hint.hq, 'folder', hint.attempt);
  if (!current) return null;
  if (!current.workspace)
    return {
      text: 'No folder exploration was confirmed here. Choose Explore Documents to review a folder.'
    };
  const raw = (
    await read(`/api/personal-assistant/folder-agent/${encodeURIComponent(current.workspace)}`)
  )?.folder_agent;
  if (
    raw?.workspace_id !== current.workspace ||
    currentHQAttempt(hint.hq, 'folder', hint.attempt)?.workspace !== current.workspace
  )
    return null;
  const name = raw.folder_name || 'the approved folder';
  const link = /^\/workspaces\/[a-z\d_-]+$/i.test(raw.route)
    ? { href: raw.route, label: 'Open folder workspace' }
    : {};
  if (raw.state === 'failed')
    return {
      text: `The first look at ${name} failed. Review the workspace before trying again.`,
      ...link
    };
  if (raw.state === 'looking')
    return {
      text: `An agent is exploring ${name}. The report is not ready yet.`,
      checkAgain: true,
      ...link
    };
  if (['reported', 'kept', 'set_up'].includes(raw.state)) {
    // This optional read must not replace the ordinary receipt on failure.
    const query = new URLSearchParams({ hq: hint.hq, attempt: hint.attempt });
    let offer;
    try {
      ({ offer } = await read(
        `/api/personal-assistant/folder-agent/${encodeURIComponent(current.workspace)}/documents-offer?${query}`
      ));
    } catch (_) {
      /* The report still has its own workspace. */
    }
    if (currentHQAttempt(hint.hq, 'folder', hint.attempt)?.workspace !== current.workspace)
      return null;
    if (offer?.decision) {
      const d = offer.decision;
      const text = {
        dismissed: `The first look at ${name} is ready.`,
        saved: `Saved ${d.name} note.`,
        pending: 'The note was written, but its save confirmation is incomplete.',
        unverified:
          'The earlier note could not be verified. It may have been edited, removed, or saved in a different notebook.'
      }[d.state];
      if (text) return { text, offer, ...link };
    }
    if (offer?.notes_only)
      return { text: 'Your read-only Notes connection is unchanged.', offer, ...link };
    if (!offer?.projects?.length && offer?.vaults?.length) {
      return { text: 'The Documents report found a Notes vault.', offer, ...link };
    }
    if (offer?.projects?.length) {
      const first = offer.projects[0];
      return {
        text: `I found a ${first.kind} in ${first.location}.${offer.writable || offer.setup ? ' Start a project note?' : ''}`,
        offer,
        ...link
      };
    }
  }
  if (['reported', 'kept', 'setting_up', 'set_up'].includes(raw.state))
    return { text: `The first look at ${name} is ready.`, ...link };
  return null;
}
