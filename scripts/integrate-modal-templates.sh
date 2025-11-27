#!/bin/bash
# Script to integrate extracted modal templates back into studios.tmpl

TEMPLATE_FILE="internal/web/templates/pages/studios.tmpl"
BACKUP_FILE="${TEMPLATE_FILE}.backup-modals-$(date +%Y%m%d-%H%M%S)"

echo "Creating backup: $BACKUP_FILE"
cp "$TEMPLATE_FILE" "$BACKUP_FILE"

echo "Integrating modal template includes..."

# Create new file with:
# 1. Lines 1-258 (before modals)
# 2. Template includes for the 3 modals
# 3. Lines 436-end (scripts and closing tags)

{
    # Part 1: Everything before the modals (lines 1-258)
    sed -n '1,258p' "$TEMPLATE_FILE"

    # Part 2: Template includes for modals
    cat << 'EOF'

  <!-- Modal Templates -->
  {{template "components/studios/manage-agents-modal.tmpl" .}}
  {{template "components/studios/create-workspace-modal.tmpl" .}}
  {{template "components/studios/workspace-details-modal.tmpl" .}}

EOF

    # Part 3: Scripts and closing tags (lines 436-end)
    sed -n '436,$p' "$TEMPLATE_FILE"

} > "${TEMPLATE_FILE}.new"

echo "Replacing original file..."
mv "${TEMPLATE_FILE}.new" "$TEMPLATE_FILE"

echo "Done! Backup saved to: $BACKUP_FILE"
echo ""
echo "Changes:"
echo "  - Removed 177 lines of inline modal HTML (lines 259-435)"
echo "  - Added 3 template include directives"
echo "  - Net reduction: ~171 lines"
echo ""
echo "New file structure:"
wc -l "$TEMPLATE_FILE"
echo ""
echo "To restore backup if needed:"
echo "  cp $BACKUP_FILE $TEMPLATE_FILE"
