package agentstore

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// NewTestServer creates a test HTTP server backed by an in-memory store.
func NewTestServer(t *testing.T) (*Store, *http.ServeMux) {
	t.Helper()
	s := testStore(t)
	mux := http.NewServeMux()
	RegisterHandlers(mux, s)
	return s, mux
}

func TestHTTPAgentsCRUD(t *testing.T) {
	_, mux := NewTestServer(t)

	// Create agent
	body := `{"slug":"test","display_name":"Test","enabled":true}`
	req := httptest.NewRequest("POST", "/agents", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List agents
	req = httptest.NewRequest("GET", "/agents", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var agents []Agent
	json.NewDecoder(w.Body).Decode(&agents)
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	// Get agent
	req = httptest.NewRequest("GET", "/agents/test", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}

	// Update agent
	body = `{"display_name":"Test Updated","enabled":true}`
	req = httptest.NewRequest("PUT", "/agents/test", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Delete agent
	req = httptest.NewRequest("DELETE", "/agents/test", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}

	// Confirm deleted
	req = httptest.NewRequest("GET", "/agents/test", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

func TestHTTPMemorySaveAndSearch(t *testing.T) {
	s, mux := NewTestServer(t)

	// Create an agent first
	a := &Agent{Slug: "mem-agent", DisplayName: "Mem Agent", Enabled: true}
	if err := s.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}

	// Save memory
	body := `{"id":"mem-1","content":"Go is a great programming language","kind":"fact","source":"test","agent_slug":"mem-agent","tags":["code","golang"]}`
	req := httptest.NewRequest("POST", "/memories/save", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("save: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Search
	body = `{"query":"programming language","agent_slug":"mem-agent","limit":5}`
	req = httptest.NewRequest("POST", "/memories/search", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("search: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var results []Memory
	json.NewDecoder(w.Body).Decode(&results)
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].ID != "mem-1" {
		t.Fatalf("expected mem-1, got %s", results[0].ID)
	}

	// Expand
	body = `{"id":"mem-1"}`
	req = httptest.NewRequest("POST", "/memories/expand", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expand: expected 200, got %d", w.Code)
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/memories/mem-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}
}

func TestHTTPMemoryDecayPruneCompact(t *testing.T) {
	_, mux := NewTestServer(t)

	// Decay
	req := httptest.NewRequest("POST", "/memories/decay", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("decay: expected 200, got %d", w.Code)
	}

	// Prune
	body := `{"threshold":0.01}`
	req = httptest.NewRequest("POST", "/memories/prune", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("prune: expected 200, got %d", w.Code)
	}

	// Compact
	body = `{}`
	req = httptest.NewRequest("POST", "/memories/compact", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("compact: expected 200, got %d", w.Code)
	}
}

func TestHTTPReconcile(t *testing.T) {
	_, mux := NewTestServer(t)

	req := httptest.NewRequest("GET", "/reconcile", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("reconcile: expected 200, got %d", w.Code)
	}
}

func TestHTTPAgentConfig(t *testing.T) {
	s, mux := NewTestServer(t)

	a := &Agent{Slug: "cfg-agent", DisplayName: "Cfg Agent", Role: "test role", Enabled: true}
	s.UpsertAgent(a)
	s.UpsertOrchestrator(&Orchestrator{ID: "inber", DisplayName: "Inber", DefaultAgentID: &a.ID})
	s.UpsertAgentOrchestrator(&AgentOrchestrator{
		AgentID: a.ID, OrchestratorID: "inber", OrchestratorAgentID: "cfg-agent",
		Enabled: true, ModelPrimary: "claude-sonnet-4-5", ThinkingBudget: 1024,
		ContextBudget: 30000, ContextTags: `["code"]`, MaxTurns: 3,
	})
	s.SetAgentTools(a.ID, "inber", []string{"shell", "read_file"})
	s.UpsertAgentNature(&AgentNature{AgentID: a.ID, Kind: "identity", Content: "test soul"})

	// GET /agents/cfg-agent/config?orchestrator=inber
	req := httptest.NewRequest("GET", "/agents/cfg-agent/config?orchestrator=inber", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cfg FullAgentConfig
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.Model != "claude-sonnet-4-5" {
		t.Fatalf("expected model claude-sonnet-4-5, got %s", cfg.Model)
	}
	if len(cfg.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(cfg.Tools))
	}
	if cfg.Soul != "test soul" {
		t.Fatalf("expected soul, got %q", cfg.Soul)
	}

	// GET /configs?orchestrator=inber
	req = httptest.NewRequest("GET", "/configs?orchestrator=inber", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["default"] != "cfg-agent" {
		t.Fatalf("expected default cfg-agent, got %v", resp["default"])
	}
}

func TestHTTPHealth(t *testing.T) {
	_, mux := NewTestServer(t)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("health: expected 200, got %d", w.Code)
	}
}
