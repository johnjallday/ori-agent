package helpers

import (
	"strings"
	"testing"
)

// Assertion helpers for user tests

// AssertEqual checks if two values are equal
func AssertEqual(t *testing.T, expected, actual interface{}, message string) {
	t.Helper()
	if expected != actual {
		t.Errorf("%s: expected %v, got %v", message, expected, actual)
	}
}

// AssertNotEqual checks if two values are not equal
func AssertNotEqual(t *testing.T, expected, actual interface{}, message string) {
	t.Helper()
	if expected == actual {
		t.Errorf("%s: expected values to be different, both are %v", message, expected)
	}
}

// AssertTrue checks if a condition is true
func AssertTrue(t *testing.T, condition bool, message string) {
	t.Helper()
	if !condition {
		t.Errorf("%s: expected true, got false", message)
	}
}

// AssertFalse checks if a condition is false
func AssertFalse(t *testing.T, condition bool, message string) {
	t.Helper()
	if condition {
		t.Errorf("%s: expected false, got true", message)
	}
}

// AssertContains checks if a string contains a substring
func AssertContains(t *testing.T, haystack, needle, message string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: expected '%s' to contain '%s'", message, haystack, needle)
	}
}

// AssertNotContains checks if a string does not contain a substring
func AssertNotContains(t *testing.T, haystack, needle, message string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%s: expected '%s' to not contain '%s'", message, haystack, needle)
	}
}

// AssertNil checks if a value is nil
func AssertNil(t *testing.T, value interface{}, message string) {
	t.Helper()
	if value != nil {
		t.Errorf("%s: expected nil, got %v", message, value)
	}
}

// AssertNotNil checks if a value is not nil
func AssertNotNil(t *testing.T, value interface{}, message string) {
	t.Helper()
	if value == nil {
		t.Errorf("%s: expected non-nil value", message)
	}
}

// AssertError checks if an error is not nil
func AssertError(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error, got nil", message)
	}
}

// AssertNoError checks if an error is nil
func AssertNoError(t *testing.T, err error, message string) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: expected no error, got %v", message, err)
	}
}

// AssertLen checks if a slice/array/map has expected length
func AssertLen(t *testing.T, collection interface{}, expectedLen int, message string) {
	t.Helper()

	var actualLen int
	switch v := collection.(type) {
	case []interface{}:
		actualLen = len(v)
	case []string:
		actualLen = len(v)
	case map[string]interface{}:
		actualLen = len(v)
	default:
		t.Errorf("%s: unsupported collection type: %T", message, collection)
		return
	}

	if actualLen != expectedLen {
		t.Errorf("%s: expected length %d, got %d", message, expectedLen, actualLen)
	}
}

// AssertGreaterThan checks if a value is greater than another
func AssertGreaterThan(t *testing.T, value, threshold int, message string) {
	t.Helper()
	if value <= threshold {
		t.Errorf("%s: expected %d > %d", message, value, threshold)
	}
}

// AssertLessThan checks if a value is less than another
func AssertLessThan(t *testing.T, value, threshold int, message string) {
	t.Helper()
	if value >= threshold {
		t.Errorf("%s: expected %d < %d", message, value, threshold)
	}
}

// AssertInRange checks if a value is within a range
func AssertInRange(t *testing.T, value, min, max int, message string) {
	t.Helper()
	if value < min || value > max {
		t.Errorf("%s: expected %d to be in range [%d, %d]", message, value, min, max)
	}
}

// Logf logs a formatted message (helper for verbose output)
func Logf(t *testing.T, format string, args ...interface{}) {
	t.Helper()
	t.Logf(format, args...)
}

// Errorf logs a formatted error
func Errorf(t *testing.T, format string, args ...interface{}) {
	t.Helper()
	t.Errorf(format, args...)
}

// Fatalf logs a formatted fatal error
func Fatalf(t *testing.T, format string, args ...interface{}) {
	t.Helper()
	t.Fatalf(format, args...)
}

// PrintSection prints a section header for better test output organization
func PrintSection(t *testing.T, title string) {
	t.Helper()
	t.Log("\n" + strings.Repeat("=", 60))
	t.Logf("  %s", title)
	t.Log(strings.Repeat("=", 60))
}
