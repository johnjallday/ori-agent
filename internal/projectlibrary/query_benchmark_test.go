package projectlibrary

import (
	"slices"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// BenchmarkQuery_PersistedThousandRecords is the explicit measurement that
// TestQuery_BoundedRevisionCursorAtThousandRecords used to run inline as 100
// requests with a logged p95. It reads the same persisted library of 1,000
// entries (40 of them edited) through the real Store:
//
//	go test ./internal/projectlibrary -run '^$' -bench BenchmarkQuery_PersistedThousandRecords -benchtime=100x -count=3
//
// -benchtime=100x reproduces the former sample size. Every sub-benchmark also
// reports the p50 and p95 of its own requests (p50-ns, p95-ns), the figure the
// old log line gave. Seeding, the restart and the cursor walk are outside the
// measured region.
//
// Cold and warm differ on purpose. cold_first_page opens a store over the files
// afresh for every request, so each one pays the document decode and projection
// a restarted process pays. The warm_* requests share one restarted store that
// has already served pages (first, the middle cursor, the last cursor, and a
// search that matches nothing). These are normal-build timings on the machine
// running them: never compare them with race-instrumented CI numbers.
func BenchmarkQuery_PersistedThousandRecords(b *testing.B) {
	// Every restart rebuilds the workspace index and logs it; a hundred of those
	// lines would bury the results.
	logger.SetLevel(logger.ErrorLevel)
	b.Cleanup(func() { logger.SetLevel(logger.InfoLevel) })
	fixture := newThousandRecordFixture(b)
	warm, closeWarm := fixture.open(b)
	b.Cleanup(closeWarm)
	middle, last := pageCursors(b, warm, fixture.scope)
	shapes := []struct {
		name   string
		cold   bool
		search Search
	}{
		{"cold_first_page", true, Search{PageSize: 50}},
		{"warm_first_page", false, Search{PageSize: 50}},
		{"warm_middle_page", false, Search{PageSize: 50, Cursor: middle}},
		{"warm_last_page", false, Search{PageSize: 50, Cursor: last}},
		{"warm_no_match", false, Search{Text: "no such project"}},
	}
	for _, shape := range shapes {
		b.Run(shape.name, func(b *testing.B) {
			b.ReportAllocs()
			durations := make([]time.Duration, 0, b.N)
			b.ResetTimer()
			for range b.N {
				started := time.Now()
				store, release := warm, func() {}
				if shape.cold {
					store, release = fixture.open(b)
				}
				_, err := store.Query(fixture.scope, shape.search)
				durations = append(durations, time.Since(started))
				if shape.cold {
					// Releasing the store is not part of the request.
					b.StopTimer()
					release()
					b.StartTimer()
				}
				if err != nil {
					b.Fatalf("%s: %v", shape.name, err)
				}
			}
			b.StopTimer()
			slices.Sort(durations)
			percentile := func(p float64) float64 {
				return float64(durations[min(len(durations)-1, int(float64(len(durations))*p))].Nanoseconds())
			}
			b.ReportMetric(percentile(0.50), "p50-ns")
			b.ReportMetric(percentile(0.95), "p95-ns")
		})
	}
}
