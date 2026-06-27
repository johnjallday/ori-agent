/**
 * Workspace templates ("starting points") shown in the create modal.
 *
 * Each template represents a use case for the workspace. On creation, the
 * picked template:
 *   - pre-fills name + description if the user hasn't already typed them,
 *   - maps to a default "Agent behavior" profile (behaviorProfile) that the
 *     create modal applies unless the user overrides it,
 *   - records its id in workspace shared_data.template_id (so the backend can
 *     recognise the workspace's purpose later when skill/MCP auto-binding
 *     ships),
 *   - seeds a small set of starter tasks via POST /api/orchestration/tasks
 *     so the user lands in a workspace with concrete examples instead of an
 *     empty list.
 *
 * `behaviorProfile` must be one of the workspace behavior profiles the backend
 * understands (general | research | software_project); it is sent as
 * `workspace_preset` and drives planning/workflow defaults. A dev-time guard
 * below warns if a template omits it.
 *
 * Skill / MCP auto-binding is deliberately out of scope for this slice — those
 * paths require the marketplace install flow and should be added in a
 * follow-up. For now, the description text in each template hints at which
 * skills/MCPs the user might want to add manually.
 *
 * Loaded as a non-module global so the create-modal scripts can read it via
 * window.WorkspaceTemplates.
 */
(function () {
  const TEMPLATES = [
    {
      id: 'blank',
      label: 'Blank',
      icon: '✍',
      description: 'Start from scratch.',
      behaviorProfile: 'general',
      defaultName: '',
      defaultDescription: '',
      starterTasks: []
    },
    {
      id: 'travels',
      label: 'Travels',
      icon: '✈',
      description: 'Flights, hotels, trip planning.',
      behaviorProfile: 'general',
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
      behaviorProfile: 'general',
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
    },
    {
      id: 'content-production',
      label: 'Content Production',
      icon: '✏',
      description: 'Brand voice, drafts, scheduled posts.',
      behaviorProfile: 'general',
      defaultName: 'Content Production',
      defaultDescription:
        'Drafts content (posts, newsletters, copy) in a consistent voice. ' +
        'Recommended add-ons: a style-guide note, a publishing-tool MCP, and a Brand Voice skill.',
      starterTasks: [
        {
          description: 'Draft a style guide',
          details:
            'Replace with your brand voice rules: tone, words to avoid, target audience. ' +
            'The agent uses this as ground truth for every draft.'
        },
        {
          description: 'Draft this week\'s posts',
          details:
            'List the topics, then ask the agent to draft each in the brand voice. ' +
            'Schedule this task weekly once the style guide is settled.'
        }
      ]
    },
    {
      id: 'research-project',
      label: 'Research Project',
      icon: '📚',
      description: 'Synthesis docs, sources, weekly reading.',
      behaviorProfile: 'research',
      defaultName: 'Research Project',
      defaultDescription:
        'Tracks a research topic over time. Maintains a synthesis doc, a sources index, ' +
        'and recurring summaries. Recommended add-ons: a web search MCP and a Citations skill.',
      starterTasks: [
        {
          description: 'Build the synthesis doc',
          details:
            'Capture the research question, current best understanding, and open gaps. ' +
            'The agent keeps this up to date as new sources are added.'
        },
        {
          description: 'Compile this week\'s reading list',
          details:
            'Ask the agent for new sources on the topic and a 1-paragraph TL;DR of each. ' +
            'Schedule this task weekly to keep the synthesis fresh.'
        }
      ]
    },
    {
      id: 'personal-ops',
      label: 'Personal Ops',
      icon: '🗓',
      description: 'Daily journal, briefings, follow-ups.',
      behaviorProfile: 'general',
      defaultName: 'Personal Ops',
      defaultDescription:
        'Personal command center — a daily journal, morning briefing, and inbox follow-ups. ' +
        'Recommended add-ons: a calendar MCP, an email MCP, and a daily-summary skill.',
      starterTasks: [
        {
          description: 'Morning briefing',
          details:
            'Customize with the channels you care about (calendar, email, urgent threads). ' +
            'Schedule this task to run before your workday starts.'
        },
        {
          description: 'End-of-day journal',
          details:
            'Write a short reflection on what shipped today and what carries over. ' +
            'Schedule this task to run at the end of your workday.'
        }
      ]
    }
  ];

  // Valid workspace behavior profiles the backend understands (sent as
  // `workspace_preset`). Keep in sync with internal/workspacesettings.
  const VALID_BEHAVIOR_PROFILES = ['general', 'research', 'software_project'];

  // Dev-time guard: every starting point must declare a valid behaviorProfile
  // so adding a template forces a behavior decision. Warn loudly rather than
  // throw — a bad value should not break the create modal.
  TEMPLATES.forEach((t) => {
    if (!VALID_BEHAVIOR_PROFILES.includes(t.behaviorProfile)) {
      console.warn(
        `[WorkspaceTemplates] template "${t.id}" has missing/invalid behaviorProfile ` +
        `(${JSON.stringify(t.behaviorProfile)}); expected one of ${VALID_BEHAVIOR_PROFILES.join(', ')}.`
      );
    }
  });

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
    validBehaviorProfiles: VALID_BEHAVIOR_PROFILES,
    getById,
    render,
    getSelected,
    seedStarterTasks
  };
})();
