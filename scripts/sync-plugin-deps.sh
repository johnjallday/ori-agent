#!/bin/bash
# Sync plugin dependencies with main module
# Ensures example_plugins/* go.mod files are in sync with the main go.mod
# Run this after updating dependencies in the main module

set -e

cd "$(dirname "$0")/.."

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║    Plugin Dependency Sync                  ║"
echo "╚════════════════════════════════════════════╝"
echo ""

PLUGINS_DIR="example_plugins"
CHANGED=0

if [ ! -d "$PLUGINS_DIR" ]; then
  echo -e "${YELLOW}No example_plugins directory found${NC}"
  exit 0
fi

# Find all plugins with go.mod files
PLUGINS=$(find "$PLUGINS_DIR" -maxdepth 2 -name "go.mod" -exec dirname {} \;)

if [ -z "$PLUGINS" ]; then
  echo -e "${YELLOW}No plugins with go.mod files found${NC}"
  exit 0
fi

echo -e "${BLUE}Found plugins:${NC}"
for plugin in $PLUGINS; do
  echo "  - $(basename "$plugin")"
done
echo ""

echo -e "${BLUE}Running go mod tidy in each plugin...${NC}"
echo ""

for plugin in $PLUGINS; do
  plugin_name=$(basename "$plugin")
  echo -n "  Syncing $plugin_name... "

  # Capture the go.mod before
  BEFORE=$(cat "$plugin/go.mod" 2>/dev/null || echo "")

  # Run go mod tidy
  if (cd "$plugin" && go mod tidy 2>&1); then
    # Check if go.mod changed
    AFTER=$(cat "$plugin/go.mod" 2>/dev/null || echo "")

    if [ "$BEFORE" != "$AFTER" ]; then
      echo -e "${YELLOW}UPDATED${NC}"
      CHANGED=1
    else
      echo -e "${GREEN}OK${NC}"
    fi
  else
    echo -e "${RED}FAILED${NC}"
    exit 1
  fi
done

echo ""

# Also try to build plugins to verify they work
echo -e "${BLUE}Verifying plugin builds...${NC}"
echo ""

for plugin in $PLUGINS; do
  plugin_name=$(basename "$plugin")
  echo -n "  Building $plugin_name... "

  if (cd "$plugin" && GOWORK=off go build -o /dev/null . 2>&1); then
    echo -e "${GREEN}OK${NC}"
  else
    echo -e "${RED}FAILED${NC}"
    echo ""
    echo -e "${RED}Build error:${NC}"
    (cd "$plugin" && GOWORK=off go build -o /dev/null . 2>&1) || true
    exit 1
  fi
done

echo ""

if [ $CHANGED -eq 1 ]; then
  echo -e "${YELLOW}⚠️  Plugin dependencies were updated${NC}"
  echo ""
  echo "Changed files:"
  git status --porcelain "$PLUGINS_DIR"/*/go.mod "$PLUGINS_DIR"/*/go.sum 2>/dev/null || true
  echo ""
  echo "Run the following to commit:"
  echo "  git add $PLUGINS_DIR/*/go.mod $PLUGINS_DIR/*/go.sum"
  echo "  git commit -m 'chore: sync plugin dependencies'"
  echo ""
else
  echo -e "${GREEN}✅ All plugin dependencies are in sync!${NC}"
fi
