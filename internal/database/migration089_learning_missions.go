package database

import "context"

// Learning authority is device-local. Only source incarnation rows have a
// workspace FK: deleting a source must revoke its local identity without
// erasing earned aggregate practice or replay tombstones.
// App-record reset explicitly inventories all of these tables.
func (db *DB) migration089LearningMissions(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS learning_owners (
 owner_id TEXT PRIMARY KEY CHECK(length(owner_id) BETWEEN 1 AND 128),
 revision INTEGER NOT NULL CHECK(revision > 0),
 epoch INTEGER NOT NULL CHECK(epoch > 0),
 preferences TEXT NOT NULL CHECK(length(CAST(preferences AS BLOB)) <= 8192)
);
CREATE TABLE IF NOT EXISTS learning_sources (
 owner_id TEXT NOT NULL REFERENCES learning_owners(owner_id) ON DELETE CASCADE,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 incarnation TEXT NOT NULL CHECK(length(incarnation) BETWEEN 1 AND 128),
 PRIMARY KEY(owner_id,workspace_id)
);
CREATE TABLE IF NOT EXISTS learning_instances (
 owner_id TEXT NOT NULL REFERENCES learning_owners(owner_id) ON DELETE CASCADE,
 id TEXT NOT NULL CHECK(length(id) BETWEEN 1 AND 128),
 epoch INTEGER NOT NULL CHECK(epoch > 0),
 revision INTEGER NOT NULL CHECK(revision > 0),
 state TEXT NOT NULL CHECK(state IN ('offered','active','blocked','completed','deferred','declined','stopped','invalidated')),
 definition TEXT NOT NULL CHECK(length(CAST(definition AS BLOB)) <= 16384),
 progress TEXT NOT NULL CHECK(length(CAST(progress AS BLOB)) <= 16384),
 created_at_ms INTEGER NOT NULL,
 updated_at_ms INTEGER NOT NULL,
 PRIMARY KEY(owner_id,id)
);
CREATE UNIQUE INDEX IF NOT EXISTS learning_one_active ON learning_instances(owner_id) WHERE state IN ('active','blocked');
CREATE UNIQUE INDEX IF NOT EXISTS learning_one_offer ON learning_instances(owner_id) WHERE state = 'offered';
CREATE TABLE IF NOT EXISTS learning_operations (
 owner_id TEXT NOT NULL REFERENCES learning_owners(owner_id) ON DELETE CASCADE,
 id TEXT NOT NULL CHECK(length(id) BETWEEN 1 AND 128),
 epoch INTEGER NOT NULL CHECK(epoch > 0),
 kind TEXT NOT NULL CHECK(length(kind) BETWEEN 1 AND 32),
 status TEXT NOT NULL CHECK(status IN ('pending','succeeded','failed','unknown','invalidated')),
 request_digest TEXT NOT NULL CHECK(length(request_digest) = 64),
 record TEXT NOT NULL CHECK(length(CAST(record AS BLOB)) <= 16384),
 created_at_ms INTEGER NOT NULL,
 PRIMARY KEY(owner_id,id)
);
CREATE UNIQUE INDEX IF NOT EXISTS learning_one_generation ON learning_operations(owner_id) WHERE kind = 'generate' AND status = 'pending';
CREATE TABLE IF NOT EXISTS learning_reviews (
 owner_id TEXT NOT NULL REFERENCES learning_owners(owner_id) ON DELETE CASCADE,
 id TEXT NOT NULL CHECK(length(id) BETWEEN 1 AND 128),
 epoch INTEGER NOT NULL CHECK(epoch > 0),
 record TEXT NOT NULL CHECK(length(CAST(record AS BLOB)) <= 8192),
 created_at_ms INTEGER NOT NULL,
 PRIMARY KEY(owner_id,id)
);
CREATE TABLE IF NOT EXISTS learning_evidence (
 owner_id TEXT NOT NULL REFERENCES learning_owners(owner_id) ON DELETE CASCADE,
 dedup_key TEXT NOT NULL CHECK(length(dedup_key) = 64),
 record TEXT NOT NULL CHECK(length(CAST(record AS BLOB)) <= 8192),
 created_at_ms INTEGER NOT NULL,
 PRIMARY KEY(owner_id,dedup_key)
);
`)
	return err
}
