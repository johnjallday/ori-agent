// Presentation only. WorkspaceCommandView and the existing managers own actions,
// form state, URL state and permissions. Never derive plugin identity from a name.
export function chromeText(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]
  );
}

export function workspaceNavigationHTML({
  mode,
  dashboard = false,
  plans = '',
  canvas = '',
  ticketCount = '—',
  ticketDescription = '',
  ticketAlert = false
}) {
  const modes = [
    ['details', 'Overview'],
    ['map', 'Map'],
    ['tickets', 'Tickets'],
    ...(dashboard ? [['dashboard', 'Dashboard']] : [])
  ];
  return (
    '<nav class="ws-cmd-view-switch" aria-label="Workspace views">' +
    modes
      .map(
        ([key, label]) =>
          `<button type="button" class="ws-cmd-view-btn${mode === key ? ' is-active' : ''}${key === 'tickets' && ticketAlert ? ' is-alert' : ''}" data-cmd-view-mode="${key}" aria-pressed="${mode === key}"${key === 'tickets' && ticketDescription ? ` title="${chromeText(ticketDescription)}"` : ''}>${label}${key === 'tickets' ? ` <span data-cmd-ticket-count>${chromeText(ticketCount)}</span>` : ''}</button>`
      )
      .join('') +
    '<span class="ws-cmd-program-destinations" data-cmd-program-destinations></span>' +
    (plans || canvas
      ? '<details class="ws-cmd-disclosure" data-cmd-disclosure="views"><summary class="ws-cmd-view-btn">More views</summary>' +
        '<div class="ws-cmd-disclosure-body">' +
        plans +
        (canvas
          ? `<a class="ws-cmd-view-btn ws-cmd-view-link" href="${chromeText(canvas)}">Canvas</a>`
          : '') +
        '</div></details>'
      : '') +
    '</nav>'
  );
}

// Transitional access to the real owners. Group 2 consolidates these into the
// persistent Settings host; no configuration is saved by opening this disclosure.
export function workspaceSettingsHTML() {
  return '<button type="button" class="ws-cmd-nav-btn" data-cmd-settings aria-haspopup="dialog">Settings</button>';
}
