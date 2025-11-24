package nativemenubar

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// Forward declaration for Go callback
extern void goClickCallback(int itemId);

// Global variables to hold menu items and status item
static NSStatusItem *statusItem = nil;
static NSMenu *menu = nil;
static NSMutableDictionary *menuItems = nil;

// Initialize the status bar item
void initStatusBar() {
    if (statusItem != nil) return;

    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSStatusBar *statusBar = [NSStatusBar systemStatusBar];
            statusItem = [statusBar statusItemWithLength:NSSquareStatusItemLength];
            [statusItem retain];

            menu = [[NSMenu alloc] init];
            [menu setAutoenablesItems:NO];
            statusItem.menu = menu;

            menuItems = [[NSMutableDictionary alloc] init];
        }
    });
}

// Set the status bar icon from PNG data
void setIcon(const void *data, int length) {
    if (statusItem == nil) return;

    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSData *imageData = [NSData dataWithBytes:data length:length];
            NSImage *image = [[NSImage alloc] initWithData:imageData];

            if (image != nil) {
                [image setSize:NSMakeSize(18, 18)];
                [image setTemplate:YES];
                statusItem.button.image = image;
            }
        }
    });
}

// Set the tooltip
void setTooltip(const char *tooltip) {
    if (statusItem == nil) return;

    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSString *str = [NSString stringWithUTF8String:tooltip];
            statusItem.button.toolTip = str;
        }
    });
}

// Menu item click handler
@interface MenuItemTarget : NSObject
@property (nonatomic) int itemId;
@end

@implementation MenuItemTarget
- (void)handleClick:(id)sender {
    // Call the Go callback directly
    goClickCallback(self.itemId);
}
@end

// Add a menu item
int addMenuItem(const char *title, const char *tooltip, int itemId) {
    __block int result = 0;

    dispatch_sync(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSString *titleStr = [NSString stringWithUTF8String:title];
            NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:titleStr
                                                          action:@selector(handleClick:)
                                                   keyEquivalent:@""];

            if (tooltip != NULL && strlen(tooltip) > 0) {
                item.toolTip = [NSString stringWithUTF8String:tooltip];
            }

            MenuItemTarget *target = [[MenuItemTarget alloc] init];
            target.itemId = itemId;
            item.target = target;

            [menu addItem:item];
            [menuItems setObject:item forKey:@(itemId)];

            result = 1;
        }
    });

    return result;
}

// Add separator
void addSeparator() {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            [menu addItem:[NSMenuItem separatorItem]];
        }
    });
}

// Enable/disable menu item
void setMenuItemEnabled(int itemId, int enabled) {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSMenuItem *item = [menuItems objectForKey:@(itemId)];
            if (item != nil) {
                item.enabled = enabled ? YES : NO;
            }
        }
    });
}

// Update menu item title
void setMenuItemTitle(int itemId, const char *title) {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSMenuItem *item = [menuItems objectForKey:@(itemId)];
            if (item != nil) {
                item.title = [NSString stringWithUTF8String:title];
            }
        }
    });
}

// Check/uncheck menu item
void setMenuItemChecked(int itemId, int checked) {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSMenuItem *item = [menuItems objectForKey:@(itemId)];
            if (item != nil) {
                item.state = checked ? NSControlStateValueOn : NSControlStateValueOff;
            }
        }
    });
}

// Run the app loop
void run() {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp run];
    }
}

// Quit the app
void quit() {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp terminate:nil];
    });
}

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

	// Call onReady after a short delay to ensure menu is set up
	if onReady != nil {
		go func() {
			// Give time for menu to initialize
			onReady()
		}()
	}

	// Run the NSApp main loop (this blocks)
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

func boolToInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
