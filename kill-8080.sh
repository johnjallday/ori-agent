#!/bin/bash
# Kill any process running on port 8080

echo "Looking for processes on port 8080..."

# Find the process ID using lsof (works on macOS)
PID=$(lsof -ti tcp:8080)

if [ -z "$PID" ]; then
    echo "No process found running on port 8080"
    exit 0
fi

echo "Found process(es): $PID"
echo "Killing process(es)..."

# Kill the process(es)
kill -9 $PID

if [ $? -eq 0 ]; then
    echo "✅ Successfully killed process(es) on port 8080"
else
    echo "❌ Failed to kill process(es)"
    exit 1
fi
