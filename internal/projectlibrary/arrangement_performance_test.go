package projectlibrary

import (
	"os"
	"runtime"
	"slices"
	"testing"
	"time"
)

// Opt-in, normal-build page latency evidence. This measures sequential warm
// calls through real authorization/storage, not model time or cold OS caches.
// Run with ORI_ARRANGEMENT_PERF=1 go test -short ... -run TestArrangementPopulatedFlow -v.
func measureArrangementPages(t *testing.T, phase string, documentBytes, members int, read func(int) error) {
	t.Helper()
	if os.Getenv("ORI_ARRANGEMENT_PERF") != "1" {
		return
	}
	const samples = 100
	start := time.Now()
	if err := read(0); err != nil {
		t.Fatal(err)
	}
	first := time.Since(start)
	elapsed := make([]time.Duration, samples)
	for i := range elapsed {
		start = time.Now()
		if err := read(i); err != nil {
			t.Fatal(err)
		}
		elapsed[i] = time.Since(start)
	}
	slices.Sort(elapsed)
	t.Logf("page_latency phase=%s go=%s os=%s arch=%s cpus=%d catalog_entries=1001 document_bytes=%d selected_members=%d samples=%d first_probe=%s warm_p50=%s warm_p95=%s warm_max=%s", phase, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), documentBytes, members, samples, first, elapsed[49], elapsed[94], elapsed[99])
	if elapsed[94] > 250*time.Millisecond {
		t.Errorf("%s warm page p95 %s exceeds 250ms target", phase, elapsed[94])
	}
}
