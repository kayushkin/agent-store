package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// SearchFiltered finds memories matching the query, optionally filtered by orchestrator.
// If orchestrator is empty, all memories are searched.
func (s *Store) SearchFiltered(query string, limit int, orchestrator string) ([]Memory, error) {
	return s.searchInternal(query, limit, orchestrator)
}

// Search finds memories matching the query, ranked by similarity, recency, and importance.
func (s *Store) Search(query string, limit int) ([]Memory, error) {
	return s.searchInternal(query, limit, "")
}

func (s *Store) searchInternal(query string, limit int, orchestrator string) ([]Memory, error) {
	if limit <= 0 {
		limit = 10
	}

	// Generate query embedding
	queryEmb := s.embedder.Embed(query)

	// Fetch all non-forgotten, non-expired memories
	now := time.Now()
	sqlQuery := `
	SELECT id, content, summary, original_id, importance, access_count, last_accessed, created_at, source, embedding, always_load, expires_at, tokens, ref_type, ref_target, is_lazy, orchestrator
	FROM memories
	WHERE importance > 0
	  AND (expires_at IS NULL OR expires_at > ?)
	`
	args := []interface{}{now.Unix()}
	if orchestrator != "" {
		sqlQuery += " AND orchestrator = ?"
		args = append(args, orchestrator)
	}
	rows, err := s.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	type scored struct {
		memory Memory
		score  float64
	}
	var candidates []scored
	memoryIDs := []string{}

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
			&m.RefType, &refTarget, &m.IsLazy, &m.Orchestrator,
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

		// Calculate similarity
		similarity := CosineSimilarity(queryEmb, m.Embedding)

		// Calculate recency boost (decays over time)
		daysSinceAccess := now.Sub(m.LastAccessed).Hours() / 24
		recencyBoost := math.Pow(0.99, daysSinceAccess)

		// Combined score
		score := similarity * m.Importance * recencyBoost

		candidates = append(candidates, scored{memory: m, score: score})
		memoryIDs = append(memoryIDs, m.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	// Fetch tags for all memories in a single query
	if len(memoryIDs) > 0 {
		tagMap := make(map[string][]string)
		
		// Build IN clause
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
		for i := range candidates {
			candidates[i].memory.Tags = tagMap[candidates[i].memory.ID]
			if candidates[i].memory.Tags == nil {
				candidates[i].memory.Tags = []string{}
			}
		}
	}

	// Sort by score (descending)
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// Take top N
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	result := make([]Memory, len(candidates))
	for i, c := range candidates {
		result[i] = c.memory
	}

	return result, nil
}