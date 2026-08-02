// Source audit: the surfaces this feature introduced must speak the cozy
// vocabulary, and cosmetic choices must not reach runtime behavior.
//
// Run with: node --test internal/web/static/js/modules/workspace-toolbox-terminology.test.js
//
// This scans the real module sources rather than rendered output, because the
// thing being protected is a property of the code: a legacy term added to a new
// string a year from now should fail here, not wait for someone to notice it in
// the UI (PRD FR-166, FR-168, FR-169).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const MODULES = ['workspace-toolbox.js', 'workspace-goal-prepare.js'];

function source(name) {
  return readFileSync(new URL('./' + name, import.meta.url), 'utf8');
}

// Strip comments before auditing. A comment explaining what a surface REPLACED
// legitimately names the old thing — "this replaces the old Loadout editor" is
// documentation, not user-facing copy.
function codeOnly(text) {
  return text
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .filter(line => !line.trimStart().startsWith('//'))
    .join('\n');
}

// Extract the string literals — that is what a user can actually read.
function literals(text) {
  const matches = codeOnly(text).match(/'[^'\n]*'|"[^"\n]*"|`[^`]*`/g) || [];
  return matches.join('\n');
}

test('no legacy militaristic vocabulary reaches a user-facing string (FR-168)', () => {
  // Section 8.1's forbidden register: weapons, tactical silhouettes, military
  // ranks, combat language, and rarity tiers.
  const forbidden = [
    /\bloadout\b/i,
    /\barmory\b/i,
    /\bequip(ped|ping)?\b/i,
    /\bdeploy(ed|ment)?\b/i,
    /\bgarrison\b/i,
    /\bsquad\b/i,
    /\bformation\b/i,
    /\bcombat\b/i,
    /\btactical\b/i,
    /\bweapon/i,
    /\bammo\b/i,
    /\brarity\b/i,
    /\blegendary\b/i,
    /\bepic\b/i,
    /\bmission\b/i,
    /after-action/i
  ];

  for (const name of MODULES) {
    const text = literals(source(name));
    for (const pattern of forbidden) {
      assert.doesNotMatch(text, pattern, `${name} exposes a legacy term matching ${pattern}`);
    }
  }
});

test('the cozy vocabulary is actually used (FR-168)', () => {
  const toolbox = literals(source('workspace-toolbox.js'));
  assert.match(toolbox, /Toolbox/i);
  assert.match(toolbox, /Workshop/i);
  assert.match(toolbox, /Use This Toolbox/);

  const prepare = literals(source('workspace-goal-prepare.js'));
  assert.match(prepare, /goal/i);
  assert.match(prepare, /toolbox/i);
});

test('no fictional bonus or quality claim is made (FR-72, FR-166, FR-169)', () => {
  // Focus describes a surface; it never promises an outcome. A string that
  // claimed a bonus, a multiplier, or better results would be unfalsifiable
  // and, worse, would make people trust a state that guarantees nothing.
  const forbidden = [
    /\bbonus\b/i,
    /\bmultiplier\b/i,
    /\bpower level\b/i,
    /\bstat boost/i,
    /\bguarantee/i,
    /\bwill perform better/i,
    /\+\d+%/
  ];

  for (const name of MODULES) {
    const text = literals(source(name));
    for (const pattern of forbidden) {
      assert.doesNotMatch(
        text,
        pattern,
        `${name} makes a fictional performance claim matching ${pattern}`
      );
    }
  }
});

test('cosmetic fields never reach a runtime decision (FR-169)', () => {
  // A toolbox's name, icon, and color are decoration. If one of them were read
  // while deciding readiness, whether to allow a switch, or how to rank a
  // recommendation, renaming a toolbox could change what an agent may do.
  const text = codeOnly(source('workspace-toolbox.js'));

  // The decision points in this module.
  const decisions = [
    /const canSubmit\s*=([^;]*);/,
    /function allIssuesAcknowledged\(\)\s*\{([\s\S]*?)\n {2}\}/
  ];
  for (const pattern of decisions) {
    const match = text.match(pattern);
    assert.ok(match, `expected to find the decision matching ${pattern}`);
    const body = match[1];
    for (const cosmetic of [/\.icon\b/, /\.color\b/, /\.name\b/]) {
      assert.doesNotMatch(
        body,
        cosmetic,
        `a runtime decision reads the cosmetic field ${cosmetic}`
      );
    }
  }
});

test('status is never conveyed by color alone (FR-162)', () => {
  // Every state chip carries its own words. The CSS may tint them; the text is
  // what survives a screenshot in greyscale, a colorblind reader, or a screen
  // reader.
  const text = source('workspace-toolbox.js');
  const asText = [
    // Readiness, Focus, and connection state each render their own words.
    [/text: 'Readiness: ' \+ preview\.readiness/, 'readiness'],
    [/text: focus\.state/, 'Focus state'],
    [/text: item\.connected \? 'Connected' : 'Not connected'/, 'connection state']
  ];
  for (const [pattern, label] of asText) {
    assert.match(text, pattern, `${label} must be rendered as text, not conveyed by styling alone`);
  }
});
