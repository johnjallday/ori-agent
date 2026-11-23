// Files a saved view's loose documents into a folder the person chooses.
//
// Nothing here moves a file by itself. "Check" re-reads the Filing scan, "Preview"
// shows exactly where each file would go and writes nothing, and only the
// "File N documents" press, quoting the previewed plan's digest, can move files.
// It never uses Filing's category plan, saved ticks or confirm actions.

const MAX_FILES = 8;

const CHECK_STATES = {
  install: 'In this workspace, open Tools → Capabilities and review the Filing installation.',
  access: 'Review Filing access to this Documents folder.',
  scan: 'Run a manual scan of Documents in Filing, then return and check these files again.',
  changed: 'A selected file changed or is no longer eligible. Revisit the view and Filing scan.'
};

const PREVIEW_STATES = {
  ...CHECK_STATES,
  unsupported:
    'Ori cannot file into this folder safely on this computer, so it will not move anything here.',
  destination:
    'That folder is not available. Use a plain folder inside Documents, at most two levels deep.',
  unavailable: 'Filing is unavailable for this view.'
};

const RETRY_KEY = /^[A-Za-z0-9_-]{16,128}$/;

// newKey makes a retry key. A retry of the same press reuses it, so a lost
// response replays the first result instead of filing twice.
function newKey() {
  const fromCrypto = globalThis.crypto?.randomUUID?.();
  if (fromCrypto && RETRY_KEY.test(fromCrypto)) return fromCrypto;
  let out = '';
  while (out.length < 32) out += Math.random().toString(36).slice(2);
  return out.slice(0, 32);
}

// parseFolder reads the typed folder. It only shapes the text; the server decides
// whether the folder is allowed.
export function parseFolder(text) {
  const typed = String(text ?? '')
    .trim()
    .replace(/\/+$/, '');
  if (!typed) return { error: 'Type the folder to file into, for example Taxes/2024.' };
  const parts = typed.split('/');
  if (parts.length > 2) return { error: 'Use at most two levels, like Taxes/2024.' };
  if (parts.some(part => !part.trim() || part === '.' || part === '..'))
    return { error: 'Use plain folder names separated by a slash.' };
  return { folder: parts.join('/'), parent: parts.slice(0, -1).join('/'), last: parts.at(-1) };
}

export function documentsFilingCheck({
  node,
  button,
  group,
  review,
  sources,
  route,
  post,
  refresh
}) {
  const box = node('details');
  box.append(node('summary', 'Review filing'));
  const choices = node('fieldset');
  choices.append(node('legend', 'Choose loose documents (up to eight)'));
  const selected = new Set();
  const message = node('div');
  message.setAttribute('role', 'status');
  const flow = node('div');
  let generation = 0;
  const keys = new Map();

  const selectionBody = () => ({
    review: review.id,
    group: group.id,
    revision: review.revision,
    sources: [...selected]
  });
  const say = text => {
    message.textContent = text;
  };
  const reset = () => {
    generation++;
    flow.replaceChildren();
  };

  const openLink = state => {
    if (
      !['install', 'access', 'scan'].includes(state) ||
      !/^\/workspaces\/[a-z0-9][a-z0-9-]*$/.test(route)
    )
      return [];
    const link = node(
      'a',
      state === 'install' ? 'Open workspace in a new tab' : 'Open Filing in a new tab'
    );
    link.href = state === 'install' ? route : `${route}?panel=file-janitor`;
    link.target = '_blank';
    link.rel = 'noopener';
    return [link, node('p', 'Your saved view stays open here.')];
  };

  const check = button('Check selected files', async () => {
    const ticket = ++generation;
    flow.replaceChildren();
    say('Checking the current Filing scan…');
    try {
      const { selection } = await post('organization-filing-selection', selectionBody());
      if (ticket !== generation) return;
      if (selection.state !== 'matched') {
        message.replaceChildren(
          node('p', CHECK_STATES[selection.state] || 'Filing is unavailable for this view.'),
          ...openLink(selection.state)
        );
        return;
      }
      say(`Matched: ${selection.names.join(', ')}. Nothing has moved.`);
      showDestination();
    } catch {
      if (ticket === generation)
        say('The workspace, view or Filing access changed. Reopen the review.');
    }
  });
  check.disabled = true;

  // ---------------------------------------------------------------- destination
  function showDestination() {
    const label = node('label', 'Folder in Documents', 'd-block');
    const input = node('input');
    input.type = 'text';
    input.value = group.example_folder || '';
    input.maxLength = 120;
    input.autocomplete = 'off';
    input.spellcheck = false;
    input.setAttribute('aria-describedby', 'documents-filing-hint');
    label.append(input);
    const hint = node(
      'p',
      'A folder inside Documents, for example Taxes/2024 (at most two levels). If it is not there, Ori can make it.'
    );
    hint.id = 'documents-filing-hint';
    const go = button('Preview filing', () => {
      const parsed = parseFolder(input.value);
      if (parsed.error) return say(parsed.error);
      preview({ folder: parsed.folder }, parsed);
    });
    flow.replaceChildren(label, hint, go);
    input.focus?.();
  }

  async function preview(destination, parsed) {
    const ticket = ++generation;
    say('Working out where each file would go…');
    try {
      const { preview: plan } = await post('organization-filing-preview', {
        ...selectionBody(),
        destination
      });
      if (ticket !== generation) return;
      renderPreview(plan, destination, parsed);
    } catch {
      if (ticket === generation)
        say('The workspace, view or Filing access changed. Reopen the review.');
    }
  }

  function renderPreview(plan, destination, parsed) {
    const back = button('Choose another folder', () => {
      say('');
      showDestination();
    });
    if (plan.state !== 'ready') {
      const parts = [
        node('p', PREVIEW_STATES[plan.state] || 'Filing is unavailable for this view.')
      ];
      // A folder that is not there can be made, one level at a time.
      if (plan.state === 'destination' && !destination.new && parsed) {
        parts.push(
          button(`Preview making "${parsed.last}"`, () =>
            preview({ folder: parsed.parent, new: parsed.last }, parsed)
          )
        );
      }
      parts.push(back, ...openLink(plan.state));
      say('');
      flow.replaceChildren(...parts);
      return;
    }
    const heading = node('p', `Ori would file into ${plan.folder}:`);
    heading.tabIndex = -1;
    const list = node('ul');
    for (const item of plan.items)
      list.append(
        node(
          'li',
          `${item.name} → ${item.to}${item.renamed ? ' (name adjusted: another file already has it)' : ''}`
        )
      );
    const lines = [heading, list];
    if (plan.create?.length)
      lines.push(node('p', `Ori will make the folder ${plan.create.join(', ')} first.`));
    lines.push(
      node('p', 'No file is replaced. Nothing moves until you press the button below.'),
      button(
        `File ${plan.items.length} ${plan.items.length === 1 ? 'document' : 'documents'} into ${plan.folder}`,
        event => press(plan, destination, parsed, event?.currentTarget),
        true
      ),
      back
    );
    say('');
    flow.replaceChildren(...lines);
    heading.focus?.();
  }

  // ---------------------------------------------------------------------- press
  async function press(plan, destination, parsed, pressed) {
    const intent = `${plan.digest}|${destination.folder}|${destination.new || ''}`;
    if (!keys.has(intent)) keys.set(intent, newKey());
    const ticket = ++generation;
    if (pressed) pressed.disabled = true;
    say('Filing…');
    try {
      const { filing } = await post('organization-filing', {
        ...selectionBody(),
        destination,
        digest: plan.digest,
        client_key: keys.get(intent)
      });
      if (ticket !== generation) return;
      renderResult(filing);
    } catch (error) {
      if (ticket !== generation) return;
      if (pressed) pressed.disabled = false;
      const body = error?.body;
      if (body?.code === 'filing_changed') {
        // Render the fresh plan first: it clears the status line, and the reason the
        // plan is different must stay on screen next to it.
        renderPreview(body.preview || { state: 'changed' }, destination, parsed);
        say(
          'The files or the folder changed since you looked. Nothing was moved. Check the new plan, then press again.'
        );
      } else if (body?.code === 'filing_key_reused') {
        keys.delete(intent);
        say(
          'That press was already used for a different filing. Nothing was moved. Preview it again.'
        );
      } else if (body?.code === 'filing_unsupported') {
        say(PREVIEW_STATES.unsupported);
      } else {
        say(
          'The answer did not arrive. Pressing the button again will not file anything twice; it reports what happened.'
        );
      }
    }
  }

  function renderResult(filing) {
    const lines = [];
    const heading = node(
      'p',
      filing.filed ? `Filed ${filing.filed} into ${filing.folder}.` : 'Nothing was moved.'
    );
    heading.tabIndex = -1;
    lines.push(heading);
    if (filing.folder_created) lines.push(node('p', `Ori made the folder ${filing.folder}.`));
    if (filing.folder_unconfirmed)
      lines.push(
        node(
          'p',
          `Ori may have made the folder ${filing.folder} but could not confirm it. No file was moved.`
        )
      );
    const problems = (filing.outcomes || []).filter(item => item.result !== 'applied');
    if (problems.length) {
      const list = node('ul');
      for (const item of problems)
        list.append(node('li', `${item.name}: ${item.message || 'not moved'}`));
      lines.push(node('p', 'These were left where they were:'), list);
    }
    if (filing.interrupted)
      lines.push(
        node(
          'p',
          'Ori stopped before it finished. What is listed above as filed is filed; the rest were not moved.'
        )
      );
    if (filing.not_attempted)
      lines.push(
        node('p', `${filing.not_attempted} not attempted. They are still where they were.`)
      );
    if (filing.decision_recorded === false)
      lines.push(
        node('p', 'The files were filed, but Ori could not add a note to this saved view.')
      );
    lines.push(
      node('p', 'Nothing was replaced or deleted. Undo is not available for this filing yet.'),
      button('Reload this view', () => refresh?.())
    );
    for (const input of choices.querySelectorAll?.('input') ?? []) input.disabled = true;
    check.disabled = true;
    say('');
    flow.replaceChildren(...lines);
    heading.focus?.();
  }

  // ------------------------------------------------------------------ the files
  for (const ref of group.members) {
    const file = sources.get(ref)?.file;
    if (!file || file.source.example || file.status !== 'read') continue;
    const label = node('label', null, 'd-block');
    const input = node('input');
    input.type = 'checkbox';
    input.addEventListener('change', () => {
      reset();
      if (input.checked) selected.add(ref);
      else selected.delete(ref);
      check.disabled = !selected.size || selected.size > MAX_FILES;
      say(selected.size > MAX_FILES ? 'Choose at most eight files.' : '');
    });
    label.append(input, node('span', file.source.path));
    choices.append(label);
  }
  box.append(choices, check, message, flow);
  return box;
}
