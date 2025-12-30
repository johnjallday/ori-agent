#!/bin/bash

# Build external plugins script for ori-agent
# Builds plugins that are in separate repositories/directories

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Allow overriding monorepo root (where external plugins live).
MONOREPO_ROOT="${MONOREPO_ROOT:-}"
if [ -z "$MONOREPO_ROOT" ]; then
  if [ -d "$PROJECT_ROOT/../plugins" ]; then
    MONOREPO_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"
  elif [ -d "$PROJECT_ROOT/../../plugins" ]; then
    MONOREPO_ROOT="$(cd "$PROJECT_ROOT/../.." && pwd)"
  else
    MONOREPO_ROOT="$PROJECT_ROOT"
  fi
fi
EXTERNAL_PLUGINS_ROOT="${EXTERNAL_PLUGINS_ROOT:-$MONOREPO_ROOT/plugins}"
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

  # Always build into ori-agent's uploaded_plugins to avoid relying on external repo write access.
  local output_path="$PROJECT_ROOT/uploaded_plugins/$plugin_name"
  rm -f "$output_path" 2>/dev/null || true

  # Use repo-local caches when available (helps in sandboxed environments).
  export GOMODCACHE="${GOMODCACHE:-$PROJECT_ROOT/.gomodcache}"
  export GOCACHE="${GOCACHE:-$PROJECT_ROOT/.gocache}"

  if [ -f "$plugin_path/build.sh" ]; then
    # Use the plugin's own build script
    pushd "$plugin_path" >/dev/null
    if ./build.sh; then
      echo -e "${GREEN}✓ Successfully built $plugin_name using build.sh${NC}"

      # Find and copy the built binary
      # Try common output names: plugin_name, or files matching pattern
      # Prefer the plugin's expected binary name, but fall back to any executable in the repo root.
      local built_binary=""
      if [ -f "$plugin_name" ]; then
        built_binary="$plugin_name"
      elif [ -f "${plugin_name/-/_}" ]; then
        built_binary="${plugin_name/-/_}"
      else
        # Look for any executable in the plugin root.
        built_binary="$(find . -maxdepth 1 -type f -perm -111 2>/dev/null | head -n 1 || true)"
      fi

      if [ -n "$built_binary" ] && [ -f "$built_binary" ]; then
        cp "$built_binary" "$output_path"
        echo -e "${GREEN}  → Copied $built_binary to uploaded_plugins/$plugin_name${NC}"
      else
        echo -e "${YELLOW}  ⚠ Could not find built binary to copy (plugin build succeeded)${NC}"
      fi
    else
      echo -e "${RED}✗ build.sh failed for $plugin_name; attempting fallback build${NC}"

      # Fallback: build the plugin directly into uploaded_plugins without modifying its go.mod/go.sum.
      # This is best-effort and intentionally avoids `go mod tidy`.
      if go build -mod=readonly -o "$output_path" .; then
        echo -e "${GREEN}✓ Fallback build succeeded: uploaded_plugins/$plugin_name${NC}"
        popd >/dev/null
        return 0
      fi

      popd >/dev/null
      return 1
    fi
    popd >/dev/null
  elif [ -f "$plugin_path/main.go" ]; then
    # Build RPC plugin executable
    pushd "$plugin_path" >/dev/null
    if go build -mod=readonly -o "$output_path" .; then
      echo -e "${GREEN}✓ Successfully built $plugin_name${NC}"
    else
      echo -e "${RED}✗ Failed to build $plugin_name${NC}"
      popd >/dev/null
      return 1
    fi
    popd >/dev/null
  else
    echo -e "${YELLOW}⚠ Skipping $plugin_name: no build.sh or main.go found${NC}"
    return 0
  fi
}

# List of external plugins to build
# Format: "relative_path:plugin_name"
EXTERNAL_PLUGINS=(
  "$EXTERNAL_PLUGINS_ROOT/ori-reaper:ori-reaper"
  "$EXTERNAL_PLUGINS_ROOT/ori-music-project-manager:ori-music-project-manager"
  "$EXTERNAL_PLUGINS_ROOT/ori-mac-os-tools:ori-mac-os-tools"
  "$EXTERNAL_PLUGINS_ROOT/ori-meta-threads-manager:ori-meta-threads-manager"
  "$EXTERNAL_PLUGINS_ROOT/ori-agent-doc-builder:ori-agent-doc-builder"
  "$EXTERNAL_PLUGINS_ROOT/ori-plugin-manager:ori-plugin-manager"
)

# Build each external plugin
processed_count=0
skipped_count=0
built_count=0
failed_count=0
failed_plugins=()
for plugin_spec in "${EXTERNAL_PLUGINS[@]}"; do
  IFS=':' read -r plugin_path plugin_name <<<"$plugin_spec"
  processed_count=$((processed_count + 1))
  if [ ! -d "$plugin_path" ]; then
    skipped_count=$((skipped_count + 1))
    echo -e "${YELLOW}⚠ Skipping $plugin_name: directory not found at $plugin_path${NC}"
    continue
  fi

  if build_external_plugin "$plugin_path" "$plugin_name"; then
    built_count=$((built_count + 1))
  else
    failed_count=$((failed_count + 1))
    failed_plugins+=("$plugin_name")
  fi
done

echo ""
if [ $processed_count -gt 0 ]; then
  echo -e "${BLUE}Processed: $processed_count (built: $built_count, skipped: $skipped_count, failed: $failed_count)${NC}"
  echo ""
fi

if [ $built_count -gt 0 ]; then
  echo -e "${GREEN}✓ Built $built_count external plugin(s)!${NC}"
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
