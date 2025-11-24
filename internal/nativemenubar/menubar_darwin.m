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
    dispatch_async(dispatch_get_main_queue(), ^{
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
        }
    });

    return 1;
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
