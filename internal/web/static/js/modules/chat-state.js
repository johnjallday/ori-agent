// Chat State Machine Module
// Manages chat operation states with cancel support via AbortController

export const ChatState = {
  IDLE: 'idle',
  SENDING: 'sending',
  THINKING: 'thinking',
  PROCESSING: 'processing'
};

class ChatStateMachine {
  constructor() {
    this.state = ChatState.IDLE;
    this.startTime = null;
    this.abortController = null;
    this.listeners = new Set();
    this.elapsedInterval = null;
  }

  /**
   * Transition to a new state
   * @param {string} newState - The new state to transition to
   * @param {object} data - Optional data to pass to listeners
   */
  transition(newState, data = {}) {
    const oldState = this.state;
    this.state = newState;

    if (newState !== ChatState.IDLE) {
      this.startTime = Date.now();
      this.startElapsedTimer();
    } else {
      this.stopElapsedTimer();
      this.startTime = null;
    }

    this.notify({ oldState, newState, ...data });
  }

  /**
   * Subscribe to state changes
   * @param {function} callback - Function to call on state changes
   * @returns {function} Unsubscribe function
   */
  subscribe(callback) {
    this.listeners.add(callback);
    return () => this.listeners.delete(callback);
  }

  /**
   * Notify all listeners of state change
   * @param {object} data - Data to pass to listeners
   */
  notify(data) {
    this.listeners.forEach(cb => {
      try {
        cb(data);
      } catch (err) {
        console.error('Chat state listener error:', err);
      }
    });
  }

  // State transition methods

  /**
   * Start sending a message - creates new AbortController
   */
  send() {
    this.abortController = new AbortController();
    this.transition(ChatState.SENDING);
  }

  /**
   * Transition to thinking state (waiting for LLM response)
   */
  think() {
    this.transition(ChatState.THINKING);
  }

  /**
   * Transition to processing state (formatting response)
   */
  process() {
    this.transition(ChatState.PROCESSING);
  }

  /**
   * Complete the operation and return to idle
   */
  complete() {
    this.abortController = null;
    this.transition(ChatState.IDLE);
  }

  /**
   * Cancel the current operation
   */
  cancel() {
    if (this.abortController) {
      this.abortController.abort();
      this.abortController = null;
    }
    this.transition(ChatState.IDLE);
  }

  /**
   * Get the abort signal for fetch requests
   * @returns {AbortSignal|undefined}
   */
  getSignal() {
    return this.abortController?.signal;
  }

  /**
   * Check if the state machine is active (not idle)
   * @returns {boolean}
   */
  isActive() {
    return this.state !== ChatState.IDLE;
  }

  /**
   * Get the current state
   * @returns {string}
   */
  getState() {
    return this.state;
  }

  /**
   * Get elapsed seconds since operation started
   * @returns {number}
   */
  getElapsedSeconds() {
    if (!this.startTime) return 0;
    return Math.floor((Date.now() - this.startTime) / 1000);
  }

  /**
   * Start the elapsed time timer
   */
  startElapsedTimer() {
    this.stopElapsedTimer();
    this.elapsedInterval = setInterval(() => {
      this.notify({ type: 'tick', elapsed: this.getElapsedSeconds() });
    }, 1000);
  }

  /**
   * Stop the elapsed time timer
   */
  stopElapsedTimer() {
    if (this.elapsedInterval) {
      clearInterval(this.elapsedInterval);
      this.elapsedInterval = null;
    }
  }
}

// Export singleton instance
export const chatStateMachine = new ChatStateMachine();

// Also export the class for testing
export { ChatStateMachine };
