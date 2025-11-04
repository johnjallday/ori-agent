package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// GlassCard creates a card with glassmorphism effect
func NewGlassCard(title, subtitle string, content fyne.CanvasObject) *fyne.Container {
	// Background with glass effect
	background := canvas.NewRectangle(color.NRGBA{R: 255, G: 255, B: 255, A: 25})
	background.StrokeColor = color.NRGBA{R: 255, G: 255, B: 255, A: 60}
	background.StrokeWidth = 1

	// Title and subtitle
	var titleContent fyne.CanvasObject
	if title != "" {
		titleLabel := widget.NewLabel(title)
		titleLabel.TextStyle = fyne.TextStyle{Bold: true}

		if subtitle != "" {
			subtitleLabel := widget.NewLabel(subtitle)
			subtitleLabel.TextStyle = fyne.TextStyle{Italic: true}
			titleContent = container.NewVBox(titleLabel, subtitleLabel)
		} else {
			titleContent = titleLabel
		}
	}

	// Create content container
	var cardContent fyne.CanvasObject
	if titleContent != nil {
		cardContent = container.NewBorder(titleContent, nil, nil, nil, content)
	} else {
		cardContent = content
	}

	// Combine background and content
	card := container.NewStack(background, container.NewPadded(cardContent))
	return card
}

// GlassButton creates a button with glassmorphism effect
func NewGlassButton(text string, tapped func()) *widget.Button {
	btn := widget.NewButton(text, tapped)
	return btn
}

// GlassSeparator creates a subtle glass separator
func NewGlassSeparator() *canvas.Rectangle {
	sep := canvas.NewRectangle(color.NRGBA{R: 255, G: 255, B: 255, A: 30})
	sep.Resize(fyne.NewSize(0, 1))
	return sep
}

// GlassEntry creates an entry with glass styling
func NewGlassEntry() *widget.Entry {
	entry := widget.NewEntry()
	return entry
}

// GlassLabel creates a label with glass text styling
func NewGlassLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	return label
}

// GlassContainer creates a container with glass background
func NewGlassContainer(objects ...fyne.CanvasObject) *fyne.Container {
	// Background
	background := canvas.NewRectangle(color.NRGBA{R: 255, G: 255, B: 255, A: 15})
	background.StrokeColor = color.NRGBA{R: 255, G: 255, B: 255, A: 40}
	background.StrokeWidth = 1

	// Content
	content := container.NewVBox(objects...)

	// Combine
	return container.NewStack(background, container.NewPadded(content))
}

// GlassSidebar creates a sidebar with enhanced glass effect
func NewGlassSidebar(content fyne.CanvasObject) *fyne.Container {
	// Enhanced background for sidebar
	background := canvas.NewRectangle(color.NRGBA{R: 255, G: 255, B: 255, A: 35})
	background.StrokeColor = color.NRGBA{R: 255, G: 255, B: 255, A: 80}
	background.StrokeWidth = 1

	// Add gradient-like effect with multiple layers
	gradient1 := canvas.NewRectangle(color.NRGBA{R: 120, G: 180, B: 255, A: 10})
	gradient2 := canvas.NewRectangle(color.NRGBA{R: 200, G: 150, B: 255, A: 5})

	return container.NewStack(background, gradient1, gradient2, container.NewPadded(content))
}

// GlassChatArea creates a chat area with glass styling
func NewGlassChatArea(content fyne.CanvasObject) *fyne.Container {
	// Main background
	background := canvas.NewRectangle(color.NRGBA{R: 255, G: 255, B: 255, A: 20})
	background.StrokeColor = color.NRGBA{R: 255, G: 255, B: 255, A: 50}
	background.StrokeWidth = 1

	// Subtle accent
	accent := canvas.NewRectangle(color.NRGBA{R: 100, G: 200, B: 255, A: 8})

	return container.NewStack(background, accent, container.NewPadded(content))
}

// GlassMessageCard creates a message card with glass effect
func NewGlassMessageCard(title, text string, isUser bool) *fyne.Container {
	var bgColor color.NRGBA
	var accentColor color.NRGBA

	if isUser {
		// User message - blue tint
		bgColor = color.NRGBA{R: 100, G: 150, B: 255, A: 40}
		accentColor = color.NRGBA{R: 100, G: 150, B: 255, A: 20}
	} else {
		// Assistant message - purple/white tint
		bgColor = color.NRGBA{R: 255, G: 255, B: 255, A: 35}
		accentColor = color.NRGBA{R: 200, G: 150, B: 255, A: 15}
	}

	// Background
	background := canvas.NewRectangle(bgColor)
	background.StrokeColor = color.NRGBA{R: 255, G: 255, B: 255, A: 60}
	background.StrokeWidth = 1

	// Accent layer
	accent := canvas.NewRectangle(accentColor)

	// Content
	titleLabel := widget.NewLabel(title)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	contentLabel := widget.NewRichTextFromMarkdown(text)

	content := container.NewVBox(titleLabel, contentLabel)

	return container.NewStack(background, accent, container.NewPadded(content))
}