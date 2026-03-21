package memory

import (
	"database/sql"
	"encoding/json"
	"time"
)

// scanMemory scans a memory row from a SQL result
func (s *Store) scanMemory(scanner interface {
	Scan(dest ...interface{}) error
}) (Memory, error) {
	var m Memory
	var summary, originalID sql.NullString
	var embJSON []byte
	var lastAccessed, createdAt int64
	var expiresAt sql.NullInt64

	err := scanner.Scan(
		&m.ID, &m.Content, &summary, &originalID,
		&m.Importance, &m.AccessCount, &lastAccessed, &createdAt, &m.Source, &embJSON,
		&m.AlwaysLoad, &expiresAt, &m.Tokens,
	)
	if err != nil {
		return Memory{}, err
	}

	m.Summary = summary.String
	m.OriginalID = originalID.String
	m.LastAccessed = time.Unix(lastAccessed, 0)
	m.CreatedAt = time.Unix(createdAt, 0)
	if expiresAt.Valid {
		exp := time.Unix(expiresAt.Int64, 0)
		m.ExpiresAt = &exp
	}

	if len(embJSON) > 0 {
		json.Unmarshal(embJSON, &m.Embedding)
	}

	return m, nil
}

// loadTags loads tags for a given memory ID
func (s *Store) loadTags(memoryID string) ([]string, error) {
	rows, err := s.db.Query("SELECT tag FROM memory_tags WHERE memory_id = ?", memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			continue
		}
		tags = append(tags, tag)
	}

	return tags, nil
}