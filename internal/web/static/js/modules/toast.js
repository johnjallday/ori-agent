// Toast Notification Module
// Provides user feedback for success, error, warning, and info messages

const Toast = (function() {
  let container = null;
  
  const maxToasts = 5;

  // Initialize toast container
  function init() {
    if (container) return;

    container = document.getElementById('toastContainer');
    if (!container) {
      container = document.createElement('div');
      container.id = 'toastContainer';
      container.className = 'toast-container';
      container.setAttribute('aria-live', 'polite');
      container.setAttribute('aria-atomic', 'true');
      document.body.appendChild(container);
    }
    // Ensure toast stack is always above modal stacks.
    container.style.zIndex = '2147483647';
    container.style.position = 'fixed';
    container.style.right = '20px';
    container.style.bottom = '20px';
  }

  // Get icon SVG based on type
  function getIcon(type) {
    const icons = {
      success: `<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
        <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-2 15l-5-5 1.41-1.41L10 14.17l7.59-7.59L19 8l-9 9z"/>
      </svg>`,
      error: `<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
        <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z"/>
      </svg>`,
      warning: `<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
        <path d="M1 21h22L12 2 1 21zm12-3h-2v-2h2v2zm0-4h-2v-4h2v4z"/>
      </svg>`,
      info: `<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
        <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-6h2v6zm0-8h-2V7h2v2z"/>
      </svg>`
    };
    return icons[type] || icons.info;
  }

  // Create toast element
  function createToast(message, type, options = {}) {
    const toast = document.createElement('div');
    // Keep Bootstrap from auto-hiding `.toast:not(.show)` while using custom styles.
    toast.className = `toast show toast-${type}`;
    const urgent = type === 'error';
    toast.setAttribute('role', urgent ? 'alert' : 'status');
    toast.setAttribute('aria-live', urgent ? 'assertive' : 'polite');
    toast.setAttribute('aria-atomic', 'true');

    const icon = getIcon(type);
    const title = options.title || type.charAt(0).toUpperCase() + type.slice(1);

    // Optional inline action (e.g. "Undo"). Rendered as a button the caller
    // wires up in show(); see options.action = { label, onClick }.
    const actionHtml = options.action && options.action.label
      ? `<button type="button" class="toast-action">${escapeHtml(options.action.label)}</button>`
      : '';

    toast.innerHTML = `
      <div class="toast-icon">${icon}</div>
      <div class="toast-content">
        <div class="toast-title">${escapeHtml(title)}</div>
        <div class="toast-message">${escapeHtml(message)}</div>
      </div>
      ${actionHtml}
      <button type="button" class="toast-close" aria-label="Close notification">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
          <path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/>
        </svg>
      </button>
      <div class="toast-progress"></div>
    `;

    return toast;
  }

  // Escape HTML to prevent XSS
  function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  // Show toast notification
  function show(message, type = 'info', options = {}) {
    init();

    const duration = options.duration || 5000;
    const toast = createToast(message, type, options);

    // Remove oldest toast if at max
    while (container.children.length >= maxToasts) {
      const oldest = container.firstChild;
      if (oldest) {
        oldest.classList.add('toast-exit');
        setTimeout(() => oldest.remove(), 300);
      }
    }

    // Add toast to container
    container.appendChild(toast);

    // Trigger entrance animation
    requestAnimationFrame(() => {
      toast.classList.add('toast-enter');
    });

    // Setup close button
    const closeBtn = toast.querySelector('.toast-close');
    closeBtn.addEventListener('click', () => dismiss(toast));

    // Setup optional action button: run the callback, then dismiss.
    if (options.action && typeof options.action.onClick === 'function') {
      const actionBtn = toast.querySelector('.toast-action');
      if (actionBtn) {
        actionBtn.addEventListener('click', () => {
          try {
            options.action.onClick();
          } finally {
            dismiss(toast);
          }
        });
      }
    }

    // Setup progress bar animation
    const progress = toast.querySelector('.toast-progress');
    progress.style.animationDuration = `${duration}ms`;

    // Auto-dismiss after duration
    const timeout = setTimeout(() => dismiss(toast), duration);

    // Pause on hover
    toast.addEventListener('mouseenter', () => {
      progress.style.animationPlayState = 'paused';
      clearTimeout(timeout);
    });

    toast.addEventListener('mouseleave', () => {
      progress.style.animationPlayState = 'running';
      // Restart timeout with remaining time (simplified: restart full duration)
      setTimeout(() => dismiss(toast), duration);
    });

    return toast;
  }

  // Dismiss a toast
  function dismiss(toast) {
    if (!toast || !toast.parentNode) return;

    toast.classList.add('toast-exit');
    setTimeout(() => {
      if (toast.parentNode) {
        toast.remove();
      }
    }, 300);
  }

  // Dismiss all toasts
  function dismissAll() {
    if (!container) return;

    Array.from(container.children).forEach(toast => dismiss(toast));
  }

  // Convenience methods
  function success(message, options = {}) {
    return show(message, 'success', options);
  }

  function error(message, options = {}) {
    return show(message, 'error', { duration: 7000, ...options });
  }

  function warning(message, options = {}) {
    return show(message, 'warning', options);
  }

  function info(message, options = {}) {
    return show(message, 'info', options);
  }

  // Public API
  return {
    init,
    show,
    success,
    error,
    warning,
    info,
    dismiss,
    dismissAll
  };
})();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => Toast.init());

// Export for use in other modules
if (typeof window !== 'undefined') {
  window.Toast = Toast;
  window.notifyToast = function notifyToast(message, type = 'info', options = {}) {
    const normalizedType = String(type || 'info').toLowerCase();
    const toastType = normalizedType === 'danger' ? 'error' : normalizedType === 'warn' ? 'warning' : normalizedType;

    if (window.Toast) {
      const toastFn = window.Toast[toastType];
      if (typeof toastFn === 'function') {
        toastFn(message, options);
        return;
      }
      if (typeof window.Toast.show === 'function') {
        window.Toast.show(message, toastType, options);
        return;
      }
    }

    // Fallback if toast module is unavailable for any reason.
    const fallback = document.createElement('div');
    fallback.textContent = String(message || '');
    fallback.style.cssText = [
      'position:fixed',
      'top:16px',
      'right:16px',
      'z-index:2147483647',
      'background:#111827',
      'color:#f9fafb',
      'padding:10px 12px',
      'border-radius:8px',
      'font-size:13px',
      'box-shadow:0 8px 20px rgba(0,0,0,0.35)',
      'max-width:360px'
    ].join(';');
    document.body.appendChild(fallback);
    setTimeout(() => {
      fallback.remove();
    }, 2200);
  };
}
