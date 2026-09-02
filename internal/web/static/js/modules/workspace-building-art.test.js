import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-building-art.js', import.meta.url), 'utf8');

function loadBuildingArt() {
  const window = {};
  vm.runInNewContext(source, { window }, { filename: 'workspace-building-art.js' });
  return window.OriWorkspaceBuildingArt;
}

test('maps each curated built-in blueprint to its stable building variant', () => {
  const art = loadBuildingArt();
  const expected = {
    'personal-ops': 'hq',
    'email-ops': 'mail',
    'calendar-ops': 'calendar',
    'research-project': 'research',
    'content-production': 'studio',
    'writing-project': 'studio',
    'daily-briefings': 'briefing',
    'downloads-janitor': 'depot',
    'file-janitor': 'depot',
    'github-ops': 'github',
    travels: 'travel'
  };

  for (const [blueprintID, variant] of Object.entries(expected)) {
    assert.equal(art.variantForBlueprint(blueprintID, true), variant);
    const svg = art.svgForVariant(variant, { context: 'map' });
    assert.match(svg, new RegExp(`data-building-variant="${variant}"`));
    assert.match(svg, /data-building-emblem=/);
  }
});

test('does not infer a curated silhouette for custom, unknown, or name-only workspaces', () => {
  const art = loadBuildingArt();

  assert.equal(art.variantForBlueprint('email-ops', false), '');
  assert.equal(art.variantForBlueprint('future-blueprint', true), '');
  assert.equal(art.variantForBlueprint('constructor', true), '');
  assert.equal(art.svgForVariant('__proto__', { context: 'map' }), '');
  assert.equal(art.variantForWorkspace({ name: 'Email Ops' }), '');
  assert.equal(
    art.variantForWorkspace({ blueprint_id: 'email-ops', blueprint_builtin: false }),
    ''
  );
  assert.equal(
    art.variantForWorkspace({ blueprint_id: 'personal-ops', blueprint_builtin: true }),
    '',
    'provenance alone cannot designate Personal HQ'
  );
  assert.equal(
    art.variantForWorkspace({
      blueprint_id: 'personal-ops',
      blueprint_builtin: true,
      designation: 'personal_hq'
    }),
    'hq'
  );
});

test('GitHub Ops and Travels use distinct repository and flight emblems', () => {
  const art = loadBuildingArt();
  const githubSVG = art.svgForVariant('github', { context: 'map' });
  const travelSVG = art.svgForVariant('travel', { context: 'map' });

  assert.match(githubSVG, /data-building-emblem="github"/);
  assert.doesNotMatch(githubSVG, /data-building-emblem="issue"/);
  assert.match(travelSVG, /data-building-emblem="flight"/);
  assert.doesNotMatch(travelSVG, /data-building-emblem="travel"/);
});

test('renders catalog and map contexts from the same inert artwork', () => {
  const art = loadBuildingArt();
  const mapSVG = art.svgForVariant('research', { context: 'map' });
  const catalogSVG = art.svgForVariant('research', { context: 'catalog' });

  assert.match(mapSVG, /class="ws-map-struct ws-map-struct--blueprint"/);
  assert.match(catalogSVG, /class="workspace-template-building-art"/);
  assert.match(mapSVG, /data-building-emblem="research"/);
  assert.match(catalogSVG, /data-building-emblem="research"/);
  assert.equal(art.svgForVariant('unknown', { context: 'map' }), '');
});
