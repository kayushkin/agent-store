// Package agentstore provides a unified store for agent identity, nature, and configs.
package agentstore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// Store is the main handle for agent storage.
type Store struct {
	db       *sql.DB
	embedder *Embedder
}

// Open opens or creates the agent store at the given path.
func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	s := &Store{db: db, embedder: NewEmbedder()}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return err
	}
	// Add columns that may not exist in older databases
	migrations := []string{
		"ALTER TABLE agent_orchestrators ADD COLUMN is_default INTEGER DEFAULT 0",
		"ALTER TABLE agent_orchestrators ADD COLUMN subagent_allow TEXT",
	}
	for _, m := range migrations {
		s.db.Exec(m) // ignore "duplicate column" errors
	}
	return nil
}

func now() int64 { return time.Now().Unix() }

// ============================================
// AGENTS
// ============================================

type Agent struct {
	ID          int64
	Slug        string
	DisplayName string
	Emoji       string
	Projects    string // comma-separated
	Description string
	Role        string
	Enabled     bool
	CreatedAt   int64
	UpdatedAt   int64
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) UpsertAgent(a *Agent) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = now()
	}
	a.UpdatedAt = now()
	if a.ID > 0 {
		_, err := s.db.Exec(`
			UPDATE agents SET slug=?, display_name=?, emoji=?, projects=?, description=?, role=?, enabled=?, updated_at=?
			WHERE id=?`,
			a.Slug, a.DisplayName, a.Emoji, a.Projects, a.Description, a.Role, boolToInt(a.Enabled), a.UpdatedAt, a.ID)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO agents (slug, display_name, emoji, projects, description, role, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			display_name=excluded.display_name, emoji=excluded.emoji, projects=excluded.projects,
			description=excluded.description, role=excluded.role, enabled=excluded.enabled,
			updated_at=excluded.updated_at
	`, a.Slug, a.DisplayName, a.Emoji, a.Projects, a.Description, a.Role, boolToInt(a.Enabled), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return err
	}
	// Always fetch the canonical ID — LastInsertId is unreliable with ON CONFLICT
	return s.db.QueryRow(`SELECT id FROM agents WHERE slug=?`, a.Slug).Scan(&a.ID)
}

func scanAgent(row interface{ Scan(...any) error }) (*Agent, error) {
	a := &Agent{}
	var enabled int
	var emoji, projects, description, role *string
	err := row.Scan(&a.ID, &a.Slug, &a.DisplayName, &emoji, &projects, &description, &role, &enabled, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Enabled = enabled == 1
	if emoji != nil { a.Emoji = *emoji }
	if projects != nil { a.Projects = *projects }
	if description != nil { a.Description = *description }
	if role != nil { a.Role = *role }
	return a, nil
}

const agentCols = `id, slug, display_name, emoji, projects, description, role, enabled, created_at, updated_at`

func (s *Store) GetAgent(id int64) (*Agent, error) {
	return scanAgent(s.db.QueryRow(`SELECT `+agentCols+` FROM agents WHERE id=?`, id))
}

func (s *Store) GetAgentBySlug(slug string) (*Agent, error) {
	return scanAgent(s.db.QueryRow(`SELECT `+agentCols+` FROM agents WHERE slug=?`, slug))
}

func (s *Store) ListAgents() ([]Agent, error) {
	rows, err := s.db.Query(`SELECT ` + agentCols + ` FROM agents ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// AgentWithOrchestrator is an agent entry expanded per orchestrator.
type AgentWithOrchestrator struct {
	Slug             string
	DisplayName      string
	Emoji            string
	Projects         string
	Description      string
	Role             string
	Enabled          bool
	Orchestrator     string // orchestrator ID
	OrchestratorName string // what the orchestrator calls this agent
	Model            string
	IsDefault        bool   // true if this is the orchestrator's default agent
	OrchEmoji        string // orchestrator emoji
}

// ListAgentsExpanded returns one row per agent+orchestrator pair.
func (s *Store) ListAgentsExpanded() ([]AgentWithOrchestrator, error) {
	rows, err := s.db.Query(`
		SELECT a.slug, COALESCE(a.display_name,''), COALESCE(a.emoji,''), COALESCE(a.projects,''),
		       COALESCE(a.description,''), COALESCE(a.role,''), a.enabled,
		       ao.orchestrator_id, ao.orchestrator_agent_id, COALESCE(ao.model_primary, ''),
		       CASE WHEN o.default_agent_id = a.id THEN 1 ELSE 0 END,
		       COALESCE(o.emoji, '')
		FROM agents a
		JOIN agent_orchestrators ao ON a.id = ao.agent_id
		LEFT JOIN orchestrators o ON o.id = ao.orchestrator_id
		WHERE ao.shelved = 0
		ORDER BY ao.orchestrator_id, a.slug
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentWithOrchestrator
	for rows.Next() {
		var a AgentWithOrchestrator
		var enabled, isDefault int
		if err := rows.Scan(&a.Slug, &a.DisplayName, &a.Emoji, &a.Projects, &a.Description, &a.Role, &enabled,
			&a.Orchestrator, &a.OrchestratorName, &a.Model, &isDefault, &a.OrchEmoji); err != nil {
			return nil, err
		}
		a.Enabled = enabled != 0
		a.IsDefault = isDefault != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAgent(id int64) error {
	_, err := s.db.Exec("DELETE FROM agents WHERE id=?", id)
	return err
}

// ============================================
// ORCHESTRATORS
// ============================================

type Orchestrator struct {
	ID             string
	DisplayName    string
	DefaultAgentID *int64
	ConfigPath     string
	APIEndpoint    string
	CreatedAt      int64
	UpdatedAt      int64
}

func (s *Store) UpsertOrchestrator(o *Orchestrator) error {
	if o.CreatedAt == 0 {
		o.CreatedAt = now()
	}
	o.UpdatedAt = now()
	_, err := s.db.Exec(`
		INSERT INTO orchestrators (id, display_name, default_agent_id, config_path, api_endpoint, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			display_name=excluded.display_name, default_agent_id=excluded.default_agent_id,
			config_path=excluded.config_path, api_endpoint=excluded.api_endpoint,
			updated_at=excluded.updated_at
	`, o.ID, o.DisplayName, o.DefaultAgentID, o.ConfigPath, o.APIEndpoint, o.CreatedAt, o.UpdatedAt)
	return err
}

func (s *Store) GetOrchestrator(id string) (*Orchestrator, error) {
	o := &Orchestrator{ID: id}
	err := s.db.QueryRow(`
		SELECT display_name, default_agent_id, config_path, api_endpoint, created_at, updated_at
		FROM orchestrators WHERE id=?`, id).Scan(
		&o.DisplayName, &o.DefaultAgentID, &o.ConfigPath, &o.APIEndpoint, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Store) ListOrchestrators() ([]Orchestrator, error) {
	rows, err := s.db.Query(`SELECT id, display_name, default_agent_id, config_path, api_endpoint, created_at, updated_at FROM orchestrators ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Orchestrator
	for rows.Next() {
		var o Orchestrator
		if err := rows.Scan(&o.ID, &o.DisplayName, &o.DefaultAgentID, &o.ConfigPath, &o.APIEndpoint, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) DeleteOrchestrator(id string) error {
	_, err := s.db.Exec("DELETE FROM orchestrators WHERE id=?", id)
	return err
}

// ============================================
// AGENT ORCHESTRATORS
// ============================================

type AgentOrchestrator struct {
	AgentID             int64
	OrchestratorID      string
	OrchestratorAgentID string
	Enabled             bool
	ModelPrimary        string
	ModelFallbacks      string // JSON array
	WorkspacePath       string
	ThinkingBudget      int
	ContextBudget       int
	ContextTags         string // JSON array
	MaxTurns            int
	MaxInputTokens      int
	MaxResponseTime     int // seconds
	SystemPrompt        string
	Project             string
	Shelved             bool
	IsDefault           bool
	SubagentAllow       string // JSON array, e.g. '["*"]'
	CreatedAt           int64
	UpdatedAt           int64
}

func (s *Store) UpsertAgentOrchestrator(ao *AgentOrchestrator) error {
	if ao.CreatedAt == 0 {
		ao.CreatedAt = now()
	}
	ao.UpdatedAt = now()
	_, err := s.db.Exec(`
		INSERT INTO agent_orchestrators (agent_id, orchestrator_id, orchestrator_agent_id, enabled, model_primary, model_fallbacks, workspace_path, thinking_budget, context_budget, context_tags, max_turns, max_input_tokens, max_response_time, system_prompt, project, shelved, is_default, subagent_allow, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, orchestrator_id) DO UPDATE SET
			orchestrator_agent_id=excluded.orchestrator_agent_id, enabled=excluded.enabled,
			model_primary=excluded.model_primary, model_fallbacks=excluded.model_fallbacks,
			workspace_path=excluded.workspace_path, thinking_budget=excluded.thinking_budget,
			context_budget=excluded.context_budget, context_tags=excluded.context_tags,
			max_turns=excluded.max_turns, max_input_tokens=excluded.max_input_tokens,
			max_response_time=excluded.max_response_time, system_prompt=excluded.system_prompt,
			project=excluded.project, shelved=excluded.shelved, is_default=excluded.is_default,
			subagent_allow=excluded.subagent_allow, updated_at=excluded.updated_at
	`, ao.AgentID, ao.OrchestratorID, ao.OrchestratorAgentID, boolToInt(ao.Enabled), ao.ModelPrimary, ao.ModelFallbacks, ao.WorkspacePath, ao.ThinkingBudget, ao.ContextBudget, ao.ContextTags, ao.MaxTurns, ao.MaxInputTokens, ao.MaxResponseTime, ao.SystemPrompt, ao.Project, boolToInt(ao.Shelved), boolToInt(ao.IsDefault), ao.SubagentAllow, ao.CreatedAt, ao.UpdatedAt)
	return err
}

const aoCols = `agent_id, orchestrator_id, orchestrator_agent_id, enabled, model_primary, model_fallbacks, workspace_path, thinking_budget, context_budget, context_tags, max_turns, max_input_tokens, max_response_time, system_prompt, project, shelved, is_default, subagent_allow, created_at, updated_at`

func scanAO(row interface{ Scan(...any) error }) (AgentOrchestrator, error) {
	var ao AgentOrchestrator
	var enabled, shelved, isDefault int
	var modelFallbacks, contextTags, systemPrompt, workspacePath, project, orchestratorAgentID, subagentAllow *string
	err := row.Scan(&ao.AgentID, &ao.OrchestratorID, &orchestratorAgentID, &enabled, &ao.ModelPrimary, &modelFallbacks, &workspacePath, &ao.ThinkingBudget, &ao.ContextBudget, &contextTags, &ao.MaxTurns, &ao.MaxInputTokens, &ao.MaxResponseTime, &systemPrompt, &project, &shelved, &isDefault, &subagentAllow, &ao.CreatedAt, &ao.UpdatedAt)
	ao.Enabled = enabled == 1
	ao.Shelved = shelved == 1
	ao.IsDefault = isDefault == 1
	if orchestratorAgentID != nil { ao.OrchestratorAgentID = *orchestratorAgentID }
	if modelFallbacks != nil { ao.ModelFallbacks = *modelFallbacks }
	if workspacePath != nil { ao.WorkspacePath = *workspacePath }
	if contextTags != nil { ao.ContextTags = *contextTags }
	if systemPrompt != nil { ao.SystemPrompt = *systemPrompt }
	if project != nil { ao.Project = *project }
	if subagentAllow != nil { ao.SubagentAllow = *subagentAllow }
	return ao, err
}

func (s *Store) GetAgentOrchestrators(agentID int64) ([]AgentOrchestrator, error) {
	rows, err := s.db.Query(`SELECT `+aoCols+` FROM agent_orchestrators WHERE agent_id=? ORDER BY orchestrator_id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentOrchestrator
	for rows.Next() {
		ao, err := scanAO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ao)
	}
	return out, rows.Err()
}

func (s *Store) GetOrchestratorAgents(orchestratorID string) ([]AgentOrchestrator, error) {
	rows, err := s.db.Query(`SELECT `+aoCols+` FROM agent_orchestrators WHERE orchestrator_id=? ORDER BY agent_id`, orchestratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentOrchestrator
	for rows.Next() {
		ao, err := scanAO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ao)
	}
	return out, rows.Err()
}

// GetAgentByOrchestratorID finds an agent by its orchestrator-specific ID.
func (s *Store) GetAgentByOrchestratorID(orchestratorID, orchAgentID string) (*Agent, error) {
	var agentID int64
	err := s.db.QueryRow(`SELECT agent_id FROM agent_orchestrators WHERE orchestrator_id=? AND orchestrator_agent_id=?`,
		orchestratorID, orchAgentID).Scan(&agentID)
	if err != nil {
		return nil, err
	}
	return s.GetAgent(agentID)
}

func (s *Store) DeleteAgentOrchestrator(agentID int64, orchestratorID string) error {
	_, err := s.db.Exec("DELETE FROM agent_orchestrators WHERE agent_id=? AND orchestrator_id=?", agentID, orchestratorID)
	return err
}

// ============================================
// AGENT TOOLS
// ============================================

func (s *Store) SetAgentTools(agentID int64, orchestratorID string, tools []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM agent_tools WHERE agent_id=? AND orchestrator_id=?", agentID, orchestratorID); err != nil {
		return err
	}
	for _, t := range tools {
		if _, err := tx.Exec("INSERT INTO agent_tools (agent_id, orchestrator_id, tool_name) VALUES (?, ?, ?)", agentID, orchestratorID, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListAgentTools(agentID int64, orchestratorID string) ([]string, error) {
	rows, err := s.db.Query("SELECT tool_name FROM agent_tools WHERE agent_id=? AND orchestrator_id=? ORDER BY tool_name", agentID, orchestratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ============================================
// FULL AGENT CONFIG (for orchestrators)
// ============================================

// FullAgentConfig is everything an orchestrator needs for one agent.
type FullAgentConfig struct {
	Slug        string   `json:"slug"`
	DisplayName string   `json:"name"`
	Role        string   `json:"role"`
	Emoji       string   `json:"emoji,omitempty"`
	Model       string   `json:"model"`
	Thinking    int      `json:"thinking,omitempty"`
	Tools       []string `json:"tools"`
	ContextTags []string `json:"context_tags,omitempty"`
	ContextBudget int    `json:"context_budget,omitempty"`
	MaxTurns      int    `json:"max_turns,omitempty"`
	MaxInputTokens int   `json:"max_input_tokens,omitempty"`
	MaxResponseTime int  `json:"max_response_time,omitempty"`
	Workspace   string   `json:"workspace,omitempty"`
	Project     string   `json:"project,omitempty"`
	Projects    string   `json:"projects,omitempty"`
	SystemPrompt string  `json:"system_prompt,omitempty"`
	Shelved     bool     `json:"shelved,omitempty"`
	// Nature content
	Soul       string `json:"soul,omitempty"`
	Principles string `json:"principles,omitempty"`
	Values     string `json:"values,omitempty"`
	UserContext string `json:"user_context,omitempty"`
}

func (s *Store) GetFullAgentConfig(slug string, orchestratorID string) (*FullAgentConfig, error) {
	a, err := s.GetAgentBySlug(slug)
	if err != nil {
		return nil, fmt.Errorf("agent not found: %s", slug)
	}

	// Get orchestrator config
	row := s.db.QueryRow(`SELECT `+aoCols+` FROM agent_orchestrators WHERE agent_id=? AND orchestrator_id=?`, a.ID, orchestratorID)
	ao, err := scanAO(row)
	if err != nil {
		return nil, fmt.Errorf("agent %s not registered with orchestrator %s", slug, orchestratorID)
	}

	tools, _ := s.ListAgentTools(a.ID, orchestratorID)
	if tools == nil {
		tools = []string{}
	}

	var contextTags []string
	if ao.ContextTags != "" {
		json.Unmarshal([]byte(ao.ContextTags), &contextTags)
	}

	cfg := &FullAgentConfig{
		Slug:            a.Slug,
		DisplayName:     a.DisplayName,
		Role:            a.Role,
		Emoji:           a.Emoji,
		Model:           ao.ModelPrimary,
		Thinking:        ao.ThinkingBudget,
		Tools:           tools,
		ContextTags:     contextTags,
		ContextBudget:   ao.ContextBudget,
		MaxTurns:        ao.MaxTurns,
		MaxInputTokens:  ao.MaxInputTokens,
		MaxResponseTime: ao.MaxResponseTime,
		Workspace:       ao.WorkspacePath,
		Project:         ao.Project,
		Projects:        a.Projects,
		SystemPrompt:    ao.SystemPrompt,
		Shelved:         ao.Shelved,
	}

	// Load nature content
	natures, _ := s.ListAgentNature(a.ID)
	for _, n := range natures {
		switch n.Kind {
		case "identity":
			cfg.Soul = n.Content
		case "principle":
			cfg.Principles = n.Content
		case "value":
			cfg.Values = n.Content
		case "user":
			cfg.UserContext = n.Content
		}
	}

	return cfg, nil
}

// GetAllAgentConfigs returns full configs for all agents registered with an orchestrator.
func (s *Store) GetAllAgentConfigs(orchestratorID string) ([]FullAgentConfig, string, error) {
	// Get default agent slug
	var defaultSlug string
	orch, err := s.GetOrchestrator(orchestratorID)
	if err == nil && orch.DefaultAgentID != nil {
		if da, err := s.GetAgent(*orch.DefaultAgentID); err == nil {
			defaultSlug = da.Slug
		}
	}

	aos, err := s.GetOrchestratorAgents(orchestratorID)
	if err != nil {
		return nil, "", err
	}

	var configs []FullAgentConfig
	for _, ao := range aos {
		a, err := s.GetAgent(ao.AgentID)
		if err != nil {
			continue
		}
		cfg, err := s.GetFullAgentConfig(a.Slug, orchestratorID)
		if err != nil {
			continue
		}
		configs = append(configs, *cfg)
	}
	return configs, defaultSlug, nil
}

// ============================================
// AGENT SYSTEM PROMPT REFS
// ============================================

type AgentSystemPromptRef struct {
	OrchestratorID    string
	HostAgentID       int64
	ReferencedAgentID int64
	PromptLocation    string
	CreatedAt         int64
}

func (s *Store) UpsertSystemPromptRef(r *AgentSystemPromptRef) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = now()
	}
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO agent_system_prompt_refs (orchestrator_id, host_agent_id, referenced_agent_id, prompt_location, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, r.OrchestratorID, r.HostAgentID, r.ReferencedAgentID, r.PromptLocation, r.CreatedAt)
	return err
}

func (s *Store) ListSystemPromptRefs(hostAgentID int64) ([]AgentSystemPromptRef, error) {
	rows, err := s.db.Query(`
		SELECT orchestrator_id, host_agent_id, referenced_agent_id, prompt_location, created_at
		FROM agent_system_prompt_refs WHERE host_agent_id=?`, hostAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentSystemPromptRef
	for rows.Next() {
		var r AgentSystemPromptRef
		if err := rows.Scan(&r.OrchestratorID, &r.HostAgentID, &r.ReferencedAgentID, &r.PromptLocation, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSystemPromptRef(orchID string, hostID, refID int64, location string) error {
	_, err := s.db.Exec(`DELETE FROM agent_system_prompt_refs WHERE orchestrator_id=? AND host_agent_id=? AND referenced_agent_id=? AND prompt_location=?`,
		orchID, hostID, refID, location)
	return err
}

// ============================================
// AGENT NATURE
// ============================================

type AgentNature struct {
	ID          int64
	AgentID     int64
	Kind        string // identity, principle, value, user, project
	Content     string
	ContentHash string
	Priority    int
	SourcePath  string
	CreatedAt   int64
	UpdatedAt   int64
}

func (s *Store) UpsertAgentNature(n *AgentNature) error {
	if n.CreatedAt == 0 {
		n.CreatedAt = now()
	}
	n.UpdatedAt = now()
	if n.ID > 0 {
		_, err := s.db.Exec(`
			UPDATE agent_nature SET agent_id=?, kind=?, content=?, content_hash=?, priority=?, source_path=?, updated_at=?
			WHERE id=?`, n.AgentID, n.Kind, n.Content, n.ContentHash, n.Priority, n.SourcePath, n.UpdatedAt, n.ID)
		return err
	}
	res, err := s.db.Exec(`
		INSERT INTO agent_nature (agent_id, kind, content, content_hash, priority, source_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, n.AgentID, n.Kind, n.Content, n.ContentHash, n.Priority, n.SourcePath, n.CreatedAt, n.UpdatedAt)
	if err != nil {
		return err
	}
	n.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) GetAgentNature(id int64) (*AgentNature, error) {
	n := &AgentNature{}
	err := s.db.QueryRow(`
		SELECT id, agent_id, kind, content, content_hash, priority, source_path, created_at, updated_at
		FROM agent_nature WHERE id=?`, id).Scan(
		&n.ID, &n.AgentID, &n.Kind, &n.Content, &n.ContentHash, &n.Priority, &n.SourcePath, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Store) ListAgentNature(agentID int64) ([]AgentNature, error) {
	rows, err := s.db.Query(`
		SELECT id, agent_id, kind, content, content_hash, priority, source_path, created_at, updated_at
		FROM agent_nature WHERE agent_id=? ORDER BY priority, id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentNature
	for rows.Next() {
		var n AgentNature
		if err := rows.Scan(&n.ID, &n.AgentID, &n.Kind, &n.Content, &n.ContentHash, &n.Priority, &n.SourcePath, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAgentNature(id int64) error {
	_, err := s.db.Exec("DELETE FROM agent_nature WHERE id=?", id)
	return err
}

// ============================================
// AGENT NAME ALIASES
// ============================================

type AgentNameAlias struct {
	AgentID   int64
	Alias     string
	Context   string
	CreatedAt int64
}

func (s *Store) UpsertAlias(a *AgentNameAlias) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = now()
	}
	_, err := s.db.Exec(`
		INSERT INTO agent_name_aliases (agent_id, alias, context, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id, alias) DO UPDATE SET context=excluded.context
	`, a.AgentID, a.Alias, a.Context, a.CreatedAt)
	return err
}

func (s *Store) GetAgentAliases(agentID int64) ([]AgentNameAlias, error) {
	rows, err := s.db.Query(`SELECT agent_id, alias, context, created_at FROM agent_name_aliases WHERE agent_id=?`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentNameAlias
	for rows.Next() {
		var a AgentNameAlias
		if err := rows.Scan(&a.AgentID, &a.Alias, &a.Context, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAlias(agentID int64, alias string) error {
	_, err := s.db.Exec("DELETE FROM agent_name_aliases WHERE agent_id=? AND alias=?", agentID, alias)
	return err
}

// ResolveAgentName looks up an agent by slug or alias and returns it.
func (s *Store) ResolveAgentName(name string) (*Agent, error) {
	// Try slug first
	a, err := s.GetAgentBySlug(name)
	if err == nil {
		return a, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Try alias
	var agentID int64
	err = s.db.QueryRow(`SELECT agent_id FROM agent_name_aliases WHERE alias=?`, name).Scan(&agentID)
	if err != nil {
		return nil, fmt.Errorf("agent not found: %q", name)
	}
	return s.GetAgent(agentID)
}

// ============================================
// FILE DISTRIBUTIONS
// ============================================

type FileDistribution struct {
	ID              int64
	AgentID         int64
	OrchestratorID  string
	FilePath        string
	ContentHash     string
	DistributedAt   int64
	SourceNatureIDs string // JSON array
}

func (s *Store) RecordDistribution(agentID int64, orchID string, filePath string, contentHash string, sourceNatureIDs []int64) (int64, error) {
	var idsJSON string
	if len(sourceNatureIDs) > 0 {
		b, _ := json.Marshal(sourceNatureIDs)
		idsJSON = string(b)
	}
	res, err := s.db.Exec(`
		INSERT INTO file_distributions (agent_id, orchestrator_id, file_path, content_hash, distributed_at, source_nature_ids)
		VALUES (?, ?, ?, ?, ?, ?)
	`, agentID, orchID, filePath, contentHash, now(), idsJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetLatestDistribution(filePath string) (*FileDistribution, error) {
	d := &FileDistribution{}
	err := s.db.QueryRow(`
		SELECT id, agent_id, orchestrator_id, file_path, content_hash, distributed_at, COALESCE(source_nature_ids,'')
		FROM file_distributions WHERE file_path=? ORDER BY distributed_at DESC LIMIT 1`, filePath).Scan(
		&d.ID, &d.AgentID, &d.OrchestratorID, &d.FilePath, &d.ContentHash, &d.DistributedAt, &d.SourceNatureIDs)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) ListDistributions(agentID int64) ([]FileDistribution, error) {
	rows, err := s.db.Query(`
		SELECT id, agent_id, orchestrator_id, file_path, content_hash, distributed_at, COALESCE(source_nature_ids,'')
		FROM file_distributions WHERE agent_id=? ORDER BY distributed_at DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileDistribution
	for rows.Next() {
		var d FileDistribution
		if err := rows.Scan(&d.ID, &d.AgentID, &d.OrchestratorID, &d.FilePath, &d.ContentHash, &d.DistributedAt, &d.SourceNatureIDs); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDistribution(id int64) error {
	_, err := s.db.Exec("DELETE FROM file_distributions WHERE id=?", id)
	return err
}

// ============================================
// FILE SCANS
// ============================================

type FileScan struct {
	ID                 int64
	FileDistributionID int64
	ScannedAt          int64
	CurrentHash        string
	Status             string // unchanged, modified, missing, new
	DiffSummary        string
	Ingested           bool
}

func (s *Store) RecordScan(distributionID int64, currentHash string, status string, diffSummary string) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO file_scans (file_distribution_id, scanned_at, current_hash, status, diff_summary)
		VALUES (?, ?, ?, ?, ?)
	`, distributionID, now(), currentHash, status, diffSummary)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) MarkIngested(scanID int64) error {
	_, err := s.db.Exec(`UPDATE file_scans SET ingested=1 WHERE id=?`, scanID)
	return err
}

func (s *Store) GetPendingScans() ([]FileDistribution, error) {
	// Distributions that have never been scanned, or whose latest scan is older than their distribution
	rows, err := s.db.Query(`
		SELECT d.id, d.agent_id, d.orchestrator_id, d.file_path, d.content_hash, d.distributed_at, COALESCE(d.source_nature_ids,'')
		FROM file_distributions d
		LEFT JOIN file_scans fs ON fs.file_distribution_id = d.id
		GROUP BY d.id
		HAVING MAX(fs.scanned_at) IS NULL OR MAX(fs.scanned_at) < d.distributed_at
		ORDER BY d.distributed_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileDistribution
	for rows.Next() {
		var d FileDistribution
		if err := rows.Scan(&d.ID, &d.AgentID, &d.OrchestratorID, &d.FilePath, &d.ContentHash, &d.DistributedAt, &d.SourceNatureIDs); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetModifiedFiles() ([]FileScan, error) {
	rows, err := s.db.Query(`
		SELECT id, file_distribution_id, scanned_at, current_hash, status, COALESCE(diff_summary,''), ingested
		FROM file_scans WHERE status='modified' AND ingested=0 ORDER BY scanned_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileScan
	for rows.Next() {
		var fs FileScan
		var ingested int
		if err := rows.Scan(&fs.ID, &fs.FileDistributionID, &fs.ScannedAt, &fs.CurrentHash, &fs.Status, &fs.DiffSummary, &ingested); err != nil {
			return nil, err
		}
		fs.Ingested = ingested == 1
		out = append(out, fs)
	}
	return out, rows.Err()
}

// ============================================
// MEMORIES
// ============================================
//
// TODO: This memory implementation is superseded by the memory/ package.
// These functions are kept for compatibility with server.go endpoints.
// Consider migrating server.go to use the memory/ package directly.

type Memory struct {
	ID           string
	Content      string
	Summary      string
	OriginalID   string
	Kind         string
	Scope        string
	Importance   float64
	AccessCount  int
	LastAccessed *int64
	Source       string
	AgentID      *int64
	ProjectID    *int64
	ExpiresAt    *int64
	Embedding    []float64
	Tokens       int
	Tags         []string
	CreatedAt    int64
}

func (s *Store) SaveMemory(m *Memory) error {
	if m.CreatedAt == 0 {
		m.CreatedAt = now()
	}
	if m.Importance == 0 {
		m.Importance = 0.5
	}
	if m.Tokens == 0 && m.Content != "" {
		m.Tokens = (len(m.Content) + 2) / 3
	}

	// Generate embedding if not provided
	if len(m.Embedding) == 0 {
		m.Embedding = s.embedder.Embed(m.Content)
	}
	var embJSON []byte
	if len(m.Embedding) > 0 {
		embJSON, _ = json.Marshal(m.Embedding)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO memories (id, content, summary, original_id, kind, scope, importance, access_count, last_accessed, source, agent_id, project_id, expires_at, embedding, tokens, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content=excluded.content, summary=excluded.summary, kind=excluded.kind, scope=excluded.scope,
			importance=excluded.importance, source=excluded.source,
			agent_id=excluded.agent_id, project_id=excluded.project_id,
			expires_at=excluded.expires_at, embedding=excluded.embedding, tokens=excluded.tokens
	`, m.ID, m.Content, nullStr(m.Summary), nullStr(m.OriginalID), m.Kind, m.Scope, m.Importance, m.AccessCount, m.LastAccessed, m.Source, m.AgentID, m.ProjectID, m.ExpiresAt, embJSON, m.Tokens, m.CreatedAt)
	if err != nil {
		return err
	}

	// Replace tags
	if _, err := tx.Exec("DELETE FROM memory_tags WHERE memory_id=?", m.ID); err != nil {
		return err
	}
	for _, tag := range m.Tags {
		if _, err := tx.Exec(`INSERT INTO memory_tags (memory_id, tag) VALUES (?, ?)`, m.ID, tag); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// SearchMemories finds memories matching the query using embedding similarity.
// Results are ranked by similarity * importance * recency.
func (s *Store) SearchMemories(query string, agentID *int64, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 10
	}
	queryEmb := s.embedder.Embed(query)
	nowTs := now()

	q := `SELECT id, content, summary, original_id, kind, scope, importance, access_count, last_accessed, source, agent_id, project_id, expires_at, embedding, tokens, created_at
		FROM memories WHERE importance > 0 AND (expires_at IS NULL OR expires_at > ?)`
	args := []any{nowTs}
	if agentID != nil {
		q += " AND agent_id = ?"
		args = append(args, *agentID)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		m     Memory
		score float64
	}
	var candidates []scored

	for rows.Next() {
		m, err := scanMemoryRow(rows)
		if err != nil {
			continue
		}
		similarity := CosineSimilarity(queryEmb, m.Embedding)
		var daysSinceAccess float64
		if m.LastAccessed != nil {
			daysSinceAccess = float64(nowTs-*m.LastAccessed) / 86400
		} else {
			daysSinceAccess = float64(nowTs-m.CreatedAt) / 86400
		}
		recency := math.Pow(0.99, daysSinceAccess)
		score := similarity * m.Importance * recency
		candidates = append(candidates, scored{m: m, score: score})
	}

	// Sort descending
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	// Fetch tags
	result := make([]Memory, len(candidates))
	for i, c := range candidates {
		c.m.Tags = s.getMemoryTags(c.m.ID)
		result[i] = c.m
	}
	return result, nil
}

func scanMemoryRow(row interface{ Scan(...any) error }) (Memory, error) {
	var m Memory
	var summary, originalID sql.NullString
	var lastAccessed, expiresAt sql.NullInt64
	var agentID, projectID sql.NullInt64
	var embJSON []byte

	err := row.Scan(&m.ID, &m.Content, &summary, &originalID, &m.Kind, &m.Scope,
		&m.Importance, &m.AccessCount, &lastAccessed, &m.Source,
		&agentID, &projectID, &expiresAt, &embJSON, &m.Tokens, &m.CreatedAt)
	if err != nil {
		return m, err
	}
	m.Summary = summary.String
	m.OriginalID = originalID.String
	if lastAccessed.Valid {
		m.LastAccessed = &lastAccessed.Int64
	}
	if expiresAt.Valid {
		m.ExpiresAt = &expiresAt.Int64
	}
	if agentID.Valid {
		m.AgentID = &agentID.Int64
	}
	if projectID.Valid {
		m.ProjectID = &projectID.Int64
	}
	if len(embJSON) > 0 {
		json.Unmarshal(embJSON, &m.Embedding)
	}
	return m, nil
}

func (s *Store) getMemoryTags(id string) []string {
	rows, err := s.db.Query("SELECT tag FROM memory_tags WHERE memory_id=?", id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tags = append(tags, t)
	}
	return tags
}

// ForgetMemory soft-deletes a memory by setting importance to 0.
func (s *Store) ForgetMemory(id string) error {
	res, err := s.db.Exec("UPDATE memories SET importance = 0 WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory not found: %s", id)
	}
	return nil
}

// ExpandMemory retrieves full memory details by ID, updating access tracking.
func (s *Store) ExpandMemory(id string) (*Memory, error) {
	m, err := s.GetMemory(id)
	if err != nil {
		return nil, err
	}
	// Update access tracking
	ts := now()
	s.db.Exec("UPDATE memories SET access_count=access_count+1, last_accessed=?, importance=MIN(1.0, importance*1.01) WHERE id=?", ts, id)
	m.AccessCount++
	m.LastAccessed = &ts
	return m, nil
}

// DecayMemories applies time-based decay to all memories not accessed recently.
func (s *Store) DecayMemories() (int64, error) {
	dayAgo := now() - 86400
	res, err := s.db.Exec("UPDATE memories SET importance = importance * 0.99 WHERE last_accessed < ?", dayAgo)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneMemories removes memories with importance near zero.
func (s *Store) PruneMemories(threshold float64) (int64, error) {
	if threshold <= 0 {
		threshold = 0.01
	}
	res, err := s.db.Exec("DELETE FROM memories WHERE importance > 0 AND importance < ?", threshold)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CompactMemories groups old low-access memories by tag and merges them.
func (s *Store) CompactMemories(minAgeSec int64, minAccessCount int) ([]CompactionResult, error) {
	if minAgeSec <= 0 {
		minAgeSec = 7 * 86400 // 7 days
	}
	if minAccessCount <= 0 {
		minAccessCount = 3
	}
	cutoff := now() - minAgeSec

	rows, err := s.db.Query(`SELECT id, content, importance, access_count, created_at
		FROM memories WHERE created_at < ? AND access_count < ? AND importance < 0.7 AND importance > 0`, cutoff, minAccessCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type candidate struct {
		id, content string
	}
	var candidates []candidate
	var ids []string
	for rows.Next() {
		var id, content string
		var importance float64
		var accessCount int
		var createdAt int64
		if err := rows.Scan(&id, &content, &importance, &accessCount, &createdAt); err != nil {
			continue
		}
		candidates = append(candidates, candidate{id, content})
		ids = append(ids, id)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// Fetch tags
	tagMap := make(map[string][]string)
	if len(ids) > 0 {
		placeholders := make([]string, len(ids))
		args := make([]any, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			args[i] = id
		}
		tq := fmt.Sprintf("SELECT memory_id, tag FROM memory_tags WHERE memory_id IN (%s)", strings.Join(placeholders, ","))
		trows, err := s.db.Query(tq, args...)
		if err == nil {
			defer trows.Close()
			for trows.Next() {
				var mid, tag string
				trows.Scan(&mid, &tag)
				tagMap[mid] = append(tagMap[mid], tag)
			}
		}
	}

	// Group by primary tag
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
			continue
		}
		var parts, origIDs []string
		for _, m := range members {
			parts = append(parts, m.content)
			origIDs = append(origIDs, m.id)
		}
		combined := strings.Join(parts, "\n---\n")
		if len(combined) > 2000 {
			combined = combined[:2000] + "..."
		}
		var allTags []string
		for t := range groupTags[groupKey] {
			allTags = append(allTags, t)
		}

		newID := fmt.Sprintf("compact-%s-%d", groupKey, time.Now().UnixNano())
		newMem := &Memory{
			ID:         newID,
			Content:    fmt.Sprintf("[Compacted from %d memories]\n%s", len(members), combined),
			Tags:       allTags,
			Importance: 0.5,
			Source:     "compaction",
			OriginalID: origIDs[0],
		}
		if err := s.SaveMemory(newMem); err != nil {
			continue
		}
		for _, id := range origIDs {
			s.db.Exec("UPDATE memories SET importance = 0 WHERE id = ?", id)
		}
		results = append(results, CompactionResult{OriginalIDs: origIDs, NewID: newID, Tags: allTags, Count: len(members)})
	}
	return results, nil
}

// CompactionResult describes what was compacted.
type CompactionResult struct {
	OriginalIDs []string `json:"original_ids"`
	NewID       string   `json:"new_id"`
	Tags        []string `json:"tags"`
	Count       int      `json:"count"`
}

func (s *Store) GetMemory(id string) (*Memory, error) {
	row := s.db.QueryRow(`SELECT id, content, summary, original_id, kind, scope, importance, access_count, last_accessed, source, agent_id, project_id, expires_at, embedding, tokens, created_at
		FROM memories WHERE id=?`, id)
	m, err := scanMemoryRow(row)
	if err != nil {
		return nil, err
	}
	m.Tags = s.getMemoryTags(m.ID)
	return &m, nil
}

func (s *Store) DeleteMemory(id string) error {
	_, err := s.db.Exec("DELETE FROM memories WHERE id=?", id)
	return err
}

// ============================================
// PROJECTS
// ============================================

type Project struct {
	ID          int64
	Slug        string
	Name        string
	Path        string
	Description string
	CreatedAt   int64
	UpdatedAt   int64
}

func (s *Store) UpsertProject(p *Project) error {
	if p.CreatedAt == 0 {
		p.CreatedAt = now()
	}
	p.UpdatedAt = now()
	if p.ID > 0 {
		_, err := s.db.Exec(`
			UPDATE projects SET slug=?, name=?, path=?, description=?, updated_at=? WHERE id=?`,
			p.Slug, p.Name, p.Path, p.Description, p.UpdatedAt, p.ID)
		return err
	}
	res, err := s.db.Exec(`
		INSERT INTO projects (slug, name, path, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			name=excluded.name, path=excluded.path, description=excluded.description, updated_at=excluded.updated_at
	`, p.Slug, p.Name, p.Path, p.Description, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if id > 0 {
		p.ID = id
	} else {
		return s.db.QueryRow(`SELECT id FROM projects WHERE slug=?`, p.Slug).Scan(&p.ID)
	}
	return nil
}

func (s *Store) GetProject(id int64) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRow(`SELECT id, slug, name, path, description, created_at, updated_at FROM projects WHERE id=?`, id).Scan(
		&p.ID, &p.Slug, &p.Name, &p.Path, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) GetProjectBySlug(slug string) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRow(`SELECT id, slug, name, path, description, created_at, updated_at FROM projects WHERE slug=?`, slug).Scan(
		&p.ID, &p.Slug, &p.Name, &p.Path, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query("SELECT id, slug, name, path, description, created_at, updated_at FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Path, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProject(id int64) error {
	_, err := s.db.Exec("DELETE FROM projects WHERE id=?", id)
	return err
}

// ============================================
// PROJECT NATURE
// ============================================

type ProjectNature struct {
	ProjectID int64
	NatureID  int64
	Priority  int
}

func (s *Store) UpsertProjectNature(pn *ProjectNature) error {
	_, err := s.db.Exec(`
		INSERT INTO project_nature (project_id, nature_id, priority)
		VALUES (?, ?, ?)
		ON CONFLICT(project_id, nature_id) DO UPDATE SET priority=excluded.priority
	`, pn.ProjectID, pn.NatureID, pn.Priority)
	return err
}

func (s *Store) ListProjectNature(projectID int64) ([]ProjectNature, error) {
	rows, err := s.db.Query(`SELECT project_id, nature_id, priority FROM project_nature WHERE project_id=? ORDER BY priority`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectNature
	for rows.Next() {
		var pn ProjectNature
		if err := rows.Scan(&pn.ProjectID, &pn.NatureID, &pn.Priority); err != nil {
			return nil, err
		}
		out = append(out, pn)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProjectNature(projectID, natureID int64) error {
	_, err := s.db.Exec("DELETE FROM project_nature WHERE project_id=? AND nature_id=?", projectID, natureID)
	return err
}

// ============================================
// HELPERS
// ============================================

// ============================================
// AGENT STATUS
// ============================================

// AgentStatus represents the runtime status of an agent on an orchestrator.
type AgentStatus struct {
	AgentSlug    string  `json:"agent_slug"`
	DisplayName  string  `json:"display_name"`
	Emoji        string  `json:"emoji"`
	Orchestrator string  `json:"orchestrator"`
	Status       string  `json:"status"`
	Task         *string `json:"task,omitempty"`
	SessionID    *string `json:"session_id,omitempty"`
	StartedAt    *int64  `json:"started_at,omitempty"`
	UpdatedAt    int64   `json:"updated_at"`
}

// SetStatus upserts an agent status row. Sets started_at when transitioning to "working".
func (s *Store) SetStatus(agentSlug, orchestratorID, status, task, sessionID string) error {
	agent, err := s.ResolveAgentName(agentSlug)
	if err != nil {
		return err
	}
	n := now()
	var taskPtr, sessionPtr *string
	if task != "" {
		taskPtr = &task
	}
	if sessionID != "" {
		sessionPtr = &sessionID
	}
	_, err = s.db.Exec(`
		INSERT INTO agent_status (agent_id, orchestrator_id, status, task, session_id, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, orchestrator_id) DO UPDATE SET
			status=excluded.status, task=excluded.task, session_id=excluded.session_id,
			started_at=CASE WHEN agent_status.status != 'working' AND excluded.status = 'working' THEN excluded.updated_at ELSE agent_status.started_at END,
			updated_at=excluded.updated_at
	`, agent.ID, orchestratorID, status, taskPtr, sessionPtr, n, n)
	return err
}

// ClearStatus sets an agent's status to idle and clears task/session.
func (s *Store) ClearStatus(agentSlug, orchestratorID string) error {
	agent, err := s.ResolveAgentName(agentSlug)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE agent_status SET status='idle', task=NULL, session_id=NULL, updated_at=?
		WHERE agent_id=? AND orchestrator_id=?
	`, now(), agent.ID, orchestratorID)
	return err
}

// GetStatus returns the status of an agent across all orchestrators.
func (s *Store) GetStatus(agentSlug string) ([]AgentStatus, error) {
	agent, err := s.GetAgentBySlug(agentSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT a.slug, a.display_name, COALESCE(a.emoji,''), s.orchestrator_id, s.status, s.task, s.session_id, s.started_at, s.updated_at
		FROM agent_status s JOIN agents a ON a.id = s.agent_id
		WHERE s.agent_id=?`, agent.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentStatus
	for rows.Next() {
		var st AgentStatus
		if err := rows.Scan(&st.AgentSlug, &st.DisplayName, &st.Emoji, &st.Orchestrator, &st.Status, &st.Task, &st.SessionID, &st.StartedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ListStatuses returns all agent statuses.
func (s *Store) ListStatuses() ([]AgentStatus, error) {
	rows, err := s.db.Query(`
		SELECT a.slug, a.display_name, COALESCE(a.emoji,''), s.orchestrator_id, s.status, s.task, s.session_id, s.started_at, s.updated_at
		FROM agent_status s JOIN agents a ON a.id = s.agent_id
		ORDER BY a.slug, s.orchestrator_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentStatus
	for rows.Next() {
		var st AgentStatus
		if err := rows.Scan(&st.AgentSlug, &st.DisplayName, &st.Emoji, &st.Orchestrator, &st.Status, &st.Task, &st.SessionID, &st.StartedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ResolveOrchestratorAgent finds the canonical agent slug given an orchestrator-specific agent name.
func (s *Store) ResolveOrchestratorAgent(orchestratorID, orchAgentName string) (string, error) {
	var slug string
	err := s.db.QueryRow(`
		SELECT a.slug FROM agents a
		JOIN agent_orchestrators ao ON a.id = ao.agent_id
		WHERE ao.orchestrator_id=? AND ao.orchestrator_agent_id=?
	`, orchestratorID, orchAgentName).Scan(&slug)
	if err != nil {
		return "", fmt.Errorf("no agent found for orchestrator=%s agent=%s", orchestratorID, orchAgentName)
	}
	return slug, nil
}

// Int64Ptr is a convenience for creating *int64 values.
func Int64Ptr(v int64) *int64 { return &v }

// ParseSourceNatureIDs parses the JSON array stored in file_distributions.source_nature_ids.
func ParseSourceNatureIDs(jsonStr string) []int64 {
	if jsonStr == "" {
		return nil
	}
	// Try JSON array first
	var ids []int64
	if err := json.Unmarshal([]byte(jsonStr), &ids); err == nil {
		return ids
	}
	// Try comma-separated as fallback
	var out []int64
	for _, s := range strings.Split(jsonStr, ",") {
		s = strings.TrimSpace(s)
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}
