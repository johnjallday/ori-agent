/**
 * EventBus - Centralized pub/sub system for Ori Agent
 *
 * Provides decoupled communication between modules with:
 * - Subscribe/publish pattern
 * - Wildcard event matching
 * - One-time event listeners
 * - Event namespacing
 * - Event history for debugging
 *
 * Usage:
 *   // Subscribe to events
 *   EventBus.on('agent:switched', (data) => {
 *     console.log('Agent changed to:', data.name);
 *   });
 *
 *   // Publish events
 *   EventBus.emit('agent:switched', { name: 'my-agent' });
 *
 *   // One-time listener
 *   EventBus.once('init:complete', () => {
 *     console.log('Initialization done');
 *   });
 *
 *   // Wildcard subscription
 *   EventBus.on('agent:*', (data, eventName) => {
 *     console.log('Agent event:', eventName, data);
 *   });
 *
 *   // Namespaced events for easy cleanup
 *   EventBus.on('chat:message', handler, 'chatModule');
 *   EventBus.offAll('chatModule');  // Remove all handlers from namespace
 */

const EventBus = (function() {
  'use strict';

  // Event listeners storage
  // Map<eventName, Set<{handler, namespace, once}>>
  const listeners = new Map();

  // Wildcard listeners (e.g., 'agent:*')
  const wildcardListeners = new Map();

  // Event history for debugging
  const eventHistory = [];
  const MAX_HISTORY = 50;

  // Configuration
  const config = {
    debug: false,
    recordHistory: true
  };

  /**
   * Check if event name matches a pattern (supports wildcards)
   */
  function matchesPattern(eventName, pattern) {
    if (pattern === '*') return true;
    if (!pattern.includes('*')) return eventName === pattern;

    // Convert pattern to regex
    const regexPattern = pattern
      .replace(/[.+?^${}()|[\]\\]/g, '\\$&')
      .replace(/\*/g, '.*');

    return new RegExp(`^${regexPattern}$`).test(eventName);
  }

  /**
   * Get all matching handlers for an event
   */
  function getHandlers(eventName) {
    const handlers = [];

    // Exact match listeners
    if (listeners.has(eventName)) {
      listeners.get(eventName).forEach(listener => {
        handlers.push({ ...listener, exact: true });
      });
    }

    // Wildcard listeners
    wildcardListeners.forEach((listenerSet, pattern) => {
      if (matchesPattern(eventName, pattern)) {
        listenerSet.forEach(listener => {
          handlers.push({ ...listener, pattern, exact: false });
        });
      }
    });

    return handlers;
  }

  /**
   * Store event in history
   */
  function recordEvent(eventName, data, handlerCount) {
    if (!config.recordHistory) return;

    eventHistory.push({
      timestamp: new Date().toISOString(),
      event: eventName,
      data,
      handlerCount
    });

    // Trim history
    if (eventHistory.length > MAX_HISTORY) {
      eventHistory.shift();
    }
  }

  /**
   * Subscribe to an event
   * @param {string} eventName - Event name (supports wildcards like 'agent:*')
   * @param {function} handler - Callback function
   * @param {string} namespace - Optional namespace for grouping handlers
   * @returns {function} Unsubscribe function
   */
  function on(eventName, handler, namespace = null) {
    if (typeof handler !== 'function') {
      throw new Error('EventBus: handler must be a function');
    }

    const listener = { handler, namespace, once: false };
    const isWildcard = eventName.includes('*');
    const targetMap = isWildcard ? wildcardListeners : listeners;

    if (!targetMap.has(eventName)) {
      targetMap.set(eventName, new Set());
    }

    targetMap.get(eventName).add(listener);

    if (config.debug) {
      console.debug(`[EventBus] Subscribed to "${eventName}"${namespace ? ` (${namespace})` : ''}`);
    }

    // Return unsubscribe function
    return () => off(eventName, handler);
  }

  /**
   * Subscribe to an event (one-time)
   */
  function once(eventName, handler, namespace = null) {
    if (typeof handler !== 'function') {
      throw new Error('EventBus: handler must be a function');
    }

    const listener = { handler, namespace, once: true };
    const isWildcard = eventName.includes('*');
    const targetMap = isWildcard ? wildcardListeners : listeners;

    if (!targetMap.has(eventName)) {
      targetMap.set(eventName, new Set());
    }

    targetMap.get(eventName).add(listener);

    return () => off(eventName, handler);
  }

  /**
   * Unsubscribe from an event
   */
  function off(eventName, handler) {
    const isWildcard = eventName.includes('*');
    const targetMap = isWildcard ? wildcardListeners : listeners;

    if (!targetMap.has(eventName)) return false;

    const listenerSet = targetMap.get(eventName);
    let removed = false;

    listenerSet.forEach(listener => {
      if (listener.handler === handler) {
        listenerSet.delete(listener);
        removed = true;
      }
    });

    // Clean up empty sets
    if (listenerSet.size === 0) {
      targetMap.delete(eventName);
    }

    if (config.debug && removed) {
      console.debug(`[EventBus] Unsubscribed from "${eventName}"`);
    }

    return removed;
  }

  /**
   * Remove all handlers for a namespace
   */
  function offAll(namespace) {
    let count = 0;

    [listeners, wildcardListeners].forEach(targetMap => {
      targetMap.forEach((listenerSet, eventName) => {
        listenerSet.forEach(listener => {
          if (listener.namespace === namespace) {
            listenerSet.delete(listener);
            count++;
          }
        });

        if (listenerSet.size === 0) {
          targetMap.delete(eventName);
        }
      });
    });

    if (config.debug && count > 0) {
      console.debug(`[EventBus] Removed ${count} handlers from namespace "${namespace}"`);
    }

    return count;
  }

  /**
   * Emit an event
   * @param {string} eventName - Event name
   * @param {*} data - Event data
   * @returns {number} Number of handlers called
   */
  function emit(eventName, data) {
    const handlers = getHandlers(eventName);
    const toRemove = [];

    handlers.forEach(({ handler, once, exact, pattern }) => {
      try {
        // Pass event name as second arg for wildcard handlers
        handler(data, eventName);
      } catch (error) {
        console.error(`[EventBus] Error in handler for "${eventName}":`, error);
      }

      // Mark one-time handlers for removal
      if (once) {
        toRemove.push({ handler, exact, pattern, eventName });
      }
    });

    // Remove one-time handlers
    toRemove.forEach(({ handler, exact, pattern, eventName: evtName }) => {
      const key = exact ? evtName : pattern;
      const targetMap = exact ? listeners : wildcardListeners;

      if (targetMap.has(key)) {
        const listenerSet = targetMap.get(key);
        listenerSet.forEach(listener => {
          if (listener.handler === handler) {
            listenerSet.delete(listener);
          }
        });
      }
    });

    // Record event
    recordEvent(eventName, data, handlers.length);

    if (config.debug) {
      console.debug(`[EventBus] Emitted "${eventName}" to ${handlers.length} handlers`, data);
    }

    return handlers.length;
  }

  /**
   * Emit an event asynchronously (non-blocking)
   */
  function emitAsync(eventName, data) {
    return new Promise(resolve => {
      setTimeout(() => {
        const count = emit(eventName, data);
        resolve(count);
      }, 0);
    });
  }

  /**
   * Wait for an event to be emitted
   * @param {string} eventName - Event to wait for
   * @param {number} timeout - Optional timeout in ms
   * @returns {Promise} Resolves with event data
   */
  function waitFor(eventName, timeout = 0) {
    return new Promise((resolve, reject) => {
      let timeoutId;

      const unsubscribe = once(eventName, (data) => {
        if (timeoutId) clearTimeout(timeoutId);
        resolve(data);
      });

      if (timeout > 0) {
        timeoutId = setTimeout(() => {
          unsubscribe();
          reject(new Error(`EventBus: Timeout waiting for "${eventName}"`));
        }, timeout);
      }
    });
  }

  /**
   * Check if there are listeners for an event
   */
  function hasListeners(eventName) {
    return getHandlers(eventName).length > 0;
  }

  /**
   * Get count of listeners for an event
   */
  function listenerCount(eventName) {
    return getHandlers(eventName).length;
  }

  /**
   * Get all registered event names
   */
  function eventNames() {
    const names = new Set();
    listeners.forEach((_, name) => names.add(name));
    wildcardListeners.forEach((_, pattern) => names.add(pattern));
    return Array.from(names);
  }

  /**
   * Get event history
   */
  function getHistory(filter = {}) {
    let filtered = [...eventHistory];

    if (filter.event) {
      filtered = filtered.filter(entry =>
        matchesPattern(entry.event, filter.event)
      );
    }

    if (filter.since) {
      const sinceTime = new Date(filter.since).getTime();
      filtered = filtered.filter(entry =>
        new Date(entry.timestamp).getTime() >= sinceTime
      );
    }

    return filtered;
  }

  /**
   * Clear event history
   */
  function clearHistory() {
    eventHistory.length = 0;
  }

  /**
   * Clear all listeners
   */
  function clear() {
    listeners.clear();
    wildcardListeners.clear();

    if (config.debug) {
      console.debug('[EventBus] All listeners cleared');
    }
  }

  /**
   * Configure EventBus
   */
  function configure(options) {
    Object.assign(config, options);
  }

  /**
   * Enable debug mode
   */
  function setDebug(enabled) {
    config.debug = enabled;
  }

  // Public API
  return {
    // Core methods
    on,
    once,
    off,
    offAll,
    emit,
    emitAsync,

    // Utilities
    waitFor,
    hasListeners,
    listenerCount,
    eventNames,

    // History
    getHistory,
    clearHistory,

    // Management
    clear,
    configure,
    setDebug
  };
})();

// Export for ES modules (if supported)
if (typeof module !== 'undefined' && module.exports) {
  module.exports = EventBus;
}
