//go:build !darwin
// +build !darwin

package nativemenubar

// MenuItem represents a menu entry placeholder for non-darwin builds.
type MenuItem struct {
	ID      int
	Title   string
	Tooltip string
	Enabled bool
	Checked bool
	OnClick func()
}

// MenuBar is a no-op implementation for non-darwin platforms.
type MenuBar struct{}

// Initialize returns a stub MenuBar.
func Initialize() *MenuBar {
	return &MenuBar{}
}

// SetIcon is a no-op on non-darwin platforms.
func (mb *MenuBar) SetIcon(iconData []byte) {}

// SetTooltip is a no-op on non-darwin platforms.
func (mb *MenuBar) SetTooltip(text string) {}

// AddMenuItem records a stub menu item without native hooks.
func (mb *MenuBar) AddMenuItem(title, tooltip string, onClick func()) *MenuItem {
	return &MenuItem{
		Title:   title,
		Tooltip: tooltip,
		OnClick: onClick,
	}
}

// AddSeparator is a no-op on non-darwin platforms.
func (mb *MenuBar) AddSeparator() {}

// SetItemEnabled updates the Enabled flag locally.
func (mb *MenuBar) SetItemEnabled(item *MenuItem, enabled bool) {
	if item != nil {
		item.Enabled = enabled
	}
}

// SetItemTitle updates the Title field locally.
func (mb *MenuBar) SetItemTitle(item *MenuItem, title string) {
	if item != nil {
		item.Title = title
	}
}

// SetItemChecked updates the Checked flag locally.
func (mb *MenuBar) SetItemChecked(item *MenuItem, checked bool) {
	if item != nil {
		item.Checked = checked
	}
}

// Run calls the callbacks immediately for non-darwin platforms.
func (mb *MenuBar) Run(onReady, onExit func()) {
	if onReady != nil {
		onReady()
	}
	if onExit != nil {
		onExit()
	}
}

// Quit is a no-op on non-darwin platforms.
func (mb *MenuBar) Quit() {}
