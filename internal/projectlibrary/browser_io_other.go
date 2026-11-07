//go:build !darwin && !linux && !freebsd

package projectlibrary

import "context"

func browserCheckRoot(browserSource) error                           { return ErrBrowserUnsupported }
func browserCheckLocation(browserSource, string, string, bool) error { return ErrBrowserUnsupported }
func browserReadDirectory(context.Context, browserSource, string) (BrowserListing, error) {
	return BrowserListing{}, ErrBrowserUnsupported
}
func browserReadPreview(context.Context, browserSource, string, string) (BrowserPreview, error) {
	return BrowserPreview{}, ErrBrowserUnsupported
}
