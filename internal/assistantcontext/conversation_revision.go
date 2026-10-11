package assistantcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// ConversationRevision is canonical metadata evidence, not a transcript or
// authorization. A current approved read/save pins it across concurrent turns.
func ConversationRevision(updated time.Time, count int, epoch ...int64) string {
	text := updated.UTC().String() + "/" + strconv.Itoa(count)
	if len(epoch) > 0 && epoch[0] != 0 {
		text += "/" + strconv.FormatInt(epoch[0], 10)
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
