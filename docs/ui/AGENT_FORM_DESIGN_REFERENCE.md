# Create New Agent Form - Design & Structure Guide

## Overview
The Create New Agent form appears in two locations in your application:
1. **Main Chat Page** - Modal in the sidebar (`addAgentModal`)
2. **Workspace Dashboard** - Modal manage agents section (`manageAgentsModal`)

This guide shows you how the form is styled and structured on the main chat page so you can match that design in the workspace dashboard.

---

## Main Chat Page Form (Reference Design)
**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/layout/base.tmpl`
**Lines**: 57-122

### HTML Structure

```html
<!-- Add Agent Modal -->
<div class="modal fade" id="addAgentModal" tabindex="-1" aria-labelledby="addAgentModalLabel" aria-hidden="true">
  <div class="modal-dialog">
    <div class="modal-content" style="background: var(--bg-primary); border: 1px solid var(--border-color);">
      <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
        <h5 class="modal-title" id="addAgentModalLabel" style="color: var(--text-primary);">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
            <!-- Agent icon SVG -->
          </svg>
          Create New Agent
        </h5>
        <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"
                style="filter: invert(1);"></button>
      </div>

      <div class="modal-body">
        <form id="addAgentForm">
          <!-- Form fields go here -->
        </form>
      </div>

      <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
        <button type="button" class="modern-btn modern-btn-secondary" data-bs-dismiss="modal">
          Cancel
        </button>
        <button type="button" id="createAgentBtn" class="modern-btn modern-btn-primary">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <!-- Plus icon SVG -->
          </svg>
          Create Agent
        </button>
      </div>
    </div>
  </div>
</div>
```

---

## Form Fields Styling

### 1. Agent Name Field
```html
<div class="mb-3">
  <label for="agentName" class="form-label" style="color: var(--text-primary);">
    Agent Name
  </label>
  <input type="text" id="agentName" class="modern-input w-100"
         placeholder="Enter agent name..." required>
</div>
```

**Styling Details**:
- Class: `modern-input` (custom glassmorphism style)
- Margin bottom: `mb-3` (Bootstrap spacing)
- Full width: `w-100`
- Label color: `color: var(--text-primary)`
- Placeholder text is descriptive

**CSS from head.tmpl (lines 200-226)**:
```css
.modern-input {
  border: 1px solid rgba(255, 255, 255, 0.2);
  border-radius: var(--radius-md);
  padding: 0.75rem;
  background: rgba(255, 255, 255, 0.1);
  backdrop-filter: blur(10px);
  color: var(--text-primary);
  font-size: 14px;
  transition: all 0.2s ease;
}

.modern-input:focus {
  outline: none;
  border-color: rgba(255, 255, 255, 0.4);
  background: rgba(255, 255, 255, 0.15);
  box-shadow: 0 0 0 3px rgba(99, 102, 241, 0.2);
}

.dark-mode .modern-input {
  background: rgba(15, 23, 42, 0.3);
  border: 1px solid rgba(255, 255, 255, 0.1);
}

.dark-mode .modern-input:focus {
  background: rgba(15, 23, 42, 0.5);
  border-color: rgba(255, 255, 255, 0.2);
}
```

---

### 2. Agent Type Dropdown
```html
<div class="mb-3">
  <label for="agentType" class="form-label" style="color: var(--text-primary);">
    Agent Type
  </label>
  <select id="agentType" class="modern-input w-100">
    <option value="tool-calling">Tool Calling (Cheapest - Optimized for tool use)</option>
    <option value="general">General Purpose (Mid-tier - Balanced capability)</option>
    <option value="research">Research (Expensive - Complex thinking)</option>
  </select>
</div>
```

**Styling Details**:
- Class: `modern-input` (same as text inputs)
- Full width: `w-100`
- Options include helpful descriptions
- Default selected: "tool-calling" (cheapest tier)

**Key Feature**: Options include descriptions to help users understand the difference:
- **Tool Calling** - Cheapest, optimized for tool use
- **General Purpose** - Mid-tier, balanced
- **Research** - Most expensive, complex thinking

---

### 3. Model Dropdown
```html
<div class="mb-3">
  <label for="agentModel" class="form-label" style="color: var(--text-primary);">
    Model
  </label>
  <select id="agentModel" class="modern-input w-100">
    <!-- Models will be loaded dynamically from /api/providers -->
    <option value="">Loading models...</option>
  </select>
</div>
```

**Styling Details**:
- Class: `modern-input` (consistent with other form fields)
- Dynamically populated by JavaScript
- Default: "Loading models..." placeholder option
- Models filtered by selected agent type

**Related Functions** (from agents.js):
```javascript
// Populate model select with options from available providers
function populateModelSelect(modelSelect, selectedType = 'tool-calling') {
  if (!modelSelect || availableProviders.length === 0) return;

  // Clear existing options
  modelSelect.innerHTML = '';

  // Group models by provider
  availableProviders.forEach(provider => {
    const providerGroup = document.createElement('optgroup');
    providerGroup.label = provider.display_name;

    provider.models.forEach(model => {
      const option = document.createElement('option');
      option.value = model.value;
      option.textContent = model.label;
      option.setAttribute('data-type', model.type);
      option.setAttribute('data-provider', model.provider);

      // Only show models matching the selected type
      if (model.type !== selectedType) {
        option.style.display = 'none';
        option.disabled = true;
      }

      providerGroup.appendChild(option);
    });

    modelSelect.appendChild(providerGroup);
  });

  // Select first available option
  for (let i = 0; i < modelSelect.options.length; i++) {
    if (!modelSelect.options[i].disabled) {
      modelSelect.selectedIndex = i;
      break;
    }
  }
}
```

---

### 4. Temperature Slider
```html
<div class="mb-3">
  <label for="agentTemperature" class="form-label" style="color: var(--text-primary);">
    Temperature: <span id="temperatureValue">1.0</span>
  </label>
  <input type="range" id="agentTemperature" class="form-range"
         min="0" max="2" step="0.1" value="1.0"
         style="cursor: pointer;">
  <div class="d-flex justify-content-between" style="font-size: 0.75rem; color: var(--text-muted);">
    <span>Focused (0)</span>
    <span>Balanced (1)</span>
    <span>Creative (2)</span>
  </div>
</div>
```

**Styling Details**:
- Uses Bootstrap `form-range` class
- Range: 0-2 with 0.1 step increments
- Default value: 1.0 (Balanced)
- Helper text labels:
  - **0** = Focused (deterministic, precise)
  - **1** = Balanced (default, recommended)
  - **2** = Creative (more varied responses)
- Real-time value display in label

**JavaScript Handler** (from agents.js, lines 594-600):
```javascript
// Temperature slider update
const agentTemperatureInput = document.getElementById('agentTemperature');
const temperatureValueSpan = document.getElementById('temperatureValue');
if (agentTemperatureInput && temperatureValueSpan) {
  agentTemperatureInput.addEventListener('input', (e) => {
    temperatureValueSpan.textContent = e.target.value;
  });
}
```

---

### 5. System Prompt Textarea
```html
<div class="mb-3">
  <label for="agentSystemPrompt" class="form-label"
         style="color: var(--text-primary);">
    System Prompt
  </label>
  <textarea id="agentSystemPrompt" class="form-control" rows="4"
            style="background: var(--bg-tertiary); border: 1px solid var(--border-color);
                   color: var(--text-primary); font-size: 0.9em; resize: vertical;"
            placeholder="You are a helpful assistant with access to various tools.
                        When a user request can be fulfilled by using an available tool,
                        use the tool instead of providing general information.
                        Be concise and direct in your responses.">
  </textarea>
</div>
```

**Styling Details**:
- Uses Bootstrap `form-control` class (more spacious than input)
- 4 rows default height
- Background: `var(--bg-tertiary)` (slightly darker background for text areas)
- Border: `1px solid var(--border-color)`
- Font size: `0.9em`
- Vertical resize only: `resize: vertical`
- Detailed placeholder text showing expected format

**Important Note**: The placeholder includes a well-crafted system prompt template to help users understand what to write.

---

## Button Styling

### Primary Button (Create Agent)
```html
<button type="button" id="createAgentBtn" class="modern-btn modern-btn-primary">
  <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
    <!-- Plus icon SVG -->
  </svg>
  Create Agent
</button>
```

**Classes & CSS** (from head.tmpl, lines 128-198):
```css
.modern-btn {
  border-radius: var(--radius-md);
  font-weight: 500;
  font-size: 13px;
  padding: 0.5rem 1rem;
  transition: all 0.2s ease;
  border: 1px solid rgba(255, 255, 255, 0.2);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  backdrop-filter: blur(10px);
  position: relative;
  overflow: hidden;
}

.modern-btn::before {
  content: '';
  position: absolute;
  top: 0;
  left: -100%;
  width: 100%;
  height: 100%;
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.2), transparent);
  transition: left 0.5s;
}

.modern-btn:hover::before {
  left: 100%;
}

.modern-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 25px rgba(0, 0, 0, 0.15);
  border-color: rgba(255, 255, 255, 0.3);
}

.modern-btn-primary {
  background: linear-gradient(135deg, var(--primary-color) 0%, var(--primary-light) 100%);
  color: white;
}
```

**Button Features**:
- Glassmorphism design with backdrop filter
- Gradient background for primary button
- Smooth hover animation (shine effect + lift)
- Icon + text layout using flexbox
- Gap between icon and text: `0.5rem`

### Secondary Button (Cancel)
```html
<button type="button" class="modern-btn modern-btn-secondary" data-bs-dismiss="modal">
  Cancel
</button>
```

**CSS** (from head.tmpl, lines 170-188):
```css
.modern-btn-secondary {
  background: rgba(255, 255, 255, 0.1);
  color: var(--text-primary);
  border: 1px solid rgba(255, 255, 255, 0.2);
}

.modern-btn-secondary:hover {
  background: rgba(255, 255, 255, 0.2);
  color: var(--text-primary);
}

.dark-mode .modern-btn-secondary {
  background: rgba(15, 23, 42, 0.3);
  color: var(--text-primary);
}

.dark-mode .modern-btn-secondary:hover {
  background: rgba(15, 23, 42, 0.5);
}
```

---

## Form Layout Structure

### Current Main Page (Single Column)
```html
<form id="addAgentForm">
  <!-- Agent Name -->
  <div class="mb-3">...</div>

  <!-- Agent Type -->
  <div class="mb-3">...</div>

  <!-- Model -->
  <div class="mb-3">...</div>

  <!-- Temperature -->
  <div class="mb-3">...</div>

  <!-- System Prompt -->
  <div class="mb-3">...</div>
</form>
```

### Workspace Dashboard (Two Column - NOT YET MATCHING)
```html
<form id="createAgentForm" class="modern-card p-3">
  <div class="row">
    <div class="col-md-6 mb-3">
      <!-- Agent Name -->
    </div>
    <div class="col-md-6 mb-3">
      <!-- Type -->
    </div>
  </div>
  <div class="row">
    <div class="col-md-8 mb-3">
      <!-- Model -->
    </div>
    <div class="col-md-4 mb-3">
      <!-- Temperature -->
    </div>
  </div>
  <div class="mb-3">
    <!-- System Prompt -->
  </div>
</form>
```

**Current Issue**: Workspace dashboard uses `form-control` instead of `modern-input`

---

## CSS Variables Used

All styling relies on these CSS custom properties defined in head.tmpl:

```css
:root {
  /* Colors */
  --primary-color: #1d4ed8;
  --primary-light: #2563eb;
  --primary-dark: #1e40af;
  --secondary-color: #64748b;
  --success-color: #10b981;
  --danger-color: #ef4444;
  --warning-color: #f59e0b;
  --info-color: #06b6d4;

  /* Backgrounds */
  --bg-primary: #ffffff;
  --bg-secondary: #f8fafc;
  --bg-tertiary: #f1f5f9;

  /* Text */
  --text-primary: #0f172a;
  --text-secondary: #475569;
  --text-muted: #94a3b8;

  /* Borders */
  --border-color: #e2e8f0;
  --border-light: #f1f5f9;

  /* Border Radius */
  --radius-sm: 0.375rem;
  --radius-md: 0.5rem;
  --radius-lg: 0.75rem;
  --radius-xl: 1rem;
}

.dark-mode {
  --bg-primary: #0f172a;
  --bg-secondary: #1e293b;
  --bg-tertiary: #334155;
  --text-primary: #f8fafc;
  --text-secondary: #cbd5e1;
  --text-muted: #94a3b8;
  --border-color: #334155;
  --border-light: #475569;
}
```

---

## Key JavaScript Functions

### Loading Available Providers
**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/static/js/modules/agents.js`
**Lines**: 11-21

```javascript
// Fetch available providers and models from API
async function loadAvailableProviders() {
  try {
    const response = await fetch('/api/providers');
    const data = await response.json();
    availableProviders = data.providers || [];
    return availableProviders;
  } catch (error) {
    console.error('Failed to load providers:', error);
    return [];
  }
}
```

### Initializing Models on Page Load
**Lines**: 64-79

```javascript
// Initialize models on page load
async function initializeModels() {
  await loadAvailableProviders();

  // Populate the model select in the create agent modal
  const agentModelSelect = document.getElementById('agentModel');
  if (agentModelSelect) {
    populateModelSelect(agentModelSelect, 'tool-calling');
  }
}

// Call initialization when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initializeModels);
} else {
  initializeModels();
}
```

### Filtering Models by Agent Type
**Lines**: 134-139

```javascript
// Filter models based on agent type
function filterModelsByType(agentType, modelSelect) {
  if (!modelSelect) return;

  // Repopulate the select with filtered models
  populateModelSelect(modelSelect, agentType);
}
```

### Event Listener for Type Change
**Lines**: 603-609

```javascript
// Agent type selector update - filter models when type changes
const agentTypeInput = document.getElementById('agentType');
const agentModelInput = document.getElementById('agentModel');
if (agentTypeInput && agentModelInput) {
  agentTypeInput.addEventListener('change', (e) => {
    filterModelsByType(e.target.value, agentModelInput);
  });
}
```

### Creating New Agent
**Lines**: 142-241

```javascript
// Create new agent
async function createNewAgent() {
  const agentNameInput = document.getElementById('agentName');
  const agentTypeInput = document.getElementById('agentType');
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');
  const agentModelInput = document.getElementById('agentModel');
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const createBtn = document.getElementById('createAgentBtn');

  if (!agentNameInput) return;

  const agentName = agentNameInput.value.trim();
  if (!agentName) {
    alert('Please enter an agent name');
    agentNameInput.focus();
    return;
  }

  // Set loading state
  const originalText = createBtn.textContent;
  createBtn.disabled = true;
  createBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Creating...';

  try {
    const requestBody = { name: agentName };

    // Add agent type if provided
    if (agentTypeInput && agentTypeInput.value) {
      requestBody.type = agentTypeInput.value;
    }

    // Add model if provided
    if (agentModelInput && agentModelInput.value) {
      requestBody.model = agentModelInput.value;
    }

    // Add temperature if provided
    if (agentTemperatureInput && agentTemperatureInput.value) {
      requestBody.temperature = parseFloat(agentTemperatureInput.value);
    }

    // Add system prompt if provided
    if (agentSystemPromptInput && agentSystemPromptInput.value.trim()) {
      requestBody.system_prompt = agentSystemPromptInput.value.trim();
    }

    const response = await fetch('/api/agents', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(requestBody)
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    // Success - close modal and refresh agent list
    const modal = bootstrap.Modal.getInstance(document.getElementById('addAgentModal'));
    if (modal) {
      modal.hide();
    }

    // Clear form
    agentNameInput.value = '';
    if (agentSystemPromptInput) {
      agentSystemPromptInput.value = '';
    }
    // ... reset other fields

    // Refresh the agent list
    await refreshAgentList();
    window.location.reload();

  } catch (error) {
    console.error('Error creating agent:', error);
    alert(`Failed to create agent: ${error.message}`);
  } finally {
    // Reset button state
    createBtn.disabled = false;
    createBtn.innerHTML = originalText;
  }
}
```

---

## Summary of Design Patterns

1. **Consistent Input Styling**
   - All form inputs use `modern-input` class
   - Includes glassmorphism effect (backdrop-filter blur)
   - Consistent padding, border radius, and transitions

2. **Form Layout**
   - Bootstrap `mb-3` for vertical spacing between fields
   - Labels with `color: var(--text-primary)` for visibility
   - Placeholders are descriptive and helpful

3. **Button Styling**
   - Primary buttons: gradient background, white text
   - Secondary buttons: transparent with subtle background
   - Hover effects: lift + shine animation
   - Icon + text layout with gap spacing

4. **Color Scheme**
   - Glassmorphism theme with transparency and blur effects
   - Dark mode support with CSS variables
   - Consistent variable usage throughout

5. **JavaScript Initialization**
   - Models loaded asynchronously from /api/providers
   - Models grouped by provider and filtered by agent type
   - Temperature value displayed in real-time
   - Form submission validation before API call

---

## Files Referenced

1. **HTML Template**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/layout/base.tmpl` (lines 57-122)
2. **CSS Styles**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/layout/head.tmpl` (lines 16-1333)
3. **JavaScript Logic**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/static/js/modules/agents.js` (all functions)
4. **Workspace Dashboard**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/pages/workspace-dashboard.tmpl` (lines 234-308)

---

## Next Steps for Workspace Dashboard

To match the main chat page design in the workspace dashboard modal:

1. **Replace form-control with modern-input**
   - Change `class="form-control"` to `class="modern-input w-100"`

2. **Update the modal header**
   - Add background styling to match primary design

3. **Import and use the same JavaScript functions**
   - Use `populateModelSelect()` instead of custom implementation
   - Use `filterModelsByType()` for type changes
   - Reuse `loadAvailableProviders()` function

4. **Match button styling**
   - Use `modern-btn modern-btn-primary` and `modern-btn modern-btn-secondary` classes
   - Include icon with text

5. **Ensure dark mode support**
   - All inputs should automatically support dark mode via CSS variables
