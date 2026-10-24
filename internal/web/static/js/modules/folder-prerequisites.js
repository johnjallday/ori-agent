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
    cancel() {
      if (value.busy || value.pending) return;
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
      if (!owner || value.busy || (!value.review && !value.pending)) return;
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
        reset();
        changed();
        await refreshed();
      } catch (error) {
        if (visit === epoch && current === key()) value.error = error.message;
      } finally {
        if (visit === epoch && current === key()) {
          value.busy = false;
          changed();
        }
      }
    }
  };
}

export function renderFolderPrerequisites(
  container,
  offer,
  controller,
  { blocked = false, trust } = {}
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
            review: state.review
              ? { ...state.review, review_token: undefined, run_id: undefined }
              : null,
            pending: Boolean(state.pending),
            error: state.error
          }
        : { busy: state.busy },
      blocked
    });
    if (mount.dataset.signature === signature) continue;
    mount.dataset.signature = signature;
    mount.replaceChildren();
    mount.onkeydown = null;
    if (!eligible || !['install', 'update', 'enable'].includes(operation)) continue;
    const make = (tag, text, className = '') => {
      const node = doc.createElement(tag);
      node.textContent = text;
      node.className = className;
      return node;
    };
    const button = (label, action, disabled = false) => {
      const node = make('button', label, 'personal-assistant-panel__chip');
      node.type = 'button';
      node.disabled = disabled;
      node.addEventListener('click', action);
      mount.append(node);
      return node;
    };
    const verb = { install: 'Install', update: 'Update', enable: 'Enable' }[operation];
    if (!selected || (!state.review && !state.pending && !state.busy))
      button(
        `${verb} ${name}`,
        () => void controller.review(row.dataset.requirementKind),
        blocked || state.busy
      );
    if (selected && state.review) {
      const review = state.review;
      const panel = make('section', '', 'folder-requirements__review');
      panel.setAttribute('aria-label', `Review ${name}`);
      panel.append(
        make('h5', `${verb} ${name}`),
        make(
          'p',
          `Release ${review.version || 'not reported'} · ${review.source || 'Source not reported'}`
        )
      );
      panel.append(
        make(
          'p',
          `Enabled after this action: ${review.enabled_after ? 'Yes' : 'No — enable separately afterward'}`
        )
      );
      if (review.disclosure && trust) panel.append(trust(review.disclosure));
      else
        panel.append(
          make(
            'p',
            operation === 'enable'
              ? 'Enables the installed, reviewed package. No download.'
              : 'Disclosure unavailable. Do not confirm an undisclosed installation.'
          )
        );
      if (review.details) {
        const details = make('details', '');
        details.append(
          make('summary', 'Full reviewed package details'),
          make('pre', JSON.stringify(review.details, null, 2))
        );
        panel.append(details);
      }
      panel.append(
        make(
          'p',
          'This changes only this Ori plugin. No workspace, Home, agent or model task is created or started.'
        )
      );
      mount.append(panel);
      button(
        state.busy
          ? 'Applying reviewed change…'
          : `Confirm ${operation === 'enable' ? 'enablement' : operation === 'update' ? 'update' : 'installation'}`,
        () => void controller.confirm(),
        blocked || state.busy || ((!review.disclosure || !trust) && operation !== 'enable')
      );
      const cancel = () => {
        controller.cancel();
        row.querySelector('.folder-requirements__actions button')?.focus();
      };
      button('Cancel', cancel, state.busy || Boolean(state.pending));
      mount.onkeydown = event => {
        if (event.key !== 'Escape' || state.busy || state.pending) return;
        event.preventDefault();
        event.stopPropagation();
        cancel();
      };
      const heading = panel.querySelector('h5');
      heading.tabIndex = -1;
      if (!state.busy && !state.pending && doc.activeElement === doc.body) {
        heading.focus({ preventScroll: true });
        globalThis.PersonalAssistantTranscript?.revealControl?.(heading);
      }
    } else if (selected && state.busy) {
      const status = make('p', 'Reading the reviewed package…');
      status.setAttribute('role', 'status');
      mount.append(status);
    }
    if (selected && state.error) {
      const error = make('p', state.error, 'folder-requirements__error');
      error.setAttribute('role', 'alert');
      mount.append(error);
    }
    if (selected && state.pending && !state.busy)
      button('Check the same installation', () => void controller.confirm(), blocked);
    mount.setAttribute('aria-busy', selected && state.busy ? 'true' : 'false');
  }
}
