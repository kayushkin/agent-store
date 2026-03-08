// Package agentstore provides a unified store for agent identity, nature, and configs.
// It serves as the single source of truth for orchestrators like inber and openclaw.
package agentstore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DefaultPath returns the default database path (~/.config/agent-store/agents.db)
func DefaultPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".config", "agent-store", "agents.db")
}

// Store is the main handle for agent storage
type Store struct {
	db *sql.DB
}

// Open opens or creates the agent store at the given path.
// If path is empty, uses DefaultPath().
func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate creates tables if they don't exist
func (s *Store) migrate() error {
	// Read schema from embedded string
	_, err := s.db.Exec(schemaSQL)
	return err
}

// ============================================
// NATURE
// ============================================

// Nature represents a piece of agent character/identity
type Nature struct {
	ID         string
	Content    string
	Kind       string // identity, principle, value, user, project
	Scope      string // global, agent, project
	Priority   int
	Importance float64
	Source     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// UpsertNature creates or updates a nature entry
func (s *Store) UpsertNature(n Nature) error {
	now := time.Now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now

	_, err := s.db.Exec(`
		INSERT INTO nature (id, content, kind, scope, priority, importance, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			kind = excluded.kind,
			scope = excluded.scope,
			priority = excluded.priority,
			importance = excluded.importance,
			source = excluded.source,
			updated_at = excluded.updated_at
	`, n.ID, n.Content, n.Kind, n.Scope, n.Priority, n.Importance, n.Source, n.CreatedAt.Unix(), n.UpdatedAt.Unix())

	return err
}

// GetNature retrieves a nature entry by ID
func (s *Store) GetNature(id string) (*Nature, error) {
	n := &Nature{ID: id}
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
		SELECT content, kind, scope, priority, importance, source, created_at, updated_at
		FROM nature WHERE id = ?
	`, id).Scan(&n.Content, &n.Kind, &n.Scope, &n.Priority, &n.Importance, &n.Source, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	n.CreatedAt = time.Unix(createdAt, 0)
	n.UpdatedAt = time.Unix(updatedAt, 0)
	return n, nil
}

// ListNature returns all nature entries, optionally filtered by kind or scope
func (s *Store) ListNature(kind, scope string) ([]Nature, error) {
	query := "SELECT id, content, kind, scope, priority, importance, source, created_at, updated_at FROM nature"
	args := []any{}
	conditions := []string{}

	if kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, kind)
	}
	if scope != "" {
		conditions = append(conditions, "scope = ?")
		args = append(args, scope)
	}
	if len(conditions) > 0 {
		query += " WHERE " + joinConditions(conditions, " AND ")
	}
	query += " ORDER BY priority, importance DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Nature
	for rows.Next() {
		var n Nature
		var createdAt, updatedAt int64
		if err := rows.Scan(&n.ID, &n.Content, &n.Kind, &n.Scope, &n.Priority, &n.Importance, &n.Source, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		n.CreatedAt = time.Unix(createdAt, 0)
		n.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, n)
	}
	return results, nil
}

// DeleteNature removes a nature entry
func (s *Store) DeleteNature(id string) error {
	_, err := s.db.Exec("DELETE FROM nature WHERE id = ?", id)
	return err
}

// ============================================
// AGENTS
// ============================================

// Agent represents an agent entry point
type Agent struct {
	ID        string
	Name      string
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertAgent creates or updates an agent
func (s *Store) UpsertAgent(a Agent) error {
	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	_, err := s.db.Exec(`
		INSERT INTO agents (id, name, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			role = excluded.role,
			updated_at = excluded.updated_at
	`, a.ID, a.Name, a.Role, a.CreatedAt.Unix(), a.UpdatedAt.Unix())

	return err
}

// GetAgent retrieves an agent by ID
func (s *Store) GetAgent(id string) (*Agent, error) {
	a := &Agent{ID: id}
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
		SELECT name, role, created_at, updated_at FROM agents WHERE id = ?
	`, id).Scan(&a.Name, &a.Role, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	a.CreatedAt = time.Unix(createdAt, 0)
	a.UpdatedAt = time.Unix(updatedAt, 0)
	return a, nil
}

// ListAgents returns all agents
func (s *Store) ListAgents() ([]Agent, error) {
	rows, err := s.db.Query("SELECT id, name, role, created_at, updated_at FROM agents ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Agent
	for rows.Next() {
		var a Agent
		var createdAt, updatedAt int64
		if err := rows.Scan(&a.ID, &a.Name, &a.Role, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		a.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, a)
	}
	return results, nil
}

// DeleteAgent removes an agent (cascades to configs and nature links)
func (s *Store) DeleteAgent(id string) error {
	_, err := s.db.Exec("DELETE FROM agents WHERE id = ?", id)
	return err
}

// LinkNature links a nature entry to an agent
func (s *Store) LinkNature(agentID, natureID string, priority int, required bool) error {
	reqInt := 0
	if required {
		reqInt = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO agent_nature (agent_id, nature_id, priority, required)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id, nature_id) DO UPDATE SET
			priority = excluded.priority,
			required = excluded.required
	`, agentID, natureID, priority, reqInt)
	return err
}

// UnlinkNature removes a nature link from an agent
func (s *Store) UnlinkNature(agentID, natureID string) error {
	_, err := s.db.Exec("DELETE FROM agent_nature WHERE agent_id = ? AND nature_id = ?", agentID, natureID)
	return err
}

// GetAgentNature returns all nature entries linked to an agent, ordered by priority
func (s *Store) GetAgentNature(agentID string) ([]Nature, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.content, n.kind, n.scope, n.priority, n.importance, n.source, n.created_at, n.updated_at
		FROM nature n
		JOIN agent_nature an ON n.id = an.nature_id
		WHERE an.agent_id = ?
		ORDER BY an.priority, n.priority, n.importance DESC
	`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Nature
	for rows.Next() {
		var n Nature
		var createdAt, updatedAt int64
		if err := rows.Scan(&n.ID, &n.Content, &n.Kind, &n.Scope, &n.Priority, &n.Importance, &n.Source, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		n.CreatedAt = time.Unix(createdAt, 0)
		n.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, n)
	}
	return results, nil
}

// ============================================
// ORCHESTRATORS
// ============================================

// Orchestrator represents an orchestrator system (inber, openclaw, etc.)
type Orchestrator struct {
	ID           string
	DefaultAgent string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UpsertOrchestrator creates or updates an orchestrator
func (s *Store) UpsertOrchestrator(o Orchestrator) error {
	now := time.Now()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	o.UpdatedAt = now

	_, err := s.db.Exec(`
		INSERT INTO orchestrators (id, default_agent, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			default_agent = excluded.default_agent,
			updated_at = excluded.updated_at
	`, o.ID, o.DefaultAgent, o.CreatedAt.Unix(), o.UpdatedAt.Unix())

	return err
}

// GetOrchestrator retrieves an orchestrator by ID
func (s *Store) GetOrchestrator(id string) (*Orchestrator, error) {
	o := &Orchestrator{ID: id}
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
		SELECT default_agent, created_at, updated_at FROM orchestrators WHERE id = ?
	`, id).Scan(&o.DefaultAgent, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	o.CreatedAt = time.Unix(createdAt, 0)
	o.UpdatedAt = time.Unix(updatedAt, 0)
	return o, nil
}

// ListOrchestrators returns all orchestrators
func (s *Store) ListOrchestrators() ([]Orchestrator, error) {
	rows, err := s.db.Query("SELECT id, default_agent, created_at, updated_at FROM orchestrators ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Orchestrator
	for rows.Next() {
		var o Orchestrator
		var createdAt, updatedAt int64
		if err := rows.Scan(&o.ID, &o.DefaultAgent, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		o.CreatedAt = time.Unix(createdAt, 0)
		o.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, o)
	}
	return results, nil
}

// SetOrchestratorSetting sets a key-value setting for an orchestrator
func (s *Store) SetOrchestratorSetting(orchestratorID, key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO orchestrator_settings (orchestrator_id, key, value)
		VALUES (?, ?, ?)
		ON CONFLICT(orchestrator_id, key) DO UPDATE SET value = excluded.value
	`, orchestratorID, key, value)
	return err
}

// GetOrchestratorSetting gets a setting value for an orchestrator
func (s *Store) GetOrchestratorSetting(orchestratorID, key string) (string, error) {
	var value string
	err := s.db.QueryRow(`
		SELECT value FROM orchestrator_settings WHERE orchestrator_id = ? AND key = ?
	`, orchestratorID, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// GetOrchestratorSettings returns all settings for an orchestrator as a map
func (s *Store) GetOrchestratorSettings(orchestratorID string) (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT key, value FROM orchestrator_settings WHERE orchestrator_id = ?
	`, orchestratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

// ============================================
// AGENT CONFIGS (per-orchestrator)
// ============================================

// AgentConfig represents runtime configuration for an agent under an orchestrator
type AgentConfig struct {
	AgentID        string
	OrchestratorID string
	Enabled        bool
	Values         map[string]string // EAV values (model, thinking, etc.)
	Tools          []ToolConfig
	Limits         map[string]int
}

// ToolConfig represents a tool assignment
type ToolConfig struct {
	Tool     string
	Priority int
	Enabled  bool
}

// GetAgentConfig retrieves the full config for an agent under an orchestrator
func (s *Store) GetAgentConfig(agentID, orchestratorID string) (*AgentConfig, error) {
	cfg := &AgentConfig{
		AgentID:        agentID,
		OrchestratorID: orchestratorID,
		Values:         make(map[string]string),
		Limits:         make(map[string]int),
	}

	// Check if config exists and get enabled status
	var enabled int
	err := s.db.QueryRow(`
		SELECT enabled FROM agent_configs WHERE agent_id = ? AND orchestrator_id = ?
	`, agentID, orchestratorID).Scan(&enabled)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no config for agent %s under orchestrator %s", agentID, orchestratorID)
	}
	if err != nil {
		return nil, err
	}
	cfg.Enabled = enabled == 1

	// Get values
	valRows, err := s.db.Query(`
		SELECT key, value FROM agent_config_values WHERE agent_id = ? AND orchestrator_id = ?
	`, agentID, orchestratorID)
	if err != nil {
		return nil, err
	}
	defer valRows.Close()
	for valRows.Next() {
		var key, value string
		if err := valRows.Scan(&key, &value); err != nil {
			return nil, err
		}
		cfg.Values[key] = value
	}

	// Get tools
	toolRows, err := s.db.Query(`
		SELECT tool, priority, enabled FROM agent_tools WHERE agent_id = ? AND orchestrator_id = ?
		ORDER BY priority, tool
	`, agentID, orchestratorID)
	if err != nil {
		return nil, err
	}
	defer toolRows.Close()
	for toolRows.Next() {
		var tc ToolConfig
		var enabled int
		if err := toolRows.Scan(&tc.Tool, &tc.Priority, &enabled); err != nil {
			return nil, err
		}
		tc.Enabled = enabled == 1
		cfg.Tools = append(cfg.Tools, tc)
	}

	// Get limits
	limitRows, err := s.db.Query(`
		SELECT key, value FROM agent_limits WHERE agent_id = ? AND orchestrator_id = ?
	`, agentID, orchestratorID)
	if err != nil {
		return nil, err
	}
	defer limitRows.Close()
	for limitRows.Next() {
		var key string
		var value int
		if err := limitRows.Scan(&key, &value); err != nil {
			return nil, err
		}
		cfg.Limits[key] = value
	}

	return cfg, nil
}

// EnsureAgentConfig creates a config entry if it doesn't exist
func (s *Store) EnsureAgentConfig(agentID, orchestratorID string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO agent_configs (agent_id, orchestrator_id, enabled, created_at, updated_at)
		VALUES (?, ?, 1, ?, ?)
	`, agentID, orchestratorID, now, now)
	return err
}

// SetConfigValue sets a config value for an agent under an orchestrator
func (s *Store) SetConfigValue(agentID, orchestratorID, key, value string) error {
	if err := s.EnsureAgentConfig(agentID, orchestratorID); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO agent_config_values (agent_id, orchestrator_id, key, value)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id, orchestrator_id, key) DO UPDATE SET value = excluded.value
	`, agentID, orchestratorID, key, value)
	if err != nil {
		return err
	}
	// Update timestamp
	_, err = s.db.Exec(`UPDATE agent_configs SET updated_at = ? WHERE agent_id = ? AND orchestrator_id = ?`,
		time.Now().Unix(), agentID, orchestratorID)
	return err
}

// AddTool adds a tool to an agent's config
func (s *Store) AddTool(agentID, orchestratorID, tool string, priority int, enabled bool) error {
	if err := s.EnsureAgentConfig(agentID, orchestratorID); err != nil {
		return err
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO agent_tools (agent_id, orchestrator_id, tool, priority, enabled)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, orchestrator_id, tool) DO UPDATE SET
			priority = excluded.priority,
			enabled = excluded.enabled
	`, agentID, orchestratorID, tool, priority, enabledInt)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE agent_configs SET updated_at = ? WHERE agent_id = ? AND orchestrator_id = ?`,
		time.Now().Unix(), agentID, orchestratorID)
	return err
}

// RemoveTool removes a tool from an agent's config
func (s *Store) RemoveTool(agentID, orchestratorID, tool string) error {
	_, err := s.db.Exec(`DELETE FROM agent_tools WHERE agent_id = ? AND orchestrator_id = ? AND tool = ?`,
		agentID, orchestratorID, tool)
	return err
}

// SetLimit sets a limit for an agent under an orchestrator
func (s *Store) SetLimit(agentID, orchestratorID, key string, value int) error {
	if err := s.EnsureAgentConfig(agentID, orchestratorID); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO agent_limits (agent_id, orchestrator_id, key, value)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id, orchestrator_id, key) DO UPDATE SET value = excluded.value
	`, agentID, orchestratorID, key, value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE agent_configs SET updated_at = ? WHERE agent_id = ? AND orchestrator_id = ?`,
		time.Now().Unix(), agentID, orchestratorID)
	return err
}

// ============================================
// MEMORIES
// ============================================

// Memory represents a learned piece of information
type Memory struct {
	ID           string
	Content      string
	Kind         string
	Scope        string
	Importance   float64
	AccessCount  int
	LastAccessed *time.Time
	Source       string
	AgentID      string
	ProjectID    string
	ExpiresAt    *time.Time
	Tags         []string
	CreatedAt    time.Time
}

// SaveMemory stores a new memory
func (s *Store) SaveMemory(m Memory) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}

	var expiresAt interface{}
	if m.ExpiresAt != nil {
		expiresAt = m.ExpiresAt.Unix()
	}

	var lastAccessed interface{}
	if m.LastAccessed != nil {
		lastAccessed = m.LastAccessed.Unix()
	}

	_, err := s.db.Exec(`
		INSERT INTO memories (id, content, kind, scope, importance, access_count, last_accessed, source, agent_id, project_id, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.ID, m.Content, m.Kind, m.Scope, m.Importance, m.AccessCount, lastAccessed, m.Source, m.AgentID, m.ProjectID, expiresAt, m.CreatedAt.Unix())
	if err != nil {
		return err
	}

	// Insert tags
	for _, tag := range m.Tags {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO memory_tags (memory_id, tag) VALUES (?, ?)`, m.ID, tag); err != nil {
			return err
		}
	}

	return nil
}

// GetMemory retrieves a memory by ID
func (s *Store) GetMemory(id string) (*Memory, error) {
	m := &Memory{ID: id}
	var createdAt int64
	var expiresAt, lastAccessed sql.NullInt64

	err := s.db.QueryRow(`
		SELECT content, kind, scope, importance, access_count, last_accessed, source, agent_id, project_id, expires_at, created_at
		FROM memories WHERE id = ?
	`, id).Scan(&m.Content, &m.Kind, &m.Scope, &m.Importance, &m.AccessCount, &lastAccessed, &m.Source, &m.AgentID, &m.ProjectID, &expiresAt, &createdAt)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = time.Unix(createdAt, 0)
	if expiresAt.Valid {
		t := time.Unix(expiresAt.Int64, 0)
		m.ExpiresAt = &t
	}
	if lastAccessed.Valid {
		t := time.Unix(lastAccessed.Int64, 0)
		m.LastAccessed = &t
	}

	// Get tags
	rows, err := s.db.Query(`SELECT tag FROM memory_tags WHERE memory_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		m.Tags = append(m.Tags, tag)
	}

	return m, nil
}

// ListMemories returns memories filtered by agent, project, or kind
func (s *Store) ListMemories(agentID, projectID, kind string, limit int) ([]Memory, error) {
	query := `SELECT id, content, kind, scope, importance, access_count, last_accessed, source, agent_id, project_id, expires_at, created_at FROM memories`
	args := []any{}
	conditions := []string{}

	if agentID != "" {
		conditions = append(conditions, "(agent_id = ? OR agent_id IS NULL)")
		args = append(args, agentID)
	}
	if projectID != "" {
		conditions = append(conditions, "(project_id = ? OR project_id IS NULL)")
		args = append(args, projectID)
	}
	if kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, kind)
	}
	if len(conditions) > 0 {
		query += " WHERE " + joinConditions(conditions, " AND ")
	}
	query += " ORDER BY importance DESC, created_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		var m Memory
		var createdAt int64
		var expiresAt, lastAccessed sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Content, &m.Kind, &m.Scope, &m.Importance, &m.AccessCount, &lastAccessed, &m.Source, &m.AgentID, &m.ProjectID, &expiresAt, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		if expiresAt.Valid {
			t := time.Unix(expiresAt.Int64, 0)
			m.ExpiresAt = &t
		}
		if lastAccessed.Valid {
			t := time.Unix(lastAccessed.Int64, 0)
			m.LastAccessed = &t
		}
		results = append(results, m)
	}
	return results, nil
}

// DeleteMemory removes a memory
func (s *Store) DeleteMemory(id string) error {
	_, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
	return err
}

// TouchMemory increments access count and updates last_accessed
func (s *Store) TouchMemory(id string) error {
	_, err := s.db.Exec(`
		UPDATE memories SET access_count = access_count + 1, last_accessed = ? WHERE id = ?
	`, time.Now().Unix(), id)
	return err
}

// ============================================
// PROJECTS
// ============================================

// Project represents a project context
type Project struct {
	ID          string
	Name        string
	Path        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UpsertProject creates or updates a project
func (s *Store) UpsertProject(p Project) error {
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now

	_, err := s.db.Exec(`
		INSERT INTO projects (id, name, path, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			path = excluded.path,
			description = excluded.description,
			updated_at = excluded.updated_at
	`, p.ID, p.Name, p.Path, p.Description, p.CreatedAt.Unix(), p.UpdatedAt.Unix())

	return err
}

// GetProject retrieves a project by ID
func (s *Store) GetProject(id string) (*Project, error) {
	p := &Project{ID: id}
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
		SELECT name, path, description, created_at, updated_at FROM projects WHERE id = ?
	`, id).Scan(&p.Name, &p.Path, &p.Description, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)
	return p, nil
}

// ListProjects returns all projects
func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query("SELECT id, name, path, description, created_at, updated_at FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Project
	for rows.Next() {
		var p Project
		var createdAt, updatedAt int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.Description, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, p)
	}
	return results, nil
}

// LinkProjectNature links a nature entry to a project
func (s *Store) LinkProjectNature(projectID, natureID string, priority int) error {
	_, err := s.db.Exec(`
		INSERT INTO project_nature (project_id, nature_id, priority)
		VALUES (?, ?, ?)
		ON CONFLICT(project_id, nature_id) DO UPDATE SET priority = excluded.priority
	`, projectID, natureID, priority)
	return err
}

// GetProjectNature returns all nature entries linked to a project
func (s *Store) GetProjectNature(projectID string) ([]Nature, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.content, n.kind, n.scope, n.priority, n.importance, n.source, n.created_at, n.updated_at
		FROM nature n
		JOIN project_nature pn ON n.id = pn.nature_id
		WHERE pn.project_id = ?
		ORDER BY pn.priority, n.priority
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Nature
	for rows.Next() {
		var n Nature
		var createdAt, updatedAt int64
		if err := rows.Scan(&n.ID, &n.Content, &n.Kind, &n.Scope, &n.Priority, &n.Importance, &n.Source, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		n.CreatedAt = time.Unix(createdAt, 0)
		n.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, n)
	}
	return results, nil
}

// ============================================
// HELPER
// ============================================

func joinConditions(conditions []string, sep string) string {
	result := ""
	for i, c := range conditions {
		if i > 0 {
			result += sep
		}
		result += c
	}
	return result
}
