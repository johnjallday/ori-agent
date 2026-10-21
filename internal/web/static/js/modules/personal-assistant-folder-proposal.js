// A folder the user pointed at by its path, offered as one card under the
// assistant's reply. The card is the confirmation: nothing in the folder has
// been read, and what the browser sends back is the server-held selection
// token. The path is shown, never sent.
//
// The card is presentation for the reply it arrived with. It is not saved, so
// a reopened conversation shows the reply without it; pasting the path again
// brings a fresh one.

const text = value => String(value == null ? '' : value).trim();

export const FOLDER_PROPOSAL_EXPIRED = 'That folder selection expired. Paste the path again.';

// folderProposalView is the whole decision of what the card says and does, kept
// pure so the tests can assert it without a DOM. It returns null for a proposal
// the card cannot honestly offer.
export function folderProposalView(proposal) {
  const token = text(proposal?.selection_token);
  const path = text(proposal?.path);
  if (!token || !path) return null;
  const name = text(proposal.folder_name) || 'this folder';
  const warning = text(proposal.warning_note);
  // An Obsidian vault can also be connected as the user's second brain, with
  // its own unbound selection (second-brain FR10).
  const secondBrainToken = text(proposal.second_brain_token);
  if (text(proposal.kind) === 'link') {
    const workspaceId = text(proposal.workspace_id);
    const workspace = text(proposal.workspace_name) || 'this workspace';
    if (!workspaceId) return null;
    return {
      kind: 'link',
      token,
      path,
      name,
      workspaceId,
      warning,
      secondBrainToken,
      title: `Link ${name} to ${workspace}?`,
      detail: `Linking lets Ori read the files in this folder from ${workspace}. Nothing is moved or changed.`,
      confirm: 'Link folder',
      working: 'Linking the folder…',
      done: `Linked ${name} to ${workspace}.`,
      followUp: `I linked ${name}. Take a look and answer my last message.`
    };
  }
  return {
    kind: 'explore',
    token,
    path,
    name,
    workspaceId: '',
    warning,
    secondBrainToken,
    title: `Explore ${name}?`,
    detail:
      'A read-only peek. Nothing moves. Ori looks at names, kinds and dates, not at what is inside the files.',
    confirm: 'Explore folder',
    working: 'Looking at the folder…',
    done: `Looking at ${name}.`,
    followUp: `Take a look at ${name} and answer my last message.`
  };
}

// renderFolderProposal draws the card inside the reply's bubble. explore and
// link do the work and reject with a sentence to show; isCurrent says whether
// the reply still belongs to the conversation and page it was given on.
export function renderFolderProposal(
  row,
  proposal,
  { explore, link, secondBrain, isCurrent = () => true } = {}
) {
  const view = folderProposalView(proposal);
  if (!view || !row?.ownerDocument) return null;
  const doc = row.ownerDocument;
  const bubble = row.firstElementChild || row;
  bubble.querySelector?.('.personal-assistant-folder-proposal')?.remove();

  const card = doc.createElement('section');
  card.className = 'personal-assistant-folder-proposal';
  card.setAttribute('role', 'group');
  card.setAttribute('aria-label', view.title);
  card.dataset.kind = view.kind;

  const title = doc.createElement('h4');
  title.className = 'personal-assistant-folder-proposal__title';
  title.textContent = view.title;
  const path = doc.createElement('p');
  path.className = 'personal-assistant-folder-proposal__path';
  path.textContent = view.path;
  const detail = doc.createElement('p');
  detail.className = 'personal-assistant-folder-proposal__detail';
  detail.textContent = view.detail;
  card.append(title, path, detail);
  if (view.warning) {
    const warning = doc.createElement('p');
    warning.className = 'personal-assistant-folder-proposal__warning';
    warning.textContent = view.warning;
    card.append(warning);
  }

  const actions = doc.createElement('div');
  actions.className = 'personal-assistant-folder-proposal__actions';
  const confirm = doc.createElement('button');
  confirm.type = 'button';
  confirm.textContent = view.confirm;
  actions.append(confirm);
  if (view.secondBrainToken && typeof secondBrain === 'function') {
    const connect = doc.createElement('button');
    connect.type = 'button';
    connect.dataset.folderProposalAction = 'second-brain';
    connect.textContent = 'Connect as my second brain';
    connect.addEventListener('click', async () => {
      if (connect.disabled) return;
      if (!isCurrent()) {
        status.textContent =
          'Reopen the conversation this reply belongs to, or paste the path again. Nothing was opened.';
        return;
      }
      connect.disabled = true;
      try {
        // The second brain shows what connecting means before anything is
        // read; this card is done once that step is open.
        await secondBrain(view);
        actions.remove();
        card.dataset.state = 'done';
        status.textContent = `Connecting ${view.name} as your notes. Check the card below.`;
      } catch (error) {
        connect.disabled = false;
        status.textContent = text(error?.message) || FOLDER_PROPOSAL_EXPIRED;
      }
    });
    actions.append(connect);
  }
  const status = doc.createElement('p');
  status.className = 'personal-assistant-folder-proposal__status';
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  card.append(actions, status);

  confirm.addEventListener('click', async () => {
    if (confirm.disabled) return;
    if (!isCurrent()) {
      status.textContent =
        'Reopen the conversation this reply belongs to, or paste the path again. Nothing was opened.';
      return;
    }
    const run = view.kind === 'link' ? link : explore;
    if (typeof run !== 'function') {
      status.textContent = 'Folders are unavailable on this page. Nothing was opened.';
      return;
    }
    confirm.disabled = true;
    status.textContent = view.working;
    try {
      await run(view);
      actions.remove();
      card.dataset.state = 'done';
      status.textContent = view.done;
    } catch (error) {
      confirm.disabled = false;
      status.textContent = text(error?.message) || FOLDER_PROPOSAL_EXPIRED;
    }
  });

  bubble.append(card);
  return card;
}

if (typeof window !== 'undefined') {
  window.PersonalAssistantFolderProposal = {
    render: renderFolderProposal,
    view: folderProposalView
  };
}
