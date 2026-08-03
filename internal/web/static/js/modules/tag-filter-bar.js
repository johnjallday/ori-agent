/**
 * Shared multi-select tag filter bar.
 *
 * Modeled on the workspace hub launcher's tag filter: chips for every
 * available tag, AND-matching multi-select, a Clear button, and the whole
 * bar hides when there is nothing to filter by. Selection is plain UI
 * state — it intentionally does not persist across reloads.
 *
 * Pure helpers are exported for node:test; plain (non-module) scripts use
 * window.OriTagFilterBar.
 */

/** Normalized tag list for one item (workspace, note, task…). */
export function itemTags(item) {
  if (!item || !Array.isArray(item.tags)) return [];
  return item.tags.map(tag => String(tag || '').trim()).filter(tag => tag.length > 0);
}

/** Sorted unique tags across all items. */
export function collectTags(items, getTags = itemTags) {
  const tags = new Set();
  (Array.isArray(items) ? items : []).forEach(item => {
    getTags(item).forEach(tag => tags.add(tag));
  });
  return Array.from(tags).sort((a, b) => a.localeCompare(b));
}

/** AND-matching: the item must carry every active tag. */
export function matchesActiveTags(tags, activeTags) {
  const active = Array.from(activeTags || []);
  if (active.length === 0) return true;
  const tagSet = new Set(tags || []);
  return active.every(tag => tagSet.has(tag));
}

/** Items that carry every active tag. */
export function filterItems(items, activeTags, getTags = itemTags) {
  const active = Array.from(activeTags || []);
  if (active.length === 0) return Array.isArray(items) ? items : [];
  return (Array.isArray(items) ? items : []).filter(item =>
    matchesActiveTags(getTags(item), active)
  );
}

/**
 * Create the filter bar inside `container`.
 *
 * Options: label (default "Tags"), onChange(activeTagsArray).
 * Returns { setAvailableTags, getActiveTags, clear, element, destroy }.
 */
export function createTagFilterBar(options = {}) {
  const container = options.container;
  if (!container) throw new Error('createTagFilterBar requires a container element');

  const active = new Set();
  let available = [];

  const root = document.createElement('div');
  root.className = 'tag-filter-bar';
  root.hidden = true;

  const label = document.createElement('span');
  label.className = 'tag-filter-bar-label';
  label.textContent = options.label ?? 'Tags';

  const chips = document.createElement('div');
  chips.className = 'tag-filter-bar-chips';

  const clearBtn = document.createElement('button');
  clearBtn.type = 'button';
  clearBtn.className = 'tag-filter-bar-clear';
  clearBtn.textContent = 'Clear';
  clearBtn.hidden = true;

  root.appendChild(label);
  root.appendChild(chips);
  root.appendChild(clearBtn);
  container.appendChild(root);

  function emitChange() {
    if (typeof options.onChange === 'function') {
      options.onChange(Array.from(active));
    }
  }

  function render() {
    chips.innerHTML = '';
    if (available.length === 0) {
      root.hidden = true;
      clearBtn.hidden = true;
      return;
    }
    root.hidden = false;
    available.forEach(tag => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'tag-filter-bar-chip';
      if (active.has(tag)) chip.classList.add('is-active');
      chip.setAttribute('aria-pressed', active.has(tag) ? 'true' : 'false');
      chip.textContent = tag;
      chip.title = tag;
      chip.addEventListener('click', () => {
        if (active.has(tag)) {
          active.delete(tag);
        } else {
          active.add(tag);
        }
        render();
        emitChange();
      });
      chips.appendChild(chip);
    });
    clearBtn.hidden = active.size === 0;
  }

  clearBtn.addEventListener('click', () => {
    if (active.size === 0) return;
    active.clear();
    render();
    emitChange();
  });

  return {
    element: root,
    setAvailableTags(tags) {
      available = Array.from(new Set(tags || [])).sort((a, b) => a.localeCompare(b));
      // Prune selections that no longer exist among the available tags.
      let pruned = false;
      Array.from(active).forEach(tag => {
        if (!available.includes(tag)) {
          active.delete(tag);
          pruned = true;
        }
      });
      render();
      if (pruned) emitChange();
    },
    getActiveTags: () => Array.from(active),
    clear() {
      if (active.size === 0) return;
      active.clear();
      render();
      emitChange();
    },
    destroy() {
      root.remove();
    }
  };
}

if (typeof window !== 'undefined') {
  window.OriTagFilterBar = {
    createTagFilterBar,
    collectTags,
    filterItems,
    matchesActiveTags,
    itemTags
  };
}
