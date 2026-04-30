package agentstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedContextEntry is one tracked file that contributed to a resolved
// context. Path/scope mirror tracked_files; Bytes is the byte count of the
// content actually read from disk (post-enable check).
type ResolvedContextEntry struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
	Bytes int    `json:"bytes"`
}

// ResolvedContext is the merged context that a non-native harness should be
// started with. Content is the concatenated body, ready to ship as
// system_prompt. Manifest lists which files contributed and from which
// scope, so the UI can show the user how the prompt was assembled.
//
// SkipReason is non-empty when the requested harness reads context files
// natively (e.g., Claude Code reads CLAUDE.md itself); callers MUST NOT
// inject Content in that case — doing so duplicates context the harness
// will already discover on its own.
type ResolvedContext struct {
	Harness    string                 `json:"harness"`
	WorkDir    string                 `json:"work_dir,omitempty"`
	SkipReason string                 `json:"skip_reason,omitempty"`
	Content    string                 `json:"content"`
	Manifest   []ResolvedContextEntry `json:"manifest"`
}

// nativeHarnesses read CLAUDE.md (and similar agent-config files) from disk
// themselves on every session start. The bridge does NOT inject for these:
// injection would duplicate what the harness will already discover.
var nativeHarnesses = map[string]bool{
	"claude-code": true,
	"claude":      true,
}

// contextFilenames is the basename allowlist for context resolution. Today
// only AGENTS.md is injected — it's the canonical "all agents" file.
// CLAUDE.md is intentionally excluded (Claude reads it natively; other
// harnesses use AGENTS.md). Per-harness files like GEMINI.md can be added
// here later if we want the bridge to inject them per-harness.
var contextFilenames = map[string]bool{
	"AGENTS.md": true,
}

// ResolveContext returns the merged context that should be injected as a
// system prompt for the given non-native harness. Pulls AGENTS.md (and any
// other entries in contextFilenames) from scope=global and, when workDir is
// supplied, from scope=project where the file's directory is workDir or one
// of its ancestors.
//
// For native harnesses, returns SkipReason set and empty Content — callers
// MUST honor SkipReason and not inject anything.
func (s *Store) ResolveContext(harness, workDir string) (*ResolvedContext, error) {
	out := &ResolvedContext{
		Harness:  harness,
		WorkDir:  workDir,
		Manifest: []ResolvedContextEntry{},
	}

	if nativeHarnesses[harness] {
		out.SkipReason = "harness discovers context files natively"
		return out, nil
	}

	files, err := s.ListTrackedFiles("", "")
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}

	if workDir != "" {
		workDir = filepath.Clean(workDir)
	}

	var globals, projects []TrackedFile
	for _, f := range files {
		if !f.Enabled || f.Status != "present" {
			continue
		}
		if !contextFilenames[filepath.Base(f.Path)] {
			continue
		}
		switch f.Scope {
		case "global":
			globals = append(globals, f)
		case "project":
			if workDir == "" {
				continue
			}
			dir := filepath.Dir(f.Path)
			if dir == workDir || strings.HasPrefix(workDir, dir+string(os.PathSeparator)) {
				projects = append(projects, f)
			}
		}
	}

	var sb strings.Builder
	appendOne := func(f TrackedFile) {
		data, err := os.ReadFile(f.DiskPath())
		if err != nil {
			return
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		// Header makes the source legible if the model dumps its system
		// prompt — debugging beats opacity.
		fmt.Fprintf(&sb, "===== %s (%s) =====\n\n", f.Path, f.Scope)
		sb.Write(data)
		out.Manifest = append(out.Manifest, ResolvedContextEntry{
			Path:  f.Path,
			Scope: f.Scope,
			Bytes: len(data),
		})
	}

	for _, f := range globals {
		appendOne(f)
	}
	for _, f := range projects {
		appendOne(f)
	}

	out.Content = sb.String()
	return out, nil
}
