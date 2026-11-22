//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

static char* ori_running_bundle_path(void) {
	@autoreleasepool {
		NSURL *bundle = [[NSRunningApplication currentApplication] bundleURL];
		const char *path = [[bundle path] fileSystemRepresentation];
		return path ? strdup(path) : NULL;
	}
}

static char* ori_bundle_executable(const char *path) {
	@autoreleasepool {
		NSString *bundlePath = [[NSFileManager defaultManager] stringWithFileSystemRepresentation:path length:strlen(path)];
		NSBundle *bundle = [NSBundle bundleWithPath:bundlePath];
		if (![[bundle infoDictionary][@"CFBundlePackageType"] isEqual:@"APPL"] || ![[bundle bundleIdentifier] length]) return NULL;
		const char *executable = [[bundle executablePath] fileSystemRepresentation];
		return executable ? strdup(executable) : NULL;
	}
}
*/
import "C"

import "unsafe"

func runningNativeBundle() string {
	path := C.ori_running_bundle_path()
	defer C.free(unsafe.Pointer(path))
	return C.GoString(path)
}

func nativeBundleExecutable(path string) string {
	bundle := C.CString(path)
	defer C.free(unsafe.Pointer(bundle))
	executable := C.ori_bundle_executable(bundle)
	defer C.free(unsafe.Pointer(executable))
	return C.GoString(executable)
}
