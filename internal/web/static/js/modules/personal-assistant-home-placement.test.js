import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { sourceHomePlacementResult, sourceHomeReviewPlan } from './personal-assistant-folder.js';
import { workspaceCreationReviewHTML } from './workspace-creation-review.js';

const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
// Execute the real source controller, isolating only browser/owner I/O. Domain
// creation and proof are covered separately by real-server acceptance.
function controller() {
  const effects = { setup: 0, position: 0, returned: 0 };
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
    console,
    document: {
      querySelector: () => ({ getClientRects: () => [1] }),
      getElementById: () => ({ focus() {} })
    },
    window: {
      innerWidth: 1200,
      OriWorkspaceMap: {
        getLayoutState: () => ({ status: 'ready' }),
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
      OriHomeCockpit: { refreshQuietly: async () => {} }
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
    fetch: async () => ({
      ok: true,
      json: async () => ({ id: 'home', kind: 'group', name: 'Music Home' })
    })
  });
  const code = source.slice(
    source.indexOf('let sourceCreationReview = null;'),
    source.indexOf('// A runner can finish before its verified receipt')
  );
  vm.runInContext(
    code.replaceAll('export function ', 'function ') +
      '\nglobalThis.harness = { startOneCardSetup, actOnSourceCreationReview, observeSourceCreationResult, sourceReviewMapAvailable, review: () => sourceCreationReview };',
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
