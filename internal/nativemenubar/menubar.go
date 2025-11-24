package nativemenubar

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>

// Forward declarations
void initStatusBar();
void setIcon(const void *data, int length);
void setTooltip(const char *tooltip);
int addMenuItem(const char *title, const char *tooltip, int itemId);
void addSeparator();
void setMenuItemEnabled(int itemId, int enabled);
void setMenuItemTitle(int itemId, const char *title);
void setMenuItemChecked(int itemId, int checked);
void run();
void quit();
*/
import "C"
import (
	"sync"
	"unsafe"
)

// MenuItem represents a menu item with its properties
type MenuItem struct {
	ID      int
	Title   string
	Tooltip string
	Enabled bool
	Checked bool
	OnClick func()
}

// MenuBar manages the native macOS menu bar
type MenuBar struct {
	mu          sync.Mutex
	items       map[int]*MenuItem
	nextID      int
	clickChan   chan int
	initialized bool
	onReady     func()
	onExit      func()
}

var (
	globalMenuBar *MenuBar
	globalMu      sync.Mutex
)

// Initialize creates and initializes the menu bar
func Initialize() *MenuBar {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalMenuBar != nil {
		return globalMenuBar
	}

	mb := &MenuBar{
		items:     make(map[int]*MenuItem),
		nextID:    1,
		clickChan: make(chan int, 100),
	}

	globalMenuBar = mb
	C.initStatusBar()

	return mb
}

// SetIcon sets the menu bar icon from PNG data
func (mb *MenuBar) SetIcon(iconData []byte) {
	if len(iconData) == 0 {
		return
	}
	C.setIcon(unsafe.Pointer(&iconData[0]), C.int(len(iconData)))
}

// SetTooltip sets the tooltip text
func (mb *MenuBar) SetTooltip(text string) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	C.setTooltip(cText)
}

// AddMenuItem adds a new menu item
func (mb *MenuBar) AddMenuItem(title, tooltip string, onClick func()) *MenuItem {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	item := &MenuItem{
		ID:      mb.nextID,
		Title:   title,
		Tooltip: tooltip,
		Enabled: true,
		OnClick: onClick,
	}

	mb.items[item.ID] = item
	mb.nextID++

	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))

	var cTooltip *C.char
	if tooltip != "" {
		cTooltip = C.CString(tooltip)
		defer C.free(unsafe.Pointer(cTooltip))
	}

	C.addMenuItem(cTitle, cTooltip, C.int(item.ID))

	return item
}

// AddSeparator adds a separator line
func (mb *MenuBar) AddSeparator() {
	C.addSeparator()
}

// SetItemEnabled enables or disables a menu item
func (mb *MenuBar) SetItemEnabled(item *MenuItem, enabled bool) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	item.Enabled = enabled
	C.setMenuItemEnabled(C.int(item.ID), boolToInt(enabled))
}

// SetItemTitle updates a menu item's title
func (mb *MenuBar) SetItemTitle(item *MenuItem, title string) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	item.Title = title
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	C.setMenuItemTitle(C.int(item.ID), cTitle)
}

// SetItemChecked sets the checked state of a menu item
func (mb *MenuBar) SetItemChecked(item *MenuItem, checked bool) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	item.Checked = checked
	C.setMenuItemChecked(C.int(item.ID), boolToInt(checked))
}

// Run starts the menu bar and processes events
func (mb *MenuBar) Run(onReady, onExit func()) {
	mb.onReady = onReady
	mb.onExit = onExit

	// Start click handler in goroutine
	go mb.handleClicks()

	// Run the NSApp main loop (this blocks)
	// onReady will be called via goReadyCallback when app finishes launching
	C.run()

	// Cleanup
	if onExit != nil {
		onExit()
	}
}

// Quit terminates the menu bar app
func (mb *MenuBar) Quit() {
	C.quit()
}

// handleClicks processes menu item clicks
func (mb *MenuBar) handleClicks() {
	for itemID := range mb.clickChan {
		mb.mu.Lock()
		item := mb.items[itemID]
		mb.mu.Unlock()

		if item != nil && item.OnClick != nil {
			go item.OnClick()
		}
	}
}

//export goClickCallback
func goClickCallback(itemID C.int) {
	if globalMenuBar != nil {
		globalMenuBar.clickChan <- int(itemID)
	}
}

//export goReadyCallback
func goReadyCallback() {
	if globalMenuBar != nil && globalMenuBar.onReady != nil {
		go globalMenuBar.onReady()
	}
}

func boolToInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
