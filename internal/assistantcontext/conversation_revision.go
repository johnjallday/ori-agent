package assistantcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// ConversationRevision is canonical metadata evidence, not a transcript or
// authorization. A current approved read/save pins it across concurrent turns.
func ConversationRevision(updated time.Time, count int) string {
	sum := sha256.Sum256([]byte(updated.UTC().String() + "/" + strconv.Itoa(count)))
	return hex.EncodeToString(sum[:])
}
