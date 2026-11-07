package learningmissions

import (
	"context"
	"errors"
)

const (
	MaxReviewReceipts = 1000
	// Within (not in addition to) the ceiling: keep room to finish already
	// admitted reviews/effects and stop/disable learning when admission is full.
	StorageControlReserve    int64 = 256 * 1024
	GenerationStorageReserve       = 2 * MaxRecordBytes
)

var ErrStorageLimit = errors.New("learning record capacity reached")

type storageUsage struct {
	Bytes             int64
	Reviews, Evidence int
}

// Logical owned-record bytes plus conservative row/key overhead and pending
// design-publication reservations, not the SQLite file/freelist or Run storage.
// Only lengths/counts are returned; private contents never enter diagnostics.
func learningStorageUsage(ctx context.Context, q queryer, owner string) (storageUsage, error) {
	var out storageUsage
	err := q.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(SUM(length(CAST(preferences AS BLOB))+length(owner_id)+64),0) FROM learning_owners)+
 (SELECT COALESCE(SUM(length(owner_id)+length(workspace_id)+length(incarnation)+64),0) FROM learning_sources)+
 (SELECT COALESCE(SUM(length(CAST(definition AS BLOB))+length(CAST(progress AS BLOB))+length(owner_id)+length(id)+128),0) FROM learning_instances)+
 (SELECT COALESCE(SUM(length(CAST(record AS BLOB))+length(owner_id)+length(id)+length(request_digest)+96+CASE WHEN kind='generate' AND status='pending' THEN ? ELSE 0 END),0) FROM learning_operations)+
 (SELECT COALESCE(SUM(length(CAST(record AS BLOB))+length(owner_id)+length(id)+64),0) FROM learning_reviews)+
 (SELECT COALESCE(SUM(length(CAST(record AS BLOB))+length(owner_id)+length(dedup_key)+64),0) FROM learning_evidence),
 (SELECT COUNT(*) FROM learning_reviews WHERE owner_id=? AND COALESCE(json_extract(record,'$.compacted'),0)=0),
 (SELECT COUNT(*) FROM learning_evidence WHERE owner_id=?)`, GenerationStorageReserve, owner, owner).Scan(&out.Bytes, &out.Reviews, &out.Evidence)
	if err != nil {
		return storageUsage{}, ErrUnavailable
	}
	return out, nil
}

func (m *Mutation) checkStorage(before storageUsage) error {
	after, err := learningStorageUsage(m.ctx, m.tx, m.owner.ID)
	if err != nil {
		return err
	}
	if (after.Reviews > MaxReviewReceipts && after.Reviews > before.Reviews) || (after.Evidence > MaxEvidence && after.Evidence > before.Evidence) {
		return ErrStorageLimit
	}
	limit := MaxLearningBytes - StorageControlReserve
	if m.storageControl && !m.storageAdmission {
		limit = MaxLearningBytes
	}
	// A reducing clear/control remains usable even for an over-limit old store.
	if after.Bytes > limit && after.Bytes > before.Bytes {
		return ErrStorageLimit
	}
	return nil
}
