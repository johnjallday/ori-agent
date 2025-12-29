//go:build !windows

package detector

// NewWindowsDetector returns a fallback detector on non-Windows systems.
func NewWindowsDetector(cfg Config) *FallbackDetector {
	return NewFallbackDetector(cfg)
}
