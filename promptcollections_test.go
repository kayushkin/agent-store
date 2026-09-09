package agentstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptCollectionsSeedAndCompile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	globalClaude := "# Host prompt\n\nclaude body"
	globalAgents := "# Agent prompt\n\nagents body"
	if err := os.WriteFile(filepath.Join(home, "CLAUDE.md"), []byte(globalClaude), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte(globalAgents), 0644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(filepath.Join(home, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}

	views, err := store.ListPromptCollectionViews()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) == 0 {
		t.Fatal("expected at least one prompt collection")
	}

	main := views[0]
	if main.Collection.Scope != promptCollectionScopeGlobal {
		t.Fatalf("scope = %q, want global", main.Collection.Scope)
	}
	if len(main.Sections) < 2 {
		t.Fatalf("sections = %d, want at least 2 from CLAUDE.md and AGENTS.md", len(main.Sections))
	}

	var shared *PromptSection
	for i := range main.Sections {
		if main.Sections[i].AppliesTo == PromptAppliesClaude {
			shared = &main.Sections[i]
			break
		}
	}
	if shared == nil {
		t.Fatal("expected a claude section")
	}
	shared.Title = "Shared directives"
	shared.Heading = "# Shared directives"
	shared.Body = "updated body"
	shared.AppliesTo = PromptAppliesAll
	result, err := store.UpdatePromptSection(shared.ID, shared)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.View == nil {
		t.Fatal("expected a mutation result with view")
	}

	claudeBytes, err := os.ReadFile(filepath.Join(home, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudeBytes), "updated body") {
		t.Fatalf("compiled CLAUDE.md missing updated body:\n%s", string(claudeBytes))
	}

	agentsBytes, err := os.ReadFile(filepath.Join(home, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsBytes), "updated body") {
		t.Fatalf("compiled AGENTS.md missing updated body:\n%s", string(agentsBytes))
	}
}
