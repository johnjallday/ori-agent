import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./toast.js', import.meta.url), 'utf8');

function node(className = '') {
  const parts = new Map();
  const classes = new Set();
  const attributes = new Map();
  return {
    className,
    children: [],
    style: {},
    parentNode: null,
    textContent: '',
    _html: '',
    classList: { add: name => classes.add(name) },
    get firstChild() {
      return this.children[0];
    },
    get innerHTML() {
      return this.textContent
        ? this.textContent.replace(/&/g, '&amp;').replace(/</g, '&lt;')
        : this._html;
    },
    set innerHTML(value) {
      this._html = value;
    },
    setAttribute: (key, value) => attributes.set(key, value),
    getAttribute: key => attributes.get(key),
    appendChild(child) {
      this.children.push(child);
      child.parentNode = this;
    },
    insertBefore(child, before) {
      const index = this.children.indexOf(before);
      this.children.splice(index < 0 ? this.children.length : index, 0, child);
      child.parentNode = this;
    },
    contains(target) {
      return (
        target === this ||
        [...this.children, ...parts.values()].some(child => child.contains(target))
      );
    },
    closest(selector) {
      const classes = this.className.split(' ');
      if (selector === '.modal.show' && classes.includes('modal') && classes.includes('show'))
        return this;
      return this.parentNode?.closest(selector) || null;
    },
    remove() {
      if (!this.parentNode) return;
      this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1);
      this.parentNode = null;
    },
    querySelector(selector) {
      if (selector === '.toast-container--modal' || selector === '.modal-body') {
        return (
          this.children.find(child => child.className.split(' ').includes(selector.slice(1))) ||
          null
        );
      }
      if (!parts.has(selector)) parts.set(selector, node(selector.slice(1)));
      return parts.get(selector);
    },
    addEventListener() {}
  };
}

function load() {
  const body = node();
  const callbacks = [];
  const document = {
    body,
    activeModal: null,
    createElement: () => node(),
    getElementById: id => body.children.find(child => child.id === id),
    addEventListener() {},
    querySelector() {
      return this.activeModal;
    },
    querySelectorAll() {
      const all = [];
      const visit = parent => {
        for (const child of parent.children) {
          if (child.className.split(' ').includes('toast')) all.push(child);
          visit(child);
        }
      };
      visit(body);
      return all;
    }
  };
  const context = vm.createContext({
    document,
    window: {},
    requestAnimationFrame: callback => callback(),
    setTimeout: callback => callbacks.push(callback),
    clearTimeout() {}
  });
  vm.runInContext(source, context, { filename: 'toast.js' });
  return { Toast: context.window.Toast, document, callbacks };
}

test('new modal feedback is in-flow while earlier page feedback stays in its own host', () => {
  const { Toast, document, callbacks } = load();
  const earlier = Toast.warning('Installed, but not enabled');
  const globalHost = earlier.parentNode;
  assert.equal(globalHost.style.zIndex, undefined, 'CSS, not an inline maximum, owns layering');
  const modal = node('modal-content');
  modal.appendChild(node('modal-header'));
  const body = node('modal-body');
  modal.appendChild(body);
  modal.appendChild(node('modal-footer'));
  document.body.appendChild(modal);
  document.activeModal = modal;
  const feedback = Toast.error('A fictional save failure');
  assert.equal(feedback.getAttribute('role'), 'alert');
  assert.equal(earlier.parentNode, globalHost);
  assert.equal(feedback.parentNode.parentNode, modal);
  assert.equal(modal.children.indexOf(feedback.parentNode), modal.children.indexOf(body) - 1);
  Toast.dismissAll();
  while (callbacks.length) callbacks.shift()();
  assert.equal(earlier.parentNode, null);
  assert.equal(feedback.parentNode, null);
});

test('a full feedback queue removes the oldest synchronously and stays bounded', () => {
  const { Toast } = load();
  const oldest = Toast.info('First');
  let newest;
  for (let i = 0; i < 6; i += 1) newest = Toast.info(`Message ${i}`);
  assert.equal(oldest.parentNode, null);
  assert.equal(newest.parentNode.children.length, 5);
});

test('focused modal feedback restores the dialog, without stealing focus or reopening a closed modal', () => {
  const { Toast, document, callbacks } = load();
  const modal = node('modal show');
  const content = node('modal-content');
  modal.appendChild(content);
  document.body.appendChild(modal);
  document.activeModal = content;
  let restores = 0;
  modal.focus = () => {
    restores += 1;
    document.activeElement = modal;
  };
  const focused = Toast.error('Error');
  document.activeElement = focused.querySelector('.toast-close');
  Toast.dismiss(focused);
  while (callbacks.length) callbacks.shift()();
  assert.equal(restores, 1);
  assert.equal(document.activeElement, modal);

  Toast.info('Background notification');
  while (callbacks.length) callbacks.shift()();
  assert.equal(restores, 1);

  const closed = Toast.error('Closing');
  document.activeElement = closed.querySelector('.toast-close');
  modal.className = 'modal';
  while (callbacks.length) callbacks.shift()();
  assert.equal(restores, 1);
});
