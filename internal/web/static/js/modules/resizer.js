/**
 * Sidebar Resizer Module
 * Handles draggable sidebar resizing functionality
 */

class SidebarResizer {
    constructor() {
        this.isResizing = false;
        this.sidebarWidth = parseInt(localStorage.getItem('sidebarWidth')) || 300;
        this.minWidth = 250;
        this.maxWidth = 600;
        this.init();
    }

    init() {
        this.bindEvents();
        this.setSidebarWidth(this.sidebarWidth);
    }

    bindEvents() {
        const resizeHandle = document.getElementById('sidebarResizeHandle');
        const sidebar = document.getElementById('sidebar');
        
        if (!resizeHandle || !sidebar) return;

        // Mouse events
        resizeHandle.addEventListener('mousedown', this.startResize.bind(this));
        document.addEventListener('mousemove', this.handleResize.bind(this));
        document.addEventListener('mouseup', this.stopResize.bind(this));

        // Touch events for mobile
        resizeHandle.addEventListener('touchstart', this.startResize.bind(this));
        document.addEventListener('touchmove', this.handleResize.bind(this));
        document.addEventListener('touchend', this.stopResize.bind(this));

        // Prevent text selection during resize
        resizeHandle.addEventListener('selectstart', (e) => e.preventDefault());
    }

    startResize(e) {
        e.preventDefault();
        this.isResizing = true;
        
        // Add resizing class to prevent interaction issues
        document.body.classList.add('sidebar-resizing');
        
        // Store initial mouse position
        this.startX = this.getEventX(e);
        this.startWidth = this.sidebarWidth;
        
        console.log('Started resizing sidebar');
    }

    handleResize(e) {
        if (!this.isResizing) return;
        
        e.preventDefault();
        
        const currentX = this.getEventX(e);
        const deltaX = currentX - this.startX;
        const newWidth = Math.max(this.minWidth, Math.min(this.maxWidth, this.startWidth + deltaX));
        
        this.setSidebarWidth(newWidth);
    }

    stopResize() {
        if (!this.isResizing) return;
        
        this.isResizing = false;
        document.body.classList.remove('sidebar-resizing');
        
        // Save the width to localStorage
        localStorage.setItem('sidebarWidth', this.sidebarWidth.toString());
        
        console.log(`Stopped resizing sidebar at width: ${this.sidebarWidth}px`);
    }

    setSidebarWidth(width) {
        this.sidebarWidth = width;
        
        // Update CSS custom property
        document.documentElement.style.setProperty('--sidebar-width', `${width}px`);
        
        // Apply responsive layout classes based on width
        this.updateResponsiveLayout(width);
        
        // Optional: Dispatch resize event for other components
        window.dispatchEvent(new CustomEvent('sidebarResize', {
            detail: { width: width }
        }));
    }

    updateResponsiveLayout(width) {
        const sidebar = document.getElementById('sidebar');
        if (!sidebar) return;

        // Apply compact layout for narrow sidebars
        if (width <= 300) {
            sidebar.classList.add('sidebar-compact');
        } else {
            sidebar.classList.remove('sidebar-compact');
        }

        // Apply very compact layout for very narrow sidebars
        if (width <= 280) {
            sidebar.classList.add('sidebar-very-compact');
        } else {
            sidebar.classList.remove('sidebar-very-compact');
        }
    }

    getEventX(e) {
        return e.type.includes('touch') ? e.touches[0].clientX : e.clientX;
    }

    // Public methods for programmatic control
    resetWidth() {
        this.setSidebarWidth(300);
        localStorage.setItem('sidebarWidth', '300');
    }

    setWidth(width) {
        const clampedWidth = Math.max(this.minWidth, Math.min(this.maxWidth, width));
        this.setSidebarWidth(clampedWidth);
        localStorage.setItem('sidebarWidth', clampedWidth.toString());
    }

    getWidth() {
        return this.sidebarWidth;
    }
}

// Initialize the resizer when DOM is loaded
document.addEventListener('DOMContentLoaded', () => {
    window.sidebarResizer = new SidebarResizer();
});

// Export for module usage
if (typeof module !== 'undefined' && module.exports) {
    module.exports = SidebarResizer;
}