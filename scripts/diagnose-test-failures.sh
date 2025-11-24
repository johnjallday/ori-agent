#!/bin/bash
# Test Failure Diagnostic and Fix Script
# Diagnoses common test failures and offers automated fixes
# Usage: ./scripts/diagnose-test-failures.sh

set -e
cd "$(dirname "$0")/.."

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║     Test Failure Diagnostic Tool          ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# Step 1: Check API keys
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 1: Checking API Configuration${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

HAS_OPENAI=false
HAS_ANTHROPIC=false
HAS_OLLAMA=false

if [ -n "$OPENAI_API_KEY" ]; then
  echo -e "${GREEN}✓ OPENAI_API_KEY is set${NC}"
  HAS_OPENAI=true
else
  echo -e "${YELLOW}⚠️  OPENAI_API_KEY is not set${NC}"
fi

if [ -n "$ANTHROPIC_API_KEY" ]; then
  echo -e "${GREEN}✓ ANTHROPIC_API_KEY is set${NC}"
  HAS_ANTHROPIC=true
else
  echo -e "${YELLOW}⚠️  ANTHROPIC_API_KEY is not set${NC}"
fi

if command -v ollama &> /dev/null; then
  echo -e "${GREEN}✓ Ollama is installed${NC}"
  HAS_OLLAMA=true
  # Check if ollama is running
  if ollama list &> /dev/null; then
    echo -e "${GREEN}✓ Ollama is running${NC}"
    echo -e "${BLUE}  Available models:${NC}"
    ollama list | head -5
  else
    echo -e "${YELLOW}⚠️  Ollama is installed but not running${NC}"
    HAS_OLLAMA=false
  fi
else
  echo -e "${YELLOW}⚠️  Ollama is not installed${NC}"
fi

echo ""

if [ "$HAS_OPENAI" = false ] && [ "$HAS_ANTHROPIC" = false ] && [ "$HAS_OLLAMA" = false ]; then
  echo -e "${RED}❌ No LLM provider configured!${NC}"
  echo ""
  echo "Please configure at least one provider:"
  echo "  • OpenAI: export OPENAI_API_KEY='sk-...'"
  echo "  • Anthropic: export ANTHROPIC_API_KEY='sk-ant-...'"
  echo "  • Ollama: Install from https://ollama.com"
  echo ""
  exit 1
fi

# Step 2: Test API connectivity
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 2: Testing API Connectivity${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

CONNECTIVITY_ISSUES=()

if [ "$HAS_OPENAI" = true ]; then
  echo "Testing OpenAI API..."
  # Test with a simple models list request
  if curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $OPENAI_API_KEY" \
    "https://api.openai.com/v1/models" | grep -q "200"; then
    echo -e "${GREEN}✓ OpenAI API is accessible${NC}"

    # Check specific model availability
    echo "Checking model availability..."
    MODELS_RESPONSE=$(curl -s -H "Authorization: Bearer $OPENAI_API_KEY" \
      "https://api.openai.com/v1/models")

    if echo "$MODELS_RESPONSE" | grep -q "gpt-4o-mini"; then
      echo -e "${GREEN}✓ gpt-4o-mini is available${NC}"
    elif echo "$MODELS_RESPONSE" | grep -q "gpt-4.1-nano"; then
      echo -e "${YELLOW}⚠️  gpt-4o-mini not found, but gpt-4.1-nano is available${NC}"
      CONNECTIVITY_ISSUES+=("gpt-4o-mini model not available")
    elif echo "$MODELS_RESPONSE" | grep -q "gpt-4o"; then
      echo -e "${YELLOW}⚠️  gpt-4o-mini not found, but gpt-4o is available${NC}"
      CONNECTIVITY_ISSUES+=("gpt-4o-mini model not available")
    else
      echo -e "${YELLOW}⚠️  No suitable GPT-4 models found${NC}"
      echo -e "${BLUE}Available GPT-4 models:${NC}"
      echo "$MODELS_RESPONSE" | grep -o '"id":"gpt-[^"]*"' | head -5
      CONNECTIVITY_ISSUES+=("Expected models not available")
    fi
  else
    echo -e "${RED}❌ Cannot connect to OpenAI API${NC}"
    CONNECTIVITY_ISSUES+=("OpenAI API connection failed")
  fi
  echo ""
fi

if [ "$HAS_ANTHROPIC" = true ]; then
  echo "Testing Anthropic API..."
  # Anthropic doesn't have a simple models endpoint, so we'll just verify the key format
  if [[ "$ANTHROPIC_API_KEY" =~ ^sk-ant- ]]; then
    echo -e "${GREEN}✓ Anthropic API key format is valid${NC}"
  else
    echo -e "${YELLOW}⚠️  Anthropic API key format may be invalid${NC}"
    CONNECTIVITY_ISSUES+=("Anthropic API key format issue")
  fi
  echo ""
fi

# Step 3: Run a quick test
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 3: Running Quick Test${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Run a single quick test to identify issues
TEST_OUTPUT=$(mktemp)
trap "rm -f $TEST_OUTPUT" EXIT

echo "Running sample test..."
if go test -v -run TestMathPluginIntegration ./tests/user/plugins -timeout 30s > "$TEST_OUTPUT" 2>&1; then
  echo -e "${GREEN}✓ Tests passed!${NC}"
  exit 0
else
  echo -e "${RED}❌ Test failed${NC}"
  echo ""
  echo "Error details:"
  grep -A 5 "Error\|FAIL\|404\|401\|403" "$TEST_OUTPUT" | head -20
fi

echo ""

# Step 4: Offer solutions
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 4: Recommended Solutions${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

if grep -q "404" "$TEST_OUTPUT"; then
  echo -e "${YELLOW}Issue: 404 Not Found (Model or endpoint doesn't exist)${NC}"
  echo ""
  echo "Possible fixes:"
  echo "  1. The model name 'gpt-4o-mini' may have changed"
  echo "  2. Update test model to 'gpt-4.1-nano'"
  echo "  3. Use Ollama instead (local, free)"
  echo ""

  read -p "Would you like to: [1] Update to gpt-4.1-nano, [2] Use Ollama, [3] Exit: " -n 1 -r
  echo ""

  case $REPLY in
    1)
      echo ""
      echo "Trying different model variants..."

      # Try models in order of preference
      MODELS_TO_TRY=("gpt-4o" "gpt-4-turbo" "gpt-3.5-turbo")
      SUCCESS=false

      for MODEL in "${MODELS_TO_TRY[@]}"; do
        echo ""
        echo "Testing with $MODEL..."

        # Test if model is accessible
        if curl -s -o /dev/null -w "%{http_code}" \
          -H "Authorization: Bearer $OPENAI_API_KEY" \
          -H "Content-Type: application/json" \
          -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"test\"}],\"max_tokens\":5}" \
          "https://api.openai.com/v1/chat/completions" | grep -q "200"; then

          echo -e "${GREEN}✓ $MODEL is accessible!${NC}"
          echo "Updating tests to use $MODEL..."

          # Update all test files
          find ./tests -name "*.go" -exec sed -i.bak "s/gpt-4o-mini/$MODEL/g;s/gpt-4\.1-nano/$MODEL/g" {} \;
          find ./internal -name "*.go" -exec sed -i.bak "s/gpt-4o-mini/$MODEL/g;s/gpt-4\.1-nano/$MODEL/g" {} \;
          find . -name "*.go.bak" -delete

          echo -e "${GREEN}✓ Updated model references to $MODEL${NC}"
          echo ""
          echo "Re-running tests..."

          if go test -v -run TestMathPluginIntegration ./tests/user/plugins -timeout 30s; then
            echo -e "${GREEN}✅ Tests passed with $MODEL!${NC}"
            SUCCESS=true
            break
          else
            echo -e "${YELLOW}Tests failed with $MODEL, trying next...${NC}"
          fi
        else
          echo -e "${YELLOW}✗ $MODEL not accessible, trying next...${NC}"
        fi
      done

      if [ "$SUCCESS" = false ]; then
        echo ""
        echo -e "${RED}❌ All models failed. Please check:${NC}"
        echo "  1. API key is valid"
        echo "  2. Account has access to GPT models"
        echo "  3. Try using Ollama instead (option 2)"
      fi
      ;;
    2)
      if [ "$HAS_OLLAMA" = true ]; then
        echo ""
        echo "Running tests with Ollama..."
        USE_OLLAMA=true OLLAMA_MODEL=granite4 go test -v ./tests/user/plugins -timeout 5m
      else
        echo -e "${YELLOW}Ollama not installed. Install from: https://ollama.com${NC}"
      fi
      ;;
    *)
      echo "Exiting..."
      exit 1
      ;;
  esac

elif grep -q "401\|403" "$TEST_OUTPUT"; then
  echo -e "${YELLOW}Issue: Authentication failed${NC}"
  echo ""
  echo "Your API key may be:"
  echo "  • Invalid or expired"
  echo "  • Missing required permissions"
  echo "  • Not properly set in environment"
  echo ""
  echo "Please check your API key and try again."

else
  echo -e "${YELLOW}Unknown test failure${NC}"
  echo ""
  echo "Full test output:"
  cat "$TEST_OUTPUT"
  echo ""
  echo "Consider:"
  echo "  1. Review the error messages above"
  echo "  2. Check API key validity"
  echo "  3. Ensure plugins are built: make plugins"
  echo "  4. Try using Ollama: USE_OLLAMA=true go test ./..."
fi

echo ""
