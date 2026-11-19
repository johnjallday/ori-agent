/**
 * A lightweight modal that lists agents and resolves to the one the user
 * picks (or null when they cancel), for the ticket page's agent chooser. It
 * replaces the native window.prompt, which broke the design system.
 *
 * It builds a transient overlay + card in the DOM, returns a Promise that
 * resolves when the user picks an option (or cancels), and tears down its
 * own DOM on resolution. No bootstrap dependency: the dialog is small enough
 * that pulling one in just for it would be heavier than the dialog itself.
 */

const ESCAPE_HTML = value =>
  String(value || '').replace(
    /[&<>"']/g,
    ch =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      })[ch]
  );

let stylesInjected = false;
function ensureDialogStyles() {
  if (stylesInjected) return;
  stylesInjected = true;
  const style = document.createElement('style');
  style.setAttribute('data-agent-picker-dialog', 'true');
  style.textContent = `
    .agent-picker-backdrop {
      position: fixed;
      inset: 0;
      background: rgba(15, 23, 42, 0.4);
      backdrop-filter: blur(2px);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 1080;
      animation: agentPickerIn 0.14s ease-out;
    }
    @keyframes agentPickerIn { from { opacity: 0; } to { opacity: 1; } }
    .agent-picker-card {
      width: min(420px, calc(100vw - 32px));
      max-height: calc(100vh - 64px);
      background: var(--bg-primary, #fff);
      color: var(--text-primary, #1f2937);
      border-radius: 16px;
      border: 1px solid var(--border-color, #e5e7eb);
      box-shadow: 0 20px 50px rgba(15, 23, 42, 0.25);
      padding: 18px 20px;
      display: flex;
      flex-direction: column;
      gap: 12px;
      overflow: hidden;
    }
    .agent-picker-title {
      font-size: 1rem;
      font-weight: 700;
      margin: 0;
    }
    .agent-picker-message {
      margin: 0;
      font-size: 0.9rem;
      line-height: 1.5;
      color: var(--text-secondary, #4b5563);
    }
    .agent-picker-options {
      display: flex;
      flex-direction: column;
      gap: 6px;
      max-height: 260px;
      overflow-y: auto;
      padding-right: 4px;
    }
    .agent-picker-option {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 8px 12px;
      border: 1px solid var(--border-color, #e5e7eb);
      border-radius: 10px;
      background: var(--bg-secondary, #f9fafb);
      color: inherit;
      font: inherit;
      text-align: left;
      cursor: pointer;
      transition: border-color 0.14s ease, background 0.14s ease, transform 0.14s ease;
    }
    .agent-picker-option:hover {
      border-color: color-mix(in srgb, var(--primary-color, #2563eb) 42%, var(--border-color, #e5e7eb));
      background: color-mix(in srgb, var(--primary-color, #2563eb) 6%, var(--bg-secondary, #f9fafb));
    }
    .agent-picker-option:focus-visible {
      outline: 2px solid color-mix(in srgb, var(--primary-color, #2563eb) 54%, transparent);
      outline-offset: 2px;
    }
    .agent-picker-option-name {
      font-weight: 600;
    }
    .agent-picker-option-instance {
      font-size: 0.78rem;
      color: var(--text-muted, #6b7280);
    }
    .agent-picker-actions {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
      margin-top: 4px;
    }
    .agent-picker-btn {
      padding: 8px 14px;
      border-radius: 999px;
      font-size: 0.85rem;
      font-weight: 700;
      cursor: pointer;
      border: 1px solid var(--border-color, #e5e7eb);
      background: var(--bg-secondary, #f9fafb);
      color: inherit;
    }
    .agent-picker-btn-cancel:hover {
      border-color: color-mix(in srgb, var(--primary-color, #2563eb) 38%, var(--border-color, #e5e7eb));
    }
  `;
  document.head.appendChild(style);
}

function mountDialog({ render }) {
  ensureDialogStyles();

  return new Promise(resolve => {
    const backdrop = document.createElement('div');
    backdrop.className = 'agent-picker-backdrop';
    backdrop.setAttribute('role', 'dialog');
    backdrop.setAttribute('aria-modal', 'true');

    const card = document.createElement('div');
    card.className = 'agent-picker-card';
    backdrop.appendChild(card);

    let resolved = false;
    const finish = value => {
      if (resolved) return;
      resolved = true;
      document.removeEventListener('keydown', onKey);
      backdrop.remove();
      resolve(value);
    };

    const onKey = event => {
      if (event.key === 'Escape') {
        event.preventDefault();
        finish(null);
      }
    };
    document.addEventListener('keydown', onKey);

    backdrop.addEventListener('click', event => {
      if (event.target === backdrop) finish(null);
    });

    render(card, finish);
    document.body.appendChild(backdrop);

    // Focus the first interactive element so keyboard users land in the
    // dialog without an extra Tab.
    const focusTarget = card.querySelector(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    );
    if (focusTarget) focusTarget.focus();
  });
}

/**
 * Show a clickable list of agents and return the picked entry, or null
 * when the user cancels. Each agent option renders its display name plus
 * its instance suffix when present.
 */
export function showAgentPicker({ title, message, agents } = {}) {
  if (!Array.isArray(agents) || agents.length === 0) {
    return Promise.resolve(null);
  }
  return mountDialog({
    render: (card, finish) => {
      const optionsHtml = agents
        .map((agent, index) => {
          const name = ESCAPE_HTML(agent?.name || `Agent ${index + 1}`);
          const instance = agent?.instanceNumber
            ? `<span class="agent-picker-option-instance">#${ESCAPE_HTML(agent.instanceNumber)}</span>`
            : '';
          return `
          <button type="button" class="agent-picker-option" data-agent-picker-index="${index}">
            <span class="agent-picker-option-name">${name}</span>
            ${instance}
          </button>
        `;
        })
        .join('');
      card.innerHTML = `
        <h3 class="agent-picker-title">${ESCAPE_HTML(title || 'Pick an agent')}</h3>
        ${message ? `<p class="agent-picker-message">${ESCAPE_HTML(message)}</p>` : ''}
        <div class="agent-picker-options" role="listbox">${optionsHtml}</div>
        <div class="agent-picker-actions">
          <button type="button" class="agent-picker-btn agent-picker-btn-cancel" data-agent-picker-action="cancel">Cancel</button>
        </div>
      `;
      card.querySelectorAll('[data-agent-picker-index]').forEach(btn => {
        btn.addEventListener('click', () => {
          const idx = Number.parseInt(btn.getAttribute('data-agent-picker-index') || '-1', 10);
          if (idx >= 0 && idx < agents.length) {
            finish(agents[idx]);
          } else {
            finish(null);
          }
        });
      });
      card
        .querySelector('[data-agent-picker-action="cancel"]')
        .addEventListener('click', () => finish(null));
    }
  });
}
