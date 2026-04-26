(function() {
  'use strict';

  const ACTIVATION_KEY = 'f';
  const PERSISTENT_MODE_STORAGE_KEY = 'ori-keyboard-nav-persistent';
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
      this.mode = 'transient';
      this.suspended = false;
      this.buffer = '';
      this.selectedElement = null;
      this.hints = [];
      this.groups = [];
      this.overlay = null;
      this.regionLayer = null;
      this.hintLayer = null;
      this.status = null;
      this.refreshFrame = null;
      this.invalidFrame = null;
      this.scopeRoot = null;
      this.zoneLegend = '';
      this.observer = null;
      this.launcherButton = null;
      this.suspendedEditable = null;
      this.resumeSuspendedEditable = null;
      this.onKeydown = this.onKeydown.bind(this);
      this.onFocusIn = this.onFocusIn.bind(this);
      this.onRefreshRequested = this.onRefreshRequested.bind(this);
    }

    init() {
      document.addEventListener('keydown', this.onKeydown, true);
      this.setupLauncherButton();
      this.syncLauncherButton();

      if (this.loadPersistentPreference()) {
        this.activate('persistent');
      }
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
      this.activate('transient');
    }

    handleActiveKeydown(event) {
      const vimDirectionMap = {
        h: 'ArrowLeft',
        j: 'ArrowDown',
        k: 'ArrowUp',
        l: 'ArrowRight'
      };

      if (this.suspended || isEditableTarget(event.target)) {
        if (this.mode === 'persistent' && isEditableTarget(event.target)) {
          this.suspendForEditableTarget(event.target);
        }

        if (event.key === 'Escape') {
          event.preventDefault();
          event.stopPropagation();
          this.deactivate();
        }
        return;
      }

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

      if (event.key === 'ArrowUp' || event.key === 'ArrowDown' || event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault();
        event.stopPropagation();
        this.moveSelection(event.key);
        return;
      }

      if (!event.shiftKey && vimDirectionMap[event.key]) {
        event.preventDefault();
        event.stopPropagation();
        this.moveSelection(vimDirectionMap[event.key]);
        return;
      }

      if (event.key === 'Enter') {
        const selectedHint = this.getSelectedHint(this.getVisibleMatches());
        if (selectedHint) {
          event.preventDefault();
          event.stopPropagation();
          this.activateHint(selectedHint);
          return;
        }

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

    activate(mode = 'transient') {
      if (this.active) {
        this.mode = mode;
        this.suspended = false;
        this.buffer = '';
        this.selectedElement = null;
        this.detachSuspendedEditable();
        this.persistPreference();
        this.refreshHints();
        this.syncLauncherButton();
        return;
      }

      this.active = true;
      this.mode = mode;
      this.suspended = false;
      this.buffer = '';
      this.selectedElement = null;
      document.body.classList.add('ori-keyboard-nav-active');
      this.ensureOverlay();
      this.startObservers();
      this.persistPreference();
      this.refreshHints();
      if (this.mode === 'persistent' && isEditableTarget(document.activeElement)) {
        this.suspendForEditableTarget(document.activeElement);
      }
      this.syncLauncherButton();
    }

    deactivate() {
      if (!this.active) {
        return;
      }

      this.active = false;
      this.mode = 'transient';
      this.suspended = false;
      this.buffer = '';
      this.selectedElement = null;
      this.detachSuspendedEditable();
      this.stopObservers();
      this.teardownOverlay();
      document.body.classList.remove('ori-keyboard-nav-active');
      this.persistPreference();
      this.syncLauncherButton();
    }

    togglePersistentMode() {
      if (this.active && this.mode === 'persistent') {
        this.deactivate();
        return;
      }

      this.activate('persistent');
    }

    setupLauncherButton() {
      this.launcherButton = document.getElementById('keyboardNavigationToggle');
      if (!this.launcherButton) {
        return;
      }

      this.launcherButton.addEventListener('click', (event) => {
        event.preventDefault();
        this.togglePersistentMode();
      });
    }

    syncLauncherButton() {
      if (!this.launcherButton) {
        return;
      }

      const persistentActive = this.active && this.mode === 'persistent';
      const label = persistentActive ? 'Disable persistent keyboard navigation' : 'Enable persistent keyboard navigation';
      this.launcherButton.setAttribute('aria-pressed', persistentActive ? 'true' : 'false');
      this.launcherButton.setAttribute('aria-label', label);
      this.launcherButton.setAttribute('title', label);
    }

    persistPreference() {
      try {
        if (this.active && this.mode === 'persistent') {
          sessionStorage.setItem(PERSISTENT_MODE_STORAGE_KEY, '1');
        } else {
          sessionStorage.removeItem(PERSISTENT_MODE_STORAGE_KEY);
        }
      } catch (error) {
        // Ignore storage access failures and keep behavior in-memory only.
      }
    }

    loadPersistentPreference() {
      try {
        return sessionStorage.getItem(PERSISTENT_MODE_STORAGE_KEY) === '1';
      } catch (error) {
        return false;
      }
    }

    detachSuspendedEditable() {
      if (this.suspendedEditable && this.resumeSuspendedEditable) {
        this.suspendedEditable.removeEventListener('blur', this.resumeSuspendedEditable);
      }

      this.suspendedEditable = null;
      this.resumeSuspendedEditable = null;
    }

    suspendForEditableTarget(target) {
      if (this.mode !== 'persistent' || !target) {
        return;
      }

      if (this.suspendedEditable === target) {
        return;
      }

      this.detachSuspendedEditable();
      this.suspended = true;
      this.buffer = '';
      this.selectedElement = null;
      this.suspendedEditable = target;
      this.resumeSuspendedEditable = () => {
        this.detachSuspendedEditable();
        this.suspended = false;
        this.refreshHints();
        this.syncLauncherButton();
      };

      target.addEventListener('blur', this.resumeSuspendedEditable, { once: true });
      this.updateHintState();
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

      this.regionLayer = document.createElement('div');
      this.regionLayer.className = 'ori-keyboard-nav-regions';

      this.hintLayer = document.createElement('div');
      this.hintLayer.className = 'ori-keyboard-nav-hints';

      this.overlay.appendChild(this.status);
      this.overlay.appendChild(this.regionLayer);
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
      this.groups = [];
      this.scopeRoot = null;
      this.zoneLegend = '';
      this.selectedElement = null;

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
      document.addEventListener('focusin', this.onFocusIn, true);

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
      document.removeEventListener('focusin', this.onFocusIn, true);

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

    onFocusIn(event) {
      if (!this.active || this.mode !== 'persistent') {
        return;
      }

      if (isEditableTarget(event.target)) {
        this.suspendForEditableTarget(event.target);
      }
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
      this.groups = groups;

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
        this.hintLayer.appendChild(hint.badge);
        this.positionHint(hint);
      });

      if (this.selectedElement && !this.hints.some((hint) => hint.element === this.selectedElement)) {
        this.selectedElement = null;
      }

      this.updateHintState();
    }

    getSelectedZoneGroup() {
      if (!this.buffer) {
        return null;
      }

      const zonePrefix = this.buffer.charAt(0);
      return this.groups.find((group) => group.zone.prefix === zonePrefix) || null;
    }

    buildZoneHighlightBoxes(group) {
      if (!group || !Array.isArray(group.items) || group.items.length === 0) {
        return [];
      }

      const paddingX = 10;
      const paddingY = 8;
      const mergeGapX = 28;
      const mergeGapY = 18;

      const rects = group.items
        .filter((element) => element && document.contains(element) && isVisible(element))
        .map((element) => {
          const rect = element.getBoundingClientRect();
          return {
            top: rect.top - paddingY,
            right: rect.right + paddingX,
            bottom: rect.bottom + paddingY,
            left: rect.left - paddingX
          };
        })
        .sort((leftRect, rightRect) => {
          if (Math.abs(leftRect.top - rightRect.top) > 10) {
            return leftRect.top - rightRect.top;
          }

          return leftRect.left - rightRect.left;
        });

      const boxes = [];

      rects.forEach((rect) => {
        const targetBox = boxes.find((box) => {
          const horizontalGap = Math.max(0, Math.max(box.left - rect.right, rect.left - box.right));
          const verticalGap = Math.max(0, Math.max(box.top - rect.bottom, rect.top - box.bottom));
          return horizontalGap <= mergeGapX && verticalGap <= mergeGapY;
        });

        if (!targetBox) {
          boxes.push({ ...rect });
          return;
        }

        targetBox.top = Math.min(targetBox.top, rect.top);
        targetBox.right = Math.max(targetBox.right, rect.right);
        targetBox.bottom = Math.max(targetBox.bottom, rect.bottom);
        targetBox.left = Math.min(targetBox.left, rect.left);
      });

      return boxes;
    }

    renderZoneSelection(group) {
      if (!this.regionLayer) {
        return;
      }

      this.regionLayer.innerHTML = '';

      if (!group) {
        return;
      }

      const boxes = this.buildZoneHighlightBoxes(group);
      boxes.forEach((rect, index) => {
        const box = document.createElement('div');
        box.className = 'ori-keyboard-nav-region';
        box.dataset.zone = group.zone.id;
        const top = clamp(rect.top, 6, window.innerHeight - 6);
        const left = clamp(rect.left, 6, window.innerWidth - 6);
        const width = Math.max(0, Math.min(rect.right, window.innerWidth - 6) - left);
        const height = Math.max(0, Math.min(rect.bottom, window.innerHeight - 6) - top);

        if (width < 8 || height < 8) {
          return;
        }

        box.style.top = `${top}px`;
        box.style.left = `${left}px`;
        box.style.width = `${width}px`;
        box.style.height = `${height}px`;

        if (index === 0) {
          const label = document.createElement('div');
          label.className = 'ori-keyboard-nav-region-label';
          label.textContent = `${group.zone.prefix} ${group.zone.label}`;
          box.appendChild(label);
        }

        this.regionLayer.appendChild(box);
      });
    }

    positionHint(hint) {
      const rect = hint.element.getBoundingClientRect();
      const badgeWidth = hint.badge.offsetWidth || 40;
      const badgeHeight = hint.badge.offsetHeight || 24;
      const viewportPadding = 8;
      const gap = 6;
      const maxLeft = Math.max(viewportPadding, window.innerWidth - badgeWidth - viewportPadding);
      const maxTop = Math.max(viewportPadding, window.innerHeight - badgeHeight - viewportPadding);
      const candidates = [
        {
          placement: 'top-left',
          top: rect.top - badgeHeight - gap,
          left: rect.left - badgeWidth * 0.18
        },
        {
          placement: 'top-right',
          top: rect.top - badgeHeight - gap,
          left: rect.right - badgeWidth * 0.82
        },
        {
          placement: 'bottom-left',
          top: rect.bottom + gap,
          left: rect.left - badgeWidth * 0.18
        },
        {
          placement: 'bottom-right',
          top: rect.bottom + gap,
          left: rect.right - badgeWidth * 0.82
        }
      ];

      let bestPlacement = candidates[0];
      let bestScore = Number.POSITIVE_INFINITY;

      candidates.forEach((candidate, index) => {
        const clampedTop = clamp(candidate.top, viewportPadding, maxTop);
        const clampedLeft = clamp(candidate.left, viewportPadding, maxLeft);
        const overflowPenalty = Math.abs(clampedTop - candidate.top) + Math.abs(clampedLeft - candidate.left);
        const score = overflowPenalty + index * 0.01;

        if (score < bestScore) {
          bestPlacement = {
            placement: candidate.placement,
            top: clampedTop,
            left: clampedLeft
          };
          bestScore = score;
        }
      });

      hint.badge.dataset.placement = bestPlacement.placement;
      hint.badge.style.top = `${bestPlacement.top}px`;
      hint.badge.style.left = `${bestPlacement.left}px`;
    }

    getMatches(buffer = this.buffer) {
      if (!buffer) {
        return this.hints.slice();
      }

      return this.hints.filter((hint) => hint.code.startsWith(buffer));
    }

    getVisibleMatches(buffer = this.buffer) {
      return this.getMatches(buffer).filter((hint) => {
        return hint.element && document.contains(hint.element) && !hint.badge.hidden;
      });
    }

    getSelectedHint(matches = this.hints) {
      if (!this.selectedElement) {
        return null;
      }

      return matches.find((hint) => hint.element === this.selectedElement) || null;
    }

    getHintRect(hint) {
      const rect = hint.element.getBoundingClientRect();
      return {
        top: rect.top,
        right: rect.right,
        bottom: rect.bottom,
        left: rect.left,
        width: rect.width,
        height: rect.height,
        centerX: rect.left + rect.width / 2,
        centerY: rect.top + rect.height / 2
      };
    }

    findDirectionalHint(currentHint, matches, direction) {
      const currentRect = this.getHintRect(currentHint);
      let bestHint = null;
      let bestScore = Number.POSITIVE_INFINITY;

      matches.forEach((candidate) => {
        if (candidate === currentHint) {
          return;
        }

        const candidateRect = this.getHintRect(candidate);
        const deltaX = candidateRect.centerX - currentRect.centerX;
        const deltaY = candidateRect.centerY - currentRect.centerY;

        let primaryDistance = 0;
        let secondaryDistance = 0;

        if (direction === 'ArrowRight') {
          if (deltaX <= 4) return;
          primaryDistance = deltaX;
          secondaryDistance = Math.abs(deltaY);
        } else if (direction === 'ArrowLeft') {
          if (deltaX >= -4) return;
          primaryDistance = Math.abs(deltaX);
          secondaryDistance = Math.abs(deltaY);
        } else if (direction === 'ArrowDown') {
          if (deltaY <= 4) return;
          primaryDistance = deltaY;
          secondaryDistance = Math.abs(deltaX);
        } else if (direction === 'ArrowUp') {
          if (deltaY >= -4) return;
          primaryDistance = Math.abs(deltaY);
          secondaryDistance = Math.abs(deltaX);
        } else {
          return;
        }

        const score = primaryDistance + secondaryDistance * 2.5;
        if (score < bestScore) {
          bestScore = score;
          bestHint = candidate;
        }
      });

      return bestHint;
    }

    findSequentialHint(currentHint, matches, direction) {
      if (!matches.length) {
        return null;
      }

      const currentIndex = matches.findIndex((hint) => hint.element === currentHint.element);
      if (currentIndex === -1) {
        return null;
      }

      const isReverse = direction === 'ArrowLeft' || direction === 'ArrowUp';
      if (isReverse) {
        return matches[currentIndex - 1] || matches[matches.length - 1];
      }

      return matches[currentIndex + 1] || matches[0];
    }

    moveSelection(direction) {
      const matches = this.getVisibleMatches();
      if (!matches.length) {
        this.flashInvalid();
        return;
      }

      const currentHint = this.getSelectedHint(matches);
      let nextHint = null;

      if (!currentHint) {
        nextHint = matches[0];
      } else {
        nextHint = this.findDirectionalHint(currentHint, matches, direction) ||
          this.findSequentialHint(currentHint, matches, direction);
      }

      if (!nextHint) {
        this.flashInvalid();
        return;
      }

      this.selectedElement = nextHint.element;
      this.updateHintState();
    }

    renderSelectedTarget(hint) {
      if (!this.regionLayer || !hint || !hint.element || !document.contains(hint.element) || !isVisible(hint.element)) {
        return;
      }

      const rect = hint.element.getBoundingClientRect();
      const paddingX = 6;
      const paddingY = 6;
      const top = clamp(rect.top - paddingY, 6, window.innerHeight - 6);
      const left = clamp(rect.left - paddingX, 6, window.innerWidth - 6);
      const width = Math.max(0, Math.min(rect.right + paddingX, window.innerWidth - 6) - left);
      const height = Math.max(0, Math.min(rect.bottom + paddingY, window.innerHeight - 6) - top);

      if (width < 8 || height < 8) {
        return;
      }

      const box = document.createElement('div');
      box.className = 'ori-keyboard-nav-target';
      box.style.top = `${top}px`;
      box.style.left = `${left}px`;
      box.style.width = `${width}px`;
      box.style.height = `${height}px`;
      this.regionLayer.appendChild(box);
    }

    getModeTitle() {
      return this.mode === 'persistent' ? 'Navigation Mode Toggle On' : 'Navigation Mode Quick';
    }

    buildStatusPills(items) {
      return items
        .filter((item) => item && item.label)
        .map((item) => {
          const activeClass = item.active ? ' is-active' : '';
          return `<span class="ori-keyboard-nav-status-pill${activeClass}">${item.label}</span>`;
        })
        .join('');
    }

    updateHintState() {
      const matches = this.getMatches();
      let selectedHint = this.getSelectedHint(matches);
      if (this.selectedElement && !selectedHint) {
        this.selectedElement = null;
      }

      selectedHint = this.getSelectedHint(matches);
      const selectedZoneGroup = this.getSelectedZoneGroup();
      this.renderZoneSelection(selectedZoneGroup);
      this.renderSelectedTarget(selectedHint);

      this.hints.forEach((hint) => {
        const isMatch = !this.suspended && (!this.buffer || hint.code.startsWith(this.buffer));
        const isExact = Boolean(this.buffer) && hint.code === this.buffer;

        hint.badge.hidden = !isMatch;
        hint.badge.dataset.state = isExact ? 'exact' : (this.buffer ? 'match' : 'idle');
        hint.badge.dataset.zoneSelected = selectedZoneGroup && hint.zone.id === selectedZoneGroup.zone.id ? 'true' : 'false';
        hint.badge.dataset.navSelected = selectedHint && hint.element === selectedHint.element ? 'true' : 'false';
      });

      if (!this.status) {
        return;
      }

      if (this.suspended) {
        this.regionLayer.innerHTML = '';
        this.status.innerHTML = [
          `<span class="ori-keyboard-nav-status-title">${this.getModeTitle()}</span>`,
          '<span class="ori-keyboard-nav-status-text">Typing is paused while an input is focused.</span>',
          `<div class="ori-keyboard-nav-status-pills">${this.buildStatusPills([
            { label: 'Blur resumes', active: true },
            { label: 'Esc exits' }
          ])}</div>`
        ].join('');
        return;
      }

      if (!this.hints.length) {
        this.status.innerHTML = [
          `<span class="ori-keyboard-nav-status-title">${this.getModeTitle()}</span>`,
          '<span class="ori-keyboard-nav-status-text">No visible targets on this surface.</span>',
          `<div class="ori-keyboard-nav-status-pills">${this.buildStatusPills([
            { label: 'Esc exits' }
          ])}</div>`
        ].join('');
        return;
      }

      const bufferLabel = this.buffer || '-';
      const detailPills = [
        { label: 'Arrows or HJKL move' },
        { label: 'Enter opens selected' },
        { label: 'Backspace removes last key' },
        { label: 'Esc exits' }
      ];
      const zonePills = this.groups.map((group) => ({
        label: `${group.zone.prefix}: ${group.zone.label}`,
        active: Boolean(selectedZoneGroup && group.zone.id === selectedZoneGroup.zone.id)
      }));

      this.status.innerHTML = [
        `<span class="ori-keyboard-nav-status-title">${this.getModeTitle()}</span>`,
        `<div class="ori-keyboard-nav-status-row"><span class="ori-keyboard-nav-status-label">Type Hint</span><span class="ori-keyboard-nav-status-value">${bufferLabel}</span></div>`,
        `<div class="ori-keyboard-nav-status-pills">${this.buildStatusPills(detailPills)}</div>`,
        zonePills.length
          ? `<div class="ori-keyboard-nav-status-zones"><span class="ori-keyboard-nav-status-label">Zones</span><div class="ori-keyboard-nav-status-pills">${this.buildStatusPills(zonePills)}</div></div>`
          : ''
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
      const isPersistentMode = this.active && this.mode === 'persistent';

      if (!isPersistentMode) {
        this.deactivate();
      } else {
        this.buffer = '';
        this.suspended = false;
        this.selectedElement = null;
        this.detachSuspendedEditable();
      }

      if (!target || !document.contains(target)) {
        return;
      }

      requestAnimationFrame(() => {
        if (!document.contains(target)) {
          return;
        }

        let focusTarget = target;
        if (target.tagName === 'LABEL' && target.control) {
          focusTarget = target.control;
        }

        if (typeof focusTarget.focus === 'function') {
          try {
            focusTarget.focus({ preventScroll: false });
          } catch (error) {
            focusTarget.focus();
          }
        }

        if (focusTarget.matches('input:not([type="checkbox"]):not([type="radio"]):not([type="file"]), textarea')) {
          if (typeof focusTarget.select === 'function') {
            focusTarget.select();
          }
          if (isPersistentMode) {
            this.suspendForEditableTarget(focusTarget);
          }
          return;
        }

        if (focusTarget.matches('select')) {
          if (isPersistentMode) {
            this.suspendForEditableTarget(focusTarget);
          }
          return;
        }

        target.click();

        if (isPersistentMode) {
          if (isEditableTarget(focusTarget)) {
            this.suspendForEditableTarget(focusTarget);
            return;
          }

          this.onRefreshRequested();
          this.updateHintState();
        }
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
