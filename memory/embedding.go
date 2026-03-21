package memory

import (
	"math"
	"strings"
	"unicode"
)

// Embedder generates simple TF-IDF style embeddings for text.
// This is a placeholder until we integrate a proper embedding model.
type Embedder struct {
	// Document frequency map (built incrementally)
	// In production, this would be pre-computed or use an external model
}

// NewEmbedder creates a new embedder.
func NewEmbedder() *Embedder {
	return &Embedder{}
}

// Embed generates a simple bag-of-words embedding vector from text.
// Uses a fixed vocabulary of the most common/useful words and computes term frequency.
func (e *Embedder) Embed(text string) []float64 {
	// Normalize and tokenize
	tokens := tokenize(text)
	
	// Build term frequency map
	tf := make(map[string]float64)
	for _, token := range tokens {
		tf[token]++
	}
	
	// Normalize by document length
	totalTerms := float64(len(tokens))
	if totalTerms > 0 {
		for token := range tf {
			tf[token] /= totalTerms
		}
	}
	
	// Map to fixed-size vector using hash bucketing
	// This ensures consistent dimensionality across all documents
	const vectorSize = 256
	vector := make([]float64, vectorSize)
	
	for token, freq := range tf {
		// Simple hash to bucket index
		bucket := hashString(token) % vectorSize
		vector[bucket] += freq
	}
	
	// Normalize the vector (L2 norm)
	var norm float64
	for _, v := range vector {
		norm += v * v
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vector {
			vector[i] /= norm
		}
	}
	
	return vector
}

// tokenize splits text into normalized tokens (lowercase, alphanumeric only).
func tokenize(text string) []string {
	// Lowercase and split on non-alphanumeric
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder
	
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				token := current.String()
				// Filter out very short tokens and common stop words
				if len(token) > 2 && !isStopWord(token) {
					tokens = append(tokens, token)
				}
				current.Reset()
			}
		}
	}
	
	// Don't forget the last token
	if current.Len() > 0 {
		token := current.String()
		if len(token) > 2 && !isStopWord(token) {
			tokens = append(tokens, token)
		}
	}
	
	return tokens
}

// hashString computes a simple hash of a string.
func hashString(s string) int {
	h := 0
	for i := 0; i < len(s); i++ {
		h = 31*h + int(s[i])
	}
	if h < 0 {
		h = -h
	}
	return h
}

// isStopWord checks if a token is a common stop word.
func isStopWord(token string) bool {
	// Minimal stop word list
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "are": true,
		"but": true, "not": true, "you": true, "all": true,
		"can": true, "her": true, "was": true, "one": true,
		"our": true, "out": true, "has": true, "had": true,
		"have": true, "this": true, "that": true, "from": true,
		"with": true, "they": true, "been": true, "will": true,
		"into": true, "more": true, "than": true, "what": true,
		"when": true, "where": true, "who": true, "which": true,
	}
	return stopWords[token]
}

// CosineSimilarity computes the cosine similarity between two vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}