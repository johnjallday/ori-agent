#!/bin/bash

# Script to run both ori-agent server and Wails desktop app together
# This provides the complete desktop experience with both backend and frontend

set -e # Exit on any error

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

echo -e "${BLUE}🐬 Dolphin Desktop Launcher${NC}"
echo -e "${BLUE}=================================${NC}"
echo ""

# Function to cleanup background processes on exit
cleanup() {
    echo ""
    echo -e "${YELLOW}🧹 Cleaning up background processes...${NC}"

    # Kill ori-agent server
    if [[ -n $SERVER_PID ]]; then
        echo -e "${YELLOW}   Stopping ori-agent server (PID: $SERVER_PID)${NC}"
        kill $SERVER_PID 2>/dev/null || true
    fi

    # Kill wails dev process
    if [[ -n $WAILS_PID ]]; then
        echo -e "${YELLOW}   Stopping Wails desktop app (PID: $WAILS_PID)${NC}"
        kill $WAILS_PID 2>/dev/null || true
    fi

    # Kill any remaining ori-agent processes
    pkill -f "ori-agent" 2>/dev/null || true

    # Kill any remaining wails processes
    pkill -f "wails dev" 2>/dev/null || true

    echo -e "${GREEN}✅ Cleanup complete${NC}"
    exit 0
}

# Trap cleanup on script exit, interrupt, or termination
trap cleanup EXIT INT TERM

# Check if API key is set
echo -e "${BLUE}🔑 Checking API key configuration...${NC}"
if [[ ! -f "settings.json" ]] || ! grep -q "api_key" settings.json 2>/dev/null; then
    if [[ -z "$OPENAI_API_KEY" ]]; then
        echo -e "${RED}❌ Error: No API key found${NC}"
        echo -e "${YELLOW}   Please either:${NC}"
        echo -e "${YELLOW}   1. Set OPENAI_API_KEY environment variable${NC}"
        echo -e "${YELLOW}   2. Add api_key to settings.json${NC}"
        echo ""
        exit 1
    else
        echo -e "${GREEN}✅ Using OPENAI_API_KEY environment variable${NC}"
    fi
else
    echo -e "${GREEN}✅ Using API key from settings.json${NC}"
fi

# Check if ori-agent binary exists
echo -e "${BLUE}🔍 Checking ori-agent binary...${NC}"
if [[ ! -f "./bin/ori-agent" ]] && [[ ! -f "./ori-agent" ]]; then
    echo -e "${RED}❌ Error: ori-agent binary not found${NC}"
    echo -e "${YELLOW}   Building ori-agent...${NC}"

    # Try to build the server
    if ! go build -o bin/ori-agent ./cmd/server; then
        echo -e "${RED}❌ Failed to build ori-agent${NC}"
        exit 1
    fi
    echo -e "${GREEN}✅ Built ori-agent successfully${NC}"
else
    echo -e "${GREEN}✅ ori-agent binary found${NC}"
fi

# Check if wails is installed
echo -e "${BLUE}🔍 Checking Wails installation...${NC}"
if ! command -v wails &> /dev/null; then
    echo -e "${RED}❌ Error: Wails is not installed${NC}"
    echo -e "${YELLOW}   Please install Wails with: go install github.com/wailsapp/wails/v2/cmd/wails@latest${NC}"
    exit 1
else
    echo -e "${GREEN}✅ Wails is installed${NC}"
fi

echo ""
echo -e "${PURPLE}🚀 Starting Dolphin Desktop Experience...${NC}"
echo ""

# Start ori-agent server in background
echo -e "${BLUE}1. Starting ori-agent server...${NC}"
if [[ -f "./bin/ori-agent" ]]; then
    ./bin/ori-agent &
else
    ./ori-agent &
fi
SERVER_PID=$!

echo -e "${GREEN}   ✅ Server started (PID: $SERVER_PID)${NC}"
echo -e "${YELLOW}   📡 Server will be available at: http://localhost:8080${NC}"

# Wait a moment for server to start
echo -e "${BLUE}   ⏳ Waiting for server to initialize...${NC}"
sleep 3

# Check if server is responding
echo -e "${BLUE}   🔍 Checking server health...${NC}"
for i in {1..10}; do
    if curl -s http://localhost:8080/ > /dev/null 2>&1; then
        echo -e "${GREEN}   ✅ Server is responding${NC}"
        break
    fi
    if [[ $i -eq 10 ]]; then
        echo -e "${RED}   ❌ Server failed to start properly${NC}"
        exit 1
    fi
    sleep 1
done

echo ""

# Start Wails desktop app in background
echo -e "${BLUE}2. Starting Wails desktop app...${NC}"
cd cmd/desktop-wails
wails dev &
WAILS_PID=$!
cd "$PROJECT_ROOT"

echo -e "${GREEN}   ✅ Desktop app started (PID: $WAILS_PID)${NC}"
echo -e "${YELLOW}   🖥️  Desktop app will launch automatically${NC}"

echo ""
echo -e "${PURPLE}🎉 Dolphin Desktop is now running!${NC}"
echo ""
echo -e "${GREEN}Services running:${NC}"
echo -e "${GREEN}  🌐 Server:     http://localhost:8080 (PID: $SERVER_PID)${NC}"
echo -e "${GREEN}  🖥️  Desktop:    Wails app (PID: $WAILS_PID)${NC}"
echo ""
echo -e "${YELLOW}📝 Logs:${NC}"
echo -e "${YELLOW}  - Server logs will appear below${NC}"
echo -e "${YELLOW}  - Desktop app logs in separate window${NC}"
echo ""
echo -e "${BLUE}💡 Tips:${NC}"
echo -e "${BLUE}  - Use the desktop app for the best glassmorphism experience${NC}"
echo -e "${BLUE}  - Access web interface at http://localhost:8080 if needed${NC}"
echo -e "${BLUE}  - Press Ctrl+C to stop both services${NC}"
echo ""
echo -e "${PURPLE}=================================${NC}"
echo ""

# Wait for both processes and show their output
wait