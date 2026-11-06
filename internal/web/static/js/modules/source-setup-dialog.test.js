import test from 'node:test';
import assert from 'node:assert/strict';
import { createSourceSetupDialog } from './source-setup-dialog.js';
import { musicSetupOffer } from './personal-assistant-folder.js';

function target() {
  const listeners = [];
  return {
    dataset: {},
    addEventListener(type, fn, capture = false) {
      listeners.push({ type, fn, capture });
    },
    fire(type, props = {}) {
      const event = {
        type,
        ...props,
        stopped: false,
        prevented: false,
        stopImmediatePropagation() {
          this.stopped = true;
        },
        preventDefault() {
          this.prevented = true;
        }
      };
      for (const listener of listeners
        .filter(l => l.type === type)
        .sort((a, b) => Number(b.capture) - Number(a.capture))) {
        listener.fn(event);
        if (event.stopped) break;
      }
      return event;
    }
  };
}
function fixture({ transitioning = false } = {}) {
  const modal = target(),
    win = target();
  let shown = false,
    occupied = false,
    current = true;
  const effects = { resets: 0, closes: 0, shows: 0, focus: 0, returned: 0 };
  const title = { textContent: 'Create Workspace', focus: () => effects.focus++ };
  const close = {
    label: 'Close create workspace',
    getAttribute() {
      return this.label;
    },
    setAttribute(_key, value) {
      this.label = value;
    }
  };
  const host = { hidden: true, querySelector: () => null };
  const others = [];
  modal.querySelector = () => close;
  modal.classList = { contains: () => shown };
  for (const event of ['show', 'shown', 'hide', 'hidden'])
    modal.addEventListener(`${event}.bs.modal`, () => effects.resets++);
  const bs = {
    _isTransitioning: false,
    show() {
      if (modal.fire('show.bs.modal').prevented) return;
      effects.shows++;
      shown = true;
      this._isTransitioning = transitioning;
      if (!transitioning) modal.fire('shown.bs.modal');
    },
    hide() {
      if (this._isTransitioning) return;
      modal.fire('hide.bs.modal');
      shown = false;
      modal.fire('hidden.bs.modal');
    }
  };
  const ref = {
    kind: 'music-folder',
    offerID: 'offer',
    generation: 3,
    current: () => current,
    title: 'Review music folder setup',
    invoker: { isConnected: true, focus: () => effects.returned++ },
    onClose: () => effects.closes++
  };
  const adapter = createSourceSetupDialog({
    modal,
    host,
    win,
    doc: { getElementById: () => title, querySelectorAll: () => others },
    bootstrap: { Modal: { getOrCreateInstance: () => bs } },
    occupied: () => occupied
  });
  return {
    adapter,
    modal,
    win,
    host,
    title,
    close,
    ref,
    effects,
    others,
    bs,
    busy: value => {
      occupied = value;
    },
    stale: () => {
      current = false;
    }
  };
}

test('eligibility requires canonical music evidence, never names or a fabricated plan', () => {
  assert.equal(
    musicSetupOffer({
      id: 'a',
      folder: 'REAPER Songs.rpp',
      plan: { destination: { name: 'Music Home' } }
    }),
    false
  );
  assert.equal(musicSetupOffer({ id: 'a', blueprint: 'reaper-song' }), true);
  assert.equal(
    musicSetupOffer({ id: 'a', blueprint: 'reaper-song', needs_candidate: true }),
    false
  );
  assert.equal(
    musicSetupOffer({
      id: 'a',
      portfolio: { provider_key: 'music_project_management', integration_key: 'ori_reaper' }
    }),
    true
  );
  assert.equal(musicSetupOffer({ id: 'a', blueprint: 'writing' }), false);
});

test('source shell fences ordinary lifecycle, duplicate entry, Close and focus return', async () => {
  const f = fixture();
  assert.equal(f.adapter.open(f.ref), true);
  assert.equal(f.adapter.open(f.ref), true);
  assert.equal(f.effects.shows, 1);
  assert.equal(f.title.textContent, 'Review music folder setup');
  assert.equal(f.close.label, 'Close setup review');
  assert.equal(f.host.hidden, false);
  assert.equal(f.effects.resets, 0);
  await f.adapter.close();
  assert.equal(f.effects.resets, 0);
  assert.equal(f.effects.closes, 1);
  assert.equal(f.effects.returned, 1);
  assert.equal(f.title.textContent, 'Create Workspace');
  assert.equal(f.host.hidden, true);
  assert.equal(f.modal.dataset.sourceSetupOwner, undefined);
  f.bs.show();
  assert.equal(f.effects.resets, 2, 'ordinary creation resumes its own lifecycle');
});

test('competing creator, sibling dialog and obsolete reference refuse without taking ownership', () => {
  const f = fixture();
  f.busy(true);
  assert.equal(f.adapter.open(f.ref), false);
  f.busy(false);
  f.others.push({});
  assert.equal(f.adapter.open(f.ref), false);
  f.others.length = 0;
  f.stale();
  assert.equal(f.adapter.open(f.ref), false);
  assert.equal(f.effects.shows, 0);
  assert.equal(f.effects.resets, 0);
  assert.equal(f.modal.dataset.sourceSetupOwner, undefined);
});

test('Escape during show waits for hidden and cannot leave a hidden focus trap or reset the creator', async () => {
  const f = fixture({ transitioning: true });
  f.adapter.open(f.ref);
  assert.equal(f.win.fire('keydown', { key: 'Escape' }).prevented, true);
  assert.equal(f.adapter.visible(), true);
  f.bs._isTransitioning = false;
  f.modal.fire('shown.bs.modal');
  assert.equal(f.adapter.visible(), false);
  assert.equal(f.effects.focus, 0);
  assert.equal(f.effects.closes, 1);
  assert.equal(f.effects.resets, 0);
});

test('suspension releases only presentation, does not close/return focus, and rechecks ownership', async () => {
  const f = fixture();
  f.adapter.open(f.ref);
  assert.equal(await f.adapter.suspend(), true);
  assert.equal(f.effects.closes, 0);
  assert.equal(f.effects.returned, 0);
  f.busy(true);
  assert.equal(f.adapter.open(f.ref), false);
  f.busy(false);
  assert.equal(f.adapter.open(f.ref), true);
  f.stale();
  await f.adapter.invalidate();
  assert.equal(f.effects.returned, 0);
  assert.equal(f.effects.closes, 0);
});
