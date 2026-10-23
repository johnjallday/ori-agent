// Navbar offset
// Keeps --navbar-offset equal to the navbar's rendered height, so panels that
// sit below it (Home's drawers, the workspace command view, keyboard
// navigation, the Daily Brief) follow it when it wraps to two rows on narrow
// screens. variables.css supplies the 76px fallback until the first measure.
(function () {
  function syncNavbarOffset() {
    const root = document.documentElement;
    const navbar = document.querySelector('.navbar');

    if (!root || !navbar) {
      return;
    }

    let frameId = null;

    const updateOffset = () => {
      frameId = null;
      const navbarHeight = Math.max(76, Math.ceil(navbar.getBoundingClientRect().height));
      root.style.setProperty('--navbar-offset', `${navbarHeight}px`);
    };

    const scheduleOffsetUpdate = () => {
      if (frameId !== null) {
        cancelAnimationFrame(frameId);
      }
      frameId = requestAnimationFrame(updateOffset);
    };

    scheduleOffsetUpdate();
    window.addEventListener('load', scheduleOffsetUpdate, { once: true });
    window.addEventListener('resize', scheduleOffsetUpdate);

    if (typeof ResizeObserver === 'function') {
      const navbarObserver = new ResizeObserver(scheduleOffsetUpdate);
      navbarObserver.observe(navbar);
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', syncNavbarOffset);
  } else {
    syncNavbarOffset();
  }
})();
