// Migration tool: inber agents.json + soul.md files → agent-store database
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	agentstore "github.com/kayushkin/agent-store"
)

// inberAgentsFile matches the structure of agents.json
type inberAgentsFile struct {
	Default string                   `json:"default"`
	Tiers   *inberTiersConfig        `json:"tiers,omitempty"`
	Agents  map[string]*inberAgentConfig `json:"agents"`
}

type inberTiersConfig struct {
	High  []string `json:"high"`
	Low   []string `json:"low"`
	Delay int      `json:"delay,omitempty"`
	Grace int      `json:"grace,omitempty"`
}

type inberAgentConfig struct {
	Name     string           `json:"name"`
	Role     string           `json:"role"`
	Model    string           `json:"model"`
	Thinking int64            `json:"thinking"`
	Tools    []string         `json:"tools"`
	Context  inberContextConfig `json:"context"`
	Limits   *inberAgentLimits `json:"limits,omitempty"`
}

type inberContextConfig struct {
	Tags   []string `json:"tags"`
	Budget int      `json:"budget"`
}

type inberAgentLimits struct {
	MaxTurns       int `json:"maxTurns,omitempty"`
	MaxInputTokens int `json:"maxInputTokens,omitempty"`
}

func main() {
	inberPath := os.Getenv("INBER_PATH")
	if inberPath == "" {
		inberPath = os.ExpandEnv("$HOME/life/repos/inber")
	}

	// Open agent-store
	store, err := agentstore.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open agent-store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Read agents.json
	agentsPath := filepath.Join(inberPath, "agents.json")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read %s: %v\n", agentsPath, err)
		os.Exit(1)
	}

	var af inberAgentsFile
	if err := json.Unmarshal(data, &af); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse agents.json: %v\n", err)
		os.Exit(1)
	}

	agentsDir := filepath.Join(inberPath, "agents")

	// Migrate global nature (principles, values, user)
	fmt.Println("Migrating global nature...")
	if err := migrateGlobalNature(store, agentsDir); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to migrate global nature: %v\n", err)
		os.Exit(1)
	}

	// Create orchestrator
	fmt.Println("Creating orchestrator 'inber'...")
	if err := store.UpsertOrchestrator(agentstore.Orchestrator{
		ID:           "inber",
		DefaultAgent: af.Default,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create orchestrator: %v\n", err)
		os.Exit(1)
	}

	// Migrate tier settings
	if af.Tiers != nil {
		if err := migrateTiers(store, af.Tiers); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to migrate tiers: %v\n", err)
			os.Exit(1)
		}
	}

	// Migrate agents
	fmt.Printf("Migrating %d agents...\n", len(af.Agents))
	for id, cfg := range af.Agents {
		fmt.Printf("  - %s (%s)\n", id, cfg.Name)
		if err := migrateAgent(store, agentsDir, id, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to migrate agent %s: %v\n", id, err)
			os.Exit(1)
		}
	}

	fmt.Println("\nMigration complete!")
	fmt.Println("Database: ~/.config/agent-store/agents.db")
}

func migrateGlobalNature(store *agentstore.Store, agentsDir string) error {
	globalFiles := []struct {
		filename string
		id       string
		kind     string
		priority int
	}{
		{"_principles.md", "principles", "principle", 10},
		{"_values.md", "values", "value", 20},
		{"_user.md", "user-slava", "user", 30},
	}

	for _, gf := range globalFiles {
		path := filepath.Join(agentsDir, gf.filename)
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("    Warning: could not read %s: %v\n", gf.filename, err)
			continue
		}

		if err := store.UpsertNature(agentstore.Nature{
			ID:        gf.id,
			Content:   string(content),
			Kind:      gf.kind,
			Scope:     "global",
			Priority:  gf.priority,
			Source:    "migration",
		}); err != nil {
			return fmt.Errorf("upsert %s: %w", gf.id, err)
		}
		fmt.Printf("    - %s (%d bytes)\n", gf.id, len(content))
	}

	return nil
}

func migrateTiers(store *agentstore.Store, tiers *inberTiersConfig) error {
	if len(tiers.High) > 0 {
		data, _ := json.Marshal(tiers.High)
		if err := store.SetOrchestratorSetting("inber", "tier_high", string(data)); err != nil {
			return err
		}
	}
	if len(tiers.Low) > 0 {
		data, _ := json.Marshal(tiers.Low)
		if err := store.SetOrchestratorSetting("inber", "tier_low", string(data)); err != nil {
			return err
		}
	}
	if tiers.Delay > 0 {
		if err := store.SetOrchestratorSetting("inber", "tier_delay", fmt.Sprintf("%d", tiers.Delay)); err != nil {
			return err
		}
	}
	if tiers.Grace > 0 {
		if err := store.SetOrchestratorSetting("inber", "tier_grace", fmt.Sprintf("%d", tiers.Grace)); err != nil {
			return err
		}
	}
	return nil
}

func migrateAgent(store *agentstore.Store, agentsDir, id string, cfg *inberAgentConfig) error {
	// Create agent
	if err := store.UpsertAgent(agentstore.Agent{
		ID:   id,
		Name: cfg.Name,
		Role: cfg.Role,
	}); err != nil {
		return fmt.Errorf("upsert agent: %w", err)
	}

	// Read and create identity nature
	soulPath := filepath.Join(agentsDir, id, "soul.md")
	soulContent, err := os.ReadFile(soulPath)
	if err != nil {
		fmt.Printf("    Warning: could not read soul.md for %s: %v\n", id, err)
		soulContent = []byte(fmt.Sprintf("# %s\n\n%s", cfg.Name, cfg.Role))
	}

	identityID := id + "-identity"
	if err := store.UpsertNature(agentstore.Nature{
		ID:        identityID,
		Content:   string(soulContent),
		Kind:      "identity",
		Scope:     "agent",
		Priority:  0,
		Source:    "migration",
	}); err != nil {
		return fmt.Errorf("upsert identity: %w", err)
	}

	// Link agent to nature
	// Identity first (priority 0), then global nature
	if err := store.LinkNature(id, identityID, 0, true); err != nil {
		return fmt.Errorf("link identity: %w", err)
	}
	if err := store.LinkNature(id, "principles", 10, true); err != nil {
		// Non-fatal - might not exist
		fmt.Printf("    Warning: could not link principles: %v\n", err)
	}
	if err := store.LinkNature(id, "values", 20, true); err != nil {
		fmt.Printf("    Warning: could not link values: %v\n", err)
	}
	if err := store.LinkNature(id, "user-slava", 30, true); err != nil {
		fmt.Printf("    Warning: could not link user: %v\n", err)
	}

	// Create orchestrator config
	if err := store.EnsureAgentConfig(id, "inber"); err != nil {
		return fmt.Errorf("ensure config: %w", err)
	}

	// Set config values
	if cfg.Model != "" {
		if err := store.SetConfigValue(id, "inber", "model", cfg.Model); err != nil {
			return fmt.Errorf("set model: %w", err)
		}
	}
	if cfg.Thinking > 0 {
		if err := store.SetConfigValue(id, "inber", "thinking", fmt.Sprintf("%d", cfg.Thinking)); err != nil {
			return fmt.Errorf("set thinking: %w", err)
		}
	}
	if cfg.Context.Budget > 0 {
		if err := store.SetConfigValue(id, "inber", "context_budget", fmt.Sprintf("%d", cfg.Context.Budget)); err != nil {
			return fmt.Errorf("set context_budget: %w", err)
		}
	}

	// Add tools
	for i, tool := range cfg.Tools {
		if err := store.AddTool(id, "inber", tool, i, true); err != nil {
			return fmt.Errorf("add tool %s: %w", tool, err)
		}
	}

	// Set limits
	if cfg.Limits != nil {
		if cfg.Limits.MaxTurns > 0 {
			if err := store.SetLimit(id, "inber", "max_turns", cfg.Limits.MaxTurns); err != nil {
				return fmt.Errorf("set max_turns: %w", err)
			}
		}
		if cfg.Limits.MaxInputTokens > 0 {
			if err := store.SetLimit(id, "inber", "max_input_tokens", cfg.Limits.MaxInputTokens); err != nil {
				return fmt.Errorf("set max_input_tokens: %w", err)
			}
		}
	}

	return nil
}

func init() {
	// Ensure time package is used
	_ = time.Now()
}
