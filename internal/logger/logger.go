package logger

import (
	"fmt"
	"os"
)

// IsVerbose returns true if verbose logging is enabled
func IsVerbose() bool {
	return os.Getenv("ORI_VERBOSE") == "true"
}

// Verbose prints the message only if verbose mode is enabled
func Verbose(format string, args ...interface{}) {
	if IsVerbose() {
		fmt.Printf(format, args...)
	}
}

// Verbosef prints the message with a newline only if verbose mode is enabled
func Verbosef(format string, args ...interface{}) {
	if IsVerbose() {
		fmt.Printf(format+"\n", args...)
	}
}
