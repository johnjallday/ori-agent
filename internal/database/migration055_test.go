package database

import (
	"context"
	"testing"
)

func TestMigration055CreatesNormalizedSampleLibrarySchema(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, table := range []string{"sample_library_state", "sample_library_root", "sample_library_entry", "sample_library_content_fact", "sample_library_annotation", "sample_library_collection", "sample_library_collection_member", "sample_library_child_copy", "sample_library_review_receipt", "sample_library_operation_receipt"} {
		exists, existsErr := db.tableExists(ctx, table)
		if existsErr != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, existsErr)
		}
	}
	for _, forbidden := range []string{"absolute_path", "audio_bytes", "prompt", "credential", "manifest", "agent_state"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('sample_library_entry') WHERE lower(name) = ?`, forbidden).Scan(&count); err != nil || count != 0 {
			t.Fatalf("forbidden entry column %q count=%d err=%v", forbidden, count, err)
		}
	}
}
