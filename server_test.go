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
	s.UpsertHarness(&Harness{ID: "inber", DisplayName: "Inber", DefaultAgentID: &a.ID})
	s.UpsertAgentHarness(&AgentHarness{
		AgentID: a.ID, HarnessID: "inber", HarnessAgentID: "cfg-agent",
		Enabled: true, ModelPrimary: "claude-sonnet-4-5", ThinkingBudget: 1024,
		ContextBudget: 30000, ContextTags: `["code"]`, MaxTurns: 3,
	})
	s.SetAgentTools(a.ID, "inber", []string{"shell", "read_file"})
	s.UpsertAgentNature(&AgentNature{AgentID: a.ID, Kind: "identity", Content: "test soul"})

	// GET /agents/cfg-agent/config?harness=inber
	req := httptest.NewRequest("GET", "/agents/cfg-agent/config?harness=inber", nil)
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

	// GET /configs?harness=inber
	req = httptest.NewRequest("GET", "/configs?harness=inber", nil)
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

// TestHTTPHealth pins the standalone shape: a process that owns its mux
// (cmd/server) registers the domain handlers plus its own /health.
func TestHTTPHealth(t *testing.T) {
	s := testStore(t)
	mux := http.NewServeMux()
	RegisterHandlers(mux, s)
	RegisterHealthHandler(mux)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("health: expected 200, got %d", w.Code)
	}
}

// TestRegisterHandlersEmbedsInHostOwningHealth pins the contract that makes
// agent-store safe to mount as a library: RegisterHandlers must not claim any
// pattern the host already owns.
//
// This is a regression test for a real outage-in-waiting. agent-store briefly
// registered "GET /health" from RegisterHandlers; llm-bridge-server mounts
// agent-store into its *root* mux and serves its own "GET /health", and Go
// 1.22+ ServeMux panics on a conflicting pattern at registration time. The
// gateway's next deploy would have built a binary that panicked at boot.
//
// Nothing caught it: agent-store compiled, its own tests passed (they never
// simulated a host), and the gateway's tests construct the server with a nil
// agent-store so its routes were never mounted under test. The panic was only
// reachable from a real boot. Registering into a mux that already owns /health
// -- exactly what the gateway does -- is what makes it reachable from `go test`.
func TestRegisterHandlersEmbedsInHostOwningHealth(t *testing.T) {
	s := testStore(t)

	// A host mux that already serves its own /health, like llm-bridge-server.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "host"})
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterHandlers panicked when embedded in a host mux: %v\n\n"+
				"agent-store is mounted as a library into host muxes (llm-bridge-server "+
				"mounts it at the root). Every pattern it registers must live under a "+
				"segment agent-store owns -- /agents, /configs, /reconcile, /files, "+
				"/versions, /seed, /context. A generic top-level route panics the host "+
				"at boot. If you meant to add a process-level route, it belongs in "+
				"cmd/server, not in RegisterHandlers.", r)
		}
	}()
	RegisterHandlers(mux, s)

	// The host's own /health must still be the one that answers.
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("host health: expected 200, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode host health: %v", err)
	}
	if resp["status"] != "host" {
		t.Fatalf("host health: agent-store shadowed the host's handler, got status=%q want %q",
			resp["status"], "host")
	}
}
