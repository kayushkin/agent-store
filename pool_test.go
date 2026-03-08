package agentstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPoolInitAcquireRelease(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()
	
	// Create a git repo to use as base
	baseRepo := filepath.Join(tmpDir, "test-repo")
	if err := os.MkdirAll(baseRepo, 0755); err != nil {
		t.Fatalf("create base repo dir: %v", err)
	}
	
	// Initialize git repo
	runGit(t, baseRepo, "init")
	runGit(t, baseRepo, "config", "user.email", "test@test.com")
	runGit(t, baseRepo, "config", "user.name", "Test")
	runGit(t, baseRepo, "commit", "--allow-empty", "-m", "initial")
	
	// Create test store
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	
	// Create pool directory
	poolDir := filepath.Join(tmpDir, ".pools", "test-repo")
	
	// Initialize pool with 3 slots
	err = store.InitPool("test-repo", baseRepo, poolDir, 3)
	if err != nil {
		t.Fatalf("InitPool: %v", err)
	}
	
	// Verify worktrees were created
	for i := 0; i < 3; i++ {
		slotPath := filepath.Join(poolDir, "slot-"+string(rune('0'+i)))
		if _, err := os.Stat(slotPath); err != nil {
			t.Errorf("slot %d path not created: %v", i, err)
		}
	}
	
	// Check initial status
	slots, err := store.SlotStatus("test-repo")
	if err != nil {
		t.Fatalf("SlotStatus: %v", err)
	}
	if len(slots) != 3 {
		t.Errorf("expected 3 slots, got %d", len(slots))
	}
	for _, s := range slots {
		if s.Status != "ready" {
			t.Errorf("expected status ready, got %s", s.Status)
		}
	}
	
	// Acquire a slot
	slot, err := store.Acquire("test-repo", "agent-1", "session-abc")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if slot.AgentID != "agent-1" {
		t.Errorf("expected agent-1, got %s", slot.AgentID)
	}
	if slot.SessionID != "session-abc" {
		t.Errorf("expected session-abc, got %s", slot.SessionID)
	}
	if slot.Status != "" { // Status is not set in Acquire return
		t.Errorf("expected empty status in returned slot, got %s", slot.Status)
	}
	
	// Verify slot is now acquired
	slots, err = store.SlotStatus("test-repo")
	if err != nil {
		t.Fatalf("SlotStatus after acquire: %v", err)
	}
	acquiredCount := 0
	for _, s := range slots {
		if s.Status == "acquired" {
			acquiredCount++
		}
	}
	if acquiredCount != 1 {
		t.Errorf("expected 1 acquired slot, got %d", acquiredCount)
	}
	
	// Release the slot (no push since we have no remote)
	err = store.Release("test-repo", slot.ID, false)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	
	// Verify slot is ready again
	slots, err = store.SlotStatus("test-repo")
	if err != nil {
		t.Fatalf("SlotStatus after release: %v", err)
	}
	for _, s := range slots {
		if s.Status != "ready" {
			t.Errorf("expected all slots ready, got %s", s.Status)
		}
	}
}

func TestPoolAcquireAllSlots(t *testing.T) {
	tmpDir := t.TempDir()
	
	baseRepo := filepath.Join(tmpDir, "test-repo")
	if err := os.MkdirAll(baseRepo, 0755); err != nil {
		t.Fatalf("create base repo dir: %v", err)
	}
	
	runGit(t, baseRepo, "init")
	runGit(t, baseRepo, "config", "user.email", "test@test.com")
	runGit(t, baseRepo, "config", "user.name", "Test")
	runGit(t, baseRepo, "commit", "--allow-empty", "-m", "initial")
	
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	
	poolDir := filepath.Join(tmpDir, ".pools", "test-repo")
	
	// Initialize pool with 2 slots
	err = store.InitPool("test-repo", baseRepo, poolDir, 2)
	if err != nil {
		t.Fatalf("InitPool: %v", err)
	}
	
	// Acquire both slots
	slot1, err := store.Acquire("test-repo", "agent-1", "session-1")
	if err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	
	slot2, err := store.Acquire("test-repo", "agent-1", "session-2")
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	
	// Try to acquire again - should fail
	_, err = store.Acquire("test-repo", "agent-1", "session-3")
	if err != ErrNoSlotsAvailable {
		t.Errorf("expected ErrNoSlotsAvailable, got %v", err)
	}
	
	// Release one slot
	err = store.Release("test-repo", slot1.ID, false)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	
	// Now acquire should work again
	slot3, err := store.Acquire("test-repo", "agent-2", "session-3")
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if slot3.ID != slot1.ID {
		t.Errorf("expected to get released slot %d, got %d", slot1.ID, slot3.ID)
	}
	
	// Cleanup
	_ = store.Release("test-repo", slot2.ID, false)
	_ = store.Release("test-repo", slot3.ID, false)
}

func TestSlotOperations(t *testing.T) {
	tmpDir := t.TempDir()
	
	baseRepo := filepath.Join(tmpDir, "test-repo")
	if err := os.MkdirAll(baseRepo, 0755); err != nil {
		t.Fatalf("create base repo dir: %v", err)
	}
	
	runGit(t, baseRepo, "init")
	runGit(t, baseRepo, "config", "user.email", "test@test.com")
	runGit(t, baseRepo, "config", "user.name", "Test")
	runGit(t, baseRepo, "commit", "--allow-empty", "-m", "initial")
	
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	
	poolDir := filepath.Join(tmpDir, ".pools", "test-repo")
	
	err = store.InitPool("test-repo", baseRepo, poolDir, 1)
	if err != nil {
		t.Fatalf("InitPool: %v", err)
	}
	
	slot, err := store.Acquire("test-repo", "agent-1", "session-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	
	// Create a file in the slot
	testFile := filepath.Join(slot.Path, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	
	// Commit the file
	err = slot.Commit("add test file")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	
	// Get diff (should show changes relative to main)
	// Note: on the pool branch, diff vs main shows the branch changes
	_, err = slot.Diff()
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	// Diff may be empty if branches diverged, just verify no error
	
	// Test Pull (will fail without remote, but that's expected)
	// We'll just verify the method exists and runs
	_ = slot.Pull() // ignore error - no remote configured
	
	// Reset should clean up
	err = slot.Reset()
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	
	// Verify file is gone after reset
	if _, err := os.Stat(testFile); err == nil {
		t.Error("test file should be gone after reset")
	}
	
	// Release the slot
	err = store.Release("test-repo", slot.ID, false)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	
	tests := []struct {
		input    string
		expected string
	}{
		{"~/test", filepath.Join(home, "test")},
		{"/absolute/path", "/absolute/path"},
		{"relative", "relative"},
	}
	
	for _, tc := range tests {
		result := expandPath(tc.input)
		if result != tc.expected {
			t.Errorf("expandPath(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
