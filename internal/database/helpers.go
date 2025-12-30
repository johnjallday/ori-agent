package database

import (
	"database/sql"
	"errors"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// ErrNotFound is returned when the requested entity was not found
var ErrNotFound = errors.New("entity not found")

// CheckRowsAffected checks if the SQL operation affected any rows.
// It handles the error from RowsAffected() and returns ErrNotFound if no rows were affected.
// The entityName parameter is used for logging purposes.
//
// Usage:
//
//	result, err := db.ExecContext(ctx, "UPDATE ...", args...)
//	if err != nil {
//		return fmt.Errorf("failed to update: %w", err)
//	}
//	if err := database.CheckRowsAffected(result, "session"); err != nil {
//		return err
//	}
func CheckRowsAffected(result sql.Result, entityName string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		// Log the error but don't fail - this is a driver limitation, not a real failure
		logger.Warn("Failed to get rows affected", logger.Fields{
			"entity": entityName,
			"error":  err.Error(),
		})
		// Continue since the operation itself succeeded
		return nil
	}

	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

// CheckRowsAffectedWithError is like CheckRowsAffected but allows specifying a custom error
// to return when no rows are affected.
//
// Usage:
//
//	if err := database.CheckRowsAffectedWithError(result, "session", session.ErrSessionNotFound); err != nil {
//		return err
//	}
func CheckRowsAffectedWithError(result sql.Result, entityName string, notFoundErr error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		logger.Warn("Failed to get rows affected", logger.Fields{
			"entity": entityName,
			"error":  err.Error(),
		})
		return nil
	}

	if rows == 0 {
		return notFoundErr
	}

	return nil
}
