// blueprint-intake-step.js — source collection and consent for `intake` steps.
(function () {
  'use strict';

  const states = new Map();

  function stateKey(ctx) {
    return `${ctx.workspaceId}:${ctx.step?.intake_key || ''}`;
  }

  function intakeURL(ctx, suffix = '') {
    return `/api/workspaces/${encodeURIComponent(ctx.workspaceId)}/blueprint-intakes/${encodeURIComponent(
      ctx.step.intake_key
    )}${suffix}`;
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function getState(ctx) {
    const key = stateKey(ctx);
    let state = states.get(key);
    if (!state) {
      state = { loaded: false, loading: false, uploading: false, files: [], consent: null };
      states.set(key, state);
    }
    return state;
  }

  async function request(ctx, suffix = '', options) {
    const response = await fetch(intakeURL(ctx, suffix), options || {});
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(payload?.message || payload?.error || 'Blueprint intake request failed');
    }
    return payload;
  }

  async function load(container, ctx, state) {
    if (state.loading) return;
    state.loading = true;
    ctx.setBusy(true, 'Loading intake…');
    try {
      const payload = await request(ctx);
      state.files = Array.isArray(payload.files) ? payload.files : [];
      state.consent = payload.consent || null;
      state.loaded = true;
      draw(container, ctx, state);
    } catch (error) {
      ctx.setError(error.message || 'This intake could not be loaded.');
    } finally {
      state.loading = false;
      ctx.setBusy(false);
    }
  }

  function draw(container, ctx, state) {
    container.textContent = '';
    if (!state.loaded) {
      container.appendChild(el('p', 'blueprint-intake-loading', 'Loading intake…'));
      return;
    }

    if (ctx.step.intake_files) {
      const picker = document.createElement('input');
      picker.type = 'file';
      picker.multiple = true;
      picker.className = 'blueprint-intake-file-input';
      picker.id = `blueprintIntakeFiles-${ctx.step.id}`;
      picker.disabled = state.uploading;
      if (Array.isArray(ctx.step.intake_accepted_extensions)) {
        picker.accept = ctx.step.intake_accepted_extensions.join(',');
      }

      const label = el('label', 'blueprint-intake-file-label', 'Add files');
      label.setAttribute('for', picker.id);
      const hint = el(
        'p',
        'blueprint-intake-hint',
        ctx.step.intake_accepted_extensions?.length
          ? `Accepted: ${ctx.step.intake_accepted_extensions.join(', ')}`
          : 'PDF, Office documents, text, Markdown, HTML, JSON, XML, and CSV are supported.'
      );
      picker.addEventListener('change', () => uploadFiles(container, ctx, state, picker));
      container.append(label, picker, hint);
    }

    const list = el('ul', 'blueprint-intake-source-list');
    if (!state.files.length) {
      list.appendChild(el('li', 'blueprint-intake-empty', 'No files added yet.'));
    } else {
      state.files.forEach(file => {
        const item = el('li', `blueprint-intake-source blueprint-intake-source-${file.status}`);
        item.appendChild(el('span', 'blueprint-intake-source-name', file.name || 'Unnamed file'));
        item.appendChild(el('span', 'blueprint-intake-source-status', statusLabel(file.status)));
        if (file.message)
          item.appendChild(el('span', 'blueprint-intake-source-message', file.message));
        list.appendChild(item);
      });
    }
    container.appendChild(list);

    const consent = el('section', 'blueprint-intake-consent');
    consent.appendChild(el('h4', 'blueprint-intake-consent-title', 'Before Ori reads the text'));
    consent.appendChild(
      el(
        'p',
        'blueprint-intake-consent-statement',
        state.consent?.statement || 'The content-reading statement is unavailable.'
      )
    );
    const consentLabel = el('label', 'blueprint-intake-consent-check');
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.checked = Boolean(state.consent?.accepted);
    checkbox.disabled = Boolean(state.consent?.accepted) || state.uploading;
    checkbox.addEventListener('change', () => acceptConsent(container, ctx, state, checkbox));
    consentLabel.append(checkbox, document.createTextNode(' I understand and agree.'));
    consent.appendChild(consentLabel);
    container.appendChild(consent);
  }

  async function uploadFiles(container, ctx, state, input) {
    const selected = Array.from(input.files || []);
    if (!selected.length) return;
    const form = new FormData();
    selected.forEach(file => form.append('files', file));
    state.uploading = true;
    ctx.setError('');
    ctx.setBusy(true, `Adding ${selected.length} file${selected.length === 1 ? '' : 's'}…`);
    draw(container, ctx, state);
    try {
      const payload = await request(ctx, '/sources/files', { method: 'POST', body: form });
      state.files = state.files.concat(Array.isArray(payload.files) ? payload.files : []);
      ctx.announce('Files added. Review their status and the content-reading statement.');
    } catch (error) {
      ctx.setError(error.message || 'The files could not be added.');
    } finally {
      state.uploading = false;
      input.value = '';
      draw(container, ctx, state);
      ctx.setBusy(false);
    }
  }

  async function acceptConsent(container, ctx, state, checkbox) {
    if (!checkbox.checked || state.consent?.accepted) return;
    ctx.setError('');
    ctx.setBusy(true, 'Recording your consent…');
    try {
      const payload = await request(ctx, '/consent', { method: 'POST' });
      state.consent = payload.consent || state.consent;
      ctx.announce('Content reading accepted.');
    } catch (error) {
      checkbox.checked = false;
      ctx.setError(error.message || 'Consent could not be recorded.');
    } finally {
      draw(container, ctx, state);
      ctx.setBusy(false);
    }
  }

  function statusLabel(status) {
    switch (status) {
      case 'parsed':
        return 'Parsed';
      case 'skipped':
        return 'Skipped';
      case 'too_large':
        return 'Too large';
      case 'unreadable':
        return 'Unreadable';
      default:
        return 'Waiting';
    }
  }

  const renderer = {
    render(container, ctx) {
      const state = getState(ctx);
      draw(container, ctx, state);
      if (!state.loaded) void load(container, ctx, state);
    },

    primaryLabel() {
      return 'Continue';
    },

    disablePrimary(ctx) {
      if (ctx.step?.status === 'complete') return false;
      const state = getState(ctx);
      return (
        !state.loaded ||
        state.loading ||
        state.uploading ||
        !state.files.length ||
        !state.consent?.accepted
      );
    },

    async onPrimary(ctx) {
      const state = getState(ctx);
      if (!state.files.length) {
        ctx.setError('Add at least one file before continuing.');
        return;
      }
      if (!state.consent?.accepted) {
        ctx.setError('Accept the content-reading statement before continuing.');
        return;
      }
      await ctx.confirm();
    }
  };

  function register() {
    const wizard = window.SetupWizard;
    if (!wizard || typeof wizard.registerStepRenderer !== 'function') return false;
    wizard.registerStepRenderer('intake', renderer);
    return true;
  }

  if (!register() && typeof document !== 'undefined') {
    document.addEventListener('DOMContentLoaded', register);
  }

  window.BlueprintIntakeStep = renderer;
})();
