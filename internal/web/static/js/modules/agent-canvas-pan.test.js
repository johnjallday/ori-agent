import test from 'node:test';
import assert from 'node:assert/strict';
import { AgentCanvasInteractionHandler } from './agent-canvas-interactions.js';

function probe(target, { hovered = false, overlay = false, description = false } = {}) {
  const canvas = { style: {}, matches: () => hovered },
    body = {};
  const state = {
    forms: { createTaskDescriptionFocused: description, createTaskDescription: '' },
    spacePressed: false
  };
  const doc = {
    body,
    activeElement: target === 'canvas' ? canvas : target === 'body' ? body : { tagName: target },
    querySelector: () => (overlay ? {} : null)
  };
  let prevented = 0;
  const old = globalThis.document;
  globalThis.document = doc;
  try {
    const handler = new AgentCanvasInteractionHandler(canvas, state, { draw() {} });
    handler.onKeyDown({
      key: ' ',
      preventDefault() {
        prevented++;
      }
    });
    const armed = state.spacePressed;
    handler.onKeyUp({ key: ' ' });
    assert.equal(state.spacePressed, false);
    return { armed, prevented, text: state.forms.createTaskDescription };
  } finally {
    if (old === undefined) delete globalThis.document;
    else globalThis.document = old;
  }
}
test('focused Canvas Space arms pan and suppresses document scrolling', () => {
  assert.deepEqual(probe('canvas'), { armed: true, prevented: 1, text: '' });
});
test('hovered Canvas accepts Space from the background, not another control', () => {
  assert.equal(probe('body', { hovered: true }).armed, true);
  for (const target of ['INPUT', 'TEXTAREA', 'SELECT', 'BUTTON', 'A', 'SUMMARY']) {
    assert.deepEqual(probe(target, { hovered: true }), { armed: false, prevented: 0, text: '' });
  }
});
test('Space elsewhere retains native document scrolling', () => {
  assert.deepEqual(probe('body'), { armed: false, prevented: 0, text: '' });
});
test('active overlays and the Canvas description editor cannot arm panning', () => {
  assert.deepEqual(probe('canvas', { overlay: true }), { armed: false, prevented: 0, text: '' });
  assert.deepEqual(probe('canvas', { description: true }), {
    armed: false,
    prevented: 1,
    text: ' '
  });
});
