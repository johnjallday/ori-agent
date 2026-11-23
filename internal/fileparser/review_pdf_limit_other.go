//go:build !linux && !darwin && !freebsd

package fileparser

// No resource-limited review worker on this platform; report the file skipped.
func limitReviewPDF() bool { return false }
