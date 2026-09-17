package agentstore

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Every harness gets the same prompt: the host collection's sections, then
// the sections of each project collection whose root is the session's working
// directory or an ancestor of it, outermost first. What differs per harness is
// only how it arrives, and that is a row in prompt_harness_deliveries:
//
//   - inject: the bridge puts the resolved prompt in the system prompt.
//   - native_file: the harness reads one rendered file itself
//     (native_relative_path, looked for in each collection's root). The bridge
//     injects only the collections that do NOT render to that file — injecting
//     the rest would hand the harness the same text twice.
//
// There is no default delivery. A harness with no row is an error the caller
// must surface: guessing `inject` for a harness that reads its own file
// doubles the prompt, and guessing `native_file` silently drops it. The first
// version of this code keyed a hardcoded "native" set on `claude-code` while
// the bridge's id is `claude_code`; the lookup never matched, and nothing said so.

const (
	PromptDeliveryInject     = "inject"
	PromptDeliveryNativeFile = "native_file"
)

// ErrPromptHarnessDeliveryUnknown is returned by ResolveContext for a harness
// with no prompt_harness_deliveries row.
var ErrPromptHarnessDeliveryUnknown = errors.New("no prompt delivery is recorded for this harness")

type PromptHarnessDelivery struct {
	Harness            string `json:"harness"`
	Delivery           string `json:"delivery"`
	NativeRelativePath string `json:"native_relative_path,omitempty"`
	Note               string `json:"note,omitempty"`
	UpdatedAt          int64  `json:"updated_at"`
}

func (s *Store) ListPromptHarnessDeliveries() ([]PromptHarnessDelivery, error) {
	rows, err := s.db.Query(`SELECT harness, delivery, COALESCE(native_relative_path,''), COALESCE(note,''), updated_at FROM prompt_harness_deliveries ORDER BY harness`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PromptHarnessDelivery{}
	for rows.Next() {
		var delivery PromptHarnessDelivery
		if err := rows.Scan(&delivery.Harness, &delivery.Delivery, &delivery.NativeRelativePath, &delivery.Note, &delivery.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}

func (s *Store) GetPromptHarnessDelivery(harness string) (*PromptHarnessDelivery, error) {
	var delivery PromptHarnessDelivery
	err := s.db.QueryRow(`SELECT harness, delivery, COALESCE(native_relative_path,''), COALESCE(note,''), updated_at FROM prompt_harness_deliveries WHERE harness=?`, harness).
		Scan(&delivery.Harness, &delivery.Delivery, &delivery.NativeRelativePath, &delivery.Note, &delivery.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %q", ErrPromptHarnessDeliveryUnknown, harness)
	}
	if err != nil {
		return nil, err
	}
	return &delivery, nil
}

func validatePromptHarnessDelivery(delivery *PromptHarnessDelivery) error {
	delivery.Harness = strings.TrimSpace(delivery.Harness)
	if delivery.Harness == "" {
		return errors.New("harness is required")
	}
	switch delivery.Delivery {
	case PromptDeliveryInject:
		if delivery.NativeRelativePath != "" {
			return errors.New("native_relative_path only applies to native_file delivery")
		}
	case PromptDeliveryNativeFile:
		relativePath, err := validatePromptOutputRelativePath(delivery.NativeRelativePath)
		if err != nil {
			return fmt.Errorf("native_file delivery needs the file the harness reads: %w", err)
		}
		delivery.NativeRelativePath = relativePath
	default:
		return fmt.Errorf("delivery must be %q or %q", PromptDeliveryInject, PromptDeliveryNativeFile)
	}
	return nil
}

// SetPromptHarnessDelivery records or replaces how one harness gets the prompt.
func (s *Store) SetPromptHarnessDelivery(delivery PromptHarnessDelivery) (*PromptHarnessDelivery, error) {
	if err := validatePromptHarnessDelivery(&delivery); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`
		INSERT INTO prompt_harness_deliveries (harness, delivery, native_relative_path, note, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (harness) DO UPDATE SET delivery=excluded.delivery, native_relative_path=excluded.native_relative_path, note=excluded.note, updated_at=excluded.updated_at`,
		delivery.Harness, delivery.Delivery, nullIfEmpty(delivery.NativeRelativePath), nullIfEmpty(delivery.Note), now()); err != nil {
		return nil, err
	}
	return s.GetPromptHarnessDelivery(delivery.Harness)
}

// RecordPromptHarnessDeliveryIfAbsent is for the bridge's startup, which knows
// the harness ids: it writes the row only when the harness has none, so a
// choice made on the Files page is never overwritten by a restart.
func (s *Store) RecordPromptHarnessDeliveryIfAbsent(delivery PromptHarnessDelivery) error {
	if err := validatePromptHarnessDelivery(&delivery); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO prompt_harness_deliveries (harness, delivery, native_relative_path, note, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (harness) DO NOTHING`,
		delivery.Harness, delivery.Delivery, nullIfEmpty(delivery.NativeRelativePath), nullIfEmpty(delivery.Note), now())
	return err
}

// ResolvedContextEntry is one collection that contributed to, or was left out
// of, a resolved context.
type ResolvedContextEntry struct {
	CollectionID int64  `json:"collection_id"`
	Slug         string `json:"slug"`
	Scope        string `json:"scope"`
	RootPath     string `json:"root_path"`
	Bytes        int    `json:"bytes"`
	Injected     bool   `json:"injected"`
	// ReadNativelyFrom is the file the harness reads this collection from when
	// it was not injected.
	ReadNativelyFrom string `json:"read_natively_from,omitempty"`
}

// ResolvedContext is what the bridge should put in a harness's system prompt.
// Content may be empty: a native_file harness whose file every applicable
// collection renders to needs nothing injected.
type ResolvedContext struct {
	Harness  string                 `json:"harness"`
	WorkDir  string                 `json:"work_dir,omitempty"`
	Delivery string                 `json:"delivery"`
	Content  string                 `json:"content"`
	Manifest []ResolvedContextEntry `json:"manifest"`
}

// ResolveContext assembles the prompt for one harness in one working
// directory. See the comment at the top of this file for the rule.
func (s *Store) ResolveContext(harness, workDir string) (*ResolvedContext, error) {
	delivery, err := s.GetPromptHarnessDelivery(harness)
	if err != nil {
		return nil, err
	}
	out := &ResolvedContext{Harness: harness, WorkDir: workDir, Delivery: delivery.Delivery, Manifest: []ResolvedContextEntry{}}
	if workDir != "" {
		workDir = filepath.Clean(workDir)
	}

	collections, err := s.ListPromptCollections()
	if err != nil {
		return nil, err
	}
	var applicable []PromptCollection
	for _, c := range collections {
		switch {
		case c.Scope == promptCollectionScopeGlobal:
			applicable = append(applicable, c)
		case workDir != "" && (workDir == c.RootPath || strings.HasPrefix(workDir, c.RootPath+string(os.PathSeparator))):
			applicable = append(applicable, c)
		}
	}
	sort.SliceStable(applicable, func(i, j int) bool {
		if (applicable[i].Scope == promptCollectionScopeGlobal) != (applicable[j].Scope == promptCollectionScopeGlobal) {
			return applicable[i].Scope == promptCollectionScopeGlobal
		}
		return len(applicable[i].RootPath) < len(applicable[j].RootPath)
	})

	var parts []string
	for _, c := range applicable {
		sections, err := s.ListPromptSections(c.ID)
		if err != nil {
			return nil, err
		}
		rendered := strings.TrimSpace(renderPromptSections(sections))
		if rendered == "" {
			continue
		}
		entry := ResolvedContextEntry{CollectionID: c.ID, Slug: c.Slug, Scope: c.Scope, RootPath: c.RootPath, Bytes: len(rendered)}
		if delivery.Delivery == PromptDeliveryNativeFile {
			var rendersToNativeFile int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM prompt_collection_outputs WHERE collection_id=? AND relative_path=? AND enabled=1`, c.ID, delivery.NativeRelativePath).Scan(&rendersToNativeFile); err != nil {
				return nil, err
			}
			if rendersToNativeFile > 0 {
				entry.ReadNativelyFrom = filepath.Join(c.RootPath, delivery.NativeRelativePath)
				out.Manifest = append(out.Manifest, entry)
				continue
			}
		}
		entry.Injected = true
		out.Manifest = append(out.Manifest, entry)
		parts = append(parts, rendered)
	}
	out.Content = strings.Join(parts, "\n\n")
	return out, nil
}
