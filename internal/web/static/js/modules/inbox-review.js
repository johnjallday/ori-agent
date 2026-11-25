import { notesRequest } from './notes-api.js';
import { workspacePageURL } from './workspace-routes.js';

export function ticketForSuggestion(item, workspaces) {
  const target = workspaces.find(ws => ws.id === item.workspace_id);
  return item.state === 'accepted' && target?.slug && item.ticket_id
    ? workspacePageURL(target.slug, [], {
        search: new URLSearchParams({ ticket: item.ticket_id }).toString()
      })
    : '';
}

// One decision changes one section. Other workspace choices, reader selection
// and scroll remain intact. If the reader has left, a late response is inert.
export function suggestionSection(item, workspaces, { ref, name, current, focusReader }) {
  const node = (tag, text = '') => {
    const n = document.createElement(tag);
    n.textContent = text;
    return n;
  };
  const btn = (text, primary = false) => {
    const b = node('button', text);
    b.type = 'button';
    b.className = `modern-btn modern-btn-${primary ? 'primary' : 'secondary'} modern-btn-sm`;
    return b;
  };
  const section = node('section');
  section.className = 'my-notes-suggestion';
  const paint = () => {
    const accepted = item.state === 'accepted';
    const heading = node('h4', accepted ? 'Linked backlog ticket' : 'Suggested ticket');
    heading.tabIndex = -1;
    section.replaceChildren(heading, node('blockquote', item.excerpt), node('strong', item.title));
    if (accepted) {
      const href = ticketForSuggestion(item, workspaces);
      if (href) {
        const target = workspaces.find(ws => ws.id === item.workspace_id);
        const link = node('a', `Open ticket in ${target.name}`);
        link.href = href;
        // Back from the ticket should reopen this exact note even if it was
        // originally chosen from Browse rather than a capture receipt.
        link.addEventListener('click', () =>
          history.replaceState(history.state, '', '#' + new URLSearchParams({ ref, note: name }))
        );
        section.append(link);
      } else section.append(node('p', 'The ticket’s workspace is unavailable.'));
      return;
    }
    const locked = item.state === 'claiming';
    const select = node('select');
    select.className = 'form-select';
    select.id = `inbox-target-${item.id}`;
    const label = node('label', 'Change workspace');
    label.htmlFor = select.id;
    select.add(new Option('Choose workspace', ''));
    for (const ws of workspaces) select.add(new Option(ws.name, ws.id));
    select.value = locked ? item.workspace_id : '';
    select.disabled = locked || !workspaces.length;
    const accept = btn(locked ? 'Retry confirmation' : 'Create ticket', true);
    accept.disabled = !select.value;
    const dismiss = btn('Keep as note');
    dismiss.disabled = locked;
    const actions = node('div');
    actions.className = 'my-notes-reader-actions';
    actions.append(accept, dismiss);
    const status = node(
      'p',
      locked
        ? 'This decision is waiting for confirmation in its original workspace.'
        : workspaces.length
          ? 'Creates an unassigned backlog ticket. Nothing runs.'
          : 'No active workspace is available.'
    );
    status.setAttribute('role', 'status');
    section.append(label, select, status, actions);
    select.addEventListener('change', () => {
      accept.disabled = !select.value;
    });
    const decide = async decision => {
      if ((decision === 'accept' ? accept : dismiss).disabled) return;
      const target = select.value;
      accept.disabled = dismiss.disabled = select.disabled = true;
      status.textContent = 'Confirming…';
      try {
        const body = {
          source_ref: ref,
          name,
          action_id: item.id,
          revision: item.revision,
          decision,
          ...(decision === 'accept' ? { workspace_id: target } : {})
        };
        const { suggestion } = await notesRequest('/inbox/decision', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body)
        });
        if (!current() || !section.isConnected) return;
        if (suggestion?.id !== item.id || !['accepted', 'dismissed'].includes(suggestion.state))
          throw new Error('The decision was not confirmed. Reopen the note to check.');
        Object.assign(item, suggestion);
        if (item.state === 'dismissed') {
          section.remove();
          focusReader();
        } else {
          paint();
          (section.querySelector('a') || section.querySelector('h4')).focus({
            preventScroll: true
          });
        }
      } catch (error) {
        if (!current() || !section.isConnected) return;
        status.textContent = error.message || 'Could not confirm. Reopen the note or retry.';
        select.disabled = locked || !workspaces.length;
        accept.disabled = !select.value;
        dismiss.disabled = locked;
      }
    };
    accept.addEventListener('click', () => void decide('accept'));
    dismiss.addEventListener('click', () => void decide('dismiss'));
  };
  paint();
  return section;
}
