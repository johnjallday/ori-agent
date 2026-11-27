// A read-only projection of the current page. Each control hands exactly one
// candidate back to Filing; the ordinary picture's bulk File button is absent.
export function renderDownloadsReview(doc, files, picture, choices, actions) {
  const make = (tag, cls, text = '') => {
    const n = doc.createElement(tag);
    n.className = cls;
    n.textContent = text;
    return n;
  };
  const section = make('section', 'fj-frame fj-downloads-routing');
  section.setAttribute('aria-labelledby', 'fjRoutingTitle');
  const heading = make('h3', 'fj-frame-title', 'Where do these downloads belong?');
  heading.id = 'fjRoutingTitle';
  section.append(
    heading,
    make(
      'p',
      'fj-sub',
      'Review one file at a time. Nothing moves until you confirm its destination.'
    )
  );
  const examples = new Map((picture.folder_suggestions || []).map(e => [e.candidate_id, e]));
  const selected = new Map(choices.map(c => [c.candidate_id, c]));
  let count = 0;
  for (const file of files) {
    const choice = selected.get(file.id);
    if (
      !['pending', 'approved'].includes(file.state) ||
      file.decision === 'trash' ||
      choice?.operation === 'trash'
    )
      continue;
    count++;
    const row = make('article', 'fj-route-file');
    row.append(make('h4', 'fj-label', file.name));
    const example = examples.get(file.id);
    row.append(
      make(
        'p',
        'fj-sub',
        example
          ? `You filed ${example.name} in ${example.folder}.`
          : 'No saved folder example matches this file.'
      )
    );
    const kind = file.decision_category || file.category;
    const matches =
      !file.needs_review || file.decision_category
        ? (picture.leaving || []).filter(c => c.kinds.includes(kind))
        : [];
    const route = matches.length === 1 ? matches[0] : null;
    if (route) {
      row.append(
        make(
          'p',
          'fj-sub',
          `Your connection to ${route.receiver_name} accepts ${kind}. Sending puts this file at the top level of that workspace, not in its final folder.`
        )
      );
      if (example) {
        const records = /(?:^|\/)travel\/receipts$/i.test(example.folder)
          ? 'your travel records'
          : example.folder;
        row.append(make('p', 'fj-label', `Is this for ${records} or for ${route.receiver_name}?`));
      }
    }
    const buttons = make('div', 'fj-shelf-tools');
    const add = (key, label, fn, primary = false) => {
      const b = make('button', `fj-btn fj-btn-${primary ? 'primary' : 'secondary'}`, label);
      b.type = 'button';
      b.id = `fj-route-${file.id}-${key}`;
      b.addEventListener('click', fn);
      buttons.append(b);
    };
    if (example)
      add('example', 'Review filing', () => actions.folder(file, example.action_id), true);
    if (route)
      add('send', `Send to ${route.receiver_name}`, () => actions.send(file, route), !example);
    add('choose', example ? 'Choose another folder' : 'Choose a folder', () =>
      actions.folder(file)
    );
    add('leave', 'Leave in Downloads', () => actions.leave(file, choice));
    row.append(buttons);
    section.append(row);
  }
  if (!count)
    section.append(make('p', 'fj-sub', 'No files on this page are waiting for a destination.'));
  return section;
}
// This URL identifies *one* file and connection, unlike the station's bulk
// plan. The read is pinned to the displayed review; a refusal never retries
// against a newer revision, another receiver, or a local folder.
async function reviewSend(file, route, { base, current, onFiled }, binding) {
  if (!current()) return;
  const url = `${base}/send/${encodeURIComponent(route.connection_id)}/${encodeURIComponent(file.id)}`;
  const dialog = document.createElement('dialog');
  dialog.className = 'fj-destination-dialog';
  dialog.setAttribute('aria-labelledby', 'fjSendTitle');
  const title = document.createElement('h3');
  title.id = 'fjSendTitle';
  title.textContent = `Send ${file.name} to ${route.receiver_name}?`;
  const note = document.createElement('p');
  note.setAttribute('role', 'status');
  note.textContent = 'Checking the connection…';
  const cancel = document.createElement('button');
  cancel.type = 'button';
  cancel.className = 'fj-btn fj-btn-secondary';
  cancel.textContent = 'Cancel';
  cancel.onclick = () => dialog.close();
  dialog.append(title, note, cancel);
  dialog.addEventListener('close', () => dialog.remove(), { once: true });
  document.body.append(dialog);
  dialog.showModal();
  const refused =
    'The file, connection, or destination changed. Nothing was sent. Close and review Downloads again.';
  try {
    const query = new URLSearchParams({
      revision: binding.revision,
      group_revision: binding.groupRevision
    });
    const response = await fetch(url + '?' + query);
    const { plan } = await response.json();
    if (!dialog.open) return;
    if (
      !current() ||
      !response.ok ||
      plan?.total !== 1 ||
      plan.send_count !== 1 ||
      plan.sends?.[0]?.connection_id !== route.connection_id ||
      plan.sends[0].receiver_workspace_id !== route.receiver_workspace_id ||
      !plan.plan_digest
    )
      throw new Error(refused);
    if (plan.sends[0].name_used) {
      note.textContent = 'That name is already in use at the destination. Nothing was sent.';
      return;
    }
    note.textContent = `Only ${file.name} will move, with its name unchanged. It will arrive at the top level of ${route.receiver_name}. Filing it there is a separate decision.`;
    const confirm = document.createElement('button');
    confirm.type = 'button';
    confirm.className = 'fj-btn fj-btn-primary';
    confirm.textContent = 'Confirm send';
    const key = crypto.randomUUID();
    confirm.onclick = async () => {
      confirm.disabled = true;
      if (!current()) {
        note.textContent = refused;
        return;
      }
      try {
        const response = await fetch(url, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            expected_revision: plan.revision,
            expected_group_revision: plan.group_revision,
            plan_digest: plan.plan_digest,
            client_key: key
          })
        });
        const body = await response.json();
        if (!response.ok) {
          note.textContent =
            body.message ||
            body.error?.message ||
            'Unable to confirm the send. Check Filing History.';
          return;
        }
        dialog.close();
        await onFiled(body.filing);
      } catch {
        note.textContent =
          'The result could not be confirmed. Check Filing History before trying again.';
      }
    };
    dialog.insertBefore(confirm, cancel);
    confirm.focus();
  } catch {
    note.textContent = refused;
  }
}
window.DownloadsDestinationReview = { renderDownloadsReview, reviewSend };
