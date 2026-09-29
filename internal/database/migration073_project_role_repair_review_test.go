package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMigration073ProjectRoleRepairReviewHasSeparateBoundedConsentIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, &Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("migration not applied: %d %v", version, err)
	}
	if err := db.migration073ProjectRoleRepairReview(ctx); err != nil {
		t.Fatalf("idempotent schema migration: %v", err)
	}
	now := time.Now().UTC()
	insert := func(token, owner, key string) error {
		_, insertErr := db.ExecContext(ctx, `INSERT INTO project_role_repair_review
			(token, owner_user_id, home_id, project_id, idempotency_key, evidence_digest, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, token, owner, "home", "exact-child", key, strings.Repeat("a", 64), now, now.Add(10*time.Minute))
		return insertErr
	}
	if err := insert("first", "local", "review-one"); err != nil {
		t.Fatal(err)
	}
	if err := insert("replacement-token", "local", "review-one"); err == nil {
		t.Fatal("same owner/child/key minted a second consent token")
	}
	if err := insert("other-owner", "different-owner", "review-one"); err != nil {
		t.Fatalf("another owner should have an independent namespace: %v", err)
	}
	claim := func(token, owner, reviewToken, key string) error {
		_, claimErr := db.ExecContext(ctx, `INSERT INTO project_role_repair_operation
			(token, owner_user_id, home_id, project_id, idempotency_key, review_token,
			evidence_digest, status, created_at, updated_at)
			VALUES (?, ?, 'home', 'exact-child', ?, ?, ?, 'claimed', ?, ?)`, token, owner, key, reviewToken, strings.Repeat("a", 64), now, now)
		return claimErr
	}
	if err := claim("first-claim", "local", "first", "claim-one"); err != nil {
		t.Fatal(err)
	}
	if err := claim("competing-claim", "different-owner", "other-owner", "claim-two"); err == nil {
		t.Fatal("a second process/owner claimed an active child")
	}
	if err := claim("duplicate-review", "local", "first", "claim-two"); err == nil {
		t.Fatal("the same review token authorized two claims")
	}
	if err := insert("bad-digest", "local", "review-two"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE project_role_repair_review SET evidence_digest = ? WHERE token = ?`, "not-a-digest", "bad-digest"); err == nil {
		t.Fatal("invalid digest was stored as review authority")
	}
}
