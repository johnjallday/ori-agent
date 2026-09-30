package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMigration074HomePackageUpgradeAllowsOneActiveOperationPerPackage(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, &Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.migration074HomePackageUpgrade(ctx); err != nil {
		t.Fatalf("idempotent schema migration: %v", err)
	}
	now := time.Now().UTC()
	review := func(token string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO home_package_upgrade_review
			(token, owner_user_id, plugin_id, plan_digest, created_at, expires_at) VALUES (?, 'local', 'music', ?, ?, ?)`,
			token, strings.Repeat("a", 64), now, now.Add(10*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(id, pluginID, reviewToken, status string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO home_package_upgrade_operation
			(id, owner_user_id, plugin_id, review_token, plan_digest, plan_json, status, created_at, updated_at)
			VALUES (?, 'local', ?, ?, ?, '{}', ?, ?, ?)`, id, pluginID, reviewToken, strings.Repeat("a", 64), status, now, now)
		return err
	}
	for _, token := range []string{"r1", "r2", "r3", "r4", "r5"} {
		review(token)
	}
	if err := claim("op-1", "music", "r1", "claimed"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"claimed", "replaced", "reconcile_required"} {
		if err := claim("competing-"+status, "music", "r2", status); err == nil {
			t.Fatalf("a second %s operation joined an active package upgrade", status)
		}
	}
	if err := claim("other-package", "reaper", "r3", "claimed"); err != nil {
		t.Fatalf("another package has an independent slot: %v", err)
	}
	if err := claim("same-review", "tools", "r1", "claimed"); err == nil {
		t.Fatal("one review authorized two operations")
	}
	if err := claim("unknown-review", "tools", "missing", "claimed"); err == nil {
		t.Fatal("an operation was recorded without a review")
	}
	if err := claim("bad-status", "tools", "r4", "running"); err == nil {
		t.Fatal("an unknown status was stored")
	}
	if _, err := db.ExecContext(ctx, `UPDATE home_package_upgrade_operation SET status = 'succeeded' WHERE id = 'op-1'`); err != nil {
		t.Fatal(err)
	}
	if err := claim("after-success", "music", "r5", "claimed"); err != nil {
		t.Fatalf("a finished operation still holds the package slot: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM home_package_upgrade_review WHERE token = 'r1'`); err == nil {
		t.Fatal("the review behind a recorded operation was deleted")
	}
}
