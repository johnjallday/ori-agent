package setupjourney

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserTemplateBindingMismatch = errors.New("user setup quest binding does not match saved execution identity")
	ErrUserTemplateRootClaimed     = errors.New("setup resource root is already claimed by another journey")

	userTemplateIDPattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	userTemplateAttachmentPattern = regexp.MustCompile(`^uqatt_[a-f0-9]{24}$`)
)

type UserTemplateBinding struct {
	UserID           string
	TemplateID       string
	AttachmentID     string
	QuestID          string
	DefinitionDigest string
	ExecutionDigest  string
	CreatedAt        time.Time
}

type UserTemplateRootClaim struct {
	RootKind string
	RootID   string
	UserTemplateBinding
}

// CreateOrGetUserTemplateRoot commits the permanent definition/execution
// binding and the first inert progress row in one SQLite transaction.
func (s *SQLiteStore) CreateOrGetUserTemplateRoot(ctx context.Context, spec RootSpec, binding UserTemplateBinding) (*Run, bool, error) {
	if err := s.configured(); err != nil {
		return nil, false, err
	}
	normalizedSpec, err := normalizeRootSpec(spec)
	if err != nil {
		return nil, false, err
	}
	normalizedBinding, err := normalizeUserTemplateBinding(binding)
	key := QuestKey{
		Source: QuestSourceUserTemplate, TemplateID: normalizedBinding.TemplateID,
		AttachmentID: normalizedBinding.AttachmentID, ID: normalizedBinding.QuestID,
	}
	if err != nil || normalizedSpec.OwnerUserID != normalizedBinding.UserID || normalizedSpec.JourneyID != normalizedBinding.QuestID ||
		normalizedSpec.RelationshipID != questRelationshipID(key) || normalizedSpec.SpecialistSlug != "user_template_quest" {
		return nil, false, ErrInvalid
	}
	now := s.now().UTC()
	run := &Run{
		ID: uuid.New().String(), Kind: RunKindRoot,
		OwnerUserID: normalizedSpec.OwnerUserID, RelationshipID: normalizedSpec.RelationshipID,
		SpecialistSlug: normalizedSpec.SpecialistSlug, JourneyID: normalizedSpec.JourneyID,
		DeclarationSchemaVersion: normalizedSpec.DeclarationSchemaVersion,
		DeclarationVersion:       normalizedSpec.DeclarationVersion, StateRevision: 1,
		Lifecycle: LifecycleNotStarted, CurrentStepID: normalizedSpec.StepStates[0].StepID,
		StepStates: normalizedSpec.StepStates, CreatedAt: now, UpdatedAt: now,
	}
	stepJSON, err := encodeStepStates(run.StepStates)
	if err != nil {
		return nil, false, err
	}
	var result *Run
	created := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, _, claimErr := claimUserTemplateBindingWith(ctx, tx, normalizedBinding, now); claimErr != nil {
			return claimErr
		}
		var createErr error
		result, created, createErr = createOrGetRootWith(ctx, tx, run, stepJSON)
		return createErr
	})
	if err != nil {
		return nil, false, err
	}
	return result.Clone(), created, nil
}

// GetUserTemplateBinding returns the immutable first-progress binding. A
// binding's presence permanently locks protected template fields.
func (s *SQLiteStore) ListUserTemplateBindings(ctx context.Context, templateID string) ([]UserTemplateBinding, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	templateID = strings.ToLower(strings.TrimSpace(templateID))
	if !userTemplateIDPattern.MatchString(templateID) {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, template_id, attachment_id, quest_id, definition_digest, execution_digest
		FROM setup_user_template_binding WHERE template_id = ? ORDER BY user_id, attachment_id`, templateID)
	if err != nil {
		return nil, safeStoreFailure(err, 0)
	}
	defer func() { _ = rows.Close() }()
	bindings := make([]UserTemplateBinding, 0)
	for rows.Next() {
		var binding UserTemplateBinding
		if err := rows.Scan(&binding.UserID, &binding.TemplateID, &binding.AttachmentID, &binding.QuestID,
			&binding.DefinitionDigest, &binding.ExecutionDigest); err != nil {
			return nil, safeStoreFailure(err, 0)
		}
		normalized, normalizeErr := normalizeUserTemplateBinding(binding)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		bindings = append(bindings, normalized)
	}
	if err := rows.Err(); err != nil {
		return nil, safeStoreFailure(err, 0)
	}
	return bindings, nil
}

func (s *SQLiteStore) GetUserTemplateBinding(ctx context.Context, userID, templateID string) (*UserTemplateBinding, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	templateID = strings.ToLower(strings.TrimSpace(templateID))
	if userID == "" || !userTemplateIDPattern.MatchString(templateID) {
		return nil, ErrInvalid
	}
	binding, err := getUserTemplateBindingWith(ctx, s.db, userID, templateID)
	if err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *SQLiteStore) HasUserTemplateBinding(ctx context.Context, userID, templateID string) (bool, error) {
	_, err := s.GetUserTemplateBinding(ctx, userID, templateID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

// ClaimUserTemplateBinding inserts the first immutable binding or verifies the
// exact existing one. It never updates digests in place.
func (s *SQLiteStore) ClaimUserTemplateBinding(ctx context.Context, binding UserTemplateBinding) (*UserTemplateBinding, bool, error) {
	if err := s.configured(); err != nil {
		return nil, false, err
	}
	normalized, err := normalizeUserTemplateBinding(binding)
	if err != nil {
		return nil, false, err
	}
	var result *UserTemplateBinding
	created := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var claimErr error
		result, created, claimErr = claimUserTemplateBindingWith(ctx, tx, normalized, s.now().UTC())
		return claimErr
	})
	if err != nil {
		return nil, false, err
	}
	return result, created, nil
}

// ClaimUserTemplateRoots atomically claims all non-empty canonical roots for
// one immutable binding. A conflict rolls back the complete claim set.
func (s *SQLiteStore) ClaimUserTemplateRoots(ctx context.Context, claims []UserTemplateRootClaim) error {
	if err := s.configured(); err != nil {
		return err
	}
	if len(claims) == 0 {
		return nil
	}
	normalized := make([]UserTemplateRootClaim, len(claims))
	var binding UserTemplateBinding
	for index, claim := range claims {
		current, err := normalizeUserTemplateBinding(claim.UserTemplateBinding)
		claim.RootKind = strings.ToLower(strings.TrimSpace(claim.RootKind))
		claim.RootID = strings.TrimSpace(claim.RootID)
		if err != nil || !validUserTemplateRootKind(claim.RootKind) || claim.RootID == "" || len(claim.RootID) > 200 ||
			(index > 0 && !sameUserTemplateBinding(current, binding)) {
			return ErrInvalid
		}
		binding = current
		claim.UserTemplateBinding = current
		normalized[index] = claim
	}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, _, claimErr := claimUserTemplateBindingWith(ctx, tx, binding, s.now().UTC()); claimErr != nil {
			return claimErr
		}
		for _, claim := range normalized {
			if claimErr := claimUserTemplateRootWith(ctx, tx, claim, s.now().UTC()); claimErr != nil {
				return claimErr
			}
		}
		return nil
	})
	if err == nil || errors.Is(err, ErrUserTemplateBindingMismatch) || errors.Is(err, ErrUserTemplateRootClaimed) || errors.Is(err, ErrInvalid) {
		return err
	}
	return fmt.Errorf("setup journey: claim user template roots: %w", err)
}

// ClaimUserTemplateRoot atomically establishes/verifies the template binding
// and then claims one canonical resource root. A different binding can never
// adopt that root, including after restart or across processes.
func (s *SQLiteStore) ClaimUserTemplateRoot(ctx context.Context, claim UserTemplateRootClaim) (*UserTemplateRootClaim, bool, error) {
	if err := s.configured(); err != nil {
		return nil, false, err
	}
	binding, err := normalizeUserTemplateBinding(claim.UserTemplateBinding)
	claim.RootKind = strings.ToLower(strings.TrimSpace(claim.RootKind))
	claim.RootID = strings.TrimSpace(claim.RootID)
	if err != nil || !validUserTemplateRootKind(claim.RootKind) || claim.RootID == "" || len(claim.RootID) > 200 {
		return nil, false, ErrInvalid
	}
	var result *UserTemplateRootClaim
	created := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, _, claimErr := claimUserTemplateBindingWith(ctx, tx, binding, s.now().UTC()); claimErr != nil {
			return claimErr
		}
		now := s.now().UTC()
		execResult, execErr := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO setup_user_template_root_claim (
				root_kind, root_id, user_id, template_id, attachment_id, quest_id, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, claim.RootKind, claim.RootID, binding.UserID, binding.TemplateID, binding.AttachmentID, binding.QuestID, now)
		if execErr != nil {
			return execErr
		}
		rows, rowsErr := execResult.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		created = rows == 1
		row := tx.QueryRowContext(ctx, `
			SELECT c.root_kind, c.root_id, c.user_id, c.template_id, c.attachment_id,
				c.quest_id, b.definition_digest, b.execution_digest, c.created_at
			FROM setup_user_template_root_claim c
			JOIN setup_user_template_binding b
			  ON b.user_id = c.user_id AND b.template_id = c.template_id AND b.attachment_id = c.attachment_id
			WHERE c.root_kind = ? AND c.root_id = ?
		`, claim.RootKind, claim.RootID)
		result = &UserTemplateRootClaim{}
		if scanErr := row.Scan(&result.RootKind, &result.RootID, &result.UserID, &result.TemplateID,
			&result.AttachmentID, &result.QuestID, &result.DefinitionDigest, &result.ExecutionDigest, &result.CreatedAt); scanErr != nil {
			return scanErr
		}
		if !sameUserTemplateBinding(result.UserTemplateBinding, binding) {
			return ErrUserTemplateRootClaimed
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrUserTemplateBindingMismatch) || errors.Is(err, ErrUserTemplateRootClaimed) || errors.Is(err, ErrInvalid) {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("setup journey: claim user template root: %w", err)
	}
	return result, created, nil
}

func claimUserTemplateRootWith(ctx context.Context, tx *sql.Tx, claim UserTemplateRootClaim, now time.Time) error {
	binding := claim.UserTemplateBinding
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO setup_user_template_root_claim (
			root_kind, root_id, user_id, template_id, attachment_id, quest_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, claim.RootKind, claim.RootID, binding.UserID, binding.TemplateID, binding.AttachmentID, binding.QuestID, now)
	if err != nil {
		return err
	}
	row := tx.QueryRowContext(ctx, `
		SELECT c.user_id, c.template_id, c.attachment_id, c.quest_id, b.definition_digest, b.execution_digest
		FROM setup_user_template_root_claim c
		JOIN setup_user_template_binding b
		  ON b.user_id = c.user_id AND b.template_id = c.template_id AND b.attachment_id = c.attachment_id
		WHERE c.root_kind = ? AND c.root_id = ?
	`, claim.RootKind, claim.RootID)
	var stored UserTemplateBinding
	if err := row.Scan(&stored.UserID, &stored.TemplateID, &stored.AttachmentID, &stored.QuestID,
		&stored.DefinitionDigest, &stored.ExecutionDigest); err != nil {
		return err
	}
	if !sameUserTemplateBinding(stored, binding) {
		return ErrUserTemplateRootClaimed
	}
	return nil
}

func claimUserTemplateBindingWith(ctx context.Context, tx *sql.Tx, binding UserTemplateBinding, now time.Time) (*UserTemplateBinding, bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO setup_user_template_binding (
			user_id, template_id, attachment_id, quest_id, definition_digest, execution_digest, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, binding.UserID, binding.TemplateID, binding.AttachmentID, binding.QuestID,
		binding.DefinitionDigest, binding.ExecutionDigest, now)
	if err != nil {
		return nil, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	stored, err := getUserTemplateBindingWith(ctx, tx, binding.UserID, binding.TemplateID)
	if errors.Is(err, ErrNotFound) {
		return nil, false, ErrUserTemplateBindingMismatch
	}
	if err != nil {
		return nil, false, err
	}
	if !sameUserTemplateBinding(*stored, binding) {
		return nil, false, ErrUserTemplateBindingMismatch
	}
	return stored, rows == 1, nil
}

func getUserTemplateBindingWith(ctx context.Context, source queryRower, userID, templateID string) (*UserTemplateBinding, error) {
	row := source.QueryRowContext(ctx, `
		SELECT user_id, template_id, attachment_id, quest_id, definition_digest, execution_digest, created_at
		FROM setup_user_template_binding
		WHERE user_id = ? AND template_id = ?
		ORDER BY created_at ASC LIMIT 1
	`, userID, templateID)
	binding := &UserTemplateBinding{}
	if err := row.Scan(&binding.UserID, &binding.TemplateID, &binding.AttachmentID, &binding.QuestID,
		&binding.DefinitionDigest, &binding.ExecutionDigest, &binding.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("setup journey: read user template binding: %w", err)
	}
	return binding, nil
}

func normalizeUserTemplateBinding(binding UserTemplateBinding) (UserTemplateBinding, error) {
	binding.UserID = strings.TrimSpace(binding.UserID)
	binding.TemplateID = strings.ToLower(strings.TrimSpace(binding.TemplateID))
	binding.AttachmentID = strings.ToLower(strings.TrimSpace(binding.AttachmentID))
	binding.QuestID = strings.ToLower(strings.TrimSpace(binding.QuestID))
	binding.DefinitionDigest = strings.ToLower(strings.TrimSpace(binding.DefinitionDigest))
	binding.ExecutionDigest = strings.ToLower(strings.TrimSpace(binding.ExecutionDigest))
	if binding.UserID == "" || !userTemplateIDPattern.MatchString(binding.TemplateID) ||
		!userTemplateAttachmentPattern.MatchString(binding.AttachmentID) || !userTemplateIDPattern.MatchString(binding.QuestID) ||
		!validateDigest(binding.DefinitionDigest, false) || !validateDigest(binding.ExecutionDigest, false) {
		return UserTemplateBinding{}, ErrInvalid
	}
	return binding, nil
}

func sameUserTemplateBinding(left, right UserTemplateBinding) bool {
	return left.UserID == right.UserID && left.TemplateID == right.TemplateID &&
		left.AttachmentID == right.AttachmentID && left.QuestID == right.QuestID &&
		left.DefinitionDigest == right.DefinitionDigest && left.ExecutionDigest == right.ExecutionDigest
}

func validUserTemplateRootKind(value string) bool {
	switch value {
	case "home", "project", "workspace":
		return true
	default:
		return false
	}
}
