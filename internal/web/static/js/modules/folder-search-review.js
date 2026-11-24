// Only an opaque, short-lived selection crosses the same-tab handoff. Paths,
// labels and filenames never enter browser storage, history or query strings.
const handoffKey = 'ori.folderSearchReview.v1';
async function post(op, body) {
  const response = await fetch(`/api/file-search/${op}`, {
    method: 'POST',
    cache: 'no-store',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  if (!response.ok) {
    const message = (await response.text()).trim();
    throw new Error(
      message && message.length < 160 && !message.includes('<')
        ? message
        : 'The folder could not be verified. Choose it again.'
    );
  }
  return response.json();
}
export async function chooseSearchFolder(kind) {
  const { intent } = await post('begin', { kind });
  return post('review', { intent });
}
// Both entry points use this same review for an existing workspace. The
// opaque IDs identify an offered link; the server checks it again on submit.
export async function reviewExisting(review, host = document.body) {
  const matches = review.matches || [];
  if (!matches.length) return null;
  const connect = (m, verify = false) =>
    post('connect', {
      intent: review.id,
      workspace: m.id,
      link: m.link,
      verify_current_link: verify
    });
  const connected = matches.find(m => m.connected);
  if (connected) return connect(connected);
  const el = (tag, text) => {
    const node = document.createElement(tag);
    node.textContent = text;
    return node;
  };
  const dialog = el('dialog', '');
  dialog.setAttribute('aria-label', 'Connect folder to workspace');
  const title = el('h2', `Connect ${review.label} to a workspace`);
  const details = el(
    'p',
    review.source
      ? 'Keep the existing filename index. No workspace, agent or ticket will be created.'
      : 'Make filenames, locations and dates searchable on this Mac. File contents are not indexed. No workspace, agent or ticket will be created.'
  );
  const label = el('label', 'Workspace '),
    select = el('select', '');
  label.append(select);
  if (matches.length > 1) select.add(new Option('Choose a workspace…', ''));
  matches.forEach((m, i) =>
    select.add(new Option(`${m.name} · ${m.slug} · ${m.id.slice(0, 8)}`, String(i)))
  );
  const confirm = el('button', review.source ? 'Connect to workspace' : 'Allow filename search'),
    cancel = el('button', 'Cancel');
  confirm.type = cancel.type = 'button';
  const verify = el('input', '');
  verify.type = 'checkbox';
  const attestation = el('label', '');
  attestation.append(
    verify,
    document.createTextNode(
      ' I confirm this is the folder currently linked to this workspace. Its original disk identity was not recorded.'
    )
  );
  const chosen = () => (select.value === '' ? null : matches[Number(select.value)]);
  const update = () => {
    attestation.hidden = !chosen()?.verify;
    confirm.disabled = !chosen() || (chosen().verify && !verify.checked);
  };
  select.onchange = () => {
    verify.checked = false;
    update();
  };
  verify.onchange = update;
  update();
  dialog.append(title, details, label, attestation, cancel, confirm);
  host.append(dialog);
  cancel.onclick = () => dialog.close('cancel');
  confirm.onclick = () => dialog.close('confirm');
  cancel.autofocus = true;
  try {
    dialog.showModal();
    await new Promise(resolve => dialog.addEventListener('close', resolve, { once: true }));
    return dialog.returnValue === 'confirm' && chosen()
      ? await connect(chosen(), verify.checked)
      : null;
  } finally {
    dialog.remove();
  }
}

export function openFolderReview(review) {
  sessionStorage.setItem(handoffKey, review.id);
  location.assign('/?create=1&folder_review=1');
}
export async function resumeFolderReview() {
  const intent = sessionStorage.getItem(handoffKey);
  sessionStorage.removeItem(handoffKey);
  if (!intent) throw new Error('Choose the folder again in Files.');
  return post('resume-review', { intent });
}
