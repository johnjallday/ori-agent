import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import {
  sourceHomePlacementResult,
  sourceHomeObservationResult,
  sourceHomeReviewPlan,
  folderHomeMapTarget
} from './personal-assistant-folder.js';
import {
  workspaceCreationReviewHTML,
  setupCreationReviewMaterial
} from './workspace-creation-review.js';

const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
// Execute the real source controller, isolating only browser/owner I/O. Domain
// creation and proof are covered separately by real-server acceptance.
function controller() {
  const effects = { setup: 0, position: 0, returned: 0, proofReads: 0, layoutReads: 0, retries: 0 };
  const state = {
    reviewGeneration: 1,
    reviewSlot: true,
    offer: {
      id: 'offer',
      status: 'pending',
      subject: { name: 'Song' },
      plan: {
        digest: 'digest',
        destination: { kind: 'home', status: 'new', name: 'Music Home' },
        visual: { entity: { kind: 'home', operation: 'create', name: 'Music Home' } }
      }
    }
  };
  let placement,
    sequence = 0;
  const context = vm.createContext({
    state,
    effects,
    DIGEST_ENDPOINT: '/digest',
    console,
    document: {
      querySelector: () => ({ getClientRects: () => [1] }),
      getElementById: () => ({ focus() {} })
    },
    window: {
      innerWidth: 1200,
      OriWorkspaceMap: {
        getLayoutState: () => ({ status: 'ready', positions: {} }),
        refreshLayout: async options => {
          effects.layoutReads++;
          return options.current();
        },
        retryPlacement: async (_id, _point, options) => {
          effects.retries++;
          assert.equal(options.kind, 'group');
          return true;
        },
        beginPlacement: spec => {
          placement = spec;
          return true;
        },
        endPlacement: () => true,
        commitPlacement: async () => {
          effects.position++;
          return { saved: true };
        }
      },
      PersonalAssistantPanel: {
        close() {},
        open() {
          effects.returned++;
        }
      },
      OriHomeCockpit: { refreshQuietly: async () => true }
    },
    OriFolderSetupVisual: { briefError: () => '', setupAcceptedLine: () => '' },
    render() {},
    elements: () => null,
    enterThread() {},
    requestId: () => `request-${++sequence}`,
    briefFor: () => '',
    monitoringChosen: () => false,
    isInboxFolderOffer: () => false,
    reviewResponseGuard: () => () => true,
    conversationReviewLoading: () => false,
    prerequisites: { active: () => false },
    showError() {},
    showOfferFailure() {},
    announceOffer() {},
    revealRunSurface() {},
    acceptedSetups: new Set(),
    sayInConversation() {},
    postOffer: async (_id, _action, body) => {
      effects.setup++;
      state.offer = {
        ...state.offer,
        status: 'awaiting_outcome',
        home_result: {
          offer_id: 'offer',
          plan_digest: 'digest',
          request_id: body.request_id,
          state: 'ready',
          home_id: 'home',
          created_by_this_operation: true,
          layout_subject_new: true,
          route: '/workspaces/music/assistant'
        }
      };
      return { ok: true, payload: {} };
    },
    fetch: async url => {
      if (url.startsWith('/digest')) effects.proofReads++;
      return {
        ok: true,
        json: async () =>
          url.startsWith('/digest')
            ? { folder_digest: { offer: state.offer } }
            : { id: 'home', kind: 'group', name: 'Music Home' }
      };
    }
  });
  const code = source.slice(
    source.indexOf('let sourceCreationReview = null;'),
    source.indexOf('// A runner can finish before its verified receipt')
  );
  vm.runInContext(
    code.replaceAll('export function ', 'function ') +
      '\nglobalThis.harness = { startOneCardSetup, actOnSourceCreationReview, observeSourceCreationResult, renderSourceCreationReview, navigateFolderHomeMap, sourceReviewMapAvailable, review: () => sourceCreationReview };',
    context
  );
  return { ...context.harness, context, state, effects, placement: () => placement };
}

test('Home review requires a canonical Home destination, not a name', () => {
  assert.equal(sourceHomeReviewPlan({ visual: { entity: { kind: 'home' } } }), false);
  assert.equal(
    sourceHomeReviewPlan({ destination: { kind: 'home' }, visual: { entity: { kind: 'home' } } }),
    true
  );
});

test('Home proof binds the original accepted offer, plan and press, not the later song', () => {
  const review = { home: true, offerID: 'offer', planDigest: 'digest', acceptedRequestID: 'press' };
  const home = {
    offer_id: 'offer',
    plan_digest: 'digest',
    request_id: 'press',
    home_id: 'home',
    state: 'ready'
  };
  assert.equal(
    sourceHomePlacementResult({ home_result: home, outcome: { workspace_id: 'song' } }, review),
    home
  );
  for (const key of ['offer_id', 'plan_digest', 'request_id']) {
    assert.equal(
      sourceHomePlacementResult({ home_result: { ...home, [key]: 'other' } }, review),
      null
    );
  }
  assert.equal(sourceHomePlacementResult({ home_result: home }, {}), null);
});

test('a second press may observe the original Home but cannot inherit new-placement proof', async () => {
  const review = {
    home: true,
    offerID: 'offer',
    planDigest: 'digest',
    acceptedRequestID: 'second'
  };
  const home = {
    offer_id: 'offer',
    plan_digest: 'digest',
    request_id: 'first',
    state: 'ready',
    home_id: 'home',
    created_by_this_operation: true,
    layout_subject_new: true
  };
  const observed = sourceHomeObservationResult({ home_result: home }, review);
  assert.equal(observed.home_id, 'home');
  assert.equal(observed.layout_subject_new, false);
  assert.equal(observed.created_by_this_operation, false);
  assert.equal(home.layout_subject_new, true, 'the canonical original proof is not rewritten');
  for (const changed of [{ offer_id: 'other' }, { plan_digest: 'other' }, { state: 'partial' }])
    assert.equal(
      sourceHomeObservationResult({ home_result: { ...home, ...changed } }, review),
      null
    );
  const c = controller();
  await c.startOneCardSetup({});
  await c.actOnSourceCreationReview(c.review(), { action: 'place' });
  await c.placement().onConfirm(c.review().token, { x: 1600, y: 900 });
  c.state.offer.home_result.request_id = 'first-tab';
  await c.observeSourceCreationResult(c.review());
  assert.equal(c.review().result.id, 'home');
  assert.equal(c.review().existing, true);
  assert.equal(c.effects.position, 0);
});

test('a withdrawn or changed Home plan returns to source review without stale terms or effects', async () => {
  for (const plan of [
    undefined,
    { digest: 'different', visual: { entity: { kind: 'workspace' } } }
  ]) {
    const c = controller();
    await c.startOneCardSetup({});
    const host = { hidden: false };
    c.context.elements = () => ({
      offer: { classList: { toggle() {} }, querySelector: () => host }
    });
    c.state.offer.plan = plan;
    c.renderSourceCreationReview();
    assert.equal(c.review().editing, true);
    assert.equal(host.hidden, true);
    assert.equal(c.effects.setup, 0);
    assert.equal(c.effects.position, 0);
  }
});

test('Home identity is not the source song name and its later goal is not a Home purpose', async () => {
  const c = controller();
  await c.startOneCardSetup({});
  c.state.workspaceName = 'The source song';
  c.state.offer.plan.lines = [];
  c.state.offer.plan.brief_default = 'Goal for the later project';
  c.context.briefFor = () => null;
  c.context.setupCreationReviewMaterial = setupCreationReviewMaterial;
  c.context.handoffFirstPressLabel = () => '';
  c.context.handoffLineParts = () => null;
  c.context.elements = () => ({ offer: { classList: { toggle() {} }, querySelector: () => ({}) } });
  let view;
  c.review().panel = {
    update(value) {
      view = value;
    }
  };
  c.renderSourceCreationReview();
  assert.equal(view.name, 'Music Home');
  assert.equal(view.canEditName, false);
  assert.equal(view.goal, 'Goal for the later project');
  assert.equal(view.goalLabel, 'Later project goal');
  assert.equal(view.canEditGoal, false);
  c.state.offer.plan.brief_default = '';
  c.renderSourceCreationReview();
  assert.equal(view.showGoal, false, 'a collection does not invent an empty Home-purpose question');
  c.state.offer.plan.lines = [{ kind: 'handoff', name: 'Open later' }];
  c.state.offer.plan.hire = { role_label: 'REAPER Assistant', mode: 'create' };
  c.renderSourceCreationReview();
  assert.equal(view.rows[0].label, 'Later project · REAPER Assistant');
  assert.match(view.rows[0].detail, /not by this Home setup/);
});

test('enter, hover and Back retain review and candidate with no domain effects; final gesture dispatches once', async () => {
  const c = controller();
  await c.startOneCardSetup({});
  const review = c.review();
  await c.actOnSourceCreationReview(review, { action: 'place' });
  assert.equal(c.placement().preview.kind, 'group');
  c.placement().onCandidate(review.token, { x: 1200, y: 800 });
  c.placement().onBack();
  assert.equal(c.review(), review);
  assert.equal(review.candidate.x, 1200);
  assert.equal(c.effects.setup, 0);
  await c.actOnSourceCreationReview(review, { action: 'place' });
  assert.equal(c.placement().initialCandidate.x, 1200);
  await c.placement().onConfirm(review.token, { x: 1600, y: 900 });
  await c.placement().onConfirm(review.token, { x: 2000, y: 1000 });
  assert.equal(c.effects.setup, 1);
  assert.equal(review.candidate.x, 1600);
  await c.observeSourceCreationResult(review);
  await c.observeSourceCreationResult(review);
  assert.equal(c.effects.position, 1);
  assert.equal(review.result.id, 'home');
});

test('automatic mode only clears candidate; hidden/narrow/read-only maps cannot place', async () => {
  const c = controller();
  await c.startOneCardSetup({});
  const review = c.review();
  review.candidate = { x: 1, y: 2 };
  await c.actOnSourceCreationReview(review, { action: 'suggested' });
  assert.equal(review.candidate, null);
  assert.equal(c.effects.setup, 0);
  c.context.window.innerWidth = 700;
  assert.equal(c.sourceReviewMapAvailable(), false);
  c.context.window.innerWidth = 1200;
  c.context.document.querySelector = () => ({ getClientRects: () => [] });
  assert.equal(c.sourceReviewMapAvailable(), false);
  c.context.document.querySelector = () => ({ getClientRects: () => [1] });
  c.context.window.OriWorkspaceMap.getLayoutState = () => ({ status: 'unavailable' });
  assert.equal(c.sourceReviewMapAvailable(), false);
  await c.actOnSourceCreationReview(review, { action: 'confirm' });
  await c.observeSourceCreationResult(review);
  assert.equal(c.effects.setup, 1);
  assert.equal(c.effects.position, 0);
});

test('lost confirmation retires the old nothing-is-set-up summary without claiming success or resending', async () => {
  const c = controller();
  let cleared = 0;
  c.context.window.PersonalAssistantFolderSetup = {
    applyReviewContext(value) {
      assert.equal(value, null);
      cleared++;
    }
  };
  await c.startOneCardSetup({});
  await c.actOnSourceCreationReview(c.review(), { action: 'place' });
  assert.equal(cleared, 0);
  c.context.postOffer = async () => {
    c.effects.setup++;
    throw new Error('lost delivery');
  };
  await c.placement().onConfirm(c.review().token, { x: 1600, y: 900 });
  assert.equal(cleared, 1);
  assert.match(c.review().message, /delivery is unknown/);
  assert.equal(c.review().result, undefined);
  await c.placement().onConfirm(c.review().token, { x: 1600, y: 900 });
  assert.equal(c.effects.setup, 1);
  assert.equal(c.effects.position, 0);
});

test('changed material or replacement editor refuses final gesture without setup', async () => {
  for (const mutate of [
    c => {
      c.state.offer.plan.digest = 'changed';
    },
    c => {
      c.state.reviewGeneration++;
    }
  ]) {
    const c = controller();
    await c.startOneCardSetup({});
    await c.actOnSourceCreationReview(c.review(), { action: 'place' });
    mutate(c);
    assert.equal(await c.placement().onConfirm(c.review().token, { x: 20, y: 20 }), false);
    assert.equal(c.effects.setup, 0);
  }
});

test('reused/history Home navigation requires canonical identity, never the name or later project', () => {
  assert.equal(
    folderHomeMapTarget({
      home_result: { state: 'ready', home_id: 'canonical-home', name: 'Same name' },
      outcome: { workspace_id: 'later-song' }
    }),
    'canonical-home'
  );
  assert.equal(
    folderHomeMapTarget({
      plan: { destination: { kind: 'home', status: 'existing', workspace_id: 'canonical-home' } }
    }),
    'canonical-home'
  );
  for (const state of ['unproven', 'unavailable'])
    assert.equal(folderHomeMapTarget({ home_result: { state, home_id: 'namesake' } }), '');
  assert.equal(
    folderHomeMapTarget({ outcome: { workspace_id: 'song', name: 'Music Production Home' } }),
    ''
  );
});

test('verified Show navigation closes the drawer instead of restoring it over the Home', async () => {
  const c = controller();
  await c.startOneCardSetup({});
  await c.actOnSourceCreationReview(c.review(), { action: 'place' });
  await c.placement().onConfirm(c.review().token, { x: 1600, y: 900 });
  c.context.window.location = { href: '' };
  let closes = 0;
  c.context.window.PersonalAssistantPanel.close = () => closes++;
  await c.navigateFolderHomeMap('home', 'show');
  assert.equal(closes, 1);
  assert.equal(c.context.window.location.href, '/?view=map&show_on_map=home');
  assert.equal(c.effects.setup, 1);
  assert.equal(c.effects.position, 0);
});

async function failedPosition() {
  const c = controller();
  await c.startOneCardSetup({});
  await c.actOnSourceCreationReview(c.review(), { action: 'place' });
  await c.placement().onConfirm(c.review().token, { x: 1600, y: 900 });
  c.context.window.OriWorkspaceMap.commitPlacement = async () => {
    c.effects.position++;
    return { saved: false };
  };
  await c.observeSourceCreationResult(c.review());
  assert.equal(c.review().positionFailed, true);
  return c;
}

test('position-only retry rereads exact Home authority and live layout, never setup', async () => {
  const c = await failedPosition();
  const reads = c.effects.proofReads;
  await c.actOnSourceCreationReview(c.review(), { action: 'retry-position' });
  assert.equal(c.effects.setup, 1);
  assert.equal(c.effects.position, 1);
  assert.equal(c.effects.proofReads, reads + 1);
  assert.equal(c.effects.layoutReads, 2);
  assert.equal(c.effects.retries, 1);
  assert.equal(c.review().positionFailed, false);
});

test('a saved but unacknowledged position is observed without another PATCH', async () => {
  const c = await failedPosition();
  c.context.window.OriWorkspaceMap.getLayoutState = () => ({
    status: 'ready',
    positions: { home: c.review().candidate }
  });
  await c.actOnSourceCreationReview(c.review(), { action: 'retry-position' });
  assert.equal(c.effects.retries, 0);
  assert.equal(c.effects.setup, 1);
  assert.equal(c.review().positionFailed, false);
});

test('changed Home identity/proof/phase refuses position repair', async () => {
  for (const change of [
    { home_id: 'replacement' },
    { state: 'partial' },
    { state: 'unavailable' },
    { created_by_this_operation: false },
    { layout_subject_new: false },
    { request_id: 'other' }
  ]) {
    const c = await failedPosition();
    Object.assign(c.state.offer.home_result, change);
    await c.actOnSourceCreationReview(c.review(), { action: 'retry-position' });
    assert.equal(c.effects.retries, 0);
    assert.equal(c.effects.setup, 1);
    assert.equal(c.review().result, null);
    assert.equal(c.review().observeFailed, true);
  }
});

test('changed position, occupancy, or failed map refresh cannot silently substitute a point', async () => {
  for (const failure of ['moved', 'occupied', 'tree-unavailable', 'layout-unavailable']) {
    const c = await failedPosition();
    const map = c.context.window.OriWorkspaceMap;
    if (failure === 'moved')
      map.getLayoutState = () => ({ status: 'ready', positions: { home: { x: 1, y: 2 } } });
    if (failure === 'occupied') map.retryPlacement = async () => false;
    if (failure === 'tree-unavailable')
      c.context.window.OriHomeCockpit.refreshQuietly = async () => false;
    if (failure === 'layout-unavailable') map.refreshLayout = async () => false;
    await c.actOnSourceCreationReview(c.review(), { action: 'retry-position' });
    assert.equal(c.effects.setup, 1);
    assert.equal(c.effects.retries, 0);
    assert.equal(c.review().positionFailed, true);
  }
});

test('late proof or accepted reply cannot release a newer owner busy state or save geometry', async () => {
  const c = await failedPosition();
  let resolve;
  c.context.fetch = () =>
    new Promise(done => {
      resolve = done;
    });
  const repair = c.actOnSourceCreationReview(c.review(), { action: 'retry-position' });
  c.state.reviewGeneration++;
  c.state.busy = true;
  resolve({ ok: true, json: async () => ({ folder_digest: { offer: c.state.offer } }) });
  await repair;
  assert.equal(c.effects.retries, 0);
  assert.equal(c.state.busy, true);

  const d = controller();
  await d.startOneCardSetup({});
  await d.actOnSourceCreationReview(d.review(), { action: 'place' });
  d.context.postOffer = () =>
    new Promise(done => {
      resolve = done;
    });
  const accepted = d.placement().onConfirm(d.review().token, { x: 10, y: 20 });
  d.state.reviewGeneration++;
  d.state.busy = true;
  resolve({ ok: true, payload: {} });
  await accepted;
  assert.equal(d.state.busy, true);
  assert.equal(d.effects.position, 0);
});

test('a newer map editor prevents old completion refresh, positioning, and focus return', async () => {
  const c = controller();
  await c.startOneCardSetup({});
  await c.actOnSourceCreationReview(c.review(), { action: 'place' });
  await c.placement().onConfirm(c.review().token, { x: 1600, y: 900 });
  c.context.window.OriWorkspaceMap.canReturnFromPlacement = () => false;
  c.context.window.OriHomeCockpit.refreshQuietly = async () => {
    throw new Error('must not repaint newer editor');
  };
  await c.observeSourceCreationResult(c.review());
  assert.equal(c.review().result.id, 'home');
  assert.equal(c.effects.position, 0);
  assert.equal(c.effects.returned, 0);
  assert.equal(c.effects.layoutReads, 0);
});

test('shared renderer uses truthful owner labels and escapes Home names', () => {
  const html = workspaceCreationReviewHTML({
    subjectKind: 'group',
    name: '<Home>',
    placeLabel: 'Place & set up on map',
    confirmLabel: 'Set up Home',
    automatic: true,
    reviewed: true,
    canConfirm: true
  });
  assert.match(html, /Place &amp; set up on map/);
  assert.match(html, /Set up Home<\/button>/);
  assert.doesNotMatch(html, /<Home>/);
});
