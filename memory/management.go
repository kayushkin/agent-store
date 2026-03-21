package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Forget marks a memory as forgotten (soft delete by setting importance to 0).
func (s *Store) Forget(id string) error {
	query := `UPDATE memories SET importance = 0 WHERE id = ? OR id LIKE ?`
	res, err := s.db.Exec(query, id, id+"%")
	if err != nil {
		return fmt.Errorf("forget memory: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("memory not found: %s", id)
	}
	return nil
}

// DecayImportance applies time-based decay to all memories.
// Called periodically (e.g., daily) to gradually reduce importance of unused memories.
func (s *Store) DecayImportance() error {
	now := time.Now()
	query := `
	UPDATE memories
	SET importance = importance * ?
	WHERE last_accessed < ?
	`
	dayAgo := now.Add(-24 * time.Hour).Unix()
	_, err := s.db.Exec(query, 0.99, dayAgo)
	return err
}

// ListByOrchestrator returns recent memories for a specific orchestrator.
func (s *Store) ListByOrchestrator(orchestrator string, limit int, minImportance float64) ([]Memory, error) {
	now := time.Now()
	query := `
	SELECT id, content, summary, original_id, importance, access_count, last_accessed, created_at, source, embedding, always_load, expires_at, tokens, ref_type, ref_target, is_lazy, orchestrator
	FROM memories
	WHERE importance >= ?
	  AND (expires_at IS NULL OR expires_at > ?)
	  AND orchestrator = ?
	ORDER BY created_at DESC
	LIMIT ?
	`
	rows, err := s.db.Query(query, minImportance, now.Unix(), orchestrator, limit)
	if err != nil {
		return nil, fmt.Errorf("query by orchestrator: %w", err)
	}
	defer rows.Close()

	var result []Memory
	for rows.Next() {
		var m Memory
		var summary, originalID, refTarget, orch sql.NullString
		var embJSON []byte
		var lastAccessed, createdAt int64
		var expiresAt sql.NullInt64

		err := rows.Scan(
			&m.ID, &m.Content, &summary, &originalID,
			&m.Importance, &m.AccessCount, &lastAccessed, &createdAt, &m.Source, &embJSON,
			&m.AlwaysLoad, &expiresAt, &m.Tokens,
			&m.RefType, &refTarget, &m.IsLazy, &orch,
		)
		if err != nil {
			continue
		}

		m.Summary = summary.String
		m.OriginalID = originalID.String
		m.RefTarget = refTarget.String
		m.Orchestrator = orch.String
		m.LastAccessed = time.Unix(lastAccessed, 0)
		m.CreatedAt = time.Unix(createdAt, 0)
		if expiresAt.Valid {
			exp := time.Unix(expiresAt.Int64, 0)
			m.ExpiresAt = &exp
		}
		if len(embJSON) > 0 {
			json.Unmarshal(embJSON, &m.Embedding)
		}

		result = append(result, m)
	}

	// Load tags
	for i := range result {
		tags, _ := s.loadTags(result[i].ID)
		if tags != nil {
			result[i].Tags = tags
		} else {
			result[i].Tags = []string{}
		}
	}

	return result, nil
}

// CountByOrchestrator returns the total memory count for an orchestrator.
func (s *Store) CountByOrchestrator(orchestrator string) (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM memories WHERE orchestrator = ? AND importance > 0",
		orchestrator,
	).Scan(&count)
	return count, err
}

// ListOrchestrators returns all distinct orchestrator values.
func (s *Store) ListOrchestrators() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT orchestrator FROM memories WHERE orchestrator != '' AND importance > 0 ORDER BY orchestrator")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var orch string
		if err := rows.Scan(&orch); err == nil {
			result = append(result, orch)
		}
	}
	return result, nil
}

// ListRecent returns the N most recently created memories with importance > threshold.
func (s *Store) ListRecent(limit int, minImportance float64) ([]Memory, error) {
	now := time.Now()
	query := `
	SELECT id, content, summary, original_id, importance, access_count, last_accessed, created_at, source, embedding, always_load, expires_at, tokens, ref_type, ref_target, is_lazy
	FROM memories
	WHERE importance >= ?
	  AND (expires_at IS NULL OR expires_at > ?)
	ORDER BY created_at DESC
	LIMIT ?
	`
	rows, err := s.db.Query(query, minImportance, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("query recent: %w", err)
	}
	defer rows.Close()

	var result []Memory
	var memoryIDs []string
	
	for rows.Next() {
		var m Memory
		var summary, originalID, refTarget sql.NullString
		var embJSON []byte
		var lastAccessed, createdAt int64
		var expiresAt sql.NullInt64

		err := rows.Scan(
			&m.ID, &m.Content, &summary, &originalID,
			&m.Importance, &m.AccessCount, &lastAccessed, &createdAt, &m.Source, &embJSON,
			&m.AlwaysLoad, &expiresAt, &m.Tokens,
			&m.RefType, &refTarget, &m.IsLazy,
		)
		if err != nil {
			continue
		}

		m.Summary = summary.String
		m.OriginalID = originalID.String
		m.RefTarget = refTarget.String
		m.LastAccessed = time.Unix(lastAccessed, 0)
		m.CreatedAt = time.Unix(createdAt, 0)
		if expiresAt.Valid {
			exp := time.Unix(expiresAt.Int64, 0)
			m.ExpiresAt = &exp
		}

		if err := json.Unmarshal(embJSON, &m.Embedding); err != nil {
			continue
		}

		result = append(result, m)
		memoryIDs = append(memoryIDs, m.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch tags for all memories
	if len(memoryIDs) > 0 {
		tagMap := make(map[string][]string)
		
		placeholders := make([]string, len(memoryIDs))
		args := make([]interface{}, len(memoryIDs))
		for i, id := range memoryIDs {
			placeholders[i] = "?"
			args[i] = id
		}
		
		tagQuery := fmt.Sprintf("SELECT memory_id, tag FROM memory_tags WHERE memory_id IN (%s)", 
			strings.Join(placeholders, ","))
		
		tagRows, err := s.db.Query(tagQuery, args...)
		if err == nil {
			defer tagRows.Close()
			for tagRows.Next() {
				var memID, tag string
				if err := tagRows.Scan(&memID, &tag); err == nil {
					tagMap[memID] = append(tagMap[memID], tag)
				}
			}
		}
		
		// Attach tags to memories
		for i := range result {
			result[i].Tags = tagMap[result[i].ID]
			if result[i].Tags == nil {
				result[i].Tags = []string{}
			}
		}
	}

	return result, nil
}