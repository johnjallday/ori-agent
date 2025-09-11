/**
 * Icon utility for loading and managing SVG icons
 */

'use strict';

/**
 * Icon cache to avoid repeated requests
 */
const iconCache = new Map();

/**
 * Icon utility class
 */
class Icons {
  /**
   * Load an SVG icon from the icons directory
   * @param {string} iconName - Name of the icon (without .svg extension)
   * @param {Object} options - Options for the icon
   * @param {string} options.size - Size of the icon (default: '16')
   * @param {string} options.className - CSS class to add to the icon
   * @returns {Promise<string>} SVG markup
   */
  static async load(iconName, options = {}) {
    const { size = '16', className = '' } = options;
    
    // Check cache first
    const cacheKey = `${iconName}-${size}`;
    if (iconCache.has(cacheKey)) {
      const cachedSvg = iconCache.get(cacheKey);
      return className ? cachedSvg.replace('<svg', `<svg class="${className}"`) : cachedSvg;
    }
    
    try {
      const response = await fetch(`/icons/${iconName}.svg`);
      if (!response.ok) {
        throw new Error(`Failed to load icon: ${iconName}`);
      }
      
      let svgContent = await response.text();
      
      // Update size attributes
      svgContent = svgContent.replace(/width="[^"]*"/, `width="${size}"`);
      svgContent = svgContent.replace(/height="[^"]*"/, `height="${size}"`);
      
      // Cache the SVG
      iconCache.set(cacheKey, svgContent);
      
      // Add class if provided
      if (className) {
        svgContent = svgContent.replace('<svg', `<svg class="${className}"`);
      }
      
      return svgContent;
      
    } catch (error) {
      Logger?.warn && Logger.warn(`Failed to load icon: ${iconName}`, error);
      return `<span class="icon-fallback">[${iconName}]</span>`;
    }
  }
  
  /**
   * Create an icon element
   * @param {string} iconName - Name of the icon
   * @param {Object} options - Options for the icon
   * @returns {Promise<Element>} Icon element
   */
  static async createElement(iconName, options = {}) {
    const svgMarkup = await this.load(iconName, options);
    const wrapper = document.createElement('div');
    wrapper.innerHTML = svgMarkup;
    return wrapper.firstElementChild;
  }
  
  /**
   * Replace all data-icon attributes with actual SVG icons
   */
  static async replaceIconPlaceholders() {
    const iconPlaceholders = document.querySelectorAll('[data-icon]');
    
    for (const element of iconPlaceholders) {
      const iconName = element.dataset.icon;
      const size = element.dataset.iconSize || '16';
      const className = element.dataset.iconClass || '';
      
      try {
        const svgMarkup = await this.load(iconName, { size, className });
        element.innerHTML = svgMarkup;
        element.removeAttribute('data-icon');
        element.removeAttribute('data-icon-size');
        element.removeAttribute('data-icon-class');
      } catch (error) {
        Logger?.warn && Logger.warn(`Failed to replace icon placeholder: ${iconName}`, error);
      }
    }
  }
  
  /**
   * Preload commonly used icons
   */
  static async preloadCommonIcons() {
    const commonIcons = [
      'menu', 'close', 'user', 'plugin', 'settings', 
      'plus', 'save', 'refresh', 'dolphin', 'eye'
    ];
    
    const loadPromises = commonIcons.map(icon => this.load(icon));
    await Promise.allSettled(loadPromises);
    
    Logger?.info && Logger.info(`Preloaded ${commonIcons.length} common icons`);
  }
}

// Auto-replace icon placeholders when DOM is loaded
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => {
    Icons.replaceIconPlaceholders();
    Icons.preloadCommonIcons();
  });
} else {
  Icons.replaceIconPlaceholders();
  Icons.preloadCommonIcons();
}

// Export for module systems if needed
if (typeof module !== 'undefined' && module.exports) {
  module.exports = Icons;
}