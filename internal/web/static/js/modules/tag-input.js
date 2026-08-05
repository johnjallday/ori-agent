/**
 * Shared tag input component.
 *
 * One canonical editor for tags everywhere they are entered (workspace
 * detail, create-workspace modal, sessions, notes, tasks): chips with
 * remove buttons, a text field with typeahead suggestions from the unified
 * tag pool (GET /api/tags?scope=all), and a clickable row of the most-used
 * pool tags not yet applied.
 *
 * Pure helpers are exported for node:test; the DOM widget is created with
 * createTagInput(). Plain (non-module) scripts use window.OriTagInput.
 */

const TAG_POOL_URL = '/api/tags?scope=all';
const TAG_POOL_CACHE_MS = 30000;

export const DEFAULT_MAX_TAGS = 20;
export const DEFAULT_MAX_TAG_LENGTH = 64;

/** Normalize a single tag the same way the backend does: trim + lowercase. */
export function normalizeTagValue(value) {
  return String(value ?? '')
    .trim()
    .toLowerCase();
}

/**
 * Normalize a tag list: trim/lowercase, drop empties, dedupe, enforce caps.
 * Returns { tags, error } where error is '' when the list is valid.
 */
export function normalizeTagList(tags, options = {}) {
  const maxTags = options.maxTags ?? DEFAULT_MAX_TAGS;
  const maxTagLength = options.maxTagLength ?? DEFAULT_MAX_TAG_LENGTH;

  const normalized = [];
  const seen = new Set();
  (Array.isArray(tags) ? tags : []).forEach(tag => {
    const value = normalizeTagValue(tag);
    if (!value || seen.has(value)) return;
    seen.add(value);
    normalized.push(value);
  });

  const overlong = normalized.find(tag => Array.from(tag).length > maxTagLength);
  if (overlong) {
    return {
      tags: normalized,
      error: `"${overlong}" exceeds the ${maxTagLength} character limit.`
    };
  }
  if (normalized.length > maxTags) {
    return { tags: normalized, error: `At most ${maxTags} tags are allowed.` };
  }
  return { tags: normalized, error: '' };
}

/**
 * Typeahead matches for the dropdown: pool tags containing the query,
 * prefix matches first, then by usage. Already-selected tags are excluded.
 */
export function filterSuggestions(pool, options = {}) {
  const query = normalizeTagValue(options.query);
  const limit = options.limit ?? 8;
  const exclude = new Set((options.exclude || []).map(normalizeTagValue));

  const candidates = (Array.isArray(pool) ? pool : [])
    .filter(entry => entry && entry.name && !exclude.has(entry.name))
    .filter(entry => query === '' || entry.name.includes(query));

  candidates.sort((a, b) => {
    if (query !== '') {
      const aPrefix = a.name.startsWith(query) ? 0 : 1;
      const bPrefix = b.name.startsWith(query) ? 0 : 1;
      if (aPrefix !== bPrefix) return aPrefix - bPrefix;
    }
    if ((b.total ?? 0) !== (a.total ?? 0)) return (b.total ?? 0) - (a.total ?? 0);
    return a.name.localeCompare(b.name);
  });

  return candidates.slice(0, limit).map(entry => entry.name);
}

/** Most-used pool tags not yet applied, for the "Suggested" chip row. */
export function topSuggestions(pool, options = {}) {
  return filterSuggestions(pool, {
    query: '',
    exclude: options.exclude || [],
    limit: options.limit ?? 10
  });
}

let tagPoolCache = { at: 0, data: null };

/** Clear the shared pool cache (used after tag mutations and in tests). */
export function clearTagPoolCache() {
  tagPoolCache = { at: 0, data: null };
}

/**
 * Fetch the unified tag pool with a short-lived shared cache so several
 * editors on one page don't refetch. Returns [] when the request fails —
 * tag entry must keep working without suggestions.
 */
export async function fetchTagPool(options = {}) {
  const now = Date.now();
  if (!options.force && tagPoolCache.data && now - tagPoolCache.at < TAG_POOL_CACHE_MS) {
    return tagPoolCache.data;
  }
  const fetcher = options.fetcher || (typeof fetch !== 'undefined' ? fetch : null);
  if (!fetcher) return [];
  try {
    const response = await fetcher(TAG_POOL_URL);
    if (!response || !response.ok) return [];
    const payload = await response.json();
    const pool = Array.isArray(payload?.tags) ? payload.tags : [];
    tagPoolCache = { at: now, data: pool };
    return pool;
  } catch {
    return [];
  }
}

/**
 * Create the tag input widget inside `container`.
 *
 * Options: initialTags, maxTags, maxTagLength, placeholder, suggestedLimit,
 * dropdownLimit, fetchPool (override), onChange(tags).
 * Returns { getTags, setTags, focus, refreshPool, destroy, element }.
 */
export function createTagInput(options = {}) {
  const container = options.container;
  if (!container) throw new Error('createTagInput requires a container element');

  const maxTags = options.maxTags ?? DEFAULT_MAX_TAGS;
  const maxTagLength = options.maxTagLength ?? DEFAULT_MAX_TAG_LENGTH;
  const suggestedLimit = options.suggestedLimit ?? 10;
  const dropdownLimit = options.dropdownLimit ?? 8;
  const loadPool = options.fetchPool || fetchTagPool;

  let tags = normalizeTagList(options.initialTags, { maxTags, maxTagLength }).tags;
  let pool = [];
  let poolLoaded = false;
  let highlightIndex = -1;
  let dropdownItems = [];

  const root = document.createElement('div');
  root.className = 'tag-input';

  const chips = document.createElement('div');
  chips.className = 'tag-input-chips';

  const field = document.createElement('input');
  field.type = 'text';
  field.className = 'tag-input-field';
  field.placeholder = options.placeholder ?? 'Add tag…';
  field.maxLength = maxTagLength;
  field.autocomplete = 'off';
  field.spellcheck = false;
  field.setAttribute('aria-label', 'Add tag');

  const dropdown = document.createElement('div');
  dropdown.className = 'tag-input-dropdown';
  dropdown.hidden = true;
  dropdown.setAttribute('role', 'listbox');

  const suggested = document.createElement('div');
  suggested.className = 'tag-input-suggested';
  suggested.hidden = true;

  const hint = document.createElement('div');
  hint.className = 'tag-input-hint';
  hint.hidden = true;
  hint.setAttribute('role', 'alert');

  chips.appendChild(field);
  root.appendChild(chips);
  root.appendChild(dropdown);
  root.appendChild(suggested);
  root.appendChild(hint);
  container.appendChild(root);

  function setHint(message) {
    const text = String(message || '').trim();
    hint.textContent = text;
    hint.hidden = text === '';
  }

  function emitChange() {
    if (typeof options.onChange === 'function') {
      options.onChange([...tags]);
    }
  }

  function renderChips() {
    chips.querySelectorAll('.tag-chip').forEach(chip => chip.remove());
    tags.forEach(tag => {
      const chip = document.createElement('span');
      chip.className = 'tag-chip';

      const label = document.createElement('span');
      label.className = 'tag-chip-label';
      label.textContent = tag;
      label.title = tag;

      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'tag-chip-remove';
      remove.textContent = '×';
      remove.setAttribute('aria-label', `Remove ${tag}`);
      remove.addEventListener('click', () => removeTag(tag));

      chip.appendChild(label);
      chip.appendChild(remove);
      chips.insertBefore(chip, field);
    });
  }

  function closeDropdown() {
    dropdown.hidden = true;
    dropdown.innerHTML = '';
    dropdownItems = [];
    highlightIndex = -1;
  }

  function renderDropdown() {
    const query = field.value;
    if (normalizeTagValue(query) === '') {
      closeDropdown();
      return;
    }
    dropdownItems = filterSuggestions(pool, {
      query,
      exclude: tags,
      limit: dropdownLimit
    });
    dropdown.innerHTML = '';
    if (dropdownItems.length === 0) {
      closeDropdown();
      return;
    }
    highlightIndex = Math.min(highlightIndex, dropdownItems.length - 1);
    dropdownItems.forEach((name, index) => {
      const option = document.createElement('button');
      option.type = 'button';
      option.className = 'tag-input-option';
      option.setAttribute('role', 'option');
      option.textContent = name;
      if (index === highlightIndex) option.classList.add('is-highlighted');
      // mousedown beats the field blur so the click still lands.
      option.addEventListener('mousedown', event => {
        event.preventDefault();
        addTag(name);
      });
      dropdown.appendChild(option);
    });
    dropdown.hidden = false;
  }

  function renderSuggested() {
    const names = topSuggestions(pool, { exclude: tags, limit: suggestedLimit });
    suggested.innerHTML = '';
    if (names.length === 0) {
      suggested.hidden = true;
      return;
    }
    const label = document.createElement('span');
    label.className = 'tag-input-suggested-label';
    label.textContent = 'Suggested:';
    suggested.appendChild(label);
    names.forEach(name => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'tag-input-suggested-chip';
      chip.textContent = name;
      chip.title = `Add ${name}`;
      chip.addEventListener('click', () => addTag(name));
      suggested.appendChild(chip);
    });
    suggested.hidden = false;
  }

  function renderAll() {
    renderChips();
    renderSuggested();
  }

  function addTag(rawValue) {
    const value = normalizeTagValue(rawValue);
    if (!value) return false;
    const result = normalizeTagList([...tags, value], { maxTags, maxTagLength });
    if (result.error) {
      setHint(result.error);
      return false;
    }
    const changed = result.tags.length !== tags.length;
    tags = result.tags;
    setHint('');
    field.value = '';
    closeDropdown();
    renderAll();
    if (changed) emitChange();
    field.focus();
    return changed;
  }

  function removeTag(tag) {
    const next = tags.filter(existing => existing !== tag);
    if (next.length === tags.length) return;
    tags = next;
    setHint('');
    renderAll();
    emitChange();
  }

  async function ensurePool(force = false) {
    if (poolLoaded && !force) return;
    poolLoaded = true;
    pool = await loadPool(force ? { force: true } : {});
    renderSuggested();
    if (!dropdown.hidden) renderDropdown();
  }

  function onKeydown(event) {
    if (event.key === 'ArrowDown' && !dropdown.hidden) {
      event.preventDefault();
      highlightIndex = Math.min(highlightIndex + 1, dropdownItems.length - 1);
      renderDropdown();
    } else if (event.key === 'ArrowUp' && !dropdown.hidden) {
      event.preventDefault();
      highlightIndex = Math.max(highlightIndex - 1, -1);
      renderDropdown();
    } else if (event.key === 'Enter') {
      event.preventDefault();
      if (highlightIndex >= 0 && highlightIndex < dropdownItems.length) {
        addTag(dropdownItems[highlightIndex]);
      } else if (normalizeTagValue(field.value)) {
        addTag(field.value);
      }
    } else if (event.key === ',') {
      event.preventDefault();
      if (normalizeTagValue(field.value)) addTag(field.value);
    } else if (event.key === 'Escape') {
      if (!dropdown.hidden) {
        // Swallow Esc when it only closes the dropdown, so a surrounding
        // editor (e.g. the workspace tags editor) doesn't also close.
        event.stopPropagation();
        closeDropdown();
      }
    } else if (event.key === 'Backspace' && field.value === '' && tags.length > 0) {
      removeTag(tags[tags.length - 1]);
    }
  }

  function onInput() {
    setHint('');
    highlightIndex = -1;
    renderDropdown();
  }

  function onFocus() {
    void ensurePool();
  }

  function onBlur() {
    // Delay so option mousedown handlers run first.
    setTimeout(() => closeDropdown(), 120);
  }

  field.addEventListener('keydown', onKeydown);
  field.addEventListener('input', onInput);
  field.addEventListener('focus', onFocus);
  field.addEventListener('blur', onBlur);
  chips.addEventListener('click', event => {
    if (event.target === chips) field.focus();
  });

  renderAll();

  return {
    element: root,
    getTags: () => [...tags],
    setTags: nextTags => {
      tags = normalizeTagList(nextTags, { maxTags, maxTagLength }).tags;
      setHint('');
      field.value = '';
      closeDropdown();
      renderAll();
    },
    focus: () => field.focus(),
    refreshPool: () => ensurePool(true),
    destroy: () => {
      root.remove();
    }
  };
}

if (typeof window !== 'undefined') {
  window.OriTagInput = {
    createTagInput,
    fetchTagPool,
    clearTagPoolCache,
    normalizeTagValue,
    normalizeTagList,
    filterSuggestions,
    topSuggestions
  };
}
