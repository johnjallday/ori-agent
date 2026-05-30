# Ori Agent Assets

This directory contains branding and visual assets for Ori Agent.

## Files

### Logo
- **`logo.svg`** - Source app icon SVG
  - Charred `#1A1614` rounded plate with Emerald `#1F6B4E` logo mark
  - Used to generate the macOS app icon

### Favicon
- **`favicon.svg`** - Browser favicon
  - Uses the same Charred and Emerald treatment as the app icon
- **`logo-readme.svg`** - README header logo
  - Uses the same Charred and Emerald treatment as the app icon

### App Icon
- **`AppIcon.icns`** - macOS app bundle icon
  - Multi-resolution icon set (16x16 to 1024x1024)
  - Automatically included in DMG installers
  - Generated from logo.svg

### Menubar Icons
Menubar status icons are located in `internal/menubar/icons/` and are embedded in the binary.

## Regenerating Icons

If you update `logo.svg`, regenerate the app icon. If you update `logo-menubar.svg` or the menu bar state colors, regenerate the menu bar icons.

```bash
# Regenerate app icon (.icns)
./scripts/generate-app-icon.sh

# Regenerate menubar icons (22x22, colored by state)
./scripts/generate-menubar-icons.sh
```

## Icon States

**Menubar Icons** (colored variants):
- **Gray** (#AAAAAA) - Server stopped
- **Orange** (#FFA500) - Server starting
- **Green** (#00FF00) - Server running
- **Gold** (#FFD700) - Server stopping
- **Red** (#FF0000) - Error state

## Requirements

Icon generation requires:
- **librsvg** (for SVG to PNG conversion)
  ```bash
  brew install librsvg
  ```
- **iconutil** (built-in on macOS, for .icns creation)
