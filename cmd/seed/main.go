// Command seed populates the agent-store DB from mapping.json + inber agents.json + soul files.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	agentstore "github.com/kayushkin/agent-store"
	"github.com/kayushkin/agent-store/internal/textutil"
)

// mapping.json structures
type MappingFile struct {
	Agents []MappingAgent `json:"agents"`
}

type MappingAgent struct {
	ID       string      `json:"id"`
	Inber    *string     `json:"inber"`
	OpenClaw interface{} `json:"openclaw"`
	Project  string      `json:"project"`
	Emoji    string      `json:"emoji"`
}

// openclaw.json structures
type OpenClawConfig struct {
	Agents struct {
		Defaults struct {
			Model struct {
				Primary   string   `json:"primary"`
				Fallbacks []string `json:"fallbacks"`
			} `json:"model"`
			Workspace string `json:"workspace"`
		} `json:"defaults"`
		List []OpenClawAgent `json:"list"`
	} `json:"agents"`
}

type OpenClawAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Default   bool   `json:"default"`
	Workspace string `json:"workspace"`
	Identity  *struct {
		Name  string `json:"name"`
		Emoji string `json:"emoji"`
	} `json:"identity"`
	Model *struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
	} `json:"model"`
	Subagents *struct {
		AllowAgents []string `json:"allowAgents"`
	} `json:"subagents"`
}

// agents.json structures
type InberConfig struct {
	Default string                 `json:"default"`
	Agents  map[string]InberAgent  `json:"agents"`
}

type InberAgent struct {
	Name     string   `json:"name"`
	Role     string   `json:"role"`
	Model    string   `json:"model"`
	Thinking int      `json:"thinking"`
	Tools    []string `json:"tools"`
	Context  struct {
		Tags   []string `json:"tags"`
		Budget int      `json:"budget"`
	} `json:"context"`
	Limits struct {
		MaxTurns        int `json:"maxTurns"`
		MaxInputTokens  int `json:"maxInputTokens"`
		MaxResponseTime int `json:"maxResponseTime"`
	} `json:"limits"`
	Workspace string   `json:"workspace"`
	Project   string   `json:"project"`
	Projects  []string `json:"projects"`
	System    string   `json:"system"`
	Shelved   bool     `json:"shelved"`
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func main() {
	dbPath := os.Getenv("AGENT_STORE_DB")
	if dbPath == "" {
		dbPath = agentstore.DefaultPath()
	}

	repoRoot := filepath.Dir(filepath.Dir(os.Args[0]))
	// Try to find repo root from working directory
	if _, err := os.Stat("agents/mapping.json"); err == nil {
		repoRoot = "."
	}

	mappingPath := filepath.Join(repoRoot, "agents/mapping.json")
	if len(os.Args) > 1 {
		mappingPath = os.Args[1]
	}

	inberRoot := os.Getenv("INBER_ROOT")
	if inberRoot == "" {
		home, _ := os.UserHomeDir()
		inberRoot = filepath.Join(home, "repos/inber")
	}

	// Read mapping.json
	data, err := os.ReadFile(mappingPath)
	if err != nil {
		log.Fatalf("read mapping: %v", err)
	}
	var mapping MappingFile
	if err := json.Unmarshal(data, &mapping); err != nil {
		log.Fatalf("parse mapping: %v", err)
	}

	// Read inber agents.json
	inberData, err := os.ReadFile(filepath.Join(inberRoot, "agents.json"))
	if err != nil {
		log.Fatalf("read agents.json: %v", err)
	}
	var inberCfg InberConfig
	if err := json.Unmarshal(inberData, &inberCfg); err != nil {
		log.Fatalf("parse agents.json: %v", err)
	}

	// Read shared identity files
	principles := readFile(filepath.Join(inberRoot, "agents/_principles.md"))
	values := readFile(filepath.Join(inberRoot, "agents/_values.md"))
	userCtx := readFile(filepath.Join(inberRoot, "agents/_user.md"))

	store, err := agentstore.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Seed harnesses
	orchs := []agentstore.Harness{
		{ID: "inber", DisplayName: "Inber", ConfigPath: "~/repos/inber/agents/"},
		{ID: "openclaw", DisplayName: "OpenClaw", ConfigPath: "~/.openclaw/openclaw.json"},
		{ID: "dash", DisplayName: "Dash", APIEndpoint: "localhost:8101/api/agents"},
	}
	for i := range orchs {
		if err := store.UpsertHarness(&orchs[i]); err != nil {
			log.Fatalf("upsert harness %s: %v", orchs[i].ID, err)
		}
		fmt.Printf("  harness: %s\n", orchs[i].ID)
	}

	agentIDs := map[string]int64{}

	for _, ma := range mapping.Agents {
		displayName := textutil.UpperFirstRune(ma.ID)
		role := ""
		projects := ma.Project

		// Override from inber config if present
		var inberSlug string
		if ma.Inber != nil {
			inberSlug = *ma.Inber
		}
		ia, hasInber := inberCfg.Agents[inberSlug]
		if hasInber {
			displayName = ia.Name
			role = ia.Role
			if len(ia.Projects) > 0 {
				projects = strings.Join(ia.Projects, ",")
			} else if ia.Project != "" {
				projects = ia.Project
			}
		}

		agent := agentstore.Agent{
			Slug:        ma.ID,
			DisplayName: displayName,
			Emoji:       ma.Emoji,
			Projects:    projects,
			Role:        role,
			Enabled:     true,
		}
		if err := store.UpsertAgent(&agent); err != nil {
			log.Fatalf("upsert agent %s: %v", ma.ID, err)
		}
		agentIDs[ma.ID] = agent.ID
		fmt.Printf("  agent: %s %s (id=%d)\n", ma.Emoji, ma.ID, agent.ID)

		// Inber harness config
		if hasInber {
			tagsJSON, _ := json.Marshal(ia.Context.Tags)
			ao := agentstore.AgentHarness{
				AgentID:             agent.ID,
				HarnessID:      "inber",
				HarnessAgentID: inberSlug,
				Enabled:             true,
				ModelPrimary:        ia.Model,
				WorkspacePath:       ia.Workspace,
				ThinkingBudget:      ia.Thinking,
				ContextBudget:       ia.Context.Budget,
				ContextTags:         string(tagsJSON),
				MaxTurns:            ia.Limits.MaxTurns,
				MaxInputTokens:      ia.Limits.MaxInputTokens,
				MaxResponseTime:     ia.Limits.MaxResponseTime,
				SystemPrompt:        ia.System,
				Project:             ia.Project,
				Shelved:             ia.Shelved,
			}
			if err := store.UpsertAgentHarness(&ao); err != nil {
				log.Fatalf("upsert ao inber/%s: %v", ma.ID, err)
			}

			// Set tools
			if len(ia.Tools) > 0 {
				if err := store.SetAgentTools(agent.ID, "inber", ia.Tools); err != nil {
					log.Fatalf("set tools inber/%s: %v", ma.ID, err)
				}
				fmt.Printf("    tools: %v\n", ia.Tools)
			}

			// Soul file
			soulPath := filepath.Join(inberRoot, "agents", inberSlug, "soul.md")
			soul := readFile(soulPath)
			if soul != "" {
				if err := upsertNature(store, agent.ID, "identity", soul, soulPath); err != nil {
					log.Fatalf("upsert soul %s: %v", ma.ID, err)
				}
			}

			// Shared nature (principles, values, user) — only for inber agents
			if principles != "" {
				upsertNature(store, agent.ID, "principle", principles, filepath.Join(inberRoot, "agents/_principles.md"))
			}
			if values != "" {
				upsertNature(store, agent.ID, "value", values, filepath.Join(inberRoot, "agents/_values.md"))
			}
			if userCtx != "" {
				upsertNature(store, agent.ID, "user", userCtx, filepath.Join(inberRoot, "agents/_user.md"))
			}
		}

		// OpenClaw mapping(s)
		var ocIDs []string
		switch v := ma.OpenClaw.(type) {
		case string:
			ocIDs = []string{v}
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					ocIDs = append(ocIDs, s)
				}
			}
		}
		for i, ocID := range ocIDs {
			if i == 0 {
				ao := agentstore.AgentHarness{
					AgentID:             agent.ID,
					HarnessID:      "openclaw",
					HarnessAgentID: ocID,
					Enabled:             true,
				}
				if err := store.UpsertAgentHarness(&ao); err != nil {
					log.Fatalf("upsert ao openclaw/%s: %v", ma.ID, err)
				}
			}
			alias := agentstore.AgentNameAlias{AgentID: agent.ID, Alias: ocID, Context: "openclaw"}
			if err := store.UpsertAlias(&alias); err != nil {
				log.Fatalf("upsert alias %s→%s: %v", ocID, ma.ID, err)
			}
		}
	}

	// Seed OpenClaw agent configs from openclaw.json
	home, _ := os.UserHomeDir()
	openclawPath := filepath.Join(home, ".openclaw/openclaw.json")
	ocData, err := os.ReadFile(openclawPath)
	if err != nil {
		log.Printf("warning: read openclaw.json: %v (skipping openclaw config seeding)", err)
	} else {
		var ocCfg OpenClawConfig
		if err := json.Unmarshal(ocData, &ocCfg); err != nil {
			log.Fatalf("parse openclaw.json: %v", err)
		}

		defaultModel := ocCfg.Agents.Defaults.Model.Primary
		defaultFallbacks := ocCfg.Agents.Defaults.Model.Fallbacks

		for _, oca := range ocCfg.Agents.List {
			// Find agent by openclaw ID alias or harness mapping
			agentID, found := int64(0), false
			for _, ma := range mapping.Agents {
				var ocIDs []string
				switch v := ma.OpenClaw.(type) {
				case string:
					ocIDs = []string{v}
				case []interface{}:
					for _, item := range v {
						if s, ok := item.(string); ok {
							ocIDs = append(ocIDs, s)
						}
					}
				}
				for _, ocID := range ocIDs {
					if ocID == oca.ID {
						agentID = agentIDs[ma.ID]
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found || agentID == 0 {
				log.Printf("  openclaw agent %q not found in mapping, skipping", oca.ID)
				continue
			}

			modelPrimary := defaultModel
			var fallbacksJSON string
			if oca.Model != nil {
				modelPrimary = oca.Model.Primary
				fb, _ := json.Marshal(oca.Model.Fallbacks)
				fallbacksJSON = string(fb)
			} else {
				fb, _ := json.Marshal(defaultFallbacks)
				fallbacksJSON = string(fb)
			}

			var subagentAllowJSON string
			if oca.Subagents != nil {
				sa, _ := json.Marshal(oca.Subagents.AllowAgents)
				subagentAllowJSON = string(sa)
			}

			ao := agentstore.AgentHarness{
				AgentID:             agentID,
				HarnessID:      "openclaw",
				HarnessAgentID: oca.ID,
				Enabled:             true,
				ModelPrimary:        modelPrimary,
				ModelFallbacks:      fallbacksJSON,
				WorkspacePath:       oca.Workspace,
				IsDefault:           oca.Default,
				SubagentAllow:       subagentAllowJSON,
			}
			if err := store.UpsertAgentHarness(&ao); err != nil {
				log.Fatalf("upsert ao openclaw/%s: %v", oca.ID, err)
			}
			fmt.Printf("    openclaw: %s → model=%s workspace=%s\n", oca.ID, modelPrimary, oca.Workspace)
		}
	}

	// Set default agents
	defaults := map[string]string{"inber": "claxon", "openclaw": "claxon", "dash": "oisin"}
	for orchID, agentSlug := range defaults {
		if id, ok := agentIDs[agentSlug]; ok {
			o, err := store.GetHarness(orchID)
			if err != nil {
				continue
			}
			o.DefaultAgentID = agentstore.Int64Ptr(id)
			store.UpsertHarness(o)
		}
	}

	fmt.Printf("\nSeeded %d agents across %d harnesses.\n", len(mapping.Agents), len(orchs))
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func upsertNature(store *agentstore.Store, agentID int64, kind, content, sourcePath string) error {
	// Check if nature already exists for this agent+kind+source
	natures, _ := store.ListAgentNature(agentID)
	for _, n := range natures {
		if n.Kind == kind && n.SourcePath == sourcePath {
			n.Content = content
			n.ContentHash = hash(content)
			return store.UpsertAgentNature(&n)
		}
	}
	n := &agentstore.AgentNature{
		AgentID:     agentID,
		Kind:        kind,
		Content:     content,
		ContentHash: hash(content),
		SourcePath:  sourcePath,
	}
	return store.UpsertAgentNature(n)
}
