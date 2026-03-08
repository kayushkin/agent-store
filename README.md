# agent-store

Single source of truth for agent identity, nature, and orchestrator configs.

## Purpose

Provides a unified database for orchestrators like inber and openclaw to:
- Store agent identity and characteristics (nature)
- Manage per-orchestrator runtime configs (model, tools, limits)
- Track memories and learned knowledge
- Share agents across orchestrators

## Location

Default: `~/.config/agent-store/agents.db`

## Schema

### Core Tables

| Table | Purpose |
|-------|---------|
| `nature` | Identity, principles, values, user context |
| `agents` | Named entry points (claxon, bran, fionn...) |
| `agent_nature` | Links agents to nature entries |
| `orchestrators` | Orchestrator systems (inber, openclaw) |
| `agent_configs` | Agent + orchestrator pairs |
| `agent_config_values` | EAV config (model, thinking, etc.) |
| `agent_tools` | Tool assignments per agent/orchestrator |
| `agent_limits` | Turn/token limits |
| `memories` | Learned knowledge and events |
| `projects` | Project-scoped context |

### Key Concepts

**Nature** - Character definition (identity, principles, values). Not owned by agents, linked via `agent_nature`.

**Agents** - Named entry points. Don't "own" nature, they reference it. Same agent can have different configs per orchestrator.

**Orchestrator Config** - EAV-based runtime settings. `agent_config_values` for scalars, `agent_tools` for arrays, `agent_limits` for integers.

## Usage

```go
import "github.com/kayushkin/agent-store"

store, _ := agentstore.Open("")

// Create agent
store.UpsertAgent(agentstore.Agent{ID: "claxon", Name: "Claxon", Role: "main orchestrator"})

// Create nature
store.UpsertNature(agentstore.Nature{
    ID:      "claxon-identity",
    Content: "# Claxon 🦀\n\nI'm the main session agent...",
    Kind:    "identity",
    Scope:   "agent",
})

// Link agent to nature
store.LinkNature("claxon", "claxon-identity", 0, true)

// Set orchestrator config
store.SetConfigValue("claxon", "inber", "model", "claude-opus-4-6")
store.SetConfigValue("claxon", "inber", "thinking", "2048")
store.AddTool("claxon", "inber", "shell", 0, true)
store.AddTool("claxon", "inber", "spawn_agent", 0, true)
store.SetLimit("claxon", "inber", "max_turns", 50)

// Get full config for runtime
cfg, _ := store.GetAgentConfig("claxon", "inber")
// cfg.Values["model"] == "claude-opus-4-6"
// cfg.Tools = [{Tool: "shell", Enabled: true}, ...]

// Get agent's nature for prompt construction
nature, _ := store.GetAgentNature("claxon")
```

## Nature Kinds

| Kind | Scope | Description |
|------|-------|-------------|
| `identity` | agent | Who the agent is |
| `principle` | global | How to operate |
| `value` | global | What matters |
| `user` | global | User context |
| `project` | project | Project-specific knowledge |

## Memory Kinds

| Kind | Description |
|------|-------------|
| `lesson` | Something learned from experience |
| `knowledge` | Factual information |
| `event` | Something that happened |
| `observation` | Noted pattern or behavior |
