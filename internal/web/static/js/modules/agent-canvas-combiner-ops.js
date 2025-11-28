/**
 * AgentCanvasCombinerOperations - Combiner node operations module
 * Handles creation, modification, and deletion of combiner nodes and connections
 */
export class AgentCanvasCombinerOperations {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Ensure a combiner has a tracked input port entry (used for spacing/persistence)
   */
  ensureCombinerInputPort(combiner, portId) {
    if (!combiner) return;
    combiner.inputPorts = combiner.inputPorts || [];
    if (!combiner.inputPorts.find(p => p.id === portId)) {
      combiner.inputPorts.push({ id: portId });
    }
  }

  /**
   * Create a connection between two nodes (agent/combiner to agent/combiner)
   */
  createConnection(fromNodeId, fromPort, toNodeId, toPort) {
    // Avoid duplicate connections with same endpoints
    const existing = this.parent.connections.find(c =>
      c.from === fromNodeId &&
      c.fromPort === fromPort &&
      c.to === toNodeId &&
      c.toPort === toPort
    );
    if (existing) {
      console.log(`ℹ️ Connection already exists: ${fromNodeId}.${fromPort} → ${toNodeId}.${toPort}`);
      return existing;
    }

    const connection = {
      id: `conn-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`,
      from: fromNodeId,
      fromPort: fromPort,
      to: toNodeId,
      toPort: toPort,
      color: '#6366f1',
      animated: false
    };

    // Track combiner input ports so spacing is stable and persisted
    const targetNode = this.parent.getNodeById(toNodeId);
    if (targetNode && targetNode.type === 'combiner' && toPort && toPort.startsWith('input')) {
      this.ensureCombinerInputPort(targetNode.node, toPort);
    }

    this.parent.connections.push(connection);
    console.log(`🔗 Created connection: ${fromNodeId}.${fromPort} → ${toNodeId}.${toPort}`);
    this.parent.saveLayout();
    return connection;
  }

  /**
   * Delete a combiner node and its connections
   */
  deleteCombinerNode(nodeId) {
    // Remove the node
    this.parent.combinerNodes = this.parent.combinerNodes.filter(n => n.id !== nodeId);

    // Remove all connections involving this node
    this.parent.connections = this.parent.connections.filter(c =>
      c.from !== nodeId && c.to !== nodeId
    );

    console.log(`🗑️ Deleted combiner node: ${nodeId}`);
    this.parent.saveLayout();
    this.parent.draw();
  }

  /**
   * Delete a connection
   */
  deleteConnection(connectionId) {
    // Find the connection before deleting to check if it's connected to a combiner
    const connectionToDelete = this.parent.connections.find(c => c.id === connectionId);

    // Remove the connection
    this.parent.connections = this.parent.connections.filter(c => c.id !== connectionId);
    console.log(`🗑️ Deleted connection: ${connectionId}`);

    // Clean up unused combiner input ports
    if (connectionToDelete) {
      const targetNode = this.parent.getNodeById(connectionToDelete.to);
      if (targetNode && targetNode.type === 'combiner') {
        this.cleanupCombinerInputPorts(targetNode.node);
      }
    }

    this.parent.saveLayout();
    this.parent.draw();
  }

  /**
   * Remove unused input ports from a combiner node
   */
  cleanupCombinerInputPorts(combiner, silent = false) {
    if (!combiner || !combiner.inputPorts) return;

    // Get all connections to this combiner
    const connected = this.parent.connections
      .filter(c => c.to === combiner.id && c.toPort && c.toPort.startsWith('input'));

    // Normalize and reindex input ports to remove gaps
    const normalized = connected
      .map(conn => {
        const match = /input-(\d+)/.exec(conn.toPort);
        return { conn, index: match ? parseInt(match[1], 10) : 0 };
      })
      .sort((a, b) => a.index - b.index);

    normalized.forEach(({ conn }, idx) => {
      const targetPortId = `input-${idx}`;
      if (conn.toPort !== targetPortId) {
        conn.toPort = targetPortId;
      }
    });

    combiner.inputPorts = normalized.map((_, idx) => ({ id: `input-${idx}` }));

    if (!silent) {
      console.log(`🧹 Cleaned up combiner ${combiner.name}: ${combiner.inputPorts.length} ports remaining`);
    }
  }

  /**
   * Build a preview of combiner results from its input connections
   */
  buildCombinerResultPreview(combiner) {
    const inputConns = this.parent.connections.filter(c => c.to === combiner.id);
    if (!inputConns.length) return '';
    const inputs = [];
    inputConns.forEach(conn => {
      const nodeData = this.parent.getNodeById(conn.from);
      if (nodeData?.type === 'task' && nodeData.node?.result) {
        inputs.push(nodeData.node.result);
      }
    });
    if (!inputs.length) return '';

    switch (combiner.resultCombinationMode) {
      case 'append':
        return inputs.join('\n---\n');
      case 'summarize':
      case 'merge':
      default:
        return inputs.map((t, i) => `• Input ${i + 1}: ${t}`).join('\n');
    }
  }
}
