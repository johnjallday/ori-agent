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
      state = { loaded: false, loading: false, uploading: false, sources: [], consent: null };
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
      state.sources = Array.isArray(payload.sources)
        ? payload.sources
        : Array.isArray(payload.files)
          ? payload.files
          : [];
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

    const actions = el('div', 'blueprint-intake-source-actions');
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
      actions.append(label, picker, hint);
    }
    if (ctx.step.intake_url) {
      const linkInput = document.createElement('input');
      linkInput.type = 'url';
      linkInput.placeholder = 'https://example.com/page';
      linkInput.className = 'blueprint-intake-link-input';
      linkInput.disabled = state.uploading || !state.consent?.accepted;
      const addLink = el('button', 'modern-btn modern-btn-secondary', 'Add a link');
      addLink.type = 'button';
      addLink.disabled = linkInput.disabled;
      addLink.addEventListener('click', () => addLinkSource(container, ctx, state, linkInput));
      actions.append(linkInput, addLink);
    }
    if (ctx.step.intake_directory_key) {
      const chooseFolder = el('button', 'modern-btn modern-btn-secondary', 'Choose a folder');
      chooseFolder.type = 'button';
      chooseFolder.disabled = state.uploading || !state.consent?.accepted;
      chooseFolder.addEventListener('click', () => addFolderSource(container, ctx, state));
      actions.appendChild(chooseFolder);
    }
    if (actions.children.length) container.appendChild(actions);

    const list = el('ul', 'blueprint-intake-source-list');
    if (!state.sources.length) {
      list.appendChild(el('li', 'blueprint-intake-empty', 'No sources added yet.'));
    } else {
      state.sources.forEach(source => {
        const item = el('li', `blueprint-intake-source blueprint-intake-source-${source.status}`);
        const kind = source.kind === 'link' ? 'Page' : source.kind === 'folder' ? 'Folder' : 'File';
        item.appendChild(
          el(
            'span',
            'blueprint-intake-source-name',
            `${kind}: ${source.title || source.name || 'Unnamed source'}`
          )
        );
        item.appendChild(el('span', 'blueprint-intake-source-status', statusLabel(source.status)));
        if (source.message)
          item.appendChild(el('span', 'blueprint-intake-source-message', source.message));
        if (source.omitted && !source.message)
          item.appendChild(
            el(
              'span',
              'blueprint-intake-source-message',
              `${source.omitted} immediate file(s) were left out.`
            )
          );
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
      state.sources = state.sources.concat(Array.isArray(payload.files) ? payload.files : []);
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

  async function addLinkSource(container, ctx, state, input) {
    const url = input.value.trim();
    if (!url) return;
    state.uploading = true;
    ctx.setBusy(true, 'Fetching page…');
    try {
      const payload = await request(ctx, '/sources/links', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url })
      });
      if (payload.source) state.sources.push(payload.source);
      input.value = '';
      ctx.announce('Page added. Review its status before continuing.');
    } catch (error) {
      ctx.setError(error.message || 'The page could not be added.');
    } finally {
      state.uploading = false;
      draw(container, ctx, state);
      ctx.setBusy(false);
    }
  }

  async function addFolderSource(container, ctx, state) {
    state.uploading = true;
    ctx.setBusy(true, 'Choosing folder…');
    try {
      const pickerResponse = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: ctx.workspaceId,
          title: 'Choose the folder whose immediate files Ori may read'
        })
      });
      const picked = await pickerResponse.json().catch(() => ({}));
      if (!pickerResponse.ok) throw new Error(picked.error || 'The folder picker failed.');
      if (!picked.selected) return;
      if (!picked.selection_token)
        throw new Error('Choose the folder again; its selection expired.');
      await request(ctx, '/sources/folder', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ selection_token: picked.selection_token })
      });
      const refreshed = await request(ctx);
      state.sources = Array.isArray(refreshed.sources) ? refreshed.sources : [];
      ctx.announce('Folder added. Review each immediate file status before continuing.');
    } catch (error) {
      ctx.setError(error.message || 'The folder could not be added.');
    } finally {
      state.uploading = false;
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
      case 'selected':
        return 'Selected';
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
        !state.sources.length ||
        !state.consent?.accepted
      );
    },

    async onPrimary(ctx) {
      const state = getState(ctx);
      if (!state.sources.length) {
        ctx.setError('Add at least one source before continuing.');
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
