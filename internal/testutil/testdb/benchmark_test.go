package testdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

// BenchmarkOpenFresh retains the real initialization path as a performance
// baseline. It includes setup, a seed-row check, and close, not compilation.
// Use -benchtime=3x -count=3 -race for bounded, fresh-execution comparisons.
func BenchmarkOpenFresh(b *testing.B) {
	for _, storage := range []string{"memory", "file"} {
		b.Run(storage, func(b *testing.B) {
			ctx := context.Background()
			for b.Loop() {
				cfg := &database.Config{InMemory: storage == "memory"}
				if !cfg.InMemory {
					cfg.Path = filepath.Join(b.TempDir(), "fresh.db")
				}
				db, err := database.Open(ctx, cfg)
				if err != nil {
					b.Fatal(err)
				}
				var localUsers int
				queryErr := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id = 'local'").Scan(&localUsers)
				closeErr := db.Close()
				if queryErr != nil || localUsers != 1 || closeErr != nil {
					b.Fatalf("fresh fixture: local users=%d, query=%v, close=%v", localUsers, queryErr, closeErr)
				}
			}
		})
	}
}
