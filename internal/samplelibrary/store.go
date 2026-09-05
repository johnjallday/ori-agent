// Package samplelibrary owns the bounded, Home-scoped Sample Library catalog.
// It intentionally stores catalog state outside workspace.json and never stores
// source absolute paths; those remain in capability-owned Directory References.
package samplelibrary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

const SchemaVersion = 1

var (
	ErrNotFound            = errors.New("sample library state not found")
	ErrRevisionConflict    = errors.New("sample library revision changed")
	ErrOperationInProgress = errors.New("sample library operation in progress")
	ErrIdempotencyConflict = errors.New("sample library idempotency conflict")
)

type State struct {
	HomeWorkspaceID string    `json:"home_workspace_id"`
	SchemaVersion   int       `json:"schema_version"`
	Lifecycle       string    `json:"lifecycle"`
	CatalogRevision int64     `json:"catalog_revision"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Root struct {
	ID                   string    `json:"id"`
	HomeWorkspaceID      string    `json:"home_workspace_id"`
	DirectoryReferenceID string    `json:"directory_reference_id"`
	DirectoryFingerprint string    `json:"-"`
	State                string    `json:"state"`
	Revision             int64     `json:"revision"`
	Generation           int64     `json:"generation"`
	Completeness         string    `json:"completeness"`
	HashEnabled          bool      `json:"hash_enabled"`
	TagsEnabled          bool      `json:"tags_enabled"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type Entry struct {
	ID              string       `json:"id"`
	HomeWorkspaceID string       `json:"home_workspace_id"`
	RootID          string       `json:"root_id"`
	Generation      int64        `json:"generation"`
	RelativeLocator string       `json:"relative_locator"`
	Filename        string       `json:"filename"`
	Extension       string       `json:"extension"`
	SizeBytes       int64        `json:"size_bytes"`
	ModifiedAt      time.Time    `json:"modified_at"`
	CreatedAt       *time.Time   `json:"created_at,omitempty"`
	SHA256          string       `json:"sha256,omitempty"`
	Content         ContentFacts `json:"content,omitempty"`
}

type Collection struct {
	ID              string    `json:"id"`
	HomeWorkspaceID string    `json:"home_workspace_id"`
	Name            string    `json:"name"`
	Note            string    `json:"note,omitempty"`
	Revision        int64     `json:"revision"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type Annotation struct {
	EntryID     string    `json:"entry_id"`
	Revision    int64     `json:"revision"`
	UserTags    []string  `json:"user_tags"`
	PackNote    string    `json:"pack_note,omitempty"`
	SourceNote  string    `json:"source_note,omitempty"`
	LicenseNote string    `json:"license_note,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ScanReceipt struct {
	OperationID     string     `json:"operation_id"`
	HomeWorkspaceID string     `json:"home_workspace_id"`
	RootID          string     `json:"root_id"`
	Status          string     `json:"status"`
	InputDigest     string     `json:"-"`
	ReasonCode      string     `json:"reason_code,omitempty"`
	Visited         int        `json:"visited"`
	Indexed         int        `json:"indexed"`
	Skipped         int        `json:"skipped"`
	Errors          int        `json:"errors"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

type RootReviewRecord struct {
	Token, HomeWorkspaceID, SelectionDigest, DirectoryFingerprint, InputDigest, DisclosureDigest string
	CatalogRevision, RootRevision                                                                int64
	CreatedAt, ExpiresAt                                                                         time.Time
	ConsumedAt                                                                                   *time.Time
	ConsumedBy                                                                                   string
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) SaveRootReview(ctx context.Context, r RootReviewRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sample_library_review_receipt(token,home_workspace_id,action_kind,selection_digest,directory_fingerprint,input_digest,disclosure_digest,catalog_revision,root_revision,created_at,expires_at) VALUES(?,?,'connect_root',?,?,?,?,?,0,?,?)`, r.Token, r.HomeWorkspaceID, r.SelectionDigest, r.DirectoryFingerprint, r.InputDigest, r.DisclosureDigest, r.CatalogRevision, r.CreatedAt, r.ExpiresAt)
	return err
}

func (s *Store) SaveRevokeReview(ctx context.Context, r RootReviewRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sample_library_review_receipt(token,home_workspace_id,action_kind,directory_fingerprint,input_digest,disclosure_digest,catalog_revision,root_revision,created_at,expires_at) VALUES(?,?,'revoke',?,?,?,?,?,?,?)`, r.Token, r.HomeWorkspaceID, r.DirectoryFingerprint, r.InputDigest, r.DisclosureDigest, r.CatalogRevision, r.RootRevision, r.CreatedAt, r.ExpiresAt)
	return err
}

func (s *Store) SaveCurationReview(ctx context.Context, kind string, r RootReviewRecord) error {
	if kind != "collection" && kind != "annotation" {
		return ErrRevisionConflict
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sample_library_review_receipt(token,home_workspace_id,action_kind,input_digest,disclosure_digest,catalog_revision,root_revision,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`, r.Token, r.HomeWorkspaceID, kind, r.InputDigest, r.DisclosureDigest, r.CatalogRevision, r.RootRevision, r.CreatedAt, r.ExpiresAt)
	return err
}

func (s *Store) SaveAnalysisReview(ctx context.Context, r RootReviewRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sample_library_review_receipt(token,home_workspace_id,action_kind,directory_fingerprint,input_digest,disclosure_digest,catalog_revision,root_revision,created_at,expires_at) VALUES(?,?,'analysis',?,?,?,?,?,?,?)`, r.Token, r.HomeWorkspaceID, r.DirectoryFingerprint, r.InputDigest, r.DisclosureDigest, r.CatalogRevision, r.RootRevision, r.CreatedAt, r.ExpiresAt)
	return err
}

func (s *Store) Review(ctx context.Context, token, kind string) (RootReviewRecord, error) {
	var r RootReviewRecord
	err := s.db.QueryRowContext(ctx, `SELECT token,home_workspace_id,selection_digest,directory_fingerprint,input_digest,disclosure_digest,catalog_revision,root_revision,created_at,expires_at,consumed_at,consumed_by_idempotency_key FROM sample_library_review_receipt WHERE token=? AND action_kind=?`, token, kind).Scan(&r.Token, &r.HomeWorkspaceID, &r.SelectionDigest, &r.DirectoryFingerprint, &r.InputDigest, &r.DisclosureDigest, &r.CatalogRevision, &r.RootRevision, &r.CreatedAt, &r.ExpiresAt, &r.ConsumedAt, &r.ConsumedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return RootReviewRecord{}, ErrNotFound
	}
	return r, err
}

func (s *Store) RootReview(ctx context.Context, token string) (RootReviewRecord, error) {
	return s.Review(ctx, token, "connect_root")
}

func (s *Store) ConnectedRootByKey(ctx context.Context, homeID, key, inputDigest string) (Root, bool, error) {
	var rootID, digest string
	err := s.db.QueryRowContext(ctx, `SELECT root_id,input_digest FROM sample_library_operation_receipt WHERE home_workspace_id=? AND operation_kind='connect_root' AND idempotency_key=? AND status='succeeded'`, homeID, key).Scan(&rootID, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return Root{}, false, nil
	}
	if err != nil {
		return Root{}, false, err
	}
	if digest != inputDigest {
		return Root{}, false, ErrRevisionConflict
	}
	root, err := s.Root(ctx, homeID, rootID)
	return root, true, err
}

func (s *Store) Ensure(ctx context.Context, homeID string) (State, error) {
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sample_library_state(home_workspace_id,schema_version,catalog_revision,updated_at) VALUES(?,1,0,?) ON CONFLICT(home_workspace_id) DO NOTHING`, homeID, now); err != nil {
		return State{}, err
	}
	return s.Get(ctx, homeID)
}

func (s *Store) Get(ctx context.Context, homeID string) (State, error) {
	var v State
	err := s.db.QueryRowContext(ctx, `SELECT home_workspace_id,schema_version,lifecycle,catalog_revision,updated_at FROM sample_library_state WHERE home_workspace_id=?`, homeID).Scan(&v.HomeWorkspaceID, &v.SchemaVersion, &v.Lifecycle, &v.CatalogRevision, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, ErrNotFound
	}
	return v, err
}

func (s *Store) Roots(ctx context.Context, homeID string, activeOnly bool) ([]Root, error) {
	q := `SELECT root_id,home_workspace_id,directory_reference_id,directory_fingerprint,state,root_revision,generation,completeness,hash_enabled,tags_enabled,created_at,updated_at FROM sample_library_root WHERE home_workspace_id=?`
	if activeOnly {
		q += ` AND state='active'`
	}
	q += ` ORDER BY root_id`
	rows, err := s.db.QueryContext(ctx, q, homeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Root
	for rows.Next() {
		var r Root
		if err := rows.Scan(&r.ID, &r.HomeWorkspaceID, &r.DirectoryReferenceID, &r.DirectoryFingerprint, &r.State, &r.Revision, &r.Generation, &r.Completeness, &r.HashEnabled, &r.TagsEnabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Root(ctx context.Context, homeID, rootID string) (Root, error) {
	var r Root
	err := s.db.QueryRowContext(ctx, `SELECT root_id,home_workspace_id,directory_reference_id,directory_fingerprint,state,root_revision,generation,completeness,hash_enabled,tags_enabled,created_at,updated_at FROM sample_library_root WHERE home_workspace_id=? AND root_id=?`, homeID, rootID).Scan(&r.ID, &r.HomeWorkspaceID, &r.DirectoryReferenceID, &r.DirectoryFingerprint, &r.State, &r.Revision, &r.Generation, &r.Completeness, &r.HashEnabled, &r.TagsEnabled, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Root{}, ErrNotFound
	}
	return r, err
}

func (s *Store) AddRoot(ctx context.Context, root Root, reviewToken, idempotencyKey, inputDigest string, expectedCatalog int64, now time.Time) (State, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_state SET catalog_revision=catalog_revision+1,updated_at=? WHERE home_workspace_id=? AND catalog_revision=?`, root.UpdatedAt, root.HomeWorkspaceID, expectedCatalog)
	if err != nil {
		return State{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return State{}, ErrRevisionConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sample_library_root(root_id,home_workspace_id,directory_reference_id,directory_fingerprint,state,root_revision,generation,completeness,hash_enabled,tags_enabled,created_at,updated_at) VALUES(?,?,?,?,?,1,0,'not_indexed',0,0,?,?)`, root.ID, root.HomeWorkspaceID, root.DirectoryReferenceID, root.DirectoryFingerprint, "active", root.CreatedAt, root.UpdatedAt)
	if err != nil {
		return State{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sample_library_review_receipt SET consumed_at=?,consumed_by_idempotency_key=? WHERE token=? AND home_workspace_id=? AND consumed_at IS NULL AND expires_at>?`, now, idempotencyKey, reviewToken, root.HomeWorkspaceID, now)
	if err != nil {
		return State{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return State{}, ErrRevisionConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,created_at,completed_at) VALUES(?,?,?,'connect_root',?,?,'succeeded',?,?)`, uuid.NewString(), root.HomeWorkspaceID, root.ID, idempotencyKey, inputDigest, now, now)
	if err != nil {
		return State{}, err
	}
	if err = tx.Commit(); err != nil {
		return State{}, err
	}
	return s.Get(ctx, root.HomeWorkspaceID)
}

func (s *Store) SetAnalysis(ctx context.Context, homeID, rootID, reviewToken, key, inputDigest string, hashEnabled, tagsEnabled bool, expectedCatalog, expectedRoot int64, now time.Time) (State, Root, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, Root{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_state SET catalog_revision=catalog_revision+1,updated_at=? WHERE home_workspace_id=? AND catalog_revision=?`, now, homeID, expectedCatalog)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	res, err = tx.ExecContext(ctx, `UPDATE sample_library_root SET hash_enabled=?,tags_enabled=?,root_revision=root_revision+1,updated_at=? WHERE home_workspace_id=? AND root_id=? AND root_revision=? AND state='active'`, hashEnabled, tagsEnabled, now, homeID, rootID, expectedRoot)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	if !hashEnabled {
		if _, err = tx.ExecContext(ctx, `UPDATE sample_library_content_fact SET sha256='' WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE root_id=?)`, rootID); err != nil {
			return State{}, Root{}, err
		}
	}
	if !tagsEnabled {
		if _, err = tx.ExecContext(ctx, `UPDATE sample_library_content_fact SET title='',artist='',album='',album_artist='',genre='',comment='',year='',track='',disc='' WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE root_id=?)`, rootID); err != nil {
			return State{}, Root{}, err
		}
	}
	res, err = tx.ExecContext(ctx, `UPDATE sample_library_review_receipt SET consumed_at=?,consumed_by_idempotency_key=? WHERE token=? AND home_workspace_id=? AND action_kind='analysis' AND consumed_at IS NULL AND expires_at>?`, now, key, reviewToken, homeID, now)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,created_at,completed_at) VALUES(?,?,?,'analysis',?,?,'succeeded',?,?)`, uuid.NewString(), homeID, rootID, key, inputDigest, now, now)
	if err != nil {
		return State{}, Root{}, err
	}
	if err = tx.Commit(); err != nil {
		return State{}, Root{}, err
	}
	state, err := s.Get(ctx, homeID)
	if err != nil {
		return State{}, Root{}, err
	}
	root, err := s.Root(ctx, homeID, rootID)
	return state, root, err
}

func (s *Store) AnalysisByKey(ctx context.Context, homeID, rootID, key, inputDigest string) (bool, error) {
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT input_digest FROM sample_library_operation_receipt WHERE home_workspace_id=? AND root_id=? AND operation_kind='analysis' AND idempotency_key=? AND status='succeeded'`, homeID, rootID, key).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digest != inputDigest {
		return false, ErrRevisionConflict
	}
	return true, nil
}

func (s *Store) RevokeRoot(ctx context.Context, homeID, rootID, reviewToken, key, inputDigest string, expectedCatalog, expectedRoot int64, now time.Time) (State, Root, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, Root{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_state SET catalog_revision=catalog_revision+1,updated_at=? WHERE home_workspace_id=? AND catalog_revision=?`, now, homeID, expectedCatalog)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	res, err = tx.ExecContext(ctx, `UPDATE sample_library_root SET state='revoked',root_revision=root_revision+1,hash_enabled=0,tags_enabled=0,completeness='not_indexed',updated_at=? WHERE home_workspace_id=? AND root_id=? AND root_revision=? AND state='active'`, now, homeID, rootID, expectedRoot)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_content_fact WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE root_id=?)`, rootID); err != nil {
		return State{}, Root{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_annotation WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE root_id=?)`, rootID); err != nil {
		return State{}, Root{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sample_library_entry SET relative_locator=entry_id,filename='Unavailable',extension='.gone',size_bytes=0,modified_at=? WHERE root_id=?`, now, rootID); err != nil {
		return State{}, Root{}, err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sample_library_review_receipt SET consumed_at=?,consumed_by_idempotency_key=? WHERE token=? AND home_workspace_id=? AND action_kind='revoke' AND consumed_at IS NULL AND expires_at>?`, now, key, reviewToken, homeID, now)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,reason_code,created_at,completed_at) VALUES(?,?,?,'revoke',?,?,'succeeded','user_revoked',?,?)`, uuid.NewString(), homeID, rootID, key, inputDigest, now, now)
	if err != nil {
		return State{}, Root{}, err
	}
	if err = tx.Commit(); err != nil {
		return State{}, Root{}, err
	}
	state, err := s.Get(ctx, homeID)
	if err != nil {
		return State{}, Root{}, err
	}
	root, err := s.Root(ctx, homeID, rootID)
	return state, root, err
}
func (s *Store) RevokeByKey(ctx context.Context, homeID, rootID, key, inputDigest string) (bool, error) {
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT input_digest FROM sample_library_operation_receipt WHERE home_workspace_id=? AND root_id=? AND operation_kind='revoke' AND idempotency_key=? AND status='succeeded'`, homeID, rootID, key).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digest != inputDigest {
		return false, ErrRevisionConflict
	}
	return true, nil
}

func (s *Store) ClaimScan(ctx context.Context, receipt ScanReceipt, expires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE sample_library_operation_receipt SET status='failed',reason_code='sample_operation_failed',completed_at=? WHERE operation_kind='index' AND status='claimed' AND expires_at<=?`, receipt.CreatedAt, receipt.CreatedAt); err != nil {
		return err
	}
	var digest, status string
	queryErr := tx.QueryRowContext(ctx, `SELECT input_digest,status FROM sample_library_operation_receipt WHERE home_workspace_id=? AND operation_kind='index' AND idempotency_key=?`, receipt.HomeWorkspaceID, receipt.OperationID).Scan(&digest, &status)
	if queryErr == nil {
		if digest != receipt.InputDigest {
			return ErrRevisionConflict
		}
		return ErrOperationInProgress
	}
	if !errors.Is(queryErr, sql.ErrNoRows) {
		return queryErr
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,created_at,expires_at) VALUES(?,?,?,'index',?,?,'claimed',?,?)`, receipt.OperationID, receipt.HomeWorkspaceID, receipt.RootID, receipt.OperationID, receipt.InputDigest, receipt.CreatedAt, expires)
	if err != nil {
		return ErrOperationInProgress
	}
	return tx.Commit()
}
func (s *Store) FailScan(ctx context.Context, receipt ScanReceipt) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sample_library_operation_receipt SET status='failed',reason_code=?,visited_count=?,indexed_count=?,skipped_count=?,error_count=?,completed_at=? WHERE operation_id=? AND home_workspace_id=? AND root_id=? AND status='claimed'`, receipt.ReasonCode, receipt.Visited, receipt.Indexed, receipt.Skipped, receipt.Errors, receipt.CompletedAt, receipt.OperationID, receipt.HomeWorkspaceID, receipt.RootID)
	return err
}

func (s *Store) ReplaceGeneration(ctx context.Context, root Root, expectedCatalog, expectedRoot int64, entries []Entry, receipt ScanReceipt) (State, Root, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, Root{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_state SET catalog_revision=catalog_revision+1,updated_at=? WHERE home_workspace_id=? AND catalog_revision=?`, root.UpdatedAt, root.HomeWorkspaceID, expectedCatalog)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	res, err = tx.ExecContext(ctx, `UPDATE sample_library_root SET root_revision=root_revision+1,generation=?,completeness=?,updated_at=? WHERE root_id=? AND home_workspace_id=? AND root_revision=? AND state='active'`, root.Generation, root.Completeness, root.UpdatedAt, root.ID, root.HomeWorkspaceID, expectedRoot)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_content_fact WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE root_id=?)`, root.ID); err != nil {
		return State{}, Root{}, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sample_library_entry(entry_id,home_workspace_id,root_id,generation,relative_locator,filename,extension,size_bytes,modified_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entry_id) DO UPDATE SET generation=excluded.generation,relative_locator=excluded.relative_locator,filename=excluded.filename,extension=excluded.extension,size_bytes=excluded.size_bytes,modified_at=excluded.modified_at,created_at=excluded.created_at`)
	if err != nil {
		return State{}, Root{}, err
	}
	defer func() { _ = stmt.Close() }()
	for _, e := range entries {
		if _, err = stmt.ExecContext(ctx, e.ID, e.HomeWorkspaceID, e.RootID, e.Generation, e.RelativeLocator, e.Filename, e.Extension, e.SizeBytes, e.ModifiedAt, e.CreatedAt); err != nil {
			return State{}, Root{}, err
		}
		if e.SHA256 != "" || e.Content != (ContentFacts{}) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_content_fact(entry_id,sha256,title,artist,album,album_artist,genre,comment,year,track,disc) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, e.ID, e.SHA256, e.Content.Title, e.Content.Artist, e.Content.Album, e.Content.AlbumArtist, e.Content.Genre, e.Content.Comment, e.Content.Year, e.Content.Track, e.Content.Disc); err != nil {
				return State{}, Root{}, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_annotation WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE root_id=? AND generation!=?)`, root.ID, root.Generation); err != nil {
		return State{}, Root{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_entry WHERE root_id=? AND generation!=? AND entry_id NOT IN (SELECT entry_id FROM sample_library_collection_member)`, root.ID, root.Generation); err != nil {
		return State{}, Root{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sample_library_entry SET relative_locator=entry_id,filename='Unavailable',extension='.gone',size_bytes=0,modified_at=? WHERE root_id=? AND generation!=?`, root.UpdatedAt, root.ID, root.Generation); err != nil {
		return State{}, Root{}, err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sample_library_operation_receipt SET status=?,reason_code=?,visited_count=?,indexed_count=?,skipped_count=?,error_count=?,completed_at=?,expires_at=NULL WHERE operation_id=? AND home_workspace_id=? AND root_id=? AND operation_kind='index' AND status='claimed'`, receipt.Status, receipt.ReasonCode, receipt.Visited, receipt.Indexed, receipt.Skipped, receipt.Errors, receipt.CompletedAt, receipt.OperationID, receipt.HomeWorkspaceID, receipt.RootID)
	if err != nil {
		return State{}, Root{}, err
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return State{}, Root{}, ErrRevisionConflict
	}
	if err = tx.Commit(); err != nil {
		return State{}, Root{}, err
	}
	state, err := s.Get(ctx, root.HomeWorkspaceID)
	if err != nil {
		return State{}, Root{}, err
	}
	saved, err := s.Root(ctx, root.HomeWorkspaceID, root.ID)
	return state, saved, err
}

func (s *Store) Disable(ctx context.Context, homeID string, now time.Time) ([]Root, error) {
	roots, err := s.Roots(ctx, homeID, true)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE sample_library_state SET lifecycle='disabled',catalog_revision=catalog_revision+1,updated_at=? WHERE home_workspace_id=?`, now, homeID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sample_library_root SET state='revoked',root_revision=root_revision+1,hash_enabled=0,tags_enabled=0,completeness='not_indexed',updated_at=? WHERE home_workspace_id=? AND state='active'`, now, homeID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_content_fact WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE home_workspace_id=?)`, homeID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_annotation WHERE entry_id IN (SELECT entry_id FROM sample_library_entry WHERE home_workspace_id=?)`, homeID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_collection_member WHERE collection_id IN (SELECT collection_id FROM sample_library_collection WHERE home_workspace_id=?)`, homeID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sample_library_collection WHERE home_workspace_id=?`, homeID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sample_library_entry SET relative_locator=entry_id,filename='Unavailable',extension='.gone',size_bytes=0,modified_at=? WHERE home_workspace_id=?`, now, homeID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return roots, nil
}

func (s *Store) ScanByKey(ctx context.Context, homeID, rootID, key, inputDigest string) (ScanReceipt, bool, error) {
	var r ScanReceipt
	err := s.db.QueryRowContext(ctx, `SELECT operation_id,home_workspace_id,root_id,input_digest,status,reason_code,visited_count,indexed_count,skipped_count,error_count,created_at,completed_at FROM sample_library_operation_receipt WHERE home_workspace_id=? AND root_id=? AND operation_kind='index' AND idempotency_key=? AND status!='claimed'`, homeID, rootID, key).Scan(&r.OperationID, &r.HomeWorkspaceID, &r.RootID, &r.InputDigest, &r.Status, &r.ReasonCode, &r.Visited, &r.Indexed, &r.Skipped, &r.Errors, &r.CreatedAt, &r.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanReceipt{}, false, nil
	}
	if err != nil {
		return ScanReceipt{}, false, err
	}
	if r.InputDigest != inputDigest {
		return ScanReceipt{}, false, ErrRevisionConflict
	}
	return r, true, nil
}

func (s *Store) Collections(ctx context.Context, homeID string) ([]Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT collection_id,home_workspace_id,name,note,revision,created_at,updated_at FROM sample_library_collection WHERE home_workspace_id=? ORDER BY lower(name),collection_id`, homeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err = rows.Scan(&c.ID, &c.HomeWorkspaceID, &c.Name, &c.Note, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) CreateCollection(ctx context.Context, c Collection, key, inputDigest string, expectedCatalog int64) (State, Collection, error) {
	var existingID, digest string
	err := s.db.QueryRowContext(ctx, `SELECT root_id,input_digest FROM sample_library_operation_receipt WHERE home_workspace_id=? AND operation_kind='collection' AND idempotency_key=?`, c.HomeWorkspaceID, key).Scan(&existingID, &digest)
	if err == nil {
		if digest != inputDigest {
			return State{}, Collection{}, ErrRevisionConflict
		}
		collections, e := s.Collections(ctx, c.HomeWorkspaceID)
		if e != nil {
			return State{}, Collection{}, e
		}
		for _, item := range collections {
			if item.ID == existingID {
				state, e := s.Get(ctx, c.HomeWorkspaceID)
				return state, item, e
			}
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return State{}, Collection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, Collection{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_state SET catalog_revision=catalog_revision+1,updated_at=? WHERE home_workspace_id=? AND catalog_revision=?`, c.UpdatedAt, c.HomeWorkspaceID, expectedCatalog)
	if err != nil {
		return State{}, Collection{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return State{}, Collection{}, ErrRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_collection(collection_id,home_workspace_id,name,note,revision,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`, c.ID, c.HomeWorkspaceID, c.Name, c.Note, c.CreatedAt, c.UpdatedAt); err != nil {
		return State{}, Collection{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,created_at,completed_at) VALUES(?,?,?,'collection',?,?,'succeeded',?,?)`, uuid.NewString(), c.HomeWorkspaceID, c.ID, key, inputDigest, c.CreatedAt, c.UpdatedAt); err != nil {
		return State{}, Collection{}, err
	}
	if err = tx.Commit(); err != nil {
		return State{}, Collection{}, err
	}
	state, err := s.Get(ctx, c.HomeWorkspaceID)
	return state, c, err
}
func (s *Store) AddCollectionMember(ctx context.Context, homeID, collectionID, entryID, key, inputDigest string, expectedRevision int64, now time.Time) (Collection, error) {
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT input_digest FROM sample_library_operation_receipt WHERE home_workspace_id=? AND operation_kind='collection' AND idempotency_key=?`, homeID, key).Scan(&digest)
	if err == nil {
		if digest != inputDigest {
			return Collection{}, ErrRevisionConflict
		}
		items, e := s.Collections(ctx, homeID)
		if e != nil {
			return Collection{}, e
		}
		for _, item := range items {
			if item.ID == collectionID {
				return item, nil
			}
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Collection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var active string
	if err = tx.QueryRowContext(ctx, `SELECT r.state FROM sample_library_entry e JOIN sample_library_root r ON r.root_id=e.root_id WHERE e.home_workspace_id=? AND e.entry_id=? AND e.generation=r.generation`, homeID, entryID).Scan(&active); err != nil || active != "active" {
		return Collection{}, ErrNotFound
	}
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_collection SET revision=revision+1,updated_at=? WHERE home_workspace_id=? AND collection_id=? AND revision=?`, now, homeID, collectionID, expectedRevision)
	if err != nil {
		return Collection{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Collection{}, ErrRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_collection_member(collection_id,entry_id,added_at) VALUES(?,?,?)`, collectionID, entryID, now); err != nil {
		return Collection{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,created_at,completed_at) VALUES(?,?,?,'collection',?,?,'succeeded',?,?)`, uuid.NewString(), homeID, collectionID, key, inputDigest, now, now); err != nil {
		return Collection{}, err
	}
	if err = tx.Commit(); err != nil {
		return Collection{}, err
	}
	items, err := s.Collections(ctx, homeID)
	for _, item := range items {
		if item.ID == collectionID {
			return item, nil
		}
	}
	return Collection{}, err
}

func (s *Store) SetAnnotation(ctx context.Context, homeID string, a Annotation, key, inputDigest string, expectedCatalog, expectedRevision int64) (State, Annotation, error) {
	var priorDigest string
	if priorErr := s.db.QueryRowContext(ctx, `SELECT input_digest FROM sample_library_operation_receipt WHERE home_workspace_id=? AND operation_kind='annotation' AND idempotency_key=?`, homeID, key).Scan(&priorDigest); priorErr == nil {
		if priorDigest != inputDigest {
			return State{}, Annotation{}, ErrRevisionConflict
		}
		var saved Annotation
		var tagsJSON string
		if err := s.db.QueryRowContext(ctx, `SELECT entry_id,revision,user_tags_json,pack_note,source_note,license_note,updated_at FROM sample_library_annotation WHERE entry_id=?`, a.EntryID).Scan(&saved.EntryID, &saved.Revision, &tagsJSON, &saved.PackNote, &saved.SourceNote, &saved.LicenseNote, &saved.UpdatedAt); err != nil {
			return State{}, Annotation{}, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &saved.UserTags); err != nil {
			return State{}, Annotation{}, err
		}
		state, err := s.Get(ctx, homeID)
		return state, saved, err
	} else if !errors.Is(priorErr, sql.ErrNoRows) {
		return State{}, Annotation{}, priorErr
	}
	tags, err := json.Marshal(a.UserTags)
	if err != nil {
		return State{}, Annotation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, Annotation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var rootState string
	if err = tx.QueryRowContext(ctx, `SELECT r.state FROM sample_library_entry e JOIN sample_library_root r ON r.root_id=e.root_id WHERE e.home_workspace_id=? AND e.entry_id=? AND e.generation=r.generation`, homeID, a.EntryID).Scan(&rootState); err != nil || rootState != "active" {
		return State{}, Annotation{}, ErrNotFound
	}
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_state SET catalog_revision=catalog_revision+1,updated_at=? WHERE home_workspace_id=? AND catalog_revision=?`, a.UpdatedAt, homeID, expectedCatalog)
	if err != nil {
		return State{}, Annotation{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return State{}, Annotation{}, ErrRevisionConflict
	}
	if expectedRevision == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO sample_library_annotation(entry_id,revision,user_tags_json,pack_note,source_note,license_note,updated_at) VALUES(?,1,?,?,?,?,?)`, a.EntryID, string(tags), a.PackNote, a.SourceNote, a.LicenseNote, a.UpdatedAt)
		a.Revision = 1
	} else {
		res, err = tx.ExecContext(ctx, `UPDATE sample_library_annotation SET revision=revision+1,user_tags_json=?,pack_note=?,source_note=?,license_note=?,updated_at=? WHERE entry_id=? AND revision=?`, string(tags), a.PackNote, a.SourceNote, a.LicenseNote, a.UpdatedAt, a.EntryID, expectedRevision)
		if err == nil {
			n, _ = res.RowsAffected()
			if n != 1 {
				return State{}, Annotation{}, ErrRevisionConflict
			}
			a.Revision = expectedRevision + 1
		}
	}
	if err != nil {
		return State{}, Annotation{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,root_id,operation_kind,idempotency_key,input_digest,status,created_at,completed_at) VALUES(?,?,?,'annotation',?,?,'succeeded',?,?)`, uuid.NewString(), homeID, a.EntryID, key, inputDigest, a.UpdatedAt, a.UpdatedAt); err != nil {
		return State{}, Annotation{}, err
	}
	if err = tx.Commit(); err != nil {
		return State{}, Annotation{}, err
	}
	state, err := s.Get(ctx, homeID)
	return state, a, err
}

func (s *Store) SearchEntries(ctx context.Context, homeID, query, extension, sortKey, direction string, rootIDs []string, limit int) ([]Entry, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(rootIDs)), ",")
	pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(query)) + "%"
	args := []any{homeID}
	for _, id := range rootIDs {
		args = append(args, id)
	}
	args = append(args, extension, extension, query, pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern, limit)
	order := "lower(e.filename),e.entry_id"
	switch sortKey {
	case "modified":
		order = "e.modified_at,e.entry_id"
	case "size":
		order = "e.size_bytes,e.entry_id"
	}
	if direction == "desc" {
		order = strings.ReplaceAll(order, ",e.entry_id", " DESC,e.entry_id DESC")
	}
	sqlQuery := `SELECT DISTINCT e.entry_id,e.home_workspace_id,e.root_id,e.generation,e.relative_locator,e.filename,e.extension,e.size_bytes,e.modified_at,e.created_at,COALESCE(f.sha256,''),COALESCE(f.title,''),COALESCE(f.artist,''),COALESCE(f.album,''),COALESCE(f.album_artist,''),COALESCE(f.genre,''),COALESCE(f.comment,''),COALESCE(f.year,''),COALESCE(f.track,''),COALESCE(f.disc,'') FROM sample_library_entry e JOIN sample_library_root r ON r.root_id=e.root_id LEFT JOIN sample_library_content_fact f ON f.entry_id=e.entry_id LEFT JOIN sample_library_annotation a ON a.entry_id=e.entry_id WHERE e.home_workspace_id=? AND r.state='active' AND e.generation=r.generation AND e.root_id IN (` + placeholders + `) AND (?='' OR e.extension=?) AND (?='' OR lower(e.filename) LIKE ? ESCAPE '\' OR lower(e.relative_locator) LIKE ? ESCAPE '\' OR lower(e.extension) LIKE ? ESCAPE '\' OR lower(COALESCE(f.title,'')) LIKE ? ESCAPE '\' OR lower(COALESCE(f.artist,'')) LIKE ? ESCAPE '\' OR lower(COALESCE(a.user_tags_json,'')) LIKE ? ESCAPE '\' OR lower(COALESCE(a.pack_note,'')) LIKE ? ESCAPE '\' OR lower(COALESCE(a.source_note,'')) LIKE ? ESCAPE '\' OR lower(COALESCE(a.license_note,'')) LIKE ? ESCAPE '\') ORDER BY ` + order + ` LIMIT ?`
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.HomeWorkspaceID, &e.RootID, &e.Generation, &e.RelativeLocator, &e.Filename, &e.Extension, &e.SizeBytes, &e.ModifiedAt, &e.CreatedAt, &e.SHA256, &e.Content.Title, &e.Content.Artist, &e.Content.Album, &e.Content.AlbumArtist, &e.Content.Genre, &e.Content.Comment, &e.Content.Year, &e.Content.Track, &e.Content.Disc); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type ChildCopy struct {
	ID                     string    `json:"id"`
	ChildWorkspaceID       string    `json:"child_workspace_id"`
	AssistantProjectLinkID string    `json:"assistant_project_link_id"`
	SourceRootID           string    `json:"source_root_id"`
	SourceEntryID          string    `json:"source_entry_id"`
	DestinationLocator     string    `json:"destination_locator"`
	SizeBytes              int64     `json:"size_bytes"`
	SHA256                 string    `json:"sha256"`
	CopiedAt               time.Time `json:"copied_at"`
}

func (s *Store) ActiveEntry(ctx context.Context, homeID, entryID string) (Entry, error) {
	var e Entry
	err := s.db.QueryRowContext(ctx, `SELECT e.entry_id,e.home_workspace_id,e.root_id,e.generation,e.relative_locator,e.filename,e.extension,e.size_bytes,e.modified_at,e.created_at,COALESCE(f.sha256,''),COALESCE(f.title,''),COALESCE(f.artist,''),COALESCE(f.album,''),COALESCE(f.album_artist,''),COALESCE(f.genre,''),COALESCE(f.comment,''),COALESCE(f.year,''),COALESCE(f.track,''),COALESCE(f.disc,'') FROM sample_library_entry e JOIN sample_library_root r ON r.root_id=e.root_id LEFT JOIN sample_library_content_fact f ON f.entry_id=e.entry_id WHERE e.home_workspace_id=? AND e.entry_id=? AND r.state='active' AND e.generation=r.generation`, homeID, entryID).Scan(&e.ID, &e.HomeWorkspaceID, &e.RootID, &e.Generation, &e.RelativeLocator, &e.Filename, &e.Extension, &e.SizeBytes, &e.ModifiedAt, &e.CreatedAt, &e.SHA256, &e.Content.Title, &e.Content.Artist, &e.Content.Album, &e.Content.AlbumArtist, &e.Content.Genre, &e.Content.Comment, &e.Content.Year, &e.Content.Track, &e.Content.Disc)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	return e, err
}
func (s *Store) CreateCopyReview(ctx context.Context, review RootReviewRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sample_library_review_receipt(token,home_workspace_id,action_kind,selection_digest,input_digest,disclosure_digest,catalog_revision,created_at,expires_at) VALUES(?,?,'copy',?,?,?,?,?,?)`, review.Token, review.HomeWorkspaceID, review.SelectionDigest, review.InputDigest, review.DisclosureDigest, review.CatalogRevision, review.CreatedAt, review.ExpiresAt)
	return err
}
func (s *Store) CopyOperationByKey(ctx context.Context, homeID, key, input string) (string, error) {
	var digest, status string
	err := s.db.QueryRowContext(ctx, `SELECT input_digest,status FROM sample_library_operation_receipt WHERE home_workspace_id=? AND operation_kind='copy' AND idempotency_key=?`, homeID, key).Scan(&digest, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if digest != input {
		return "", ErrIdempotencyConflict
	}
	return status, nil
}
func (s *Store) BeginCopies(ctx context.Context, homeID, token, key, input string, copies []ChildCopy, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE sample_library_review_receipt SET consumed_at=?,consumed_by_idempotency_key=? WHERE token=? AND home_workspace_id=? AND action_kind='copy' AND consumed_at IS NULL`, now, key, token, homeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrRevisionConflict
	}
	for _, copy := range copies {
		if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_child_copy(copy_id,child_workspace_id,assistant_project_link_id,source_root_id,source_entry_id,destination_locator,size_bytes,sha256,copied_at) VALUES(?,?,?,?,?,?,?,?,?)`, copy.ID, copy.ChildWorkspaceID, copy.AssistantProjectLinkID, copy.SourceRootID, copy.SourceEntryID, copy.DestinationLocator, copy.SizeBytes, copy.SHA256, copy.CopiedAt); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sample_library_operation_receipt(operation_id,home_workspace_id,operation_kind,idempotency_key,input_digest,status,created_at) VALUES(?,?,'copy',?,?,'reconcile_required',?)`, uuid.NewString(), homeID, key, input, now); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CompleteCopies(ctx context.Context, homeID, key string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sample_library_operation_receipt SET status='succeeded',completed_at=? WHERE home_workspace_id=? AND operation_kind='copy' AND idempotency_key=? AND status='reconcile_required'`, now, homeID, key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrRevisionConflict
	}
	return nil
}
func (s *Store) CopiesByIDs(ctx context.Context, ids []string) ([]ChildCopy, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT copy_id,child_workspace_id,assistant_project_link_id,source_root_id,source_entry_id,destination_locator,size_bytes,sha256,copied_at FROM sample_library_child_copy WHERE copy_id IN (`+placeholders+`) ORDER BY copy_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ChildCopy
	for rows.Next() {
		var copy ChildCopy
		if err = rows.Scan(&copy.ID, &copy.ChildWorkspaceID, &copy.AssistantProjectLinkID, &copy.SourceRootID, &copy.SourceEntryID, &copy.DestinationLocator, &copy.SizeBytes, &copy.SHA256, &copy.CopiedAt); err != nil {
			return nil, err
		}
		out = append(out, copy)
	}
	return out, rows.Err()
}

type RemovalImpact struct{ Roots, Entries, Collections, CollectionMembers, DerivedFacts, Annotations int }

func (s *Store) RemovalImpact(ctx context.Context, homeID string) (RemovalImpact, error) {
	var result RemovalImpact
	queries := []struct {
		sql    string
		target *int
	}{{`SELECT count(*) FROM sample_library_root WHERE home_workspace_id=? AND state='active'`, &result.Roots}, {`SELECT count(*) FROM sample_library_entry WHERE home_workspace_id=?`, &result.Entries}, {`SELECT count(*) FROM sample_library_collection WHERE home_workspace_id=?`, &result.Collections}, {`SELECT count(*) FROM sample_library_collection_member m JOIN sample_library_collection c ON c.collection_id=m.collection_id WHERE c.home_workspace_id=?`, &result.CollectionMembers}, {`SELECT count(*) FROM sample_library_content_fact f JOIN sample_library_entry e ON e.entry_id=f.entry_id WHERE e.home_workspace_id=?`, &result.DerivedFacts}, {`SELECT count(*) FROM sample_library_annotation a JOIN sample_library_entry e ON e.entry_id=a.entry_id WHERE e.home_workspace_id=?`, &result.Annotations}}
	for _, query := range queries {
		if err := s.db.QueryRowContext(ctx, query.sql, homeID).Scan(query.target); err != nil {
			return RemovalImpact{}, err
		}
	}
	return result, nil
}

func (s *Store) EntryCount(ctx context.Context, homeID, rootID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sample_library_entry WHERE home_workspace_id=? AND root_id=?`, homeID, rootID).Scan(&count)
	return count, err
}

func (s *Store) Entries(ctx context.Context, homeID, rootID string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.entry_id,e.home_workspace_id,e.root_id,e.generation,e.relative_locator,e.filename,e.extension,e.size_bytes,e.modified_at,e.created_at,COALESCE(f.sha256,''),COALESCE(f.title,''),COALESCE(f.artist,''),COALESCE(f.album,''),COALESCE(f.album_artist,''),COALESCE(f.genre,''),COALESCE(f.comment,''),COALESCE(f.year,''),COALESCE(f.track,''),COALESCE(f.disc,'') FROM sample_library_entry e JOIN sample_library_root r ON r.root_id=e.root_id LEFT JOIN sample_library_content_fact f ON f.entry_id=e.entry_id WHERE e.home_workspace_id=? AND e.root_id=? AND (r.state!='active' OR e.generation=r.generation) ORDER BY e.relative_locator LIMIT ?`, homeID, rootID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.HomeWorkspaceID, &e.RootID, &e.Generation, &e.RelativeLocator, &e.Filename, &e.Extension, &e.SizeBytes, &e.ModifiedAt, &e.CreatedAt, &e.SHA256, &e.Content.Title, &e.Content.Artist, &e.Content.Album, &e.Content.AlbumArtist, &e.Content.Genre, &e.Content.Comment, &e.Content.Year, &e.Content.Track, &e.Content.Disc); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
