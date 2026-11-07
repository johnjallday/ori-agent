package learningmissions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/johnjallday/ori-agent/internal/database"
)

// SQLiteStore shares the application connection. Constructors perform no writes.
// Every writer serializes through the durable owner revision, not a Go mutex.
type SQLiteStore struct{ db *sql.DB }

func NewSQLiteStore(db *database.DB) *SQLiteStore {
	if db == nil {
		return &SQLiteStore{}
	}
	return &SQLiteStore{db: db.DB}
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type rowScanner interface{ Scan(...any) error }

func readOwner(ctx context.Context, q queryer, id string) (Owner, error) {
	owner := Owner{ID: id, Preferences: DefaultPreferences()}
	if !validID(id) {
		return Owner{}, ErrUnavailable
	}
	var prefs string
	err := q.QueryRowContext(ctx, `SELECT revision,epoch,preferences FROM learning_owners WHERE owner_id=?`, id).
		Scan(&owner.Revision, &owner.Epoch, &prefs)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, nil // no enrollment on read
	}
	if err != nil || len(prefs) > MaxResponseBytes || json.Unmarshal([]byte(prefs), &owner.Preferences) != nil || owner.Preferences.Validate() != nil {
		return Owner{}, ErrUnavailable
	}
	return owner, nil
}

func (s *SQLiteStore) Owner(ctx context.Context, id string) (Owner, error) {
	if s == nil || s.db == nil {
		return Owner{}, ErrUnavailable
	}
	return readOwner(ctx, s.db, id)
}

const instanceColumns = `id,owner_id,epoch,revision,state,definition,progress,created_at_ms,updated_at_ms`

func scanInstance(row rowScanner) (Instance, error) {
	var i Instance
	var definition, progress string
	err := row.Scan(&i.ID, &i.OwnerID, &i.Epoch, &i.Revision, &i.State, &definition, &progress, &i.CreatedAt, &i.UpdatedAt)
	if err != nil || len(definition) > MaxRecordBytes || len(progress) > MaxRecordBytes {
		return Instance{}, ErrUnavailable
	}
	var header struct {
		Structure      StructureID `json:"structure"`
		Version        int         `json:"version"`
		SupportVersion int         `json:"support_version"`
	}
	if json.Unmarshal([]byte(definition), &header) != nil || !validID(string(header.Structure)) {
		return Instance{}, ErrUnavailable
	}
	_, supported := Lookup(header.Structure, header.Version)
	if supported != nil || header.SupportVersion > GuidanceProtocolVersion {
		// Never decode an unsupported schema's private/newly-shaped fields.
		// State-only retirement preserves its raw immutable bytes in SQLite.
		i.Definition = Definition{Structure: header.Structure, Version: header.Version, SupportVersion: header.SupportVersion}
		i.Progress = Progress{Blocker: "definition_unavailable"}
		if !errors.Is(i.Validate(), ErrUnsupported) {
			return Instance{}, ErrUnavailable
		}
		return i, nil
	}
	if json.Unmarshal([]byte(definition), &i.Definition) != nil || json.Unmarshal([]byte(progress), &i.Progress) != nil {
		return Instance{}, ErrUnavailable
	}
	// Retired definitions remain inspectable, but all writers call Validate.
	if err := i.Validate(); err != nil && !errors.Is(err, ErrUnsupported) {
		return Instance{}, ErrUnavailable
	}
	return i, nil
}

func readInstance(ctx context.Context, q queryer, owner, id string) (Instance, error) {
	if !validID(owner) || !validID(id) {
		return Instance{}, ErrUnavailable
	}
	return scanInstance(q.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM learning_instances WHERE owner_id=? AND id=?`, owner, id))
}

func (s *SQLiteStore) Instance(ctx context.Context, owner, id string) (Instance, error) {
	if s == nil || s.db == nil {
		return Instance{}, ErrUnavailable
	}
	return readInstance(ctx, s.db, owner, id)
}

func (s *SQLiteStore) Instances(ctx context.Context, owner string) ([]Instance, error) {
	if s == nil || s.db == nil || !validID(owner) {
		return nil, ErrUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+` FROM learning_instances WHERE owner_id=?
 ORDER BY CASE WHEN state IN ('active','blocked') THEN 0 WHEN state='offered' THEN 1 ELSE 2 END, created_at_ms DESC,id LIMIT 100`, owner)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	out := []Instance{}
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

// Mutation is only valid inside Change's callback. No external work may be
// performed in it: record an operation, commit, then cross the owner boundary.
type Mutation struct {
	ctx              context.Context
	tx               *sql.Tx
	owner            Owner
	storageControl   bool
	storageAdmission bool
}

// Change requires an exact owner revision AND deletion epoch. allowCreate is
// reserved for explicit owner admission, never provider/event callbacks. A
// failed callback rolls back both all child records and the owner revision.
func (s *SQLiteStore) Change(ctx context.Context, id string, revision, epoch int64, allowCreate bool, fn func(*Mutation) error) (Owner, error) {
	if s == nil || s.db == nil || !validID(id) || revision < 0 || epoch < 0 || fn == nil {
		return Owner{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Owner{}, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	// The first statement acquires the writer lock before any read snapshot.
	// This works across store objects/connections; no deferred read->write race.
	if _, err := tx.ExecContext(ctx, `UPDATE learning_owners SET revision=revision WHERE owner_id=?`, id); err != nil {
		return Owner{}, ErrUnavailable
	}
	owner, err := readOwner(ctx, tx, id)
	if err != nil {
		return Owner{}, err
	}
	if owner.Revision != revision || owner.Epoch != epoch {
		return Owner{}, ErrConflict
	}
	beforeStorage, err := learningStorageUsage(ctx, tx, id)
	if err != nil {
		return Owner{}, err
	}
	if owner.Revision == 0 {
		if !allowCreate {
			return Owner{}, ErrConflict
		}
		owner.Epoch = 1
		prefs, err := encode(owner.Preferences, MaxResponseBytes)
		if err != nil {
			return Owner{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO learning_owners(owner_id,revision,epoch,preferences) VALUES(?,1,1,?)`, id, prefs); err != nil {
			return Owner{}, ErrConflict
		}
	}
	owner.Revision++
	m := &Mutation{ctx: ctx, tx: tx, owner: owner}
	if err := fn(m); err != nil {
		return Owner{}, err
	}
	if m.owner.ID != id || m.owner.Revision != owner.Revision || m.owner.Epoch < owner.Epoch || m.owner.Preferences.Validate() != nil {
		return Owner{}, ErrInvalid
	}
	prefs, err := encode(m.owner.Preferences, MaxResponseBytes)
	if err != nil {
		return Owner{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE learning_owners SET revision=?,epoch=?,preferences=? WHERE owner_id=?`,
		m.owner.Revision, m.owner.Epoch, prefs, id); err != nil {
		return Owner{}, ErrUnavailable
	}
	if err := m.checkStorage(beforeStorage); err != nil {
		return Owner{}, err
	}
	if err := tx.Commit(); err != nil {
		return Owner{}, ErrUnavailable // unknown commit outcome: caller must read its operation
	}
	return m.owner, nil
}

func (m *Mutation) Instance(id string) (Instance, error) {
	return readInstance(m.ctx, m.tx, m.owner.ID, id)
}

func (m *Mutation) InsertInstance(i Instance) error {
	m.storageAdmission = true
	if i.OwnerID != m.owner.ID || i.Epoch != m.owner.Epoch || i.Revision != 1 || i.State != Offered {
		return ErrInvalid
	}
	if err := i.Validate(); err != nil {
		return err
	}
	d, err := encode(i.Definition, MaxRecordBytes)
	if err != nil {
		return err
	}
	p, err := encode(i.Progress, MaxRecordBytes)
	if err != nil {
		return err
	}
	_, err = m.tx.ExecContext(m.ctx, `INSERT INTO learning_instances (`+instanceColumns+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		i.ID, i.OwnerID, i.Epoch, i.Revision, i.State, d, p, i.CreatedAt, i.UpdatedAt)
	if err != nil {
		return ErrConflict
	}
	return nil
}

func (m *Mutation) UpdateInstance(i Instance, expected int64) error {
	if i.OwnerID != m.owner.ID || i.Epoch != m.owner.Epoch || i.Revision != expected+1 {
		return ErrConflict
	}
	if err := i.Validate(); err != nil {
		if errors.Is(err, ErrUnsupported) {
			return m.retireUnsupported(i, expected)
		}
		return err
	}
	previous, err := m.Instance(i.ID)
	if err != nil {
		return err
	}
	d, err := encode(i.Definition, MaxRecordBytes)
	if err != nil {
		return err
	}
	old, err := encode(previous.Definition, MaxRecordBytes)
	if err != nil {
		return err
	}
	if d != old || i.CreatedAt != previous.CreatedAt || i.UpdatedAt < previous.UpdatedAt ||
		(i.State != previous.State && !CanTransition(previous.State, i.State)) {
		return ErrConflict
	}
	p, err := encode(i.Progress, MaxRecordBytes)
	if err != nil {
		return err
	}
	if (i.State != previous.State && i.State != Active) || (i.State == Active && previous.State == Active && previous.Progress.TicketID != "") {
		m.storageControl = true
	}
	result, err := m.tx.ExecContext(m.ctx, `UPDATE learning_instances SET revision=?,state=?,progress=?,updated_at_ms=? WHERE owner_id=? AND id=? AND epoch=? AND revision=?`,
		i.Revision, i.State, p, i.UpdatedAt, i.OwnerID, i.ID, i.Epoch, expected)
	if err != nil {
		return ErrConflict
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrConflict
	}
	return nil
}

func readOperation(ctx context.Context, q queryer, owner, id string) (Operation, error) {
	var op Operation
	var data string
	var epoch int64
	var hash string
	err := q.QueryRowContext(ctx, `SELECT record,epoch,request_digest FROM learning_operations WHERE owner_id=? AND id=?`, owner, id).Scan(&data, &epoch, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrUnavailable
	}
	if err != nil || len(data) > MaxRecordBytes || json.Unmarshal([]byte(data), &op) != nil {
		return Operation{}, ErrUnavailable
	}
	op.Epoch, op.RequestDigest = epoch, hash
	if op.Validate() != nil || op.ID != id {
		return Operation{}, ErrUnavailable
	}
	return op, nil
}

func (s *SQLiteStore) Operation(ctx context.Context, owner, id string) (Operation, error) {
	if s == nil || s.db == nil || !validID(owner) || !validID(id) {
		return Operation{}, ErrUnavailable
	}
	return readOperation(ctx, s.db, owner, id)
}

func (m *Mutation) InsertOperation(op Operation) error {
	if op.Kind == "generate" || op.Kind == "prepare_work" || op.Kind == "observe" || op.Kind == "propose" || op.Kind == "adjust" {
		m.storageAdmission = true
	}
	if op.Epoch != m.owner.Epoch {
		return ErrConflict
	}
	if err := op.Validate(); err != nil {
		return err
	}
	data, err := encode(op, MaxRecordBytes)
	if err != nil {
		return err
	}
	_, err = m.tx.ExecContext(m.ctx, `INSERT INTO learning_operations(owner_id,id,epoch,kind,status,request_digest,record,created_at_ms) VALUES(?,?,?,?,?,?,?,?)`,
		m.owner.ID, op.ID, op.Epoch, op.Kind, op.Status, op.RequestDigest, data, op.CreatedAt)
	if err != nil {
		return ErrConflict
	}
	return nil
}

func (m *Mutation) UpdateOperation(op Operation) error {
	if op.Epoch != m.owner.Epoch || op.Validate() != nil {
		return ErrConflict
	}
	old, err := readOperation(m.ctx, m.tx, m.owner.ID, op.ID)
	if err != nil {
		return err
	}
	if old.Status != "pending" || old.Epoch != op.Epoch || old.Kind != op.Kind || old.RequestDigest != op.RequestDigest || old.CreatedAt != op.CreatedAt {
		return ErrConflict
	}
	data, err := encode(op, MaxRecordBytes)
	if err != nil {
		return err
	}
	if op.Status != "pending" {
		m.storageControl = true
	}
	_, err = m.tx.ExecContext(m.ctx, `UPDATE learning_operations SET status=?,record=? WHERE owner_id=? AND id=?`, op.Status, data, m.owner.ID, op.ID)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Mutation) InsertReview(r Review) error {
	m.storageAdmission = true
	if r.Epoch != m.owner.Epoch || r.Validate() != nil {
		return ErrInvalid
	}
	data, err := encode(r, MaxResponseBytes)
	if err != nil {
		return err
	}
	_, err = m.tx.ExecContext(m.ctx, `INSERT INTO learning_reviews(owner_id,id,epoch,record,created_at_ms) VALUES(?,?,?,?,?)`,
		m.owner.ID, r.ID, r.Epoch, data, r.CreatedAt)
	if err != nil {
		return ErrConflict
	}
	return nil
}

func (m *Mutation) Credit(e Evidence) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}
	if e.AttemptDigest != "" {
		review, err := m.Review(e.ReviewID)
		if err != nil || review.Decision == "" {
			return false, ErrUnavailable
		}
		var existing int
		// The writer transaction makes this secondary identity atomic. Retries
		// with unchanged (including whitespace-only) input cannot add credit or
		// upgrade old support. Legacy receipts use their exact input digest.
		if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*) FROM learning_evidence e
 LEFT JOIN learning_reviews r ON r.owner_id=e.owner_id AND r.id=json_extract(e.record,'$.review_id')
 WHERE e.owner_id=? AND json_extract(e.record,'$.competency')=? AND
 (json_extract(e.record,'$.attempt_digest')=? OR (COALESCE(json_extract(e.record,'$.attempt_digest'),'')='' AND
 json_extract(r.record,'$.result.studio_id')=? AND json_extract(r.record,'$.result.incarnation')=? AND
 json_extract(r.record,'$.result.ticket_id')=? AND json_extract(r.record,'$.input_digest')=?))`,
			m.owner.ID, e.Competency, e.AttemptDigest, review.Result.WorkspaceID, review.Result.Incarnation, review.Result.TicketID, review.InputDigest).Scan(&existing); err != nil {
			return false, ErrUnavailable
		}
		if existing > 0 {
			return false, nil
		}
	}
	data, err := encode(e, MaxResponseBytes)
	if err != nil {
		return false, err
	}
	result, err := m.tx.ExecContext(m.ctx, `INSERT INTO learning_evidence(owner_id,dedup_key,record,created_at_ms) VALUES(?,?,?,?) ON CONFLICT(owner_id,dedup_key) DO NOTHING`,
		m.owner.ID, e.Key, data, e.CreatedAt)
	if err != nil {
		return false, ErrUnavailable
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, ErrUnavailable
	}
	return n == 1, nil
}
