package detector

import (
	"context"
)

// FallbackDetector is used when the platform is not supported.
// It returns an empty list, allowing the user to use self-description instead.
type FallbackDetector struct {
	config Config
}

// NewFallbackDetector creates a new fallback detector.
func NewFallbackDetector(cfg Config) *FallbackDetector {
	return &FallbackDetector{config: cfg}
}

// Platform returns "unknown".
func (d *FallbackDetector) Platform() string {
	return "unknown"
}

// DetectApps returns an empty list on unsupported platforms.
func (d *FallbackDetector) DetectApps(ctx context.Context) ([]DetectedApp, error) {
	return []DetectedApp{}, nil
}
