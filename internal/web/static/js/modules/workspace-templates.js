/**
 * Workspace templates ("starting points") shown in the create modal.
 *
 * Each template represents a use case for the workspace. On creation, the
 * picked template:
 *   - pre-fills name + description if the user hasn't already typed them,
 *   - records its id in workspace shared_data.template_id (so the backend can
 *     recognise the workspace's purpose later when skill/MCP auto-binding
 *     ships),
 *   - seeds a small set of starter tasks via POST /api/orchestration/tasks
 *     so the user lands in a workspace with concrete examples instead of an
 *     empty list.
 *
 * Skill / MCP auto-binding is deliberately out of scope for this slice — those
 * paths require the marketplace install flow and should be added in a
 * follow-up. For now, the description text in each template hints at which
 * skills/MCPs the user might want to add manually.
 *
 * Loaded as a non-module global so workspace-create.js (a non-module script)
 * can read it via window.WorkspaceTemplates.
 */
(function () {
  const TEMPLATES = [
    {
      id: 'blank',
      label: 'Blank',
      icon: '✍',
      description: 'Start from scratch.',
      defaultName: '',
      defaultDescription: '',
      starterTasks: []
    },
    {
      id: 'travels',
      label: 'Travels',
      icon: '✈',
      description: 'Flights, hotels, trip planning.',
      defaultName: 'Travels',
      defaultDescription:
        'Plans, books, and tracks trips. Help me with flights, hotels, and itineraries. ' +
        'Recommended add-ons: a flight/hotel MCP, a calendar MCP, and a Travel skill.',
      starterTasks: [
        {
          description: 'Plan an upcoming trip',
          details:
            'Replace this with destination, dates, budget, and any constraints. ' +
            'The agent will research flights and hotels and propose an itinerary.'
        }
      ]
    },
    {
      id: 'daily-briefings',
      label: 'Daily Briefings',
      icon: '📊',
      description: 'Morning roundup, market summary, news digest.',
      defaultName: 'Daily Briefings',
      defaultDescription:
        'Generates a daily summary of the topics I care about. ' +
        'Recommended add-ons: a web search / news MCP, and any data sources you want summarized.',
      starterTasks: [
        {
          description: 'Morning briefing',
          details:
            'Customize the topics (e.g. headlines, market open, calendar conflicts) ' +
            'then schedule this task to run every weekday morning.'
        }
      ]
    }
  ];

  const escape = (s) => String(s || '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  })[c]);

  function getById(id) {
    return TEMPLATES.find((t) => t.id === id) || TEMPLATES[0];
  }

  /**
   * Render the template card grid into the given container element. Calls
   * onSelect(template) whenever the user picks a card. The first card is
   * selected by default.
   */
  function render(container, options) {
    if (!container) return null;
    const onSelect = options && typeof options.onSelect === 'function' ? options.onSelect : null;
    container.innerHTML = '';

    TEMPLATES.forEach((template, idx) => {
      const card = document.createElement('button');
      card.type = 'button';
      card.className = 'workspace-template-card';
      if (idx === 0) card.classList.add('is-selected');
      card.dataset.templateId = template.id;
      card.setAttribute('role', 'radio');
      card.setAttribute('aria-checked', idx === 0 ? 'true' : 'false');
      card.innerHTML = `
        <span class="workspace-template-card-icon" aria-hidden="true">${escape(template.icon)}</span>
        <span class="workspace-template-card-label">${escape(template.label)}</span>
        <span class="workspace-template-card-desc">${escape(template.description)}</span>
      `;
      card.addEventListener('click', () => {
        container.querySelectorAll('.workspace-template-card').forEach((el) => {
          el.classList.remove('is-selected');
          el.setAttribute('aria-checked', 'false');
        });
        card.classList.add('is-selected');
        card.setAttribute('aria-checked', 'true');
        if (onSelect) onSelect(template);
      });
      container.appendChild(card);
    });

    return TEMPLATES[0];
  }

  /**
   * Returns the currently selected template, defaulting to the first one if
   * no card is marked is-selected.
   */
  function getSelected(container) {
    if (!container) return TEMPLATES[0];
    const selected = container.querySelector('.workspace-template-card.is-selected');
    if (!selected) return TEMPLATES[0];
    return getById(selected.dataset.templateId);
  }

  /**
   * Posts the template's starter tasks to the new workspace. Returns the
   * count of successfully created tasks. Errors on individual tasks are
   * logged but do not abort — partial seeding is better than none.
   */
  async function seedStarterTasks(workspaceId, template) {
    if (!workspaceId || !template || !Array.isArray(template.starterTasks)) {
      return 0;
    }
    let created = 0;
    for (const task of template.starterTasks) {
      if (!task || !task.description) continue;
      try {
        const response = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            workspace_id: workspaceId,
            description: task.description,
            details: task.details || '',
            status: 'pending'
          })
        });
        if (response.ok) {
          created++;
        } else {
          const text = await response.text().catch(() => '');
          console.warn('Failed to seed starter task:', task.description, text);
        }
      } catch (error) {
        console.warn('Failed to seed starter task:', task.description, error);
      }
    }
    return created;
  }

  window.WorkspaceTemplates = {
    list: TEMPLATES,
    getById,
    render,
    getSelected,
    seedStarterTasks
  };
})();
