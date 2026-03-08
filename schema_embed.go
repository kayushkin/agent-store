package agentstore

// schemaSQL contains the database schema
const schemaSQL = `
-- agent-store schema
-- Single source of truth for agent identity, nature, and orchestrator configs

-- ============================================
-- NATURE (who you are, how you work)
-- ============================================

CREATE TABLE IF NOT EXISTS nature (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    kind TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'global',
    priority INTEGER DEFAULT 0,
    importance REAL DEFAULT 0.5,
    source TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_nature_kind ON nature(kind);
CREATE INDEX IF NOT EXISTS idx_nature_scope ON nature(scope);

-- ============================================
-- AGENTS (entry points, not owners)
-- ============================================

CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    role TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_nature (
    agent_id TEXT NOT NULL,
    nature_id TEXT NOT NULL,
    priority INTEGER DEFAULT 0,
    required INTEGER DEFAULT 1,
    PRIMARY KEY (agent_id, nature_id),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (nature_id) REFERENCES nature(id) ON DELETE CASCADE
);

-- ============================================
-- ORCHESTRATORS
-- ============================================

CREATE TABLE IF NOT EXISTS orchestrators (
    id TEXT PRIMARY KEY,
    default_agent TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS orchestrator_settings (
    orchestrator_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    PRIMARY KEY (orchestrator_id, key),
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE
);

-- ============================================
-- AGENT RUNTIME CONFIG (per-orchestrator)
-- ============================================

CREATE TABLE IF NOT EXISTS agent_configs (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, orchestrator_id),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_config_values (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    PRIMARY KEY (agent_id, orchestrator_id, key),
    FOREIGN KEY (agent_id, orchestrator_id) REFERENCES agent_configs(agent_id, orchestrator_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_tools (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    tool TEXT NOT NULL,
    priority INTEGER DEFAULT 0,
    enabled INTEGER DEFAULT 1,
    PRIMARY KEY (agent_id, orchestrator_id, tool),
    FOREIGN KEY (agent_id, orchestrator_id) REFERENCES agent_configs(agent_id, orchestrator_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_limits (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value INTEGER NOT NULL,
    PRIMARY KEY (agent_id, orchestrator_id, key),
    FOREIGN KEY (agent_id, orchestrator_id) REFERENCES agent_configs(agent_id, orchestrator_id) ON DELETE CASCADE
);

-- ============================================
-- MEMORIES (what you've learned)
-- ============================================

CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    kind TEXT,
    scope TEXT DEFAULT 'agent',
    importance REAL DEFAULT 0.5,
    access_count INTEGER DEFAULT 0,
    last_accessed INTEGER,
    source TEXT,
    agent_id TEXT,
    project_id TEXT,
    expires_at INTEGER,
    embedding BLOB,
    created_at INTEGER NOT NULL
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
-- PROJECTS (optional, for project-scoped context)
-- ============================================

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT,
    description TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS project_nature (
    project_id TEXT NOT NULL,
    nature_id TEXT NOT NULL,
    priority INTEGER DEFAULT 0,
    PRIMARY KEY (project_id, nature_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (nature_id) REFERENCES nature(id) ON DELETE CASCADE
);

-- ============================================
-- SESSIONS (optional tracking)
-- ============================================

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    agent_id TEXT,
    orchestrator_id TEXT,
    project_id TEXT,
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    cost REAL DEFAULT 0,
    summary TEXT,
    FOREIGN KEY (agent_id) REFERENCES agents(id),
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id),
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent_id);
CREATE INDEX IF NOT EXISTS idx_sessions_orchestrator ON sessions(orchestrator_id);
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at);

`
