/**
 * Logger - Centralized logging utility for Ori Agent
 *
 * Provides consistent logging with:
 * - Log levels (DEBUG, INFO, WARN, ERROR)
 * - Configurable output (console, custom handlers)
 * - Contextual logging with tags
 * - Production mode to suppress debug logs
 * - Structured log format
 *
 * Usage:
 *   // Basic logging
 *   Logger.debug('Loading agents...');
 *   Logger.info('Agent created', { name: 'my-agent' });
 *   Logger.warn('Deprecated API used');
 *   Logger.error('Failed to load', error);
 *
 *   // With context
 *   const log = Logger.withContext('PluginManager');
 *   log.info('Plugin loaded');  // [PluginManager] Plugin loaded
 *
 *   // Configure log level
 *   Logger.setLevel(Logger.LEVELS.WARN);  // Only show warnings and errors
 */

const Logger = (function() {
  'use strict';

  // Log levels
  const LEVELS = {
    DEBUG: 0,
    INFO: 1,
    WARN: 2,
    ERROR: 3,
    NONE: 4
  };

  // Level names for display
  const LEVEL_NAMES = {
    [LEVELS.DEBUG]: 'DEBUG',
    [LEVELS.INFO]: 'INFO',
    [LEVELS.WARN]: 'WARN',
    [LEVELS.ERROR]: 'ERROR'
  };

  // Level styles for console
  const LEVEL_STYLES = {
    [LEVELS.DEBUG]: 'color: #6c757d',
    [LEVELS.INFO]: 'color: #0d6efd',
    [LEVELS.WARN]: 'color: #ffc107; font-weight: bold',
    [LEVELS.ERROR]: 'color: #dc3545; font-weight: bold'
  };

  // Current configuration
  const config = {
    level: LEVELS.DEBUG,
    enableTimestamp: true,
    enableContext: true,
    handlers: []
  };

  // Log history for debugging
  const logHistory = [];
  const MAX_HISTORY = 100;

  /**
   * Format timestamp
   */
  function formatTimestamp() {
    const now = new Date();
    return now.toISOString().slice(11, 23); // HH:mm:ss.SSS
  }

  /**
   * Format log entry
   */
  function formatEntry(level, context, message, data) {
    const parts = [];

    if (config.enableTimestamp) {
      parts.push(`[${formatTimestamp()}]`);
    }

    parts.push(`[${LEVEL_NAMES[level]}]`);

    if (context && config.enableContext) {
      parts.push(`[${context}]`);
    }

    parts.push(message);

    return parts.join(' ');
  }

  /**
   * Store log entry in history
   */
  function storeInHistory(level, context, message, data) {
    const entry = {
      timestamp: new Date().toISOString(),
      level: LEVEL_NAMES[level],
      context,
      message,
      data
    };

    logHistory.push(entry);

    // Trim history if too large
    if (logHistory.length > MAX_HISTORY) {
      logHistory.shift();
    }
  }

  /**
   * Call custom handlers
   */
  function callHandlers(level, context, message, data) {
    config.handlers.forEach(handler => {
      try {
        handler({
          level: LEVEL_NAMES[level],
          levelValue: level,
          context,
          message,
          data,
          timestamp: new Date().toISOString()
        });
      } catch (error) {
        console.error('Logger handler error:', error);
      }
    });
  }

  /**
   * Core log function
   */
  function log(level, context, message, data) {
    // Skip if below current level
    if (level < config.level) {
      return;
    }

    // Store in history
    storeInHistory(level, context, message, data);

    // Call custom handlers
    callHandlers(level, context, message, data);

    // Format the message
    const formatted = formatEntry(level, context, message, data);

    // Output to console with appropriate method
    const style = LEVEL_STYLES[level];

    switch (level) {
      case LEVELS.DEBUG:
        if (data !== undefined) {
          console.debug(`%c${formatted}`, style, data);
        } else {
          console.debug(`%c${formatted}`, style);
        }
        break;

      case LEVELS.INFO:
        if (data !== undefined) {
          console.info(`%c${formatted}`, style, data);
        } else {
          console.info(`%c${formatted}`, style);
        }
        break;

      case LEVELS.WARN:
        if (data !== undefined) {
          console.warn(`%c${formatted}`, style, data);
        } else {
          console.warn(`%c${formatted}`, style);
        }
        break;

      case LEVELS.ERROR:
        if (data !== undefined) {
          console.error(`%c${formatted}`, style, data);
        } else {
          console.error(`%c${formatted}`, style);
        }
        break;
    }
  }

  /**
   * Create a logger with a fixed context
   */
  function withContext(context) {
    return {
      debug: (message, data) => log(LEVELS.DEBUG, context, message, data),
      info: (message, data) => log(LEVELS.INFO, context, message, data),
      warn: (message, data) => log(LEVELS.WARN, context, message, data),
      error: (message, data) => log(LEVELS.ERROR, context, message, data),
      log: (level, message, data) => log(level, context, message, data)
    };
  }

  /**
   * Set the minimum log level
   */
  function setLevel(level) {
    if (typeof level === 'string') {
      level = LEVELS[level.toUpperCase()] ?? LEVELS.DEBUG;
    }
    config.level = level;
  }

  /**
   * Get current log level
   */
  function getLevel() {
    return config.level;
  }

  /**
   * Enable/disable timestamps
   */
  function enableTimestamp(enabled) {
    config.enableTimestamp = enabled;
  }

  /**
   * Enable/disable context display
   */
  function enableContext(enabled) {
    config.enableContext = enabled;
  }

  /**
   * Add a custom log handler
   * Handler receives: { level, levelValue, context, message, data, timestamp }
   */
  function addHandler(handler) {
    config.handlers.push(handler);
    return () => {
      const index = config.handlers.indexOf(handler);
      if (index > -1) {
        config.handlers.splice(index, 1);
      }
    };
  }

  /**
   * Get log history
   */
  function getHistory(filter = {}) {
    let filtered = [...logHistory];

    if (filter.level !== undefined) {
      const minLevel = typeof filter.level === 'string'
        ? LEVELS[filter.level.toUpperCase()]
        : filter.level;
      filtered = filtered.filter(entry =>
        LEVELS[entry.level] >= minLevel
      );
    }

    if (filter.context) {
      filtered = filtered.filter(entry =>
        entry.context === filter.context
      );
    }

    if (filter.search) {
      const searchLower = filter.search.toLowerCase();
      filtered = filtered.filter(entry =>
        entry.message.toLowerCase().includes(searchLower)
      );
    }

    return filtered;
  }

  /**
   * Clear log history
   */
  function clearHistory() {
    logHistory.length = 0;
  }

  /**
   * Set production mode (only errors)
   */
  function setProductionMode(enabled = true) {
    config.level = enabled ? LEVELS.ERROR : LEVELS.DEBUG;
  }

  /**
   * Create a timer for performance logging
   */
  function time(label) {
    const start = performance.now();
    return {
      end: (message) => {
        const duration = performance.now() - start;
        log(LEVELS.DEBUG, 'Timer', `${label}: ${message || 'completed'} (${duration.toFixed(2)}ms)`);
        return duration;
      }
    };
  }

  /**
   * Group related logs
   */
  function group(label, fn) {
    console.group(label);
    try {
      const result = fn();
      if (result instanceof Promise) {
        return result.finally(() => console.groupEnd());
      }
      console.groupEnd();
      return result;
    } catch (error) {
      console.groupEnd();
      throw error;
    }
  }

  /**
   * Table output for structured data
   */
  function table(data, columns) {
    if (config.level > LEVELS.DEBUG) return;
    console.table(data, columns);
  }

  // Public API
  return {
    // Log levels
    LEVELS,

    // Core logging
    debug: (message, data) => log(LEVELS.DEBUG, null, message, data),
    info: (message, data) => log(LEVELS.INFO, null, message, data),
    warn: (message, data) => log(LEVELS.WARN, null, message, data),
    error: (message, data) => log(LEVELS.ERROR, null, message, data),
    log: (level, message, data) => log(level, null, message, data),

    // Context-aware logging
    withContext,

    // Configuration
    setLevel,
    getLevel,
    enableTimestamp,
    enableContext,
    addHandler,
    setProductionMode,

    // History
    getHistory,
    clearHistory,

    // Utilities
    time,
    group,
    table
  };
})();

// Export for ES modules (if supported)
if (typeof module !== 'undefined' && module.exports) {
  module.exports = Logger;
}
