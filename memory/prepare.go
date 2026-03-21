package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// PrepareSessionConfig configures what gets loaded into memory for a session
type PrepareSessionConfig struct {
	RootDir        string        // Repository root directory
	IdentityFile   string        // Path to agent identity file (optional)
	IdentityText   string        // Direct identity text (used if IdentityFile is empty)
	AgentName      string        // Agent name for identity
	RecencyWindow  time.Duration // How far back to look for recent files (e.g., 24h)
	RecentFilesTTL time.Duration // How long recent file refs live (e.g., 10min)
}

// DefaultPrepareSessionConfig returns sensible defaults
func DefaultPrepareSessionConfig(rootDir string) PrepareSessionConfig {
	return PrepareSessionConfig{
		RootDir:        rootDir,
		AgentName:      "agent",
		RecencyWindow:  24 * time.Hour,
		RecentFilesTTL: 10 * time.Minute,
	}
}

// PrepareSession loads identity and recent files into memory for a new session.
// This replaces the old context.AutoLoad() pattern.
func (s *Store) PrepareSession(cfg PrepareSessionConfig) error {
	// 1. Load identity (permanent, always-load)
	if err := s.loadIdentity(cfg); err != nil {
		return fmt.Errorf("failed to load identity: %w", err)
	}

	// 2. Load memory usage instructions (permanent, always-load)
	if err := s.loadMemoryInstructions(); err != nil {
		return fmt.Errorf("failed to load memory instructions: %w", err)
	}

	// 3. Load tool registry (permanent, always-load)
	// Note: This will be populated later by engine after tools are built
	// For now, just ensure the structure is ready

	// 4. Load recent files (ephemeral, TTL-based)
	if cfg.RecencyWindow > 0 {
		if err := s.loadRecentFiles(cfg); err != nil {
			// Don't fail if recency detection fails
			fmt.Fprintf(os.Stderr, "warning: failed to load recent files: %v\n", err)
		}
	}

	return nil
}

// loadIdentity loads agent identity into memory as an always-load memory
func (s *Store) loadIdentity(cfg PrepareSessionConfig) error {
	var identityText string

	// Try to load from file first
	if cfg.IdentityFile != "" {
		content, err := os.ReadFile(cfg.IdentityFile)
		if err != nil {
			return err
		}
		identityText = string(content)
	} else if cfg.IdentityText != "" {
		identityText = cfg.IdentityText
	} else {
		// Default identity
		identityText = fmt.Sprintf("You are %s, a helpful coding assistant with access to file operations and shell commands.", cfg.AgentName)
	}

	// Save as always-load memory
	return s.Save(Memory{
		ID:         "identity",
		Content:    identityText,
		Tags:       []string{"identity", "always-load"},
		Importance: 1.0,
		Source:     "system",
		AlwaysLoad: true,
	})
}

// loadMemoryInstructions loads memory usage instructions
func (s *Store) loadMemoryInstructions() error {
	instructions := `You have persistent memory across sessions via these tools:
- memory_search: Search your memories before answering questions about past work, preferences, or decisions
- memory_save: Save important information — decisions made, user preferences, project context, lessons learned
- memory_forget: Mark outdated or incorrect memories as forgotten

Guidelines:
- Search memory at the start of conversations about ongoing projects
- Save key decisions and their reasoning
- Save user preferences when explicitly stated
- Don't save trivial or temporary information
- Review and forget outdated memories when you notice them`

	return s.Save(Memory{
		ID:         "memory-instructions",
		Content:    instructions,
		Tags:       []string{"instructions", "memory", "always-load"},
		Importance: 0.9,
		Source:     "system",
		AlwaysLoad: true,
	})
}

// loadRecentFiles loads recently modified file references into memory with TTL
func (s *Store) loadRecentFiles(cfg PrepareSessionConfig) error {
	// Find recently modified files
	recentFiles, err := FindRecentlyModified(cfg.RootDir, cfg.RecencyWindow)
	if err != nil {
		return err
	}

	// Calculate expiration time
	expiresAt := time.Now().Add(cfg.RecentFilesTTL)

	// Save each file as a lightweight reference
	for _, f := range recentFiles {
		// Skip if file is very recently accessed (likely just saved)
		ageMinutes := int(time.Since(f.ModTime).Minutes())
		
		// Build content stub
		var ageStr string
		if ageMinutes < 60 {
			ageStr = fmt.Sprintf("%d minute%s ago", ageMinutes, pluralString(ageMinutes))
		} else {
			hours := ageMinutes / 60
			ageStr = fmt.Sprintf("%d hour%s ago", hours, pluralString(hours))
		}

		content := fmt.Sprintf("Recently modified (%s): %s", ageStr, f.RelativePath)

		// Determine importance based on recency (more recent = more important)
		importance := 0.5
		if ageMinutes < 60 {
			importance = 0.7 // modified in last hour
		} else if ageMinutes < 360 {
			importance = 0.6 // modified in last 6 hours
		}

		// Extract file extension for tagging
		ext := filepath.Ext(f.RelativePath)
		tags := []string{"recent", "file:" + f.RelativePath}
		if ext != "" {
			tags = append(tags, "ext:"+ext)
		}

		// Save with TTL
		err := s.Save(Memory{
			ID:         "recent:" + uuid.NewString(),
			Content:    content,
			Tags:       tags,
			Importance: importance,
			Source:     "system",
			ExpiresAt:  &expiresAt,
		})
		if err != nil {
			return fmt.Errorf("failed to save recent file %s: %w", f.RelativePath, err)
		}
	}

	return nil
}

// pluralString returns "s" if n != 1
func pluralString(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}