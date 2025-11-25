#!/bin/bash
# Automatically create a self-signed certificate for code signing OriAgent
# Based on AeroSpace's approach but fully automated

set -e

CERT_NAME="OriAgent Self-Signed"
KEYCHAIN="$HOME/Library/Keychains/login.keychain-db"

echo "🔐 Creating self-signed certificate for OriAgent..."
echo "=================================================="

# Check if certificate already exists
if security find-identity -v -p codesigning 2>/dev/null | grep -q "${CERT_NAME}"; then
    echo "✅ Certificate '${CERT_NAME}' already exists!"
    security find-identity -v -p codesigning | grep "${CERT_NAME}"
    exit 0
fi

# Create a temporary file for the certificate configuration
CERT_CONFIG=$(mktemp)
cat > "$CERT_CONFIG" <<EOF
[ req ]
default_bits       = 2048
distinguished_name = req_distinguished_name
x509_extensions    = v3_ca
prompt             = no

[ req_distinguished_name ]
CN = ${CERT_NAME}

[ v3_ca ]
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
basicConstraints = critical,CA:true
keyUsage = critical,keyCertSign,cRLSign,digitalSignature
extendedKeyUsage = codeSigning
EOF

# Generate the certificate
CERT_PEM=$(mktemp)
KEY_PEM=$(mktemp)
P12_FILE=$(mktemp)

echo "📝 Generating certificate..."
openssl req -x509 -newkey rsa:2048 -keyout "$KEY_PEM" -out "$CERT_PEM" -days 3650 -nodes -config "$CERT_CONFIG"

echo "📦 Creating P12 bundle..."
openssl pkcs12 -export -out "$P12_FILE" -inkey "$KEY_PEM" -in "$CERT_PEM" -passout pass:""

echo "🔑 Importing certificate into keychain..."
security import "$P12_FILE" -k "$KEYCHAIN" -T /usr/bin/codesign -T /usr/bin/security -P ""

# Trust the certificate for code signing
CERT_SHA1=$(openssl x509 -in "$CERT_PEM" -noout -fingerprint -sha1 | cut -d'=' -f2 | tr -d ':')
security add-trusted-cert -d -r trustRoot -k "$KEYCHAIN" "$CERT_PEM" 2>/dev/null || true

# Set the certificate to "Always Trust" for code signing
# This requires the certificate to be in the keychain first
sleep 1
security set-key-partition-list -S apple-tool:,apple: -s -k "" "$KEYCHAIN" 2>/dev/null || true

# Clean up
rm -f "$CERT_CONFIG" "$CERT_PEM" "$KEY_PEM" "$P12_FILE"

echo ""
echo "✅ Certificate created successfully!"
echo ""
echo "Certificate details:"
security find-identity -v -p codesigning | grep "${CERT_NAME}" || echo "  (Certificate is installing, please wait a moment and run this script again)"
echo ""
echo "🎉 You can now sign OriAgent with:"
echo "  codesign --force --deep --sign \"${CERT_NAME}\" /Applications/OriAgent.app"
