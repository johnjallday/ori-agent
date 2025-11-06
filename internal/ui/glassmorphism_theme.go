package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// GlassmorphismTheme implements a glass-like theme for Fyne
type GlassmorphismTheme struct{}

var _ fyne.Theme = (*GlassmorphismTheme)(nil)

// Glass color palette - semi-transparent with blur effect simulation
var (
	// Background colors with transparency
	glassBackground     = color.NRGBA{R: 20, G: 25, B: 40, A: 240}   // Dark blue-gray base
	glassCardBackground = color.NRGBA{R: 255, G: 255, B: 255, A: 25} // Semi-transparent white
	glassSurfaceHigh    = color.NRGBA{R: 255, G: 255, B: 255, A: 40} // Lighter glass
	glassSurfaceMid     = color.NRGBA{R: 255, G: 255, B: 255, A: 20} // Medium glass
	glassSurfaceLow     = color.NRGBA{R: 255, G: 255, B: 255, A: 10} // Subtle glass

	// Accent colors
	glassAccent    = color.NRGBA{R: 100, G: 200, B: 255, A: 200} // Bright blue
	glassPrimary   = color.NRGBA{R: 120, G: 180, B: 255, A: 255} // Primary blue
	glassSecondary = color.NRGBA{R: 200, G: 150, B: 255, A: 180} // Purple accent
	glassSuccess   = color.NRGBA{R: 100, G: 255, B: 150, A: 200} // Green
	glassWarning   = color.NRGBA{R: 255, G: 200, B: 100, A: 200} // Orange
	glassError     = color.NRGBA{R: 255, G: 120, B: 120, A: 200} // Red

	// Text colors
	glassTextPrimary   = color.NRGBA{R: 255, G: 255, B: 255, A: 240} // White text
	glassTextSecondary = color.NRGBA{R: 255, G: 255, B: 255, A: 180} // Dimmed white
	glassTextDisabled  = color.NRGBA{R: 255, G: 255, B: 255, A: 100} // Faded white

	// Border and outline colors
	glassBorder = color.NRGBA{R: 255, G: 255, B: 255, A: 60}  // Semi-transparent border
	glassFocus  = color.NRGBA{R: 120, G: 180, B: 255, A: 255} // Focus highlight
)

func (t *GlassmorphismTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	// Background colors
	case theme.ColorNameBackground:
		return glassBackground
	case theme.ColorNameOverlayBackground:
		return glassSurfaceMid

	// Button colors
	case theme.ColorNameButton:
		return glassSurfaceHigh
	case theme.ColorNameDisabledButton:
		return glassSurfaceLow

	// Text colors
	case theme.ColorNameForeground:
		return glassTextPrimary
	case theme.ColorNameDisabled:
		return glassTextDisabled
	case theme.ColorNamePlaceHolder:
		return glassTextSecondary

	// Primary colors
	case theme.ColorNamePrimary:
		return glassPrimary
	case theme.ColorNameFocus:
		return glassFocus
	case theme.ColorNameSelection:
		return glassAccent

	// Status colors
	case theme.ColorNameSuccess:
		return glassSuccess
	case theme.ColorNameWarning:
		return glassWarning
	case theme.ColorNameError:
		return glassError

	// Input colors
	case theme.ColorNameInputBackground:
		return glassSurfaceMid
	case theme.ColorNameInputBorder:
		return glassBorder

	// Card/Surface colors
	case theme.ColorNameHeaderBackground:
		return glassSurfaceHigh
	case theme.ColorNameMenuBackground:
		return glassSurfaceMid
	case theme.ColorNameSeparator:
		return glassBorder

	default:
		return theme.DefaultTheme().Color(name, variant)
	}
}

func (t *GlassmorphismTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *GlassmorphismTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *GlassmorphismTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 12 // Slightly more padding for glass effect
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameScrollBar:
		return 8 // Thinner scrollbars for modern look
	case "separator":
		return 1 // Thin separators
	case theme.SizeNameText:
		return 14
	case theme.SizeNameCaptionText:
		return 12
	case theme.SizeNameHeadingText:
		return 18
	case theme.SizeNameSubHeadingText:
		return 16
	default:
		return theme.DefaultTheme().Size(name)
	}
}
