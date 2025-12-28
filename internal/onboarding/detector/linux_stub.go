//go:build !linux

package detector

// NewLinuxDetector returns a fallback detector on non-Linux systems.
func NewLinuxDetector(cfg Config) *FallbackDetector {
	return NewFallbackDetector(cfg)
}
