//go:build !darwin && !linux

package resetstate

import "os"

// Until native locking, private ACLs and durable replacement are implemented and
// validated together, these hosts cannot stage/apply destructive reset. Normal
// startup is allowed only when no reset metadata exists (see BeforeStores).
func Supported() bool                     { return false }
func noFollowFlag() int                   { return 0 }
func privateEntry(os.FileInfo, bool) bool { return false }
func lockExclusive(*os.File) error        { return ErrUnsupported }
