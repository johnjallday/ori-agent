/*
 * The dashboard itself. Reads live workspace data over the Ori bridge and
 * renders it. Everything here is ordinary DOM code — the only Ori-specific
 * part is Ori.invoke().
 */
(function () {
  const text = value => (typeof value === 'string' ? value : '');

  function card(value, label) {
    const node = document.createElement('div');
    node.className = 'card';
    const v = document.createElement('div');
    v.className = 'value';
    v.textContent = value === undefined || value === null ? '—' : String(value);
    const l = document.createElement('div');
    l.className = 'label';
    l.textContent = label;
    node.append(v, l);
    return node;
  }

  function row(primary, secondary) {
    const li = document.createElement('li');
    const left = document.createElement('span');
    // textContent, never innerHTML. Workspace data is data, not markup.
    left.textContent = primary;
    const right = document.createElement('span');
    right.className = 'status';
    right.textContent = secondary;
    li.append(left, right);
    return li;
  }

  function fill(listId, items, render) {
    const list = document.getElementById(listId);
    list.replaceChildren();
    if (!items || items.length === 0) {
      const empty = document.createElement('li');
      empty.className = 'empty';
      empty.textContent = 'Nothing here yet.';
      list.appendChild(empty);
      return;
    }
    for (const item of items) list.appendChild(render(item));
  }

  function showError(message) {
    const box = document.createElement('div');
    box.className = 'error';
    box.textContent = message;
    document.body.prepend(box);
  }

  async function load() {
    await Ori.whenReady();
    try {
      // Operation 1: the workspace summary and its headline counts.
      const summary = await Ori.invoke('workspace.summary');
      document.getElementById('title').textContent = text(summary.name) || 'Workspace';
      document.getElementById('subtitle').textContent = [
        text(summary.kind),
        text(summary.designation)
      ]
        .filter(Boolean)
        .join(' · ');

      const counts = summary.counts || {};
      document
        .getElementById('cards')
        .replaceChildren(
          card(counts.open_tasks, 'Open tasks'),
          card(counts.notes, 'Notes'),
          card(counts.agents, 'Agents'),
          card(counts.sessions, 'Sessions')
        );

      // Operation 2: the open task list.
      const tasks = await Ori.invoke('workspace.tasks.list', { status: 'open', limit: 10 });
      fill('tasks', tasks.tasks, task => row(text(task.title), text(task.status)));

      // Operation 3: notes.
      const notes = await Ori.invoke('workspace.notes.list', { limit: 10 });
      fill('notes', notes.notes, note => row(text(note.title), text(note.updated_at)));
    } catch (error) {
      showError('Could not load workspace data: ' + error.message);
    }
  }

  void load();
})();
