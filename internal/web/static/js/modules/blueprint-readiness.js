/**
 * Blueprint readiness — the browser half of the readiness contract.
 *
 * The server sends one projection per blueprint in the creation catalog
 * (`readiness` on each template) and the same shape back in a creation
 * conflict. This module is the only place that interprets it: it normalizes
 * the payload defensively, turns a reason code into copy, and renders the
 * badge, the accessible description, and the inline recovery panel.
 *
 * Two rules shape everything here:
 *
 *   - The projection is guidance, never authorization. Nothing in this file
 *     decides that a workspace may be created; it decides what the user is
 *     told and which explicit action is offered. The server refuses again at
 *     create time regardless.
 *   - Every string that reaches the DOM goes in as text. The server already
 *     sanitizes its copy; writing it as text rather than markup means a
 *     regression there cannot become script here.
 */

const BR_STATES = {
  READY: 'ready',
  ACTION_REQUIRED: 'action_required',
  UNAVAILABLE: 'unavailable'
};

const BR_OWNERSHIP = {
  BUILTIN: 'builtin',
  USER: 'user',
  PLUGIN: 'plugin'
};

const BR_REASONS = {
  NONE: '',
  PLUGIN_INSTALL_REQUIRED: 'plugin_install_required',
  PLUGIN_ENABLE_REQUIRED: 'plugin_enable_required',
  PLUGIN_UPDATE_REQUIRED: 'plugin_update_required',
  PLATFORM_UNSUPPORTED: 'platform_unsupported',
  PROTOCOL_INCOMPATIBLE: 'protocol_incompatible',
  BLUEPRINT_RETIRED: 'blueprint_retired',
  MANIFEST_INVALID: 'manifest_invalid',
  RUNTIME_PROVIDER_UNAVAILABLE: 'runtime_provider_unavailable',
  DEPENDENCY_STATE_UNKNOWN: 'dependency_state_unknown'
};

// The allowlist, mirrored from the server. An action the server did not send —
// or one this build does not know — is dropped rather than rendered, so a
// stale or hostile payload cannot put an unexpected button in the wizard.
const BR_ACTION_LABELS = {
  install_plugin: 'Install plugin…',
  enable_plugin: 'Enable plugin',
  review_plugin_update: 'Review update…',
  retry: 'Check again',
  manage_plugins: 'Manage plugins',
  change_blueprint: 'Choose another blueprint',
  edit_template_manifest: 'Open template folder'
};

// The primary action is the one the panel presents first and the only one
// styled as a call to action: one blocked blueprint means one next step.
const BR_PRIMARY_ACTIONS = new Set([
  'install_plugin',
  'enable_plugin',
  'review_plugin_update',
  'retry'
]);

const BR_VALID_STATES = new Set(Object.values(BR_STATES));
const BR_VALID_OWNERSHIP = new Set(Object.values(BR_OWNERSHIP));
const BR_VALID_REASONS = new Set(Object.values(BR_REASONS));

function brText(value) {
  return typeof value === 'string' ? value.trim() : '';
}

/**
 * normalize turns whatever arrived into a projection this module can render.
 *
 * An absent `readiness` means a server that predates the contract, or the
 * synthetic Blank entry: both are treated as ready, because refusing to show
 * a blueprint on the strength of a missing field would break the picker for
 * every ordinary template. An unrecognized state, by contrast, is treated as
 * unavailable — a value this build cannot interpret must not be rendered as
 * an ordinary, creatable card.
 */
function brNormalize(raw) {
  if (!raw || typeof raw !== 'object') {
    return { state: BR_STATES.READY, ownership: BR_OWNERSHIP.USER, reason: '', actions: [] };
  }
  const state = BR_VALID_STATES.has(raw.state) ? raw.state : BR_STATES.UNAVAILABLE;
  const ownership = BR_VALID_OWNERSHIP.has(raw.ownership) ? raw.ownership : BR_OWNERSHIP.PLUGIN;
  const reason = BR_VALID_REASONS.has(raw.reason) ? raw.reason : '';
  const actions = Array.isArray(raw.actions)
    ? raw.actions.filter(
        (action, index, list) =>
          Object.hasOwn(BR_ACTION_LABELS, action) && list.indexOf(action) === index
      )
    : [];
  // Both key styles are accepted so normalize is idempotent: the wire format
  // is snake_case, and a projection that has already been through here is
  // camelCase. Callers pass results of this function back into renderPanel, and
  // a second pass must not quietly empty the dependency it already read.
  const dependency =
    raw.dependency && typeof raw.dependency === 'object'
      ? {
          pluginName: brText(raw.dependency.plugin_name ?? raw.dependency.pluginName),
          pluginVersion: brText(raw.dependency.plugin_version ?? raw.dependency.pluginVersion),
          installed: Boolean(raw.dependency.installed),
          enabled: Boolean(raw.dependency.enabled),
          sourceDeclared: Boolean(raw.dependency.source_declared ?? raw.dependency.sourceDeclared)
        }
      : null;

  return {
    state,
    ownership,
    reason,
    summary: brText(raw.summary),
    detail: brText(raw.detail),
    // A diagnostic is author-facing only. The server already withholds it from
    // anyone but a template's owner; dropping it again here means a change on
    // either side alone cannot start showing parser text to end users.
    diagnostic: ownership === BR_OWNERSHIP.USER ? brText(raw.diagnostic) : '',
    dependency,
    actions,
    generation: Number.isFinite(raw.generation) ? raw.generation : 0
  };
}

function brIsReady(readiness) {
  return brNormalize(readiness).state === BR_STATES.READY;
}

function brIsBlocked(readiness) {
  return !brIsReady(readiness);
}

/**
 * badgeLabel returns the short, text-backed state label for a card.
 *
 * Ready returns '' on purpose: a ready blueprint looks exactly as it always
 * has. Badging every card would make the recoverable and unavailable ones
 * harder to pick out, which is the only thing the badge exists to do.
 */
function brBadgeLabel(readiness) {
  const state = brNormalize(readiness).state;
  if (state === BR_STATES.ACTION_REQUIRED) return 'Setup required';
  if (state === BR_STATES.UNAVAILABLE) return 'Unavailable';
  return '';
}

// The glyph is a second, non-color cue carried alongside the label, so state
// survives a monochrome display, a color-vision difference, and a forced-colors
// theme. It is decorative — the label is what a screen reader announces.
function brBadgeGlyph(readiness) {
  const state = brNormalize(readiness).state;
  if (state === BR_STATES.ACTION_REQUIRED) return '!';
  if (state === BR_STATES.UNAVAILABLE) return '×';
  return '';
}

/**
 * describe returns the sentence a card's accessible description carries, so a
 * screen-reader user hears the blueprint's state without having to select it
 * and read the briefing panel.
 */
function brDescribe(readiness) {
  const normalized = brNormalize(readiness);
  if (normalized.state === BR_STATES.READY) return '';
  const label = brBadgeLabel(normalized);
  const summary = normalized.summary;
  if (summary) return `${label}. ${summary}`;
  return label;
}

function brActionLabel(action) {
  return BR_ACTION_LABELS[action] || '';
}

function brCreateElement(tag, className) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  return el;
}

/**
 * renderBadge builds the card badge, or null for a ready blueprint.
 *
 * The badge is text plus a decorative glyph, never color alone, and it is
 * marked as presentation-free content inside the card's own button so it does
 * not become a second focus stop the user has to tab through.
 */
function brRenderBadge(readiness) {
  const normalized = brNormalize(readiness);
  const label = brBadgeLabel(normalized);
  if (!label) return null;
  const badge = brCreateElement('span', 'workspace-template-readiness-badge');
  badge.dataset.state = normalized.state;
  const glyph = brCreateElement('span', 'workspace-template-readiness-glyph');
  glyph.setAttribute('aria-hidden', 'true');
  glyph.textContent = brBadgeGlyph(normalized);
  const text = brCreateElement('span', 'workspace-template-readiness-label');
  text.textContent = label;
  badge.append(glyph, text);
  return badge;
}

/**
 * dependencyLine states the plugin's real lifecycle position in plain words.
 *
 * "Installed, still disabled" is the phrasing that matters most: installing
 * and enabling are separate acts, and a user who has just installed something
 * needs to be told, without ambiguity, that it is not yet running.
 */
function brDependencyLine(readiness) {
  const dependency = readiness.dependency;
  if (!dependency || !dependency.pluginName) return '';
  const name = dependency.pluginName;
  if (!dependency.installed) return `${name} — not installed`;
  const version = dependency.pluginVersion ? ` ${dependency.pluginVersion}` : '';
  return dependency.enabled
    ? `${name}${version} — installed and enabled`
    : `${name}${version} — installed, still disabled`;
}

/**
 * renderPanel builds the inline readiness panel shown in the Step 1 briefing
 * and reused for a creation conflict on Review.
 *
 * Returns null for a ready blueprint: there is nothing to say, and an empty
 * "all good" panel would only add noise to the common case.
 *
 * options.onAction(action, readiness) is called with an allowlisted action
 * name when the user presses one of the offered buttons. The panel itself
 * performs nothing — every recovery is an explicit, host-owned act.
 */
function brRenderPanel(readiness, options) {
  const normalized = brNormalize(readiness);
  if (normalized.state === BR_STATES.READY) return null;
  const settings = options || {};

  const panel = brCreateElement('div', 'workspace-blueprint-readiness');
  panel.dataset.state = normalized.state;
  panel.dataset.reason = normalized.reason;
  // Not a live region. The panel re-renders on every selection change, and
  // announcing all of it each time would read the whole briefing aloud while
  // the user is still browsing. Deliberate announcements go through the
  // wizard's own live region when navigation is actually blocked.
  panel.setAttribute('role', 'group');
  panel.setAttribute('tabindex', '-1');

  const heading = brCreateElement('p', 'workspace-blueprint-readiness-heading');
  const badge = brRenderBadge(normalized);
  if (badge) heading.appendChild(badge);
  const summary = brCreateElement('span', 'workspace-blueprint-readiness-summary');
  summary.textContent = normalized.summary || brBadgeLabel(normalized);
  heading.appendChild(summary);
  panel.appendChild(heading);
  panel.setAttribute('aria-label', `Blueprint status: ${summary.textContent}`);

  if (normalized.detail) {
    const detail = brCreateElement('p', 'workspace-blueprint-readiness-detail');
    detail.textContent = normalized.detail;
    panel.appendChild(detail);
  }

  const dependencyLine = brDependencyLine(normalized);
  if (dependencyLine) {
    const dependency = brCreateElement('p', 'workspace-blueprint-readiness-dependency');
    dependency.textContent = dependencyLine;
    panel.appendChild(dependency);
  }

  // The diagnostic is collapsed behind a disclosure: it is the exact text a
  // template author needs and noise for everyone else, including the author
  // until they choose to look at it.
  if (normalized.diagnostic) {
    const details = brCreateElement('details', 'workspace-blueprint-readiness-diagnostic');
    const summaryEl = brCreateElement('summary', '');
    summaryEl.textContent = 'Technical details';
    const pre = brCreateElement('p', 'workspace-blueprint-readiness-diagnostic-text');
    pre.textContent = normalized.diagnostic;
    details.append(summaryEl, pre);
    panel.appendChild(details);
  }

  if (normalized.actions.length > 0) {
    const actions = brCreateElement('div', 'workspace-blueprint-readiness-actions');
    normalized.actions.forEach((action, index) => {
      const label = brActionLabel(action);
      if (!label) return;
      const button = brCreateElement(
        'button',
        index === 0 && BR_PRIMARY_ACTIONS.has(action)
          ? 'modern-btn modern-btn-primary workspace-blueprint-readiness-action'
          : 'modern-btn modern-btn-secondary workspace-blueprint-readiness-action'
      );
      button.type = 'button';
      button.dataset.readinessAction = action;
      button.textContent = label;
      // Buttons for several blueprints can coexist on Review; naming the
      // blueprint keeps every control's accessible name distinct.
      const subject = brText(settings.blueprintName);
      if (subject) button.setAttribute('aria-label', `${label} for ${subject}`);
      button.addEventListener('click', () => {
        if (typeof settings.onAction === 'function') settings.onAction(action, normalized);
      });
      actions.appendChild(button);
    });
    if (actions.children.length > 0) panel.appendChild(actions);
  }

  return panel;
}

window.BlueprintReadiness = {
  STATES: BR_STATES,
  OWNERSHIP: BR_OWNERSHIP,
  REASONS: BR_REASONS,
  ACTION_LABELS: BR_ACTION_LABELS,
  normalize: brNormalize,
  isReady: brIsReady,
  isBlocked: brIsBlocked,
  badgeLabel: brBadgeLabel,
  badgeGlyph: brBadgeGlyph,
  describe: brDescribe,
  actionLabel: brActionLabel,
  dependencyLine: brDependencyLine,
  renderBadge: brRenderBadge,
  renderPanel: brRenderPanel
};
