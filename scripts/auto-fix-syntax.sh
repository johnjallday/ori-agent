#!/bin/bash

# Auto-fix Go syntax and code quality issues using Claude
# This script detects syntax errors, unused imports, and other go vet issues
# and uses the claude CLI to fix them automatically

set -e

echo "🔧 Auto-fixing Go code issues with Claude..."
echo ""

MAX_ITERATIONS=5
iteration=0

while [ $iteration -lt $MAX_ITERATIONS ]; do
    iteration=$((iteration + 1))
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Iteration $iteration/$MAX_ITERATIONS"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""

    # Try to format and check with go vet
    fmt_errors=$(go fmt ./... 2>&1 || true)
    vet_errors=$(go vet ./... 2>&1 || true)
    error_output="${fmt_errors}${vet_errors}"

    if [ -z "$error_output" ]; then
        echo "✅ No issues found!"
        echo ""
        echo "Running final checks..."
        go fmt ./...
        go vet ./...
        echo ""
        echo "🎉 All issues have been fixed!"
        exit 0
    fi

    echo "Found issues:"
    echo "$error_output"
    echo ""

    # Extract unique files with errors (handle both go fmt and go vet output)
    # Pattern matches: internal/http/errors.go:6:2: or vet: internal/http/errors.go:6:2:
    files=$(echo "$error_output" | grep -oE '([a-zA-Z0-9_/-]+\.go):[0-9]+:[0-9]+:' | cut -d: -f1 | sort -u)

    # Also check for package errors (go vet format: "# package/path")
    if [ -z "$files" ]; then
        # Try to extract from package errors
        packages=$(echo "$error_output" | grep '^# ' | sed 's/^# //' || true)
        if [ -n "$packages" ]; then
            for pkg in $packages; do
                # Find .go files in the package directory
                pkg_files=$(find "$pkg" -maxdepth 1 -name "*.go" 2>/dev/null | head -5 || true)
                files="$files $pkg_files"
            done
        fi
    fi

    files=$(echo "$files" | tr ' ' '\n' | sort -u | tr '\n' ' ')

    if [ -z "$files" ]; then
        echo "❌ Could not parse error output. Manual intervention required."
        exit 1
    fi

    echo "Files with errors:"
    echo "$files"
    echo ""

    # Fix each file using Claude
    for file in $files; do
        echo "🤖 Fixing $file with Claude..."

        # Get errors for this specific file (handle both go fmt and go vet format)
        file_errors=$(echo "$error_output" | grep "$file" || true)

        # Create prompt for Claude
        prompt="Fix the issues in $file. The Go tools report these errors:

$file_errors

Common issues to fix:
- Unused imports (remove them)
- Syntax errors (fix malformed code)
- Missing/incorrect function arguments
- Type mismatches

Please read the file, identify and fix the issues. Preserve the intended functionality - only fix errors, don't refactor or change logic."

        # Use claude to fix the file (claude will read the file itself)
        if claude -p "$prompt" --output-format json --permission-mode acceptEdits > /tmp/claude-fix-$iteration.log 2>&1; then
            echo "✅ Claude processed $file"
        else
            echo "⚠️  Claude encountered an issue with $file"
            echo "Check /tmp/claude-fix-$iteration.log for details"
        fi
        echo ""
    done

    echo "Retrying go fmt to check if errors are resolved..."
    echo ""
done

echo "❌ Max iterations ($MAX_ITERATIONS) reached. Some errors may remain."
echo "Please review the remaining issues manually."
exit 1
