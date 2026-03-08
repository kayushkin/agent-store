package agentstore

import (
	"database/sql"
	"fmt"
	"time"
)

// PoolConfig represents a registered project pool
type PoolConfig struct {
	Project       string
	BaseRepo      string
	PoolDir       string
	Size          int
	DefaultBranch string
	Settings      map[string]string // deploy_host, deploy_user, deploy_dir, base_port, repo_url
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// RegisterPool registers a project pool in the database
func (s *Store) RegisterPool(cfg PoolConfig) error {
	now := time.Now()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}
	cfg.UpdatedAt = now
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = "main"
	}
	if cfg.Size == 0 {
		cfg.Size = 3
	}

	_, err := s.db.Exec(`
		INSERT INTO pools (project, base_repo, pool_dir, size, default_branch, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project) DO UPDATE SET
			base_repo = excluded.base_repo,
			pool_dir = excluded.pool_dir,
			size = excluded.size,
			default_branch = excluded.default_branch,
			updated_at = excluded.updated_at
	`, cfg.Project, cfg.BaseRepo, cfg.PoolDir, cfg.Size, cfg.DefaultBranch, cfg.CreatedAt.Unix(), cfg.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("register pool: %w", err)
	}

	// Store settings
	for k, v := range cfg.Settings {
		if err := s.SetPoolSetting(cfg.Project, k, v); err != nil {
			return fmt.Errorf("set pool setting %s: %w", k, err)
		}
	}

	return nil
}

// GetPool retrieves a pool config by project name
func (s *Store) GetPool(project string) (*PoolConfig, error) {
	cfg := &PoolConfig{Project: project}
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
		SELECT base_repo, pool_dir, size, default_branch, created_at, updated_at
		FROM pools WHERE project = ?
	`, project).Scan(&cfg.BaseRepo, &cfg.PoolDir, &cfg.Size, &cfg.DefaultBranch, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("pool %q not registered", project)
	}
	if err != nil {
		return nil, err
	}
	cfg.CreatedAt = time.Unix(createdAt, 0)
	cfg.UpdatedAt = time.Unix(updatedAt, 0)

	// Load settings
	settings, err := s.GetPoolSettings(project)
	if err != nil {
		return nil, err
	}
	cfg.Settings = settings

	return cfg, nil
}

// ListPools returns all registered pools
func (s *Store) ListPools() ([]PoolConfig, error) {
	rows, err := s.db.Query(`
		SELECT project, base_repo, pool_dir, size, default_branch, created_at, updated_at
		FROM pools ORDER BY project
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PoolConfig
	for rows.Next() {
		var cfg PoolConfig
		var createdAt, updatedAt int64
		if err := rows.Scan(&cfg.Project, &cfg.BaseRepo, &cfg.PoolDir, &cfg.Size, &cfg.DefaultBranch, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		cfg.CreatedAt = time.Unix(createdAt, 0)
		cfg.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, cfg)
	}
	return results, nil
}

// SetPoolSetting sets a key-value setting for a pool
func (s *Store) SetPoolSetting(project, key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO pool_settings (project, key, value)
		VALUES (?, ?, ?)
		ON CONFLICT(project, key) DO UPDATE SET value = excluded.value
	`, project, key, value)
	return err
}

// GetPoolSetting gets a setting value for a pool
func (s *Store) GetPoolSetting(project, key string) (string, error) {
	var value string
	err := s.db.QueryRow(`
		SELECT value FROM pool_settings WHERE project = ? AND key = ?
	`, project, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// GetPoolSettings returns all settings for a pool as a map
func (s *Store) GetPoolSettings(project string) (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT key, value FROM pool_settings WHERE project = ?
	`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

// ============================================
// DEV SERVERS
// ============================================

// DevServer represents a preview server instance
type DevServer struct {
	Project    string
	SlotID     int
	Port       int
	PID        int
	Branch     string
	Status     string // stopped, running, error
	DeployHost string
	DeployedAt *time.Time
	StoppedAt  *time.Time
}

// SetDevServer upserts a dev server record
func (s *Store) SetDevServer(ds DevServer) error {
	var deployedAt, stoppedAt interface{}
	if ds.DeployedAt != nil {
		deployedAt = ds.DeployedAt.Unix()
	}
	if ds.StoppedAt != nil {
		stoppedAt = ds.StoppedAt.Unix()
	}

	_, err := s.db.Exec(`
		INSERT INTO dev_servers (project, slot_id, port, pid, branch, status, deploy_host, deployed_at, stopped_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project, slot_id) DO UPDATE SET
			port = excluded.port,
			pid = excluded.pid,
			branch = excluded.branch,
			status = excluded.status,
			deploy_host = excluded.deploy_host,
			deployed_at = excluded.deployed_at,
			stopped_at = excluded.stopped_at
	`, ds.Project, ds.SlotID, ds.Port, ds.PID, ds.Branch, ds.Status, ds.DeployHost, deployedAt, stoppedAt)
	return err
}

// GetDevServer retrieves a dev server by project and slot
func (s *Store) GetDevServer(project string, slotID int) (*DevServer, error) {
	ds := &DevServer{Project: project, SlotID: slotID}
	var deployedAt, stoppedAt sql.NullInt64
	err := s.db.QueryRow(`
		SELECT port, pid, branch, status, deploy_host, deployed_at, stopped_at
		FROM dev_servers WHERE project = ? AND slot_id = ?
	`, project, slotID).Scan(&ds.Port, &ds.PID, &ds.Branch, &ds.Status, &ds.DeployHost, &deployedAt, &stoppedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if deployedAt.Valid {
		t := time.Unix(deployedAt.Int64, 0)
		ds.DeployedAt = &t
	}
	if stoppedAt.Valid {
		t := time.Unix(stoppedAt.Int64, 0)
		ds.StoppedAt = &t
	}
	return ds, nil
}

// ListDevServers returns all dev servers, optionally filtered by project
func (s *Store) ListDevServers(project string) ([]DevServer, error) {
	query := `SELECT project, slot_id, port, pid, branch, status, deploy_host, deployed_at, stopped_at FROM dev_servers`
	args := []any{}
	if project != "" {
		query += " WHERE project = ?"
		args = append(args, project)
	}
	query += " ORDER BY project, slot_id"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []DevServer
	for rows.Next() {
		var ds DevServer
		var deployedAt, stoppedAt sql.NullInt64
		if err := rows.Scan(&ds.Project, &ds.SlotID, &ds.Port, &ds.PID, &ds.Branch, &ds.Status, &ds.DeployHost, &deployedAt, &stoppedAt); err != nil {
			return nil, err
		}
		if deployedAt.Valid {
			t := time.Unix(deployedAt.Int64, 0)
			ds.DeployedAt = &t
		}
		if stoppedAt.Valid {
			t := time.Unix(stoppedAt.Int64, 0)
			ds.StoppedAt = &t
		}
		results = append(results, ds)
	}
	return results, nil
}
