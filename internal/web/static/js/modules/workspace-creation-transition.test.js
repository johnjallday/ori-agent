import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createCreationStatusSurface,
  createCreationTransition
} from './workspace-creation-transition.js';

function fakePanel(open = true) {
  const calls = [];
  return {
    calls,
    isOpen: () => open,
    suspend() {
      calls.push('suspend');
      if (!open) return false;
      open = false;
      return true;
    },
    resume(options) {
      calls.push(`resume:${options.reveal ? 'reveal' : 'closed'}`);
      if (options.reveal) open = true;
      return true;
    }
  };
}

const enter = (t, owner = 'source', token = 'a') => t.enter({ owner, generation: 1, token });

test('entering suspends an open drawer and Back restores that same drawer once', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  const handle = enter(t);
  assert.equal(panel.isOpen(), false);
  assert.equal(t.phase(), 'placing');
  assert.equal(t.returnToReview(handle), true);
  assert.equal(panel.isOpen(), true);
  assert.equal(t.returnToReview(handle), false, 'a repeated Back is inert');
  assert.deepEqual(panel.calls, ['suspend', 'resume:reveal']);
});

test('a drawer the user had closed is not opened by returning', () => {
  const panel = fakePanel(false);
  const t = createCreationTransition({ panel });
  t.returnToReview(enter(t));
  assert.equal(panel.isOpen(), false);
  assert.deepEqual(panel.calls, ['suspend']);
});

test('a newer owner supersedes an older one; the stale callback changes nothing', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  const older = enter(t, 'source', 'a');
  const newer = enter(t, 'ordinary', 'b');
  assert.equal(t.isCurrent(older), false);
  assert.equal(t.returnToReview(older), false);
  assert.equal(t.accept(older), false);
  assert.equal(t.release(older, { reveal: true }), false);
  assert.equal(panel.isOpen(), false, 'stale owners cannot bring the drawer back');
  assert.equal(t.owner(), 'ordinary');
  assert.equal(t.returnToReview(newer), true);
  assert.equal(panel.isOpen(), true, 'the suspended drawer carried over to the newer owner');
  assert.deepEqual(panel.calls, ['suspend', 'resume:reveal']);
});

test('repeated entry after Back keeps one suspension per entry', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  t.returnToReview(enter(t, 'source', 'a'));
  const again = enter(t, 'source', 'a');
  assert.equal(panel.isOpen(), false);
  assert.equal(t.isCurrent(again), true);
  assert.deepEqual(panel.calls, ['suspend', 'resume:reveal', 'suspend']);
});

test('a refusal during dispatch can return; accepted work never can', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  const refused = enter(t);
  assert.equal(t.dispatch(refused), true);
  assert.equal(t.dispatch(refused), false, 'one final gesture');
  assert.equal(t.returnToReview(refused), true);

  const accepted = enter(t);
  t.dispatch(accepted);
  assert.equal(t.accept(accepted), true);
  assert.equal(t.returnToReview(accepted), false);
  assert.equal(panel.isOpen(), false, 'accepted work keeps the map clear');
  assert.equal(t.settle(accepted), true);
  assert.equal(t.settle(accepted), false);
});

test('release closes a suspended drawer quietly unless reveal is asked for', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  const quiet = enter(t);
  t.accept(quiet);
  assert.equal(t.release(quiet), true);
  assert.equal(panel.isOpen(), false);
  assert.equal(panel.calls.at(-1), 'resume:closed');

  const other = createCreationTransition({ panel: fakePanel() });
  const abandoned = enter(other);
  other.release(abandoned, { reveal: true });
  assert.equal(other.blocksReveal(), false);
});

test('unsolicited reveals are blocked for the whole lifetime', () => {
  const t = createCreationTransition({ panel: fakePanel(false) });
  assert.equal(t.blocksReveal(), false);
  const handle = enter(t);
  assert.equal(t.blocksReveal(), true);
  t.accept(handle);
  t.settle(handle);
  assert.equal(t.blocksReveal(), true, 'a receipt on the map still owns the view');
  t.release(handle);
  assert.equal(t.blocksReveal(), false);
});

test('an explicit open before acceptance leaves through the owner’s own Back', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  let backs = 0;
  const handle = t.enter({
    owner: 'ordinary',
    generation: 1,
    token: 'a',
    onExplicitReopen: () => {
      backs += 1;
      assert.equal(t.isReopening(), true);
      t.returnToReview(handle);
    }
  });
  t.requestOpen();
  assert.equal(backs, 1);
  assert.equal(panel.isOpen(), true);
  assert.equal(t.blocksReveal(), false);
});

test('an owner with nothing to do cannot lock the user out of the drawer', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  t.enter({ owner: 'ordinary', generation: 1, token: 'a', onExplicitReopen: () => {} });
  t.requestOpen();
  assert.equal(panel.isOpen(), true);
  assert.equal(t.blocksReveal(), false);
});

test('an explicit open after acceptance shows the drawer and leaves the work running', () => {
  const panel = fakePanel();
  const t = createCreationTransition({ panel });
  let backs = 0;
  const handle = t.enter({
    owner: 'source',
    generation: 1,
    token: 'a',
    onExplicitReopen: () => (backs += 1)
  });
  t.accept(handle);
  t.requestOpen();
  assert.equal(backs, 0, 'accepted work is never sent back to its review');
  assert.equal(panel.isOpen(), true);
  assert.equal(t.isCurrent(handle), true);
  assert.equal(t.phase(), 'accepted');
});

test('only the current lifetime can speak, and every ending clears its status', () => {
  const shown = [];
  const status = { show: view => shown.push(view.title), hide: () => shown.push('hidden') };
  const t = createCreationTransition({ panel: fakePanel(false), status });
  const older = enter(t, 'source', 'a');
  assert.equal(t.showStatus(older, { title: 'one' }), true);
  const newer = enter(t, 'ordinary', 'b');
  assert.equal(t.showStatus(older, { title: 'late' }), false);
  assert.deepEqual(shown, ['hidden', 'one', 'hidden']);
  t.accept(newer);
  t.showStatus(newer, { title: 'two' });
  t.release(newer);
  assert.equal(shown.at(-1), 'hidden');
});

test('the status surface renders names as text and never takes focus', () => {
  const made = [];
  const element = tag => {
    const node = {
      tag,
      dataset: {},
      children: [],
      hidden: false,
      classList: { add() {} },
      setAttribute() {},
      addEventListener(type, run) {
        node[type] = run;
      },
      append(...next) {
        node.children.push(...next);
      },
      replaceChildren(...next) {
        node.children = next;
      },
      contains: () => false,
      querySelector(selector) {
        const name = selector.slice(1);
        const walk = list =>
          list.reduce(
            (found, child) => found || (child.className === name ? child : walk(child.children)),
            null
          );
        return walk(node.children);
      }
    };
    made.push(node);
    return node;
  };
  const doc = { createElement: element, body: element('body'), activeElement: null };
  const surface = createCreationStatusSurface(doc);
  let ran = 0;
  const hostile = '<img src=x onerror=boom()>';
  surface.show({
    tone: 'warning',
    title: hostile,
    detail: '',
    actions: [{ label: 'Open', primary: true, run: () => (ran += 1) }]
  });
  const root = surface.element();
  assert.equal(root.dataset.tone, 'warning');
  assert.equal(root.querySelector('.creation-map-status__title').textContent, hostile);
  assert.equal(root.querySelector('.creation-map-status__detail').hidden, true);
  const [button] = root.querySelector('.creation-map-status__actions').children;
  button.click();
  assert.equal(ran, 1);
  surface.hide();
  assert.equal(root.hidden, true);
  assert.ok(!made.some(node => 'innerHTML' in node));
});

test('entry needs an owner and a token, and works with no drawer on the page', () => {
  const t = createCreationTransition({ panel: () => null });
  assert.equal(t.enter({ owner: 'source' }), null);
  assert.equal(t.enter({ token: 'a' }), null);
  const handle = enter(t);
  assert.equal(t.returnToReview(handle), true);
});
