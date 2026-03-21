package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// NewStore creates or opens a memory store at the given path.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Create normalized schema
	// Note: We only create the table and basic indexes here
	// Migrations will add new columns to existing tables
	schema := `
	CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		summary TEXT,
		original_id TEXT,
		importance REAL NOT NULL DEFAULT 0.5,
		access_count INTEGER NOT NULL DEFAULT 0,
		last_accessed INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		source TEXT NOT NULL,
		embedding BLOB
	);
	CREATE INDEX IF NOT EXISTS idx_importance ON memories(importance);
	CREATE INDEX IF NOT EXISTS idx_last_accessed ON memories(last_accessed);
	CREATE INDEX IF NOT EXISTS idx_created_at ON memories(created_at);
	CREATE INDEX IF NOT EXISTS idx_source ON memories(source);

	CREATE TABLE IF NOT EXISTS memory_tags (
		memory_id TEXT NOT NULL,
		tag TEXT NOT NULL,
		FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_memory_tags_memory_id ON memory_tags(memory_id);
	CREATE INDEX IF NOT EXISTS idx_memory_tags_tag ON memory_tags(tag);

	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	// Create session-related tables
	if _, err := db.Exec(SessionsSchema()); err != nil {
		db.Close()
		return nil, fmt.Errorf("create sessions schema: %w", err)
	}

	// Run migrations for existing databases
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &Store{
		db:       db,
		embedder: NewEmbedder(),
	}, nil
}

// OpenOrCreate opens an existing memory store or creates a new one.
func OpenOrCreate(rootDir string) (MemoryStore, error) {
	localDBPath := DefaultMemoryPath(rootDir)
	if err := os.MkdirAll(filepath.Dir(localDBPath), 0755); err != nil {
		return nil, fmt.Errorf("create memory directory: %w", err)
	}

	return NewStore(localDBPath)
}

// NewSQLiteStore creates a new SQLite-backed memory store at the given path.
// This is the same as NewStore but with a more descriptive name.
func NewSQLiteStore(dbPath string) (MemoryStore, error) {
	return NewStore(dbPath)
}