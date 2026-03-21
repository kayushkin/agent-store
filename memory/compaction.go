package memory

import (
	"fmt"
	"strings"
	"time"
)

// CompactionResult describes what was compacted.
type CompactionResult struct {
	OriginalIDs []string
	NewID       string
	Tags        []string
	Count       int
}

// Compact finds old, low-access, low-importance memories, groups them by tags,
// creates compacted summaries, and soft-deletes originals.
func (s *Store) Compact(minAge time.Duration, minCount int) ([]CompactionResult, error) {
	cutoff := time.Now().Add(-minAge).Unix()

	query := `
	SELECT id, content, importance, access_count, created_at
	FROM memories
	WHERE created_at < ? AND access_count < ? AND importance < 0.7 AND importance > 0
	`
	rows, err := s.db.Query(query, cutoff, minCount)
	if err != nil {
		return nil, fmt.Errorf("query compactable: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id      string
		content string
	}
	var candidates []candidate
	var candidateIDs []string

	for rows.Next() {
		var id, content string
		var importance float64
		var accessCount int
		var createdAt int64
		if err := rows.Scan(&id, &content, &importance, &accessCount, &createdAt); err != nil {
			continue
		}
		candidates = append(candidates, candidate{id: id, content: content})
		candidateIDs = append(candidateIDs, id)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Fetch tags for candidates
	tagMap := make(map[string][]string)
	if len(candidateIDs) > 0 {
		placeholders := make([]string, len(candidateIDs))
		args := make([]interface{}, len(candidateIDs))
		for i, id := range candidateIDs {
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
	}

	// Group by primary tag (first tag, or "untagged")
	groups := make(map[string][]candidate)
	groupTags := make(map[string]map[string]bool)
	for _, c := range candidates {
		tags := tagMap[c.id]
		key := "untagged"
		if len(tags) > 0 {
			key = tags[0]
		}
		groups[key] = append(groups[key], c)
		if groupTags[key] == nil {
			groupTags[key] = make(map[string]bool)
		}
		for _, t := range tags {
			groupTags[key][t] = true
		}
	}

	var results []CompactionResult

	for groupKey, members := range groups {
		if len(members) < 2 {
			continue // don't compact single memories
		}

		// Build combined content
		var parts []string
		var origIDs []string
		for _, m := range members {
			parts = append(parts, m.content)
			origIDs = append(origIDs, m.id)
		}
		combined := strings.Join(parts, "\n---\n")
		if len(combined) > 2000 {
			combined = combined[:2000] + "..."
		}

		// Collect all tags for the group
		var allTags []string
		for t := range groupTags[groupKey] {
			allTags = append(allTags, t)
		}

		newID := fmt.Sprintf("compact-%s-%d", groupKey, time.Now().UnixNano())
		newMem := Memory{
			ID:         newID,
			Content:    fmt.Sprintf("[Compacted from %d memories]\n%s", len(members), combined),
			Tags:       allTags,
			Importance: 0.5,
			Source:     "compaction",
			OriginalID: origIDs[0],
		}

		if err := s.Save(newMem); err != nil {
			continue
		}

		// Soft-delete originals
		for _, id := range origIDs {
			s.db.Exec("UPDATE memories SET importance = 0 WHERE id = ?", id)
		}

		results = append(results, CompactionResult{
			OriginalIDs: origIDs,
			NewID:       newID,
			Tags:        allTags,
			Count:       len(members),
		})
	}

	return results, nil
}