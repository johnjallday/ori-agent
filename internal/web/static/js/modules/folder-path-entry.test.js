import test from 'node:test';
import assert from 'node:assert/strict';
import {
  FOLDER_PATH_BUTTON,
  FOLDER_PATH_ENDPOINT,
  FOLDER_PATH_LABEL,
  createFolderPathEntry,
  folderDialogOffNote,
  openFolderPathDialog,
  selectTypedFolder
} from './folder-path-entry.js';

// Just enough DOM for the control: elements that hold children, attributes and
// listeners, and a document that remembers what has focus.
function fakeDocument() {
  const doc = { activeElement: null };
  doc.createElement = tag => {
    const listeners = {};
    const classes = new Set();
    const element = {
      tagName: String(tag).toUpperCase(),
      children: [],
      attributes: {},
      parentNode: null,
      className: '',
      textContent: '',
      value: '',
      hidden: false,
      disabled: false,
      open: false,
      classList: {
        toggle: (name, on) => (on ? classes.add(name) : classes.delete(name)),
        contains: name => classes.has(name)
      },
      setAttribute: (name, value) => {
        element.attributes[name] = String(value);
      },
      append: (...nodes) => {
        for (const child of nodes) {
          child.parentNode = element;
          element.children.push(child);
        }
      },
      remove: () => {
        const parent = element.parentNode;
        if (parent) parent.children = parent.children.filter(child => child !== element);
        element.parentNode = null;
      },
      addEventListener: (type, listener) => {
        (listeners[type] ||= []).push(listener);
      },
      // Runs the listeners and waits for the async ones.
      fire: (type, event = {}) => {
        const sent = { preventDefault() {}, stopPropagation() {}, ...event };
        return Promise.all((listeners[type] || []).map(listener => listener(sent)));
      },
      focus: () => {
        doc.activeElement = element;
      },
      showModal: () => {
        element.open = true;
      },
      close: () => {
        element.open = false;
      }
    };
    return element;
  };
  doc.body = doc.createElement('body');
  return doc;
}

const find = (root, className) => {
  if (String(root.className).split(' ').includes(className)) return root;
  for (const child of root.children) {
    const found = find(child, className);
    if (found) return found;
  }
  return null;
};

const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};

const answer = (status, body) => ({ ok: status < 400, status, json: async () => body });

test('a typed path is posted to the picker endpoint and comes back as its selection', async () => {
  const calls = [];
  const selection = await selectTypedFolder('  ~/Music/REAPER Projects ', {
    workspaceId: 'ws-1',
    title: 'Link a folder',
    fetchImpl: async (url, options) => {
      calls.push({ url, method: options.method, body: JSON.parse(options.body) });
      return answer(200, {
        success: true,
        selected: true,
        path: '/Users/riley/Music/REAPER Projects',
        selection_token: 'tok-1',
        warning: 'outside_home',
        warning_note: 'This folder is outside your home folder.'
      });
    }
  });
  assert.deepEqual(calls, [
    {
      url: FOLDER_PATH_ENDPOINT,
      method: 'POST',
      body: { path: '~/Music/REAPER Projects', workspace_id: 'ws-1', title: 'Link a folder' }
    }
  ]);
  // The warning's sentence is kept; its code is not.
  assert.deepEqual(selection, {
    path: '/Users/riley/Music/REAPER Projects',
    selectionToken: 'tok-1',
    warningNote: 'This folder is outside your home folder.'
  });

  // No workspace and no title: neither is sent.
  await selectTypedFolder('/Volumes/Sessions', {
    fetchImpl: async (url, options) => {
      assert.deepEqual(JSON.parse(options.body), { path: '/Volumes/Sessions' });
      return answer(200, { success: true, selected: true, path: '/Volumes/Sessions' });
    }
  });
});

test('a refused typed path rejects with the server sentence', async () => {
  const refuse = (status, body) =>
    selectTypedFolder('/x', { fetchImpl: async () => answer(status, body) });
  await assert.rejects(refuse(422, { success: false, error: 'That is a file, not a folder.' }), {
    message: 'That is a file, not a folder.'
  });
  // An answer that selected nothing is a refusal even with a 200.
  await assert.rejects(refuse(200, { success: true, selected: false }), {
    message: 'That folder could not be used. Check the path and try again.'
  });
  await assert.rejects(refuse(200, { success: false, reason: 'dialog_off', error: 'Off.' }), {
    message: 'Off.'
  });
  await assert.rejects(
    selectTypedFolder('/x', {
      fetchImpl: async () => {
        throw new Error('socket hang up');
      }
    }),
    { message: 'That folder could not be used. Check the path and try again.' }
  );
  let asked = false;
  await assert.rejects(
    selectTypedFolder('   ', {
      fetchImpl: async () => {
        asked = true;
      }
    }),
    { message: 'Type or paste a folder path first.' }
  );
  assert.equal(asked, false);
});

test('a dialog-off answer becomes its sentence and never its code', () => {
  const note = 'The folder dialog is switched off in this session. Paste a folder path instead.';
  assert.equal(folderDialogOffNote({ success: false, reason: 'dialog_off', note }), note);
  assert.equal(
    folderDialogOffNote({ reason: 'dialog_off' }),
    'The folder dialog is switched off in this session.'
  );
  for (const other of [null, {}, { success: true }, { reason: 'not_found', note: 'x' }]) {
    assert.equal(folderDialogOffNote(other), '');
  }
  assert.doesNotMatch(folderDialogOffNote({ reason: 'dialog_off', note }), /dialog_off/);
});

function entryFixture(options = {}) {
  const doc = fakeDocument();
  const selected = [];
  const entry = createFolderPathEntry({
    doc,
    onSelect: selection => {
      selected.push(selection);
    },
    ...options
  });
  return {
    doc,
    entry,
    selected,
    input: entry.input,
    use: find(entry.element, 'folder-path-entry__use'),
    label: find(entry.element, 'folder-path-entry__label'),
    note: find(entry.element, 'folder-path-entry__note'),
    status: find(entry.element, 'folder-path-entry__status')
  };
}

test('the control is a labelled field, a button, and two quiet lines', () => {
  const { input, use, label, note, status } = entryFixture();
  assert.equal(label.textContent, FOLDER_PATH_LABEL);
  assert.equal(label.attributes.for, input.id);
  assert.equal(use.textContent, FOLDER_PATH_BUTTON);
  assert.equal(use.type, 'button');
  assert.equal(input.attributes.spellcheck, 'false');
  assert.equal(note.hidden, true);
  assert.equal(status.hidden, true);
  assert.equal(status.attributes.role, 'status');
});

test('Use this path hands the accepted selection on and clears the field', async () => {
  const asked = [];
  const { input, use, status, selected } = entryFixture({
    workspaceId: 'ws-1',
    title: 'Link a folder',
    select: async (path, context) => {
      asked.push({ path, workspaceId: context.workspaceId, title: context.title });
      return { path: '/Users/riley/Music', selectionToken: 'tok-1', warningNote: '' };
    }
  });
  input.value = '~/Music';
  await use.fire('click');
  assert.deepEqual(asked, [{ path: '~/Music', workspaceId: 'ws-1', title: 'Link a folder' }]);
  assert.deepEqual(selected, [
    { path: '/Users/riley/Music', selectionToken: 'tok-1', warningNote: '' }
  ]);
  assert.equal(input.value, '');
  assert.equal(status.hidden, true);
  assert.equal(use.disabled, false);
});

test('an accepted folder outside the home folder leaves its warning on screen', async () => {
  const { input, use, status } = entryFixture({
    select: async () => ({
      path: '/Volumes/Sessions',
      selectionToken: 'tok-2',
      warningNote: 'This folder is outside your home folder.'
    })
  });
  input.value = '/Volumes/Sessions';
  await use.fire('click');
  assert.equal(status.textContent, 'This folder is outside your home folder.');
  assert.equal(status.hidden, false);
  assert.equal(status.classList.contains('is-error'), false);
});

test('a refusal is shown, keeps what was typed, and returns to the field', async () => {
  const { doc, input, use, status, selected } = entryFixture({
    select: async () => {
      throw new Error('That folder no longer exists. Choose a folder that is on this computer.');
    }
  });
  input.value = '~/Music/Gone';
  await use.fire('click');
  assert.equal(
    status.textContent,
    'That folder no longer exists. Choose a folder that is on this computer.'
  );
  assert.equal(status.classList.contains('is-error'), true);
  assert.equal(input.value, '~/Music/Gone');
  assert.deepEqual(selected, []);
  assert.equal(use.disabled, false);
  assert.equal(doc.activeElement, input);
});

test('a surface that cannot use the folder reports it through the same line', async () => {
  const { input, use, status } = entryFixture({
    select: async () => ({ path: '/Users/riley/Music', selectionToken: 'tok', warningNote: '' }),
    onSelect: async () => {
      throw new Error('That folder selection expired. Paste the path again.');
    }
  });
  input.value = '~/Music';
  await use.fire('click');
  assert.equal(status.textContent, 'That folder selection expired. Paste the path again.');
  assert.equal(input.value, '~/Music');
});

test('an empty field asks for a path without calling the server', async () => {
  let asked = 0;
  const { input, use, status } = entryFixture({
    select: async () => {
      asked++;
    }
  });
  input.value = '   ';
  await use.fire('click');
  assert.equal(asked, 0);
  assert.equal(status.textContent, 'Type or paste a folder path first.');
});

test('Enter submits the field and stays out of the form around it', async () => {
  const { input, selected } = entryFixture({
    select: async path => ({ path, selectionToken: 'tok', warningNote: '' })
  });
  input.value = '/Users/riley/Music';
  const event = { key: 'Enter', prevented: false, stopped: false };
  event.preventDefault = () => {
    event.prevented = true;
  };
  event.stopPropagation = () => {
    event.stopped = true;
  };
  await input.fire('keydown', event);
  // The submit runs after the keydown returns; let it settle.
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(event.prevented && event.stopped, true);
  assert.equal(selected.length, 1);

  await input.fire('keydown', { key: 'a' });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(selected.length, 1);
});

test('the field is locked while a path is being checked, and one click is one request', async () => {
  const pending = deferred();
  let asked = 0;
  const { input, use } = entryFixture({
    select: () => {
      asked++;
      return pending.promise;
    }
  });
  input.value = '~/Music';
  const first = use.fire('click');
  assert.equal(use.disabled, true);
  assert.equal(input.disabled, true);
  assert.equal(use.textContent, 'Checking…');
  await use.fire('click');
  assert.equal(asked, 1);
  pending.resolve({ path: '/Users/riley/Music', selectionToken: 'tok', warningNote: '' });
  await first;
  assert.equal(use.disabled, false);
  assert.equal(use.textContent, FOLDER_PATH_BUTTON);
});

test('the note says why the dialog is off, and the surface can disable the control', async () => {
  const { entry, note, use, input } = entryFixture({ note: 'The folder dialog is switched off.' });
  assert.equal(note.textContent, 'The folder dialog is switched off.');
  assert.equal(note.hidden, false);
  entry.setNote('');
  assert.equal(note.hidden, true);

  entry.setDisabled(true);
  assert.equal(use.disabled && input.disabled, true);
  entry.setDisabled(false);
  assert.equal(use.disabled || input.disabled, false);

  input.value = 'left over';
  entry.reset();
  assert.equal(input.value, '');
});

function dialogFixture(options = {}) {
  const doc = fakeDocument();
  const opener = doc.createElement('button');
  doc.body.append(opener);
  opener.focus();
  const linked = [];
  const view = openFolderPathDialog({
    doc,
    heading: 'Link a folder',
    workspaceId: 'ws-1',
    select: async path => ({ path, selectionToken: 'tok', warningNote: '' }),
    onSelect: selection => {
      linked.push(selection);
    },
    ...options
  });
  return {
    doc,
    opener,
    linked,
    view,
    choose: find(view.dialog, 'folder-path-dialog__choose'),
    use: find(view.dialog, 'folder-path-entry__use'),
    note: find(view.dialog, 'folder-path-entry__note')
  };
}

test('the folder dialog offers Choose folder and the typed path side by side', () => {
  const { doc, view, choose } = dialogFixture({ onChoose: async () => null });
  assert.equal(view.dialog.open, true);
  assert.equal(view.dialog.parentNode, doc.body);
  assert.equal(find(view.dialog, 'folder-path-dialog__title').textContent, 'Link a folder');
  assert.equal(choose.textContent, 'Choose folder…');
  assert.ok(find(view.dialog, 'folder-path-entry'));
  assert.equal(doc.activeElement, choose);
});

test('a switched-off dialog turns Choose folder into the note and keeps the field', async () => {
  const note = 'The folder dialog is switched off in this session. Paste a folder path instead.';
  const fixture = dialogFixture({
    onChoose: async () => ({ success: false, launched: false, reason: 'dialog_off', note })
  });
  await fixture.choose.fire('click');
  assert.equal(fixture.view.dialog.open, true);
  assert.equal(fixture.choose.hidden, true);
  assert.equal(fixture.note.textContent, note);
  assert.equal(fixture.doc.activeElement, fixture.view.entry.input);
});

test('a launched picker closes the dialog; a failed launch leaves it open', async () => {
  const launched = dialogFixture({ onChoose: async () => ({ success: true }) });
  await launched.choose.fire('click');
  assert.equal(launched.view.dialog.open, false);
  assert.equal(launched.view.dialog.parentNode, null);
  assert.equal(launched.doc.activeElement, launched.opener);

  const failed = dialogFixture({ onChoose: async () => null });
  await failed.choose.fire('click');
  assert.equal(failed.view.dialog.open, true);
  assert.equal(failed.choose.disabled, false);
});

test('a typed path links and closes; a link that fails keeps the dialog and says why', async () => {
  const linked = dialogFixture();
  linked.view.entry.input.value = '~/Music/REAPER Projects';
  await linked.use.fire('click');
  assert.deepEqual(linked.linked, [
    { path: '~/Music/REAPER Projects', selectionToken: 'tok', warningNote: '' }
  ]);
  assert.equal(linked.view.dialog.open, false);
  // With no native choice to offer, the field takes focus on open.
  assert.equal(dialogFixture().doc.activeElement.tagName, 'INPUT');

  const failing = dialogFixture({
    onSelect: async () => {
      throw new Error('That folder could not be linked.');
    }
  });
  failing.view.entry.input.value = '~/Music';
  await failing.use.fire('click');
  assert.equal(failing.view.dialog.open, true);
  assert.equal(
    find(failing.view.dialog, 'folder-path-entry__status').textContent,
    'That folder could not be linked.'
  );
});

test('Cancel and Escape close the dialog and give focus back', async () => {
  const cancelled = dialogFixture();
  const cancel = cancelled.view.dialog.children.at(-1).children[0];
  assert.equal(cancel.textContent, 'Cancel');
  await cancel.fire('click');
  assert.equal(cancelled.view.dialog.open, false);
  assert.equal(cancelled.doc.activeElement, cancelled.opener);

  const escaped = dialogFixture();
  await escaped.view.dialog.fire('cancel');
  assert.equal(escaped.view.dialog.parentNode, null);

  assert.equal(openFolderPathDialog({ doc: null }), null);
});
