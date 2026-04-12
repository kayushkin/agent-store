# agent-store

Single source of truth for agent identity, nature, and orchestrator configs.

## What This Repo Does

- Stores agent definitions (identity, nature, role) in SQLite
- Manages per-orchestrator configs (model, tools, limits) for each agent
- Provides reconciliation: detects drift between inber, openclaw, and dash registry
- CLI for listing, diffing, and syncing agents across orchestrators

## Also Responsible For

- `~/repos/model-store` — credential and provider management

## Key Files

- `store.go` — Go library (import as `github.com/kayushkin/agent-store`)
- `schema.sql` — SQLite schema
- `cmd/` — CLI tool
- `agents/` — seed data

## Orchestrator Sources

- **Inber**: `~/repos/inber/agents/` directories + `agents.json` seed
- **OpenClaw**: `~/.openclaw/openclaw.json` → `agents.list[]`
- **Dash registry**: Si API at `localhost:8101/api/agents`

## Agent Name Mapping

Inber uses celtic names, OpenClaw uses project names. The mapping:

| Agent ID | Inber Name | OpenClaw ID | Project |
|----------|-----------|-------------|---------|
| claxon | claxon | main | orchestrator |
| brigid | brigid | kayushkin | kayushkin.com |
| fionn | fionn | inber | inber |
| oisin | oisin | si | si/dash |
| goibniu | goibniu | forge | forge |
| manannan | manannan | downloadstack | downloadstack |
| ogma | ogma | logstack | logstack |
| scathach | scathach | claxon-android | claxon-android |
| bench | bench | agent-bench | agent-bench |
| dagda | dagda | scheduler/noteboard | noteboard+scheduler |
| lugh | lugh | agent-store | agent-store+model-store |

## Rules

- Always build + test before push
- agent-store is the source of truth — orchestrators consume from it, not the other way around
- Reconciliation shows diffs but doesn't auto-push without confirmation
