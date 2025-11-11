# Agent Form Styling Documentation Index

This directory contains comprehensive documentation on how to style the Create New Agent form to match the main chat page design in the workspace dashboard modal.

## Documentation Files

### 1. AGENT_FORM_DESIGN_REFERENCE.md (19 KB)
**Complete design guide with detailed technical specifications**

Contents:
- Overview of form locations
- Complete HTML structure breakdown
- Individual field styling with CSS examples
- Button styling with hover effects
- Form layout patterns
- CSS variables reference
- Key JavaScript functions with code examples
- Design patterns and philosophy
- File references

**Best for:** Understanding the complete design system and how everything fits together

**Quick Links:**
- Form Fields Styling (5 fields detailed)
- Button Styling (primary & secondary)
- CSS Variables Used (colors, spacing, radius)
- Key JavaScript Functions (with line numbers)

---

### 2. FORM_COMPARISON.md (13 KB)
**Side-by-side comparison of main page vs workspace dashboard**

Contents:
- Main page form (correct implementation)
- Workspace dashboard form (current implementation)
- HTML comparison blocks
- CSS class comparison
- Option labels comparison
- Temperature field comparison
- System prompt field comparison
- Summary table of required changes
- Code snippets ready to copy-paste
- Required JavaScript changes

**Best for:** Seeing exactly what needs to change and why

**Quick Links:**
- Side-by-Side HTML Comparison
- CSS Class Comparison (modern-input vs form-control)
- Option Labels Comparison
- Temperature Field Comparison
- Summary of Required Changes (table format)
- Code to Update in workspace-dashboard.tmpl

---

### 3. AGENT_FORM_QUICK_REFERENCE.md (9.1 KB)
**Quick lookup guide and cheatsheet**

Contents:
- Key files table
- CSS classes cheatsheet
- CSS variables list
- Form fields checklist (5 detailed specs)
- JavaScript functions to use
- Event handlers examples
- Common mistakes to avoid (6 items)
- Testing checklist (14 items)
- Copy-paste templates (5 ready-to-use)
- API endpoints reference
- Visual hierarchy
- Accessibility notes
- Performance tips

**Best for:** Quick reference while coding, copy-paste templates

**Quick Links:**
- CSS Classes Cheat Sheet
- Form Fields Checklist (with all specs)
- Copy-Paste Templates (5 ready-to-use blocks)
- Common Mistakes to Avoid
- Testing Checklist
- API Endpoints

---

## File Locations in Your Project

**Form Implementation Files:**
```
ori-agent/
├── internal/web/templates/layout/base.tmpl (lines 57-122)  [REFERENCE - Main Chat Page]
├── internal/web/templates/pages/workspace-dashboard.tmpl (lines 234-308)  [NEEDS UPDATE]
├── internal/web/templates/layout/head.tmpl (lines 16-1333)  [CSS Styles]
└── internal/web/static/js/modules/agents.js  [JavaScript Functions]
```

**Documentation Files (in project root):**
```
ori-agent/
├── FORM_STYLING_INDEX.md (this file)
├── AGENT_FORM_DESIGN_REFERENCE.md
├── FORM_COMPARISON.md
└── AGENT_FORM_QUICK_REFERENCE.md
```

---

## Key Design Elements

### Form Fields (5 total)
1. **Agent Name** - Text input, required
2. **Agent Type** - Dropdown with 3 options, descriptive labels
3. **Model** - Dropdown, dynamically populated from API
4. **Temperature** - Range slider (0-2) with helper labels
5. **System Prompt** - Textarea, 4 rows

### CSS Classes
- **modern-input** - Glassmorphism input styling
- **form-range** - Range slider styling
- **form-control** - Text area styling (with custom styles)
- **modern-btn** - Button base styling
- **modern-btn-primary** - Primary button (gradient)
- **modern-btn-secondary** - Secondary button (transparent)

### CSS Variables (Auto Dark Mode)
- Primary color: #1d4ed8
- Text color: var(--text-primary)
- Background: var(--bg-primary), var(--bg-secondary), var(--bg-tertiary)
- Border: var(--border-color)
- Spacing: var(--radius-md), var(--radius-lg)

---

## Quick Start Guide

### For Understanding the Design
1. Read: **AGENT_FORM_DESIGN_REFERENCE.md**
   - Understand the overall system
   - See all CSS details
   - Learn JavaScript functions

### For Making Updates
1. Open: **FORM_COMPARISON.md**
   - See what changed
   - Copy new code
   - Understand why each change matters

### For Quick Reference While Coding
1. Use: **AGENT_FORM_QUICK_REFERENCE.md**
   - Copy templates
   - Check specifications
   - Verify with testing checklist

---

## Common Tasks

### "How do I style an input field?"
→ See QUICK_REFERENCE.md → Copy-Paste Templates → Basic Field

### "What's the difference between the main page and dashboard?"
→ See FORM_COMPARISON.md → Side-by-Side HTML Comparison

### "Where's the CSS for modern-input?"
→ See DESIGN_REFERENCE.md → Form Fields Styling → Agent Name Field

### "What are all the Agent Type options?"
→ See QUICK_REFERENCE.md → Form Fields Checklist → Agent Type

### "How do I handle the temperature slider?"
→ See QUICK_REFERENCE.md → JavaScript Functions to Use → Event Handlers

### "What should I test?"
→ See QUICK_REFERENCE.md → Testing Checklist

### "What are common mistakes?"
→ See QUICK_REFERENCE.md → Common Mistakes to Avoid

---

## Files to Reference While Coding

**For HTML Structure:**
- `/internal/web/templates/layout/base.tmpl` (lines 57-122)

**For CSS Styling:**
- `/internal/web/templates/layout/head.tmpl` (lines 200-226 for modern-input, 128-198 for buttons)

**For JavaScript Logic:**
- `/internal/web/static/js/modules/agents.js` (functions: loadAvailableProviders, populateModelSelect, filterModelsByType)

**For Templates to Update:**
- `/internal/web/templates/pages/workspace-dashboard.tmpl` (lines 234-308)

---

## Summary of Changes Needed

| Item | Current | Target | Priority |
|------|---------|--------|----------|
| Input Class | form-control | modern-input | High |
| Temperature Field | Number input | Range slider | High |
| Temperature Labels | None | Focused/Balanced/Creative | High |
| System Prompt Rows | 2 | 4 | High |
| Agent Type Options | Generic | Descriptive (with cost tiers) | Medium |
| Form Layout | Multi-column | Single column | Medium |
| System Prompt Styling | Default | Dark background | Medium |

---

## Additional Notes

- All CSS variables are defined in head.tmpl and automatically support dark mode
- The agents.js file has all necessary functions already written
- Both pages use Bootstrap 5.3 for base styles
- Modern-input class includes glassmorphism effect (backdrop-filter blur)
- Temperature slider has real-time value display
- Form validation happens in JavaScript before API call

---

## Document Statistics

| Document | Size | Content |
|----------|------|---------|
| AGENT_FORM_DESIGN_REFERENCE.md | 19 KB | 500+ lines, complete guide |
| FORM_COMPARISON.md | 13 KB | 400+ lines, detailed comparison |
| AGENT_FORM_QUICK_REFERENCE.md | 9.1 KB | 300+ lines, quick reference |
| FORM_STYLING_INDEX.md | This file | Index and navigation |

**Total Documentation:** 41 KB of comprehensive, detailed guides

---

## Version Information

- Created: 2025-01-09
- Documentation Version: 1.0
- Ori Agent Version: Latest (feature/manage-agents-modal-box branch)
- Bootstrap Version: 5.3.0
- Tested Against: base.tmpl (lines 57-122) and workspace-dashboard.tmpl (lines 234-308)

---

## Contact/Questions

All information sourced from:
1. Main chat page form: `/internal/web/templates/layout/base.tmpl`
2. CSS definitions: `/internal/web/templates/layout/head.tmpl`
3. JavaScript utilities: `/internal/web/static/js/modules/agents.js`
4. Current dashboard: `/internal/web/templates/pages/workspace-dashboard.tmpl`

For questions about styling, refer to the specific document linked above.
