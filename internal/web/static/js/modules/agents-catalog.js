/**
 * Agents Create page: Hire / Advanced tabs.
 *
 * Owns the ARIA tablist and the Hire tab: the same Hire card the Agents
 * roster's Create Agent modal shows (agent-hire-card.js), sending the same
 * POST /api/agents/hire. Hire is offered once a personal assistant is hired;
 * until then the page is Advanced alone. The Advanced tab's form and behavior
 * (agents-create.js) are untouched by this module.
 */
(function () {
  'use strict';

  let hireCard = null;

  function byId(id) {
    return document.getElementById(id);
  }

  function tabs() {
    return [byId('catalogTabBtn'), byId('customTabBtn')].filter(Boolean);
  }

  function initTabs() {
    const api = window.OriAgentHire;
    const list = tabs();
    if (!api || list.length !== 2) return;
    api.bindTablist(list, function (tab) {
      api.selectTab(list, tab);
    });
  }

  async function readAssistant() {
    try {
      const response = await fetch('/api/personal-assistant', {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) return null;
      const data = await response.json();
      return data?.personal_assistant || null;
    } catch (_) {
      return null;
    }
  }

  // Without an assistant there is no Hire tab: the page opens on Advanced.
  function withoutHire() {
    const api = window.OriAgentHire;
    const [hireTab, advancedTab] = tabs();
    if (api && advancedTab) api.selectTab(tabs(), advancedTab);
    if (hireTab) hireTab.hidden = true;
  }

  async function mountHire() {
    const api = window.OriAgentHire;
    const host = byId('hireCardHost');
    const button = byId('hireStartBtn');
    if (!api || !host || !button) return;
    const assistant = await readAssistant();
    if (!api.assistantAllowsHire(assistant)) {
      withoutHire();
      return;
    }
    let catalog;
    try {
      catalog = await api.loadHireCatalog();
    } catch (error) {
      console.error('Error loading agent catalog:', error);
      host.innerHTML =
        '<p class="form-help" role="alert">Could not load the roles. Try reloading the page.</p>';
      button.disabled = true;
      return;
    }
    hireCard = api.mountHireCard(host, {
      catalog,
      idPrefix: 'pageHire',
      onChange: function (view) {
        button.disabled = view.busy;
        button.textContent = view.buttonLabel;
        if (view.busy) button.setAttribute('aria-busy', 'true');
        else button.removeAttribute('aria-busy');
      },
      onHired: function (result) {
        if (typeof window.notifyToast === 'function')
          window.notifyToast(api.hiredMessage(result), 'success');
        window.location.href = '/agents?agent=' + encodeURIComponent(result.agent_name || '');
      }
    });
    button.addEventListener('click', function () {
      void hireCard?.submit();
    });
  }

  // While the personal assistant is unhired, point at Mission 01: it is hired
  // from the Agents page's Hire button, not from this page (PRD FR40). The
  // page stays usable for a plain agent either way, so any failure just leaves
  // the line hidden.
  async function showAssistantPointer() {
    const pointer = byId('assistantPointer');
    const link = byId('assistantPointerLink');
    if (!pointer || !link) return;
    try {
      const response = await fetch('/api/personal-assistant', {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) return;
      const data = await response.json();
      const hire = window.OriAssistantHire;
      if (!hire || hire.presetView(data?.personal_assistant).mode !== 'form') return;
      link.href = hire.MEET_ASSISTANT_AGENTS_ROUTE;
      pointer.hidden = false;
    } catch (_) {
      // Leave the line hidden.
    }
  }

  document.addEventListener('DOMContentLoaded', function () {
    initTabs();
    void mountHire();
    showAssistantPointer();
  });
})();
