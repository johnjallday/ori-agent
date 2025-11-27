#!/bin/bash
# Script to remove duplicate inline JavaScript from studios.tmpl
# All functionality has been extracted to external modules

TEMPLATE_FILE="internal/web/templates/pages/studios.tmpl"
BACKUP_FILE="${TEMPLATE_FILE}.backup-$(date +%Y%m%d-%H%M%S)"

echo "Creating backup: $BACKUP_FILE"
cp "$TEMPLATE_FILE" "$BACKUP_FILE"

echo "Removing duplicate inline script (lines 466-2306)..."

# Create new file with:
# 1. Lines 1-465 (everything before inline script)
# 2. Minimal script replacement
# 3. Lines 2307-end (theme manager script and closing tags)

{
    # Part 1: Header through line 465
    sed -n '1,465p' "$TEMPLATE_FILE"

    # Part 2: Minimal script replacement
    cat << 'EOF'

  <script>
    // NOTE: Most JavaScript has been extracted to external modules:
    // - studios-workspace.js: Core workspace CRUD & polling
    // - studios-agent-modals.js: Agent management modals
    // - studios-workspace-create.js: Workspace creation
    // - studios-canvas-helpers.js: Canvas visualization & interactions

    // Shared state (accessed by modules via window object)
    window.availableAgents = [];
    window.selectedAgents = new Set();

    // All initialization is handled by the modules via their own DOMContentLoaded handlers
    // No additional page-specific initialization needed
  </script>
EOF

    # Part 3: Theme manager and closing tags (lines 2307-end)
    sed -n '2307,$p' "$TEMPLATE_FILE"

} > "${TEMPLATE_FILE}.new"

echo "Replacing original file..."
mv "${TEMPLATE_FILE}.new" "$TEMPLATE_FILE"

echo "Done! Backup saved to: $BACKUP_FILE"
echo ""
echo "Changes:"
echo "  - Removed ~1,840 lines of duplicate JavaScript"
echo "  - Kept only minimal inline script for shared state"
echo "  - All functionality now in external modules"
echo ""
echo "To restore backup if needed:"
echo "  cp $BACKUP_FILE $TEMPLATE_FILE"
