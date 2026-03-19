-- agent-store schema
-- Single source of truth for agent identity, nature, and orchestrator configs

PRAGMA foreign_keys = ON;

-- ============================================
-- AGENTS (canonical definitions)
-- ============================================

CREATE TABLE IF NOT EXISTS agents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT UNIQUE NOT NULL,            -- celtic name: "claxon", "brigid", etc.
    display_name TEXT NOT NULL,           -- "Claxon", "Brigid"
    emoji TEXT,                           -- "🦀", "🔥"
    projects TEXT,                        -- comma-separated: "si,dash"
    description TEXT,
    role TEXT,
    enabled INTEGER DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- ============================================
-- ORCHESTRATORS
-- ============================================

CREATE TABLE IF NOT EXISTS orchestrators (
    id TEXT PRIMARY KEY,                  -- "inber", "openclaw", "dash"
    display_name TEXT NOT NULL,
    default_agent_id INTEGER,             -- FK to agents.id
    config_path TEXT,                     -- e.g. "~/.openclaw/openclaw.json"
    api_endpoint TEXT,                    -- e.g. "localhost:8101/api/agents"
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (default_agent_id) REFERENCES agents(id) ON DELETE SET NULL
);

-- ============================================
-- AGENT ↔ ORCHESTRATOR (reconciliation join)
-- ============================================

CREATE TABLE IF NOT EXISTS agent_orchestrators (
    agent_id INTEGER NOT NULL,
    orchestrator_id TEXT NOT NULL,
    orchestrator_agent_id TEXT NOT NULL,   -- what the orchestrator calls this agent
    enabled INTEGER DEFAULT 1,
    model_primary TEXT,
    model_fallbacks TEXT,                  -- JSON array
    workspace_path TEXT,
    thinking_budget INTEGER DEFAULT 0,     -- token budget for extended thinking
    context_budget INTEGER DEFAULT 0,      -- token budget for context assembly
    context_tags TEXT,                     -- JSON array of tags, e.g. ["identity","code"]
    max_turns INTEGER DEFAULT 0,
    max_input_tokens INTEGER DEFAULT 0,
    max_response_time INTEGER DEFAULT 0,   -- seconds
    system_prompt TEXT,                    -- custom system prompt override
    project TEXT,                          -- forge project name
    shelved INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, orchestrator_id),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_orch_orch ON agent_orchestrators(orchestrator_id);

-- ============================================
-- AGENT TOOLS (per orchestrator)
-- ============================================

CREATE TABLE IF NOT EXISTS agent_tools (
    agent_id INTEGER NOT NULL,
    orchestrator_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    PRIMARY KEY (agent_id, orchestrator_id, tool_name),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE
);

-- ============================================
-- AGENT SYSTEM PROMPT REFS
-- ============================================

CREATE TABLE IF NOT EXISTS agent_system_prompt_refs (
    orchestrator_id TEXT NOT NULL,
    host_agent_id INTEGER NOT NULL,       -- agent whose prompt contains the listing
    referenced_agent_id INTEGER NOT NULL, -- agent being listed
    prompt_location TEXT NOT NULL,         -- "AGENTS.md", "openclaw.json"
    created_at INTEGER NOT NULL,
    PRIMARY KEY (orchestrator_id, host_agent_id, referenced_agent_id, prompt_location),
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE,
    FOREIGN KEY (host_agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (referenced_agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

-- ============================================
-- AGENT NATURE (identity/personality)
-- ============================================

CREATE TABLE IF NOT EXISTS agent_nature (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id INTEGER NOT NULL,
    kind TEXT NOT NULL,                    -- identity, principle, value, user, project
    content TEXT NOT NULL,
    content_hash TEXT,                     -- SHA256 of content for quick comparison
    priority INTEGER DEFAULT 0,
    source_path TEXT,                      -- e.g. ~/life/repos/inber/agents/claxon/soul.md
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_nature_agent ON agent_nature(agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_nature_kind ON agent_nature(kind);

-- ============================================
-- AGENT NAME ALIASES
-- ============================================

CREATE TABLE IF NOT EXISTS agent_name_aliases (
    agent_id INTEGER NOT NULL,
    alias TEXT NOT NULL,
    context TEXT,                           -- "openclaw", "dash", "logstack"
    created_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, alias),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_aliases_alias ON agent_name_aliases(alias);

-- ============================================
-- FILE DISTRIBUTIONS (agent-store → orchestrator files)
-- ============================================

CREATE TABLE IF NOT EXISTS file_distributions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id INTEGER NOT NULL,
    orchestrator_id TEXT NOT NULL,
    file_path TEXT NOT NULL,               -- absolute path where file was written
    content_hash TEXT NOT NULL,            -- SHA256 of file content at distribution time
    distributed_at INTEGER NOT NULL,
    source_nature_ids TEXT,                -- JSON array of agent_nature IDs that contributed
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_file_dist_agent ON file_distributions(agent_id);
CREATE INDEX IF NOT EXISTS idx_file_dist_orch ON file_distributions(orchestrator_id);
CREATE INDEX IF NOT EXISTS idx_file_dist_path ON file_distributions(file_path);

-- ============================================
-- FILE SCANS (drift detection)
-- ============================================

CREATE TABLE IF NOT EXISTS file_scans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_distribution_id INTEGER NOT NULL,
    scanned_at INTEGER NOT NULL,
    current_hash TEXT NOT NULL,             -- hash at scan time
    status TEXT NOT NULL,                   -- "unchanged", "modified", "missing", "new"
    diff_summary TEXT,                      -- brief description of changes
    ingested INTEGER DEFAULT 0,            -- whether changes were pulled back into agent_nature
    FOREIGN KEY (file_distribution_id) REFERENCES file_distributions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_file_scans_dist ON file_scans(file_distribution_id);
CREATE INDEX IF NOT EXISTS idx_file_scans_status ON file_scans(status);

-- ============================================
-- MEMORIES
-- ============================================

CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    summary TEXT,
    original_id TEXT,
    kind TEXT,
    scope TEXT DEFAULT 'agent',
    importance REAL DEFAULT 0.5,
    access_count INTEGER DEFAULT 0,
    last_accessed INTEGER,
    source TEXT,
    agent_id INTEGER,
    project_id INTEGER,
    expires_at INTEGER,
    embedding BLOB,
    tokens INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE SET NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_memories_agent ON memories(agent_id);
CREATE INDEX IF NOT EXISTS idx_memories_project ON memories(project_id);
CREATE INDEX IF NOT EXISTS idx_memories_kind ON memories(kind);
CREATE INDEX IF NOT EXISTS idx_memories_scope ON memories(scope);
CREATE INDEX IF NOT EXISTS idx_memories_importance ON memories(importance);

CREATE TABLE IF NOT EXISTS memory_tags (
    memory_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    PRIMARY KEY (memory_id, tag),
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_memory_tags_tag ON memory_tags(tag);

-- ============================================
-- PROJECTS
-- ============================================

CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    path TEXT,
    description TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS project_nature (
    project_id INTEGER NOT NULL,
    nature_id INTEGER NOT NULL,
    priority INTEGER DEFAULT 0,
    PRIMARY KEY (project_id, nature_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (nature_id) REFERENCES agent_nature(id) ON DELETE CASCADE
);
