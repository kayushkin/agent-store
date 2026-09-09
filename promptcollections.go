package agentstore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	PromptAppliesAll    = "all"
	PromptAppliesClaude = "claude"
	PromptAppliesAgents = "agents"

	promptCollectionScopeGlobal  = "global"
	promptCollectionScopeProject = "project"
)

type PromptCollection struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Scope       string `json:"scope"`
	RootPath    string `json:"root_path"`
	Description string `json:"description,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type PromptSection struct {
	ID           int64  `json:"id"`
	CollectionID int64  `json:"collection_id"`
	Title        string `json:"title"`
	Heading      string `json:"heading,omitempty"`
	Body         string `json:"body"`
	AppliesTo    string `json:"applies_to"`
	Priority     int    `json:"priority"`
	Enabled      bool   `json:"enabled"`
	SourcePath   string `json:"source_path,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type PromptOutput struct {
	Target        string `json:"target"`
	Path          string `json:"path"`
	TrackedFileID int64  `json:"tracked_file_id,omitempty"`
	Exists        bool   `json:"exists"`
	Content       string `json:"content"`
	CurrentSHA256 string `json:"current_sha256,omitempty"`
}

type PromptCollectionView struct {
	Collection PromptCollection `json:"collection"`
	Sections   []PromptSection  `json:"sections"`
	Outputs    []PromptOutput   `json:"outputs"`
}

type PromptCollectionMutationResult struct {
	View                *PromptCollectionView `json:"view"`
	MaterializedOutputs []TrackedFile         `json:"materialized_outputs,omitempty"`
}

func (s *Store) ListPromptCollectionViews() ([]PromptCollectionView, error) {
	if err := s.EnsurePromptCollectionsSeeded(); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT id, slug, title, scope, root_path, COALESCE(description,''), created_at, updated_at
		FROM prompt_collections
		ORDER BY CASE scope WHEN 'global' THEN 0 ELSE 1 END, title, root_path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []PromptCollectionView
	for rows.Next() {
		var c PromptCollection
		if err := rows.Scan(&c.ID, &c.Slug, &c.Title, &c.Scope, &c.RootPath, &c.Description, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		v, err := s.GetPromptCollectionView(c.ID)
		if err != nil {
			return nil, err
		}
		views = append(views, *v)
	}
	return views, rows.Err()
}

func (s *Store) GetPromptCollectionView(collectionID int64) (*PromptCollectionView, error) {
	c, err := s.getPromptCollection(collectionID)
	if err != nil {
		return nil, err
	}
	sections, err := s.ListPromptSections(collectionID)
	if err != nil {
		return nil, err
	}
	outputs, err := s.previewPromptCollection(*c, sections)
	if err != nil {
		return nil, err
	}
	return &PromptCollectionView{Collection: *c, Sections: sections, Outputs: outputs}, nil
}

func (s *Store) CreatePromptCollection(scope, rootPath, title, description string) (*PromptCollectionView, error) {
	rootPath = filepath.Clean(rootPath)
	if scope != promptCollectionScopeGlobal && scope != promptCollectionScopeProject {
		return nil, fmt.Errorf("unknown scope %q", scope)
	}
	if rootPath == "" || rootPath == "." {
		return nil, fmt.Errorf("root_path is required")
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("root_path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root_path is not a directory")
	}
	if title == "" {
		if scope == promptCollectionScopeGlobal {
			title = "Main prompt"
		} else {
			title = filepath.Base(rootPath)
		}
	}

	slug := slugFromRoot(scope, rootPath)
	if _, err := s.db.Exec(`
		INSERT INTO prompt_collections (slug, title, scope, root_path, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, root_path) DO UPDATE SET
			title=excluded.title,
			description=excluded.description,
			updated_at=excluded.updated_at
	`, slug, title, scope, rootPath, description, now(), now()); err != nil {
		return nil, err
	}

	var id int64
	if err := s.db.QueryRow(`SELECT id FROM prompt_collections WHERE scope=? AND root_path=?`, scope, rootPath).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetPromptCollectionView(id)
}

func (s *Store) ListPromptSections(collectionID int64) ([]PromptSection, error) {
	rows, err := s.db.Query(`
		SELECT id, collection_id, title, COALESCE(heading,''), body, applies_to, priority, enabled,
		       COALESCE(source_path,''), created_at, updated_at
		FROM prompt_sections
		WHERE collection_id=?
		ORDER BY priority, id
	`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PromptSection
	for rows.Next() {
		var ps PromptSection
		var enabled int
		if err := rows.Scan(&ps.ID, &ps.CollectionID, &ps.Title, &ps.Heading, &ps.Body, &ps.AppliesTo, &ps.Priority, &enabled, &ps.SourcePath, &ps.CreatedAt, &ps.UpdatedAt); err != nil {
			return nil, err
		}
		ps.Enabled = enabled == 1
		out = append(out, ps)
	}
	return out, rows.Err()
}

func (s *Store) CreatePromptSection(collectionID int64, section *PromptSection) (*PromptCollectionMutationResult, error) {
	if err := s.validatePromptSection(section); err != nil {
		return nil, err
	}
	if _, err := s.getPromptCollection(collectionID); err != nil {
		return nil, err
	}
	ts := now()
	res, err := s.db.Exec(`
		INSERT INTO prompt_sections (collection_id, title, heading, body, applies_to, priority, enabled, source_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, collectionID, section.Title, section.Heading, section.Body, section.AppliesTo, section.Priority, boolToInt(section.Enabled), section.SourcePath, ts, ts)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(`UPDATE prompt_collections SET updated_at=? WHERE id=?`, now(), collectionID)
	_ = id
	return s.compilePromptCollectionMutation(collectionID)
}

func (s *Store) UpdatePromptSection(sectionID int64, section *PromptSection) (*PromptCollectionMutationResult, error) {
	if err := s.validatePromptSection(section); err != nil {
		return nil, err
	}
	var collectionID int64
	if err := s.db.QueryRow(`SELECT collection_id FROM prompt_sections WHERE id=?`, sectionID).Scan(&collectionID); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`
		UPDATE prompt_sections
		SET title=?, heading=?, body=?, applies_to=?, priority=?, enabled=?, source_path=?, updated_at=?
		WHERE id=?
	`, section.Title, section.Heading, section.Body, section.AppliesTo, section.Priority, boolToInt(section.Enabled), section.SourcePath, now(), sectionID); err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(`UPDATE prompt_collections SET updated_at=? WHERE id=?`, now(), collectionID)
	return s.compilePromptCollectionMutation(collectionID)
}

func (s *Store) DeletePromptSection(sectionID int64) (*PromptCollectionMutationResult, error) {
	var collectionID int64
	if err := s.db.QueryRow(`SELECT collection_id FROM prompt_sections WHERE id=?`, sectionID).Scan(&collectionID); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`DELETE FROM prompt_sections WHERE id=?`, sectionID); err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(`UPDATE prompt_collections SET updated_at=? WHERE id=?`, now(), collectionID)
	return s.compilePromptCollectionMutation(collectionID)
}

func (s *Store) EnsurePromptCollectionsSeeded() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if _, err := s.ensurePromptCollection(promptCollectionScopeGlobal, home, "Main prompt", "Structured source for the host-wide prompt files."); err != nil {
		return err
	}

	files, err := s.ListTrackedFiles("", "")
	if err != nil {
		return err
	}
	reposRoot := filepath.Join(home, "repos")
	grouped := map[string][]TrackedFile{}
	for _, f := range files {
		base := filepath.Base(f.Path)
		if base != "CLAUDE.md" && base != "AGENTS.md" {
			continue
		}
		if f.Scope == "global" && filepath.Dir(f.Path) == home {
			grouped[home] = append(grouped[home], f)
			continue
		}
		if f.Scope != "project" {
			continue
		}
		root, ok := promptProjectRoot(f.Path, reposRoot)
		if !ok {
			continue
		}
		grouped[root] = append(grouped[root], f)
	}
	for root := range grouped {
		if root == home {
			continue
		}
		title := filepath.Base(root)
		if _, err := s.ensurePromptCollection(promptCollectionScopeProject, root, title, "Structured source for this project's shared prompt files."); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensurePromptCollection(scope, rootPath, title, description string) (*PromptCollection, error) {
	rootPath = filepath.Clean(rootPath)
	var id int64
	err := s.db.QueryRow(`SELECT id FROM prompt_collections WHERE scope=? AND root_path=?`, scope, rootPath).Scan(&id)
	switch {
	case err == nil:
		c, err := s.getPromptCollection(id)
		if err != nil {
			return nil, err
		}
		if err := s.seedPromptSectionsIfMissing(*c); err != nil {
			return nil, err
		}
		return c, nil
	case err != sql.ErrNoRows:
		return nil, err
	}
	ts := now()
	slug := slugFromRoot(scope, rootPath)
	res, err := s.db.Exec(`
		INSERT INTO prompt_collections (slug, title, scope, root_path, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, slug, title, scope, rootPath, description, ts, ts)
	if err != nil {
		return nil, err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return nil, err
	}
	c, err := s.getPromptCollection(id)
	if err != nil {
		return nil, err
	}
	if err := s.seedPromptSectionsIfMissing(*c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) seedPromptSectionsIfMissing(c PromptCollection) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM prompt_sections WHERE collection_id=?`, c.ID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	ts := now()
	for _, target := range []string{PromptAppliesClaude, PromptAppliesAgents} {
		path := filepath.Join(c.RootPath, promptFilenameForTarget(target))
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		sections := splitPromptSections(string(data))
		if len(sections) == 0 {
			sections = []PromptSection{{
				Title:      "Imported " + filepath.Base(path),
				Body:       strings.TrimSpace(string(data)),
				AppliesTo:  target,
				Priority:   100,
				Enabled:    true,
				SourcePath: path,
			}}
		}
		for i, section := range sections {
			title := section.Title
			if title == "" {
				title = "Imported " + filepath.Base(path)
			}
			if _, err := s.db.Exec(`
				INSERT INTO prompt_sections (collection_id, title, heading, body, applies_to, priority, enabled, source_path, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
			`, c.ID, title, section.Heading, section.Body, target, i*100, path, ts, ts); err != nil {
				return err
			}
		}
	}
	return nil
}

func splitPromptSections(content string) []PromptSection {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	type rawSection struct {
		heading string
		lines   []string
	}
	var raw []rawSection
	current := rawSection{}
	flush := func() {
		if current.heading == "" && len(current.lines) == 0 {
			return
		}
		raw = append(raw, current)
		current = rawSection{}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			flush()
			current.heading = line
			continue
		}
		current.lines = append(current.lines, line)
	}
	flush()
	if len(raw) == 0 {
		body := strings.TrimSpace(content)
		if body == "" {
			return nil
		}
		return []PromptSection{{Title: "Imported prompt", Body: body}}
	}

	out := make([]PromptSection, 0, len(raw))
	for _, section := range raw {
		body := strings.TrimSpace(strings.Join(section.lines, "\n"))
		title := strings.TrimSpace(strings.TrimPrefix(section.heading, "#"))
		if title == "" {
			title = "Preamble"
		}
		out = append(out, PromptSection{
			Title:   title,
			Heading: section.heading,
			Body:    body,
			Enabled: true,
		})
	}
	return out
}

func (s *Store) compilePromptCollectionMutation(collectionID int64) (*PromptCollectionMutationResult, error) {
	rendered, err := s.CompilePromptCollection(collectionID)
	if err != nil {
		return nil, err
	}
	view, err := s.GetPromptCollectionView(collectionID)
	if err != nil {
		return nil, err
	}
	out := &PromptCollectionMutationResult{View: view}
	for _, materialized := range rendered {
		out.MaterializedOutputs = append(out.MaterializedOutputs, *materialized.File)
	}
	return out, nil
}

type compiledPromptOutput struct {
	File    *TrackedFile
	Version *TrackedFileVersion
}

func (s *Store) CompilePromptCollection(collectionID int64) ([]compiledPromptOutput, error) {
	c, err := s.getPromptCollection(collectionID)
	if err != nil {
		return nil, err
	}
	sections, err := s.ListPromptSections(collectionID)
	if err != nil {
		return nil, err
	}

	var out []compiledPromptOutput
	for _, target := range []string{PromptAppliesClaude, PromptAppliesAgents} {
		content := renderPromptSections(sections, target)
		if strings.TrimSpace(content) == "" {
			continue
		}
		path := filepath.Join(c.RootPath, promptFilenameForTarget(target))
		file, version, err := s.writeManagedPromptFile(path, []byte(content))
		if err != nil {
			return nil, err
		}
		out = append(out, compiledPromptOutput{File: file, Version: version})
	}
	_, _ = s.db.Exec(`UPDATE prompt_collections SET updated_at=? WHERE id=?`, now(), collectionID)
	return out, nil
}

func renderPromptSections(sections []PromptSection, target string) string {
	filtered := make([]PromptSection, 0, len(sections))
	for _, section := range sections {
		if !section.Enabled {
			continue
		}
		if section.AppliesTo != PromptAppliesAll && section.AppliesTo != target {
			continue
		}
		filtered = append(filtered, section)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Priority != filtered[j].Priority {
			return filtered[i].Priority < filtered[j].Priority
		}
		return filtered[i].ID < filtered[j].ID
	})

	parts := make([]string, 0, len(filtered))
	for _, section := range filtered {
		var b strings.Builder
		heading := strings.TrimSpace(section.Heading)
		body := strings.TrimSpace(section.Body)
		if heading != "" {
			b.WriteString(heading)
			if body != "" {
				b.WriteString("\n\n")
			}
		}
		if body != "" {
			b.WriteString(body)
		}
		part := strings.TrimSpace(b.String())
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func (s *Store) writeManagedPromptFile(path string, content []byte) (*TrackedFile, *TrackedFileVersion, error) {
	path = filepath.Clean(path)
	scope, ok := classifyManagedPromptPath(path)
	if !ok {
		return nil, nil, fmt.Errorf("path %q is not a managed prompt target", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, err
	}

	var fileID int64
	err := s.db.QueryRow(`SELECT id FROM tracked_files WHERE path=?`, path).Scan(&fileID)
	switch {
	case err == nil:
		file, version, err := s.WriteTrackedFileVersioned(fileID, content, "prompt-compile", "", "")
		if err != nil {
			return nil, nil, err
		}
		return file, version, nil
	case err != sql.ErrNoRows:
		return nil, nil, err
	}

	if err := os.WriteFile(path, content, 0644); err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	fileID, _, err = s.upsertTrackedFile(path, scope, "", true, hash, info.Size(), info.ModTime().Unix())
	if err != nil {
		return nil, nil, err
	}
	version, err := s.AppendVersion(fileID, content, "prompt-compile", "", "")
	if err != nil {
		return nil, nil, err
	}
	file, err := s.GetTrackedFile(fileID)
	if err != nil {
		return nil, nil, err
	}
	return file, version, nil
}

func classifyManagedPromptPath(path string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	base := filepath.Base(path)
	if base != "CLAUDE.md" && base != "AGENTS.md" {
		return "", false
	}
	dir := filepath.Dir(path)
	if dir == home {
		return "global", true
	}
	if root, ok := promptProjectRoot(path, filepath.Join(home, "repos")); ok && root == dir {
		return "project", true
	}
	return "", false
}

func promptProjectRoot(path, reposRoot string) (string, bool) {
	path = filepath.Clean(path)
	reposRoot = filepath.Clean(reposRoot)
	if !strings.HasPrefix(path, reposRoot+string(os.PathSeparator)) {
		return "", false
	}
	rel := strings.TrimPrefix(path, reposRoot+string(os.PathSeparator))
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) != 2 {
		return "", false
	}
	if parts[1] != "CLAUDE.md" && parts[1] != "AGENTS.md" {
		return "", false
	}
	return filepath.Join(reposRoot, parts[0]), true
}

func promptFilenameForTarget(target string) string {
	if target == PromptAppliesAgents {
		return "AGENTS.md"
	}
	return "CLAUDE.md"
}

func slugFromRoot(scope, rootPath string) string {
	root := filepath.Base(rootPath)
	if scope == promptCollectionScopeGlobal {
		return "main"
	}
	root = strings.ToLower(root)
	root = strings.ReplaceAll(root, " ", "-")
	root = strings.ReplaceAll(root, "_", "-")
	return "project-" + root
}

func (s *Store) previewPromptCollection(c PromptCollection, sections []PromptSection) ([]PromptOutput, error) {
	outputs := make([]PromptOutput, 0, 2)
	for _, target := range []string{PromptAppliesClaude, PromptAppliesAgents} {
		path := filepath.Join(c.RootPath, promptFilenameForTarget(target))
		output := PromptOutput{
			Target:  target,
			Path:    path,
			Content: renderPromptSections(sections, target),
		}
		if f, err := s.findTrackedFileByPath(path); err == nil {
			output.TrackedFileID = f.ID
			output.Exists = f.Status == "present"
			output.CurrentSHA256 = f.FSHash
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (s *Store) findTrackedFileByPath(path string) (*TrackedFile, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM tracked_files WHERE path=?`, path).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetTrackedFile(id)
}

func (s *Store) getPromptCollection(id int64) (*PromptCollection, error) {
	var c PromptCollection
	err := s.db.QueryRow(`
		SELECT id, slug, title, scope, root_path, COALESCE(description,''), created_at, updated_at
		FROM prompt_collections
		WHERE id=?
	`, id).Scan(&c.ID, &c.Slug, &c.Title, &c.Scope, &c.RootPath, &c.Description, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) validatePromptSection(section *PromptSection) error {
	if section == nil {
		return fmt.Errorf("section is required")
	}
	if strings.TrimSpace(section.Title) == "" {
		return fmt.Errorf("title is required")
	}
	switch section.AppliesTo {
	case PromptAppliesAll, PromptAppliesClaude, PromptAppliesAgents:
	default:
		return fmt.Errorf("unknown applies_to %q", section.AppliesTo)
	}
	return nil
}
