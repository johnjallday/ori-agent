#!/bin/bash
# Kill any process running on port 8765 and orphaned plugin processes

echo "Looking for processes on port 8765..."

# Find the process ID using lsof (works on macOS)
PID=$(lsof -ti tcp:8765)

if [ -z "$PID" ]; then
    echo "No process found running on port 8765"
else
    echo "Found process(es): $PID"
    echo "Killing process(es)..."

    # Kill the process(es)
    kill -9 $PID

    if [ $? -eq 0 ]; then
        echo "✅ Successfully killed process(es) on port 8765"
    else
        echo "❌ Failed to kill process(es)"
    fi
fi

# Kill orphaned plugin processes
echo ""
echo "Looking for orphaned plugin processes..."

# Find plugin processes from common plugin directories
PLUGIN_PIDS=$(ps aux | grep -E "(uploaded_plugins|example_plugins|plugin_cache)/.*" | grep -v grep | awk '{print $2}')

if [ -z "$PLUGIN_PIDS" ]; then
    echo "No orphaned plugin processes found"
else
    PLUGIN_COUNT=$(echo "$PLUGIN_PIDS" | wc -l | tr -d ' ')
    echo "Found $PLUGIN_COUNT orphaned plugin process(es)"
    echo "Killing plugin process(es)..."

    echo "$PLUGIN_PIDS" | xargs kill 2>/dev/null

    if [ $? -eq 0 ]; then
        echo "✅ Successfully killed orphaned plugin process(es)"
    else
        echo "❌ Failed to kill some plugin process(es)"
    fi
fi

echo ""
echo "Done!"
