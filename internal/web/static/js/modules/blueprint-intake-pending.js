// Pending reviewed re-intake proposals in the workspace Command view.
(function () {
  'use strict';

  function counts(items) {
    return (items || []).reduce(
      (result, item) => {
        if (item.classification === 'new') result.newItems += 1;
        if (item.classification === 'changed') result.changed += 1;
        return result;
      },
      { newItems: 0, changed: 0 }
    );
  }

  function summary(value) {
    const parts = [];
    if (value.newItems) parts.push(`${value.newItems} new item${value.newItems === 1 ? '' : 's'}`);
    if (value.changed) parts.push(`${value.changed} changed`);
    return parts.join(', ');
  }

  function createModal(proposal, workspaceId, refresh) {
    const overlay = document.createElement('div');
    overlay.className = 'blueprint-intake-pending-modal';
    const dialog = document.createElement('div');
    dialog.className = 'blueprint-intake-pending-dialog';
    dialog.setAttribute('role', 'dialog');
    dialog.setAttribute('aria-modal', 'true');
    const heading = document.createElement('h2');
    heading.textContent = 'Review course material changes';
    const error = document.createElement('p');
    error.className = 'blueprint-intake-pending-error';
    const content = document.createElement('div');
    const actions = document.createElement('div');
    actions.className = 'blueprint-intake-pending-actions';
    const close = document.createElement('button');
    close.type = 'button';
    close.textContent = 'Close';
    const primary = document.createElement('button');
    primary.type = 'button';
    primary.className = 'btn btn-primary';
    actions.append(close, primary);
    dialog.append(heading, error, content, actions);
    overlay.appendChild(dialog);
    document.body.appendChild(overlay);

    const ctx = {
      workspaceId,
      step: { intake_key: proposal.intake_key },
      setBusy(busy) {
        primary.disabled = Boolean(busy);
      },
      setError(message) {
        error.textContent = message || '';
      },
      announce() {},
      async confirm() {
        overlay.remove();
        await refresh();
      }
    };
    const renderer = window.BlueprintIntakeReview;
    renderer.reset(ctx);
    renderer.render(content, ctx);
    const updatePrimary = () => {
      primary.textContent = renderer.primaryLabel(ctx);
      primary.disabled = renderer.disablePrimary(ctx);
    };
    updatePrimary();
    const observer = new MutationObserver(updatePrimary);
    observer.observe(content, { childList: true, subtree: true, attributes: true });
    close.addEventListener('click', () => {
      observer.disconnect();
      overlay.remove();
    });
    primary.addEventListener('click', async () => {
      await renderer.onPrimary(ctx);
      updatePrimary();
    });
    close.focus();
  }

  let commandObserver;

  async function load() {
    const mount = document.getElementById('blueprintIntakePending');
    const command = document.getElementById('workspaceCommandView');
    const workspaceId = document.body?.dataset?.workspaceId || window.currentWorkspaceId;
    if (!mount || !command || !workspaceId || !window.BlueprintIntakeReview) return;
    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(workspaceId)}/blueprint-intakes/pending`
      );
      if (!response.ok) return;
      const payload = await response.json();
      const proposal = (payload.proposals || []).find(item =>
        (item.items || []).some(proposed => proposed.classification)
      );
      const value = counts(proposal?.items);
      const text = summary(value);
      mount.textContent = '';
      mount.hidden = !proposal || !text || command.hidden;
      if (!proposal || !text) return;
      const label = document.createElement('span');
      label.textContent = `${text} from updated course materials.`;
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = 'Review changes';
      button.addEventListener('click', () => createModal(proposal, workspaceId, load));
      mount.append(label, button);
      if (commandObserver) commandObserver.disconnect();
      commandObserver = new MutationObserver(() => {
        mount.hidden = command.hidden;
      });
      commandObserver.observe(command, { attributes: true, attributeFilter: ['hidden'] });
    } catch (_) {
      // The Command view remains usable when this optional status fetch fails.
    }
  }

  function start() {
    void load();
    window.setInterval(load, 15000);
  }

  window.BlueprintIntakePending = { counts, summary, load };
  document.addEventListener('DOMContentLoaded', start);
})();
