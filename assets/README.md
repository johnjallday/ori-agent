# Ori Agent Assets

This directory contains branding and visual assets for Ori Agent.

## Files

### Logo
- **`logo.svg`** - Original SVG logo (black)
  - Source vector graphic
  - Can be colored programmatically
  - Used to generate all icon variants

### App Icon
- **`AppIcon.icns`** - macOS app bundle icon
  - Multi-resolution icon set (16x16 to 1024x1024)
  - Automatically included in DMG installers
  - Generated from logo.svg

### Menubar Icons
Menubar status icons are located in `internal/menubar/icons/` and are embedded in the binary.

## Regenerating Icons

If you update the logo.svg, regenerate all icons:

```bash
# Regenerate menubar icons (22x22, colored by state)
./scripts/generate-menubar-icons.sh

# Regenerate app icon (.icns)
./scripts/generate-app-icon.sh
```

## Icon States

**Menubar Icons** (colored variants):
- **Gray** (#666666) - Server stopped
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
