// Package agentstore provides a unified store for agent identity, nature, and configs.
package agentstore

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Pool manages worktree slots for a project
type Pool struct {
	baseRepo string // ~/life/repos/inber (the main clone)
	poolDir  string // ~/life/repos/.pools/inber/ (worktrees live here)
	db       *sql.DB
}

// Slot represents a leased worktree
type Slot struct {
	ID         int       // slot index (0, 1, 2...)
	Path       string    // ~/life/repos/.pools/inber/slot-0
	Branch     string    // pool/inber/slot-0-<timestamp>
	AgentID    string    // who's using it
	SessionID  string    // which session (for concurrent same-agent)
	Project    string    // project name
	Status     string    // ready, acquired, dirty
	AcquiredAt time.Time // when it was acquired
}

// ErrNoSlotsAvailable is returned when all pool slots are busy
var ErrNoSlotsAvailable = fmt.Errorf("no available slots in pool")

// InitPool creates a pool of worktree slots for a project
// baseRepo is the main git repository (e.g., ~/life/repos/inber)
// poolDir is where worktrees will be created (e.g., ~/life/repos/.pools/inber)
// size is the number of slots to create
func (s *Store) InitPool(project, baseRepo, poolDir string, size int) error {
	// Expand paths
	baseRepo = expandPath(baseRepo)
	poolDir = expandPath(poolDir)

	// Verify base repo exists and is a git repo
	if _, err := os.Stat(filepath.Join(baseRepo, ".git")); err != nil {
		return fmt.Errorf("base repo %s is not a git repository: %w", baseRepo, err)
	}

	// Create pool directory
	if err := os.MkdirAll(poolDir, 0755); err != nil {
		return fmt.Errorf("create pool directory: %w", err)
	}

	// Initialize slots in database and create worktrees
	for i := 0; i < size; i++ {
		slotPath := filepath.Join(poolDir, fmt.Sprintf("slot-%d", i))
		branchName := fmt.Sprintf("pool/%s/slot-%d", project, i)

		// Insert slot record if not exists
		_, err := s.db.Exec(`
			INSERT OR IGNORE INTO pool_slots (id, project, path, branch, status)
			VALUES (?, ?, ?, ?, 'ready')
		`, i, project, slotPath, branchName)
		if err != nil {
			return fmt.Errorf("insert slot %d: %w", i, err)
		}

		// Check if worktree already exists
		if _, err := os.Stat(slotPath); err == nil {
			continue // worktree already exists
		}

		// Create the worktree
		cmd := exec.Command("git", "worktree", "add", slotPath, "-b", branchName)
		cmd.Dir = baseRepo
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create worktree %d: %w\n%s", i, err, output)
		}
	}

	return nil
}

// Acquire finds an available slot and marks it as acquired
// Returns ErrNoSlotsAvailable if all slots are busy
func (s *Store) Acquire(project, agentID, sessionID string) (*Slot, error) {
	// Find first ready slot
	var id int
	var path, branch string
	var nullAgentID, nullSessionID sql.NullString
	err := s.db.QueryRow(`
		SELECT id, path, branch, agent_id, session_id
		FROM pool_slots
		WHERE project = ? AND status = 'ready'
		ORDER BY id
		LIMIT 1
	`, project).Scan(&id, &path, &branch, &nullAgentID, &nullSessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoSlotsAvailable
		}
		return nil, fmt.Errorf("query slot: %w", err)
	}

	// Mark as acquired
	now := time.Now()
	_, err = s.db.Exec(`
		UPDATE pool_slots
		SET status = 'acquired', agent_id = ?, session_id = ?, acquired_at = ?, released_at = NULL
		WHERE project = ? AND id = ?
	`, agentID, sessionID, now.Unix(), project, id)
	if err != nil {
		return nil, fmt.Errorf("acquire slot: %w", err)
	}

	return &Slot{
		ID:         id,
		Path:       path,
		Branch:     branch,
		AgentID:    agentID,
		SessionID:  sessionID,
		Project:    project,
		AcquiredAt: now,
	}, nil
}

// Release returns a slot to the pool
// If push is true, pushes the branch before resetting
func (s *Store) Release(project string, slotID int, push bool) error {
	// Get slot info
	var id int
	var path, branch string
	err := s.db.QueryRow(`
		SELECT id, path, branch
		FROM pool_slots
		WHERE project = ? AND id = ?
	`, project, slotID).Scan(&id, &path, &branch)
	if err != nil {
		return fmt.Errorf("get slot: %w", err)
	}

	// Create slot object for operations
	slot := &Slot{
		ID:      id,
		Path:    path,
		Branch:  branch,
		Project: project,
	}

	// Push if requested
	if push {
		if err := slot.Push(); err != nil {
			// Log but continue - we still want to reset
			fmt.Fprintf(os.Stderr, "warning: push failed: %v\n", err)
		}
	}

	// Reset worktree to main
	if err := slot.Reset(); err != nil {
		return fmt.Errorf("reset slot: %w", err)
	}

	// Mark as ready in database
	_, err = s.db.Exec(`
		UPDATE pool_slots
		SET status = 'ready', agent_id = NULL, session_id = NULL, released_at = ?
		WHERE project = ? AND id = ?
	`, time.Now().Unix(), project, slotID)
	if err != nil {
		return fmt.Errorf("release slot: %w", err)
	}

	return nil
}

// SlotStatus returns all slots for a project
func (s *Store) SlotStatus(project string) ([]Slot, error) {
	rows, err := s.db.Query(`
		SELECT id, project, path, branch, agent_id, session_id, status, acquired_at
		FROM pool_slots
		WHERE project = ?
		ORDER BY id
	`, project)
	if err != nil {
		return nil, fmt.Errorf("query slots: %w", err)
	}
	defer rows.Close()

	var slots []Slot
	for rows.Next() {
		var s Slot
		var acquiredAt sql.NullInt64
		var agentID, sessionID sql.NullString
		if err := rows.Scan(&s.ID, &s.Project, &s.Path, &s.Branch, &agentID, &sessionID, &s.Status, &acquiredAt); err != nil {
			return nil, fmt.Errorf("scan slot: %w", err)
		}
		if acquiredAt.Valid {
			s.AcquiredAt = time.Unix(acquiredAt.Int64, 0)
		}
		if agentID.Valid {
			s.AgentID = agentID.String
		}
		if sessionID.Valid {
			s.SessionID = sessionID.String
		}
		slots = append(slots, s)
	}
	return slots, nil
}

// Pull fetches and merges origin/main in the slot's worktree
func (slot *Slot) Pull() error {
	// Fetch
	cmd := exec.Command("git", "fetch", "origin", "main")
	cmd.Dir = slot.Path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %w\n%s", err, output)
	}

	// Merge
	cmd = exec.Command("git", "merge", "origin/main")
	cmd.Dir = slot.Path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git merge: %w\n%s", err, output)
	}

	return nil
}

// Commit stages all changes and commits with the given message
func (slot *Slot) Commit(msg string) error {
	// Stage all
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = slot.Path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w\n%s", err, output)
	}

	// Commit
	cmd = exec.Command("git", "commit", "-m", msg)
	cmd.Dir = slot.Path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w\n%s", err, output)
	}

	return nil
}

// Push pushes the slot's branch to origin
func (slot *Slot) Push() error {
	cmd := exec.Command("git", "push", "origin", slot.Branch)
	cmd.Dir = slot.Path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %w\n%s", err, output)
	}
	return nil
}

// Diff returns the diff against main
func (slot *Slot) Diff() (string, error) {
	cmd := exec.Command("git", "diff", "main")
	cmd.Dir = slot.Path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff: %w\n%s", err, output)
	}
	return string(output), nil
}

// Reset resets the worktree to main, discarding all changes
func (slot *Slot) Reset() error {
	// Fetch latest
	cmd := exec.Command("git", "fetch", "origin", "main")
	cmd.Dir = slot.Path
	if _, err := cmd.CombinedOutput(); err != nil {
		// Continue even if fetch fails (e.g., no remote)
		fmt.Fprintf(os.Stderr, "warning: fetch failed: %v\n", err)
	}

	// Checkout the slot's branch (we're already on it, but this ensures clean state)
	cmd = exec.Command("git", "checkout", slot.Branch)
	cmd.Dir = slot.Path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s: %w\n%s", slot.Branch, err, output)
	}

	// Reset hard to origin/main (or main if no origin)
	cmd = exec.Command("git", "reset", "--hard", "origin/main")
	cmd.Dir = slot.Path
	if _, err := cmd.CombinedOutput(); err != nil {
		// Try without origin
		cmd = exec.Command("git", "reset", "--hard", "main")
		cmd.Dir = slot.Path
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git reset: %w\n%s", err, output)
		}
	}

	// Clean untracked files
	cmd = exec.Command("git", "clean", "-fd")
	cmd.Dir = slot.Path
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clean: %w\n%s", err, output)
	}

	return nil
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
