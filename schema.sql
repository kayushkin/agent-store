-- agent-store schema
-- Single source of truth for agent identity, nature, and orchestrator configs

-- ============================================
-- NATURE (who you are, how you work)
-- ============================================

CREATE TABLE nature (
    id TEXT PRIMARY KEY,           -- "claxon-identity", "principles", "slava-user"
    content TEXT NOT NULL,
    kind TEXT NOT NULL,            -- identity, principle, value, user, project
    scope TEXT NOT NULL DEFAULT 'global', -- global, agent, project
    
    -- Ordering and importance
    priority INTEGER DEFAULT 0,    -- lower = earlier in prompt construction
    importance REAL DEFAULT 0.5,   -- for retrieval ranking
    
    -- Metadata
    source TEXT,                   -- user, system, imported
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_nature_kind ON nature(kind);
CREATE INDEX idx_nature_scope ON nature(scope);

-- ============================================
-- AGENTS (entry points, not owners)
-- ============================================

CREATE TABLE agents (
    id TEXT PRIMARY KEY,           -- "claxon", "bran", "fionn"
    name TEXT NOT NULL,            -- display name "Claxon"
    role TEXT,                     -- short description
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Agent -> Nature mapping (many-to-many, ordered)
CREATE TABLE agent_nature (
    agent_id TEXT NOT NULL,
    nature_id TEXT NOT NULL,
    priority INTEGER DEFAULT 0,    -- ordering for this agent's nature
    required INTEGER DEFAULT 1,    -- 1 = always include, 0 = optional enrichment
    PRIMARY KEY (agent_id, nature_id),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (nature_id) REFERENCES nature(id) ON DELETE CASCADE
);

-- ============================================
-- ORCHESTRATORS
-- ============================================

CREATE TABLE orchestrators (
    id TEXT PRIMARY KEY,           -- "inber", "openclaw"
    default_agent TEXT,            -- which agent to use by default
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Orchestrator-level settings (EAV)
CREATE TABLE orchestrator_settings (
    orchestrator_id TEXT NOT NULL,
    key TEXT NOT NULL,             -- "default_model", "tier_high", "tier_low", etc.
    value TEXT,                    -- string value, parsed by consumer
    PRIMARY KEY (orchestrator_id, key),
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE
);

-- ============================================
-- AGENT RUNTIME CONFIG (per-orchestrator)
-- ============================================

-- Agent + Orchestrator pair (the config "header")
CREATE TABLE agent_configs (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,     -- can disable an agent for specific orchestrator
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, orchestrator_id),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    FOREIGN KEY (orchestrator_id) REFERENCES orchestrators(id) ON DELETE CASCADE
);

-- Config values (EAV for flexibility)
CREATE TABLE agent_config_values (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    key TEXT NOT NULL,             -- "model", "thinking", "context_budget"
    value TEXT,                    -- string value
    PRIMARY KEY (agent_id, orchestrator_id, key),
    FOREIGN KEY (agent_id, orchestrator_id) REFERENCES agent_configs(agent_id, orchestrator_id) ON DELETE CASCADE
);

-- Tools (array as junction table)
CREATE TABLE agent_tools (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    tool TEXT NOT NULL,            -- "shell", "read_file", "spawn_agent"
    priority INTEGER DEFAULT 0,    -- ordering (optional)
    enabled INTEGER DEFAULT 1,     -- can disable individual tools
    PRIMARY KEY (agent_id, orchestrator_id, tool),
    FOREIGN KEY (agent_id, orchestrator_id) REFERENCES agent_configs(agent_id, orchestrator_id) ON DELETE CASCADE
);

-- Limits (structured for type safety)
CREATE TABLE agent_limits (
    agent_id TEXT NOT NULL,
    orchestrator_id TEXT NOT NULL,
    key TEXT NOT NULL,             -- "max_turns", "max_input_tokens", "max_output_tokens"
    value INTEGER NOT NULL,
    PRIMARY KEY (agent_id, orchestrator_id, key),
    FOREIGN KEY (agent_id, orchestrator_id) REFERENCES agent_configs(agent_id, orchestrator_id) ON DELETE CASCADE
);

-- ============================================
-- MEMORIES (what you've learned)
-- ============================================

CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    
    -- Classification
    kind TEXT,                     -- lesson, knowledge, event, observation
    scope TEXT DEFAULT 'agent',    -- global, agent, project, session
    
    -- Retrieval signals
    importance REAL DEFAULT 0.5,
    access_count INTEGER DEFAULT 0,
    last_accessed INTEGER,
    
    -- Lifecycle
    source TEXT,                   -- user, agent, reflection, system
    agent_id TEXT,                 -- who learned this (nullable = shared)
    project_id TEXT,               -- project context (nullable = global)
    expires_at INTEGER,            -- for ephemeral memories
    
    -- Vector search
    embedding BLOB,
    
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_memories_agent ON memories(agent_id);
CREATE INDEX idx_memories_project ON memories(project_id);
CREATE INDEX idx_memories_kind ON memories(kind);
CREATE INDEX idx_memories_scope ON memories(scope);
CREATE INDEX idx_memories_importance ON memories(importance);

-- Memory tags (many-to-many)
CREATE TABLE memory_tags (
    memory_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    PRIMARY KEY (memory_id, tag),
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
);

CREATE INDEX idx_memory_tags_tag ON memory_tags(tag);

-- ============================================
-- PROJECTS (optional, for project-scoped context)
-- ============================================

CREATE TABLE projects (
    id TEXT PRIMARY KEY,           -- "inber", "kayushkin", "si"
    name TEXT NOT NULL,
    path TEXT,                     -- filesystem path or URL
    description TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Project -> Nature (project-specific context blocks)
CREATE TABLE project_nature (
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

CREATE TABLE sessions (
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

CREATE INDEX idx_sessions_agent ON sessions(agent_id);
CREATE INDEX idx_sessions_orchestrator ON sessions(orchestrator_id);
CREATE INDEX idx_sessions_started ON sessions(started_at);

-- ============================================
-- POOLS (project pool registration)
-- ============================================

CREATE TABLE pools (
    project TEXT PRIMARY KEY,
    base_repo TEXT NOT NULL,
    pool_dir TEXT NOT NULL,
    size INTEGER NOT NULL DEFAULT 3,
    default_branch TEXT DEFAULT 'main',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE pool_settings (
    project TEXT NOT NULL,
    key TEXT NOT NULL,              -- deploy_host, deploy_user, deploy_dir, base_port, repo_url
    value TEXT,
    PRIMARY KEY (project, key),
    FOREIGN KEY (project) REFERENCES pools(project) ON DELETE CASCADE
);

-- ============================================
-- POOL SLOTS (worktree instances)
-- ============================================

CREATE TABLE pool_slots (
    id INTEGER NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    branch TEXT,
    agent_id TEXT,
    session_id TEXT,
    status TEXT NOT NULL DEFAULT 'ready',
    acquired_at INTEGER,
    released_at INTEGER,
    PRIMARY KEY (project, id),
    FOREIGN KEY (project) REFERENCES pools(project) ON DELETE CASCADE
);

CREATE INDEX idx_pool_slots_status ON pool_slots(project, status);

-- ============================================
-- DEV SERVERS (preview instances on remote)
-- ============================================

CREATE TABLE dev_servers (
    project TEXT NOT NULL,
    slot_id INTEGER NOT NULL,
    port INTEGER NOT NULL,
    pid INTEGER,
    branch TEXT,
    status TEXT DEFAULT 'stopped',
    deploy_host TEXT,
    deployed_at INTEGER,
    stopped_at INTEGER,
    PRIMARY KEY (project, slot_id),
    FOREIGN KEY (project, slot_id) REFERENCES pool_slots(project, id) ON DELETE CASCADE
);
