(function() {
  'use strict';

  const ACTIVATION_KEY = 'f';
  const HINT_ALPHABET = 'ASDFGHJKLQWERTYUIOPZXCVBNM';
  const ACTIONABLE_SELECTOR = [
    'a[href]',
    'button:not([disabled])',
    'input:not([type="hidden"]):not([disabled])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    'summary',
    '[role="button"]',
    '[role="link"]',
    '[role="menuitem"]',
    '[role="tab"]',
    '[role="checkbox"]',
    '[role="radio"]',
    '[role="switch"]',
    '[onclick]',
    '[data-workspace-id]',
    '[data-group-toggle]',
    '.workspace-detail-panel.is-expandable'
  ].join(', ');
  const ZONE_DEFINITIONS = [
    {
      id: 'modal',
      prefix: 'M',
      label: 'modal',
      matches: (element) => element.closest('.modal.show')
    },
    {
      id: 'chat',
      prefix: 'H',
      label: 'chat',
      matches: (element) => element.closest('#chatPanel[aria-hidden="false"]')
    },
    {
      id: 'panel',
      prefix: 'P',
      label: 'panel',
      matches: (element) => element.closest('.workspace-detail-panel.is-expanded')
    },
    {
      id: 'navbar',
      prefix: 'N',
      label: 'navbar',
      matches: (element) => element.closest('.navbar')
    },
    {
      id: 'sidebar',
      prefix: 'S',
      label: 'sidebar',
      matches: (element) => element.closest('#sidebar')
    },
    {
      id: 'content',
      prefix: 'C',
      label: 'content',
      matches: (element) => element.closest('main, .main-content, #workspace-detail-view')
    },
    {
      id: 'page',
      prefix: 'X',
      label: 'page',
      matches: () => true
    }
  ];

  function isEditableTarget(target) {
    if (!target) return false;
    const tagName = target.tagName;
    return Boolean(
      target.isContentEditable ||
      tagName === 'INPUT' ||
      tagName === 'TEXTAREA' ||
      tagName === 'SELECT'
    );
  }

  function isVisible(element) {
    if (!element || !document.contains(element)) {
      return false;
    }

    if (element.closest('[hidden], .d-none, [aria-hidden="true"], [inert]')) {
      return false;
    }

    const style = window.getComputedStyle(element);
    if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') {
      return false;
    }

    if (Number.parseFloat(style.opacity || '1') <= 0.02) {
      return false;
    }

    const rect = element.getBoundingClientRect();
    if (rect.width < 4 || rect.height < 4) {
      return false;
    }

    if (rect.bottom < 0 || rect.right < 0 || rect.top > window.innerHeight || rect.left > window.innerWidth) {
      return false;
    }

    return element.getClientRects().length > 0;
  }

  function computeCodeLength(count) {
    if (count <= 1) {
      return 1;
    }

    let length = 1;
    let capacity = HINT_ALPHABET.length;
    while (capacity < count) {
      length += 1;
      capacity *= HINT_ALPHABET.length;
    }

    return length;
  }

  function encodeHint(index, length) {
    const base = HINT_ALPHABET.length;
    let remaining = index;
    let code = '';

    for (let i = 0; i < length; i += 1) {
      code = HINT_ALPHABET[remaining % base] + code;
      remaining = Math.floor(remaining / base);
    }

    return code;
  }

  function clamp(value, min, max) {
    return Math.min(Math.max(value, min), max);
  }

  function compareByVisualOrder(leftElement, rightElement) {
    const leftRect = leftElement.getBoundingClientRect();
    const rightRect = rightElement.getBoundingClientRect();
    const rowThreshold = 12;

    if (Math.abs(leftRect.top - rightRect.top) > rowThreshold) {
      return leftRect.top - rightRect.top;
    }

    if (Math.abs(leftRect.left - rightRect.left) > 1) {
      return leftRect.left - rightRect.left;
    }

    return leftRect.width * leftRect.height - rightRect.width * rightRect.height;
  }

  class KeyboardNavigation {
    constructor() {
      this.active = false;
      this.buffer = '';
      this.hints = [];
      this.overlay = null;
      this.hintLayer = null;
      this.status = null;
      this.refreshFrame = null;
      this.invalidFrame = null;
      this.scopeRoot = null;
      this.zoneLegend = '';
      this.observer = null;
      this.onKeydown = this.onKeydown.bind(this);
      this.onRefreshRequested = this.onRefreshRequested.bind(this);
    }

    init() {
      document.addEventListener('keydown', this.onKeydown, true);
    }

    onKeydown(event) {
      if (event.isComposing || event.defaultPrevented) {
        return;
      }

      if (this.active) {
        this.handleActiveKeydown(event);
        return;
      }

      if (event.repeat || event.metaKey || event.ctrlKey || event.altKey) {
        return;
      }

      if (String(event.key || '').toLowerCase() !== ACTIVATION_KEY) {
        return;
      }

      if (isEditableTarget(event.target)) {
        return;
      }

      event.preventDefault();
      event.stopPropagation();
      this.activate();
    }

    handleActiveKeydown(event) {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        this.deactivate();
        return;
      }

      if (event.key === 'Backspace') {
        event.preventDefault();
        event.stopPropagation();
        this.buffer = this.buffer.slice(0, -1);
        this.updateHintState();
        return;
      }

      if (event.key === 'Enter') {
        const matches = this.getMatches();
        if (matches.length === 1) {
          event.preventDefault();
          event.stopPropagation();
          this.activateHint(matches[0]);
        }
        return;
      }

      if (event.metaKey || event.ctrlKey || event.altKey) {
        return;
      }

      const key = String(event.key || '').toUpperCase();
      if (!/^[A-Z]$/.test(key)) {
        return;
      }

      event.preventDefault();
      event.stopPropagation();

      const nextBuffer = this.buffer + key;
      const matches = this.getMatches(nextBuffer);
      if (!matches.length) {
        this.flashInvalid();
        return;
      }

      this.buffer = nextBuffer;
      this.updateHintState();

      if (matches.length === 1 && matches[0].code === this.buffer) {
        this.activateHint(matches[0]);
      }
    }

    activate() {
      if (this.active) {
        return;
      }

      this.active = true;
      this.buffer = '';
      document.body.classList.add('ori-keyboard-nav-active');
      this.ensureOverlay();
      this.startObservers();
      this.refreshHints();
    }

    deactivate() {
      if (!this.active) {
        return;
      }

      this.active = false;
      this.buffer = '';
      this.stopObservers();
      this.teardownOverlay();
      document.body.classList.remove('ori-keyboard-nav-active');
    }

    ensureOverlay() {
      if (this.overlay) {
        return;
      }

      this.overlay = document.createElement('div');
      this.overlay.className = 'ori-keyboard-nav-overlay';
      this.overlay.setAttribute('aria-hidden', 'true');

      this.status = document.createElement('div');
      this.status.className = 'ori-keyboard-nav-status';

      this.hintLayer = document.createElement('div');
      this.hintLayer.className = 'ori-keyboard-nav-hints';

      this.overlay.appendChild(this.status);
      this.overlay.appendChild(this.hintLayer);
      document.body.appendChild(this.overlay);
    }

    teardownOverlay() {
      if (this.refreshFrame) {
        cancelAnimationFrame(this.refreshFrame);
        this.refreshFrame = null;
      }

      if (this.invalidFrame) {
        cancelAnimationFrame(this.invalidFrame);
        this.invalidFrame = null;
      }

      this.hints = [];
      this.scopeRoot = null;
      this.zoneLegend = '';

      if (this.overlay && this.overlay.parentNode) {
        this.overlay.parentNode.removeChild(this.overlay);
      }

      this.overlay = null;
      this.hintLayer = null;
      this.status = null;
    }

    startObservers() {
      window.addEventListener('resize', this.onRefreshRequested);
      window.addEventListener('scroll', this.onRefreshRequested, true);

      this.observer = new MutationObserver((mutations) => {
        const hasExternalMutation = mutations.some((mutation) => {
          return !(this.overlay && this.overlay.contains(mutation.target));
        });

        if (!hasExternalMutation) {
          return;
        }

        this.onRefreshRequested();
      });

      this.observer.observe(document.body, {
        subtree: true,
        childList: true,
        attributes: true,
        attributeFilter: ['class', 'style', 'hidden', 'aria-hidden', 'aria-expanded', 'disabled', 'aria-disabled']
      });
    }

    stopObservers() {
      window.removeEventListener('resize', this.onRefreshRequested);
      window.removeEventListener('scroll', this.onRefreshRequested, true);

      if (this.observer) {
        this.observer.disconnect();
        this.observer = null;
      }
    }

    onRefreshRequested() {
      if (!this.active || this.refreshFrame) {
        return;
      }

      this.refreshFrame = requestAnimationFrame(() => {
        this.refreshFrame = null;
        this.refreshHints();
      });
    }

    resolveScopeRoot() {
      const scopedRoots = [
        ...document.querySelectorAll('.modal.show'),
        ...document.querySelectorAll('#chatPanel[aria-hidden="false"]'),
        ...document.querySelectorAll('.main-task-modal'),
        ...document.querySelectorAll('.workspace-detail-panel.is-expanded')
      ].filter((element) => isVisible(element));

      if (scopedRoots.length > 0) {
        return scopedRoots[scopedRoots.length - 1];
      }

      return document.body;
    }

    collectCandidates(scopeRoot) {
      const queryRoot = scopeRoot === document.body ? document : scopeRoot;
      const seen = new Set();
      const candidates = [];

      queryRoot.querySelectorAll(ACTIONABLE_SELECTOR).forEach((element) => {
        if (seen.has(element) || !this.isNavigable(element)) {
          return;
        }

        seen.add(element);
        candidates.push(element);
      });

      return candidates;
    }

    resolveZone(element, scopeRoot) {
      if (scopeRoot && scopeRoot !== document.body) {
        const scopedZone = ZONE_DEFINITIONS.find((zone) => {
          const match = zone.matches(element);
          return match && (match === scopeRoot || scopeRoot.contains(match));
        });
        if (scopedZone) {
          return scopedZone;
        }
      }

      return ZONE_DEFINITIONS.find((zone) => zone.matches(element)) || ZONE_DEFINITIONS[ZONE_DEFINITIONS.length - 1];
    }

    groupCandidatesByZone(candidates, scopeRoot) {
      const groups = [];
      const groupsById = new Map();

      candidates.forEach((element) => {
        const zone = this.resolveZone(element, scopeRoot);
        if (!groupsById.has(zone.id)) {
          const group = { zone, items: [] };
          groupsById.set(zone.id, group);
          groups.push(group);
        }

        groupsById.get(zone.id).items.push(element);
      });

      groups.forEach((group) => {
        group.items.sort(compareByVisualOrder);
      });

      groups.sort((leftGroup, rightGroup) => {
        const leftIndex = ZONE_DEFINITIONS.findIndex((zone) => zone.id === leftGroup.zone.id);
        const rightIndex = ZONE_DEFINITIONS.findIndex((zone) => zone.id === rightGroup.zone.id);
        return leftIndex - rightIndex;
      });

      return groups;
    }

    isNavigable(element) {
      if (!element || element.closest('.ori-keyboard-nav-overlay')) {
        return false;
      }

      if (element.matches('[disabled], [aria-disabled="true"]')) {
        return false;
      }

      const role = String(element.getAttribute('role') || '').toLowerCase();
      if (role === 'presentation' || role === 'none' || role === 'region' || role === 'tabpanel') {
        return false;
      }

      if (!isVisible(element)) {
        return false;
      }

      if (element.matches('label[for]') && !element.control && !document.getElementById(element.getAttribute('for'))) {
        return false;
      }

      return true;
    }

    refreshHints() {
      if (!this.active) {
        return;
      }

      this.scopeRoot = this.resolveScopeRoot();
      const candidates = this.collectCandidates(this.scopeRoot);
      const groups = this.groupCandidatesByZone(candidates, this.scopeRoot);

      this.hintLayer.innerHTML = '';
      this.zoneLegend = groups.map((group) => `${group.zone.prefix}:${group.zone.label}`).join('  ');
      this.hints = [];

      groups.forEach((group) => {
        const codeLength = computeCodeLength(group.items.length);

        group.items.forEach((element, index) => {
          const badge = document.createElement('div');
          badge.className = 'ori-keyboard-nav-hint';
          badge.dataset.zone = group.zone.id;
          badge.textContent = `${group.zone.prefix}${encodeHint(index, codeLength)}`;

          this.hints.push({
            badge,
            code: badge.textContent,
            element,
            zone: group.zone
          });
        });
      });

      this.hints.forEach((hint) => {
        this.positionHint(hint);
        this.hintLayer.appendChild(hint.badge);
      });

      this.updateHintState();
    }

    positionHint(hint) {
      const rect = hint.element.getBoundingClientRect();
      const maxLeft = Math.max(12, window.innerWidth - 64);
      const maxTop = Math.max(12, window.innerHeight - 28);
      const top = clamp(rect.top + 6, 12, maxTop);
      const left = clamp(rect.left + 6, 12, maxLeft);

      hint.badge.style.top = `${top}px`;
      hint.badge.style.left = `${left}px`;
    }

    getMatches(buffer = this.buffer) {
      if (!buffer) {
        return this.hints.slice();
      }

      return this.hints.filter((hint) => hint.code.startsWith(buffer));
    }

    updateHintState() {
      const matches = this.getMatches();

      this.hints.forEach((hint) => {
        const isMatch = !this.buffer || hint.code.startsWith(this.buffer);
        const isExact = Boolean(this.buffer) && hint.code === this.buffer;

        hint.badge.hidden = !isMatch;
        hint.badge.dataset.state = isExact ? 'exact' : (this.buffer ? 'match' : 'idle');
      });

      if (!this.status) {
        return;
      }

      if (!this.hints.length) {
        this.status.innerHTML = [
          '<span class="ori-keyboard-nav-status-title">Navigation Mode</span>',
          '<span class="ori-keyboard-nav-status-text">No visible targets on this surface.</span>',
          '<span class="ori-keyboard-nav-status-meta">Esc to exit</span>'
        ].join('');
        return;
      }

      const bufferLabel = this.buffer || '-';
      const matchLabel = matches.length === 1 ? '1 match' : `${matches.length} matches`;
      const zoneLegend = this.zoneLegend ? ` - Zones ${this.zoneLegend}` : '';
      this.status.innerHTML = [
        '<span class="ori-keyboard-nav-status-title">Navigation Mode</span>',
        `<span class="ori-keyboard-nav-status-text">Type hint: <strong>${bufferLabel}</strong></span>`,
        `<span class="ori-keyboard-nav-status-meta">${matchLabel}${zoneLegend} - Backspace edits - Esc exits</span>`
      ].join('');
    }

    flashInvalid() {
      if (!this.status) {
        return;
      }

      this.status.classList.remove('is-invalid');
      void this.status.offsetWidth;
      this.status.classList.add('is-invalid');

      if (this.invalidFrame) {
        cancelAnimationFrame(this.invalidFrame);
      }

      this.invalidFrame = requestAnimationFrame(() => {
        this.invalidFrame = requestAnimationFrame(() => {
          if (this.status) {
            this.status.classList.remove('is-invalid');
          }
          this.invalidFrame = null;
        });
      });
    }

    activateHint(hint) {
      const target = hint && hint.element;
      this.deactivate();

      if (!target || !document.contains(target)) {
        return;
      }

      requestAnimationFrame(() => {
        if (!document.contains(target)) {
          return;
        }

        if (typeof target.focus === 'function') {
          try {
            target.focus({ preventScroll: false });
          } catch (error) {
            target.focus();
          }
        }

        if (target.matches('input:not([type="checkbox"]):not([type="radio"]):not([type="file"]), textarea')) {
          if (typeof target.select === 'function') {
            target.select();
          }
          return;
        }

        if (target.matches('select')) {
          return;
        }

        if (target.tagName === 'LABEL' && target.control) {
          try {
            target.control.focus({ preventScroll: false });
          } catch (error) {
            target.control.focus();
          }
        }

        target.click();
      });
    }
  }

  function initKeyboardNavigation() {
    const keyboardNavigation = new KeyboardNavigation();
    keyboardNavigation.init();
    window.KeyboardNavigation = keyboardNavigation;
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initKeyboardNavigation, { once: true });
  } else {
    initKeyboardNavigation();
  }
})();
