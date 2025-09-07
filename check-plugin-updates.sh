#!/bin/bash

set -e # Exit on any error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

# Configuration
LOCAL_REGISTRY="plugin_registry.json"

# Try different registry locations
REGISTRY_LOCATIONS=(
  "plugin_registry.json"                            # Current directory
  "../dolphin-plugin-registry/plugin_registry.json" # Relative to dolphin-agent
  "local_plugin_registry.json"                      # Local agent registry
)

# Find the registry file
REGISTRY_FOUND=""
for location in "${REGISTRY_LOCATIONS[@]}"; do
  if [[ -f "$location" ]]; then
    LOCAL_REGISTRY="$location"
    REGISTRY_FOUND="$location"
    break
  fi
done
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

echo -e "${BLUE}🔍 Checking for plugin updates...${NC}"
echo "======================================="

# Parse command line arguments
AUTO_DOWNLOAD=false
DOWNLOAD_DIR="./downloaded_updates"
UPDATE_REGISTRY=false

while [[ $# -gt 0 ]]; do
  case $1 in
  --auto-download)
    AUTO_DOWNLOAD=true
    shift
    ;;
  --download-dir)
    DOWNLOAD_DIR="$2"
    shift 2
    ;;
  --update-registry)
    UPDATE_REGISTRY=true
    shift
    ;;
  --help)
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --auto-download     Automatically download available updates"
    echo "  --download-dir DIR  Directory to download updates to (default: ./downloaded_updates)"
    echo "  --update-registry   Update plugin_registry.json with latest versions"
    echo "  --help             Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                              # Check for updates only"
    echo "  $0 --auto-download              # Check and download updates"
    echo "  $0 --update-registry            # Check and update registry versions"
    echo "  $0 --auto-download --update-registry  # Download and update registry"
    echo "  $0 --auto-download --download-dir /tmp/plugins"
    exit 0
    ;;
  *)
    echo -e "${RED}❌ Unknown option: $1${NC}"
    echo "Use --help for usage information"
    exit 1
    ;;
  esac
done

# Check if local registry exists
if [[ -z "$REGISTRY_FOUND" ]]; then
  echo -e "${RED}❌ No plugin registry found${NC}"
  echo "Searched locations:"
  for location in "${REGISTRY_LOCATIONS[@]}"; do
    echo "  • $location"
  done
  exit 1
fi

echo -e "${BLUE}📋 Using registry: $REGISTRY_FOUND${NC}"

# Check if jq is available
if ! command -v jq &>/dev/null; then
  echo -e "${RED}❌ jq is required but not installed${NC}"
  echo "Install jq: brew install jq (on macOS) or apt-get install jq (on Ubuntu)"
  exit 1
fi

# Function to extract GitHub repo info from URL
extract_github_info() {
  local github_repo="$1"
  # Extract owner/repo from various GitHub URL formats
  echo "$github_repo" | sed -E 's|.*github\.com/([^/]+/[^/]+).*|\1|' | sed 's|/$||'
}

# Function to get latest release version from GitHub API
get_latest_github_version() {
  local repo="$1"
  local api_url="https://api.github.com/repos/$repo/releases/latest"

  # Fetch release info with error handling
  local response
  if response=$(curl -s "$api_url" 2>/dev/null); then
    # Check if response contains an error
    if echo "$response" | jq -e '.message' >/dev/null 2>&1; then
      local error_msg=$(echo "$response" | jq -r '.message')
      if [[ "$error_msg" == "Not Found" ]]; then
        echo "no-releases"
      else
        echo "error: $error_msg"
      fi
    else
      # Extract version from tag_name, removing 'v' prefix if present
      echo "$response" | jq -r '.tag_name' | sed 's/^v//'
    fi
  else
    echo "error: API request failed"
  fi
}

# Function to compare semantic versions
version_gt() {
  test "$(echo "$@" | tr " " "\n" | sort -V | head -n 1)" != "$1"
}

# Function to get download URL for latest release
get_latest_download_url() {
  local repo="$1"
  local filename="$2"
  local api_url="https://api.github.com/repos/$repo/releases/latest"

  local response
  if response=$(curl -s "$api_url" 2>/dev/null); then
    if ! echo "$response" | jq -e '.message' >/dev/null 2>&1; then
      # Look for asset with matching filename
      echo "$response" | jq -r --arg filename "$filename" '.assets[] | select(.name == $filename) | .browser_download_url'
    fi
  fi
}

# Function to check if download URL is accessible
check_download_url() {
  local url="$1"
  if curl -s --head "$url" >/dev/null 2>&1; then
    return 0
  else
    return 1
  fi
}

echo -e "${YELLOW}📋 Processing plugin registry...${NC}"

# Parse the plugin registry and check each plugin
updates_available=false
registry_updated=false
total_plugins=0
checked_plugins=0
error_plugins=0
updated_plugins=0

# Create a backup of registry for update mode
REGISTRY_BACKUP=""
if [[ "$UPDATE_REGISTRY" == "true" ]]; then
  REGISTRY_BACKUP="${LOCAL_REGISTRY}.backup.$(date +%Y%m%d_%H%M%S)"
  cp "$LOCAL_REGISTRY" "$REGISTRY_BACKUP"
  echo -e "${BLUE}📋 Registry backup created: $REGISTRY_BACKUP${NC}"
fi

# Read plugins from registry
plugins=$(cat "$LOCAL_REGISTRY" | jq -c '.plugins[]')

while IFS= read -r plugin; do
  total_plugins=$((total_plugins + 1))

  # Extract plugin information
  name=$(echo "$plugin" | jq -r '.name')
  current_version=$(echo "$plugin" | jq -r '.version // "unknown"')
  github_repo=$(echo "$plugin" | jq -r '.github_repo // empty')
  download_url=$(echo "$plugin" | jq -r '.download_url // empty')

  echo ""
  echo -e "${BLUE}🔌 Plugin: $name${NC}"
  echo -e "   Current version: $current_version"

  # Skip if no GitHub repo
  if [[ -z "$github_repo" || "$github_repo" == "null" ]]; then
    echo -e "   ${YELLOW}⚠️  No GitHub repository specified - skipping${NC}"
    continue
  fi

  # Extract repo info
  repo_path=$(extract_github_info "$github_repo")
  if [[ -z "$repo_path" ]]; then
    echo -e "   ${RED}❌ Invalid GitHub repository URL${NC}"
    error_plugins=$((error_plugins + 1))
    continue
  fi

  echo -e "   Repository: $repo_path"

  # Get latest version from GitHub
  echo -e "   ${YELLOW}🔍 Checking for updates...${NC}"
  latest_version=$(get_latest_github_version "$repo_path")

  checked_plugins=$((checked_plugins + 1))

  case "$latest_version" in
  "no-releases")
    echo -e "   ${YELLOW}⚠️  No releases found${NC}"
    ;;
  "error:"*)
    echo -e "   ${RED}❌ ${latest_version}${NC}"
    error_plugins=$((error_plugins + 1))
    ;;
  *)
    echo -e "   Latest version: $latest_version"

    # Compare versions
    if [[ "$current_version" == "unknown" ]]; then
      echo -e "   ${PURPLE}ℹ️  Current version unknown - update recommended${NC}"
      updates_available=true
    elif [[ "$current_version" == "$latest_version" ]]; then
      echo -e "   ${GREEN}✅ Up to date${NC}"
    elif version_gt "$latest_version" "$current_version"; then
      echo -e "   ${GREEN}🆙 Update available: $current_version → $latest_version${NC}"
      updates_available=true

      # Update registry if requested
      if [[ "$UPDATE_REGISTRY" == "true" ]]; then
        echo -e "   ${YELLOW}📝 Updating registry version: $current_version → $latest_version${NC}"
        
        # Update the version in the JSON file
        temp_file=$(mktemp)
        cat "$LOCAL_REGISTRY" | jq --arg plugin_name "$name" --arg new_version "$latest_version" '
          .plugins |= map(
            if .name == $plugin_name then
              .version = $new_version
            else
              .
            end
          )
        ' > "$temp_file" && mv "$temp_file" "$LOCAL_REGISTRY"
        
        registry_updated=true
        updated_plugins=$((updated_plugins + 1))
        echo -e "   ${GREEN}✅ Registry updated${NC}"
      fi

      # Check if download URL works
      if [[ -n "$download_url" && "$download_url" != "null" ]]; then
        if check_download_url "$download_url"; then
          echo -e "   ${GREEN}📦 Download: $download_url${NC}"

          # Auto-download if requested
          if [[ "$AUTO_DOWNLOAD" == "true" ]]; then
            echo -e "   ${YELLOW}⬇️  Downloading update...${NC}"
            mkdir -p "$DOWNLOAD_DIR"
            filename=$(basename "$download_url")
            download_path="$DOWNLOAD_DIR/$filename"

            if curl -sL "$download_url" -o "$download_path"; then
              echo -e "   ${GREEN}✅ Downloaded: $download_path${NC}"
            else
              echo -e "   ${RED}❌ Download failed${NC}"
            fi
          fi
        else
          echo -e "   ${YELLOW}⚠️  Download URL not accessible: $download_url${NC}"

          # Try to get the correct download URL
          filename=$(basename "$download_url")
          correct_url=$(get_latest_download_url "$repo_path" "$filename")
          if [[ -n "$correct_url" ]]; then
            echo -e "   ${BLUE}💡 Correct URL: $correct_url${NC}"

            # Auto-download using correct URL if requested
            if [[ "$AUTO_DOWNLOAD" == "true" ]]; then
              echo -e "   ${YELLOW}⬇️  Downloading from correct URL...${NC}"
              mkdir -p "$DOWNLOAD_DIR"
              download_path="$DOWNLOAD_DIR/$filename"

              if curl -sL "$correct_url" -o "$download_path"; then
                echo -e "   ${GREEN}✅ Downloaded: $download_path${NC}"
              else
                echo -e "   ${RED}❌ Download failed${NC}"
              fi
            fi
          fi
        fi
      else
        echo -e "   ${YELLOW}⚠️  No download URL specified${NC}"
      fi
    else
      echo -e "   ${BLUE}ℹ️  Local version ($current_version) newer than latest release ($latest_version)${NC}"
    fi
    ;;
  esac
done <<<"$plugins"

# Summary
echo ""
echo "======================================="
echo -e "${BLUE}📊 Update Check Summary${NC}"
echo -e "   Total plugins: $total_plugins"
echo -e "   Checked: $checked_plugins"
echo -e "   Errors: $error_plugins"
if [[ "$UPDATE_REGISTRY" == "true" ]]; then
  echo -e "   Registry updates: $updated_plugins"
fi

if [[ "$updates_available" == "true" ]]; then
  echo -e "   ${GREEN}🆙 Updates available!${NC}"
  echo ""
  
  # Registry update information
  if [[ "$registry_updated" == "true" ]]; then
    echo -e "${GREEN}📝 Registry updated successfully!${NC}"
    echo -e "   • $updated_plugins plugin(s) version(s) updated in registry"
    echo -e "   • Backup saved: $REGISTRY_BACKUP"
    echo ""
  elif [[ "$UPDATE_REGISTRY" == "true" && "$updated_plugins" -eq 0 ]]; then
    echo -e "${BLUE}📝 Registry was already up to date${NC}"
    echo ""
  fi
  
  if [[ "$AUTO_DOWNLOAD" == "true" ]]; then
    echo -e "${YELLOW}💡 Downloads saved to: $DOWNLOAD_DIR${NC}"
    echo ""
    echo -e "${YELLOW}Next steps:${NC}"
    echo "   1. Review the downloaded .so files in $DOWNLOAD_DIR"
    echo "   2. Replace the old files in your plugins directory"
    echo "   3. Restart your dolphin-agent"
    echo ""
    echo -e "${BLUE}Quick install commands:${NC}"
    if [[ -d "uploaded_plugins" ]]; then
      echo "   cp $DOWNLOAD_DIR/*.so uploaded_plugins/"
    else
      echo "   cp $DOWNLOAD_DIR/*.so /path/to/your/plugins/directory/"
    fi
  else
    echo -e "${YELLOW}💡 To update plugins:${NC}"
    echo "   1. Download the new .so files from the URLs above"
    echo "   2. Replace the old files in your plugins directory"
    echo "   3. Restart your dolphin-agent"
    echo ""
    echo -e "${BLUE}Automation options:${NC}"
    echo "   ./check-plugin-updates.sh --auto-download              # Download updates"
    echo "   ./check-plugin-updates.sh --update-registry            # Update registry versions"
    echo "   ./check-plugin-updates.sh --auto-download --update-registry  # Both"
  fi
else
  echo -e "   ${GREEN}✅ All plugins are up to date${NC}"
  
  if [[ "$UPDATE_REGISTRY" == "true" && "$registry_updated" == "false" ]]; then
    echo -e "   ${BLUE}📝 Registry versions are current${NC}"
  fi
fi

echo ""
