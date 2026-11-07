package learningmissions

import "time"

const (
	RetentionAge               = 30 * 24 * time.Hour
	RetainedTerminalInstances  = 100
	RetainedTerminalOperations = 200
	RetentionBatch             = 200
)

// retain runs only inside a new admitted mutation, never on GET or a timer.
// Age/count alone never authorizes removing live, uncertain, original-lineage
// or recovery dependencies. If safe compaction cannot make room, admission
// fails; exclusions and content-free evidence are never silently evicted.
func (m *Mutation) retain(now int64) error {
	cutoff := now - RetentionAge.Milliseconds()
	_, err := m.tx.ExecContext(m.ctx, `DELETE FROM learning_instances WHERE owner_id=? AND id IN (
 SELECT i.id FROM learning_instances i WHERE i.owner_id=? AND i.state NOT IN ('active','blocked','offered') AND i.updated_at_ms<?
 AND i.id NOT IN (SELECT id FROM learning_instances WHERE owner_id=? AND state NOT IN ('active','blocked','offered') ORDER BY created_at_ms DESC,id LIMIT ?)
 AND NOT EXISTS (SELECT 1 FROM learning_operations o WHERE o.owner_id=i.owner_id AND o.status IN ('pending','unknown') AND json_extract(o.record,'$.instance_id')=i.id)
 AND NOT EXISTS (SELECT 1 FROM learning_instances child JOIN learning_reviews r ON r.owner_id=child.owner_id AND r.id=json_extract(child.definition,'$.original_receipt')
 WHERE child.owner_id=i.owner_id AND child.id!=i.id AND json_extract(r.record,'$.instance_id')=i.id)
 ORDER BY i.updated_at_ms,i.id LIMIT ?)`, m.owner.ID, m.owner.ID, cutoff, m.owner.ID, RetainedTerminalInstances, RetentionBatch)
	if err != nil {
		return ErrUnavailable
	}
	// Full receipts are capped separately from content-free replay metadata.
	// Compaction removes notes, comparisons, support challenges and authority;
	// only exact dedup/ranking metadata survives with the earned evidence.
	var count int
	if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*) FROM learning_reviews WHERE owner_id=? AND COALESCE(json_extract(record,'$.compacted'),0)=0`, m.owner.ID).Scan(&count); err != nil {
		return ErrUnavailable
	}
	if count >= MaxReviewReceipts {
		rows, err := m.tx.QueryContext(m.ctx, `SELECT r.id FROM learning_reviews r WHERE r.owner_id=? AND r.created_at_ms<? AND COALESCE(json_extract(r.record,'$.compacted'),0)=0
 AND NOT EXISTS (SELECT 1 FROM learning_instances i WHERE i.owner_id=r.owner_id AND (json_extract(i.definition,'$.original_receipt')=r.id OR json_extract(i.progress,'$.review_id')=r.id OR i.id=json_extract(r.record,'$.instance_id')))
 AND NOT EXISTS (SELECT 1 FROM learning_operations o WHERE o.owner_id=r.owner_id AND o.status IN ('pending','unknown') AND
 (json_extract(o.record,'$.review_id')=r.id OR json_extract(o.record,'$.observation.review_id')=r.id OR json_extract(o.record,'$.milestone.review_id')=r.id))
 ORDER BY r.created_at_ms,r.id LIMIT ?`, m.owner.ID, cutoff, min(RetentionBatch, count-MaxReviewReceipts+1))
		if err != nil {
			return ErrUnavailable
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return ErrUnavailable
			}
			ids = append(ids, id)
		}
		rowErr := rows.Err()
		closeErr := rows.Close()
		if rowErr != nil || closeErr != nil {
			return ErrUnavailable
		}
		for _, id := range ids {
			if _, err := m.tx.ExecContext(m.ctx, `UPDATE learning_evidence SET record=json_set(record,'$.unavailable',json('true')) WHERE owner_id=? AND json_extract(record,'$.review_id')=?`, m.owner.ID, id); err != nil {
				return ErrUnavailable
			}
			if _, err := m.tx.ExecContext(m.ctx, `UPDATE learning_reviews SET record=json_object('id',id,'compacted',json('true'),
 'input_digest',json_extract(record,'$.input_digest'),'decision',json_extract(record,'$.decision'),'decided_at_ms',json_extract(record,'$.decided_at_ms'),
 'result',json_object('studio_id',json_extract(record,'$.result.studio_id'),'incarnation',json_extract(record,'$.result.incarnation'),'ticket_id',json_extract(record,'$.result.ticket_id')))
 WHERE owner_id=? AND id=? AND EXISTS (SELECT 1 FROM learning_evidence e WHERE e.owner_id=learning_reviews.owner_id AND json_extract(e.record,'$.review_id')=learning_reviews.id)`, m.owner.ID, id); err != nil {
				return ErrUnavailable
			}
			if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM learning_reviews WHERE owner_id=? AND id=? AND NOT EXISTS (SELECT 1 FROM learning_evidence e WHERE e.owner_id=learning_reviews.owner_id AND json_extract(e.record,'$.review_id')=learning_reviews.id)`, m.owner.ID, id); err != nil {
				return ErrUnavailable
			}
		}
	}
	_, err = m.tx.ExecContext(m.ctx, `DELETE FROM learning_operations WHERE owner_id=? AND id IN (
 SELECT o.id FROM learning_operations o WHERE o.owner_id=? AND o.status IN ('succeeded','failed','invalidated') AND o.created_at_ms<?
 AND NOT (o.kind='generate' AND COALESCE(json_extract(o.record,'$.automatic'),0)=1 AND o.epoch=?)
 AND o.id NOT IN (SELECT id FROM learning_operations WHERE owner_id=? AND status IN ('succeeded','failed','invalidated') ORDER BY created_at_ms DESC,id LIMIT ?)
 AND NOT EXISTS (SELECT 1 FROM learning_instances i WHERE i.owner_id=o.owner_id AND json_extract(i.progress,'$.operation_id')=o.id)
 AND NOT EXISTS (SELECT 1 FROM learning_reviews r WHERE r.owner_id=o.owner_id AND json_extract(r.record,'$.observation_id')=o.id)
 AND NOT EXISTS (SELECT 1 FROM learning_operations pending WHERE pending.owner_id=o.owner_id AND pending.status IN ('pending','unknown') AND
 (json_extract(pending.record,'$.observation_id')=o.id OR json_extract(pending.record,'$.milestone_id')=o.id))
 ORDER BY o.created_at_ms,o.id LIMIT ?)`, m.owner.ID, m.owner.ID, cutoff, m.owner.Epoch, m.owner.ID, RetainedTerminalOperations, RetentionBatch)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
