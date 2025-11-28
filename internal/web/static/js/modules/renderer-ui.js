/**
 * Renderer UI
 *
 * Handles rendering of UI elements:
 * - Buttons (create task, add agent, auto-layout, save)
 * - Notifications
 * - Context menus
 * - Help overlay
 * - Workspace progress
 * - Mission display
 */

export class RendererUI {
  /**
   * @param {CanvasRenderingContext2D} ctx - Canvas 2D context
   * @param {AgentCanvasState} state - Shared state object
   * @param {HTMLCanvasElement} canvas - Canvas element
   * @param {AgentCanvas} parent - Parent AgentCanvas instance
   * @param {RendererPrimitives} primitives - Primitives renderer
   */
  constructor(ctx, state, canvas, parent, primitives) {
    this.ctx = ctx;
    this.state = state;
    this.canvas = canvas;
    this.parent = parent;
    this.primitives = primitives;
  }

  drawWorkspaceProgress() {
    if (!this.state.workspaceProgress || this.state.workspaceProgress.total_tasks === 0) return;

    const panelWidth = Math.min(600, this.state.width * 0.8);
    const panelHeight = 95;
    const panelX = 100; // Move right to avoid overlapping with studio title
    const panelY = 100; // Move down to avoid studio title overlay
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.strokeStyle = 'rgba(16, 185, 129, 0.6)'; // Green border
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.1)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 2;

    this.primitives.roundRect(panelX, panelY, panelWidth, panelHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();

    this.ctx.shadowColor = 'transparent';

    // Title
    this.ctx.fillStyle = '#10b981';
    this.ctx.font = 'bold 11px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';
    this.ctx.fillText('📊 WORKSPACE PROGRESS', panelX + padding, panelY + padding);

    // Task status text
    const statsY = panelY + padding + 18;
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '10px system-ui';
    let statusText = `${this.state.workspaceProgress.completed_tasks}/${this.state.workspaceProgress.total_tasks} tasks complete | ${this.state.workspaceProgress.in_progress_tasks} running | ${this.state.workspaceProgress.pending_tasks} pending`;
    if (this.state.workspaceProgress.failed_tasks > 0) {
      statusText += ` | ${this.state.workspaceProgress.failed_tasks} failed`;
    }
    this.ctx.fillText(statusText, panelX + padding, statsY);

    // Progress bar
    const progressBarY = panelY + padding + 36;
    const progressBarWidth = panelWidth - padding * 2;
    const progressBarHeight = 16; // Slightly taller for better visibility

    // Background
    this.ctx.fillStyle = '#e5e7eb';
    this.primitives.roundRect(panelX + padding, progressBarY, progressBarWidth, progressBarHeight, 6);
    this.ctx.fill();

    // Progress fill
    const fillWidth = (progressBarWidth * this.state.workspaceProgress.percentage) / 100;
    if (fillWidth > 0) {
      const gradient = this.ctx.createLinearGradient(panelX + padding, progressBarY, panelX + padding + fillWidth, progressBarY);
      gradient.addColorStop(0, '#10b981');
      gradient.addColorStop(1, '#059669');
      this.ctx.fillStyle = gradient;
      this.primitives.roundRect(panelX + padding, progressBarY, fillWidth, progressBarHeight, 6);
      this.ctx.fill();
    }

    // Percentage text on progress bar - smaller font
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 9px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText(`${this.state.workspaceProgress.percentage}%`, panelX + padding + progressBarWidth / 2, progressBarY + progressBarHeight / 2);

    // Bottom row: Agent status and estimated time
    const bottomY = panelY + padding + 58;
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';

    // Agent status
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '10px system-ui';
    const agentText = `Agents: ${this.state.workspaceProgress.active_agents} active | ${this.state.workspaceProgress.idle_agents} idle`;
    this.ctx.fillText(agentText, panelX + padding, bottomY);

    // Estimated time remaining
    if (this.state.workspaceProgress.remaining_time_ms && this.state.workspaceProgress.remaining_time_ms > 0) {
      this.ctx.textAlign = 'right';
      const minutes = Math.ceil(this.state.workspaceProgress.remaining_time_ms / 60000);
      const seconds = Math.ceil((this.state.workspaceProgress.remaining_time_ms % 60000) / 1000);
      let timeText = '';
      if (minutes > 0) {
        timeText = `Est. ${minutes}m ${seconds}s remaining`;
      } else {
        timeText = `Est. ${seconds}s remaining`;
      }
      this.ctx.fillText(timeText, panelX + panelWidth - padding, bottomY);
    }

    this.ctx.restore();
  }

  drawMission() {
    if (!this.state.mission) return;

    // Calculate center of canvas in world coordinates
    const centerX = this.state.width / 2;
    const centerY = this.state.height / 2;

    // Draw mission background box
    this.ctx.save();

    // Measure text to size the box appropriately
    this.ctx.font = 'bold 18px system-ui';
    const maxWidth = this.state.width * 0.6; // Max 60% of canvas width
    const lines = this.primitives.wrapText(this.state.mission, maxWidth);
    const lineHeight = 26;
    const totalHeight = lines.length * lineHeight + 30;
    const boxWidth = Math.min(maxWidth + 40, this.state.width * 0.7);
    const boxHeight = totalHeight;

    // Position at top center
    const boxX = centerX - boxWidth / 2;
    const boxY = 40;

    // Draw semi-transparent background with border
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.strokeStyle = 'rgba(59, 130, 246, 0.8)'; // Primary blue
    this.ctx.lineWidth = 3;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 10;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 2;

    // Rounded rectangle
    this.primitives.roundRect(boxX, boxY, boxWidth, boxHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();

    this.ctx.shadowColor = 'transparent';

    // Draw "MISSION" label
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.font = 'bold 12px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';
    this.ctx.fillText('🎯 MISSION', boxX + 20, boxY + 12);

    // Draw mission text
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = '16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';

    lines.forEach((line, i) => {
      this.ctx.fillText(line, boxX + 20, boxY + 40 + i * lineHeight);
    });

    this.ctx.restore();
  }

  drawCreateTaskButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 20;

    // Store button bounds for click detection
    this.state.createTaskButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Draw button background
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.strokeStyle = '#1e40af';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.primitives.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('+ Create Task', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  drawAddAgentButton() {
    const buttonWidth = 130;
    const buttonHeight = 40;
    const buttonX = this.state.width - 140 - 20 - buttonWidth - 10; // Left of Create Task button
    const buttonY = 20;

    // Store button bounds for click detection
    this.state.addAgentButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Draw button background
    this.ctx.fillStyle = '#10b981'; // Green color for add agent
    this.ctx.strokeStyle = '#059669';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.primitives.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('+ Add Agent', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  drawNotifications() {
    if (!this.state.notifications || this.state.notifications.length === 0) return;

    const notificationWidth = 320;
    const notificationHeight = 70;
    const padding = 15;
    const spacing = 10;

    this.ctx.save();

    this.state.notifications.forEach((notification, index) => {
      const x = this.state.width - notificationWidth - 20;
      const y = this.state.height - (notificationHeight + spacing) * (index + 1) - 80;

      // Background color based on type
      const colors = {
        'info': { bg: '#3b82f6', border: '#1e40af' },
        'success': { bg: '#10b981', border: '#059669' },
        'warning': { bg: '#f59e0b', border: '#d97706' },
        'error': { bg: '#ef4444', border: '#dc2626' }
      };
      const color = colors[notification.type] || colors['info'];

      // Background
      this.ctx.fillStyle = color.bg;
      this.ctx.strokeStyle = color.border;
      this.ctx.lineWidth = 2;
      this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
      this.ctx.shadowBlur = 12;
      this.ctx.shadowOffsetY = 4;
      this.primitives.roundRect(x, y, notificationWidth, notificationHeight, 8);
      this.ctx.fill();
      this.ctx.stroke();
      this.ctx.shadowColor = 'transparent';

      // Icon
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = '20px system-ui';
      const icons = {
        'info': 'ℹ️',
        'success': '✓',
        'warning': '⚠️',
        'error': '✗'
      };
      const icon = icons[notification.type] || 'ℹ️';
      this.ctx.fillText(icon, x + padding, y + 28);

      // Message
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = '13px system-ui';
      this.ctx.textAlign = 'left';
      this.ctx.textBaseline = 'top';

      // Wrap text if too long
      const maxWidth = notificationWidth - padding * 2 - 35;
      const lines = this.primitives.wrapText(notification.message, maxWidth);
      lines.slice(0, 2).forEach((line, i) => {
        this.ctx.fillText(line, x + padding + 30, y + padding + i * 18);
      });

      // Close button
      this.ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
      this.ctx.font = 'bold 16px system-ui';
      this.ctx.textAlign = 'right';
      this.ctx.fillText('×', x + notificationWidth - padding, y + padding + 5);

      // Store bounds for click detection
      notification.closeBounds = {
        x: x + notificationWidth - padding - 20,
        y: y + padding - 5,
        width: 25,
        height: 25
      };
    });

    this.ctx.restore();
  }

  drawTimelineToggleButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 70; // Below create task button

    // Store button bounds for click detection
    this.state.timelineToggleBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background - different color if timeline is open
    this.ctx.fillStyle = this.state.timelineVisible ? '#059669' : '#6b7280';
    this.ctx.strokeStyle = this.state.timelineVisible ? '#047857' : '#4b5563';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.primitives.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    const text = this.state.timelineVisible ? '📋 Hide Timeline' : '📋 Timeline';
    this.ctx.fillText(text, buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  drawAutoLayoutButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 120; // Below timeline button

    // Store button bounds for click detection
    this.state.autoLayoutButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background
    this.ctx.fillStyle = '#8b5cf6'; // Purple to match connection theme
    this.ctx.strokeStyle = '#7c3aed';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.primitives.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('⚡ Auto-Layout', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  drawSaveLayoutButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 170; // Below auto-layout button

    // Store button bounds for click detection
    this.state.saveLayoutButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background
    this.ctx.fillStyle = '#10b981'; // Green for save action
    this.ctx.strokeStyle = '#059669';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.primitives.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('💾 Save Layout', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  drawContextMenu() {
    if (!this.state.contextMenuAgent) return;

    const menuWidth = 200;
    const menuHeight = 140;
    const padding = 10;
    const itemHeight = 35;

    // Position menu (ensure it stays within canvas bounds)
    let x = this.state.contextMenuX;
    let y = this.state.contextMenuY;
    if (x + menuWidth > this.state.width) x = this.state.width - menuWidth - 10;
    if (y + menuHeight > this.state.height) y = this.state.height - menuHeight - 10;

    // Draw menu background (glassmorphism effect)
    this.ctx.save();
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
    this.ctx.lineWidth = 1;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 4;

    // Rounded rectangle for menu
    this.ctx.beginPath();
    const radius = 8;
    this.ctx.moveTo(x + radius, y);
    this.ctx.lineTo(x + menuWidth - radius, y);
    this.ctx.arcTo(x + menuWidth, y, x + menuWidth, y + radius, radius);
    this.ctx.lineTo(x + menuWidth, y + menuHeight - radius);
    this.ctx.arcTo(x + menuWidth, y + menuHeight, x + menuWidth - radius, y + menuHeight, radius);
    this.ctx.lineTo(x + radius, y + menuHeight);
    this.ctx.arcTo(x, y + menuHeight, x, y + menuHeight - radius, radius);
    this.ctx.lineTo(x, y + radius);
    this.ctx.arcTo(x, y, x + radius, y, radius);
    this.ctx.closePath();
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.restore();

    // Menu title
    this.ctx.save();
    this.ctx.fillStyle = '#1e293b';
    this.ctx.font = 'bold 13px Inter, sans-serif';
    this.ctx.fillText(this.state.contextMenuAgent.name, x + padding, y + padding + 12);
    this.ctx.restore();

    // Draw separator line
    this.ctx.save();
    this.ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(x + padding, y + padding + 20);
    this.ctx.lineTo(x + menuWidth - padding, y + padding + 20);
    this.ctx.stroke();
    this.ctx.restore();

    // Menu items
    const items = [
      { icon: '👁️', label: 'View Details', action: 'view' },
      { icon: '📋', label: 'Assign Task', action: 'assign' },
      { icon: '🗑️', label: 'Remove', action: 'remove' }
    ];

    this.ctx.save();
    this.ctx.font = '13px Inter, sans-serif';
    items.forEach((item, i) => {
      const itemY = y + padding + 30 + (i * itemHeight);

      // Check if mouse is hovering over this item
      const mouseX = this.state.lastMouseX || 0;
      const mouseY = this.state.lastMouseY || 0;
      const isHovered = mouseX >= x && mouseX <= x + menuWidth &&
                       mouseY >= itemY && mouseY <= itemY + itemHeight;

      // Draw hover background
      if (isHovered) {
        this.ctx.fillStyle = 'rgba(29, 78, 216, 0.1)';
        this.ctx.fillRect(x + 5, itemY, menuWidth - 10, itemHeight);
      }

      // Draw icon
      this.ctx.fillStyle = '#475569';
      this.ctx.fillText(item.icon, x + padding + 5, itemY + 22);

      // Draw label
      this.ctx.fillStyle = item.action === 'remove' ? '#dc2626' : '#1e293b';
      this.ctx.fillText(item.label, x + padding + 30, itemY + 22);

      // Store item bounds for click detection
      if (!this.state.contextMenuItems) this.state.contextMenuItems = [];
      this.state.contextMenuItems[i] = {
        x, y: itemY, width: menuWidth, height: itemHeight,
        action: item.action, agent: this.state.contextMenuAgent
      };
    });
    this.ctx.restore();
  }

  drawHelpOverlay() {
    const overlayWidth = 400;
    const overlayHeight = 450;
    const x = (this.state.width - overlayWidth) / 2;
    const y = (this.state.height - overlayHeight) / 2;
    const padding = 20;

    // Draw semi-transparent backdrop
    this.ctx.save();
    this.ctx.fillStyle = 'rgba(0, 0, 0, 0.5)';
    this.ctx.fillRect(0, 0, this.state.width, this.state.height);
    this.ctx.restore();

    // Draw overlay background
    this.ctx.save();
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.98)';
    this.ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
    this.ctx.lineWidth = 1;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 30;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 8;

    // Rounded rectangle
    const radius = 12;
    this.ctx.beginPath();
    this.ctx.moveTo(x + radius, y);
    this.ctx.lineTo(x + overlayWidth - radius, y);
    this.ctx.arcTo(x + overlayWidth, y, x + overlayWidth, y + radius, radius);
    this.ctx.lineTo(x + overlayWidth, y + overlayHeight - radius);
    this.ctx.arcTo(x + overlayWidth, y + overlayHeight, x + overlayWidth - radius, y + overlayHeight, radius);
    this.ctx.lineTo(x + radius, y + overlayHeight);
    this.ctx.arcTo(x, y + overlayHeight, x, y + overlayHeight - radius, radius);
    this.ctx.lineTo(x, y + radius);
    this.ctx.arcTo(x, y, x + radius, y, radius);
    this.ctx.closePath();
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.restore();

    // Title
    this.ctx.save();
    this.ctx.fillStyle = '#1e293b';
    this.ctx.font = 'bold 20px Inter, sans-serif';
    this.ctx.fillText('⌨️ Keyboard Shortcuts', x + padding, y + padding + 20);
    this.ctx.restore();

    // Close hint
    this.ctx.save();
    this.ctx.fillStyle = '#64748b';
    this.ctx.font = '12px Inter, sans-serif';
    this.ctx.fillText('Press H or ESC to close', x + padding, y + padding + 45);
    this.ctx.restore();

    // Shortcuts list
    const shortcuts = [
      { section: 'Navigation', items: [] },
      { key: 'Space + Drag', desc: 'Pan canvas' },
      { key: 'Mouse Wheel', desc: 'Zoom in/out' },
      { key: 'Ctrl + Wheel', desc: 'Precise zoom' },
      { key: 'R', desc: 'Reset view (zoom to fit)' },
      { section: 'Agents', items: [] },
      { key: 'Click Agent', desc: 'Select agent' },
      { key: 'Right-click', desc: 'Agent quick actions' },
      { key: 'Drag Agent', desc: 'Move agent position' },
      { section: 'Tasks', items: [] },
      { key: 'Click Task', desc: 'View task details' },
      { key: 'Drag Task', desc: 'Assign to agent' },
      { section: 'General', items: [] },
      { key: 'H', desc: 'Toggle this help' },
      { key: 'ESC', desc: 'Cancel/Close' }
    ];

    let currentY = y + padding + 70;
    const lineHeight = 28;
    const sectionSpacing = 10;

    this.ctx.save();
    shortcuts.forEach(item => {
      if (item.section) {
        // Section header
        this.ctx.fillStyle = '#1e293b';
        this.ctx.font = 'bold 14px Inter, sans-serif';
        this.ctx.fillText(item.section, x + padding, currentY);
        currentY += lineHeight + sectionSpacing;
      } else {
        // Shortcut item
        // Draw key badge
        const keyWidth = this.ctx.measureText(item.key).width + 16;
        this.ctx.fillStyle = 'rgba(29, 78, 216, 0.1)';
        this.ctx.strokeStyle = 'rgba(29, 78, 216, 0.3)';
        this.ctx.lineWidth = 1;
        const badgeRadius = 4;
        const badgeX = x + padding;
        const badgeY = currentY - 18;
        const badgeHeight = 24;
        this.ctx.beginPath();
        this.ctx.moveTo(badgeX + badgeRadius, badgeY);
        this.ctx.lineTo(badgeX + keyWidth - badgeRadius, badgeY);
        this.ctx.arcTo(badgeX + keyWidth, badgeY, badgeX + keyWidth, badgeY + badgeRadius, badgeRadius);
        this.ctx.lineTo(badgeX + keyWidth, badgeY + badgeHeight - badgeRadius);
        this.ctx.arcTo(badgeX + keyWidth, badgeY + badgeHeight, badgeX + keyWidth - badgeRadius, badgeY + badgeHeight, badgeRadius);
        this.ctx.lineTo(badgeX + badgeRadius, badgeY + badgeHeight);
        this.ctx.arcTo(badgeX, badgeY + badgeHeight, badgeX, badgeY + badgeHeight - badgeRadius, badgeRadius);
        this.ctx.lineTo(badgeX, badgeY + badgeRadius);
        this.ctx.arcTo(badgeX, badgeY, badgeX + badgeRadius, badgeY, badgeRadius);
        this.ctx.closePath();
        this.ctx.fill();
        this.ctx.stroke();

        // Draw key text
        this.ctx.fillStyle = '#1d4ed8';
        this.ctx.font = 'bold 12px Inter, monospace';
        this.ctx.fillText(item.key, badgeX + 8, currentY);

        // Draw description
        this.ctx.fillStyle = '#475569';
        this.ctx.font = '13px Inter, sans-serif';
        this.ctx.fillText(item.desc, badgeX + keyWidth + 15, currentY);

        currentY += lineHeight;
      }
    });
    this.ctx.restore();
  }

}
