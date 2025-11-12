# Agent Form - Quick Reference Guide

## Key Files

| File | Purpose | Lines |
|------|---------|-------|
| `/internal/web/templates/layout/base.tmpl` | Main chat page form (REFERENCE) | 57-122 |
| `/internal/web/templates/pages/workspace-dashboard.tmpl` | Workspace modal form (NEEDS UPDATE) | 234-308 |
| `/internal/web/templates/layout/head.tmpl` | CSS styles (modern-input, modern-btn, etc.) | 16-1333 |
| `/internal/web/static/js/modules/agents.js` | Form logic & utilities | All |

---

## CSS Classes Cheat Sheet

### Input Styling
```html
<!-- TEXT INPUT (Correct) -->
<input type="text" class="modern-input w-100">

<!-- DROPDOWN/SELECT (Correct) -->
<select class="modern-input w-100">...</select>

<!-- TEMPERATURE SLIDER (Correct) -->
<input type="range" class="form-range">

<!-- TEXTAREA (Can use form-control with styling) -->
<textarea class="form-control" style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.9em; resize: vertical;">
```

### Button Styling
```html
<!-- PRIMARY BUTTON -->
<button class="modern-btn modern-btn-primary">
  <svg>...</svg>
  Create Agent
</button>

<!-- SECONDARY BUTTON -->
<button class="modern-btn modern-btn-secondary">Cancel</button>
```

### Layout
```html
<!-- FIELD SPACING -->
<div class="mb-3">
  <label class="form-label" style="color: var(--text-primary);">...</label>
  <input class="modern-input w-100">
</div>

<!-- FLEXBOX LAYOUTS -->
<div class="d-flex justify-content-between">...</div>
<div class="d-flex align-items-center gap-2">...</div>
```

---

## CSS Variables

All colors are defined with CSS variables (auto dark-mode support):

```css
/* Colors */
--primary-color: #1d4ed8
--text-primary: #0f172a (text on light background)
--text-secondary: #475569
--text-muted: #94a3b8
--bg-primary: #ffffff
--bg-secondary: #f8fafc
--bg-tertiary: #f1f5f9
--border-color: #e2e8f0

/* Radius */
--radius-md: 0.5rem (6px)
--radius-lg: 0.75rem (12px)
```

---

## Form Fields Checklist

### Agent Name
- Class: `modern-input w-100`
- Placeholder: "Enter agent name..."
- Required: yes

### Agent Type
- Class: `modern-input w-100`
- Options:
  - "Tool Calling (Cheapest - Optimized for tool use)"
  - "General Purpose (Mid-tier - Balanced capability)"
  - "Research (Expensive - Complex thinking)"
- Default: "tool-calling"

### Model
- Class: `modern-input w-100`
- Populated by: `populateModelSelect()` function
- Filtered by agent type
- Placeholder: "Loading models..."

### Temperature
- Type: `range` (slider, NOT number input)
- Class: `form-range`
- Range: 0-2, step 0.1, default 1.0
- Display value in label: `<span id="temperatureValue">1.0</span>`
- Helper labels below slider:
  - "Focused (0)" - deterministic, precise
  - "Balanced (1)" - default, recommended
  - "Creative (2)" - varied responses

### System Prompt
- Type: `textarea`
- Rows: 4 (not 2!)
- Class: `form-control`
- Style: `background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.9em; resize: vertical;`
- Placeholder: "You are a helpful assistant with access to various tools. When a user request can be fulfilled by using an available tool, use the tool instead of providing general information. Be concise and direct in your responses."

---

## JavaScript Functions to Use

### Load Providers & Models
```javascript
// Already exists in agents.js
loadAvailableProviders()      // Fetch from /api/providers
populateModelSelect()          // Populate dropdown with models
filterModelsByType()           // Filter by agent type
```

### Event Handlers
```javascript
// Temperature slider
document.getElementById('new-agent-temperature').addEventListener('input', (e) => {
  document.getElementById('new-agent-temperature-value').textContent = e.target.value;
});

// Agent type change
document.getElementById('new-agent-type').addEventListener('change', (e) => {
  const modelSelect = document.getElementById('new-agent-model');
  populateModelSelect(modelSelect, e.target.value);
});

// Form submit
document.getElementById('createAgentForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  // Create agent via POST /api/agents
});
```

---

## Common Mistakes to Avoid

1. **Using form-control instead of modern-input**
   - form-control lacks glassmorphism effect
   - Doesn't match app design

2. **Temperature as number input instead of slider**
   - Less intuitive for users
   - Missing helper labels (Focused/Balanced/Creative)

3. **System prompt textarea with only 2 rows**
   - Not enough space for detailed prompts
   - Should be 4 rows

4. **Missing descriptive option labels**
   - Agent Type options need to explain cost/capability differences
   - Users need context to make informed choices

5. **Wrong form layout**
   - Main page: single column (each field full width)
   - Workspace: currently multi-column (inconsistent)

6. **Forgetting to import JavaScript functions**
   - `agents.js` already loaded on both pages
   - Reuse existing functions instead of duplicating

---

## Testing Checklist

- [ ] Form fields use `modern-input` class (except textarea)
- [ ] Fields are full width (`w-100`)
- [ ] Labels are styled with `color: var(--text-primary)`
- [ ] Temperature uses slider with helper labels
- [ ] Temperature value displays in real-time
- [ ] Agent Type options have descriptive text
- [ ] System prompt textarea is 4 rows high
- [ ] System prompt textarea has dark background
- [ ] Models populate dynamically from API
- [ ] Models filter when agent type changes
- [ ] Form submission validation works
- [ ] Buttons use `modern-btn` classes
- [ ] Dark mode works correctly
- [ ] Layout matches single-column design

---

## Copy-Paste Templates

### Basic Field
```html
<div class="mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Field Name
  </label>
  <input type="text" id="field-id" class="modern-input w-100"
         placeholder="Placeholder text" required>
</div>
```

### Dropdown Field
```html
<div class="mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Dropdown Label
  </label>
  <select id="field-id" class="modern-input w-100">
    <option value="">Default option</option>
    <option value="value1">Option 1</option>
  </select>
</div>
```

### Temperature Slider
```html
<div class="mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Temperature: <span id="temp-value">1.0</span>
  </label>
  <input type="range" id="temp-slider" class="form-range"
         min="0" max="2" step="0.1" value="1.0" style="cursor: pointer;">
  <div class="d-flex justify-content-between"
       style="font-size: 0.75rem; color: var(--text-muted);">
    <span>Focused (0)</span>
    <span>Balanced (1)</span>
    <span>Creative (2)</span>
  </div>
</div>
```

### Textarea
```html
<div class="mb-3">
  <label class="form-label" style="color: var(--text-primary);">
    Label Text
  </label>
  <textarea id="field-id" class="form-control" rows="4"
            style="background: var(--bg-tertiary); border: 1px solid var(--border-color);
                   color: var(--text-primary); font-size: 0.9em; resize: vertical;"
            placeholder="Placeholder text..."></textarea>
</div>
```

### Button Group
```html
<div class="d-flex gap-2 justify-content-end">
  <button type="button" class="modern-btn modern-btn-secondary"
          data-bs-dismiss="modal">
    Cancel
  </button>
  <button type="button" class="modern-btn modern-btn-primary">
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
      <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
    </svg>
    Create Agent
  </button>
</div>
```

---

## API Endpoints

### Create Agent
```javascript
POST /api/agents
Body: {
  name: string (required),
  type: string (tool-calling|general|research),
  model: string,
  temperature: number (0-2),
  system_prompt: string
}
```

### Get Available Providers
```javascript
GET /api/providers
Returns: {
  providers: [
    {
      display_name: string,
      models: [
        {
          value: string,
          label: string,
          type: string (tool-calling|general|research),
          provider: string
        }
      ]
    }
  ]
}
```

### Get Agent Settings
```javascript
GET /api/settings?agent={agentName}
Returns: {
  Settings: {
    model: string,
    temperature: number,
    system_prompt: string
  }
}
```

---

## Visual Hierarchy

1. **Modal Title**: Large, bold, with icon
2. **Field Labels**: Medium, primary text color
3. **Form Fields**: Large input areas with 0.75rem padding
4. **Helper Text**: Small, muted color (0.75rem or 0.875rem)
5. **Buttons**: Medium sized, 0.5rem 1rem padding

---

## Accessibility Notes

- All inputs have associated labels
- Labels use `for` attribute pointing to input `id`
- Color variables ensure contrast in dark mode
- Focus states include blue glow
- Temperature slider has aria-friendly structure
- Buttons have clear text labels + icons

---

## Performance Tips

- Models loaded asynchronously (don't block page)
- Provider data cached in `availableProviders` variable
- Temperature value updated via listener (not rerender)
- Form submission shows loading state with spinner
- Modal reused for consistency
