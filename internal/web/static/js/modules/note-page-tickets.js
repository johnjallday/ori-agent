/**
 * Linked tickets on the Note page
 * (tasks/prd-workspace-ticket-management.md FR-75, FR-78).
 *
 * Shows which tickets reference this note, with a state chip and a
 * workspace-qualified number, and links to each one.
 *
 * Read-only on purpose. The note surface DISPLAYS and NAVIGATES to tickets; it
 * is never a second place to mutate them, and a note gains no execution
 * controls by being referenced — a note is knowledge, not work.
 *
 * @module note-page-tickets
 */
(function () {
  'use strict';

  const SECTION_ID = 'notePageTickets';
  const LIST_ID = 'notePageTicketsList';

  /**
   * Resolves the workspace and note from the page.
   *
   * The dataset is set server-side in note.tmpl, so it is available on first
   * paint — unlike module state, which classic `defer` scripts race.
   */
  function routeFromPage() {
    if (typeof document === 'undefined') return { workspaceId: '', noteId: '' };
    const main = document.getElementById('noteMainContent');
    const workspaceId = (main && main.dataset.workspaceId) || '';
    const noteId = (main && main.dataset.noteId) || '';
    if (workspaceId && noteId) return { workspaceId, noteId };

    // Focused route: /workspaces/{workspaceId}/notes/{noteId}
    const match = String(window.location.pathname || '').match(
      /^\/workspaces\/([^/]+)\/notes\/([^/]+)/
    );
    if (match) {
      return { workspaceId: decodeURIComponent(match[1]), noteId: decodeURIComponent(match[2]) };
    }
    return { workspaceId, noteId };
  }

  function buildTicketRow(ticket) {
    const item = document.createElement('li');
    item.className = 'note-page-ticket';

    const link = document.createElement('a');
    link.className = 'note-page-ticket-link';
    link.href = `/workspaces/${encodeURIComponent(ticket.owningWorkspaceId)}?ticket=${encodeURIComponent(ticket.id)}`;

    const number = document.createElement('span');
    number.className = 'note-page-ticket-number';
    // Workspace-qualified so a ticket from a different workspace in the same
    // tree is unambiguous (FR-141).
    number.textContent = ticket.owningWorkspaceName
      ? `${ticket.owningWorkspaceName} ${ticket.displayNumber}`
      : ticket.displayNumber;

    const chip = document.createElement('span');
    chip.className = `ticket-state-chip ticket-state-${ticket.state}`;
    // Real text, not a color swatch.
    chip.textContent = ticket.stateLabel;

    const title = document.createElement('span');
    title.className = 'note-page-ticket-title';
    title.textContent = ticket.title;

    link.appendChild(number);
    link.appendChild(chip);
    link.appendChild(title);
    item.appendChild(link);
    return item;
  }

  async function load() {
    if (typeof document === 'undefined') return;
    const section = document.getElementById(SECTION_ID);
    const list = document.getElementById(LIST_ID);
    if (!section || !list) return;

    const tickets = window.WorkspaceHubTickets;
    if (!tickets || !tickets.api || typeof tickets.api.ticketsForNote !== 'function') return;

    const { workspaceId, noteId } = routeFromPage();
    if (!workspaceId || !noteId) return;

    let linked = [];
    try {
      linked = await tickets.api.ticketsForNote(workspaceId, noteId);
    } catch {
      // A note that cannot load its tickets is still a perfectly good note.
      // Staying hidden beats showing a broken section on the editor.
      section.hidden = true;
      return;
    }

    list.replaceChildren();
    if (!linked.length) {
      // Nothing links here: no empty state, because most notes are not
      // referenced and an empty panel on every note is just noise.
      section.hidden = true;
      return;
    }

    linked.forEach(ticket => list.appendChild(buildTicketRow(ticket)));
    section.hidden = false;
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => void load());
    } else {
      void load();
    }
  }

  window.NotePageTickets = { load };
})();
