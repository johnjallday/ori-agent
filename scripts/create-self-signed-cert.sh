#!/bin/bash
# Create a self-signed certificate for code signing OriAgent
# Based on AeroSpace's approach

set -e

CERT_NAME="OriAgent Self-Signed"

echo "🔐 Creating self-signed certificate for OriAgent..."
echo "=================================================="
echo ""
echo "This will open Keychain Access. Please follow these steps:"
echo "1. Enter Certificate Name: ${CERT_NAME}"
echo "2. Identity Type: Self Signed Root"
echo "3. Certificate Type: Code Signing"
echo "4. Check 'Let me override defaults'"
echo "5. Click Continue through all steps, accepting defaults"
echo "6. When asked 'Specify a Location For The Certificate', select 'login' keychain"
echo ""
echo "Press Enter when ready to open Keychain Access..."
read

# Open Keychain Access to Certificate Assistant
open "/System/Applications/Utilities/Keychain Access.app"

echo ""
echo "⏳ Waiting for you to create the certificate..."
echo "   Once created, the certificate should appear in your login keychain"
echo ""
echo "Press Enter after you've created the certificate..."
read

# Check if certificate was created
if security find-identity -v -p codesigning | grep -q "${CERT_NAME}"; then
    echo "✅ Certificate found!"
    security find-identity -v -p codesigning | grep "${CERT_NAME}"
    echo ""
    echo "🎉 Certificate created successfully!"
    echo ""
    echo "You can now sign OriAgent with:"
    echo "  codesign --force --deep --sign \"${CERT_NAME}\" /Applications/OriAgent.app"
else
    echo "❌ Certificate not found. Please make sure:"
    echo "   1. You created a certificate named: ${CERT_NAME}"
    echo "   2. Certificate Type: Code Signing"
    echo "   3. Stored in 'login' keychain"
    exit 1
fi
