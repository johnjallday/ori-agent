// An assistant-only adapter. The shared requirements renderer remains inert;
// all package effects use the existing server-owned review/consent boundaries.
function failureMessage(result, fallback) {
  const data = result?.data;
  return typeof data?.error === 'string'
    ? data.error
    : typeof data?.message === 'string'
      ? data.message
      : fallback;
}

export function createFolderPrerequisites({
  key,
  request,
  changed,
  refreshed,
  id = () => crypto.randomUUID()
}) {
  let owner = '',
    epoch = 0;
  let value = { kind: '', busy: false, review: null, pending: null, error: '' };
  const reset = () => {
    epoch++;
    value = { kind: '', busy: false, review: null, pending: null, error: '' };
  };
  return {
    sync(eligible) {
      const next = eligible ? key() : '';
      if (next !== owner) {
        owner = next;
        reset();
      }
    },
    view: () => value,
    active: () => Boolean(owner && (value.busy || value.review || value.pending)),
    // The operation the server actually reviewed, which outranks the card's row.
    operation: () => value.review?.operation || value.review?.action || '',
    // A review still being read can be abandoned; its late reply is ignored by
    // the epoch check. A sent confirmation cannot, so its envelope survives.
    cancel() {
      if (value.pending) return;
      reset();
      changed();
    },
    async review(kind) {
      if (!owner || value.busy || value.pending || !['integration', 'provider'].includes(kind))
        return;
      const visit = epoch,
        current = owner;
      value = { kind, busy: true, review: null, pending: null, error: '' };
      changed();
      try {
        const result = await request({ kind, request_id: id() });
        if (visit !== epoch || current !== key()) return;
        const disclosure = result?.data?.prerequisite;
        const envelope =
          kind === 'integration'
            ? disclosure?.run_id &&
              disclosure?.review_token &&
              Number.isSafeInteger(disclosure?.revision) &&
              disclosure.revision > 0 &&
              ['install', 'enable', 'update'].includes(disclosure.action)
            : /^[a-f0-9]{64}$/.test(disclosure?.digest || '') && disclosure?.version;
        if (!result?.ok || disclosure?.kind !== kind || disclosure.completed || !envelope)
          throw new Error(
            failureMessage(result, 'The plugin review is unavailable. Nothing was installed.')
          );
        value.review = result.data.prerequisite;
      } catch (error) {
        if (visit === epoch && current === key()) value.error = error.message;
      } finally {
        if (visit === epoch && current === key()) {
          value.busy = false;
          changed();
        }
      }
    },
    async confirm() {
      if (!owner || value.busy || (!value.review && !value.pending)) return { completed: false };
      const visit = epoch,
        current = owner;
      const review = value.review;
      value.pending ||= {
        kind: value.kind,
        confirm: true,
        request_id: id(),
        ...(review.run_id
          ? {
              run_id: review.run_id,
              revision: review.revision,
              review_token: review.review_token,
              action: review.action
            }
          : { digest: review.digest, reviewed_version: review.version })
      };
      value.busy = true;
      value.error = '';
      changed();
      try {
        const result = await request(value.pending);
        if (visit !== epoch || current !== key()) return;
        if (!result?.ok || !result.data?.prerequisite?.completed) {
          // A reached server refused this consent; a new disclosure is needed.
          // An unknown transport result keeps the exact idempotency envelope.
          if (result?.status) {
            value.pending = null;
            value.review = null;
            // A refused or partly completed change may still have installed
            // bytes. Re-read facts, never manufacture another confirmation.
            await refreshed(
              failureMessage(result, 'Check the current plugin state before reviewing again.')
            );
          }
          throw new Error(
            failureMessage(
              result,
              'The plugin change was not confirmed. Check its current state before trying again.'
            )
          );
        }
        const applied = {
          completed: true,
          action: review?.operation || review?.action || '',
          enabledAfter: Boolean(review?.enabled_after)
        };
        reset();
        changed();
        // Awaited so a caller reads the refreshed requirements, not the old ones.
        await refreshed();
        return applied;
      } catch (error) {
        if (visit === epoch && current === key()) value.error = error.message;
      } finally {
        if (visit === epoch && current === key()) {
          value.busy = false;
          changed();
        }
      }
      return { completed: false };
    }
  };
}

export function renderFolderPrerequisites(
  container,
  offer,
  controller,
  { blocked = false, openWizard } = {}
) {
  const eligible = Boolean(
    offer?.prerequisites_available &&
    offer.status === 'pending' &&
    !offer.setup &&
    !offer.needs_pick &&
    offer.destination_status !== 'changed'
  );
  controller.sync(eligible);
  const state = controller.view();
  const doc = container?.ownerDocument;
  if (!doc) return;
  for (const row of container.querySelectorAll('[data-requirement-kind]')) {
    const mount = row.querySelector('.folder-requirements__actions');
    if (!mount) continue;
    const operation = row.dataset.operation;
    const name = row.querySelector('strong')?.textContent || 'plugin';
    const selected = state.kind === row.dataset.requirementKind;
    row.classList.toggle(
      'folder-requirements__row--actionable',
      eligible && ['install', 'update', 'enable'].includes(operation)
    );
    const signature = JSON.stringify({
      eligible,
      operation,
      selected,
      state: selected
        ? {
            busy: state.busy,
            pending: Boolean(state.pending),
            error: state.error
          }
        : { busy: state.busy },
      blocked
    });
    if (mount.dataset.signature === signature) continue;
    mount.dataset.signature = signature;
    mount.replaceChildren();
    if (!eligible || !['install', 'update', 'enable'].includes(operation)) continue;
    const verb = { install: 'Install', update: 'Update', enable: 'Enable' }[operation];
    const kind = row.dataset.requirementKind;
    // One launch control. The review, its consent and its progress live in the
    // wizard. An unknown confirmation result keeps its own wording, so the user
    // is sent back to the same installation rather than to a new one.
    const launch = doc.createElement('button');
    launch.type = 'button';
    launch.className = 'personal-assistant-panel__chip';
    launch.textContent =
      selected && state.pending ? 'Check the same installation' : `${verb} ${name}`;
    launch.disabled = blocked || state.busy || !openWizard;
    launch.addEventListener('click', () => openWizard?.(kind, launch));
    mount.append(launch);
    if (selected && state.error) {
      const error = doc.createElement('p');
      error.textContent = state.error;
      error.className = 'folder-requirements__error';
      error.setAttribute('role', 'alert');
      mount.append(error);
    }
    mount.setAttribute('aria-busy', selected && state.busy ? 'true' : 'false');
  }
}
