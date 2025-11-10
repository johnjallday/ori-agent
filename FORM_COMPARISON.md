# Form Comparison: Main Chat Page vs Workspace Dashboard

## Side-by-Side HTML Comparison

### MAIN CHAT PAGE (Reference - Correct Implementation)
**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/layout/base.tmpl`

```html
<!-- Agent Name -->
<div class="mb-3">
  <label for="agentName" class="form-label" style="color: var(--text-primary);">
    Agent Name
  </label>
  <input type="text" id="agentName" class="modern-input w-100"
         placeholder="Enter agent name..." required>
</div>

<!-- Agent Type -->
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

<!-- Model -->
<div class="mb-3">
  <label for="agentModel" class="form-label" style="color: var(--text-primary);">
    Model
  </label>
  <select id="agentModel" class="modern-input w-100">
    <option value="">Loading models...</option>
  </select>
</div>

<!-- Temperature -->
<div class="mb-3">
  <label for="agentTemperature" class="form-label"
         style="color: var(--text-primary);">
    Temperature: <span id="temperatureValue">1.0</span>
  </label>
  <input type="range" id="agentTemperature" class="form-range"
         min="0" max="2" step="0.1" value="1.0"
         style="cursor: pointer;">
  <div class="d-flex justify-content-between"
       style="font-size: 0.75rem; color: var(--text-muted);">
    <span>Focused (0)</span>
    <span>Balanced (1)</span>
    <span>Creative (2)</span>
  </div>
</div>

<!-- System Prompt -->
<div class="mb-3">
  <label for="agentSystemPrompt" class="form-label"
         style="color: var(--text-primary);">
    System Prompt
  </label>
  <textarea id="agentSystemPrompt" class="form-control" rows="4"
            style="background: var(--bg-tertiary); border: 1px solid var(--border-color);
                   color: var(--text-primary); font-size: 0.9em; resize: vertical;"
            placeholder="You are a helpful assistant with access to various tools...">
  </textarea>
</div>
```

**Key Points**:
- Uses `modern-input` class for all text/select inputs
- Full width: `w-100`
- Proper spacing: `mb-3` between fields
- Label styling with CSS variable colors
- Clear, descriptive placeholders

---

### WORKSPACE DASHBOARD (Current Implementation)
**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/pages/workspace-dashboard.tmpl`

```html
<div class="col-md-6 mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Agent Name
  </label>
  <input type="text" id="new-agent-name" class="form-control"
         placeholder="my-agent" required>
</div>

<div class="col-md-6 mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Type
  </label>
  <select id="new-agent-type" class="form-control">
    <option value="tool-calling">Tool-Calling Agent</option>
    <option value="chat">Chat Agent</option>
  </select>
</div>

<div class="col-md-8 mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Model
  </label>
  <select id="new-agent-model" class="form-control">
    <option value="">Loading models...</option>
  </select>
</div>

<div class="col-md-4 mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Temperature
  </label>
  <input type="number" id="new-agent-temperature" class="form-control"
         placeholder="1.0" step="0.1" min="0" max="2">
</div>

<div class="mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    System Prompt (optional)
  </label>
  <textarea id="new-agent-prompt" class="form-control" rows="2"
            placeholder="You are a helpful assistant..."></textarea>
</div>
```

**Issues Found**:
1. Uses `form-control` instead of `modern-input`
2. Layout uses Bootstrap grid with multiple columns (not matching main design)
3. Temperature is number input instead of slider
4. Missing temperature helper labels (Focused/Balanced/Creative)
5. System prompt is 2 rows instead of 4
6. Missing descriptive option labels in Agent Type dropdown

---

## CSS Class Comparison

### modern-input (Correct)
```css
.modern-input {
  border: 1px solid rgba(255, 255, 255, 0.2);
  border-radius: var(--radius-md);      /* 0.5rem */
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
```

**Features**:
- Glassmorphism effect (blur + transparency)
- Automatic dark mode support
- Smooth transitions
- Blue focus glow (rgba(99, 102, 241, 0.2))
- Consistent with overall design theme

### form-control (Bootstrap - Less Consistent)
```css
/* Bootstrap default */
.form-control {
  /* Lacks the glassmorphism effect */
  /* No backdrop-filter blur */
  /* Plain white/gray background */
}
```

**Disadvantages**:
- No glassmorphism effect
- Doesn't match the modern design theme
- Less elegant visual appearance
- Inconsistent with rest of the sidebar forms

---

## Option Labels Comparison

### Agent Type - MAIN PAGE (Better UX)
```
Tool Calling (Cheapest - Optimized for tool use)
General Purpose (Mid-tier - Balanced capability)
Research (Expensive - Complex thinking)
```
- Includes cost tier information
- Includes capabilities description
- Helps users make informed choice

### Agent Type - WORKSPACE (Less Helpful)
```
Tool-Calling Agent
Chat Agent
```
- Generic labels
- No context about differences
- User doesn't know which to choose

---

## Temperature Field Comparison

### MAIN PAGE (Better UX - Slider)
```html
<div class="mb-3">
  <label for="agentTemperature" class="form-label">
    Temperature: <span id="temperatureValue">1.0</span>
  </label>
  <input type="range" id="agentTemperature" class="form-range"
         min="0" max="2" step="0.1" value="1.0">
  <div class="d-flex justify-content-between"
       style="font-size: 0.75rem; color: var(--text-muted);">
    <span>Focused (0)</span>
    <span>Balanced (1)</span>
    <span>Creative (2)</span>
  </div>
</div>
```
- Visual slider (more intuitive)
- Real-time value display
- Helper labels explaining what each value means
- Bootstrap `form-range` styling

### WORKSPACE (Less Intuitive - Number Input)
```html
<div class="col-md-4 mb-3">
  <label class="form-label">Temperature</label>
  <input type="number" id="new-agent-temperature" class="form-control"
         placeholder="1.0" step="0.1" min="0" max="2">
</div>
```
- Plain number input
- No visual representation
- No helper text about what values mean
- Takes up more vertical space

---

## System Prompt Field Comparison

### MAIN PAGE (More Space for Input)
```html
<div class="mb-3">
  <label for="agentSystemPrompt" class="form-label">
    System Prompt
  </label>
  <textarea id="agentSystemPrompt" class="form-control" rows="4"
            style="background: var(--bg-tertiary); border: 1px solid var(--border-color);
                   color: var(--text-primary); font-size: 0.9em; resize: vertical;"
            placeholder="You are a helpful assistant...">
  </textarea>
</div>
```
- 4 rows height (more space for typing)
- Dark background (var(--bg-tertiary)) for better contrast
- `resize: vertical` allows users to expand if needed
- Clear, helpful placeholder text

### WORKSPACE (Less Space)
```html
<div class="mb-3">
  <label class="form-label">
    System Prompt (optional)
  </label>
  <textarea id="new-agent-prompt" class="form-control" rows="2"
            placeholder="You are a helpful assistant..."></textarea>
</div>
```
- Only 2 rows (not enough space)
- Uses generic `form-control` background
- Less space for detailed prompts
- Labeled as "optional" (might discourage use)

---

## Summary of Required Changes

| Aspect | Main Page | Workspace | Change Needed |
|--------|-----------|-----------|---------------|
| Input Class | `modern-input` | `form-control` | Change to `modern-input` |
| Layout | Single column | Multi-column grid | Keep single column for consistency |
| Temperature | Range slider | Number input | Change to range slider + labels |
| Temperature Labels | Focused/Balanced/Creative | None | Add helper labels |
| System Prompt Rows | 4 | 2 | Increase to 4 |
| Textarea Background | var(--bg-tertiary) | default | Style with darker background |
| Agent Type Options | Detailed descriptions | Generic labels | Add descriptions for each option |
| Agent Type Options | 3 types (tool-calling, general, research) | 2 types (tool-calling, chat) | Match option values with main page |

---

## Code to Update in workspace-dashboard.tmpl

### Required HTML Changes:

1. **Replace form-control with modern-input**
```html
<!-- BEFORE -->
<input type="text" id="new-agent-name" class="form-control" placeholder="my-agent" required>

<!-- AFTER -->
<input type="text" id="new-agent-name" class="modern-input w-100" placeholder="my-agent" required>
```

2. **Update Agent Type Options**
```html
<!-- BEFORE -->
<select id="new-agent-type" class="form-control">
  <option value="tool-calling">Tool-Calling Agent</option>
  <option value="chat">Chat Agent</option>
</select>

<!-- AFTER -->
<select id="new-agent-type" class="modern-input w-100">
  <option value="tool-calling">Tool Calling (Cheapest - Optimized for tool use)</option>
  <option value="general">General Purpose (Mid-tier - Balanced capability)</option>
  <option value="research">Research (Expensive - Complex thinking)</option>
</select>
```

3. **Replace Temperature Number Input with Slider**
```html
<!-- BEFORE -->
<input type="number" id="new-agent-temperature" class="form-control"
       placeholder="1.0" step="0.1" min="0" max="2">

<!-- AFTER -->
<div class="mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Temperature: <span id="new-agent-temperature-value">1.0</span>
  </label>
  <input type="range" id="new-agent-temperature" class="form-range"
         min="0" max="2" step="0.1" value="1.0" style="cursor: pointer;">
  <div class="d-flex justify-content-between"
       style="font-size: 0.75rem; color: var(--text-muted);">
    <span>Focused (0)</span>
    <span>Balanced (1)</span>
    <span>Creative (2)</span>
  </div>
</div>
```

4. **Update System Prompt Textarea**
```html
<!-- BEFORE -->
<textarea id="new-agent-prompt" class="form-control" rows="2"
          placeholder="You are a helpful assistant..."></textarea>

<!-- AFTER -->
<textarea id="new-agent-prompt" class="form-control" rows="4"
          style="background: var(--bg-tertiary); border: 1px solid var(--border-color);
                 color: var(--text-primary); font-size: 0.9em; resize: vertical;"
          placeholder="You are a helpful assistant with access to various tools. When a user request can be fulfilled by using an available tool, use the tool instead of providing general information. Be concise and direct in your responses."></textarea>
```

5. **Layout Changes** (move form fields to single column if using grid)
```html
<!-- Change from multi-column grid to full-width fields -->
<form id="createAgentForm" class="modern-card p-3">
  <!-- Remove nested col-md-6/col-md-8/col-md-4 divs -->
  <!-- Make each field full width with mb-3 spacing -->
  <div class="mb-3">
    <!-- Agent Name -->
  </div>
  <div class="mb-3">
    <!-- Agent Type -->
  </div>
  <div class="mb-3">
    <!-- Model -->
  </div>
  <div class="mb-3">
    <!-- Temperature -->
  </div>
  <div class="mb-3">
    <!-- System Prompt -->
  </div>
</form>
```

### Required JavaScript Changes:

Add temperature update handler:
```javascript
// Update temperature display value in real-time
const temperatureInput = document.getElementById('new-agent-temperature');
const temperatureValue = document.getElementById('new-agent-temperature-value');
if (temperatureInput && temperatureValue) {
  temperatureInput.addEventListener('input', (e) => {
    temperatureValue.textContent = e.target.value;
  });
}
```

---

## Files Involved

1. **Main Chat Page (Reference)**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/layout/base.tmpl` (lines 57-122)
2. **Workspace Dashboard (Needs Update)**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/pages/workspace-dashboard.tmpl` (lines 234-308)
3. **Shared CSS**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/templates/layout/head.tmpl` (defines `modern-input`, etc.)
4. **Shared JavaScript**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/static/js/modules/agents.js` (utility functions)
