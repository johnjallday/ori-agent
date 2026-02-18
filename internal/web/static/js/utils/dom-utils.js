/**
 * DOM Utilities - Shared helper functions for DOM manipulation
 *
 * Provides safe DOM operations including:
 * - HTML escaping to prevent XSS
 * - Safe element creation
 *
 * Usage:
 *   // Escape HTML to prevent XSS
 *   const safe = DOMUtils.escapeHtml(userInput);
 *   element.innerHTML = `<span>${safe}</span>`;
 *
 *   // Or use the global shorthand
 *   const safe = escapeHtml(userInput);
 */

const DOMUtils = (function() {
  'use strict';

  /**
   * Escape HTML special characters to prevent XSS attacks
   * @param {string} text - The text to escape
   * @returns {string} The escaped text safe for innerHTML
   */
  function escapeHtml(text) {
    if (text === null || text === undefined) {
      return '';
    }
    const div = document.createElement('div');
    div.textContent = String(text);
    return div.innerHTML;
  }

  /**
   * Escape a string for use in HTML attributes
   * @param {string} text - The text to escape
   * @returns {string} The escaped text safe for attributes
   */
  function escapeAttr(text) {
    if (text === null || text === undefined) {
      return '';
    }
    return String(text)
      .replace(/&/g, '&amp;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }

  /**
   * Escape a string for use in JavaScript string literals
   * Useful when embedding data in onclick handlers
   * @param {string} text - The text to escape
   * @returns {string} The escaped text safe for JS strings
   */
  function escapeJs(text) {
    if (text === null || text === undefined) {
      return '';
    }
    return String(text)
      .replace(/\\/g, '\\\\')
      .replace(/'/g, "\\'")
      .replace(/"/g, '\\"')
      .replace(/\n/g, '\\n')
      .replace(/\r/g, '\\r');
  }

  /**
   * Strip version suffix from plugin names (e.g., "name-0.0.1" -> "name")
   * @param {string} name - The plugin name
   * @returns {string} The name without version suffix
   */
  function stripVersionSuffix(name = '') {
    if (!name) return '';
    return name.replace(/-\d+\.\d+\.\d+(?:[-+][\w.]+)?$/, '');
  }

  // Public API
  return {
    escapeHtml,
    escapeAttr,
    escapeJs,
    stripVersionSuffix
  };
})();

// Expose escapeHtml globally for convenience (most common use case)
// This provides backward compatibility with existing code
const escapeHtml = DOMUtils.escapeHtml;
const escapeAttr = DOMUtils.escapeAttr;
const escapeJs = DOMUtils.escapeJs;
const stripVersionSuffix = DOMUtils.stripVersionSuffix;

// Export for ES modules (if supported)
if (typeof module !== 'undefined' && module.exports) {
  module.exports = DOMUtils;
}
