# agent-store

Single source of truth for agent identity, nature, and harness configs.

Multiple harnesses — inber, openclaw, dash — each have their own config formats and agent naming. agent-store provides a canonical registry so agents are defined once and configured per-harness. It tracks identity (nature), runtime config (model, tools, limits), file distribution, and drift detection.

```
  ┌─────────────────────────────────────────────────────────┐
  │                    agent-store                          │
  │                                                        │
  │   Agents ──── canonical identity (slug, emoji, role)   │
  │   Nature ──── personality content (identity, values)   │
  │   Configs ─── per-harness runtime settings        │
  │   Tools ───── per-harness tool assignments        │
  │   Status ──── runtime state (idle, working, task)      │
  │   Files ───── distribution tracking + drift detection  │
  │                                                        │
  ╚═══════════╤══════════════════╤═════════════════════════╝
              │                  │
     ┌────────▼────────┐  ┌─────▼──────────┐
     │     inber        │  │   openclaw     │
     │  (harness)  │  │ (harness) │
     └─────────────────┘  └────────────────┘
```

Default database location: `~/.config/agent-store/agents.db`

## Usage

### As a library

```go
import agentstore "github.com/kayushkin/agent-store"

store, _ := agentstore.Open("") // uses default path

// Create agent
store.UpsertAgent(&agentstore.Agent{
    Slug:        "claxon",
    DisplayName: "Claxon",
    Emoji:       "🦀",
    Role:        "main harness",
    Enabled:     true,
})

// Add nature (identity content)
store.UpsertAgentNature(&agentstore.AgentNature{
    AgentID: claxon.ID,
    Kind:    "identity",
    Content: "# Claxon 🦀\nI'm the main session agent...",
})

// Register with harness
store.UpsertAgentOrchestrator(&agentstore.AgentOrchestrator{
    AgentID:              claxon.ID,
    OrchestratorID:       "inber",
    OrchestratorAgentID:  "claxon",
    ModelPrimary:         "claude-opus-4-6",
    ThinkingBudget:       2048,
})

// Set tools
store.SetAgentTools(claxon.ID, "inber", []string{"shell", "spawn_agent", "file_read"})

// Get full runtime config
cfg, _ := store.GetFullAgentConfig("claxon", "inber")
// cfg.Model, cfg.Tools, cfg.ThinkingBudget, cfg.NatureContent, ...
```

### As an HTTP server

Register handlers on any `http.ServeMux`:

```go
store, _ := agentstore.Open("")
agentstore.RegisterHandlers(mux, store)
```

When used with [llm-bridge-server](https://github.com/kayushkin/llm-bridge-server), the handlers are mounted automatically.

## HTTP API

### Agents

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/agents` | List all agents (add `?expanded=true` for per-harness rows with status) |
| `GET` | `/agents/{slug}` | Get single agent |
| `POST` | `/agents` | Create agent |
| `PUT` | `/agents/{slug}` | Update agent |
| `DELETE` | `/agents/{slug}` | Delete agent (cascades to configs, nature, tools) |

### Orchestrator configs

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/agents/{slug}/harnesses` | Get harness configs for agent |
| `POST` | `/agents/{slug}/harnesses` | Add/update harness config |
| `GET` | `/agents/{slug}/config` | Full runtime config (`?harness=inber`) |
| `GET` | `/configs` | All agent configs for an harness (`?harness=inber`) |

### Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/reconcile` | Check for missing registrations and file drift |
| `GET` | `/agents/health` | Health check |

## Schema

### Core tables

| Table | Purpose |
|-------|---------|
| `agents` | Canonical agent definitions — slug, display name, emoji, role |
| `harnesses` | Orchestrator systems (inber, openclaw, dash) with default agent and endpoint |
| `agent_harnesses` | Agent-to-harness configs — model, thinking budget, context tags, limits |
| `agent_tools` | Tool assignments per agent/harness pair |
| `agent_nature` | Identity and personality content (kind: identity, principle, value, user, project) |
| `agent_name_aliases` | Alternative names per context for agent resolution |
| `agent_system_prompt_refs` | Tracks which agents appear in other agents' system prompts |
| `agent_status` | Runtime status (idle, working) with task and session tracking |
| `projects` | Project grouping with slug, name, path |
| `project_nature` | Links projects to nature entries with priority |
| `file_distributions` | Tracks files distributed to harnesses with content hashes |
| `file_scans` | Drift detection — compares current file hash against distributed hash |

### Key concepts

**Agents** are identified by slug (e.g. `claxon`, `brigid`, `fionn`). The same agent can have different configs per harness — different model, different tools, different limits.

**Nature** is the agent's personality content, organized by kind:

| Kind | Description |
|------|-------------|
| `identity` | Who the agent is |
| `principle` | How to operate |
| `value` | What matters |
| `user` | User context |
| `project` | Project-specific knowledge |

**File distribution** tracks when nature content is written out to harness config files (e.g. soul.md). **File scans** detect when those files have been externally modified, enabling a bidirectional sync loop.

## CLI tools

### `cmd/seed`

Populates agent-store from existing config files:

```bash
go run ./cmd/seed
```

Reads from `mapping.json`, `inber/agents.json`, and agent soul files to bootstrap the database.

### `cmd/test-cycle`

Exercises the full nature → distribute → scan → drift detection cycle:

```bash
go run ./cmd/test-cycle
```

### `cmd/migrate-inber`

One-time migration of configs from inber to agent-store:

```bash
go run ./cmd/migrate-inber
```

## Part of the llm-bridge ecosystem

agent-store is one of several optional stores used by [llm-bridge-server](https://github.com/kayushkin/llm-bridge-server). See the [llm-bridge](https://github.com/kayushkin/llm-bridge) README for the full ecosystem.
