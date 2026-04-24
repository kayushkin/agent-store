package agentstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TrackedFile is a reference to a file on disk that agent-store indexes.
// The DB holds no content — reads and writes hit the filesystem live.
type TrackedFile struct {
	ID            int64  `json:"id"`
	Path          string `json:"path"` // canonical path (no .disabled suffix)
	Scope         string `json:"scope"`
	AgentSlug     string `json:"agent_slug,omitempty"`
	Enabled       bool   `json:"enabled"`
	FSHash        string `json:"fs_hash,omitempty"`
	Size          int64  `json:"size"`
	MTime         int64  `json:"mtime"`
	LastScannedAt int64  `json:"last_scanned_at"`
	Status        string `json:"status"` // present, missing
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// Well-known agent-config filenames (matched anywhere outside ~/.claude).
var agentFileNames = map[string]bool{
	"CLAUDE.md":               true,
	"AGENTS.md":               true,
	"GEMINI.md":               true,
	"copilot-instructions.md": true,
	".cursorrules":            true,
	".clinerules":             true,
	".windsurfrules":          true,
	".aider.conf.yml":         true,
	".continuerules":          true,
}

// Directories to skip during the walk.
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".cache":       true,
	"target":       true,
	".pnpm-store":  true,
	".yarn":        true,
}

// classifyFile returns the scope for a file, or ("", false) if it is not tracked.
// Skills are intentionally excluded — skill-store owns those.
func classifyFile(absPath, home string) (string, bool) {
	base := filepath.Base(absPath)
	// Strip .disabled suffix for classification purposes.
	base = strings.TrimSuffix(base, ".disabled")

	claudeRoot := filepath.Join(home, ".claude")
	if strings.HasPrefix(absPath, claudeRoot+string(os.PathSeparator)) {
		if !strings.HasSuffix(base, ".md") {
			return "", false
		}
		rel := strings.TrimPrefix(absPath, claudeRoot+string(os.PathSeparator))
		rel = strings.TrimSuffix(rel, ".disabled")
		parts := strings.Split(rel, string(os.PathSeparator))
		if len(parts) == 1 {
			return "global", true
		}
		switch parts[0] {
		case "commands":
			return "command", true
		case "agents":
			return "subagent", true
		case "projects":
			for i, p := range parts {
				if p == "memory" && i > 1 {
					return "memory", true
				}
			}
		}
		return "", false
	}

	inberRoot := filepath.Join(home, "inber-workspace", "agents")
	if strings.HasPrefix(absPath, inberRoot+string(os.PathSeparator)) {
		rel := strings.TrimPrefix(absPath, inberRoot+string(os.PathSeparator))
		rel = strings.TrimSuffix(rel, ".disabled")
		if !strings.Contains(rel, string(os.PathSeparator)) && strings.HasSuffix(rel, ".md") {
			return "inber", true
		}
		return "", false
	}

	if absPath == filepath.Join(home, "CLAUDE.md") ||
		absPath == filepath.Join(home, "CLAUDE.md.disabled") {
		return "global", true
	}

	if agentFileNames[base] {
		return "project", true
	}
	return "", false
}

// canonicalPath strips the .disabled suffix if present.
func canonicalPath(p string) (string, bool) {
	if strings.HasSuffix(p, ".disabled") {
		return strings.TrimSuffix(p, ".disabled"), false
	}
	return p, true
}

func hashFile(path string) (string, int64, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), info.Size(), info.ModTime().Unix(), nil
}

// agentSlugFromPath returns the filename stem if it matches a known agent slug.
// Returns empty string if no match or scope doesn't link to agents.
func (s *Store) agentSlugFromPath(path, scope string) string {
	if scope != "subagent" && scope != "inber" {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(path), ".disabled")
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return ""
	}
	var slug string
	err := s.db.QueryRow("SELECT slug FROM agents WHERE slug = ?", stem).Scan(&slug)
	if err != nil {
		return ""
	}
	return slug
}

// ScanResult is the aggregate of one full scan.
type ScanResult struct {
	Scanned int `json:"scanned"`
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Missing int `json:"missing"`
}

// Scan walks $HOME (with a curated set of skip paths), classifies every
// matching file, and upserts tracked_files rows. Rows whose underlying file
// has disappeared (in both enabled and .disabled variants) are marked missing.
//
// We skip third-party caches (~/.config, ~/.codex) and ephemeral forge envs
// so imported plugin/skill bundles don't pollute the index.
func (s *Store) Scan() (*ScanResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home: %w", err)
	}

	// Absolute paths to skip during the walk.
	skipPaths := map[string]bool{
		filepath.Join(home, ".config"):                          true,
		filepath.Join(home, ".codex"):                           true,
		filepath.Join(home, ".cache"):                           true,
		filepath.Join(home, "forge", "envs"):                    true,
		filepath.Join(home, ".claude", "plugins"):               true,
		filepath.Join(home, ".claude", "skills"):                true,
		filepath.Join(home, ".claude", "cache"):                 true,
		filepath.Join(home, ".claude", "sessions"):              true,
		filepath.Join(home, ".claude", "file-history"):          true,
		filepath.Join(home, ".claude", "backups"):               true,
		filepath.Join(home, ".claude", "debug"):                 true,
		filepath.Join(home, ".claude", "downloads"):             true,
		filepath.Join(home, ".claude", "shell-snapshots"):       true,
		filepath.Join(home, ".claude", "telemetry"):             true,
		filepath.Join(home, ".claude", "session-env"):           true,
		filepath.Join(home, ".claude", "mcp-needs-auth-cache"):  true,
	}

	res := &ScanResult{}
	seen := make(map[string]bool)

	visitFile := func(path string) {
		scope, ok := classifyFile(path, home)
		if !ok {
			return
		}
		canon, enabled := canonicalPath(path)
		if seen[canon] {
			return
		}
		seen[canon] = true
		hash, size, mtime, err := hashFile(path)
		if err != nil {
			return
		}
		slug := s.agentSlugFromPath(canon, scope)
		added, err := s.upsertTrackedFile(canon, scope, slug, enabled, hash, size, mtime)
		if err != nil {
			return
		}
		res.Scanned++
		if added {
			res.Added++
		} else {
			res.Updated++
		}
	}

	err = filepath.WalkDir(home, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			if skipPaths[path] {
				return fs.SkipDir
			}
			return nil
		}
		visitFile(path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Mark rows whose underlying file (in either variant) no longer exists.
	rows, err := s.db.Query("SELECT id, path, enabled FROM tracked_files WHERE status != 'missing'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type stale struct {
		id   int64
		path string
		en   bool
	}
	var candidates []stale
	for rows.Next() {
		var st stale
		var en int
		if err := rows.Scan(&st.id, &st.path, &en); err != nil {
			return nil, err
		}
		st.en = en == 1
		candidates = append(candidates, st)
	}
	for _, c := range candidates {
		if seen[c.path] {
			continue
		}
		if _, err := os.Stat(c.path); err == nil {
			continue
		}
		if _, err := os.Stat(c.path + ".disabled"); err == nil {
			continue
		}
		s.db.Exec("UPDATE tracked_files SET status='missing', updated_at=? WHERE id=?", now(), c.id)
		res.Missing++
	}

	return res, nil
}

func (s *Store) upsertTrackedFile(path, scope, agentSlug string, enabled bool, hash string, size, mtime int64) (bool, error) {
	var id int64
	err := s.db.QueryRow("SELECT id FROM tracked_files WHERE path = ?", path).Scan(&id)
	if err == nil {
		_, err := s.db.Exec(`
			UPDATE tracked_files
			SET scope=?, agent_slug=?, enabled=?, fs_hash=?, size=?, mtime=?, last_scanned_at=?, status='present', updated_at=?
			WHERE id=?`,
			scope, nullIfEmpty(agentSlug), boolToInt(enabled), hash, size, mtime, now(), now(), id)
		return false, err
	}
	_, err = s.db.Exec(`
		INSERT INTO tracked_files (path, scope, agent_slug, enabled, fs_hash, size, mtime, last_scanned_at, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'present', ?, ?)`,
		path, scope, nullIfEmpty(agentSlug), boolToInt(enabled), hash, size, mtime, now(), now(), now())
	return true, err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ListTrackedFiles returns all rows, optionally filtered. Empty filters = all.
func (s *Store) ListTrackedFiles(scope, agentSlug string) ([]TrackedFile, error) {
	q := "SELECT id, path, scope, COALESCE(agent_slug,''), enabled, COALESCE(fs_hash,''), COALESCE(size,0), COALESCE(mtime,0), COALESCE(last_scanned_at,0), status, created_at, updated_at FROM tracked_files WHERE 1=1"
	var args []any
	if scope != "" {
		q += " AND scope = ?"
		args = append(args, scope)
	}
	if agentSlug != "" {
		q += " AND agent_slug = ?"
		args = append(args, agentSlug)
	}
	q += " ORDER BY scope, path"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackedFile
	for rows.Next() {
		var f TrackedFile
		var en int
		if err := rows.Scan(&f.ID, &f.Path, &f.Scope, &f.AgentSlug, &en, &f.FSHash, &f.Size, &f.MTime, &f.LastScannedAt, &f.Status, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.Enabled = en == 1
		out = append(out, f)
	}
	return out, nil
}

// GetTrackedFile fetches a single row by ID.
func (s *Store) GetTrackedFile(id int64) (*TrackedFile, error) {
	var f TrackedFile
	var en int
	err := s.db.QueryRow("SELECT id, path, scope, COALESCE(agent_slug,''), enabled, COALESCE(fs_hash,''), COALESCE(size,0), COALESCE(mtime,0), COALESCE(last_scanned_at,0), status, created_at, updated_at FROM tracked_files WHERE id=?", id).
		Scan(&f.ID, &f.Path, &f.Scope, &f.AgentSlug, &en, &f.FSHash, &f.Size, &f.MTime, &f.LastScannedAt, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	f.Enabled = en == 1
	return &f, nil
}

// DiskPath returns the on-disk path (canonical or .disabled variant) for a row.
func (f *TrackedFile) DiskPath() string {
	if f.Enabled {
		return f.Path
	}
	return f.Path + ".disabled"
}

// ReadTrackedFile returns the on-disk content for a tracked file.
func (s *Store) ReadTrackedFile(id int64) (*TrackedFile, []byte, error) {
	f, err := s.GetTrackedFile(id)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(f.DiskPath())
	if err != nil {
		return f, nil, err
	}
	return f, data, nil
}

// WriteTrackedFile writes new content to disk and updates the row's hash/size/mtime.
func (s *Store) WriteTrackedFile(id int64, content []byte) (*TrackedFile, error) {
	f, err := s.GetTrackedFile(id)
	if err != nil {
		return nil, err
	}
	diskPath := f.DiskPath()
	tmp := diskPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, diskPath); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	info, _ := os.Stat(diskPath)
	var size, mtime int64
	if info != nil {
		size = info.Size()
		mtime = info.ModTime().Unix()
	}
	_, err = s.db.Exec("UPDATE tracked_files SET fs_hash=?, size=?, mtime=?, last_scanned_at=?, status='present', updated_at=? WHERE id=?",
		hash, size, mtime, now(), now(), id)
	if err != nil {
		return nil, err
	}
	return s.GetTrackedFile(id)
}

// SetTrackedFileEnabled renames on disk (canonical ↔ .disabled) and updates the row.
func (s *Store) SetTrackedFileEnabled(id int64, enable bool) (*TrackedFile, error) {
	f, err := s.GetTrackedFile(id)
	if err != nil {
		return nil, err
	}
	if f.Enabled == enable {
		return f, nil
	}
	from := f.DiskPath()
	to := f.Path
	if !enable {
		to = f.Path + ".disabled"
	}
	if _, err := os.Stat(from); err != nil {
		return nil, fmt.Errorf("source file missing: %s", from)
	}
	if err := os.Rename(from, to); err != nil {
		return nil, err
	}
	_, err = s.db.Exec("UPDATE tracked_files SET enabled=?, updated_at=? WHERE id=?", boolToInt(enable), now(), id)
	if err != nil {
		return nil, err
	}
	return s.GetTrackedFile(id)
}
