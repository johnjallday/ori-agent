/**
 * API Client - Centralized HTTP client for Ori Agent
 *
 * Provides a consistent interface for all API calls with:
 * - Automatic JSON parsing
 * - Standardized error handling
 * - Request/response interceptors
 * - Request cancellation support
 * - Retry logic for transient failures
 *
 * Usage:
 *   // Simple GET
 *   const agents = await API.get('/api/agents');
 *
 *   // POST with body
 *   const result = await API.post('/api/agents', { name: 'my-agent' });
 *
 *   // With options
 *   const data = await API.get('/api/chat', {
 *     timeout: 30000,
 *     retries: 3
 *   });
 */

const API = (function () {
  'use strict';

  // Default configuration
  const defaults = {
    baseUrl: '',
    timeout: 30000,
    retries: 0,
    retryDelay: 1000,
    headers: {
      'Content-Type': 'application/json'
    }
  };

  // Request interceptors
  const requestInterceptors = [];

  // Response interceptors
  const responseInterceptors = [];

  // Active requests for cancellation
  const activeRequests = new Map();

  /**
   * Custom API Error class with additional context
   */
  class APIError extends Error {
    constructor(message, status, data, url) {
      super(message);
      this.name = 'APIError';
      this.status = status;
      this.data = data;
      this.url = url;
      this.timestamp = new Date().toISOString();
    }

    /**
     * Check if error is a network/connection error
     */
    isNetworkError() {
      return this.status === 0 || this.status === undefined;
    }

    /**
     * Check if error is a client error (4xx)
     */
    isClientError() {
      return this.status >= 400 && this.status < 500;
    }

    /**
     * Check if error is a server error (5xx)
     */
    isServerError() {
      return this.status >= 500;
    }

    /**
     * Check if request was cancelled
     */
    isCancelled() {
      return this.name === 'AbortError' || this.message.includes('aborted');
    }
  }

  /**
   * Sleep helper for retry delays
   */
  function sleep(ms) {
    return new Promise(resolve => setTimeout(resolve, ms));
  }

  /**
   * Create an AbortController with timeout
   */
  function createAbortController(timeout, requestId) {
    const controller = new AbortController();

    if (timeout > 0) {
      setTimeout(() => {
        controller.abort();
      }, timeout);
    }

    if (requestId) {
      activeRequests.set(requestId, controller);
    }

    return controller;
  }

  /**
   * Run request through interceptors
   */
  async function runRequestInterceptors(config) {
    let currentConfig = { ...config };

    for (const interceptor of requestInterceptors) {
      try {
        currentConfig = (await interceptor(currentConfig)) || currentConfig;
      } catch (error) {
        console.error('Request interceptor error:', error);
      }
    }

    return currentConfig;
  }

  /**
   * Run response through interceptors
   */
  async function runResponseInterceptors(response, config) {
    let currentResponse = response;

    for (const interceptor of responseInterceptors) {
      try {
        currentResponse = (await interceptor(currentResponse, config)) || currentResponse;
      } catch (error) {
        console.error('Response interceptor error:', error);
      }
    }

    return currentResponse;
  }

  /**
   * Parse response based on content type
   */
  async function parseResponse(response) {
    const contentType = response.headers.get('content-type') || '';

    if (contentType.includes('application/json')) {
      try {
        return await response.json();
      } catch {
        return null;
      }
    }

    if (contentType.includes('text/')) {
      return await response.text();
    }

    // For other types, return the response for manual handling
    return response;
  }

  /**
   * Core request function
   */
  async function request(url, options = {}) {
    // Merge with defaults
    const config = {
      ...defaults,
      ...options,
      headers: {
        ...defaults.headers,
        ...options.headers
      }
    };

    // Build full URL
    const fullUrl = config.baseUrl + url;

    // Run request interceptors
    const finalConfig = await runRequestInterceptors({
      url: fullUrl,
      ...config
    });

    // Create abort controller
    const controller = createAbortController(finalConfig.timeout, finalConfig.requestId);

    // Build fetch options
    const fetchOptions = {
      method: finalConfig.method || 'GET',
      headers: finalConfig.headers,
      signal: controller.signal
    };

    // Add body for non-GET requests
    if (finalConfig.body !== undefined && finalConfig.method !== 'GET') {
      fetchOptions.body =
        typeof finalConfig.body === 'string' ? finalConfig.body : JSON.stringify(finalConfig.body);
    }

    // Retry logic
    let lastError;
    const maxAttempts = (finalConfig.retries || 0) + 1;

    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        const response = await fetch(fullUrl, fetchOptions);

        // Clean up active request
        if (finalConfig.requestId) {
          activeRequests.delete(finalConfig.requestId);
        }

        // Parse response
        const data = await parseResponse(response);

        // Check for HTTP errors
        if (!response.ok) {
          const errorMessage =
            data?.error || data?.message || response.statusText || 'Request failed';
          throw new APIError(errorMessage, response.status, data, fullUrl);
        }

        // Run response interceptors
        const finalResponse = await runResponseInterceptors(data, finalConfig);

        return finalResponse;
      } catch (error) {
        lastError = error;

        // Don't retry on abort or client errors
        if (error.name === 'AbortError') {
          throw new APIError('Request was cancelled', 0, null, fullUrl);
        }

        if (error instanceof APIError && error.isClientError()) {
          throw error;
        }

        // Retry on network or server errors
        if (attempt < maxAttempts) {
          const delay = finalConfig.retryDelay * attempt;
          console.warn(
            `API request failed (attempt ${attempt}/${maxAttempts}), retrying in ${delay}ms...`
          );
          await sleep(delay);
          continue;
        }

        // Convert regular errors to APIError
        if (!(error instanceof APIError)) {
          throw new APIError(error.message || 'Network error', 0, null, fullUrl);
        }

        throw error;
      }
    }

    throw lastError;
  }

  /**
   * GET request
   */
  function get(url, options = {}) {
    return request(url, { ...options, method: 'GET' });
  }

  /**
   * POST request
   */
  function post(url, body, options = {}) {
    return request(url, { ...options, method: 'POST', body });
  }

  /**
   * PUT request
   */
  function put(url, body, options = {}) {
    return request(url, { ...options, method: 'PUT', body });
  }

  /**
   * PATCH request
   */
  function patch(url, body, options = {}) {
    return request(url, { ...options, method: 'PATCH', body });
  }

  /**
   * DELETE request
   */
  function del(url, options = {}) {
    return request(url, { ...options, method: 'DELETE' });
  }

  /**
   * Cancel a specific request by ID
   */
  function cancel(requestId) {
    const controller = activeRequests.get(requestId);
    if (controller) {
      controller.abort();
      activeRequests.delete(requestId);
      return true;
    }
    return false;
  }

  /**
   * Cancel all active requests
   */
  function cancelAll() {
    activeRequests.forEach((controller, id) => {
      controller.abort();
    });
    activeRequests.clear();
  }

  /**
   * Add request interceptor
   * Interceptor receives config and should return modified config
   */
  function addRequestInterceptor(interceptor) {
    requestInterceptors.push(interceptor);
    return () => {
      const index = requestInterceptors.indexOf(interceptor);
      if (index > -1) {
        requestInterceptors.splice(index, 1);
      }
    };
  }

  /**
   * Add response interceptor
   * Interceptor receives (data, config) and should return modified data
   */
  function addResponseInterceptor(interceptor) {
    responseInterceptors.push(interceptor);
    return () => {
      const index = responseInterceptors.indexOf(interceptor);
      if (index > -1) {
        responseInterceptors.splice(index, 1);
      }
    };
  }

  /**
   * Configure default settings
   */
  function configure(config) {
    Object.assign(defaults, config);
    if (config.headers) {
      defaults.headers = { ...defaults.headers, ...config.headers };
    }
  }

  // Public API
  return {
    // HTTP methods
    get,
    post,
    put,
    patch,
    delete: del,
    request,

    // Request management
    cancel,
    cancelAll,

    // Interceptors
    addRequestInterceptor,
    addResponseInterceptor,

    // Configuration
    configure,

    // Error class for instanceof checks
    APIError
  };
})();

// Export for ES modules (if supported)
if (typeof module !== 'undefined' && module.exports) {
  module.exports = API;
}
