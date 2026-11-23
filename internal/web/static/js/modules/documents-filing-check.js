// Read-only handoff. Do not import Filing's draft/confirm actions or open a
// default-ticked regular plan on behalf of a saved view.
export function documentsFilingCheck({ node, button, group, review, sources, route, post }) {
  const box = node('details');
  box.append(node('summary', 'Review filing'));
  const choices = node('fieldset');
  choices.append(node('legend', 'Choose loose documents (up to eight)'));
  const selected = new Set();
  const message = node('div');
  message.setAttribute('role', 'status');
  let generation = 0;
  const check = button('Check selected files', async () => {
    const ticket = ++generation;
    message.textContent = 'Checking the current Filing scan…';
    try {
      const { selection } = await post('organization-filing-selection', {
        review: review.id,
        group: group.id,
        revision: review.revision,
        sources: [...selected]
      });
      if (ticket !== generation) return;
      const states = {
        install: 'In this workspace, open Tools → Capabilities and review the Filing installation.',
        access: 'Review Filing access to this Documents folder.',
        scan: 'Run a manual scan of Documents in Filing, then return and check these files again.',
        changed:
          'A selected file changed or is no longer eligible. Revisit the view and Filing scan.'
      };
      message.replaceChildren(
        node(
          'p',
          selection.state === 'matched'
            ? `Matched: ${selection.names.join(', ')}. Filing into a chosen folder is not available. No files moved.`
            : states[selection.state] || 'Filing is unavailable for this view.'
        )
      );
      if (
        ['install', 'access', 'scan'].includes(selection.state) &&
        /^\/workspaces\/[a-z0-9][a-z0-9-]*$/.test(route)
      ) {
        const link = node(
          'a',
          selection.state === 'install' ? 'Open workspace in a new tab' : 'Open Filing in a new tab'
        );
        link.href = selection.state === 'install' ? route : `${route}?panel=file-janitor`;
        link.target = '_blank';
        link.rel = 'noopener';
        message.append(link, node('p', 'Your saved view stays open here.'));
      }
    } catch {
      if (ticket === generation)
        message.textContent = 'The workspace, view or Filing access changed. Reopen the review.';
    }
  });
  check.disabled = true;
  for (const ref of group.members) {
    const file = sources.get(ref)?.file;
    if (!file || file.source.example || file.status !== 'read') continue;
    const label = node('label', null, 'd-block');
    const input = node('input');
    input.type = 'checkbox';
    input.addEventListener('change', () => {
      generation++;
      if (input.checked) selected.add(ref);
      else selected.delete(ref);
      check.disabled = !selected.size || selected.size > 8;
      message.textContent = selected.size > 8 ? 'Choose at most eight files.' : '';
    });
    label.append(input, node('span', file.source.path));
    choices.append(label);
  }
  box.append(choices, check, message);
  return box;
}
