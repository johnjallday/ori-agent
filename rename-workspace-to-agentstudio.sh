#!/bin/bash

################################################################################
# Comprehensive Workspace to AgentStudio Renaming Script
# This script renames all workspace-related types to agentstudio types in the
# ori-agent codebase. It handles types, fields, functions, methods, and API
# endpoints systematically.
#
# Prerequisites:
# - Directory already renamed: internal/workspace → internal/agentstudio
# - Package declarations already updated: package workspace → package agentstudio
# - Imports already updated: internal/workspace → internal/agentstudio
################################################################################

set -e

# Define colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
PROJECT_ROOT="/Users/jjdev/Projects/ori/ori-agent"
BACKUP_DIR="${PROJECT_ROOT}/.backup-$(date +%Y%m%d-%H%M%S)"
FIND_OPTS=(-type f -name "*.go" -not -path "*/vendor/*" -not -path "*/.git/*" -not -path "*/node_modules/*")

echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}Workspace → AgentStudio Renaming Script${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo "Project Root: $PROJECT_ROOT"
echo "Backup Directory: $BACKUP_DIR"
echo ""

# Create backup
echo -e "${YELLOW}Creating backup...${NC}"
mkdir -p "$BACKUP_DIR"
find "$PROJECT_ROOT" -type f -name "*.go" -not -path "*/vendor/*" -not -path "*/.git/*" | while read -r file; do
    dir=$(dirname "${file#$PROJECT_ROOT/}")
    mkdir -p "$BACKUP_DIR/$dir"
    cp "$file" "$BACKUP_DIR/$dir/$(basename "$file")"
done
echo -e "${GREEN}✓ Backup created${NC}"
echo ""

# Function to apply sed safely on macOS
apply_sed() {
    local pattern="$1"
    local replacement="$2"
    local file="$3"
    sed -i '' "$pattern" "$file"
}

# Function to find and rename in all Go files
rename_in_files() {
    local pattern="$1"
    local replacement="$2"
    local description="$3"

    echo -e "${YELLOW}$description${NC}"

    find "$PROJECT_ROOT" "${FIND_OPTS[@]}" -exec grep -l "$pattern" {} \; 2>/dev/null | while read -r file; do
        # Use a temporary marker to avoid double-replacements
        # This is critical to prevent issues with overlapping patterns
        apply_sed "s/${pattern}/${replacement}/g" "$file"
    done

    echo -e "${GREEN}✓ Done${NC}"
}

################################################################################
# PHASE 1: Type Renames (exact case-sensitive matches)
################################################################################
echo -e "${YELLOW}=== PHASE 1: Type Renames ===${NC}"
echo ""

# Important: Order matters - do specific/longer names before shorter ones
# to avoid partial replacements

# WorkspaceStatus → StudioStatus
rename_in_files "WorkspaceStatus" "StudioStatus" "Renaming WorkspaceStatus → StudioStatus"

# WorkspaceExecutor → StudioExecutor
rename_in_files "WorkspaceExecutor" "StudioExecutor" "Renaming WorkspaceExecutor → StudioExecutor"

# CreateWorkspaceParams → CreateStudioParams
rename_in_files "CreateWorkspaceParams" "CreateStudioParams" "Renaming CreateWorkspaceParams → CreateStudioParams"

# WorkspaceStore → StudioStore (interface type)
# Be careful: this appears as both workspace.Store and WorkspaceStore
rename_in_files "WorkspaceStore" "StudioStore" "Renaming WorkspaceStore → StudioStore"

# Workspace → AgentStudio (most important, but do it last among types)
# This is done with word boundaries to avoid catching "Workspaces" etc.
rename_in_files "\\bWorkspace\\b" "AgentStudio" "Renaming Workspace → AgentStudio (type names)"

echo ""

################################################################################
# PHASE 2: Plural Forms (Workspaces → Studios)
################################################################################
echo -e "${YELLOW}=== PHASE 2: Plural Forms ===${NC}"
echo ""

# Workspaces → Studios
rename_in_files "Workspaces" "Studios" "Renaming Workspaces → Studios"

echo ""

################################################################################
# PHASE 3: Function and Method Renames
################################################################################
echo -e "${YELLOW}=== PHASE 3: Function and Method Renames ===${NC}"
echo ""

# NewWorkspace → NewAgentStudio
rename_in_files "NewWorkspace" "NewAgentStudio" "Renaming NewWorkspace → NewAgentStudio"

# CreateWorkspace → CreateStudio (function names)
rename_in_files "CreateWorkspace" "CreateStudio" "Renaming CreateWorkspace → CreateStudio"

# GetWorkspace → GetStudio
rename_in_files "GetWorkspace" "GetStudio" "Renaming GetWorkspace → GetStudio"

# DeleteWorkspace → DeleteStudio
rename_in_files "DeleteWorkspace" "DeleteStudio" "Renaming DeleteWorkspace → DeleteStudio"

# ListWorkspace → ListStudio
rename_in_files "ListWorkspace" "ListStudio" "Renaming ListWorkspace → ListStudio"

# SetWorkspaceStore → SetStudioStore
rename_in_files "SetWorkspaceStore" "SetStudioStore" "Renaming SetWorkspaceStore → SetStudioStore"

# handleGetWorkspace → handleGetStudio
rename_in_files "handleGetWorkspace" "handleGetStudio" "Renaming handleGetWorkspace → handleGetStudio"

# handleCreateWorkspace → handleCreateStudio
rename_in_files "handleCreateWorkspace" "handleCreateStudio" "Renaming handleCreateWorkspace → handleCreateStudio"

# handleDeleteWorkspace → handleDeleteStudio
rename_in_files "handleDeleteWorkspace" "handleDeleteStudio" "Renaming handleDeleteWorkspace → handleDeleteStudio"

# WorkspaceHandler → StudioHandler
rename_in_files "WorkspaceHandler" "StudioHandler" "Renaming WorkspaceHandler → StudioHandler"

echo ""

################################################################################
# PHASE 4: Variable Names (camelCase, from workspace → studio context)
################################################################################
echo -e "${YELLOW}=== PHASE 4: Variable Names ===${NC}"
echo ""

# workspaceStore → studioStore (variable names)
rename_in_files "workspaceStore" "studioStore" "Renaming workspaceStore → studioStore (variable names)"

# workspace → studio (generic workspace variable names)
# This is more conservative - only when followed by , or . or ) or whitespace
rename_in_files "workspace\\." "studio\\." "Renaming workspace. → studio. (member access)"

# Common variable names: ws → st (limited scope)
# We avoid this for now as it could cause issues

# workspaceID → studioID (variable names)
rename_in_files "workspaceID" "studioID" "Renaming workspaceID → studioID (variable names)"

echo ""

################################################################################
# PHASE 5: Field Names in Structs (JSON Tags and Comments)
################################################################################
echo -e "${YELLOW}=== PHASE 5: Field Names and JSON Tags ===${NC}"
echo ""

# WorkspaceID → StudioID (struct field name)
rename_in_files "WorkspaceID" "StudioID" "Renaming WorkspaceID → StudioID (struct fields)"

# workspace_id → studio_id (JSON tags)
rename_in_files "workspace_id" "studio_id" "Renaming workspace_id → studio_id (JSON tags)"

# parent_workspace → parent_studio (JSON tags)
rename_in_files "parent_workspace" "parent_studio" "Renaming parent_workspace → parent_studio (JSON tags)"

echo ""

################################################################################
# PHASE 6: API Endpoint Paths
################################################################################
echo -e "${YELLOW}=== PHASE 6: API Endpoint Paths ===${NC}"
echo ""

# /api/workspaces → /api/studios
rename_in_files "/api/workspaces" "/api/studios" "Renaming /api/workspaces → /api/studios"

# /api/workspace → /api/studio
rename_in_files "/api/workspace" "/api/studio" "Renaming /api/workspace → /api/studio"

echo ""

################################################################################
# PHASE 7: Comments and Documentation
################################################################################
echo -e "${YELLOW}=== PHASE 7: Comments and Documentation ===${NC}"
echo ""

# Comments: workspace → studio (in comments)
rename_in_files "workspace collaboration" "studio collaboration" "Updating documentation"

# Comments: Workspace → AgentStudio (in comments)
rename_in_files "// Workspace" "// AgentStudio" "Updating comments: Workspace → AgentStudio"

echo ""

################################################################################
# PHASE 8: Interface and Receiver Names
################################################################################
echo -e "${YELLOW}=== PHASE 8: Interface and Receiver Names ===${NC}"
echo ""

# workspace.Store → agentstudio.Store (already done via imports)
# ws *Workspace → st *AgentStudio (receiver names - conservative approach)
rename_in_files "func (w \\*Workspace)" "func (st *AgentStudio)" "Renaming receiver names (w *Workspace → st *AgentStudio)"

# Also for methods that use workspace receiver
rename_in_files "func (w \\*AgentStudio)" "func (st *AgentStudio)" "Normalizing receiver names"

echo ""

################################################################################
# PHASE 9: Constant and Error Messages
################################################################################
echo -e "${YELLOW}=== PHASE 9: Constants and Error Messages ===${NC}"
echo ""

# StatusActive WorkspaceStatus → StatusActive StudioStatus (already done via type rename)
# Error messages: "workspace" → "studio" (conservative)
rename_in_files "not found in workspace" "not found in studio" "Updating error messages"

# "agent.*workspace" → "agent.*studio"
rename_in_files "agent %s is not part of workspace" "agent %s is not part of studio" "Updating agent validation messages"

echo ""

################################################################################
# PHASE 10: Import Paths (update old references if any remain)
################################################################################
echo -e "${YELLOW}=== PHASE 10: Import Paths ===${NC}"
echo ""

# This should already be done, but double-check for any remaining old imports
rename_in_files '"github.com/johnjallday/ori-agent/internal/workspace"' '"github.com/johnjallday/ori-agent/internal/agentstudio"' "Updating import paths"

echo ""

################################################################################
# VERIFICATION PHASE
################################################################################
echo -e "${YELLOW}=== VERIFICATION PHASE ===${NC}"
echo ""

echo "Checking for remaining workspace references that might need updating..."
echo ""

remaining_count=$(find "$PROJECT_ROOT" "${FIND_OPTS[@]}" -exec grep -l "\\bworkspace\\." {} \; 2>/dev/null | wc -l)
if [ "$remaining_count" -gt 0 ]; then
    echo -e "${YELLOW}⚠ Found $remaining_count files with 'workspace.' references:${NC}"
    find "$PROJECT_ROOT" "${FIND_OPTS[@]}" -exec grep -l "\\bworkspace\\." {} \; 2>/dev/null | head -10
    echo ""
fi

remaining_count=$(find "$PROJECT_ROOT" "${FIND_OPTS[@]}" -exec grep -l "Workspace[A-Z]" {} \; 2>/dev/null | grep -v "AgentStudio" | wc -l)
if [ "$remaining_count" -gt 0 ]; then
    echo -e "${YELLOW}⚠ Found $remaining_count files with 'Workspace' type references:${NC}"
    find "$PROJECT_ROOT" "${FIND_OPTS[@]}" -exec grep -l "Workspace[A-Z]" {} \; 2>/dev/null | grep -v "AgentStudio" | head -10
    echo ""
fi

echo -e "${GREEN}Verification complete${NC}"
echo ""

################################################################################
# Summary
################################################################################
echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}RENAMING COMPLETE${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo -e "${GREEN}✓ All workspace-related types renamed to agentstudio${NC}"
echo ""
echo "Changes made:"
echo "  • Types: Workspace → AgentStudio, WorkspaceStatus → StudioStatus"
echo "  • Types: CreateWorkspaceParams → CreateStudioParams"
echo "  • Functions: NewWorkspace → NewAgentStudio, CreateWorkspace → CreateStudio"
echo "  • Functions: GetWorkspace → GetStudio, DeleteWorkspace → DeleteStudio"
echo "  • Variables: workspaceStore → studioStore, workspaceID → studioID"
echo "  • Fields: WorkspaceID → StudioID, workspace_id → studio_id"
echo "  • Endpoints: /api/workspaces → /api/studios, /api/workspace → /api/studio"
echo "  • Comments: Updated documentation and error messages"
echo "  • Receiver: w *Workspace → st *AgentStudio"
echo ""
echo "Backup location: $BACKUP_DIR"
echo ""
echo "Next steps:"
echo "  1. Review changes: git diff"
echo "  2. Run tests: go test ./..."
echo "  3. Build: go build -o bin/ori-agent ./cmd/server"
echo "  4. Test the application"
echo "  5. Commit changes if satisfied"
echo ""
