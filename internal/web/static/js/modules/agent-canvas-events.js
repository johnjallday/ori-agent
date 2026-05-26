/**
 * EventSource and polling utilities for AgentCanvas.
 */

export function createEventStream(workspaceId, handlers) {
  const source = new EventSource(`/api/orchestration/progress/stream?workspace_id=${workspaceId}`);

  const safe = (fn) => (...args) => {
    if (typeof fn === 'function') {
      try { fn(...args); } catch (err) { console.error(err); }
    }
  };

  source.addEventListener('initial', (event) => safe(handlers.onInitial)(event));
  source.addEventListener('workspace.progress', (event) => safe(handlers.onWorkspaceProgress)(event));
  source.addEventListener('workspace.updated', (event) => safe(handlers.onWorkspaceUpdated)(event));
  source.addEventListener('task.created', (event) => safe(handlers.onTaskEvent)(event));
  source.addEventListener('task.started', (event) => safe(handlers.onTaskEvent)(event));
  source.addEventListener('task.completed', (event) => safe(handlers.onTaskEvent)(event));
  source.addEventListener('task.failed', (event) => safe(handlers.onTaskEvent)(event));
  source.addEventListener('task.thinking', (event) => safe(handlers.onTaskThinking)(event));
  source.addEventListener('task.tool_call', (event) => safe(handlers.onTaskToolCall)(event));
  source.addEventListener('task.tool_success', (event) => safe(handlers.onTaskToolSuccess)(event));
  source.addEventListener('task.tool_error', (event) => safe(handlers.onTaskToolError)(event));
  source.addEventListener('task.progress', (event) => safe(handlers.onTaskProgress)(event));
  source.addEventListener('attachment.created', (event) => safe(handlers.onAttachmentEvent)(event));
  source.addEventListener('attachment.updated', (event) => safe(handlers.onAttachmentEvent)(event));
  source.addEventListener('attachment.deleted', (event) => safe(handlers.onAttachmentEvent)(event));
  source.addEventListener('scheduled_task.triggered', (event) => safe(handlers.onScheduledTaskEvent)(event));
  source.addEventListener('scheduled_task.failed', (event) => safe(handlers.onScheduledTaskEvent)(event));
  source.addEventListener('scheduled_task.completed', (event) => safe(handlers.onScheduledTaskEvent)(event));
  source.addEventListener('store_node.write.success', (event) => safe(handlers.onStoreNodeEvent)(event));
  source.addEventListener('store_node.write.failed', (event) => safe(handlers.onStoreNodeEvent)(event));
  source.addEventListener('store_node.created', (event) => safe(handlers.onStoreNodeEvent)(event));
  source.addEventListener('store_node.updated', (event) => safe(handlers.onStoreNodeEvent)(event));
  source.addEventListener('store_node.deleted', (event) => safe(handlers.onStoreNodeEvent)(event));

  source.onerror = (error) => safe(handlers.onError)(error);

  return source;
}

/**
 * Helper to connect and parse events, then forward parsed data to callbacks.
 * Handlers receive already-parsed payloads.
 */
export function connectProgressStream(workspaceId, handlers) {
  const parseJSON = (event) => {
    try {
      return JSON.parse(event.data);
    } catch (error) {
      console.error('Failed to parse event:', error);
      return null;
    }
  };

  return createEventStream(workspaceId, {
    onInitial: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onInitial) handlers.onInitial(data);
    },
    onWorkspaceProgress: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onWorkspaceProgress) handlers.onWorkspaceProgress(data);
    },
    onWorkspaceUpdated: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onWorkspaceUpdated) handlers.onWorkspaceUpdated(data);
    },
    onTaskEvent: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onTaskEvent) handlers.onTaskEvent(event.type, data);
    },
    onTaskThinking: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onTaskThinking) handlers.onTaskThinking(data);
    },
    onTaskToolCall: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onTaskToolCall) handlers.onTaskToolCall(data);
    },
    onTaskToolSuccess: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onTaskToolSuccess) handlers.onTaskToolSuccess(data);
    },
    onTaskToolError: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onTaskToolError) handlers.onTaskToolError(data);
    },
    onTaskProgress: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onTaskProgress) handlers.onTaskProgress(data);
    },
    onAttachmentEvent: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onAttachmentEvent) handlers.onAttachmentEvent(event.type, data);
    },
    onScheduledTaskEvent: (event) => {
      const data = parseJSON(event);
      if (data && handlers.onScheduledTaskEvent) handlers.onScheduledTaskEvent(event.type, data);
    },
    onError: (error) => {
      if (handlers.onError) handlers.onError(error);
    }
  });
}
