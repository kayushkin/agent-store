package memory

import (
	"fmt"
	"time"
)

// Session represents a tracked agent session.
type Session struct {
	ID           string
	AgentName    string
	Model        string
	StartedAt    time.Time
	EndedAt      time.Time
	InputTokens  int
	OutputTokens int
	Cost         float64
	Summary      string
	Tags         []string
}

// SaveSession stores a session record.
func (s *Store) SaveSession(sess Session) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
	INSERT INTO sessions (id, agent_name, model, started_at, ended_at, input_tokens, output_tokens, cost, summary)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = tx.Exec(query,
		sess.ID, sess.AgentName, sess.Model,
		sess.StartedAt.Unix(), nullInt64(sess.EndedAt),
		sess.InputTokens, sess.OutputTokens, sess.Cost, nullString(sess.Summary),
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	// Insert tags
	if len(sess.Tags) > 0 {
		tagStmt, err := tx.Prepare("INSERT INTO session_tags (session_id, tag) VALUES (?, ?)")
		if err != nil {
			return fmt.Errorf("prepare tag insert: %w", err)
		}
		defer tagStmt.Close()

		for _, tag := range sess.Tags {
			if _, err := tagStmt.Exec(sess.ID, tag); err != nil {
				return fmt.Errorf("insert tag: %w", err)
			}
		}
	}

	return tx.Commit()
}

// TrackMemoryUsage records when a memory was used in a session.
func (s *Store) TrackMemoryUsage(memoryID, sessionID string, turnNumber int, usageType string) error {
	query := `
	INSERT INTO memory_usage (memory_id, session_id, turn_number, usage_type, accessed_at)
	VALUES (?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, memoryID, sessionID, turnNumber, usageType, time.Now().Unix())
	return err
}

// SessionsSchema returns the SQL schema for session-related tables.
func SessionsSchema() string {
	return `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		agent_name TEXT NOT NULL,
		model TEXT NOT NULL,
		started_at INTEGER NOT NULL,
		ended_at INTEGER,
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		cost REAL DEFAULT 0.0,
		summary TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_started_at ON sessions(started_at);
	CREATE INDEX IF NOT EXISTS idx_sessions_agent_name ON sessions(agent_name);

	CREATE TABLE IF NOT EXISTS session_tags (
		session_id TEXT NOT NULL,
		tag TEXT NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_session_tags_session_id ON session_tags(session_id);
	CREATE INDEX IF NOT EXISTS idx_session_tags_tag ON session_tags(tag);

	CREATE TABLE IF NOT EXISTS memory_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		memory_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		turn_number INTEGER NOT NULL,
		usage_type TEXT NOT NULL,
		accessed_at INTEGER NOT NULL,
		FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_memory_usage_memory_id ON memory_usage(memory_id);
	CREATE INDEX IF NOT EXISTS idx_memory_usage_session_id ON memory_usage(session_id);
	`
}