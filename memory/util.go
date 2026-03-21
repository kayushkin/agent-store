package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// nullString returns a sql.NullString from a string.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// nullInt64 returns a sql.NullInt64 from a time.Time.
func nullInt64(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{Valid: false}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

func nullInt64Ptr(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{Valid: false}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

// loadLazyContent loads content for lazy-loaded references from their source.
func (s *Store) loadLazyContent(m *Memory) error {
	switch m.RefType {
	case "file", "identity":
		if m.RefTarget == "" {
			return fmt.Errorf("%s reference missing ref_target path", m.RefType)
		}
		data, err := os.ReadFile(m.RefTarget)
		if err != nil {
			return fmt.Errorf("read %s: %w", m.RefTarget, err)
		}
		m.Content = string(data)
		m.Tokens = len(m.Content) / 4
		return nil
	default:
		return nil // content already in DB
	}
}

// DefaultMemoryPath returns the default path for the memory database.
func DefaultMemoryPath(rootDir string) string {
	return filepath.Join(rootDir, ".inber", "memory.db")
}