//go:build !darwin

package detector

// NewDarwinDetector returns a fallback detector on non-macOS systems.
func NewDarwinDetector(cfg Config) *FallbackDetector {
	return NewFallbackDetector(cfg)
}
