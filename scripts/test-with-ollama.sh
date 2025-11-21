#!/bin/bash
# Test runner for using Ollama (local LLM) instead of OpenAI/Claude

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Ori Agent - Ollama Test Runner ===${NC}"
echo ""

# Default values
MODEL="${OLLAMA_MODEL:-granite4}"
OLLAMA_HOST="${OLLAMA_HOST:-http://localhost:11434}"
TEST_VERBOSE="${TEST_VERBOSE:-false}"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --model)
            MODEL="$2"
            shift 2
            ;;
        --host)
            OLLAMA_HOST="$2"
            shift 2
            ;;
        --verbose)
            TEST_VERBOSE="true"
            shift
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            echo "Usage: $0 [--model llama3.1] [--host http://localhost:11434] [--verbose]"
            exit 1
            ;;
    esac
done

# Check if Ollama is running
echo -e "${BLUE}Checking Ollama connection...${NC}"
if ! curl -s "$OLLAMA_HOST" > /dev/null 2>&1; then
    echo -e "${RED}✗ Cannot connect to Ollama at $OLLAMA_HOST${NC}"
    echo ""
    echo "Make sure Ollama is running:"
    echo "  1. Install Ollama: https://ollama.ai"
    echo "  2. Start Ollama: ollama serve"
    echo "  3. Pull model: ollama pull $MODEL"
    echo ""
    exit 1
fi
echo -e "${GREEN}✓ Connected to Ollama at $OLLAMA_HOST${NC}"

# Check if model is available
echo -e "${BLUE}Checking if model '$MODEL' is available...${NC}"
MODELS_JSON=$(curl -s "$OLLAMA_HOST/api/tags")
if echo "$MODELS_JSON" | grep -q "\"name\":\"$MODEL"; then
    echo -e "${GREEN}✓ Model '$MODEL' is available${NC}"
else
    echo -e "${YELLOW}⚠ Model '$MODEL' not found locally${NC}"
    echo ""
    echo "Pulling model '$MODEL'..."
    ollama pull "$MODEL" || {
        echo -e "${RED}✗ Failed to pull model${NC}"
        exit 1
    }
    echo -e "${GREEN}✓ Model pulled successfully${NC}"
fi

echo ""
echo -e "${BLUE}Configuration:${NC}"
echo "  Provider: Ollama"
echo "  Model: $MODEL"
echo "  Host: $OLLAMA_HOST"
echo "  Verbose: $TEST_VERBOSE"
echo ""

# Warn about tool calling limitations for poor models
if [[ "$MODEL" == "llama3.1" ]] || [[ "$MODEL" == "llama3.1:8b" ]] || [[ "$MODEL" == "llama3.2" ]]; then
    echo -e "${YELLOW}⚠️  WARNING: $MODEL has poor tool calling support${NC}"
    echo -e "${YELLOW}   Many plugin tests may fail (tools not called)${NC}"
    echo ""
    echo "For better tool calling, use:"
    echo "  • granite4      - Good tool calling (2.1GB) [Recommended]"
    echo "  • mistral       - Excellent tool calling (4GB)"
    echo "  • phi4-mini     - Fast and good (2.5GB)"
    echo "  • nemotron-mini - Optimized for functions (2.7GB)"
    echo ""
    echo "Or press Ctrl+C to cancel and switch models"
    echo ""
    sleep 3
elif [[ "$MODEL" == "granite"* ]]; then
    echo -e "${GREEN}✓ Using Granite - good tool calling support${NC}"
    echo ""
fi

# Build server and plugins
echo -e "${BLUE}Building server and plugins...${NC}"
make build plugins || {
    echo -e "${RED}✗ Build failed${NC}"
    exit 1
}
echo -e "${GREEN}✓ Build complete${NC}"
echo ""

# Run tests with Ollama configuration
echo -e "${BLUE}Running tests with Ollama...${NC}"
echo ""

export USE_OLLAMA=true
export OLLAMA_HOST="$OLLAMA_HOST"
export OLLAMA_MODEL="$MODEL"
export TEST_VERBOSE="$TEST_VERBOSE"

# Run the tests
# Use -p 1 to run test packages sequentially (avoids port conflicts)
if [ "$TEST_VERBOSE" = "true" ]; then
    go test -p 1 -v -timeout 10m ./tests/user/... \
        -args -model="$MODEL"
else
    go test -p 1 -timeout 10m ./tests/user/... \
        -args -model="$MODEL"
fi

TEST_RESULT=$?

echo ""
if [ $TEST_RESULT -eq 0 ]; then
    echo -e "${GREEN}✓ All tests passed with Ollama!${NC}"
else
    echo -e "${RED}✗ Tests failed${NC}"
    exit 1
fi
