package database

import (
	"context"
	"testing"
)

func TestMigration057CreatesImmutableUserSetupBindingAndRootClaims(t *testing.T) {
	db, err := Open(context.Background(), &Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{"setup_user_template_binding", "setup_user_template_root_claim"} {
		var count int
		if err := db.QueryRowContext(context.Background(), `
			SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?
		`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO setup_user_template_binding
		(user_id, template_id, attachment_id, quest_id, definition_digest, execution_digest, created_at)
		VALUES ('local', 'template', 'uqatt_0123456789abcdef01234567', 'quest_0123456789abcdef01234567', ?, ?, CURRENT_TIMESTAMP)
	`, digestFixture('a'), digestFixture('b')); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO setup_user_template_root_claim
		(root_kind, root_id, user_id, template_id, attachment_id, quest_id, created_at)
		VALUES ('project', 'project-1', 'local', 'template', 'uqatt_0123456789abcdef01234567', 'quest_0123456789abcdef01234567', CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `
		DELETE FROM setup_user_template_binding WHERE user_id = 'local' AND template_id = 'template'
	`); err == nil {
		t.Fatal("binding deletion succeeded despite a durable root claim")
	}
}

func digestFixture(value byte) string {
	result := make([]byte, 64)
	for index := range result {
		result[index] = value
	}
	return string(result)
}
