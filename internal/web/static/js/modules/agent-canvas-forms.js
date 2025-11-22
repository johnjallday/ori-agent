/**
 * AgentCanvasForms - Manages all form UI for the canvas
 * Handles Create Task and Add Agent forms
 */
export class AgentCanvasForms {
  constructor(canvas) {
    this.canvas = canvas;

    // Create Task form state
    this.createTaskFormVisible = false;
    this.createTaskDescription = '';
    this.createTaskDescriptionFocused = false;
    this.createTaskAssignToAgent = false;
    this.selectedAgentForTask = null;
    this.agentSelectionBounds = [];

    // Add Agent form state
    this.addAgentFormVisible = false;
    this.availableAgentsForAdd = [];
    this.selectedAgentToAdd = null;
    this.agentAddSelectionBounds = [];

    // Form bounds for click detection
    this.createTaskFormBounds = null;
    this.createTaskCloseButtonBounds = null;
    this.createTaskSubmitButtonBounds = null;
    this.createTaskCheckboxBounds = null;
    this.createTaskDescriptionBounds = null;

    this.addAgentFormBounds = null;
    this.addAgentCloseButtonBounds = null;
    this.addAgentSubmitButtonBounds = null;
  }

  // ========== CREATE TASK FORM ==========

  showCreateTaskForm() {
    this.createTaskFormVisible = true;
    this.createTaskDescription = '';
    this.createTaskAssignToAgent = false;
    this.selectedAgentForTask = null;
    this.agentSelectionBounds = [];
    this.canvas.draw();
  }

  hideCreateTaskForm() {
    this.createTaskFormVisible = false;
    this.createTaskDescription = '';
    this.createTaskDescriptionFocused = false;
    this.createTaskAssignToAgent = false;
    this.selectedAgentForTask = null;
    this.agentSelectionBounds = [];
    this.canvas.canvas.style.cursor = 'grab';
    this.canvas.draw();
  }

  async submitCreateTaskForm() {
    if (!this.createTaskDescription || this.createTaskDescription.trim() === '') {
      alert('Please enter a task description');
      return;
    }

    if (!this.canvas.studioId) {
      alert('Error: Workspace ID not found. Please refresh the page and try again.');
      console.error('Canvas studioId is not set:', this.canvas.studioId);
      return;
    }

    const requestBody = {
      studio_id: this.canvas.studioId,
      from: 'user',
      description: this.createTaskDescription.trim(),
      priority: 0,
    };

    if (this.createTaskAssignToAgent && this.selectedAgentForTask) {
      requestBody.to = this.selectedAgentForTask;
    } else {
      requestBody.to = 'unassigned';
    }

    console.log('Creating task with request body:', requestBody);

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(requestBody)
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to create task');
      }

      const result = await response.json();
      console.log('✅ Task created:', result);

      this.hideCreateTaskForm();
      await this.canvas.init();
      alert(`✅ Task created successfully!`);
    } catch (error) {
      console.error('❌ Error creating task:', error);
      alert('Failed to create task: ' + error.message);
    }
  }

  drawCreateTaskForm() {
    const ctx = this.canvas.ctx;
    const width = this.canvas.width;
    const height = this.canvas.height;

    // Semi-transparent overlay
    ctx.fillStyle = 'rgba(0, 0, 0, 0.4)';
    ctx.fillRect(0, 0, width, height);

    const formWidth = 600;
    const formHeight = 500;
    const formX = (width - formWidth) / 2;
    const formY = (height - formHeight) / 2;
    const padding = 20;

    this.createTaskFormBounds = { x: formX, y: formY, width: formWidth, height: formHeight };

    ctx.save();

    // Form background
    ctx.fillStyle = '#ffffff';
    ctx.strokeStyle = '#e5e7eb';
    ctx.lineWidth = 1;
    ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    ctx.shadowBlur = 20;
    ctx.shadowOffsetY = 10;
    this.canvas.roundRect(formX, formY, formWidth, formHeight, 12);
    ctx.fill();
    ctx.stroke();
    ctx.shadowColor = 'transparent';

    let currentY = formY + padding;

    // Title and close button
    ctx.fillStyle = '#1f2937';
    ctx.font = 'bold 22px system-ui';
    ctx.fillText('Create New Task', formX + padding, currentY + 20);

    const closeBtnSize = 30;
    const closeBtnX = formX + formWidth - padding - closeBtnSize;
    const closeBtnY = currentY;

    ctx.fillStyle = '#6b7280';
    ctx.font = 'bold 24px system-ui';
    ctx.textAlign = 'center';
    ctx.fillText('×', closeBtnX + closeBtnSize / 2, closeBtnY + 22);
    ctx.textAlign = 'left';

    this.createTaskCloseButtonBounds = { x: closeBtnX, y: closeBtnY, width: closeBtnSize, height: closeBtnSize };

    currentY += 50;

    // Note
    ctx.fillStyle = '#6b7280';
    ctx.font = '12px system-ui';
    const noteText = 'Note: Please use the dashboard below to create tasks with more options.';
    const noteLines = this.canvas.wrapText(noteText, formWidth - padding * 2);
    noteLines.forEach((line, i) => {
      ctx.fillText(line, formX + padding, currentY + i * 16);
    });
    currentY += noteLines.length * 16 + 20;

    // Section title
    ctx.fillStyle = '#1f2937';
    ctx.font = 'bold 14px system-ui';
    ctx.fillText('Quick Task (Unassigned)', formX + padding, currentY);
    currentY += 25;

    ctx.fillStyle = '#6b7280';
    ctx.font = '12px system-ui';
    ctx.fillText('Creates a task without assigning to a specific agent.', formX + padding, currentY);
    currentY += 25;

    // Description field label
    ctx.fillStyle = '#4b5563';
    ctx.font = 'bold 12px system-ui';
    ctx.fillText('Task Description:', formX + padding, currentY);
    currentY += 20;

    // Description field
    const inputHeight = 80;
    ctx.fillStyle = '#f3f4f6';

    if (this.createTaskDescriptionFocused) {
      ctx.strokeStyle = '#3b82f6';
      ctx.lineWidth = 2;
    } else {
      ctx.strokeStyle = '#d1d5db';
      ctx.lineWidth = 1;
    }

    this.canvas.roundRect(formX + padding, currentY, formWidth - padding * 2, inputHeight, 6);
    ctx.fill();
    ctx.stroke();

    if (!this.createTaskDescription || this.createTaskDescription.trim() === '') {
      ctx.fillStyle = '#9ca3af';
      ctx.font = 'italic 12px system-ui';
      const placeholderText = this.createTaskDescriptionFocused
        ? 'Type your task description... (Press Enter when done)'
        : 'Click here to enter task description...';
      ctx.fillText(placeholderText, formX + padding + 10, currentY + 20);
    } else {
      ctx.fillStyle = '#1f2937';
      ctx.font = '12px system-ui';
      const descLines = this.canvas.wrapText(this.createTaskDescription, formWidth - padding * 2 - 20);
      descLines.slice(0, 5).forEach((line, i) => {
        ctx.fillText(line, formX + padding + 10, currentY + 18 + i * 15);
      });

      if (this.createTaskDescriptionFocused) {
        const cursorVisible = Math.floor(Date.now() / 500) % 2 === 0;
        if (cursorVisible) {
          ctx.fillStyle = '#3b82f6';
          const lastLine = descLines[descLines.length - 1] || '';
          const textWidth = ctx.measureText(lastLine).width;
          const cursorX = formX + padding + 10 + textWidth;
          const cursorY = currentY + 18 + (descLines.length - 1) * 15;
          ctx.fillRect(cursorX, cursorY - 12, 2, 14);
        }
        requestAnimationFrame(() => this.canvas.draw());
      }
    }

    this.createTaskDescriptionBounds = {
      x: formX + padding,
      y: currentY,
      width: formWidth - padding * 2,
      height: inputHeight
    };

    currentY += inputHeight + 20;

    // Checkbox
    const checkboxSize = 18;
    ctx.strokeStyle = '#d1d5db';
    ctx.lineWidth = 2;
    this.canvas.roundRect(formX + padding, currentY - 13, checkboxSize, checkboxSize, 3);
    ctx.stroke();

    if (this.createTaskAssignToAgent) {
      ctx.fillStyle = '#3b82f6';
      this.canvas.roundRect(formX + padding + 3, currentY - 10, checkboxSize - 6, checkboxSize - 6, 2);
      ctx.fill();
    }

    ctx.fillStyle = '#4b5563';
    ctx.font = 'bold 13px system-ui';
    ctx.fillText('Assign to specific agent', formX + padding + checkboxSize + 10, currentY);

    this.createTaskCheckboxBounds = { x: formX + padding, y: currentY - 13, width: checkboxSize, height: checkboxSize };

    currentY += 30;

    // Agent selection
    if (this.createTaskAssignToAgent) {
      const agentButtonHeight = 40;
      this.agentSelectionBounds = [];

      this.canvas.agents.slice(0, 5).forEach((agent, index) => {
        const isSelected = this.selectedAgentForTask === agent.name;

        ctx.fillStyle = isSelected ? '#dbeafe' : '#f3f4f6';
        ctx.strokeStyle = isSelected ? '#3b82f6' : '#d1d5db';
        ctx.lineWidth = isSelected ? 2 : 1;
        this.canvas.roundRect(formX + padding, currentY, formWidth - padding * 2, agentButtonHeight, 6);
        ctx.fill();
        ctx.stroke();

        ctx.fillStyle = '#1f2937';
        ctx.font = 'bold 13px system-ui';
        ctx.fillText(agent.name, formX + padding + 10, currentY + 24);

        this.agentSelectionBounds[index] = {
          x: formX + padding,
          y: currentY,
          width: formWidth - padding * 2,
          height: agentButtonHeight,
          agentName: agent.name
        };

        currentY += agentButtonHeight + 5;
      });
      currentY += 10;
    }

    // Create button (centered)
    const buttonWidth = 120;
    const buttonHeight = 36;
    const buttonX = formX + (formWidth - buttonWidth) / 2;
    const buttonY = formY + formHeight - padding - buttonHeight - 10;

    ctx.fillStyle = '#3b82f6';
    ctx.strokeStyle = '#1e40af';
    ctx.lineWidth = 2;
    this.canvas.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    ctx.fill();
    ctx.stroke();

    ctx.fillStyle = '#ffffff';
    ctx.font = 'bold 13px system-ui';
    ctx.textAlign = 'center';
    ctx.fillText('Create', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2 + 1);
    ctx.textAlign = 'left';

    this.createTaskSubmitButtonBounds = { x: buttonX, y: buttonY, width: buttonWidth, height: buttonHeight };

    ctx.restore();
  }

  // ========== ADD AGENT FORM ==========

  async showAddAgentForm() {
    try {
      const response = await fetch('/api/agents');
      if (!response.ok) {
        throw new Error(`Failed to fetch agents: ${response.status}`);
      }
      const data = await response.json();
      this.availableAgentsForAdd = data.agents || data || [];

      const currentAgentNames = this.canvas.agents.map(a => a.name);
      this.availableAgentsForAdd = this.availableAgentsForAdd.filter(
        agent => !currentAgentNames.includes(agent.name)
      );

      this.selectedAgentToAdd = null;
      this.addAgentFormVisible = true;
      this.canvas.draw();
    } catch (error) {
      console.error('Error loading agents:', error);
      alert('Failed to load available agents: ' + error.message);
    }
  }

  hideAddAgentForm() {
    this.addAgentFormVisible = false;
    this.selectedAgentToAdd = null;
    this.availableAgentsForAdd = [];
    this.canvas.draw();
  }

  async submitAddAgentForm() {
    if (!this.selectedAgentToAdd) {
      alert('Please select an agent to add');
      return;
    }

    if (!this.canvas.studioId) {
      alert('Error: Workspace ID not found');
      return;
    }

    try {
      const response = await fetch(`/api/studios/${this.canvas.studioId}/agents`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agent_name: this.selectedAgentToAdd })
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to add agent');
      }

      console.log('✅ Agent added:', this.selectedAgentToAdd);

      this.hideAddAgentForm();
      await this.canvas.init();
      this.canvas.addNotification(`✅ Agent "${this.selectedAgentToAdd}" added successfully!`, 'success');
    } catch (error) {
      console.error('❌ Error adding agent:', error);
      alert('Failed to add agent: ' + error.message);
    }
  }

  drawAddAgentForm() {
    const ctx = this.canvas.ctx;
    const width = this.canvas.width;
    const height = this.canvas.height;

    // Semi-transparent overlay
    ctx.fillStyle = 'rgba(0, 0, 0, 0.4)';
    ctx.fillRect(0, 0, width, height);

    const formWidth = 500;
    const formHeight = 400;
    const formX = (width - formWidth) / 2;
    const formY = (height - formHeight) / 2;
    const padding = 20;

    this.addAgentFormBounds = { x: formX, y: formY, width: formWidth, height: formHeight };

    ctx.save();

    // Form background
    ctx.fillStyle = '#ffffff';
    ctx.strokeStyle = '#e5e7eb';
    ctx.lineWidth = 1;
    ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    ctx.shadowBlur = 20;
    ctx.shadowOffsetY = 10;
    this.canvas.roundRect(formX, formY, formWidth, formHeight, 12);
    ctx.fill();
    ctx.stroke();
    ctx.shadowColor = 'transparent';

    let currentY = formY + padding;

    // Title and close button
    ctx.fillStyle = '#1f2937';
    ctx.font = 'bold 22px system-ui';
    ctx.fillText('Add Agent', formX + padding, currentY + 20);

    const closeBtnSize = 30;
    const closeBtnX = formX + formWidth - padding - closeBtnSize;
    const closeBtnY = currentY;

    ctx.fillStyle = '#6b7280';
    ctx.font = 'bold 24px system-ui';
    ctx.textAlign = 'center';
    ctx.fillText('×', closeBtnX + closeBtnSize / 2, closeBtnY + 22);
    ctx.textAlign = 'left';

    this.addAgentCloseButtonBounds = { x: closeBtnX, y: closeBtnY, width: closeBtnSize, height: closeBtnSize };

    currentY += 50;

    // Description
    ctx.fillStyle = '#6b7280';
    ctx.font = '14px system-ui';
    ctx.fillText('Select an agent to add to this studio:', formX + padding, currentY);
    currentY += 30;

    // Agent list
    if (this.availableAgentsForAdd.length === 0) {
      ctx.fillStyle = '#9ca3af';
      ctx.font = 'italic 14px system-ui';
      ctx.fillText('No available agents to add', formX + padding, currentY + 20);
    } else {
      const agentButtonHeight = 50;
      this.agentAddSelectionBounds = [];

      this.availableAgentsForAdd.slice(0, 5).forEach((agent, index) => {
        const isSelected = this.selectedAgentToAdd === agent.name;

        ctx.fillStyle = isSelected ? '#dbeafe' : '#f3f4f6';
        ctx.strokeStyle = isSelected ? '#3b82f6' : '#d1d5db';
        ctx.lineWidth = isSelected ? 2 : 1;
        this.canvas.roundRect(formX + padding, currentY, formWidth - padding * 2, agentButtonHeight, 6);
        ctx.fill();
        ctx.stroke();

        ctx.fillStyle = '#1f2937';
        ctx.font = 'bold 14px system-ui';
        ctx.fillText(agent.name, formX + padding + 15, currentY + 22);

        if (agent.role) {
          ctx.fillStyle = '#6b7280';
          ctx.font = '12px system-ui';
          ctx.fillText(agent.role, formX + padding + 15, currentY + 38);
        }

        this.agentAddSelectionBounds.push({
          x: formX + padding,
          y: currentY,
          width: formWidth - padding * 2,
          height: agentButtonHeight,
          agentName: agent.name
        });

        currentY += agentButtonHeight + 10;
      });
    }

    // Add button (centered)
    const buttonWidth = 120;
    const buttonHeight = 36;
    const buttonX = formX + (formWidth - buttonWidth) / 2;
    const buttonY = formY + formHeight - padding - buttonHeight - 10;

    ctx.fillStyle = this.selectedAgentToAdd ? '#10b981' : '#9ca3af';
    ctx.strokeStyle = this.selectedAgentToAdd ? '#059669' : '#6b7280';
    ctx.lineWidth = 2;
    this.canvas.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    ctx.fill();
    ctx.stroke();

    ctx.fillStyle = '#ffffff';
    ctx.font = 'bold 13px system-ui';
    ctx.textAlign = 'center';
    ctx.fillText('Add Agent', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2 + 1);
    ctx.textAlign = 'left';

    this.addAgentSubmitButtonBounds = { x: buttonX, y: buttonY, width: buttonWidth, height: buttonHeight };

    ctx.restore();
  }
}
