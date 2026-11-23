import { receiptHint } from './hq-guided-attempt.js';

// One explicit, assistant-owned review. The answer lives only in this dialog;
// neither the tab return hint nor a URL holds draft text. A failed save keeps
// exactly the confirmation the user reviewed, including its request id.
export function openDocumentsDraft({ hint, offer, project, trigger }) {
  if (document.querySelector('dialog[data-documents-review]')) return;
  const dialog = document.createElement('dialog');
  dialog.dataset.documentsReview = '';
  dialog.className = 'ws-documents-review';
  dialog.setAttribute('aria-labelledby', 'documents-draft-heading');
  const node = (tag, text) => {
    const el = document.createElement(tag);
    if (text) el.textContent = text;
    return el;
  };
  const title = node('h2', 'Draft a project note');
  title.id = 'documents-draft-heading';
  const content = node('section'),
    actions = node('footer'),
    message = node('p');
  message.setAttribute('role', 'status');
  dialog.append(node('small', 'Personal Assistant · Notes'), title, content, message, actions);
  document.body.append(dialog);
  const current = () => {
    const now = receiptHint(window.location, hint.hq);
    return dialog.open && now?.attempt === hint.attempt && now?.workspace === hint.workspace;
  };
  let confirmed,
    review,
    busy = false;
  const name = node('input');
  name.value = project.name;
  name.maxLength = 100;
  const answer = node('textarea');
  answer.maxLength = 500;
  answer.rows = 3;
  const close = () => dialog.close();
  const cleanup = () => {
    dialog.remove();
    window.removeEventListener('pagehide', close);
    if (trigger?.isConnected) trigger.focus();
  };
  dialog.addEventListener('close', cleanup, { once: true });
  window.addEventListener('pagehide', close, { once: true });
  const button = (label, fn) => {
    const b = node('button', label);
    b.type = 'button';
    b.className = 'ws-cmd-nav-btn';
    b.onclick = async () => {
      if (busy || !current()) return;
      busy = true;
      for (const el of actions.children) el.disabled = true;
      message.textContent = '';
      try {
        await fn();
      } catch (err) {
        if (current())
          message.textContent = err.message || 'Could not verify the result. Try again.';
      } finally {
        busy = false;
        for (const el of actions.children) el.disabled = false;
      }
    };
    actions.append(b);
    return b;
  };
  const send = async (action, data) => {
    const r = await fetch(
      `/api/personal-assistant/folder-agent/${encodeURIComponent(hint.workspace)}/documents-draft/${action}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
      }
    );
    const raw = await r.json();
    if (!current()) throw Error('This return has changed.');
    if (!r.ok || !raw.draft) throw Error(raw.error || 'The result could not be verified.');
    return raw.draft;
  };
  const label = (text, el) => {
    const l = node('label', text);
    l.append(el);
    return l;
  };
  const edit = () => {
    confirmed = null;
    review = null;
    title.textContent = 'Draft a project note';
    content.replaceChildren(
      node('p', 'Source: ' + project.location),
      label('Project name', name),
      label('What are you trying to finish?', answer)
    );
    actions.replaceChildren();
    button('Review note', async () => {
      const input = {
        hq: hint.hq,
        attempt: hint.attempt,
        report: offer.report,
        candidate: project.ref,
        name: name.value,
        answer: answer.value
      };
      review = await send('review', input);
      confirmed = { ...input, review: review.review, request_id: crypto.randomUUID() };
      preview();
    });
    button('Cancel', close);
    answer.focus();
  };
  const preview = () => {
    title.textContent = 'Review your note';
    content.replaceChildren(
      node('p', `Destination: ${review.notebook}/${review.rel}`),
      node('pre', review.body)
    );
    actions.replaceChildren();
    button('Back to edit', edit);
    button('Save to My Notes', async () => {
      const saved = await send('save', confirmed);
      if (!saved.saved) throw Error('Save was not confirmed.');
      title.textContent = 'Project note saved';
      content.replaceChildren(node('p', `${saved.notebook}/${saved.rel}`));
      actions.replaceChildren();
      button(`Open ${saved.name} note`, async () => {
        const actual = await send('open', {
          hq: hint.hq,
          attempt: hint.attempt,
          request_id: confirmed.request_id
        });
        title.textContent = actual.name;
        content.replaceChildren(
          node('p', `${actual.notebook}/${actual.rel}`),
          node('pre', actual.body)
        );
        actions.replaceChildren();
        button('Close', close).focus();
      }).focus();
      button('Close', close);
    }).focus();
    button('Cancel', close);
  };
  dialog.showModal();
  edit();
}
