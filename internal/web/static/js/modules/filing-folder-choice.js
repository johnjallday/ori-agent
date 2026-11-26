// A separate, exact selection within Review. Nothing else ticked in the
// picture participates. The chosen path goes only to the trusted picker;
// Filing receives a scoped token (never a browser-supplied path).
export function openFolderChoice({ workspace, endpoint, selection, current, onFiled, suggested }) {
  document.getElementById('filingFolderChoice')?.close();
  const dialog = document.createElement('dialog');
  dialog.id = 'filingFolderChoice';
  dialog.className = 'fj-destination-dialog';
  dialog.setAttribute('aria-labelledby', 'filingFolderChoiceTitle');
  const make = (tag, text, cls) => {
    const node = document.createElement(tag);
    node.textContent = text;
    if (cls) node.className = cls;
    return node;
  };
  const title = make('h3', 'File to an existing folder');
  title.id = 'filingFolderChoiceTitle';
  dialog.append(
    title,
    make(
      'p',
      'Only the files in this preview will move. This does not let an agent browse or watch the destination.'
    )
  );
  const body = make('div', '');
  const message = make('p', '');
  message.setAttribute('role', 'status');
  const button = (text, fn, primary = false) => {
    const n = make('button', text, primary ? 'fj-btn fj-btn-primary' : 'fj-btn fj-btn-secondary');
    n.type = 'button';
    n.addEventListener('click', fn);
    return n;
  };
  const cancel = button('Cancel', () => dialog.close());
  dialog.append(body, message, cancel);
  dialog.addEventListener('close', () => dialog.remove(), { once: true });
  document.body.append(dialog);
  dialog.showModal();
  const live = () => dialog.open && current();
  const post = async (url, args) => {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(args)
    });
    const reply = await res.json();
    if (!res.ok)
      throw new Error(
        (typeof reply.error === 'string' ? reply.error : reply.error?.message) ||
          'The review changed. Close and try again.'
      );
    return reply;
  };
  let working = false;
  const run = async fn => {
    if (working) return;
    if (!live()) {
      dialog.close();
      return;
    }
    working = true;
    body.inert = true;
    message.textContent = '';
    try {
      await fn();
    } catch (e) {
      if (dialog.open) message.textContent = e.message;
    } finally {
      working = false;
      body.inert = false;
    }
  };
  const preview = async target => {
    const args = { selection, ...target };
    const { preview, destination, warning_note: warning } = await post(endpoint, args);
    if (!live()) return;
    if (!preview?.digest || preview.state !== 'ready')
      throw new Error(
        preview?.state === 'unsupported'
          ? 'These folders do not support a safe move on the same disk. Nothing moved.'
          : 'The folder or files changed or cannot be used. Nothing moved.'
      );
    title.textContent = 'File to ' + preview.folder + '?';
    const list = make('ul', '');
    for (const item of preview.items) list.append(make('li', item.name));
    const key = crypto.randomUUID();
    body.replaceChildren(
      make('p', destination, 'fj-destination-path'),
      ...(warning ? [make('p', warning)] : []),
      list,
      make('p', 'Files keep their names. An occupied name is never replaced.'),
      button(
        'File',
        () =>
          void run(async () => {
            const { result } = await post(endpoint + '?confirm=true', {
              ...args,
              plan_digest: preview.digest,
              client_key: key
            });
            dialog.close();
            await onFiled(result);
          }),
        true
      ),
      button('Change folder', chooser)
    );
    body.querySelector('button').focus();
  };
  const pick = path =>
    run(async () => {
      const picked = await post('/api/folder-picker/select-path', {
        workspace_id: workspace,
        title: 'Choose a filing destination',
        ...(path ? { path } : {})
      });
      if (!live()) return;
      if (!picked.selected) {
        message.textContent =
          picked.reason === 'dialog_off'
            ? 'The folder dialog is off. Paste a folder path below.'
            : 'No folder chosen.';
        return;
      }
      await preview({ selection_token: picked.selection_token });
    });
  function chooser() {
    title.textContent = 'File to an existing folder';
    const input = make('input', '', 'fj-input');
    input.type = 'text';
    input.placeholder = '~/Documents/Travel/Receipts';
    input.setAttribute('aria-label', 'Destination folder path');
    body.replaceChildren(
      button('Choose a folder…', () => void pick('')),
      input,
      button('Preview folder', () => {
        if (input.value.trim()) void pick(input.value.trim());
        else input.focus();
      })
    );
  }
  chooser();
  if (suggested) void run(() => preview({ example: suggested }));
}
if (typeof window !== 'undefined') window.FilingFolderChoice = { openFolderChoice };
