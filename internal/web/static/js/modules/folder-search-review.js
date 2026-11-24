import { savedGroupedCreationReference } from './grouped-creation-operation.js';
import {
  reviewCreationOperation,
  confirmCreationOperation,
  observeCreationOperation,
  releaseCreationReference,
  savedCreationReference
} from './workspace-creation-operation.js';

async function post(op, body) {
  const response = await fetch(`/api/file-search/${op}`, {
    method: 'POST',
    cache: 'no-store',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  if (!response.ok) {
    const message = (await response.text()).trim();
    throw new Error(
      message && message.length < 160 && !message.includes('<')
        ? message
        : 'The folder could not be verified. Choose it again.'
    );
  }
  return response.json();
}
export async function chooseSearchFolder(kind) {
  const { intent } = await post('begin', { kind });
  return post('review', { intent });
}

// The compact card has one creation shape. The same owner-bound review and
// delivery journal as advanced creation recheck all material at confirmation.
export function folderWorkspaceRequest(review, name, search, revision) {
  return {
    name: name.trim(),
    folder_selection: review.id,
    allow_filename_search: search,
    blank: true,
    create_template_agents: false,
    existing_agent_names: [],
    team_intent: { version: 1, mode: 'agentless', plan_revision: revision },
    role_staffing: []
  };
}

function notifyFiles() {
  if (typeof BroadcastChannel === 'function') {
    const channel = new BroadcastChannel('ori-file-search-v1');
    channel.postMessage('changed');
    channel.close();
  }
}

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text) node.textContent = text;
  if (className) node.className = className;
  return node;
}
function button(text, primary = false) {
  const b = element('button', text, primary ? 'explore-card__primary' : '');
  b.type = 'button';
  return b;
}

// The native modal owns focus on Files; the same form is a reply on Home.
// Only opaque operation IDs are retained by the existing recovery transport.
export function exploreFolder(
  review,
  { host = document.body, inline = false, signal, preferWorkspace = false } = {}
) {
  if (signal?.aborted) return Promise.resolve(null);
  return new Promise(resolve => {
    const card = element(inline ? 'section' : 'dialog', null, 'explore-card');
    const title = element(
      'h2',
      review.matches?.length
        ? `Linked workspaces for ${review.label}`
        : `Create a workspace from ${review.label}?`
    );
    title.id = `explore-${crypto.randomUUID()}`;
    card.setAttribute('aria-labelledby', title.id);
    const description = element('p', null, 'explore-card__description');
    const form = element('form');
    const fields = element('fieldset');
    const actions = element('div', null, 'explore-card__actions');
    const status = element('p', null, 'explore-card__status');
    status.setAttribute('role', 'status');
    const cancel = button('Cancel'),
      submit = button('', true);
    submit.type = 'submit';
    const close = (result = null) => {
      if (!alive) return;
      alive = false;
      signal?.removeEventListener('abort', abort);
      if (!inline && card.open) card.close();
      card.remove();
      resolve(result);
    };
    const abort = () => close();
    let alive = true,
      busy = false,
      reference = null,
      editing = true;
    const matches = review.matches || [],
      connected = matches.find(m => m.connected);
    const openOnly = matches.length && !connected ? button('Open workspace', true) : null;
    const name = element('input');
    name.required = true;
    name.maxLength = 120;
    name.value = review.label;
    const search = element('input');
    search.type = 'checkbox';
    search.checked = !!review.source;
    const select = element('select'),
      verify = element('input');
    verify.type = 'checkbox';
    const verifyLabel = element('label', null, 'explore-card__check');
    verifyLabel.append(
      verify,
      document.createTextNode(
        'I confirm this is the folder currently linked to this workspace. Its original disk identity was not recorded.'
      )
    );
    const chosen = () => connected || (select.value === '' ? null : matches[Number(select.value)]);
    function update() {
      const m = chosen();
      verifyLabel.hidden = !m?.verify;
      if (openOnly) {
        openOnly.disabled = busy || !m;
        openOnly.textContent = m ? `Open ${m.name}` : 'Open workspace';
      }
      submit.disabled =
        busy || (!matches.length ? !name.value.trim() : !m || (m.verify && !verify.checked));
    }
    function finish(workspace, source, destination) {
      close({ workspace, source, destination });
    }
    function receipt(workspace, source, warning) {
      editing = false;
      fields.hidden = true;
      status.textContent = warning || '';
      title.textContent = 'Workspace created';
      window.dispatchEvent(
        new CustomEvent('ori:workspaces-changed', {
          detail: { reason: 'create', workspaceId: workspace.id }
        })
      );
      description.textContent = `${review.label} is linked to ${workspace.name}.`;
      actions.replaceChildren();
      const open = button('Open workspace', preferWorkspace || !source);
      open.onclick = () => finish(workspace, source, 'workspace');
      if (source) {
        notifyFiles();
        const files = button('Search this folder in Files', !preferWorkspace);
        files.onclick = () => finish(workspace, source, 'files');
        actions.append(files);
      }
      actions.append(open);
      const staff = button('Add an agent…');
      staff.onclick = () => {
        close();
        location.assign(
          `/workspaces/${encodeURIComponent(workspace.slug)}?staffWorkspace=${encodeURIComponent(workspace.id)}`
        );
      };
      actions.append(staff);
      const done = button('Done');
      done.onclick = () => close();
      actions.append(done);
      actions.querySelector('.explore-card__primary')?.focus();
    }
    function outcome(view) {
      if (view.state === 'complete') {
        releaseCreationReference(localStorage, reference);
        const { folder, filename_source_id, search_warning } = view.result;
        receipt(
          { id: folder.id, name: folder.name, slug: folder.folder_slug },
          filename_source_id,
          search_warning
        );
        return;
      }
      if (view.state === 'refused' || view.state === 'abandoned') {
        releaseCreationReference(localStorage, reference);
        reference = null;
        throw new Error(
          view.message || 'Workspace was not created. Check the name or choose the folder again.'
        );
      }
      editing = false;
      fields.disabled = true;
      actions.replaceChildren();
      const read = button('Read saved status', true);
      read.onclick = async () => {
        read.disabled = true;
        try {
          const next = await observeCreationOperation(fetch, reference);
          if (alive) outcome(next);
        } catch (error) {
          status.textContent = error.message;
          actions.replaceChildren(cancel);
        } finally {
          read.disabled = false;
        }
      };
      actions.append(cancel, read);
      cancel.disabled = false;
      cancel.textContent = 'Close';
      status.textContent =
        'Creation is not yet verified. Read its saved status; do not create again.';
    }
    if (!matches.length) {
      description.textContent =
        'Create a workspace linked to this folder. Workspace tools can read its files. The originals stay where they are.';
      const label = element('label', 'Workspace name', 'explore-card__name');
      label.append(name);
      const consent = element('label', null, 'explore-card__check');
      consent.append(
        search,
        document.createTextNode(
          review.source
            ? 'Connect the existing filename index without rescanning.'
            : 'Make filenames searchable in Files on this Mac.'
        )
      );
      fields.append(
        label,
        consent,
        element(
          'small',
          review.source
            ? 'Leaving this off does not revoke existing search access.'
            : 'Only names, locations and dates are indexed, not file contents.'
        )
      );
      name.oninput = update;
      submit.textContent = 'Create workspace';
    } else if (connected) {
      description.textContent = `This folder is already linked to ${connected.name}.`;
      submit.textContent = `Open ${connected.name}`;
    } else {
      description.textContent =
        matches.length === 1
          ? `This folder is already linked to ${matches[0].name}.`
          : 'More than one workspace links to this folder. Choose which one to open or connect.';
      if (matches.length > 1) select.add(new Option('Choose a workspace…', ''));
      matches.forEach((m, i) =>
        select.add(new Option(`${m.name} · ${m.slug} · ${m.id.slice(0, 8)}`, String(i)))
      );
      if (matches.length > 1) {
        const label = element('label', 'Workspace', 'explore-card__name');
        label.append(select);
        fields.append(label);
      }
      fields.append(
        verifyLabel,
        element(
          'small',
          review.source
            ? 'Keep the existing index; nothing is rescanned.'
            : 'Allow local filename indexing. File contents are not indexed.'
        )
      );
      select.onchange = () => {
        verify.checked = false;
        update();
      };
      verify.onchange = update;
      submit.textContent = review.source ? 'Connect to workspace' : 'Allow filename search';
    }
    actions.append(cancel, submit);
    if (openOnly) {
      submit.className = '';
      submit.type = 'button';
      submit.onclick = () => perform(() => connect(preferWorkspace ? 'workspace' : 'files'));
      openOnly.type = 'submit';
      actions.insertBefore(openOnly, submit);
    }
    async function openVerified() {
      const selected = chosen();
      const current = await post('resume-review', { intent: review.id });
      const workspace = current.matches?.find(
        m => m.id === selected.id && m.link === selected.link
      );
      if (!workspace) throw new Error('This folder link changed. Choose the folder again.');
      if (alive) finish(workspace, null, 'workspace');
    }
    async function connect(destination) {
      const m = chosen();
      const result = await post('connect', {
        intent: review.id,
        workspace: m.id,
        link: m.link,
        verify_current_link: verify.checked
      });
      if (!m.connected) notifyFiles();
      if (alive) finish(result.workspace, result.source, destination);
    }
    if (connected && !preferWorkspace) {
      const files = button('Search this folder in Files');
      files.onclick = () => perform(() => connect('files'));
      actions.append(files);
    }
    async function perform(action) {
      if (busy) return;
      busy = true;
      fields.disabled = true;
      for (const b of actions.children) b.disabled = true;
      status.textContent = '';
      try {
        await action();
      } catch (err) {
        if (alive) status.textContent = err.message || 'Folder setup could not be completed.';
      } finally {
        busy = false;
        if (alive && editing) {
          fields.disabled = false;
          for (const b of actions.children) b.disabled = false;
          update();
        }
      }
    }
    form.onsubmit = event => {
      event.preventDefault();
      if (!editing || (openOnly || submit).disabled) return;
      void perform(async () => {
        if (matches.length) {
          return openVerified();
        }
        if (savedCreationReference(localStorage) || savedGroupedCreationReference(localStorage))
          throw new Error(
            'Open New Workspace on Map to check the previous creation receipt first.'
          );
        const planResponse = await fetch('/api/workspaces/template-agent-plan', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ blank: true })
        });
        const plan = planResponse.ok ? await planResponse.json() : null;
        if (!alive) return;
        if (!plan?.revision)
          throw new Error('Could not verify the empty workspace plan. Nothing was created.');
        reference = await reviewCreationOperation(
          fetch,
          crypto.randomUUID(),
          folderWorkspaceRequest(review, name.value, search.checked, plan.revision)
        );
        if (!alive) return;
        const view = await confirmCreationOperation(fetch, localStorage, reference);
        if (alive) outcome(view);
      });
    };
    cancel.onclick = () => close();
    card.addEventListener('cancel', e => {
      e.preventDefault();
      if (!busy) close();
    });
    card.addEventListener('keydown', e => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        e.preventDefault();
        if (!busy) close();
      }
    });
    form.append(fields, status, actions);
    card.append(element('small', 'Local folder', 'explore-card__kicker'), title, description, form);
    host.append(card);
    signal?.addEventListener('abort', abort, { once: true });
    update();
    if (!inline) card.showModal();
    (matches.length > 1 && !connected
      ? select
      : matches.length
        ? openOnly || submit
        : name
    ).focus();
  });
}

export const reviewExisting = (review, host) =>
  exploreFolder(review, { host, preferWorkspace: true });
