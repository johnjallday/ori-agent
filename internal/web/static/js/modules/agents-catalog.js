/**
 * Agents Create page: Catalog / Custom tabs.
 *
 * Owns the ARIA tablist toggle and the Catalog tab (card grid fetched from
 * GET /api/agents/catalog, one-field create via POST /api/agents with
 * catalog_role). The Custom tab's form and behavior (agents-create.js) are
 * untouched by this module.
 */
(function () {
  'use strict';

  let catalogEntries = [];
  let selectedEntry = null;

  function byId(id) {
    return document.getElementById(id);
  }

  // ---------------------------------------------------------------------
  // Tabs (ARIA APG tabs pattern: click + Left/Right/Home/End keyboard nav)
  // ---------------------------------------------------------------------
  function initTabs() {
    const tabs = [byId('catalogTabBtn'), byId('customTabBtn')];
    const panels = {
      catalogTabBtn: byId('catalogTabPanel'),
      customTabBtn: byId('customTabPanel')
    };
    if (!tabs[0] || !tabs[1]) return;

    function activate(tab) {
      tabs.forEach(function (t) {
        const isActive = t === tab;
        t.classList.toggle('is-active', isActive);
        t.setAttribute('aria-selected', isActive ? 'true' : 'false');
        t.tabIndex = isActive ? 0 : -1;
        const panel = panels[t.id];
        if (panel) panel.hidden = !isActive;
      });
      tab.focus();
    }

    tabs.forEach(function (tab, i) {
      tab.addEventListener('click', function () {
        activate(tab);
      });
      tab.addEventListener('keydown', function (e) {
        let targetIndex = null;
        if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
          targetIndex = (i + 1) % tabs.length;
        } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
          targetIndex = (i - 1 + tabs.length) % tabs.length;
        } else if (e.key === 'Home') {
          targetIndex = 0;
        } else if (e.key === 'End') {
          targetIndex = tabs.length - 1;
        }
        if (targetIndex !== null) {
          e.preventDefault();
          activate(tabs[targetIndex]);
        }
      });
    });
  }

  // ---------------------------------------------------------------------
  // Catalog grid
  // ---------------------------------------------------------------------
  async function loadCatalog() {
    const grid = byId('catalogGrid');
    if (!grid) return;
    try {
      const response = await fetch('/api/agents/catalog');
      if (!response.ok) throw new Error('Failed to load catalog');
      const data = await response.json();
      catalogEntries = Array.isArray(data.entries) ? data.entries : [];
      renderCatalogGrid();
    } catch (error) {
      console.error('Error loading agent catalog:', error);
      showCatalogError('Could not load the role catalog. Try reloading the page.');
    }
  }

  function renderCatalogGrid() {
    const grid = byId('catalogGrid');
    if (!grid) return;
    grid.innerHTML = catalogEntries
      .map(function (entry) {
        return (
          '<button type="button" class="catalog-card" role="radio" aria-checked="false" ' +
          'data-slug="' +
          escapeAttr(entry.slug) +
          '" style="--catalog-accent: ' +
          escapeAttr(entry.accent_color) +
          ';">' +
          '<span class="catalog-card-emblem"><i class="bi bi-' +
          escapeAttr(entry.emblem) +
          '" aria-hidden="true"></i></span>' +
          '<span class="catalog-card-name">' +
          escapeHtml(entry.display_name) +
          '</span>' +
          '<span class="catalog-card-tagline">' +
          escapeHtml(entry.tagline) +
          '</span>' +
          '</button>'
        );
      })
      .join('');

    grid.querySelectorAll('.catalog-card').forEach(function (card) {
      card.addEventListener('click', function () {
        selectEntry(card.dataset.slug);
      });
    });
  }

  function selectEntry(slug) {
    selectedEntry =
      catalogEntries.find(function (e) {
        return e.slug === slug;
      }) || null;
    if (!selectedEntry) return;

    document.querySelectorAll('#catalogGrid .catalog-card').forEach(function (card) {
      const isSelected = card.dataset.slug === slug;
      card.classList.toggle('is-selected', isSelected);
      card.setAttribute('aria-checked', isSelected ? 'true' : 'false');
    });

    const selectionCard = byId('catalogSelectionCard');
    if (selectionCard) selectionCard.hidden = false;

    const nameInput = byId('catalogAgentName');
    if (nameInput && !nameInput.value.trim()) {
      nameInput.value = selectedEntry.display_name;
    }

    const domainGroup = byId('catalogDomainGroup');
    if (domainGroup) domainGroup.hidden = !selectedEntry.supports_domain;

    renderPresetSummary();
    hideCatalogModelNotice();
    hideCatalogError();
  }

  function tierLabel(tier) {
    switch (tier) {
      case 'fast':
        return 'Fast';
      case 'balanced':
        return 'Balanced';
      case 'deep':
        return 'Deep reasoning';
      default:
        return tier;
    }
  }

  function renderPresetSummary() {
    const summary = byId('catalogPresetSummary');
    if (!summary || !selectedEntry) return;
    const skillsLine =
      selectedEntry.starter_skills && selectedEntry.starter_skills.length > 0
        ? escapeHtml(selectedEntry.starter_skills.join(', '))
        : 'none — starts with an empty loadout';
    summary.innerHTML =
      '<div><strong>Role:</strong> ' +
      escapeHtml(selectedEntry.display_name) +
      '</div>' +
      '<div><strong>Model tier:</strong> ' +
      escapeHtml(tierLabel(selectedEntry.model_tier)) +
      '</div>' +
      '<div><strong>Starter skills:</strong> ' +
      skillsLine +
      '</div>' +
      '<div>' +
      escapeHtml(selectedEntry.description) +
      '</div>';
  }

  function showCatalogModelNotice(message) {
    const notice = byId('catalogModelNotice');
    if (!notice) return;
    notice.textContent = message;
    notice.hidden = false;
  }

  function hideCatalogModelNotice() {
    const notice = byId('catalogModelNotice');
    if (notice) notice.hidden = true;
  }

  function showCatalogError(message) {
    const box = byId('catalogError');
    if (!box) return;
    box.textContent = message;
    box.hidden = false;
    box.focus();
  }

  function hideCatalogError() {
    const box = byId('catalogError');
    if (box) box.hidden = true;
  }

  async function createFromCatalog() {
    if (!selectedEntry) return;
    const nameInput = byId('catalogAgentName');
    const name = nameInput ? nameInput.value.trim() : '';
    const nameError = byId('catalogAgentNameError');
    if (!name) {
      if (nameError) nameError.textContent = 'Agent name is required.';
      if (nameInput) nameInput.classList.add('is-invalid');
      return;
    }
    if (nameError) nameError.textContent = '';
    if (nameInput) nameInput.classList.remove('is-invalid');

    const domainInput = byId('catalogDomain');
    const domain = selectedEntry.supports_domain && domainInput ? domainInput.value.trim() : '';

    const createBtn = byId('catalogCreateBtn');
    if (createBtn) createBtn.disabled = true;
    hideCatalogError();

    try {
      const response = await fetch('/api/agents', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: name,
          catalog_role: selectedEntry.slug,
          domain: domain
        })
      });

      const result = await response.json().catch(function () {
        return {};
      });
      if (!response.ok) {
        throw new Error(result.error || 'Failed to create agent');
      }

      if (result.model_category_fallback) {
        showCatalogModelNotice(
          result.notice ||
            'Used your default model — the selected tier has no configured model yet.'
        );
      }

      window.location.href = '/agents';
    } catch (error) {
      console.error('Error creating agent from catalog:', error);
      showCatalogError(error.message || 'Failed to create agent');
    } finally {
      if (createBtn) createBtn.disabled = false;
    }
  }

  document.addEventListener('DOMContentLoaded', function () {
    initTabs();
    loadCatalog();
    const createBtn = byId('catalogCreateBtn');
    if (createBtn) createBtn.addEventListener('click', createFromCatalog);
  });
})();
