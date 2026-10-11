// The full agent page's membership editor. One desired-set PUT through the
// existing server owner; the roster remains a reader. No task/provider fetches.
(function () {
  'use strict';

  const text = value => String(value ?? '').trim();
  const sameSet = (a, b) => a.size === b.size && [...a].every(id => b.has(id));

  function editable(agent) {
    const name = text(agent?.name).toLowerCase();
    return (
      Boolean(agent?.version) &&
      !['ori', 'ask ori', '__assistant__', 'workspace manager'].includes(name) &&
      agent?.source !== 'cli' &&
      agent?.role !== 'cli_agent' &&
      agent?.state !== 'unreadable' &&
      !['system', 'workspace'].includes(agent?.origin?.source)
    );
  }

  function membership(agent) {
    const refs = agent?.workspaces;
    const count = agent?.workspace_count;
    if (!Array.isArray(refs) && !(count === 0 && refs == null)) {
      throw new Error('Workspace membership is unavailable.');
    }
    const members = new Map();
    for (const ref of refs || []) {
      const id = text(ref?.id);
      if (!id) throw new Error('Workspace membership is incomplete.');
      members.set(id, { ...ref, id, entry_point: Boolean(ref.entry_point) });
    }
    if (count !== undefined && (!Number.isInteger(count) || count !== members.size)) {
      throw new Error('Workspace membership is incomplete.');
    }
    return members;
  }

  function signature(members) {
    return JSON.stringify([...members].map(([id, ref]) => [id, ref.entry_point]).sort());
  }

  function workspaceRows(payload, members) {
    const list = Array.isArray(payload) ? payload : payload?.workspaces || payload?.folders;
    if (!Array.isArray(list)) throw new Error('The workspace list is unavailable.');
    const rows = new Map();
    const visit = entries =>
      entries.forEach(ws => {
        const id = text(ws?.id);
        if (!id) throw new Error('The workspace list is incomplete.');
        rows.set(id, {
          ...ws,
          id,
          available: true,
          entry_point: members.get(id)?.entry_point || false
        });
        if (Array.isArray(ws.children)) visit(ws.children);
      });
    visit(list);
    for (const [id, member] of members) {
      if (!rows.has(id)) rows.set(id, { ...member, available: false });
    }
    return [...rows.values()].sort(
      (a, b) => text(a.name).localeCompare(text(b.name)) || a.id.localeCompare(b.id)
    );
  }

  // Preserve deliberate edits on reload, but never strip a newly protected
  // entry agent or silently detach a membership missing from the collection.
  function rebaseSelection(previous, selected, members, rows) {
    const next = new Set(members.keys());
    const available = new Set(rows.filter(row => row.available).map(row => row.id));
    for (const id of selected) if (!previous.has(id) && available.has(id)) next.add(id);
    for (const id of previous.keys()) {
      if (!selected.has(id) && available.has(id) && !members.get(id)?.entry_point) next.delete(id);
    }
    return next;
  }

  function mount({ root, readAgent, onSaved = () => {} }) {
    if (!root) return null;
    const list = root.querySelector('[data-workspace-list]');
    const status = root.querySelector('[data-workspace-status]');
    const fields = root.querySelector('fieldset');
    const save = root.querySelector('[data-workspace-save]');
    const reload = root.querySelector('[data-workspace-reload]');
    let agent = null;
    let members = new Map();
    let selected = new Set();
    let rows = [];
    let ready = false;
    let busy = false;
    let generation = 0;

    const dirty = () => !sameSet(new Set(members.keys()), selected);
    const controls = () => {
      fields.disabled = busy || !ready || !editable(agent);
      save.disabled = busy || !ready || !editable(agent) || !dirty();
      reload.disabled = busy;
      save.textContent = busy ? 'Working…' : 'Save membership';
      root.setAttribute('aria-busy', String(busy));
    };
    const message = value => {
      status.textContent = value;
    };
    const element = (tag, content) => {
      const node = document.createElement(tag);
      if (content !== undefined) node.textContent = content;
      return node;
    };
    const render = () => {
      const focusedID = document.activeElement?.dataset?.workspaceId;
      list.replaceChildren();
      for (const row of rows) {
        const item = element('li');
        const label = element('label');
        const input = element('input');
        input.type = 'checkbox';
        input.dataset.workspaceId = row.id;
        input.checked = selected.has(row.id);
        input.disabled = Boolean(row.entry_point || !row.available);
        label.append(input, element('span', row.name || 'Unnamed workspace'));
        item.append(label);
        const notes = [];
        if (row.kind === 'group') notes.push('Group');
        if (row.entry_point)
          notes.push(
            'Entry agent — choose another entry agent in the workspace before removing this one.'
          );
        if (!row.available) notes.push('Not in the loaded workspace list; membership is kept.');
        if (notes.length) item.append(element('p', notes.join(' · ')));
        if (text(row.folder_slug)) {
          const link = element('a', 'Open workspace');
          const search = members.has(row.id)
            ? `?agent=${encodeURIComponent(text(agent.name).toLowerCase())}`
            : '';
          link.href = `/workspaces/${encodeURIComponent(text(row.folder_slug))}${search}`;
          link.setAttribute('aria-label', `Open ${row.name || 'workspace'}`);
          item.append(link);
        } else item.append(element('span', 'Workspace link unavailable'));
        list.append(item);
        if (focusedID === row.id) input.focus();
      }
      if (!rows.length)
        list.append(
          element('li', 'No workspaces yet. Create one from Home, then reload this list.')
        );
      controls();
    };

    async function load({ initial = false } = {}) {
      if (busy || !agent) return;
      const current = ++generation;
      const restoreFocus = !initial && document.activeElement === reload;
      busy = true;
      ready = false;
      message('Loading workspaces and membership…');
      controls();
      try {
        const [latest, response] = await Promise.all([
          initial ? Promise.resolve(agent) : readAgent(),
          fetch('/api/workspaces')
        ]);
        if (!response.ok) throw new Error('Workspaces could not be loaded.');
        const nextMembers = membership(latest);
        const nextRows = workspaceRows(await response.json(), nextMembers);
        if (generation !== current) return;
        if (text(latest.name) !== text(agent.name))
          throw new Error('The agent changed. Reload the page before editing membership.');
        selected = initial
          ? new Set(nextMembers.keys())
          : rebaseSelection(members, selected, nextMembers, nextRows);
        agent = latest;
        members = nextMembers;
        rows = nextRows;
        ready = true;
        onSaved({ workspace_count: members.size, workspaces: [...members.values()] });
        message(
          editable(agent)
            ? dirty()
              ? 'Membership reloaded. Review your unsaved choices before saving.'
              : 'Membership loaded. Changes take effect only when you save.'
            : 'This agent is read-only here. Manage workspace-owned agents in their workspace; built-in agents cannot be assigned here.'
        );
        render();
      } catch (error) {
        if (generation === current)
          message(`${error.message} Your choices are kept here. Reload to try again.`);
      } finally {
        if (generation === current) {
          busy = false;
          controls();
          if (restoreFocus && document.activeElement === document.body) status.focus();
        }
      }
    }

    async function submit() {
      if (busy || !ready || !editable(agent) || !dirty()) return;
      const restoreFocus = document.activeElement === save;
      busy = true;
      message('Checking current membership…');
      controls();
      try {
        const latest = await readAgent();
        if (!editable(latest) || text(latest.name) !== text(agent.name))
          throw new Error('This agent can no longer be edited here.');
        if (signature(membership(latest)) !== signature(members)) {
          throw new Error(
            'Membership changed elsewhere. Reload it and review your choices before saving.'
          );
        }
        const response = await fetch(`/api/agents/${encodeURIComponent(agent.name)}/workspaces`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ workspace_ids: [...selected].sort() })
        });
        const body = await response.json();
        if (!response.ok)
          throw new Error(body.message || body.error || 'The server could not save membership.');
        members = membership(body);
        selected = new Set(members.keys());
        agent = { ...latest, workspace_count: members.size, workspaces: [...members.values()] };
        rows = rows.map(row => ({
          ...row,
          entry_point: members.get(row.id)?.entry_point || false
        }));
        onSaved({ workspace_count: members.size, workspaces: [...members.values()] });
        message(
          'Workspace membership saved. Open a workspace to see tasks or give this agent work.'
        );
        render();
      } catch (error) {
        ready = false;
        // The existing endpoint can fail after a partial write. Never imply
        // rollback, retry automatically, or submit the old baseline again.
        message(
          `${error.message} Save was not confirmed. Your choices are kept. Reload current membership before saving again.`
        );
      } finally {
        busy = false;
        controls();
        if (restoreFocus && document.activeElement === document.body) status.focus();
      }
    }

    list.addEventListener('change', event => {
      const id = event.target?.dataset?.workspaceId;
      if (!id || busy || !ready || !editable(agent)) return;
      const row = rows.find(candidate => candidate.id === id);
      if (!row?.available || row.entry_point) return;
      if (event.target.checked) selected.add(id);
      else selected.delete(id);
      message(
        dirty()
          ? 'Unsaved membership changes. Save to apply them.'
          : 'No unsaved membership changes.'
      );
      controls();
    });
    save.addEventListener('click', submit);
    reload.addEventListener('click', () => {
      void load();
    });
    window.addEventListener('beforeunload', event => {
      if (!dirty() && !busy) return;
      event.preventDefault();
      event.returnValue = '';
    });
    return {
      setAgent(value) {
        const first = !agent || agent.name !== value?.name;
        agent = value;
        root.hidden = !agent;
        if (first && agent) void load({ initial: true });
        else controls();
      }
    };
  }

  window.AgentWorkspaceEditor = {
    mount,
    editable,
    membership,
    workspaceRows,
    rebaseSelection,
    signature
  };
})();
