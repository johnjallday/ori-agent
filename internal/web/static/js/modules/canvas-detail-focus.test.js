import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('./workspace-canvas-helpers.js', import.meta.url), 'utf8');
for (const [name, id] of [
  ['hideAgentDetails', 'agent-details-panel'],
  ['hideAttachmentDetails', 'attachment-details-panel'],
  ['hideTaskDetails', 'task-details-panel']
]) {
  function fixture(activeInside = false) {
    let focused = 0;
    const button = {},
      body = {};
    const panel = { style: { display: 'block' }, contains: node => node === button };
    const actions = { style: { display: 'block' } };
    const canvas = {
      focus() {
        focused++;
      }
    };
    const context = {
      window: { agentCanvas: { state: { expandedTask: {} } } },
      document: {
        activeElement: activeInside ? button : body,
        getElementById: key =>
          key === id
            ? panel
            : key === 'agent-canvas'
              ? canvas
              : key === 'task-actions'
                ? actions
                : null
      }
    };
    const start = source.indexOf(`function ${name}(`);
    assert.ok(start >= 0);
    vm.runInNewContext(source.slice(start, source.indexOf('\n}', start) + 2), context);
    return { close: context[name], panel, actions, button, focused: () => focused };
  }
  test(`${name}: actual mouse invoker restores Canvas even when Safari did not focus it`, () => {
    const f = fixture();
    f.close(f.button);
    assert.equal(f.panel.style.display, 'none');
    assert.equal(f.focused(), 1);
  });
  test(`${name}: keyboard close returns focus from its own panel`, () => {
    const f = fixture(true);
    f.close();
    assert.equal(f.panel.style.display, 'none');
    assert.equal(f.focused(), 1);
  });
  test(`${name}: background updates cannot take another host's focus`, () => {
    const f = fixture();
    f.close();
    assert.equal(f.panel.style.display, 'none');
    assert.equal(f.focused(), 0);
  });
}
