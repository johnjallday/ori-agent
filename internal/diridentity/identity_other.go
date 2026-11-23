//go:build !darwin && !linux && !freebsd

package diridentity

import "os"

// Of returns no witness on platforms without supported durable identities.
func Of(os.FileInfo) string { return "" }
