/**
 * Shared Navbar Component
 * Creates a consistent navbar across all pages
 */

function createNavbar(activePage = '') {
    const navbar = document.createElement('nav');
    navbar.className = 'navbar navbar-expand-lg glassmorphism';
    navbar.style.cssText = 'z-index: 1030; backdrop-filter: blur(10px); border-bottom: 1px solid var(--border-color);';

    navbar.innerHTML = `
        <div class="container-fluid px-4">
            <div class="d-flex align-items-center w-100">
                <button id="sidebarToggle" class="modern-btn modern-btn-secondary me-3" title="Toggle Sidebar">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M3 18h18v-2H3v2zm0-5h18v-2H3v2zm0-7v2h18V6H3z"/>
                    </svg>
                </button>

                <a class="navbar-brand fw-bold d-flex align-items-center" href="/" style="color: var(--primary-color);">
                    <svg width="24" height="24" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                        <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-2 15l-5-5 1.41-1.41L10 14.17l7.59-7.59L19 8l-9 9z"/>
                    </svg>
                    Ori Agent
                </a>

                <div class="d-flex align-items-center gap-2 ms-4">
                    <a href="/agents.html" class="nav-link-item ${activePage === 'agents' ? 'active' : ''}" style="color: var(--text-primary); text-decoration: none; padding: 0.5rem 1rem; border-radius: var(--radius-md); transition: background 0.2s;">Agents</a>
                    <span style="color: var(--text-muted);">|</span>
                    <a href="/marketplace" class="nav-link-item ${activePage === 'marketplace' ? 'active' : ''}" style="color: var(--text-primary); text-decoration: none; padding: 0.5rem 1rem; border-radius: var(--radius-md); transition: background 0.2s;">Plugins</a>
                    <span style="color: var(--text-muted);">|</span>
                    <a href="/studios" class="nav-link-item ${activePage === 'studios' ? 'active' : ''}" style="color: var(--text-primary); text-decoration: none; padding: 0.5rem 1rem; border-radius: var(--radius-md); transition: background 0.2s;">Studio</a>
                </div>

                <div class="d-flex align-items-center gap-3 ms-auto">
                    <button id="darkModeToggle" class="modern-btn modern-btn-secondary">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
                            <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79Z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                        </svg>
                        <span>Dark</span>
                    </button>
                </div>
            </div>
        </div>
    `;
    return navbar;
}

/**
 * Auto-detect active page and create navbar
 */
function initNavbar() {
    const path = window.location.pathname;
    let activePage = '';

    if (path.includes('/agents')) {
        activePage = 'agents';
    } else if (path.includes('/marketplace')) {
        activePage = 'marketplace';
    } else if (path.includes('/studios')) {
        activePage = 'studios';
    }

    return createNavbar(activePage);
}

/**
 * Insert navbar into the page
 * Call this with a CSS selector where the navbar should be inserted
 */
function insertNavbar(targetSelector) {
    const target = document.querySelector(targetSelector);
    if (target) {
        const navbar = initNavbar();
        target.appendChild(navbar);
    } else {
        console.error('Navbar target not found:', targetSelector);
    }
}

// Auto-insert if there's a #navbar-container element
document.addEventListener('DOMContentLoaded', function() {
    const container = document.getElementById('navbar-container');
    if (container) {
        const navbar = initNavbar();
        container.appendChild(navbar);
    }
});
