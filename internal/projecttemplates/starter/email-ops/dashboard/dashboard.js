/*
 * Email Ops dashboard. Reads this workspace's live data over the Ori bridge.
 * Everything here is ordinary DOM code; the only Ori-specific call is
 * Ori.invoke(). See docs/features/custom-workspace-dashboard.md.
 */
(function () {
  const text = value => (typeof value === 'string' ? value : '');

  function set(id, value) {
    document.getElementById(id).textContent = value === undefined || value === null ? '—' : String(value);
  }

  function fill(listId, items, render) {
    const list = document.getElementById(listId);
    list.replaceChildren();
    if (!items || items.length === 0) {
      const empty = document.createElement('li');
      empty.className = 'empty';
      empty.textContent = 'Nothing right now.';
      list.appendChild(empty);
      return;
    }
    for (const item of items) list.appendChild(render(item));
  }

  function row(primary, secondary) {
    const li = document.createElement('li');
    const left = document.createElement('span');
    // textContent, never innerHTML: workspace data is data, not markup.
    left.textContent = primary;
    const right = document.createElement('span');
    right.className = 'meta';
    right.textContent = secondary;
    li.append(left, right);
    return li;
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
      const summary = await Ori.invoke('workspace.summary');
      document.getElementById('title').textContent = text(summary.name) || 'Email Ops';
      document.getElementById('subtitle').textContent =
        text(summary.description) || 'Triage, draft, and follow up — nothing sends without you.';

      const counts = summary.counts || {};
      set('openCount', counts.open_tasks);
      set('crewCount', counts.agents);
      set('noteCount', counts.notes);
      set('sessionCount', counts.sessions);

      const tasks = await Ori.invoke('workspace.tasks.list', { status: 'open', limit: 10 });
      fill('tasks', tasks.tasks, task => row(text(task.title), text(task.status)));

      const agents = await Ori.invoke('workspace.agents.list', { limit: 10 });
      fill('crew', agents.agents, agent =>
        row(text(agent.name), text(agent.role) || (agent.entry_point ? 'entry point' : ''))
      );
    } catch (error) {
      showError('Could not load workspace data: ' + error.message);
    }
  }

  void load();
})();
