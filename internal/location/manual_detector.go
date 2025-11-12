package location

import (
	"context"
	"errors"
	"sync"
)

// ManualDetector implements Detector for manual location override
type ManualDetector struct {
	mu       sync.RWMutex
	location string
}

// NewManualDetector creates a new manual detector
func NewManualDetector() *ManualDetector {
	return &ManualDetector{
		location: "",
	}
}

// Name returns the detector name
func (d *ManualDetector) Name() string {
	return "manual"
}

// Detect returns the manually set location
func (d *ManualDetector) Detect(ctx context.Context) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.location == "" {
		return "", errors.New("no manual location set")
	}

	return d.location, nil
}

// SetLocation sets the manual location override
func (d *ManualDetector) SetLocation(location string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.location = location
}

// ClearLocation clears the manual location override
func (d *ManualDetector) ClearLocation() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.location = ""
}
