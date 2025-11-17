# macOS Code Signing Guide for Ori Agent

## The Problem

When users download and try to open OriAgent.dmg, macOS Gatekeeper shows:

> **"OriAgent" Not Opened**
> Apple could not verify "OriAgent" is free of malware that may harm your Mac or compromise your privacy.

This happens because the app is not code-signed with an Apple Developer certificate.

## Quick Fix for Users

### Option 1: Right-click to Open
1. Right-click (or Control+click) on OriAgent.app
2. Select "Open"
3. Click "Open" in the confirmation dialog
4. The app will now run (macOS remembers this choice)

### Option 2: System Settings
1. Go to **System Settings → Privacy & Security**
2. Scroll down to find "OriAgent was blocked"
3. Click **"Open Anyway"**

### Option 3: Command Line (Fastest)
```bash
xattr -d com.apple.quarantine /Applications/OriAgent.app
```

After using any of these methods once, the app will open normally.

## Proper Solution: Code Signing

To eliminate this warning permanently, you need to sign your app with an Apple Developer certificate.

### Prerequisites

1. **Apple Developer Account** ($99/year)
   - Enroll at: https://developer.apple.com/programs/

2. **Developer ID Application Certificate**
   - Log in to https://developer.apple.com/account/
   - Go to Certificates, Identifiers & Profiles
   - Create a new "Developer ID Application" certificate
   - Download and install in Keychain Access

### Step 1: Export Certificate

On your Mac with the certificate installed:

```bash
# Open Keychain Access
# Find "Developer ID Application: Your Name (Team ID)"
# Right-click → Export "Developer ID Application..."
# Save as certificate.p12
# Set a password (you'll need this later)
```

### Step 2: Encode Certificate for GitHub Secrets

```bash
# Convert certificate to base64
base64 -i certificate.p12 | pbcopy
# The base64 string is now in your clipboard
```

### Step 3: Add GitHub Secrets

Go to your GitHub repository → Settings → Secrets and variables → Actions

Add these secrets:

| Secret Name | Value | Description |
|-------------|-------|-------------|
| `DEVELOPER_ID_APPLICATION` | Base64 certificate | Paste the base64 string from Step 2 |
| `P12_PASSWORD` | Your password | Password you set when exporting |
| `KEYCHAIN_PASSWORD` | Any password | Temporary keychain password for CI |
| `SIGNING_IDENTITY` | Your identity | e.g., "Developer ID Application: Your Name (TEAM123)" |

To find your signing identity:
```bash
security find-identity -v -p codesigning
# Look for "Developer ID Application: ..."
# Copy the entire string including the team ID
```

### Step 4: Test Locally (Optional)

Before pushing, test signing locally:

```bash
# Build the app
make menubar

# Sign the binary
codesign --force --sign "Developer ID Application: Your Name (TEAM123)" \
  --options runtime \
  --entitlements entitlements.plist \
  --timestamp \
  bin/ori-menubar

# Verify
codesign --verify --deep --strict --verbose=2 bin/ori-menubar
```

### Step 5: Create Release

Once secrets are configured, create a release:

```bash
./scripts/create-release.sh v0.1.0
```

GitHub Actions will:
1. Build the menubar app
2. **Sign it with your certificate** (if secrets are set)
3. Create the DMG
4. Upload to GitHub releases

### Step 6: Notarization (Optional but Recommended)

For the best user experience, also notarize your app:

1. **Generate App-Specific Password**
   - Go to https://appleid.apple.com/
   - Sign in → App-Specific Passwords
   - Generate a new password
   - Save it securely

2. **Add to GitHub Secrets**
   - `APPLE_ID`: Your Apple ID email
   - `APPLE_ID_PASSWORD`: App-specific password
   - `TEAM_ID`: Your team ID (e.g., "ABC123DEF4")

3. **Update Workflow** (add after code signing):
   ```bash
   # Notarize the DMG
   xcrun notarytool submit "releases/$DMG_NAME" \
     --apple-id "$APPLE_ID" \
     --password "$APPLE_ID_PASSWORD" \
     --team-id "$TEAM_ID" \
     --wait

   # Staple the notarization ticket
   xcrun stapler staple "releases/$DMG_NAME"
   ```

## How the Workflow Works

The updated `.github/workflows/release.yml` includes:

```yaml
# Code signing (if certificates are available)
if [ -n "${{ secrets.DEVELOPER_ID_APPLICATION }}" ]; then
  echo "Code signing enabled"
  # Import certificate, sign binary, sign app bundle
else
  echo "⚠️  Code signing skipped (no certificates configured)"
  echo "⚠️  Users will see Gatekeeper warning"
fi
```

**Behavior:**
- **With secrets**: App is signed, users see no warning
- **Without secrets**: App is unsigned, users see Gatekeeper warning but can still override

## Verification

After release, download the DMG and check:

```bash
# Check if signed
codesign --verify --deep --strict --verbose=2 /Applications/OriAgent.app

# View signature details
codesign -dv --verbose=4 /Applications/OriAgent.app

# Check if notarized
spctl --assess --verbose=4 --type execute /Applications/OriAgent.app
```

Expected output (signed):
```
/Applications/OriAgent.app: valid on disk
/Applications/OriAgent.app: satisfies its Designated Requirement
```

Expected output (unsigned):
```
/Applications/OriAgent.app: code object is not signed at all
```

## Cost-Free Alternative: Build Instructions

If you don't want to pay for Apple Developer Program, document clear build instructions:

**In README.md:**
```markdown
## Installation (macOS)

### Option 1: Download Release (unsigned)
1. Download OriAgent.dmg from [releases](https://github.com/...)
2. Open the DMG
3. **Right-click** on OriAgent.app → Open → Open
4. Drag to Applications folder

### Option 2: Build from Source
```bash
git clone https://github.com/johnjallday/ori-agent.git
cd ori-agent
make menubar
./bin/ori-menubar
```
No Gatekeeper warning when building locally!
```

## Summary

| Approach | Cost | User Experience | Setup Time |
|----------|------|-----------------|------------|
| **No signing** | Free | Warning, requires override | 0 min |
| **Code signing** | $99/year | No warning | 30 min |
| **Code signing + Notarization** | $99/year | Best (no warning, auto-approved) | 45 min |
| **Build from source** | Free | No warning | 5 min (per user) |

## Current Status

✅ **Workflow updated** to support code signing (optional)
⏳ **Awaiting**: GitHub secrets configuration
📝 **Next release**: Will be signed if secrets are configured

For now, users can bypass the warning using the methods above.
