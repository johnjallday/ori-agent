//go:build !darwin && !linux && !freebsd

package projectlibrary

import (
	"errors"
	"testing"
)

func TestBrowserIO_UnsupportedPlatformRefusesWithoutIO(t *testing.T) {
	source := browserSource{}
	if err := browserCheckRoot(source); !errors.Is(err, ErrBrowserUnsupported) {
		t.Fatal(err)
	}
	if err := browserCheckLocation(source, "", "", false); !errors.Is(err, ErrBrowserUnsupported) {
		t.Fatal(err)
	}
	if _, err := browserReadDirectory(t.Context(), source, ""); !errors.Is(err, ErrBrowserUnsupported) {
		t.Fatal(err)
	}
	if _, err := browserReadPreview(t.Context(), source, "", ""); !errors.Is(err, ErrBrowserUnsupported) {
		t.Fatal(err)
	}
}
