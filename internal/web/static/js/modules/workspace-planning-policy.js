// Planning policy: rendering the split between what the planner is asked for
// and what Ori actually checks (FR-124, FR-128, FR-129).
//
// The whole reason this is a separate module is the language rule. Guidance
// lines must never read as promises, and enforced lines must say plainly what
// they block. Keeping the wording in one small, tested place is what stops
// "preferred" and "required" from drifting into each other as the settings
// screen grows.

// GUIDANCE_LABELS names each advisory field for a reader.
const GUIDANCE_LABELS = {
  style: 'Planning style',
  clarification_depth: 'Clarification depth',
  detail_level: 'Level of detail',
  tone: 'Tone'
};

// guidanceLines renders the advisory half.
//
// Every line is phrased as a preference — "asked to", "prefers" — because
// nothing verifies any of it. A line reading "uses an investigation style"
// would state as fact something only requested (FR-129).
export function guidanceLines(guidance) {
  if (!guidance) return [];

  const lines = [];
  for (const [key, label] of Object.entries(GUIDANCE_LABELS)) {
    const value = String(guidance[key] || '').trim();
    if (value) lines.push(`${label}: ${humanize(value)} (asked, not enforced)`);
  }

  const artifacts = guidance.preferred_artifacts || [];
  if (artifacts.length > 0) {
    lines.push(`Prefers to propose: ${artifacts.map(humanize).join(', ')} (asked, not enforced)`);
  }
  return lines;
}

// enforcedLine renders one compiled control: whether it runs, what it does, and
// — when it cannot run here — why not and what is missing (FR-128).
export function enforcedLine(control) {
  if (!control) return '';

  const label = control.label || control.key || 'Control';
  if (!control.available) {
    const detail = control.detail || 'This workspace does not support it.';
    return `${label} — unavailable. ${detail}`;
  }
  if (!control.enabled) {
    return `${label} — off. ${control.description || ''}`.trim();
  }
  return `${label} — on. ${control.description || ''}`.trim();
}

// enforcedState classifies a control for styling and for assistive text, so the
// state is never carried by color alone (FR-162).
export function enforcedState(control) {
  if (!control?.available) return 'unavailable';
  return control.enabled ? 'on' : 'off';
}

// unavailableControls returns the controls this workspace cannot enforce, which
// is what the UI disables rather than hides.
//
// Disabling beats hiding: a control that vanishes leaves the user wondering
// whether the feature exists, while a disabled one with a reason answers the
// question they were about to ask (FR-128).
export function unavailableControls(policy) {
  return (policy?.enforced || []).filter(control => !control.available);
}

// policySummary is the one-line headline: how much of this policy is real.
export function policySummary(policy) {
  const controls = policy?.enforced || [];
  if (controls.length === 0) return 'No planning policy is configured.';

  const active = controls.filter(control => control.available && control.enabled).length;
  const blocked = controls.filter(control => !control.available).length;

  const parts = [`${active} of ${controls.length} checks active`];
  if (blocked > 0) {
    parts.push(`${blocked} unavailable in this workspace`);
  }
  return `${parts.join(', ')}.`;
}

// humanize turns a schema value into something readable without pretending it
// means more than it does.
function humanize(value) {
  return String(value || '')
    .replace(/[_-]+/g, ' ')
    .trim();
}
