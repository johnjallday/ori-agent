// A manual, names-only look. No listing request is made when the workspace opens.
export function bindProjectDiscovery(trigger, panel, id, available) {
  if (!trigger || !panel) return;
  trigger.hidden = !available;
  if (!available || panel.dataset?.workspace !== id) panel.hidden = true;
  if (!available) return;
  if (panel.dataset.workspace === id && trigger.onclick) return;
  panel.dataset.workspace = id;
  function element(tag, text, css) {
    const node = document.createElement(tag);
    node.textContent = text;
    if (css) node.className = css;
    return node;
  }
  function button(name, fn) {
    const b = element('button', name, 'btn btn-outline-primary btn-sm');
    b.type = 'button';
    b.onclick = fn;
    return b;
  }
  const present = (...nodes) => {
    panel.hidden = false;
    panel.replaceChildren(...nodes);
  };
  async function request(action, params, onSuccess) {
    present(element('p', 'Checking folder names…'));
    try {
      const r = await fetch(`/api/workspaces/${encodeURIComponent(id)}/documents-projects`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action, ...params })
      });
      if (!r.ok) throw Error();
      const data = await r.json();
      if (!trigger.hidden && panel.dataset.workspace === id) onSuccess(data);
    } catch {
      present(element('p', 'The folder could not be verified. Choose it again for a fresh look.'));
      trigger.focus();
    }
  }
  function find(parent) {
    return request('find', { parent }, data => showResults(parent, data));
  }
  function showResults(parent, data) {
    present(element('h3', 'What I found', 'h6'));
    for (const c of data.suggestions || []) {
      const card = element('article', '', 'py-2');
      card.setAttribute('aria-label', c.folder);
      const actions = element('div', '', 'documents-look__actions mt-2');
      const params = { parent, candidate: c.ref };
      const dismiss = () => {
        data.suggestions = data.suggestions.filter(row => row !== c);
        showResults(parent, data);
      };
      const review = button('Review setup', () => {
        const heading = element('h3', 'Review course setup', 'h5');
        const confirm = button('Open course workspace', () =>
          request('open', params, result => {
            location.assign(result.route);
          })
        );
        present(
          heading,
          element('p', `Folder: Documents/${c.folder}`),
          element(
            'p',
            `Open the existing workspace, or create a course workspace with a manager. First, choose ${c.syllabus} and approve reading it to find the next stated deadline. Your originals stay in place.`
          ),
          element(
            'p',
            'The Course intake skill needs separate review. No folder access, recurring scan or calendar connection is added.',
            'text-body-secondary'
          ),
          confirm,
          button('Cancel', () => showResults(parent, data))
        );
        confirm.focus();
      });
      actions.append(
        review,
        button('Not now', dismiss),
        button('Ignore this folder', () => request('ignore', params, dismiss))
      );
      card.append(
        element(
          'p',
          `I found ${c.folder}/${c.syllabus} and ${c.folder}/${c.structure}/. Is this a course you want help tracking?`
        ),
        actions
      );
      panel.append(card);
    }
    if (data.partial)
      panel.append(element('p', 'This look is incomplete. Some entries could not be checked.'));
    else if (!data.suggestions?.length)
      panel.append(element('p', 'No convincing project match in this folder.'));
    panel.tabIndex = -1;
    panel.focus();
  }
  trigger.onclick = () =>
    request('folders', {}, data => {
      const label = element('label', 'Choose a folder in Documents', 'form-label');
      const select = element('select', '', 'form-select');
      for (const c of data.folders || []) select.add(new Option(c.name, c.ref));
      label.append(select);
      present(
        label,
        element(
          'p',
          'One look at names and folder structure. No file contents are read or sent to a model.',
          'text-body-secondary my-2'
        ),
        button('Look for projects', () => find(select.value))
      );
      if (data.partial) panel.append(element('p', 'Only part of Documents could be listed.'));
      select.focus();
    });
}
