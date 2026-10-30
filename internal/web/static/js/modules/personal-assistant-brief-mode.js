// Brief mode: while a Home's manager offers exactly one waiting song, a reply
// to the manager is that song's brief, not a chat turn. Send then reads
// "Use as brief", stores the text as the card's brief and echoes it as the
// user's message; no session is created and no turn runs. Pure decisions only;
// personal-assistant-panel.js applies them.
import { RECIPIENT_WORKSPACE_ENTRY } from './personal-assistant-recipient.js';

export const BRIEF_LIMIT = 2000;
export const BRIEF_SEND_LABEL = 'Use as brief';
export const BRIEF_TOO_LONG = `That brief is longer than ${BRIEF_LIMIT} characters. Shorten it, then use it.`;

function text(value) {
  return String(value ?? '').trim();
}

const OFF = Object.freeze({ on: false });

/**
 * Whether the composer is in brief mode, from who the drawer is addressed to
 * and the manager's offer state (createHomeOffer().state()). On only when the
 * recipient is the Home's entry agent and exactly one song is still offered:
 * Not now, the press, a second waiting song or the Personal Assistant each
 * turn it off.
 */
export function briefModeView({ recipient, offers } = {}) {
  if (recipient?.kind !== RECIPIENT_WORKSPACE_ENTRY || !recipient.available) return OFF;
  if (offers?.kind !== 'offers') return OFF;
  const pending = Array.isArray(offers.pending) ? offers.pending : [];
  if (pending.length !== 1) return OFF;
  const view = pending[0];
  if (!view?.offerID || !view.asksBrief) return OFF;
  const manager = text(offers.manager?.name) || text(recipient.name) || text(view.manager);
  const assistant = text(view.hire?.name) || 'its assistant';
  return {
    on: true,
    offerID: view.offerID,
    song: view.song,
    manager,
    placeholder: `Tell ${manager} what the ${assistant} should do with ${view.song}`,
    sendLabel: BRIEF_SEND_LABEL
  };
}

/**
 * What Send does in brief mode: nothing for empty text, the over-limit
 * message for a brief that is too long, else the brief to store and echo.
 */
export function briefSend(mode, draft) {
  if (!mode?.on) return { action: 'turn' };
  const brief = text(draft);
  if (!brief) return { action: 'none' };
  if (brief.length > BRIEF_LIMIT) return { action: 'tooLong', message: BRIEF_TOO_LONG };
  return { action: 'brief', offerID: mode.offerID, brief };
}
