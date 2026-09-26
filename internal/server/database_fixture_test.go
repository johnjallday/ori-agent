package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
)

// newFixtureDatabase is only for ordinary fixtures whose services do not own
// database shutdown. Reset/startup/reopen fixtures retain real database.Open.
func newFixtureDatabase(t *testing.T) *database.DB {
	t.Helper()
	db := testdb.Open(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close fixture database: %v", err)
		}
	})
	return db
}
