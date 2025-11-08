package menubar

import (
	_ "embed"
)

// Icon assets embedded at compile time
// These icons are displayed in the macOS menu bar to indicate server status

//go:embed icons/icon.png
var iconStopped []byte

//go:embed icons/icon-starting.png
var iconStarting []byte

//go:embed icons/icon-running.png
var iconRunning []byte

//go:embed icons/icon-stopping.png
var iconStopping []byte

//go:embed icons/icon-error.png
var iconError []byte

// GetIconForStatus returns the appropriate icon for the given server status
func GetIconForStatus(status ServerStatus) []byte {
	switch status {
	case StatusStopped:
		return iconStopped
	case StatusStarting:
		return iconStarting
	case StatusRunning:
		return iconRunning
	case StatusStopping:
		return iconStopping
	case StatusError:
		return iconError
	default:
		return iconStopped
	}
}

// Icon getters for backward compatibility and direct access
func GetStoppedIcon() []byte {
	return iconStopped
}

func GetStartingIcon() []byte {
	return iconStarting
}

func GetRunningIcon() []byte {
	return iconRunning
}

func GetStoppingIcon() []byte {
	return iconStopping
}

func GetErrorIcon() []byte {
	return iconError
}
