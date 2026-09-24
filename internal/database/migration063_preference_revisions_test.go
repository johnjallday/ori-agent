package database

import "testing"

func TestMigration063BackfillsExistingUsersAndTracksOnlyChangedPreferenceFields(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`DROP TRIGGER user_preference_revisions_update`,
		`DROP TRIGGER user_preference_revisions_insert`,
		`DROP TABLE user_preference_revisions`,
		`INSERT INTO users (id, preferences, created_at, updated_at) VALUES ('prior-user', '{"response_style":"concise","language":"English"}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.migration063PreferenceRevisions(ctx); err != nil {
		t.Fatal(err)
	}
	var generation int
	if err := db.QueryRowContext(ctx, `SELECT revision FROM user_preference_revisions WHERE user_id = 'prior-user' AND field = 'response_style'`).Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("legacy profile generation = %d, %v", generation, err)
	}
	for _, update := range []string{
		`{"response_style":"concise","language":"French"}`,
		`{"language":"French"}`,
		`{"response_style":"concise","language":"French"}`,
	} {
		if _, err := db.ExecContext(ctx, `UPDATE users SET preferences = ? WHERE id = 'prior-user'`, update); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT revision FROM user_preference_revisions WHERE user_id = 'prior-user' AND field = 'response_style'`).Scan(&generation); err != nil || generation != 3 {
		t.Fatalf("unchanged/cleared/re-entered generation = %d, %v; want 3", generation, err)
	}
}
