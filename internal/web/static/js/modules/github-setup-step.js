/**
 * GitHub Ops — the wizard's Connect step.
 *
 * Without this, the connect step is a dead end. The generic wizard shows
 * "Approve and continue" for every step, but confirming this one cannot create
 * a connection: the adapter only reports whether one exists. A user with no
 * connection yet clicked a button that correctly did nothing, which reads as
 * broken rather than as "go do this elsewhere first".
 *
 * So the step accepts the token itself, which is what PRD FR-9 asks for: the
 * first workspace that needs GitHub doubles as the one-time global connect
 * flow. Every later workspace finds the connection already there and this
 * renderer stays out of the way.
 *
 * Registered on the shared `account_link` kind, so it checks `step.adapter`
 * before drawing anything — Email Ops uses the same kind and must keep its own
 * behavior.
 */
(function () {
  'use strict';

  const ADAPTER_ID = 'github_ops';
  const TOKEN_URL = 'https://github.com/settings/personal-access-tokens/new';
  // The list of existing tokens, where repository access is edited. This is
  // where a user lands when a repo they picked turns out not to be writable,
  // so it is offered on the repository step rather than buried in an error.
  const TOKEN_SETTINGS_URL = 'https://github.com/settings/personal-access-tokens';
  // Below this many repositories the filter is noise; above it, scanning the
  // list is the slow part.
  const FILTER_THRESHOLD = 6;

  function ownsStep(step) {
    return String(step?.adapter || '') === ADAPTER_ID;
  }

  // A step that is already satisfied needs nothing from this renderer; the
  // generic "Continue" is correct there.
  function needsConnection(step) {
    return ownsStep(step) && step?.status !== 'complete';
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function tokenField() {
    return document.getElementById('githubSetupToken');
  }

  const connectStepRenderer = {
    // Claims only GitHub Ops' account_link steps. Email Ops registers a
    // renderer on the same kind, and without this the last module to load
    // silently replaced the other -- which is exactly how this step's primary
    // button ended up doing nothing.
    owns: ownsStep,

    render(container, ctx) {
      if (!needsConnection(ctx.step)) {
        // Not ours, or already connected — let the wizard draw it.
        ctx.renderDefault(container);
        return;
      }

      // Keep the blueprint's own disclosure, then add the way to act on it.
      ctx.renderDefault(container);

      const box = el('div', 'setup-wizard-panel github-setup-connect');

      const steps = el('ol', 'github-setup-steps');
      const link = el('a', null, "GitHub's token page ↗");
      link.href = TOKEN_URL;
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      const first = el('li');
      first.append(document.createTextNode('Open '), link, document.createTextNode('.'));
      steps.appendChild(first);
      [
        'Under Repository access, choose "Only select repositories" and pick the repo you want triaged.',
        'Under Permissions → Repository permissions, set Issues to "Read and write" and Metadata to "Read-only". Leave everything else at No access.',
        'Generate the token and paste it below.'
      ].forEach(text => steps.appendChild(el('li', null, text)));
      box.appendChild(steps);

      const label = el('label', 'github-setup-label', 'Personal access token');
      label.setAttribute('for', 'githubSetupToken');
      const input = document.createElement('input');
      input.id = 'githubSetupToken';
      input.type = 'password';
      input.className = 'modern-input github-setup-input';
      input.autocomplete = 'off';
      input.spellcheck = false;
      input.placeholder = 'github_pat_…';
      label.appendChild(input);
      box.appendChild(label);

      box.appendChild(
        el(
          'p',
          'github-setup-note',
          'Stored in your vault on this device and never shown again. Connecting only lets Ori read issues and draft changes — nothing is written to GitHub without your approval.'
        )
      );

      container.appendChild(box);
    },

    primaryLabel(ctx) {
      // '' hands the label back to the wizard, which is what every step this
      // renderer does not own needs.
      if (!needsConnection(ctx.step)) return '';
      return 'Connect GitHub';
    },

    async onPrimary(ctx) {
      if (!needsConnection(ctx.step)) {
        // Not ours: fall back to the ordinary confirm.
        await ctx.confirm();
        return;
      }

      const input = tokenField();
      const token = (input?.value || '').trim();
      if (!token) {
        ctx.setError('Paste a personal access token to connect.');
        input?.focus();
        return;
      }

      ctx.setError('');
      ctx.setBusy(true, 'Connecting to GitHub…');
      let payload = {};
      let ok = false;
      try {
        const response = await fetch('/api/connections/github/connect', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ token })
        });
        ok = response.ok;
        payload = await response.json().catch(() => ({}));
      } catch {
        payload = {};
      }
      // Clear the field either way, so a token never lingers in the DOM.
      if (input) input.value = '';
      ctx.setBusy(false);

      if (!ok) {
        ctx.setError(payload.message || 'Could not connect to GitHub.');
        return;
      }

      ctx.announce(`GitHub connected as @${payload.login || ''}`.trim());
      // Confirm rather than merely recheck. Recheck refreshes the status --
      // which correctly marked step 1 done -- but leaves the dialog showing
      // the step the user is standing on, so connecting appeared to do
      // nothing while the progress header said otherwise. Confirming is the
      // wizard's own advance path, and it still advances only because the
      // server re-evaluated the step as satisfied.
      await ctx.confirm();
    }
  };

  // The repository step needs no custom UI -- the wizard's own renderer draws
  // an options list, which is exactly right for "pick one repository". It
  // needs a renderer only to CLAIM the step: `capability_configure` is also
  // registered by Calendar Ops, whose renderer returns early for steps it does
  // not own without drawing anything, leaving this step with an empty body and
  // no repositories to choose from.
  const repositoryStepRenderer = {
    owns: ownsStep,
    render(container, ctx) {
      const options = Array.isArray(ctx.step?.options) ? ctx.step.options : [];

      // A link to where repository access is actually changed, offered up
      // front. The picker can only list what the token can READ, which is a
      // wider set than it can write to, so "this one is not writable" is a
      // normal outcome here -- and the fix lives on a page the user would
      // otherwise have to go find.
      const help = document.createElement('p');
      help.className = 'github-setup-help';
      help.appendChild(document.createTextNode('Repository missing, or not writable? '));
      const manage = document.createElement('a');
      manage.href = TOKEN_SETTINGS_URL;
      manage.target = '_blank';
      manage.rel = 'noopener noreferrer';
      manage.textContent = "Manage your token's repository access ↗";
      help.appendChild(manage);
      help.appendChild(
        document.createTextNode(
          ' — grant it Issues (read and write) on the repo you want, then come back and choose it.'
        )
      );
      container.appendChild(help);

      // The wizard draws the options; this only adds a way to find one.
      ctx.renderDefault(container);

      if (options.length > FILTER_THRESHOLD) {
        attachFilter(container, options.length);
      }
    }
  };

  // attachFilter narrows the rendered option list in place. It filters what is
  // already on screen rather than re-querying: the server's list is already
  // capped, and a round trip per keystroke would make a long list slower, not
  // faster.
  function attachFilter(container, total) {
    const list = container.querySelector('.setup-wizard-options');
    if (!list) return;

    const wrap = document.createElement('label');
    wrap.className = 'github-setup-filter';
    wrap.textContent = `Filter ${total} repositories`;

    const input = document.createElement('input');
    input.type = 'search';
    input.className = 'modern-input';
    input.placeholder = 'Start typing a name…';
    input.autocomplete = 'off';
    wrap.appendChild(input);

    const empty = document.createElement('p');
    empty.className = 'github-setup-help';
    empty.hidden = true;
    empty.textContent = 'No repository matches that. Clear the filter to see them all.';

    input.addEventListener('input', () => {
      const needle = input.value.trim().toLowerCase();
      let shown = 0;
      list.querySelectorAll('.setup-wizard-option').forEach(item => {
        const match = !needle || item.textContent.toLowerCase().includes(needle);
        item.hidden = !match;
        if (match) shown += 1;
      });
      empty.hidden = shown !== 0;
    });

    list.parentNode.insertBefore(wrap, list);
    list.parentNode.insertBefore(empty, list.nextSibling);
  }

  function register() {
    const wizard = window.SetupWizard;
    if (!wizard || typeof wizard.registerStepRenderer !== 'function') return false;
    wizard.registerStepRenderer('account_link', connectStepRenderer);
    wizard.registerStepRenderer('capability_configure', repositoryStepRenderer);
    return true;
  }

  // setup-wizard.js is a sibling deferred script, so it may not have run yet.
  if (!register() && typeof document !== 'undefined') {
    document.addEventListener('DOMContentLoaded', register);
  }

  window.GitHubSetupStep = connectStepRenderer;
})();
