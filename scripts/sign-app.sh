#!/bin/bash
# Sign OriAgent with ad-hoc signature or self-signed certificate
# This is FREE and works locally without Apple Developer account

set -e

APP_PATH="${1:-/Applications/OriAgent.app}"
CERT_NAME="OriAgent Self-Signed"

if [ ! -d "$APP_PATH" ]; then
    echo "❌ App not found at: $APP_PATH"
    exit 1
fi

echo "🔐 Signing OriAgent..."
echo "===================="

# Check if we have a self-signed certificate
if security find-identity -v -p codesigning 2>/dev/null | grep -q "${CERT_NAME}"; then
    echo "✅ Using existing certificate: ${CERT_NAME}"
    codesign --force --deep --options runtime --sign "${CERT_NAME}" "$APP_PATH"
else
    echo "📝 No certificate found, using ad-hoc signature (this is FREE)"
    echo "   For better compatibility, you can create a self-signed certificate:"
    echo "   1. Open Keychain Access"
    echo "   2. Menu -> Certificate Assistant -> Create a Certificate..."
    echo "   3. Name: ${CERT_NAME}"
    echo "   4. Identity Type: Self Signed Root"
    echo "   5. Certificate Type: Code Signing"
    echo "   6. Click Create"
    echo ""
    codesign --force --deep --sign - "$APP_PATH"
fi

echo ""
echo "✅ App signed successfully!"
echo ""
echo "Signature info:"
codesign -dv "$APP_PATH" 2>&1 | head -10
echo ""
echo "🎉 You can now launch the app:"
echo "  open -a \"$APP_PATH\""
