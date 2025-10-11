#!/bin/bash

# Sync frontend assets from internal/web/static to frontend/dist
# This keeps the Wails frontend directory in sync with the embedded static files

echo "🔄 Syncing frontend assets..."

# Remove old frontend/dist and recreate
rm -rf frontend/dist
mkdir -p frontend/dist

# Copy all static files
cp -r internal/web/static/* frontend/dist/

echo "✅ Frontend assets synced!"
echo "📁 frontend/dist/ is now up to date with internal/web/static/"
