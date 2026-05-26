/**
 * AgentCanvasInitialization - Initialization and setup module
 * Handles canvas setup, data loading, and agent positioning
 */
import { apiGet } from './agent-canvas-api.js';
import { EVENT_TYPES } from './agent-canvas-state.js';

export class AgentCanvasInitialization {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;

    // Bind methods
    this.handleWorkflowSelected = this.handleWorkflowSelected.bind(this);
  }

  /**
   * Resize canvas to match container
   */
  resize() {
    const rect = this.parent.canvas.getBoundingClientRect();
    this.parent.canvas.width = rect.width * window.devicePixelRatio;
    this.parent.canvas.height = rect.height * window.devicePixelRatio;
    this.parent.ctx.scale(window.devicePixelRatio, window.devicePixelRatio);
    this.parent.width = rect.width;
    this.parent.height = rect.height;
    // Don't call draw() here - let caller decide when to redraw
  }

  /**
   * Initialize canvas with workspace data
   */
  async init() {
    try {
      // First, resize canvas to set width/height properties
      this.resize();

      // Load workspace data
      this.parent.workspace = await apiGet(`/api/workspaces/${this.parent.workspaceId}`);

      // Load workspace progress
      this.parent.workspaceProgress = this.parent.workspace.workspace_progress || {
        total_tasks: 0,
        completed_tasks: 0,
        in_progress_tasks: 0,
        pending_tasks: 0,
        failed_tasks: 0,
        percentage: 0,
        active_agents: 0,
        idle_agents: 0,
        total_agents: 0
      };

      // Load mission from shared data if it exists
      if (this.parent.workspace.shared_data && this.parent.workspace.shared_data.mission) {
        this.parent.mission = this.parent.workspace.shared_data.mission;
      }

      // Load tasks from workspace (store all tasks, filter for display)
      if (this.parent.workspace.tasks) {
        // Store all tasks in state (unfiltered) for workflow detection
        this.parent.allTasks = this.parent.workspace.tasks;
        this.state.setAllTasks(this.parent.workspace.tasks);

        // Check for workflow filter parameter and set in state
        const urlParams = new URLSearchParams(window.location.search);
        const workflowId = urlParams.get('workflow');
        if (workflowId) {
          this.state.setSelectedWorkflow(workflowId);
        }

        // Filter tasks based on selected workflow (or show all if none selected)
        let tasksToProcess = this.state.filterTasksByWorkflow(this.parent.workspace.tasks);

        if (this.state.selectedWorkflowId) {
          // Store workflow mode flag for UI adjustments
          this.state.workflowViewMode = true;
          this.state.workflowId = this.state.selectedWorkflowId;
        }

        const tasks = tasksToProcess.map(task => {
          // If task doesn't have position, set to null so it will be calculated in drawTaskFlows
          const normalized = {
            ...task,
            x: task.x ?? null,
            y: task.y ?? null,
            combiner_type: task.combiner_type,
            combinerType: task.combiner_type,
            combiner_node_id: task.combiner_node_id,
            combinerNodeID: task.combiner_node_id
          };
          if (normalized.to === 'unassigned' || !normalized.to) {
            normalized.status = 'pending';
            normalized.result = null;
            normalized.error = null;
            normalized.progress = 0;
            normalized.completed_at = null;
            normalized.started_at = null;
          }
          return normalized;
        });
        // Ensure tasks are placed in the current viewport if missing or off-screen
        if (this.parent.eventHandler && typeof this.parent.eventHandler.ensureTaskPosition === 'function') {
          tasks.forEach(t => this.parent.eventHandler.ensureTaskPosition(t));
        }
        // Preserve assigned_node_id hints from existing state (if any)
        tasks.forEach(t => {
          const existing = this.state.tasks.find(et => et.id === t.id);
          if (existing && existing.assigned_node_id) {
            t.assigned_node_id = existing.assigned_node_id;
          }
        });
        this.state.setTasks(tasks);
        // Seed agent lastResult based on any completed tasks with results
        if (this.parent.eventHandler && typeof this.parent.eventHandler.updateAgentResultsFromTasks === 'function') {
          this.parent.eventHandler.updateAgentResultsFromTasks(tasks);
        }
      }

      // Load attachments from workspace
      if (this.parent.workspace.attachments) {
        const attachments = this.parent.workspace.attachments.map(att => ({
          ...att,
          file: att.file || att.file_meta,
          x: att.x ?? null,
          y: att.y ?? null
        }));

        // Ensure positions exist
        if (this.parent.eventHandler && typeof this.parent.eventHandler.ensureAttachmentPosition === 'function') {
          attachments.forEach(a => this.parent.eventHandler.ensureAttachmentPosition(a));
        }

        this.state.setAttachments(attachments);
      }

      // Load store nodes from workspace
      if (this.parent.workspace.store_nodes) {
        const storeNodes = this.parent.workspace.store_nodes.map(node => {
          // Use position from node (API), then layout, then defaults
          let x = node.x ?? 400;
          let y = node.y ?? 400;

          // Override with layout position if available
          if (this.parent.workspace.layout && this.parent.workspace.layout.store_positions) {
            const pos = this.parent.workspace.layout.store_positions[node.canvas_node_id];
            if (pos) {
              x = pos.x;
              y = pos.y;
            }
          }

          return {
            ...node,
            x: x,
            y: y
          };
        });

        this.state.setStoreNodes(storeNodes);
      }

      // Load directory references from workspace
      if (this.parent.workspace.directory_references) {
        const directories = this.parent.workspace.directory_references.map(dir => {
          // Use position from dir (API), then layout, then defaults
          let x = dir.x ?? 400;
          let y = dir.y ?? 400;

          // Override with layout position if available
          if (this.parent.workspace.layout && this.parent.workspace.layout.directory_positions) {
            const pos = this.parent.workspace.layout.directory_positions[dir.id];
            if (pos) {
              x = pos.x;
              y = pos.y;
            }
          }

          return {
            ...dir,
            x: x,
            y: y
          };
        });

        this.state.setDirectoryReferences(directories);
      }

      await this.loadWorkspaceFileFolders();

      // Initialize agent positions
      this.initializeAgents();

      // Load saved layout (positions only)
      this.parent.layout.loadLayout();

      // Sync combiner nodes with their tasks
      this.syncCombinerTasks();

      // Immediately reset zoom to fit all content (ignore saved zoom values)
      this.parent.layout.zoomToFitContent();

      // Detect and initialize chains
      this.parent.animation.updateChains();

      // Fetch initial progress data immediately before connecting to SSE
      await this.fetchInitialProgressData();

      // Connect to real-time events
      this.parent.eventHandler.connectEventStream();

      // Start animation loop
      this.parent.animation.startAnimation();

      // Subscribe to workflow selection changes
      this.state.on(EVENT_TYPES.WORKFLOW_SELECTED, this.handleWorkflowSelected);

      // Update canvas info
      this.updateCanvasInfo();

    } catch (error) {
      console.error('Failed to initialize canvas:', error);
      document.getElementById('canvas-info').textContent = 'Error loading workspace';
    }
  }

  async loadWorkspaceFileFolders() {
    if (!this.parent.workspaceId) {
      this.state.setWorkspaceFileTree([]);
      this.state.setWorkspaceFolders([]);
      return;
    }

    try {
      const payload = await apiGet(
        `/api/workspaces/${encodeURIComponent(this.parent.workspaceId)}/files/tree`
      );
      const files = Array.isArray(payload?.files) ? payload.files : [];
      this.state.setWorkspaceFileTree(files);
      this.state.setWorkspaceFolders(this.buildWorkspaceFolderNodes(files));
    } catch (error) {
      console.warn('Failed to load workspace file folders for canvas:', error);
      this.state.setWorkspaceFileTree([]);
      this.state.setWorkspaceFolders([]);
    }
  }

  buildWorkspaceFolderNodes(files) {
    const existingByID = new Map(
      (this.state.workspaceFolders || []).map(folder => [folder.folder_id || folder.id, folder])
    );
    const layoutPositions = this.parent.workspace?.layout?.folder_positions || {};
    const folderEntries = files
      .filter(item => item?.is_dir && item?.folder_id)
      .sort((left, right) =>
        String(left.relative_path || '').localeCompare(String(right.relative_path || ''), undefined, {
          sensitivity: 'base',
          numeric: true
        })
      );
    const threshold = 6;

    return folderEntries.map((folder, index) => {
      const id = folder.folder_id || folder.id || folder.relative_path;
      const existing = existingByID.get(id);
      const savedPosition = layoutPositions[id];
      const children = this.getDirectWorkspaceFileChildren(files, folder.relative_path);
      const childFolderCount = children.filter(child => child.is_dir).length;
      const childFileCount = children.length - childFolderCount;
      const collapsed =
        existing?.collapsed !== undefined
          ? existing.collapsed
          : children.length > threshold;

      return {
        id,
        folder_id: id,
        name: folder.name || String(folder.relative_path || '').split('/').pop() || 'Folder',
        path: folder.relative_path || '',
        relative_path: folder.relative_path || '',
        x: savedPosition?.x ?? existing?.x ?? (520 + (index % 3) * 240),
        y: savedPosition?.y ?? existing?.y ?? (220 + Math.floor(index / 3) * 170),
        width: existing?.width || 220,
        height: existing?.height || 96,
        collapsed,
        children,
        childFolderCount,
        childFileCount
      };
    });
  }

  getDirectWorkspaceFileChildren(files, folderPath) {
    const normalizedFolder = this.normalizeWorkspaceRelativePath(folderPath);
    return files
      .filter(item => {
        const relativePath = this.normalizeWorkspaceRelativePath(item?.relative_path);
        if (!relativePath || relativePath === normalizedFolder) return false;
        return this.parentPathFor(relativePath) === normalizedFolder;
      })
      .map(item => ({
        id: item.id || item.attachment_id || item.folder_id || item.relative_path,
        attachment_id: item.attachment_id || '',
        folder_id: item.folder_id || '',
        name: item.name || String(item.relative_path || '').split('/').pop() || 'Item',
        relative_path: item.relative_path || '',
        is_dir: Boolean(item.is_dir),
        size: Number(item.size || 0)
      }));
  }

  normalizeWorkspaceRelativePath(path) {
    return String(path || '')
      .trim()
      .replace(/\\/g, '/')
      .replace(/^\.\/+/, '')
      .replace(/^\/+/, '')
      .replace(/\/+/g, '/')
      .replace(/\/+$/, '');
  }

  parentPathFor(path) {
    const normalizedPath = this.normalizeWorkspaceRelativePath(path);
    if (!normalizedPath || !normalizedPath.includes('/')) {
      return '';
    }
    return normalizedPath.slice(0, normalizedPath.lastIndexOf('/'));
  }

  /**
   * Initialize agent positions and stats from workspace data
   */
  initializeAgents() {
    if (!this.parent.workspace) return;

    const agentStats = this.parent.workspace.agent_stats || {};

    // Use AgentInstances if available (new stable system)
    if (this.parent.workspace.agent_instances && this.parent.workspace.agent_instances.length > 0) {
      const agentCount = this.parent.workspace.agent_instances.length;
      const centerY = this.parent.height * 0.6;
      const spacing = Math.min(150, (this.parent.width * 0.8) / Math.max(agentCount - 1, 1));
      const totalWidth = spacing * (agentCount - 1);
      const startX = (this.parent.width - totalWidth) / 2;

      const agents = this.parent.workspace.agent_instances.map((instance, index) => {
        const stats = agentStats[instance.name] || {
          status: 'idle',
          current_tasks: [],
          queued_tasks: [],
          completed_tasks: 0,
          failed_tasks: 0,
          total_executions: 0
        };

        return {
          name: instance.name,
          id: instance.id,                    // Stable UUID
          nodeId: instance.node_id,            // Stable node ID (e.g., "default-node-1")
          instanceNumber: instance.instance_number,
          x: startX + (index * spacing),
          y: centerY,
          width: 120,
          height: 70,
          color: this.parent.helpers.getAgentColor(index),
          status: stats.status,
          currentTasks: stats.current_tasks || [],
          queuedTasks: stats.queued_tasks || [],
          completedTasks: stats.completed_tasks || 0,
          failedTasks: stats.failed_tasks || 0,
          totalExecutions: stats.total_executions || 0,
          tasks: [],
          pulsePhase: Math.random() * Math.PI * 2
        };
      });
      this.state.setAgents(agents);
      return;
    }

    // FALLBACK: Legacy system (agent names only, no stable instances)
    if (!this.parent.workspace.agents || this.parent.workspace.agents.length === 0) return;

    const agentCount = this.parent.workspace.agents.length;
    const centerY = this.parent.height * 0.6;
    const spacing = Math.min(150, (this.parent.width * 0.8) / Math.max(agentCount - 1, 1));
    const totalWidth = spacing * (agentCount - 1);
    const startX = (this.parent.width - totalWidth) / 2;

    const instanceCounters = {};

    const agents = this.parent.workspace.agents.map((agentName, index) => {
      if (!instanceCounters[agentName]) {
        instanceCounters[agentName] = 0;
      }
      instanceCounters[agentName]++;
      const instanceNumber = instanceCounters[agentName];
      const nodeId = `${agentName}-node-${instanceNumber}`;

      const stats = agentStats[agentName] || {
        status: 'idle',
        current_tasks: [],
        queued_tasks: [],
        completed_tasks: 0,
        failed_tasks: 0,
        total_executions: 0
      };

      return {
        name: agentName,
        nodeId: nodeId,
        instanceNumber: instanceNumber,
        x: startX + (index * spacing),
        y: centerY,
        width: 120,
        height: 70,
        color: this.parent.helpers.getAgentColor(index),
        status: stats.status,
        currentTasks: stats.current_tasks || [],
        queuedTasks: stats.queued_tasks || [],
        completedTasks: stats.completed_tasks || 0,
        failedTasks: stats.failed_tasks || 0,
        totalExecutions: stats.total_executions || 0,
        tasks: [],
        pulsePhase: Math.random() * Math.PI * 2
      };
    });
    this.state.setAgents(agents);

    // Load tasks if available
    if (this.parent.workspace.tasks) {
      // Preserve existing positions when updating tasks
      const existingPositions = {};
      this.state.tasks.forEach(t => {
        if (t.x !== null && t.y !== null) {
          existingPositions[t.id] = { x: t.x, y: t.y };
        }
      });

      const tasks = this.parent.workspace.tasks.map(task => {
        const existing = existingPositions[task.id];
        return {
          id: task.id,
          from: task.from,
          to: task.to,
          description: task.description,
          status: task.status,
          progress: 0,
          x: existing ? existing.x : null,
          y: existing ? existing.y : null,
          combiner_type: task.combiner_type,
          combinerType: task.combiner_type,
          combiner_node_id: task.combiner_node_id,
          combinerNodeID: task.combiner_node_id
        };
      });
      this.state.setTasks(tasks);
    }
  }

  /**
   * Sync combiner nodes with their backend tasks
   * Links combiner nodes to their corresponding tasks by combiner_node_id
   */
  syncCombinerTasks() {
    if (!this.state.combinerNodes || this.state.combinerNodes.length === 0) {
      return;
    }

    if (!this.state.tasks || this.state.tasks.length === 0) {
      return;
    }

    // For each combiner node, find its corresponding task
    this.state.combinerNodes.forEach(combiner => {
      // Find task with matching combiner_node_id
      const task = this.state.tasks.find(t =>
        t.combiner_node_id === combiner.id || t.combinerNodeID === combiner.id
      );

      if (task) {
        combiner.taskId = task.id;
      }
    });
  }

  /**
   * Handle workflow selection change
   * Re-filters tasks and redraws the canvas
   */
  handleWorkflowSelected({ workflowId }) {
    // Use all tasks from the original workspace data
    const allTasks = this.parent.allTasks || this.parent.workspace?.tasks || [];

    // Filter tasks based on the new selection
    const filteredTasks = this.state.filterTasksByWorkflow(allTasks);

    // Preserve existing positions
    const existingPositions = {};
    this.state.tasks.forEach(t => {
      if (t.x !== null && t.y !== null) {
        existingPositions[t.id] = { x: t.x, y: t.y };
      }
    });

    // Map and normalize tasks
    const tasks = filteredTasks.map(task => {
      const existing = existingPositions[task.id];
      const normalized = {
        ...task,
        x: existing ? existing.x : (task.x ?? null),
        y: existing ? existing.y : (task.y ?? null),
        combiner_type: task.combiner_type,
        combinerType: task.combiner_type,
        combiner_node_id: task.combiner_node_id,
        combinerNodeID: task.combiner_node_id
      };

      // Normalize unassigned tasks
      if (normalized.to === 'unassigned' || !normalized.to) {
        normalized.status = 'pending';
        normalized.result = null;
        normalized.error = null;
        normalized.progress = 0;
        normalized.completed_at = null;
        normalized.started_at = null;
      }

      return normalized;
    });

    // Ensure tasks have positions
    if (this.parent.eventHandler && typeof this.parent.eventHandler.ensureTaskPosition === 'function') {
      tasks.forEach(t => this.parent.eventHandler.ensureTaskPosition(t));
    }

    // Update state flags
    this.state.workflowViewMode = !!workflowId;
    this.state.workflowId = workflowId;

    // Update tasks in state
    this.state.setTasks(tasks);

    // Update canvas info
    this.updateCanvasInfo();

    // Zoom to fit the new content
    this.parent.layout.zoomToFitContent();

    // Redraw canvas
    this.parent.draw();
  }

  /**
   * Update the canvas info display
   */
  updateCanvasInfo() {
    const infoEl = document.getElementById('canvas-info');
    if (!infoEl) return;

    const workflowLabel = this.state.selectedWorkflowId ? ' (filtered)' : '';
    infoEl.textContent =
      `Workspace: ${this.parent.workspace?.name || this.parent.workspaceId} | Agents: ${this.state.agents.length} | Tasks: ${this.state.tasks.length}${workflowLabel} | Attachments: ${this.state.attachments.length} | Stores: ${this.state.storeNodes.length}`;
  }

  /**
   * Fetch initial progress data immediately after initialization
   * This ensures the canvas is fully functional without waiting for SSE
   */
  async fetchInitialProgressData() {
    try {
      // Fetch fresh workspace data with progress
      const response = await fetch(`/api/workspaces/${this.parent.workspaceId}`, {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' }
      });

      if (!response.ok) {
        console.warn('Failed to fetch initial progress data:', response.statusText);
        return;
      }

      const data = await response.json();

      if (this.parent.eventHandler &&
          typeof this.parent.eventHandler.processWorkspacePayload === 'function') {
        this.parent.eventHandler.processWorkspacePayload(
          { ...data, type: data.type || 'workspace.progress' },
          { setTasks: true, source: 'initial.fetch' }
        );
        return;
      }

      // Fallback if event handler is unavailable
      if (data.workspace_progress) {
        this.parent.workspaceProgress = data.workspace_progress;
      }

      if (data.agent_stats && this.parent.eventHandler &&
          typeof this.parent.eventHandler.updateAgentStats === 'function') {
        this.parent.eventHandler.updateAgentStats(data.agent_stats);
      }

      if (data.tasks) {
        // Store all tasks for workflow detection
        this.parent.allTasks = data.tasks;
        this.state.setAllTasks(data.tasks);

        // Filter tasks based on selected workflow
        const filteredTasks = this.state.filterTasksByWorkflow(data.tasks);

        // Preserve existing positions
        const existingPositions = {};
        this.state.tasks.forEach(t => {
          if (t.x !== null && t.y !== null) {
            existingPositions[t.id] = { x: t.x, y: t.y };
          }
        });

        const tasks = filteredTasks.map(task => {
          const existing = existingPositions[task.id];
          const mapped = {
            ...task,
            x: existing ? existing.x : (task.x ?? null),
            y: existing ? existing.y : (task.y ?? null)
          };

          // Ensure task has a position
          if (this.parent.eventHandler &&
              typeof this.parent.eventHandler.ensureTaskPosition === 'function') {
            this.parent.eventHandler.ensureTaskPosition(mapped);
          }

          return mapped;
        });

        // Normalize unassigned tasks
        tasks.forEach(t => {
          if (t.to === 'unassigned' || !t.to) {
            t.status = 'pending';
            t.result = null;
            t.error = null;
            t.progress = 0;
            t.completed_at = null;
            t.started_at = null;
          }
        });

        this.state.setTasks(tasks);

        // Update agent results from tasks
        if (this.parent.eventHandler &&
            typeof this.parent.eventHandler.updateAgentResultsFromTasks === 'function') {
          this.parent.eventHandler.updateAgentResultsFromTasks(tasks);
        }
      }

      // Redraw canvas with updated data
      this.parent.draw();

    } catch (error) {
      console.error('❌ Failed to fetch initial progress data:', error);
    }
  }
}
