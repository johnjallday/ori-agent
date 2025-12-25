// Package database provides SQLite database initialization and connection management.
//
// This package uses modernc.org/sqlite, a pure Go implementation that doesn't
// require CGO. This ensures cross-platform compatibility and simpler builds.
//
// The database is created in the application's data directory alongside other
// configuration files. Migrations are handled automatically on startup.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/johnjallday/ori-agent/internal/logger"
	_ "modernc.org/sqlite"
)

// DB wraps a sql.DB with additional functionality for migrations and management.
type DB struct {
	*sql.DB
	path string
	mu   sync.RWMutex
}

// Config holds database configuration options.
type Config struct {
	// Path is the full path to the database file.
	// If empty, uses the default data directory.
	Path string

	// InMemory creates an in-memory database (useful for testing).
	InMemory bool

	// WALMode enables Write-Ahead Logging for better concurrency.
	// Default is true.
	WALMode bool

	// BusyTimeout is the timeout in milliseconds for busy locks.
	// Default is 5000 (5 seconds).
	BusyTimeout int
}

// DefaultConfig returns the default database configuration.
func DefaultConfig() *Config {
	return &Config{
		WALMode:     true,
		BusyTimeout: 5000,
	}
}

// Open opens or creates a SQLite database at the specified path.
// It applies pragmas for optimal performance and runs any pending migrations.
func Open(ctx context.Context, cfg *Config) (*DB, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	var dsn string
	var path string

	if cfg.InMemory {
		dsn = ":memory:"
		path = ":memory:"
	} else {
		if cfg.Path == "" {
			dataDir, err := getDataDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get data directory: %w", err)
			}
			path = filepath.Join(dataDir, "sessions.db")
		} else {
			path = cfg.Path
		}

		// Ensure the directory exists
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}

		dsn = path
	}

	// Open the database
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{
		DB:   sqlDB,
		path: path,
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(1) // SQLite works best with a single writer
	sqlDB.SetMaxIdleConns(1)

	// Apply pragmas for performance
	if err := db.applyPragmas(ctx, cfg); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to apply pragmas: %w", err)
	}

	// Run migrations
	if err := db.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	logger.Info("Database initialized", logger.Fields{
		"path":     path,
		"wal_mode": cfg.WALMode,
	})

	return db, nil
}

// applyPragmas sets SQLite pragmas for optimal performance.
func (db *DB) applyPragmas(ctx context.Context, cfg *Config) error {
	pragmas := []string{
		// Enable foreign keys
		"PRAGMA foreign_keys = ON",

		// Set busy timeout
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.BusyTimeout),

		// Synchronous mode - NORMAL is a good balance of safety and speed
		"PRAGMA synchronous = NORMAL",

		// Use memory-mapped I/O (64MB)
		"PRAGMA mmap_size = 67108864",

		// Cache size (negative = KB, positive = pages)
		"PRAGMA cache_size = -8000", // 8MB cache

		// Store temp tables in memory
		"PRAGMA temp_store = MEMORY",
	}

	// WAL mode provides better concurrency
	if cfg.WALMode {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL")
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("failed to execute %s: %w", pragma, err)
		}
	}

	return nil
}

// Path returns the database file path.
func (db *DB) Path() string {
	return db.path
}

// Close closes the database connection.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Checkpoint WAL before closing
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		logger.Warn("Failed to checkpoint WAL", logger.Fields{"error": err})
	}

	return db.DB.Close()
}

// getDataDir returns the application's data directory.
// This mirrors the logic in other parts of the application.
func getDataDir() (string, error) {
	// Check for environment variable first
	if dir := os.Getenv("ORI_DATA_DIR"); dir != "" {
		return dir, nil
	}

	// Default to current working directory
	// This aligns with how settings.json and other files are stored
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return cwd, nil
}

// InTransaction executes a function within a database transaction.
// The transaction is committed if the function returns nil, or rolled back otherwise.
func (db *DB) InTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			logger.Error("Failed to rollback transaction", logger.Fields{"error": rbErr})
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
