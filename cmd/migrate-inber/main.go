// Migration tool: inber agents.json + soul.md files → agent-store database
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentstore "github.com/kayushkin/agent-store"
)

type inberAgentsFile struct {
	Default string                       `json:"default"`
	Agents  map[string]*inberAgentConfig `json:"agents"`
}

type inberAgentConfig struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	Model string `json:"model"`
}

func main() {
	inberPath := os.Getenv("INBER_PATH")
	if inberPath == "" {
		inberPath = os.ExpandEnv("$HOME/repos/inber")
	}

	store, err := agentstore.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open agent-store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

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

	// Ensure inber harness
	orch := agentstore.Harness{
		ID:          "inber",
		DisplayName: "Inber",
		ConfigPath:  filepath.Join(inberPath, "agents"),
	}
	if err := store.UpsertHarness(&orch); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create harness: %v\n", err)
		os.Exit(1)
	}

	agentsDir := filepath.Join(inberPath, "agents")

	fmt.Printf("Migrating %d agents...\n", len(af.Agents))
	for id, cfg := range af.Agents {
		displayName := strings.ToUpper(id[:1]) + id[1:]
		if cfg.Name != "" {
			displayName = cfg.Name
		}

		agent := agentstore.Agent{
			Slug:        id,
			DisplayName: displayName,
			Role:        cfg.Role,
			Enabled:     true,
		}
		if err := store.UpsertAgent(&agent); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to upsert agent %s: %v\n", id, err)
			os.Exit(1)
		}

		// Agent→harness mapping
		ao := agentstore.AgentHarness{
			AgentID:             agent.ID,
			HarnessID:      "inber",
			HarnessAgentID: id,
			Enabled:             true,
			ModelPrimary:        cfg.Model,
		}
		if err := store.UpsertAgentHarness(&ao); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to map agent %s: %v\n", id, err)
			os.Exit(1)
		}

		// Import soul.md as nature
		soulPath := filepath.Join(agentsDir, id, "soul.md")
		if content, err := os.ReadFile(soulPath); err == nil {
			n := agentstore.AgentNature{
				AgentID:    agent.ID,
				Kind:       "identity",
				Content:    string(content),
				Priority:   0,
				SourcePath: soulPath,
			}
			if err := store.UpsertAgentNature(&n); err != nil {
				fmt.Printf("    Warning: could not save nature for %s: %v\n", id, err)
			}
		}

		fmt.Printf("  - %s (id=%d)\n", id, agent.ID)
	}

	// Set default agent
	if af.Default != "" {
		defaultAgent, err := store.GetAgentBySlug(af.Default)
		if err == nil {
			orch.DefaultAgentID = agentstore.Int64Ptr(defaultAgent.ID)
			_ = store.UpsertHarness(&orch)
		}
	}

	fmt.Println("\nMigration complete!")
}
