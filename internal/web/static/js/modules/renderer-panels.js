/**
 * Renderer Panels
 *
 * Handles rendering of all panel types:
 * - Task detail panels
 * - Agent detail panels
 * - Combiner detail panels
 * - Timeline panel and events
 */

export class RendererPanels {
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

  drawExpandedTaskPanel() {
    if (!this.state.expandedTask) return;

    const panelX = this.state.width - this.state.expandedPanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.expandedPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.state.expandedPanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const padding = 20;
    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.expandedPanelWidth - padding, currentY + 20);
    currentY += 40;

    // Task title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Task Details', contentX, currentY);
    currentY += 30;

    // Status badge
    let statusColor = '#6b7280';
    if (this.state.expandedTask.status === 'completed') statusColor = '#10b981';
    else if (this.state.expandedTask.status === 'in_progress') statusColor = '#3b82f6';
    else if (this.state.expandedTask.status === 'failed') statusColor = '#ef4444';
    else if (this.state.expandedTask.status === 'pending') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.state.expandedTask.status || 'pending').toUpperCase();
    const statusWidth = this.ctx.measureText(statusText).width + 12;
    this.roundRect(contentX, currentY, statusWidth, 18, 9);
    this.ctx.fill();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.fillText(statusText, contentX + 6, currentY + 13);
    currentY += 30;

    // Description
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Description:', contentX, currentY);
    currentY += 20;

    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = '13px system-ui';
    const descLines = this.wrapText(this.state.expandedTask.description || '', this.state.expandedPanelWidth - padding * 2);
    descLines.forEach(line => {
      this.ctx.fillText(line, contentX, currentY);
      currentY += 18;
    });
    currentY += 15;

    // Agents
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText(`From: ${this.state.expandedTask.from}  →  To: ${this.state.expandedTask.to}`, contentX, currentY);
    currentY += 25;

    // Progress section (for in_progress tasks)
    if (this.state.expandedTask.status === 'in_progress') {
      this.ctx.fillStyle = '#3b82f6';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('⏳ Progress', contentX, currentY);
      currentY += 25;

      // Calculate elapsed time
      let elapsedMs = 0;
      if (this.state.expandedTask.started_at) {
        elapsedMs = Date.now() - new Date(this.state.expandedTask.started_at).getTime();
      }

      // Progress box
      const progressBoxHeight = 100;
      this.ctx.fillStyle = '#eff6ff';
      this.ctx.strokeStyle = '#3b82f6';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, currentY, this.state.expandedPanelWidth - padding * 2, progressBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      let progressY = currentY + 20;

      // Percentage or indeterminate
      const hasProgress = this.state.expandedTask.progress && this.state.expandedTask.progress.percentage !== undefined;
      if (hasProgress) {
        const percentage = this.state.expandedTask.progress.percentage;

        // Progress bar
        const barWidth = this.state.expandedPanelWidth - padding * 2 - 40;
        const barHeight = 12;
        const barX = contentX + 20;

        this.ctx.fillStyle = '#dbeafe';
        this.roundRect(barX, progressY, barWidth, barHeight, 6);
        this.ctx.fill();

        const fillWidth = (barWidth * percentage) / 100;
        this.ctx.fillStyle = '#3b82f6';
        this.roundRect(barX, progressY, fillWidth, barHeight, 6);
        this.ctx.fill();

        // Percentage text
        this.ctx.fillStyle = '#1e40af';
        this.ctx.font = 'bold 14px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText(`${percentage}%`, barX + barWidth / 2, progressY + barHeight + 18);
        this.ctx.textAlign = 'left';

        progressY += 40;

        // Current step
        if (this.state.expandedTask.progress.current_step) {
          this.ctx.fillStyle = '#1e3a8a';
          this.ctx.font = '11px system-ui';
          const stepLines = this.wrapText(this.state.expandedTask.progress.current_step, this.state.expandedPanelWidth - padding * 2 - 40);
          stepLines.forEach(line => {
            this.ctx.fillText(line, contentX + 20, progressY);
            progressY += 14;
          });
        }
      } else {
        // No specific progress - show elapsed time only
        this.ctx.fillStyle = '#1e3a8a';
        this.ctx.font = '13px system-ui';
        this.ctx.fillText('Running...', contentX + 20, progressY);
      }

      // Elapsed time
      const elapsedSeconds = Math.floor(elapsedMs / 1000);
      const minutes = Math.floor(elapsedSeconds / 60);
      const seconds = elapsedSeconds % 60;
      const timeText = minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;

      this.ctx.fillStyle = '#6b7280';
      this.ctx.font = '11px system-ui';
      this.ctx.fillText(`Elapsed: ${timeText}`, contentX + 20, currentY + progressBoxHeight - 10);

      currentY += progressBoxHeight + 20;
    }

    // Separator line
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.state.expandedPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Result section
    if (this.state.expandedTask.result) {
      this.ctx.fillStyle = '#059669';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Result', contentX, currentY);

      // Copy button
      const copyButtonWidth = 80;
      const copyButtonHeight = 24;
      const copyButtonX = panelX + this.state.expandedPanelWidth - padding - copyButtonWidth;
      const copyButtonY = currentY - 18;

      // Store bounds for click detection
      this.state.copyButtonBounds = {
        x: copyButtonX,
        y: copyButtonY,
        width: copyButtonWidth,
        height: copyButtonHeight
      };

      // Button background
      if (this.state.copyButtonState === 'copied') {
        this.ctx.fillStyle = '#10b981';
      } else if (this.state.copyButtonState === 'hover') {
        this.ctx.fillStyle = '#059669';
      } else {
        this.ctx.fillStyle = '#047857';
      }
      this.ctx.strokeStyle = '#065f46';
      this.ctx.lineWidth = 1.5;
      this.roundRect(copyButtonX, copyButtonY, copyButtonWidth, copyButtonHeight, 4);
      this.ctx.fill();
      this.ctx.stroke();

      // Button text
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 11px system-ui';
      this.ctx.textAlign = 'center';
      const buttonText = this.state.copyButtonState === 'copied' ? '✓ Copied!' : '📋 Copy';
      this.ctx.fillText(buttonText, copyButtonX + copyButtonWidth / 2, copyButtonY + copyButtonHeight / 2 + 4);
      this.ctx.textAlign = 'left';

      currentY += 25;

      // Result background box
      const resultBoxY = currentY;
      const resultBoxHeight = Math.min(300, panelHeight - currentY - padding);
      const resultBoxWidth = this.state.expandedPanelWidth - padding * 2;

      // Store bounds for scroll detection
      this.state.resultBoxBounds = {
        x: panelX + padding,
        y: resultBoxY,
        width: resultBoxWidth,
        height: resultBoxHeight
      };

      this.ctx.fillStyle = '#f0fdf4';
      this.ctx.strokeStyle = '#10b981';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, resultBoxY, resultBoxWidth, resultBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // Result text with scrolling
      this.ctx.fillStyle = '#065f46';
      this.ctx.font = '11px monospace';
      const resultLines = this.wrapText(this.state.expandedTask.result, resultBoxWidth - 40); // Extra padding for scrollbar
      const lineHeight = 14;
      const visibleLines = Math.floor((resultBoxHeight - 20) / lineHeight);
      const totalLines = resultLines.length;

      // Clamp scroll offset
      const maxScroll = Math.max(0, totalLines - visibleLines);
      this.state.resultScrollOffset = Math.max(0, Math.min(this.state.resultScrollOffset, maxScroll));

      // Enable clipping to prevent text overflow
      this.ctx.save();
      this.ctx.beginPath();
      this.ctx.rect(contentX + 5, resultBoxY + 5, resultBoxWidth - 10, resultBoxHeight - 10);
      this.ctx.clip();

      // Render visible lines based on scroll offset
      const startLine = Math.floor(this.state.resultScrollOffset);
      const endLine = Math.min(startLine + visibleLines + 1, totalLines);

      resultLines.slice(startLine, endLine).forEach((line, i) => {
        const yPos = resultBoxY + 15 + (i * lineHeight) - ((this.state.resultScrollOffset - startLine) * lineHeight);
        this.ctx.fillText(line, contentX + 10, yPos);
      });

      this.ctx.restore();

      // Draw scrollbar if content is scrollable
      if (totalLines > visibleLines) {
        const scrollbarWidth = 8;
        const scrollbarHeight = (visibleLines / totalLines) * (resultBoxHeight - 20);
        const scrollbarY = resultBoxY + 10 + (this.state.resultScrollOffset / maxScroll) * (resultBoxHeight - 20 - scrollbarHeight);

        // Scrollbar track
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.1)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, resultBoxY + 10, scrollbarWidth, resultBoxHeight - 20);

        // Scrollbar thumb
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.5)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, scrollbarY, scrollbarWidth, scrollbarHeight);
      }
    } else if (this.state.expandedTask.error) {
      this.state.resultBoxBounds = null;
      this.state.copyButtonBounds = null;
      this.ctx.fillStyle = '#dc2626';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('❌ Error', contentX, currentY);
      currentY += 25;

      this.ctx.fillStyle = '#7f1d1d';
      this.ctx.font = '11px monospace';
      const errorLines = this.wrapText(this.state.expandedTask.error, this.state.expandedPanelWidth - padding * 2);
      errorLines.slice(0, 10).forEach(line => {
        this.ctx.fillText(line, contentX, currentY);
        currentY += 14;
      });
    } else {
      this.state.resultBoxBounds = null;
      this.state.copyButtonBounds = null;
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = 'italic 12px system-ui';
      this.ctx.fillText('No result yet', contentX, currentY);
    }

    // Connection-to-agent flow removed; keep bounds null
    this.state.connectButtonBounds = null;

    this.ctx.restore();
  }

  drawExpandedAgentPanel() {
    if (!this.state.expandedAgent) return;

    const panelX = this.state.width - this.state.expandedAgentPanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.expandedAgentPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.state.expandedAgentPanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const padding = 20;
    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button (fixed, no scroll)
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.expandedAgentPanelWidth - padding, currentY + 20);
    currentY += 40;

    // Agent title (fixed, no scroll)
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Agent Details', contentX, currentY);
    currentY += 30;

    // Start scrollable content area
    const scrollableStartY = currentY;
    const scrollableHeight = panelHeight - scrollableStartY;

    // Enable clipping for scrollable area
    this.ctx.save();
    this.ctx.beginPath();
    this.ctx.rect(panelX, scrollableStartY, this.state.expandedAgentPanelWidth, scrollableHeight);
    this.ctx.clip();

    // Apply scroll offset
    currentY -= this.state.agentPanelScrollOffset;

    // Status badge
    let statusColor = '#6b7280';
    if (this.state.expandedAgent.status === 'active') statusColor = '#10b981';
    else if (this.state.expandedAgent.status === 'busy') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.state.expandedAgent.status || 'idle').toUpperCase();
    const statusWidth = this.ctx.measureText(statusText).width + 12;
    this.roundRect(contentX, currentY, statusWidth, 18, 9);
    this.ctx.fill();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.fillText(statusText, contentX + 6, currentY + 13);
    currentY += 30;

    // Agent name
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Name:', contentX, currentY);
    currentY += 18;

    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText(this.state.expandedAgent.name, contentX, currentY);
    currentY += 25;

    // Agent color indicator
    this.ctx.fillStyle = this.state.expandedAgent.color;
    this.roundRect(contentX, currentY, 30, 30, 15);
    this.ctx.fill();
    currentY += 40;

    // Last Result section (if available)
    if (this.state.expandedAgent.lastResult) {
      this.ctx.fillStyle = '#10b981'; // Green
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Last Result', contentX, currentY);
      currentY += 20;

      // Result box
      const resultBoxWidth = this.state.expandedAgentPanelWidth - padding * 2;
      const resultText = this.state.expandedAgent.lastResult.toString();

      // Wrap text for long results
      const maxLineLength = 40;
      const resultLines = this.wrapText(resultText, resultBoxWidth - 20);
      const resultBoxHeight = Math.max(60, resultLines.length * 18 + 20);

      // Background gradient
      const gradient = this.ctx.createLinearGradient(
        contentX, currentY,
        contentX, currentY + resultBoxHeight
      );
      gradient.addColorStop(0, 'rgba(16, 185, 129, 0.1)');
      gradient.addColorStop(1, 'rgba(5, 150, 105, 0.15)');

      this.ctx.fillStyle = gradient;
      this.ctx.strokeStyle = '#10b981';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, currentY, resultBoxWidth, resultBoxHeight, 8);
      this.ctx.fill();
      this.ctx.stroke();

      // Result text
      this.ctx.fillStyle = '#065f46'; // Dark green
      this.ctx.font = 'bold 14px monospace';
      resultLines.forEach((line, index) => {
        this.ctx.fillText(line, contentX + 10, currentY + 20 + index * 18);
      });

      currentY += resultBoxHeight + 20;
    }

    // Activity Statistics section
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText('📊 Activity Statistics', contentX, currentY);
    currentY += 20;

    // Statistics grid
    const stats = [
      { label: 'Current Tasks', value: this.state.expandedAgent.currentTasks?.length || 0, color: '#10b981' },
      { label: 'Queued Tasks', value: this.state.expandedAgent.queuedTasks?.length || 0, color: '#3b82f6' },
      { label: 'Completed', value: this.state.expandedAgent.completedTasks || 0, color: '#6b7280' },
      { label: 'Failed', value: this.state.expandedAgent.failedTasks || 0, color: '#ef4444' },
    ];

    stats.forEach((stat, index) => {
      // Stat box
      const statBoxWidth = (this.state.expandedAgentPanelWidth - padding * 2 - 10) / 2;
      const statBoxHeight = 50;
      const statBoxX = contentX + (index % 2) * (statBoxWidth + 10);
      const statBoxY = currentY + Math.floor(index / 2) * (statBoxHeight + 10);

      // Background
      this.ctx.fillStyle = '#f9fafb';
      this.ctx.strokeStyle = stat.color;
      this.ctx.lineWidth = 2;
      this.roundRect(statBoxX, statBoxY, statBoxWidth, statBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // Value (large)
      this.ctx.fillStyle = stat.color;
      this.ctx.font = 'bold 24px system-ui';
      this.ctx.textAlign = 'center';
      this.ctx.fillText(stat.value.toString(), statBoxX + statBoxWidth / 2, statBoxY + 22);

      // Label (small)
      this.ctx.fillStyle = '#6b7280';
      this.ctx.font = '10px system-ui';
      this.ctx.fillText(stat.label, statBoxX + statBoxWidth / 2, statBoxY + 40);
    });

    this.ctx.textAlign = 'left'; // Reset
    currentY += 120;

    // Total executions
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '11px system-ui';
    const totalExec = this.state.expandedAgent.totalExecutions || 0;
    this.ctx.fillText(`Total Executions: ${totalExec}`, contentX, currentY);
    currentY += 25;

    // Separator line
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.state.expandedAgentPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Enabled Tools section
    if (this.state.expandedAgent.config && this.state.expandedAgent.config.enabled_plugins) {
      this.ctx.fillStyle = '#7c3aed';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('🔧 Enabled Tools', contentX, currentY);
      currentY += 20;

      const plugins = this.state.expandedAgent.config.enabled_plugins;
      if (plugins.length > 0) {
        plugins.forEach(plugin => {
          // Plugin badge
          this.ctx.fillStyle = '#ede9fe';
          this.ctx.strokeStyle = '#7c3aed';
          this.ctx.lineWidth = 1;
          const pluginText = plugin.length > 20 ? plugin.substring(0, 17) + '...' : plugin;
          const badgeWidth = this.ctx.measureText(pluginText).width + 16;
          this.roundRect(contentX, currentY, badgeWidth, 22, 11);
          this.ctx.fill();
          this.ctx.stroke();

          this.ctx.fillStyle = '#5b21b6';
          this.ctx.font = '11px system-ui';
          this.ctx.fillText(pluginText, contentX + 8, currentY + 15);

          currentY += 28;
        });
        currentY += 10;
      } else {
        this.ctx.fillStyle = '#9ca3af';
        this.ctx.font = 'italic 11px system-ui';
        this.ctx.fillText('No tools enabled', contentX, currentY);
        currentY += 25;
      }

      // Separator
      this.ctx.strokeStyle = '#e5e7eb';
      this.ctx.lineWidth = 1;
      this.ctx.beginPath();
      this.ctx.moveTo(contentX, currentY);
      this.ctx.lineTo(panelX + this.state.expandedAgentPanelWidth - padding, currentY);
      this.ctx.stroke();
      currentY += 20;
    }

    // System Prompt section
    if (this.state.expandedAgent.config && this.state.expandedAgent.config.system_prompt) {
      this.ctx.fillStyle = '#ea580c';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('💬 System Prompt', contentX, currentY);
      currentY += 20;

      // System prompt box
      const promptBoxY = currentY;

      // Calculate height based on actual content (now showing ALL lines)
      this.ctx.fillStyle = '#7c2d12';
      this.ctx.font = '10px system-ui';
      const promptLines = this.wrapText(this.state.expandedAgent.config.system_prompt, this.state.expandedAgentPanelWidth - padding * 2 - 20);
      const lineHeight = 13;
      const promptBoxHeight = Math.max(60, 15 + (promptLines.length * lineHeight) + 15); // top padding + lines + bottom padding

      // Draw box
      this.ctx.fillStyle = '#fff7ed';
      this.ctx.strokeStyle = '#ea580c';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, promptBoxY, this.state.expandedAgentPanelWidth - padding * 2, promptBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // System prompt text (show ALL lines now)
      this.ctx.fillStyle = '#7c2d12';
      this.ctx.font = '10px system-ui';
      promptLines.forEach((line, i) => {
        this.ctx.fillText(line, contentX + 10, promptBoxY + 15 + i * lineHeight);
      });

      currentY += promptBoxHeight + 15;

      // Separator
      this.ctx.strokeStyle = '#e5e7eb';
      this.ctx.lineWidth = 1;
      this.ctx.beginPath();
      this.ctx.moveTo(contentX, currentY);
      this.ctx.lineTo(panelX + this.state.expandedAgentPanelWidth - padding, currentY);
      this.ctx.stroke();
      currentY += 20;
    }

    // Task count
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Active Tasks:', contentX, currentY);
    currentY += 18;

    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    const taskCount = this.state.expandedAgent.tasks ? this.state.expandedAgent.tasks.length : 0;
    this.ctx.fillText(`${taskCount} task${taskCount !== 1 ? 's' : ''}`, contentX, currentY);
    currentY += 25;

    // Tasks list
    if (taskCount > 0) {
      currentY += 10;
      this.ctx.fillStyle = '#059669';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📋 Tasks', contentX, currentY);
      currentY += 20;

      // List tasks
      const maxTasksToShow = 5;
      const tasksToShow = this.state.expandedAgent.tasks.slice(0, maxTasksToShow);

      tasksToShow.forEach((taskId, index) => {
        // Find the task details
        const task = this.state.tasks.find(t => t.id === taskId);
        if (task) {
          // Task background
          const taskBoxY = currentY;
          const taskBoxHeight = 45;
          this.ctx.fillStyle = '#f0fdf4';
          this.ctx.strokeStyle = '#10b981';
          this.ctx.lineWidth = 1;
          this.roundRect(contentX, taskBoxY, this.state.expandedAgentPanelWidth - padding * 2, taskBoxHeight, 6);
          this.ctx.fill();
          this.ctx.stroke();

          // Task description (truncated)
          this.ctx.fillStyle = '#065f46';
          this.ctx.font = '11px system-ui';
          const desc = task.description.length > 35 ? task.description.substring(0, 32) + '...' : task.description;
          this.ctx.fillText(desc, contentX + 8, taskBoxY + 15);

          // Task status
          this.ctx.fillStyle = '#6b7280';
          this.ctx.font = '9px system-ui';
          this.ctx.fillText(`Status: ${task.status}`, contentX + 8, taskBoxY + 32);

          currentY += taskBoxHeight + 8;
        }
      });

      if (this.state.expandedAgent.tasks.length > maxTasksToShow) {
        this.ctx.fillStyle = '#6b7280';
        this.ctx.font = 'italic 10px system-ui';
        this.ctx.fillText(`... and ${this.state.expandedAgent.tasks.length - maxTasksToShow} more`, contentX, currentY + 5);
        currentY += 20;
      }
    }

    // Calculate total content height
    // Note: currentY has scroll offset applied (subtracted), so add it back to get actual unscrolled content height
    const totalContentHeight = currentY + this.state.agentPanelScrollOffset - scrollableStartY + 20; // +20 for bottom padding

    // Restore clipping context
    this.ctx.restore();

    // Calculate scroll parameters
    const maxScroll = Math.max(0, totalContentHeight - scrollableHeight);
    this.state.agentPanelMaxScroll = maxScroll; // Store for wheel event handler

    // Clamp scroll offset
    this.state.agentPanelScrollOffset = Math.max(0, Math.min(this.state.agentPanelScrollOffset, maxScroll));

    // Draw scrollbar if content is scrollable
    if (maxScroll > 0) {
      const scrollbarWidth = 6;
      const scrollbarX = panelX + this.state.expandedAgentPanelWidth - padding / 2 - scrollbarWidth;
      const scrollbarHeight = Math.max(30, (scrollableHeight / totalContentHeight) * scrollableHeight);
      const scrollbarY = scrollableStartY + (this.state.agentPanelScrollOffset / maxScroll) * (scrollableHeight - scrollbarHeight);

      this.ctx.fillStyle = 'rgba(0, 0, 0, 0.2)';
      this.roundRect(scrollbarX, scrollbarY, scrollbarWidth, scrollbarHeight, 3);
      this.ctx.fill();
    }

    this.ctx.restore();
  }

  drawExpandedCombinerPanel() {
    if (!this.state.expandedCombiner) return;

    const panelX = this.state.width - this.state.expandedCombinerPanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;
    const padding = 20;

    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.25)';
    this.ctx.shadowBlur = 18;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.expandedCombinerPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    if (this.state.expandedCombinerPanelWidth < 80) {
      this.ctx.restore();
      return;
    }

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.expandedCombinerPanelWidth - padding, padding + 20);

    // Title
    let currentY = padding + 50;
    this.ctx.textAlign = 'left';
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.fillText(`${this.state.expandedCombiner.name} Node`, panelX + padding, currentY);
    currentY += 26;

    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText(`Mode: ${this.state.expandedCombiner.resultCombinationMode || 'merge'}`, panelX + padding, currentY);
    currentY += 22;

    // Inputs section
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.fillText('Inputs', panelX + padding, currentY);
    currentY += 18;

    const inputConnections = this.state.connections.filter(c => c.to === this.state.expandedCombiner.id);
    if (inputConnections.length === 0) {
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = '12px system-ui';
      this.ctx.fillText('No inputs connected', panelX + padding, currentY);
      currentY += 22;
    } else {
      inputConnections.forEach(conn => {
        const source = this.parent.getNodeById(conn.from);
        this.ctx.fillStyle = '#2563eb';
        this.ctx.font = 'bold 12px system-ui';
        this.ctx.fillText(source?.node?.description || source?.node?.name || conn.from, panelX + padding, currentY);
        currentY += 16;
      });
    }

    currentY += 10;
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(panelX + padding, currentY);
    this.ctx.lineTo(panelX + this.state.expandedCombinerPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Combined result
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.fillText('Combined Output', panelX + padding, currentY);
    currentY += 18;

    const combinedText = this.parent.buildCombinerResultPreview(this.state.expandedCombiner);
    const textLines = this.wrapText(combinedText || 'No results yet', this.state.expandedCombinerPanelWidth - padding * 2);

    this.ctx.fillStyle = combinedText ? '#111827' : '#9ca3af';
    this.ctx.font = '12px system-ui';
    textLines.slice(0, 12).forEach(line => {
      this.ctx.fillText(line, panelX + padding, currentY);
      currentY += 16;
    });

    this.ctx.restore();
  }

  drawTimelinePanel() {
    if (!this.state.timelineEvents || this.state.timelineEvents.length === 0) {
      // Show empty state
      this.drawEmptyTimeline();
      return;
    }

    const panelX = this.state.width - this.state.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.state.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.timelinePanelWidth - padding, currentY + 20);
    currentY += 40;

    // Title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Activity Timeline', contentX, currentY);
    currentY += 10;

    // Event count
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '11px system-ui';
    this.ctx.fillText(`${this.state.timelineEvents.length} recent events`, contentX, currentY);
    currentY += 25;

    // Separator
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.state.timelinePanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 15;

    // Draw events
    const maxVisibleEvents = Math.floor((panelHeight - currentY - 20) / 70);
    const visibleEvents = this.state.timelineEvents.slice(0, maxVisibleEvents);

    visibleEvents.forEach((event, index) => {
      this.drawTimelineEvent(event, contentX, currentY, this.state.timelinePanelWidth - padding * 2);
      currentY += 70;
    });

    this.ctx.restore();
  }

  drawEmptyTimeline() {
    const panelX = this.state.width - this.state.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    if (this.state.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.timelinePanelWidth - padding, currentY + 20);
    currentY += 40;

    // Title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Activity Timeline', contentX, currentY);
    currentY += 60;

    // Empty state message
    this.ctx.fillStyle = '#9ca3af';
    this.ctx.font = '13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.fillText('No activity yet', panelX + this.state.timelinePanelWidth / 2, currentY);
    this.ctx.fillText('Events will appear here', panelX + this.state.timelinePanelWidth / 2, currentY + 20);

    this.ctx.restore();
  }

  drawTimelineEvent(event, x, y, width) {
    const icon = this.parent.getEventIcon(event.type);
    const message = this.parent.getEventMessage(event);
    const time = new Date(event.timestamp).toLocaleTimeString();

    // Icon
    this.ctx.font = '18px system-ui';
    this.ctx.fillStyle = this.parent.getEventColor(event.type);
    this.ctx.fillText(icon, x, y + 14);

    // Time
    this.ctx.fillStyle = '#9ca3af';
    this.ctx.font = '10px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText(time, x + 30, y + 6);

    // Message
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = '12px system-ui';
    const lines = this.wrapText(message, width - 35);
    lines.slice(0, 2).forEach((line, i) => {
      this.ctx.fillText(line, x + 30, y + 20 + i * 16);
    });

    // Agent name (if available)
    if (event.data.agent) {
      this.ctx.fillStyle = '#6b7280';
      this.ctx.font = '10px system-ui';
      this.ctx.fillText(`Agent: ${event.data.agent}`, x + 30, y + 54);
    }
  }

}
