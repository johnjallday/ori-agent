package fileparser

import (
	"os"
	"runtime/metrics"
	"time"

	"golang.org/x/sys/unix"
)

// Darwin refuses RLIMIT_AS. Run only in the disposable Go parser child,
// with its own GC budget, CPU and wall deadlines, and a Go memory watchdog.
func limitReviewPDF() bool {
	if unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: 2, Max: 2}) != nil {
		return false
	}
	go func() {
		samples := []metrics.Sample{{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/released:bytes"}}
		for {
			metrics.Read(samples)
			if samples[0].Value.Uint64()-samples[1].Value.Uint64() > 96<<20 {
				os.Exit(2)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return true
}
