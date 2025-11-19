#!/bin/bash

# Build external plugins script for ori-agent
# Builds plugins that are in separate repositories/directories

set -e # Exit on any error

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Building external plugins...${NC}"

# Create uploaded_plugins directory if it doesn't exist
mkdir -p uploaded_plugins

# Build external plugins
build_external_plugin() {
  local plugin_path="$1"
  local plugin_name="$2"

  if [ ! -d "$plugin_path" ]; then
    echo -e "${YELLOW}⚠ Skipping $plugin_name: directory not found at $plugin_path${NC}"
    return 0
  fi

  echo -e "${YELLOW}Building external plugin: $plugin_name${NC}"

  if [ -f "$plugin_path/build.sh" ]; then
    # Use the plugin's own build script
    cd "$plugin_path"
    if ./build.sh; then
      echo -e "${GREEN}✓ Successfully built $plugin_name using build.sh${NC}"

      # Find and copy the built binary
      # Try common output names: plugin_name, or files matching pattern
      if [ -f "$plugin_name" ]; then
        cp "$plugin_name" "$PROJECT_ROOT/uploaded_plugins/"
        echo -e "${GREEN}  → Copied $plugin_name to uploaded_plugins/${NC}"
      elif [ -f "${plugin_name/-/_}" ]; then
        # Try with underscores instead of hyphens
        cp "${plugin_name/-/_}" "$PROJECT_ROOT/uploaded_plugins/$plugin_name"
        echo -e "${GREEN}  → Copied ${plugin_name/-/_} to uploaded_plugins/$plugin_name${NC}"
      else
        # Look for any recently built executable
        local built_binary=$(find . -maxdepth 1 -type f -perm +111 -newer build.sh 2>/dev/null | head -n 1)
        if [ -n "$built_binary" ]; then
          cp "$built_binary" "$PROJECT_ROOT/uploaded_plugins/$plugin_name"
          echo -e "${GREEN}  → Copied $built_binary to uploaded_plugins/$plugin_name${NC}"
        else
          echo -e "${YELLOW}  ⚠ Could not find built binary to copy${NC}"
        fi
      fi
    else
      echo -e "${RED}✗ Failed to build $plugin_name${NC}"
      cd "$PROJECT_ROOT"
      return 1
    fi
    cd "$PROJECT_ROOT"
  elif [ -f "$plugin_path/main.go" ]; then
    # Build RPC plugin executable
    cd "$plugin_path"
    local output_name="$plugin_name"
    if go build -o "$output_name" .; then
      echo -e "${GREEN}✓ Successfully built $plugin_name${NC}"
      # Copy to uploaded_plugins
      cp "$output_name" "$PROJECT_ROOT/uploaded_plugins/"
    else
      echo -e "${RED}✗ Failed to build $plugin_name${NC}"
      cd "$PROJECT_ROOT"
      return 1
    fi
    cd "$PROJECT_ROOT"
  else
    echo -e "${YELLOW}⚠ Skipping $plugin_name: no build.sh or main.go found${NC}"
    return 0
  fi
}

# List of external plugins to build
# Format: "relative_path:plugin_name"
EXTERNAL_PLUGINS=(
  "../plugins/ori-reaper:ori-reaper"
  "../plugins/ori-music-project-manager:ori-music-project-manager"
  "../plugins/ori-mac-os-tools:ori-mac-os-tools"
  "../plugins/ori-meta-threads-manager:ori-meta-threads-manager"
  "../plugins/ori-agent-doc-builder:ori-agent-doc-builder"
)

# Build each external plugin
built_count=0
failed_count=0
failed_plugins=()
for plugin_spec in "${EXTERNAL_PLUGINS[@]}"; do
  IFS=':' read -r plugin_path plugin_name <<<"$plugin_spec"
  if build_external_plugin "$plugin_path" "$plugin_name"; then
    built_count=$((built_count + 1))
  else
    failed_count=$((failed_count + 1))
    failed_plugins+=("$plugin_name")
  fi
done

echo ""
if [ $built_count -gt 0 ]; then
  echo -e "${GREEN}✓ Built $built_count external plugin(s) successfully!${NC}"
  echo -e "${BLUE}External plugin binaries are in: uploaded_plugins/${NC}"
  ls -lh uploaded_plugins/ | grep -E "^-.*ori-" || true
fi

if [ $failed_count -gt 0 ]; then
  echo ""
  echo -e "${RED}✗ Failed to build $failed_count plugin(s):${NC}"
  for plugin in "${failed_plugins[@]}"; do
    echo -e "${RED}  - $plugin${NC}"
  done
  exit 1
fi

if [ $built_count -eq 0 ]; then
  echo -e "${YELLOW}No external plugins were built${NC}"
fi
