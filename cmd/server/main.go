package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	agentstore "github.com/kayushkin/agent-store"
	"github.com/kayushkin/agent-store/memory"
	"github.com/kayushkin/bus"
	"github.com/kayushkin/bus/messages"
)

func main() {
	addr := os.Getenv("AGENT_STORE_ADDR")
	if addr == "" {
		addr = ":8300"
	}
	dbPath := os.Getenv("AGENT_STORE_DB")
	if dbPath == "" {
		dbPath = agentstore.DefaultPath()
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	store, err := agentstore.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Open memory store (use same directory as agent store)
	memoryDir := filepath.Dir(dbPath)
	memStore, err := memory.NewSQLiteStore(filepath.Join(memoryDir, "memory.db"))
	if err != nil {
		log.Printf("warning: memory store unavailable: %v", err)
	} else {
		defer memStore.Close()
		log.Printf("memory store: %s", filepath.Join(memoryDir, "memory.db"))
	}

	// Start HTTP server
	mux := http.NewServeMux()
	agentstore.RegisterHandlers(mux, store)

	go func() {
		log.Printf("agent-store HTTP server listening on %s (db: %s)", addr, dbPath)
		log.Fatal(http.ListenAndServe(addr, mux))
	}()

	// Start NATS handlers
	{
		busClient, err := bus.Connect(bus.Options{
			URL:  natsURL,
			Name: "agent-store",
		})
		if err != nil {
			log.Printf("warning: NATS unavailable: %v", err)
		} else {
			defer busClient.Close()

			// Register agents.list handler
			busClient.Reply("agents.list", func(data []byte) (any, error) {
				agents, err := store.ListAgentsExpanded()
				if err != nil {
					return messages.APIResponse{OK: false, Error: err.Error()}, nil
				}
				entries := make([]messages.AgentEntry, len(agents))
				for i, a := range agents {
					name := a.OrchestratorName
					if name == "" {
						name = a.Slug
					}
					entries[i] = messages.AgentEntry{
						Name:         name,
						DisplayName:  a.DisplayName,
						Orchestrator: a.Orchestrator,
						Description:  a.Description,
						Emoji:        a.Emoji,
						Project:      a.Projects,
						Enabled:      a.Enabled,
						IsDefault:    a.IsDefault,
					}
				}
				return messages.APIResponse{OK: true, Data: entries}, nil
			})
			log.Printf("agents.list NATS handler registered")

			if memStore == nil {
				log.Printf("warning: memory store unavailable, skipping memory NATS handlers")
			} else if err := RegisterNATSHandlers(busClient, memStore); err != nil {
				log.Printf("warning: failed to register NATS handlers: %v", err)
			} else {
				log.Printf("agent-store NATS handlers registered (url: %s)", natsURL)
			}
		}
	}

	// Keep the server running
	select {}
}

// RegisterNATSHandlers sets up NATS request/reply handlers for memory operations
func RegisterNATSHandlers(busClient *bus.Client, memStore memory.MemoryStore) error {
	// Helper function to convert memory.Memory to messages.Memory
	memoryToMessage := func(m memory.Memory) messages.Memory {
		return messages.Memory{
			ID:           m.ID,
			Content:      m.Content,
			Summary:      m.Summary,
			OriginalID:   m.OriginalID,
			Tags:         m.Tags,
			Importance:   m.Importance,
			AccessCount:  m.AccessCount,
			LastAccessed: m.LastAccessed,
			CreatedAt:    m.CreatedAt,
			Source:       m.Source,
			Embedding:    m.Embedding,
			AlwaysLoad:   m.AlwaysLoad,
			ExpiresAt:    m.ExpiresAt,
			Tokens:       m.Tokens,
			RefType:      m.RefType,
			RefTarget:    m.RefTarget,
			IsLazy:       m.IsLazy,
			Orchestrator: m.Orchestrator,
		}
	}

	// Helper function to convert messages.Memory to memory.Memory
	messageToMemory := func(m messages.Memory, orchestrator string) memory.Memory {
		orch := m.Orchestrator
		if orch == "" {
			orch = orchestrator
		}
		return memory.Memory{
			ID:           m.ID,
			Content:      m.Content,
			Summary:      m.Summary,
			OriginalID:   m.OriginalID,
			Tags:         m.Tags,
			Importance:   m.Importance,
			AccessCount:  m.AccessCount,
			LastAccessed: m.LastAccessed,
			CreatedAt:    m.CreatedAt,
			Source:       m.Source,
			Embedding:    m.Embedding,
			AlwaysLoad:   m.AlwaysLoad,
			ExpiresAt:    m.ExpiresAt,
			Tokens:       m.Tokens,
			RefType:      m.RefType,
			RefTarget:    m.RefTarget,
			IsLazy:       m.IsLazy,
			Orchestrator: orch,
		}
	}

	// memory.save
	_, err := busClient.Reply("memory.save", func(data []byte) (any, error) {
		var req messages.MemorySaveRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemorySaveResponse{Success: false, Error: err.Error()}, nil
		}

		m := messageToMemory(req.Memory, req.Orchestrator)
		if err := memStore.Save(m); err != nil {
			return messages.MemorySaveResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemorySaveResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.save: %w", err)
	}

	// memory.get
	_, err = busClient.Reply("memory.get", func(data []byte) (any, error) {
		var req messages.MemoryGetRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryGetResponse{Error: err.Error()}, nil
		}

		m, err := memStore.Get(req.ID)
		if err != nil {
			return messages.MemoryGetResponse{Error: err.Error()}, nil
		}

		msgMem := memoryToMessage(*m)
		return messages.MemoryGetResponse{Memory: &msgMem}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.get: %w", err)
	}

	// memory.search
	_, err = busClient.Reply("memory.search", func(data []byte) (any, error) {
		var req messages.MemorySearchRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemorySearchResponse{Error: err.Error()}, nil
		}

		memories, err := memStore.SearchFiltered(req.Query, req.Limit, req.Orchestrator)
		if err != nil {
			return messages.MemorySearchResponse{Error: err.Error()}, nil
		}

		msgMemories := make([]messages.Memory, len(memories))
		for i, m := range memories {
			msgMemories[i] = memoryToMessage(m)
		}

		return messages.MemorySearchResponse{Memories: msgMemories}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.search: %w", err)
	}

	// memory.forget
	_, err = busClient.Reply("memory.forget", func(data []byte) (any, error) {
		var req messages.MemoryForgetRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryForgetResponse{Success: false, Error: err.Error()}, nil
		}

		if err := memStore.Forget(req.ID); err != nil {
			return messages.MemoryForgetResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemoryForgetResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.forget: %w", err)
	}

	// memory.decay
	_, err = busClient.Reply("memory.decay", func(data []byte) (any, error) {
		var req messages.MemoryDecayRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryDecayResponse{Success: false, Error: err.Error()}, nil
		}

		if err := memStore.DecayImportance(); err != nil {
			return messages.MemoryDecayResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemoryDecayResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.decay: %w", err)
	}

	// memory.list
	_, err = busClient.Reply("memory.list", func(data []byte) (any, error) {
		var req messages.MemoryListRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryListResponse{Error: err.Error()}, nil
		}

		memories, err := memStore.ListRecent(req.Limit, req.MinImportance)
		if err != nil {
			return messages.MemoryListResponse{Error: err.Error()}, nil
		}

		msgMemories := make([]messages.Memory, len(memories))
		for i, m := range memories {
			msgMemories[i] = memoryToMessage(m)
		}

		return messages.MemoryListResponse{Memories: msgMemories}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.list: %w", err)
	}

	// memory.compare — cross-orchestrator comparison
	_, err = busClient.Reply("memory.compare", func(data []byte) (any, error) {
		var req messages.MemoryCompareRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryCompareResponse{Error: err.Error()}, nil
		}

		sqlStore, ok := memStore.(*memory.Store)
		if !ok {
			return messages.MemoryCompareResponse{Error: "compare requires SQLite store"}, nil
		}

		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}

		// Determine orchestrators to compare
		orchestrators := req.Orchestrators
		if len(orchestrators) == 0 {
			var err error
			orchestrators, err = sqlStore.ListOrchestrators()
			if err != nil {
				return messages.MemoryCompareResponse{Error: err.Error()}, nil
			}
		}

		var groups []messages.MemoryCompareGroup
		for _, orch := range orchestrators {
			var memories []memory.Memory
			var err error

			if req.Query != "" {
				memories, err = memStore.SearchFiltered(req.Query, limit, orch)
			} else {
				memories, err = sqlStore.ListByOrchestrator(orch, limit, req.MinImportance)
			}
			if err != nil {
				log.Printf("[compare] error for %s: %v", orch, err)
				continue
			}

			total, _ := sqlStore.CountByOrchestrator(orch)

			msgMems := make([]messages.Memory, len(memories))
			for i, m := range memories {
				msgMems[i] = memoryToMessage(m)
			}

			groups = append(groups, messages.MemoryCompareGroup{
				Orchestrator: orch,
				Memories:     msgMems,
				Total:        total,
			})
		}

		return messages.MemoryCompareResponse{Groups: groups}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.compare: %w", err)
	}

	// memory.compact
	_, err = busClient.Reply("memory.compact", func(data []byte) (any, error) {
		var req messages.MemoryCompactRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryCompactResponse{Error: err.Error()}, nil
		}

		results, err := memStore.Compact(req.MinAge, req.MinCount)
		if err != nil {
			return messages.MemoryCompactResponse{Error: err.Error()}, nil
		}

		msgResults := make([]messages.CompactionResult, len(results))
		for i, r := range results {
			msgResults[i] = messages.CompactionResult{
				OriginalIDs: r.OriginalIDs,
				NewID:       r.NewID,
				Tags:        r.Tags,
				Count:       r.Count,
			}
		}

		return messages.MemoryCompactResponse{Results: msgResults}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.compact: %w", err)
	}

	// memory.build-context
	_, err = busClient.Reply("memory.build-context", func(data []byte) (any, error) {
		var req messages.MemoryBuildContextRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryBuildContextResponse{Error: err.Error()}, nil
		}

		// Convert from message type to memory type
		memReq := memory.BuildContextRequest{
			Tags:              req.Request.Tags,
			TokenBudget:       req.Request.TokenBudget,
			MinImportance:     req.Request.MinImportance,
			ExcludeTags:       req.Request.ExcludeTags,
			IncludeAlwaysLoad: req.Request.IncludeAlwaysLoad,
			MaxChunkSize:      req.Request.MaxChunkSize,
			TruncateThreshold: req.Request.TruncateThreshold,
			TruncatePreview:   req.Request.TruncatePreview,
		}

		memories, tokenCount, err := memStore.BuildContext(memReq)
		if err != nil {
			return messages.MemoryBuildContextResponse{Error: err.Error()}, nil
		}

		msgMemories := make([]messages.Memory, len(memories))
		for i, m := range memories {
			msgMemories[i] = memoryToMessage(m)
		}

		return messages.MemoryBuildContextResponse{
			Memories:   msgMemories,
			TokenCount: tokenCount,
		}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.build-context: %w", err)
	}

	// memory.prepare-session
	_, err = busClient.Reply("memory.prepare-session", func(data []byte) (any, error) {
		var req messages.MemoryPrepareSessionRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryPrepareSessionResponse{Success: false, Error: err.Error()}, nil
		}

		// Convert from message type to memory type
		memCfg := memory.PrepareSessionConfig{
			RootDir:        req.Config.RootDir,
			IdentityFile:   req.Config.IdentityFile,
			IdentityText:   req.Config.IdentityText,
			AgentName:      req.Config.AgentName,
			RecencyWindow:  req.Config.RecencyWindow,
			RecentFilesTTL: req.Config.RecentFilesTTL,
		}

		if err := memStore.PrepareSession(memCfg); err != nil {
			return messages.MemoryPrepareSessionResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemoryPrepareSessionResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.prepare-session: %w", err)
	}

	// memory.load-tool-registry
	_, err = busClient.Reply("memory.load-tool-registry", func(data []byte) (any, error) {
		var req messages.MemoryLoadToolRegistryRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryLoadToolRegistryResponse{Success: false, Error: err.Error()}, nil
		}

		// Convert from message type to memory type
		tools := make([]memory.ToolMetadata, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = memory.ToolMetadata{
				Name:        t.Name,
				Description: t.Description,
				Category:    t.Category,
			}
		}

		if err := memStore.LoadToolRegistry(tools); err != nil {
			return messages.MemoryLoadToolRegistryResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemoryLoadToolRegistryResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.load-tool-registry: %w", err)
	}

	// memory.update-tool-usage
	_, err = busClient.Reply("memory.update-tool-usage", func(data []byte) (any, error) {
		var req messages.MemoryUpdateToolUsageRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryUpdateToolUsageResponse{Success: false, Error: err.Error()}, nil
		}

		if err := memStore.UpdateToolUsageSummary(req.ToolName, req.Summary, req.TTLSeconds); err != nil {
			return messages.MemoryUpdateToolUsageResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemoryUpdateToolUsageResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.update-tool-usage: %w", err)
	}

	// memory.session-save
	_, err = busClient.Reply("memory.session-save", func(data []byte) (any, error) {
		var req messages.MemorySessionSaveRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemorySessionSaveResponse{Success: false, Error: err.Error()}, nil
		}

		// Convert from message type to memory type
		sess := memory.Session{
			ID:           req.Session.ID,
			AgentName:    req.Session.AgentName,
			Model:        req.Session.Model,
			StartedAt:    req.Session.StartedAt,
			EndedAt:      req.Session.EndedAt,
			InputTokens:  req.Session.InputTokens,
			OutputTokens: req.Session.OutputTokens,
			Cost:         req.Session.Cost,
			Summary:      req.Session.Summary,
			Tags:         req.Session.Tags,
		}

		if err := memStore.SaveSession(sess); err != nil {
			return messages.MemorySessionSaveResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemorySessionSaveResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.session-save: %w", err)
	}

	// memory.track-usage
	_, err = busClient.Reply("memory.track-usage", func(data []byte) (any, error) {
		var req messages.MemoryTrackUsageRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return messages.MemoryTrackUsageResponse{Success: false, Error: err.Error()}, nil
		}

		if err := memStore.TrackMemoryUsage(req.MemoryID, req.SessionID, req.TurnNumber, req.UsageType); err != nil {
			return messages.MemoryTrackUsageResponse{Success: false, Error: err.Error()}, nil
		}

		return messages.MemoryTrackUsageResponse{Success: true}, nil
	})
	if err != nil {
		return fmt.Errorf("register memory.track-usage: %w", err)
	}

	return nil
}
